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

// TestCopilotLiveSmoke forks the REAL `copilot` CLI once to re-confirm the two
// facts the copilot backend depends on, which only a live run can verify:
//
//  1. the MCP tool-name scheme — a server named "kitsoki-validator" must expose
//     the tool copilotBackend.ValidatorToolName returns; and
//  2. the side-channel capture contract — copilot can be driven to call that
//     tool through --additional-mcp-config @<file> and the kitsoki mcp-validator
//     server writes the schema-validated payload to its --output file.
//
// It is GATED: skipped unless KITSOKI_ORACLE_LIVE=1, because it incurs a real
// (free-model) Copilot request. CI never runs it. Run manually with:
//
//	KITSOKI_ORACLE_LIVE=1 go test ./internal/host -run TestCopilotLiveSmoke -v
//
// It builds the kitsoki binary on the fly to serve as the validator MCP server.
func TestCopilotLiveSmoke(t *testing.T) {
	if os.Getenv("KITSOKI_ORACLE_LIVE") != "1" {
		t.Skip("set KITSOKI_ORACLE_LIVE=1 to run the live copilot smoke test (real Copilot request)")
	}
	copilotBin, err := exec.LookPath("copilot")
	if err != nil {
		t.Skipf("copilot binary not on PATH: %v", err)
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
	cfgPath := filepath.Join(dir, "mcp.json")
	cfgBytes, _ := json.Marshal(mcpCfg)
	mustWrite(t, cfgPath, string(cfgBytes))

	toolName := copilotBackend{}.ValidatorToolName(serverName)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, copilotBin,
		"-p", "Call the `"+toolName+"` tool exactly once with {\"answer\":\"hello\"}. Do nothing else.",
		"--additional-mcp-config", "@"+cfgPath,
		"--output-format", "json", "--allow-all-tools", "--no-color", "--log-level", "none",
	)
	out, runErr := cmd.CombinedOutput()
	if runErr != nil {
		t.Fatalf("copilot run failed: %v\n%s", runErr, out)
	}

	// (1) The capture file proves the submit tool fired and validated.
	captured, rerr := os.ReadFile(capturePath)
	if rerr != nil {
		t.Fatalf("no capture file written — submit tool %q likely not invoked; copilot output:\n%s", toolName, out)
	}
	if !strings.Contains(string(captured), `"answer"`) {
		t.Errorf("capture = %q, want a payload with an answer field", captured)
	}

	// (2) The tool name copilot actually used must match ValidatorToolName, so
	//     a future copilot release changing the scheme fails loudly here.
	if !strings.Contains(string(out), toolName) {
		t.Errorf("copilot JSONL never references the expected tool name %q; output:\n%s", toolName, out)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// repoRoot walks up from the test's cwd to the module root (the dir holding
// go.mod) so `go build kitsoki/cmd/kitsoki` resolves.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("go.mod not found above %s", dir)
		}
		dir = parent
	}
}
