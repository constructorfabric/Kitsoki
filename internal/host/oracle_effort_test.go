package host_test

// Effort forwarding: an agent's effort: (or an effect's inline effort: arg)
// must reach the claude CLI as `--effort <level>`. The inline arg wins over the
// agent's declared value; when neither is set no --effort flag is added (the
// CLI keeps its own default). Reuses capturingRunner/hasFlagPair from
// oracle_setting_sources_test.go.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"kitsoki/internal/host"
)

// hasFlag reports whether args contains flag at any position.
func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

func TestOracleEffort_AgentForwarded(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	promptFile := filepath.Join(dir, "p.md")
	if err := os.WriteFile(promptFile, []byte("inspect"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	var captured []string
	ctx := host.WithClaudeRunner(context.Background(), capturingRunner(&captured))
	ctx = host.WithAgents(ctx, map[string]host.Agent{
		"judge": {SystemPrompt: "sp", Effort: "high"},
	})

	if _, err := host.OracleAskHandler(ctx, map[string]any{
		"prompt_path": promptFile,
		"agent":       "judge",
	}); err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !hasFlagPair(captured, "--effort", "high") {
		t.Errorf("agent effort not forwarded; argv: %v", captured)
	}
}

func TestOracleEffort_InlineWinsOverAgent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	promptFile := filepath.Join(dir, "p.md")
	if err := os.WriteFile(promptFile, []byte("inspect"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	var captured []string
	ctx := host.WithClaudeRunner(context.Background(), capturingRunner(&captured))
	ctx = host.WithAgents(ctx, map[string]host.Agent{
		"judge": {SystemPrompt: "sp", Effort: "low"},
	})

	if _, err := host.OracleAskHandler(ctx, map[string]any{
		"prompt_path": promptFile,
		"agent":       "judge",
		"effort":      "max",
	}); err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !hasFlagPair(captured, "--effort", "max") {
		t.Errorf("inline effort did not win over agent effort; argv: %v", captured)
	}
}

func TestOracleEffort_AbsentNoFlag(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	promptFile := filepath.Join(dir, "p.md")
	if err := os.WriteFile(promptFile, []byte("inspect"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	var captured []string
	ctx := host.WithClaudeRunner(context.Background(), capturingRunner(&captured))

	if _, err := host.OracleAskHandler(ctx, map[string]any{
		"prompt_path": promptFile,
	}); err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if hasFlag(captured, "--effort") {
		t.Errorf("--effort added with no effort configured; argv: %v", captured)
	}
}
