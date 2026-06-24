// operator_prompter.go — the TUI surface's host.OperatorPrompter.
//
// When a dispatched `claude -p` agent agent forwards an AskUserQuestion into
// kitsoki (see internal/host/operator_ask_bridge.go), the host layer pulls the
// in-context OperatorPrompter and calls Ask, which BLOCKS the turn goroutine
// until the operator answers. On the web surface that prompter surfaces the
// question over SSE and blocks on a channel (internal/runstatus/server/
// operator_questions.go); the TUI equivalent is TUIOperatorPrompter:
//
//	Ask(questions)                       [agent subprocess reader goroutine,
//	  ├─ prog.Send(operatorQuestionMsg)   off the bubbletea Update loop]
//	  │     → RootModel.Update opens the inline question widget
//	  └─ block on answerCh until the operator commits (or ctx is cancelled)
//
// Lifecycle mirrors MetaStreamSink exactly: construct with
// NewTUIOperatorPrompter BEFORE tea.NewProgram (so RootModel can hold a
// reference and inject it into each turn ctx), then Attach(prog) once the
// program exists so Ask knows where to dispatch. Detach() clears the program
// reference; an Ask with no bound program degrades to "no operator" so the
// agent is told to proceed on its own (matching the headless posture).
//
// Concurrency contract: Ask is called on the agent subprocess's listener
// goroutine, NOT the bubbletea Update loop. It blocks — that is the whole point
// (the agent waits for a human) — but it never blocks Update: the question is
// handed off via prog.Send and the answer comes back over a buffered channel.
package tui

import (
	"context"
	"errors"
	"sync"

	tea "github.com/charmbracelet/bubbletea"

	"kitsoki/internal/host"
)

// operatorQuestionMsg carries a forwarded question batch into the bubbletea
// program. answerCh is buffered (cap 1) so the Update-loop handler can send the
// operator's answer without blocking, and Ask drains it. A nil answer on the
// channel signals operator cancellation (Esc) — the host maps that to an
// LLM-visible "proceed using your best judgement".
type operatorQuestionMsg struct {
	questions []host.OperatorQuestion
	answerCh  chan map[string]any
}

// TUIOperatorPrompter implements host.OperatorPrompter for the TUI. Allocate
// with NewTUIOperatorPrompter BEFORE the program exists; bind via Attach once
// it does. Until Attach (or after Detach) Ask reports no operator so the agent
// proceeds unaided.
type TUIOperatorPrompter struct {
	// mu guards prog. Held briefly on every Ask / Attach / Detach so a
	// teardown can't race a mid-turn question.
	mu   sync.Mutex
	prog *tea.Program
}

// NewTUIOperatorPrompter returns an unbound prompter. Safe to thread through
// RootModel before tea.NewProgram exists; it surfaces nothing until Attach.
func NewTUIOperatorPrompter() *TUIOperatorPrompter {
	return &TUIOperatorPrompter{}
}

// Attach binds the prompter to prog. After Attach, every Ask dispatches an
// operatorQuestionMsg via prog.Send. Safe to call from any goroutine; passing
// nil is a no-op.
func (p *TUIOperatorPrompter) Attach(prog *tea.Program) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prog = prog
}

// Detach clears the bound program; subsequent Ask calls report no operator.
// Typically deferred alongside p.Run() so a forwarded question after the
// program exits resolves cleanly rather than stranding the agent.
func (p *TUIOperatorPrompter) Detach() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prog = nil
}

// errNoOperator is returned when Ask fires with no bound program. The host
// bridge logs it and tells the agent to proceed on its own — the same outcome
// as the headless (no-prompter) path, just reached one layer later.
var errNoOperator = errors.New("tui operator prompter: no program attached")

// Ask implements host.OperatorPrompter. It hands the question batch to the
// bubbletea program (which opens the inline question widget) and blocks until
// the operator commits an answer or ctx is cancelled. The sessionID argument is
// unused: the TUI hosts a single live session, and the question is surfaced in
// that session's own transcript.
func (p *TUIOperatorPrompter) Ask(ctx context.Context, _ string, questions []host.OperatorQuestion) (map[string]any, error) {
	p.mu.Lock()
	prog := p.prog
	p.mu.Unlock()
	if prog == nil {
		return nil, errNoOperator
	}

	// Buffered (cap 1) so the Update handler's send never blocks the loop.
	answerCh := make(chan map[string]any, 1)
	prog.Send(operatorQuestionMsg{questions: questions, answerCh: answerCh})

	select {
	case answers := <-answerCh:
		return answers, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Compile-time assertion that *TUIOperatorPrompter satisfies the seam.
var _ host.OperatorPrompter = (*TUIOperatorPrompter)(nil)
