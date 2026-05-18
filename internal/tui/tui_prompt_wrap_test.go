package tui_test

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/stretchr/testify/require"

	tuipkg "kitsoki/internal/tui"
)

// TestPromptTextareaWrapsLongInput is the golden assertion for the
// textinput→textarea swap (proposal §"Input fixes"). At an 80-column
// width, a paragraph longer than the wrap column must produce a prompt
// View() that spans ≥ 2 display rows, with no truncation of any
// constituent word.
func TestPromptTextareaWrapsLongInput(t *testing.T) {
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)

	// Drive resize so the textarea picks up the production wrap width.
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	rm, ok := tuipkg.ExtractRootModel(m)
	require.True(t, ok)

	// 116-character paragraph — comfortably longer than the inner wrap
	// width of 76 at terminal width 80.
	long := "the quick brown fox jumps over the lazy dog while a careful crow watches from the cedar overhanging the fence row"
	require.Greater(t, len(long), 100, "fixture must be > 100 chars")
	tuipkg.SetPromptValue(&rm, long)
	// Resize again to recompute the textarea height from the new value.
	rm = tuipkg.ResizeRootModel(rm, 80, 24)

	view := tuipkg.GetPromptView(rm)
	require.NotEmpty(t, view, "prompt view should not be empty when value is non-empty")

	rows := lipgloss.Height(view)
	require.GreaterOrEqual(t, rows, 2,
		"prompt view should wrap onto ≥ 2 display rows for input longer than the visible area (got %d rows for %d-char input)\nview:\n%s",
		rows, len(long), view)

	// Sanity check: every word from the original input should be
	// present in the rendered view. lipgloss.Height counts newlines,
	// not horizontal clipping, so the only other way long input could
	// regress to a single line is if the textarea silently truncated.
	for _, word := range strings.Fields(long) {
		require.Contains(t, view, word,
			"word %q should appear in the wrapped prompt view — input must not be clipped",
			word)
	}
}

// TestPromptTextareaGrowsHeight verifies that the textarea's reported
// Height grows in response to multi-line content and shrinks back to 1
// when the value clears. This is the user-visible "the input box
// expanded" feedback.
func TestPromptTextareaGrowsHeight(t *testing.T) {
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	rm, _ := tuipkg.ExtractRootModel(m)
	require.Equal(t, 1, tuipkg.GetPromptHeight(rm),
		"empty prompt should have height 1")

	long := strings.Repeat("abcdefghij ", 30) // 330 chars, wraps several times at 76
	tuipkg.SetPromptValue(&rm, long)
	rm = tuipkg.ResizeRootModel(rm, 80, 24)
	require.Greater(t, tuipkg.GetPromptHeight(rm), 1,
		"prompt height should grow > 1 when content wraps")

	tuipkg.SetPromptValue(&rm, "")
	rm = tuipkg.ResizeRootModel(rm, 80, 24)
	require.Equal(t, 1, tuipkg.GetPromptHeight(rm),
		"prompt height should snap back to 1 after the value clears")
}

// TestPromptAltEnterInsertsNewline verifies that Alt+Enter inserts a
// literal newline into the textarea buffer instead of submitting.
// Plain Enter still submits — covered by TestHistoryUpRecallsLastSubmission
// and friends.
func TestPromptAltEnterInsertsNewline(t *testing.T) {
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	// Type "ab", Alt+Enter, then "cd". The resulting prompt value
	// should contain a "\n" between the runs and the model should NOT
	// have submitted (no echo / history entry, still on-path).
	m, _ = typeString(m, "ab")
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	m, _ = typeString(m, "cd")

	rm, _ := tuipkg.ExtractRootModel(m)
	value := tuipkg.GetPromptValue(rm)
	require.Equal(t, "ab\ncd", value,
		"Alt+Enter should split the prompt into two lines without submitting")
	require.Empty(t, tuipkg.GetInputHistory(rm),
		"Alt+Enter must not push the in-progress draft into input history")
	require.Equal(t, tuipkg.ModeOnPath, tuipkg.GetMode(rm),
		"Alt+Enter must not transition the mode")
}

// TestPromptUpInsideWrappedTextMovesCursor verifies that with the
// textarea-based prompt, plain Up moves the cursor up within the
// wrapped buffer when the cursor is NOT on the topmost wrapped row.
// Up at the topmost row still walks input history (covered by
// TestHistoryUpRecallsLastSubmission in tui_history_test.go).
func TestPromptUpInsideWrappedTextMovesCursor(t *testing.T) {
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	// Seed one history entry so Up at the top row WOULD walk history —
	// this makes the "did NOT walk history" assertion meaningful.
	m = runTurnBlocking(t, m, "look around")

	// Compose a two-logical-line value via Alt+Enter so the cursor lands
	// on the second line. Up should now move the cursor back to the
	// first line — leaving the prompt value untouched and NOT recalling
	// "look around" from history.
	m, _ = typeString(m, "first")
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	m, _ = typeString(m, "second")
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})

	rm, _ := tuipkg.ExtractRootModel(m)
	require.Equal(t, "first\nsecond", tuipkg.GetPromptValue(rm),
		"Up on the second line of a multi-line prompt must NOT recall history; "+
			"the prompt value must stay exactly what the user typed")
	require.False(t, tuipkg.HistoryNavigating(rm),
		"Up inside a multi-line prompt should not enter history-walk mode")
}
