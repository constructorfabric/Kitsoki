// Package host — per-call agent-transcript sidecar writer.
//
// Every oracle verb whose operator is the claude CLI produces a rich
// execution stream — tool_use inputs, tool_result outputs, assistant
// thinking — that the host already parses (ClaudeRun.RawEvents) and
// today throws away. TranscriptWriter is the ctx-seam that lets the
// decide/ask/task path tee that stream into a per-call *sidecar* keyed
// by the deterministic call_id, so the web "Agent actions" drawer can
// render it without bloating the lean, replay-stable story trace.
//
// It MIRRORS the StreamSink pattern in stream_sink.go: the writer is
// pulled out of the request context (WithTranscriptWriter /
// TranscriptWriterFrom) and a nil writer is a no-op, so non-tracing
// callers and tests need no changes. The difference from StreamSink is
// lifecycle, not shape — StreamSink is fire-and-forget per event for the
// live TUI pane, whereas TranscriptWriter is ACCUMULATING per call: a
// single decide call_id is several `claude --resume` sessions, so the
// loop Appends across all of them under one id and Finalizes once.
//
// Concurrency contract: multiple oracle calls can interleave on the same
// writer (parallel background + foreground turns), so a shared writer
// MUST be safe for concurrent use across distinct call_ids. The
// file-backed implementation guards its per-call buffers with a mutex.
//
// Determinism: this writer is the live-capture half only. On replay the
// recorded transcript is written to the sidecar verbatim from the
// cassette — TranscriptWriter is never invoked, so a replayed run never
// re-executes a tool. See the proposal's Determinism section.

package host

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// TranscriptSchemaVersion is the schema version stamped into every
// TranscriptRef. Bump it when the sidecar layout (the .jsonl event shape or
// the .timings format) changes incompatibly so a consumer can refuse or adapt.
const TranscriptSchemaVersion = 1

// TranscriptRef is the pointer-only reference attached to oracle.call.complete
// in the story trace. It carries no detail — just enough to locate and
// summarize the sidecar so the UI can show an "Agent actions (N)" affordance
// and lazily fetch the full stream on demand.
type TranscriptRef struct {
	// Format names the event schema in the sidecar (e.g. "claude-stream-json",
	// "openai-chat", or a plugin identifier). Mirrors oracle.Transcript.Format.
	Format string `json:"format"`
	// Path is the sidecar location relative to the trace dir, e.g.
	// "transcripts/<call_id>.jsonl". Relative so a run dir stays relocatable.
	Path string `json:"path"`
	// Events is the count of events written to the sidecar — the "(N)" badge.
	Events int `json:"events"`
	// SchemaVersion is TranscriptSchemaVersion at write time.
	SchemaVersion int `json:"schema_version"`
}

// TranscriptWriter accumulates one oracle call's native execution events into a
// sidecar and finalizes them under the call's deterministic id. The decide loop
// Appends across several `claude --resume` sessions sharing one call_id, then
// Finalizes once to flush the sidecar and obtain the trace pointer.
//
// Implementations MUST be safe for concurrent use across distinct call_ids
// (interleaving turns) and tolerant of unknown call_ids on Finalize (returning
// nil rather than erroring when nothing was appended).
type TranscriptWriter interface {
	// Append records a backend-native, verbatim event for callID. The event is
	// written byte-for-byte (one JSON object per line) so an off-the-shelf
	// parser for format can consume the sidecar unchanged. offsetMs is the
	// capture-time offset (ms since the call started) stored in the parallel
	// timings sidecar, never folded into the verbatim event.
	Append(callID, format string, event json.RawMessage, offsetMs int64)

	// AppendSynthetic records a kitsoki-side synthetic event (a decide nudge, a
	// validator-reject boundary, a tool-bypass banner) for callID. It is
	// additive — written to the same sidecar and still valid JSON on its own
	// line — but marked (by convention, a "_kitsoki" key) so a parser keying on
	// the backend's known event types skips it. Same timings treatment as Append.
	AppendSynthetic(callID, format string, event json.RawMessage, offsetMs int64)

	// Finalize flushes <callID>.jsonl (verbatim events, one per line) and
	// <callID>.timings (event-index -> ms offset) and returns the trace pointer.
	// Returns nil if no events were appended for callID (so a call with no
	// transcript yields no affordance). format names the event schema in the
	// returned ref; it should match the format passed to Append.
	Finalize(callID, format string) *TranscriptRef
}

// callIDKey is the context key for the active oracle call's deterministic
// call_id. The claude transport tees its RawEvents into the TranscriptWriter
// under this id (see oracle_runner.go runClaudeStreamJSON), and the dispatch
// path Finalizes the same id to obtain the trace pointer. It is set by each
// verb handler / the dispatcher just before the claude subprocess runs, so the
// sidecar filename pairs with the oracle.call.complete event's CallID by value.
type callIDKey struct{}

// WithCallID returns a child context carrying the active oracle call_id. A nil
// (empty) id is a no-op so callers can set it unguarded. The claude tee in
// runClaudeStreamJSON reads it via CallIDFrom; when absent (no call_id in ctx)
// the tee is skipped, so non-oracle claude invocations write no sidecar.
func WithCallID(ctx context.Context, callID string) context.Context {
	if callID == "" {
		return ctx
	}
	return context.WithValue(ctx, callIDKey{}, callID)
}

// CallIDFrom returns the active oracle call_id installed in ctx, or "" when none
// is installed (the common case for non-oracle claude calls and unit tests).
func CallIDFrom(ctx context.Context) string {
	s, _ := ctx.Value(callIDKey{}).(string)
	return s
}

// callStartKey is the context key for the active oracle call's start instant.
// The claude tee (runClaudeStreamJSON) stamps each verbatim event with its
// offset from this instant. A SINGLE decide call_id spans several `claude
// --resume` subprocesses, each of which would otherwise reset its own clock to
// zero — scrambling the waterfall against the decide loop's synthetic-row clock.
// Installing one call-start (WithCallStart) for the whole call keeps every
// verbatim event AND every synthetic _kitsoki row on one monotonic timeline.
// When absent, the tee falls back to the per-invocation start (the common
// single-session case, where the two coincide).
type callStartKey struct{}

// WithCallStart returns a child context carrying the call-start instant shared
// by the verbatim tee and any synthetic rows. A zero time is a no-op so callers
// can set it unguarded.
func WithCallStart(ctx context.Context, start time.Time) context.Context {
	if start.IsZero() {
		return ctx
	}
	return context.WithValue(ctx, callStartKey{}, start)
}

// CallStartFrom returns the call-start instant installed in ctx and whether one
// was present. When absent, callers use their own per-invocation time.Now().
func CallStartFrom(ctx context.Context) (time.Time, bool) {
	t, ok := ctx.Value(callStartKey{}).(time.Time)
	return t, ok
}

// transcriptWriterKey is the context key for a per-session TranscriptWriter.
type transcriptWriterKey struct{}

// WithTranscriptWriter returns a child context carrying w. The oracle path
// pulls it out via TranscriptWriterFrom and tees events into it. A nil writer
// is a no-op — returns ctx unchanged so callers can use the value unguarded.
func WithTranscriptWriter(ctx context.Context, w TranscriptWriter) context.Context {
	if w == nil {
		return ctx
	}
	return context.WithValue(ctx, transcriptWriterKey{}, w)
}

// TranscriptWriterFrom returns the writer installed in ctx, or nil if none is
// installed. Nil-return is the normal case for non-tracing callers and tests.
func TranscriptWriterFrom(ctx context.Context) TranscriptWriter {
	w, _ := ctx.Value(transcriptWriterKey{}).(TranscriptWriter)
	return w
}

// finalizeTranscript flushes the accumulated transcript for callID (if a writer
// is installed in ctx) and returns the trace pointer. Returns nil when no writer
// is installed or no events were appended for callID, so the caller can attach
// the result unguarded (a nil ref is omitted from the event payload). The Format
// is the in-host claude stream-json format — out-of-host backends Append their
// own-format events first, but a single call only ever has one transcript, so
// one Finalize covers both producers. See oracle_event_sink.go's use on
// appendOracleReturnedEvent.
func finalizeTranscript(ctx context.Context, callID string) *TranscriptRef {
	w := TranscriptWriterFrom(ctx)
	if w == nil || callID == "" {
		return nil
	}
	return w.Finalize(callID, claudeTranscriptFormat)
}

// finalizeOutOfHostTranscript feeds an out-of-host backend's carried-up events
// (oracle.AskResponse.Transcript: format + verbatim events + optional timings)
// into the writer for callID, then Finalizes under the backend's own format and
// returns the trace pointer. Used by the dispatcher so out-of-host backends
// (local_llm, subprocess, MCP-HTTP) converge on the SAME sidecar + transcript_ref
// as the in-host claude tee. The caller passes the format/events/timings rather
// than the oracle.Transcript type to avoid a host→oracle import cycle.
//
// Returns nil (writing nothing) when no writer is installed, callID is empty, or
// there are no events. timings, when shorter than events, defaults missing
// offsets to 0; extra timings entries are ignored.
func finalizeOutOfHostTranscript(ctx context.Context, callID, format string, events []json.RawMessage, timings []int64) *TranscriptRef {
	w := TranscriptWriterFrom(ctx)
	if w == nil || callID == "" || len(events) == 0 {
		return nil
	}
	appendOutOfHostTranscript(ctx, callID, format, events, timings)
	return w.Finalize(callID, format)
}

// appendOutOfHostTranscript feeds an out-of-host backend's carried-up events
// into the writer for callID WITHOUT finalizing. Used when the call will
// continue under the same call_id (the local_llm → claude validation-reject
// fallback): the rejected local-model transcript — exactly the evidence the
// operator wants — is preserved by appending it here so a single later Finalize
// (on the closing OracleReturned/OracleError) flushes the whole arc into one
// sidecar. A nil writer / empty callID / empty events is a no-op.
func appendOutOfHostTranscript(ctx context.Context, callID, format string, events []json.RawMessage, timings []int64) {
	w := TranscriptWriterFrom(ctx)
	if w == nil || callID == "" {
		return
	}
	for i, ev := range events {
		var off int64
		if i < len(timings) {
			off = timings[i]
		}
		w.Append(callID, format, ev, off)
	}
}

// WriteReplayTranscript writes a recorded transcript (format + verbatim events +
// optional timings) to the per-call sidecar via the TranscriptWriter in ctx and
// returns the trace pointer to attach to oracle.call.complete. It is the replay
// entry point for the cassette dispatcher (internal/testrunner): on replay the
// recorded transcript is written to the sidecar verbatim — no live tool runs —
// so a replayed run produces a byte-identical sidecar (the golden contract). It
// shares the exact accumulate-then-Finalize logic of the live out-of-host path.
//
// Returns nil (writing nothing) when no writer is installed in ctx, callID is
// empty, or events is empty, so the caller can attach the result unguarded.
func WriteReplayTranscript(ctx context.Context, callID, format string, events []json.RawMessage, timings []int64) *TranscriptRef {
	return finalizeOutOfHostTranscript(ctx, callID, format, events, timings)
}

// transcriptEvent is one accumulated event: its verbatim JSON and capture-time
// offset. Synthetic events carry the same shape (the distinction lives in the
// event body's "_kitsoki" key, not here).
type transcriptEvent struct {
	raw      json.RawMessage
	offsetMs int64
}

// fileTranscriptWriter is the file-backed TranscriptWriter. It buffers each
// call's events in memory (keyed by call_id) until Finalize, then writes the
// <call_id>.jsonl + <call_id>.timings sidecars under its transcripts dir.
// Buffering until Finalize (rather than appending to disk per event) keeps the
// decide loop's multi-session accumulation atomic and avoids partial sidecars
// for an in-flight call.
type fileTranscriptWriter struct {
	dir string // absolute path to the transcripts directory

	mu     sync.Mutex
	events map[string][]transcriptEvent // call_id -> accumulated events
}

// NewFileTranscriptWriter constructs a file-backed writer that emits sidecars
// into dir. dir is created on first Finalize (lazily, so an unused writer leaves
// no empty directory). dir is the absolute transcripts directory; the
// TranscriptRef.Path returned by Finalize is relative to its parent (the trace
// dir), e.g. "transcripts/<call_id>.jsonl".
func NewFileTranscriptWriter(dir string) *fileTranscriptWriter {
	return &fileTranscriptWriter{
		dir:    dir,
		events: make(map[string][]transcriptEvent),
	}
}

func (w *fileTranscriptWriter) Append(callID, format string, event json.RawMessage, offsetMs int64) {
	w.appendEvent(callID, event, offsetMs)
}

func (w *fileTranscriptWriter) AppendSynthetic(callID, format string, event json.RawMessage, offsetMs int64) {
	// Synthetic events accumulate identically; the "_kitsoki" marker is in the
	// caller-supplied body, so verbatim and synthetic share one buffer in order.
	w.appendEvent(callID, event, offsetMs)
}

func (w *fileTranscriptWriter) appendEvent(callID string, event json.RawMessage, offsetMs int64) {
	if len(event) == 0 {
		return
	}
	// Copy the bytes: the caller may reuse the backing slice after returning.
	buf := make(json.RawMessage, len(event))
	copy(buf, event)
	w.mu.Lock()
	w.events[callID] = append(w.events[callID], transcriptEvent{raw: buf, offsetMs: offsetMs})
	w.mu.Unlock()
}

func (w *fileTranscriptWriter) Finalize(callID, format string) *TranscriptRef {
	w.mu.Lock()
	evs := w.events[callID]
	delete(w.events, callID)
	w.mu.Unlock()

	if len(evs) == 0 {
		return nil
	}

	if err := os.MkdirAll(w.dir, 0o755); err != nil {
		return nil
	}

	// Verbatim .jsonl: one event per line, byte-for-byte. We do NOT re-marshal
	// (which would reorder keys / reformat) — the raw bytes are written as-is.
	var jsonl strings.Builder
	for _, e := range evs {
		jsonl.Write(e.raw)
		jsonl.WriteByte('\n')
	}
	jsonlPath := filepath.Join(w.dir, callID+".jsonl")
	if err := os.WriteFile(jsonlPath, []byte(jsonl.String()), 0o644); err != nil {
		return nil
	}

	// Parallel .timings: "<event-index> <ms-offset>" per line, kept out of the
	// verbatim stream so the .jsonl stays pristine for an off-the-shelf parser.
	var timings strings.Builder
	for i, e := range evs {
		timings.WriteString(strconv.Itoa(i))
		timings.WriteByte(' ')
		timings.WriteString(strconv.FormatInt(e.offsetMs, 10))
		timings.WriteByte('\n')
	}
	_ = os.WriteFile(filepath.Join(w.dir, callID+".timings"), []byte(timings.String()), 0o644)

	return &TranscriptRef{
		Format:        format,
		Path:          fmt.Sprintf("%s/%s.jsonl", filepath.Base(w.dir), callID),
		Events:        len(evs),
		SchemaVersion: TranscriptSchemaVersion,
	}
}
