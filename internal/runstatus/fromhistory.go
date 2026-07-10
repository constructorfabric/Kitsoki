package runstatus

import (
	"encoding/json"
	"strings"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/store"
	"kitsoki/internal/viz"
)

// FromSink is identical to FromHistory but uses sink.Lines() to populate
// Snapshot.RawLines with the exact bytes the sink wrote (byte-copy-equal),
// rather than re-marshalling each event.  Use this wherever you have access
// to the JSONLSink that holds the history; use FromHistory when only a
// History slice is available (e.g. in-memory synthetic histories in tests).
//
// Contract: a nil sink yields the zero Snapshot and a nil error (the caller
// simply has nothing to snapshot). Otherwise FromSink delegates to FromHistory
// and shares its error contract — it propagates a [viz.FlowchartWithMap]
// failure unchanged. The sink-retained bytes replace RawLines only when their
// count matches the event count, so the Events[i]↔RawLines[i] alignment is
// preserved even if the two ever diverge.
func FromSink(sink *store.JSONLSink, def *app.AppDef, sessionID string) (Snapshot, error) {
	if sink == nil {
		return Snapshot{}, nil
	}
	snap, err := FromHistory(sink.History(), def, sessionID)
	if err != nil {
		return snap, err
	}
	// Replace RawLines with the sink-retained bytes (byte-copy-equal, not
	// encoder-pair-equal).  The two slices have the same length because
	// FromHistory processes exactly the events in sink.History().
	rawLines := sink.Lines()
	if len(rawLines) == len(snap.RawLines) {
		snap.RawLines = rawLines
	}
	return snap, nil
}

// ToTraceEvent maps a single store.Event to a TraceEvent exactly as
// [FromHistory] does per event: the JSON Payload is decoded into Attrs, the
// off-band CallID is merged into Attrs so the SPA sees it, Msg is the event
// Kind, and Level is "ERROR" for the failure kinds (HarnessError,
// ValidationFailed, GuardRejected) and "INFO" otherwise.
//
// It is the single per-event mapping shared by [FromHistory] (and available to
// any other consumer mapping store.Events one at a time) so that path cannot
// drift from the snapshot view. Note that the SQLite store does not persist
// per-event state_path / call_id / parent_turn — those survive only in the
// JSONL trace — so events loaded from the store carry them empty; the
// full-fidelity trace path is [ParseTrace] over the JSONL.
func ToTraceEvent(ev store.Event) TraceEvent {
	var attrs map[string]any
	if len(ev.Payload) > 0 {
		_ = json.Unmarshal(ev.Payload, &attrs)
	}
	if ev.CallID != "" {
		if attrs == nil {
			attrs = make(map[string]any)
		}
		attrs["call_id"] = ev.CallID
	}

	level := "INFO"
	switch ev.Kind {
	case store.HarnessError, store.ValidationFailed, store.GuardRejected:
		level = "ERROR"
	}

	return TraceEvent{
		Time:       ev.Ts,
		Level:      level,
		Msg:        string(ev.Kind),
		Turn:       int(ev.Turn),
		StatePath:  string(ev.StatePath),
		ParentTurn: int(ev.ParentTurn),
		Attrs:      attrs,
	}
}

// FromHistory converts a real store.History into a Snapshot suitable for the
// runstatus UI. Used by both the fromsession exporter (real SQLite-backed
// sessions) and the fromflow exporter (in-memory store from a flow run), so
// the two paths emit identical event shapes.
//
// sessionID is the value to copy into Snapshot.Session.SessionID — the caller
// supplies it since History rows don't carry it.
//
// Every store.Event maps 1:1 to a TraceEvent; no synthesis or back-fill is
// performed. Agent events (AgentCalled, AgentReturned, AgentError) are
// already written inline into the history by the orchestrator and appear in
// Events verbatim.
//
// Contract: def must be non-nil — it is dereferenced for the diagram render
// and the terminal-state lookup. FromHistory returns the zero Snapshot and a
// non-nil error only when [viz.FlowchartWithMap] fails to render def; all
// other steps are infallible (a per-event marshal failure records a nil
// RawLines entry rather than erroring). An empty hist yields a valid Snapshot
// with no events and a zero-time StartedAt. The returned Snapshot is a value
// safe for concurrent reads; see [Snapshot] for the shared-map caveat.
func FromHistory(hist store.History, def *app.AppDef, sessionID string) (Snapshot, error) {
	var (
		lastEntered string // state_path of the most recent state_entered event
		lastAny     string // most recent non-empty state_path (any event)
		lastTurn    int
		terminal    bool
		started     time.Time
	)

	events := make([]TraceEvent, 0, len(hist))
	rawLines := make([][]byte, 0, len(hist))
	for _, ev := range hist {
		if started.IsZero() {
			started = ev.Ts
		}

		te := ToTraceEvent(ev)

		// Track current state for SessionHeader. A state_entered event is
		// authoritative; other events merely carry the state that was active
		// when they were written — notably turn.end is stamped with the turn's
		// STARTING state, so after a transition it must not mask the entered
		// state. Hence: prefer the last state_entered, fall back to the last
		// non-empty state_path (covers traces with no state_entered event).
		if ev.Kind == store.StateEntered {
			switch {
			case te.StatePath != "":
				lastEntered = te.StatePath
			default:
				if sp, ok := te.Attrs["state"].(string); ok {
					lastEntered = sp
				}
			}
		}
		if te.StatePath != "" {
			lastAny = te.StatePath
		}

		if te.Turn > lastTurn {
			lastTurn = te.Turn
		}

		events = append(events, te)

		// Populate RawLines for byte-equality assertions against the source
		// trace. MarshalEventLine produces the same bytes as JSONLSink.Append
		// writes for the same event, so joining snap.RawLines with newlines
		// reproduces the original JSONL event section.
		if raw, merr := store.MarshalEventLine(ev); merr == nil {
			rawLines = append(rawLines, raw)
		} else {
			rawLines = append(rawLines, nil) // gap marker; test can detect
		}
	}

	currentState := lastEntered
	if currentState == "" {
		currentState = lastAny
	}
	if currentState != "" {
		if st, ok := app.Compile(def).LookupState(app.StatePath(strings.ReplaceAll(currentState, "/", "."))); ok && st != nil && st.Terminal {
			terminal = true
		}
	}

	fc, err := viz.FlowchartWithMap(def, viz.FlowchartOptions{Detail: viz.DetailStates, Banners: true})
	if err != nil {
		return Snapshot{}, err
	}

	return Snapshot{
		Session: SessionHeader{
			SessionID:    sessionID,
			AppID:        def.App.ID,
			CurrentState: currentState,
			Turn:         lastTurn,
			StartedAt:    started,
			Terminal:     terminal,
			OperationRun: operationRunSummaryFromTraceEvents(events),
		},
		App: def,
		Mermaid: MermaidSnapshot{
			Source:  fc.Source,
			NodeMap: fc.NodeMap,
		},
		Events:   events,
		RawLines: rawLines,
	}, nil
}
