// Package harness — LiveHarness implementation (§10.5, §12.1).
// Calls the Anthropic Messages API to route a user utterance to an IntentCall.
// No MCP dependency: the LLM is forced to call a local "transition" tool and
// the result is parsed into a mcp.CallToolParams.
package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"hally/internal/app"
	"hally/internal/trace"
)

// LiveConfig holds optional knobs for the LiveHarness.
type LiveConfig struct {
	// MaxTokens caps the response size. Default 512.
	MaxTokens int64
	// Temperature controls LLM randomness. Default 0 (deterministic envelope).
	Temperature float64
	// MaxRetries is the number of transient-error retries. Default 3.
	MaxRetries int
}

// LiveHarness calls the real Anthropic API to route user text → IntentCall.
// It declares a single local "transition" tool and forces the LLM to call it
// with tool_choice = {type: "tool", name: "transition"}.
//
// System prompt structure (for prompt caching):
//
//	[stable prefix — app name, description, tool schema] ← cache_control: ephemeral
//	[dynamic suffix — current state, allowed intents, world context]
type LiveHarness struct {
	client *anthropic.Client
	model  string
	appDef *app.AppDef
	cfg    LiveConfig
	logger *slog.Logger

	// stablePrefix is the rendered stable portion of the system prompt.
	// It never changes for a given app, so it qualifies for Anthropic's
	// prompt caching (ephemeral breakpoint, min ~1024 tokens threshold).
	stablePrefix string
}

// UsageInfo records token usage for a single LLM call (used by RecordingHarness).
type UsageInfo struct {
	InputTokens       int64
	OutputTokens      int64
	CacheReadTokens   int64
	CacheCreateTokens int64
	CacheHit          bool
}

// NewLive creates a LiveHarness for the given app definition.
// model defaults to claude-sonnet-4-5 if empty.
func NewLive(client *anthropic.Client, model string, appDef *app.AppDef) (*LiveHarness, error) {
	if client == nil {
		return nil, errors.New("harness/live: anthropic client must not be nil")
	}
	if appDef == nil {
		return nil, errors.New("harness/live: app definition must not be nil")
	}
	if model == "" {
		model = string(anthropic.ModelClaudeSonnet4_5)
	}
	cfg := LiveConfig{
		MaxTokens:   512,
		Temperature: 0,
		MaxRetries:  3,
	}
	h := &LiveHarness{
		client: client,
		model:  model,
		appDef: appDef,
		cfg:    cfg,
		logger: slog.Default(),
	}
	h.stablePrefix = buildStablePrefix(appDef)
	return h, nil
}

// WithLogger sets the logger for trace emission.
func (h *LiveHarness) WithLogger(l *slog.Logger) {
	if l != nil {
		h.logger = l
	}
}

// transitionToolInputSchema is the JSON schema for the local transition tool.
var transitionToolInputSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"intent": map[string]any{
			"type":        "string",
			"description": "One of the currently-allowed intent names (see system context).",
		},
		"slots": map[string]any{
			"type":        "object",
			"description": "Intent-specific slot values. Keys match the intent's declared slot names.",
		},
		"confidence": map[string]any{
			"type":        "number",
			"description": "0..1 self-reported extraction confidence (optional).",
		},
	},
	"required": []string{"intent"},
}

// RunTurn sends the user utterance to the Anthropic API and extracts the
// tool_use block as a mcp.CallToolParams.
//
// Retry policy: up to cfg.MaxRetries attempts on 429 / 5xx with exponential backoff.
func (h *LiveHarness) RunTurn(ctx context.Context, in TurnInput) (mcp.CallToolParams, error) {
	l := h.logger.With(
		slog.String("session_id", string(in.SessionID)),
		slog.Int64("turn", int64(in.TurnNumber)),
		slog.String("state_path", string(in.StatePath)),
	)

	// Build system prompt.
	stable := h.stablePrefix
	dynamic := buildDynamicSuffix(h.appDef, in)

	// Tool definition for the local (non-MCP) Anthropic tool call.
	// The input schema is passed via ExtraFields so we can embed the full JSON schema
	// structure directly.
	tool := anthropic.ToolUnionParam{
		OfTool: &anthropic.ToolParam{
			Name:        "transition",
			Description: param.NewOpt("Map the user utterance to one allowed intent with filled slots."),
			InputSchema: anthropic.ToolInputSchemaParam{
				ExtraFields: map[string]any{
					"properties": transitionToolInputSchema["properties"],
					"required":   transitionToolInputSchema["required"],
				},
			},
		},
	}

	userMsg := anthropic.NewUserMessage(anthropic.NewTextBlock(in.UserText))

	// Force tool use: the LLM must call exactly the "transition" tool.
	toolChoice := anthropic.ToolChoiceParamOfTool("transition")

	fullPrompt := stable + dynamic
	if l.Enabled(ctx, slog.LevelDebug) {
		l.DebugContext(ctx, trace.EvHarnessRequest,
			slog.Int("prompt_bytes", len(fullPrompt)),
			slog.String("model", h.model),
			slog.String("prompt_head", trace.Truncate(fullPrompt, trace.TruncateCap)),
		)
	}

	var lastErr error
	for attempt := 0; attempt < h.cfg.MaxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff: 500ms, 1s, 2s.
			wait := time.Duration(500<<uint(attempt-1)) * time.Millisecond
			l.DebugContext(ctx, trace.EvHarnessRetry,
				slog.Int("attempt", attempt),
				slog.String("reason", lastErr.Error()),
			)
			select {
			case <-ctx.Done():
				return mcp.CallToolParams{}, ctx.Err()
			case <-time.After(wait):
			}
		}

		resp, err := h.client.Messages.New(ctx, anthropic.MessageNewParams{
			Model:     anthropic.Model(h.model),
			MaxTokens: h.cfg.MaxTokens,
			System: []anthropic.TextBlockParam{
				{
					Text:         fullPrompt,
					CacheControl: anthropic.NewCacheControlEphemeralParam(),
				},
			},
			Tools:       []anthropic.ToolUnionParam{tool},
			ToolChoice:  toolChoice,
			Messages:    []anthropic.MessageParam{userMsg},
			Temperature: param.NewOpt(h.cfg.Temperature),
		})
		if err != nil {
			// Classify error for retry.
			if isRetryable(err) {
				slog.Warn("harness/live: retryable error", "attempt", attempt+1, "err", err)
				l.DebugContext(ctx, trace.EvHarnessRetry,
					slog.Int("attempt", attempt+1),
					slog.String("reason", err.Error()),
				)
				lastErr = err
				continue
			}
			l.DebugContext(ctx, trace.EvHarnessError, slog.String("error", err.Error()))
			return mcp.CallToolParams{}, fmt.Errorf("harness/live: messages.new: %w", err)
		}

		// Log usage.
		usage := resp.Usage
		slog.Debug("harness/live: usage",
			"input_tokens", usage.InputTokens,
			"output_tokens", usage.OutputTokens,
			"cache_read_tokens", usage.CacheReadInputTokens,
			"cache_create_tokens", usage.CacheCreationInputTokens,
		)

		// Extract tool_use block.
		params, err := extractToolCall(resp)
		if err != nil {
			return mcp.CallToolParams{}, fmt.Errorf("harness/live: extract tool call: %w", err)
		}

		intentName, slots, confidence := parseTransitionArgs(params)
		l.DebugContext(ctx, trace.EvHarnessResponseParsed,
			slog.String("intent", intentName),
			slog.Any("slots", slots),
			slog.Float64("confidence", confidence),
		)

		return params, nil
	}

	return mcp.CallToolParams{}, fmt.Errorf("harness/live: all %d attempts failed, last error: %w", h.cfg.MaxRetries, lastErr)
}

// extractToolCall pulls the tool_use block out of the Anthropic response.
func extractToolCall(resp *anthropic.Message) (mcp.CallToolParams, error) {
	for _, block := range resp.Content {
		if block.Type == "tool_use" {
			toolUse := block.AsToolUse()
			if toolUse.Name != "transition" {
				return mcp.CallToolParams{}, fmt.Errorf("harness/live: unexpected tool name %q", toolUse.Name)
			}
			// Parse the raw input JSON.
			var args map[string]any
			rawBytes, err := json.Marshal(toolUse.Input)
			if err != nil {
				return mcp.CallToolParams{}, fmt.Errorf("harness/live: marshal tool input: %w", err)
			}
			if err := json.Unmarshal(rawBytes, &args); err != nil {
				return mcp.CallToolParams{}, fmt.Errorf("harness/live: unmarshal tool input: %w", err)
			}
			return mcp.CallToolParams{
				Name:      "transition",
				Arguments: args,
			}, nil
		}
	}
	return mcp.CallToolParams{}, fmt.Errorf("harness/live: response contained no tool_use block (stop_reason=%q)", resp.StopReason)
}

// isRetryable returns true for transient errors (rate limits, server errors).
func isRetryable(err error) bool {
	// The anthropic SDK returns typed errors; check for HTTP status codes.
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) {
		status := apiErr.StatusCode
		return status == 429 || (status >= 500 && status < 600)
	}
	return false
}

// Close is a no-op for LiveHarness (the anthropic client is shared/managed externally).
func (h *LiveHarness) Close() error { return nil }

// extractUsage returns the UsageInfo from a Message (for RecordingHarness).
func extractUsage(resp *anthropic.Message) UsageInfo {
	u := resp.Usage
	return UsageInfo{
		InputTokens:       u.InputTokens,
		OutputTokens:      u.OutputTokens,
		CacheReadTokens:   u.CacheReadInputTokens,
		CacheCreateTokens: u.CacheCreationInputTokens,
		CacheHit:          u.CacheReadInputTokens > 0,
	}
}
