// Package host — the codex agentBackend.
//
// codexBackend drives OpenAI's `codex exec` CLI as a third drop-in alternative
// to `claude` and `copilot` for every agent verb. As with copilot, the verb
// handlers are unaware of it: they build a claude-shaped invocation (claude
// argv + prompt on stdin) and TranslateInvocation rewrites that onto codex's
// surface, which differs on every wire detail:
//
//   - the prompt is read from stdin (codex `exec` reads the instructions from
//     stdin when no positional prompt is given) — same delivery as claude;
//   - codex does not accept Claude's allowed/disallowed tool flags. Calls that
//     carry a mutator deny-list preserve the story's read-only posture via
//     `--sandbox read-only`; other calls still use
//     `--dangerously-bypass-approvals-and-sandbox`, the only way the validator
//     submit tool can execute (verified live; see TranslateInvocation). This is
//     why write-capable `--agent codex` paths require a trusted/externally
//     sandboxed environment;
//   - MCP config is not a file flag: the --mcp-config JSON is read and each
//     server is converted to codex `-c mcp_servers.<name>.{command,args,env}`
//     TOML config overrides;
//   - there is no system-prompt flag, so the composed kitsoki system prompt is
//     prepended into the stdin prompt;
//   - output is `--json` — JSONL, one event per line — with a distinct,
//     two-layer event vocabulary (thread.started / turn.* / item.* with nested
//     item types agent_message / command_execution / mcp_tool_call) parsed by
//     classifyCodexEvent;
//   - the terminal `turn.completed` reports usage as token counts
//     (input/cached_input/output/reasoning_output), never a dollar cost.
//
// Session resume maps onto codex's `exec resume <id>` subcommand form rather
// than a flag (see TranslateInvocation).
//
// Flags claude understands but codex does not (--permission-mode,
// --setting-sources, --effort, --exclude-dynamic-system-prompt-sections,
// --no-session-persistence, --verbose, --allowedTools, --output-format) are
// dropped during translation. --disallowedTools is interpreted only to select
// Codex's sandbox for read-only story agents.
package host

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// CodexBinEnv overrides the `codex` binary path (tests / non-PATH installs).
const CodexBinEnv = "KITSOKI_AGENT_CODEX_BIN"

// codexBackend drives OpenAI's `codex exec` CLI.
type codexBackend struct{}

func (codexBackend) Name() string { return "codex" }

func (codexBackend) ResolveBin(ctx context.Context) (string, error) {
	if CodexRunnerFromContext(ctx) != nil {
		return "stub://codex", nil
	}
	if bin := os.Getenv(CodexBinEnv); bin != "" {
		return bin, nil
	}
	path, err := exec.LookPath("codex")
	if err != nil {
		return "", ErrAgentUnavailable
	}
	return path, nil
}

// TranslateInvocation rewrites a claude-shaped invocation into a `codex exec`
// invocation. The prompt is kept on stdin (codex reads instructions from stdin
// when no positional prompt is supplied); any system prompt extracted from the
// claude argv is prepended to it. The working dir is carried both on the
// Invocation (cmd.Dir) and as `-C` so it survives either way. MCP servers from
// the --mcp-config file become `-c mcp_servers.<name>.*` TOML overrides.
func (codexBackend) TranslateInvocation(claudeArgs []string, stdin, workingDir string) Invocation {
	var (
		out          []string // passthrough flags (e.g. --add-dir, unknown)
		systemPrompt string
		model        string
		mcpConfig    string
		sessionID    string // --session-id (first call) — codex has no equivalent
		resumeID     string // --resume <id> → `exec resume <id>`
	)

	// Split a "--flag=value" element into ("--flag","value"); leave others.
	flagVal := func(a string) (flag, val string, inlined bool) {
		if i := strings.IndexByte(a, '='); i > 0 && strings.HasPrefix(a, "-") {
			return a[:i], a[i+1:], true
		}
		return a, "", false
	}

	for i := 0; i < len(claudeArgs); i++ {
		a := claudeArgs[i]
		flag, inlineVal, inlined := flagVal(a)

		val := inlineVal
		consumed := false
		if claudeValueFlags[flag] && !inlined {
			if i+1 < len(claudeArgs) {
				val = claudeArgs[i+1]
				consumed = true
			}
		}

		switch flag {
		case "-p", "--verbose", "--exclude-dynamic-system-prompt-sections", "--no-session-persistence",
			"--disable-slash-commands", "--strict-mcp-config":
			// Dropped: no codex equivalent (or supplied differently).
			// `--strict-mcp-config` is a claude-only boolean (restrict MCP to the
			// --mcp-config file). codex exec rejects it ("unexpected argument
			// '--strict-mcp-config'", exit 2) — which silently burned every
			// acceptance attempt in ~60ms and made codex-profile sessions
			// "impossible" (validator submit never ran). codex registers the
			// validator MCP via the `-c mcp_servers.*` overrides below instead.
		case "--permission-mode", "--setting-sources", "--effort",
			"--allowedTools":
			// Dropped along with their value. (Tool-scoping is a parity gap;
			// codex runs with the bypass flag set below.)
		case "--disallowedTools":
			// Codex has no direct equivalent. Kitsoki's read-only posture is
			// enforced by the story/tooling layer; see the bypass rationale below.
		case "--output-format":
			// Always normalized to codex's --json below.
		case "--session-id":
			// Codex has no --session-id; a session is created per run and its
			// thread_id is captured from thread.started. First-call id is dropped.
			sessionID = val
		case "--resume":
			// Codex resumes via the `exec resume <id>` subcommand form, not a
			// flag; the id is emitted as the first positional args below.
			resumeID = val
		case "--add-dir":
			// Codex supports --add-dir <directory> with the same meaning.
			if strings.TrimSpace(val) != "" {
				out = append(out, "--add-dir", val)
			}
		case "--system-prompt", "--append-system-prompt":
			systemPrompt = val
		case "--model":
			model = val
		case "--mcp-config":
			mcpConfig = val
		default:
			// Unknown flag — pass through verbatim (and its value if separate).
			out = append(out, a)
			if claudeValueFlags[flag] && consumed {
				out = append(out, val)
			}
			continue
		}
		if consumed {
			i++ // skip the value element we just handled
		}
	}
	_ = sessionID // dropped intentionally — codex creates the session per run.

	// Base exec invocation. `resume <id>` is prepended when re-engaging a
	// recorded session across the decide/task/converse nudge rounds.
	//
	// `codex exec resume` is a DIFFERENT subcommand from `codex exec` with a
	// much narrower arg surface: `codex exec resume --json --skip-git-repo-check
	// <SESSION_ID> [PROMPT]`. It rejects the sandbox/approval flag, `-m`, `-C`,
	// `-c` overrides, and any passthrough flag with "unexpected argument …".
	// The recorded session already fixed the model, cwd, sandbox posture, and
	// MCP wiring, so on a resume we emit ONLY the accepted flags. (Surfaced by a
	// live converse follow-up failing with "unexpected argument '--sandbox'",
	// dogfood issue #33.)
	isResume := strings.TrimSpace(resumeID) != ""
	args := []string{"exec"}
	if isResume {
		args = append(args, "resume", strings.TrimSpace(resumeID))
	}
	args = append(args, "--json", "--skip-git-repo-check")
	if !isResume {
		// `codex exec` auto-cancels EVERY MCP tool call ("user cancelled MCP tool
		// call") in non-interactive mode — verified live (2026-06-11) against
		// codex-cli 0.139.0 across approval_policy="never", every sandbox mode,
		// per-server trust keys, and both ephemeral (-c) and persisted (`codex mcp
		// add`) registration. The ONLY way to let the validator `submit` tool and
		// the operator-ask/write-mode MCP bridge execute is to disable codex's
		// approval+sandbox gate. Kitsoki's read-only posture is still expressed via
		// --disallowedTools / Bash MCP policy; relying on Codex's read-only sandbox
		// preempts Kitsoki's own write-mode opt-in and makes MCP-only dogfood unable
		// to apply an operator-granted edit.
		args = append(args, "--dangerously-bypass-approvals-and-sandbox")

		// Forward the model ONLY when it is not a claude model id (reuse the shared
		// helper). Stories/router specify claude model names; passing those to codex
		// fails, so we drop them and let codex use its configured model. A genuine
		// codex/OpenAI model name is forwarded as `-m`.
		if m := strings.TrimSpace(model); m != "" && !isClaudeModelID(m) {
			args = append(args, "-m", m)
		}
		// `-C/--cd <DIR>` is accepted by `codex exec` but NOT by `codex exec resume`
		// (the resume subcommand rejects it: "unexpected argument '-C'"). On a
		// resume the working root is fixed by the recorded session, so omit it.
		//
		// The value MUST be absolute. The runner sets the child process cwd to
		// inv.WorkingDir (agent_runner.go: cmd.Dir = inv.WorkingDir), so codex
		// already starts IN workingDir; a RELATIVE `-C workingDir` would then
		// resolve against that cwd (workingDir/workingDir) → "No such file or
		// directory (os error 2)" and every attempt fails. An absolute path is
		// idempotent regardless of the inherited cwd.
		if strings.TrimSpace(workingDir) != "" && strings.TrimSpace(resumeID) == "" {
			cd := workingDir
			if abs, err := filepath.Abs(workingDir); err == nil {
				cd = abs
			}
			args = append(args, "-C", cd)
		}
		// Convert each MCP server in the --mcp-config file into codex `-c` overrides.
		args = append(args, codexMCPConfigArgs(mcpConfig)...)
		// Forwarded/passthrough flags (--add-dir and any unknown flag).
		args = append(args, out...)
	} // end !isResume — resume rejects all of the above flags.

	// Compose the stdin prompt: system prompt (if any) prepended to the user
	// prompt claude would have piped on stdin. Unlike copilot, codex keeps the
	// prompt on stdin.
	prompt := stdin
	if sp := strings.TrimSpace(systemPrompt); sp != "" {
		prompt = sp + "\n\n---\n\n" + stdin
	}
	// codex (≥0.142) DEFERS MCP server tools behind its `tool_search` tool — a
	// registered server's tools (e.g. the acceptance-loop validator `submit`) are
	// NOT in the eager tool list, so a prompt that says "call submit(…)" finds no
	// such tool and the model emulates it as TEXT — every acceptance attempt then
	// fails and the host.agent.task bounces (verified live, codex-cli 0.142.3).
	// Tool-search itself works: when the model is told to discover the tool first
	// it calls server="kitsoki-validator" tool="submit" and the payload validates.
	// So whenever we registered MCP servers (mcpConfig present, !resume), prepend
	// an explicit discovery instruction. Resume inherits the recorded session's
	// wiring and arg surface, so it is left untouched.
	if !isResume && strings.TrimSpace(mcpConfig) != "" {
		prompt = codexMCPToolSearchPreamble + "\n\n---\n\n" + prompt
	}

	return Invocation{Args: args, Stdin: prompt, WorkingDir: workingDir}
}

// codexMCPToolSearchPreamble instructs a codex agent to surface deferred MCP
// tools via tool_search before using them. codex ≥0.142 does not expose MCP
// server tools eagerly; without this, the validator `submit` tool (and any other
// kitsoki-registered MCP tool the prompt asks for) appears to "not exist" and the
// model fakes the call in prose. Phrased producer-neutrally — it names no
// specific server so it holds for the validator, operator-ask, and write-mode
// bridges alike.
const codexMCPToolSearchPreamble = "TOOL ACCESS (codex): Some tools provided to you — including the " +
	"`submit` tool used to submit your final result — are NOT listed in your " +
	"default tool set. They are reachable only via the `tool_search` tool. " +
	"BEFORE you conclude that a tool the task asks for (e.g. `submit`) is " +
	"unavailable, you MUST call `tool_search` to locate it, then call it. Never " +
	"emulate such a tool by printing its name or its arguments as text — a " +
	"printed call does nothing."

// codexMCPConfigArgs reads a claude-shaped --mcp-config JSON file
// ({"mcpServers":{name:{command,args,env}}}) and emits codex `-c` TOML config
// overrides registering each server: mcp_servers.<name>.command/args/env. This
// is the crux of parity — the validator server must be registered so codex can
// call its submit tool. Defensive: a missing/malformed file or server is
// skipped rather than fatal (the caller still gets a usable invocation).
func codexMCPConfigArgs(path string) []string {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var cfg struct {
		MCPServers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil
	}
	// Stable order for deterministic argv (tests + reproducible transcripts).
	names := make([]string, 0, len(cfg.MCPServers))
	for name := range cfg.MCPServers {
		names = append(names, name)
	}
	sort.Strings(names)

	var out []string
	for _, name := range names {
		s := cfg.MCPServers[name]
		base := "mcp_servers." + name + "."
		if s.Command != "" {
			out = append(out, "-c", base+"command="+tomlString(s.Command))
		}
		if len(s.Args) > 0 {
			out = append(out, "-c", base+"args="+tomlStringArray(s.Args))
		}
		if len(s.Env) > 0 {
			out = append(out, "-c", base+"env="+tomlStringTable(s.Env))
		}
	}
	return out
}

// tomlString encodes a Go string as a TOML basic string (double-quoted, with
// the minimal escapes TOML requires).
func tomlString(s string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
		`"`, `\"`,
		"\n", `\n`,
		"\t", `\t`,
		"\r", `\r`,
	)
	return `"` + r.Replace(s) + `"`
}

// tomlStringArray encodes a slice of strings as a TOML inline array.
func tomlStringArray(xs []string) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = tomlString(x)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// tomlStringTable encodes a string→string map as a TOML inline table with keys
// in sorted order (deterministic argv).
func tomlStringTable(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = tomlString(k) + "=" + tomlString(m[k])
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func (codexBackend) Classify(ev map[string]any) classifiedEvent {
	return classifyCodexEvent(ev)
}

func (codexBackend) TranscriptFormat() string { return "codex-jsonl" }

// ValidatorToolName returns codex's tool name for the `submit` tool of an MCP
// server registered via the `-c mcp_servers.<name>.*` overrides. This is a
// best-guess placeholder ("<server>__submit") modeled on claude's
// "mcp__<server>__submit" scheme minus the "mcp__" prefix.
//
// IMPORTANT: this MUST be confirmed against the real `codex` binary by the
// gated live smoke test (agent_codex_smoke_test.go, KITSOKI_AGENT_LIVE=1) and
// Live-verified (KITSOKI_AGENT_LIVE=1 smoke, 2026-06-11): codex names the MCP
// submit tool bare "submit" and carries the server in a SEPARATE JSONL field
// (server="kitsoki-validator", tool="submit"), so it does NOT concatenate like
// claude ("mcp__<server>__submit") or copilot ("<server>-submit"). The server
// argument is therefore unused here.
func (codexBackend) ValidatorToolName(server string) string {
	return CodexValidatorToolName(server)
}

// CodexValidatorToolName is the package-exported form of
// codexBackend.ValidatorToolName, used by cmd/kitsoki to set the routing
// harness's validator tool name from the single source of truth (so the
// live-pinned scheme stays consistent between the backend and the harness).
// codex exposes the tool as bare "submit" (server lives in its own JSONL field).
func CodexValidatorToolName(server string) string {
	_ = server // codex does not namespace the tool name with the server
	return "submit"
}

func (codexBackend) runnerFromContext(ctx context.Context) ClaudeRunner {
	return CodexRunnerFromContext(ctx)
}

// --- codex test-stub seam (mirror of WithCopilotRunner) ---

type codexRunnerCtxKey struct{}

// WithCodexRunner installs a stub runner for the codex backend so tests
// exercise the translation + JSONL parsing without forking `codex` or incurring
// an LLM call. The stub receives the TRANSLATED codex argv/stdin.
func WithCodexRunner(ctx context.Context, r ClaudeRunner) context.Context {
	return context.WithValue(ctx, codexRunnerCtxKey{}, r)
}

// CodexRunnerFromContext returns the codex stub runner installed in ctx, or nil
// for the real-exec path.
func CodexRunnerFromContext(ctx context.Context) ClaudeRunner {
	r, _ := ctx.Value(codexRunnerCtxKey{}).(ClaudeRunner)
	return r
}
