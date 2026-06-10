// Package host — pluggable LLM CLI backend seam for the oracle path.
//
// kitsoki's oracle verbs (ask, decide, task, converse, extract, search) and the
// intent-routing harness all fork a coding-agent CLI one-shot, pipe a rendered
// prompt, and parse the agent's structured output. Historically that CLI was
// hardwired to Anthropic's `claude`: the binary name, the flag vocabulary
// (`-p`, `--permission-mode bypassPermissions`, `--system-prompt`,
// `--mcp-config`, `--output-format stream-json`), and the Anthropic stream-json
// event shapes were baked into oracle_runner.go and every verb handler.
//
// oracleBackend abstracts that one seam so a second CLI — GitHub's `copilot` —
// can serve the same role without touching any verb handler. The verb handlers
// still build a *claude-shaped* invocation (claude argv + prompt on stdin); the
// backend's TranslateInvocation maps that neutral shape onto the concrete CLI it
// drives. The claude backend is the identity translation, so its argv and stdin
// are byte-for-byte what they were before this seam existed — the flagship
// stories must not regress. The copilot backend rewrites the flags, moves the
// prompt into a `-p` argument, and parses copilot's JSONL event vocabulary.
//
// The selected backend rides on the context (WithOracleBackend); when none is
// installed every call defaults to claude, so existing call sites and the entire
// test suite are unaffected.
package host

import "context"

// Invocation is a ready-to-exec one-shot description produced by
// oracleBackend.TranslateInvocation. It is the backend-concrete form of a call:
// the exact argv to pass after the binary, the bytes to feed on stdin (empty
// when the backend carries the prompt as an argument instead), and the working
// directory for the subprocess.
type Invocation struct {
	// Args is the full argument vector (excluding the binary itself).
	Args []string
	// Stdin is the data piped to the subprocess. For claude this is the
	// rendered prompt; for copilot (which takes the prompt as a `-p` arg) it
	// is empty.
	Stdin string
	// WorkingDir is the cwd for the subprocess. Claude sets it via cmd.Dir;
	// copilot also folds it into a `-C` flag (so it survives even if a caller
	// ignores cmd.Dir) — both are honoured.
	WorkingDir string
}

// classifiedEvent is the backend-neutral distillation of one streamed JSONL
// event. The runner's scan loop builds one per line via oracleBackend.Classify
// and feeds it to emitStreamEvent + the reply/usage assembly, so the loop never
// reads any CLI-specific JSON field directly.
type classifiedEvent struct {
	Type    string // event kind ("assistant" / "result" / "tool.execution_start" / …)
	Subtype string // optional sub-discriminator (claude "init"/"success"; "" for copilot)
	// Text is the assistant narration / reasoning prose for this event, full
	// and untruncated. Empty for tool-only, setup, and terminal events.
	Text string
	// Tool / ToolArgs describe the FIRST tool invocation in this event (a
	// compact preview of its args). Tools holds every tool invocation in
	// declaration order. Empty for non-tool events.
	Tool     string
	ToolArgs string
	Tools    []StreamToolUse
	// IsResult marks the terminal event of a run. ResultText, when non-empty,
	// is the authoritative final reply text carried by that event (claude
	// only; copilot leaves it empty and the reply is the last assistant
	// message's content).
	IsResult   bool
	ResultText string
	// SessionID is the agent session id surfaced by this event, if any.
	SessionID string
	// Usage / Cost are populated on the terminal result event. Usage is a
	// normalized token/credit map (claude: input_tokens/output_tokens/…;
	// copilot: premium_requests + durations). Cost is total_cost_usd (0 for
	// copilot, which reports no per-call dollar cost).
	Usage   map[string]any
	Cost    float64
	IsError bool
	// OutputTokens is the per-event output-token count, when the backend
	// reports it per message rather than only as a terminal total (copilot
	// stamps it on each assistant.message; claude leaves 0 and reports the
	// total on the result event instead). The runner sums these and, when the
	// terminal usage carries no output_tokens of its own, injects the sum.
	OutputTokens int
}

// oracleBackend is the single pluggable seam between kitsoki's oracle engine and
// the coding-agent CLI it forks. Implementations: claudeBackend (the identity /
// default) and copilotBackend.
type oracleBackend interface {
	// Name returns the stable backend id: "claude" or "copilot".
	Name() string

	// ResolveBin returns the path to the backend binary, honoring the
	// per-backend override env var and the test-stub seam. Returns
	// ErrOracleUnavailable when the binary is absent.
	ResolveBin(ctx context.Context) (string, error)

	// TranslateInvocation maps a claude-shaped invocation (the argv every verb
	// handler builds today, with the prompt on stdin and the working dir
	// separate) into this backend's concrete Invocation. The claude backend is
	// the identity; the copilot backend rewrites flags and relocates the prompt.
	TranslateInvocation(claudeArgs []string, stdin, workingDir string) Invocation

	// Classify distills one parsed JSONL event into a backend-neutral
	// classifiedEvent. Best-effort and defensive — unknown shapes return a
	// near-zero value carrying only the type.
	Classify(ev map[string]any) classifiedEvent

	// TranscriptFormat is the Format string stamped on the per-call transcript
	// sidecar ("claude-stream-json" | "copilot-jsonl").
	TranscriptFormat() string

	// ValidatorToolName returns the MCP tool name the model must call to submit
	// a schema-validated payload, given the advertised MCP server name.
	ValidatorToolName(server string) string

	// runnerFromContext returns the test-stub runner installed for this backend
	// (WithClaudeRunner / WithCopilotRunner), or nil for the real-exec path.
	runnerFromContext(ctx context.Context) ClaudeRunner
}

// oracleBackendCtxKey carries the selected backend on the context.
type oracleBackendCtxKey struct{}

// WithOracleBackend installs b as the oracle backend for every oracle/routing
// subprocess reached through the returned context. The orchestrator installs
// the user-selected backend per dispatch; absent that, the default is claude.
func WithOracleBackend(ctx context.Context, b oracleBackend) context.Context {
	if b == nil {
		return ctx
	}
	return context.WithValue(ctx, oracleBackendCtxKey{}, b)
}

// OracleBackendFromContext returns the backend installed in ctx, defaulting to
// the claude backend when none is present. This default is load-bearing: every
// pre-existing call site and test runs with no backend installed and MUST keep
// hitting the byte-identical claude path.
func OracleBackendFromContext(ctx context.Context) oracleBackend {
	if b, ok := ctx.Value(oracleBackendCtxKey{}).(oracleBackend); ok && b != nil {
		return b
	}
	return claudeBackend{}
}

// WithOracleBackendNamed installs the backend selected by name onto ctx. An
// empty or "claude" name is a no-op (the default backend is already claude), so
// the common path installs nothing. Exported for the orchestrator, which
// carries only the user-selected name string (the oracleBackend type is
// unexported).
func WithOracleBackendNamed(ctx context.Context, name string) context.Context {
	if name == "" || name == "claude" {
		return ctx
	}
	b, _ := ResolveOracleBackendName(name)
	return WithOracleBackend(ctx, b)
}

// ResolveOracleBackendName maps a user-facing backend name ("", "claude",
// "copilot") to a backend, defaulting to claude on empty/unknown so a typo
// degrades safely to the battle-tested path. ok reports whether name was a
// recognized non-empty selector (callers can warn on a typo).
func ResolveOracleBackendName(name string) (b oracleBackend, ok bool) {
	switch name {
	case "copilot":
		return copilotBackend{}, true
	case "claude", "":
		return claudeBackend{}, name == "claude"
	default:
		return claudeBackend{}, false
	}
}
