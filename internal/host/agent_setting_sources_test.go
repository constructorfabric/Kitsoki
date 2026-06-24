package host_test

// Hermetic agent isolation: every claude-CLI invocation a story makes must
// pin --setting-sources to a set that EXCLUDES the operator's user-global
// configuration and disable slash commands/skills, so a globally-enabled or
// project-local Claude Code plugin/skill can never hijack a story's agent.
//
// Regression of record: with BMAD-METHOD enabled in ~/.claude/settings.json
// (enabledPlugins), the prd story's `interviewer` converse call stopped
// following its --append-system-prompt and instead role-played BMAD's "John"
// PM persona — announcing a deprecation notice, choosing its own output path,
// and presenting its own pick-one menu (the operator saw a "selection input"
// where the room renders none). Dropping the "user" source isolates the agent.
//
// These tests capture the exact argv handed to the claude runner and assert
// the flag pair is present on each construction path (ask/decide/task via
// buildBaseCLIArgs, the non-chat converse path, and the chat-aware converse
// path — the one the interviewer actually uses).

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"kitsoki/internal/host"
)

// capturingRunner records the argv of the most recent claude invocation into
// *captured and returns a benign reply so the handler completes.
func capturingRunner(captured *[]string) host.ClaudeRunner {
	return func(_ context.Context, args []string, _, _ string) (host.ClaudeRun, error) {
		*captured = append([]string(nil), args...)
		return host.ClaudeRun{Stdout: "ANSWER ok"}, nil
	}
}

// hasFlagPair reports whether args contains flag immediately followed by value.
func hasFlagPair(args []string, flag, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}

func assertHermeticSettingSources(t *testing.T, path string, args []string) {
	t.Helper()
	if len(args) == 0 {
		t.Fatalf("%s: runner was never invoked (no argv captured)", path)
	}
	if !hasFlagPair(args, "--setting-sources", "project,local") {
		t.Errorf("%s: claude argv missing hermetic isolation flag "+
			"`--setting-sources project,local`; a user-global plugin could "+
			"hijack the agent. argv: %v", path, args)
	}
}

// assertStrictMCPConfig asserts the agent argv carries --strict-mcp-config, so the
// subprocess uses ONLY the MCP servers kitsoki attaches via --mcp-config and ignores
// the working_dir's project .mcp.json. Without it, a failing project MCP server in a
// maker worktree silently drops the validator (`submit`) tool — the P0 that discarded
// correct maker output. See appendStrictMCPConfigFlag in agents.go.
func assertStrictMCPConfig(t *testing.T, path string, args []string) {
	t.Helper()
	if len(args) == 0 {
		t.Fatalf("%s: runner was never invoked (no argv captured)", path)
	}
	for _, a := range args {
		if a == "--strict-mcp-config" {
			return
		}
	}
	t.Errorf("%s: claude argv missing `--strict-mcp-config`; the working_dir's "+
		"project .mcp.json could interfere and drop the validator `submit` tool. "+
		"argv: %v", path, args)
}

func assertSkillsDisabled(t *testing.T, path string, args []string) {
	t.Helper()
	if len(args) == 0 {
		t.Fatalf("%s: runner was never invoked (no argv captured)", path)
	}
	for _, a := range args {
		if a == "--disable-slash-commands" {
			return
		}
	}
	t.Errorf("%s: claude argv missing `--disable-slash-commands`; a story "+
		"agent could stop to discover or invoke skills instead of following "+
		"the story prompt. argv: %v", path, args)
}

func TestAgentSettingSources_Ask(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	promptFile := filepath.Join(dir, "p.md")
	if err := os.WriteFile(promptFile, []byte("inspect"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	var captured []string
	ctx := host.WithClaudeRunner(context.Background(), capturingRunner(&captured))

	if _, err := host.AgentAskHandler(ctx, map[string]any{
		"prompt_path": promptFile,
	}); err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	assertHermeticSettingSources(t, "ask (buildBaseCLIArgs)", captured)
	assertSkillsDisabled(t, "ask (buildBaseCLIArgs)", captured)
	assertStrictMCPConfig(t, "ask (buildBaseCLIArgs)", captured)
}

func TestAgentSettingSources_ConverseNoChat(t *testing.T) {
	t.Parallel()
	var captured []string
	ctx := host.WithClaudeRunner(context.Background(), capturingRunner(&captured))

	if _, err := host.AgentConverseHandler(ctx, map[string]any{
		"question": "ping",
	}); err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	assertHermeticSettingSources(t, "converse (non-chat path)", captured)
	assertSkillsDisabled(t, "converse (non-chat path)", captured)
	assertStrictMCPConfig(t, "converse (non-chat path)", captured)
}

func TestAgentSettingSources_ConverseChat(t *testing.T) {
	t.Parallel()
	cs := newFakeChatStore()
	cs.addChat(host.ChatRecord{ID: "chat-1", Status: "active"})

	var captured []string
	ctx := host.WithClaudeRunner(
		host.WithChatStore(context.Background(), cs),
		capturingRunner(&captured),
	)

	// The chat-aware path is exactly what the prd story's interviewer uses
	// (host.chat.resolve → converse with chat_id) — the call that was hijacked.
	if _, err := host.AgentConverseHandler(ctx, map[string]any{
		"question": "I want a notes app PRD",
		"chat_id":  "chat-1",
	}); err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	assertHermeticSettingSources(t, "converse (chat path — interviewer)", captured)
	assertSkillsDisabled(t, "converse (chat path — interviewer)", captured)
	assertStrictMCPConfig(t, "converse (chat path — interviewer)", captured)
}
