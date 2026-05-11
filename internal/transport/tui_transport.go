// Package transport — TUI transport adapter.
//
// The TUI transport is the in-process default. It buffers Post calls in a
// thread-safe ring and exposes them via Take/Drain so the running TUI program
// can drain pending messages on each tick and append them to the transcript.
//
// In environments where no TUI is attached (CLI invocations, tests), the
// buffer simply accumulates; consumers can drain or Close().
package transport

import (
	"context"
	"sync"
	"time"

	"kitsoki/internal/ulid"
)

// TUITransport buffers Post calls for in-process consumption by a Bubble Tea
// program (the TUI) or by tests. Goroutine-safe.
type TUITransport struct {
	mu  sync.Mutex
	buf []TUIPost
}

// TUIPost is a buffered Post call captured by TUITransport.
type TUIPost struct {
	// MessageID is the ULID assigned to this post; also returned from Post.
	MessageID string
	Key       SessionKey
	Msg       Message
}

// NewTUITransport constructs an empty TUITransport.
func NewTUITransport() *TUITransport {
	return &TUITransport{}
}

// ID reports the transport ID. Always "tui".
func (t *TUITransport) ID() string { return "tui" }

// Post records the message and returns a generated message ID.
func (t *TUITransport) Post(_ context.Context, key SessionKey, msg Message) (string, error) {
	if msg.Timestamp.IsZero() {
		msg.Timestamp = time.Now().UTC()
	}
	if msg.BotMarker == "" {
		msg.BotMarker = DefaultBotMarker
	}
	id := ulid.New()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, TUIPost{
		MessageID: id,
		Key:       key,
		Msg:       msg,
	})
	return id, nil
}

// Drain returns and clears the buffered Post calls. The returned slice is
// safe to retain — the internal buffer is replaced, not aliased.
func (t *TUITransport) Drain() []TUIPost {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := t.buf
	t.buf = nil
	return out
}

// Pending returns the count of buffered Post calls without draining.
func (t *TUITransport) Pending() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.buf)
}

// Close discards any buffered messages.
func (t *TUITransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = nil
	return nil
}
