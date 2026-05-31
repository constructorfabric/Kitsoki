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

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"kitsoki/internal/app"
	kitsokimcp "kitsoki/internal/mcp"
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
// enforced by that validator, the same way the Oracle host call enforces them.
//
// Invocation detail: the complete prompt is piped via stdin to avoid argv size
// limits; the per-turn JSON Schema, the --mcp-config document, and the capture
// file all live in one tempdir removed on return.
type ClaudeCLIHarness struct {
	appDef       *app.AppDef
	cfg          ClaudeCLIConfig
	stablePrefix string
	logger       *slog.Logger
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
}

// WithClaudeModel returns a copy of the harness using the given model.
// Pass an empty string to reset to DefaultClaudeModel.
func (h *ClaudeCLIHarness) WithClaudeModel(model string) *ClaudeCLIHarness {
	copy := *h
	if model == "" {
		copy.cfg.Model = DefaultClaudeModel
	} else {
		copy.cfg.Model = model
	}
	return &copy
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

// submitInstruction is appended to every prompt. It tells the LLM the only
// way to "respond" is to call the validator's submit tool with a payload
// that matches the schema attached to that tool.
const submitInstruction = `

---

## Output Contract

You must call the tool ` + "`" + validatorToolName + "`" + ` exactly once with an object of shape:

  {"intent": "<one of the allowed intent names>",
   "slots":  {"<slot>": <value>, ...},
   "confidence": 0.0}

The tool's input schema (attached to the tool itself) constrains the allowed
intent values, the slot shape per intent, and any semantic format checks
(e.g. JQL). If the call is rejected, fix the payload and call submit again.

Once submit returns OK, your turn is done — write at most a one-line
acknowledgement; do not repeat the JSON.
`

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

	dynamic := buildDynamicSuffix(h.appDef, in)
	prompt := h.stablePrefix + dynamic + submitInstruction +
		"\n## User Input\n\n" + in.UserText + "\n"

	args := buildClaudeArgs(h.cfg, configPath)
	if l.Enabled(ctx, slog.LevelDebug) {
		l.DebugContext(ctx, trace.EvHarnessRequest,
			slog.Int("prompt_bytes", len(prompt)),
			slog.String("prompt_head", trace.Truncate(prompt, trace.TruncateCap)),
		)
		l.DebugContext(ctx, trace.EvHarnessExec,
			slog.String("bin", claudeBin),
			slog.Any("args", args),
		)
	}

	raw, runErr := h.invoke(ctx, claudeBin, args, prompt)
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
			"harness/claude-cli: LLM did not call %s (no validated payload captured); claude said: %q",
			validatorToolName, truncate(message, 200),
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

	intentName, slots, confidence := parseTransitionArgs(params)
	l.DebugContext(ctx, trace.EvHarnessResponseParsed,
		slog.String("intent", intentName),
		slog.Any("slots", slots),
		slog.Float64("confidence", confidence),
	)
	return params, nil
}

// buildClaudeArgs returns the CLI argument list for `claude -p`. configPath
// is the --mcp-config file path. The model is always included.
func buildClaudeArgs(cfg ClaudeCLIConfig, configPath string) []string {
	model := cfg.Model
	if model == "" {
		model = DefaultClaudeModel
	}
	args := []string{
		"-p",
		"--output-format", "json",
		"--no-session-persistence",
		"--permission-mode", "bypassPermissions",
		"--model", model,
	}
	if configPath != "" {
		args = append(args, "--mcp-config", configPath)
	}
	return args
}

// invoke runs `claude -p ...` with the prompt piped via stdin. Returns raw stdout.
func (h *ClaudeCLIHarness) invoke(ctx context.Context, claudeBin string, args []string, prompt string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, claudeBin, args...)
	cmd.Stdin = strings.NewReader(prompt)

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		stderrText := strings.TrimSpace(stderr.String())
		if stderrText != "" {
			return nil, fmt.Errorf("harness/claude-cli: claude exited with error: %w\nstderr: %s", err, stderrText)
		}
		return nil, fmt.Errorf("harness/claude-cli: claude exited with error: %w", err)
	}
	return []byte(stdout.String()), nil
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

// Close is a no-op for ClaudeCLIHarness (no persistent resources).
func (h *ClaudeCLIHarness) Close() error { return nil }

// truncate returns at most n characters from s, appending "..." if truncated.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
