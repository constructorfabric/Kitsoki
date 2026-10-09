package bugreport

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"
	"unicode/utf8"

	"kitsoki/internal/app"
	"kitsoki/internal/runstatus"
	"kitsoki/internal/runstatus/harscrub"
	"kitsoki/internal/store"
)

const depersonalizedTraceKind = "TEXT"

var traceSafeStringKeys = map[string]bool{
	"agent":            true,
	"call_id":          true,
	"chosen_intent":    true,
	"component":        true,
	"decider":          true,
	"error_code":       true,
	"format":           true,
	"from":             true,
	"from_decision_id": true,
	"from_state":       true,
	"handler":          true,
	"harness":          true,
	"id":               true,
	"intent":           true,
	"kind":             true,
	"level":            true,
	"match_type":       true,
	"model":            true,
	"msg":              true,
	"parent_trace_id":  true,
	"phase":            true,
	"profile":          true,
	"provider":         true,
	"reason":           true,
	"role":             true,
	"route":            true,
	"routed_by":        true,
	"schema_version":   true,
	"state":            true,
	"state_path":       true,
	"status":           true,
	"target":           true,
	"task_trace_id":    true,
	"to":               true,
	"to_decision_id":   true,
	"to_state":         true,
	"trace_id":         true,
	"type":             true,
	"verb":             true,
}

var traceSafeStringListKeys = map[string]bool{
	"available_intents": true,
	"intents":           true,
	"removed":           true,
}

type traceRecord struct {
	Turn       app.TurnNumber  `json:"turn"`
	Seq        int             `json:"seq"`
	Ts         time.Time       `json:"ts"`
	Kind       store.EventKind `json:"kind"`
	StatePath  app.StatePath   `json:"state_path,omitempty"`
	ParentTurn app.TurnNumber  `json:"parent_turn,omitempty"`
	CallID     string          `json:"call_id,omitempty"`
	EpisodeID  string          `json:"episode_id,omitempty"`
	MatchIdx   int             `json:"match_idx,omitempty"`
	Payload    json.RawMessage `json:"payload"`
}

// ReadTraceEvents reads either Kitsoki's store-event JSONL trace or the older
// slog TraceEvent JSONL shape, converting both into runstatus.TraceEvent values.
func ReadTraceEvents(path, sourceID string) ([]runstatus.TraceEvent, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return TraceEventsFromJSONL(f, sourceID)
}

// TraceEventsFromBytes parses a JSONL blob into runstatus trace events.
func TraceEventsFromBytes(data []byte, sourceID string) ([]runstatus.TraceEvent, error) {
	return TraceEventsFromJSONL(bytes.NewReader(data), sourceID)
}

// TraceEventsFromHistory converts an in-memory store history into the same
// trace-event shape used by JSONL-backed reports.
func TraceEventsFromHistory(hist store.History, sourceID string) []runstatus.TraceEvent {
	events := make([]runstatus.TraceEvent, 0, len(hist))
	for _, ev := range hist {
		te := runstatus.ToTraceEvent(ev)
		if te.SessionID == "" {
			te.SessionID = sourceID
		}
		events = append(events, te)
	}
	runstatus.AggregateTaskDetails(events)
	return events
}

// TraceEventsFromJSONL parses a JSONL trace. It skips malformed lines so a
// partially-written final trace line does not prevent bug filing.
func TraceEventsFromJSONL(r io.Reader, sourceID string) ([]runstatus.TraceEvent, error) {
	var events []runstatus.TraceEvent
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var probe struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(line, &probe) == nil && probe.Kind == "session.header" {
			continue
		}
		if ev, ok := decodeStoreEvent(line); ok {
			te := runstatus.ToTraceEvent(ev)
			if te.SessionID == "" {
				te.SessionID = sourceID
			}
			events = append(events, te)
			continue
		}
		var te runstatus.TraceEvent
		if err := json.Unmarshal(line, &te); err != nil {
			continue
		}
		if te.SessionID == "" {
			te.SessionID = sourceID
		}
		events = append(events, te)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	runstatus.AggregateTaskDetails(events)
	return events, nil
}

func decodeStoreEvent(line []byte) (store.Event, bool) {
	var rec traceRecord
	if err := json.Unmarshal(line, &rec); err != nil || rec.Kind == "" || rec.Kind == "session.header" {
		return store.Event{}, false
	}
	payload := rec.Payload
	if payload == nil {
		payload = json.RawMessage(`{}`)
	}
	return store.Event{
		Turn:       rec.Turn,
		Seq:        rec.Seq,
		Ts:         rec.Ts,
		Kind:       rec.Kind,
		StatePath:  rec.StatePath,
		Payload:    payload,
		ParentTurn: rec.ParentTurn,
		CallID:     rec.CallID,
		EpisodeID:  rec.EpisodeID,
		MatchIdx:   rec.MatchIdx,
	}, true
}

// DepersonalizedTraceJSONL renders trace events as JSONL with user free text
// aliased and credential/home-path patterns scrubbed.
func DepersonalizedTraceJSONL(events []runstatus.TraceEvent, opts harscrub.ScrubOptions) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	redactor := newTraceRedactor(opts)
	for _, ev := range events {
		line := redactor.depersonalizedTraceEvent(ev)
		if err := enc.Encode(line); err != nil {
			continue
		}
	}
	return buf.Bytes()
}

// DepersonalizedTraceValue applies the trace redaction rules to a structured
// value. It is useful for sibling evidence such as a session world snapshot.
func DepersonalizedTraceValue(key string, v any, opts harscrub.ScrubOptions) any {
	return newTraceRedactor(opts).redactTraceValue(key, v)
}

type traceRedactor struct {
	opts    harscrub.ScrubOptions
	aliases map[string]string
	next    int
}

func newTraceRedactor(opts harscrub.ScrubOptions) *traceRedactor {
	return &traceRedactor{
		opts:    opts,
		aliases: make(map[string]string),
	}
}

func (r *traceRedactor) depersonalizedTraceEvent(ev runstatus.TraceEvent) map[string]any {
	out := map[string]any{
		"level":      ev.Level,
		"msg":        ev.Msg,
		"session_id": scrubSafeTraceString(ev.SessionID, r.opts),
		"turn":       ev.Turn,
		"state_path": scrubSafeTraceString(ev.StatePath, r.opts),
	}
	if !ev.Time.IsZero() {
		out["time"] = ev.Time.Format(time.RFC3339Nano)
	}
	if ev.ParentTurn != 0 {
		out["parent_turn"] = ev.ParentTurn
	}
	for k, v := range ev.Attrs {
		if _, exists := out[k]; exists {
			continue
		}
		out[k] = r.redactTraceValue(k, v)
	}
	return out
}

func (r *traceRedactor) redactTraceValue(key string, v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case bool, float64, int, int64, json.Number:
		return x
	case string:
		if traceSafeStringKeys[key] {
			return scrubSafeTraceString(x, r.opts)
		}
		return r.aliasString(x)
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			if s, ok := item.(string); ok && traceSafeStringListKeys[key] {
				out[i] = scrubSafeTraceString(s, r.opts)
				continue
			}
			out[i] = r.redactTraceValue("", item)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for childKey, childVal := range x {
			out[childKey] = r.redactTraceValue(childKey, childVal)
		}
		return out
	default:
		return r.aliasString(fmt.Sprint(x))
	}
}

func (r *traceRedactor) aliasString(s string) string {
	scrubbed := harscrub.ScrubString(s, r.opts)
	if scrubbed == "" {
		return ""
	}
	if alias, ok := r.aliases[s]; ok {
		return alias
	}
	r.next++
	alias := fmt.Sprintf("[%s#%d len=%d]", depersonalizedTraceKind, r.next, utf8.RuneCountInString(scrubbed))
	r.aliases[s] = alias
	return alias
}

func scrubSafeTraceString(s string, opts harscrub.ScrubOptions) string {
	return harscrub.ScrubString(s, opts)
}
