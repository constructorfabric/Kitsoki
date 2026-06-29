package harness_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"kitsoki/internal/app"
	"kitsoki/internal/harness"
	"kitsoki/internal/host"
	"kitsoki/internal/world"
)

func TestClaudeCLIHarness_RepeatedDispatchDoesNotRechargeStablePrefix(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test executable: %v", err)
	}

	var measured []int
	exec := func(ctx context.Context, bin string, args []string, stdin, workingDir string) (string, error) {
		runner := func(ctx context.Context, args []string, stdin, workingDir string) (host.ClaudeRun, error) {
			if err := writeValidatedTransition(args, "go"); err != nil {
				return host.ClaudeRun{}, err
			}
			tokens := promptPayloadBytes(args, stdin) / 4
			return host.ClaudeRun{Stdout: fmt.Sprintf(`{"type":"result","subtype":"success","result":"ok","usage":{"input_tokens":%d,"output_tokens":1}}`, tokens)}, nil
		}
		return host.RunClaudeOneShotForHarness(host.WithClaudeRunner(ctx, runner), bin, args, stdin, workingDir)
	}

	h, err := harness.NewClaudeCLI(largeStablePrefixApp(), harness.ClaudeCLIConfig{
		ClaudeBin:  exe,
		KitsokiBin: exe,
		Exec:       exec,
	})
	if err != nil {
		t.Fatalf("NewClaudeCLI: %v", err)
	}

	for i, text := range []string{"first dispatch", "second dispatch"} {
		ctx := host.WithAgentUsageBox(context.Background())
		_, err := h.RunTurn(ctx, harness.TurnInput{
			SessionID:      app.SessionID("token-bloat-repro"),
			TurnNumber:     app.TurnNumber(i + 1),
			StatePath:      app.StatePath("main"),
			UserText:       text,
			World:          world.New(),
			AllowedIntents: []string{"go"},
		})
		if err != nil {
			t.Fatalf("RunTurn %d: %v", i+1, err)
		}
		usage := host.AgentUsageFrom(ctx)
		if usage == nil {
			t.Fatalf("RunTurn %d recorded no usage", i+1)
		}
		measured = append(measured, usageInputTokens(usage))
	}

	if len(measured) != 2 {
		t.Fatalf("expected 2 measured requests, got %d", len(measured))
	}
	const warmDispatchBudget = 2500
	if measured[1] > warmDispatchBudget {
		t.Fatalf("second dispatch measured %d input tokens, want <= %d after stable-prefix caching/trimming (first dispatch measured %d)",
			measured[1], warmDispatchBudget, measured[0])
	}
}

func usageInputTokens(usage map[string]any) int {
	switch v := usage["input_tokens"].(type) {
	case float64:
		return int(v)
	case int:
		return v
	default:
		return 0
	}
}

func largeStablePrefixApp() *app.AppDef {
	intents := map[string]app.Intent{
		"go": {
			Title:       "Go",
			Description: "Move to the selected room.",
			Slots: map[string]app.Slot{
				"direction": {Type: "string"},
			},
		},
	}
	for i := 0; i < 80; i++ {
		name := fmt.Sprintf("stable_intent_%02d", i)
		intents[name] = app.Intent{
			Title:       fmt.Sprintf("Stable intent %02d", i),
			Description: strings.Repeat("This sentence stands in for serialized story routing context and must not be recharged on every warm dispatch. ", 8),
			Examples: []string{
				fmt.Sprintf("example phrase %02d alpha", i),
				fmt.Sprintf("example phrase %02d beta", i),
			},
			Slots: map[string]app.Slot{
				"value": {Type: "string", Description: strings.Repeat("stable slot detail ", 8)},
			},
		}
	}
	return &app.AppDef{
		App: app.AppMeta{
			ID:      "token-bloat-repro",
			Title:   "Token Bloat Repro",
			Context: strings.Repeat("PROJECT-STABLE-CONTEXT ", 2048),
		},
		Intents: intents,
		States: map[string]*app.State{
			"main": {
				Description: "Main room.",
				View:        app.LegacyView("Ready."),
			},
		},
	}
}

func writeValidatedTransition(args []string, intent string) error {
	configPath := flagValue(args, "--mcp-config")
	if configPath == "" {
		return fmt.Errorf("missing --mcp-config")
	}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	var cfg struct {
		MCPServers map[string]struct {
			Args []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return err
	}
	for _, server := range cfg.MCPServers {
		out := flagValue(server.Args, "--output")
		if out == "" {
			continue
		}
		return os.WriteFile(out, []byte(fmt.Sprintf(`{"intent":%q,"slots":{},"confidence":0.99}`, intent)), 0o600)
	}
	return fmt.Errorf("validator output path not found")
}

func promptPayloadBytes(args []string, stdin string) int {
	total := len(stdin)
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--system-prompt", "--append-system-prompt":
			if i+1 < len(args) {
				total += len(args[i+1])
				i++
			}
		default:
			if v, ok := strings.CutPrefix(args[i], "--system-prompt="); ok {
				total += len(v)
			}
			if v, ok := strings.CutPrefix(args[i], "--append-system-prompt="); ok {
				total += len(v)
			}
		}
	}
	return total
}

func flagValue(args []string, name string) string {
	for i := 0; i < len(args); i++ {
		if args[i] == name && i+1 < len(args) {
			return args[i+1]
		}
		if v, ok := strings.CutPrefix(args[i], name+"="); ok {
			return v
		}
	}
	return ""
}
