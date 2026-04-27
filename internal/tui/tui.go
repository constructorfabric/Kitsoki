// Package tui implements the full-screen Bubble Tea interface (§9a).
// It composes sub-models for the location header, transcript pane,
// menu list, graph overlay, prompt input, and slot-fill modals.
package tui

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"hally/internal/app"
	"hally/internal/orchestrator"
	"hally/internal/viz"
)

// Mode describes which interaction mode the TUI is currently in (§7.3, §7.7).
type Mode int

const (
	// ModeOnPath is the default on-path mode.
	ModeOnPath Mode = iota
	// ModeOffPath is the visually-framed free-form mode (§7.7).
	ModeOffPath
	// ModeSlotFilling is active while the user fills missing slots (§7.3).
	ModeSlotFilling
	// ModeDisambiguating is active while the disambiguation menu is shown (§7.4).
	ModeDisambiguating
	// ModeAwaitingLLM is active while the LLM harness is processing a turn.
	// Input is disabled and a spinner is shown.
	ModeAwaitingLLM
	// ModeMenu is active while the Esc-triggered system menu is on screen.
	ModeMenu
	// ModeEdit is active while the LLM-driven authoring overlay owns the
	// prompt and transcript (proposal → diff review → apply + reload).
	ModeEdit
)

// ctrlCQuitWindow is how long after a Ctrl+C the next Ctrl+C will quit
// rather than just re-clearing the prompt.
const ctrlCQuitWindow = 2 * time.Second

// TurnComplete is the tea.Msg sent by the orchestrator when a turn finishes.
type TurnComplete struct {
	// View is the rendered narrative from the transition.
	View string
	// Allowed is the list of currently-allowed intent names for the menu.
	Allowed []string
	// NewState is the new state path, used to highlight the graph pane.
	NewState string
}

// MenuChanged is sent when the allowed-intent list changes outside of a turn
// (e.g., after a guard re-evaluation).
type MenuChanged struct {
	Allowed []string
}

// turnOutcomeMsg wraps a TurnOutcome for delivery as a tea.Msg.
type turnOutcomeMsg struct {
	outcome *orchestrator.TurnOutcome
	input   string // the user input that triggered this turn
	err     error
}

// continueTurnOutcomeMsg wraps the result of ContinueTurn.
type continueTurnOutcomeMsg struct {
	outcome *orchestrator.TurnOutcome
	err     error
}

// RootModel is the top-level Bubble Tea model composing all sub-models (§9a.1).
// It uses pointer receivers so the same *RootModel can be type-asserted after
// being returned from Update as a tea.Model interface.
type RootModel struct {
	orch      *orchestrator.Orchestrator
	sid       app.SessionID
	appPath   string // path to app.yaml, needed for edit mode reloads ("" disables edit)
	mode      Mode
	width     int
	height    int
	quitting  bool

	location   locationModel
	transcript transcriptModel
	menu       menuModel
	offPath    offPathModel
	clarify        clarifyModel
	disambiguation disambiguationModel
	menuSystem     menuSystemModel
	edit           editModel
	prompt         textinput.Model

	// lastCtrlC is the time the most recent Ctrl+C was pressed, used to
	// detect a double-tap quit. Zero means no recent press (or the window
	// has expired).
	lastCtrlC time.Time

	// spinner is shown while ModeAwaitingLLM is active.
	spinner spinner.Model

	// inFlightCancel cancels the running turn's context. Non-nil only while
	// ModeAwaitingLLM is active.
	inFlightCancel context.CancelFunc

	// pendingKind classifies the in-flight async work so the spinner caption
	// can describe what is actually happening (LLM routing vs. host call).
	pendingKind pendingKind

	// currentState tracks the current state path for location updates.
	currentState app.StatePath

	// lastInput remembers the input that triggered the current turn.
	lastInput string

	// traceFile is the open trace file when /trace is active (nil otherwise).
	traceFile *os.File
	// traceWriter is the buffered writer for the trace file.
	traceWriter *bufio.Writer

	// mouseOn reflects whether the TUI is currently capturing mouse events.
	// Controlled at runtime via the /mouse slash command. Defaults to true
	// because the program is launched with tea.WithMouseCellMotion.
	mouseOn bool
}

// NewRootModel creates the root TUI model. appPath is the path to the
// app.yaml backing this session — required for the Esc-menu "Edit mode"
// reload flow. Pass "" to disable edit mode (e.g., from tests).
func NewRootModel(orch *orchestrator.Orchestrator, sid app.SessionID, appPath, initialView string) RootModel {
	const defaultWidth = 120
	const defaultHeight = 40
	const menuWidth = 30
	const transcriptHeight = 30

	ti := textinput.New()
	ti.Placeholder = "what now?"
	ti.Focus()
	ti.CharLimit = 512

	sp := spinner.New()
	sp.Spinner = spinner.MiniDot
	sp.Style = lipgloss.NewStyle().Foreground(colorPrimary)

	m := RootModel{
		orch:           orch,
		sid:            sid,
		appPath:        appPath,
		mode:           ModeOnPath,
		width:          defaultWidth,
		height:         defaultHeight,
		location:       newLocationModel(),
		transcript:     newTranscriptModel(defaultWidth-menuWidth-6, transcriptHeight),
		menu:           newMenuModel(menuWidth, transcriptHeight),
		offPath:        newOffPathModel(""),
		clarify:        newClarifyModel(),
		disambiguation: newDisambiguationModel(),
		menuSystem:     newMenuSystemModel(),
		edit:           newEditModel(),
		prompt:         ti,
		spinner:        sp,
		mouseOn:        true,
	}

	// Set initial state.
	m.currentState = orch.InitialState()

	// Show initial view in transcript.
	if initialView != "" {
		m.transcript.AppendSystem(initialView)
	}

	// Populate initial menu.
	w := orch.InitialWorld()
	computedMenu := orchestrator.ComputeMenu(orch.AppDef(), orch.Machine(), m.currentState, w)
	m.menu, _ = m.menu.Update(menuItemsChanged{items: computedMenu.Primary, blocked: computedMenu.Blocked})

	// Set initial location.
	loc := orchestrator.ComputeLocation(orch.AppDef(), m.currentState, w, 0)
	m.location, _ = m.location.Update(locationUpdated{loc: loc})

	return m
}

// Init implements tea.Model.
func (m RootModel) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, m.spinner.Tick)
}

// Update implements tea.Model.
func (m RootModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// If slot-filling is active, route to clarify model first.
	if m.mode == ModeSlotFilling {
		return m.updateSlotFilling(msg)
	}
	// If disambiguating, route to disambiguation model first.
	if m.mode == ModeDisambiguating {
		return m.updateDisambiguating(msg)
	}
	// If the system menu overlay is active, it owns the keyboard.
	if m.mode == ModeMenu {
		return m.updateMenuSystem(msg)
	}
	// If edit mode is active, it owns the keyboard and prompt.
	if m.mode == ModeEdit {
		return m.updateEdit(msg)
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		slog.Debug("tui.resize",
			slog.Int("width", msg.Width),
			slog.Int("height", msg.Height),
		)
		m.width = msg.Width
		m.height = msg.Height
		m = m.resize()
		return m, nil

	case tea.KeyMsg:
		return m.routeKey(msg)

	case turnOutcomeMsg:
		// Clear in-flight state before handling.
		m.mode = ModeOnPath
		m.inFlightCancel = nil
		return m.handleTurnOutcome(msg)

	case continueTurnOutcomeMsg:
		return m.handleContinueTurnOutcome(msg)

	case supplementSlotsMsg:
		return m.handleSupplementSlots(msg)

	case disambiguationChoiceMsg:
		return m.handleDisambiguationChoice(msg)

	case menuSystemChoiceMsg:
		return m.handleMenuSystemChoice(msg)

	case spinner.TickMsg:
		// Only animate when awaiting LLM.
		if m.mode == ModeAwaitingLLM {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil

	default:
		// Pass to sub-models.
		var cmd tea.Cmd
		m.location, _ = m.location.Update(msg)
		m.transcript, cmd = m.transcript.Update(msg)
		m.menu, _ = m.menu.Update(msg)
		return m, cmd
	}
}

func (m RootModel) updateSlotFilling(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case supplementSlotsMsg:
		m.mode = ModeOnPath
		m.clarify.Close()
		return m.handleSupplementSlots(msg)

	case tea.KeyMsg:
		if msg.Type == tea.KeyEsc {
			// Cancel clarify.
			m.mode = ModeOnPath
			m.clarify.Close()
			m.transcript.AppendSystem("(slot fill cancelled)")
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.clarify, cmd = m.clarify.Update(msg)
	return m, cmd
}

func (m RootModel) routeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Ctrl+C tiered behaviour:
	//   1) in-flight LLM turn  → cancel the turn (no quit, no timer bump)
	//   2) prompt has text     → clear the prompt
	//   3) empty prompt, first press → remember the time, warn user
	//   4) empty prompt, second press within ctrlCQuitWindow → quit
	if msg.Type == tea.KeyCtrlC {
		if m.mode == ModeAwaitingLLM && m.inFlightCancel != nil {
			m.inFlightCancel()
			// Don't quit; wait for the turnOutcomeMsg to arrive with the cancel error.
			return m, nil
		}
		if strings.TrimSpace(m.prompt.Value()) != "" {
			m.prompt.SetValue("")
			m.lastCtrlC = time.Time{}
			return m, nil
		}
		now := time.Now()
		if !m.lastCtrlC.IsZero() && now.Sub(m.lastCtrlC) <= ctrlCQuitWindow {
			m.quitting = true
			return m, tea.Quit
		}
		m.lastCtrlC = now
		m.transcript.AppendSystem("(press Ctrl+C again to exit, or Esc for menu)")
		return m, nil
	}

	// Esc opens the system menu from the default interactive modes.
	// It deliberately does not fire during ModeAwaitingLLM (Ctrl+C cancels
	// the turn first) or while a slot-filling / disambiguation overlay is
	// already using Esc to back out.
	if msg.Type == tea.KeyEsc {
		if m.mode == ModeOnPath || m.mode == ModeOffPath {
			m.mode = ModeMenu
			m.menuSystem.Open()
			return m, nil
		}
	}

	// Scroll keys forward to the transcript viewport so prior turns can be
	// re-read. Allowed in every mode (including ModeAwaitingLLM) because
	// scrollback never mutates state.
	if isScrollKey(msg) {
		var cmd tea.Cmd
		m.transcript, cmd = m.transcript.Update(msg)
		return m, cmd
	}

	// Slash commands always route, even during ModeAwaitingLLM.
	if msg.Type == tea.KeyEnter {
		input := strings.TrimSpace(m.prompt.Value())
		if strings.HasPrefix(input, "/") {
			m.prompt.SetValue("")
			return m.handleSlashCommand(input)
		}
	}

	// While awaiting LLM: show a notice and swallow other input.
	if m.mode == ModeAwaitingLLM {
		if msg.Type == tea.KeyEnter {
			// Flash a transient notice — appended as a system message.
			m.transcript.AppendSystem("hold on — still thinking about the previous turn")
		}
		// All other keys are silently ignored while in-flight.
		return m, nil
	}

	switch msg.String() {
	case "enter":
		// If the prompt is empty and a menu row is highlighted, dispatch it directly.
		if strings.TrimSpace(m.prompt.Value()) == "" {
			if entry := m.menu.SelectedEntry(); entry != nil {
				return m.dispatchMenuEntry(entry)
			}
		}
		return m.submitInput()

	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		// When the prompt is empty, number keys are menu hotkeys — they select a row
		// without feeding the digit into the text input.
		if strings.TrimSpace(m.prompt.Value()) == "" {
			var menuCmd tea.Cmd
			m.menu, menuCmd = m.menu.Update(msg)
			_ = menuCmd
			return m, nil
		}
		// Prompt has text — fall through to normal text input handling.
	}

	// Pass key to prompt.
	var cmd tea.Cmd
	m.prompt, cmd = m.prompt.Update(msg)
	return m, cmd
}

func (m RootModel) submitInput() (tea.Model, tea.Cmd) {
	input := strings.TrimSpace(m.prompt.Value())
	if input == "" {
		return m, nil
	}
	m.prompt.SetValue("")

	// Handle slash commands.
	if strings.HasPrefix(input, "/") {
		return m.handleSlashCommand(input)
	}

	m.lastInput = input

	// Cheap, side-effect-free match against the current menu. This avoids the
	// LLM round-trip when the user typed something we can route locally — but
	// we still dispatch the resulting transition asynchronously so a slow
	// on_enter host call (e.g. host.oracle.ask, host.run on a long command)
	// doesn't freeze the TUI.
	orch := m.orch
	sid := m.sid
	ctx := context.Background()
	intent, slots, hit, err := orch.MatchDeterministic(ctx, sid, input)
	if err != nil {
		m.transcript.AppendError(input, fmt.Sprintf("error: %v", err))
		return m, nil
	}
	if hit {
		return startAsyncTurn(m, input, asyncSubmitDirect(orch, sid, intent, slots), pendingDeterministic)
	}

	// No deterministic match — call the LLM router asynchronously.
	return startAsyncTurn(m, input, asyncTurn(orch, sid, input), pendingLLM)
}

// pendingKind classifies why the TUI is currently in ModeAwaitingLLM, so the
// spinner caption can describe the right thing (intent routing vs. effects
// running).
type pendingKind int

const (
	// pendingLLM means the LLM router is currently mapping user text → intent.
	pendingLLM pendingKind = iota
	// pendingDeterministic means the input matched a menu entry locally; the
	// async work is the post-transition effect dispatch (host calls, etc.).
	pendingDeterministic
)

// asyncTurn returns a function that runs orch.Turn for the LLM-router path.
// It is the closure shape that startAsyncTurn understands.
func asyncTurn(orch *orchestrator.Orchestrator, sid app.SessionID, input string) func(context.Context) (*orchestrator.TurnOutcome, error) {
	return func(ctx context.Context) (*orchestrator.TurnOutcome, error) {
		return orch.Turn(ctx, sid, input)
	}
}

// asyncSubmitDirect returns a function that runs orch.SubmitDirect for the
// deterministic-hit path.
func asyncSubmitDirect(orch *orchestrator.Orchestrator, sid app.SessionID, intent string, slots map[string]any) func(context.Context) (*orchestrator.TurnOutcome, error) {
	return func(ctx context.Context) (*orchestrator.TurnOutcome, error) {
		return orch.SubmitDirect(ctx, sid, intent, slots)
	}
}

// startAsyncTurn puts the model into ModeAwaitingLLM and returns a tea.Cmd that
// runs the supplied turn function asynchronously. The caller must replace its
// own model with the returned model before returning from Update.
func startAsyncTurn(
	m RootModel,
	input string,
	run func(ctx context.Context) (*orchestrator.TurnOutcome, error),
	kind pendingKind,
) (RootModel, tea.Cmd) {
	ctx, cancel := context.WithCancel(context.Background())
	m.inFlightCancel = cancel
	m.mode = ModeAwaitingLLM
	m.pendingKind = kind

	cmd := func() tea.Msg {
		out, err := run(ctx)
		if err != nil {
			// If the context was cancelled, return a ModeCancelled outcome
			// rather than a raw error.
			if ctx.Err() != nil {
				return turnOutcomeMsg{
					outcome: &orchestrator.TurnOutcome{Mode: orchestrator.ModeCancelled},
					input:   input,
					err:     nil,
				}
			}
			return turnOutcomeMsg{outcome: nil, input: input, err: err}
		}
		return turnOutcomeMsg{outcome: out, input: input, err: nil}
	}
	return m, tea.Batch(m.spinner.Tick, cmd)
}

func (m RootModel) handleSlashCommand(cmd string) (tea.Model, tea.Cmd) {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return m, nil
	}

	switch parts[0] {
	case "/quit", "/q":
		m.quitting = true
		return m, tea.Quit

	case "/freeform":
		m.mode = ModeOffPath
		m.offPath, _ = m.offPath.Update(enterOffPathMsg{})
		m.location, _ = m.location.Update(offPathToggled{on: true})
		m.transcript, _ = m.transcript.Update(offPathToggled{on: true})
		m.prompt.Placeholder = "freeform chat — /onpath to resume"
		m.transcript.AppendSystem(m.offPath.Banner())
		return m, nil

	case "/onpath":
		m.mode = ModeOnPath
		m.offPath, _ = m.offPath.Update(exitOffPathMsg{})
		m.location, _ = m.location.Update(offPathToggled{on: false})
		m.transcript, _ = m.transcript.Update(offPathToggled{on: false})
		m.prompt.Placeholder = "what now?"
		m.transcript.AppendSystem("(returned to on-path mode)")
		return m, nil

	case "/menu":
		// Toggle menu visibility — for Stage 6 just echo.
		m.transcript.AppendSystem("(menu toggle — use arrow keys to navigate)")
		return m, nil

	case "/viz":
		// Write a DOT file for the current app.
		dotPath := m.orch.AppDef().App.ID + "-viz.dot"
		f, err := os.Create(dotPath)
		if err != nil {
			m.transcript.AppendSystem(fmt.Sprintf("(viz: could not create %s: %v)", dotPath, err))
		} else {
			if exportErr := viz.Export(m.orch.AppDef(), f); exportErr != nil {
				m.transcript.AppendSystem(fmt.Sprintf("(viz: export error: %v)", exportErr))
			} else {
				m.transcript.AppendSystem(fmt.Sprintf("(viz: wrote %s — render with: dot -Tpng %s -o graph.png)", dotPath, dotPath))
			}
			_ = f.Close()
		}
		return m, nil

	case "/trace":
		return m.handleTraceToggle(), nil

	case "/mouse":
		return m.handleMouseCommand(parts[1:])

	default:
		m.transcript.AppendSystem(fmt.Sprintf("(unknown command: %s)", parts[0]))
		return m, nil
	}
}

// handleMouseCommand toggles the TUI's mouse capture on or off at runtime.
// Mouse capture enables scroll-wheel events on the transcript viewport but
// hijacks native click-drag text selection. Without an argument it toggles;
// "/mouse on" and "/mouse off" force a state.
//
// When mouse is off, standard terminal selection works again; keyboard
// scroll bindings (Shift+↑/↓, Ctrl+U/Ctrl+D, Ctrl+B/Ctrl+F) still work.
func (m RootModel) handleMouseCommand(args []string) (tea.Model, tea.Cmd) {
	want := !m.mouseOn // default: toggle
	if len(args) > 0 {
		switch strings.ToLower(args[0]) {
		case "on", "enable", "true", "1":
			want = true
		case "off", "disable", "false", "0":
			want = false
		case "toggle", "":
			want = !m.mouseOn
		default:
			m.transcript.AppendSystem(fmt.Sprintf("(mouse: unknown arg %q — use on|off|toggle)", args[0]))
			return m, nil
		}
	}
	if want == m.mouseOn {
		state := "off"
		if m.mouseOn {
			state = "on"
		}
		m.transcript.AppendSystem(fmt.Sprintf("(mouse: already %s)", state))
		return m, nil
	}
	m.mouseOn = want
	if want {
		m.transcript.AppendSystem("(mouse: ON — scroll wheel works; hold Option/Shift while dragging to select text)")
		return m, tea.EnableMouseCellMotion
	}
	m.transcript.AppendSystem("(mouse: OFF — native click-drag text selection works; keyboard scroll still active)")
	return m, tea.DisableMouse
}

// handleTraceToggle opens (first invocation) or closes (second invocation) a live
// JSONL trace file in /tmp. It wires a slog.JSONHandler into the orchestrator.
func (m RootModel) handleTraceToggle() RootModel {
	if m.traceFile != nil {
		// Close the trace — restore default (no-op) logger.
		_ = m.traceWriter.Flush()
		_ = m.traceFile.Close()
		m.orch.SetLogger(slog.Default())
		m.transcript.AppendSystem(fmt.Sprintf("(trace: closed %s)", m.traceFile.Name()))
		m.traceFile = nil
		m.traceWriter = nil
		return m
	}

	// Open a new temp file.
	f, err := os.CreateTemp("", "hally-trace-*.jsonl")
	if err != nil {
		m.transcript.AppendSystem(fmt.Sprintf("(trace: could not create temp file: %v)", err))
		return m
	}
	bw := bufio.NewWriter(f)
	handler := slog.NewJSONHandler(bw, &slog.HandlerOptions{Level: slog.LevelDebug})
	logger := slog.New(handler)
	m.orch.SetLogger(logger)

	m.traceFile = f
	m.traceWriter = bw
	m.transcript.AppendSystem(fmt.Sprintf("(trace: writing to %s — /trace again to stop)", f.Name()))
	return m
}

// dispatchMenuEntry handles a menu row selection:
//   - If MissingSlots is empty: submit the full IntentCall directly via SubmitDirect
//     (bypasses the LLM harness entirely — we already know the intent + all slots).
//   - If MissingSlots is non-empty: open the clarification flow with PrefilledSlots
//     as the starting values.
func (m RootModel) dispatchMenuEntry(entry *orchestrator.MenuEntry) (tea.Model, tea.Cmd) {
	if len(entry.MissingSlots) == 0 {
		// All slots are known — submit directly. Run async with the spinner
		// so a slow on_enter host call (e.g. host.oracle.ask) does not freeze
		// the TUI.
		slots := entry.PrefilledSlots
		if slots == nil {
			slots = make(map[string]any)
		}
		displayText := entry.Display
		m.lastInput = displayText
		return startAsyncTurn(m, displayText, asyncSubmitDirect(m.orch, m.sid, entry.Intent, slots), pendingDeterministic)
	}

	// Missing slots remain — open the clarification flow.
	// Convert SlotRef list to SlotNeed list.
	needs := make([]orchestrator.SlotNeed, len(entry.MissingSlots))
	for i, sr := range entry.MissingSlots {
		needs[i] = orchestrator.SlotNeed{
			Name:        sr.Name,
			Type:        sr.Type,
			Values:      sr.Values,
			Description: sr.Description,
			Prompt:      sr.Prompt,
		}
	}
	m.mode = ModeSlotFilling
	m.clarify.Open(entry.Intent, needs, entry.PrefilledSlots)
	if len(needs) > 0 {
		slot := needs[0]
		prompt := slot.Prompt
		if prompt == "" {
			prompt = "Please provide: " + slot.Name
		}
		m.transcript.AppendSystem("> " + entry.Display + "\n" + prompt)
	}
	return m, nil
}

func (m RootModel) runTurn(input string) tea.Cmd {
	orch := m.orch
	sid := m.sid
	return func() tea.Msg {
		ctx := context.Background()
		out, err := orch.Turn(ctx, sid, input)
		return turnOutcomeMsg{outcome: out, input: input, err: err}
	}
}


func (m RootModel) handleTurnOutcome(msg turnOutcomeMsg) (tea.Model, tea.Cmd) {
	// Clear in-flight state (safe to call even if already cleared).
	if m.inFlightCancel != nil {
		m.inFlightCancel = nil
	}
	if m.mode == ModeAwaitingLLM {
		m.mode = ModeOnPath
	}

	if msg.err != nil {
		m.transcript.AppendError(msg.input, fmt.Sprintf("error: %v", msg.err))
		return m, nil
	}

	out := msg.outcome

	switch out.Mode {
	case orchestrator.ModeCancelled:
		m.transcript.AppendSystem("(cancelled)")
		return m, nil

	case orchestrator.ModeTransitioned, orchestrator.ModeCompleted:
		m.currentState = out.NewState
		m.transcript.AppendTurn(msg.input, out.View)

		// Update menu.
		w := m.orch.InitialWorld() // only used for initial world; menu comes from allowed list
		m = m.updateMenuFromAllowed(out.AllowedIntents, w)

		// Update location.
		m = m.updateLocation(out)

		if out.Mode == orchestrator.ModeCompleted {
			m.transcript.AppendSystem("\n[Game over — start a new session to play again]")
			m.prompt.Placeholder = "(game over)"
		}

	case orchestrator.ModeClarify:
		// Enter slot-filling mode.
		m.mode = ModeSlotFilling
		m.clarify.Open(out.PendingIntent, out.SlotsNeeded, out.PendingSlots)
		// Show a prompt asking for the first slot.
		if len(out.SlotsNeeded) > 0 {
			slot := out.SlotsNeeded[0]
			prompt := slot.Prompt
			if prompt == "" {
				prompt = "Please provide: " + slot.Name
			}
			m.transcript.AppendSystem("> " + msg.input + "\n" + prompt)
		}

	case orchestrator.ModeRejected:
		m.currentState = out.NewState

		// If there are disambiguation candidates, enter disambiguation mode.
		if len(out.Candidates) > 0 {
			m.mode = ModeDisambiguating
			m.disambiguation.Open(out.Candidates)
			m.transcript.AppendTurn(msg.input, "")
			m.transcript.AppendSystem(m.disambiguation.View())
			return m, nil
		}

		var errMsg string
		if out.GuardHint != "" {
			errMsg = out.GuardHint
		} else if out.ErrorMessage != "" {
			errMsg = out.ErrorMessage
		} else {
			errMsg = string(out.ErrorCode)
		}
		m.transcript.AppendTurn(msg.input, "")
		m.transcript.AppendGuardHint(errMsg)
	}

	return m, nil
}

func (m RootModel) handleContinueTurnOutcome(msg continueTurnOutcomeMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.transcript.AppendError("(slot-fill)", fmt.Sprintf("error: %v", msg.err))
		return m, nil
	}
	out := msg.outcome

	fakeMsg := turnOutcomeMsg{outcome: out, input: "(slot-fill)", err: nil}
	return m.handleTurnOutcome(fakeMsg)
}

// updateEdit owns the keyboard while the LLM-driven edit overlay is
// active. Phase transitions:
//
//   editPhaseInput     → Enter starts authoring.Propose (→ editPhaseThinking)
//                        Esc cancels back to ModeOnPath
//   editPhaseThinking  → Ctrl+C cancels the in-flight Propose
//                        editProposalReadyMsg arrives → editPhaseReview
//   editPhaseReview    → 'a' applies + reloads (→ editPhaseApplying)
//                        'r' refines (→ editPhaseInput, keep last proposal text)
//                        'c' or Esc cancels back to ModeOnPath
//   editPhaseApplying  → editApplyDoneMsg arrives → reload + return to ModeOnPath
func (m RootModel) updateEdit(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case editProposalReadyMsg:
		// Clear the spinner state.
		m.inFlightCancel = nil
		if msg.err != nil {
			m.transcript.AppendError("(edit)", "proposal failed: "+msg.err.Error())
			m.edit.phase = editPhaseInput
			m.prompt.Placeholder = "describe a change to app.yaml…"
			return m, nil
		}
		m.edit.proposal = msg.proposal
		m.edit.phase = editPhaseReview
		summary := msg.proposal.Summary
		if summary == "" {
			summary = "(no summary returned)"
		}
		m.transcript.AppendSystem("**Proposal:** " + summary +
			"\n\n" + renderDiffForTranscript(msg.proposal.UnifiedDiff))
		return m, nil

	case editApplyDoneMsg:
		if msg.err != nil {
			m.transcript.AppendError("(edit)", "apply failed: "+msg.err.Error())
			m.edit.phase = editPhaseReview
			return m, nil
		}
		// File is on disk — reload the orchestrator.
		res, err := m.orch.Reload(m.appPath, m.currentState)
		if err != nil {
			m.transcript.AppendError("(edit)",
				"applied to disk but reload failed: "+err.Error())
			m.edit.phase = editPhaseReview
			return m, nil
		}
		// On reload, refresh menu and location from the new machine.
		w := m.orch.CurrentWorld(m.sid)
		computed := orchestrator.ComputeMenu(m.orch.AppDef(), m.orch.Machine(), m.currentState, w)
		m.menu, _ = m.menu.Update(menuItemsChanged{items: computed.Primary, blocked: computed.Blocked})
		loc := orchestrator.ComputeLocation(m.orch.AppDef(), m.currentState, w, 0)
		m.location, _ = m.location.Update(locationUpdated{loc: loc})

		note := "applied + reloaded."
		if !res.PrevStateExists {
			note += " (your current state no longer exists in the new app — restart to enter the new graph)"
		}
		m.transcript.AppendSystem("_" + note + "_")
		m.edit.Open()
		m.mode = ModeOnPath
		m.prompt.Placeholder = "what now?"
		return m, nil

	case spinner.TickMsg:
		if m.edit.phase == editPhaseThinking || m.edit.phase == editPhaseApplying {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil
	}

	keyMsg, isKey := msg.(tea.KeyMsg)
	if !isKey {
		return m, nil
	}

	// Ctrl+C during thinking cancels the in-flight Claude call.
	if keyMsg.Type == tea.KeyCtrlC {
		if m.edit.phase == editPhaseThinking && m.inFlightCancel != nil {
			m.inFlightCancel()
			return m, nil
		}
		// Otherwise: same exit semantics as ModeOnPath.
		m.mode = ModeOnPath
		m.edit.Open()
		m.prompt.Placeholder = "what now?"
		m.transcript.AppendSystem("_(edit cancelled)_")
		return m, nil
	}

	if keyMsg.Type == tea.KeyEsc {
		// Esc cancels in any phase except thinking (where Ctrl+C cancels the call).
		if m.edit.phase == editPhaseThinking {
			return m, nil
		}
		m.mode = ModeOnPath
		m.edit.Open()
		m.prompt.SetValue("")
		m.prompt.Placeholder = "what now?"
		m.transcript.AppendSystem("_(edit cancelled)_")
		return m, nil
	}

	switch m.edit.phase {
	case editPhaseInput:
		if keyMsg.Type == tea.KeyEnter {
			text := strings.TrimSpace(m.prompt.Value())
			if text == "" {
				return m, nil
			}
			m.prompt.SetValue("")
			ctx, cancel := context.WithCancel(context.Background())
			m.inFlightCancel = cancel
			m.edit.phase = editPhaseThinking
			m.transcript.AppendSystem("> " + text)
			return m, tea.Batch(m.spinner.Tick, proposeCmd(ctx, m.appPath, text))
		}
		// All other keys feed the prompt textinput.
		var cmd tea.Cmd
		m.prompt, cmd = m.prompt.Update(msg)
		return m, cmd

	case editPhaseThinking:
		// Swallow keys other than Ctrl+C / Esc (handled above).
		return m, nil

	case editPhaseReview:
		switch strings.ToLower(keyMsg.String()) {
		case "a":
			if m.edit.proposal == nil {
				return m, nil
			}
			m.edit.phase = editPhaseApplying
			return m, tea.Batch(m.spinner.Tick, applyCmd(m.edit.proposal))
		case "r":
			m.edit.phase = editPhaseInput
			m.edit.proposal = nil
			m.prompt.Placeholder = "refine the proposal…"
			return m, nil
		case "c":
			m.mode = ModeOnPath
			m.edit.Open()
			m.prompt.Placeholder = "what now?"
			m.transcript.AppendSystem("_(edit cancelled)_")
			return m, nil
		}
		return m, nil

	case editPhaseApplying:
		return m, nil
	}
	return m, nil
}

// updateMenuSystem routes keys to the overlay and reverts to ModeOnPath when
// the user dismisses it without choosing.
func (m RootModel) updateMenuSystem(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.menuSystem, cmd = m.menuSystem.Update(msg)
	if !m.menuSystem.IsActive() && m.mode == ModeMenu {
		// Dismissed without picking (Esc/q) — return to on-path.
		m.mode = ModeOnPath
	}
	return m, cmd
}

// handleMenuSystemChoice dispatches the action selected in the overlay.
// The overlay closed itself before emitting the message; this function is
// responsible for reverting mode and performing the effect.
func (m RootModel) handleMenuSystemChoice(msg menuSystemChoiceMsg) (tea.Model, tea.Cmd) {
	m.mode = ModeOnPath
	switch msg.action {
	case menuActionExit:
		m.quitting = true
		return m, tea.Quit
	case menuActionReportBug:
		m.transcript.AppendSystem("(bug report: coming soon — this will bundle session state and a freeform note)")
		return m, nil
	case menuActionEditMode:
		if m.appPath == "" {
			m.transcript.AppendSystem("(edit mode: unavailable — no app path bound to this session)")
			return m, nil
		}
		m.mode = ModeEdit
		m.edit.Open()
		m.prompt.SetValue("")
		m.prompt.Placeholder = "describe a change to app.yaml…"
		m.transcript.AppendSystem("**Edit mode** — describe a change in plain English. Esc to cancel.")
		return m, nil
	}
	return m, nil
}

func (m RootModel) updateDisambiguating(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case disambiguationChoiceMsg:
		return m.handleDisambiguationChoice(msg)
	case tea.KeyMsg:
		if msg.Type == tea.KeyEsc {
			m.mode = ModeOnPath
			m.disambiguation.Close()
			m.transcript.AppendSystem("(disambiguation cancelled)")
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.disambiguation, cmd = m.disambiguation.Update(msg)
	return m, cmd
}

func (m RootModel) handleDisambiguationChoice(msg disambiguationChoiceMsg) (tea.Model, tea.Cmd) {
	m.mode = ModeOnPath
	m.disambiguation.Close()
	chosen := msg.chosen
	m.transcript.AppendSystem(fmt.Sprintf("(chose: %s)", chosen.Intent))
	// Re-run the turn with the chosen intent directly.
	// We synthesise the input as the intent name so the harness is bypassed
	// and the machine receives the choice directly.
	orch := m.orch
	sid := m.sid
	return m, func() tea.Msg {
		// Note: for a real implementation this would call a ContinueWithIntent method.
		// For the PoC, we surface the choice in the transcript.
		_ = orch
		_ = sid
		return nil
	}
}

func (m RootModel) handleSupplementSlots(msg supplementSlotsMsg) (tea.Model, tea.Cmd) {
	m.mode = ModeOnPath
	orch := m.orch
	sid := m.sid
	return m, func() tea.Msg {
		ctx := context.Background()
		out, err := orch.ContinueTurn(ctx, sid, msg.slots)
		return continueTurnOutcomeMsg{outcome: out, err: err}
	}
}

func (m RootModel) updateMenuFromAllowed(allowedNames []string, w interface{}) RootModel {
	// Recompute the full menu (with enum expansion and guard dry-runs).
	world := m.orch.CurrentWorld(m.sid)
	computedMenu := orchestrator.ComputeMenu(m.orch.AppDef(), m.orch.Machine(), m.currentState, world)
	m.menu, _ = m.menu.Update(menuItemsChanged{items: computedMenu.Primary, blocked: computedMenu.Blocked})
	return m
}

func (m RootModel) updateLocation(out *orchestrator.TurnOutcome) RootModel {
	w := m.orch.CurrentWorld(m.sid)
	loc := orchestrator.ComputeLocation(m.orch.AppDef(), out.NewState, w, out.TurnNumber)
	m.location, _ = m.location.Update(locationUpdated{loc: loc})
	return m
}

func (m RootModel) resize() RootModel {
	const minMenuWidth = 28
	const locationHeight = 2
	const promptHeight = 3

	menuWidth := minMenuWidth

	// Allocate the full remaining horizontal space to the transcript.
	// lipgloss.JoinHorizontal concatenates panes with no gap, so the
	// transcript panel's total width must be m.width - menuWidth for both
	// panes together to fill the terminal. Previously this subtracted an
	// extra 4 chars "for safety", which just left a visible dead zone on
	// the right and made the body text wrap 4 chars early.
	transcriptWidth := m.width - menuWidth
	if transcriptWidth < 40 {
		transcriptWidth = 40
	}
	transcriptHeight := m.height - locationHeight - promptHeight - 4
	if transcriptHeight < 5 {
		transcriptHeight = 5
	}

	// Inner content area = panel width minus border (1 each side) and
	// padding (1 each side) from transcriptStyle. That's 4 chars total.
	innerWidth := transcriptWidth - 4

	// Apply size through SetSize so the Glamour renderer rebuilds with the
	// correct wrap width. The root intercepts tea.WindowSizeMsg before it
	// reaches the transcript, so this is the only place the transcript
	// learns its real size.
	m.transcript.SetSize(transcriptWidth, transcriptHeight, innerWidth, transcriptHeight-2)

	m.menu.width = menuWidth
	m.menu.height = transcriptHeight

	m.location, _ = m.location.Update(tea.WindowSizeMsg{Width: m.width, Height: 1})

	return m
}

// View implements tea.Model.
func (m RootModel) View() string {
	if m.quitting {
		return "Goodbye!\n"
	}

	// Location bar (full width).
	locationBar := m.location.View()

	// Main body: transcript (left) + menu (right).
	transcriptView := m.transcript.View()
	menuView := m.menu.View()
	body := lipgloss.JoinHorizontal(lipgloss.Top, transcriptView, menuView)

	// Prompt line.
	// Spinner placement: right of the prompt prefix on the input line.
	// This keeps the location bar uncluttered and puts the spinner
	// where the user's eye is already focused (the input area).
	var promptLine string
	if m.mode == ModeSlotFilling {
		promptLine = m.clarify.View()
	} else if m.mode == ModeDisambiguating {
		promptLine = m.disambiguation.View()
	} else if m.mode == ModeMenu {
		promptLine = m.menuSystem.View()
	} else if m.mode == ModeEdit {
		switch m.edit.phase {
		case editPhaseInput:
			promptLine = promptStyle.Render("✎ ") + m.prompt.View() + "\n" + editInputHint
		case editPhaseThinking:
			promptLine = promptStyle.Render("✎ ") + m.spinner.View() + " " +
				lipgloss.NewStyle().Foreground(colorMuted).Render("claude is rewriting app.yaml… (Ctrl+C to cancel)")
		case editPhaseReview:
			promptLine = promptStyle.Render("✎ ") + editReviewHint
		case editPhaseApplying:
			promptLine = promptStyle.Render("✎ ") + m.spinner.View() + " " +
				lipgloss.NewStyle().Foreground(colorMuted).Render("writing + reloading…")
		}
	} else if m.mode == ModeAwaitingLLM {
		spinnerStr := m.spinner.View()
		caption := "thinking via claude… (Ctrl+C to cancel)"
		if m.pendingKind == pendingDeterministic {
			caption = "running… (Ctrl+C to cancel)"
		}
		promptLine = promptStyle.Render("> ") + spinnerStr + " " +
			lipgloss.NewStyle().Foreground(colorMuted).Render(caption)
	} else {
		prefix := promptStyle.Render("> ")
		if m.mode == ModeOffPath {
			prefix = promptOffPathStyle.Render("> ")
		}
		promptLine = prefix + m.prompt.View()
	}

	// Key hints — shown under the prompt so users know how to scroll back
	// through prior turns. Bindings are picked to work on MacBook keyboards
	// without PgUp/PgDn/Home/End physical keys. When mouse capture is off,
	// drop "mouse wheel" from the hint and advertise the /mouse command.
	var scrollHint string
	if m.mouseOn {
		scrollHint = "scroll: mouse wheel · Shift+↑/↓ · Ctrl+U/Ctrl+D (half) · Ctrl+B/Ctrl+F (page) · /mouse off to select text"
	} else {
		scrollHint = "scroll: Shift+↑/↓ · Ctrl+U/Ctrl+D (half) · Ctrl+B/Ctrl+F (page) · /mouse on to re-enable wheel"
	}
	hints := lipgloss.NewStyle().Foreground(colorMuted).Render(scrollHint)

	// Stack vertically.
	return lipgloss.JoinVertical(lipgloss.Left,
		locationBar,
		body,
		promptLine,
		hints,
	)
}

// isScrollKey reports whether a key event is a transcript-scroll key.
// Scroll keys are forwarded to the viewport rather than the prompt so the
// user can re-read earlier turns. Bindings are picked to work on MacBook
// keyboards (no PgUp/PgDn/Home/End) while still honouring those keys when
// present on external keyboards.
//
// Primary set (MacBook-friendly):
//   - Shift+↑ / Shift+↓ — one line up / down
//   - Ctrl+B / Ctrl+F   — full page up / down
//   - Ctrl+U / Ctrl+D   — half page up / down
//
// Also accepted: Opt+↑/↓ (alias for Shift+↑/↓) and PgUp/PgDn.
func isScrollKey(msg tea.KeyMsg) bool {
	switch msg.String() {
	case "pgup", "pgdown", "page up", "page down",
		"ctrl+b", "ctrl+f",
		"ctrl+u", "ctrl+d",
		"shift+up", "shift+down",
		"alt+up", "alt+down":
		return true
	}
	return false
}
