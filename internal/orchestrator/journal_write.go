// Package orchestrator — dual-write helpers for continue-mode §4.9 Rule 1.
//
// Every site that calls store.AppendEvents is migrated to
// store.AppendEventsAndJournal.  This file contains:
//   - journalEntriesForEvents: walks a []store.Event and returns the matching
//     []journal.Entry batch (world.patch, state.transition, host.*, typed).
//   - standalone journal-write helpers for post-commit / no-events paths
//     (timeout.armed, timeout.cancelled, clarify.requested, clarify.answered).
package orchestrator

import (
	"encoding/json"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/journal"
	"kitsoki/internal/store"
	"kitsoki/internal/world"
)

// journalEntriesForEvents builds the journal.Entry batch that accompanies a
// []store.Event batch being written via AppendEventsAndJournal.
//
// Rules (per continue-mode proposal §2.2 and call-sites notes):
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
			_ = json.Unmarshal(ev.Payload, &p)
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

	// Emit view.rendered on TurnEnded.
	if hasTurnEnded {
		vrBody, _ := json.Marshal(map[string]any{
			"view_text":  viewText,
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
