// Package harness — RecordingHarness implementation (§10.5, §12.1).
// Wraps an inner Harness (typically LiveHarness) and appends each call as a
// JSONL record to an output file. Stage 7 will convert the JSONL → oracle YAML.
package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// recordedTurn is one JSONL record written by RecordingHarness.
// JSON tags are the canonical shape; Stage 7's oracle emitter reads them.
type recordedTurn struct {
	// State is the state path at the time of the call.
	State string `json:"state"`
	// Input is the raw user utterance.
	Input string `json:"input"`
	// Intent is the routed intent name.
	Intent string `json:"intent"`
	// Slots holds the extracted slot values (may be nil for zero-arg intents).
	Slots map[string]any `json:"slots,omitempty"`
	// TS is the Unix timestamp in milliseconds.
	TS int64 `json:"ts"`
	// Model is the LLM model name (empty for non-Live harnesses).
	Model string `json:"model,omitempty"`
	// TokensIn is the number of input tokens consumed.
	TokensIn int64 `json:"tokens_in,omitempty"`
	// TokensOut is the number of output tokens produced.
	TokensOut int64 `json:"tokens_out,omitempty"`
}

// RecordingHarness wraps an inner Harness and records each call to a JSONL file.
// Every RunTurn call appends exactly one JSON object + newline, making the file
// safe for concurrent tail/streaming readers.
type RecordingHarness struct {
	inner Harness
	mu    sync.Mutex
	f     *os.File
	enc   *json.Encoder
}

// NewRecording opens outputPath for appending and returns a RecordingHarness
// that wraps inner. Call Close() when done to flush and close the file.
func NewRecording(inner Harness, outputPath string) (*RecordingHarness, error) {
	if inner == nil {
		return nil, fmt.Errorf("harness/recording: inner harness must not be nil")
	}
	f, err := os.OpenFile(outputPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("harness/recording: open %q: %w", outputPath, err)
	}
	return &RecordingHarness{
		inner: inner,
		f:     f,
		enc:   json.NewEncoder(f),
	}, nil
}

// RunTurn delegates to the inner harness, then appends a JSONL record to disk.
// The inner harness error is returned unchanged; the record is only written on success.
func (h *RecordingHarness) RunTurn(ctx context.Context, in TurnInput) (mcp.CallToolParams, error) {
	params, err := h.inner.RunTurn(ctx, in)
	if err != nil {
		return params, err
	}

	// Parse the intent and slots from the CallToolParams arguments.
	intentName, slots, _ := parseTransitionArgs(params)

	rec := recordedTurn{
		State:  string(in.StatePath),
		Input:  in.UserText,
		Intent: intentName,
		Slots:  slots,
		TS:     time.Now().UnixMilli(),
	}

	// If the inner harness is a LiveHarness, we could theoretically read
	// usage from a side-channel; for now the model is set at construction
	// and tokens are zero (Stage 7 will wire this up via a dedicated interface).
	if lh, ok := h.inner.(*LiveHarness); ok {
		rec.Model = lh.model
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if encErr := h.enc.Encode(rec); encErr != nil {
		// Non-fatal: log but don't fail the turn.
		_ = encErr
	}
	return params, nil
}

// Close flushes the write buffer and closes the output file.
// It also calls Close on the inner harness.
func (h *RecordingHarness) Close() error {
	innerErr := h.inner.Close()
	h.mu.Lock()
	defer h.mu.Unlock()
	fileErr := h.f.Close()
	if innerErr != nil {
		return innerErr
	}
	return fileErr
}

// parseTransitionArgs extracts the intent name and slots from a CallToolParams.
// Returns empty strings/nil on parse failure (non-fatal; the record is still written).
func parseTransitionArgs(p mcp.CallToolParams) (intent string, slots map[string]any, confidence float64) {
	if p.Arguments == nil {
		return "", nil, 0
	}
	// Arguments is map[string]any (as set by LiveHarness.extractToolCall).
	argsMap, ok := p.Arguments.(map[string]any)
	if !ok {
		// Try JSON round-trip.
		b, err := json.Marshal(p.Arguments)
		if err != nil {
			return "", nil, 0
		}
		if err := json.Unmarshal(b, &argsMap); err != nil {
			return "", nil, 0
		}
	}

	if v, ok := argsMap["intent"].(string); ok {
		intent = v
	}
	if v, ok := argsMap["slots"].(map[string]any); ok {
		slots = v
	}
	if v, ok := argsMap["confidence"].(float64); ok {
		confidence = v
	}
	return intent, slots, confidence
}
