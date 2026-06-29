package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/google/uuid"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"kitsoki/internal/app"
	kitsokimcp "kitsoki/internal/mcp"
	"kitsoki/internal/sysprompt"
	"kitsoki/internal/trace"
)

// ErrClaudeCLIUnavailable is returned when the `claude` binary is not on PATH.
// The caller can use this to fall back to another harness or surface a helpful
// install message.
var ErrClaudeCLIUnavailable = errors.New("harness/claude-cli: `claude` binary not found on PATH; install Claude Code from https://claude.ai/download")

// DefaultClaudeModel is the model used when ClaudeCLIConfig.Model is empty.
// Haiku is faster and cheaper than Opus for intent-routing; users can override
// via WithClaudeModel or the --claude-model flag on `kitsoki run`.
const DefaultClaudeModel = "claude-haiku-4-5-20251001"

// validatorServerName is the MCP server name advertised to claude. The
// resulting tool name is `mcp__<server>__submit` — keep this in sync with
// the prompt instruction below.
const validatorServerName = "kitsoki-validator"

// validatorToolName is the full tool name the LLM must call. Built from
// validatorServerName + the validator server's default `submit` tool.
const validatorToolName = "mcp__" + validatorServerName + "__submit"

// ClaudeExec runs the `claude` binary one-shot. It receives the resolved
// binary path, the CLI args, and the prompt to pipe on stdin, and returns
// claude's stdout. It is the single seam through which the routing harness
// forks claude: the harness no longer execs claude itself.
//
// Production wires host.RunClaudeOneShotForHarness so intent routing shares the
// agent's invocation engine — stream/usage capture, the ClaudeRunner test
// seam, IDE-link scrub, and env handling all apply uniformly. Tests inject the
// same adapter (to exercise the real path) or a stub.
//
// The signature mirrors host.RunClaudeOneShotForHarness exactly so that bare
// function is directly assignable to this named type without host importing
// harness. workingDir is unused by routing (always "") but kept so the
// contract matches the canonical runner.
type ClaudeExec func(ctx context.Context, bin string, args []string, stdin, workingDir string) (stdout string, err error)

// ClaudeCLIConfig holds optional knobs for ClaudeCLIHarness.
type ClaudeCLIConfig struct {
	// Model is passed as --model to claude. If empty, DefaultClaudeModel is used.
	Model string
	// ClaudeBin overrides the path to the claude binary (used in tests).
	// If empty, exec.LookPath("claude") is used.
	ClaudeBin string
	// KitsokiBin overrides the path to the kitsoki binary used to spawn the
	// validator MCP server. If empty, os.Executable() is used. Tests set
	// this to a stub that mimics the validator's capture behavior.
	KitsokiBin string
	// Exec forks the claude binary. Required for routing: when nil, RunTurn
	// returns an error rather than forking claude through a private path.
	// Production wires host.RunClaudeOneShotForHarness at construction so all
	// claude invocations flow through the one canonical engine.
	Exec ClaudeExec
	// ValidatorTool overrides the MCP tool name the model is told to call in
	// the output contract. Empty uses validatorToolName (the claude
	// "mcp__<server>__submit" form). The copilot backend sets this to
	// "kitsoki-validator-submit" because copilot namespaces MCP tools as
	// "<server>-<tool>".
	ValidatorTool string
}

// validatorTool returns the configured submit-tool name, defaulting to the
// claude form when unset.
func (c ClaudeCLIConfig) validatorTool() string {
	if c.ValidatorTool != "" {
		return c.ValidatorTool
	}
	return validatorToolName
}

// ClaudeCLIHarness shells out to `claude -p` to route user text → IntentCall.
// It exists so kitsoki can route without an ANTHROPIC_API_KEY: the subprocess
// reuses the user's existing Claude Code login. Prompt composition reuses the
// same builders as LiveHarness so the prompt shape is identical across the two.
//
// Slot extraction rides on MCP rather than stdout scraping: the harness spawns
// the kitsoki binary itself as a validator subprocess (attached via
// --mcp-config) and instructs the model to call mcp__kitsoki-validator__submit.
// The validator writes the schema-validated payload to a side-channel file via
// atomic rename, so we never have to parse fences out of free-form output or
// beg the model for "raw JSON". Semantic slot formats (e.g. `format: jql`) are
// enforced by that validator, the same way the Agent host call enforces them.
//
// Invocation detail: the cold turn primes a persistent Claude session with the
// stable composed router prompt via --system-prompt; warm turns resume that
// session and pipe only the per-turn dynamic context/user text on stdin. The
// per-turn JSON Schema, the --mcp-config document, and the capture file all
// live in one tempdir removed on return.
type ClaudeCLIHarness struct {
	appDef        *app.AppDef
	cfg           ClaudeCLIConfig
	stablePrefix  string
	claudeSession claudeCLISessionState
	logger        *slog.Logger
}

type claudeCLISessionState struct {
	mu     sync.Mutex
	id     string
	primed bool
}

// NewClaudeCLI creates a ClaudeCLIHarness for the given app definition.
// It does NOT verify that the `claude` binary is present at construction time;
// the check happens in RunTurn so the error is contextual.
//
// If cfg.Model is empty, DefaultClaudeModel (haiku) is used for fast, cheap
// intent routing. Override with WithClaudeModel or the --claude-model flag.
func NewClaudeCLI(appDef *app.AppDef, cfg ClaudeCLIConfig) (*ClaudeCLIHarness, error) {
	if appDef == nil {
		return nil, errors.New("harness/claude-cli: app definition must not be nil")
	}
	if cfg.Model == "" {
		cfg.Model = DefaultClaudeModel
	}
	return &ClaudeCLIHarness{
		appDef:       appDef,
		cfg:          cfg,
		stablePrefix: buildStablePrefix(appDef),
		logger:       slog.Default(),
	}, nil
}

// AppDef returns the app definition this harness is currently using.
// Provided so tests and external callers can verify a hot-swap took
// effect after orchestrator.Reload.
func (h *ClaudeCLIHarness) AppDef() *app.AppDef { return h.appDef }

// SetAppDef swaps the app definition this harness uses to build prompts
// and recomputes the cached stable prefix. Used by orchestrator.Reload
// when the user authors changes via the in-TUI Edit mode. Not safe to
// call concurrently with RunTurn — Reload's own concurrency invariant
// (no in-flight turn while reloading) guards this.
func (h *ClaudeCLIHarness) SetAppDef(appDef *app.AppDef) {
	if appDef == nil {
		return
	}
	h.appDef = appDef
	h.stablePrefix = buildStablePrefix(appDef)
	h.resetClaudeSession()
}

// WithClaudeModel returns a copy of the harness using the given model.
// Pass an empty string to reset to DefaultClaudeModel.
func (h *ClaudeCLIHarness) WithClaudeModel(model string) *ClaudeCLIHarness {
	cfg := h.cfg
	if model == "" {
		cfg.Model = DefaultClaudeModel
	} else {
		cfg.Model = model
	}
	return &ClaudeCLIHarness{
		appDef:       h.appDef,
		cfg:          cfg,
		stablePrefix: h.stablePrefix,
		logger:       h.logger,
	}
}

// WithLogger sets the logger for trace emission.
func (h *ClaudeCLIHarness) WithLogger(l *slog.Logger) {
	if l != nil {
		h.logger = l
	}
}

// claudeJSONEnvelope is the outer JSON object emitted by `claude -p --output-format json`.
// We don't strictly need to parse the result text anymore (the side-channel
// capture file carries the canonical payload), but the envelope is still
// useful for surfacing exec errors.
type claudeJSONEnvelope struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	Result  string `json:"result"`
	IsError bool   `json:"is_error"`
}

// buildSubmitInstruction returns the output-contract block appended to every
// routing prompt. It tells the LLM the only way to "respond" is to call the
// validator's submit tool (named toolName, which differs per backend:
// "mcp__kitsoki-validator__submit" for claude, "kitsoki-validator-submit" for
// copilot) with a payload that matches the schema attached to that tool.
func buildSubmitInstruction(toolName string) string {
	return `

---

## Output Contract

You must call the tool ` + "`" + toolName + "`" + ` exactly once with an object of shape:

  {"intent": "<one of the allowed intent names>",
   "slots":  {"<slot>": <value>, ...},
   "confidence": 0.0}

The tool's input schema (attached to the tool itself) constrains the allowed
intent values, the slot shape per intent, and any semantic format checks
(e.g. JQL). If the call is rejected, fix the payload and call submit again.

Once submit returns OK, your turn is done — write at most a one-line
acknowledgement; do not repeat the JSON.
`
}

// RunTurn pipes the user utterance and app context to `claude -p` and extracts
// the resulting IntentCall via the MCP validator's side-channel capture file.
func (h *ClaudeCLIHarness) RunTurn(ctx context.Context, in TurnInput) (mcp.CallToolParams, error) {
	claudeBin, err := h.resolveBin()
	if err != nil {
		return mcp.CallToolParams{}, err
	}
	kitsokiBin, err := h.resolveKitsokiBin()
	if err != nil {
		return mcp.CallToolParams{}, err
	}

	l := h.logger.With(
		slog.String("session_id", string(in.SessionID)),
		slog.Int64("turn", int64(in.TurnNumber)),
		slog.String("state_path", string(in.StatePath)),
	)

	// Per-turn schema, written to a tempdir alongside the --mcp-config
	// document and the side-channel capture file.
	schemaBytes, err := BuildTransitionSchema(h.appDef, in.AllowedIntents)
	if err != nil {
		return mcp.CallToolParams{}, fmt.Errorf("harness/claude-cli: build transition schema: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "kitsoki-claude-cli-*")
	if err != nil {
		return mcp.CallToolParams{}, fmt.Errorf("harness/claude-cli: mkdir tmp: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	schemaPath := filepath.Join(tmpDir, "schema.json")
	if err := os.WriteFile(schemaPath, schemaBytes, 0o600); err != nil {
		return mcp.CallToolParams{}, fmt.Errorf("harness/claude-cli: write schema: %w", err)
	}

	capturePath := filepath.Join(tmpDir, "capture.json")
	// Don't pre-create capturePath; the validator writes via atomic
	// rename, and absence-after-exit is the "LLM never called submit" signal.

	configPath := filepath.Join(tmpDir, "config.json")
	mcpConfig := map[string]any{
		"mcpServers": map[string]any{
			validatorServerName: map[string]any{
				"command": kitsokiBin,
				"args": []any{
					"mcp-validator",
					"--schema", schemaPath,
					"--output", capturePath,
				},
			},
		},
	}
	configBytes, err := json.Marshal(mcpConfig)
	if err != nil {
		return mcp.CallToolParams{}, fmt.Errorf("harness/claude-cli: marshal mcp-config: %w", err)
	}
	if err := os.WriteFile(configPath, configBytes, 0o600); err != nil {
		return mcp.CallToolParams{}, fmt.Errorf("harness/claude-cli: write mcp-config: %w", err)
	}

	claudeSessionID, resumeClaudeSession := h.prepareClaudeSession()
	systemPrompt := ""
	if !resumeClaudeSession {
		// The stable router prefix + output contract are turn-invariant, so the
		// cold dispatch primes a persistent Claude session with them as the
		// system prompt. Warm dispatches resume that session and send only the
		// per-turn context/user input on stdin.
		//
		// Routing composes through the same layered builder as every agent verb
		// (internal/sysprompt): the kitsoki grounding (Layer 1) and any project
		// context (Layer 2) are prepended to the routing prefix + output contract
		// (Layer 3), so the router is grounded identically to the rest of the system.
		composed := sysprompt.Compose(sysprompt.Spec{
			Verb:    sysprompt.Route,
			Project: projectLayer(h.appDef),
			Task:    h.stablePrefix + buildSubmitInstruction(h.cfg.validatorTool()),
		})
		systemPrompt = composed.SystemPrompt
	}
	dynamic := buildDynamicSuffix(h.appDef, in)
	userMessage := dynamic + "\n## User Input\n\n" + in.UserText + "\n"

	args := buildClaudeArgs(h.cfg, configPath, systemPrompt, claudeSessionID, resumeClaudeSession)
	if l.Enabled(ctx, slog.LevelDebug) {
		l.DebugContext(ctx, trace.EvHarnessRequest,
			slog.Int("system_prompt_bytes", len(systemPrompt)),
			slog.Int("user_message_bytes", len(userMessage)),
			slog.Bool("claude_session_resume", resumeClaudeSession),
			slog.String("claude_session_id", claudeSessionID),
			slog.String("prompt_head", trace.Truncate(systemPrompt, trace.TruncateCap)),
		)
		l.DebugContext(ctx, trace.EvHarnessExec,
			slog.String("bin", claudeBin),
			slog.Any("args", args),
		)
	}

	if h.cfg.Exec == nil {
		return mcp.CallToolParams{}, errors.New("harness/claude-cli: no ClaudeExec injected; wire host.RunClaudeOneShotForHarness at construction")
	}
	stdout, runErr := h.cfg.Exec(ctx, claudeBin, args, userMessage, "")
	raw := []byte(stdout)
	if runErr != nil {
		l.DebugContext(ctx, trace.EvHarnessError, slog.String("error", runErr.Error()))
		return mcp.CallToolParams{}, runErr
	}
	if l.Enabled(ctx, slog.LevelDebug) {
		l.DebugContext(ctx, trace.EvHarnessResponseRaw,
			slog.String("raw", trace.Truncate(string(raw), trace.TruncateCap)),
		)
	}

	// Read the side-channel capture file. Empty/missing → the LLM answered
	// without calling submit. Return a ClarifyResponse so the orchestrator
	// surfaces the LLM's free-form text to the user as a soft clarification
	// rather than a red technical error. The trace still gets the technical
	// form via the wrapped Underlying error.
	captured, readErr := kitsokimcp.ReadCapturedPayload(capturePath)
	if readErr != nil || len(captured) == 0 {
		var env claudeJSONEnvelope
		_ = json.Unmarshal(raw, &env)
		message := strings.TrimSpace(env.Result)
		underlying := fmt.Errorf(
			"harness/claude-cli: LLM did not call %s (no validated payload captured); model said: %q",
			h.cfg.validatorTool(), truncate(message, 200),
		)
		return mcp.CallToolParams{}, &ClarifyResponse{
			Message:    message,
			Underlying: underlying,
		}
	}

	params, parseErr := parseValidatedPayload(captured)
	if parseErr != nil {
		return mcp.CallToolParams{}, fmt.Errorf("harness/claude-cli: parse validated payload: %w", parseErr)
	}
	if !resumeClaudeSession {
		h.markClaudeSessionPrimed(claudeSessionID)
	}

	intentName, slots, confidence := parseTransitionArgs(params)
	l.DebugContext(ctx, trace.EvHarnessResponseParsed,
		slog.String("intent", intentName),
		slog.Any("slots", slots),
		slog.Float64("confidence", confidence),
	)
	return params, nil
}

// buildClaudeArgs returns the CLI argument list for `claude -p`. configPath
// is the --mcp-config file path. Cold calls pass systemPrompt via
// --system-prompt and pin the new persistent conversation with --session-id;
// warm calls pass --resume and intentionally omit systemPrompt.
//
// systemPrompt is passed via --system-prompt, which *replaces* Claude Code's
// built-in default system prompt rather than stacking on top of it (the
// append-mode flag is --append-system-prompt, which we deliberately do not
// use). --exclude-dynamic-system-prompt-sections additionally strips the
// per-machine sections claude would otherwise inject (cwd, env info, memory
// paths, git status) — all irrelevant to intent routing. The net effect is
// that a cold routing turn primes the session with kitsoki's own lean router
// prefix, and warm turns reuse that prefix without resending it.
func buildClaudeArgs(cfg ClaudeCLIConfig, configPath, systemPrompt, sessionID string, resume bool) []string {
	model := cfg.Model
	if model == "" {
		model = DefaultClaudeModel
	}
	args := []string{
		"-p",
		"--output-format", "json",
		"--permission-mode", "bypassPermissions",
		"--model", model,
	}
	if resume {
		if sessionID != "" {
			args = append(args, "--resume", sessionID)
		}
	} else if sessionID != "" {
		args = append(args, "--session-id", sessionID)
	}
	if !resume && systemPrompt != "" {
		args = append(args,
			"--system-prompt", systemPrompt,
			"--exclude-dynamic-system-prompt-sections",
		)
	}
	if configPath != "" {
		args = append(args, "--mcp-config", configPath)
	}
	return args
}

func (h *ClaudeCLIHarness) prepareClaudeSession() (sessionID string, resume bool) {
	h.claudeSession.mu.Lock()
	defer h.claudeSession.mu.Unlock()

	if h.claudeSession.primed && h.claudeSession.id != "" {
		return h.claudeSession.id, true
	}
	sessionID = uuid.NewString()
	h.claudeSession.id = sessionID
	h.claudeSession.primed = false
	return sessionID, false
}

func (h *ClaudeCLIHarness) markClaudeSessionPrimed(sessionID string) {
	h.claudeSession.mu.Lock()
	defer h.claudeSession.mu.Unlock()

	if h.claudeSession.id == sessionID {
		h.claudeSession.primed = true
	}
}

func (h *ClaudeCLIHarness) resetClaudeSession() {
	h.claudeSession.mu.Lock()
	defer h.claudeSession.mu.Unlock()

	h.claudeSession.id = ""
	h.claudeSession.primed = false
}

// parseValidatedPayload decodes the JSON written by the MCP validator on a
// successful submit. The schema we generate guarantees the `intent` field;
// `slots` may be omitted (treated as empty) and `confidence` is optional.
func parseValidatedPayload(raw []byte) (mcp.CallToolParams, error) {
	var schema struct {
		Intent     string         `json:"intent"`
		Slots      map[string]any `json:"slots"`
		Confidence float64        `json:"confidence,omitempty"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		return mcp.CallToolParams{}, fmt.Errorf("decode payload: %w (raw=%q)", err, truncate(string(raw), 200))
	}
	if schema.Intent == "" {
		return mcp.CallToolParams{}, fmt.Errorf("validated payload missing \"intent\" field (raw=%q)", truncate(string(raw), 200))
	}
	args := map[string]any{
		"intent": schema.Intent,
	}
	if schema.Slots != nil {
		args["slots"] = schema.Slots
	} else {
		args["slots"] = map[string]any{}
	}
	if schema.Confidence != 0 {
		args["confidence"] = schema.Confidence
	}
	return mcp.CallToolParams{
		Name:      "transition",
		Arguments: args,
	}, nil
}

// resolveBin returns the path to the claude binary, checking ClaudeBin override
// first and then PATH. Returns ErrClaudeCLIUnavailable if not found.
func (h *ClaudeCLIHarness) resolveBin() (string, error) {
	if h.cfg.ClaudeBin != "" {
		if _, err := os.Stat(h.cfg.ClaudeBin); err != nil {
			return "", fmt.Errorf("%w (ClaudeBin=%q: %v)", ErrClaudeCLIUnavailable, h.cfg.ClaudeBin, err)
		}
		return h.cfg.ClaudeBin, nil
	}
	path, err := exec.LookPath("claude")
	if err != nil {
		return "", ErrClaudeCLIUnavailable
	}
	return path, nil
}

// resolveKitsokiBin returns the path used to spawn the validator MCP server.
// Tests override via ClaudeCLIConfig.KitsokiBin; production uses os.Executable().
func (h *ClaudeCLIHarness) resolveKitsokiBin() (string, error) {
	if h.cfg.KitsokiBin != "" {
		if _, err := os.Stat(h.cfg.KitsokiBin); err != nil {
			return "", fmt.Errorf("harness/claude-cli: KitsokiBin=%q: %w", h.cfg.KitsokiBin, err)
		}
		return h.cfg.KitsokiBin, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("harness/claude-cli: locate kitsoki binary: %w", err)
	}
	return exe, nil
}

// projectLayer resolves the app's Layer-2 project grounding for the router
// (sysprompt Layer 2). It reads app.context (inline) or app.context_path (a file
// relative to KITSOKI_APP_DIR) as raw text. Unlike the agent path
// (internal/host/sysprompt.go), routing does not render the project context
// through the overlay/@shared template machinery or honour the
// prompts/_project.md convention — the harness has no prompt renderer wired —
// so routing supports the inline/file forms only. Returns "" when neither is
// set or the file is unreadable.
func projectLayer(appDef *app.AppDef) string {
	if appDef == nil {
		return ""
	}
	if c := strings.TrimSpace(appDef.App.Context); c != "" {
		return c
	}
	p := strings.TrimSpace(appDef.App.ContextPath)
	if p == "" {
		return ""
	}
	abs := p
	if !filepath.IsAbs(abs) {
		if dir := os.Getenv("KITSOKI_APP_DIR"); dir != "" {
			abs = filepath.Join(dir, p)
		}
	}
	body, err := os.ReadFile(abs)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(body))
}

// Close is a no-op for ClaudeCLIHarness (no persistent resources).
func (h *ClaudeCLIHarness) Close() error { return nil }

// truncate returns at most n characters from s, appending "..." if truncated.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
