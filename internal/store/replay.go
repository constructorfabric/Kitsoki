package store

// replay.go reconstructs a journey from an event history. See doc.go for the
// package overview.
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
//	StorySnapshot/StoryChanged: noted — embedded story source, no fold effect.
//	All other kinds:   silently ignored (forward-compatible with future kinds).

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
	// History is the exact event slice BuildJourney replayed to produce this
	// JourneyState. Callers that need a post-hoc pass over the same rows
	// (e.g. deriving RecentTurns for the harness) should read this field
	// instead of re-querying the store — it is already the identical data
	// loadJourney read to build State/World/Turn, so a second store round
	// trip would be redundant work over the same rows.
	History History
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
			// Payload: {"set": {...}} or {"increment": {...}}.  Legacy traces
			// (before say was split into MachineSay) may also carry {"say": "..."};
			// the Say field is tolerated on decode and ignored here — narration
			// now lands as a MachineSay event.
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
				if p.OperationLocal {
					js.World.Set(k, coerceWorldVar(def, k, v))
				} else {
					js.World.SetDurable(k, coerceWorldVar(def, k, v))
				}
			}
			for k, delta := range p.Increment {
				next := toInt64Replay(js.World.Vars[k]) + int64(delta)
				if p.OperationLocal {
					js.World.Set(k, next)
				} else {
					js.World.SetDurable(k, next)
				}
			}
			// Say effects don't affect world state; skip.

		case OperationStarted:
			var p struct {
				OperationID string `json:"operation_id"`
				State       string `json:"state"`
			}
			if err := json.Unmarshal(ev.Payload, &p); err != nil {
				return nil, fmt.Errorf("replay: OperationStarted turn=%d seq=%d: %w", ev.Turn, ev.Seq, err)
			}
			js.World = js.World.StartOperation(p.OperationID, p.State)

		case OperationCommitted:
			var p struct {
				WorldPatch map[string]any `json:"world_patch"`
			}
			if err := json.Unmarshal(ev.Payload, &p); err != nil {
				return nil, fmt.Errorf("replay: OperationCommitted turn=%d seq=%d: %w", ev.Turn, ev.Seq, err)
			}
			patch := make(map[string]any, len(p.WorldPatch))
			for k, v := range p.WorldPatch {
				patch[k] = coerceWorldVar(def, k, v)
			}
			js.World = js.World.CommitOperation(patch, true)

		case OperationDraftPersisted:
			var p struct {
				DraftID string         `json:"draft_id"`
				World   map[string]any `json:"world"`
			}
			if err := json.Unmarshal(ev.Payload, &p); err != nil {
				return nil, fmt.Errorf("replay: OperationDraftPersisted turn=%d seq=%d: %w", ev.Turn, ev.Seq, err)
			}
			drafts := map[string]any{}
			if existing, ok := js.World.DurableVars()["operation_drafts"].(map[string]any); ok {
				for k, v := range existing {
					drafts[k] = v
				}
			}
			drafts[p.DraftID] = map[string]any{"id": p.DraftID, "world": p.World}
			js.World.SetDurable("operation_drafts", drafts)
			js.World = js.World.DiscardOperation()

		case OperationAbandoned:
			js.World = js.World.DiscardOperation()

		case OperationRunStarted, OperationRunPhaseStarted, OperationRunPhaseCompleted, OperationRunWaiting, OperationRunCompleted, OperationRunFailed:
			// Session-level operation-run lifecycle annotations. They describe
			// how a surface/driver should present the workflow, but they do not
			// mutate state or world.

		case MachineSay:
			// Operator narration: annotation only — say does not mutate
			// world or state. No-op on replay.

		case StateExited, StateEntered:
			// Noted for debugging but don't affect JourneyState directly.

		case IntentAccepted:
			// The intent was accepted; the transition events that follow will
			// update state/world. Nothing to do here.

		case ValidationFailed, GuardRejected:
			// Failed intents: state and world are unchanged. Skip.

		case TurnStarted, UserInputReceived, TurnEnded:
			// Orchestrator-level bookkeeping. No state/world change.

		case LLMToolCall, AgentStreamEvent:
			// LLM-layer event; no state/world change.

		case HostInvoked, HostDispatched, HostReturned:
			// Host side-effects are already materialized as EffectApplied events.
			// Nothing to re-apply here.

		case OffPathEntered, OffPathExited, OffPathQuestion, OffPathAnswer:
			// Off-path turns do not mutate world or state.
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

		case MachineError:
			// Annotation-only event recording a turn that aborted because
			// machine.Turn returned an error (e.g. a non-compiling effect
			// expression). No transition fired, so there is nothing to
			// re-apply; the event exists purely so the trace records why
			// the turn produced no state change.

		case StorySnapshot, StoryChanged:
			// The embedded story source (base snapshot + diffs). These make
			// the trace self-contained for re-compilation and branching, but
			// they do not mutate world or state — the journey fold ignores
			// them. The story is consumed by StoryAtTurn + app.LoadFromFiles
			// when reconstructing the machine, not here.

		case MiningProposalRaised, MiningProposalDecided, MiningPassRan:
			// The mining pass + surface-and-verdict records. They pin which
			// mined recipe proposed which structure and whether it stuck, and
			// which pass surfaced it, but carry no world/state effect of their
			// own — an accept's edit reaches the journey via the StoryChanged its
			// Reload emits. Folded as no-ops, exactly like GateDecided.

		default:
			// Forward-compatible: silently ignore unknown event kinds.
		}
	}

	js.History = history
	return js, nil
}

// BuildJourneyUntil is like BuildJourney but stops before folding any event
// whose turn number is >= beforeTurn. It is used by the rewind path to
// reconstruct the pre-dispatch state for a given turn without modifying the
// event log.
//
// Pass the same initialState/initialWorld as you would to BuildJourney — the
// same snapshot-relative contract applies: history should already be the events
// returned by LoadHistory (i.e. events after the latest snapshot), and
// initialState/initialWorld should be initialised from that snapshot.
func BuildJourneyUntil(def *app.AppDef, initialState app.StatePath, initialWorld world.World, history History, beforeTurn app.TurnNumber) (*JourneyState, error) {
	filtered := make(History, 0, len(history))
	for _, ev := range history {
		if ev.Turn < beforeTurn {
			filtered = append(filtered, ev)
		}
	}
	return BuildJourney(def, initialState, initialWorld, filtered)
}

// effectPayload is the JSON structure for an EffectApplied event payload.
type effectPayload struct {
	Set            map[string]any `json:"set,omitempty"`
	Increment      map[string]int `json:"increment,omitempty"`
	Say            string         `json:"say,omitempty"`
	OperationID    string         `json:"operation_id,omitempty"`
	OperationLocal bool           `json:"operation_local,omitempty"`
}

// cloneWorldVars returns a deep clone of a world.World.
func cloneWorldVars(w world.World) world.World {
	return w.Clone()
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
