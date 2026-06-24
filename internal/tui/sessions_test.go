package tui_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/chats"
	"kitsoki/internal/store"
	tuipkg "kitsoki/internal/tui"
)

// openSessionsTestStore opens a fresh on-disk *chats.Store for use by
// /sessions tests. The DB lives in the test's TempDir so each case
// gets isolation.
func openSessionsTestStore(t *testing.T) (*chats.Store, func()) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	s, err := store.Open(dbPath)
	require.NoError(t, err)
	cs, err := chats.NewStore(s.DB())
	require.NoError(t, err)
	return cs, func() { _ = s.Close() }
}

// buildSessionsTestModel wires a RootModel with a real *chats.Store
// over a temp SQLite DB, using the cloak orchestrator so the TUI is
// ready to accept slash commands.
func buildSessionsTestModel(t *testing.T, cs *chats.Store) tea.Model {
	t.Helper()
	orch, sid := setupCloak(t)
	w := orch.InitialWorld()
	initialView, err := orch.InitialView(w)
	require.NoError(t, err)
	return tuipkg.NewRootModel(orch, sid, "", initialView,
		tuipkg.WithChatStore(cs),
	)
}

// TestSessions_ListEmpty: /sessions list with no chat_pty_sessions
// rows prints the empty-state message and leaves the cache nil.
func TestSessions_ListEmpty(t *testing.T) {
	cs, cleanup := openSessionsTestStore(t)
	defer cleanup()

	m := buildSessionsTestModel(t, cs)
	m = runTurnBlocking(t, m, "/sessions list")

	tx := extractTranscript(t, m)
	assert.Contains(t, tx, "no active claude sessions",
		"empty-list path should mention 'no active claude sessions'; got:\n%s", tx)
}

// TestSessions_ListThenAttachIndexInvalid: list with one row, then
// attach an out-of-range index → friendly error, no attempt to exec
// tmux.
func TestSessions_ListThenAttachIndexInvalid(t *testing.T) {
	cs, cleanup := openSessionsTestStore(t)
	defer cleanup()

	ctx := context.Background()
	chat, err := cs.Create(ctx, "bugfix", "live", "PROJ-1", "live work")
	require.NoError(t, err)
	_, err = cs.AttachPTY(ctx, chats.AttachPTYOptions{
		ChatID:      chat.ID,
		TmuxSession: "kitsoki-chat-" + chat.ID,
	})
	require.NoError(t, err)

	m := buildSessionsTestModel(t, cs)
	m = runTurnBlocking(t, m, "/sessions list")

	tx := extractTranscript(t, m)
	// Styled-table row should appear with the chat title and scope.
	// The table uses its own column for SCOPE — earlier prose
	// formatting appended scope inline as "[PROJ-1]", which we've
	// since replaced.
	assert.Contains(t, tx, "claude sessions",
		"list should include the styled-table title; got:\n%s", tx)
	assert.Contains(t, tx, "live work",
		"list should include the chat title; got:\n%s", tx)
	assert.Contains(t, tx, "PROJ-1",
		"list should surface scope_key in its own column; got:\n%s", tx)
	assert.Contains(t, tx, "/sessions attach",
		"list footer should hint at the attach verb; got:\n%s", tx)

	// Out-of-range attach.
	m = runTurnBlocking(t, m, "/sessions attach 99")
	tx = extractTranscript(t, m)
	assert.Contains(t, tx, "invalid index",
		"out-of-range attach should print invalid-index error; got:\n%s", tx)
}

func TestSessions_AttachDryRunResolvesCachedTarget(t *testing.T) {
	cs, cleanup := openSessionsTestStore(t)
	defer cleanup()

	ctx := context.Background()
	chat, err := cs.Create(ctx, "bugfix", "live", "PROJ-1", "live work")
	require.NoError(t, err)
	_, err = cs.AttachPTY(ctx, chats.AttachPTYOptions{
		ChatID:      chat.ID,
		TmuxSession: "kitsoki-chat-" + chat.ID,
	})
	require.NoError(t, err)

	m := buildSessionsTestModel(t, cs)
	m = runTurnBlocking(t, m, "/sessions list")
	m = runTurnBlocking(t, m, "/sessions attach 1 --dry-run")

	tx := extractTranscript(t, m)
	assert.Contains(t, tx, "would attach 1",
		"dry-run attach should resolve the cached index; got:\n%s", tx)
	assert.Contains(t, tx, "live work",
		"dry-run attach should name the resolved chat; got:\n%s", tx)
	assert.Contains(t, tx, "kitsoki-chat-"+chat.ID,
		"dry-run attach should name the resolved tmux session; got:\n%s", tx)
}

// TestSessions_AttachWithoutListFirst: /sessions attach with no
// prior /sessions list call refuses with a friendly hint instead of
// firing a tea.Exec.
func TestSessions_AttachWithoutListFirst(t *testing.T) {
	cs, cleanup := openSessionsTestStore(t)
	defer cleanup()

	// Seed a row so a hasty caller might expect it to "just work".
	ctx := context.Background()
	chat, _ := cs.Create(ctx, "bugfix", "live", "", "x")
	_, _ = cs.AttachPTY(ctx, chats.AttachPTYOptions{
		ChatID:      chat.ID,
		TmuxSession: "kitsoki-chat-" + chat.ID,
	})

	m := buildSessionsTestModel(t, cs)
	m = runTurnBlocking(t, m, "/sessions attach 1")
	tx := extractTranscript(t, m)
	assert.Contains(t, tx, "no cached sessions list",
		"attach-without-list should hint at /sessions list; got:\n%s", tx)
}

// TestSessions_UnknownSubcommand: /sessions foobar produces a
// usage-style hint rather than firing anything.
func TestSessions_UnknownSubcommand(t *testing.T) {
	cs, cleanup := openSessionsTestStore(t)
	defer cleanup()
	m := buildSessionsTestModel(t, cs)
	m = runTurnBlocking(t, m, "/sessions foobar")
	tx := extractTranscript(t, m)
	assert.Contains(t, tx, "unknown subcommand",
		"unknown verb should print 'unknown subcommand'; got:\n%s", tx)
}

// TestSessions_NoChatStoreWired: without WithChatStore, /sessions
// should surface a polite "requires a chat store" message rather
// than panicking.
func TestSessions_NoChatStoreWired(t *testing.T) {
	orch, sid := setupCloak(t)
	w := orch.InitialWorld()
	initialView, err := orch.InitialView(w)
	require.NoError(t, err)
	m := tea.Model(tuipkg.NewRootModel(orch, sid, "", initialView))
	m = runTurnBlocking(t, m, "/sessions list")
	tx := extractTranscript(t, m)
	require.True(t, strings.Contains(tx, "requires a chat store") ||
		strings.Contains(tx, "no chat store"),
		"missing chat store should produce a clear hint; got:\n%s", tx)
}
