package orchestrator

import (
	"context"
	"fmt"
	"log/slog"

	"kitsoki/internal/app"
	"kitsoki/internal/intent"
	"kitsoki/internal/machine"
	"kitsoki/internal/store"
	"kitsoki/internal/trace"
	"kitsoki/internal/world"
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

// RunIntent submits an intent call directly to the machine, bypassing the LLM
// harness entirely. This is the programmatic dispatch path used by tooling and
// test consumers that already know the exact intent name and slots — for
// example, the flow runner in internal/testrunner (which backs `kitsoki test
// flows`) and the `kitsoki turn` CLI command.
//
// The method mirrors the full success path of Turn — load journey, run machine,
// dispatch host calls, persist events, stop the session listener on terminal
// transitions — but skips harness.RunTurn and the LLMToolCall event. Every
// other invariant (events, host dispatch, session listener lifecycle) is
// preserved exactly as in Turn.
//
// Guaranteed use cases:
//   - Flow-fixture turns declared as intent: (not input:) in YAML fixtures.
//   - Programmatic one-shot dispatches from `kitsoki turn` / `kitsoki test`.
//
// If you are writing user-facing conversation handling, use Turn instead so the
// LLM harness participates in routing.
func (o *Orchestrator) RunIntent(ctx context.Context, sid app.SessionID, intentName string, slots map[string]any) (*TurnOutcome, error) {
	// Serialise against handleJobTerminal — see Turn for rationale.
	sessMu := o.sessionLock(sid)
	sessMu.Lock()
	defer sessMu.Unlock()

	journey, err := o.loadJourney(sid)
	if err != nil {
		return nil, fmt.Errorf("orchestrator: RunIntent: load journey: %w", err)
	}

	turnNum := journey.Turn + 1
	tl := trace.NewTurnLogger(o.logger, sid, turnNum, journey.State)
	tl.Debug(ctx, trace.EvTurnStart,
		slog.String("intent", intentName),
		slog.String("mode", "run-intent"),
	)

	call := intent.IntentCall{
		Intent: intentName,
		Slots:  world.Slots(slots),
	}

	result, machineErr := o.machine.Turn(ctx, journey.State, journey.World, call)
	if machineErr != nil {
		return nil, fmt.Errorf("orchestrator: RunIntent: machine.Turn: %w", machineErr)
	}

	// Stamp turn number onto all machine events.
	for i := range result.Events {
		result.Events[i].Turn = turnNum
	}

	// Build a minimal prefix event (no LLMToolCall since no harness was involved).
	startEvent := newOrchestratorEvent(store.TurnStarted, map[string]any{
		"turn":   int64(turnNum),
		"input":  fmt.Sprintf("[intent] %s", intentName),
		"direct": true,
	}, turnNum)

	allowedNames := allowedNamesFromMachine(o.machine, journey.State, journey.World)

	if result.ValidationError != nil {
		ve := result.ValidationError
		if ve.Code == intent.ErrMissingSlots {
			o.mu.Lock()
			o.pending[sid] = &pendingClarify{
				intentName: call.Intent,
				slots:      slotsToMap(call.Slots),
			}
			o.mu.Unlock()
			clarification := ComputeClarification(o.def, journey.State, call.Intent, ve.MissingSlots)
			return &TurnOutcome{
				Mode:           ModeClarify,
				NewState:       journey.State,
				PendingIntent:  call.Intent,
				PendingSlots:   slotsToMap(call.Slots),
				SlotsNeeded:    clarification.Slots,
				AllowedIntents: allowedNames,
				TurnNumber:     turnNum,
			}, nil
		}

		failureEvents := append([]store.Event{startEvent}, result.Events...)
		endEvent := newOrchestratorEvent(store.TurnEnded, map[string]any{
			"outcome": "rejected",
			"code":    string(ve.Code),
		}, turnNum)
		failureEvents = append(failureEvents, endEvent)
		for i := range failureEvents {
			failureEvents[i].Turn = turnNum
		}
		if appendErr := o.store.AppendEvents(sid, failureEvents); appendErr != nil {
			return nil, fmt.Errorf("orchestrator: RunIntent: append failure events: %w", appendErr)
		}
		newAllowed := allowedNamesFromMachine(o.machine, journey.State, journey.World)
		return &TurnOutcome{
			Mode:           ModeRejected,
			NewState:       journey.State,
			Events:         failureEvents,
			AllowedIntents: newAllowed,
			GuardHint:      ve.GuardHint,
			ErrorCode:      ve.Code,
			ErrorMessage:   ve.Message,
			TurnNumber:     turnNum,
		}, nil
	}

	// Success path: dispatch host calls, persist events.
	hostEvents, hostWorld, hostView, hostRedirect, hostErr := o.dispatchHostCalls(ctx, sid, result.HostCalls, result.World, result.NewState)
	if hostErr != nil {
		tl.Debug(ctx, trace.EvHarnessError, slog.String("host_dispatch_error", hostErr.Error()))
	}
	if len(hostEvents) > 0 {
		result.Events = append(result.Events, hostEvents...)
		result.World = hostWorld
		if hostView != "" {
			result.View = hostView
		}
	}
	if hostRedirect != "" {
		result.NewState = hostRedirect
	}

	successEvents := append([]store.Event{startEvent}, result.Events...)
	endEvent := newOrchestratorEvent(store.TurnEnded, map[string]any{
		"outcome": "transitioned",
		"to":      string(result.NewState),
	}, turnNum)
	successEvents = append(successEvents, endEvent)
	for i := range successEvents {
		successEvents[i].Turn = turnNum
	}

	if appendErr := o.store.AppendEvents(sid, successEvents); appendErr != nil {
		return nil, fmt.Errorf("orchestrator: RunIntent: append events: %w", appendErr)
	}

	tl.Debug(ctx, trace.EvTurnPersisted,
		slog.Int("count", len(successEvents)),
		slog.String("outcome", "transitioned"),
	)

	// Clear any pending clarification.
	o.mu.Lock()
	delete(o.pending, sid)
	o.mu.Unlock()

	newAllowed := allowedNamesFromMachine(o.machine, result.NewState, result.World)

	mode := ModeTransitioned
	newStateDef := lookupStateByPath(o.def, result.NewState)
	if newStateDef != nil && newStateDef.Terminal {
		mode = ModeCompleted
		o.stopSessionListener(sid)
	}

	tl.Debug(ctx, trace.EvTurnDone,
		slog.String("mode", mode.String()),
		slog.Int("view_bytes", len(result.View)),
		slog.String("new_state", string(result.NewState)),
	)

	return &TurnOutcome{
		Mode:           mode,
		View:           result.View,
		NewState:       result.NewState,
		Events:         successEvents,
		AllowedIntents: newAllowed,
		TurnNumber:     turnNum,
	}, nil
}
