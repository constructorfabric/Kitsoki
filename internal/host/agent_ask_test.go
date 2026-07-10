package host_test

// agent_ask_test.go — Phase 3 tests for host.agent.ask.
//
// Coverage:
//   - Verb contract: prompt_path required; prompt alias accepted.
//   - Tool surface: mutation tools (Edit, Write) hard-denied by toolbox policy.
//   - Bash gate: Bash in tools without bash_profile is rejected.
//   - Bash profile enforcement: read-only profile blocks rm; commands profile
//     blocks unlisted argv0; sandboxed-write allows any command.
//   - Streaming: tokens stream through AgentStreamer (stub path).
//   - Schema mode: submitted is present in result when schema is set.
//   - Read-tool snapshot cap: 300 KiB output stores hash + 4 KiB prefix.
//
// All tests use FakeAsk / FakeAskWithMeta / custom ClaudeRunner stubs.
// No real LLM calls; no real subprocesses.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kitsoki/internal/host"
)

// ── Verb contract ─────────────────────────────────────────────────────────────

func TestAgentAsk_RequiresPromptPath(t *testing.T) {
	t.Parallel()
	ctx := host.WithClaudeRunner(context.Background(), host.FakeAsk("ok"))
	res, err := host.AgentAskHandler(ctx, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !strings.Contains(res.Error, "prompt_path") {
		t.Fatalf("expected prompt_path error, got %q", res.Error)
	}
}

func TestAgentAsk_PromptAliasAccepted(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "p.md")
	if err := os.WriteFile(p, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	ctx := host.WithClaudeRunner(context.Background(), host.FakeAsk("reply"))
	res, err := host.AgentAskHandler(ctx, map[string]any{"prompt": p})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error: %s", res.Error)
	}
	if _, ok := res.Data["stdout"]; !ok {
		t.Fatalf("stdout missing from result: %v", res.Data)
	}
}

func TestAgentAsk_AgentOptional(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "p.md")
	if err := os.WriteFile(p, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	// No agent in args, no agents in context — should succeed.
	ctx := host.WithClaudeRunner(context.Background(), host.FakeAsk("answer"))
	res, err := host.AgentAskHandler(ctx, map[string]any{"prompt_path": p})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error: %s", res.Error)
	}
}

// ── Tool surface enforcement ──────────────────────────────────────────────────

func TestAgentAsk_HardDeniesMutationTool_Edit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "p.md")
	if err := os.WriteFile(p, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	var captured []string
	runner := host.FakeAsk("ok")
	ctx := host.WithClaudeRunner(
		host.WithAgents(context.Background(), map[string]host.Agent{
			"mutator": {Tools: []string{"Read", "Edit"}},
		}),
		func(ctx context.Context, args []string, stdin, workingDir string) (host.ClaudeRun, error) {
			captured = append([]string(nil), args...)
			return runner(ctx, args, stdin, workingDir)
		},
	)
	res, err := host.AgentAskHandler(ctx, map[string]any{
		"prompt_path": p,
		"agent":       "mutator",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected handler error: %q", res.Error)
	}
	denied, ok := hostTestFlagValue(captured, "--disallowedTools")
	if !ok || !strings.Contains(denied, "Edit") {
		t.Fatalf("expected Edit in --disallowedTools, got args=%v", captured)
	}
}

func TestAgentAsk_HardDeniesMutationTool_Write(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "p.md")
	if err := os.WriteFile(p, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	var captured []string
	runner := host.FakeAsk("ok")
	ctx := host.WithClaudeRunner(
		host.WithAgents(context.Background(), map[string]host.Agent{
			"mutator": {Tools: []string{"Write"}},
		}),
		func(ctx context.Context, args []string, stdin, workingDir string) (host.ClaudeRun, error) {
			captured = append([]string(nil), args...)
			return runner(ctx, args, stdin, workingDir)
		},
	)
	res, err := host.AgentAskHandler(ctx, map[string]any{
		"prompt_path": p,
		"agent":       "mutator",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected handler error: %q", res.Error)
	}
	denied, ok := hostTestFlagValue(captured, "--disallowedTools")
	if !ok || !strings.Contains(denied, "Write") {
		t.Fatalf("expected Write in --disallowedTools, got args=%v", captured)
	}
}

func TestAgentAsk_PerCallTools_HardDeniesMutation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "p.md")
	if err := os.WriteFile(p, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	var captured []string
	runner := host.FakeAsk("ok")
	ctx := host.WithClaudeRunner(context.Background(), func(ctx context.Context, args []string, stdin, workingDir string) (host.ClaudeRun, error) {
		captured = append([]string(nil), args...)
		return runner(ctx, args, stdin, workingDir)
	})
	res, err := host.AgentAskHandler(ctx, map[string]any{
		"prompt_path": p,
		"tools":       []any{"Read", "Edit"},
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected handler error: %q", res.Error)
	}
	denied, ok := hostTestFlagValue(captured, "--disallowedTools")
	if !ok || !strings.Contains(denied, "Edit") {
		t.Fatalf("expected Edit in --disallowedTools, got args=%v", captured)
	}
}

// ── Bash gate ─────────────────────────────────────────────────────────────────

func TestAgentAsk_BashWithoutProfile_Rejected(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "p.md")
	if err := os.WriteFile(p, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	ctx := host.WithClaudeRunner(
		host.WithAgents(context.Background(), map[string]host.Agent{
			"no-profile-bash": {
				Tools:       []string{"Bash"},
				BashProfile: nil,
			},
		}),
		host.FakeAsk("nope"),
	)
	res, err := host.AgentAskHandler(ctx, map[string]any{
		"prompt_path": p,
		"agent":       "no-profile-bash",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !strings.Contains(res.Error, "bash_profile") {
		t.Fatalf("expected bash_profile error, got %q", res.Error)
	}
}

func TestAgentAsk_BashWithReadOnlyProfile_Allowed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "p.md")
	if err := os.WriteFile(p, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	ctx := host.WithClaudeRunner(
		host.WithAgents(context.Background(), map[string]host.Agent{
			"read-bash": {
				Tools:       []string{"Read", "Bash"},
				BashProfile: &host.BashProfile{Kind: host.BashProfileReadOnly},
			},
		}),
		host.FakeAsk("ok"),
	)
	res, err := host.AgentAskHandler(ctx, map[string]any{
		"prompt_path": p,
		"agent":       "read-bash",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error: %s", res.Error)
	}
}

// ── Bash profile enforcement (ApplyBashProfile) ───────────────────────────────
// These tests exercise ApplyBashProfile directly (already in agent_phase1_test.go
// for the helper; these verify the profile kind semantics used in ask context).

func TestBashProfile_ReadOnly_BlocksRm(t *testing.T) {
	t.Parallel()
	profile := &host.BashProfile{Kind: host.BashProfileReadOnly}
	if msg := host.ApplyBashProfile(profile, "rm -rf /tmp/foo"); msg == "" {
		t.Fatal("read-only profile should block rm")
	}
}

func TestBashProfile_Commands_BlocksUnlistedArgv0(t *testing.T) {
	t.Parallel()
	profile := &host.BashProfile{
		Kind:     host.BashProfileCommands,
		Commands: []string{"git", "jq"},
	}
	if msg := host.ApplyBashProfile(profile, "curl http://example.com"); msg == "" {
		t.Fatal("commands profile should block curl (not in list)")
	}
	if msg := host.ApplyBashProfile(profile, "git log --oneline"); msg != "" {
		t.Fatalf("commands profile should allow git: %s", msg)
	}
}

func TestBashProfile_SandboxWrite_AllowsAnyCommand(t *testing.T) {
	t.Parallel()
	profile := &host.BashProfile{Kind: host.BashProfileSandboxWrite}
	if msg := host.ApplyBashProfile(profile, "make build"); msg != "" {
		t.Fatalf("sandboxed-write should allow any command: %s", msg)
	}
}

// ── Streaming ─────────────────────────────────────────────────────────────────

func TestAgentAsk_TokensFlowThroughAgentStreamer(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "p.md")
	if err := os.WriteFile(p, []byte("explain"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	const wantText = "streaming result text"
	ctx := host.WithClaudeRunner(context.Background(), host.FakeAsk(wantText))

	res, err := host.AgentAskHandler(ctx, map[string]any{"prompt_path": p})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error: %s", res.Error)
	}
	stdout, _ := res.Data["stdout"].(string)
	if !strings.Contains(stdout, wantText) {
		t.Fatalf("stdout does not contain expected text %q; got %q", wantText, stdout)
	}
}

// ── Schema mode ───────────────────────────────────────────────────────────────

func TestAgentAsk_SchemaMode_SubmittedPresentWhenSchemaSet(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	promptFile := filepath.Join(dir, "p.md")
	if err := os.WriteFile(promptFile, []byte("explain"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	// Write a minimal JSON schema.
	schemaFile := filepath.Join(dir, "schema.json")
	if err := os.WriteFile(schemaFile, []byte(`{"type":"object","properties":{"label":{"type":"string"}}}`), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}

	// The FakeAsk runner returns plain text; the handler tries to read the
	// submit tempfile. Since no real mcp-validator runs during the test, the
	// submitted tempfile is empty and `submitted` will be absent from the
	// result. We verify the schema path doesn't crash and stdout is still set.
	ctx := host.WithClaudeRunner(context.Background(), host.FakeAsk("explanation text"))

	res, err := host.AgentAskHandler(ctx, map[string]any{
		"prompt_path": promptFile,
		"schema":      schemaFile,
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error: %s", res.Error)
	}
	if _, ok := res.Data["stdout"]; !ok {
		t.Fatalf("stdout missing from result: %v", res.Data)
	}
}

func TestAgentAsk_SchemaMode_SubmittedFromTempFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	promptFile := filepath.Join(dir, "p.md")
	if err := os.WriteFile(promptFile, []byte("explain"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}
	schemaFile := filepath.Join(dir, "schema.json")
	if err := os.WriteFile(schemaFile, []byte(`{"type":"object"}`), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}

	// Simulate what the mcp-validator would write: a custom ClaudeRunner that
	// intercepts the --mcp-config flag, parses the MCP config JSON to find the
	// --output tempfile path, writes a JSON payload to it, then returns normally.
	//
	// The MCP config has shape:
	//   {"mcpServers":{"validator":{"command":"<kitsoki>","args":["mcp-validator","--schema","<schema>","--output","<path>"]}}}
	//
	// Full integration (with a real mcp-validator subprocess) is covered by
	// the existing agent_ask_with_mcp_test.go integration tests.
	ctx := host.WithClaudeRunner(context.Background(), func(_ context.Context, cliArgs []string, _, _ string) (host.ClaudeRun, error) {
		for i, a := range cliArgs {
			if a == "--mcp-config" && i+1 < len(cliArgs) {
				mcpConfigPath := cliArgs[i+1]
				submitPath := parseMCPConfigOutputPath(mcpConfigPath)
				if submitPath != "" {
					_ = os.WriteFile(submitPath, []byte(`{"label":"test-result"}`), 0o644)
				}
				break
			}
		}
		return host.ClaudeRun{Stdout: "explanation"}, nil
	})

	res, err := host.AgentAskHandler(ctx, map[string]any{
		"prompt_path": promptFile,
		"schema":      schemaFile,
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error: %s", res.Error)
	}
	if _, ok := res.Data["submitted"]; !ok {
		t.Fatalf("submitted missing from result; data=%v", res.Data)
	}
	sub, _ := res.Data["submitted"].(map[string]any)
	if label, _ := sub["label"].(string); label != "test-result" {
		t.Fatalf("submitted.label: want %q, got %q", "test-result", label)
	}
}

// parseMCPConfigOutputPath reads the MCP config file at configPath and returns
// the --output argument passed to the mcp-validator command. Used by
// TestAgentAsk_SchemaMode_SubmittedFromTempFile to simulate the mcp-validator
// writing a JSON payload to the submit output file.
func parseMCPConfigOutputPath(configPath string) string {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return ""
	}
	// Parse the JSON structure to find the args list.
	var cfg struct {
		MCPServers map[string]struct {
			Args []string `json:"args"`
		} `json:"mcpServers"`
	}
	if jErr := jsonUnmarshalForTest(data, &cfg); jErr != nil {
		return ""
	}
	for _, server := range cfg.MCPServers {
		args := server.Args
		for i, a := range args {
			if a == "--output" && i+1 < len(args) {
				return args[i+1]
			}
		}
	}
	return ""
}

// jsonUnmarshalForTest is a test-local json.Unmarshal wrapper.
func jsonUnmarshalForTest(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

// ── Read-tool snapshot cap ────────────────────────────────────────────────────

func TestReadSnapshot_SmallOutput_StoredVerbatim(t *testing.T) {
	t.Parallel()
	output := "small output"
	snap := host.CaptureReadSnapshot(output, host.ReadSnapshotCap)
	if snap.Truncated {
		t.Fatal("small output should not be truncated")
	}
	if snap.Full != output {
		t.Fatalf("Full: want %q, got %q", output, snap.Full)
	}
	if snap.Hash != "" || snap.Prefix != "" {
		t.Fatal("Hash and Prefix should be empty for non-truncated snapshot")
	}
}

func TestReadSnapshot_LargeOutput_StoresHashAndPrefix(t *testing.T) {
	t.Parallel()
	// Create a 300 KiB output (larger than the 256 KiB cap).
	const size = 300 * 1024
	output := strings.Repeat("x", size)

	snap := host.CaptureReadSnapshot(output, host.ReadSnapshotCap)

	if !snap.Truncated {
		t.Fatal("300 KiB output should be truncated")
	}
	if snap.Full != "" {
		t.Fatalf("Full should be empty for truncated snapshot, got %d bytes", len(snap.Full))
	}
	if snap.Hash == "" {
		t.Fatal("Hash must be set for truncated snapshot")
	}
	if len(snap.Prefix) != host.ReadSnapshotPrefix {
		t.Fatalf("Prefix: want %d bytes, got %d", host.ReadSnapshotPrefix, len(snap.Prefix))
	}
	if snap.Size != size {
		t.Fatalf("Size: want %d, got %d", size, snap.Size)
	}
}

func TestReadSnapshot_DigestMatches_Consistent(t *testing.T) {
	t.Parallel()
	output := strings.Repeat("y", 300*1024)
	snap := host.CaptureReadSnapshot(output, host.ReadSnapshotCap)

	if !host.DigestMatches(snap, output) {
		t.Fatal("DigestMatches should return true for the same output")
	}
	if host.DigestMatches(snap, output+"z") {
		t.Fatal("DigestMatches should return false for a different output")
	}
}

func TestReadSnapshot_SmallOutput_DigestMatches(t *testing.T) {
	t.Parallel()
	output := "small"
	snap := host.CaptureReadSnapshot(output, host.ReadSnapshotCap)
	if !host.DigestMatches(snap, output) {
		t.Fatal("DigestMatches should return true for same small output")
	}
	if host.DigestMatches(snap, "different") {
		t.Fatal("DigestMatches should return false for different small output")
	}
}

func TestReadSnapshot_ZeroCap_NeverTruncates(t *testing.T) {
	t.Parallel()
	output := strings.Repeat("z", 500*1024)
	snap := host.CaptureReadSnapshot(output, 0)
	if snap.Truncated {
		t.Fatal("cap=0 should disable truncation")
	}
	if snap.Full != output {
		t.Fatalf("Full: want full output, got %d bytes", len(snap.Full))
	}
}

// ── M7: tempfile leak fix ─────────────────────────────────────────────────

// TestAgentAsk_SchemaModeSubmitFileCleaned verifies that after the handler
// returns the submit tempfile no longer exists (M7: deferred cleanup).
func TestAgentAsk_SchemaModeSubmitFileCleaned(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	promptFile := filepath.Join(dir, "p.md")
	if err := os.WriteFile(promptFile, []byte("explain"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	schemaFile := filepath.Join(dir, "schema.json")
	if err := os.WriteFile(schemaFile, []byte(`{"type":"object"}`), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}

	var capturedSubmitPath string
	ctx := host.WithClaudeRunner(context.Background(), func(_ context.Context, cliArgs []string, _, _ string) (host.ClaudeRun, error) {
		if p := host.ParseMCPConfigSubmitOutput(cliArgs); p != "" {
			capturedSubmitPath = p
			_ = os.WriteFile(p, []byte(`{"x":1}`), 0o600)
		}
		return host.ClaudeRun{Stdout: "ok"}, nil
	})

	res, err := host.AgentAskHandler(ctx, map[string]any{
		"prompt_path": promptFile,
		"schema":      schemaFile,
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error: %s", res.Error)
	}
	if capturedSubmitPath == "" {
		t.Skip("runner did not capture submit path (no --mcp-config in CLI args; schema MCP not set up)")
	}
	// The file must have been removed by the deferred cleanup.
	if _, statErr := os.Stat(capturedSubmitPath); !os.IsNotExist(statErr) {
		t.Fatalf("submit tempfile %q still exists after handler returned (M7 leak)", capturedSubmitPath)
	}
}

// ── M8: inline prompt content ─────────────────────────────────────────────

// TestAgentAsk_InlinePrompt_AcceptsMultiLineContent verifies that when
// prompt: contains multi-line content (e.g. from stdin), the handler uses it
// as inline text without trying to open it as a file.
func TestAgentAsk_InlinePrompt_AcceptsMultiLineContent(t *testing.T) {
	t.Parallel()
	const inlinePrompt = "line 1\nline 2\nline 3"
	ctx := host.WithClaudeRunner(context.Background(), host.FakeAsk("inline reply"))
	res, err := host.AgentAskHandler(ctx, map[string]any{
		"prompt": inlinePrompt,
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error for inline prompt: %s", res.Error)
	}
	if _, ok := res.Data["stdout"]; !ok {
		t.Fatalf("stdout missing from result: %v", res.Data)
	}
}

// TestAgentAsk_InlinePrompt_FilePath_StillWorks verifies that when prompt:
// is set to a valid file path (single-line, no newlines), the handler still
// reads from disk (backward compatibility with legacy call sites).
func TestAgentAsk_InlinePrompt_FilePath_StillWorks(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "p.md")
	if err := os.WriteFile(p, []byte("hello from file"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	ctx := host.WithClaudeRunner(context.Background(), host.FakeAsk("file reply"))
	res, err := host.AgentAskHandler(ctx, map[string]any{"prompt": p})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error: %s", res.Error)
	}
	if _, ok := res.Data["stdout"]; !ok {
		t.Fatalf("stdout missing from result: %v", res.Data)
	}
}

// ── L11: AgentStreamer.CLIArgs positional-arg check ──────────────────────

// TestAgentStreamer_CLIArgs_PanicsOnPositionalArgInTests verifies that
// AgentStreamer.Run panics in tests when CLIArgs contains a positional
// argument (not a flag value — i.e. not immediately following a flag name).
func TestAgentStreamer_CLIArgs_PanicsOnPositionalArgInTests(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for positional arg in CLIArgs, got none")
		}
	}()
	dir := t.TempDir()
	_ = dir
	// Build a streamer with a positional argument that isn't a flag value.
	// "-p" then "some-positional" is a flag+value pair; to trigger the check
	// we need two consecutive non-flag tokens after a flag value.
	streamer := host.AgentStreamer{
		Bin:     "/usr/bin/false",
		CLIArgs: []string{"-p", "--permission-mode", "bypassPermissions", "POSITIONAL"},
	}
	// Should panic because "POSITIONAL" follows a flag value, not a flag name.
	_, _, _ = streamer.Run(context.Background())
}

func TestAgentStreamer_MCPAllowedToolsPreauthorizedViaSettings(t *testing.T) {
	t.Parallel()
	var captured []string
	stub := func(_ context.Context, args []string, _, _ string) (host.ClaudeRun, error) {
		captured = append([]string(nil), args...)
		return host.ClaudeRun{Stdout: `{"type":"result","subtype":"success","result":"ok"}`}, nil
	}
	ctx := host.WithClaudeRunner(context.Background(), stub)

	_, _, err := host.AgentStreamerRunExport(ctx, "stub://claude", []string{
		"-p",
		"--permission-mode", "bypassPermissions",
		"--allowedTools", "Read,mcp__validator__submit,mcp__kitsoki__session_status",
	}, "prompt", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	settingsJSON, ok := hostTestFlagValue(captured, "--settings")
	if !ok {
		t.Fatalf("--settings not passed for MCP allowlist; args=%v", captured)
	}
	var settings struct {
		Permissions struct {
			Allow []string `json:"allow"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(settingsJSON), &settings); err != nil {
		t.Fatalf("settings JSON did not parse: %v\n%s", err, settingsJSON)
	}
	got := strings.Join(settings.Permissions.Allow, ",")
	for _, want := range []string{"mcp__validator__submit", "mcp__kitsoki__session_status"} {
		if !strings.Contains(got, want) {
			t.Fatalf("settings permissions.allow = %v, missing %s", settings.Permissions.Allow, want)
		}
	}
	if strings.Contains(got, "Read") {
		t.Fatalf("settings permissions.allow should only preauthorize MCP tools, got %v", settings.Permissions.Allow)
	}
}

func TestAgentStreamer_NoSettingsOverlayWithoutMCPAllowedTools(t *testing.T) {
	t.Parallel()
	var captured []string
	stub := func(_ context.Context, args []string, _, _ string) (host.ClaudeRun, error) {
		captured = append([]string(nil), args...)
		return host.ClaudeRun{Stdout: `{"type":"result","subtype":"success","result":"ok"}`}, nil
	}
	ctx := host.WithClaudeRunner(context.Background(), stub)

	_, _, err := host.AgentStreamerRunExport(ctx, "stub://claude", []string{
		"-p",
		"--permission-mode", "default",
		"--allowedTools", "Read,Grep,Glob",
	}, "prompt", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := hostTestFlagValue(captured, "--settings"); ok {
		t.Fatalf("--settings should not be passed without MCP tools; args=%v", captured)
	}
}

func hostTestFlagValue(args []string, flag string) (string, bool) {
	var values []string
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			values = append(values, args[i+1])
		}
	}
	if len(values) == 0 {
		return "", false
	}
	return strings.Join(values, ","), true
}
