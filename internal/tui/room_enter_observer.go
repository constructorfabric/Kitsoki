// Package tui — bridge between orchestrator.RoomEnterSink and the
// Bubble Tea message loop.
//
// The orchestrator calls RoomEnterSink.OnRoomEnter the moment a turn
// transitions into a new room (top-level state change), BEFORE the
// on_enter chain's host calls dispatch. We turn that callback into a
// roomEnteredMsg posted via tea.Program.Send so the live TUI can
// paint the room's banner above the tool-call breadcrumbs that are
// about to stream in (oracle, Bash, Read, etc.).
//
// Lifecycle pattern mirrors MetaStreamSink: construct with
// NewRoomEnterSink BEFORE tea.NewProgram, then Attach(prog) after
// the program exists. Detach() clears the binding (the sink keeps
// receiving callbacks; they just become no-ops). Concurrency
// contract: OnRoomEnter is called on the orchestrator's turn
// goroutine — we MUST NOT block, so we forward via a fresh goroutine
// + tea.Program.Send (bounded channel, drops on Quit, never stalls
// the turn).
package tui

import (
	"sync"

	tea "github.com/charmbracelet/bubbletea"

	"kitsoki/internal/app"
	"kitsoki/internal/orchestrator"
)

// roomEnteredMsg is the tea.Msg carrying a pre-rendered room banner.
// Delivered from the orchestrator turn goroutine via tea.Program.Send
// so the existing RootModel.Update switch can pattern-match on it
// like every other mid-turn message.
type roomEnteredMsg struct {
	State  app.StatePath
	Banner string
}

// RoomEnterSink is the orchestrator.RoomEnterSink impl that forwards
// room-entry events into a *tea.Program's message channel. Allocate
// BEFORE the program exists; bind via Attach once it does.
type RoomEnterSink struct {
	mu   sync.Mutex
	prog *tea.Program
}

// NewRoomEnterSink returns an unbound sink. Safe to thread through
// the orchestrator wiring before tea.NewProgram exists; the sink
// does nothing until Attach is called.
func NewRoomEnterSink() *RoomEnterSink {
	return &RoomEnterSink{}
}

// Attach binds the sink to prog. After Attach, every OnRoomEnter
// fans out a roomEnteredMsg via prog.Send. Safe to call from any
// goroutine. Passing nil is a no-op.
func (s *RoomEnterSink) Attach(prog *tea.Program) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prog = prog
}

// Detach clears the bound program; subsequent OnRoomEnter calls
// become no-ops. Typically called from a defer alongside p.Run().
func (s *RoomEnterSink) Detach() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prog = nil
}

// OnRoomEnter implements orchestrator.RoomEnterSink. Non-blocking by
// spawning a fresh goroutine for the Send call — tea.Program.Send
// writes through a buffered channel which either accepts the msg
// immediately or drops it on Quit. Either way the orchestrator's turn
// goroutine is never stalled waiting on the TUI.
func (s *RoomEnterSink) OnRoomEnter(state app.StatePath, banner string) {
	if s == nil {
		return
	}
	if banner == "" {
		return
	}
	s.mu.Lock()
	prog := s.prog
	s.mu.Unlock()
	if prog == nil {
		return
	}
	go prog.Send(roomEnteredMsg{State: state, Banner: banner})
}

// Compile-time assertion that *RoomEnterSink satisfies the
// orchestrator interface.
var _ orchestrator.RoomEnterSink = (*RoomEnterSink)(nil)
