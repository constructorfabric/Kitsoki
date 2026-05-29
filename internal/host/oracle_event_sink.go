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
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/store"
)

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
// NOTE: Prompt and SystemPrompt are omitted from the event to keep the JSONL
// line under PIPE_BUF (4096 bytes). The full prompt is available in:
// - The oracle.AskRequest.PromptText (live mode)
// - The cassette via !include (replay mode)
// This ensures deterministic replay while staying under atomic write limits.
// See oracle_dispatch.go appendOracleCalledEventWithEpisode for details.
type OracleCalledPayload struct {
	Verb  string          `json:"verb"`
	Agent string          `json:"agent,omitempty"`
	Model string          `json:"model,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

// OracleReturnedPayload is the payload written to OracleReturned events.
// Meta is opaque (tokens, cost, model — varies by oracle transport).
// Replay treats OracleReturned as a no-op.
type OracleReturnedPayload struct {
	Verb       string          `json:"verb"`
	Agent      string          `json:"agent,omitempty"`
	Model      string          `json:"model,omitempty"`
	DurationMS int64           `json:"duration_ms"`
	Response   json.RawMessage `json:"response,omitempty"`
	Meta       map[string]any  `json:"meta,omitempty"`
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
func appendOracleReturnedEvent(ctx context.Context, ts time.Time, callID string, payload OracleReturnedPayload) {
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
