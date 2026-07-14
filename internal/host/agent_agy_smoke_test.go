package host

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestAgyLiveSmoke forks the REAL `agy` CLI once to re-confirm the two
// facts the agy backend depends on, which only a live run can verify:
//
//  1. the MCP tool-name scheme — a server named "kitsoki-validator" must expose
//     the tool agyBackend.ValidatorToolName returns; and
//  2. the side-channel capture contract — agy can be driven to call that
//     tool through the validator server registered in its app_data_dir, and
//     the kitsoki mcp-validator server writes the schema-validated payload.
//
// It is GATED: skipped unless KITSOKI_AGENT_LIVE=1, because it incurs a real
// (free-model) Antigravity request. CI never runs it. Run manually with:
//
//	KITSOKI_AGENT_LIVE=1 go test ./internal/host -run TestAgyLiveSmoke -v
//
// It builds the kitsoki binary on the fly to serve as the validator MCP server.
func TestAgyLiveSmoke(t *testing.T) {
	if os.Getenv("KITSOKI_AGENT_LIVE") != "1" {
		t.Skip("set KITSOKI_AGENT_LIVE=1 to run the live agy smoke test")
	}
	agyBin, err := exec.LookPath("agy")
	if err != nil {
		t.Skipf("agy binary not on PATH: %v", err)
	}

	dir := t.TempDir()

	// Build kitsoki so the validator MCP server is a real on-disk binary.
	kitsokiBin := filepath.Join(dir, "kitsoki")
	build := exec.Command("go", "build", "-o", kitsokiBin, "kitsoki/cmd/kitsoki")
	build.Dir = repoRoot(t)
	if out, berr := build.CombinedOutput(); berr != nil {
		t.Fatalf("build kitsoki: %v\n%s", berr, out)
	}

	schemaPath := filepath.Join(dir, "schema.json")
	mustWrite(t, schemaPath, `{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"],"additionalProperties":false}`)

	capturePath := filepath.Join(dir, "capture.json")
	serverName := "kitsoki-validator"
	mcpCfg := map[string]any{
		"mcpServers": map[string]any{
			serverName: map[string]any{
				"command": kitsokiBin,
				"args":    []any{"mcp-validator", "--schema", schemaPath, "--output", capturePath},
			},
		},
	}

	// Create simulated app data dir with credentials copied from home
	geminiDir := filepath.Join(dir, ".gemini")
	cliDir := filepath.Join(geminiDir, "antigravity-cli")
	if err := os.MkdirAll(cliDir, 0700); err != nil {
		t.Fatalf("mkdir app_data_dir: %v", err)
	}

	home, herr := os.UserHomeDir()
	if herr == nil {
		realGemini := filepath.Join(home, ".gemini")
		realCli := filepath.Join(realGemini, "antigravity-cli")

		copyFile := func(src, dst string) {
			if data, rErr := os.ReadFile(src); rErr == nil {
				os.WriteFile(dst, data, 0600)
			}
		}

		copyFile(filepath.Join(realGemini, "google_accounts.json"), filepath.Join(geminiDir, "google_accounts.json"))
		copyFile(filepath.Join(realGemini, "oauth_creds.json"), filepath.Join(geminiDir, "oauth_creds.json"))
		copyFile(filepath.Join(realGemini, "state.json"), filepath.Join(geminiDir, "state.json"))
		copyFile(filepath.Join(realCli, "settings.json"), filepath.Join(cliDir, "settings.json"))
	}

	cfgPath := filepath.Join(cliDir, "mcp_config.json")
	cfgBytes, _ := json.Marshal(mcpCfg)
	mustWrite(t, cfgPath, string(cfgBytes))

	toolName := agyBackend{}.ValidatorToolName(serverName)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, agyBin,
		"--print", "Call the `"+toolName+"` tool exactly once with {\"answer\":\"hello\"}. Do nothing else.",
		"--app_data_dir", dir,
		"--output-format", "json",
		"--dangerously-skip-permissions",
	)
	out, runErr := cmd.CombinedOutput()
	if runErr != nil {
		t.Fatalf("agy run failed: %v\n%s", runErr, out)
	}

	// (1) The capture file proves the submit tool fired and validated.
	captured, rerr := os.ReadFile(capturePath)
	if rerr != nil {
		t.Fatalf("no capture file written — submit tool %q likely not invoked; agy output:\n%s", toolName, out)
	}
	if !strings.Contains(string(captured), `"answer"`) {
		t.Errorf("capture = %q, want a payload with an answer field", captured)
	}

	// (2) The response must be success.
	if !strings.Contains(string(out), `"SUCCESS"`) {
		t.Errorf("agy response was not success; output:\n%s", out)
	}
}
