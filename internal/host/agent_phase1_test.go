package host_test

// Agent verb-ladder tests — Tools/--allowedTools plumbing (D5 precedence),
// BashProfile threading, stub handler registration, and FakeXxx helpers.
//
// These tests do NOT fork the claude binary. They use the ClaudeRunner stub
// seam (WithClaudeRunner) so every test runs in milliseconds.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kitsoki/internal/host"
)

// ── Tools / --allowedTools forwarding (D5) ────────────────────────────────

// TestAgentAsk_AgentTools_ForwardedAsAllowedTools verifies that when an agent
// declares Tools, the handler appends --allowedTools to the claude invocation.
func TestAgentAsk_AgentTools_ForwardedAsAllowedTools(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "p.md")
	if err := os.WriteFile(promptPath, []byte("inspect"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	ctx := host.WithClaudeRunner(
		host.WithAgents(context.Background(), map[string]host.Agent{
			"inspector": {
				SystemPrompt: "inspect mode",
				Tools:        []string{"host.Read", "host.Grep"},
			},
		}),
		host.FakeAskWithMeta("inspection result"),
	)

	res, err := host.AgentAskHandler(ctx, map[string]any{
		"prompt_path": promptPath,
		"agent":       "inspector",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error: %s", res.Error)
	}
	stdout, _ := res.Data["stdout"].(string)
	if !strings.Contains(stdout, "tools=[host.Read,host.Grep]") {
		t.Fatalf("--allowedTools not forwarded; stdout=%q", stdout)
	}
}

func TestAgentAsk_ContractMCPAndPermissionsForwarded(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "p.md")
	if err := os.WriteFile(promptPath, []byte("inspect"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	var captured []string
	runner := func(_ context.Context, args []string, _, _ string) (host.ClaudeRun, error) {
		captured = append([]string(nil), args...)
		return host.ClaudeRun{Stdout: "ok"}, nil
	}
	ctx := host.WithClaudeRunner(
		host.WithAgents(context.Background(), map[string]host.Agent{
			"inspector": {
				SystemPrompt: "inspect mode",
				Tools:        []string{"host.Read"},
				MCPTools:     []string{"mcp__fs__read_file"},
				MCPServers: map[string]any{
					"fs": map[string]any{"command": "node", "args": []any{"server.js"}},
				},
				Permissions: host.AgentPermissions{
					Mode:            "ask",
					DisallowedTools: []string{"WebFetch"},
				},
			},
		}),
		runner,
	)

	res, err := host.AgentAskHandler(ctx, map[string]any{
		"prompt_path": promptPath,
		"agent":       "inspector",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error: %s", res.Error)
	}
	joined := strings.Join(captured, " ")
	if !strings.Contains(joined, "--permission-mode default") {
		t.Fatalf("permissions.mode ask must map to CLI default; args=%v", captured)
	}
	if !strings.Contains(joined, "mcp__fs__read_file") {
		t.Fatalf("MCP tool was not forwarded in --allowedTools; args=%v", captured)
	}
	if !strings.Contains(joined, "--mcp-config") {
		t.Fatalf("MCP server config was not attached; args=%v", captured)
	}
	if !strings.Contains(joined, "WebFetch") {
		t.Fatalf("permissions.disallowed_tools was not forwarded; args=%v", captured)
	}
}

// TestAgentAsk_PerCallTools_WinsOverAgentTools verifies D5: when the effect
// sets `tools:` and the agent also declares Tools, the per-call list wins.
func TestAgentAsk_PerCallTools_WinsOverAgentTools(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "p.md")
	if err := os.WriteFile(promptPath, []byte("inspect"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	ctx := host.WithClaudeRunner(
		host.WithAgents(context.Background(), map[string]host.Agent{
			"inspector": {
				SystemPrompt: "inspect mode",
				Tools:        []string{"host.Read"},
			},
		}),
		host.FakeAskWithMeta("result"),
	)

	// Per-call tools list overrides agent.Tools.
	res, err := host.AgentAskHandler(ctx, map[string]any{
		"prompt_path": promptPath,
		"agent":       "inspector",
		"tools":       []any{"host.Grep", "host.Glob"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error: %s", res.Error)
	}
	stdout, _ := res.Data["stdout"].(string)
	if !strings.Contains(stdout, "tools=[host.Grep,host.Glob]") {
		t.Fatalf("per-call tools did not win over agent.Tools; stdout=%q", stdout)
	}
	if strings.Contains(stdout, "host.Read") {
		t.Fatalf("agent.Tools leaked through despite per-call override; stdout=%q", stdout)
	}
}

// TestAgentAsk_AgentDefaultCwd verifies that when the agent declares
// DefaultCwd and the effect omits working_dir, the agent cwd is used.
func TestAgentAsk_AgentDefaultCwd(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "p.md")
	if err := os.WriteFile(promptPath, []byte("hi"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	agentCwd := t.TempDir()

	var capturedCwd string
	runner := func(_ context.Context, _ []string, _, workingDir string) (host.ClaudeRun, error) {
		capturedCwd = workingDir
		return host.ClaudeRun{Stdout: "ok"}, nil
	}

	ctx := host.WithClaudeRunner(
		host.WithAgents(context.Background(), map[string]host.Agent{
			"cwd-agent": {SystemPrompt: "sp", DefaultCwd: agentCwd},
		}),
		runner,
	)

	_, err := host.AgentAskHandler(ctx, map[string]any{
		"prompt_path": promptPath,
		"agent":       "cwd-agent",
		// No working_dir — agent.DefaultCwd should apply.
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedCwd != agentCwd {
		t.Fatalf("expected capturedCwd=%q, got %q", agentCwd, capturedCwd)
	}
}

// TestAgentAsk_PerCallCwd_WinsOverAgentCwd verifies that an explicit
// working_dir arg wins over agent.DefaultCwd.
func TestAgentAsk_PerCallCwd_WinsOverAgentCwd(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "p.md")
	if err := os.WriteFile(promptPath, []byte("hi"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	agentCwd := t.TempDir()
	perCallCwd := t.TempDir()

	var capturedCwd string
	runner := func(_ context.Context, _ []string, _, workingDir string) (host.ClaudeRun, error) {
		capturedCwd = workingDir
		return host.ClaudeRun{Stdout: "ok"}, nil
	}

	ctx := host.WithClaudeRunner(
		host.WithAgents(context.Background(), map[string]host.Agent{
			"cwd-agent": {SystemPrompt: "sp", DefaultCwd: agentCwd},
		}),
		runner,
	)

	_, err := host.AgentAskHandler(ctx, map[string]any{
		"prompt_path": promptPath,
		"agent":       "cwd-agent",
		"working_dir": perCallCwd,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedCwd != perCallCwd {
		t.Fatalf("expected per-call cwd %q, got %q", perCallCwd, capturedCwd)
	}
}

// ── Stub handler registration ──────────────────────────────────────────────

// After Phases 2/3/4/5/7, no agent.* handler is a stub. The dedicated
// per-handler tests live in their own files (agent_decide_test.go,
// agent_ask_test.go, agent_task_test.go, agent_extract_test.go,
// agent_converse_test.go). The two tests below cover the argument-validation
// shapes that distinguish a live handler from the old stub envelope.

// TestAgentTaskHandler_MissingAgent verifies that the Phase 4 task handler
// rejects calls with no agent: argument.
func TestAgentTaskHandler_MissingAgent(t *testing.T) {
	t.Parallel()
	res, err := host.AgentTaskHandler(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !strings.Contains(res.Error, "agent:") {
		t.Fatalf("expected agent: error, got: %q", res.Error)
	}
}

// TestAgentExtractHandler_Phase5_Implemented verifies that the extract handler
// is live (Phase 5) and returns a schema-required error on nil args instead of
// the old "not yet implemented" stub response.
func TestAgentExtractHandler_Phase5_Implemented(t *testing.T) {
	t.Parallel()
	res, err := host.AgentExtractHandler(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if strings.Contains(res.Error, "not yet implemented") {
		t.Fatal("extract handler still returns stub — Phase 5 implementation not wired")
	}
	if !strings.Contains(res.Error, "schema argument is required") {
		t.Errorf("expected schema-required error from Phase 5 handler, got %q", res.Error)
	}
}

// ── FakeXxx helpers ───────────────────────────────────────────────────────

// TestFakeRunners_ReturnScriptedResult verifies that each FakeXxx factory
// produces a ClaudeRunner that returns the scripted text.
func TestFakeRunners_ReturnScriptedResult(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		runner host.ClaudeRunner
		want   string
	}{
		{"FakeExtract", host.FakeExtract("extracted"), "extracted"},
		{"FakeDecide", host.FakeDecide("verdict"), "verdict"},
		{"FakeAsk", host.FakeAsk("answer"), "answer"},
		{"FakeTask", host.FakeTask("done"), "done"},
		{"FakeConverse", host.FakeConverse("chat reply"), "chat reply"},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			run, err := tc.runner(context.Background(), nil, "", "")
			if err != nil {
				t.Fatalf("%s: unexpected error: %v", tc.name, err)
			}
			if run.Stdout != tc.want {
				t.Fatalf("%s: want %q, got %q", tc.name, tc.want, run.Stdout)
			}
		})
	}
}

// TestFakeDecideWithMeta_ReturnsMetadata verifies that FakeDecideWithMeta
// embeds flag metadata in the reply so tests can assert forwarding.
func TestFakeDecideWithMeta_ReturnsMetadata(t *testing.T) {
	t.Parallel()
	runner := host.FakeDecideWithMeta("verdict")
	args := []string{
		"--append-system-prompt", "my-system-prompt",
		"--model", "claude-haiku-4-5",
		"--allowedTools", "host.Read,host.Grep",
	}
	run, err := runner(context.Background(), args, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result, sp, model, tools := host.ParseFakeMetaReply(run.Stdout)
	if result != "verdict" {
		t.Fatalf("result: want %q, got %q", "verdict", result)
	}
	if sp != "my-system-prompt" {
		t.Fatalf("system_prompt: want %q, got %q", "my-system-prompt", sp)
	}
	if model != "claude-haiku-4-5" {
		t.Fatalf("model: want %q, got %q", "claude-haiku-4-5", model)
	}
	if tools != "host.Read,host.Grep" {
		t.Fatalf("tools: want %q, got %q", "host.Read,host.Grep", tools)
	}
}

// ── BashProfile enforcement helpers ───────────────────────────────────────

// TestApplyBashProfile_ReadOnly verifies that only allowlisted commands pass.
func TestApplyBashProfile_ReadOnly(t *testing.T) {
	t.Parallel()
	profile := &host.BashProfile{Kind: host.BashProfileReadOnly}

	// Allowed commands pass.
	for _, cmd := range []string{"grep foo bar.go", "cat README.md", "git log --oneline", "ls -la"} {
		if msg := host.ApplyBashProfile(profile, cmd); msg != "" {
			t.Errorf("read-only profile denied allowed command %q: %s", cmd, msg)
		}
	}

	// Non-allowlisted command is denied.
	if msg := host.ApplyBashProfile(profile, "rm -rf /tmp/foo"); msg == "" {
		t.Error("read-only profile should deny rm")
	}

	// Shell metacharacters are denied regardless of argv0.
	if msg := host.ApplyBashProfile(profile, "grep foo; rm -rf /"); msg == "" {
		t.Error("read-only profile should deny metachar chain")
	}
}

// TestApplyBashProfile_Commands verifies that only listed argv0s pass.
func TestApplyBashProfile_Commands(t *testing.T) {
	t.Parallel()
	profile := &host.BashProfile{
		Kind:     host.BashProfileCommands,
		Commands: []string{"git", "jq"},
	}

	if msg := host.ApplyBashProfile(profile, "git log --oneline"); msg != "" {
		t.Errorf("commands profile denied allowed command: %s", msg)
	}
	if msg := host.ApplyBashProfile(profile, "jq . data.json"); msg != "" {
		t.Errorf("commands profile denied allowed command: %s", msg)
	}
	if msg := host.ApplyBashProfile(profile, "cat README.md"); msg == "" {
		t.Error("commands profile should deny cat (not in list)")
	}
}

// TestApplyBashProfile_SandboxWrite verifies that any command passes the
// sandboxed-write profile (network denial is env-based, not command-based).
func TestApplyBashProfile_SandboxWrite(t *testing.T) {
	t.Parallel()
	profile := &host.BashProfile{Kind: host.BashProfileSandboxWrite}
	if msg := host.ApplyBashProfile(profile, "make build"); msg != "" {
		t.Errorf("sandboxed-write profile should allow any command; got %q", msg)
	}
}

// TestApplyBashProfile_Nil_AllowsAll verifies that a nil profile is a no-op
// (the handler decides based on context, not the profile wrapper).
func TestApplyBashProfile_Nil_AllowsAll(t *testing.T) {
	t.Parallel()
	if msg := host.ApplyBashProfile(nil, "rm -rf /"); msg != "" {
		t.Errorf("nil profile should return empty string (caller decides); got %q", msg)
	}
}
