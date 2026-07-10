package host_test

import (
	"context"
	"strings"
	"testing"

	"kitsoki/internal/host"
)

func TestAgentTask_LaunchPolicyDeniesBeforeRunnerOrPlugin(t *testing.T) {
	t.Parallel()
	ctx := host.WithAgents(context.Background(), map[string]host.Agent{
		"worker": {SystemPrompt: "do work"},
	})
	ctx = host.WithClaudeRunner(ctx, func(context.Context, []string, string, string) (host.ClaudeRun, error) {
		t.Fatal("runner must not be called after launch policy denial")
		return host.ClaudeRun{}, nil
	})
	ctx = host.WithAgentLaunchPolicy(ctx, host.AgentLaunchPolicy{
		Enabled:           true,
		RequireCapsule:    true,
		ProtectedBranches: []string{"release"},
	})

	res, err := host.AgentTaskHandler(ctx, map[string]any{
		"agent":       "worker",
		"working_dir": t.TempDir(),
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.FailureKind != host.FailureFatal {
		t.Fatalf("expected fatal failure kind, got %#v", res)
	}
	if !strings.Contains(res.Error, "agent launch policy denied") {
		t.Fatalf("expected policy denial, got %q", res.Error)
	}
}

func TestAgentConverse_LaunchPolicyDeniesBeforeRunner(t *testing.T) {
	t.Parallel()
	ctx := host.WithClaudeRunner(context.Background(), func(context.Context, []string, string, string) (host.ClaudeRun, error) {
		t.Fatal("runner must not be called after launch policy denial")
		return host.ClaudeRun{}, nil
	})
	ctx = host.WithAgentLaunchPolicy(ctx, host.AgentLaunchPolicy{
		Enabled:           true,
		RequireCapsule:    true,
		ProtectedBranches: []string{"release"},
	})

	res, err := host.AgentConverseHandler(ctx, map[string]any{
		"question":    "hello",
		"working_dir": t.TempDir(),
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !strings.Contains(res.Error, "agent launch policy denied") {
		t.Fatalf("expected policy denial, got %q", res.Error)
	}
}

func TestAgentCodeact_LaunchPolicyDeniesBeforeRunner(t *testing.T) {
	t.Parallel()
	ctx := host.WithAgents(context.Background(), map[string]host.Agent{
		"coder": {SystemPrompt: "write snippets"},
	})
	ctx = host.WithClaudeRunner(ctx, func(context.Context, []string, string, string) (host.ClaudeRun, error) {
		t.Fatal("runner must not be called after launch policy denial")
		return host.ClaudeRun{}, nil
	})
	ctx = host.WithAgentLaunchPolicy(ctx, host.AgentLaunchPolicy{
		Enabled:           true,
		RequireCapsule:    true,
		ProtectedBranches: []string{"release"},
	})

	res, err := host.AgentCodeactHandler(ctx, map[string]any{
		"agent":       "coder",
		"goal":        "produce output",
		"working_dir": t.TempDir(),
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.FailureKind != host.FailureFatal {
		t.Fatalf("expected fatal failure kind, got %#v", res)
	}
	if !strings.Contains(res.Error, "agent launch policy denied") {
		t.Fatalf("expected policy denial, got %q", res.Error)
	}
}
