// session_current.go — the "current session" SSE feed.
//
// Trace-only and graph-only VS Code windows have no chat to start a session, so
// they must discover and follow the single active session. This file mirrors the
// cross-session notification feed (notifications.go) for a much smaller payload:
// the id of the session most recently created (runstatus.session.new) or attached
// (runstatus.session.attach).
//
// # The model — mirrors the notification feed
//
// The server keeps a tiny in-memory ring of "current session changed" frames, a
// subscription remembers how many it has delivered, and the SSE handler streams
// the new tail each poll tick. The only differences from notifications.go are the
// frame shape (just a session_id) and that a fresh subscription is SEEDED with the
// current value so a late subscriber syncs immediately rather than waiting for the
// next change. The frames are appended by the registry through the [Notifier] seam
// (EmitCurrentSession) inside the new/attach code paths.
//
//	registry.NewSession / AttachExternal
//	    └─ notifier.EmitCurrentSession(id, true)
//	         └─ append {session_id: id} to the ring
//	              └─ SSE poll → runstatus.session.changed frame → client
package server

import (
	"strconv"
	"sync"
)

// currentSessionFrame is one entry in the current-session feed. It is the wire
// shape carried in the `params` of a runstatus.session.changed SSE frame. A nil
// SessionID marshals to JSON null (no current session).
type currentSessionFrame struct {
	SessionID *string `json:"session_id"`
}

// currentBufferCap bounds the in-memory ring. Current-session changes are rare
// (one per session create/attach), so a modest cap is ample; a slow subscriber
// that falls behind re-syncs to the latest value via the seed-on-subscribe path
// and the watermark clamp.
const currentBufferCap = 64

// currentBuffer is the server-level ring of current-session frames plus the
// subscription watermarks. It mirrors notifBuffer so the streaming path is
// uniform. Safe for concurrent use. It also retains `latest` so a new
// subscription can be seeded with the current value.
type currentBuffer struct {
	mu      sync.Mutex
	frames  []currentSessionFrame
	dropped int
	subs    map[string]*currentSub
	nextID  int

	// latest is the most recent current-session value, retained so a fresh
	// subscription is seeded with it (immediate sync for a late subscriber).
	// hasLatest is false until the first EmitCurrentSession.
	latest    *string
	hasLatest bool
}

// currentSub is one current-session subscription. sent is the absolute frame
// index (including dropped frames) already delivered.
type currentSub struct {
	id   string
	mu   sync.Mutex
	sent int
}

func newCurrentBuffer() *currentBuffer {
	return &currentBuffer{subs: map[string]*currentSub{}}
}

// set records a new current-session value as a frame, updating `latest` so later
// subscribers seed from it, and evicting the oldest frame when over capacity.
func (b *currentBuffer) set(sessionID *string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.latest = sessionID
	b.hasLatest = true
	b.frames = append(b.frames, currentSessionFrame{SessionID: sessionID})
	if len(b.frames) > currentBufferCap {
		drop := len(b.frames) - currentBufferCap
		b.frames = b.frames[drop:]
		b.dropped += drop
	}
}

// subscribe registers a new subscription. Unlike the notification feed it is
// seeded at the head MINUS one when a current value exists, so the first poll
// immediately re-delivers the latest value (a late subscriber syncs at once).
func (b *currentBuffer) subscribe() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextID++
	id := currentSubID(b.nextID)
	head := b.dropped + len(b.frames)
	sent := head
	// Seed: if there is a current value, replay just the latest frame so the new
	// subscriber receives it on its first poll. When the ring is non-empty the
	// last frame already carries `latest`; rewind the watermark by one to emit it.
	if b.hasLatest && len(b.frames) > 0 {
		sent = head - 1
	} else if b.hasLatest {
		// The latest frame was evicted past the cap; append a fresh frame carrying
		// the current value so the new subscriber still syncs.
		b.frames = append(b.frames, currentSessionFrame{SessionID: b.latest})
		head = b.dropped + len(b.frames)
		sent = head - 1
	}
	b.subs[id] = &currentSub{id: id, sent: sent}
	return id
}

func (b *currentBuffer) unsubscribe(id string) {
	b.mu.Lock()
	delete(b.subs, id)
	b.mu.Unlock()
}

func (b *currentBuffer) lookup(id string) *currentSub {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.subs[id]
}

// since returns the frames a subscription has not yet seen and the new watermark,
// clamping a stale watermark forward past dropped frames.
func (b *currentBuffer) since(sent int) (frames []currentSessionFrame, newWatermark int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	head := b.dropped + len(b.frames)
	if sent < b.dropped {
		sent = b.dropped
	}
	if sent >= head {
		return nil, head
	}
	tail := b.frames[sent-b.dropped:]
	out := make([]currentSessionFrame, len(tail))
	copy(out, tail)
	return out, head
}

// currentValue returns the latest current-session value, mirroring the
// CurrentSession provider answer for the runstatus.session.current RPC fallback
// when the provider does not implement [CurrentSessionProvider].
func (b *currentBuffer) currentValue() (*string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.latest, b.hasLatest
}

func currentSubID(n int) string {
	return "current-sub-" + strconv.Itoa(n)
}

// EmitCurrentSession implements the current-session half of [Notifier]: it pushes
// a new current-session value onto the feed so subscribers receive a
// runstatus.session.changed frame. Pass ok=false (any sessionID) to signal "no
// current session" (a null session_id on the wire). The registry calls this from
// its new/attach code paths after the session id is determined.
func (s *Server) EmitCurrentSession(sessionID string, ok bool) {
	if s.current == nil {
		return
	}
	if ok {
		id := sessionID
		s.current.set(&id)
	} else {
		s.current.set(nil)
	}
}
