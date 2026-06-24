// Package host — per-call agent-transcript sidecar writer.
//
// Every agent verb whose operator is the claude CLI produces a rich
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
// Concurrency contract: multiple agent calls can interleave on the same
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

// slowPlayEnv is the env var that gates and scales cassette slow-play. It is the
// ONE knob for the feature (see slowPlayScale for the semantics).
const slowPlayEnv = "KITSOKI_CASSETTE_SLOWPLAY"

// slowPlayScale parses the slow-play pace multiplier from the KITSOKI_CASSETTE_SLOWPLAY
// env var and reports whether slow-play is enabled.
//
// Semantics (one float knob, default OFF so the test suite and normal replay stay
// instant and deterministic):
//   - unset / "" / "0" / "off" / anything that doesn't parse as a positive float
//     → (0, false): DISABLED. WriteReplayTranscript behaves exactly as today —
//     the sidecar is written instantly and nothing is streamed.
//   - "1"   → (1, true):  replay at the recorded real-time pace.
//   - "2"   → (2, true):  2× SLOWER than recorded (delays doubled).
//   - "0.5" → (0.5, true): 2× FASTER than recorded (delays halved).
//
// The scale multiplies the recorded inter-event delta (timings[i]-timings[i-1]);
// it never changes which events stream or in what order. A value ≤ 0 is treated
// as disabled rather than "instant", because "stream instantly" is already the
// disabled path and an enabled-but-zero pace would be a confusing no-op.
func slowPlayScale() (scale float64, enabled bool) {
	raw := strings.TrimSpace(os.Getenv(slowPlayEnv))
	switch strings.ToLower(raw) {
	case "", "0", "off", "false", "no":
		return 0, false
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil || f <= 0 {
		return 0, false
	}
	return f, true
}

// replaySleeper is the injectable pacing seam for slow-play. It blocks for d (or
// returns early with ctx.Err() if ctx is cancelled first) between streamed
// replay events. Production wires realReplaySleeper (a ctx-aware time.Sleep);
// tests inject a recording fake so pacing is asserted deterministically with no
// real wall-clock sleeps. d ≤ 0 returns immediately (still honouring ctx).
type replaySleeper func(ctx context.Context, d time.Duration) error

// realReplaySleeper sleeps for d but wakes immediately if ctx is cancelled, so a
// cancelled turn never hangs in the middle of a paced replay.
func realReplaySleeper(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// replaySleeperKey is the context key for an injected replaySleeper (tests only).
type replaySleeperKey struct{}

// withReplaySleeper installs a custom sleeper for slow-play pacing. Used only by
// tests to drive pacing deterministically; production leaves it unset and the
// real ctx-aware time.Sleep is used.
func withReplaySleeper(ctx context.Context, s replaySleeper) context.Context {
	if s == nil {
		return ctx
	}
	return context.WithValue(ctx, replaySleeperKey{}, s)
}

// replaySleeperFrom returns the injected sleeper, or realReplaySleeper when none
// is installed (the production default).
func replaySleeperFrom(ctx context.Context) replaySleeper {
	if s, ok := ctx.Value(replaySleeperKey{}).(replaySleeper); ok && s != nil {
		return s
	}
	return realReplaySleeper
}

// TranscriptSchemaVersion is the schema version stamped into every
// TranscriptRef. Bump it when the sidecar layout (the .jsonl event shape or
// the .timings format) changes incompatibly so a consumer can refuse or adapt.
const TranscriptSchemaVersion = 1

// TranscriptRef is the pointer-only reference attached to agent.call.complete
// in the story trace. It carries no detail — just enough to locate and
// summarize the sidecar so the UI can show an "Agent actions (N)" affordance
// and lazily fetch the full stream on demand.
type TranscriptRef struct {
	// Format names the event schema in the sidecar (e.g. "claude-stream-json",
	// "openai-chat", or a plugin identifier). Mirrors agent.Transcript.Format.
	Format string `json:"format"`
	// Path is the sidecar location relative to the trace dir, e.g.
	// "transcripts/<call_id>.jsonl". Relative so a run dir stays relocatable.
	Path string `json:"path"`
	// Events is the count of events written to the sidecar — the "(N)" badge.
	Events int `json:"events"`
	// SchemaVersion is TranscriptSchemaVersion at write time.
	SchemaVersion int `json:"schema_version"`
}

// TranscriptWriter accumulates one agent call's native execution events into a
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

// callIDKey is the context key for the active agent call's deterministic
// call_id. The claude transport tees its RawEvents into the TranscriptWriter
// under this id (see agent_runner.go runClaudeStreamJSON), and the dispatch
// path Finalizes the same id to obtain the trace pointer. It is set by each
// verb handler / the dispatcher just before the claude subprocess runs, so the
// sidecar filename pairs with the agent.call.complete event's CallID by value.
type callIDKey struct{}

// WithCallID returns a child context carrying the active agent call_id. A nil
// (empty) id is a no-op so callers can set it unguarded. The claude tee in
// runClaudeStreamJSON reads it via CallIDFrom; when absent (no call_id in ctx)
// the tee is skipped, so non-agent claude invocations write no sidecar.
func WithCallID(ctx context.Context, callID string) context.Context {
	if callID == "" {
		return ctx
	}
	return context.WithValue(ctx, callIDKey{}, callID)
}

// CallIDFrom returns the active agent call_id installed in ctx, or "" when none
// is installed (the common case for non-agent claude calls and unit tests).
func CallIDFrom(ctx context.Context) string {
	s, _ := ctx.Value(callIDKey{}).(string)
	return s
}

// callStartKey is the context key for the active agent call's start instant.
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

// WithTranscriptWriter returns a child context carrying w. The agent path
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
// one Finalize covers both producers. See agent_event_sink.go's use on
// appendAgentReturnedEvent.
func finalizeTranscript(ctx context.Context, callID string) *TranscriptRef {
	w := TranscriptWriterFrom(ctx)
	if w == nil || callID == "" {
		return nil
	}
	return w.Finalize(callID, claudeTranscriptFormat)
}

// finalizeOutOfHostTranscript feeds an out-of-host backend's carried-up events
// (agent.AskResponse.Transcript: format + verbatim events + optional timings)
// into the writer for callID, then Finalizes under the backend's own format and
// returns the trace pointer. Used by the dispatcher so out-of-host backends
// (local_llm, subprocess, MCP-HTTP) converge on the SAME sidecar + transcript_ref
// as the in-host claude tee. The caller passes the format/events/timings rather
// than the agent.Transcript type to avoid a host→agent import cycle.
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
// (on the closing AgentReturned/AgentError) flushes the whole arc into one
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
// returns the trace pointer to attach to agent.call.complete. It is the replay
// entry point for the cassette dispatcher (internal/testrunner): on replay the
// recorded transcript is written to the sidecar verbatim — no live tool runs —
// so a replayed run produces a byte-identical sidecar (the golden contract). It
// shares the exact accumulate-then-Finalize logic of the live out-of-host path.
//
// Slow-play (KITSOKI_CASSETTE_SLOWPLAY): when slow-play is enabled AND a
// StreamSink is installed in ctx, the recorded events are ALSO streamed to that
// sink — paced by their recorded per-event timings — through the exact same
// classify→emit path a live claude call uses (emitClassified), so a web/TUI
// stream watching a REPLAY sees streaming behaviour unfold in real-ish time
// instead of instantly. This is REPLAY-ONLY: the live out-of-host path
// (appendOutOfHostTranscript) already streamed live and must not double-emit, so
// the slow-play tee lives here rather than in the shared accumulate helper. The
// sidecar write is unchanged either way — same golden bytes. Disabled (the
// default) restores today's instant, deterministic behaviour.
//
// Returns nil (writing nothing) when no writer is installed in ctx, callID is
// empty, or events is empty, so the caller can attach the result unguarded.
func WriteReplayTranscript(ctx context.Context, callID, format string, events []json.RawMessage, timings []int64) *TranscriptRef {
	slowPlayReplay(ctx, format, events, timings)
	return finalizeOutOfHostTranscript(ctx, callID, format, events, timings)
}

// slowPlayReplay streams recorded events to the StreamSink in ctx, paced by their
// recorded timings, so a live replay watcher sees the agent's actions unfold. It
// is a no-op (returns immediately) unless slow-play is enabled AND a StreamSink
// is installed — the common case (tests, normal replay) pays nothing.
//
// Each event is run through the backend's Classify → emitClassified, identical to
// the live streaming loop, so the StreamEvents are byte-identical in shape to a
// live call. Synthetic "_kitsoki" rows and any other non-classifiable event still
// reach Classify; emitClassified naturally produces a near-empty StreamEvent for
// a row the live classifier skips — to mirror the live path (which never emits a
// Sink event for a synthetic row) we skip emitting those but STILL honour their
// timing slot (the inter-event sleep is taken regardless), so the waterfall pacing
// of the surrounding real events is preserved.
//
// Pacing: between event i-1 and i we sleep (timings[i]-timings[i-1]) * scale ms,
// clamping negative or missing deltas to 0. The sleep is ctx-aware (injected via
// replaySleeperFrom) so a cancelled turn stops emission promptly instead of
// hanging, and tests drive it with a recording fake — no real wall-clock sleeps.
func slowPlayReplay(ctx context.Context, format string, events []json.RawMessage, timings []int64) {
	scale, enabled := slowPlayScale()
	if !enabled {
		return
	}
	sink := StreamSinkFrom(ctx)
	if sink == nil || len(events) == 0 {
		return
	}
	backend := AgentBackendFromContext(ctx)
	sleep := replaySleeperFrom(ctx)

	var prevOffset int64
	for i, raw := range events {
		// Honour this event's timing slot first: sleep the scaled delta from the
		// previous event before emitting it, so the inter-event cadence matches the
		// recording. Clamp negative / missing deltas to 0.
		var offset int64
		if i < len(timings) {
			offset = timings[i]
		}
		delta := offset - prevOffset
		prevOffset = offset
		if delta < 0 {
			delta = 0
		}
		if i > 0 {
			d := time.Duration(float64(delta) * scale * float64(time.Millisecond))
			if err := sleep(ctx, d); err != nil {
				return // ctx cancelled mid-replay: stop emitting promptly.
			}
		}

		var ev map[string]any
		if json.Unmarshal(raw, &ev) != nil {
			continue // unparseable row: honoured its timing slot, emit nothing.
		}
		if isSyntheticKitsokiEvent(ev) {
			continue // synthetic row: timing slot honoured above, but no sink emit.
		}
		emitClassified(ctx, backend.Classify(ev))
	}
}

// isSyntheticKitsokiEvent reports whether ev is a kitsoki-side synthetic row (a
// decide nudge, validator-reject boundary, tool-bypass banner) rather than a
// backend-native stream event. Such rows carry a top-level "_kitsoki" marker key
// (see AppendSynthetic / agent_decide.go) and are skipped by the live classifier,
// so slow-play must not surface them to the StreamSink either.
func isSyntheticKitsokiEvent(ev map[string]any) bool {
	_, ok := ev["_kitsoki"]
	return ok
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
