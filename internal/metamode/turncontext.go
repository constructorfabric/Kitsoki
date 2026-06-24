package metamode

import (
	"kitsoki/internal/app"
	"kitsoki/internal/machine"
	"kitsoki/internal/world"
)

// ContextSource is the narrow orchestrator surface BuildTurnContext reads to
// fill the per-turn ambient context (the resolved world, the rendered view, and
// the imported-manifest watch set). *orchestrator.Orchestrator satisfies it
// directly; tests can supply a stub.
//
// It exists so the TUI and the `kitsoki web` meta driver build the agent's
// [context] preamble from one code path — historically the web driver was a
// no-LLM stub that left World / RenderedView / TracePath empty, so its agents
// flew blind relative to the TUI's. There is no reason for the two surfaces to
// differ; this is the seam that keeps them identical.
type ContextSource interface {
	// CurrentWorld returns the resolved world snapshot for the session.
	CurrentWorld(sid app.SessionID) world.World
	// Machine renders the current state's view (RenderState).
	Machine() machine.Machine
	// AppDef supplies LoadedManifests (the imported-story watch set).
	AppDef() *app.AppDef
}

// BuildTurnContext assembles the TurnContext threaded into Controller.Send from
// the live orchestrator state. tracePath is resolved by the caller (the trace
// plumbing differs between surfaces — the TUI may dump a ring buffer while web
// streams via slog) and passed through verbatim; everything else is derived
// here so both surfaces stay in lockstep.
//
// RenderState errors are swallowed — a missing view leaves the field empty
// rather than aborting the turn (the preamble renderer omits empty fields).
func BuildTurnContext(src ContextSource, sid app.SessionID, state app.StatePath, appFile, tracePath string) TurnContext {
	w := src.CurrentWorld(sid)
	view, _ := src.Machine().RenderState(state, w)

	var imported []string
	if def := src.AppDef(); def != nil {
		imported = append(imported, def.LoadedManifests...)
	}

	return TurnContext{
		StatePath:             string(state),
		AppFile:               appFile,
		RenderedView:          view,
		World:                 w.Vars,
		TracePath:             tracePath,
		ImportedManifestPaths: imported,
	}
}
