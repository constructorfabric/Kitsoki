package workerserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kitsoki/internal/capsule/executor"
)

func TestProjectAgentTraceExplainsLiveNoOutputWithoutLeakingContent(t *testing.T) {
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "story-trace.jsonl")
	secret := "super-secret-provider-token"
	abs := "/Users/operator/private/project"
	lines := []string{
		`{"kind":"session.header","schema_version":1,"written_at":"2026-07-11T11:00:00Z"}`,
		traceLine(now.Add(-5*time.Minute), "harness.dispatched", "", map[string]any{"namespace": "host.agent.task", "args": map[string]any{"prompt": secret, "working_dir": abs}}),
		traceLine(now.Add(-4*time.Minute), "agent.call.start", "call-"+secret+abs, map[string]any{"verb": "task", "backend": "agent.claude", "model": secret, "profile": abs, "prompt": secret, "prompt_file": abs + "/prompt.md"}),
		traceLine(now.Add(-3*time.Minute), "agent.stream", "call-"+secret+abs, map[string]any{"type": "agent.process", "subtype": "start", "severity": "info", "backend": "claude", "bin": abs + "/claude", "working_dir": abs, "args": []string{"--token", secret}, "text": secret}),
		traceLine(now.Add(-2*time.Minute), "agent.stream", "call-"+secret+abs, map[string]any{"type": "assistant", "thinking": secret, "text": secret, "preview": abs}),
		traceLine(now.Add(-time.Minute), "agent.stream", "call-"+secret+abs, map[string]any{"type": "agent.process", "subtype": "no_output", "severity": "warn", "duration_ms": 180000, "error": secret, "working_dir": abs}),
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	projection := ProjectAgentTrace(path, now)
	if err := executor.ValidateAgentDiagnostics(projection); err != nil {
		t.Fatal(err)
	}
	if projection.TraceState != "ok" || projection.StallHint != "process_no_output" || len(projection.ActiveCalls) != 1 || projection.PendingHostCalls != 1 {
		t.Fatalf("projection = %#v", projection)
	}
	if projection.ActiveCalls[0].Backend != "claude" || projection.ActiveCalls[0].Phase != "process_no_output" || projection.ActiveCalls[0].CallRef == "" || projection.ActiveCalls[0].ModelRef == "" || projection.ActiveCalls[0].ProfileRef == "" {
		t.Fatalf("active call = %#v", projection.ActiveCalls[0])
	}
	raw, err := json.Marshal(projection)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{secret, abs, "prompt", "response", "working_dir", "--token", "private/project"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("projection leaked %q: %s", forbidden, raw)
		}
	}
}

func TestProjectAgentTraceTerminalCallDropsActiveStateAndIgnoresPartialTail(t *testing.T) {
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "story-trace.jsonl")
	callID := "terminal-call"
	lines := []string{
		`{"kind":"session.header","schema_version":1,"written_at":"2026-07-11T11:00:00Z"}`,
		traceLine(now.Add(-3*time.Minute), "agent.call.start", callID, map[string]any{"verb": "decide", "prompt": "do not project me"}),
		traceLine(now.Add(-2*time.Minute), "agent.stream", callID, map[string]any{"type": "agent.process", "subtype": "finish", "severity": "info", "duration_ms": 1000, "exit_code": 0, "raw_event_count": 2, "error": "/absolute/private/error"}),
		traceLine(now.Add(-time.Minute), "agent.call.complete", callID, map[string]any{"verb": "decide", "duration_ms": 2000, "response": "do not project me"}),
		`{"turn":1,"kind":"agent.stream"`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	projection := ProjectAgentTrace(path, now)
	if projection.TraceState != "partial" || projection.InvalidLines != 1 || len(projection.ActiveCalls) != 0 || projection.StallHint != "" {
		t.Fatalf("projection = %#v", projection)
	}
	if len(projection.Breadcrumbs) < 3 || projection.Breadcrumbs[len(projection.Breadcrumbs)-1].Kind != "call_completed" {
		t.Fatalf("breadcrumbs = %#v", projection.Breadcrumbs)
	}
}

func TestExecutionStatusProjectsLiveAgentAndLatestCleanupDiagnostics(t *testing.T) {
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	worker := cleanupTestServer(t, now)
	source := strings.Repeat("d", 40)
	writeCleanupRun(t, worker, "live-status", source, "running", time.Time{})
	tracePath := filepath.Join(worker.runDir("live-status"), "story-trace.jsonl")
	lines := []string{
		`{"kind":"session.header","schema_version":1,"written_at":"2026-07-11T11:00:00Z"}`,
		traceLine(now.Add(-time.Minute), "agent.call.start", "live-call", map[string]any{"verb": "task", "backend": "claude", "prompt": "private"}),
		traceLine(now.Add(-30*time.Second), "agent.stream", "live-call", map[string]any{"type": "agent.process", "subtype": "no_output", "severity": "warn", "working_dir": "/private"}),
	}
	if err := os.WriteFile(tracePath, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := worker.Cleanup(t.Context(), DefaultCleanupPolicy()); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(worker.Handler())
	defer server.Close()
	response, err := http.Get(server.URL + "/v1/capsules/executions/live-status")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var body struct {
		Run RunRecord `json:"run"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || body.Run.Agent == nil || body.Run.Agent.StallHint != "process_no_output" {
		t.Fatalf("status = %d run=%#v", response.StatusCode, body.Run)
	}
	if body.Run.Cleanup == nil || body.Run.Cleanup.Schema != executor.WorkerCleanupDiagnosticsSchema || body.Run.Cleanup.Outcome != "planned" {
		t.Fatalf("cleanup status = %#v", body.Run.Cleanup)
	}
	raw, _ := json.Marshal(body)
	if strings.Contains(string(raw), "/private") || strings.Contains(string(raw), `"prompt"`) {
		t.Fatalf("status projection leaked private trace content: %s", raw)
	}
}

func traceLine(at time.Time, kind, callID string, payload any) string {
	raw, _ := json.Marshal(map[string]any{"turn": 1, "seq": 1, "ts": at.UTC(), "kind": kind, "call_id": callID, "payload": payload})
	return string(raw)
}
