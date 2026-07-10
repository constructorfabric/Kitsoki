package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/machine"
	"kitsoki/internal/store"
	"kitsoki/internal/trace"
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

// Reload re-reads the app definition, rebuilds the machine, and
// atomically swaps both into the orchestrator. The host registry's
// allow-list is re-validated against the new definition; if the new
// app declares hosts the registry can't satisfy, Reload returns an
// error and leaves the orchestrator untouched.
//
// The fresh definition comes from the injected reloader closure
// (WithReloader) when one is set — this is how a config-synthesized
// rung-0/1 root re-synthesizes from .kitsoki.yaml on /reload, since it
// has no app.yaml on disk to re-read. When no reloader is injected,
// Reload falls back to app.Load(appPath), the historical file-backed
// path. appPath is ignored when a reloader is set, so a rung-1 edit and
// a rung-2 edit travel the identical Reload + RerunOnEnter path.
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
	var def *app.AppDef
	var err error
	if o.reloader != nil {
		def, err = o.reloader()
		if err != nil {
			return nil, fmt.Errorf("orchestrator.Reload: reloader: %w", err)
		}
	} else {
		def, err = app.Load(appPath)
		if err != nil {
			return nil, fmt.Errorf("orchestrator.Reload: load %q: %w", appPath, err)
		}
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

// RerunOnEnter re-fires the current state's on_enter chain against the
// session's live world, dispatches any host calls it emits, and returns
// a TurnOutcome with the freshly rendered view. This is the
// "/reload" partner of Reload: Reload swaps the app definition, then
// RerunOnEnter replays the entered state's actions so view-template
// edits, on_enter additions, or agent-prompt changes take effect
// without the user having to re-type an intent.
//
// Caveats by design:
//
//   - It re-fires the on_enter chain as-is. If the chain posts to a
//     transport, calls an agent, or otherwise has external side
//     effects, those side effects will repeat. The user explicitly
//     asked for this ("redo whatever actions") — the trade-off is
//     spelled out in `/reload`'s slash help.
//   - When the current state has no on_enter, RerunOnEnter still
//     produces a TurnOutcome with a freshly rendered view so the
//     caller's render pipeline is uniform.
//   - The synthetic turn is logged as kind="reload" in the TurnStarted
//     event so trace consumers can distinguish a reload from a user
//     turn.
//
// Not safe to call concurrently with Turn / SubmitDirect / Reload /
// other RerunOnEnter for the same session — uses the per-session
// mutex.
func (o *Orchestrator) RerunOnEnter(ctx context.Context, sid app.SessionID) (*TurnOutcome, error) {
	return o.RerunOnEnterWithOptions(ctx, sid, RerunOnEnterOptions{})
}

// RerunOnEnterOptions tunes the `/reload` on_enter replay. The zero value is
// the normal safe reload path.
type RerunOnEnterOptions struct {
	ForceOnce bool
}

func (o *Orchestrator) RerunOnEnterWithOptions(ctx context.Context, sid app.SessionID, opts RerunOnEnterOptions) (*TurnOutcome, error) {
	sessMu := o.sessionLock(sid)
	sessMu.Lock()
	defer sessMu.Unlock()

	journey, err := o.loadJourney(sid)
	if err != nil {
		return nil, fmt.Errorf("orchestrator.RerunOnEnter: load journey: %w", err)
	}

	turnNum := journey.Turn + 1
	tl := trace.NewTurnLogger(o.logger, sid, turnNum, journey.State)
	tl.Debug(ctx, trace.EvTurnStart,
		slog.String("intent", ""),
		slog.String("mode", "reload"),
		slog.Bool("force_once", opts.ForceOnce),
	)

	currentState := journey.State
	currentWorld := journey.World

	state := lookupStateByPath(o.def, currentState)

	var events []store.Event
	if state != nil && len(state.OnEnter) > 0 {
		resolved, newWorld, hostCalls, _, effEvents, runErr := o.machine.RunEffectsAndStateWithOptions(ctx, currentState, currentWorld, state.OnEnter, machine.RunEffectsOptions{
			ForceOnce: opts.ForceOnce,
		})
		if runErr != nil {
			return nil, fmt.Errorf("orchestrator.RerunOnEnter: run on_enter for %q: %w", currentState, runErr)
		}
		events = append(events, effEvents...)
		currentState = resolved
		currentWorld = newWorld

		if len(hostCalls) > 0 {
			hostEvents, hostWorld, _, hostRedirect, hostErr := o.dispatchHostCalls(ctx, sid, hostCalls, currentWorld, currentState)
			if hostErr != nil {
				tl.Debug(ctx, trace.EvHarnessError, slog.String("host_dispatch_error", hostErr.Error()))
			}
			events = append(events, hostEvents...)
			currentWorld = hostWorld
			if hostRedirect != "" {
				currentState = hostRedirect
			}
		}
	}

	// Always re-render so callers see template / view edits even when
	// the state has no on_enter chain.
	view, rErr := o.machine.RenderState(currentState, currentWorld)
	if rErr != nil {
		return nil, fmt.Errorf("orchestrator.RerunOnEnter: render state %q: %w", currentState, rErr)
	}

	startEvent := newOrchestratorEvent(store.TurnStarted, map[string]any{
		"turn":       int64(turnNum),
		"kind":       "reload",
		"state":      string(currentState),
		"force_once": opts.ForceOnce,
	}, turnNum)
	endEvent := newOrchestratorEvent(store.TurnEnded, map[string]any{
		"outcome":    "reloaded",
		"to":         string(currentState),
		"force_once": opts.ForceOnce,
	}, turnNum)

	successEvents := append([]store.Event{startEvent}, events...)
	successEvents = append(successEvents, endEvent)
	for i := range successEvents {
		successEvents[i].Turn = turnNum
	}

	jEntries := journalEntriesForEvents(sid, turnNum, time.Now(), successEvents,
		journey.World, currentWorld, view, currentState, "")
	if appendErr := o.appendEventsAndJournal(sid, successEvents, jEntries); appendErr != nil {
		return nil, fmt.Errorf("orchestrator.RerunOnEnter: append events: %w", appendErr)
	}

	tl.Debug(ctx, trace.EvTurnPersisted,
		slog.Int("count", len(successEvents)),
		slog.String("outcome", "reloaded"),
	)

	allowedNames := allowedNamesFromMachine(o.machine, currentState, currentWorld)

	mode := ModeTransitioned
	if def := lookupStateByPath(o.def, currentState); def != nil && def.Terminal {
		mode = ModeCompleted
	}

	return &TurnOutcome{
		Mode:           mode,
		View:           view,
		NewState:       currentState,
		Events:         successEvents,
		AllowedIntents: allowedNames,
		TurnNumber:     turnNum,
	}, nil
}
