// rewind.go — RewindRoute: one-decision foreground rewind + route override.
//
// RewindRoute reverses one contextual routing decision (identified by its
// DecisionID = "<session_id>:<turn_number>"), restores the pre-turn state/world
// snapshot, records a turn.context_route_overridden event, and re-dispatches
// the original utterance under the operator-chosen class.
//
// Approach A (bounded replay + re-baseline) per the CRR slice-4 brief:
//
//  1. Parse decisionID → turnN.
//  2. Load latest snapshot → startState/startWorld.
//  3. Load history (snapshot-relative, via store.LoadHistory).
//  4. Recover original utterance and old class from the TurnStarted event at turnN.
//  5. BuildJourneyUntil(history, turnN) → pre-turn state/world.
//  6. store.Snapshot(sid, turnN, pre) — re-baselines LoadHistory to skip the
//     overridden turn-N events without deleting them.
//  7. For lane classes (help/room_request/meta_edit): resolve lane, append
//     utterance, build outcome.
//     For intent class: call SubmitDirectRouted (outside the lock).
//  8. Append turn.context_route_overridden side-channel event at turnN+1.
//  9. Return outcome with the overridden event in Events and a ContextRoute receipt.
package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/roomchat"
	"kitsoki/internal/store"
	"kitsoki/internal/world"
)

// RewindRoute reverses one contextual routing decision, restores pre-turn
// state/world, records a turn.context_route_overridden event, and re-dispatches
// the original utterance under newClass. Foreground, one-decision deep (v1).
// Returns the new outcome.
func (o *Orchestrator) RewindRoute(ctx context.Context, sid app.SessionID, decisionID string, newClass ContextRouteClass, reason string, workspacePath string) (*TurnOutcome, error) {
	if o.store == nil {
		return nil, fmt.Errorf("orchestrator: RewindRoute: no store configured")
	}

	var parked *ParkedWorkspaceReceipt
	if strings.TrimSpace(workspacePath) != "" {
		parkRes, parkErr := host.ParkWorkspace(ctx, workspacePath, string(sid), reason)
		if parkErr != nil {
			return nil, fmt.Errorf("orchestrator: RewindRoute: park workspace: %w", parkErr)
		}
		if parkRes.Parked {
			parked = &ParkedWorkspaceReceipt{
				SourceWorkspace:   parkRes.SourceWorkspace,
				RecoveryWorkspace: parkRes.RecoveryWorkspace,
				RecoveryBranch:    parkRes.RecoveryBranch,
				RecoveryCommit:    parkRes.RecoveryCommit,
				Cleaned:           parkRes.Cleaned,
				Reason:            parkRes.Reason,
			}
		}
	}

	// Parse decisionID = "<session_id>:<turn_number>".
	lastColon := strings.LastIndex(decisionID, ":")
	if lastColon < 0 {
		return nil, fmt.Errorf("orchestrator: RewindRoute: invalid decisionID %q (expected <sid>:<turn>)", decisionID)
	}
	var turnN int64
	if _, err := fmt.Sscanf(decisionID[lastColon+1:], "%d", &turnN); err != nil {
		return nil, fmt.Errorf("orchestrator: RewindRoute: parse turn from decisionID %q: %w", decisionID, err)
	}

	sessMu := o.sessionLock(sid)
	sessMu.Lock()

	// Load latest snapshot baseline.
	snap, hasSnap, err := o.store.LatestSnapshot(sid)
	if err != nil {
		sessMu.Unlock()
		return nil, fmt.Errorf("orchestrator: RewindRoute: latest snapshot: %w", err)
	}
	startState := o.InitialState()
	startWorld := o.InitialWorld()
	if hasSnap {
		startState = snap.StatePath
		if unmarshalErr := json.Unmarshal(snap.WorldJSON, &startWorld); unmarshalErr != nil {
			sessMu.Unlock()
			return nil, fmt.Errorf("orchestrator: RewindRoute: unmarshal snapshot world: %w", unmarshalErr)
		}
	}

	// Load history since latest snapshot.
	history, err := o.store.LoadHistory(sid)
	if err != nil {
		sessMu.Unlock()
		return nil, fmt.Errorf("orchestrator: RewindRoute: load history: %w", err)
	}

	// Recover original utterance and old class from the TurnStarted event at turnN.
	var originalInput, oldClass string
	for _, ev := range history {
		if ev.Turn != app.TurnNumber(turnN) || ev.Kind != store.TurnStarted {
			continue
		}
		var p map[string]any
		if unmarshalErr := json.Unmarshal(ev.Payload, &p); unmarshalErr == nil {
			originalInput, _ = p["input"].(string)
			oldClass, _ = p["context_route_class"].(string)
		}
	}
	if oldClass == "" {
		sessMu.Unlock()
		return nil, fmt.Errorf("orchestrator: RewindRoute: no TurnStarted with context_route_class at turn %d (decisionID=%q)", turnN, decisionID)
	}
	if originalInput == "" || strings.HasPrefix(originalInput, "[direct]") {
		sessMu.Unlock()
		return nil, fmt.Errorf("orchestrator: RewindRoute: could not recover original utterance at turn %d", turnN)
	}

	// Build pre-turn state/world via bounded replay.
	pre, err := store.BuildJourneyUntil(o.def, startState, startWorld, history, app.TurnNumber(turnN))
	if err != nil {
		sessMu.Unlock()
		return nil, fmt.Errorf("orchestrator: RewindRoute: build journey until %d: %w", turnN, err)
	}

	// Snapshot at turnN with the pre-turn state so that the next LoadHistory
	// (WHERE turn > turnN) skips the overridden turn-N events without deleting them.
	worldJSON, err := json.Marshal(pre.World)
	if err != nil {
		sessMu.Unlock()
		return nil, fmt.Errorf("orchestrator: RewindRoute: marshal world: %w", err)
	}
	if snErr := o.store.Snapshot(sid, app.TurnNumber(turnN), store.Snapshot{
		StatePath: pre.State,
		WorldJSON: json.RawMessage(worldJSON),
	}); snErr != nil {
		sessMu.Unlock()
		return nil, fmt.Errorf("orchestrator: RewindRoute: snapshot: %w", snErr)
	}

	sessMu.Unlock()

	// Re-dispatch under newClass.
	var outcome *TurnOutcome
	switch newClass {
	case ClassIntent:
		// Intent rewind: recover the accepted intent name + slots from turnN.
		// The accepted intent is recorded on the IntentAccepted event (RunIntent
		// path, payload {"intent":<name>,"slots":{…}}; helpers.go) and, for the
		// contextual-router/SubmitDirect dispatch path, on the machine.transition
		// event (payload {"from","intent","slots","to"}). Prefer IntentAccepted,
		// fall back to the transition event so both dispatch paths are covered.
		var intentName string
		var slots map[string]any
		for _, ev := range history {
			if ev.Turn != app.TurnNumber(turnN) {
				continue
			}
			if ev.Kind != store.IntentAccepted && ev.Kind != store.TransitionApplied {
				continue
			}
			var p struct {
				Intent string         `json:"intent"`
				Slots  map[string]any `json:"slots"`
			}
			if unmarshalErr := json.Unmarshal(ev.Payload, &p); unmarshalErr != nil || p.Intent == "" {
				continue
			}
			intentName = p.Intent
			slots = p.Slots
			if ev.Kind == store.IntentAccepted {
				break // authoritative source — stop scanning
			}
		}
		if intentName == "" {
			return nil, fmt.Errorf("orchestrator: RewindRoute: no IntentAccepted/transition event at turn %d to recover intent for class=intent rewind (decisionID=%q)", turnN, decisionID)
		}

		var dispatchErr error
		outcome, dispatchErr = o.SubmitDirectRouted(ctx, sid, intentName, slots, originalInput,
			RouteProvenance{Source: "context_route", ContextRouteClass: string(ClassIntent)})
		if dispatchErr != nil {
			return nil, fmt.Errorf("orchestrator: RewindRoute: re-dispatch intent %q: %w", intentName, dispatchErr)
		}
		outcome.ContextRoute = &ContextRouteReceipt{
			Class:      string(ClassIntent),
			Intent:     intentName,
			Reason:     reason,
			DecisionID: decisionID,
		}
		outcome.ParkedWorkspace = parked

	default:
		// help / room_request / meta_edit — lane dispatch.
		if o.chatStore == nil {
			return nil, fmt.Errorf("orchestrator: RewindRoute: chat store not configured for lane dispatch")
		}
		var kind roomchat.LaneKind
		switch newClass {
		case ClassHelp:
			kind = roomchat.LaneHelp
		case ClassRoomRequest:
			kind = roomchat.LaneWork
		default: // ClassMetaEdit
			kind = roomchat.LaneMeta
		}
		resolver := roomchat.Resolver{Store: o.chatStore}
		laneTitle := string(newClass) + " lane"
		chat, _, resolveErr := resolver.Active(ctx, o.def.App.ID, kind, string(pre.State), laneTitle)
		if resolveErr != nil {
			return nil, fmt.Errorf("orchestrator: RewindRoute: resolve lane %s: %w", kind, resolveErr)
		}
		if appendErr := resolver.Append(ctx, chat.ID, "user", originalInput); appendErr != nil {
			return nil, fmt.Errorf("orchestrator: RewindRoute: append to lane %s: %w", kind, appendErr)
		}
		outcome = &TurnOutcome{
			Mode:     ModeOffPath,
			NewState: pre.State,
			ContextRoute: &ContextRouteReceipt{
				Class:        string(newClass),
				Reason:       reason,
				TargetChatID: chat.ID,
				TargetLane:   string(kind),
				DecisionID:   decisionID,
			},
			ParkedWorkspace: parked,
		}
	}

	// Append the turn.context_route_overridden side-channel event.
	// We write it at turnN+1 to avoid a sequence-number collision with the
	// existing turn-N events (the snapshot re-baselines loadJourney past them,
	// but the DB rows remain and AppendEvents would collide at seq=0).
	overriddenPayload, _ := json.Marshal(map[string]any{
		"from_decision_id": decisionID,
		"old_class":        oldClass,
		"new_class":        string(newClass),
		"reason":           reason,
	})
	// The override event is written one turn past the last persisted turn so it
	// never collides on (session_id, turn, seq). For lane classes nothing was
	// written after the pre-turn snapshot, so turnN+1 is free. For the intent
	// class SubmitDirectRouted already wrote a full turn at turnN+1, so we land
	// the override at the dispatched turn + 1.
	overrideTurn := app.TurnNumber(turnN) + 1
	if newClass == ClassIntent && outcome.TurnNumber >= overrideTurn {
		overrideTurn = outcome.TurnNumber + 1
	}
	overriddenEvent := store.Event{
		Kind:       store.TurnContextRouteOverridden,
		Turn:       overrideTurn,
		ParentTurn: app.TurnNumber(turnN),
		Payload:    overriddenPayload,
	}

	sessMu.Lock()
	emptyWorld := world.World{Vars: map[string]any{}}
	jEntries := journalEntriesForEvents(sid, overrideTurn, time.Now(), []store.Event{overriddenEvent},
		emptyWorld, emptyWorld, "", "", "")
	if appendErr := o.appendEventsAndJournal(sid, []store.Event{overriddenEvent}, jEntries); appendErr != nil {
		sessMu.Unlock()
		return nil, fmt.Errorf("orchestrator: RewindRoute: append overridden event: %w", appendErr)
	}
	sessMu.Unlock()

	outcome.Events = append(outcome.Events, overriddenEvent)
	return outcome, nil
}
