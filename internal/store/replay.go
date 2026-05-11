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
				js.World.Vars[k] = v
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

		case HostInvoked, HostReturned:
			// Host side-effects are already materialized as EffectApplied events.
			// Nothing to re-apply here.

		case OffPathEntered, OffPathExited:
			// Off-path turns do not mutate world (§2.1). Skip.

		default:
			// Forward-compatible: silently ignore unknown event kinds.
		}
	}

	return js, nil
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
