package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentBenchScoreCommand(t *testing.T) {
	dir := t.TempDir()
	jsonOut := filepath.Join(dir, "nested", "reports", "report.json")
	markdownOut := filepath.Join(dir, "nested", "reports", "report.md")
	slideyOut := filepath.Join(dir, "nested", "decks", "deck.slidey.json")
	trace := filepath.Join(dir, "trace.jsonl")
	if err := os.WriteFile(trace, []byte(`{"ts":"2026-06-26T01:00:00Z","kind":"agent.stream","state_path":"rooms/decompose","payload":{"tool":"mcp__validator__submit"}}
{"ts":"2026-06-26T01:00:01Z","kind":"agent.stream","state_path":"rooms/lint","payload":{"type":"result","input_tokens":100,"output_tokens":20,"total_cost_usd":0.001}}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(dir, "bench.yaml")
	if err := os.WriteFile(manifest, []byte(`version: agent_bench/v1
cases:
  - id: smoke
    trace: trace.jsonl
    budgets:
      max_input_tokens: 200
      max_output_tokens: 50
      max_cost_usd: 0.01
    expectations:
      require_submit: true
      final_state: rooms/lint
`), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := execRoot(t, "agent-bench", "score", manifest, "--json-out", jsonOut, "--markdown-out", markdownOut, "--slidey-out", slideyOut)
	if err != nil {
		t.Fatalf("agent-bench score: %v\n%s", err, out)
	}
	if !strings.Contains(out, "PASS smoke") {
		t.Fatalf("unexpected output:\n%s", out)
	}
	for _, path := range []string{jsonOut, markdownOut, slideyOut} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected artifact %s: %v", path, err)
		}
	}
	md, err := os.ReadFile(markdownOut)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(md), "# Agent Bench: smoke") {
		t.Fatalf("unexpected markdown:\n%s", md)
	}
}

func TestAgentBenchScoreCommandEnvelope(t *testing.T) {
	dir := t.TempDir()
	jsonOut := filepath.Join(dir, "report.json")
	markdownOut := filepath.Join(dir, "report.md")
	slideyOut := filepath.Join(dir, "deck.slidey.json")
	trace := filepath.Join(dir, "trace.jsonl")
	if err := os.WriteFile(trace, []byte(`{"ts":"2026-06-26T01:00:00Z","kind":"agent.stream","state_path":"rooms/decompose","payload":{"tool":"mcp__validator__submit"}}
{"ts":"2026-06-26T01:00:01Z","kind":"agent.stream","state_path":"rooms/lint","payload":{"type":"result","input_tokens":100,"output_tokens":20,"total_cost_usd":0.001}}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(dir, "bench.yaml")
	if err := os.WriteFile(manifest, []byte(`version: agent_bench/v1
cases:
  - id: smoke
    trace: trace.jsonl
    expectations:
      require_submit: true
      final_state: rooms/lint
`), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := execRoot(t, "agent-bench", "score", manifest, "--json-out", jsonOut, "--markdown-out", markdownOut, "--slidey-out", slideyOut, "--envelope")
	if err != nil {
		t.Fatalf("agent-bench score --envelope: %v\n%s", err, out)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var env map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &env); err != nil {
		t.Fatalf("last line is not JSON envelope: %v\n%s", err, out)
	}
	if got := env["status"]; got != "passed" {
		t.Fatalf("status = %#v, want passed", got)
	}
	if got := env["summary"]; got != "PASS smoke" {
		t.Fatalf("summary = %#v, want PASS smoke", got)
	}
	if got := env["report_json"]; got != jsonOut {
		t.Fatalf("report_json = %#v, want %q", got, jsonOut)
	}
	if got := env["stdout"]; !strings.Contains(got.(string), "PASS smoke") {
		t.Fatalf("stdout missing score text: %#v", got)
	}
}

func TestAgentBenchRunCommandRequiresLiveGate(t *testing.T) {
	dir := t.TempDir()
	manifest := filepath.Join(dir, "bench.yaml")
	if err := os.WriteFile(manifest, []byte(`version: agent_bench/v1
cases:
  - id: live
    trace: trace.jsonl
    run:
      command: ["echo", "hi"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := execRoot(t, "agent-bench", "run", manifest)
	if err == nil {
		t.Fatalf("expected live gate error, got output:\n%s", out)
	}
	if !strings.Contains(err.Error(), "live-gated") {
		t.Fatalf("expected live-gated error, got %v", err)
	}
}
