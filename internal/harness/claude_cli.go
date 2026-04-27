// Package harness — ClaudeCLIHarness implementation (§10.5).
// Shells out to the `claude -p` CLI to route a user utterance to an IntentCall.
// This avoids requiring ANTHROPIC_API_KEY: it picks up the user's existing
// Claude Code login instead.
package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"hally/internal/app"
	"hally/internal/trace"
)

// ErrClaudeCLIUnavailable is returned when the `claude` binary is not on PATH.
// The caller can use this to fall back to another harness or surface a helpful
// install message.
var ErrClaudeCLIUnavailable = errors.New("harness/claude-cli: `claude` binary not found on PATH; install Claude Code from https://claude.ai/download")

// DefaultClaudeModel is the model used when ClaudeCLIConfig.Model is empty.
// Haiku is faster and cheaper than Opus for intent-routing; users can override
// via WithClaudeModel or the --claude-model flag on `hally run`.
const DefaultClaudeModel = "claude-haiku-4-5-20251001"

// ClaudeCLIConfig holds optional knobs for ClaudeCLIHarness.
type ClaudeCLIConfig struct {
	// Model is passed as --model to claude. If empty, DefaultClaudeModel is used.
	Model string
	// MaxRetries is the number of parse-failure retries. Default 2 (3 total attempts).
	MaxRetries int
	// ClaudeBin overrides the path to the claude binary (used in tests).
	// If empty, exec.LookPath("claude") is used.
	ClaudeBin string
}

// ClaudeCLIHarness shells out to `claude -p` to route user text → IntentCall.
// Prompt composition reuses buildStablePrefix and buildDynamicSuffix from prompt.go
// so the prompt shape is identical to LiveHarness.
//
// Invocation: the complete prompt (stable prefix + dynamic suffix + JSON instruction
// + user utterance) is piped to claude via stdin, which avoids argv size limits on
// large apps. --output-format json causes claude to emit a JSON envelope whose
// "result" field contains the assistant text. We parse the JSON object from "result".
//
// Retry on parse failure: up to cfg.MaxRetries additional attempts (default 2),
// each prefixing the prompt with an explanation of what went wrong.
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
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = 2
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
type claudeJSONEnvelope struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	Result  string `json:"result"`
	IsError bool   `json:"is_error"`
}

// claudeResponseSchema is the JSON object we expect the LLM to return inside the envelope.
type claudeResponseSchema struct {
	Intent     string         `json:"intent"`
	Slots      map[string]any `json:"slots"`
	Confidence float64        `json:"confidence,omitempty"`
}

// jsonInstruction is appended to every prompt to tell the LLM the exact
// output format. The prose below is deliberately emphatic and repeats the
// "no fences" rule several times because Haiku 4.5 (the default intent
// router) has been observed to wrap JSON in ` + "```" + `json fences even when told
// not to — seeing fenced code blocks earlier in the prompt (the rendered
// view is wrapped in fences by buildDynamicSuffix) primes the model to
// mirror that style on output. Leaving the stripper as a safety net.
const jsonInstruction = `
---

## CRITICAL OUTPUT INSTRUCTION

You are acting as a JSON API endpoint, not a conversational assistant.
Your ENTIRE response must be a single raw JSON object. Nothing else.

The JSON must match this exact schema:

{
  "intent": "<one of the allowed intent names above>",
  "slots": { "<slot_name>": "<value>", ... },
  "confidence": 0.0
}

The "slots" field is required (use {} if no slots). The "confidence" field is optional.

### OUTPUT FORMAT RULES — READ CAREFULLY

- Your response starts with the character ` + "`{`" + ` and ends with the character ` + "`}`" + `.
- Do NOT wrap the output in ` + "`" + "```" + "`" + ` fences of any kind.
- Do NOT prefix the output with ` + "`" + "```" + "json`" + ` or any language tag.
- Do NOT add any prose, explanation, heading, or commentary before or after the JSON.
- Do NOT mirror the fenced code blocks that appear in the context above —
  those are the USER's view, not a template for your reply.

### Examples

CORRECT output (copy this style):
{"intent":"go","slots":{"direction":"south"},"confidence":0.9}

WRONG — do not do this:
` + "```" + `json
{"intent":"go","slots":{"direction":"south"}}
` + "```" + `

WRONG — do not do this either:
Here is the JSON: {"intent":"go","slots":{"direction":"south"}}
`

// retryInstruction is prepended when a previous attempt produced invalid JSON.
const retryInstruction = `Your last response was not valid JSON. Return ONLY a JSON object matching this schema:
{"intent":"<name>","slots":{...},"confidence":0.0}
No fences, no prose. The JSON object must be the entire response.

`

// RunTurn pipes the user utterance and app context to `claude -p` and extracts
// the resulting IntentCall as a mcp.CallToolParams.
//
// Retry policy: up to cfg.MaxRetries additional attempts on JSON parse failure.
func (h *ClaudeCLIHarness) RunTurn(ctx context.Context, in TurnInput) (mcp.CallToolParams, error) {
	// Resolve binary path.
	claudeBin, err := h.resolveBin()
	if err != nil {
		return mcp.CallToolParams{}, err
	}

	l := h.logger.With(
		slog.String("session_id", string(in.SessionID)),
		slog.Int64("turn", int64(in.TurnNumber)),
		slog.String("state_path", string(in.StatePath)),
	)

	dynamic := buildDynamicSuffix(h.appDef, in)
	basePrompt := h.stablePrefix + dynamic + jsonInstruction +
		"\n## User Input\n\n" + in.UserText + "\n"

	if l.Enabled(ctx, slog.LevelDebug) {
		l.DebugContext(ctx, trace.EvHarnessRequest,
			slog.Int("prompt_bytes", len(basePrompt)),
			slog.String("prompt_head", trace.Truncate(basePrompt, trace.TruncateCap)),
		)
	}

	var lastParseErr error
	for attempt := 0; attempt <= h.cfg.MaxRetries; attempt++ {
		prompt := basePrompt
		if attempt > 0 {
			prompt = retryInstruction + basePrompt
			slog.Warn("harness/claude-cli: retrying after parse failure",
				"attempt", attempt, "err", lastParseErr)
			l.DebugContext(ctx, trace.EvHarnessRetry,
				slog.Int("attempt", attempt),
				slog.String("reason", lastParseErr.Error()),
			)
		}

		args := buildClaudeArgs(h.cfg)
		l.DebugContext(ctx, trace.EvHarnessExec,
			slog.String("bin", claudeBin),
			slog.Any("args", args),
		)

		raw, err := h.invoke(ctx, claudeBin, prompt)
		if err != nil {
			l.DebugContext(ctx, trace.EvHarnessError, slog.String("error", err.Error()))
			return mcp.CallToolParams{}, err
		}

		if l.Enabled(ctx, slog.LevelDebug) {
			l.DebugContext(ctx, trace.EvHarnessResponseRaw,
				slog.String("raw", trace.Truncate(string(raw), trace.TruncateCap)),
			)
		}

		params, parseErr := parseClaudeEnvelope(raw)
		if parseErr == nil {
			intentName, slots, confidence := parseTransitionArgs(params)
			l.DebugContext(ctx, trace.EvHarnessResponseParsed,
				slog.String("intent", intentName),
				slog.Any("slots", slots),
				slog.Float64("confidence", confidence),
			)
			return params, nil
		}
		lastParseErr = parseErr
	}

	return mcp.CallToolParams{}, fmt.Errorf("harness/claude-cli: all %d attempts failed, last parse error: %w",
		h.cfg.MaxRetries+1, lastParseErr)
}

// buildClaudeArgs returns the CLI argument list (without prompt) for trace logging.
// The model is always included: either cfg.Model or DefaultClaudeModel.
func buildClaudeArgs(cfg ClaudeCLIConfig) []string {
	model := cfg.Model
	if model == "" {
		model = DefaultClaudeModel
	}
	return []string{"-p", "--output-format", "json", "--no-session-persistence", "--model", model}
}

// invoke runs `claude -p --output-format json` with the prompt piped via stdin.
// Returns the raw stdout bytes.
func (h *ClaudeCLIHarness) invoke(ctx context.Context, claudeBin, prompt string) ([]byte, error) {
	args := buildClaudeArgs(h.cfg)

	cmd := exec.CommandContext(ctx, claudeBin, args...)
	cmd.Stdin = strings.NewReader(prompt)

	// Capture stdout and stderr separately.
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// Check for context cancellation / timeout.
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

// parseClaudeEnvelope decodes the JSON envelope from `claude -p --output-format json`
// and extracts the IntentCall from the "result" field.
//
// Tolerances:
//   - Leading/trailing whitespace around the JSON object in "result" is stripped.
//   - A single ```json ... ``` fence in "result" is stripped (with a warning log).
//   - The "slots" field may be absent (treated as empty map).
func parseClaudeEnvelope(raw []byte) (mcp.CallToolParams, error) {
	// Decode the outer envelope.
	var env claudeJSONEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return mcp.CallToolParams{}, fmt.Errorf("harness/claude-cli: decode envelope: %w (raw=%q)", err, truncate(string(raw), 200))
	}

	if env.Type != "result" {
		return mcp.CallToolParams{}, fmt.Errorf("harness/claude-cli: unexpected envelope type %q (want \"result\")", env.Type)
	}
	if env.Subtype != "success" || env.IsError {
		return mcp.CallToolParams{}, fmt.Errorf("harness/claude-cli: envelope indicates failure (subtype=%q is_error=%v result=%q)",
			env.Subtype, env.IsError, truncate(env.Result, 200))
	}
	if env.Result == "" {
		return mcp.CallToolParams{}, errors.New("harness/claude-cli: envelope result field is empty")
	}

	// Extract the JSON object from the result string.
	resultText, fenced := stripFence(strings.TrimSpace(env.Result))
	if fenced {
		// Demoted from Warn to Debug: Haiku 4.5 routinely wraps JSON in
		// ```json fences despite explicit anti-fence prompt instructions.
		// The stripper is doing the right thing; the event is not an
		// author error worth surfacing every turn.
		slog.Debug("harness/claude-cli: result contained markdown fence; stripped")
	}

	// Find the JSON object boundaries.
	jsonText, err := extractJSONObject(resultText)
	if err != nil {
		return mcp.CallToolParams{}, fmt.Errorf("harness/claude-cli: extract JSON from result: %w (result=%q)", err, truncate(resultText, 200))
	}

	// Decode the transition schema.
	var schema claudeResponseSchema
	if err := json.Unmarshal([]byte(jsonText), &schema); err != nil {
		return mcp.CallToolParams{}, fmt.Errorf("harness/claude-cli: decode transition JSON: %w (json=%q)", err, truncate(jsonText, 200))
	}
	if schema.Intent == "" {
		return mcp.CallToolParams{}, fmt.Errorf("harness/claude-cli: transition JSON missing \"intent\" field (json=%q)", truncate(jsonText, 200))
	}

	// Build CallToolParams.
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

// stripFence removes a single ```json ... ``` (or ```) markdown fence from s.
// Returns the stripped string and true if a fence was present.
func stripFence(s string) (string, bool) {
	// Try ```json\n...\n``` first, then ```.
	for _, prefix := range []string{"```json\n", "```\n", "```json", "```"} {
		if strings.HasPrefix(s, prefix) {
			inner := strings.TrimPrefix(s, prefix)
			// Strip closing fence.
			if idx := strings.LastIndex(inner, "```"); idx >= 0 {
				inner = strings.TrimSpace(inner[:idx])
			}
			return inner, true
		}
	}
	return s, false
}

// extractJSONObject finds the first complete JSON object in s, starting from
// the first '{' and ending at the matching '}'. Returns an error if no object
// is found or the braces are unbalanced.
func extractJSONObject(s string) (string, error) {
	start := strings.Index(s, "{")
	if start < 0 {
		return "", fmt.Errorf("no JSON object found in string")
	}
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(s); i++ {
		ch := s[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && inString {
			escaped = true
			continue
		}
		if ch == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		if ch == '{' {
			depth++
		} else if ch == '}' {
			depth--
			if depth == 0 {
				return s[start : i+1], nil
			}
		}
	}
	return "", fmt.Errorf("unbalanced braces in JSON object")
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

// Close is a no-op for ClaudeCLIHarness (no persistent resources).
func (h *ClaudeCLIHarness) Close() error { return nil }

// truncate returns at most n characters from s, appending "..." if truncated.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
