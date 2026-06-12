// Package orchestrator — dual-write helpers for continue-mode (journal +
// event store written together so a resumed session sees both).
//
// Wave 2a: every site that previously called store.AppendEventsAndJournal is
// migrated to appendEventsAndJournal, which routes event writes through an
// EventSink (store.NewStoreSinkAdapter for the wave-2a SQLite backend) and
// journal writes through appendJournal (journalWriter, if wired).  This file
// contains:
//   - appendEventsAndJournal: the wave-2a write helper; replaces direct
//     o.store.AppendEventsAndJournal call sites.
//   - journalEntriesForEvents: walks a []store.Event and returns the matching
//     []journal.Entry batch (world.patch, state.transition, host.*, typed).
//   - standalone journal-write helpers for post-commit / no-events paths
//     (timeout.armed, timeout.cancelled, clarify.requested, clarify.answered).
package orchestrator

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/intent"
	"kitsoki/internal/journal"
	"kitsoki/internal/store"
	"kitsoki/internal/trace"
	"kitsoki/internal/world"
)

// appendEventsAndJournal is the wave-2a replacement for every
// o.store.AppendEventsAndJournal call site.  Event writes go through a
// StoreSinkAdapter (wrapping the SQLite store) via AppendBatch; journal
// writes go through o.appendJournal (the journalWriter, if wired).
//
// Wave 3-entry dual-write: when o.eventSink is non-nil (e.g. a *store.JSONLSink
// wired via WithEventSink), events are appended to BOTH the JSONL sink AND the
// SQLite store.  This keeps the SQLite store current so other subcommands
// (session show, session list, attach-session resume) continue to work against
// it until phase B removes SQLite event storage entirely.  The JSONL sink is
// the authoritative future path; the SQLite write is the backward-compat bridge.
//
// Journal writes proceed through o.appendJournal as before.
//
// When o.store is nil AND o.eventSink is nil (nil-store test scaffolds),
// the call is a no-op.
func (o *Orchestrator) appendEventsAndJournal(sid app.SessionID, events []store.Event, jEntries []journal.Entry) error {
	if o.eventSink != nil {
		// JSONL dual-write path: append each event to the JSONL sink.
		// The SQLite write follows below so all subcommands stay consistent.
		for _, ev := range events {
			// A SinkFlushed event was already written to the sink live (before
			// a blocking host invoke — see dispatchHostCalls). Re-appending it
			// here would duplicate the JSONL line, so skip the sink write; it
			// still falls through to the SQLite write below.
			if ev.SinkFlushed {
				continue
			}
			if err := o.eventSink.Append(ev); err != nil {
				return err
			}
		}
	}
	if o.store != nil {
		adapter := store.NewStoreSinkAdapter(o.store, sid)
		if err := adapter.AppendBatch(events); err != nil {
			return err
		}
	}
	for _, e := range jEntries {
		o.appendJournal(e)
	}
	return nil
}

// journalTurnError records a turn that aborted because o.machine.Turn
// returned an error — e.g. an effect's `set:` / `when:` expression failed
// to compile or evaluate. Such a fault used to propagate to the caller
// with NO trace written: the session JSONL kept only the last good turn,
// so a TUI bounce-to-idle was impossible to diagnose from the trace.
//
// It writes a self-contained TurnStarted → MachineError → TurnEnded
// (outcome:"error") store-event sequence so the failure shows up in the
// session trace exactly where it happened, and mirrors the error to the
// slog trace logger (the KITSOKI_TRACE_FILE sink). Best-effort: a sink
// append failure is logged, never returned — the original machine error
// must still propagate to the caller unchanged.
func (o *Orchestrator) journalTurnError(
	ctx context.Context,
	tl *trace.TurnLogger,
	sid app.SessionID,
	turnNum app.TurnNumber,
	state app.StatePath,
	call intent.IntentCall,
	w world.World,
	cause error,
) {
	if tl != nil {
		tl.Warn(ctx, trace.EvTurnError,
			slog.String("intent", call.Intent),
			slog.String("state", string(state)),
			slog.String("error", cause.Error()),
		)
	}

	errEvents := []store.Event{
		newOrchestratorEvent(store.TurnStarted, map[string]any{
			"turn":  int64(turnNum),
			"input": "[intent] " + call.Intent,
		}, turnNum),
		newOrchestratorEvent(store.MachineError, map[string]any{
			"intent": call.Intent,
			"slots":  slotsToMap(call.Slots),
			"state":  string(state),
			"error":  cause.Error(),
		}, turnNum),
		newOrchestratorEvent(store.TurnEnded, map[string]any{
			"outcome": "error",
			"error":   cause.Error(),
		}, turnNum),
	}
	for i := range errEvents {
		errEvents[i].Turn = turnNum
	}
	stampStatePathPerEvent(errEvents)
	stampStatePath(errEvents, state, o.InitialState())

	jEntries := journalEntriesForEvents(sid, turnNum, time.Now(), errEvents,
		w, w, "", state, call.Intent)
	if appendErr := o.appendEventsAndJournal(sid, errEvents, jEntries); appendErr != nil {
		o.logger.WarnContext(ctx, "orchestrator: failed to journal turn error",
			slog.String("session_id", string(sid)),
			slog.String("append_error", appendErr.Error()),
			slog.String("cause", cause.Error()),
		)
	}
}

// journalEntriesForEvents builds the journal.Entry batch that accompanies a
// []store.Event batch being written via AppendEventsAndJournal.
//
// Rules (which events earn a dedicated journal entry):
//   - TurnStarted, IntentAccepted, StateExited, StateEntered, LLMToolCall,
//     JobSubmitted, JobCompleted → no dedicated journal entry (covered by
//     state.transition / world.patch summaries or out of scope).
//   - TransitionApplied → one state.transition entry (doc="state").
//   - EffectApplied → contributes to the accumulated world.patch; emitted once
//     at the end as a single world.patch entry covering all set/increment ops.
//   - HostInvoked → host.invoked entry.
//   - HostDispatched → host.dispatched entry.
//   - HostReturned → host.returned entry.
//   - ValidationFailed / GuardRejected → guard.rejected entry.
//   - OffPathQuestion → offpath.question entry.
//   - OffPathAnswer → offpath.answer entry.
//   - OffPathEntered → offpath.entered entry.
//   - OffPathExited → offpath.exited entry.
//   - TimeoutFired → timeout.fired entry.
//   - TurnEnded → view.rendered entry (body from viewText arg).
//
// preWorld / postWorld supply the world snapshot before and after the turn so
// the world.patch ops list can be computed from the diff of Vars maps.
// viewText is the rendered view string for the TurnEnded view.rendered entry
// (pass "" if no view is available, e.g. rejected paths).
// currentStatePath is the state path after the transition (for view.rendered body).
// userInput is the user-facing input string that drove this turn (free-text input,
// slot-fill answer, etc.). Empty for synthetic turns (bg-job completion, timeout).
// Resume uses it to render the "> input" header on the resumed transcript row;
// without it the user can't tell what they typed before the restart.
// sid, turnNum, ts are used to populate every entry's identifying fields.
func journalEntriesForEvents(
	sid app.SessionID,
	turnNum app.TurnNumber,
	ts time.Time,
	events []store.Event,
	preWorld world.World,
	postWorld world.World,
	viewText string,
	currentStatePath app.StatePath,
	userInput string,
) []journal.Entry {
	var entries []journal.Entry
	seq := 0

	newEntry := func(kind string, doc journal.DocID, body any) journal.Entry {
		raw, _ := json.Marshal(body)
		e := journal.Entry{
			Ts:      ts,
			Session: sid,
			Turn:    turnNum,
			Seq:     seq,
			Kind:    kind,
			Doc:     doc,
			Body:    json.RawMessage(raw),
		}
		seq++
		return e
	}

	// Accumulated world-patch ops from EffectApplied events.
	var worldPatchOps []journal.PatchOp

	// Track whether we have a transition so we know to emit state.transition.
	var stateTransitionEntry *journal.Entry
	var hasGuardRejected bool

	for _, ev := range events {
		switch ev.Kind {

		case store.TransitionApplied:
			var p struct {
				From   string `json:"from"`
				To     string `json:"to"`
				Intent string `json:"intent"`
			}
			if err := json.Unmarshal(ev.Payload, &p); err != nil {
				// A malformed TransitionApplied payload would silently zero
				// from/to/intent in the state.transition journal entry, making
				// replay/inspection misleading. Surface it rather than discard.
				slog.Default().Error("orchestrator: journal: unmarshal TransitionApplied payload",
					slog.String("session_id", string(sid)),
					slog.Int("turn", int(turnNum)),
					slog.String("err", err.Error()),
				)
			}
			e := newEntry(journal.KindStateTransition, "state", map[string]any{
				"from":   p.From,
				"to":     p.To,
				"intent": p.Intent,
			})
			stateTransitionEntry = &e

		case store.EffectApplied:
			// Parse the effect payload to extract set/increment ops.
			var p struct {
				Set       map[string]any `json:"set,omitempty"`
				Increment map[string]int `json:"increment,omitempty"`
				Say       string         `json:"say,omitempty"`
			}
			if err := json.Unmarshal(ev.Payload, &p); err != nil {
				continue
			}
			// set: → one replace/add op per key
			for k, v := range p.Set {
				val, _ := json.Marshal(v)
				worldPatchOps = append(worldPatchOps, journal.PatchOp{
					Op:    "replace",
					Path:  "/vars/" + k,
					Value: json.RawMessage(val),
				})
			}
			// increment: → replace op with final value from postWorld
			for k := range p.Increment {
				finalVal := postWorld.Vars[k]
				val, _ := json.Marshal(finalVal)
				worldPatchOps = append(worldPatchOps, journal.PatchOp{
					Op:    "replace",
					Path:  "/vars/" + k,
					Value: json.RawMessage(val),
				})
			}
			// say: → no world patch (transcript-only)

		case store.HostInvoked:
			e := newEntry(journal.KindHostInvoked, "", json.RawMessage(ev.Payload))
			entries = append(entries, e)

		case store.HostDispatched:
			e := newEntry(journal.KindHostDispatched, "", json.RawMessage(ev.Payload))
			entries = append(entries, e)

		case store.HostReturned:
			e := newEntry(journal.KindHostReturned, "", json.RawMessage(ev.Payload))
			entries = append(entries, e)

		case store.GuardRejected, store.ValidationFailed:
			// Emit guard.rejected. Only emit once even if multiple events.
			if !hasGuardRejected {
				var p map[string]any
				_ = json.Unmarshal(ev.Payload, &p)
				e := newEntry(journal.KindGuardRejected, "", p)
				entries = append(entries, e)
				hasGuardRejected = true
			}

		case store.OffPathQuestion:
			e := newEntry(journal.KindOffPathQuestion, "", json.RawMessage(ev.Payload))
			entries = append(entries, e)

		case store.OffPathAnswer:
			e := newEntry(journal.KindOffPathAnswer, "", json.RawMessage(ev.Payload))
			entries = append(entries, e)

		case store.OffPathEntered:
			var p map[string]any
			_ = json.Unmarshal(ev.Payload, &p)
			e := newEntry(journal.KindOffPathEntered, "", p)
			entries = append(entries, e)

		case store.OffPathExited:
			var p map[string]any
			_ = json.Unmarshal(ev.Payload, &p)
			e := newEntry(journal.KindOffPathExited, "", p)
			entries = append(entries, e)

		case store.TimeoutFired:
			e := newEntry(journal.KindTimeoutFired, "", json.RawMessage(ev.Payload))
			entries = append(entries, e)
		}
	}

	// Emit state.transition after all events so seq ordering reflects "end of
	// turn" semantics for doc-versioned entries.
	if stateTransitionEntry != nil {
		stateTransitionEntry.Seq = seq
		seq++
		entries = append(entries, *stateTransitionEntry)
	}

	// Emit world.patch (always emitted on turns that have at least a TurnEnded,
	// even if ops list is empty — signals "world snapshot at this turn").
	hasTurnEnded := false
	for _, ev := range events {
		if ev.Kind == store.TurnEnded {
			hasTurnEnded = true
			break
		}
	}
	if hasTurnEnded || stateTransitionEntry != nil || hasGuardRejected {
		opsBody, _ := json.Marshal(worldPatchOps)
		if worldPatchOps == nil {
			opsBody = json.RawMessage("[]")
		}
		wpBody, _ := json.Marshal(map[string]any{"ops": json.RawMessage(opsBody)})
		e := journal.Entry{
			Ts:      ts,
			Session: sid,
			Turn:    turnNum,
			Seq:     seq,
			Kind:    journal.KindWorldPatch,
			Doc:     "world",
			Body:    json.RawMessage(wpBody),
		}
		seq++
		entries = append(entries, e)
	}

	// Emit view.rendered on TurnEnded. The view is recorded with presentation
	// ANSI stripped (sentinels preserved) so the journal entry is deterministic
	// across color profiles — see recordedView.
	if hasTurnEnded {
		vrBody, _ := json.Marshal(map[string]any{
			"view_text":  recordedView(viewText),
			"state_path": string(currentStatePath),
			"user_input": userInput,
		})
		e := journal.Entry{
			Ts:      ts,
			Session: sid,
			Turn:    turnNum,
			Seq:     seq,
			Kind:    journal.KindViewRendered,
			Body:    json.RawMessage(vrBody),
		}
		seq++
		entries = append(entries, e)
	}

	return entries
}

// appendJournal is a safe helper that calls o.journalWriter.Append, doing
// nothing if journalWriter is nil (defensive — tests without a writer still
// work).
func (o *Orchestrator) appendJournal(e journal.Entry) {
	if o.journalWriter == nil {
		return
	}
	_ = o.journalWriter.Append(e)
}

// journalEntry constructs a single journal.Entry with common fields populated.
func journalEntry(sid app.SessionID, turnNum app.TurnNumber, seq int, ts time.Time, kind string, doc journal.DocID, body any) journal.Entry {
	raw, _ := json.Marshal(body)
	return journal.Entry{
		Ts:      ts,
		Session: sid,
		Turn:    turnNum,
		Seq:     seq,
		Kind:    kind,
		Doc:     doc,
		Body:    json.RawMessage(raw),
	}
}
