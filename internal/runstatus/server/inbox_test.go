package server_test

import (
	"context"
	"database/sql"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/chats"
	"kitsoki/internal/jobs"
	"kitsoki/internal/journal"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/runstatus"
	"kitsoki/internal/runstatus/server"
	"kitsoki/internal/store"
)

// inboxFixture is a live cloak session with a JobStore wired into both the
// orchestrator and the web Driver — the shape `kitsoki web` builds. It returns
// the httptest server, the JobStore (so a test can post a notification without
// an LLM), and the orchestrator session id (the teleport target's origin).
type inboxFixture struct {
	ts       *httptest.Server
	db       *sql.DB
	js       *jobs.JobStore
	chats    *chats.Store
	sid      app.SessionID
	publicID string
}

func buildInboxFixture(t *testing.T) inboxFixture {
	t.Helper()
	def, err := app.Load("../../../testdata/apps/cloak/app.yaml")
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	jw, err := journal.NewSQLiteWriter(s.DB())
	require.NoError(t, err)
	jr, err := journal.NewSQLiteReader(s.DB())
	require.NoError(t, err)

	js, err := jobs.NewJobStore(s.DB())
	require.NoError(t, err)
	chatStore, err := chats.NewStore(s.DB())
	require.NoError(t, err)

	orch := orchestrator.New(def, m, s, nil,
		orchestrator.WithJournalWriter(jw),
		orchestrator.WithJournalReader(jr),
		orchestrator.WithJobStore(js),
	)

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	sink, err := store.OpenJSONL(filepath.Join(t.TempDir(), "run.jsonl"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = sink.Close() })

	live := server.NewLiveSession(sink, def, string(sid), string(orch.InitialState()))
	orch.SetEventSink(live)
	require.NoError(t, orch.RunInitialOnEnter(ctx, sid))

	driver := server.OrchestratorDriver{Orch: orch, SID: sid, Jobs: js, Chats: chatStore}
	publicID := "web-session-1"
	srv := server.NewMulti(&singleInboxProvider{
		entry: server.Entry{Source: live, Driver: driver},
		sid:   publicID,
	}, server.WithPollInterval(20*time.Millisecond))

	// Register the cross-session relay against this session (what web.go does
	// via SetNotifier→AttachSession).
	srv.AttachSession(orch, sid, string(sid), js)

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return inboxFixture{ts: ts, db: s.DB(), js: js, chats: chatStore, sid: sid, publicID: publicID}
}

// singleInboxProvider is a one-session SessionProvider keyed by the
// orchestrator session id (so session-routed RPCs resolve), enough for the
// inbox RPC tests.
type singleInboxProvider struct {
	entry server.Entry
	sid   string
}

func (p *singleInboxProvider) Get(id string) (server.Entry, bool) {
	if id == p.sid || id == "" {
		return p.entry, true
	}
	return server.Entry{}, false
}
func (p *singleInboxProvider) List() []runstatus.SessionHeader {
	return []runstatus.SessionHeader{{
		SessionID:    p.sid,
		AppID:        "cloak",
		CurrentState: "foyer",
		StartedAt:    time.Now(),
	}}
}

// The provider must satisfy server.SessionProvider; only Get matters for the
// inbox RPC tests — the rest are inert stubs.
func (p *singleInboxProvider) ListStories() []server.StoryHeader { return nil }
func (p *singleInboxProvider) Rescan() ([]server.StoryHeader, error) {
	return nil, nil
}
func (p *singleInboxProvider) NewSession(context.Context, string) (string, error) {
	return "", nil
}
func (p *singleInboxProvider) Reload(context.Context, string) (bool, error) { return false, nil }
func (p *singleInboxProvider) Staleness(context.Context, string) (bool, string, error) {
	return false, "", nil
}

// postNotification inserts a teleportable notification for the fixture session
// without any LLM — the deterministic stand-in for a terminal background job.
func (f inboxFixture) postNotification(t *testing.T, title string) string {
	t.Helper()
	n := &jobs.Notification{
		SessionID:     f.sid,
		CreatedAt:     time.Now(),
		Severity:      jobs.SeveritySuccess,
		Title:         title,
		Body:          "done",
		TeleportState: "foyer", // a real state in the cloak fixture
		OriginKind:    "job",
		OriginRef:     "job:test",
	}
	require.NoError(t, f.js.InsertNotification(context.Background(), n))
	return n.ID
}

// TestInbox_ListReadDismiss exercises the three CRUD RPCs over the wire.
func TestInbox_ListReadDismiss(t *testing.T) {
	f := buildInboxFixture(t)
	id := f.postNotification(t, "Background turn ready")

	var listed struct {
		Notifications []map[string]any `json:"notifications"`
	}
	rpcCall(t, f.ts, "runstatus.session.notifications.list",
		map[string]any{"session_id": f.publicID}, &listed)
	require.Len(t, listed.Notifications, 1)
	assert.Equal(t, id, listed.Notifications[0]["ID"])
	assert.Equal(t, "Background turn ready", listed.Notifications[0]["Title"])

	// read
	var ok struct {
		OK bool `json:"ok"`
	}
	rpcCall(t, f.ts, "runstatus.session.notifications.read",
		map[string]any{"session_id": f.publicID, "id": id}, &ok)
	assert.True(t, ok.OK)

	// dismiss → drops out of the list
	rpcCall(t, f.ts, "runstatus.session.notifications.dismiss",
		map[string]any{"session_id": f.publicID, "id": id}, &ok)
	assert.True(t, ok.OK)

	rpcCall(t, f.ts, "runstatus.session.notifications.list",
		map[string]any{"session_id": f.publicID}, &listed)
	assert.Empty(t, listed.Notifications, "dismissed notification should drop out")
}

// TestInbox_Teleport proves the teleport RPC resolves the notification and
// jumps the session to its origin state via Orchestrator.Teleport.
func TestInbox_Teleport(t *testing.T) {
	f := buildInboxFixture(t)
	id := f.postNotification(t, "ready")

	var res turnResultWire
	rpcCall(t, f.ts, "runstatus.session.teleport",
		map[string]any{"session_id": f.publicID, "notification_id": id}, &res)

	assert.Equal(t, "foyer", res.State, "teleport should land at the notification's origin state")
	assert.NotEmpty(t, res.View, "teleport re-renders the destination room")
}

func TestWorkList_SurfacesGlobalActiveWork(t *testing.T) {
	f := buildInboxFixture(t)
	n := &jobs.Notification{
		SessionID:     f.sid,
		CreatedAt:     time.Now(),
		Severity:      jobs.SeverityActionRequired,
		Title:         "PR #42 needs review",
		Body:          "Review requested by alice.",
		TeleportState: "inbox",
		TeleportSlots: map[string]any{"pr_id": "42", "pr_title": "Add tests"},
		OriginKind:    "external",
		OriginRef:     "github:acme/repo/pr/42",
		OriginURL:     "https://github.com/acme/repo/pull/42",
	}
	require.NoError(t, f.js.InsertNotification(context.Background(), n))
	now := time.Now()
	require.NoError(t, f.js.UpsertJob(context.Background(), &jobs.Job{
		ID:          "job-running",
		SessionID:   f.sid,
		Kind:        "host.agent.task",
		Status:      jobs.JobRunning,
		OriginState: "foyer",
		CreatedAt:   now.Add(-time.Second),
		UpdatedAt:   now,
	}))
	chat, err := f.chats.Create(context.Background(), "cloak", "agent", "scope", "Review proposal")
	require.NoError(t, err)
	_, err = f.chats.Enqueue(context.Background(), chats.EnqueueOptions{
		ChatID:          chat.ID,
		Transport:       chats.DriveTransportStateMachine,
		Actor:           "story",
		Payload:         "continue the agent task",
		OriginSessionID: string(f.sid),
		OriginState:     "foyer",
	})
	require.NoError(t, err)
	bg, err := f.chats.Create(context.Background(), "cloak", "agent", "scope-bg", "Background Claude")
	require.NoError(t, err)
	_, err = f.db.ExecContext(context.Background(), `UPDATE chats SET session_id = ? WHERE id = ?`, string(f.sid), bg.ID)
	require.NoError(t, err)
	_, err = f.chats.AttachPTY(context.Background(), chats.AttachPTYOptions{ChatID: bg.ID, TmuxSession: "kit-bg"})
	require.NoError(t, err)
	_, err = f.chats.DetachPTY(context.Background(), bg.ID)
	require.NoError(t, err)

	var work server.WorkListResult
	rpcCall(t, f.ts, "runstatus.work.list", nil, &work)
	require.Len(t, work.Sessions, 1)
	assert.Equal(t, f.publicID, work.Sessions[0].SessionID)
	assert.Equal(t, 1, work.Summary.JobsRunning)
	assert.Equal(t, 1, work.Summary.NotificationsUnread)
	assert.Equal(t, 1, work.Summary.PendingDrives)
	assert.Equal(t, 1, work.Summary.BackgroundedChats)
	assert.Equal(t, 4, work.Summary.Items)
	require.Len(t, work.Items, 4)
	assert.Equal(t, "notification", work.Items[0].Kind)
	assert.Equal(t, n.ID, work.Items[0].NotificationID)
	assert.Equal(t, "notification", work.Items[0].ReacquireTool)
	assert.Equal(t, f.publicID, work.Items[0].SessionID)
	assert.Equal(t, f.publicID, work.Items[0].ReacquireSessionID)
	assert.Equal(t, "Review requested by alice.", work.Items[0].Body)
	assert.Equal(t, map[string]any{"pr_id": "42", "pr_title": "Add tests"}, work.Items[0].TeleportSlots)
	assert.Equal(t, "external", work.Items[0].OriginKind)
	assert.Equal(t, "github:acme/repo/pr/42", work.Items[0].OriginRef)
	assert.Equal(t, "https://github.com/acme/repo/pull/42", work.Items[0].OriginURL)
	assert.Equal(t, "job", work.Items[1].Kind)
	assert.Equal(t, "job-running", work.Items[1].JobID)
	assert.Equal(t, "session", work.Items[1].ReacquireTool)
	assert.Equal(t, f.publicID, work.Items[1].SessionID)
	assert.Equal(t, "pending_drive", work.Items[2].Kind)
	assert.Equal(t, chat.ID, work.Items[2].ChatID)
	assert.Equal(t, "backgrounded_chat", work.Items[3].Kind)
	assert.Equal(t, bg.ID, work.Items[3].ChatID)
}

// TestInbox_TeleportNotTeleportable proves a notification with no destination
// state surfaces as a transport error (the surface renders it read-only).
func TestInbox_TeleportNotTeleportable(t *testing.T) {
	f := buildInboxFixture(t)
	n := &jobs.Notification{
		SessionID:  f.sid,
		CreatedAt:  time.Now(),
		Severity:   jobs.SeverityInfo,
		Title:      "informational only",
		OriginKind: "job",
	}
	require.NoError(t, f.js.InsertNotification(context.Background(), n))

	code, msg := rpcCallExpectError(t, f.ts, "runstatus.session.teleport",
		map[string]any{"session_id": f.publicID, "notification_id": n.ID})
	assert.NotZero(t, code)
	assert.Contains(t, msg, "teleport")
}

// TestInbox_NilJobStore proves the nil-safety contract directly on the Driver:
// a session with no JobStore reports an empty inbox, read/dismiss no-op, and
// teleport returns the typed ErrNoInbox.
func TestInbox_NilJobStore(t *testing.T) {
	d := server.OrchestratorDriver{} // Jobs == nil
	ctx := context.Background()

	notifs, err := d.ListNotifications(ctx)
	require.NoError(t, err)
	assert.Nil(t, notifs)

	assert.NoError(t, d.MarkNotificationRead(ctx, "x"))
	assert.NoError(t, d.DismissNotification(ctx, "x"))

	_, err = d.Teleport(ctx, "x")
	assert.ErrorIs(t, err, server.ErrNoInbox)
}
