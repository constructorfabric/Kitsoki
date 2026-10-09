// Package host — the copilot agentBackend.
//
// copilotBackend drives GitHub's `copilot` CLI as a drop-in alternative to
// `claude` for every agent verb. The verb handlers are unaware of it: they
// build a claude-shaped invocation (claude argv + prompt on stdin) and
// TranslateInvocation rewrites that into copilot's surface, which differs on
// every wire detail:
//
//   - the prompt is a `-p <text>` argument, not stdin;
//   - permission is `--allow-all-tools`, not `--permission-mode bypassPermissions`;
//   - MCP config is `--additional-mcp-config @<file>`, not `--mcp-config <file>`;
//   - there is no system-prompt flag, so the composed kitsoki system prompt is
//     prepended into the `-p` text;
//   - output is `--output-format json` — JSONL, one event per line — with a
//     distinct event vocabulary (assistant.message / tool.execution_* / result)
//     parsed by classifyCopilotEvent;
//   - the terminal result reports usage as premium-request counts + durations,
//     never tokens or a dollar cost.
//
// Flags claude understands but copilot does not (--setting-sources, --effort,
// --exclude-dynamic-system-prompt-sections, --no-session-persistence,
// --verbose, --permission-mode) are dropped during translation.
package host

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// CopilotBinEnv overrides the `copilot` binary path (tests / non-PATH installs).
const CopilotBinEnv = "KITSOKI_AGENT_COPILOT_BIN"

// copilotBackend drives GitHub's `copilot` CLI.
type copilotBackend struct{}

func (copilotBackend) Name() string { return "copilot" }

func (copilotBackend) ResolveBin(ctx context.Context) (string, error) {
	if CopilotRunnerFromContext(ctx) != nil {
		return "stub://copilot", nil
	}
	if bin := os.Getenv(CopilotBinEnv); bin != "" {
		return bin, nil
	}
	path, err := exec.LookPath("copilot")
	if err != nil {
		// See codexBackend.ResolveBin: wraps the shared sentinel so
		// errors.Is(_, ErrAgentUnavailable) still holds, but names the
		// backend that actually failed instead of the sentinel's
		// hardcoded "claude" text.
		return "", fmt.Errorf("host.agent.converse: `copilot` binary not found on PATH; install the GitHub Copilot CLI: %w", ErrAgentUnavailable)
	}
	return path, nil
}

// claudeValueFlags are the claude flags that consume the following argv element
// as their value (space-separated form). Used by the translator to skip or
// relocate a flag's value. Boolean flags are handled separately.
var claudeValueFlags = map[string]bool{
	"--permission-mode":      true,
	"--model":                true,
	"--system-prompt":        true,
	"--append-system-prompt": true,
	"--mcp-config":           true,
	"--setting-sources":      true,
	"--settings":             true,
	"--effort":               true,
	"--output-format":        true,
	"--session-id":           true,
	"--resume":               true,
	"--allowedTools":         true,
	"--disallowedTools":      true,
	"--add-dir":              true,
}

// TranslateInvocation rewrites a claude-shaped invocation into copilot's CLI.
// The prompt (stdin) and any system prompt extracted from the claude argv are
// folded into a single `-p` argument; the working dir is carried both on the
// Invocation (cmd.Dir) and as `-C` so it survives either way.
func (copilotBackend) TranslateInvocation(claudeArgs []string, stdin, workingDir string) Invocation {
	var (
		out          []string
		systemPrompt string
		model        string
		mcpConfig    string
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

		// Pull the value (next element or inlined) for value-taking flags.
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
			"--disable-slash-commands":
			// Dropped: no copilot equivalent (or supplied differently).
		case "--permission-mode", "--setting-sources", "--settings", "--effort",
			"--allowedTools", "--disallowedTools":
			// Dropped along with their value. (Tool-scoping is a parity gap;
			// copilot uses --allow-all-tools.)
		case "--session-id":
			// Copilot's --session-id has the same meaning as claude's: set the
			// UUID for a new session (first call) so it can be resumed later.
			// Forwarded verbatim (space-separated value form).
			if strings.TrimSpace(val) != "" {
				out = append(out, "--session-id", val)
			}
		case "--resume":
			// Copilot's resume flag carries the session id to re-engage across
			// the decide/task/converse nudge rounds. It is declared as an
			// optional-value flag (`-r, --resume[=value]`), so the value must
			// use the `=` form rather than a separate argv element.
			if v := strings.TrimSpace(val); v != "" {
				out = append(out, "--resume="+v)
			}
		case "--add-dir":
			// Copilot supports --add-dir <directory> with the same meaning.
			if strings.TrimSpace(val) != "" {
				out = append(out, "--add-dir", val)
			}
		case "--output-format":
			// Always normalized to copilot's JSONL json below.
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

	// Compose the prompt argument: system prompt (if any) prepended to the
	// rendered user prompt that claude would have piped on stdin.
	prompt := stdin
	if sp := strings.TrimSpace(systemPrompt); sp != "" {
		prompt = sp + "\n\n---\n\n" + stdin
	}

	args := []string{
		"-p", prompt,
		"--output-format", "json",
		"--allow-all-tools",
		"--no-color",
		"--log-level", "none",
	}
	// Forward the model ONLY when it is not a claude model id. Stories and the
	// routing harness specify claude model names (claude-haiku-…, opus, sonnet,
	// haiku); passing those to copilot fails with "model not available". When
	// the model is claude-shaped we drop it so copilot uses its own configured
	// / auto model (override via copilot's config or COPILOT_MODEL); a genuine
	// copilot model name (e.g. gpt-5) is forwarded.
	if m := strings.TrimSpace(model); m != "" && !isClaudeModelID(m) {
		args = append(args, "--model", m)
	}
	if strings.TrimSpace(workingDir) != "" {
		args = append(args, "-C", workingDir)
	}
	if strings.TrimSpace(mcpConfig) != "" {
		args = append(args, "--additional-mcp-config", "@"+mcpConfig)
	}
	// Forwarded/passthrough flags collected during translation (--session-id,
	// --resume=, --add-dir, and any unknown flag).
	args = append(args, out...)

	return Invocation{Args: args, Stdin: "", WorkingDir: workingDir}
}

// isClaudeModelID reports whether a model name is an Anthropic/claude model id
// or one of claude's short aliases — names copilot does not understand.
func isClaudeModelID(m string) bool {
	m = strings.ToLower(strings.TrimSpace(m))
	if strings.HasPrefix(m, "claude-") {
		return true
	}
	switch m {
	case "opus", "sonnet", "haiku":
		return true
	}
	return false
}

// IsClaudeModelID is the exported form of isClaudeModelID for CLI planning
// surfaces that build backend-specific argv directly instead of going through
// TranslateInvocation.
func IsClaudeModelID(m string) bool {
	return isClaudeModelID(m)
}

func (copilotBackend) Classify(ev map[string]any) classifiedEvent {
	return classifyCopilotEvent(ev)
}

func (copilotBackend) TranscriptFormat() string { return "copilot-jsonl" }

// ValidatorToolName returns copilot's tool name for the `submit` tool of an MCP
// server registered via --additional-mcp-config. Copilot namespaces MCP tools
// as "<server>-<tool>" (verified live: a server named "kitsoki-validator"
// exposes "kitsoki-validator-submit") — distinct from claude's
// "mcp__<server>__submit". The gated live smoke test (agent_copilot_smoke)
// re-confirms this against the real binary.
func (copilotBackend) ValidatorToolName(server string) string {
	return server + "-submit"
}

func (copilotBackend) runnerFromContext(ctx context.Context) ClaudeRunner {
	return CopilotRunnerFromContext(ctx)
}

// --- copilot test-stub seam (mirror of WithClaudeRunner) ---

type copilotRunnerCtxKey struct{}

// WithCopilotRunner installs a stub runner for the copilot backend so tests
// exercise the translation + JSONL parsing without forking `copilot` or
// incurring an LLM call. The stub receives the TRANSLATED copilot argv/stdin.
func WithCopilotRunner(ctx context.Context, r ClaudeRunner) context.Context {
	return context.WithValue(ctx, copilotRunnerCtxKey{}, r)
}

// CopilotRunnerFromContext returns the copilot stub runner installed in ctx, or
// nil for the real-exec path.
func CopilotRunnerFromContext(ctx context.Context) ClaudeRunner {
	r, _ := ctx.Value(copilotRunnerCtxKey{}).(ClaudeRunner)
	return r
}
