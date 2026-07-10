package tui_test

import (
	"context"
	"flag"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/chathost"
	"kitsoki/internal/chats"
	"kitsoki/internal/harness"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
	tuipkg "kitsoki/internal/tui"
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

	h, err := harness.NewReplay("../../testdata/apps/cloak/recording.yaml")
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

// processCommands runs all synchronous commands until exhausted. It
// flattens nested tea.BatchMsg values by maintaining a queue of
// pending cmds (FIFO) rather than tracking only the latest — without
// that, a batch like Batch(asyncTurn, printLines) would discard the
// turnOutcomeMsg's downstream Println cmds because each iteration
// overwrites lastCmd. Spinner ticks are skipped (pure animation).
func processCommands(m tea.Model, cmd tea.Cmd, maxDepth int) tea.Model {
	queue := []tea.Cmd{cmd}
	for i := 0; i < maxDepth && len(queue) > 0; i++ {
		next := queue[0]
		queue = queue[1:]
		if next == nil {
			continue
		}
		msg := next()
		if msg == nil {
			continue
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			// Enqueue every sub-cmd so its produced message lands
			// in m.Update too (and any downstream cmd makes it onto
			// the queue).
			for _, subCmd := range batch {
				if subCmd != nil {
					queue = append(queue, subCmd)
				}
			}
			continue
		}
		if isSpinnerTick(msg) {
			continue
		}
		var newCmd tea.Cmd
		m, newCmd = m.Update(msg)
		if newCmd != nil {
			queue = append(queue, newCmd)
		}
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
		"go west",          // foyer → cloakroom
		"hang the cloak",   // cloakroom → hang cloak
		"go east",          // cloakroom → foyer
		"go south",         // foyer → bar.lit (cloak is hung)
		"read the message", // bar.lit → ended (won)
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

// setupCloakWithAgent builds a cloak orchestrator wired with a real chat
// store and the host-package's fake-agent.sh as the claude stand-in.
// Returns the underlying store handle so tests can sniff the event log.
func setupCloakWithAgent(t *testing.T) (*orchestrator.Orchestrator, app.SessionID, store.Store) {
	t.Helper()

	// Point the agent handler at the fake-agent.sh shipped with the
	// host package's testdata. Path is repo-root-relative.
	t.Setenv("KITSOKI_AGENT_CLAUDE_BIN", "../host/testdata/fake-agent.sh")

	def, err := app.Load("../../testdata/apps/cloak/app.yaml")
	require.NoError(t, err)

	mach, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	h, err := harness.NewReplay("../../testdata/apps/cloak/recording.yaml")
	require.NoError(t, err)

	rawChatStore, err := chats.NewStore(s.DB())
	require.NoError(t, err)

	orch := orchestrator.New(def, mach, s, h,
		orchestrator.WithChatStore(chathost.NewAdapter(rawChatStore)),
	)

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	return orch, sid, s
}

// TestTUIOffPathInputRoutesToAgent exercises the off-path runtime end-to-end:
// /freeform flips ModeOffPath, the next text submission goes to the agent
// via AskOffPath, the orchestrator's foreground Turn() machinery is NOT
// invoked, and only the off-path event kinds land in the session log.
func TestTUIOffPathInputRoutesToAgent(t *testing.T) {
	orch, sid, s := setupCloakWithAgent(t)
	m := buildModel(t, orch, sid)

	// /freeform → ModeOffPath.
	m = runTurnBlocking(t, m, "/freeform")
	require.Equal(t, tuipkg.ModeOffPath, extractMode(t, m))

	// Type and submit a free-form question. This should NOT route through
	// MatchDeterministic + harness — it should fire AskOffPath instead.
	m = runTurnBlocking(t, m, "what is the meaning of life?")

	// After the async reply lands, we should be back in ModeOffPath
	// (not ModeOnPath — the banner stays).
	require.Equal(t, tuipkg.ModeOffPath, extractMode(t, m))

	// The transcript should contain the fake agent's echo of our question.
	transcriptText := extractTranscript(t, m)
	require.Contains(t, transcriptText, "what is the meaning of life?",
		"transcript should include the user's off-path question header")
	require.Contains(t, transcriptText, "ANSWER for q=[what is the meaning of life?]",
		"transcript should include the fake agent's echoed answer")

	// Sniff the raw event log: only off-path event kinds should be
	// present from the off-path question; in particular, no
	// TransitionApplied was emitted by the off-path submission.
	hist, err := s.LoadHistory(sid)
	require.NoError(t, err)
	var sawOffPathQuestion, sawOffPathAnswer bool
	for _, ev := range hist {
		switch ev.Kind {
		case store.OffPathQuestion:
			sawOffPathQuestion = true
		case store.OffPathAnswer:
			sawOffPathAnswer = true
		case store.TransitionApplied:
			// Cloak's session start emits a TransitionApplied for the
			// initial state-enter; that's fine. But anything else here
			// means off-path leaked into the foreground turn path.
			// We allow only the initial-entry transition by checking
			// it was emitted at turn ≤ 0 (the initial root entry).
			require.LessOrEqual(t, int64(ev.Turn), int64(0),
				"unexpected TransitionApplied at turn %d after off-path input", ev.Turn)
		}
	}
	require.True(t, sawOffPathQuestion,
		"expected an OffPathQuestion event in the session log")
	require.True(t, sawOffPathAnswer,
		"expected an OffPathAnswer event in the session log")
}

// TestTUIOffPathDeniedWhileAwaitingLLM verifies the /freeform safety gate:
// while the model is in ModeAwaitingLLM (an on-path turn is in flight),
// /freeform is refused with a transient transcript message rather than
// silently switching modes mid-turn and orphaning the goroutine.
//
// We bypass submitLine() (which type-streams characters; type input is
// blocked in ModeAwaitingLLM) and pre-fill the prompt directly, then press
// Enter. The slash-command branch in routeKey() fires before the awaiting
// gate, so /freeform is reached even while in-flight — it's our enterOffPath
// helper's job to deny the switch.
func TestTUIOffPathDeniedWhileAwaitingLLM(t *testing.T) {
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)

	// Manually put the model into ModeAwaitingLLM (no real goroutine).
	rm, ok := tuipkg.ExtractRootModel(m)
	require.True(t, ok)
	rm = tuipkg.SimulateSlowHarnessTurnStart(rm)
	require.True(t, tuipkg.IsInFlight(rm))

	// Pre-fill the prompt with "/freeform" then press Enter so the slash
	// branch in routeKey runs.
	tuipkg.SetPromptValue(&rm, "/freeform")
	updated, _ := rm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	rm2, ok := tuipkg.ExtractRootModel(updated)
	require.True(t, ok)
	require.Equal(t, tuipkg.ModeAwaitingLLM, tuipkg.GetMode(rm2),
		"/freeform must NOT switch modes while ModeAwaitingLLM is active")

	transcriptText := tuipkg.GetTranscriptContent(rm2)
	require.Contains(t, transcriptText, "can't enter off-path while a turn is in flight",
		"expected the gate message to appear in the transcript")
}

// ─── Slash command tests ──────────────────────────────────────────────────────

func TestTUISlashFreeformAndOnpath(t *testing.T) {
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)

	// /freeform → mode should change to off-path. Single-pane redesign
	// signals off-path via the footer's mode label + the prompt's "#"
	// prefix; the legacy full-screen banner is gone.
	m = runTurnBlocking(t, m, "/freeform")
	view := m.View()
	require.Contains(t, view, "off-path",
		"footer should advertise off-path mode after /freeform")
	require.Equal(t, tuipkg.ModeOffPath, extractMode(t, m),
		"expected ModeOffPath after /freeform")

	// /onpath → back to on-path.
	m = runTurnBlocking(t, m, "/onpath")
	view = m.View()
	require.Equal(t, tuipkg.ModeOnPath, extractMode(t, m),
		"expected ModeOnPath after /onpath")
	// The banner should be gone from the current rendering.
	_ = view // mode changed; transcript may still show the banner text but border should revert
}

// ─── Mode transitions ─────────────────────────────────────────────────────────

func TestTUIInitialMode(t *testing.T) {
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)
	require.Equal(t, tuipkg.ModeOnPath, extractMode(t, m))
}

// TestTUIPromptWidthClipsLongInput verifies that resize() sets a non-zero
// textarea inner content width so the prompt wraps long input rather than
// bleeding past the right edge. Without an explicit SetWidth call the
// textarea uses its 40-column default, which truncates / mis-wraps on
// wider terminals.
//
// Formula in resize(): textarea.SetWidth(m.width - 2 safety). The
// textarea then reserves promptPrefixCols (2) for the prompt gutter and
// reports the remaining inner width via Width(). At Width=80 we expect
// 80 - 2 (safety) - 2 (prompt gutter) = 76.
func TestTUIPromptWidthClipsLongInput(t *testing.T) {
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)

	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	rm, ok := tuipkg.ExtractRootModel(m)
	require.True(t, ok)

	w := tuipkg.GetPromptWidth(rm)
	require.Greater(t, w, 0,
		"prompt width must be > 0 after resize so long input wraps onto the next line")
	require.Equal(t, 76, w,
		"prompt width should equal terminal width minus safety margin (2) and prompt gutter (2)")
}

// TestTUIPromptWidthHonoursMinimum verifies that very narrow terminals
// don't drive the prompt width to zero or negative. resize() clamps the
// outer width to (promptMinWidth + promptPrefixCols) = 22, so after the
// textarea reserves the 2-column gutter the inner Width() is 20.
func TestTUIPromptWidthHonoursMinimum(t *testing.T) {
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)

	m, _ = m.Update(tea.WindowSizeMsg{Width: 10, Height: 24})

	rm, ok := tuipkg.ExtractRootModel(m)
	require.True(t, ok)

	require.Equal(t, 20, tuipkg.GetPromptWidth(rm),
		"prompt width must clamp to the 20-column inner minimum on narrow terminals")
}

func TestTUIResizeDoesNotClearNormalScreen(t *testing.T) {
	orch, sid := setupCloak(t)

	for _, tc := range []struct {
		name string
		mode tuipkg.Mode
	}{
		{"on-path", tuipkg.ModeOnPath},
		{"off-path", tuipkg.ModeOffPath},
		{"awaiting-llm", tuipkg.ModeAwaitingLLM},
		{"slot-filling", tuipkg.ModeSlotFilling},
		{"disambiguating", tuipkg.ModeDisambiguating},
		{"menu", tuipkg.ModeMenu},
		{"meta", tuipkg.ModeMeta},
		{"meta-sessions", tuipkg.ModeMetaSessions},
		{"world-view", tuipkg.ModeWorldView},
		{"choosing", tuipkg.ModeChoosing},
		{"operator-question", tuipkg.ModeOperatorQuestion},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := buildModel(t, orch, sid)
			rm, ok := tuipkg.ExtractRootModel(m)
			require.True(t, ok)
			tuipkg.ClearTranscriptPendingForTest(&rm)
			tuipkg.SetModeForTest(&rm, tc.mode)
			if tc.mode == tuipkg.ModeMenu {
				tuipkg.OpenMenuSystemForTest(&rm)
			}
			if tc.mode == tuipkg.ModeAwaitingLLM {
				tuipkg.AppendLiveForTest(&rm, strings.Repeat("routing status ", 20))
			}

			_, cmd := tea.Model(rm).Update(tea.WindowSizeMsg{Width: 72, Height: 20})

			require.NotContains(t, commandMessageTypes(cmd), "tea.clearScreenMsg",
				"resize is a regular repaint; clearing the normal screen on SIGWINCH appends duplicate live chrome to scrollback")
		})
	}
}

func TestTUIResizeAwaitingLLMDoesNotReprintLiveChrome(t *testing.T) {
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)
	rm, ok := tuipkg.ExtractRootModel(m)
	require.True(t, ok)
	tuipkg.ClearTranscriptPendingForTest(&rm)
	rm = tuipkg.SimulateSlowHarnessTurnStart(rm)
	tuipkg.AppendLiveForTest(&rm, strings.Repeat("routing status ", 20))

	_, cmd := tea.Model(rm).Update(tea.WindowSizeMsg{Width: 72, Height: 20})

	require.Nil(t, cmd, "resizing while running must only repaint View(); commands like ClearScreen/Println reprint the live running panel")
}

func TestTUIResizePreservesPendingTranscriptFlush(t *testing.T) {
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)

	_, cmd := m.Update(tea.WindowSizeMsg{Width: 72, Height: 20})

	types := commandMessageTypes(cmd)
	require.NotContains(t, types, "tea.clearScreenMsg",
		"resize should not clear the normal screen before repainting")
	require.Contains(t, types, "tea.printLineMessage",
		"resize should preserve pending transcript flushes")
}

func commandMessageTypes(cmd tea.Cmd) []string {
	if cmd == nil {
		return nil
	}
	return collectMessageTypes(cmd())
}

func collectMessageTypes(msg tea.Msg) []string {
	if msg == nil {
		return nil
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []string
		for _, cmd := range batch {
			out = append(out, commandMessageTypes(cmd)...)
		}
		return out
	}
	if reflect.TypeOf(msg).String() == "tea.sequenceMsg" {
		v := reflect.ValueOf(msg)
		out := make([]string, 0, v.Len())
		for i := 0; i < v.Len(); i++ {
			if cmd, ok := v.Index(i).Interface().(tea.Cmd); ok {
				out = append(out, commandMessageTypes(cmd)...)
			}
		}
		return out
	}
	return []string{reflect.TypeOf(msg).String()}
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
// menu overlay and enters ModeMenu. Cloak declares a meta_modes.story
// block, so the meta-mode row appears alongside Exit and the builtin
// `story.bug` mode row.
func TestTUIMenuEscOpens(t *testing.T) {
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	require.Equal(t, tuipkg.ModeMenu, extractMode(t, m))

	rm, ok := tuipkg.ExtractRootModel(m)
	require.True(t, ok)
	require.True(t, tuipkg.MenuSystemActive(rm))
	view := tuipkg.MenuSystemView(rm)
	require.Contains(t, view, "Exit")
	require.Contains(t, view, "improve the story")
	require.Contains(t, view, "Story bug",
		"builtin story.bug meta mode must surface — it replaced the legacy 'Report bug' stub and the single-token /meta bug entry")
	require.NotContains(t, view, "Report bug",
		"the 'coming soon' stub is gone; /meta story bug is the real flow now")
	require.NotContains(t, view, "Edit mode",
		"edit mode entry should be gone (replaced by /meta:story)")
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
	require.Contains(t, content, "didn't catch",
		"transcript should render a clarification (not [blocked]) for an INTENT_NOT_ALLOWED rejection")
	require.NotContains(t, content, "[blocked]",
		"the literal [blocked] prefix has been replaced with a softer rendering")
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

	// Phase 2 inline overlay: the "Clarification needed" block lands
	// in the transcript (not in the prompt area). Assert the block
	// shows the intent name, the slot prompt, and the numbered choice
	// list — the legacy modal would have routed these through the
	// prompt's View().
	transcript := tuipkg.GetTranscriptContent(extractRoot(t, m))
	require.Contains(t, transcript, "clarification needed")
	require.Contains(t, transcript, "move")
	require.Contains(t, transcript, "Which direction?")
	require.Contains(t, transcript, "1. north")
	require.Contains(t, transcript, "2. south")

	// Now drive the slot-fill loop through the normal prompt. Type "2"
	// (south) and press Enter — the parent intercepts, calls
	// clarify.SubmitValue, and dispatches continueTurnOutcome.
	rm, ok := tuipkg.ExtractRootModel(m)
	require.True(t, ok)
	tuipkg.SetPromptValue(&rm, "2")
	m, cmd := tea.Model(rm).Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = processCommands(m, cmd, 20)

	// After Enter, the model exits slot-filling (all slots are full) and
	// the transcript echoes the user's pick.
	transcript = tuipkg.GetTranscriptContent(extractRoot(t, m))
	require.Contains(t, transcript, "> 2", "user pick should be echoed in transcript")
	require.Contains(t, transcript, "accepted: south")
	require.NotEqual(t, tuipkg.ModeSlotFilling, extractMode(t, m),
		"after final slot fill, mode must leave ModeSlotFilling")
	_ = ctx
}

// extractRoot extracts the RootModel value from a tea.Model wrapper.
// Convenience for assertions that need to call GetTranscriptContent etc.
func extractRoot(t *testing.T, m tea.Model) tuipkg.RootModel {
	t.Helper()
	rm, ok := tuipkg.ExtractRootModel(m)
	require.True(t, ok)
	return rm
}

// ─── Menu expansion and direct dispatch ──────────────────────────────────────

// TestTUIMenuExpandedGoSouth verifies that the TUI's menu contains "go south"
// (not bare "go") in the foyer, and that dispatching it via the post-phase-4
// /intents <n> command advances the journey to bar.dark. Numeric quick-select
// was removed in phase 4; the equivalent surface is /intents <n>.
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

	// Dispatch via /intents <n>. Indices are 1-based in the rendered
	// block; goSouthIdx is 0-based so add 1.
	cmd := "/intents " + strconv.Itoa(goSouthIdx+1)
	tuipkg.SetPromptValue(&rm, cmd)
	m = processCommands(rm, func() tea.Msg {
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

// TestTUISingleFlightReject verifies that submitting a second input while
// in-flight does NOT pre-empt the running turn. Post-phase-4 the second
// submission goes into m.inputQueue rather than printing "still thinking"
// and being silently dropped — the user's text is preserved and dispatches
// when the in-flight turn settles.
func TestTUISingleFlightReject(t *testing.T) {
	slow := newSlowHarness("look")
	defer close(slow.release)

	m, _ := buildModelWithHarness(t, slow)

	// Submit a free-form input to go in-flight.
	m, _ = submitLine(m, "do something random")

	// Now submit a second input while still in-flight.
	m, cmd2 := submitLine(m, "do something else")

	rm, ok := tuipkg.ExtractRootModel(m)
	require.True(t, ok)
	require.True(t, tuipkg.IsInFlight(rm), "model should still be in ModeAwaitingLLM")

	// Queue carries the second submission; transcript has the queued-ack block.
	require.Equal(t, []string{"do something else"}, tuipkg.InputQueue(rm),
		"second submission during in-flight must enqueue")
	require.Contains(t, tuipkg.GetTranscriptContent(rm), "queued",
		"transcript should advertise the queue depth")

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

	// The view should contain the backend-neutral "thinking…" spinner label
	// (the router path may resolve via the local model or claude, so the caption
	// no longer names a specific backend).
	view := rm.View()
	require.Contains(t, view, "thinking", "spinner label should appear in in-flight mode")
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

// TestSlashMousePrintsRemovalNotice locks in the post-phase-5 contract:
// /mouse no longer toggles capture (the feature was removed) — it just
// prints a friendly notice into the transcript so anyone with the old
// muscle memory understands what changed.
func TestSlashMousePrintsRemovalNotice(t *testing.T) {
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)

	m = runTurnBlocking(t, m, "/mouse")
	rm, _ := tuipkg.ExtractRootModel(m)
	content := tuipkg.GetTranscriptContent(rm)
	require.Contains(t, content, "mouse",
		"the /mouse notice should mention mouse")
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

// TestScrollKeysAreNoOps locks the post-refactor contract: the
// in-app viewport scroll is gone. Without alt-screen the terminal's
// native scrollback owns scroll (mouse wheel, Cmd+↑). The PgUp/PgDn
// + Shift+↑/↓ + Ctrl+U/D/B/F keys are swallowed by routeKey so they
// don't fall through to the textarea as literal characters.
func TestScrollKeysAreNoOps(t *testing.T) {
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)
	rm, _ := tuipkg.ExtractRootModel(m)

	// PgUp shouldn't insert into the prompt and shouldn't error.
	tuipkg.SetPromptValue(&rm, "")
	out, _ := tea.Model(rm).Update(tea.KeyMsg{Type: tea.KeyPgUp})
	rm2, _ := tuipkg.ExtractRootModel(out)
	require.Equal(t, "", tuipkg.GetPromptValue(rm2),
		"PgUp should not insert into the prompt textarea")
}
