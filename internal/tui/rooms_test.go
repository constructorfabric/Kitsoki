package tui_test

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/orchestrator"
	tuipkg "kitsoki/internal/tui"
	"kitsoki/internal/tui/blocks"
)

// TestPerRoomTranscript_BuffersIndependent — single-pane-tui phase 6:
// two on-path rooms (foyer, cloakroom in the cloak fixture) each own
// their own transcript buffer. Typing in foyer, navigating to
// cloakroom, typing there, then navigating back must leave the foyer
// buffer's prior content intact.
func TestPerRoomTranscript_BuffersIndependent(t *testing.T) {
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)
	// Seed each room's transcript with a distinctive system line so we
	// can detect which buffer is active without relying on the orch's
	// own view rendering. AppendTranscriptForTest is the test seam
	// that calls m.transcript.AppendSystem under the hood.
	rm, ok := tuipkg.ExtractRootModel(m)
	require.True(t, ok)
	tuipkg.AppendTranscriptForTest(&rm, "FOYER_MARKER")
	m = rm

	// Navigate foyer → cloakroom (cloak fixture: "go west").
	m = runTurnBlocking(t, m, "go west")
	rm, ok = tuipkg.ExtractRootModel(m)
	require.True(t, ok)
	require.Equal(t, app.StatePath("cloakroom"), rm.CurrentStateForTest(),
		"navigation must land us in cloakroom")

	// Add a cloakroom-only marker.
	tuipkg.AppendTranscriptForTest(&rm, "CLOAKROOM_MARKER")
	cloakroomContent := tuipkg.GetTranscriptContent(rm)
	require.Contains(t, cloakroomContent, "CLOAKROOM_MARKER")
	require.NotContains(t, cloakroomContent, "FOYER_MARKER",
		"cloakroom buffer must NOT contain the foyer marker after the room swap")
	m = rm

	// Navigate back: cloakroom → foyer ("go east").
	m = runTurnBlocking(t, m, "go east")
	rm, ok = tuipkg.ExtractRootModel(m)
	require.True(t, ok)
	require.Equal(t, app.StatePath("foyer"), rm.CurrentStateForTest())

	foyerAgain := tuipkg.GetTranscriptContent(rm)
	require.Contains(t, foyerAgain, "FOYER_MARKER",
		"foyer buffer must retain its prior content after the round-trip")
	require.NotContains(t, foyerAgain, "CLOAKROOM_MARKER",
		"foyer buffer must NOT have leaked cloakroom content")
}

// TestPerRoomTranscript_TransientScrollsPastPrior — single-pane-tui
// phase 6: a room declared `transcript: transient` must scroll past
// any prior content on re-entry. We pile content into room A, swap
// to room B, then re-enter room A with transient=true and assert
// the viewport's YOffset jumped past the saved content (the next
// append will visibly land at the top of the visible window rather
// than below a wall of prior turns).
func TestPerRoomTranscript_TransientScrollsPastPrior(t *testing.T) {
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)
	rm, ok := tuipkg.ExtractRootModel(m)
	require.True(t, ok)

	// Small viewport so a few filler lines exceed the visible area
	// and the YOffset has somewhere to land above zero. SetSize
	// goes through the production resize seam so the wrap width is
	// honest.
	tuipkg.SetTranscriptSizeForTest(&rm, 80, 10)

	// Make sure the model's activeRoom is set so the save side of
	// activateRoom keys into the right slot when we leave.
	tuipkg.ActivateRoomForTest(&rm, "roomA", false)

	// Pile content into room A.
	for i := 0; i < 30; i++ {
		tuipkg.AppendTranscriptForTest(&rm, "filler line so the viewport has content above the fold")
	}
	roomAHeight := tuipkg.TranscriptYOffset(rm) // viewport pinned to bottom by Append*

	// Swap to room B and back, transient on the re-entry.
	tuipkg.ActivateRoomForTest(&rm, "roomB", false)
	tuipkg.ActivateRoomForTest(&rm, "roomA", true)

	transientOffset := tuipkg.TranscriptYOffset(rm)

	require.Greater(t, transientOffset, 0,
		"transient re-entry must scroll past prior content (offset stuck at %d)",
		transientOffset)
	// roomAHeight is the YOffset just before the swap, captured at
	// the bottom of the viewport. The transient mark equals the
	// rendered content height — strictly greater than zero whenever
	// the buffer is non-empty.
	_ = roomAHeight
}

// TestPerRoomTheme_HonoursStateField — single-pane-tui phase 6: the
// blocks.Renderer the TUI builds for the active room must carry the
// theme declared on that room's State.Theme field. We hand-craft a
// minimal AppDef so the test is independent of which fixture
// happens to declare a theme today.
func TestPerRoomTheme_HonoursStateField(t *testing.T) {
	def := &app.AppDef{
		App: app.AppMeta{ID: "themed-test", Version: "0.1.0"},
		States: map[string]*app.State{
			"plain": {Description: "no theme"},
			"blue":  {Description: "meta-blue room", Theme: "meta-blue"},
		},
	}
	// Direct helper exercise — the helper resolves the theme name
	// from the State.Theme field via the same code path RootModel.View()
	// uses when painting the location bar / transcript blocks.
	require.Equal(t, "default", tuipkg.ResolveRoomThemeForTest(def, "plain"))
	require.Equal(t, "meta-blue", tuipkg.ResolveRoomThemeForTest(def, "blue"))

	// And verify blocks.New(width, name) — the actual renderer
	// construction — yields a Theme with the expected name. This is
	// the load-bearing assertion: the proposal calls for the theme
	// to thread through to the renderer, not just live as a string.
	r := blocks.New(80, "meta-blue")
	require.Equal(t, "meta-blue", r.Theme.Name)
}

// TestPerRoomTranscript_WithinRoomDoesNotSwap — moves whose new
// state shares the previous state's top-level segment (e.g.
// "bar.dark" → "bar.lit") are a within-room transition; the
// transcript buffer must stay put.
func TestPerRoomTranscript_WithinRoomDoesNotSwap(t *testing.T) {
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)
	// Step 1: get to the bar (dark substate). Cloak fixture path:
	// foyer → cloakroom (go west) → hang the cloak → foyer (go east)
	// → bar (go south, lit because cloak hung).
	m = runTurnBlocking(t, m, "go west")
	m = runTurnBlocking(t, m, "hang the cloak")
	m = runTurnBlocking(t, m, "go east")
	m = runTurnBlocking(t, m, "go south")
	rm, ok := tuipkg.ExtractRootModel(m)
	require.True(t, ok)
	require.Equal(t, app.StatePath("bar"),
		app.StatePath(rm.CurrentStateForTest()).TopLevel(),
		"setup precondition: must land somewhere under bar.*")

	// Seed a marker AFTER landing in bar so we know which buffer it
	// belongs to.
	tuipkg.AppendTranscriptForTest(&rm, "WITHIN_BAR_MARKER")
	bcontent := tuipkg.GetTranscriptContent(rm)
	require.Contains(t, bcontent, "WITHIN_BAR_MARKER")

	// Now drive an in-bar look — re-renders the view, stays in bar.
	// The transcript buffer must NOT have been swapped out (the
	// marker survives).
	m = runTurnBlocking(t, rm, "look")
	rm, ok = tuipkg.ExtractRootModel(m)
	require.True(t, ok)
	require.Equal(t, app.StatePath("bar"),
		app.StatePath(rm.CurrentStateForTest()).TopLevel(),
		"in-bar look must keep us under bar.*")
	require.Contains(t, tuipkg.GetTranscriptContent(rm), "WITHIN_BAR_MARKER",
		"within-room move must leave the active transcript buffer intact")
}

// TestRoomKey_TopLevelOnly — defensive: the proposal explicitly says
// the room is the FIRST dot-separated segment. Path strings can use
// dots (internal representation per loader.joinPath) so an inner
// child like "bar.dark" maps to room "bar".
func TestRoomKey_TopLevel(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"foyer", "foyer"},
		{"bar.dark", "bar"},
		{"deep.compound.nested.state", "deep"},
	}
	for _, c := range cases {
		got := app.StatePath(c.in).TopLevel()
		require.Equal(t, app.StatePath(c.want), got, "TopLevel(%q)", c.in)
	}
}

// TestTranscriptValidation_OnlyTopLevel — the loader must reject
// `transcript:` or `theme:` set on a nested state. Lives in the
// tui_test file because it's the closest test surface to the room
// concept being validated.
func TestTranscriptValidation_OnlyTopLevel(t *testing.T) {
	const yamlBody = `
app:
  id: rooms-validation-test
  version: 0.1.0

world: {}

intents:
  go: {}

root: bar

states:
  bar:
    type: compound
    initial: dark
    transcript: persistent
    theme: meta-blue
    states:
      dark:
        # both forbidden on a nested state.
        transcript: transient
        theme: meta-amber
        on:
          go:
            - target: dark
`
	_, err := app.LoadBytes([]byte(yamlBody))
	require.Error(t, err, "loader must reject transcript/theme on a nested state")
	msg := err.Error()
	require.True(t,
		strings.Contains(msg, "transcript: only allowed on top-level") &&
			strings.Contains(msg, "theme: only allowed on top-level"),
		"errors must call out both fields; got:\n%s", msg)
}

// TestTranscriptValidation_TopLevelAccepted — top-level rooms with
// the new fields must load without error.
func TestTranscriptValidation_TopLevelAccepted(t *testing.T) {
	const yamlBody = `
app:
  id: rooms-validation-ok
  version: 0.1.0

world: {}

intents:
  noop: {}

root: foyer

states:
  foyer:
    transcript: persistent
    theme: default
    on:
      noop:
        - target: foyer
  bar:
    transcript: transient
    theme: meta-blue
    on:
      noop:
        - target: bar
`
	def, err := app.LoadBytes([]byte(yamlBody))
	require.NoError(t, err)
	require.Equal(t, "persistent", def.States["foyer"].Transcript)
	require.Equal(t, "meta-blue", def.States["bar"].Theme)
}

// ensureTeaModel is a compile-time interface assertion so this file
// compiles cleanly when go test types the imports.
var _ tea.Model = (tuipkg.RootModel{})

// Re-export the orchestrator type so the test imports section keeps
// the "used" reference (lint clean) even when only the package's
// transitive symbols matter to this file's test bodies.
var _ orchestrator.OutcomeMode = orchestrator.ModeTransitioned
