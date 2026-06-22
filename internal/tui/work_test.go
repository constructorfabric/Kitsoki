package tui_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/chats"
	"kitsoki/internal/jobs"
	"kitsoki/internal/store"
	tuipkg "kitsoki/internal/tui"
)

func buildWorkTestModel(t *testing.T) (tea.Model, app.SessionID, *jobs.JobStore, *chats.Store, *sql.DB) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "work.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	js, err := jobs.NewJobStore(s.DB())
	require.NoError(t, err)
	cs, err := chats.NewStore(s.DB())
	require.NoError(t, err)

	orch, sid := setupCloak(t)
	w := orch.InitialWorld()
	initialView, err := orch.InitialView(w)
	require.NoError(t, err)
	m := tuipkg.NewRootModel(orch, sid, "", initialView,
		tuipkg.WithJobStore(js),
		tuipkg.WithChatStore(cs),
	)
	return m, sid, js, cs, s.DB()
}

func TestWorkSlashListsActiveAsyncWork(t *testing.T) {
	m, sid, js, cs, db := buildWorkTestModel(t)
	ctx := context.Background()
	now := time.Now()

	require.NoError(t, js.UpsertJob(ctx, &jobs.Job{
		ID:          "job-running",
		SessionID:   sid,
		Kind:        "host.agent.task",
		Status:      jobs.JobRunning,
		OriginState: "foyer",
		CreatedAt:   now.Add(-2 * time.Minute),
		UpdatedAt:   now.Add(-time.Minute),
	}))
	require.NoError(t, js.InsertNotification(ctx, &jobs.Notification{
		SessionID:     sid,
		CreatedAt:     now.Add(-30 * time.Second),
		Severity:      jobs.SeverityActionRequired,
		Title:         "Review PR #42",
		TeleportState: "foyer",
		OriginKind:    "external",
		OriginRef:     "github:acme/repo/pr/42",
		OriginURL:     "https://github.com/acme/repo/pull/42",
	}))

	chat, err := cs.Create(ctx, "cloak", "agent", "scope", "Review proposal")
	require.NoError(t, err)
	_, err = cs.Enqueue(ctx, chats.EnqueueOptions{
		ChatID:          chat.ID,
		Transport:       chats.DriveTransportStateMachine,
		Actor:           "story",
		Payload:         "continue queued review",
		OriginSessionID: string(sid),
		OriginState:     "foyer",
	})
	require.NoError(t, err)

	bg, err := cs.Create(ctx, "cloak", "agent", "scope-bg", "Background Claude")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `UPDATE chats SET session_id = ? WHERE id = ?`, string(sid), bg.ID)
	require.NoError(t, err)
	_, err = cs.AttachPTY(ctx, chats.AttachPTYOptions{ChatID: bg.ID, TmuxSession: "kit-bg"})
	require.NoError(t, err)
	_, err = cs.DetachPTY(ctx, bg.ID)
	require.NoError(t, err)

	m = runTurnBlocking(t, m, "/work")
	tx := extractTranscript(t, m)
	require.Contains(t, tx, "active work: 4 item(s)")
	require.Contains(t, tx, "notification")
	require.Contains(t, tx, "Review PR #42")
	require.Contains(t, tx, "github:acme/repo/pr/42")
	require.Contains(t, tx, "https://github.com/acme/repo/pull/42")
	require.Contains(t, tx, "job")
	require.Contains(t, tx, "host.agent.task")
	require.Contains(t, tx, "queued")
	require.Contains(t, tx, "continue queued review")
	require.Contains(t, tx, "chat")
	require.Contains(t, tx, "Background Claude")
	require.Contains(t, tx, "/sessions attach 1")

	rm, ok := tuipkg.ExtractRootModel(m)
	require.True(t, ok)
	cached := tuipkg.CachedSessionListForTest(rm)
	require.Len(t, cached, 1)
	require.Equal(t, bg.ID, cached[0].ChatID)
}

func TestWorkSlashNoStores(t *testing.T) {
	orch, sid := setupCloak(t)
	w := orch.InitialWorld()
	initialView, err := orch.InitialView(w)
	require.NoError(t, err)
	m := tea.Model(tuipkg.NewRootModel(orch, sid, "", initialView))

	m = runTurnBlocking(t, m, "/work")
	tx := extractTranscript(t, m)
	require.Contains(t, tx, "no job or chat store wired")
}
