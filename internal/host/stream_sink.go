// Package host — meta-mode stream-event sink.
//
// runClaudeStreamJSON in oracle_runner.go emits one slog.InfoContext
// record per JSONL line claude prints in stream-json mode. The TUI's
// transcript pane has no slog listener — it would stay frozen behind
// the "agent is thinking…" spinner until the terminal `result` event
// lands and metaSendDoneMsg finally fires.
//
// StreamSink is a non-blocking observer that lets the TUI tee those
// same events into the transcript in real time. The runner pulls a
// sink out of the request context (set by the TUI's metaSendCmd via
// WithStreamSink) and calls OnStreamEvent alongside the slog emit;
// the slog signal is unchanged. A nil sink is a no-op so existing
// callers (non-metamode oracle paths, tests) need no changes.
//
// Concurrency contract: OnStreamEvent runs on the oracle subprocess's
// stdout-reader goroutine. Implementations MUST NOT block — a stalled
// sink would back-pressure claude's stdout pipe and stall the entire
// LLM call. The TUI implementation forwards into tea.Program.Send via
// a fresh goroutine + drop-on-backpressure semantics so a sluggish
// message loop never reaches back here.
package host

import "context"

// StreamEvent is one observable unit from a streaming claude-cli call.
// Mirrors the shape of the slog "metamode.oracle.event" record so a
// reader of either signal sees the same payload.
type StreamEvent struct {
	Type      string  // "system" | "assistant" | "user" | "result" | etc.
	Subtype   string  // "init" | "api_retry" | "success" | "" | …
	Tool      string  // tool name for assistant tool_use events
	Preview   string  // one-line preview (≤120 chars, single line)
	SessionID string  // claude session id (system.init / result events)
	IsResult  bool    // true on the terminal result event
	CostUSD   float64 // result events only (0 otherwise)
}

// StreamSink receives streamed events from oracle calls. Implementations
// must be safe to call from any goroutine and must be non-blocking
// (drop on backpressure rather than stall the oracle subprocess reader).
type StreamSink interface {
	OnStreamEvent(ctx context.Context, ev StreamEvent)
}

// streamSinkKey is the context key for a per-call StreamSink.
type streamSinkKey struct{}

// WithStreamSink returns a child context that carries sink. The runner
// pulls it out via StreamSinkFrom and tees events to both slog AND the
// sink. Nil sink is a no-op — returns ctx unchanged so callers can use
// the value unguarded.
func WithStreamSink(ctx context.Context, sink StreamSink) context.Context {
	if sink == nil {
		return ctx
	}
	return context.WithValue(ctx, streamSinkKey{}, sink)
}

// StreamSinkFrom returns the sink installed in ctx, or nil if none is
// installed. Nil-return is the normal case for non-metamode callers.
func StreamSinkFrom(ctx context.Context) StreamSink {
	s, _ := ctx.Value(streamSinkKey{}).(StreamSink)
	return s
}
