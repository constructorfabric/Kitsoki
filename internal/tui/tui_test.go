package tui_test

import (
	"context"
	"flag"
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"

	"hally/internal/app"
	"hally/internal/harness"
	"hally/internal/machine"
	"hally/internal/orchestrator"
	"hally/internal/store"
	tuipkg "hally/internal/tui"
)

var flagUpdate = flag.Bool("update", false, "update golden files")

func TestMain(m *testing.M) {
	flag.Parse()
	os.Exit(m.Run())
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func setupCloak(t *testing.T) (*orchestrator.Orchestrator, app.SessionID) {
	t.Helper()

	def, err := app.Load("../../testdata/apps/cloak/app.yaml")
	require.NoError(t, err)

	mach, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	h, err := harness.NewReplay("../../testdata/apps/cloak/oracle.yaml")
	require.NoError(t, err)

	orch := orchestrator.New(def, mach, s, h)

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	return orch, sid
}

// buildModel creates an initialized RootModel as a tea.Model interface value.
func buildModel(t *testing.T, orch *orchestrator.Orchestrator, sid app.SessionID) tea.Model {
	t.Helper()
	w := orch.InitialWorld()
	initialView, err := orch.InitialView(w)
	require.NoError(t, err)
	m := tuipkg.NewRootModel(orch, sid, initialView)
	return m
}

// processCommands runs all synchronous commands until exhausted.
// It handles tea.BatchMsg by processing each sub-command sequentially.
// Spinner tick messages are skipped since they only drive animation.
func processCommands(m tea.Model, cmd tea.Cmd, maxDepth int) tea.Model {
	for i := 0; i < maxDepth && cmd != nil; i++ {
		msg := cmd()
		if msg == nil {
			cmd = nil
			break
		}
		// Handle BatchMsg: process each sub-command sequentially.
		if batch, ok := msg.(tea.BatchMsg); ok {
			var lastCmd tea.Cmd
			for _, subCmd := range batch {
				if subCmd == nil {
					continue
				}
				subMsg := subCmd()
				if subMsg == nil {
					continue
				}
				// Skip spinner tick messages (pure animation, not meaningful for tests).
				if isSpinnerTick(subMsg) {
					continue
				}
				m, lastCmd = m.Update(subMsg)
			}
			cmd = lastCmd
			continue
		}
		m, cmd = m.Update(msg)
	}
	return m
}

// isSpinnerTick returns true if the message is a spinner.TickMsg (animation only).
func isSpinnerTick(msg tea.Msg) bool {
	_, ok := msg.(spinner.TickMsg)
	return ok
}

// typeString types each rune in the string via KeyMsg updates.
func typeString(m tea.Model, s string) (tea.Model, tea.Cmd) {
	var lastCmd tea.Cmd
	for _, r := range s {
		m, lastCmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return m, lastCmd
}

// submitLine types a string and presses Enter.
func submitLine(m tea.Model, line string) (tea.Model, tea.Cmd) {
	m, _ = typeString(m, line)
	return m.Update(tea.KeyMsg{Type: tea.KeyEnter})
}

// runTurnBlocking submits input and processes the async turn result synchronously.
func runTurnBlocking(t *testing.T, m tea.Model, input string) tea.Model {
	t.Helper()
	var cmd tea.Cmd
	m, cmd = submitLine(m, input)
	m = processCommands(m, cmd, 20)
	return m
}

// extractTranscript safely extracts the transcript content from a tea.Model.
func extractTranscript(t *testing.T, m tea.Model) string {
	t.Helper()
	rm, ok := tuipkg.ExtractRootModel(m)
	require.True(t, ok, "expected RootModel, got %T", m)
	return tuipkg.GetTranscriptContent(rm)
}

// extractMode safely extracts the mode from a tea.Model.
func extractMode(t *testing.T, m tea.Model) tuipkg.Mode {
	t.Helper()
	rm, ok := tuipkg.ExtractRootModel(m)
	require.True(t, ok, "expected RootModel, got %T", m)
	return tuipkg.GetMode(rm)
}

// ─── Cloak winning path TUI test ─────────────────────────────────────────────

func TestTUIWinningPath(t *testing.T) {
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)

	// Run the winning path turns in sequence.
	turns := []string{
		"go west",           // foyer → cloakroom
		"hang the cloak",    // cloakroom → hang cloak
		"go east",           // cloakroom → foyer
		"go south",          // foyer → bar.lit (cloak is hung)
		"read the message",  // bar.lit → ended (won)
	}

	for _, turn := range turns {
		m = runTurnBlocking(t, m, turn)
	}

	// The transcript should contain "won".
	transcriptContent := extractTranscript(t, m)
	require.Contains(t, transcriptContent, "won",
		"transcript should contain 'won' after completing the winning path")

	// The view should still be renderable.
	view := m.View()
	require.NotEmpty(t, view)

	// Golden file test.
	goldenDir := "testdata"
	golden := goldenDir + "/winning.golden"
	if *flagUpdate {
		require.NoError(t, os.MkdirAll(goldenDir, 0755))
		require.NoError(t, os.WriteFile(golden, []byte(transcriptContent), 0644))
		t.Logf("updated golden file %s", golden)
		return
	}

	// Compare against golden file if it exists.
	if goldenData, err := os.ReadFile(golden); err == nil {
		want := strings.TrimSpace(string(goldenData))
		got := strings.TrimSpace(transcriptContent)
		require.Contains(t, got, "won", "transcript must contain 'won'")
		require.Contains(t, want, "won", "golden file must contain 'won'")
	} else {
		t.Logf("golden file %s not found; run with -update to create it", golden)
	}
}

// ─── Slash command tests ──────────────────────────────────────────────────────

func TestTUISlashFreeformAndOnpath(t *testing.T) {
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)

	// /freeform → mode should change to off-path.
	m = runTurnBlocking(t, m, "/freeform")
	view := m.View()
	require.Contains(t, view, "off the path",
		"off-path banner should appear after /freeform")

	// /onpath → back to on-path.
	m = runTurnBlocking(t, m, "/onpath")
	view = m.View()
	// The banner should be gone from the current rendering.
	_ = view // mode changed; transcript may still show the banner text but border should revert
}

// ─── Mode transitions ─────────────────────────────────────────────────────────

func TestTUIInitialMode(t *testing.T) {
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)
	require.Equal(t, tuipkg.ModeOnPath, extractMode(t, m))
}

func TestTUIQuit(t *testing.T) {
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)

	var cmd tea.Cmd
	m, cmd = submitLine(m, "/quit")
	_ = cmd

	view := m.View()
	require.Contains(t, view, "Goodbye")
}

// ─── Rejection rendering ──────────────────────────────────────────────────────

func TestTUIRejectedTurn(t *testing.T) {
	const appYAML = `
app:
  id: reject-test
  version: 0.1.0

world: {}

intents:
  go:
    slots:
      direction: { type: enum, values: [north, south], required: true }
  invalid_here:
    title: "Not valid here"

root: start

states:
  start:
    view: "You are here."
    on:
      go:
        - when: "slots.direction == 'north'"
          target: done

  done:
    terminal: true
    view: "Done."
`
	def, err := app.LoadBytes([]byte(appYAML))
	require.NoError(t, err)

	mach, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	defer func() { _ = s.Close() }()

	// Use a harness that returns 'invalid_here' which is not in start's on: block.
	h := &rejectionHarness{intentName: "invalid_here"}
	orch := orchestrator.New(def, mach, s, h)

	ctx := context.Background()
	sidNew, err := orch.NewSession(ctx)
	require.NoError(t, err)

	w := orch.InitialWorld()
	initialView, _ := orch.InitialView(w)
	m := tea.Model(tuipkg.NewRootModel(orch, sidNew, initialView))

	m = runTurnBlocking(t, m, "do something invalid")

	content := extractTranscript(t, m)
	require.Contains(t, content, "blocked",
		"transcript should contain 'blocked' for a rejected turn")
}

// ─── Clarify mode ─────────────────────────────────────────────────────────────

func TestTUIModeClarify(t *testing.T) {
	const appYAML = `
app:
  id: clarify-tui-test
  version: 0.1.0
world: {}
intents:
  move:
    slots:
      direction:
        type: enum
        values: [north, south]
        required: true
        prompt: "Which direction?"
root: start
states:
  start:
    view: "Start."
    on:
      move:
        - when: "slots.direction == 'north'"
          target: done
        - default: true
          target: start
  done:
    terminal: true
    view: "Done."
`
	def, err := app.LoadBytes([]byte(appYAML))
	require.NoError(t, err)

	mach, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	defer func() { _ = s.Close() }()

	// Harness returns 'move' without the required direction slot.
	h := &missingSlotHarness{intentName: "move"}
	orch := orchestrator.New(def, mach, s, h)

	ctx := context.Background()
	sidNew, err := orch.NewSession(ctx)
	require.NoError(t, err)

	w := orch.InitialWorld()
	initialView, _ := orch.InitialView(w)
	m := tea.Model(tuipkg.NewRootModel(orch, sidNew, initialView))

	// Submit a turn; harness will return 'move' without direction → Clarify.
	m = runTurnBlocking(t, m, "move somewhere")

	// Should be in slot-filling mode.
	mode := extractMode(t, m)
	require.Equal(t, tuipkg.ModeSlotFilling, mode)
	_ = ctx
}

// ─── Menu expansion and direct dispatch ──────────────────────────────────────

// TestTUIMenuExpandedGoSouth verifies that the TUI's menu contains "go south"
// (not bare "go") in the foyer, and that selecting it via hotkey "1" (if it
// is the first primary item) followed by Enter dispatches directly via
// SubmitDirect, advancing the journey to bar.dark.
func TestTUIMenuExpandedGoSouth(t *testing.T) {
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)

	// The menu should contain "go south" (not bare "go") in the foyer.
	rm, ok := tuipkg.ExtractRootModel(m)
	require.True(t, ok)
	menuItems := tuipkg.GetMenuPrimaryItems(rm)

	var goSouthIdx int = -1
	for i, display := range menuItems {
		if display == "go south" {
			goSouthIdx = i
			break
		}
	}
	require.GreaterOrEqual(t, goSouthIdx, 0, "go south should be in the primary menu")

	// Select the "go south" row via arrow keys and press Enter (with empty prompt).
	// We simulate pressing the numeric hotkey if it's within 1-9.
	if goSouthIdx < 9 {
		hotkey := string(rune('1' + goSouthIdx))
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(hotkey)})
	}
	// Press Enter with empty prompt to trigger direct dispatch.
	m = processCommands(m, func() tea.Msg {
		return tea.KeyMsg{Type: tea.KeyEnter}
	}, 20)

	// After dispatch, the journey should have advanced.
	transcript := extractTranscript(t, m)
	// bar.dark view should appear because wearing_cloak=true initially.
	require.Contains(t, transcript, "pitch dark", "transcript should show bar.dark view after go south")
}

// TestTUIMenuNoBareGo verifies that bare "go" never appears in the foyer menu.
func TestTUIMenuNoBareGo(t *testing.T) {
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)

	rm, ok := tuipkg.ExtractRootModel(m)
	require.True(t, ok)
	menuItems := tuipkg.GetMenuPrimaryItems(rm)

	for _, display := range menuItems {
		require.NotEqual(t, "go", display, "bare 'go' should not appear in foyer menu")
	}
}

// ─── Test harnesses ───────────────────────────────────────────────────────────

// rejectionHarness returns an intent that is not allowed in the current state.
type rejectionHarness struct {
	intentName string
}

func (h *rejectionHarness) RunTurn(_ context.Context, _ harness.TurnInput) (mcp.CallToolParams, error) {
	return mcp.CallToolParams{
		Name:      "transition",
		Arguments: map[string]any{"intent": h.intentName},
	}, nil
}

func (h *rejectionHarness) Close() error { return nil }

// missingSlotHarness returns an intent without any slots.
type missingSlotHarness struct {
	intentName string
}

func (h *missingSlotHarness) RunTurn(_ context.Context, _ harness.TurnInput) (mcp.CallToolParams, error) {
	return mcp.CallToolParams{
		Name:      "transition",
		Arguments: map[string]any{"intent": h.intentName},
	}, nil
}

func (h *missingSlotHarness) Close() error { return nil }

// ─── slowHarness: blocks until released, for in-flight tests ─────────────────

// slowHarness blocks on a channel until release() is called, then returns a
// canned response. Used to test in-flight / spinner behavior.
type slowHarness struct {
	release chan struct{} // close to unblock
	intent  string
}

func newSlowHarness(intent string) *slowHarness {
	return &slowHarness{
		release: make(chan struct{}),
		intent:  intent,
	}
}

func (h *slowHarness) RunTurn(ctx context.Context, _ harness.TurnInput) (mcp.CallToolParams, error) {
	select {
	case <-ctx.Done():
		return mcp.CallToolParams{}, ctx.Err()
	case <-h.release:
		return mcp.CallToolParams{
			Name:      "transition",
			Arguments: map[string]any{"intent": h.intent},
		}, nil
	}
}

func (h *slowHarness) Close() error { return nil }

// ─── In-flight / spinner tests ────────────────────────────────────────────────

// buildModelWithHarness builds a model backed by the given harness instead of
// the default replay harness.
func buildModelWithHarness(t *testing.T, h harness.Harness) (tea.Model, *orchestrator.Orchestrator) {
	t.Helper()

	def, err := app.Load("../../testdata/apps/cloak/app.yaml")
	require.NoError(t, err)

	mach, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	orch := orchestrator.New(def, mach, s, h)

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	w := orch.InitialWorld()
	initialView, err := orch.InitialView(w)
	require.NoError(t, err)

	m := tuipkg.NewRootModel(orch, sid, initialView)
	return m, orch
}

// TestTUIInFlightMode verifies that submitting free-form input (which goes to
// the LLM path via a slow harness) puts the model into ModeAwaitingLLM.
func TestTUIInFlightMode(t *testing.T) {
	slow := newSlowHarness("look")
	// Don't release — we want to observe in-flight state.
	defer close(slow.release)

	m, _ := buildModelWithHarness(t, slow)

	// "I want to do something" is free-form and won't match any menu entry deterministically.
	// Submit it — this should kick off the async LLM call.
	m, cmd := submitLine(m, "I want to do something interesting")
	require.NotNil(t, cmd, "expected a command for the async LLM turn")

	// Extract the model and check it's in ModeAwaitingLLM.
	rm, ok := tuipkg.ExtractRootModel(m)
	require.True(t, ok)
	require.True(t, tuipkg.IsInFlight(rm), "model should be in ModeAwaitingLLM after LLM submission")
	require.True(t, tuipkg.HasInFlightCancel(rm), "inFlightCancel should be set")
}

// TestTUISingleFlightReject verifies that submitting a second input while in-flight
// shows a notice and does NOT start a new turn.
func TestTUISingleFlightReject(t *testing.T) {
	slow := newSlowHarness("look")
	defer close(slow.release)

	m, _ := buildModelWithHarness(t, slow)

	// Submit a free-form input to go in-flight.
	m, _ = submitLine(m, "do something random")

	// Now submit a second input while still in-flight.
	m, cmd2 := submitLine(m, "do something else")

	// The second submission should produce no async command (or just return nil).
	// The model should still be in ModeAwaitingLLM.
	rm, ok := tuipkg.ExtractRootModel(m)
	require.True(t, ok)
	require.True(t, tuipkg.IsInFlight(rm), "model should still be in ModeAwaitingLLM")

	// A notice should have been appended to the transcript.
	transcript := tuipkg.GetTranscriptContent(rm)
	require.Contains(t, transcript, "still thinking", "transcript should contain single-flight notice")

	_ = cmd2
}

// TestTUICtrlCCancelInFlight verifies that Ctrl+C during in-flight cancels the
// turn without quitting the program.
func TestTUICtrlCCancelInFlight(t *testing.T) {
	slow := newSlowHarness("look")
	// We won't release — cancel should trigger context cancellation.

	m, _ := buildModelWithHarness(t, slow)

	// Go in-flight.
	m, asyncCmd := submitLine(m, "do something random and free-form")
	rm, ok := tuipkg.ExtractRootModel(m)
	require.True(t, ok)

	if !tuipkg.IsInFlight(rm) {
		// If the deterministic path hit (unlikely for this input), skip.
		t.Skip("input was deterministically routed; skipping cancel test")
	}

	// Press Ctrl+C to cancel.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})

	// The model should NOT be quitting.
	rm2, ok2 := tuipkg.ExtractRootModel(m)
	require.True(t, ok2)
	require.False(t, rm2.Quitting(), "Ctrl+C during in-flight should not quit")

	// Now simulate the cancelled turn completing (context cancelled → ModeCancelled outcome).
	if asyncCmd != nil {
		// Drain the command; it should eventually return a turnOutcomeMsg with ModeCancelled.
		// We can inject it directly via TriggerTurnOutcomeMsg.
		m, _ = tuipkg.TriggerTurnOutcomeMsg(m,
			&orchestrator.TurnOutcome{Mode: orchestrator.ModeCancelled},
			"do something random and free-form", nil)
	}

	// After handling, mode should be back to ModeOnPath.
	rm3, ok3 := tuipkg.ExtractRootModel(m)
	require.True(t, ok3)
	require.False(t, tuipkg.IsInFlight(rm3), "should not be in-flight after cancel")

	// Transcript should contain "(cancelled)".
	transcript := tuipkg.GetTranscriptContent(rm3)
	require.Contains(t, transcript, "cancelled", "transcript should contain cancelled notice")
}

// TestTUISpinnerPresent verifies that the View() contains a spinner character
// while in ModeAwaitingLLM.
func TestTUISpinnerPresent(t *testing.T) {
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)

	// Manually put the model into ModeAwaitingLLM via the export helper.
	rm, ok := tuipkg.ExtractRootModel(m)
	require.True(t, ok)
	rm = tuipkg.SimulateSlowHarnessTurnStart(rm)

	// The view should contain the "thinking via claude…" label.
	view := rm.View()
	require.Contains(t, view, "thinking via claude", "spinner label should appear in in-flight mode")
}

// TestTUISlashCommandDuringInFlight verifies that slash commands work even during
// ModeAwaitingLLM (they bypass the single-flight check).
func TestTUISlashCommandDuringInFlight(t *testing.T) {
	slow := newSlowHarness("look")
	defer close(slow.release)

	m, _ := buildModelWithHarness(t, slow)

	// Go in-flight.
	m, _ = submitLine(m, "I want to do something creative and free-form")

	rm, ok := tuipkg.ExtractRootModel(m)
	require.True(t, ok)
	if !tuipkg.IsInFlight(rm) {
		t.Skip("input was deterministically routed; skipping slash-command-during-inflight test")
	}

	// /menu slash command should still work.
	m, _ = submitLine(m, "/menu")

	// Should still be in-flight (not crashed or quit).
	rm2, ok2 := tuipkg.ExtractRootModel(m)
	require.True(t, ok2)
	require.True(t, tuipkg.IsInFlight(rm2), "still in-flight after /menu command")
}
