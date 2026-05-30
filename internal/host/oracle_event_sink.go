// Package host — oracle EventSink context plumbing and event-append helpers.
//
// This file provides the JSONL-sink side of oracle tracing:
//
//  1. Context keys and helpers for the oracle call context (session ID, turn,
//     state path) injected by the orchestrator into each oracle handler call.
//  2. WithOracleEventSink / EventSinkFromOracleCtx — the EventSink used for
//     OracleCalled / OracleReturned / OracleError JSONL writes.
//  3. OracleCalledPayload, OracleReturnedPayload, OracleErrorPayload — the
//     wire types written to the JSONL trace for every oracle turn.
//  4. appendOracleCalledEvent / appendOracleReturnedEvent / appendOracleErrorEvent —
//     the one-stop write helpers called by each oracle verb after it completes.
//  5. marshalInput / marshalResponse — small marshal helpers used by the
//     oracle verb handlers to serialize verb-specific descriptors.
//
// The legacy SQLite-backed journal (appendOracleCallJournal, OracleCallBody,
// WithOracleJournalWriter) was deleted in wave B-5.  The EventSink here is the
// only trace write path.
package host

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/store"
)

// ── Oracle prompts directory context ──────────────────────────────────────────

// oraclePromptsDir is the context key for the directory where large prompts
// are stored (e.g., {trace_dir}/oracle-prompts/). If set, large prompts
// (>1KB) are written to separate files to keep JSONL lines under PIPE_BUF.
type oraclePromptsDirKey struct{}

// WithOraclePromptsDir returns a child context carrying the directory where
// large prompts should be stored. Pass "" to disable separate prompt storage.
func WithOraclePromptsDir(ctx context.Context, dir string) context.Context {
	if dir == "" {
		return ctx
	}
	return context.WithValue(ctx, oraclePromptsDirKey{}, dir)
}

// OraclePromptsDirFromCtx returns the oracle prompts directory from context,
// or "" if none was set.
func OraclePromptsDirFromCtx(ctx context.Context) string {
	dir, _ := ctx.Value(oraclePromptsDirKey{}).(string)
	return dir
}

// ── Oracle call context ───────────────────────────────────────────────────────

// OracleCallCtx carries session-level identifiers needed to populate JSONL
// events from within oracle handlers (which don't have direct access to the
// orchestrator's session/turn state).
type OracleCallCtx struct {
	SessionID app.SessionID
	Turn      app.TurnNumber
	StatePath app.StatePath
}

// oracleCallCtxKey is the context key for an OracleCallCtx.
type oracleCallCtxKey struct{}

// WithOracleCallCtx returns a child context carrying oc.
func WithOracleCallCtx(ctx context.Context, oc OracleCallCtx) context.Context {
	return context.WithValue(ctx, oracleCallCtxKey{}, oc)
}

// OracleCallCtxFrom returns the OracleCallCtx previously injected with
// WithOracleCallCtx, or a zero value if none was injected.
func OracleCallCtxFrom(ctx context.Context) OracleCallCtx {
	oc, _ := ctx.Value(oracleCallCtxKey{}).(OracleCallCtx)
	return oc
}

// ── EventSink context plumbing ────────────────────────────────────────────────

// oracleEventSinkKey is the context key for a store.EventSink injected into
// oracle handler calls for the JSONL write.
type oracleEventSinkKey struct{}

// WithOracleEventSink returns a child context carrying sink. Oracle handlers
// call EventSinkFromOracleCtx to retrieve it. Nil is a safe no-op.
func WithOracleEventSink(ctx context.Context, sink store.EventSink) context.Context {
	if sink == nil {
		return ctx
	}
	return context.WithValue(ctx, oracleEventSinkKey{}, sink)
}

// EventSinkFromOracleCtx returns the store.EventSink previously injected with
// WithOracleEventSink, or nil if none was injected.
func EventSinkFromOracleCtx(ctx context.Context) store.EventSink {
	s, _ := ctx.Value(oracleEventSinkKey{}).(store.EventSink)
	return s
}

// ── OracleCalled / OracleReturned / OracleError payload types ─────────────────

// OracleCalledPayload is the payload written to OracleCalled events.
// The verb identifies which oracle verb dispatched the call (ask, decide,
// extract, task, converse). The call_id is a deterministic identifier that
// pairs this event with the matching OracleReturned or OracleError event.
// Replay treats OracleCalled as a no-op.
//
// NOTE: Large prompts are stored in separate files to keep the JSONL line
// under PIPE_BUF (4096 bytes). When PromptFile is set, the full prompt is
// in that external file. The prompt is available in:
// - The oracle.AskRequest.PromptText (live mode)
// - The cassette via !include or separate prompt file (replay mode)
// This ensures deterministic replay while staying under atomic write limits.
// See oracle_dispatch.go appendOracleCalledEventWithEpisode for details.
type OracleCalledPayload struct {
	Verb       string          `json:"verb"`
	Agent      string          `json:"agent,omitempty"`
	Model      string          `json:"model,omitempty"`
	PromptFile string          `json:"prompt_file,omitempty"` // Path to external prompt file if large
	Input      json.RawMessage `json:"input,omitempty"`
}

// OracleReturnedPayload is the payload written to OracleReturned events.
// Meta is opaque (tokens, cost, model — varies by oracle transport).
// Replay treats OracleReturned as a no-op.
//
// NOTE: Large responses are stored in separate files to keep the JSONL line
// under PIPE_BUF (4096 bytes). When ResponseFile is set, the full response is
// in that external file. The response is available in:
// - The oracle handler's response field (live mode)
// - The cassette's response field or separate response file (replay mode)
type OracleReturnedPayload struct {
	Verb         string          `json:"verb"`
	Agent        string          `json:"agent,omitempty"`
	Model        string          `json:"model,omitempty"`
	DurationMS   int64           `json:"duration_ms"`
	Response     json.RawMessage `json:"response,omitempty"`
	ResponseFile string          `json:"response_file,omitempty"` // Path to external response file if large
	Meta         map[string]any  `json:"meta,omitempty"`
}

// OracleErrorPayload is the payload written to OracleError events.
// Replay treats OracleError as a no-op.
type OracleErrorPayload struct {
	Verb       string `json:"verb"`
	Agent      string `json:"agent,omitempty"`
	DurationMS int64  `json:"duration_ms"`
	Error      string `json:"error"`
}

// ── JSONL append helpers ───────────────────────────────────────────────────────

// appendOracleCalledEvent appends an OracleCalled event to the EventSink in
// ctx (if any). callID and ts are the deterministic call identifier and the
// dispatch timestamp respectively. oc carries the session/turn/state.
func appendOracleCalledEvent(ctx context.Context, ts time.Time, callID string, payload OracleCalledPayload) {
	sink := EventSinkFromOracleCtx(ctx)
	if sink == nil {
		return
	}
	oc := OracleCallCtxFrom(ctx)

	raw, err := json.Marshal(payload)
	if err != nil {
		return // best-effort; marshal failure is not a reason to abort the call
	}

	ev := store.Event{
		Turn:      oc.Turn,
		Ts:        ts,
		Kind:      store.OracleCalled,
		StatePath: oc.StatePath,
		Payload:   json.RawMessage(raw),
		CallID:    callID,
	}
	_ = sink.Append(ev)
}

// appendOracleReturnedEvent appends an OracleReturned event to the EventSink
// in ctx (if any). ts is the response timestamp (real, not fudged).
// If the response is large, it is stored in a separate file and the
// payload.ResponseFile is set to reference it.
func appendOracleReturnedEvent(ctx context.Context, ts time.Time, callID string, payload OracleReturnedPayload) {
	sink := EventSinkFromOracleCtx(ctx)
	if sink == nil {
		return
	}
	oc := OracleCallCtxFrom(ctx)

	// Store large responses in separate files to keep JSONL lines under PIPE_BUF.
	if len(payload.Response) > 0 {
		if responseFile, err := storeResponseIfLarge(ctx, callID, payload.Response); err == nil && responseFile != "" {
			payload.ResponseFile = responseFile
			payload.Response = nil // Clear the inline response since it's in a separate file
		}
		// On error, proceed with the original (possibly large) payload anyway
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}

	ev := store.Event{
		Turn:      oc.Turn,
		Ts:        ts,
		Kind:      store.OracleReturned,
		StatePath: oc.StatePath,
		Payload:   json.RawMessage(raw),
		CallID:    callID,
	}
	_ = sink.Append(ev)
}

// appendOracleErrorEvent appends an OracleError event to the EventSink in
// ctx (if any). ts is the error timestamp.
func appendOracleErrorEvent(ctx context.Context, ts time.Time, callID string, payload OracleErrorPayload) {
	sink := EventSinkFromOracleCtx(ctx)
	if sink == nil {
		return
	}
	oc := OracleCallCtxFrom(ctx)

	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}

	ev := store.Event{
		Turn:      oc.Turn,
		Ts:        ts,
		Kind:      store.OracleError,
		StatePath: oc.StatePath,
		Payload:   json.RawMessage(raw),
		CallID:    callID,
	}
	_ = sink.Append(ev)
}

// ── Legacy cassette record-mode types (B-5: dead code, kept for compile compat) ─

// OracleCallBody was the body written to KindOracleCall journal entries by the
// now-deleted appendOracleCallJournal helper. It is retained here only because
// internal/testrunner still references it for cassette record-mode scaffolding.
// With the SQLite journal write path deleted in B-5, journalLookup will always
// return (nil, false); the cassette oracle block is never populated via this
// path. Phase C+ will remove the cassette record mode entirely.
type OracleCallBody struct {
	// Identity
	CallID string `json:"call_id"`
	Verb   string `json:"verb"`
	Agent  string `json:"agent,omitempty"`
	Model  string `json:"model,omitempty"`

	// TaskTraceID (task verb only).
	TaskTraceID string `json:"task_trace_id,omitempty"`

	// Timing and token usage.
	DurationMS     int64   `json:"duration_ms"`
	PromptTokens   int     `json:"prompt_tokens,omitempty"`
	ResponseTokens int     `json:"response_tokens,omitempty"`
	CostUSD        float64 `json:"cost_usd,omitempty"`

	// Full prompt content.
	SystemPrompt string `json:"system_prompt,omitempty"`
	Prompt       string `json:"prompt,omitempty"`

	// Verb-specific input and response descriptors.
	Input    json.RawMessage `json:"input,omitempty"`
	Response json.RawMessage `json:"response,omitempty"`

	// Error string when the call failed.
	Error string `json:"error,omitempty"`
}

// marshalInput marshals the verb-specific input descriptor to JSON, returning
// nil on error.
func marshalInput(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return json.RawMessage(b)
}

// marshalResponse marshals the verb-specific response descriptor to JSON,
// returning nil on error.
func marshalResponse(v any) json.RawMessage {
	return marshalInput(v) // same logic
}

// StorePromptIfLargeForTest is exported for cassette tests.
// See storePromptIfLarge for details.
func StorePromptIfLargeForTest(ctx context.Context, callID string, prompt string) (string, error) {
	return storePromptIfLarge(ctx, callID, prompt)
}

// storePromptIfLarge writes the prompt to a separate file if it's large (>1KB),
// returning the file reference for the PromptFile field, or "" if the prompt
// was small or storage is not configured. Large prompts are stored in
// {promptsDir}/{callID}.txt to keep JSONL lines under PIPE_BUF.
//
// Returns: (promptFileRef, error). On error, returns ("", err) and the event
// should still be written (prompt unavailable in UI, but execution continues).
func storePromptIfLarge(ctx context.Context, callID string, prompt string) (string, error) {
	const largeThreshold = 1024 // 1KB

	if len(prompt) <= largeThreshold {
		return "", nil // Small enough to include inline in future; not implemented yet
	}

	promptsDir := OraclePromptsDirFromCtx(ctx)
	if promptsDir == "" {
		return "", nil // Storage not configured; skip
	}

	// Ensure prompts directory exists.
	if err := os.MkdirAll(promptsDir, 0o755); err != nil {
		return "", fmt.Errorf("storePromptIfLarge: mkdir %q: %w", promptsDir, err)
	}

	// Write prompt to {promptsDir}/{callID}.txt.
	promptPath := filepath.Join(promptsDir, callID+".txt")
	if err := os.WriteFile(promptPath, []byte(prompt), 0o644); err != nil {
		return "", fmt.Errorf("storePromptIfLarge: write %q: %w", promptPath, err)
	}

	// Return relative path for portability (relative to trace dir).
	return filepath.Join("oracle-prompts", callID+".txt"), nil
}

// storeResponseIfLarge writes the response to a separate file if it's large (>1KB),
// returning the file reference for the ResponseFile field, or "" if the response
// was small or storage is not configured. Large responses are stored in
// {promptsDir}/{callID}-response.json to keep JSONL lines under PIPE_BUF.
//
// Returns: (responseFileRef, error). On error, returns ("", err) and the event
// should still be written (response unavailable in UI, but execution continues).
func storeResponseIfLarge(ctx context.Context, callID string, response json.RawMessage) (string, error) {
	const largeThreshold = 1024 // 1KB

	if len(response) <= largeThreshold {
		return "", nil // Small enough to include inline
	}

	promptsDir := OraclePromptsDirFromCtx(ctx)
	if promptsDir == "" {
		return "", nil // Storage not configured; skip
	}

	// Ensure prompts directory exists (reuse for both prompts and responses).
	if err := os.MkdirAll(promptsDir, 0o755); err != nil {
		return "", fmt.Errorf("storeResponseIfLarge: mkdir %q: %w", promptsDir, err)
	}

	// Write response to {promptsDir}/{callID}-response.json.
	responsePath := filepath.Join(promptsDir, callID+"-response.json")
	if err := os.WriteFile(responsePath, []byte(response), 0o644); err != nil {
		return "", fmt.Errorf("storeResponseIfLarge: write %q: %w", responsePath, err)
	}

	// Return relative path for portability (relative to trace dir).
	return filepath.Join("oracle-prompts", callID+"-response.json"), nil
}
