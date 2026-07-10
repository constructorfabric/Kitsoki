package host

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustUnmarshal(t *testing.T, line string) map[string]any {
	t.Helper()
	var ev map[string]any
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		t.Fatalf("unmarshal %q: %v", line, err)
	}
	return ev
}

// The conformance suite proves both agent backends honour the same interface
// contract: argv translation, JSONL/stream parsing of REAL captured wire
// fixtures, usage normalization, and stub-runner round-trips. It is the
// interface-compliance gate for adding a backend — a new backend is "at parity"
// exactly when it passes every case here against its own fixtures.
//
// No real binary or LLM is forked: each case installs the backend's stub runner
// (WithClaudeRunner / WithCopilotRunner) returning a fixture verbatim.

func readFixture(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", rel))
	if err != nil {
		t.Fatalf("read fixture %s: %v", rel, err)
	}
	return string(b)
}

// allBackends is the registry the suite iterates. Adding a backend here (plus
// its fixtures) is all it takes to bring it under the compliance gate.
var allBackends = []struct {
	name    string
	backend agentBackend
}{
	{"claude", claudeBackend{}},
	{"copilot", copilotBackend{}},
	{"codex", codexBackend{}},
	{"agy", agyBackend{}},
}

// TestConformance_DefaultBackendIsClaude pins the load-bearing default: a
// context with no backend installed MUST resolve to claude so every pre-existing
// call site and test stays on the byte-identical path.
func TestConformance_DefaultBackendIsClaude(t *testing.T) {
	if got := AgentBackendFromContext(context.Background()).Name(); got != "claude" {
		t.Fatalf("default backend = %q, want claude", got)
	}
}

// TestConformance_StreamParse runs each backend's captured fixture through the
// shared stream parser (with the backend installed) and asserts the normalized
// outcome: final reply text, session id, and a backend-appropriate usage key.
func TestConformance_StreamParse(t *testing.T) {
	cases := []struct {
		name          string
		backend       agentBackend
		fixture       string
		wantReply     string
		wantSessionID string
		wantUsageKey  string // a key the normalized usage map must contain
	}{
		{
			name:          "claude/simple",
			backend:       claudeBackend{},
			fixture:       "claude/ask_simple.jsonl",
			wantReply:     "pong",
			wantSessionID: "sess-claude-1",
			wantUsageKey:  "output_tokens",
		},
		{
			name:          "copilot/simple",
			backend:       copilotBackend{},
			fixture:       "copilot/ask_simple.jsonl",
			wantReply:     "pong",
			wantSessionID: "e90e9dad-50a2-45d1-bb72-7687319a8163",
			wantUsageKey:  "premium_requests",
		},
		{
			name:          "copilot/tool_round",
			backend:       copilotBackend{},
			fixture:       "copilot/with_tool_round.jsonl",
			wantReply:     "`kitsoki-tool-probe`",
			wantSessionID: "92780eda-3bb4-4fe7-84c8-4963a6ae0ef3",
			wantUsageKey:  "premium_requests",
		},
		{
			name:          "codex/simple",
			backend:       codexBackend{},
			fixture:       "codex/ask_simple.jsonl",
			wantReply:     `{"answer":"hello"}`,
			wantSessionID: "00000000-0000-0000-0000-000000000000",
			wantUsageKey:  "output_tokens",
		},
		{
			name:          "agy/simple",
			backend:       agyBackend{},
			fixture:       "agy/ask_simple.jsonl",
			wantReply:     "pong",
			wantSessionID: "42e07c25-6fef-4771-aa40-1f263352723b",
			wantUsageKey:  "output_tokens",
		},
		{
			name:          "codex/tool_round",
			backend:       codexBackend{},
			fixture:       "codex/with_tool_round.jsonl",
			wantReply:     "It printed:\n\n```text\nhi\n```",
			wantSessionID: "00000000-0000-0000-0000-000000000000",
			wantUsageKey:  "input_tokens",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := WithAgentBackend(context.Background(), c.backend)
			raw := readFixture(t, c.fixture)
			reply, sid, rawEvents, usage, _ := parseStreamJSONOutput(ctx, raw)

			if got := strings.TrimSpace(reply); got != c.wantReply {
				t.Errorf("reply = %q, want %q", got, c.wantReply)
			}
			if sid != c.wantSessionID {
				t.Errorf("sessionID = %q, want %q", sid, c.wantSessionID)
			}
			if len(rawEvents) == 0 {
				t.Error("expected at least one parsed raw event")
			}
			if usage == nil {
				t.Fatalf("expected non-nil usage map")
			}
			if _, ok := usage[c.wantUsageKey]; !ok {
				t.Errorf("usage missing key %q; got keys %v", c.wantUsageKey, keysOf(usage))
			}
		})
	}
}

// TestConformance_ToolEventsClassified asserts each backend surfaces tool calls
// in the classifiedEvent stream so the Agent-actions transcript renders them.
func TestConformance_ToolEventsClassified(t *testing.T) {
	t.Run("claude", func(t *testing.T) {
		ev := mustUnmarshal(t, `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"prompt.md"}}]}}`)
		ce := claudeBackend{}.Classify(ev)
		if ce.Tool != "Read" || ce.ToolArgs != "prompt.md" {
			t.Errorf("claude tool classify = %q/%q, want Read/prompt.md", ce.Tool, ce.ToolArgs)
		}
	})
	t.Run("copilot/execution_start", func(t *testing.T) {
		ev := mustUnmarshal(t, `{"type":"tool.execution_start","data":{"toolName":"bash","arguments":{"command":"echo hi"}}}`)
		ce := copilotBackend{}.Classify(ev)
		if ce.Tool != "bash" || ce.ToolArgs != "echo hi" {
			t.Errorf("copilot tool classify = %q/%q, want bash/echo hi", ce.Tool, ce.ToolArgs)
		}
	})
	t.Run("copilot/message_toolRequests", func(t *testing.T) {
		ev := mustUnmarshal(t, `{"type":"assistant.message","data":{"content":"","toolRequests":[{"name":"bash","arguments":{"command":"ls"}}]}}`)
		ce := copilotBackend{}.Classify(ev)
		if len(ce.Tools) != 1 || ce.Tools[0].Name != "bash" {
			t.Errorf("copilot message toolRequests = %+v, want one bash tool", ce.Tools)
		}
	})
	t.Run("codex/command_execution", func(t *testing.T) {
		ev := mustUnmarshal(t, `{"type":"item.completed","item":{"type":"command_execution","command":"/bin/zsh -lc 'echo hi'","exit_code":0,"status":"completed"}}`)
		ce := codexBackend{}.Classify(ev)
		if ce.Tool != "shell" || ce.ToolArgs != "/bin/zsh -lc 'echo hi'" {
			t.Errorf("codex command classify = %q/%q, want shell/<command>", ce.Tool, ce.ToolArgs)
		}
		if len(ce.Tools) != 1 || ce.Tools[0].Name != "shell" {
			t.Errorf("codex command Tools = %+v, want one shell tool", ce.Tools)
		}
	})
	t.Run("codex/command_execution_started", func(t *testing.T) {
		ev := mustUnmarshal(t, `{"type":"item.started","item":{"id":"item_1","type":"command_execution","command":"/bin/zsh -lc 'echo hi'","aggregated_output":"","exit_code":null,"status":"in_progress"}}`)
		ce := codexBackend{}.Classify(ev)
		if ce.Type != "assistant" {
			t.Errorf("codex command started type = %q, want assistant", ce.Type)
		}
		if ce.Tool != "shell" || ce.ToolArgs != "/bin/zsh -lc 'echo hi'" {
			t.Errorf("codex command started classify = %q/%q, want shell/<command>", ce.Tool, ce.ToolArgs)
		}
	})
	t.Run("codex/mcp_tool_call", func(t *testing.T) {
		ev := mustUnmarshal(t, `{"type":"item.completed","item":{"type":"mcp_tool_call","tool":"kitsoki-validator__submit","arguments":{"answer":"hello"}}}`)
		ce := codexBackend{}.Classify(ev)
		if ce.Tool != "kitsoki-validator__submit" {
			t.Errorf("codex mcp_tool_call Tool = %q, want kitsoki-validator__submit", ce.Tool)
		}
		if len(ce.Tools) != 1 || ce.Tools[0].Name != "kitsoki-validator__submit" {
			t.Errorf("codex mcp_tool_call Tools = %+v, want one submit tool", ce.Tools)
		}
	})
	t.Run("codex/mcp_tool_call_started", func(t *testing.T) {
		ev := mustUnmarshal(t, `{"type":"item.started","item":{"id":"item_1","type":"mcp_tool_call","tool":"kitsoki-validator__submit","arguments":{"answer":"hello"}}}`)
		ce := codexBackend{}.Classify(ev)
		if ce.Type != "assistant" {
			t.Errorf("codex mcp_tool_call started type = %q, want assistant", ce.Type)
		}
		if ce.Tool != "kitsoki-validator__submit" {
			t.Errorf("codex mcp_tool_call started Tool = %q, want kitsoki-validator__submit", ce.Tool)
		}
		if len(ce.Tools) != 1 || ce.Tools[0].Name != "kitsoki-validator__submit" {
			t.Errorf("codex mcp_tool_call started Tools = %+v, want one submit tool", ce.Tools)
		}
	})
}

func TestConformance_ReasoningEventsUseThinkingChannel(t *testing.T) {
	t.Run("codex/text", func(t *testing.T) {
		ev := mustUnmarshal(t, `{"type":"item.completed","item":{"type":"reasoning","text":"I will inspect the file first."}}`)
		ce := codexBackend{}.Classify(ev)
		if ce.Type != "assistant" {
			t.Fatalf("codex reasoning type = %q, want assistant", ce.Type)
		}
		if ce.Thinking != "I will inspect the file first." {
			t.Fatalf("codex reasoning Thinking = %q", ce.Thinking)
		}
		if ce.Text != "" {
			t.Fatalf("codex reasoning polluted Text = %q", ce.Text)
		}
	})
	t.Run("codex/summary_array", func(t *testing.T) {
		ev := mustUnmarshal(t, `{"type":"item.completed","item":{"type":"reasoning","summary":[{"type":"summary_text","text":"First."},{"type":"summary_text","text":"Second."}]}}`)
		ce := codexBackend{}.Classify(ev)
		if ce.Thinking != "First.\nSecond." {
			t.Fatalf("codex reasoning summary Thinking = %q", ce.Thinking)
		}
	})
	t.Run("copilot/reasoning", func(t *testing.T) {
		ev := mustUnmarshal(t, `{"type":"assistant.reasoning","data":{"content":"I need to run a specific command."}}`)
		ce := copilotBackend{}.Classify(ev)
		if ce.Thinking != "I need to run a specific command." {
			t.Fatalf("copilot reasoning Thinking = %q", ce.Thinking)
		}
		if ce.Text != "" {
			t.Fatalf("copilot reasoning polluted Text = %q", ce.Text)
		}
	})
	t.Run("copilot/message_reasoningText", func(t *testing.T) {
		ev := mustUnmarshal(t, `{"type":"assistant.message","data":{"content":"","reasoningText":"I need to run a specific command.","toolRequests":[{"name":"bash","arguments":{"command":"echo hi"}}]}}`)
		ce := copilotBackend{}.Classify(ev)
		if ce.Thinking != "I need to run a specific command." {
			t.Fatalf("copilot message reasoningText Thinking = %q", ce.Thinking)
		}
		if ce.Text != "" {
			t.Fatalf("copilot message reasoningText polluted Text = %q", ce.Text)
		}
		if ce.Tool != "bash" {
			t.Fatalf("copilot message reasoningText lost tool = %q", ce.Tool)
		}
	})
}

// TestConformance_ArgvTranslation asserts each backend maps a representative
// claude-shaped invocation onto its own CLI surface.
func TestConformance_ArgvTranslation(t *testing.T) {
	// What every verb handler builds today: base flags + system prompt + model
	// + mcp-config + the stream-json output flags AgentStreamer appends.
	claudeArgs := []string{
		"-p",
		"--permission-mode", "bypassPermissions",
		"--setting-sources", "project,local",
		"--disable-slash-commands",
		"--system-prompt", "SYS-PROMPT",
		"--model", "some-model",
		"--effort", "low",
		"--mcp-config", "/tmp/cfg.json",
		"--output-format", "stream-json", "--verbose",
	}
	const stdin = "USER PROMPT"
	const wd = "/work/dir"

	t.Run("claude/identity", func(t *testing.T) {
		inv := claudeBackend{}.TranslateInvocation(claudeArgs, stdin, wd)
		if !equalArgs(inv.Args, claudeArgs) {
			t.Errorf("claude translation not identity:\n got %v\nwant %v", inv.Args, claudeArgs)
		}
		if inv.Stdin != stdin || inv.WorkingDir != wd {
			t.Errorf("claude stdin/wd = %q/%q, want %q/%q", inv.Stdin, inv.WorkingDir, stdin, wd)
		}
	})

	t.Run("claude/resume_strip", func(t *testing.T) {
		resumeArgs := append(append([]string(nil), claudeArgs...), "--resume", "uuid-123")
		inv := claudeBackend{}.TranslateInvocation(resumeArgs, stdin, wd)
		for _, arg := range inv.Args {
			if arg == "--system-prompt" || arg == "SYS-PROMPT" || arg == "--exclude-dynamic-system-prompt-sections" {
				t.Errorf("claude resume invocation must not contain system prompt flags or values; got %v", inv.Args)
			}
		}
		if !hasFlagValue(inv.Args, "--resume", "uuid-123") {
			t.Errorf("claude resume invocation missing --resume flag/value; got %v", inv.Args)
		}
	})

	t.Run("copilot/rewrite", func(t *testing.T) {
		inv := copilotBackend{}.TranslateInvocation(claudeArgs, stdin, wd)
		got := strings.Join(inv.Args, " ")

		// Prompt is a -p arg with the system prompt prepended; stdin is empty.
		if inv.Stdin != "" {
			t.Errorf("copilot stdin = %q, want empty (prompt is an arg)", inv.Stdin)
		}
		if !hasFlagValue(inv.Args, "-p", "SYS-PROMPT\n\n---\n\nUSER PROMPT") {
			t.Errorf("copilot -p arg missing prepended system prompt; args=%v", inv.Args)
		}
		// Flag rewrites.
		mustContain(t, got, "--allow-all-tools")
		mustContain(t, got, "--output-format json")
		if !hasFlagValue(inv.Args, "--model", "some-model") {
			t.Errorf("copilot missing --model some-model; args=%v", inv.Args)
		}
		if !hasFlagValue(inv.Args, "--additional-mcp-config", "@/tmp/cfg.json") {
			t.Errorf("copilot missing --additional-mcp-config @/tmp/cfg.json; args=%v", inv.Args)
		}
		if !hasFlagValue(inv.Args, "-C", wd) {
			t.Errorf("copilot missing -C %s; args=%v", wd, inv.Args)
		}
		// A claude model id must be dropped (copilot uses its own model).
		cb := copilotBackend{}
		mi := cb.TranslateInvocation([]string{"-p", "--model", "claude-haiku-4-5-20251001"}, "p", "")
		if strings.Contains(strings.Join(mi.Args, " "), "--model") {
			t.Errorf("copilot forwarded a claude model id; args=%v", mi.Args)
		}
		// A genuine copilot model id IS forwarded.
		ci := cb.TranslateInvocation([]string{"-p", "--model", "gpt-5"}, "p", "")
		if !hasFlagValue(ci.Args, "--model", "gpt-5") {
			t.Errorf("copilot dropped a non-claude model; args=%v", ci.Args)
		}

		// Claude-only flags must be gone.
		for _, dropped := range []string{"--permission-mode", "--setting-sources", "--disable-slash-commands", "--effort", "--verbose", "--append-system-prompt", "--mcp-config ", "stream-json"} {
			if strings.Contains(got, dropped) {
				t.Errorf("copilot args still contain dropped flag %q: %v", dropped, inv.Args)
			}
		}
	})

	// Session resume: --session-id forwards verbatim (set-the-uuid, first call);
	// --resume forwards in copilot's optional-value `=` form (re-engage rounds).
	t.Run("copilot/session_resume", func(t *testing.T) {
		cb := copilotBackend{}
		first := cb.TranslateInvocation([]string{"-p", "--session-id", "uuid-123"}, "p", "")
		if !hasFlagValue(first.Args, "--session-id", "uuid-123") {
			t.Errorf("copilot dropped --session-id; args=%v", first.Args)
		}
		resume := cb.TranslateInvocation([]string{"-p", "--resume", "uuid-123"}, "p", "")
		if !strings.Contains(strings.Join(resume.Args, " "), "--resume=uuid-123") {
			t.Errorf("copilot --resume not in =value form; args=%v", resume.Args)
		}
	})

	t.Run("codex/rewrite", func(t *testing.T) {
		codexHome := t.TempDir()
		t.Setenv("CODEX_HOME", codexHome)
		t.Setenv("HOME", t.TempDir())
		mustWrite(t, filepath.Join(codexHome, "config.toml"), `
[mcp_servers.kitsoki-validator]
command = "/old/kitsoki"

[mcp_servers.slidey]
command = "/bin/slidey"

[mcp_servers.codex_app]
command = "/bin/codex-app"
`)
		// Write a real --mcp-config file so the TOML override conversion runs.
		cfgPath := filepath.Join(t.TempDir(), "cfg.json")
		mustWrite(t, cfgPath, `{"mcpServers":{"kitsoki-validator":{"command":"/bin/kitsoki","args":["mcp-validator","--schema","/tmp/s.json"]}}}`)
		args := []string{
			"-p",
			"--permission-mode", "bypassPermissions",
			"--setting-sources", "project,local",
			"--disable-slash-commands",
			"--strict-mcp-config",
			"--system-prompt", "SYS-PROMPT",
			"--model", "some-model",
			"--effort", "low",
			"--mcp-config", cfgPath,
			"--output-format", "stream-json", "--verbose",
		}
		inv := codexBackend{}.TranslateInvocation(args, stdin, wd)
		defer inv.Cleanup()
		got := strings.Join(inv.Args, " ")

		// User prompt stays on stdin; the system prompt becomes Codex base
		// instructions via model_instructions_file.
		if inv.Stdin != codexMCPToolSearchPreamble+"\n\n---\n\n"+stdin {
			t.Errorf("codex stdin = %q, want tool-search preamble + user prompt only", inv.Stdin)
		}
		if !strings.Contains(inv.Stdin, "tool_search") {
			t.Errorf("codex stdin missing the MCP tool-search discovery preamble; got %q", inv.Stdin)
		}
		if strings.Contains(inv.Stdin, "SYS-PROMPT") {
			t.Errorf("codex stdin must not contain replacing system prompt; got %q", inv.Stdin)
		}
		sysFile := codexModelInstructionsFile(t, inv.Args)
		rawSys, err := os.ReadFile(sysFile)
		if err != nil {
			t.Fatalf("read codex model_instructions_file %q: %v", sysFile, err)
		}
		if string(rawSys) != "SYS-PROMPT" {
			t.Fatalf("model_instructions_file content = %q, want SYS-PROMPT", string(rawSys))
		}
		if inv.PromptForBudget != "SYS-PROMPT\n\n---\n\n"+inv.Stdin {
			t.Errorf("PromptForBudget = %q, want system prompt + stdin", inv.PromptForBudget)
		}
		// No MCP config registered ⇒ no preamble (nothing deferred to discover).
		noMCP := codexBackend{}.TranslateInvocation([]string{"-p", "--system-prompt", "S"}, "body", wd)
		defer noMCP.Cleanup()
		if strings.Contains(noMCP.Stdin, "tool_search") {
			t.Errorf("codex injected the tool-search preamble with no MCP config; stdin=%q", noMCP.Stdin)
		}
		if noMCP.Stdin != "body" {
			t.Errorf("codex no-MCP stdin = %q, want bare body", noMCP.Stdin)
		}
		if strings.Contains(strings.Join(noMCP.Args, " "), "--disable=apps") {
			t.Errorf("codex no-MCP invocation must not disable app connectors; args=%v", noMCP.Args)
		}
		noMCPFile := codexModelInstructionsFile(t, noMCP.Args)
		rawNoMCP, err := os.ReadFile(noMCPFile)
		if err != nil {
			t.Fatalf("read codex no-MCP model_instructions_file %q: %v", noMCPFile, err)
		}
		if string(rawNoMCP) != "S" {
			t.Fatalf("no-MCP model_instructions_file content = %q, want S", string(rawNoMCP))
		}
		// Base exec flags.
		if len(inv.Args) == 0 || inv.Args[0] != "exec" {
			t.Errorf("codex args must start with exec; args=%v", inv.Args)
		}
		mustContain(t, got, "--json")
		mustContain(t, got, "--skip-git-repo-check")
		// codex exec auto-cancels MCP tool calls unless its approval+sandbox
		// gate is disabled, so the validator submit tool only executes with the
		// bypass flag (verified live; see agent_backend_codex.go).
		mustContain(t, got, "--dangerously-bypass-approvals-and-sandbox")
		if !hasFlagValue(inv.Args, "-C", wd) {
			t.Errorf("codex missing -C %s; args=%v", wd, inv.Args)
		}
		readOnly := codexBackend{}.TranslateInvocation([]string{
			"-p", "--permission-mode", "default", "--disallowedTools", "Write,Edit,Bash,AskUserQuestion",
		}, "read only", wd)
		readOnlyArgs := strings.Join(readOnly.Args, " ")
		if hasFlagValue(readOnly.Args, "--sandbox", "read-only") {
			t.Errorf("codex read-only invocation must not use codex's sandbox; Kitsoki owns write-mode mediation; args=%v", readOnly.Args)
		}
		if !strings.Contains(readOnlyArgs, "--dangerously-bypass-approvals-and-sandbox") {
			t.Errorf("codex read-only invocation must bypass codex approvals so Kitsoki MCP submit/write-mode tools work; args=%v", readOnly.Args)
		}
		// MCP config converted to `-c mcp_servers.*` overrides.
		mustContain(t, got, `mcp_servers.kitsoki-validator.command="/bin/kitsoki"`)
		mustContain(t, got, `mcp_servers.kitsoki-validator.args=["mcp-validator","--schema","/tmp/s.json"]`)
		mustContain(t, got, `mcp_servers.kitsoki-validator.enabled=true`)
		mustContain(t, got, `mcp_servers.slidey={`)
		mustContain(t, got, `mcp_servers.codex_app={`)
		mustContain(t, got, `enabled=false`)
		// A Kitsoki-supplied MCP config is a scoped tool surface; Codex app
		// connectors are a separate inherited capability category and must not
		// be added to that surface implicitly.
		mustContain(t, got, "--disable=apps")

		// A claude model id is dropped (some-model is not claude-shaped → kept as -m).
		if !hasFlagValue(inv.Args, "-m", "some-model") {
			t.Errorf("codex dropped a non-claude model; args=%v", inv.Args)
		}
		cb := codexBackend{}
		mi := cb.TranslateInvocation([]string{"-p", "--model", "claude-haiku-4-5-20251001"}, "p", "")
		if strings.Contains(strings.Join(mi.Args, " "), "-m ") {
			t.Errorf("codex forwarded a claude model id; args=%v", mi.Args)
		}

		// Claude-only flags must be gone.
		for _, dropped := range []string{"--permission-mode", "--setting-sources", "--disable-slash-commands", "--effort", "--verbose", "--system-prompt", "--mcp-config", "stream-json", "--output-format", "--strict-mcp-config"} {
			if strings.Contains(got, dropped) {
				t.Errorf("codex args still contain dropped flag %q: %v", dropped, inv.Args)
			}
		}

		// Resume maps onto the `exec resume <id>` subcommand form.
		resume := cb.TranslateInvocation([]string{"-p", "--resume", "uuid-123"}, "p", "")
		if len(resume.Args) < 3 || resume.Args[0] != "exec" || resume.Args[1] != "resume" || resume.Args[2] != "uuid-123" {
			t.Errorf("codex --resume not in `exec resume <id>` form; args=%v", resume.Args)
		}
		// `codex exec resume` rejects -C/--cd ("unexpected argument '-C'"), which
		// failed every retry attempt. -C must be omitted when resuming (the
		// working root is fixed by the recorded session).
		resumeWithWD := cb.TranslateInvocation([]string{"-p", "--resume", "uuid-123"}, "p", wd)
		for i, a := range resumeWithWD.Args {
			if a == "-C" {
				t.Errorf("codex exec resume must not pass -C; args=%v", resumeWithWD.Args)
			}
			_ = i
		}
		// Non-resume still passes -C (codex exec accepts it).
		if !hasFlagValue(cb.TranslateInvocation([]string{"-p"}, "p", wd).Args, "-C", wd) {
			t.Errorf("codex exec (non-resume) must pass -C %s", wd)
		}

		// #33 regression: `codex exec resume` accepts ONLY
		// `--json --skip-git-repo-check <id> [prompt]`. It rejects the
		// sandbox/approval flag, -m, the `-c mcp_servers.*` overrides, and
		// passthrough flags ("unexpected argument '--sandbox'"). A live converse
		// follow-up died on this. Build a resume call carrying model + mcp-config
		// + --add-dir and assert NONE of those leak onto the resume argv.
		resumeFull := cb.TranslateInvocation([]string{
			"-p", "--resume", "uuid-123",
			"--model", "gpt-5", "--mcp-config", cfgPath, "--add-dir", "/x",
		}, "p", wd)
		resumeJoined := strings.Join(resumeFull.Args, " ")
		for _, banned := range []string{"--dangerously-bypass-approvals-and-sandbox", "--disable=apps", "-m", "mcp_servers.", "--add-dir", "-C"} {
			if strings.Contains(resumeJoined, banned) {
				t.Errorf("codex exec resume must not carry %q (resume rejects it); args=%v", banned, resumeFull.Args)
			}
		}
		// …but it MUST still carry the accepted flags.
		for _, req := range []string{"resume", "uuid-123", "--json", "--skip-git-repo-check"} {
			if !strings.Contains(resumeJoined, req) {
				t.Errorf("codex exec resume missing required %q; args=%v", req, resumeFull.Args)
			}
		}
		// -C MUST be absolute. The runner sets the child cwd to WorkingDir, so a
		// RELATIVE -C would resolve against that cwd (workingDir/workingDir) →
		// "No such file or directory" and every attempt fails. Verify a relative
		// workingDir is made absolute for -C.
		relArgs := cb.TranslateInvocation([]string{"-p"}, "p", "docs/decks").Args
		absWD, _ := filepath.Abs("docs/decks")
		if !hasFlagValue(relArgs, "-C", absWD) {
			t.Errorf("codex -C must be absolute for a relative workingDir; want -C %s; args=%v", absWD, relArgs)
		}
	})

	t.Run("agy/rewrite", func(t *testing.T) {
		inv := agyBackend{}.TranslateInvocation(claudeArgs, stdin, wd)
		got := strings.Join(inv.Args, " ")

		if inv.Stdin != "" {
			t.Errorf("agy stdin = %q, want empty (prompt is an arg)", inv.Stdin)
		}
		if !hasFlagValue(inv.Args, "--print", "SYS-PROMPT\n\n---\n\nUSER PROMPT") {
			t.Errorf("agy --print arg missing prepended system prompt; args=%v", inv.Args)
		}
		mustContain(t, got, "--dangerously-skip-permissions")
		mustContain(t, got, "--output-format json")
		if !hasFlagValue(inv.Args, "--model", "some-model") {
			t.Errorf("agy missing --model some-model; args=%v", inv.Args)
		}
		// A claude model id must be dropped.
		cb := agyBackend{}
		mi := cb.TranslateInvocation([]string{"-p", "--model", "claude-haiku-4-5-20251001"}, "p", "")
		if strings.Contains(strings.Join(mi.Args, " "), "--model") {
			t.Errorf("agy forwarded a claude model id; args=%v", mi.Args)
		}
		// A genuine model id IS forwarded.
		ci := cb.TranslateInvocation([]string{"-p", "--model", "gemini-3.5-flash"}, "p", "")
		if !hasFlagValue(ci.Args, "--model", "gemini-3.5-flash") {
			t.Errorf("agy dropped a non-claude model; args=%v", ci.Args)
		}

		// Claude-only flags must be gone.
		for _, dropped := range []string{"--permission-mode", "--setting-sources", "--disable-slash-commands", "--effort", "--verbose", "--append-system-prompt", "--mcp-config", "stream-json"} {
			if strings.Contains(got, dropped) {
				t.Errorf("agy args still contain dropped flag %q: %v", dropped, inv.Args)
			}
		}

		// Resume maps to --conversation and suppresses the system prompt on warm runs.
		resume := cb.TranslateInvocation([]string{"-p", "--resume", "uuid-123", "--system-prompt", "SYS-PROMPT"}, "USER-PROMPT", "")
		if !hasFlagValue(resume.Args, "--conversation", "uuid-123") {
			t.Errorf("agy --resume not translated to --conversation; args=%v", resume.Args)
		}
		if hasFlagValue(resume.Args, "--print", "SYS-PROMPT\n\n---\n\nUSER-PROMPT") {
			t.Errorf("agy warm run must not prepend system prompt; args=%v", resume.Args)
		}
		if !hasFlagValue(resume.Args, "--print", "USER-PROMPT") {
			t.Errorf("agy warm run missing user prompt; args=%v", resume.Args)
		}

		// Session-id maps to --conversation and prepends system prompt on cold runs.
		session := cb.TranslateInvocation([]string{"-p", "--session-id", "uuid-456", "--system-prompt", "SYS-PROMPT"}, "USER-PROMPT", "")
		if !hasFlagValue(session.Args, "--conversation", "uuid-456") {
			t.Errorf("agy --session-id not translated to --conversation; args=%v", session.Args)
		}
		if !hasFlagValue(session.Args, "--print", "SYS-PROMPT\n\n---\n\nUSER-PROMPT") {
			t.Errorf("agy cold run must prepend system prompt; args=%v", session.Args)
		}
	})
}

// TestConformance_CopilotUsageOutputTokens asserts copilot's per-message
// outputTokens are summed into the terminal usage map (claude reports the total
// directly, so this only matters for copilot).
func TestConformance_CopilotUsageOutputTokens(t *testing.T) {
	ctx := WithAgentBackend(context.Background(), copilotBackend{})
	// Sum of outputTokens across every assistant.message in the turn (each is a
	// separate API response): ask_simple has one (5); with_tool_round has the
	// tool-call message + the final message (141 + 12 = 153).
	cases := map[string]int{
		"copilot/ask_simple.jsonl":      5,
		"copilot/with_tool_round.jsonl": 153,
	}
	for fixture, want := range cases {
		_, _, _, usage, _ := parseStreamJSONOutput(ctx, readFixture(t, fixture))
		got, ok := usage["output_tokens"].(float64)
		if !ok {
			t.Errorf("%s: usage missing output_tokens; got %v", fixture, usage)
			continue
		}
		if int(got) != want {
			t.Errorf("%s: output_tokens = %d, want %d", fixture, int(got), want)
		}
		if _, ok := usage["premium_requests"]; !ok {
			t.Errorf("%s: usage missing premium_requests; got %v", fixture, usage)
		}
	}
}

// TestConformance_ValidatorToolName asserts both backends name the submit tool
// so the prompt instruction + side-channel capture path line up.
func TestConformance_ValidatorToolName(t *testing.T) {
	// Exact per-backend MCP tool-name schemes (all verified against the real
	// CLIs): claude uses mcp__<server>__<tool>, copilot uses <server>-<tool>,
	// codex uses bare "submit" (server lives in a separate JSONL field).
	want := map[string]string{
		"claude":  "mcp__kitsoki-validator__submit",
		"copilot": "kitsoki-validator-submit",
		"codex":   "submit",
		"agy":     "mcp__kitsoki-validator__submit",
	}
	for _, b := range allBackends {
		got := b.backend.ValidatorToolName("kitsoki-validator")
		if got != want[b.name] {
			t.Errorf("%s ValidatorToolName = %q, want %q", b.name, got, want[b.name])
		}
	}
}

// TestConformance_StubRoundTrip drives the full runClaudeStreamJSON stub branch
// per backend (translation + parse) and asserts the final reply surfaces.
func TestConformance_StubRoundTrip(t *testing.T) {
	cases := []struct {
		name      string
		backend   agentBackend
		withStub  func(context.Context, ClaudeRunner) context.Context
		fixture   string
		wantReply string
	}{
		{"claude", claudeBackend{}, WithClaudeRunner, "claude/ask_simple.jsonl", "pong"},
		{"copilot", copilotBackend{}, WithCopilotRunner, "copilot/ask_simple.jsonl", "pong"},
		{"codex", codexBackend{}, WithCodexRunner, "codex/ask_simple.jsonl", `{"answer":"hello"}`},
		{"agy", agyBackend{}, WithAgyRunner, "agy/ask_simple.jsonl", "pong"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw := readFixture(t, c.fixture)
			runner := func(_ context.Context, _ []string, _ string, _ string) (ClaudeRun, error) {
				return ClaudeRun{Stdout: raw}, nil
			}
			ctx := c.withStub(WithAgentBackend(context.Background(), c.backend), runner)
			cr, _, err := runClaudeStreamJSON(ctx, "stub://x", []string{"-p"}, "prompt", "")
			if err != nil {
				t.Fatalf("runClaudeStreamJSON: %v", err)
			}
			if got := strings.TrimSpace(cr.Stdout); got != c.wantReply {
				t.Errorf("reply = %q, want %q", got, c.wantReply)
			}
		})
	}
}

// --- helpers ---

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func equalArgs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// hasFlagValue reports whether args contains flag immediately followed by val.
func hasFlagValue(args []string, flag, val string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == val {
			return true
		}
	}
	return false
}

func mustContain(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Errorf("expected %q to contain %q", haystack, needle)
	}
}

func codexModelInstructionsFile(t *testing.T, args []string) string {
	t.Helper()
	for i := 0; i+1 < len(args); i++ {
		if args[i] != "-c" {
			continue
		}
		const prefix = "model_instructions_file="
		if v, ok := strings.CutPrefix(args[i+1], prefix); ok {
			return strings.Trim(v, `"`)
		}
	}
	t.Fatalf("codex args missing model_instructions_file override: %v", args)
	return ""
}
