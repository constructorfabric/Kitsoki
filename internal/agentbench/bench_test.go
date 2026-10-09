package agentbench

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// CLI CodeAct is a two-layer execution: the outer host call is not enough to
// prove that its one-or-more generator subprocesses were supervised. A scored
// campaign trace must contain a paired runtime receipt for every CLI CodeAct
// step, all under the outer call ID.
func TestScoreTraceRequiresPairedCLICodeactRuntimeReceipts(t *testing.T) {
	complete := filepath.Join(t.TempDir(), "complete.jsonl")
	if err := os.WriteFile(complete, []byte(strings.Join([]string{
		`{"ts":"2026-07-12T01:00:00Z","kind":"agent.call.start","state_path":"implement","call_id":"codeact-1","payload":{"verb":"codeact","runtime_kind":"cli"}}`,
		`{"ts":"2026-07-12T01:00:01Z","kind":"agent.runtime.start","state_path":"implement","call_id":"codeact-1","payload":{"strength":"supervised"}}`,
		`{"ts":"2026-07-12T01:00:02Z","kind":"agent.runtime.end","state_path":"implement","call_id":"codeact-1","payload":{"exit_code":0}}`,
		`{"ts":"2026-07-12T01:00:03Z","kind":"agent.call.complete","state_path":"implement","call_id":"codeact-1","payload":{}}`,
	}, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := ScoreTrace(complete, Case{ID: "codeact-runtime-complete"})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed || report.Metrics.RuntimeAccountingStatus != "complete" || report.Metrics.RuntimeStarts != 1 || report.Metrics.RuntimeEnds != 1 {
		t.Fatalf("complete runtime receipts = %+v failures=%v", report.Metrics, report.Failures)
	}

	missingEnd := filepath.Join(t.TempDir(), "missing-end.jsonl")
	if err := os.WriteFile(missingEnd, []byte(strings.Join([]string{
		`{"ts":"2026-07-12T01:00:00Z","kind":"agent.call.start","state_path":"implement","call_id":"codeact-2","payload":{"verb":"codeact","runtime_kind":"cli"}}`,
		`{"ts":"2026-07-12T01:00:01Z","kind":"agent.runtime.start","state_path":"implement","call_id":"codeact-2","payload":{"strength":"supervised"}}`,
		`{"ts":"2026-07-12T01:00:02Z","kind":"agent.call.complete","state_path":"implement","call_id":"codeact-2","payload":{}}`,
	}, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err = ScoreTrace(missingEnd, Case{ID: "codeact-runtime-missing-end"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed || report.Metrics.RuntimeAccountingStatus != "partial" || report.Metrics.AccountingStatus != "partial" {
		t.Fatalf("missing runtime end was accepted: %+v failures=%v", report.Metrics, report.Failures)
	}
	assertFailureContains(t, report.Failures, "runtime receipt lifecycle")
}

func TestScoreTraceRecordsDirectAPICodeactWithoutSubprocessReceipt(t *testing.T) {
	trace := writeTrace(t,
		callStartEvent("2026-07-12T01:00:00Z", "implement", "codeact-api", map[string]any{"verb": "codeact", "runtime_kind": "direct_api"}),
		completeEventWithCallID("2026-07-12T01:00:01Z", "implement", "codeact-api", map[string]any{}),
	)
	report, err := ScoreTrace(trace, Case{ID: "codeact-direct-api"})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed || report.Metrics.RuntimeAccountingStatus != "direct_api" {
		t.Fatalf("direct API runtime accounting = %+v failures=%v", report.Metrics, report.Failures)
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

// ── Regression tests for the false-pass class this study hit ──────────────
//
// Fixtures below reconstruct (in shape, not verbatim bytes) the exact false
// pass documented in
// .context/2026-07-11-gx10-small-model-study-adversarial-review.md: the V3
// gpt-oss-120b trace at
// .capsules/workspaces/gx10-codeact/.artifacts/model-task-engineering/gx10-v3/traces/gpt-oss-120b/trace.jsonl
// shows a decomposer (codeact) call, a reviewer (decide) call that returns
// "revise" and submits its verdict, a failed codeact refinement, and a
// terminal machine.state_entered("__exit__needs-human"). The old bench.go
// printed PASS because (a) require_submit accepted ANY submit including the
// reviewer's, and (b) final_state was derived from the last event carrying
// ANY non-empty top-level state_path — which for the terminal
// machine.transition event is the compound parent "configure", not the leaf
// "__exit__needs-human" the run actually landed in. These are no-LLM,
// offline scorer tests: no provider call, just JSONL trace fixtures — the
// deterministic replacement for a live-LLM regression case.

func TestReviewerSubmitDoesNotSatisfyMakerCompletionGate(t *testing.T) {
	trace := writeTrace(t,
		// The decomposer (codeact) call dispatches but never itself submits —
		// it fails mid-refinement (see the terminal-state test below for the
		// exit). Only the REVIEWER's decide call submits its revise verdict.
		event("2026-07-11T06:45:00Z", "agent.call.start", "decompose", map[string]any{"verb": "codeact", "agent": "decomposer"}),
		callStartEvent("2026-07-11T06:45:10Z", "review", "reviewer-call-1", map[string]any{"verb": "decide", "agent": "reviewer"}),
		streamEventWithCallID("2026-07-11T06:45:12Z", "review", "reviewer-call-1", map[string]any{
			"tool": "mcp__validator__submit",
		}),
	)

	report, err := ScoreTrace(trace, Case{
		ID:           "deliver-decompose-gpt-oss-120b",
		Expectations: Expectations{RequireSubmit: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed {
		t.Fatalf("expected a reviewer-only submit to fail the maker completion gate, got pass: %+v", report.Metrics)
	}
	if report.Metrics.Submitted != true {
		t.Fatalf("expected Submitted=true (the reviewer DID submit something)")
	}
	if report.Metrics.MakerSubmitted {
		t.Fatalf("expected MakerSubmitted=false — only the reviewer (decide verb) submitted")
	}
	assertFailureContains(t, report.Failures, "not observed on a maker/decomposer call")
}

func TestTerminalNeedsHumanCannotScoreAsPassed(t *testing.T) {
	trace := writeTrace(t,
		event("2026-07-11T06:45:00Z", "agent.call.start", "decompose", map[string]any{"verb": "codeact", "agent": "decomposer"}),
		streamEventWithCallID("2026-07-11T06:45:01Z", "decompose", "maker-call-1", map[string]any{
			"tool": "mcp__validator__submit",
		}),
		// The trace's top-level state_path on the transition event is the
		// compound parent "configure" — exactly like the real trace — but the
		// authoritative leaf state (from machine.state_entered) is the
		// reserved escalation exit.
		map[string]any{"ts": "2026-07-11T06:45:45Z", "kind": "machine.transition", "state_path": "configure", "payload": map[string]any{"from": "decompose_error", "to": "__exit__needs-human"}},
		map[string]any{"ts": "2026-07-11T06:45:46Z", "kind": "machine.state_entered", "state_path": "__exit__needs-human", "payload": map[string]any{"state": "__exit__needs-human"}},
	)

	report, err := ScoreTrace(trace, Case{
		ID:           "deliver-decompose-gpt-oss-120b",
		Expectations: Expectations{RequireSubmit: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Metrics.FinalState != "__exit__needs-human" {
		t.Fatalf("final_state = %q, want the authoritative leaf state __exit__needs-human (not the compound parent %q the old last-non-empty-state_path logic would have reported)", report.Metrics.FinalState, "configure")
	}
	if report.Passed {
		t.Fatalf("expected a run terminating in __exit__needs-human to fail even though a maker submit was observed, got pass")
	}
	assertFailureContains(t, report.Failures, "reserved escalation exit")
}

// TestUsageSumsAcrossDistinctCallsNotMax pins the fix for bench.go's own
// "max instead of sum" usage bug (distinct from the agent_event_sink.go
// live-box fix): a trace with THREE distinct host.agent.* calls (three
// call_ids, e.g. a decomposer call plus two reviewer resumes) must report
// the SUM of their usage, not the single largest call's usage. Before this
// fix, ScoreTrace's accumulateUsage ran metrics.InputTokens =
// max(metrics.InputTokens, thisCall'sInputTokens) across the WHOLE trace, so
// a trace with a $0.01 call and a $0.02 call reported $0.02 total, not
// $0.03 — silently discarding the smaller call's spend entirely. See
// "Agent-bench takes the maximum observed tokens/cost instead of summing
// unique provider requests" in
// .context/2026-07-11-gx10-small-model-study-adversarial-review.md.
func TestUsageSumsAcrossDistinctCallsNotMax(t *testing.T) {
	trace := writeTrace(t,
		callStartEvent("2026-07-11T06:45:00Z", "decompose", "call-1", map[string]any{"verb": "codeact"}),
		completeEventWithCallID("2026-07-11T06:45:05Z", "decompose", "call-1", map[string]any{
			"meta": map[string]any{"cost_usd": 0.01, "usage": map[string]any{"input_tokens": 1000, "output_tokens": 100}},
		}),
		callStartEvent("2026-07-11T06:45:10Z", "review", "call-2", map[string]any{"verb": "decide"}),
		completeEventWithCallID("2026-07-11T06:45:15Z", "review", "call-2", map[string]any{
			"meta": map[string]any{"cost_usd": 0.02, "usage": map[string]any{"input_tokens": 2000, "output_tokens": 200}},
		}),
		callStartEvent("2026-07-11T06:45:20Z", "review", "call-3", map[string]any{"verb": "decide"}),
		completeEventWithCallID("2026-07-11T06:45:25Z", "review", "call-3", map[string]any{
			"meta": map[string]any{"cost_usd": 0.005, "usage": map[string]any{"input_tokens": 500, "output_tokens": 50}},
		}),
	)

	report, err := ScoreTrace(trace, Case{ID: "usage-sum"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Metrics.InputTokens != 3500 {
		t.Fatalf("input_tokens = %d, want 3500 (1000+2000+500, not max()=2000)", report.Metrics.InputTokens)
	}
	if report.Metrics.OutputTokens != 350 {
		t.Fatalf("output_tokens = %d, want 350 (100+200+50, not max()=200)", report.Metrics.OutputTokens)
	}
	if got, want := report.Metrics.CostUSD, 0.035; got < want-0.0001 || got > want+0.0001 {
		t.Fatalf("cost_usd = %v, want ~0.035 (0.01+0.02+0.005, not max()=0.02)", got)
	}
}

// completeEventWithCallID builds an agent.call.complete trace event carrying
// a call_id, pairing with callStartEvent for multi-call usage-summing tests.
func completeEventWithCallID(ts, state, callID string, payload map[string]any) map[string]any {
	ev := event(ts, "agent.call.complete", state, payload)
	ev["call_id"] = callID
	return ev
}

func TestStaleArtifactFromPriorAttemptIsRejected(t *testing.T) {
	dir := t.TempDir()
	artifactPath := filepath.Join(dir, "decomposition.yaml")
	if err := os.WriteFile(artifactPath, []byte("stale: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Back-date the artifact so it predates the trace's first event — a
	// leftover from a prior attempt that a cleaned .artifacts/deliver run
	// would not have produced.
	stale := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(artifactPath, stale, stale); err != nil {
		t.Fatal(err)
	}

	tracePath := filepath.Join(dir, "trace.jsonl")
	traceContent := `{"ts":"2026-07-11T06:45:00Z","kind":"agent.stream","state_path":"decompose","payload":{"tool":"mcp__validator__submit"}}
`
	if err := os.WriteFile(tracePath, []byte(traceContent), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := ScoreTrace(tracePath, Case{
		ID: "deliver-decompose-stale-artifact",
		Expectations: Expectations{
			RequireSubmit:   true,
			RequireArtifact: "decomposition.yaml",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed {
		t.Fatalf("expected a stale pre-existing artifact to be rejected, got pass")
	}
	assertFailureContains(t, report.Failures, "predates this run")
}

// callStartEvent builds an agent.call.start trace event carrying a call_id,
// for tests that need to attribute a subsequent submit signal to a specific
// verb via callVerb correlation.
func callStartEvent(ts, state, callID string, payload map[string]any) map[string]any {
	ev := event(ts, "agent.call.start", state, payload)
	ev["call_id"] = callID
	return ev
}

// streamEventWithCallID builds an agent.stream event carrying a call_id, for
// tests that correlate a submit tool call back to its dispatching verb.
func streamEventWithCallID(ts, state, callID string, payload map[string]any) map[string]any {
	ev := event(ts, "agent.stream", state, payload)
	ev["call_id"] = callID
	return ev
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
