// Package orchestrator implements the turn-loop brain (§4.2).
// It is the ONLY component that calls store.AppendEvents.
// The machine is pure (no I/O); the harness may call the LLM.
package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"hally/internal/app"
	"hally/internal/harness"
	"hally/internal/host"
	"hally/internal/intent"
	"hally/internal/machine"
	"hally/internal/store"
	"hally/internal/trace"
	"hally/internal/world"
)

// pendingClarify holds the in-flight slot-fill state while the TUI
// is collecting missing slots from the user (§5.3 option a: in-memory).
type pendingClarify struct {
	intentName string
	slots      map[string]any // already-collected slots
}

// Orchestrator drives a single session from raw input to applied events.
type Orchestrator struct {
	def     *app.AppDef
	machine machine.Machine
	store   store.Store
	harness harness.Harness
	hosts   *host.Registry
	logger  *slog.Logger

	// pending tracks in-flight clarifications keyed by session ID.
	mu      sync.Mutex
	pending map[app.SessionID]*pendingClarify
}

// New creates an Orchestrator.
func New(def *app.AppDef, m machine.Machine, s store.Store, h harness.Harness, opts ...Option) *Orchestrator {
	o := &Orchestrator{
		def:     def,
		machine: m,
		store:   s,
		harness: h,
		logger:  slog.Default(),
		pending: make(map[app.SessionID]*pendingClarify),
	}
	for _, opt := range opts {
		opt(o)
	}
	return o
}

// Option is a functional option for Orchestrator.
type Option func(*Orchestrator)

// WithLogger sets the logger used for structured tracing.
func WithLogger(l *slog.Logger) Option {
	return func(o *Orchestrator) {
		if l != nil {
			o.logger = l
		}
	}
}

// WithHostRegistry enables dispatch of machine HostCalls. When unset, host
// invocations collected by the machine are ignored (the event log still records
// HostInvoked but no side-effect fires). Enable this for live sessions;
// deterministic flow tests typically leave it off.
func WithHostRegistry(r *host.Registry) Option {
	return func(o *Orchestrator) {
		o.hosts = r
	}
}

// NewSession opens a session in the store and returns its ID.
func (o *Orchestrator) NewSession(ctx context.Context) (app.SessionID, error) {
	return o.store.CreateSession(ctx, o.def)
}

// Turn processes one user utterance and returns a TurnOutcome.
// Steps (§4.2):
//  1. Load journey (state + world) from the store.
//  2. Call harness.RunTurn → mcp.CallToolParams.
//  3. Parse the intent call from the params.
//  4. Call machine.Turn.
//  5. React to the result: persist events and build the outcome.
func (o *Orchestrator) Turn(ctx context.Context, sid app.SessionID, input string) (*TurnOutcome, error) {
	// 1. Reconstruct the journey from the event log.
	journey, err := o.loadJourney(sid)
	if err != nil {
		return nil, fmt.Errorf("orchestrator: load journey: %w", err)
	}

	// 2. Build TurnInput for the harness.
	allowedIntents := o.machine.AllowedIntents(journey.State, journey.World)
	allowedNames := make([]string, len(allowedIntents))
	for i, ai := range allowedIntents {
		allowedNames[i] = ai.Name
	}

	turnNum := journey.Turn + 1
	tl := trace.NewTurnLogger(o.logger, sid, turnNum, journey.State)

	// Emit turn.start.
	tl.Debug(ctx, trace.EvTurnStart,
		slog.String("input", input),
		slog.String("mode", "normal"),
	)

	in := harness.TurnInput{
		SessionID:      app.SessionID(sid),
		TurnNumber:     turnNum,
		UserText:       input,
		StatePath:      journey.State,
		World:          journey.World,
		AllowedIntents: allowedNames,
	}

	// Append TurnStarted event.
	startEvent := newOrchestratorEvent(store.TurnStarted, map[string]any{
		"turn":  int64(turnNum),
		"input": input,
	}, turnNum)

	// 3. Call harness.
	harnessStart := time.Now()
	params, err := o.harness.RunTurn(ctx, in)
	harnessDur := time.Since(harnessStart)
	if err != nil {
		tl.Debug(ctx, trace.EvTurnRouted,
			slog.Duration("dur", harnessDur),
			slog.String("outcome", "error"),
			slog.String("error", err.Error()),
		)
		return nil, fmt.Errorf("orchestrator: harness.RunTurn: %w", err)
	}
	tl.Debug(ctx, trace.EvTurnRouted,
		slog.Duration("dur", harnessDur),
		slog.String("outcome", "hit"),
		slog.String("intent", extractIntentName(params)),
	)

	// Append LLMCalled/LLMToolCall events.
	llmEvent := newOrchestratorEvent(store.LLMToolCall, map[string]any{
		"tool":   params.Name,
		"intent": extractIntentName(params),
	}, turnNum)

	// 4. Parse the intent call from params.
	call, parseErr := parseIntentCall(params)
	if parseErr != nil {
		return nil, fmt.Errorf("orchestrator: parse intent call: %w", parseErr)
	}

	// 5. Run the machine.
	result, machineErr := o.machine.Turn(ctx, journey.State, journey.World, call)
	if machineErr != nil {
		return nil, fmt.Errorf("orchestrator: machine.Turn: %w", machineErr)
	}

	// Trace machine step.
	tl.Debug(ctx, trace.EvTurnStepped,
		slog.String("intent", call.Intent),
		slog.Any("slots", slotsToMap(call.Slots)),
	)

	// Stamp the turn number onto all machine events.
	for i := range result.Events {
		result.Events[i].Turn = turnNum
	}

	// Build a prefix of orchestrator-level events.
	prefix := []store.Event{startEvent, llmEvent}

	// 6. React to the result.
	if result.ValidationError != nil {
		ve := result.ValidationError
		switch ve.Code {
		case intent.ErrMissingSlots:
			// Do NOT persist events for clarify-required outcomes (§4.2 step 4).
			// Store the pending intent in memory.
			o.mu.Lock()
			o.pending[sid] = &pendingClarify{
				intentName: call.Intent,
				slots:      slotsToMap(call.Slots),
			}
			o.mu.Unlock()

			tl.Debug(ctx, trace.EvTurnDone,
				slog.String("mode", "clarify"),
				slog.String("pending_intent", call.Intent),
			)

			missingSlots := ve.MissingSlots
			clarification := ComputeClarification(o.def, journey.State, call.Intent, missingSlots)
			return &TurnOutcome{
				Mode:           ModeClarify,
				NewState:       journey.State,
				PendingIntent:  call.Intent,
				PendingSlots:   slotsToMap(call.Slots),
				SlotsNeeded:    clarification.Slots,
				AllowedIntents: allowedNames,
				TurnNumber:     turnNum,
			}, nil

		default:
			// INTENT_NOT_ALLOWED, GUARD_FAILED, etc.: persist the failure events.
			failureEvents := append(prefix, result.Events...)
			endEvent := newOrchestratorEvent(store.TurnEnded, map[string]any{
				"outcome": "rejected",
				"code":    string(ve.Code),
			}, turnNum)
			failureEvents = append(failureEvents, endEvent)

			if appendErr := o.store.AppendEvents(sid, failureEvents); appendErr != nil {
				return nil, fmt.Errorf("orchestrator: append failure events: %w", appendErr)
			}

			tl.Debug(ctx, trace.EvTurnPersisted,
				slog.Int("count", len(failureEvents)),
				slog.String("outcome", "rejected"),
			)
			tl.Debug(ctx, trace.EvTurnDone,
				slog.String("mode", "rejected"),
				slog.String("error_code", string(ve.Code)),
			)

			return &TurnOutcome{
				Mode:           ModeRejected,
				NewState:       journey.State,
				Events:         failureEvents,
				AllowedIntents: allowedNames,
				GuardHint:      ve.GuardHint,
				ErrorCode:      ve.Code,
				ErrorMessage:   ve.Message,
				TurnNumber:     turnNum,
			}, nil
		}
	}

	// Success path: dispatch any host calls collected by the machine, apply
	// their bindings to world, and refresh the view so the user sees the
	// updated state on the same turn.
	hostEvents, hostWorld, hostView, hostErr := o.dispatchHostCalls(ctx, result.HostCalls, result.World, result.NewState)
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

	successEvents := append(prefix, result.Events...)
	endEvent := newOrchestratorEvent(store.TurnEnded, map[string]any{
		"outcome": "transitioned",
		"to":      string(result.NewState),
	}, turnNum)
	successEvents = append(successEvents, endEvent)

	// Stamp turn number on all events.
	for i := range successEvents {
		successEvents[i].Turn = turnNum
	}

	if appendErr := o.store.AppendEvents(sid, successEvents); appendErr != nil {
		return nil, fmt.Errorf("orchestrator: append events: %w", appendErr)
	}

	tl.Debug(ctx, trace.EvTurnPersisted,
		slog.Int("count", len(successEvents)),
		slog.String("outcome", "transitioned"),
	)

	// Clear any pending clarification for this session.
	o.mu.Lock()
	delete(o.pending, sid)
	o.mu.Unlock()

	// Compute updated allowed intents in the new state.
	newAllowed := o.machine.AllowedIntents(result.NewState, result.World)
	newAllowedNames := make([]string, len(newAllowed))
	for i, ai := range newAllowed {
		newAllowedNames[i] = ai.Name
	}

	mode := ModeTransitioned

	// Check if the new state is terminal.
	newState := lookupStateByPath(o.def, result.NewState)
	if newState != nil && newState.Terminal {
		mode = ModeCompleted
	}

	tl.Debug(ctx, trace.EvTurnDone,
		slog.String("mode", mode.String()),
		slog.Int("view_bytes", len(result.View)),
		slog.String("view_rendered", result.View),
		slog.String("new_state", string(result.NewState)),
	)

	return &TurnOutcome{
		Mode:           mode,
		View:           result.View,
		NewState:       result.NewState,
		Events:         successEvents,
		AllowedIntents: newAllowedNames,
		TurnNumber:     turnNum,
	}, nil
}

// dispatchHostCalls invokes each HostInvocation, applies bindings to world,
// and re-renders the view. Returns the new events, the updated world, the
// refreshed view (empty if no changes), and an error only when re-rendering
// fails. Individual handler failures are folded into world.last_error and
// emitted as HostReturned events with error payloads — they do not stop
// dispatch of the remaining calls.
//
// When o.hosts is nil (deterministic flow tests), returns no events and the
// original world unchanged.
func (o *Orchestrator) dispatchHostCalls(ctx context.Context, calls []machine.HostInvocation, w world.World, state app.StatePath) ([]store.Event, world.World, string, error) {
	if o.hosts == nil || len(calls) == 0 {
		return nil, w, "", nil
	}

	var events []store.Event
	applied := false

	for _, hc := range calls {
		res, err := o.hosts.Invoke(ctx, hc.Namespace, hc.Args)
		if err != nil {
			// Infrastructure failure (e.g. handler not registered): record and move on.
			w.Vars["last_error"] = err.Error()
			events = append(events, newOrchestratorEvent(store.HostReturned, map[string]any{
				"namespace": hc.Namespace,
				"error":     err.Error(),
			}, 0))
			applied = true
			continue
		}
		if res.Error != "" {
			w.Vars["last_error"] = res.Error
		}

		// Emit one EffectApplied event per binding so replay reconstructs
		// the final world deterministically from the event log.
		for wkey, dkey := range hc.Bind {
			if res.Data == nil {
				continue
			}
			val, ok := res.Data[dkey]
			if !ok {
				continue
			}
			w.Vars[wkey] = val
			events = append(events, newOrchestratorEvent(store.EffectApplied, map[string]any{
				"set": map[string]any{wkey: val},
			}, 0))
			applied = true
		}

		payload := map[string]any{"namespace": hc.Namespace}
		if res.Error != "" {
			payload["error"] = res.Error
		}
		if res.Data != nil {
			payload["data"] = res.Data
		}
		events = append(events, newOrchestratorEvent(store.HostReturned, payload, 0))
	}

	if !applied {
		return events, w, "", nil
	}

	view, err := o.machine.RenderState(state, w)
	if err != nil {
		return events, w, "", fmt.Errorf("orchestrator: re-render after host dispatch: %w", err)
	}
	return events, w, view, nil
}

// SubmitDirect submits an intent call directly to the machine, bypassing the
// LLM harness entirely. This is the "direct path" for menu rows where all
// required slots are already known (e.g. enum-expanded rows like "go south").
// It mirrors the success path of Turn but skips harness.RunTurn.
func (o *Orchestrator) SubmitDirect(ctx context.Context, sid app.SessionID, intentName string, slots map[string]any) (*TurnOutcome, error) {
	journey, err := o.loadJourney(sid)
	if err != nil {
		return nil, fmt.Errorf("orchestrator: SubmitDirect: load journey: %w", err)
	}

	turnNum := journey.Turn + 1
	tl := trace.NewTurnLogger(o.logger, sid, turnNum, journey.State)
	tl.Debug(ctx, trace.EvTurnStart,
		slog.String("intent", intentName),
		slog.String("mode", "submit-direct"),
	)

	call := intent.IntentCall{
		Intent: intentName,
		Slots:  world.Slots(slots),
	}

	result, machineErr := o.machine.Turn(ctx, journey.State, journey.World, call)
	if machineErr != nil {
		return nil, fmt.Errorf("orchestrator: SubmitDirect: machine.Turn: %w", machineErr)
	}

	for i := range result.Events {
		result.Events[i].Turn = turnNum
	}

	if result.ValidationError != nil {
		ve := result.ValidationError
		if ve.Code == intent.ErrMissingSlots {
			o.mu.Lock()
			o.pending[sid] = &pendingClarify{
				intentName: call.Intent,
				slots:      slotsToMap(call.Slots),
			}
			o.mu.Unlock()

			missingSlots := ve.MissingSlots
			clarification := ComputeClarification(o.def, journey.State, call.Intent, missingSlots)
			return &TurnOutcome{
				Mode:          ModeClarify,
				NewState:      journey.State,
				PendingIntent: call.Intent,
				PendingSlots:  slotsToMap(call.Slots),
				SlotsNeeded:   clarification.Slots,
				TurnNumber:    turnNum,
			}, nil
		}
		startEvent := newOrchestratorEvent(store.TurnStarted, map[string]any{
			"turn":   int64(turnNum),
			"input":  fmt.Sprintf("[direct] intent=%s", intentName),
			"direct": true,
		}, turnNum)
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
			return nil, fmt.Errorf("orchestrator: SubmitDirect: append failure events: %w", appendErr)
		}
		allowedNames := make([]string, 0)
		for _, ai := range o.machine.AllowedIntents(journey.State, journey.World) {
			allowedNames = append(allowedNames, ai.Name)
		}
		return &TurnOutcome{
			Mode:           ModeRejected,
			NewState:       journey.State,
			Events:         failureEvents,
			GuardHint:      ve.GuardHint,
			ErrorCode:      ve.Code,
			ErrorMessage:   ve.Message,
			AllowedIntents: allowedNames,
			TurnNumber:     turnNum,
		}, nil
	}

	// Build and persist events (same as Turn success path).
	startEvent := newOrchestratorEvent(store.TurnStarted, map[string]any{
		"turn":   int64(turnNum),
		"input":  fmt.Sprintf("[direct] intent=%s", intentName),
		"direct": true,
	}, turnNum)

	hostEvents, hostWorld, hostView, hostErr := o.dispatchHostCalls(ctx, result.HostCalls, result.World, result.NewState)
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
		return nil, fmt.Errorf("orchestrator: SubmitDirect: append events: %w", appendErr)
	}

	tl.Debug(ctx, trace.EvTurnPersisted,
		slog.Int("count", len(successEvents)),
		slog.String("outcome", "transitioned"),
	)

	o.mu.Lock()
	delete(o.pending, sid)
	o.mu.Unlock()

	newAllowed := o.machine.AllowedIntents(result.NewState, result.World)
	newAllowedNames := make([]string, len(newAllowed))
	for i, ai := range newAllowed {
		newAllowedNames[i] = ai.Name
	}

	mode := ModeTransitioned
	newStateDef := lookupStateByPath(o.def, result.NewState)
	if newStateDef != nil && newStateDef.Terminal {
		mode = ModeCompleted
	}

	tl.Debug(ctx, trace.EvTurnDone,
		slog.String("mode", mode.String()),
		slog.Int("view_bytes", len(result.View)),
		slog.String("view_rendered", result.View),
		slog.String("new_state", string(result.NewState)),
	)

	return &TurnOutcome{
		Mode:           mode,
		View:           result.View,
		NewState:       result.NewState,
		Events:         successEvents,
		AllowedIntents: newAllowedNames,
		TurnNumber:     turnNum,
	}, nil
}

// OneShot runs a single turn against (state, world) without touching the
// store: no journey load, no event append, no snapshot. It is the building
// block for `hally turn`. Returns the diff (state, world, events, host calls,
// rendered view) so callers can answer "what happens if I do X in state Y
// with world Z?" without spinning up a real session.
//
// Routing:
//   - in.Intent set → direct path: the call goes straight to the machine.
//   - in.Input set  → LLM path: harness.RunTurn is called first to translate
//     the free text into an intent. Requires the orchestrator to be built
//     with a real harness (the replay harness works fine for tests).
//
// Host calls are dispatched the same way Turn dispatches them, so binding
// effects on world are visible in WorldAfter and the View reflects the
// post-binding state.
func (o *Orchestrator) OneShot(ctx context.Context, in OneShotInput) (*OneShotResult, error) {
	w := world.World{Vars: make(map[string]any, len(in.World))}
	for k, v := range in.World {
		w.Vars[k] = v
	}
	worldBefore := make(map[string]any, len(w.Vars))
	for k, v := range w.Vars {
		worldBefore[k] = v
	}

	var (
		call intent.IntentCall
		err  error
	)
	switch {
	case in.Intent != "":
		call = intent.IntentCall{
			Intent: in.Intent,
			Slots:  world.Slots(in.Slots),
		}
	case in.Input != "":
		allowed := o.machine.AllowedIntents(in.State, w)
		allowedNames := make([]string, len(allowed))
		for i, a := range allowed {
			allowedNames[i] = a.Name
		}
		params, runErr := o.harness.RunTurn(ctx, harness.TurnInput{
			SessionID:      app.SessionID("oneshot"),
			TurnNumber:     1,
			UserText:       in.Input,
			StatePath:      in.State,
			World:          w,
			AllowedIntents: allowedNames,
		})
		if runErr != nil {
			return nil, fmt.Errorf("orchestrator: OneShot: harness.RunTurn: %w", runErr)
		}
		call, err = parseIntentCall(params)
		if err != nil {
			return nil, fmt.Errorf("orchestrator: OneShot: parse intent call: %w", err)
		}
	default:
		return nil, fmt.Errorf("orchestrator: OneShot: exactly one of Intent or Input must be set")
	}

	result, machineErr := o.machine.Turn(ctx, in.State, w, call)
	if machineErr != nil {
		return nil, fmt.Errorf("orchestrator: OneShot: machine.Turn: %w", machineErr)
	}

	out := &OneShotResult{
		Intent:      call.Intent,
		Slots:       slotsToMap(call.Slots),
		PrevState:   in.State,
		NextState:   result.NewState,
		WorldBefore: worldBefore,
	}

	if result.ValidationError != nil {
		ve := result.ValidationError
		if ve.Code == intent.ErrMissingSlots {
			clarification := ComputeClarification(o.def, in.State, call.Intent, ve.MissingSlots)
			out.Mode = ModeClarify
			out.SlotsNeeded = clarification.Slots
		} else {
			out.Mode = ModeRejected
		}
		out.ErrorCode = string(ve.Code)
		out.ErrorMessage = ve.Message
		out.GuardHint = ve.GuardHint
		out.NextState = in.State
		out.WorldAfter = worldBefore
		out.AllowedIntents = allowedNamesFromMachine(o.machine, in.State, w)
		// View is whatever the unchanged state would render.
		view, _ := o.machine.RenderState(in.State, w)
		out.View = view
		return out, nil
	}

	// Capture EffectApplied events from the machine before host dispatch so
	// `hally turn` can show effect-by-effect diffs.
	out.Effects = effectsFromEvents(result.Events)

	hostSummaries, hostEvents, hostWorld, hostView, hostErr := o.dispatchHostCallsDetailed(ctx, result.HostCalls, result.World, result.NewState)
	if hostErr != nil {
		return nil, fmt.Errorf("orchestrator: OneShot: %w", hostErr)
	}
	if len(hostEvents) > 0 {
		result.Events = append(result.Events, hostEvents...)
		// Re-collect effects after host dispatch produced more EffectApplied events.
		out.Effects = effectsFromEvents(result.Events)
		result.World = hostWorld
		if hostView != "" {
			result.View = hostView
		}
	}

	out.HostCalls = hostSummaries

	out.Mode = ModeTransitioned
	if newState := lookupStateByPath(o.def, result.NewState); newState != nil && newState.Terminal {
		out.Mode = ModeCompleted
	}
	out.View = result.View

	out.WorldAfter = make(map[string]any, len(result.World.Vars))
	for k, v := range result.World.Vars {
		out.WorldAfter[k] = v
	}
	out.AllowedIntents = allowedNamesFromMachine(o.machine, result.NewState, result.World)

	return out, nil
}

// dispatchHostCallsDetailed is the same dispatch loop as dispatchHostCalls
// but additionally returns one HostCallSummary per invocation so callers
// (currently OneShot) can surface args/data/error to the user. The events
// returned here are identical to what dispatchHostCalls would have produced.
func (o *Orchestrator) dispatchHostCallsDetailed(ctx context.Context, calls []machine.HostInvocation, w world.World, state app.StatePath) ([]HostCallSummary, []store.Event, world.World, string, error) {
	if o.hosts == nil || len(calls) == 0 {
		return nil, nil, w, "", nil
	}

	summaries := make([]HostCallSummary, 0, len(calls))
	var events []store.Event
	applied := false

	for _, hc := range calls {
		summary := HostCallSummary{Namespace: hc.Namespace, Args: hc.Args}
		res, err := o.hosts.Invoke(ctx, hc.Namespace, hc.Args)
		if err != nil {
			summary.Error = err.Error()
			summaries = append(summaries, summary)
			w.Vars["last_error"] = err.Error()
			events = append(events, newOrchestratorEvent(store.HostReturned, map[string]any{
				"namespace": hc.Namespace,
				"error":     err.Error(),
			}, 0))
			applied = true
			continue
		}
		if res.Error != "" {
			w.Vars["last_error"] = res.Error
			summary.Error = res.Error
		}
		if res.Data != nil {
			summary.Data = res.Data
		}
		summaries = append(summaries, summary)

		for wkey, dkey := range hc.Bind {
			if res.Data == nil {
				continue
			}
			val, ok := res.Data[dkey]
			if !ok {
				continue
			}
			w.Vars[wkey] = val
			events = append(events, newOrchestratorEvent(store.EffectApplied, map[string]any{
				"set": map[string]any{wkey: val},
			}, 0))
			applied = true
		}

		payload := map[string]any{"namespace": hc.Namespace}
		if res.Error != "" {
			payload["error"] = res.Error
		}
		if res.Data != nil {
			payload["data"] = res.Data
		}
		events = append(events, newOrchestratorEvent(store.HostReturned, payload, 0))
	}

	if !applied {
		return summaries, events, w, "", nil
	}
	view, err := o.machine.RenderState(state, w)
	if err != nil {
		return summaries, events, w, "", fmt.Errorf("re-render after host dispatch: %w", err)
	}
	return summaries, events, w, view, nil
}

// effectsFromEvents flattens EffectApplied events into EffectSummary form.
func effectsFromEvents(events []store.Event) []EffectSummary {
	var out []EffectSummary
	for _, ev := range events {
		if ev.Kind != store.EffectApplied {
			continue
		}
		var es EffectSummary
		if err := json.Unmarshal(ev.Payload, &es); err != nil {
			continue
		}
		out = append(out, es)
	}
	return out
}

// allowedNamesFromMachine collects intent names allowed in (state, world).
func allowedNamesFromMachine(m machine.Machine, state app.StatePath, w world.World) []string {
	allowed := m.AllowedIntents(state, w)
	out := make([]string, len(allowed))
	for i, ai := range allowed {
		out[i] = ai.Name
	}
	return out
}

// ContinueTurn retries the pending intent with supplemental slot values
// collected from the clarification UI (§4.2 step 4 continuation).
func (o *Orchestrator) ContinueTurn(ctx context.Context, sid app.SessionID, supplementSlots map[string]any) (*TurnOutcome, error) {
	o.mu.Lock()
	pend, ok := o.pending[sid]
	o.mu.Unlock()

	if !ok {
		return nil, fmt.Errorf("orchestrator: no pending clarification for session %s", sid)
	}

	// Merge the supplement into the pending slots.
	merged := make(world.Slots, len(pend.slots)+len(supplementSlots))
	for k, v := range pend.slots {
		merged[k] = v
	}
	for k, v := range supplementSlots {
		merged[k] = v
	}

	// Reconstruct the journey.
	journey, err := o.loadJourney(sid)
	if err != nil {
		return nil, fmt.Errorf("orchestrator: load journey: %w", err)
	}

	call := intent.IntentCall{
		Intent: pend.intentName,
		Slots:  merged,
	}

	turnNum := journey.Turn + 1
	tl := trace.NewTurnLogger(o.logger, sid, turnNum, journey.State)
	tl.Debug(ctx, trace.EvTurnStart,
		slog.String("intent", call.Intent),
		slog.String("mode", "clarify-continue"),
	)

	result, machineErr := o.machine.Turn(ctx, journey.State, journey.World, call)
	if machineErr != nil {
		return nil, fmt.Errorf("orchestrator: machine.Turn (continue): %w", machineErr)
	}

	// Stamp turn number.
	for i := range result.Events {
		result.Events[i].Turn = turnNum
	}

	if result.ValidationError != nil {
		ve := result.ValidationError
		if ve.Code == intent.ErrMissingSlots {
			// Still missing slots; update the pending state.
			o.mu.Lock()
			o.pending[sid] = &pendingClarify{
				intentName: call.Intent,
				slots:      map[string]any(merged),
			}
			o.mu.Unlock()

			clarification := ComputeClarification(o.def, journey.State, call.Intent, ve.MissingSlots)
			return &TurnOutcome{
				Mode:          ModeClarify,
				NewState:      journey.State,
				PendingIntent: call.Intent,
				PendingSlots:  map[string]any(merged),
				SlotsNeeded:   clarification.Slots,
				TurnNumber:    turnNum,
			}, nil
		}

		// Other validation error.
		allowedNames := make([]string, 0)
		if ai := o.machine.AllowedIntents(journey.State, journey.World); len(ai) > 0 {
			for _, a := range ai {
				allowedNames = append(allowedNames, a.Name)
			}
		}
		return &TurnOutcome{
			Mode:         ModeRejected,
			NewState:     journey.State,
			Events:       result.Events,
			GuardHint:    ve.GuardHint,
			ErrorCode:    ve.Code,
			ErrorMessage: ve.Message,
			TurnNumber:   turnNum,
		}, nil
	}

	// Success: dispatch host calls then persist events.
	hostEvents, hostWorld, hostView, hostErr := o.dispatchHostCalls(ctx, result.HostCalls, result.World, result.NewState)
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

	startEvent := newOrchestratorEvent(store.TurnStarted, map[string]any{
		"turn":    int64(turnNum),
		"input":   fmt.Sprintf("[clarify-continue] intent=%s", call.Intent),
		"clarify": true,
	}, turnNum)

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
		return nil, fmt.Errorf("orchestrator: append continue events: %w", appendErr)
	}

	tl.Debug(ctx, trace.EvTurnPersisted,
		slog.Int("count", len(successEvents)),
		slog.String("outcome", "transitioned"),
	)

	// Clear pending.
	o.mu.Lock()
	delete(o.pending, sid)
	o.mu.Unlock()

	newAllowed := o.machine.AllowedIntents(result.NewState, result.World)
	newAllowedNames := make([]string, len(newAllowed))
	for i, ai := range newAllowed {
		newAllowedNames[i] = ai.Name
	}

	mode := ModeTransitioned
	newStateDef := lookupStateByPath(o.def, result.NewState)
	if newStateDef != nil && newStateDef.Terminal {
		mode = ModeCompleted
	}

	tl.Debug(ctx, trace.EvTurnDone,
		slog.String("mode", mode.String()),
		slog.Int("view_bytes", len(result.View)),
		slog.String("view_rendered", result.View),
		slog.String("new_state", string(result.NewState)),
	)

	return &TurnOutcome{
		Mode:           mode,
		View:           result.View,
		NewState:       result.NewState,
		Events:         successEvents,
		AllowedIntents: newAllowedNames,
		TurnNumber:     turnNum,
	}, nil
}

// InitialView returns the view for the initial state (to display at session start).
func (o *Orchestrator) InitialView(w world.World) (string, error) {
	initialState := app.StatePath("")
	if s, ok := o.def.Root.(string); ok {
		initialState = app.StatePath(s)
	}
	// Render the view for the initial state.
	// We do a dummy "look" turn if look is available, otherwise we read the view directly.
	s := lookupStateByPath(o.def, initialState)
	if s == nil {
		return "", nil
	}
	if s.View == "" {
		return s.Description, nil
	}
	// Use the machine to render the view by doing a self-transition via "look" if available.
	// For now, read the view template directly via the expr package.
	return renderStateView(o.def, initialState, w)
}

// InitialState returns the initial state path for the app.
func (o *Orchestrator) InitialState() app.StatePath {
	if s, ok := o.def.Root.(string); ok {
		return app.StatePath(s)
	}
	return ""
}

// InitialWorld returns a world initialised from the app's schema defaults.
func (o *Orchestrator) InitialWorld() world.World {
	return machine.WorldFromSchema(o.def.World)
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// loadJourney reconstructs the current state and world from the store.
func (o *Orchestrator) loadJourney(sid app.SessionID) (*store.JourneyState, error) {
	// Determine initial state and world from app defaults.
	initialState := o.InitialState()
	initialWorld := o.InitialWorld()

	// Try to load from the latest snapshot first.
	snap, hasSnap, err := o.store.LatestSnapshot(sid)
	if err != nil {
		return nil, fmt.Errorf("load snapshot: %w", err)
	}

	startState := initialState
	startWorld := initialWorld
	if hasSnap {
		startState = snap.StatePath
		if err := json.Unmarshal(snap.WorldJSON, &startWorld.Vars); err != nil {
			return nil, fmt.Errorf("unmarshal snapshot world: %w", err)
		}
	}

	// Load events since the snapshot.
	history, err := o.store.LoadHistory(sid)
	if err != nil {
		return nil, fmt.Errorf("load history: %w", err)
	}

	js, err := store.BuildJourney(o.def, startState, startWorld, history)
	if err != nil {
		return nil, fmt.Errorf("build journey: %w", err)
	}

	return js, nil
}

// parseIntentCall extracts an IntentCall from the harness's CallToolParams.
func parseIntentCall(params mcp.CallToolParams) (intent.IntentCall, error) {
	if params.Name != "transition" {
		return intent.IntentCall{}, fmt.Errorf("unexpected tool name %q (want \"transition\")", params.Name)
	}
	if params.Arguments == nil {
		return intent.IntentCall{}, fmt.Errorf("nil arguments in CallToolParams")
	}

	// Arguments may be map[string]any or need JSON round-trip.
	argsMap, err := toStringMap(params.Arguments)
	if err != nil {
		return intent.IntentCall{}, fmt.Errorf("arguments: %w", err)
	}

	intentName, _ := argsMap["intent"].(string)
	if intentName == "" {
		return intent.IntentCall{}, fmt.Errorf("missing 'intent' field in transition args")
	}

	var slots world.Slots
	if sv, ok := argsMap["slots"]; ok && sv != nil {
		slots, err = toSlots(sv)
		if err != nil {
			return intent.IntentCall{}, fmt.Errorf("slots: %w", err)
		}
	}

	confidence, _ := argsMap["confidence"].(float64)

	return intent.IntentCall{
		Intent:     intentName,
		Slots:      slots,
		Confidence: confidence,
	}, nil
}

// toStringMap converts an interface{} to map[string]any via JSON round-trip if needed.
func toStringMap(v any) (map[string]any, error) {
	if m, ok := v.(map[string]any); ok {
		return m, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// toSlots converts an interface{} to world.Slots.
func toSlots(v any) (world.Slots, error) {
	m, err := toStringMap(v)
	if err != nil {
		return nil, err
	}
	return world.Slots(m), nil
}

// slotsToMap converts world.Slots to map[string]any.
func slotsToMap(s world.Slots) map[string]any {
	if s == nil {
		return make(map[string]any)
	}
	m := make(map[string]any, len(s))
	for k, v := range s {
		m[k] = v
	}
	return m
}

// extractIntentName extracts the intent name from CallToolParams without erroring.
func extractIntentName(params mcp.CallToolParams) string {
	if m, ok := params.Arguments.(map[string]any); ok {
		if n, ok := m["intent"].(string); ok {
			return n
		}
	}
	return ""
}

// newOrchestratorEvent creates an orchestrator-level event.
func newOrchestratorEvent(kind store.EventKind, payload map[string]any, turn app.TurnNumber) store.Event {
	b, _ := json.Marshal(payload)
	return store.Event{
		Kind:    kind,
		Turn:    turn,
		Payload: b,
	}
}
