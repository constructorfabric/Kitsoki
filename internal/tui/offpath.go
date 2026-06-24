package tui

import (
	"context"
	"fmt"
	"log/slog"

	tea "github.com/charmbracelet/bubbletea"

	"kitsoki/internal/app"
	"kitsoki/internal/userfacing"
)

// Default off-path triggers used when the app's app.yaml declares no
// off_path: block (or declares one with empty trigger/return fields).
const (
	defaultOffPathTrigger = "/freeform"
	defaultOffPathReturn  = "/onpath"
	defaultOffPathBanner  = "*** off the path — responses do not affect your story ***"
)

// offPathModel manages the off-path mode state. See
// docs/stories/state-machine.md §11 "Off-path: the global escape hatch".
type offPathModel struct {
	active bool
	banner string
}

func newOffPathModel(banner string) offPathModel {
	if banner == "" {
		banner = defaultOffPathBanner
	}
	return offPathModel{banner: banner}
}

func (m offPathModel) Init() tea.Cmd { return nil }

func (m offPathModel) Update(msg tea.Msg) (offPathModel, tea.Cmd) {
	switch msg.(type) {
	case enterOffPathMsg:
		m.active = true
	case exitOffPathMsg:
		m.active = false
	}
	return m, nil
}

// Active returns true when in off-path mode.
func (m offPathModel) Active() bool { return m.active }

// Banner returns the off-path banner text.
func (m offPathModel) Banner() string { return m.banner }

// enterOffPathMsg activates off-path mode.
type enterOffPathMsg struct{}

// exitOffPathMsg deactivates off-path mode.
type exitOffPathMsg struct{}

// offPathReplyMsg is delivered when an async AskOffPath call returns.
// The TUI appends the answer (or an error) to the transcript and re-enables
// the prompt by clearing ModeAwaitingLLM.
type offPathReplyMsg struct {
	question string
	answer   string
	err      error
}

// offPathTriggers returns the (enter, exit) slash-command strings honoured
// by this app. When the app declares no off_path: block, both fields fall
// back to the engine defaults ("/freeform", "/onpath").
//
// When the app declares an off_path: block with only one of the two
// strings set, the other still falls back to its default — the author's
// declaration of "trigger: /go-off" should not silently disable the
// /onpath return path.
func offPathTriggers(def *app.AppDef) (enter, exit string) {
	enter = defaultOffPathTrigger
	exit = defaultOffPathReturn
	if def == nil || def.OffPath == nil {
		return
	}
	if def.OffPath.Trigger != "" {
		enter = def.OffPath.Trigger
	}
	if def.OffPath.Return != "" {
		exit = def.OffPath.Return
	}
	return
}

// offPathBannerFromApp returns the banner string the app declared in its
// off_path: block, or "" so newOffPathModel falls back to its default.
func offPathBannerFromApp(def *app.AppDef) string {
	if def == nil || def.OffPath == nil {
		return ""
	}
	return def.OffPath.Banner
}

// enterOffPath transitions the TUI from on-path → off-path. It records an
// OffPathEntered event in the session log (best-effort: a persistence
// failure surfaces as a soft warning in the transcript but does not block
// the mode switch — the user has already typed the trigger).
//
// Denied while ModeAwaitingLLM so a pending on-path turn cannot be
// abandoned mid-flight (which would leak the goroutine and produce a
// confusing transcript when the deferred turnOutcomeMsg eventually
// arrives). The user must cancel via Ctrl+C first.
func (m RootModel) enterOffPath() (tea.Model, tea.Cmd) {
	if m.mode == ModeAwaitingLLM {
		m.transcript.AppendSystem("(can't enter off-path while a turn is in flight — press Ctrl+C to cancel first)")
		return m, nil
	}
	if m.mode == ModeOffPath {
		// Idempotent — already off-path; no-op silently.
		return m, nil
	}
	// Close any active inline choice widget — the help banner takes
	// over. The next room entry's handleTurnOutcome re-opens.
	if m.mode == ModeChoosing {
		m.choice.Close()
		m.transcript.FinalizeLive("")
	}
	m.mode = ModeOffPath
	m.offPath, _ = m.offPath.Update(enterOffPathMsg{})
	m.location, _ = m.location.Update(offPathToggled{on: true})
	m.transcript, _ = m.transcript.Update(offPathToggled{on: true})
	_, exitCmd := offPathTriggers(m.orch.AppDef())
	m.prompt.Placeholder = fmt.Sprintf("freeform chat — type to ask the agent, %s to return", exitCmd)
	// Off-path prefix stays "> " but recolors to amber so the prompt
	// matches the off-path framing (transcript border, location bar).
	setPromptPrefix(&m.prompt, promptPrefixOnPath)
	setPromptStyle(&m.prompt, promptOffPathStyle)
	m.transcript.AppendSystem(m.offPath.Banner())
	m.transcript.AppendSystem(fmt.Sprintf("(type %s to return to your journey)", exitCmd))
	if err := m.orch.MarkOffPathEntered(m.sid, m.currentState); err != nil {
		// Persistence detail is an internal concern — log it instead of
		// leaking it into the player-facing transcript. The mode switch
		// already succeeded regardless of the log write.
		slog.Warn("off-path: log entry failed", "err", err, "sid", m.sid, "state", m.currentState)
	}
	return m, nil
}

// exitOffPath transitions off-path → on-path. Records OffPathExited.
func (m RootModel) exitOffPath() (tea.Model, tea.Cmd) {
	if m.mode != ModeOffPath {
		// Idempotent — already on-path; no-op silently.
		return m, nil
	}
	m.mode = ModeOnPath
	m.offPath, _ = m.offPath.Update(exitOffPathMsg{})
	m.location, _ = m.location.Update(offPathToggled{on: false})
	m.transcript, _ = m.transcript.Update(offPathToggled{on: false})
	m.prompt.Placeholder = "describe what you want, or /help"
	// Restore the on-path prefix glyph + violet bold style.
	setPromptPrefix(&m.prompt, promptPrefixOnPath)
	setPromptStyle(&m.prompt, promptStyle)
	m.transcript.AppendSystem("(returned to on-path mode)")
	if err := m.orch.MarkOffPathExited(m.sid, m.currentState); err != nil {
		// Internal persistence detail — log it rather than leak it to the
		// player. The mode switch back to on-path already succeeded.
		slog.Warn("off-path: log exit failed", "err", err, "sid", m.sid, "state", m.currentState)
	}
	return m, nil
}

// submitOffPath fires a single off-path turn against the orchestrator's
// AskOffPath helper. The user's question is appended to the transcript
// immediately so they get instant visual feedback; the answer arrives
// asynchronously via an offPathReplyMsg.
//
// While the call is in flight, ModeAwaitingLLM is set so the spinner
// renders and further input is gated. On reply, ModeOffPath is restored
// so the banner stays on screen.
func (m RootModel) submitOffPath(input string) (tea.Model, tea.Cmd) {
	m.lastInput = input
	m.transcript.AppendTurn(input, "")

	ctx, cancel := context.WithCancel(context.Background())
	m.inFlightCancel = cancel
	// We re-use ModeAwaitingLLM for the spinner / gating, but the eventual
	// reply restores ModeOffPath rather than ModeOnPath.
	m.mode = ModeAwaitingLLM
	m.pendingKind = pendingLLM

	orch := m.orch
	sid := m.sid
	cmd := func() tea.Msg {
		answer, err := orch.AskOffPath(ctx, sid, input)
		return offPathReplyFor(input, answer, err)
	}
	return m, tea.Batch(m.spinner.Tick, cmd)
}

// offPathReplyFor builds the reply message from an AskOffPath result. It
// surfaces the actual error rather than the turn ctx's ctx.Err(): the ctx may
// have been cancelled asynchronously after a genuine non-cancellation error was
// produced, and reporting ctx.Err() in that case would both mask the real
// failure and falsely label it a cancellation. The caller therefore must not
// consult ctx.Err() to classify — the err returned by the call is authoritative.
func offPathReplyFor(question, answer string, err error) offPathReplyMsg {
	if err != nil {
		return offPathReplyMsg{question: question, err: err}
	}
	return offPathReplyMsg{question: question, answer: answer, err: nil}
}

// handleOffPathReply processes the async reply from AskOffPath.
func (m RootModel) handleOffPathReply(msg offPathReplyMsg) (tea.Model, tea.Cmd) {
	if m.inFlightCancel != nil {
		m.inFlightCancel = nil
	}
	// Restore ModeOffPath (not ModeOnPath) — the user is still off the trail.
	m.mode = ModeOffPath
	if msg.err != nil {
		m.transcript.AppendError(msg.question, fmt.Sprintf("off-path: %s", userfacing.Error(msg.err)))
		return m, nil
	}
	answer := msg.answer
	if answer == "" {
		answer = "(no reply)"
	}
	m.transcript.AppendOffPathAnswer("", answer)
	return m, nil
}
