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

// The conformance suite proves both oracle backends honour the same interface
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
	backend oracleBackend
}{
	{"claude", claudeBackend{}},
	{"copilot", copilotBackend{}},
}

// TestConformance_DefaultBackendIsClaude pins the load-bearing default: a
// context with no backend installed MUST resolve to claude so every pre-existing
// call site and test stays on the byte-identical path.
func TestConformance_DefaultBackendIsClaude(t *testing.T) {
	if got := OracleBackendFromContext(context.Background()).Name(); got != "claude" {
		t.Fatalf("default backend = %q, want claude", got)
	}
}

// TestConformance_StreamParse runs each backend's captured fixture through the
// shared stream parser (with the backend installed) and asserts the normalized
// outcome: final reply text, session id, and a backend-appropriate usage key.
func TestConformance_StreamParse(t *testing.T) {
	cases := []struct {
		name          string
		backend       oracleBackend
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
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := WithOracleBackend(context.Background(), c.backend)
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
}

// TestConformance_ArgvTranslation asserts each backend maps a representative
// claude-shaped invocation onto its own CLI surface.
func TestConformance_ArgvTranslation(t *testing.T) {
	// What every verb handler builds today: base flags + system prompt + model
	// + mcp-config + the stream-json output flags OracleStreamer appends.
	claudeArgs := []string{
		"-p",
		"--permission-mode", "bypassPermissions",
		"--setting-sources", "project,local",
		"--append-system-prompt", "SYS-PROMPT",
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
		for _, dropped := range []string{"--permission-mode", "--setting-sources", "--effort", "--verbose", "--append-system-prompt", "--mcp-config ", "stream-json"} {
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
}

// TestConformance_CopilotUsageOutputTokens asserts copilot's per-message
// outputTokens are summed into the terminal usage map (claude reports the total
// directly, so this only matters for copilot).
func TestConformance_CopilotUsageOutputTokens(t *testing.T) {
	ctx := WithOracleBackend(context.Background(), copilotBackend{})
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
	// Exact per-backend MCP tool-name schemes (both verified against the real
	// CLIs): claude uses mcp__<server>__<tool>, copilot uses <server>-<tool>.
	want := map[string]string{
		"claude":  "mcp__kitsoki-validator__submit",
		"copilot": "kitsoki-validator-submit",
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
		backend   oracleBackend
		withStub  func(context.Context, ClaudeRunner) context.Context
		fixture   string
		wantReply string
	}{
		{"claude", claudeBackend{}, WithClaudeRunner, "claude/ask_simple.jsonl", "pong"},
		{"copilot", copilotBackend{}, WithCopilotRunner, "copilot/ask_simple.jsonl", "pong"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw := readFixture(t, c.fixture)
			runner := func(_ context.Context, _ []string, _ string, _ string) (ClaudeRun, error) {
				return ClaudeRun{Stdout: raw}, nil
			}
			ctx := c.withStub(WithOracleBackend(context.Background(), c.backend), runner)
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
