package host_test

// End-to-end (still no-LLM) tests proving host.agent.decide and
// host.agent.task actually drive the harness ladder via their shared
// runAgentVerbWithLadder wrap point — not just that RunLadder's loop logic is
// correct in isolation (see ladder_test.go). Each test installs a
// WithClaudeRunner stub that inspects the --model flag the handler built (via
// applyLadderRung → buildBaseCLIArgs) to script per-rung behavior, exactly as
// the brief asks for: "install a WithClaudeRunner stub returning scripted
// outcomes per rung".

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"kitsoki/internal/host"
)

// modelFlag extracts the --model value from a captured claude argv, or ""
// when absent.
func modelFlag(args []string) string {
	for i, a := range args {
		if a == "--model" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// TestAgentDecide_Ladder_InfraFallsOverToNextProvider drives
// AgentDecideHandler with a 2-provider ladder where the first provider's
// backend is "down" (an infra ClaudeRun.Infra failure). No harness_ladder:
// story wiring is involved — installing the LadderConfig on ctx (as an
// operator config would) is entirely sufficient for the SAME handler to fall
// over automatically.
func TestAgentDecide_Ladder_InfraFallsOverToNextProvider(t *testing.T) {
	t.Parallel()
	schemaPath := makeSchemaFile(t)

	cfg := host.LadderConfig{
		Models: []host.LadderModel{
			{Backend: "claude", Provider: "flaky-provider", Model: "flaky-model"},
			{Backend: "claude", Provider: "good-provider", Model: "good-model"},
		},
		Efforts:   []string{""},
		StatePath: filepath.Join(t.TempDir(), "ladder-state.json"),
	}

	var modelsSeen []string
	runner := func(_ context.Context, args []string, _, _ string) (host.ClaudeRun, error) {
		model := modelFlag(args)
		modelsSeen = append(modelsSeen, model)
		if model == "flaky-model" {
			return host.ClaudeRun{Infra: errors.New("503 service unavailable")}, nil
		}
		if outputPath := host.ParseMCPConfigSubmitOutput(args); outputPath != "" {
			_ = os.WriteFile(outputPath, []byte(`{"verdict":"ok"}`), 0o600)
		}
		return host.ClaudeRun{Stdout: "decided"}, nil
	}

	ctx := host.WithHarnessLadder(context.Background(), cfg)
	ctx = host.WithClaudeRunner(ctx, runner)

	res, err := host.AgentDecideHandler(ctx, map[string]any{
		"prompt": "decide something",
		"schema": schemaPath,
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("expected the ladder to fall over to good-model and succeed, got Result.Error=%q", res.Error)
	}
	if len(modelsSeen) != 2 || modelsSeen[0] != "flaky-model" || modelsSeen[1] != "good-model" {
		t.Fatalf("expected exactly one dispatch to flaky-model then good-model, got %v", modelsSeen)
	}
	submitted, ok := res.Data["submitted"].(map[string]any)
	if !ok || submitted["verdict"] != "ok" {
		t.Fatalf("expected good-model's verdict surfaced, got %+v", res.Data["submitted"])
	}
}

// TestAgentDecide_Ladder_NoLadderInstalled_UnchangedBehavior verifies the
// backward-compatibility invariant the whole design leans on: with no
// LadderConfig installed on ctx, AgentDecideHandler behaves exactly as before
// — a single dispatch, no retries across models. This is what keeps every
// pre-existing host-package test green without any ladder awareness.
func TestAgentDecide_Ladder_NoLadderInstalled_UnchangedBehavior(t *testing.T) {
	t.Parallel()
	schemaPath := makeSchemaFile(t)

	calls := 0
	runner := func(_ context.Context, args []string, _, _ string) (host.ClaudeRun, error) {
		calls++
		return host.ClaudeRun{Infra: errors.New("503 service unavailable")}, nil
	}
	ctx := host.WithClaudeRunner(context.Background(), runner) // no WithHarnessLadder

	res, err := host.AgentDecideHandler(ctx, map[string]any{
		"prompt": "decide something",
		"schema": schemaPath,
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected the infra failure to surface as a Result.Error with no ladder to fall over to")
	}
	if calls != 1 {
		t.Fatalf("expected exactly 1 dispatch (no ladder ⇒ no rungs to try), got %d", calls)
	}
}

// TestAgentTask_Ladder_CapabilityEscalatesToStrongerModel drives
// AgentTaskHandler with a 2-model ladder where the cheap model never manages
// to produce an accepted submission (a capability failure — the acceptance
// loop exhausts its retry budget) and the stronger model succeeds
// immediately. Proves host.agent.task escalates through the SAME wrap point
// as decide.
func TestAgentTask_Ladder_CapabilityEscalatesToStrongerModel(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "ok.schema.json")
	if err := os.WriteFile(schemaPath, []byte(`{"type":"object","required":["ok"],"properties":{"ok":{"type":"boolean"}}}`), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}

	cfg := host.LadderConfig{
		Models: []host.LadderModel{
			{Backend: "claude", Model: "cheap-model"},
			{Backend: "claude", Model: "strong-model"},
		},
		Efforts:   []string{""},
		StatePath: filepath.Join(t.TempDir(), "ladder-state.json"),
	}

	var modelsSeen []string
	runner := func(_ context.Context, args []string, _, _ string) (host.ClaudeRun, error) {
		model := modelFlag(args)
		modelsSeen = append(modelsSeen, model)
		if model == "cheap-model" {
			// Never calls submit() — the acceptance loop exhausts its budget.
			return host.ClaudeRun{Stdout: "I can't do this"}, nil
		}
		if outputPath := host.ParseMCPConfigSubmitOutput(args); outputPath != "" {
			_ = os.WriteFile(outputPath, []byte(`{"ok":true}`), 0o600)
		}
		return host.ClaudeRun{Stdout: `{"ok":true}`}, nil
	}

	ctx := host.WithAgents(context.Background(), map[string]host.Agent{
		"worker": {SystemPrompt: "do the work"},
	})
	ctx = host.WithHarnessLadder(ctx, cfg)
	ctx = host.WithClaudeRunner(ctx, runner)

	res, err := host.AgentTaskHandler(ctx, map[string]any{
		"agent":       "worker",
		"working_dir": dir,
		"context":     map[string]any{"prompt": "do it"},
		"acceptance":  map[string]any{"schema": schemaPath, "max_retries": 1},
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("expected the ladder to escalate to strong-model and succeed, got Result.Error=%q", res.Error)
	}
	if len(modelsSeen) != 2 || modelsSeen[0] != "cheap-model" || modelsSeen[1] != "strong-model" {
		t.Fatalf("expected exactly one exhausted attempt against cheap-model then strong-model, got %v", modelsSeen)
	}
	if v, _ := res.Data["submitted"].(map[string]any); v == nil || v["ok"] != true {
		t.Fatalf("expected strong-model's submission surfaced, got %+v", res.Data["submitted"])
	}
}

// TestAgentTask_Ladder_PerCallConfig proves a story can opt a single
// host.agent.task invocation into the ladder with with.harness_ladder, without
// relying on an operator-global WithHarnessLadder context.
func TestAgentTask_Ladder_PerCallConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "ok.schema.json")
	if err := os.WriteFile(schemaPath, []byte(`{"type":"object","required":["ok"],"properties":{"ok":{"type":"boolean"}}}`), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}

	var modelsSeen []string
	runner := func(_ context.Context, args []string, _, _ string) (host.ClaudeRun, error) {
		model := modelFlag(args)
		modelsSeen = append(modelsSeen, model)
		if model == "cheap-model" {
			return host.ClaudeRun{Stdout: "not enough"}, nil
		}
		if outputPath := host.ParseMCPConfigSubmitOutput(args); outputPath != "" {
			_ = os.WriteFile(outputPath, []byte(`{"ok":true}`), 0o600)
		}
		return host.ClaudeRun{Stdout: `{"ok":true}`}, nil
	}

	ctx := host.WithAgents(context.Background(), map[string]host.Agent{
		"worker": {SystemPrompt: "do the work"},
	})
	ctx = host.WithClaudeRunner(ctx, runner)

	res, err := host.AgentTaskHandler(ctx, map[string]any{
		"agent":       "worker",
		"working_dir": dir,
		"context":     map[string]any{"prompt": "do it"},
		"acceptance":  map[string]any{"schema": schemaPath, "max_retries": 1},
		"harness_ladder": map[string]any{
			"models": []any{
				map[string]any{"backend": "claude", "model": "cheap-model"},
				map[string]any{"backend": "claude", "model": "strong-model"},
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("expected per-call ladder to reach strong-model, got Result.Error=%q", res.Error)
	}
	if len(modelsSeen) != 2 || modelsSeen[0] != "cheap-model" || modelsSeen[1] != "strong-model" {
		t.Fatalf("expected per-call ladder cheap-model then strong-model, got %v", modelsSeen)
	}
}
