package runstatus

import (
	"fmt"
	"strings"

	"kitsoki/internal/app"
	"kitsoki/internal/store"
)

func operationRunSummaryFromTraceEvents(events []TraceEvent) *OperationRunSummary {
	var summary *OperationRunSummary
	for _, ev := range events {
		raw := operationRunPayloadFromTraceEvent(ev)
		if len(raw) == 0 {
			continue
		}
		summary = mergeOperationRunSummary(summary, raw)
	}
	return summary
}

func operationRunPayloadFromTraceEvent(ev TraceEvent) map[string]any {
	switch ev.Msg {
	case string(store.EffectApplied):
		return operationRunPayloadFromWorldUpdate(ev.Attrs)
	case string(store.OperationRunStarted),
		string(store.OperationRunPhaseStarted),
		string(store.OperationRunPhaseCompleted),
		string(store.OperationRunWaiting),
		string(store.OperationRunCompleted),
		string(store.OperationRunFailed):
		return ev.Attrs
	default:
		return nil
	}
}

func operationRunPayloadFromWorldUpdate(attrs map[string]any) map[string]any {
	set, ok := attrs["set"].(map[string]any)
	if !ok {
		return nil
	}
	run, ok := set[app.OperationRunWorldKey].(map[string]any)
	if !ok {
		return nil
	}
	return run
}

func mergeOperationRunSummary(prev *OperationRunSummary, raw map[string]any) *OperationRunSummary {
	next := &OperationRunSummary{}
	if prev != nil {
		*next = *prev
	}
	assignString := func(dst *string, keys ...string) {
		for _, key := range keys {
			if value := stringMapValue(raw, key); value != "" {
				*dst = value
				return
			}
		}
	}
	assignString(&next.OperationID, "operation_id")
	assignString(&next.PolicyID, "policy_id")
	assignString(&next.Title, "title")
	assignString(&next.Status, "status")
	assignString(&next.Mode, "mode")
	assignString(&next.ExecutionMode, "execution_mode")
	assignString(&next.From, "from")
	assignString(&next.To, "to")
	assignString(&next.EntryIntent, "entry_intent")
	assignString(&next.Phase, "phase")
	assignString(&next.TerminalState, "terminal_state")
	assignString(&next.TerminalArtifact, "terminal_artifact")
	assignString(&next.TerminalArtifactHandle, "terminal_artifact_handle")
	assignString(&next.StopReason, "stop_reason", "reason")
	assignString(&next.StopDetail, "stop_detail", "detail", "message")
	if value, ok := boolMapValue(raw, "run_in_background"); ok {
		next.RunInBackground = value
	}
	if next.Status == "" {
		next.Status = "running"
	}
	return next
}

func stringMapValue(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(fmt.Sprint(v))
}

func boolMapValue(m map[string]any, key string) (bool, bool) {
	v, ok := m[key]
	if !ok || v == nil {
		return false, false
	}
	switch typed := v.(type) {
	case bool:
		return typed, true
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true", "1", "yes":
			return true, true
		case "false", "0", "no":
			return false, true
		}
	}
	return false, false
}
