package host_test

// Deterministic, no-LLM coverage for the pre-dispatch budget gate
// (dispatch-context-floor task 1.4). Each test drives the real
// AgentDecideHandler entrypoint (the shared runAgentVerbWithLadder wrap
// point) with a WithClaudeRunner stub and a synthetic oversized `context`
// arg — a stateless probe in the same idiom as ladder_agent_verbs_test.go —
// and asserts the gate's proceed/escalate/refuse decision from the
// handler's observable behavior (whether the runner was ever invoked, and
// at what rung) plus the recorded agent.dispatch.budget_checked trace event.

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"kitsoki/internal/host"
	"kitsoki/internal/store"
)

// budgetCheckedEvents filters a captureSink's history down to the decoded
// agent.dispatch.budget_checked payloads, in append order.
func budgetCheckedEvents(t *testing.T, sink *captureSink) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, ev := range sink.events {
		if ev.Kind != store.AgentDispatchBudgetChecked {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(ev.Payload, &m); err != nil {
			t.Fatalf("decode budget_checked payload: %v", err)
		}
		out = append(out, m)
	}
	return out
}

// TestBudgetGate_Proceed_SmallCallDispatchesNormally proves a normal-sized
// decide call sails through the gate untouched: the runner is invoked
// exactly once, and the recorded decision is "proceed".
func TestBudgetGate_Proceed_SmallCallDispatchesNormally(t *testing.T) {
	t.Parallel()
	schemaPath := makeSchemaFile(t)

	calls := 0
	runner := func(_ context.Context, args []string, _, _ string) (host.ClaudeRun, error) {
		calls++
		if outputPath := host.ParseMCPConfigSubmitOutput(args); outputPath != "" {
			if err := os.WriteFile(outputPath, []byte(`{"verdict":"ok"}`), 0o600); err != nil {
				t.Fatalf("write captured verdict: %v", err)
			}
		}
		return host.ClaudeRun{Stdout: "decided"}, nil
	}

	sink := &captureSink{}
	ctx := host.WithClaudeRunner(context.Background(), runner)
	ctx = host.WithAgentEventSink(ctx, sink)

	res, err := host.AgentDecideHandler(ctx, map[string]any{
		"prompt": "decide something small",
		"schema": schemaPath,
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected handler error: %q", res.Error)
	}
	if calls != 1 {
		t.Fatalf("expected exactly 1 dispatch for a small call, got %d", calls)
	}

	events := budgetCheckedEvents(t, sink)
	if len(events) != 1 {
		t.Fatalf("expected exactly 1 budget_checked event, got %d: %+v", len(events), events)
	}
	if got := events[0]["decision"]; got != "proceed" {
		t.Fatalf("expected decision=proceed, got %v (event=%+v)", got, events[0])
	}
}

// TestBudgetGate_Refuse_OversizedCallNeverDispatches proves a call whose
// estimated size exceeds an agent's declared refuse_tokens threshold never
// reaches the claude subprocess at all — the runner is never invoked, the
// handler returns a Result.Error, and the decision is recorded as "refuse".
func TestBudgetGate_Refuse_OversizedCallNeverDispatches(t *testing.T) {
	t.Parallel()
	schemaPath := makeSchemaFile(t)

	calls := 0
	runner := func(_ context.Context, _ []string, _, _ string) (host.ClaudeRun, error) {
		calls++
		return host.ClaudeRun{Stdout: "should never run"}, nil
	}

	sink := &captureSink{}
	ctx := host.WithAgents(context.Background(), map[string]host.Agent{
		"tight-budget": {
			SystemPrompt: "judge things",
			TokenBudget:  &host.BudgetThresholds{WarnTokens: 50, RefuseTokens: 100},
		},
	})
	ctx = host.WithClaudeRunner(ctx, runner)
	ctx = host.WithAgentEventSink(ctx, sink)

	// ~2000 chars of synthetic oversized prior-artifact context ⇒ well past
	// a 100-token (400-char) refuse threshold for this narrowly-budgeted agent.
	oversizedContext := strings.Repeat("token-bloat-probe ", 200)

	res, err := host.AgentDecideHandler(ctx, map[string]any{
		"prompt": "decide something",
		"schema": schemaPath,
		"agent":  "tight-budget",
		"args": map[string]any{
			"synthetic_oversized_context": oversizedContext,
		},
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if calls != 0 {
		t.Fatalf("expected the refuse decision to prevent any dispatch, got %d calls", calls)
	}
	if res.Error == "" {
		t.Fatal("expected a Result.Error from the refused dispatch")
	}
	if !strings.Contains(res.Error, "budget gate refused") {
		t.Fatalf("expected the error to name the budget gate, got %q", res.Error)
	}

	events := budgetCheckedEvents(t, sink)
	if len(events) != 1 {
		t.Fatalf("expected exactly 1 budget_checked event, got %d: %+v", len(events), events)
	}
	if got := events[0]["decision"]; got != "refuse" {
		t.Fatalf("expected decision=refuse, got %v (event=%+v)", got, events[0])
	}
}

// TestBudgetGate_Escalate_SkipsCheapestEffortTier proves a call flagged
// escalate (over warn_tokens, at/under refuse_tokens) with a configured
// harness ladder skips the ladder's cheapest effort tier: the first (and
// only, since the stub always succeeds) dispatch lands at "medium" effort,
// never "low".
func TestBudgetGate_Escalate_SkipsCheapestEffortTier(t *testing.T) {
	t.Parallel()
	schemaPath := makeSchemaFile(t)

	var effortsSeen []string
	runner := func(_ context.Context, args []string, _, _ string) (host.ClaudeRun, error) {
		effortsSeen = append(effortsSeen, effortFlag(args))
		if outputPath := host.ParseMCPConfigSubmitOutput(args); outputPath != "" {
			if err := os.WriteFile(outputPath, []byte(`{"verdict":"ok"}`), 0o600); err != nil {
				t.Fatalf("write captured verdict: %v", err)
			}
		}
		return host.ClaudeRun{Stdout: "decided"}, nil
	}

	cfg := host.LadderConfig{
		Models:  []host.LadderModel{{Backend: "claude", Model: "solo-model"}},
		Efforts: []string{"low", "medium", "high"},
	}

	sink := &captureSink{}
	ctx := host.WithAgents(context.Background(), map[string]host.Agent{
		"mid-budget": {
			SystemPrompt: "judge things",
			TokenBudget:  &host.BudgetThresholds{WarnTokens: 50, RefuseTokens: 100_000},
		},
	})
	ctx = host.WithHarnessLadder(ctx, cfg)
	ctx = host.WithClaudeRunner(ctx, runner)
	ctx = host.WithAgentEventSink(ctx, sink)

	oversizedContext := strings.Repeat("token-bloat-probe ", 200)

	res, err := host.AgentDecideHandler(ctx, map[string]any{
		"prompt": "decide something",
		"schema": schemaPath,
		"agent":  "mid-budget",
		"args": map[string]any{
			"synthetic_oversized_context": oversizedContext,
		},
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected handler error: %q", res.Error)
	}
	if len(effortsSeen) != 1 || effortsSeen[0] != "medium" {
		t.Fatalf("expected the escalated ladder to start at medium (skipping low), got %v", effortsSeen)
	}

	events := budgetCheckedEvents(t, sink)
	if len(events) != 1 {
		t.Fatalf("expected exactly 1 budget_checked event, got %d: %+v", len(events), events)
	}
	if got := events[0]["decision"]; got != "escalate" {
		t.Fatalf("expected decision=escalate, got %v (event=%+v)", got, events[0])
	}
	rung, ok := events[0]["rung"].(map[string]any)
	if !ok {
		t.Fatalf("expected an escalate decision to carry a rung preview, event=%+v", events[0])
	}
	if rung["effort"] != "medium" {
		t.Fatalf("expected the previewed rung to name medium effort, got %+v", rung)
	}
}

// TestBudgetGate_FailClosed_InvalidAgentOverrideAlwaysRefuses proves an
// invalid per-agent TokenBudget override (refuse_tokens < warn_tokens) makes
// the gate refuse EVERY dispatch through that agent — including a tiny
// prompt that would otherwise sail through unnoticed. Missing/invalid budget
// config fails closed rather than silently disabling the gate.
func TestBudgetGate_FailClosed_InvalidAgentOverrideAlwaysRefuses(t *testing.T) {
	t.Parallel()
	schemaPath := makeSchemaFile(t)

	calls := 0
	runner := func(_ context.Context, _ []string, _, _ string) (host.ClaudeRun, error) {
		calls++
		return host.ClaudeRun{Stdout: "should never run"}, nil
	}

	sink := &captureSink{}
	ctx := host.WithAgents(context.Background(), map[string]host.Agent{
		"broken-budget": {
			SystemPrompt: "judge things",
			// Invalid: refuse_tokens < warn_tokens.
			TokenBudget: &host.BudgetThresholds{WarnTokens: 1000, RefuseTokens: 10},
		},
	})
	ctx = host.WithClaudeRunner(ctx, runner)
	ctx = host.WithAgentEventSink(ctx, sink)

	res, err := host.AgentDecideHandler(ctx, map[string]any{
		"prompt": "hi",
		"schema": schemaPath,
		"agent":  "broken-budget",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if calls != 0 {
		t.Fatalf("expected the invalid config to refuse closed even for a tiny prompt, got %d calls", calls)
	}
	if res.Error == "" || !strings.Contains(res.Error, "budget gate refused") {
		t.Fatalf("expected a budget-gate refusal error, got %q", res.Error)
	}

	events := budgetCheckedEvents(t, sink)
	if len(events) != 1 || events[0]["decision"] != "refuse" {
		t.Fatalf("expected exactly 1 refuse decision, got %+v", events)
	}
	if reason, _ := events[0]["reason"].(string); !strings.Contains(reason, "warn_tokens") {
		t.Fatalf("expected the refusal reason to name the invalid config, got %q", reason)
	}
}

// TestDefaultVerbBudgets_AreValid locks the invariant the budget_gate.go
// package doc claims: every shipped per-verb default (and the generic
// fallback) is itself valid() — the fail-closed path is reserved for
// author-declared overrides, never the built-in defaults.
func TestDefaultVerbBudgets_AreValid(t *testing.T) {
	t.Parallel()
	for verb, b := range host.DefaultVerbBudgetsExport() {
		if !host.BudgetThresholdsValidExport(b) {
			t.Fatalf("default budget for verb %q is not valid(): %+v", verb, b)
		}
	}
}

// effortFlag extracts the --effort value from a captured claude argv, or ""
// when absent.
func effortFlag(args []string) string {
	for i, a := range args {
		if a == "--effort" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}
