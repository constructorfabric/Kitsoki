package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/harness"
	"kitsoki/internal/intent"
	"kitsoki/internal/journal"
	"kitsoki/internal/machine"
	"kitsoki/internal/store"
	"kitsoki/internal/trace"
	"kitsoki/internal/world"
)

// RecentTurnsLimit caps how many prior turn summaries are passed to the
// harness via TurnInput.RecentTurns. Kept small to bound prompt size; the
// LLM's working memory for back-reference resolution is unlikely to need
// more than a handful of recent turns. A future iteration may expose this
// as a per-app knob in app.yaml.
const RecentTurnsLimit = 5

// extractRecentTurns scans an event history (oldest → newest) and returns
// up to RecentTurnsLimit harness.TurnSummary records, one per completed
// prior turn. The slice is ordered oldest → newest; the caller may pass it
// directly into TurnInput.RecentTurns.
//
// "Completed turn" means a TurnEnded event was appended, which covers both
// success (outcome=transitioned) and rejection (outcome=rejected). Turns
// that ended in clarify mode are intentionally excluded — they did not
// finish from the user's perspective and their pending state belongs in a
// different surface (the slot-fill flow). Synthetic turns (background-job
// completions, timeouts) are excluded as well: they did not originate from
// a user utterance and have no UserText to anchor a back-reference to.
//
// The function tolerates partial event sequences: a turn missing a
// TransitionApplied (rejected before machine.Turn fired) still produces a
// summary with Intent="" and Rejected=true. A turn missing TurnStarted is
// skipped — without UserText the summary has no anchor.
func extractRecentTurns(history store.History) []harness.TurnSummary {
	if len(history) == 0 {
		return nil
	}

	// Group events by turn number. Map preserves no order but we sort the
	// turn keys before walking, so the result is deterministic.
	type turnAcc struct {
		userText  string
		intent    string
		slots     map[string]any
		toState   string
		fromState string
		rejected  bool
		ended     bool
		synthetic bool // true when no user utterance drove this turn
	}
	turns := make(map[app.TurnNumber]*turnAcc)

	for _, ev := range history {
		acc, ok := turns[ev.Turn]
		if !ok {
			acc = &turnAcc{}
			turns[ev.Turn] = acc
		}
		switch ev.Kind {
		case store.TurnStarted:
			var p struct {
				Input  string `json:"input"`
				Direct bool   `json:"direct"`
			}
			if err := json.Unmarshal(ev.Payload, &p); err == nil {
				acc.userText = p.Input
				// RunIntent paths prefix input with "[intent] ..." and set
				// direct: true. Treat those as synthetic so they do not
				// pollute back-reference context.
				if p.Direct {
					acc.synthetic = true
				}
			}
		case store.TransitionApplied:
			var p struct {
				Intent string         `json:"intent"`
				Slots  map[string]any `json:"slots"`
				From   string         `json:"from"`
				To     string         `json:"to"`
			}
			if err := json.Unmarshal(ev.Payload, &p); err == nil {
				if acc.intent == "" {
					acc.intent = p.Intent
				}
				if len(p.Slots) > 0 && acc.slots == nil {
					acc.slots = p.Slots
				}
				acc.fromState = p.From
				acc.toState = p.To
			}
		case store.TurnEnded:
			acc.ended = true
			var p struct {
				Outcome string `json:"outcome"`
			}
			if err := json.Unmarshal(ev.Payload, &p); err == nil {
				if p.Outcome == "rejected" {
					acc.rejected = true
				}
			}
		}
	}

	// Walk turns in ascending order and collect completed summaries.
	maxTurn := app.TurnNumber(0)
	for t := range turns {
		if t > maxTurn {
			maxTurn = t
		}
	}

	var summaries []harness.TurnSummary
	for t := app.TurnNumber(1); t <= maxTurn; t++ {
		acc, ok := turns[t]
		if !ok || !acc.ended || acc.synthetic {
			continue
		}
		if acc.userText == "" {
			continue
		}
		state := acc.toState
		if state == "" {
			state = acc.fromState
		}
		summaries = append(summaries, harness.TurnSummary{
			Turn:     t,
			UserText: acc.userText,
			Intent:   acc.intent,
			Slots:    acc.slots,
			State:    app.StatePath(state),
			Rejected: acc.rejected,
		})
	}

	if len(summaries) > RecentTurnsLimit {
		summaries = summaries[len(summaries)-RecentTurnsLimit:]
	}
	return summaries
}

// supplementKeys returns the sorted list of keys in m, used purely for trace
// attribute emission so a slog handler does not have to serialise the
// (possibly large) values.
func supplementKeys(m map[string]any) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

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
			riSlotsSoFar := slotsToMap(call.Slots)
			o.mu.Lock()
			o.pending[sid] = &pendingClarify{
				intentName: call.Intent,
				slots:      riSlotsSoFar,
			}
			o.mu.Unlock()
			clarification := ComputeClarification(o.def, journey.State, call.Intent, ve.MissingSlots)
			tl.Debug(ctx, trace.EvSlotFillRequested,
				slog.String("intent", call.Intent),
				slog.Int("missing_count", len(ve.MissingSlots)),
				slog.Any("missing", ve.MissingSlots),
				slog.String("origin", "run_intent"),
			)
			// Site 8 (RunIntent path): emit clarify.requested via standalone journal write.
			riMissingNames := make([]string, len(ve.MissingSlots))
			copy(riMissingNames, ve.MissingSlots)
			o.appendJournal(journalEntry(sid, turnNum, 0, time.Now(),
				journal.KindClarifyRequested, "",
				map[string]any{
					"origin":       "foreground",
					"intent":       call.Intent,
					"slots_so_far": riSlotsSoFar,
					"slots_needed": riMissingNames,
				}))
			return &TurnOutcome{
				Mode:           ModeClarify,
				NewState:       journey.State,
				PendingIntent:  call.Intent,
				PendingSlots:   riSlotsSoFar,
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
		// Site 3: dual-write journal entries for the RunIntent rejection turn.
		riFailJEntries := journalEntriesForEvents(sid, turnNum, time.Now(), failureEvents,
			journey.World, journey.World, "", journey.State, intentName)
		if appendErr := o.store.AppendEventsAndJournal(sid, failureEvents, riFailJEntries); appendErr != nil {
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

	// Post-bind emit_intent dispatch — see settlePostBindEmits doc.
	var harnessErrMsg string
	if hostRedirect == "" && result.ValidationError == nil {
		harnessErrMsg = o.settlePostBindEmits(ctx, sid, &result, tl, 0)
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

	// Site 4: dual-write journal entries for the RunIntent success turn.
	riSuccJEntries := journalEntriesForEvents(sid, turnNum, time.Now(), successEvents,
		journey.World, result.World, result.View, result.NewState, intentName)
	if appendErr := o.store.AppendEventsAndJournal(sid, successEvents, riSuccJEntries); appendErr != nil {
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

	// (Re-)arm any Timeout: declared on the new state, cancelling any
	// pre-existing timeout on the state we just exited.
	o.armTimeoutForState(sid, journey.State, result.NewState)

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
		HarnessError:   harnessErrMsg,
	}, nil
}
