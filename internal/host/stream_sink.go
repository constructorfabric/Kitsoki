// Package host — meta-mode stream-event sink.
//
// runClaudeStreamJSON in agent_runner.go emits one slog.InfoContext
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
// callers (non-metamode agent paths, tests) need no changes.
//
// Concurrency contract: OnStreamEvent runs on the agent subprocess's
// stdout-reader goroutine. Implementations MUST NOT block — a stalled
// sink would back-pressure claude's stdout pipe and stall the entire
// LLM call. The TUI implementation forwards into tea.Program.Send via
// a fresh goroutine + drop-on-backpressure semantics so a sluggish
// message loop never reaches back here.
package host

import "context"

// StreamToolUse is one tool_use block from an assistant event: the tool
// name plus a compact, single-line preview of its arguments (already
// clipped to ≤120 runes). A single assistant message commonly carries
// several tool_use blocks (parallel tool calls), so consumers that want
// to render each tool on its own line MUST iterate StreamEvent.Tools
// rather than reading the back-compat scalar StreamEvent.Tool (the first
// tool only).
type StreamToolUse struct {
	Name    string
	Preview string
}

// StreamEvent is one observable unit from a streaming claude-cli call.
// Mirrors the shape of the slog "metamode.agent.event" record so a
// reader of either signal sees the same payload.
type StreamEvent struct {
	Type    string // "system" | "assistant" | "user" | "result" | etc.
	Subtype string // "init" | "api_retry" | "success" | "" | …
	Tool    string // FIRST tool name for assistant tool_use events (back-compat; see Tools)
	// Preview is a compact, single-line peek (≤120 runes) used by the
	// slog trace and the tool-use breadcrumb (tool args, e.g.
	// "prompt.md"). It is deliberately clipped — never render it as
	// narration prose; use Text for that. Mirrors Tools[0].Preview.
	Preview string
	// Tools is EVERY tool_use block in this assistant message, in order.
	// Claude batches parallel tool calls into one assistant event, so an
	// event can carry multiple tools; rendering only Tool collapses them
	// into a single line. Tool/Preview remain populated with the first
	// entry for back-compat. Empty for non-tool events.
	Tools []StreamToolUse
	// Text is the FULL assistant narration / "thinking" prose for this
	// event, untruncated and with newlines preserved. Consumers that
	// show reasoning to the user (the transcript pane) must render this,
	// not Preview — clipping a thought mid-sentence is a visible bug.
	// Empty for tool-only assistant events, system events, and the
	// terminal result event.
	Text string
	// Thinking is the extended-thinking prose for this event (claude
	// `{"type":"thinking"}` content blocks), full and untruncated.
	// Separate from Text — Text doubles as the reply-assembly fallback
	// and thinking is never part of the model's reply. Surfaces that
	// show reasoning (the 🧠 rows) must render BOTH, thinking first.
	Thinking  string
	SessionID string  // claude session id (system.init / result events)
	IsResult  bool    // true on the terminal result event
	CostUSD   float64 // result events only (0 otherwise)
	// Token usage from the terminal result event (all 0 on non-result
	// events). InputTokens / OutputTokens are the turn's prompt/response
	// totals; the Cache* fields break out prompt-cache reads vs. writes.
	InputTokens         int
	OutputTokens        int
	CacheReadTokens     int
	CacheCreationTokens int

	// Routing provenance frames are emitted by the orchestrator as soon as a
	// free-text turn resolves to a routing tier, before any downstream host or
	// agent work completes. Type is "routing".
	Turn       int64
	Intent     string
	RoutedBy   string
	MatchType  string
	Confidence float64

	// Harness-ladder provenance frames are emitted by the host ladder itself.
	// Type is one of ladder_attempt, ladder_fallback, ladder_session_pinned,
	// ladder_success, ladder_exhausted, or ladder_stop. Text is the human-facing
	// notice; these fields let consumers label which provider/backend/model the
	// notice refers to without parsing that prose. Severity is optional
	// ("warn", "info", etc.) for surfaces that provide a warning affordance.
	Severity string
	Backend  string
	Provider string
	Model    string
	Effort   string
	Error    string
}

// StreamSink receives streamed events from agent calls. Implementations
// must be safe to call from any goroutine and must be non-blocking
// (drop on backpressure rather than stall the agent subprocess reader).
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
