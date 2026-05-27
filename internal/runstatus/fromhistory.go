package runstatus

import (
	"database/sql"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/journal"
	"kitsoki/internal/store"
	"kitsoki/internal/viz"
)

// promptPreviewLen is the maximum number of characters included in the
// prompt_preview attr of oracle.<verb>.start events. Matches the cap used
// by export_status.go's lean slog merging path.
const promptPreviewLen = 200

// historyOptions collects functional options for FromHistory.
type historyOptions struct {
	journalDB *sql.DB
}

// HistoryOption is a functional option for FromHistory.
type HistoryOption func(*historyOptions)

// WithOracleJournal instructs FromHistory to load KindOracleCall entries from
// db and synthesise oracle.<verb>.start / oracle.<verb>.complete TraceEvents
// merged into the output stream. db must be the same SQLite database that the
// session's journal was written to (the in-memory DB for fromflow, the on-disk
// sessions.db for fromsession).
//
// When db is nil the option is a no-op, matching the zero-arg behaviour.
func WithOracleJournal(db *sql.DB) HistoryOption {
	return func(o *historyOptions) {
		o.journalDB = db
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
// opts may include WithOracleJournal to synthesise oracle trace events from
// journal data. Callers that pass no opts get identical behaviour to the
// original signature.
func FromHistory(hist store.History, def *app.AppDef, sessionID string, opts ...HistoryOption) (Snapshot, error) {
	var ho historyOptions
	for _, o := range opts {
		o(&ho)
	}

	events, currentState, lastTurn, terminal, started := mapHistory(hist, def)

	// Synthesise oracle events from the journal if a DB was supplied.
	if ho.journalDB != nil {
		oracleEvents, err := synthesiseOracleEvents(ho.journalDB, app.SessionID(sessionID), events)
		if err == nil && len(oracleEvents) > 0 {
			events = mergeEventsByTime(events, oracleEvents)
		}
		// Non-fatal: if the journal query fails we still emit the store events.
	}

	fc, err := viz.FlowchartWithMap(def, viz.FlowchartOptions{Detail: viz.DetailStates})
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
		},
		App: def,
		Mermaid: MermaidSnapshot{
			Source:  fc.Source,
			NodeMap: fc.NodeMap,
		},
		Events: events,
	}, nil
}

// mapHistory translates store.Event records into TraceEvent records, threading
// the most recent state path through (events don't carry one inline; the SPA
// expects each event to be tagged with the state the session was in).
func mapHistory(hist store.History, def *app.AppDef) (out []TraceEvent, currentState string, lastTurn int, terminal bool, started time.Time) {
	out = make([]TraceEvent, 0, len(hist))
	for i, ev := range hist {
		if i == 0 {
			started = ev.Ts
		}

		var payload map[string]any
		if len(ev.Payload) > 0 {
			_ = json.Unmarshal(ev.Payload, &payload)
		}

		if ev.Kind == store.StateEntered {
			if sp, ok := payload["state"].(string); ok {
				currentState = sp
			}
		}

		out = append(out, TraceEvent{
			Time:      ev.Ts,
			Level:     levelFor(ev.Kind),
			Msg:       msgFor(ev.Kind),
			Turn:      int(ev.Turn),
			StatePath: currentState,
			Attrs:     payload,
		})

		if int(ev.Turn) > lastTurn {
			lastTurn = int(ev.Turn)
		}
	}

	if currentState != "" {
		if st, ok := app.Compile(def).LookupState(app.StatePath(strings.ReplaceAll(currentState, "/", "."))); ok && st != nil && st.Terminal {
			terminal = true
		}
	}
	return
}

// msgFor maps a stored EventKind to the slog `msg` convention the SPA uses
// to pick subsystem chips. The prefixes (turn./harness./machine./host./oracle.)
// match those emitted by the live engine's slog handlers.
func msgFor(k store.EventKind) string {
	switch k {
	case store.TurnStarted:
		return "turn.start"
	case store.TurnEnded:
		return "turn.end"
	case store.LLMCalled:
		return "oracle.ask.start"
	case store.LLMToolCall:
		return "oracle.tool_call"
	case store.ValidationFailed:
		return "machine.validation_failed"
	case store.TransitionApplied:
		return "machine.transition"
	case store.EffectApplied:
		return "machine.effect"
	case store.HostInvoked:
		return "harness.called"
	case store.HostDispatched:
		return "harness.dispatched"
	case store.HostReturned:
		return "harness.returned"
	case store.StateExited:
		return "machine.state_exited"
	case store.StateEntered:
		return "machine.state_entered"
	case store.IntentAccepted:
		return "machine.intent_accepted"
	case store.GuardRejected:
		return "machine.guard_rejected"
	case store.OffPathEntered:
		return "machine.off_path_entered"
	case store.OffPathExited:
		return "machine.off_path_exited"
	case store.OffPathQuestion:
		return "oracle.off_path.question"
	case store.OffPathAnswer:
		return "oracle.off_path.answer"
	case store.JobSubmitted:
		return "scheduler.submitted"
	case store.JobCompleted:
		return "scheduler.completed"
	case store.TimeoutFired:
		return "machine.timeout"
	case store.HarnessError:
		return "harness.error"
	}
	return "event." + string(k)
}

func levelFor(k store.EventKind) string {
	switch k {
	case store.HarnessError, store.ValidationFailed, store.GuardRejected:
		return "ERROR"
	}
	return "INFO"
}

// synthesiseOracleEvents generates oracle.<verb>.start and oracle.<verb>.complete
// TraceEvent pairs from KindOracleCall journal entries for sessionID.
//
// For each entry:
//   - oracle.<verb>.start  — timestamp = entry.Ts - duration_ms; attrs = {verb, agent, model, call_id, prompt_preview}
//   - oracle.<verb>.complete — timestamp = entry.Ts; attrs = full OracleCallBody merged via MergeOracleBodyIntoAttrs
//
// The state_path for synthesised events is copied from the nearest-preceding
// store event (same approach mapHistory uses). existing is the store-derived
// event slice already in timestamp order.
func synthesiseOracleEvents(db *sql.DB, sid app.SessionID, existing []TraceEvent) ([]TraceEvent, error) {
	entries, err := journal.LoadOracleCallEntries(db, sid)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, nil
	}

	out := make([]TraceEvent, 0, len(entries)*2)

	for _, entry := range entries {
		// Parse the minimal fields needed for the start event.
		var partial struct {
			Verb     string `json:"verb"`
			Agent    string `json:"agent"`
			Model    string `json:"model"`
			CallID   string `json:"call_id"`
			Duration int64  `json:"duration_ms"`
			Prompt   string `json:"prompt"`
		}
		if err := json.Unmarshal(entry.Body, &partial); err != nil {
			continue
		}
		verb := partial.Verb
		if verb == "" {
			continue
		}

		// Derive start timestamp.
		startTs := entry.Ts
		if partial.Duration > 0 {
			startTs = entry.Ts.Add(-time.Duration(partial.Duration) * time.Millisecond)
		}

		// Prompt preview (truncated to promptPreviewLen chars).
		preview := partial.Prompt
		if len([]rune(preview)) > promptPreviewLen {
			runes := []rune(preview)
			preview = string(runes[:promptPreviewLen])
		}

		// state_path for synthesised events: nearest-preceding store event.
		statePath := nearestStatePath(existing, startTs)

		// oracle.<verb>.start
		out = append(out, TraceEvent{
			Time:      startTs,
			Level:     "INFO",
			Msg:       "oracle." + verb + ".start",
			Turn:      int(entry.Turn),
			StatePath: statePath,
			Attrs: map[string]any{
				"verb":           verb,
				"agent":          partial.Agent,
				"model":          partial.Model,
				"call_id":        partial.CallID,
				"prompt_preview": preview,
			},
		})

		// oracle.<verb>.complete — merge full body into attrs.
		completeAttrs := map[string]any{}
		MergeOracleBodyIntoAttrs(completeAttrs, entry.Body)

		out = append(out, TraceEvent{
			Time:      entry.Ts,
			Level:     "INFO",
			Msg:       "oracle." + verb + ".complete",
			Turn:      int(entry.Turn),
			StatePath: nearestStatePath(existing, entry.Ts),
			Attrs:     completeAttrs,
		})
	}
	return out, nil
}

// nearestStatePath returns the state_path from the last store event whose
// timestamp is <= ts. Returns "" if no store event precedes ts.
func nearestStatePath(events []TraceEvent, ts time.Time) string {
	state := ""
	for _, ev := range events {
		if ev.Time.After(ts) {
			break
		}
		if ev.StatePath != "" {
			state = ev.StatePath
		}
	}
	return state
}

// mergeEventsByTime merges two slices of TraceEvents into one sorted by Time.
// Both input slices are assumed to be individually sorted.
func mergeEventsByTime(a, b []TraceEvent) []TraceEvent {
	merged := make([]TraceEvent, 0, len(a)+len(b))
	merged = append(merged, a...)
	merged = append(merged, b...)
	sort.SliceStable(merged, func(i, j int) bool {
		return merged[i].Time.Before(merged[j].Time)
	})
	return merged
}
