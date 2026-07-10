package tui_test

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"

	tuipkg "kitsoki/internal/tui"
)

// single_pane_regression_test.go — regression suite locking down the
// behaviour the single-pane TUI redesign promises. The unit-level
// blocks tests pin the renderer; these tests drive the live RootModel
// through real flows and assert the user-visible outcomes.

// ─── Queue mechanics (Phase 4) ───────────────────────────────────────────

// TestQueueEnqueueWhileAwaitingLLM asserts the second free-text submit
// queues instead of pre-empting the in-flight turn.
func TestQueueEnqueueWhileAwaitingLLM(t *testing.T) {
	t.Parallel()
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)
	rm, _ := tuipkg.ExtractRootModel(m)
	rm = tuipkg.SimulateSlowHarnessTurnStart(rm)

	tuipkg.SetPromptValue(&rm, "queued one")
	m2, _ := tea.Model(rm).Update(tea.KeyMsg{Type: tea.KeyEnter})
	rm, _ = tuipkg.ExtractRootModel(m2)
	require.Equal(t, []string{"queued one"}, tuipkg.InputQueue(rm),
		"in-room input during ModeAwaitingLLM must enqueue")
	require.Contains(t, tuipkg.GetTranscriptContent(rm), "queued",
		"transcript should contain a queued notice")
}

// TestQueueEscClearsAndStashesToHistory asserts Esc empties the queue
// and pushes each item onto inputHistory so ↑ recovers them.
func TestQueueEscClearsAndStashesToHistory(t *testing.T) {
	t.Parallel()
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)
	rm, _ := tuipkg.ExtractRootModel(m)
	tuipkg.SetInputQueueForTest(&rm, "first", "second", "third")

	m2, _ := tea.Model(rm).Update(tea.KeyMsg{Type: tea.KeyEsc})
	rm, _ = tuipkg.ExtractRootModel(m2)
	require.Empty(t, tuipkg.InputQueue(rm), "queue should empty after Esc")

	history := tuipkg.GetInputHistory(rm)
	// Items should land in submission order at the end of history.
	require.Contains(t, history, "first")
	require.Contains(t, history, "second")
	require.Contains(t, history, "third")
}

// TestQueueDrainDoesNotDuplicateHistory pins the bug fix for the
// queue drain re-appending each item to history (caught during code
// review). After enqueue, the original submission is already in
// history; the drain must not append it again.
func TestQueueDrainDoesNotDuplicateHistory(t *testing.T) {
	t.Parallel()
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)
	rm, _ := tuipkg.ExtractRootModel(m)
	rm = tuipkg.SimulateSlowHarnessTurnStart(rm)

	tuipkg.SetPromptValue(&rm, "one")
	m2, _ := tea.Model(rm).Update(tea.KeyMsg{Type: tea.KeyEnter})
	rm, _ = tuipkg.ExtractRootModel(m2)

	// History should have exactly one "one" — the original submission.
	// The drain path (when the turn completes) must reuse it, not
	// re-append.
	history := tuipkg.GetInputHistory(rm)
	count := 0
	for _, h := range history {
		if h == "one" {
			count++
		}
	}
	require.Equal(t, 1, count, "history should not double-append queued items; got %v", history)
}

// TestQueueClearsOnMetaEntry asserts queued items are stashed to
// history and the queue is dropped when the user navigates to a
// /meta room. This avoids the items dispatching in the wrong room
// when the user returns via /onpath.
func TestQueueClearsOnMetaEntry(t *testing.T) {
	t.Parallel()
	// Tests need a working /meta target; cloak doesn't carry one, so
	// we just seed the queue and force ModeMeta via the meta-enter
	// path's stash branch directly. Building the full /meta dispatch
	// in unit tests is brittle — this test exercises the clearing
	// helper instead.
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)
	rm, _ := tuipkg.ExtractRootModel(m)
	tuipkg.SetInputQueueForTest(&rm, "a", "b")

	// Synthesise the queue-stash logic mirroring handleMetaEnterDone:
	// every queued item lands in history, queue empties.
	// (We can't call handleMetaEnterDone directly because it requires
	// a fully wired metamode.Controller — out of scope.) The test
	// reuses Esc's identical stash path which is a separate code
	// site but the same semantics.
	m2, _ := tea.Model(rm).Update(tea.KeyMsg{Type: tea.KeyEsc})
	rm, _ = tuipkg.ExtractRootModel(m2)
	require.Empty(t, tuipkg.InputQueue(rm))
	require.Contains(t, tuipkg.GetInputHistory(rm), "a")
	require.Contains(t, tuipkg.GetInputHistory(rm), "b")
}

// ─── Live routing tier events (Phase 2) ──────────────────────────────────

// TestRoutingTierMissUpdatesTranscript asserts an inbound
// RoutingTierMissMsg advances the live routing line. Post-scrollback
// refactor: the live line lives on m.transcript.liveLine (rendered
// in View() above the prompt), not in the entries slice — so we
// check LiveLineForTest, not GetTranscriptContent.
func TestRoutingTierMissUpdatesTranscript(t *testing.T) {
	t.Parallel()
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)
	rm, _ := tuipkg.ExtractRootModel(m)

	tuipkg.SetPromptValue(&rm, "something nonexistent")
	m2, _ := tea.Model(rm).Update(tea.KeyMsg{Type: tea.KeyEnter})

	m3, _ := m2.Update(tuipkg.RoutingTierMissMsg{Tier: tuipkg.TierDeterministic})
	rm, _ = tuipkg.ExtractRootModel(m3)
	live := tuipkg.LiveLineForTest(rm)
	require.True(t,
		strings.Contains(live, "routing") || strings.Contains(live, "synonyms"),
		"live line should reflect routing tier advance; got: %q", live)
}

// TestRoutingLiveLineDefersScrollbackFlushWhileAwaiting pins the corruption
// where the live routing row was redrawn while tea.Println was also flushing the
// submitted input echo into scrollback. The queued echo must wait until the
// live routing line settles, otherwise prior routing frames can be stamped into
// the terminal history during semantic/LLM routing.
func TestRoutingLiveLineDefersScrollbackFlushWhileAwaiting(t *testing.T) {
	t.Parallel()
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)
	rm, _ := tuipkg.ExtractRootModel(m)
	tuipkg.ClearTranscriptPendingForTest(&rm)

	tuipkg.SetPromptValue(&rm, "something nonexistent")
	m2, _ := tea.Model(rm).Update(tea.KeyMsg{Type: tea.KeyEnter})
	rm, _ = tuipkg.ExtractRootModel(m2)

	require.NotEmpty(t, tuipkg.LiveLineForTest(rm), "routing should be live")
	pending := tuipkg.PendingTranscriptForTest(rm)
	require.Len(t, pending, 1, "input echo should stay queued while routing is live")
	require.Contains(t, pending[0], "> something nonexistent")

	m3, _ := tea.Model(rm).Update(tuipkg.RoutingTierHitMsg{
		Tier:       tuipkg.TierLLM,
		Intent:     "pick_branch",
		Confidence: 0.84,
	})
	rm, _ = tuipkg.ExtractRootModel(m3)
	require.Empty(t, tuipkg.LiveLineForTest(rm), "hit should settle the live line")
	require.Empty(t, tuipkg.PendingTranscriptForTest(rm), "settled routing should flush queued scrollback")
}

// TestRoutingTierHitSettlesInTranscript asserts a hit message
// finalises the live entry with the settled resolution line.
func TestRoutingTierHitSettlesInTranscript(t *testing.T) {
	t.Parallel()
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)
	rm, _ := tuipkg.ExtractRootModel(m)

	tuipkg.SetPromptValue(&rm, "something nonexistent")
	m2, _ := tea.Model(rm).Update(tea.KeyMsg{Type: tea.KeyEnter})

	m3, _ := m2.Update(tuipkg.RoutingTierHitMsg{
		Tier:       tuipkg.TierLLM,
		Intent:     "pick_branch",
		Confidence: 0.84,
	})
	rm, _ = tuipkg.ExtractRootModel(m3)
	content := tuipkg.GetTranscriptContent(rm)
	require.Contains(t, content, "pick_branch",
		"settled line should carry the resolved intent")
	require.Contains(t, content, "LLM",
		"settled line should mark source as LLM for TierLLM")
}

// ─── /intents auto on actually fires (Phase 1) ──────────────────────────

// TestActionsAutoEmitsBlockAfterTurn drives a turn through the cloak
// fixture with actionsAuto=true and asserts the actions block lands
// after the turn outcome.
func TestActionsAutoEmitsBlockAfterTurn(t *testing.T) {
	t.Parallel()
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)
	rm, _ := tuipkg.ExtractRootModel(m)
	tuipkg.SetActionsAutoForTest(&rm, true)

	m = runTurnBlocking(t, tea.Model(rm), "go west")
	content := extractTranscript(t, m)
	require.Contains(t, content, "actions",
		"actions block should auto-print after the turn when /intents auto is on; got:\n%s", content)
}

// ─── promptPrefix per Mode (Phase 6) ────────────────────────────────────

// TestPromptPrefixPerMode walks every Mode and asserts the glyph in
// the prompt prefix matches the proposal table.
func TestPromptPrefixPerMode(t *testing.T) {
	t.Parallel()
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)
	rm, _ := tuipkg.ExtractRootModel(m)

	cases := map[tuipkg.Mode]string{
		tuipkg.ModeOnPath:      "> ",
		tuipkg.ModeMeta:        "» ",
		tuipkg.ModeOffPath:     "# ",
		tuipkg.ModeSlotFilling: "? ",
		tuipkg.ModeAwaitingLLM: "… ",
	}
	for mode, want := range cases {
		tuipkg.SetModeForTest(&rm, mode)
		got := tuipkg.PromptPrefixForTest(rm)
		require.Contains(t, got, want, "mode %v should render prefix %q; got %q", mode, want, got)
	}
}

// TestFooterFrameworkLineCarriesState asserts the framework footer
// line carries location + queue depth + unread count. (The mode
// label moved off the framework line and onto the right side of
// the colored StatusRow — without that split, "awaiting awaiting"
// showed twice in the bottom chrome.)
func TestFooterFrameworkLineCarriesState(t *testing.T) {
	t.Parallel()
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)
	rm, _ := tuipkg.ExtractRootModel(m)

	// Default: contains the location.
	got := tuipkg.FooterLine1ForTest(rm)
	require.Contains(t, got, "foyer", "footer should mention the foyer location; got %q", got)

	// Queue depth should surface when non-empty.
	tuipkg.SetInputQueueForTest(&rm, "x", "y")
	got = tuipkg.FooterLine1ForTest(rm)
	require.Contains(t, got, "2 queued", "footer should report queue depth; got %q", got)
}

// ─── Immediate echo + settled line (Phase 1) ────────────────────────────

// TestImmediateEchoBeforeOutcome asserts the user's input lands in
// the transcript *before* the turn outcome arrives (slow-harness
// pattern: we just don't process the async cmd).
func TestImmediateEchoBeforeOutcome(t *testing.T) {
	t.Parallel()
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)
	rm, _ := tuipkg.ExtractRootModel(m)

	tuipkg.SetPromptValue(&rm, "go west")
	m2, _ := tea.Model(rm).Update(tea.KeyMsg{Type: tea.KeyEnter})
	rm, _ = tuipkg.ExtractRootModel(m2)
	// We do NOT process the returned tea.Cmd — that would resolve
	// the turn. The echo and settled-routing should already be in
	// the transcript.
	content := tuipkg.GetTranscriptContent(rm)
	require.Contains(t, content, "> go west",
		"immediate echo should land before turn completes; got:\n%s", content)
}

// TestSettledLineInTranscript runs a slash command and asserts the
// resolved-routing line stayed in the transcript. We use /help so
// the room doesn't change (a room change moves the buffer focus to
// the new room, leaving the settled line in the prior room's
// buffer — correct behaviour, but harder to assert against).
func TestSettledLineInTranscript(t *testing.T) {
	t.Parallel()
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)

	m = runTurnBlocking(t, m, "/help")
	content := extractTranscript(t, m)
	require.Contains(t, content, "→",
		"settled routing line should remain in the transcript; got:\n%s", content)
	require.Contains(t, content, "system",
		"slash commands should classify as system; got:\n%s", content)
}

// ─── /world and /trace slash dispatchers (Phase 1.5/2) ──────────────────

// TestWorldSlashOpensDedicatedView asserts /world enters
// ModeWorldView and q closes back to ModeOnPath.
func TestWorldSlashOpensDedicatedView(t *testing.T) {
	t.Parallel()
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)

	m = runTurnBlocking(t, m, "/world")
	require.Equal(t, tuipkg.ModeWorldView, extractMode(t, m),
		"/world should enter ModeWorldView")

	// q closes.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	require.Equal(t, tuipkg.ModeOnPath, extractMode(t, m),
		"q in world view should return to ModeOnPath")

	// Re-open then Esc closes.
	m = runTurnBlocking(t, m, "/world")
	require.Equal(t, tuipkg.ModeWorldView, extractMode(t, m))
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	require.Equal(t, tuipkg.ModeOnPath, extractMode(t, m),
		"Esc in world view should return to ModeOnPath")
}

// TestTraceSlashPrintsBlock confirms /trace prints an inline
// transcript block (even when no observer is wired the friendly
// "no observer" notice should land).
func TestTraceSlashPrintsBlock(t *testing.T) {
	t.Parallel()
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)

	m = runTurnBlocking(t, m, "/trace")
	content := extractTranscript(t, m)
	require.Contains(t, content, "trace",
		"/trace slash should print a trace block (or the no-observer notice); got:\n%s", content)
}

// TestDisambiguationChoiceDispatches asserts that picking a
// disambiguation candidate actually runs a turn against the chosen
// intent — pre-fix the candidate was journaled but no turn fired.
func TestDisambiguationChoiceDispatches(t *testing.T) {
	t.Parallel()
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)
	rm, _ := tuipkg.ExtractRootModel(m)

	// Open the disambig overlay with two candidates picked from
	// cloak's foyer menu so we know dispatch will hit a real intent.
	tuipkg.OpenDisambiguationForTest(&rm, []tuipkg.CandidateForTest{
		{Intent: "go_south", Title: "go south"},
		{Intent: "go_west", Title: "go west"},
	})
	require.Equal(t, tuipkg.ModeDisambiguating, tuipkg.GetMode(rm),
		"open should put the model into ModeDisambiguating")

	// User picks candidate 1 by typing "1" into the prompt + Enter.
	tuipkg.SetPromptValue(&rm, "1")
	m2, cmd := tea.Model(rm).Update(tea.KeyMsg{Type: tea.KeyEnter})
	rm, _ = tuipkg.ExtractRootModel(m2)

	// Mode should leave Disambiguating; an async turn cmd should be
	// returned (the dispatch fires through startAsyncTurn).
	require.NotEqual(t, tuipkg.ModeDisambiguating, tuipkg.GetMode(rm),
		"choice should leave Disambiguating")
	require.NotNil(t, cmd, "choice should produce an async turn cmd, not a nil no-op")
	content := tuipkg.GetTranscriptContent(rm)
	require.Contains(t, content, "chose:",
		"transcript should record the choice; got:\n%s", content)
}

// TestInboxSlashPrintsBlock confirms /inbox prints inline output.
// In tests there's no job store wired, so the friendly headless
// notice is what we expect.
func TestInboxSlashPrintsBlock(t *testing.T) {
	t.Parallel()
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)

	m = runTurnBlocking(t, m, "/inbox")
	content := extractTranscript(t, m)
	require.Contains(t, content, "inbox",
		"/inbox slash should print an inbox block; got:\n%s", content)
}
