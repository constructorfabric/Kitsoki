package host_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	host "kitsoki/internal/host"
)

// TestAgentTask_StudioMCPAttachedForDeclaredTools reproduces the scenario-qa
// live failure: an agent whose tools: declare mcp__kitsoki__* names must get
// the kitsoki studio MCP server attached via a --mcp-config entry (WM.10 /
// attachStudioMCPServer), or the subprocess can never discover those tools.
func TestAgentTask_StudioMCPAttachedForDeclaredTools(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "ok.schema.json")
	if err := os.WriteFile(schemaPath, []byte(`{"type":"object","required":["ok"],"properties":{"ok":{"type":"boolean"}}}`), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}

	// Read every --mcp-config file INSIDE the runner — the handler deletes
	// its tempfiles on return, so a post-hoc read races the cleanup.
	var captured []string
	var mcpConfigs []string
	runner := func(_ context.Context, args []string, _, _ string) (host.ClaudeRun, error) {
		captured = append([]string(nil), args...)
		for i, a := range args {
			if a == "--mcp-config" && i+1 < len(args) {
				if data, rerr := os.ReadFile(args[i+1]); rerr == nil {
					mcpConfigs = append(mcpConfigs, string(data))
				}
			}
		}
		if outputPath := host.ParseMCPConfigSubmitOutput(args); outputPath != "" {
			_ = os.WriteFile(outputPath, []byte(`{"ok":true}`), 0o600)
		}
		return host.ClaudeRun{Stdout: `{"ok":true}`}, nil
	}
	ctx := host.WithClaudeRunner(
		host.WithAgents(context.Background(), map[string]host.Agent{
			"driver": {
				SystemPrompt: "drive",
				Model:        "claude-sonnet-4-6",
				Tools:        []string{"host.Read", "host.Bash", "mcp__kitsoki__render_tui_png", "mcp__kitsoki__session_new"},
			},
		}),
		runner,
	)

	res, err := host.AgentTaskHandler(ctx, map[string]any{
		"agent":       "driver",
		"working_dir": dir,
		"context":     map[string]any{"prompt": "do it"},
		"acceptance":  map[string]any{"schema": schemaPath, "max_retries": 1},
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error: %s", res.Error)
	}

	var kitsokiAttached, validatorAttached bool
	for _, cfg := range mcpConfigs {
		if strings.Contains(cfg, `"kitsoki"`) {
			kitsokiAttached = true
		}
		if strings.Contains(cfg, `"validator"`) {
			validatorAttached = true
		}
	}
	if !kitsokiAttached {
		t.Fatalf("agent declares mcp__kitsoki__* tools but no --mcp-config carries the kitsoki studio server; args=%v configs=%v", captured, mcpConfigs)
	}
	if !validatorAttached {
		t.Fatalf("validator server must survive alongside the contract config; configs=%v", mcpConfigs)
	}
}
