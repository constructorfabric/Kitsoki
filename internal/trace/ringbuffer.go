package trace

import (
	"bytes"
	"context"
	"log/slog"
	"sync"
)

// RingBuffer is a slog.Handler that keeps the most recent N events in
// memory as marshalled JSON records. Snapshot() returns the current
// buffer as one JSONL blob suitable for writing to a file the agent
// can Read.
//
// Concurrent calls to Handle are safe. The buffer wraps around — when
// the cap is reached, the oldest record is dropped on the next append.
//
// The TUI wires this as the always-on in-memory trace for the meta-mode
// agent. When an EventSink trace path is available the TUI hands the agent
// the JSONL file directly; when the ring is wired as a fallback it dumps
// Snapshot() to a per-session temp file on every Send.
type RingBuffer struct {
	// state is the canonical shared buffer + mutex. WithAttrs /
	// WithGroup clones reuse the same *ringState so every event lands
	// in one buffer regardless of which clone emitted it.
	state *ringState
	// inner marshals records into the per-call scratch buffer held by
	// state. Clones receive their own inner handler reflecting the
	// accumulated WithAttrs / WithGroup chain.
	inner slog.Handler
}

// ringState is the shared mutable core of a RingBuffer. The root
// handler and every clone produced by WithAttrs / WithGroup point at
// the same *ringState so emissions through any of them append to one
// underlying ring.
type ringState struct {
	mu      sync.Mutex
	buf     [][]byte // each entry is one marshalled JSON event with trailing '\n'
	cap     int
	scratch *bytes.Buffer // reused output target for the inner JSONHandler
}

// NewRingBuffer constructs a buffer holding up to size events. size <= 0
// is normalised to 1. Each slog.Record passed through Handle becomes
// one entry; on overflow the oldest is dropped.
func NewRingBuffer(size int) *RingBuffer {
	if size <= 0 {
		size = 1
	}
	st := &ringState{
		buf:     make([][]byte, 0, size),
		cap:     size,
		scratch: &bytes.Buffer{},
	}
	inner := slog.NewJSONHandler(st.scratch, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})
	return &RingBuffer{state: st, inner: inner}
}

// Snapshot returns a copy of the buffer joined into one JSONL blob.
// Each entry is already terminated with a newline by the JSON handler,
// so the result is ready to write to disk verbatim. Safe to call
// concurrently with Handle.
func (r *RingBuffer) Snapshot() []byte {
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	total := 0
	for _, e := range r.state.buf {
		total += len(e)
	}
	out := make([]byte, 0, total)
	for _, e := range r.state.buf {
		out = append(out, e...)
	}
	return out
}

// Len returns the number of records currently in the buffer.
func (r *RingBuffer) Len() int {
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	return len(r.state.buf)
}

// Enabled is always true at the debug level — the ring buffer is the
// "catch everything" sink. File handlers downstream apply their own
// filtering.
func (r *RingBuffer) Enabled(_ context.Context, _ slog.Level) bool {
	return true
}

// Handle marshals the record to JSON bytes (via the inner JSONHandler
// targeting the shared scratch buffer) and appends to the ring. On
// overflow the oldest entry is dropped. Locking the shared mutex
// covers both the scratch-buffer reuse and the slice mutation.
func (r *RingBuffer) Handle(ctx context.Context, rec slog.Record) error {
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	r.state.scratch.Reset()
	if err := r.inner.Handle(ctx, rec); err != nil {
		return err
	}
	entry := make([]byte, r.state.scratch.Len())
	copy(entry, r.state.scratch.Bytes())
	if len(r.state.buf) >= r.state.cap {
		// Drop the oldest. Slicing reuses the underlying array.
		r.state.buf = r.state.buf[1:]
	}
	r.state.buf = append(r.state.buf, entry)
	return nil
}

// WithAttrs returns a clone whose inner handler carries attrs in the
// chain; the buffer state is shared so events land in the same ring.
func (r *RingBuffer) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return r
	}
	return &RingBuffer{state: r.state, inner: r.inner.WithAttrs(attrs)}
}

// WithGroup returns a clone whose inner handler nests under name; the
// buffer state is shared.
func (r *RingBuffer) WithGroup(name string) slog.Handler {
	if name == "" {
		return r
	}
	return &RingBuffer{state: r.state, inner: r.inner.WithGroup(name)}
}

// Compile-time interface check.
var _ slog.Handler = (*RingBuffer)(nil)
