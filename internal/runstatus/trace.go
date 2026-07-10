package runstatus

import (
	"bufio"
	"encoding/json"
	"io"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/store"
	"kitsoki/internal/viz"
)

// HeaderOverrides supplies caller-chosen values that win over the ones derived
// from the trace when synthesising a [SessionHeader]. Empty fields fall back to
// the derived value. StartedAt is parsed as RFC3339; an unparseable value is
// ignored.
type HeaderOverrides struct {
	SessionID    string
	CurrentState string
	StartedAt    string
}

// ParseTrace reads a JSONL trace stream (one slog record per line, the shape
// kitsoki run --trace writes) into TraceEvents. It is lenient by design: a
// line that fails to parse is skipped rather than aborting the whole read, so
// a partial final line from an interrupted run still yields a usable result.
// When warn is non-nil it is called once per skipped line with the 1-based
// line number and the decode error.
//
// This is the full-fidelity trace path: unlike events loaded from the SQLite
// store, JSONL lines carry state_path, call_id, and parent_turn, so the
// resulting TraceEvents preserve everything the SPA needs (notably agent-call
// pairing by call_id).
func ParseTrace(r io.Reader, warn func(line int, err error)) ([]TraceEvent, error) {
	var events []TraceEvent
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1<<20), 1<<20) // 1 MB line buffer

	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev TraceEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			if warn != nil {
				warn(lineNum, err)
			}
			continue
		}
		events = append(events, ev)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

// SnapshotFromTrace assembles a full [Snapshot] from parsed trace events and an
// AppDef: it aggregates task tool-call/file-change detail onto agent.task
// events, synthesises the session header (honouring ov), and — when withMermaid
// is set — renders the diagram. A diagram render failure is non-fatal: the
// Snapshot is returned with an empty Mermaid block and the SPA omits the
// diagram panel.
//
// It is the shared builder behind both `kitsoki export-status --from-trace`
// and the live `kitsoki status serve`, so the exported artifact and the live
// view are built from the same code.
func SnapshotFromTrace(def *app.AppDef, events []TraceEvent, ov HeaderOverrides, withMermaid bool) Snapshot {
	AggregateTaskDetails(events)

	var mermaid MermaidSnapshot
	if withMermaid {
		if fc, err := viz.FlowchartWithMap(def, viz.FlowchartOptions{Detail: viz.DetailStates, Banners: true}); err == nil {
			mermaid = MermaidSnapshot{Source: fc.Source, NodeMap: fc.NodeMap}
		}
	}

	return Snapshot{
		Session: SessionHeaderFromTrace(def, events, ov),
		App:     def,
		Mermaid: mermaid,
		Events:  events,
	}
}

// SessionHeaderFromTrace derives a SessionHeader from trace events plus def,
// with ov overriding the derived values.
//
// Derivation (overrides win):
//   - SessionID: ov, else first non-empty session_id from events.
//   - AppID: always def.App.ID.
//   - CurrentState: ov, else state_path of the last event that carries one.
//   - Turn: max turn across events.
//   - StartedAt: ov (RFC3339), else earliest non-zero event time.
//   - Terminal: whether CurrentState is a terminal state in def.
func SessionHeaderFromTrace(def *app.AppDef, events []TraceEvent, ov HeaderOverrides) SessionHeader {
	var (
		sessionID   string
		lastEntered string // state_path of the most recent state_entered event
		lastAny     string // most recent non-empty state_path (any event)
		maxTurn     int
		startedAt   time.Time
	)

	enteredMsg := string(store.StateEntered)
	for _, ev := range events {
		if sessionID == "" && ev.SessionID != "" {
			sessionID = ev.SessionID
		}
		// A state_entered event is authoritative for the current state; other
		// events (notably turn.end, stamped with the turn's STARTING state)
		// carry whatever was active when written and must not mask it. Prefer
		// the last state_entered, falling back to the last non-empty state_path.
		if ev.Msg == enteredMsg && ev.StatePath != "" {
			lastEntered = ev.StatePath
		}
		if ev.StatePath != "" {
			lastAny = ev.StatePath
		}
		if ev.Turn > maxTurn {
			maxTurn = ev.Turn
		}
		if !ev.Time.IsZero() && (startedAt.IsZero() || ev.Time.Before(startedAt)) {
			startedAt = ev.Time
		}
	}

	currentState := lastEntered
	if currentState == "" {
		currentState = lastAny
	}

	if ov.SessionID != "" {
		sessionID = ov.SessionID
	}
	if ov.CurrentState != "" {
		currentState = ov.CurrentState
	}
	if ov.StartedAt != "" {
		if t, err := time.Parse(time.RFC3339, ov.StartedAt); err == nil {
			startedAt = t
		}
	}

	return SessionHeader{
		SessionID:    sessionID,
		AppID:        def.App.ID,
		CurrentState: currentState,
		Turn:         maxTurn,
		StartedAt:    startedAt,
		Terminal:     isStateTerminal(def, currentState),
		OperationRun: operationRunSummaryFromTraceEvents(events),
	}
}

// isStateTerminal reports whether the state at statePath in def is terminal.
// Empty or unknown paths are not terminal.
func isStateTerminal(def *app.AppDef, statePath string) bool {
	if def == nil || statePath == "" {
		return false
	}
	s, ok := app.Compile(def).LookupState(app.StatePath(statePath))
	return ok && s != nil && s.Terminal
}

// taskTraceWindow accumulates task.tool and task.end data keyed by task_trace_id.
type taskTraceWindow struct {
	toolCalls    []map[string]any
	filesChanged []map[string]any
}

// AggregateTaskDetails scans events and, for every agent.task.complete event,
// attaches tool_calls (from task.tool events) and files_changed (from task.end
// events) emitted during the same task invocation, correlated by task trace id.
// It mutates events in place and never overwrites detail already present.
func AggregateTaskDetails(events []TraceEvent) {
	byTraceID := make(map[string]*taskTraceWindow)

	// Pass 1: collect task.tool and task.end events indexed by task_trace_id.
	for i := range events {
		ev := &events[i]
		switch ev.Msg {
		case "task.tool":
			traceID, _ := ev.Attrs["parent_trace_id"].(string)
			if traceID == "" {
				traceID, _ = ev.Attrs["trace_id"].(string)
			}
			if traceID == "" {
				continue
			}
			w := taskTraceWindowFor(byTraceID, traceID)
			w.toolCalls = append(w.toolCalls, taskToolCallFromEvent(ev))

		case "task.end":
			traceID, _ := ev.Attrs["task_trace_id"].(string)
			if traceID == "" {
				continue
			}
			w := taskTraceWindowFor(byTraceID, traceID)
			if rawFiles, ok := ev.Attrs["files_changed"]; ok && w.filesChanged == nil {
				w.filesChanged = buildFilesChanged(rawFiles)
			}
		}
	}

	if len(byTraceID) == 0 {
		return
	}

	// Pass 2: attach aggregated data to agent.task.complete events.
	for i := range events {
		ev := &events[i]
		if ev.Msg != "agent.task.complete" {
			continue
		}
		traceID, _ := ev.Attrs["task_trace_id"].(string)
		if traceID == "" {
			continue
		}
		w, ok := byTraceID[traceID]
		if !ok {
			continue
		}
		if ev.Attrs == nil {
			ev.Attrs = make(map[string]any)
		}
		if len(w.toolCalls) > 0 {
			if _, already := ev.Attrs["tool_calls"]; !already {
				ev.Attrs["tool_calls"] = w.toolCalls
			}
		}
		if len(w.filesChanged) > 0 {
			if _, already := ev.Attrs["files_changed"]; !already {
				ev.Attrs["files_changed"] = w.filesChanged
			}
		}
	}
}

func taskTraceWindowFor(m map[string]*taskTraceWindow, traceID string) *taskTraceWindow {
	if w, ok := m[traceID]; ok {
		return w
	}
	w := &taskTraceWindow{}
	m[traceID] = w
	return w
}

// taskToolCallFromEvent converts a task.tool event into the tool_calls entry
// shape defined in AGENT_ATTRS.md.
func taskToolCallFromEvent(ev *TraceEvent) map[string]any {
	entry := make(map[string]any)
	if seq, ok := ev.Attrs["seq"]; ok {
		entry["seq"] = seq
	}
	if tool, ok := ev.Attrs["tool"].(string); ok {
		entry["tool"] = tool
	}
	if preview, ok := ev.Attrs["input_preview"].(string); ok && preview != "" {
		entry["args"] = map[string]any{"preview": preview}
	} else if preview, ok := ev.Attrs["preview"].(string); ok && preview != "" {
		entry["args"] = map[string]any{"preview": preview}
	}
	if out, ok := ev.Attrs["output_preview"].(string); ok && out != "" {
		entry["result"] = out
	}
	return entry
}

// buildFilesChanged converts a task.end files_changed value (a path list) into
// the files_changed shape defined in AGENT_ATTRS.md, defaulting status to
// "modified" with no diff.
func buildFilesChanged(rawFiles any) []map[string]any {
	arr, ok := rawFiles.([]any)
	if !ok {
		return nil
	}
	var out []map[string]any
	for _, item := range arr {
		path, _ := item.(string)
		if path == "" {
			continue
		}
		out = append(out, map[string]any{
			"path":   path,
			"status": "modified",
		})
	}
	return out
}
