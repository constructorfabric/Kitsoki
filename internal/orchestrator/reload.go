package orchestrator

import (
	"fmt"

	"hally/internal/app"
	"hally/internal/machine"
)

// ReloadResult reports the outcome of a Reload call. It tells the
// caller whether the previous state path still resolves so the TUI
// can decide whether to keep the user where they were or fall back
// to the entry state.
type ReloadResult struct {
	// Def is the freshly loaded app definition.
	Def *app.AppDef
	// PrevStateExists is true when prevState (passed to Reload) is
	// still a declared state in the new definition.
	PrevStateExists bool
}

// Reload re-reads the app YAML at appPath, rebuilds the machine, and
// atomically swaps both into the orchestrator. The host registry's
// allow-list is re-validated against the new definition; if the new
// app declares hosts the registry can't satisfy, Reload returns an
// error and leaves the orchestrator untouched.
//
// Reload does not rebuild the harness. The harness owns its own copy
// of the app definition (used to build the system prompt); after a
// reload its system prompt may be stale until the next session start.
// In practice the user iterates via the menu (deterministic routing)
// so this rarely matters.
//
// Reload is **not** safe to call concurrently with Turn, SubmitDirect,
// OneShot, or ContinueTurn. The TUI guards this by ensuring its
// edit-mode and turn-in-flight modes are mutually exclusive.
func (o *Orchestrator) Reload(appPath string, prevState app.StatePath) (*ReloadResult, error) {
	def, err := app.Load(appPath)
	if err != nil {
		return nil, fmt.Errorf("orchestrator.Reload: load %q: %w", appPath, err)
	}

	m, err := machine.New(def)
	if err != nil {
		return nil, fmt.Errorf("orchestrator.Reload: build machine: %w", err)
	}

	if o.hosts != nil {
		if err := o.hosts.ValidateAllowList(def.Hosts); err != nil {
			return nil, fmt.Errorf("orchestrator.Reload: validate hosts: %w", err)
		}
	}

	prevExists := stateExists(def, prevState)

	o.mu.Lock()
	o.def = def
	o.machine = m
	// Push the new def into the harness too, so the LLM-router's system
	// prompt reflects new states/intents on the very next turn.
	// Harnesses that don't build their prompt from def (Replay, Recording)
	// don't implement defSetter and are silently skipped.
	if ds, ok := o.harness.(defSetter); ok {
		ds.SetAppDef(def)
	}
	o.mu.Unlock()

	return &ReloadResult{Def: def, PrevStateExists: prevExists}, nil
}

// defSetter is the interface a harness implements to opt into hot
// app-definition swaps. ClaudeCLIHarness and LiveHarness do; Replay and
// Recording don't (they have no system prompt to rebuild).
type defSetter interface {
	SetAppDef(*app.AppDef)
}

// stateExists reports whether a (possibly dotted) state path resolves
// against the app definition's nested state tree.
func stateExists(def *app.AppDef, path app.StatePath) bool {
	if path == "" {
		return false
	}
	target := string(path)
	for id, s := range def.States {
		if id == target {
			return true
		}
		if hasNestedState(s, id, target) {
			return true
		}
	}
	return false
}

func hasNestedState(s *app.State, prefix, target string) bool {
	for childID, child := range s.States {
		candidate := prefix + "." + childID
		if candidate == target {
			return true
		}
		if hasNestedState(child, candidate, target) {
			return true
		}
	}
	return false
}
