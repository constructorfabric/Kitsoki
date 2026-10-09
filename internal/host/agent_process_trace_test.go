package host_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"kitsoki/internal/host"
	"kitsoki/internal/store"
)

func TestAgentProcessDiagnosticsTraceNoOutputThenFinish(t *testing.T) {
	t.Setenv("KITSOKI_AGENT_NO_OUTPUT_NOTICE_AFTER", "20ms")

	dir := t.TempDir()
	bin := filepath.Join(dir, "fake-claude")
	if err := os.WriteFile(bin, []byte(`#!/bin/sh
sleep 0.08
printf '%s\n' '{"type":"system","subtype":"init","session_id":"diag-sid"}'
printf '%s\n' '{"type":"result","subtype":"success","result":"done","session_id":"diag-sid","is_error":false}'
`), 0o755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}

	sink := &memSink{}
	ctx := host.WithCallID(agentCtxForTest(sink), "diag-call")
	cr, sid, err := host.AgentStreamerRunExport(ctx, bin, []string{"--mcp-config", filepath.Join(dir, "secret.json")}, "prompt", dir)
	if err != nil {
		t.Fatalf("AgentStreamerRunExport: %v", err)
	}
	if cr.ExitCode != 0 || cr.Infra != nil {
		t.Fatalf("unexpected run failure: exit=%d infra=%v stderr=%q", cr.ExitCode, cr.Infra, cr.Stderr)
	}
	if sid != "diag-sid" {
		t.Fatalf("session id = %q, want diag-sid", sid)
	}

	seen := map[string]map[string]any{}
	for _, ev := range sink.events {
		if ev.Kind != store.AgentStreamEvent {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(ev.Payload, &payload); err != nil {
			t.Fatalf("unmarshal stream payload: %v", err)
		}
		if payload["type"] == "agent.process" {
			subtype, _ := payload["subtype"].(string)
			seen[subtype] = payload
			if ev.CallID != "diag-call" {
				t.Fatalf("agent.process event call_id = %q, want diag-call", ev.CallID)
			}
		}
	}

	if seen["start"] == nil {
		t.Fatalf("missing agent.process start event; events=%v", kinds(sink.events))
	}
	if seen["no_output"] == nil {
		t.Fatalf("missing agent.process no_output event; seen=%v", seen)
	}
	if seen["finish"] == nil {
		t.Fatalf("missing agent.process finish event; seen=%v", seen)
	}
	if args, _ := seen["start"]["args"].([]any); len(args) < 2 || args[1] != "<redacted-path>" {
		t.Fatalf("start args did not redact mcp config path: %#v", seen["start"]["args"])
	}
	if got, _ := seen["finish"]["raw_event_count"].(float64); got != 2 {
		t.Fatalf("finish raw_event_count = %v, want 2", seen["finish"]["raw_event_count"])
	}
}

func TestAgentStreamerDirectActivityTimeoutEmitsFinish(t *testing.T) {
	t.Setenv("KITSOKI_AGENT_ACTIVITY_TIMEOUT", "30ms")
	t.Setenv("KITSOKI_AGENT_NO_OUTPUT_NOTICE_AFTER", "10ms")

	dir := t.TempDir()
	bin := filepath.Join(dir, "silent-claude")
	if err := os.WriteFile(bin, []byte(`#!/bin/sh
sleep 5
`), 0o755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}

	sink := &memSink{}
	ctx := host.WithCallID(agentCtxForTest(sink), "silent-call")
	cr, _, err := host.AgentStreamerRunExport(ctx, bin, []string{"-p"}, "prompt", dir)
	if err != nil {
		t.Fatalf("AgentStreamerRunExport returned Go error; want ClaudeRun.Infra: %v", err)
	}
	if cr.Infra == nil {
		t.Fatalf("expected inactivity cancellation in ClaudeRun.Infra, got %#v", cr)
	}

	seen := map[string]map[string]any{}
	for _, ev := range sink.events {
		if ev.Kind != store.AgentStreamEvent {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(ev.Payload, &payload); err != nil {
			t.Fatalf("unmarshal stream payload: %v", err)
		}
		if payload["type"] == "agent.process" {
			subtype, _ := payload["subtype"].(string)
			seen[subtype] = payload
			if ev.CallID != "silent-call" {
				t.Fatalf("agent.process event call_id = %q, want silent-call", ev.CallID)
			}
		}
	}
	if seen["start"] == nil || seen["no_output"] == nil || seen["finish"] == nil {
		t.Fatalf("missing process diagnostics after activity timeout; seen=%v events=%v", seen, kinds(sink.events))
	}
	if severity, _ := seen["finish"]["severity"].(string); severity != "error" {
		t.Fatalf("finish severity = %q, want error; payload=%#v", severity, seen["finish"])
	}
	if errText, _ := seen["finish"]["error"].(string); errText == "" {
		t.Fatalf("finish missing error text: %#v", seen["finish"])
	}
}
