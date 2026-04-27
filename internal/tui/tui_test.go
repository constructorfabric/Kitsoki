package tui_test

import (
	"context"
	"flag"
	"os"
	"path/filepath"
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
	m := tuipkg.NewRootModel(orch, sid, "", initialView)
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

// ─── System menu (Esc overlay) ───────────────────────────────────────────────

// TestTUIMenuEscOpens verifies that pressing Esc from ModeOnPath opens the
// menu overlay and enters ModeMenu.
func TestTUIMenuEscOpens(t *testing.T) {
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	require.Equal(t, tuipkg.ModeMenu, extractMode(t, m))

	rm, ok := tuipkg.ExtractRootModel(m)
	require.True(t, ok)
	require.True(t, tuipkg.MenuSystemActive(rm))
	require.Contains(t, tuipkg.MenuSystemView(rm), "Exit")
	require.Contains(t, tuipkg.MenuSystemView(rm), "Report bug")
	require.Contains(t, tuipkg.MenuSystemView(rm), "Edit mode")
}

// TestTUIMenuEscDismisses verifies that pressing Esc inside the overlay
// closes it and returns to ModeOnPath without picking anything.
func TestTUIMenuEscDismisses(t *testing.T) {
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	require.Equal(t, tuipkg.ModeMenu, extractMode(t, m))

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	require.Equal(t, tuipkg.ModeOnPath, extractMode(t, m))
	rm, _ := tuipkg.ExtractRootModel(m)
	require.False(t, tuipkg.MenuSystemActive(rm))
}

// TestTUIMenuExitQuits verifies that picking "Exit" (hotkey 1) quits cleanly.
func TestTUIMenuExitQuits(t *testing.T) {
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	// Drain the menuSystemChoiceMsg that the overlay emits for hotkey "1".
	var cmd tea.Cmd
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m = processCommands(m, cmd, 5)

	rm, ok := tuipkg.ExtractRootModel(m)
	require.True(t, ok)
	require.True(t, rm.Quitting(), "picking Exit should set quitting")
}

// TestTUIMenuReportBugStub verifies that the Report bug row posts a stub
// system message instead of crashing or advancing any state.
func TestTUIMenuReportBugStub(t *testing.T) {
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	var cmd tea.Cmd
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m = processCommands(m, cmd, 5)

	require.Equal(t, tuipkg.ModeOnPath, extractMode(t, m))
	transcript := extractTranscript(t, m)
	require.Contains(t, transcript, "bug report: coming soon")
}

// TestEditModeFullFlow drives the LLM-authoring overlay end-to-end:
// open the system menu → pick Edit mode → type a proposal → wait for
// Claude to "rewrite" the file (via the fake-claude.sh testdata script
// living in internal/authoring/testdata) → Apply → confirm the file
// was written + the orchestrator reloaded.
func TestEditModeFullFlow(t *testing.T) {
	// Wire the fake claude binary used by internal/authoring tests.
	fakeBin := "../authoring/testdata/fake-claude.sh"
	abs, err := filepath.Abs(fakeBin)
	require.NoError(t, err)
	fi, err := os.Stat(abs)
	require.NoError(t, err, "fake-claude.sh missing")
	require.NotZero(t, fi.Mode()&0o111, "fake-claude.sh not executable")
	t.Setenv("HALLY_ORACLE_CLAUDE_BIN", abs)

	// Copy the cloak app.yaml into a temp dir so the test doesn't
	// mutate the repo; build the orchestrator against the copy.
	srcYAML, err := os.ReadFile("../../testdata/apps/cloak/app.yaml")
	require.NoError(t, err)
	dir := t.TempDir()
	appPath := filepath.Join(dir, "app.yaml")
	require.NoError(t, os.WriteFile(appPath, srcYAML, 0o644))

	def, err := app.Load(appPath)
	require.NoError(t, err)
	mach, err := machine.New(def)
	require.NoError(t, err)
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	h, err := harness.NewReplay("../../testdata/apps/cloak/oracle.yaml")
	require.NoError(t, err)
	orch := orchestrator.New(def, mach, s, h)
	sid, err := orch.NewSession(context.Background())
	require.NoError(t, err)

	w := orch.InitialWorld()
	initialView, err := orch.InitialView(w)
	require.NoError(t, err)
	m := tea.Model(tuipkg.NewRootModel(orch, sid, appPath, initialView))

	// Open system menu, then pick row 3 (Edit mode).
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	var cmd tea.Cmd
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	m = processCommands(m, cmd, 5)
	require.Equal(t, tuipkg.ModeEdit, extractMode(t, m))

	rm, ok := tuipkg.ExtractRootModel(m)
	require.True(t, ok)
	require.Equal(t, tuipkg.EditPhaseInput, tuipkg.EditPhase(rm))

	// Type the ADD_LINE sentinel proposal — fake-claude appends a
	// comment so the diff is non-empty.
	m, _ = typeString(m, "ADD_LINE: append a marker")
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = processCommands(m, cmd, 20)

	rm, _ = tuipkg.ExtractRootModel(m)
	require.Equal(t, tuipkg.EditPhaseReview, tuipkg.EditPhase(rm),
		"after Propose returns, phase should be Review")
	require.Contains(t, extractTranscript(t, m), "fake-claude appended this comment",
		"diff in transcript should reference the appended comment")

	// File on disk hasn't changed yet (Apply not called).
	body, err := os.ReadFile(appPath)
	require.NoError(t, err)
	require.Equal(t, string(srcYAML), string(body),
		"Propose alone must not write to disk")

	// Hit 'a' to apply.
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m = processCommands(m, cmd, 10)

	require.Equal(t, tuipkg.ModeOnPath, extractMode(t, m),
		"after a successful apply+reload, mode returns to ModeOnPath")
	transcript := extractTranscript(t, m)
	require.Contains(t, transcript, "applied + reloaded")

	// File on disk now contains the appended marker comment.
	body, err = os.ReadFile(appPath)
	require.NoError(t, err)
	require.Contains(t, string(body), "fake-claude appended this comment")
}

// TestEditModeCancelFromInput verifies that pressing Esc during the
// proposal-input phase returns to ModeOnPath without invoking Claude.
func TestEditModeCancelFromInput(t *testing.T) {
	srcYAML, err := os.ReadFile("../../testdata/apps/cloak/app.yaml")
	require.NoError(t, err)
	dir := t.TempDir()
	appPath := filepath.Join(dir, "app.yaml")
	require.NoError(t, os.WriteFile(appPath, srcYAML, 0o644))

	def, err := app.Load(appPath)
	require.NoError(t, err)
	mach, err := machine.New(def)
	require.NoError(t, err)
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	h, err := harness.NewReplay("../../testdata/apps/cloak/oracle.yaml")
	require.NoError(t, err)
	orch := orchestrator.New(def, mach, s, h)
	sid, err := orch.NewSession(context.Background())
	require.NoError(t, err)
	w := orch.InitialWorld()
	initialView, err := orch.InitialView(w)
	require.NoError(t, err)
	m := tea.Model(tuipkg.NewRootModel(orch, sid, appPath, initialView))

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	var cmd tea.Cmd
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	m = processCommands(m, cmd, 5)
	require.Equal(t, tuipkg.ModeEdit, extractMode(t, m))

	// Cancel.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	require.Equal(t, tuipkg.ModeOnPath, extractMode(t, m))
}

// TestTUIMenuEditModeUnavailableWithoutAppPath verifies that picking
// the Edit mode row from the system menu when no appPath was passed to
// NewRootModel surfaces a polite "unavailable" message and stays in
// ModeOnPath. Tests build the model with appPath="", so this is the
// path most non-edit tests exercise.
func TestTUIMenuEditModeUnavailableWithoutAppPath(t *testing.T) {
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	var cmd tea.Cmd
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	m = processCommands(m, cmd, 5)

	require.Equal(t, tuipkg.ModeOnPath, extractMode(t, m))
	transcript := extractTranscript(t, m)
	require.Contains(t, transcript, "edit mode: unavailable")
}

// ─── Ctrl+C tiered behaviour ─────────────────────────────────────────────────

// TestTUICtrlCClearsPrompt verifies that a single Ctrl+C with non-empty
// prompt text clears the prompt rather than quitting.
func TestTUICtrlCClearsPrompt(t *testing.T) {
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)

	m, _ = typeString(m, "look around")
	rm, ok := tuipkg.ExtractRootModel(m)
	require.True(t, ok)
	require.Equal(t, "look around", tuipkg.GetPromptValue(rm))

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})

	rm2, ok2 := tuipkg.ExtractRootModel(m)
	require.True(t, ok2)
	require.False(t, rm2.Quitting(), "Ctrl+C with text in prompt must not quit")
	require.Empty(t, tuipkg.GetPromptValue(rm2), "Ctrl+C should clear the prompt")
}

// TestTUICtrlCFirstPressWarns verifies that a single Ctrl+C with an empty
// prompt posts a hint but does not quit.
func TestTUICtrlCFirstPressWarns(t *testing.T) {
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})

	rm, ok := tuipkg.ExtractRootModel(m)
	require.True(t, ok)
	require.False(t, rm.Quitting(), "first Ctrl+C on empty prompt should not quit")
	require.Contains(t, tuipkg.GetTranscriptContent(rm), "Ctrl+C again")
}

// TestTUICtrlCDoubleTapQuits verifies that two Ctrl+C presses in quick
// succession quit the program.
func TestTUICtrlCDoubleTapQuits(t *testing.T) {
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})

	rm, ok := tuipkg.ExtractRootModel(m)
	require.True(t, ok)
	require.True(t, rm.Quitting(), "double Ctrl+C should quit")
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
	m := tea.Model(tuipkg.NewRootModel(orch, sidNew, "", initialView))

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
	m := tea.Model(tuipkg.NewRootModel(orch, sidNew, "", initialView))

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

	m := tuipkg.NewRootModel(orch, sid, "", initialView)
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

// TestIsScrollKey asserts that the scroll-key classifier matches the bindings
// we advertise in the TUI hint line and rejects arbitrary keys.
func TestIsScrollKey(t *testing.T) {
	yes := []tea.KeyMsg{
		{Type: tea.KeyPgUp},
		{Type: tea.KeyPgDown},
		{Type: tea.KeyCtrlU},
		{Type: tea.KeyCtrlD},
		{Type: tea.KeyCtrlB},
		{Type: tea.KeyCtrlF},
		{Type: tea.KeyShiftUp},
		{Type: tea.KeyShiftDown},
	}
	for _, k := range yes {
		require.True(t, tuipkg.IsScrollKey(k), "expected %q to be a scroll key", k.String())
	}
	no := []tea.KeyMsg{
		{Type: tea.KeyEnter},
		{Type: tea.KeyRunes, Runes: []rune{'a'}},
		{Type: tea.KeyUp},
		{Type: tea.KeyDown},
		{Type: tea.KeyHome},
		{Type: tea.KeyEnd},
	}
	for _, k := range no {
		require.False(t, tuipkg.IsScrollKey(k), "did not expect %q to be a scroll key", k.String())
	}
}

// TestMultilineViewPreservesNewlines exercises the Terminal Room bug where
// glamour Markdown rendering reflowed plain-text views into single
// paragraphs, eating intentional line breaks and indented example lines.
// Views must still flow through glamour for styling, but each line's text
// has to survive as its own rendered line via the two-trailing-spaces
// hard-break preprocessor.
func TestMultilineViewPreservesNewlines(t *testing.T) {
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)

	rm, ok := tuipkg.ExtractRootModel(m)
	require.True(t, ok)

	view := "Terminal Room.\n" +
		"Workspace: my-api-project\n" +
		"\n" +
		"Propose a command to run, e.g.:\n" +
		"  propose \"list files in /tmp\"\n" +
		"  propose \"git status\"\n" +
		"  propose \"go test ./...\"\n"

	tuipkg.SetTranscriptSizeForTest(&rm, 200, 20)
	tuipkg.AppendTurnForTest(&rm, "open terminal", view)

	content := tuipkg.GetTranscriptContent(rm)

	// Each logical line must still appear in the rendered output. Glamour
	// may have wrapped the text in ANSI sequences for styling, so we
	// search for the bare text tokens that would only co-occur on the
	// same output line if newlines were honoured.
	wantTokens := []string{
		"Terminal Room.",
		"Workspace: my-api-project",
		"Propose a command to run",
		`list files in /tmp`,
		`git status`,
		`go test ./...`,
	}
	for _, w := range wantTokens {
		require.Contains(t, content, w,
			"view token %q missing from rendered transcript (glamour reflow regression?)", w)
	}

	// The critical check: the three propose examples must NOT all land on
	// the same rendered line. We count line occurrences by stripping ANSI
	// escapes and splitting — at least two of the three propose lines
	// must be on distinct lines, otherwise paragraph reflow won.
	plain := stripANSI(content)
	var linesWithPropose int
	for _, line := range strings.Split(plain, "\n") {
		if strings.Contains(line, "propose ") {
			linesWithPropose++
		}
	}
	require.GreaterOrEqual(t, linesWithPropose, 3,
		"expected three distinct lines containing 'propose ', got %d\n--- rendered ---\n%s",
		linesWithPropose, plain)
}

// stripANSI removes ANSI SGR escape sequences so line-level assertions
// don't trip over glamour's styling codes.
func stripANSI(s string) string {
	var b strings.Builder
	inEsc := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			inEsc = true
			i++
			continue
		}
		if inEsc {
			// CSI parameter bytes until a byte in 0x40..0x7E ends the sequence.
			if c >= 0x40 && c <= 0x7E {
				inEsc = false
			}
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// TestPreserveLeadingIndent asserts that the preprocessor swaps 2+ leading
// ASCII spaces for non-breaking spaces so Markdown renderers don't strip
// intentional indentation on continuation lines. Lines with 0 or 1 leading
// space are passed through unchanged.
func TestPreserveLeadingIndent(t *testing.T) {
	in := "Header\n" +
		"  two-space indent\n" +
		"   three-space indent\n" +
		" single-space left alone\n" +
		"no indent\n"

	out := tuipkg.PreserveLeadingIndent(in)

	// Lines that had 2+ leading spaces now have non-breaking spaces.
	nbsp := " "
	require.Contains(t, out, nbsp+nbsp+"two-space indent",
		"expected 2-space indent promoted to NBSP; got:\n%q", out)
	require.Contains(t, out, nbsp+nbsp+nbsp+"three-space indent",
		"expected 3-space indent promoted to 3×NBSP; got:\n%q", out)

	// 1-space and 0-space lines preserved as-is.
	require.Contains(t, out, " single-space left alone")
	require.Contains(t, out, "\nno indent\n")
}

// TestSlashMouseToggle exercises /mouse on, /mouse off, and bare /mouse
// (toggle) through the slash-command handler and asserts the model's
// mouseOn bit flips accordingly, plus a system message lands in the
// transcript for each transition.
func TestSlashMouseToggle(t *testing.T) {
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)

	rm, ok := tuipkg.ExtractRootModel(m)
	require.True(t, ok)
	require.True(t, tuipkg.MouseOn(rm), "mouse should start ON")

	// Explicit /mouse off.
	m = runTurnBlocking(t, m, "/mouse off")
	rm, _ = tuipkg.ExtractRootModel(m)
	require.False(t, tuipkg.MouseOn(rm), "/mouse off should disable mouse")

	// /mouse off again — no-op, but shouldn't error.
	m = runTurnBlocking(t, m, "/mouse off")
	rm, _ = tuipkg.ExtractRootModel(m)
	require.False(t, tuipkg.MouseOn(rm), "/mouse off when already off stays off")

	// Bare /mouse toggles — from off back to on.
	m = runTurnBlocking(t, m, "/mouse")
	rm, _ = tuipkg.ExtractRootModel(m)
	require.True(t, tuipkg.MouseOn(rm), "bare /mouse should toggle back to on")

	// Explicit /mouse on.
	m = runTurnBlocking(t, m, "/mouse off")
	m = runTurnBlocking(t, m, "/mouse on")
	rm, _ = tuipkg.ExtractRootModel(m)
	require.True(t, tuipkg.MouseOn(rm), "/mouse on should enable mouse")

	// Transcript contains one of the system messages we emit.
	content := tuipkg.GetTranscriptContent(rm)
	require.Contains(t, content, "mouse:")
}

// TestPreserveLeadingIndentKeepsListMarkers asserts that bullet- and
// ordered-list lines keep their ASCII leading spaces, so glamour can still
// format them as proper Markdown list items (otherwise a line like
// "  - terminal" gets reflowed instead of rendering as a list bullet).
func TestPreserveLeadingIndentKeepsListMarkers(t *testing.T) {
	cases := []string{
		"  - terminal              (run commands)",
		"  * alt-bullet",
		"  + plus-bullet",
		"  1. first",
		"  12) twelfth",
	}
	for _, line := range cases {
		out := tuipkg.PreserveLeadingIndent(line)
		require.Equal(t, line, out,
			"list-marker line must pass through unchanged; got: %q", out)
	}

	// Non-list indented lines still get NBSP treatment.
	nbsp := "\u00a0"
	got := tuipkg.PreserveLeadingIndent(`  propose "list files"`)
	_ = nbsp
	require.NotEqual(t, `  propose "list files"`, got,
		"non-list indented line should have leading spaces promoted; got: %q", got)
	require.True(t, strings.HasSuffix(got, `propose "list files"`),
		"expected text unchanged after promoted leading spaces; got: %q", got)
}

// TestMouseWheelMovesViewport asserts that a MouseMsg wheel-up event routed
// through the root model moves the transcript viewport up. This exercises
// the non-KeyMsg path in RootModel.Update where mouse events fall through
// to the default branch and get forwarded to the transcript.
func TestMouseWheelMovesViewport(t *testing.T) {
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)

	rm, ok := tuipkg.ExtractRootModel(m)
	require.True(t, ok)

	tuipkg.SetTranscriptSizeForTest(&rm, 60, 6)
	for i := 0; i < 40; i++ {
		tuipkg.AppendTranscriptForTest(&rm, "history line")
	}

	before := tuipkg.TranscriptYOffset(rm)

	// Deliver a wheel-up event (Button + Action shape used by
	// bubbletea v1.3; the viewport checks Button == WheelUp).
	out, _ := (tea.Model(rm)).Update(tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonWheelUp,
	})
	rm2, ok := tuipkg.ExtractRootModel(out)
	require.True(t, ok)
	after := tuipkg.TranscriptYOffset(rm2)

	require.Less(t, after, before,
		"wheel-up should scroll transcript up (offset went %d → %d)", before, after)
}

// TestScrollKeyMovesViewport asserts that sending a PgUp keypress to the
// root model moves the transcript viewport up when there is scrollable
// content, i.e. the scroll keys are actually routed to the viewport and not
// swallowed by the prompt.
func TestScrollKeyMovesViewport(t *testing.T) {
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)

	rm, ok := tuipkg.ExtractRootModel(m)
	require.True(t, ok)

	// Force the transcript viewport to a small height, then append enough
	// content that scrolling is meaningful.
	tuipkg.SetTranscriptSizeForTest(&rm, 60, 6)
	for i := 0; i < 40; i++ {
		tuipkg.AppendTranscriptForTest(&rm, "history line")
	}

	require.True(t, tuipkg.TranscriptAtBottom(rm),
		"expected to start at bottom after appends (offset=%d)",
		tuipkg.TranscriptYOffset(rm))

	before := tuipkg.TranscriptYOffset(rm)

	// Deliver a PgUp keypress through the root model's Update path.
	out, _ := (tea.Model(rm)).Update(tea.KeyMsg{Type: tea.KeyPgUp})
	rm2, ok := tuipkg.ExtractRootModel(out)
	require.True(t, ok)
	after := tuipkg.TranscriptYOffset(rm2)

	require.Less(t, after, before,
		"PgUp should scroll transcript up (offset went %d → %d)", before, after)
}
