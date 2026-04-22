package orchestrator

import (
	"log/slog"

	"hally/internal/app"
	"hally/internal/machine"
	"hally/internal/world"
)

// AppDef returns the app definition for this orchestrator.
func (o *Orchestrator) AppDef() *app.AppDef {
	return o.def
}

// Machine returns the underlying state machine for this orchestrator.
// Callers may use it with ComputeMenu to get enum-expanded menu entries.
func (o *Orchestrator) Machine() machine.Machine {
	return o.machine
}

// AllowedIntents returns the list of allowed intents for the given state.
func (o *Orchestrator) AllowedIntents(state app.StatePath, w world.World) []machine.AllowedIntent {
	return o.machine.AllowedIntents(state, w)
}

// CurrentWorld reconstructs the current world for a session by replaying the
// event history. Returns the initial world if the session has no events.
func (o *Orchestrator) CurrentWorld(sid app.SessionID) world.World {
	js, err := o.loadJourney(sid)
	if err != nil {
		return o.InitialWorld()
	}
	return js.World
}

// SetLogger replaces the logger used by this orchestrator. Primarily useful
// for the /trace TUI command that toggles live tracing mid-session.
func (o *Orchestrator) SetLogger(l *slog.Logger) {
	if l == nil {
		l = slog.Default()
	}
	o.mu.Lock()
	o.logger = l
	o.mu.Unlock()
}
