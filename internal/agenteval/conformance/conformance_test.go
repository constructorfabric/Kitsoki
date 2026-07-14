package conformance

import (
	"strings"
	"testing"

	"kitsoki/internal/store"
)

func TestCheckFileCompliantTracePasses(t *testing.T) {
	report, err := CheckFile("testdata/compliant.jsonl")
	if err != nil {
		t.Fatalf("CheckFile: %v", err)
	}
	if !report.OK() {
		t.Fatalf("compliant trace reported violations: %#v", report.Violations)
	}
	if report.Calls != 1 {
		t.Fatalf("Calls = %d, want 1", report.Calls)
	}
	if report.ToolUses != 3 {
		t.Fatalf("ToolUses = %d, want 3", report.ToolUses)
	}
}

func TestCheckFileToolboxViolationFails(t *testing.T) {
	report, err := CheckFile("testdata/toolbox_violation.jsonl")
	if err != nil {
		t.Fatalf("CheckFile: %v", err)
	}
	if report.OK() {
		t.Fatalf("broken trace unexpectedly passed")
	}
	reasons := violationReasons(report)
	assertContains(t, reasons, "denied by contract")
	assertContains(t, reasons, "outside allowed_tools")
	assertContains(t, reasons, "exceeding declared read effect")
}

func TestCheckEventsIgnoresMachineToolCallsWithoutAgentCallID(t *testing.T) {
	report := CheckEvents([]store.Event{
		{
			Kind:    store.LLMToolCall,
			Payload: []byte(`{"tool":"freeform_response"}`),
		},
	})
	if !report.OK() {
		t.Fatalf("machine-level tool call without call_id must be ignored: %#v", report.Violations)
	}
	if report.ToolUses != 0 {
		t.Fatalf("ToolUses = %d, want 0", report.ToolUses)
	}
}

func TestCheckEventsFailsUnmatchedAgentToolUse(t *testing.T) {
	report := CheckEvents([]store.Event{
		{
			Kind:    store.AgentStreamEvent,
			CallID:  "missing",
			Payload: []byte(`{"tool":"Read"}`),
		},
	})
	if report.OK() {
		t.Fatalf("unmatched agent tool use unexpectedly passed")
	}
	assertContains(t, violationReasons(report), "no matching agent.call.start contract")
}

func violationReasons(report Report) string {
	var reasons []string
	for _, violation := range report.Violations {
		reasons = append(reasons, violation.Reason)
	}
	return strings.Join(reasons, "\n")
}

func assertContains(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("reasons did not contain %q:\n%s", needle, haystack)
	}
}
