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
)

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
	prompt         textinput.Model

	// spinner is shown while ModeAwaitingLLM is active.
	spinner spinner.Model

	// inFlightCancel cancels the running turn's context. Non-nil only while
	// ModeAwaitingLLM is active.
	inFlightCancel context.CancelFunc

	// currentState tracks the current state path for location updates.
	currentState app.StatePath

	// lastInput remembers the input that triggered the current turn.
	lastInput string

	// traceFile is the open trace file when /trace is active (nil otherwise).
	traceFile *os.File
	// traceWriter is the buffered writer for the trace file.
	traceWriter *bufio.Writer
}

// NewRootModel creates the root TUI model.
func NewRootModel(orch *orchestrator.Orchestrator, sid app.SessionID, initialView string) RootModel {
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
		mode:           ModeOnPath,
		width:          defaultWidth,
		height:         defaultHeight,
		location:       newLocationModel(),
		transcript:     newTranscriptModel(defaultWidth-menuWidth-6, transcriptHeight),
		menu:           newMenuModel(menuWidth, transcriptHeight),
		offPath:        newOffPathModel(""),
		clarify:        newClarifyModel(),
		disambiguation: newDisambiguationModel(),
		prompt:         ti,
		spinner:        sp,
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

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
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
	// Ctrl+C: if in-flight, cancel the turn; otherwise quit.
	if msg.Type == tea.KeyCtrlC {
		if m.mode == ModeAwaitingLLM && m.inFlightCancel != nil {
			m.inFlightCancel()
			// Don't quit; wait for the turnOutcomeMsg to arrive with the cancel error.
			return m, nil
		}
		m.quitting = true
		return m, tea.Quit
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

	// Attempt deterministic match first (no LLM call, no spinner).
	orch := m.orch
	sid := m.sid
	ctx := context.Background()
	outcome, hit, err := orch.TryDeterministic(ctx, sid, input)
	if err != nil {
		m.transcript.AppendError(input, fmt.Sprintf("error: %v", err))
		return m, nil
	}
	if hit {
		// Deterministic hit — resolve synchronously with no spinner.
		return m.handleTurnOutcome(turnOutcomeMsg{outcome: outcome, input: input, err: nil})
	}

	// No deterministic match — call LLM asynchronously with spinner.
	return startLLMTurn(m, input)
}

// startLLMTurn puts the model into ModeAwaitingLLM and returns a tea.Cmd that
// runs the LLM turn asynchronously. The caller must replace its own model with
// the returned model before returning from Update.
func startLLMTurn(m RootModel, input string) (RootModel, tea.Cmd) {
	ctx, cancel := context.WithCancel(context.Background())
	m.inFlightCancel = cancel
	m.mode = ModeAwaitingLLM

	orch := m.orch
	sid := m.sid
	cmd := func() tea.Msg {
		out, err := orch.Turn(ctx, sid, input)
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

	default:
		m.transcript.AppendSystem(fmt.Sprintf("(unknown command: %s)", parts[0]))
		return m, nil
	}
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
		// All slots are known — submit directly.
		slots := entry.PrefilledSlots
		if slots == nil {
			slots = make(map[string]any)
		}
		displayText := entry.Display
		m.lastInput = displayText
		orch := m.orch
		sid := m.sid
		intentName := entry.Intent
		return m, func() tea.Msg {
			ctx := context.Background()
			out, err := orch.SubmitDirect(ctx, sid, intentName, slots)
			return turnOutcomeMsg{outcome: out, input: displayText, err: err}
		}
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
	transcriptWidth := m.width - menuWidth - 4
	if transcriptWidth < 40 {
		transcriptWidth = 40
	}
	transcriptHeight := m.height - locationHeight - promptHeight - 4
	if transcriptHeight < 5 {
		transcriptHeight = 5
	}

	m.transcript.width = transcriptWidth
	m.transcript.height = transcriptHeight
	m.transcript.vp.Width = transcriptWidth - 4
	m.transcript.vp.Height = transcriptHeight - 2

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
	} else if m.mode == ModeAwaitingLLM {
		spinnerStr := m.spinner.View()
		promptLine = promptStyle.Render("> ") + spinnerStr + " " +
			lipgloss.NewStyle().Foreground(colorMuted).Render("thinking via claude…")
	} else {
		prefix := promptStyle.Render("> ")
		if m.mode == ModeOffPath {
			prefix = promptOffPathStyle.Render("> ")
		}
		promptLine = prefix + m.prompt.View()
	}

	// Stack vertically.
	return lipgloss.JoinVertical(lipgloss.Left,
		locationBar,
		body,
		promptLine,
	)
}
