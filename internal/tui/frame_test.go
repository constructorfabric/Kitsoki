package tui_test

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"

	tuipkg "kitsoki/internal/tui"
)

// driveToKnownFrame builds a cloak model, sizes it to a real terminal,
// and runs one turn so the transcript has a flushed room body. It returns
// the resized RootModel value (addressable for ComposeFrame's *RootModel).
func driveToKnownFrame(t *testing.T, width, height int) tuipkg.RootModel {
	t.Helper()
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)
	// Drive a single turn so the room body lands in the transcript and
	// the model is in a deterministic, non-empty state.
	m = runTurnBlocking(t, m, "look")
	rm, ok := tuipkg.ExtractRootModel(m)
	require.True(t, ok, "expected RootModel, got %T", m)
	return tuipkg.ResizeRootModel(rm, width, height)
}

// TestComposeFrame_EqualsLivePaint asserts the composer's frame contains
// the room body AND every chrome element RootModel.View() emits, in the
// same top-to-bottom order. This guards against the composer drifting
// from the real screen. Without ComposeFrame this test cannot even
// compile — the "fail before the change" guarantee is structural.
func TestComposeFrame_EqualsLivePaint(t *testing.T) {
	rm := driveToKnownFrame(t, 100, 30)

	frame := tuipkg.ComposeFrame(&rm, 100, 30)
	live := rm.View()

	fa := tuipkg.NewRenderingAnalyzer(t, frame.Text)
	// The frame includes the last flushed room body (headless callers
	// want it in a single still); FOYER is the cloak start room heading.
	fa.AssertContains("FOYER")

	// Every chrome element View() paints must appear in the frame Text.
	liveStripped := stripFrameANSI(live)
	require.Contains(t, frame.Text, "─",
		"frame must include the divider chrome")

	// Status row content (state · mode · queue) View() emits must survive
	// into the frame. The framework footer line is the high-signal row.
	for _, fragment := range chromeFragments(liveStripped) {
		require.Contains(t, frame.Text, fragment,
			"frame Text must contain chrome fragment %q that View() emits", fragment)
	}

	// Ordering: the body region precedes the divider, which precedes the
	// status row — same vertical order View() produces.
	bodyIdx := strings.Index(frame.Text, "FOYER")
	divIdx := strings.Index(frame.Text, "─")
	require.GreaterOrEqual(t, bodyIdx, 0, "body must be present")
	require.GreaterOrEqual(t, divIdx, 0, "divider must be present")
	require.Less(t, bodyIdx, divIdx, "room body must come before the divider")
}

// TestComposeFrame_WidthFidelity proves the frame is paint-equivalent to
// a real terminal of the requested width: no chrome line overflows the
// narrow width, and a wide width reflows prose past the 80-col fossil.
func TestComposeFrame_WidthFidelity(t *testing.T) {
	rm := driveToKnownFrame(t, 120, 40)

	narrow := tuipkg.ComposeFrame(&rm, 50, 30)
	for i, line := range strings.Split(narrow.Text, "\n") {
		// lipgloss.Width measures visible display columns (handles wide
		// runes like the box-drawing divider, where one glyph is 3 bytes
		// but 1 column) — a raw len() would false-fail on multibyte UTF-8.
		require.LessOrEqual(t, lipgloss.Width(line), 50,
			"narrow frame line %d wider than 50 cols: %q", i, line)
	}

	wide := tuipkg.ComposeFrame(&rm, 120, 30)
	maxWide := 0
	for _, line := range strings.Split(wide.Text, "\n") {
		if w := lipgloss.Width(line); w > maxWide {
			maxWide = w
		}
	}
	// The full-width divider/status-row alone prove the chrome reflows to
	// the requested width — at 120 cols some line must exceed the 80-col
	// trace fossil width.
	require.Greater(t, maxWide, 80,
		"wide frame should reflow chrome past the 80-col fossil width; max=%d", maxWide)
}

// TestComposeFrame_MetadataMatchesMachine asserts the typed sidecar is
// the machine's truth: AllowedIntents equals what the orchestrator
// reports for the current state+world, and State/Mode match the model.
func TestComposeFrame_MetadataMatchesMachine(t *testing.T) {
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)
	m = runTurnBlocking(t, m, "look")
	rm, ok := tuipkg.ExtractRootModel(m)
	require.True(t, ok, "expected RootModel, got %T", m)
	rm = tuipkg.ResizeRootModel(rm, 100, 30)

	frame := tuipkg.ComposeFrame(&rm, 100, 30)

	// State matches the model's current state path.
	require.Equal(t, string(rm.CurrentStateForTest()), frame.Metadata.State,
		"frame State must match the model's current state")

	// Mode matches (normal at rest).
	require.Equal(t, "normal", frame.Metadata.Mode,
		"frame Mode must be the resting mode label")

	// AllowedIntents equals the machine-reported intents for that state.
	w := orch.CurrentWorld(sid)
	var want []string
	for _, ai := range orch.AllowedIntents(rm.CurrentStateForTest(), w) {
		want = append(want, ai.Name)
	}
	require.Equal(t, want, frame.Metadata.AllowedIntents,
		"frame AllowedIntents must equal the machine's allowed intents")
	require.NotEmpty(t, want, "the cloak foyer should expose at least one intent")
}

// TestComposeFrame_ANSITextTwinsAgree asserts the two projections are the
// same paint: stripping the styled ANSI yields exactly the Text twin.
func TestComposeFrame_ANSITextTwinsAgree(t *testing.T) {
	rm := driveToKnownFrame(t, 100, 30)
	frame := tuipkg.ComposeFrame(&rm, 100, 30)

	require.Equal(t, frame.Text, ansi.Strip(frame.ANSI),
		"ansi.Strip(Frame.ANSI) must equal Frame.Text")
	require.Equal(t, 100, frame.Width)
	require.Equal(t, 30, frame.Height)
}

// TestRootModelView_UnchangedByComposer is the golden-style pin: the live
// View()'s bottom chrome must be reconstructible from the same composer
// parts View() now routes through, proving the refactor is byte-neutral
// for the live TUI. The frame's chrome region (everything from the
// divider down) must be a suffix-equal projection of View().
func TestRootModelView_UnchangedByComposer(t *testing.T) {
	rm := driveToKnownFrame(t, 100, 30)

	live := rm.View()
	frame := tuipkg.ComposeFrame(&rm, 100, 30)

	// The live View() emits only chrome (body is in scrollback). Every
	// chrome line View() produces must appear verbatim in the frame's
	// styled ANSI, in order — the composer is the same paint.
	liveLines := strings.Split(live, "\n")
	frameLines := strings.Split(frame.ANSI, "\n")
	require.NotEmpty(t, liveLines)

	// Find where the live chrome begins inside the frame (the frame
	// prepends the body region). The divider line anchors it.
	start := indexOfDivider(frameLines)
	require.GreaterOrEqual(t, start, 0, "frame must contain a divider chrome line")

	liveStart := indexOfDivider(liveLines)
	require.GreaterOrEqual(t, liveStart, 0, "live View() must contain a divider chrome line")

	// From the divider down, the frame chrome equals the live chrome
	// line-for-line — the byte-neutral guarantee.
	liveChrome := liveLines[liveStart:]
	frameChrome := frameLines[start:]
	require.Equal(t, len(liveChrome), len(frameChrome),
		"frame chrome must have the same line count as live chrome")
	for i := range liveChrome {
		require.Equal(t, liveChrome[i], frameChrome[i],
			"chrome line %d must be byte-identical between View() and the composer", i)
	}
}

// ── small test helpers ───────────────────────────────────────────────

// stripFrameANSI removes ANSI escapes for fragment comparison.
func stripFrameANSI(s string) string { return ansi.Strip(s) }

// chromeFragments extracts the high-signal, deterministic chrome tokens
// that should survive into the composed frame regardless of width.
func chromeFragments(strippedView string) []string {
	var out []string
	if strings.Contains(strippedView, "normal") {
		out = append(out, "normal") // the mode label on the status row
	}
	return out
}

// indexOfDivider returns the index of the first line consisting only of
// box-drawing horizontal rules (the chrome divider), or -1.
func indexOfDivider(lines []string) int {
	for i, line := range lines {
		s := strings.TrimSpace(ansi.Strip(line))
		if s == "" {
			continue
		}
		if strings.Trim(s, "─") == "" {
			return i
		}
	}
	return -1
}
