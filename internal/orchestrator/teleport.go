package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/inbox"
	"kitsoki/internal/store"
	"kitsoki/internal/trace"
)

// Teleport jumps the session to the given target state with the slot bag
// restored. Stackless: does not push the current state onto the room
// history stack — used by inbox notifications and the Agent Room banner,
// where the source room remains the conceptual "where the user came from"
// for any subsequent back-pop. Returns the new TurnOutcome with
// re-rendered view and updated allowed intents.
//
// If target.State is empty, an error is returned.
// If the orchestrator has no job store or scheduler configured it still works:
// world slots are merged and the view is re-rendered.
func (o *Orchestrator) Teleport(ctx context.Context, sid app.SessionID, target inbox.TeleportTarget) (*TurnOutcome, error) {
	if target.State == "" {
		return nil, fmt.Errorf("orchestrator.Teleport: target.State is empty")
	}

	o.logger.DebugContext(ctx, trace.EvTeleportStart,
		slog.String("session_id", string(sid)),
		slog.String("to", string(target.State)),
		slog.String("job_id", target.JobID),
		slog.String("proposal_id", target.ProposalID),
		slog.Int("slot_count", len(target.Slots)),
	)

	// Serialise against handleJobTerminal — see Turn for rationale.
	sessMu := o.sessionLock(sid)
	sessMu.Lock()
	defer sessMu.Unlock()

	journey, err := o.loadJourney(sid)
	if err != nil {
		return nil, fmt.Errorf("orchestrator.Teleport: load journey: %w", err)
	}

	turnNum := journey.Turn + 1
	priorState := journey.State

	// Merge slots from the target into the current world.
	w := journey.World
	for k, v := range target.Slots {
		w.Vars[k] = v
	}
	if target.JobID != "" {
		w.Vars["teleport_job_id"] = target.JobID
	}
	if target.ProposalID != "" {
		w.Vars["teleport_proposal_id"] = target.ProposalID
	}

	// Re-render the view at the destination state.
	view, err := o.machine.RenderState(target.State, w)
	if err != nil {
		return nil, fmt.Errorf("orchestrator.Teleport: render state %q: %w", target.State, err)
	}

	// Build synthetic events for the event log.
	startEvent := newOrchestratorEvent(store.TurnStarted, map[string]any{
		"turn":        int64(turnNum),
		"kind":        "teleport",
		"from":        string(priorState),
		"to":          string(target.State),
		"job_id":      target.JobID,
		"proposal_id": target.ProposalID,
	}, turnNum)

	// Emit a TransitionApplied event so BuildJourney restores the destination
	// state after a process restart (TurnStarted alone is silently ignored by
	// the replayer — only TransitionApplied updates js.State).
	transitionEvent := newOrchestratorEvent(store.TransitionApplied, map[string]any{
		"from":   string(priorState),
		"to":     string(target.State),
		"intent": "teleport",
	}, turnNum)

	// Emit one EffectApplied event per merged slot so the world is restored on
	// replay.  Each slot gets its own event, matching the pattern used by the
	// regular Turn path when binding host results.
	var slotEvents []store.Event
	for k, v := range target.Slots {
		slotEvents = append(slotEvents, newOrchestratorEvent(store.EffectApplied, map[string]any{
			"set": map[string]any{k: v},
		}, turnNum))
	}
	if target.JobID != "" {
		slotEvents = append(slotEvents, newOrchestratorEvent(store.EffectApplied, map[string]any{
			"set": map[string]any{"teleport_job_id": target.JobID},
		}, turnNum))
	}
	if target.ProposalID != "" {
		slotEvents = append(slotEvents, newOrchestratorEvent(store.EffectApplied, map[string]any{
			"set": map[string]any{"teleport_proposal_id": target.ProposalID},
		}, turnNum))
	}

	endEvent := newOrchestratorEvent(store.TurnEnded, map[string]any{
		"outcome": "transitioned",
		"to":      string(target.State),
	}, turnNum)

	events := []store.Event{startEvent, transitionEvent}
	events = append(events, slotEvents...)
	events = append(events, endEvent)
	for i := range events {
		events[i].Turn = turnNum
	}

	// Site 17: dual-write journal entries for the teleport synthetic turn.
	// Pre-world is journey.World (before slot merge); post-world is the merged w.
	tpJEntries := journalEntriesForEvents(sid, turnNum, time.Now(), events,
		journey.World, w, view, target.State, "")
	if appendErr := o.appendEventsAndJournal(sid, events, tpJEntries); appendErr != nil {
		return nil, fmt.Errorf("orchestrator.Teleport: append events: %w", appendErr)
	}

	o.logger.DebugContext(ctx, trace.EvTeleportDone,
		slog.String("session_id", string(sid)),
		slog.String("from", string(priorState)),
		slog.String("to", string(target.State)),
		slog.Int64("turn", int64(turnNum)),
	)

	// (Re-)arm any Timeout: declared on the destination state.
	o.armTimeoutForState(sid, priorState, target.State)

	// Compute new visible allowed intents in the destination state.
	newAllowedNames := allowedNamesFromMachine(o.machine, target.State, w)

	mode := ModeTransitioned
	newStateDef := lookupStateByPath(o.def, target.State)
	if newStateDef != nil && newStateDef.Terminal {
		mode = ModeCompleted
	}

	return &TurnOutcome{
		Mode:           mode,
		View:           view,
		NewState:       target.State,
		Events:         events,
		AllowedIntents: newAllowedNames,
		TurnNumber:     turnNum,
	}, nil
}
