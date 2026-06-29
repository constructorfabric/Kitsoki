package server_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/chats"
	"kitsoki/internal/host"
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
	live     *server.LiveSession
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

	driver := server.OrchestratorDriver{Orch: orch, SID: sid, Jobs: js, Chats: chatStore, TraceHistory: live.History}
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
	return inboxFixture{ts: ts, db: s.DB(), js: js, chats: chatStore, live: live, sid: sid, publicID: publicID}
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

func TestInbox_SyncGitHubFeedsWebWork(t *testing.T) {
	f := buildInboxFixture(t)
	restore := host.SetExecRunnerForTest(func(_ context.Context, _ string, name string, args ...string) (string, string, int, error) {
		key := name + " " + strings.Join(args, " ")
		switch key {
		case "gh --version":
			return "gh version 2.x\n", "", 0, nil
		case "gh issue list --repo acme/repo --state open --assignee @me --limit 100 --json number,title,assignees,url":
			return `[{"number":7,"title":"Assigned issue","url":"https://github.com/acme/repo/issues/7","assignees":[{"login":"brad"}]}]`, "", 0, nil
		case "gh pr list --repo acme/repo --state open --search review-requested:@me --limit 100 --json number,title,author,url":
			return `[{"number":42,"title":"Review this","url":"https://github.com/acme/repo/pull/42","author":{"login":"alice"}}]`, "", 0, nil
		default:
			return "", "unexpected command: " + key, 1, nil
		}
	})
	defer restore()

	var synced server.GitHubInboxSyncResult
	rpcCall(t, f.ts, "runstatus.session.inbox.sync_github",
		map[string]any{"session_id": f.publicID, "repo": "acme/repo"}, &synced)
	assert.True(t, synced.OK)
	assert.Equal(t, 2, synced.Fetched)
	assert.Equal(t, 2, synced.Inserted)
	assert.Equal(t, 0, synced.Skipped)
	require.Len(t, synced.Items, 2)
	assert.Equal(t, "github:acme/repo/issue/7", synced.Items[0].OriginRef)
	assert.Equal(t, "github:acme/repo/pr/42", synced.Items[1].OriginRef)

	var work server.WorkListResult
	rpcCall(t, f.ts, "runstatus.work.list", nil, &work)
	assert.Equal(t, 2, work.Summary.NotificationsUnread)
	assert.Equal(t, 2, work.Summary.NotificationsActionRequired)
	require.Len(t, work.Items, 2)
	urls := map[string]bool{}
	for _, item := range work.Items {
		assert.Equal(t, "notification", item.Kind)
		urls[item.OriginURL] = true
	}
	assert.True(t, urls["https://github.com/acme/repo/pull/42"])
	assert.True(t, urls["https://github.com/acme/repo/issues/7"])

	var second server.GitHubInboxSyncResult
	rpcCall(t, f.ts, "runstatus.session.inbox.sync_github",
		map[string]any{"session_id": f.publicID, "repo": "acme/repo"}, &second)
	assert.Equal(t, 0, second.Inserted)
	assert.Equal(t, 2, second.Skipped)
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
	jobNotification := &jobs.Notification{
		SessionID:     f.sid,
		CreatedAt:     now.Add(-500 * time.Millisecond),
		Severity:      jobs.SeverityInfo,
		Title:         "Job submitted: host.agent.task",
		TeleportState: "foyer",
		TeleportJobID: "job-running",
		OriginKind:    "job",
		OriginRef:     "job:job-running",
	}
	require.NoError(t, f.js.InsertNotification(context.Background(), jobNotification))
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
	dispatchingChat, err := f.chats.Create(context.Background(), "cloak", "agent", "scope-dispatching", "Dispatching Claude")
	require.NoError(t, err)
	dispatching, err := f.chats.Enqueue(context.Background(), chats.EnqueueOptions{
		ChatID:          dispatchingChat.ID,
		Transport:       chats.DriveTransportStateMachine,
		Actor:           "story",
		Payload:         "already dispatching",
		OriginSessionID: string(f.sid),
		OriginState:     "foyer",
	})
	require.NoError(t, err)
	_, err = f.chats.ClaimDrive(context.Background(), dispatching.DriveID)
	require.NoError(t, err)
	failedChat, err := f.chats.Create(context.Background(), "cloak", "agent", "scope-failed", "Failed Claude")
	require.NoError(t, err)
	failed, err := f.chats.Enqueue(context.Background(), chats.EnqueueOptions{
		ChatID:          failedChat.ID,
		Transport:       chats.DriveTransportStateMachine,
		Actor:           "story",
		Payload:         "failed agent task",
		OriginSessionID: string(f.sid),
		OriginState:     "foyer",
	})
	require.NoError(t, err)
	_, err = f.chats.ClaimDrive(context.Background(), failed.DriveID)
	require.NoError(t, err)
	require.NoError(t, f.chats.MarkDriveFailed(context.Background(), failed.DriveID, "claude exited 1"))
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
	assert.Equal(t, 2, work.Summary.NotificationsUnread)
	assert.Equal(t, 1, work.Summary.PendingDrives)
	assert.Equal(t, 1, work.Summary.DispatchingDrives)
	assert.Equal(t, 1, work.Summary.FailedDrives)
	assert.Equal(t, 1, work.Summary.BackgroundedChats)
	assert.Equal(t, 2, work.Summary.NeedsAttention)
	assert.Equal(t, 7, work.Summary.Items)
	require.Len(t, work.Items, 7)
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
	assert.Equal(t, "failed_drive", work.Items[1].Kind)
	assert.Equal(t, failedChat.ID, work.Items[1].ChatID)
	assert.Equal(t, failed.DriveID, work.Items[1].DriveID)
	assert.Equal(t, "claude exited 1", work.Items[1].Body)
	assert.Equal(t, "chat.show", work.Items[1].ReacquireTool)
	assert.Equal(t, f.publicID, work.Items[1].ReacquireSessionID)
	assert.Equal(t, "job", work.Items[2].Kind)
	assert.Equal(t, "job-running", work.Items[2].JobID)
	assert.Equal(t, jobNotification.ID, work.Items[2].NotificationID)
	assert.Equal(t, "notification", work.Items[2].ReacquireTool)
	assert.Equal(t, "job:job-running", work.Items[2].OriginRef)
	assert.Equal(t, f.publicID, work.Items[2].SessionID)
	assert.Equal(t, "pending_drive", work.Items[3].Kind)
	assert.Equal(t, dispatchingChat.ID, work.Items[3].ChatID)
	assert.Equal(t, string(chats.DriveStatusDispatching), work.Items[3].Status)
	assert.Equal(t, "chat.show", work.Items[3].ReacquireTool)
	assert.Equal(t, f.publicID, work.Items[3].ReacquireSessionID)
	assert.Equal(t, "pending_drive", work.Items[4].Kind)
	assert.Equal(t, chat.ID, work.Items[4].ChatID)
	assert.Equal(t, string(chats.DriveStatusPending), work.Items[4].Status)
	assert.Equal(t, "chat.show", work.Items[4].ReacquireTool)
	assert.Equal(t, f.publicID, work.Items[4].ReacquireSessionID)
	assert.Equal(t, "backgrounded_chat", work.Items[5].Kind)
	assert.Equal(t, bg.ID, work.Items[5].ChatID)
	assert.Equal(t, "chat.show", work.Items[5].ReacquireTool)
	assert.Equal(t, f.publicID, work.Items[5].ReacquireSessionID)
	assert.Equal(t, "notification", work.Items[6].Kind)
	assert.Equal(t, jobNotification.ID, work.Items[6].NotificationID)
}

func TestWorkList_SurfacesTraceBackedMiningProposals(t *testing.T) {
	f := buildInboxFixture(t)
	require.NoError(t, appendMiningEvent(f.live, 1, time.Unix(10, 0), store.MiningProposalRaised, store.MiningProposalRaisedPayload{
		RecipeID:  "recipe-accepted",
		Kind:      "binding",
		Target:    "root-instance",
		Priority:  0.91,
		Rung:      1,
		DraftPath: ".artifacts/mining/recipe-accepted",
	}))
	require.NoError(t, appendMiningEvent(f.live, 2, time.Unix(20, 0), store.MiningProposalRaised, store.MiningProposalRaisedPayload{
		RecipeID:  "recipe-pending",
		Kind:      "intent",
		Target:    "dev-story",
		Priority:  0.72,
		Rung:      2,
		DraftPath: ".artifacts/mining/recipe-pending",
	}))
	require.NoError(t, appendMiningEvent(f.live, 3, time.Unix(30, 0), store.MiningProposalDecided, store.MiningProposalDecidedPayload{
		RecipeID:   "recipe-accepted",
		Verdict:    store.MiningVerdictAccept,
		By:         store.MiningByHuman,
		FlowsGreen: true,
	}))

	var work server.WorkListResult
	rpcCall(t, f.ts, "runstatus.work.list", nil, &work)
	assert.Equal(t, 1, work.Summary.MiningProposals)
	assert.Equal(t, 1, work.Summary.Items)
	assert.Equal(t, 0, work.Summary.NeedsAttention)
	require.Len(t, work.Items, 1)
	got := work.Items[0]
	assert.Equal(t, "mining_proposal", got.Kind)
	assert.Equal(t, "recipe-pending", got.ProposalID)
	assert.Equal(t, "intent", got.ProposalKind)
	assert.Equal(t, "dev-story", got.ProposalTarget)
	assert.Equal(t, ".artifacts/mining/recipe-pending", got.DraftPath)
	assert.Equal(t, 2, got.Rung)
	assert.Equal(t, "session", got.ReacquireTool)
	assert.Equal(t, f.publicID, got.ReacquireSessionID)
	assert.Contains(t, got.Body, "target=dev-story")

	require.NoError(t, appendMiningEvent(f.live, 4, time.Unix(40, 0), store.MiningProposalDecided, store.MiningProposalDecidedPayload{
		RecipeID: "recipe-pending",
		Verdict:  store.MiningVerdictReject,
		By:       store.MiningByHuman,
	}))
	var after server.WorkListResult
	rpcCall(t, f.ts, "runstatus.work.list", nil, &after)
	assert.Equal(t, 0, after.Summary.MiningProposals)
	assert.Empty(t, after.Items)
}

func TestWorkList_DoesNotTreatPassiveNotificationsAsAttention(t *testing.T) {
	f := buildInboxFixture(t)
	now := time.Now()
	n := &jobs.Notification{
		SessionID:     f.sid,
		CreatedAt:     now,
		Severity:      jobs.SeveritySuccess,
		Title:         "Job done",
		TeleportState: "inbox",
	}
	require.NoError(t, f.js.InsertNotification(context.Background(), n))
	require.NoError(t, f.js.UpsertJob(context.Background(), &jobs.Job{
		ID:        "job-running",
		SessionID: f.sid,
		Kind:      "host.agent.task",
		Status:    jobs.JobRunning,
		CreatedAt: now.Add(-time.Minute),
		UpdatedAt: now.Add(-time.Second),
	}))

	var work server.WorkListResult
	rpcCall(t, f.ts, "runstatus.work.list", nil, &work)
	assert.Equal(t, 1, work.Summary.NotificationsUnread)
	assert.Equal(t, 0, work.Summary.NotificationsActionRequired)
	assert.Equal(t, 0, work.Summary.NeedsAttention)
	require.Len(t, work.Items, 2)
	assert.Equal(t, "job", work.Items[0].Kind)
	assert.Equal(t, "job-running", work.Items[0].JobID)
	assert.Equal(t, "notification", work.Items[1].Kind)
	assert.Equal(t, jobs.SeveritySuccess, work.Items[1].Severity)
}

func appendMiningEvent(sink store.EventSink, turn int64, ts time.Time, kind store.EventKind, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return sink.Append(store.Event{
		Turn:    app.TurnNumber(turn),
		Ts:      ts,
		Kind:    kind,
		Payload: raw,
	})
}

func TestWorkList_AwaitingJobShowsClarificationPrompt(t *testing.T) {
	f := buildInboxFixture(t)
	now := time.Now()
	require.NoError(t, f.js.UpsertJob(context.Background(), &jobs.Job{
		ID:        "job-awaiting",
		SessionID: f.sid,
		Kind:      "host.run",
		Status:    jobs.JobRunning,
		CreatedAt: now.Add(-time.Minute),
		UpdatedAt: now,
	}))
	require.NoError(t, f.js.RequestClarification(context.Background(), "job-awaiting", jobs.ClarificationSchema{
		Prompt: "Which environment should I use?",
		Fields: map[string]string{"answer": "string"},
	}))

	var work server.WorkListResult
	rpcCall(t, f.ts, "runstatus.work.list", nil, &work)
	assert.Equal(t, 1, work.Summary.JobsAwaitingInput)
	assert.Equal(t, 1, work.Summary.NeedsAttention)
	require.Len(t, work.Items, 1)
	assert.Equal(t, "job", work.Items[0].Kind)
	assert.Equal(t, "job-awaiting", work.Items[0].JobID)
	assert.Equal(t, "Which environment should I use?", work.Items[0].Body)
}

func TestWorkList_JobReacquirePrefersActionRequiredNotification(t *testing.T) {
	f := buildInboxFixture(t)
	now := time.Now()
	require.NoError(t, f.js.UpsertJob(context.Background(), &jobs.Job{
		ID:        "job-awaiting",
		SessionID: f.sid,
		Kind:      "host.run",
		Status:    jobs.JobRunning,
		CreatedAt: now.Add(-time.Minute),
		UpdatedAt: now,
	}))
	require.NoError(t, f.js.RequestClarification(context.Background(), "job-awaiting", jobs.ClarificationSchema{
		Prompt: "Which environment should I use?",
		Fields: map[string]string{"answer": "string"},
	}))
	info := &jobs.Notification{
		SessionID:     f.sid,
		CreatedAt:     now.Add(-30 * time.Second),
		Severity:      jobs.SeverityInfo,
		Title:         "Job submitted",
		TeleportState: "running",
		TeleportJobID: "job-awaiting",
		OriginKind:    "job",
		OriginRef:     "job:job-awaiting",
	}
	require.NoError(t, f.js.InsertNotification(context.Background(), info))
	action := &jobs.Notification{
		SessionID:     f.sid,
		CreatedAt:     now.Add(-20 * time.Second),
		Severity:      jobs.SeverityActionRequired,
		Title:         "Input required",
		Body:          "Pick staging or prod.",
		TeleportState: "running",
		TeleportJobID: "job-awaiting",
		OriginKind:    "job",
		OriginRef:     "job:job-awaiting",
	}
	require.NoError(t, f.js.InsertNotification(context.Background(), action))

	var work server.WorkListResult
	rpcCall(t, f.ts, "runstatus.work.list", nil, &work)
	require.Len(t, work.Items, 3)
	var jobItem *server.WorkItem
	for i := range work.Items {
		if work.Items[i].Kind == "job" {
			jobItem = &work.Items[i]
			break
		}
	}
	require.NotNil(t, jobItem)
	assert.Equal(t, action.ID, jobItem.NotificationID)
	assert.Equal(t, "Pick staging or prod.", jobItem.Body)
	assert.Equal(t, "notification", jobItem.ReacquireTool)
}

func TestChatShow_SurfacesFocusedAsyncChatContext(t *testing.T) {
	f := buildInboxFixture(t)
	chat, err := f.chats.Create(context.Background(), "cloak", "agent", "scope-bg", "Background Claude")
	require.NoError(t, err)
	_, err = f.db.ExecContext(context.Background(), `UPDATE chats SET session_id = ? WHERE id = ?`, string(f.sid), chat.ID)
	require.NoError(t, err)
	_, err = f.chats.AppendMessage(context.Background(), chat.ID, "user", "check the flaky test", nil)
	require.NoError(t, err)
	_, err = f.chats.AppendMessage(context.Background(), chat.ID, "assistant", "the failure is in setup", map[string]any{"tool": "go test"})
	require.NoError(t, err)
	_, err = f.chats.AttachPTY(context.Background(), chats.AttachPTYOptions{
		ChatID:         chat.ID,
		TmuxSession:    "kit-bg",
		PermissionMode: "acceptEdits",
		WorkspacePath:  "/tmp/work",
	})
	require.NoError(t, err)
	_, err = f.chats.DetachPTY(context.Background(), chat.ID)
	require.NoError(t, err)

	var out server.ChatShowResult
	rpcCall(t, f.ts, "runstatus.chat.show",
		map[string]any{"session_id": f.publicID, "chat_id": chat.ID}, &out)

	assert.True(t, out.OK)
	require.NotNil(t, out.Context)
	assert.Equal(t, f.publicID, out.Context.SessionID)
	assert.Equal(t, chat.ID, out.Chat.ID)
	assert.Equal(t, "Background Claude", out.Chat.Title)
	assert.Equal(t, "scope-bg", out.Chat.DisplayScopeKey)
	assert.Equal(t, string(f.sid), out.Chat.SessionID)
	require.NotNil(t, out.PTY)
	assert.Equal(t, "kit-bg", out.PTY.TmuxSession)
	assert.Equal(t, "pty_background", out.PTY.Mode)
	require.Len(t, out.Messages, 2)
	assert.Equal(t, "check the flaky test", out.Messages[0].Content)
	assert.Equal(t, "the failure is in setup", out.Messages[1].Content)
	assert.Equal(t, map[string]any{"tool": "go test"}, out.Messages[1].Metadata)

	var since server.ChatShowResult
	rpcCall(t, f.ts, "runstatus.chat.show",
		map[string]any{"session_id": f.publicID, "chat_id": chat.ID, "since_seq": 1}, &since)
	require.Len(t, since.Messages, 1)
	assert.Equal(t, 1, since.Messages[0].Seq)
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
