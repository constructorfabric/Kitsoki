// Package store — replay.go: journey reconstruction from an event history.
//
// BuildJourney folds an ordered History into a JourneyState by interpreting
// each event according to its kind. The determinism contract: if you feed
// the same intents through Machine.Turn and collect the same events, then
// BuildJourney(history) must return the same (state, world) as the machine
// produced live.
//
// Event-kind → action mapping:
//
//	TransitionApplied: update state to the "to" path.
//	EffectApplied:     apply set/increment world mutations.
//	StateExited:       noted, no world change.
//	StateEntered:      noted, no world change.
//	IntentAccepted:    noted, no state/world change.
//	ValidationFailed:  noted, state/world unchanged (transition did not fire).
//	GuardRejected:     noted, state/world unchanged.
//	TurnStarted:       noted (used by orchestrator, not machine core).
//	TurnEnded:         noted.
//	All other kinds:   silently ignored (forward-compatible with future kinds).
package store

import (
	"encoding/json"
	"fmt"

	"kitsoki/internal/app"
	"kitsoki/internal/world"
)

// JourneyState is the reconstructed journey after replaying an event history.
// It is equivalent to the (NewState, World) pair returned by Machine.Turn
// at the end of the last successful turn.
type JourneyState struct {
	// State is the current active state path.
	State app.StatePath
	// World is the current world snapshot.
	World world.World
	// Turn is the highest turn number seen in the history.
	Turn app.TurnNumber
}

// BuildJourney reconstructs the journey state by replaying events in order.
// It starts from initialState with the world initialised from the app's schema
// defaults, then applies each event in history.
//
// The function is deterministic: the same event history always produces the
// same JourneyState.
func BuildJourney(def *app.AppDef, initialState app.StatePath, initialWorld world.World, history History) (*JourneyState, error) {
	js := &JourneyState{
		State: initialState,
		World: cloneWorldVars(initialWorld),
	}

	for _, ev := range history {
		// js.Turn tracks the highest turn number used in the session,
		// including off-path side-channel events. The off-path appender
		// allocates fresh turn numbers via max(existing)+1 so its events
		// don't collide with foreground events at append time; if we then
		// excluded them from js.Turn, the next foreground Turn() would
		// reuse a turn number an off-path event already claimed and hit
		// a UNIQUE (session_id, turn, seq) PK collision on insert.
		if ev.Turn > js.Turn {
			js.Turn = ev.Turn
		}

		switch ev.Kind {
		case TransitionApplied:
			// Payload: {"from": "...", "to": "...", "intent": "..."}
			var p struct {
				To string `json:"to"`
			}
			if err := json.Unmarshal(ev.Payload, &p); err != nil {
				return nil, fmt.Errorf("replay: TransitionApplied turn=%d seq=%d: %w", ev.Turn, ev.Seq, err)
			}
			if p.To != "" {
				js.State = app.StatePath(p.To)
			}

		case EffectApplied:
			// Payload: {"set": {...}} or {"increment": {...}} or {"say": "..."}
			var p effectPayload
			if err := json.Unmarshal(ev.Payload, &p); err != nil {
				return nil, fmt.Errorf("replay: EffectApplied turn=%d seq=%d: %w", ev.Turn, ev.Seq, err)
			}
			for k, v := range p.Set {
				// JSON unmarshal of integers into `any` produces float64.
				// When the app schema declares `type: int` (or `bool`)
				// for this var, coerce so downstream expr-lang guards
				// like `world.x % 100` work against the same Go types
				// the machine would have written at run-time (int64).
				// Without this, fixtures whose initial_world feeds an
				// int key through the JSON event log would see a
				// `float64 % int` runtime error when a guard fires.
				js.World.Vars[k] = coerceWorldVar(def, k, v)
			}
			for k, delta := range p.Increment {
				js.World.Vars[k] = toInt64Replay(js.World.Vars[k]) + int64(delta)
			}
			// Say effects don't affect world state; skip.

		case StateExited, StateEntered:
			// Noted for debugging but don't affect JourneyState directly.

		case IntentAccepted:
			// The intent was accepted; the transition events that follow will
			// update state/world. Nothing to do here.

		case ValidationFailed, GuardRejected:
			// Failed intents: state and world are unchanged. Skip.

		case TurnStarted, TurnEnded:
			// Orchestrator-level bookkeeping. No state/world change.

		case LLMCalled, LLMToolCall:
			// LLM-layer events; no state/world change.

		case HostInvoked, HostDispatched, HostReturned:
			// Host side-effects are already materialized as EffectApplied events.
			// Nothing to re-apply here.

		case OffPathEntered, OffPathExited, OffPathQuestion, OffPathAnswer:
			// Off-path turns do not mutate world or state (§7.7, §11).
			// All four kinds are annotation-only for the replay path.

		case TimeoutFired:
			// Annotation-only event.  The accompanying TransitionApplied
			// already updates state; nothing to do here.

		case HarnessError:
			// Annotation-only event recording an orchestrator-side
			// dispatch-loop failure (e.g. settlePostBindEmits depth cap,
			// DispatchPostBindEmits eval error).  Replay state is
			// authoritative via the preceding TransitionApplied events;
			// HarnessError surfaces the why-the-state-stopped story for
			// operators reading the journal.

		default:
			// Forward-compatible: silently ignore unknown event kinds.
		}
	}

	return js, nil
}

// isOffPathEvent reports whether kind is one of the four off-path event
// kinds that fire on the orchestrator's side-channel rather than as part
// of a foreground turn. These events must NOT advance js.Turn during
// replay — see the BuildJourney comment.
func isOffPathEvent(kind EventKind) bool {
	switch kind {
	case OffPathEntered, OffPathExited, OffPathQuestion, OffPathAnswer:
		return true
	}
	return false
}

// effectPayload is the JSON structure for an EffectApplied event payload.
type effectPayload struct {
	Set       map[string]any `json:"set,omitempty"`
	Increment map[string]int `json:"increment,omitempty"`
	Say       string         `json:"say,omitempty"`
}

// cloneWorldVars returns a deep clone of a world.World.
func cloneWorldVars(w world.World) world.World {
	nw := world.World{Vars: make(map[string]any, len(w.Vars))}
	for k, v := range w.Vars {
		nw.Vars[k] = v
	}
	return nw
}

// toInt64Replay converts a world variable value to int64 for increment operations.
func toInt64Replay(v any) int64 {
	switch x := v.(type) {
	case int:
		return int64(x)
	case int64:
		return x
	case float64:
		return int64(x)
	}
	return 0
}

// coerceWorldVar applies app-schema-aware type coercion to a value
// unmarshalled from a replayed EffectApplied event. JSON encoding/
// decoding through `any` always produces float64 for numeric values
// and the standard string/bool types for the others; for `type: int`
// world vars we round-trip through int64 so downstream expr-lang
// operations (e.g. `world.x % 100`) see an integral Go type.
//
// Vars not declared in the app schema (e.g. test-only keys) pass
// through unchanged — this keeps the function safe to call for every
// EffectApplied set entry without breaking off-schema usage.
func coerceWorldVar(def *app.AppDef, key string, v any) any {
	if def == nil {
		return v
	}
	vd, ok := def.World[key]
	if !ok {
		return v
	}
	switch vd.Type {
	case "int":
		switch x := v.(type) {
		case float64:
			return int64(x)
		case float32:
			return int64(x)
		case int:
			return int64(x)
		case int32:
			return int64(x)
		case int64:
			return x
		}
	}
	return v
}
