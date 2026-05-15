package tui_test

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"

	tuipkg "kitsoki/internal/tui"
)

// historyTestSetup wires up the cloak fixture and returns a fully driven
// initial model ready to receive arrow-key input. Each helper that pushes a
// turn through the orchestrator uses runTurnBlocking so the model lands in
// ModeOnPath (i.e. NOT ModeAwaitingLLM) before we start poking arrows.
func historyTestSetup(t *testing.T) tea.Model {
	t.Helper()
	orch, sid := setupCloak(t)
	return buildModel(t, orch, sid)
}

// pressKey sends a single tea.KeyMsg of the given type to the model.
func pressKey(m tea.Model, t tea.KeyType) tea.Model {
	out, _ := m.Update(tea.KeyMsg{Type: t})
	return out
}

// TestHistoryUpRecallsLastSubmission verifies that after one submission,
// pressing Up brings it back into the prompt and a second Up at the oldest
// entry is a no-op (matches bash readline semantics).
func TestHistoryUpRecallsLastSubmission(t *testing.T) {
	m := historyTestSetup(t)

	m = runTurnBlocking(t, m, "look around")

	rm, ok := tuipkg.ExtractRootModel(m)
	require.True(t, ok)
	require.Equal(t, []string{"look around"}, tuipkg.GetInputHistory(rm),
		"submitted line should appear in input history")
	require.Empty(t, tuipkg.GetPromptValue(rm), "prompt should be cleared after submit")

	// Up — recalls "look around".
	m = pressKey(m, tea.KeyUp)
	rm, _ = tuipkg.ExtractRootModel(m)
	require.Equal(t, "look around", tuipkg.GetPromptValue(rm),
		"Up should restore the last submitted line")
	require.True(t, tuipkg.HistoryNavigating(rm),
		"after Up we should be in history-walk mode")

	// Second Up at oldest entry — no-op, prompt unchanged.
	m = pressKey(m, tea.KeyUp)
	rm, _ = tuipkg.ExtractRootModel(m)
	require.Equal(t, "look around", tuipkg.GetPromptValue(rm),
		"second Up at oldest entry should be a no-op")
}

// TestHistoryUpThenDownReturnsToDraft verifies that Down past the newest
// entry restores the empty draft (since we had not typed anything before
// the first Up).
func TestHistoryUpThenDownReturnsToDraft(t *testing.T) {
	m := historyTestSetup(t)

	m = runTurnBlocking(t, m, "look around")

	// Up — recall.
	m = pressKey(m, tea.KeyUp)
	rm, _ := tuipkg.ExtractRootModel(m)
	require.Equal(t, "look around", tuipkg.GetPromptValue(rm))

	// Down — step past newest, restore the empty draft.
	m = pressKey(m, tea.KeyDown)
	rm, _ = tuipkg.ExtractRootModel(m)
	require.Empty(t, tuipkg.GetPromptValue(rm),
		"Down past newest should restore the empty draft")
	require.False(t, tuipkg.HistoryNavigating(rm),
		"after Down past newest we should leave history-walk mode")
}

// TestHistoryTypingThenUpSavesDraft verifies that the in-progress draft is
// stashed when the user first presses Up, and stepping back down past the
// newest entry restores exactly that draft.
func TestHistoryTypingThenUpSavesDraft(t *testing.T) {
	m := historyTestSetup(t)

	m = runTurnBlocking(t, m, "look around")
	m = runTurnBlocking(t, m, "go west")

	// Type a partial draft.
	m, _ = typeString(m, "hang ")
	rm, _ := tuipkg.ExtractRootModel(m)
	require.Equal(t, "hang ", tuipkg.GetPromptValue(rm))

	// Up — should save "hang " and recall "go west".
	m = pressKey(m, tea.KeyUp)
	rm, _ = tuipkg.ExtractRootModel(m)
	require.Equal(t, "go west", tuipkg.GetPromptValue(rm),
		"Up should recall most recent submission")

	// Up again — recall "look around".
	m = pressKey(m, tea.KeyUp)
	rm, _ = tuipkg.ExtractRootModel(m)
	require.Equal(t, "look around", tuipkg.GetPromptValue(rm),
		"second Up should recall older entry")

	// Down — back to "go west".
	m = pressKey(m, tea.KeyDown)
	rm, _ = tuipkg.ExtractRootModel(m)
	require.Equal(t, "go west", tuipkg.GetPromptValue(rm))

	// Down — past newest, restores the saved draft "hang ".
	m = pressKey(m, tea.KeyDown)
	rm, _ = tuipkg.ExtractRootModel(m)
	require.Equal(t, "hang ", tuipkg.GetPromptValue(rm),
		"Down past newest should restore the saved draft")
	require.False(t, tuipkg.HistoryNavigating(rm))
}

// TestHistoryConsecutiveDuplicatesDeduped verifies that submitting the same
// line twice in a row only adds one entry to the history (bash dedupe-only-
// consecutive semantics).
func TestHistoryConsecutiveDuplicatesDeduped(t *testing.T) {
	m := historyTestSetup(t)

	m = runTurnBlocking(t, m, "look around")
	m = runTurnBlocking(t, m, "look around")

	rm, _ := tuipkg.ExtractRootModel(m)
	require.Equal(t, []string{"look around"}, tuipkg.GetInputHistory(rm),
		"consecutive duplicate submissions should collapse to a single entry")

	// A different line, then the original — both should be kept because the
	// duplicate is no longer consecutive.
	m = runTurnBlocking(t, m, "go west")
	m = runTurnBlocking(t, m, "look around")
	rm, _ = tuipkg.ExtractRootModel(m)
	require.Equal(t, []string{"look around", "go west", "look around"},
		tuipkg.GetInputHistory(rm),
		"non-consecutive duplicates should be preserved")
}

// TestHistoryShiftUpStillScrollsTranscript verifies that modified Up keys
// (Shift+Up) continue to drive transcript scrollback rather than walking
// input history — the modifier flag must short-circuit before our plain
// "up" case fires.
func TestHistoryShiftUpStillScrollsTranscript(t *testing.T) {
	m := historyTestSetup(t)

	// Seed a single history entry so plain Up would be observable.
	m = runTurnBlocking(t, m, "look around")

	rm, _ := tuipkg.ExtractRootModel(m)
	require.Empty(t, tuipkg.GetPromptValue(rm))
	require.False(t, tuipkg.HistoryNavigating(rm))

	// Send Shift+Up — should be treated as a scroll key, not a history
	// recall. The prompt must remain empty and we must not enter
	// history-walk mode.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftUp})
	rm, _ = tuipkg.ExtractRootModel(m)
	require.Empty(t, tuipkg.GetPromptValue(rm),
		"Shift+Up must NOT recall history")
	require.False(t, tuipkg.HistoryNavigating(rm),
		"Shift+Up must NOT put the model into history-walk mode")
}

// TestHistoryTypingCommitsNavigation verifies that any regular keystroke
// while walking history leaves history-walk mode without mutating the
// prompt — the textinput.Update call then folds the keystroke into the
// recalled entry as a normal edit.
func TestHistoryTypingCommitsNavigation(t *testing.T) {
	m := historyTestSetup(t)

	m = runTurnBlocking(t, m, "go west")

	// Up — recall "go west".
	m = pressKey(m, tea.KeyUp)
	rm, _ := tuipkg.ExtractRootModel(m)
	require.True(t, tuipkg.HistoryNavigating(rm))
	require.Equal(t, "go west", tuipkg.GetPromptValue(rm))

	// Type "!". This should commit the navigation and append "!" to the
	// recalled text, so the prompt becomes "go west!".
	m, _ = typeString(m, "!")
	rm, _ = tuipkg.ExtractRootModel(m)
	require.False(t, tuipkg.HistoryNavigating(rm),
		"any non-arrow keystroke should exit history-walk mode")
	require.Equal(t, "go west!", tuipkg.GetPromptValue(rm),
		"the new keystroke should be appended to the recalled text")
}

// TestHistoryEmptySubmitNoOp verifies that pressing Enter with an empty
// prompt does NOT add an empty entry to history.
func TestHistoryEmptySubmitNoOp(t *testing.T) {
	m := historyTestSetup(t)

	// Press Enter on an empty prompt. With the cloak fixture the menu has
	// a default selection so this MAY trigger a turn; either way, no
	// "empty" line should land in history.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	// Drain any async work so the model is stable.
	_ = m

	rm, _ := tuipkg.ExtractRootModel(m)
	for _, entry := range tuipkg.GetInputHistory(rm) {
		require.NotEmpty(t, entry, "history must never contain empty strings")
	}
}
