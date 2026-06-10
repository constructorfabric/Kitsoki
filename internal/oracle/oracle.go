// oracle.go holds the core plugin contract: the Oracle interface, its
// AskRequest/AskResponse wire format, the typed AskError, and the package's
// shared tuning constants. See doc.go for the package overview.

package oracle

import (
	"context"
	"encoding/json"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/store"
	"kitsoki/internal/world"
)

// MaxHTTPResponseSize caps how many bytes the MCP-over-HTTP transport will read
// from a single response body. It bounds memory against a hostile or buggy
// plugin streaming an unbounded body; legitimate oracle submissions are far
// smaller. 16 MiB is generous headroom, not a tuned limit.
const MaxHTTPResponseSize = 16 * 1024 * 1024

// SubprocessTerminateTimeout is how long Close waits for a subprocess to exit
// after SIGTERM before escalating to SIGKILL. Long enough for a well-behaved
// plugin to flush and exit, short enough that session shutdown is not held
// hostage by a wedged process.
const SubprocessTerminateTimeout = 2 * time.Second

// ErrorDetailTruncateBytes caps the length of raw plugin bytes echoed into an
// AskError.Detail (partial frames, HTTP error bodies, malformed responses).
// Keeps a misbehaving plugin from flooding the trace while preserving enough
// of the payload to diagnose the failure.
const ErrorDetailTruncateBytes = 256

// Oracle is the plugin contract for all oracle transports.
// Each implementation must be safe for concurrent use from multiple goroutines
// (kitsoki may run background turns in parallel with foreground turns).
type Oracle interface {
	// Ask sends a rendered prompt to the oracle and blocks until either a
	// schema-shaped JSON response is returned or an error occurs.
	//
	// Kitsoki validates AskResponse.Submission against AskRequest.SchemaJSON
	// after Ask returns; plugins need not pre-validate (though they MAY as a
	// fast-fail UX). SchemaJSON == nil skips validation entirely.
	//
	// When the context is cancelled before the plugin responds, Ask MUST return
	// the context error so kitsoki can write OracleError and unblock the turn.
	// In-process plugins MUST honour ctx.Done(); subprocess and HTTP plugins
	// are best-effort but kitsoki enforces a hard cap via ctx cancel.
	Ask(ctx context.Context, req AskRequest) (AskResponse, error)

	// Close releases any resources held by the oracle (subprocess, HTTP client,
	// in-process state). Called once on session end; further Ask calls after
	// Close have undefined behaviour.
	Close() error
}

// AskRequest is the wire format sent to an oracle on every Ask call.
// All fields must round-trip through JSON encode/decode because subprocess and
// HTTP transports serialize the request.
//
// JSON field names are stable across all transports:
//
//	session_id, turn, state_path, verb, prompt, schema, with, world, deadline, call_id
type AskRequest struct {
	// SessionID identifies the kitsoki session that owns this ask.
	SessionID app.SessionID `json:"session_id"`

	// TurnNumber is the monotonic turn counter at the time of this ask.
	TurnNumber app.TurnNumber `json:"turn"`

	// StatePath is the active state path when the ask is dispatched.
	StatePath app.StatePath `json:"state_path"`

	// Verb is the opaque routing key identifying which oracle host call
	// triggered this ask (e.g. "ask", "decide", "extract", "task", "converse").
	// Story authors use it for per-verb prompt routing inside a single plugin.
	Verb string `json:"verb"`

	// PromptText is the fully rendered prompt. Template expansion (pongo2) and
	// source-color stripping have already been applied by the time the oracle
	// receives this field.
	PromptText string `json:"prompt"`

	// SchemaJSON is the optional JSON-Schema the Submission must conform to.
	// When nil, kitsoki skips validation and binds the raw Submission to world
	// as-is. Kitsoki is the validation authority; plugins MAY pre-validate as a
	// fast-fail UX but are not required to.
	SchemaJSON json.RawMessage `json:"schema,omitempty"`

	// WithArgs is the story's `with:` block after full template rendering.
	// Opaque to kitsoki beyond passing it through; plugins use it for
	// call-site configuration (e.g. target repo, task description).
	WithArgs map[string]any `json:"with,omitempty"`

	// World is a read-only snapshot of all world variables at dispatch time.
	// Plugins MAY use it for prompt augmentation or guard checks.
	World world.World `json:"world"`

	// Deadline is a soft hint for when kitsoki expects a response. Plugins
	// SHOULD honour it as a best-effort timeout; kitsoki enforces the hard cap
	// via ctx cancel. Plugins that overrun are surfaced as OracleError.
	Deadline time.Time `json:"deadline"`

	// CallID is the deterministic oracle call identifier derived via
	// host.DeriveCallID. It pairs OracleCalled with OracleReturned / OracleError
	// in the JSONL trace. The caller (room dispatch) is responsible for deriving
	// and injecting this value.
	CallID string `json:"call_id"`
}

// AskResponse is the oracle's reply to an Ask call.
// All fields must round-trip through JSON because subprocess and HTTP transports
// serialize the response.
type AskResponse struct {
	// Submission is the raw JSON produced by the oracle. Kitsoki validates it
	// against AskRequest.SchemaJSON (if non-nil) before binding to world.
	// The bytes MUST be valid JSON; malformed bytes surface as AskError with
	// Kind == "schema_invalid".
	Submission json.RawMessage `json:"submission,omitempty"`

	// Meta is opaque token/cost/model metadata. Kitsoki surfaces it verbatim on
	// the OracleReturned event payload under the "meta" key; it is never
	// interpreted by the state machine.
	Meta map[string]any `json:"meta,omitempty"`

	// SubEvents is an optional list of plugin-emitted events appended verbatim
	// to the JSONL between the OracleCalled and OracleReturned lines. Plugins
	// with meaningful internal tool calls (e.g. autofix's bash/read/edit bursts)
	// MAY populate this to preserve audit fidelity; simple plugins MAY leave it
	// nil. Kitsoki appends these verbatim to the JSONL trace.
	SubEvents []store.Event `json:"sub_events,omitempty"`

	// Transcript is the call's native execution detail — the sidecar-bound
	// sibling of SubEvents. Where SubEvents are folded into the lean story
	// JSONL, the Transcript is *evidence about* the call (every tool_use input,
	// tool_result, and assistant reasoning block) that is too heavy and too
	// non-deterministic to live in the trace, so it is written to a per-call
	// sidecar and referenced from oracle.call.complete by a single pointer.
	//
	// This is the carry-up path for *out-of-host* backends (local_llm,
	// subprocess, MCP-HTTP): they populate it from their own request/response so
	// the dispatcher can persist it uniformly. The in-host claude path does not
	// use this field — its RawEvents are too rich to survive a JSON round-trip,
	// so it tees them straight into a host.TranscriptWriter pulled from context.
	//
	// Optional and additive: a nil Transcript renders exactly as today (no
	// "Agent actions" affordance), so existing backends and old cassettes are
	// unaffected.
	Transcript *Transcript `json:"transcript,omitempty"`
}

// Transcript is one oracle call's backend-native execution detail, captured
// verbatim for the "Agent actions" drawer. It is not interpreted by the state
// machine — kitsoki writes it to a per-call sidecar and references it from the
// trace by a pointer; the web consumer renders it.
//
// The Events are kept byte-verbatim in the backend's own schema so an
// off-the-shelf parser for that format (e.g. a claude stream-json reader) can
// consume the sidecar unchanged. Capture-time offsets are deliberately NOT
// inlined into the events — they live in a parallel timings sidecar (Timings
// here) so the verbatim stream stays pristine.
type Transcript struct {
	// Format names the event schema so the consumer picks the right renderer.
	// Defined values: "claude-stream-json" (the in-host claude wire stream),
	// "openai-chat" (local_llm's request/response triple), or a "<plugin>"
	// identifier a subprocess/MCP-HTTP backend chooses for its own shape.
	Format string `json:"format"`

	// Events are the backend-native execution events, verbatim, one per line in
	// the sidecar .jsonl. Each MUST be valid JSON on its own line.
	Events []json.RawMessage `json:"events"`

	// Timings is the per-event capture-time offset in milliseconds since the
	// call started, powering the waterfall view. When present its length matches
	// Events; it MAY be nil for backends that do not stamp per-event timing
	// (the offsets then default to absent rather than fabricated). It is written
	// to a parallel timings sidecar, never folded into the verbatim events.
	Timings []int64 `json:"timings,omitempty"`
}

// AskError is the typed error returned by Oracle.Ask when the plugin fails
// before producing a usable Submission. Kitsoki writes OracleError to the trace
// when Ask returns a non-nil error.
//
// After a partial response (e.g. SubEvents were already appended, then the
// plugin crashed), the SubEvents already written to JSONL are kept; AskError
// closes the call with OracleError.
//
// Match with errors.As:
//
//	var ae *oracle.AskError
//	if errors.As(err, &ae) { ... ae.Kind ... }
type AskError struct {
	// Kind is a machine-readable error category. Defined values:
	//   "schema_invalid"     — Submission failed JSON-Schema validation (or was not valid JSON).
	//   "deadline_exceeded"  — Context deadline exceeded before the plugin responded.
	//   "transport_error"    — Network or IPC layer failure (connection refused, TLS failure, etc.).
	//   "plugin_crash"       — Subprocess exited non-zero, or in-process plugin panicked.
	Kind string

	// Underlying is the original technical error (preserved for the trace).
	Underlying error

	// Detail is a human-readable explanation. For schema_invalid errors it
	// includes the JSON Pointer path and the failing constraint.
	Detail string
}

// Error implements the error interface. Returns the Detail when set, otherwise
// falls back to the Underlying error string.
func (e *AskError) Error() string {
	if e == nil {
		return "oracle: nil AskError"
	}
	if e.Detail != "" {
		return "oracle: " + e.Kind + ": " + e.Detail
	}
	if e.Underlying != nil {
		return "oracle: " + e.Kind + ": " + e.Underlying.Error()
	}
	return "oracle: " + e.Kind
}

// Unwrap lets errors.Is / errors.As reach the underlying error chain.
func (e *AskError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Underlying
}
