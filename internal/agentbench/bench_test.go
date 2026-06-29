package agentbench

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScoreTracePassesBudgetsAndExpectations(t *testing.T) {
	trace := writeTrace(t,
		event("2026-06-26T01:00:00Z", "agent.stream", "rooms/decompose", map[string]any{
			"tool":    "Read",
			"preview": "docs/proposals/example.md",
			"input":   map[string]any{"file_path": "docs/proposals/example.md"},
		}),
		event("2026-06-26T01:00:02Z", "agent.stream", "rooms/decompose", map[string]any{
			"thinking": "checking constraints",
		}),
		event("2026-06-26T01:00:05Z", "agent.stream", "rooms/decompose", map[string]any{
			"tool": "mcp__validator__submit",
		}),
		event("2026-06-26T01:00:06Z", "agent.stream", "rooms/lint", map[string]any{
			"type":           "result",
			"input_tokens":   1200,
			"output_tokens":  300,
			"total_cost_usd": 0.02,
		}),
	)

	report, err := ScoreTrace(trace, Case{
		ID: "deliver-decompose",
		Budgets: Budgets{
			MaxWallSeconds:    10,
			MaxToolCalls:      3,
			MaxReadCalls:      1,
			MaxFilesRead:      1,
			MaxInputTokens:    2000,
			MaxOutputTokens:   500,
			MaxCostUSD:        0.05,
			MaxThinkingEvents: 1,
		},
		Expectations: Expectations{
			RequireSubmit:  true,
			FinalState:     "rooms/lint",
			ForbiddenTools: []string{"Agent", "Task", "AskUserQuestion"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed {
		t.Fatalf("expected pass, got failures: %v", report.Failures)
	}
	if report.Metrics.ToolCallsTotal != 2 {
		t.Fatalf("tool calls = %d", report.Metrics.ToolCallsTotal)
	}
	if got := strings.Join(report.Metrics.FilesRead, ","); got != "docs/proposals/example.md" {
		t.Fatalf("files read = %q", got)
	}
	if !report.Metrics.Submitted {
		t.Fatalf("submit not detected")
	}
}

func TestScoreTraceFailsBudgetsAndForbiddenTools(t *testing.T) {
	trace := writeTrace(t,
		event("2026-06-26T01:00:00Z", "agent.stream", "rooms/decompose", map[string]any{
			"tool": "Agent",
		}),
		event("2026-06-26T01:02:00Z", "agent.stream", "rooms/decompose", map[string]any{
			"type":           "result",
			"input_tokens":   426758,
			"output_tokens":  13059,
			"total_cost_usd": 2.464055,
		}),
	)

	report, err := ScoreTrace(trace, Case{
		ID: "glm-regression",
		Budgets: Budgets{
			MaxWallSeconds:  30,
			MaxToolCalls:    0,
			MaxInputTokens:  150000,
			MaxOutputTokens: 8000,
			MaxCostUSD:      1,
		},
		Expectations: Expectations{
			RequireSubmit:  true,
			ForbiddenTools: []string{"Agent", "Task", "AskUserQuestion"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed {
		t.Fatalf("expected failure")
	}
	assertFailureContains(t, report.Failures, "wall_seconds")
	assertFailureContains(t, report.Failures, "input_tokens")
	assertFailureContains(t, report.Failures, "output_tokens")
	assertFailureContains(t, report.Failures, "cost_usd")
	assertFailureContains(t, report.Failures, "forbidden tool \"Agent\"")
	assertFailureContains(t, report.Failures, "required submit")
}

func TestScoreTraceFailsInFlightAgentCall(t *testing.T) {
	trace := writeTrace(t,
		event("2026-06-26T01:00:00Z", "agent.call.start", "rooms/decompose", map[string]any{
			"agent": "decomposer",
			"model": "hf:zai-org/GLM-5.2",
		}),
	)

	report, err := ScoreTrace(trace, Case{ID: "glm-stall"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed {
		t.Fatalf("expected in-flight call to fail")
	}
	if report.Metrics.AgentCallsStarted != 1 || report.Metrics.AgentCallsInFlight != 1 {
		t.Fatalf("agent lifecycle metrics = %+v", report.Metrics)
	}
	assertFailureContains(t, report.Failures, "agent_calls_in_flight 1")
}

func TestScoreTraceTreatsAgentCallCompleteAsTerminal(t *testing.T) {
	trace := writeTrace(t,
		event("2026-06-26T01:00:00Z", "agent.call.start", "rooms/decompose", map[string]any{
			"agent": "decomposer",
		}),
		event("2026-06-26T01:00:01Z", "agent.stream", "rooms/decompose", map[string]any{
			"tool": "mcp__validator__submit",
		}),
		event("2026-06-26T01:00:02Z", "agent.call.complete", "rooms/decompose", map[string]any{
			"model": "hf:zai-org/GLM-5.2",
			"meta": map[string]any{
				"cost_usd": 0.25,
				"usage": map[string]any{
					"input_tokens":  1000,
					"output_tokens": 250,
				},
			},
		}),
	)

	report, err := ScoreTrace(trace, Case{
		ID:           "glm-complete",
		Expectations: Expectations{RequireSubmit: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed {
		t.Fatalf("expected pass, got failures: %v", report.Failures)
	}
	if report.Metrics.AgentCallsStarted != 1 || report.Metrics.AgentCallsFinished != 1 || report.Metrics.AgentCallsInFlight != 0 {
		t.Fatalf("agent lifecycle metrics = %+v", report.Metrics)
	}
	if report.Metrics.InputTokens != 1000 || report.Metrics.OutputTokens != 250 || report.Metrics.CostUSD != 0.25 {
		t.Fatalf("usage metrics = %+v", report.Metrics)
	}
}

func TestScoreTraceDoesNotDoubleCountToolAndToolsArray(t *testing.T) {
	trace := writeTrace(t,
		event("2026-06-26T01:00:00Z", "agent.stream", "rooms/decompose", map[string]any{
			"tool": "Read",
			"tools": []any{
				map[string]any{
					"name":    "Read",
					"preview": "docs/proposals/example.md",
				},
			},
		}),
	)

	report, err := ScoreTrace(trace, Case{ID: "tool-count"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Metrics.ToolCallsTotal != 1 || report.Metrics.ReadCalls != 1 {
		t.Fatalf("tool/read calls = %d/%d", report.Metrics.ToolCallsTotal, report.Metrics.ReadCalls)
	}
}

func TestLoadManifestAndSelectCase(t *testing.T) {
	dir := t.TempDir()
	manifest := filepath.Join(dir, "bench.yaml")
	if err := os.WriteFile(manifest, []byte(`version: agent_bench/v1
cases:
  - id: one
    trace: one.trace.jsonl
  - id: two
    trace: two.trace.jsonl
`), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := LoadManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	c, err := m.Case("two")
	if err != nil {
		t.Fatal(err)
	}
	if c.Trace != "two.trace.jsonl" {
		t.Fatalf("trace = %q", c.Trace)
	}
	if _, err := m.Case(""); err == nil {
		t.Fatalf("expected ambiguous empty case id to fail")
	}
}

func TestScoreManifestCaseTreatsTraceOverrideAsCallerPath(t *testing.T) {
	dir := t.TempDir()
	manifestDir := filepath.Join(dir, "manifest")
	if err := os.Mkdir(manifestDir, 0o755); err != nil {
		t.Fatal(err)
	}
	trace := filepath.Join(dir, "override.trace.jsonl")
	if err := os.WriteFile(trace, []byte(`{"ts":"2026-06-26T01:00:00Z","kind":"agent.stream","state_path":"done","payload":{"tool":"mcp__validator__submit"}}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(manifestDir, "bench.yaml")
	if err := os.WriteFile(manifest, []byte(`version: agent_bench/v1
cases:
  - id: one
    trace: missing-relative.trace.jsonl
    expectations:
      require_submit: true
`), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := ScoreManifestCase(manifest, "one", trace)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed {
		t.Fatalf("expected override trace to pass: %v", report.Failures)
	}
}

func TestMarkdownReportAndSlideyDeckSummarizeScore(t *testing.T) {
	report := Report{
		CaseID:   "glm-task",
		Trace:    ".artifacts/glm.trace.jsonl",
		Passed:   false,
		Failures: []string{"input_tokens 200 exceeds budget 100"},
		Metrics: Metrics{
			Events:             4,
			AgentStreamEvents:  2,
			InputTokens:        200,
			OutputTokens:       50,
			TotalTokens:        250,
			CostUSD:            0.12,
			ToolCallsTotal:     3,
			ReadCalls:          2,
			FilesRead:          []string{"docs/proposals/example.md"},
			ToolCallsByName:    map[string]int{"Read": 2, "Edit": 1},
			FinalState:         "configure",
			Submitted:          true,
			AgentCallsStarted:  1,
			AgentCallsFinished: 1,
		},
	}
	md := MarkdownReport(report)
	for _, want := range []string{"# Agent Bench: glm-task", "Status: FAIL", "input_tokens 200", "`Read`: 2", "`docs/proposals/example.md`"} {
		if !strings.Contains(md, want) {
			t.Fatalf("markdown report missing %q:\n%s", want, md)
		}
	}
	deck, err := SlideyDeckJSON(report)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(deck, &decoded); err != nil {
		t.Fatalf("invalid slidey json: %v\n%s", err, deck)
	}
	if decoded["title"] != "Agent Bench: glm-task" {
		t.Fatalf("deck title = %v", decoded["title"])
	}
}

func TestRunManifestCaseRequiresLiveGate(t *testing.T) {
	dir := t.TempDir()
	manifest := filepath.Join(dir, "bench.yaml")
	if err := os.WriteFile(manifest, []byte(`version: agent_bench/v1
cases:
  - id: one
    trace: one.trace.jsonl
    run:
      command: ["echo", "hello"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := RunManifestCase(RunOptions{ManifestPath: manifest, CaseID: "one"})
	if err == nil || !strings.Contains(err.Error(), "live-gated") {
		t.Fatalf("expected live gate error, got %v", err)
	}
}

func TestRunManifestCaseCleansTraceBeforeRun(t *testing.T) {
	dir := t.TempDir()
	trace := filepath.Join(dir, "trace.jsonl")
	if err := os.WriteFile(trace, []byte(`{"ts":"2026-06-26T01:00:00Z","kind":"agent.call.start","state_path":"old","payload":{"agent":"stale"}}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(dir, "bench.yaml")
	if err := os.WriteFile(manifest, []byte(`version: agent_bench/v1
cases:
  - id: clean
    trace: trace.jsonl
    run:
      command:
        - sh
        - -c
        - "printf '%s\n' '{\"ts\":\"2026-06-26T01:00:00Z\",\"kind\":\"agent.stream\",\"state_path\":\"done\",\"payload\":{\"tool\":\"mcp__validator__submit\"}}' > trace.jsonl"
    expectations:
      require_submit: true
`), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := RunManifestCase(RunOptions{ManifestPath: manifest, Live: true})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed {
		t.Fatalf("expected clean run to pass: %v", report.Failures)
	}
	if report.Metrics.AgentCallsInFlight != 0 {
		t.Fatalf("stale in-flight call was not cleaned: %+v", report.Metrics)
	}
}

func assertFailureContains(t *testing.T, failures []string, want string) {
	t.Helper()
	for _, f := range failures {
		if strings.Contains(f, want) {
			return
		}
	}
	t.Fatalf("failures %v did not contain %q", failures, want)
}

func writeTrace(t *testing.T, events ...map[string]any) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "trace.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, ev := range events {
		b, err := json.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(append(b, '\n')); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func event(ts, kind, state string, payload map[string]any) map[string]any {
	return map[string]any{
		"ts":         ts,
		"kind":       kind,
		"state_path": state,
		"payload":    payload,
	}
}
