package tui_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	tuipkg "kitsoki/internal/tui"
)

// TestFrame_DiscoverabilityHintRenders gates fix #1 (status-row half):
// the persistent "? help · Esc menu" cue must appear in every composed
// frame so a first-run user isn't stranded once the welcome banner
// scrolls off.
func TestFrame_DiscoverabilityHintRenders(t *testing.T) {
	rm := driveToKnownFrame(t, 100, 30)
	frame := tuipkg.ComposeFrame(&rm, 100, 30)
	require.Contains(t, frame.Text, "? help · Esc menu",
		"composed frame must re-advertise /help and the Esc menu")
}

// TestEscMenu_HasHelpAndWorldRows gates fix #1 (menu half): the Esc menu
// for a plain app (no meta modes) must offer more than just "Exit" — it
// surfaces Help and World rows for discoverability.
func TestEscMenu_HasHelpAndWorldRows(t *testing.T) {
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)
	rm, ok := tuipkg.ExtractRootModel(m)
	require.True(t, ok)

	// Open the Esc menu.
	tuipkg.SetModeForTest(&rm, tuipkg.ModeMenu)
	tuipkg.OpenMenuSystemForTest(&rm)

	view := tuipkg.MenuSystemView(rm)
	require.Contains(t, view, "Exit", "Esc menu must keep the Exit row")
	require.Contains(t, view, "Help", "Esc menu must surface a Help row")
	require.Contains(t, view, "World", "Esc menu must surface a World row")
	require.Contains(t, view, "/help", "Help row hint should mention /help")
	require.Contains(t, view, "/world", "World row hint should mention /world")
}

// TestFrame_MetaCancelCopyCrossListsKeys gates fix #2: the meta-mode
// in-flight caption must cross-list Ctrl+C (matching the awaiting-LLM
// caption) rather than advertising only Esc — both keys cancel a
// meta turn, so the copy shouldn't surprise the user with a single one.
func TestFrame_MetaCancelCopyCrossListsKeys(t *testing.T) {
	rm := driveToKnownFrame(t, 100, 30)
	rm = tuipkg.SimulateMetaTurnInFlight(rm)

	frame := tuipkg.ComposeFrame(&rm, 100, 30)
	require.Contains(t, frame.Text, "Ctrl+C or Esc to cancel",
		"meta in-flight caption must cross-list both cancel keys; got:\n%s", frame.Text)
	// And it must not regress to the old Esc-only copy.
	require.NotContains(t, frame.Text, "thinking… (Esc to cancel)",
		"meta caption must not advertise Esc as the only cancel key")
}

// TestTranscriptQueue_NoIndicatorLeak gates fix #4: the live spinner /
// queue indicator (⏳) belongs to the View() bottom region only and must
// never reach the scrollback pending queue. This replaces the old
// live-path slog.Warn("BUG: …") with a real assertion.
func TestTranscriptQueue_NoIndicatorLeak(t *testing.T) {
	rm := driveToKnownFrame(t, 100, 30)
	rm = tuipkg.SimulateSlowHarnessTurnStart(rm)

	// The indicator must be in the live View() bottom region…
	frame := tuipkg.ComposeFrame(&rm, 100, 30)
	require.Contains(t, frame.Text, "⏳",
		"the in-flight spinner indicator should render in the live frame")

	// …but never queued to scrollback.
	for _, body := range tuipkg.PendingTranscriptForTest(rm) {
		require.NotContains(t, body, "⏳",
			"the spinner indicator must never leak into the scrollback queue")
	}
}

// TestPromptPlaceholder_SignalsNLAndHelp gates fix #5: the resting prompt
// placeholder must signal that free-text is allowed and advertise /help,
// rather than the opaque "what now?".
func TestPromptPlaceholder_SignalsNLAndHelp(t *testing.T) {
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)
	rm, ok := tuipkg.ExtractRootModel(m)
	require.True(t, ok)

	ph := tuipkg.PromptPlaceholderForTest(rm)
	require.Truef(t, strings.Contains(ph, "/help"),
		"prompt placeholder should advertise /help; got %q", ph)
	require.Truef(t, strings.Contains(ph, "describe what you want"),
		"prompt placeholder should signal free-text typing; got %q", ph)
	require.NotEqual(t, "what now?", ph,
		"prompt placeholder should not be the opaque 'what now?'")
}
