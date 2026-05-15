package orchestrator_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/chathost"
	"kitsoki/internal/chats"
	"kitsoki/internal/host"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

// fakeOraclePath returns the absolute path to the host package's fake-oracle.sh,
// shared with TestOracleTalk_* via internal/host/testdata. Reused here so the
// off-path test doesn't need its own stub binary.
func fakeOraclePath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	// internal/orchestrator → ../host/testdata/fake-oracle.sh
	path := filepath.Join(filepath.Dir(thisFile), "..", "host", "testdata", "fake-oracle.sh")
	info, err := os.Stat(path)
	require.NoErrorf(t, err, "fake-oracle.sh not found at %s", path)
	require.NotZerof(t, info.Mode()&0111, "fake-oracle.sh is not executable")
	return path
}

// minimalOffPathApp returns an AppDef with the bare minimum to run AskOffPath:
// an ID for the chat scope plus a single state with a self-transition so the
// foreground-turn-after-offpath regression test can fire foreground turns
// through RunIntent and confirm they don't collide with off-path events.
func minimalOffPathApp() *app.AppDef {
	return &app.AppDef{
		App:  app.AppMeta{ID: "offpath-test", Version: "1"},
		Root: "start",
		Intents: map[string]app.Intent{
			"look": {Title: "Look", Description: "Look around."},
		},
		States: map[string]*app.State{
			"start": {
				View: app.LegacyView("hello"),
				On: map[string][]app.Transition{
					"look": {{Target: "start"}},
				},
			},
		},
	}
}

// setupOffPathOrch builds an orchestrator wired with a real chats.Store and
// the fake-oracle.sh binary as the claude stand-in, then returns the
// orchestrator plus the raw store and chat store and a fresh session id.
// Tests sniff store.LoadHistory directly to assert event-log content because
// the orchestrator doesn't expose its internal store handle.
func setupOffPathOrch(t *testing.T) (*orchestrator.Orchestrator, store.Store, *chats.Store, app.SessionID) {
	t.Helper()
	t.Setenv(host.OracleBinEnv, fakeOraclePath(t))

	def := minimalOffPathApp()
	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	rawChatStore, err := chats.NewStore(s.DB())
	require.NoError(t, err)

	orch := orchestrator.New(def, m, s, noopHarness{},
		orchestrator.WithChatStore(chathost.NewAdapter(rawChatStore)),
	)

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	return orch, s, rawChatStore, sid
}

// TestAskOffPath_ReturnsReply asserts the happy-path: a question is fired
// against the fake oracle, an answer comes back, and the world/state are
// unchanged.
func TestAskOffPath_ReturnsReply(t *testing.T) {
	orch, _, _, sid := setupOffPathOrch(t)
	ctx := context.Background()

	// Snapshot the journey before the off-path call.
	jBefore, err := orch.LoadJourney(sid)
	require.NoError(t, err)
	stateBefore := jBefore.State
	worldVarsBefore := len(jBefore.World.Vars)

	answer, err := orch.AskOffPath(ctx, sid, "what is 2+2?")
	require.NoError(t, err)
	require.Contains(t, answer, "ANSWER for q=[what is 2+2?]",
		"fake-oracle should have echoed the question in its reply")

	// Re-load: state and world must be unchanged. Turn IS allowed to
	// advance — off-path events claim unique turn numbers (via the
	// MAX(turn)+1 allocator) so they don't collide with the next
	// foreground turn. js.Turn tracking those is what prevents the
	// foreground from reusing a turn number and hitting a PK collision.
	jAfter, err := orch.LoadJourney(sid)
	require.NoError(t, err)
	require.Equal(t, stateBefore, jAfter.State, "off-path must not mutate state")
	require.Equal(t, worldVarsBefore, len(jAfter.World.Vars),
		"off-path must not mutate world")
	require.Greater(t, jAfter.Turn, jBefore.Turn,
		"off-path events claim turn numbers so foreground can't reuse them")
}

// TestAskOffPath_CreatesAndReusesChat asserts that the first call creates
// the per-session chat row keyed by (app_id, "off_path", session_id) and
// subsequent calls reuse it.
func TestAskOffPath_CreatesAndReusesChat(t *testing.T) {
	orch, _, rawChatStore, sid := setupOffPathOrch(t)
	ctx := context.Background()

	// Sanity: no chats exist yet.
	chatsBefore, err := rawChatStore.List(ctx, "offpath-test", "off_path", string(sid))
	require.NoError(t, err)
	require.Empty(t, chatsBefore, "no off-path chat should exist before first AskOffPath")

	// First call — must create a chat.
	_, err = orch.AskOffPath(ctx, sid, "first question")
	require.NoError(t, err)

	chatsAfter1, err := rawChatStore.List(ctx, "offpath-test", "off_path", string(sid))
	require.NoError(t, err)
	require.Len(t, chatsAfter1, 1, "first AskOffPath should have created exactly one chat")
	chatID := chatsAfter1[0].ID

	// Second call — must reuse the same chat.
	_, err = orch.AskOffPath(ctx, sid, "second question")
	require.NoError(t, err)

	chatsAfter2, err := rawChatStore.List(ctx, "offpath-test", "off_path", string(sid))
	require.NoError(t, err)
	require.Len(t, chatsAfter2, 1, "second AskOffPath should have reused the existing chat")
	require.Equal(t, chatID, chatsAfter2[0].ID, "chat ID must be stable across calls")

	// Transcript should now contain four messages (user/assistant × 2 turns).
	msgs, err := rawChatStore.Transcript(ctx, chatID, 0)
	require.NoError(t, err)
	require.Len(t, msgs, 4, "transcript should hold both Q+A pairs")
}

// TestAskOffPath_LogsEvents asserts that OffPathQuestion and OffPathAnswer
// events land in the session log so a replayer can reconstruct the off-path
// transcript from events alone.
func TestAskOffPath_LogsEvents(t *testing.T) {
	orch, rawStore, _, sid := setupOffPathOrch(t)
	ctx := context.Background()

	_, err := orch.AskOffPath(ctx, sid, "ping")
	require.NoError(t, err)

	jBefore, err := orch.LoadJourney(sid)
	require.NoError(t, err)
	// Replay should not have advanced state.
	require.Equal(t, app.StatePath("start"), jBefore.State)

	// Sniff the raw event log for the off-path event kinds.
	hist, err := rawStore.LoadHistory(sid)
	require.NoError(t, err)
	var sawQuestion, sawAnswer bool
	for _, ev := range hist {
		switch ev.Kind {
		case store.OffPathQuestion:
			sawQuestion = true
		case store.OffPathAnswer:
			sawAnswer = true
		case store.TransitionApplied, store.StateEntered, store.StateExited:
			t.Fatalf("off-path must not emit %s events; got %+v", ev.Kind, ev)
		}
	}
	require.True(t, sawQuestion, "expected OffPathQuestion in event log")
	require.True(t, sawAnswer, "expected OffPathAnswer in event log")
}

// TestForegroundTurnAfterOffPath_NoPKCollision is a regression test for the
// bug where off-path side-channel events claimed turn numbers via MAX(turn)+1
// but replay.BuildJourney's filter excluded them from js.Turn — so the next
// foreground RunIntent computed turnNum = stale_journey.Turn + 1, which
// collided with an off-path event already at that turn and surfaced as
// "orchestrator: append events: ...UNIQUE constraint failed..." to the user.
func TestForegroundTurnAfterOffPath_NoPKCollision(t *testing.T) {
	orch, _, _, sid := setupOffPathOrch(t)
	ctx := context.Background()

	// 1. Foreground turn first — journey.Turn becomes 1.
	_, err := orch.RunIntent(ctx, sid, "look", nil)
	require.NoError(t, err, "first foreground turn")

	// 2. /freeform — OffPathEntered at MAX(turn)+1 = 2.
	require.NoError(t, orch.MarkOffPathEntered(sid, "start"))

	// 3. Ask a question — OffPathQuestion + OffPathAnswer at 3.
	_, err = orch.AskOffPath(ctx, sid, "what should I do?")
	require.NoError(t, err, "off-path question")

	// 4. /onpath — OffPathExited at 4.
	require.NoError(t, orch.MarkOffPathExited(sid, "start"))

	// 5. Foreground turn — this is the call that used to fail with
	//    "UNIQUE constraint failed" because journey.Turn was stuck at 1
	//    and the next turn tried (sid, 2, 0) — already used by step 2.
	_, err = orch.RunIntent(ctx, sid, "look", nil)
	require.NoError(t, err, "foreground turn after off-path must not collide")
}

// TestMarkOffPathEnteredExited asserts the entry/exit event helpers append
// the right event kinds.
func TestMarkOffPathEnteredExited(t *testing.T) {
	orch, rawStore, _, sid := setupOffPathOrch(t)
	require.NoError(t, orch.MarkOffPathEntered(sid, "start"))
	require.NoError(t, orch.MarkOffPathExited(sid, "start"))

	hist, err := rawStore.LoadHistory(sid)
	require.NoError(t, err)
	var sawEntered, sawExited bool
	for _, ev := range hist {
		switch ev.Kind {
		case store.OffPathEntered:
			sawEntered = true
		case store.OffPathExited:
			sawExited = true
		}
	}
	require.True(t, sawEntered)
	require.True(t, sawExited)
}
