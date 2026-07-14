package host

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestCodexLiveSmoke forks the real `codex exec` binary once against a kitsoki
// mcp-validator server to (a) prove the validator submit tool fires and
// validates, and (b) pin the MCP tool name codex actually uses against
// codexBackend.ValidatorToolName. Gated on KITSOKI_AGENT_LIVE=1 (a real Codex
// request, never run in CI). mustWrite / repoRoot are shared with the copilot
// smoke test.
func TestCodexLiveSmoke(t *testing.T) {
	if os.Getenv("KITSOKI_AGENT_LIVE") != "1" {
		t.Skip("set KITSOKI_AGENT_LIVE=1 to run the live codex smoke test (real Codex request)")
	}
	codexBin, err := exec.LookPath("codex")
	if env := os.Getenv(CodexBinEnv); env != "" {
		codexBin, err = env, nil
	}
	if err != nil {
		t.Skipf("codex binary not on PATH: %v", err)
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
	toolName := codexBackend{}.ValidatorToolName(serverName)

	// codex registers MCP servers via `-c mcp_servers.<name>.*` config overrides
	// (no --additional-mcp-config flag); build the same overrides the translator
	// emits. args is a TOML array.
	argsTOML := tomlStringArray([]string{"mcp-validator", "--schema", schemaPath, "--output", capturePath})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	// codex ≥0.142 defers MCP tools behind tool_search, so the production prompt
	// carries codexMCPToolSearchPreamble (TranslateInvocation injects it whenever an
	// MCP config is registered). Mirror that here so the smoke proves the SAME
	// discovery path the real reviser uses, not a fictional eager-tool world.
	prompt := codexMCPToolSearchPreamble + "\n\n---\n\n" +
		"Call the `" + toolName + "` tool exactly once with {\"answer\":\"hello\"}. Do nothing else."
	cmd := exec.CommandContext(ctx, codexBin, "exec",
		// Bypass flag required: codex exec auto-cancels MCP tool calls without
		// it, so the validator submit tool would never execute. Mirrors the
		// translator's base args (see agent_backend_codex.go).
		"--json", "--skip-git-repo-check", "--dangerously-bypass-approvals-and-sandbox", "--disable=apps",
		"-c", "mcp_servers."+serverName+".command="+tomlString(kitsokiBin),
		"-c", "mcp_servers."+serverName+".args="+argsTOML,
	)
	cmd.Stdin = strings.NewReader(prompt)
	out, runErr := cmd.CombinedOutput()
	if runErr != nil {
		t.Fatalf("codex run failed: %v\n%s", runErr, out)
	}

	// (1) The capture file proves the submit tool fired and validated.
	captured, rerr := os.ReadFile(capturePath)
	if rerr != nil {
		t.Fatalf("no capture file written — submit tool %q likely not invoked; codex output:\n%s", toolName, out)
	}
	if !strings.Contains(string(captured), `"answer"`) {
		t.Errorf("capture = %q, want a payload with an answer field", captured)
	}

	// (2) Pin the actual MCP tool name codex used against ValidatorToolName.
	// Live-verified: codex uses bare "submit" (the server name is a separate
	// JSONL field, not part of the tool name); if codex namespaces differently,
	// this fails loudly and the actual name is printed for correcting the code.
	if !strings.Contains(string(out), toolName) {
		t.Errorf("codex JSONL never references the expected tool name %q — inspect the mcp_tool_call items in the output below and correct ValidatorToolName:\n%s", toolName, out)
		for _, line := range strings.Split(string(out), "\n") {
			if strings.Contains(line, "mcp_tool_call") || strings.Contains(line, "submit") {
				t.Logf("candidate tool event: %s", line)
			}
		}
	}
}
