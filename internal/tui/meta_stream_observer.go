// Package tui — bridge between host.StreamSink (meta-mode oracle
// stream-json events) and the Bubble Tea message loop.
//
// Background: internal/host/oracle_runner.go runs `claude -p
// --output-format stream-json --verbose` and emits one structured
// event per JSONL line claude prints (system.init, assistant.text,
// assistant.tool_use, user.tool_result, system.api_retry, result).
// Those events are emitted via slog, but until this bridge existed the
// TUI's transcript stayed frozen on the "agent is thinking…" spinner
// until the terminal `result` event finally fired metaSendDoneMsg with
// the full assistant text.
//
// MetaStreamSink is the host.StreamSink implementation that turns
// each stream event into a MetaStreamMsg and posts it into the
// program's message channel. RootModel.Update interprets the msg and
// appends a muted "→ tool args" / "→ narration" line to the transcript
// so the user sees the agent acting live.
//
// Lifecycle pattern mirrors RoutingObserver: construct the sink with
// NewMetaStreamSink BEFORE tea.NewProgram (so RootModel can hold a
// reference), then call sink.Attach(prog) AFTER tea.NewProgram so the
// sink knows where to dispatch. Detach() clears the program reference
// (slog still fires; the sink just stops dispatching) — typically the
// caller defers it alongside p.Run().
//
// Concurrency contract: OnStreamEvent is called on the oracle
// subprocess's stdout-reader goroutine. We MUST NOT block — a stalled
// sink would back-pressure claude's stdout pipe and stall the entire
// LLM call. We forward via a fresh goroutine + tea.Program.Send
// (which is bounded but writes through a buffered channel that drops
// on Quit rather than blocking). Worst-case backpressure leaks
// goroutines that block on prog.Send; each one is small and exits as
// soon as the program drains. Bursty event streams from a noisy agent
// are bounded by claude's own emission rate, which is far below
// tea.Program's intake capacity in practice.
package tui

import (
	"context"
	"sync"

	tea "github.com/charmbracelet/bubbletea"

	"kitsoki/internal/host"
)

// MetaStreamMsg is one tea.Msg carrying a host.StreamEvent. Delivered
// from the oracle subprocess goroutine via tea.Program.Send so the
// existing RootModel.Update switch can pattern-match on it like every
// other in-flight message.
type MetaStreamMsg struct {
	Event host.StreamEvent
}

// MetaStreamSink is the host.StreamSink impl that forwards stream
// events into a *tea.Program's message channel. Allocate with
// NewMetaStreamSink BEFORE the program exists; bind via Attach once
// it does.
type MetaStreamSink struct {
	// mu guards prog. Held briefly on every OnStreamEvent / Attach /
	// Detach so a teardown can't race a mid-stream event.
	mu   sync.Mutex
	prog *tea.Program
}

// NewMetaStreamSink returns an unbound sink. Safe to thread through
// RootModel before tea.NewProgram exists; the sink does nothing until
// Attach is called. Returning the same nil-safe contract as
// host.WithStreamSink means callers can pass the value unguarded.
func NewMetaStreamSink() *MetaStreamSink {
	return &MetaStreamSink{}
}

// Attach binds the sink to prog. After Attach, every OnStreamEvent
// fans out a MetaStreamMsg via prog.Send. Safe to call from any
// goroutine. Passing nil is a no-op (useful from headless paths that
// want a sink-shaped value but no fanout).
func (s *MetaStreamSink) Attach(prog *tea.Program) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prog = prog
}

// Detach clears the bound program; subsequent OnStreamEvent calls
// become no-ops. Typically called from a defer alongside p.Run() so
// the sink stops dispatching when the program exits.
func (s *MetaStreamSink) Detach() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prog = nil
}

// OnStreamEvent implements host.StreamSink. Non-blocking by spawning
// a fresh goroutine for the Send call — tea.Program.Send writes
// through a buffered channel which either accepts the msg immediately
// or drops it on Quit. Either way the oracle subprocess reader
// goroutine is never stalled.
func (s *MetaStreamSink) OnStreamEvent(_ context.Context, ev host.StreamEvent) {
	if s == nil {
		return
	}
	s.mu.Lock()
	prog := s.prog
	s.mu.Unlock()
	if prog == nil {
		return
	}
	go prog.Send(MetaStreamMsg{Event: ev})
}

// Compile-time assertion that *MetaStreamSink satisfies host.StreamSink.
var _ host.StreamSink = (*MetaStreamSink)(nil)
