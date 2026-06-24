// notifications.go — the cross-session notification feed.
//
// Background jobs already post notifications and fan their terminal turn out to
// registered [orchestrator.SessionObserver]s (internal/orchestrator/observer.go).
// This file bridges that fan-out onto a single server-level SSE feed the home
// chrome subscribes to, so a browser badge/toast can light up from *any* live
// session — not just the one on screen.
//
// # The model — consistent with the per-session event poll
//
// The existing /rpc/events stream is a per-session poll over the session's
// trace with a `sub.sent` watermark. The notification feed reuses that exact
// shape: the server keeps an in-memory ring of notification frames, a
// subscription remembers how many it has delivered, and the SSE handler streams
// the new tail each poll tick. The only new piece is *where the frames come
// from*: a [notificationRelay] registered as a SessionObserver per live session
// appends a frame on every background-turn completion.
//
//	OnBackgroundTurn(sid, outcome)
//	    └─ read the session's latest notification + refreshed $inbox counts
//	    └─ append {session_id, notification, unread, needs_attention} to the ring
//	         └─ SSE poll → runstatus.notification frame → browser
package server

import (
	"context"
	"strconv"
	"sync"

	"kitsoki/internal/app"
	"kitsoki/internal/inbox"
	"kitsoki/internal/jobs"
	"kitsoki/internal/orchestrator"
)

// notificationFrame is one entry in the cross-session feed. It is the wire
// shape carried in the `params` of a runstatus.notification SSE frame.
type notificationFrame struct {
	SessionID      string             `json:"session_id"`
	Notification   *jobs.Notification `json:"notification"`
	Unread         int                `json:"unread"`
	NeedsAttention int                `json:"needs_attention"`
}

// notifBufferCap bounds the in-memory ring. The PoC is single-operator, so a
// modest cap is ample; older frames a slow/absent subscriber never read are
// dropped (the browser re-syncs via notifications.list on reconnect).
const notifBufferCap = 256

// notifBuffer is the server-level ring of notification frames plus the
// subscription watermarks. It mirrors the subs/poll model used for trace
// events so the streaming path is uniform. Safe for concurrent use.
type notifBuffer struct {
	mu     sync.Mutex
	frames []notificationFrame
	// dropped counts frames evicted past the cap, so a watermark that falls
	// behind the retained window can be clamped forward rather than re-reading
	// from a negative offset.
	dropped int
	subs    map[string]*notifSub
	nextID  int
}

// notifSub is one cross-session notification subscription. sent is the absolute
// frame index (including dropped frames) already delivered.
type notifSub struct {
	id   string
	mu   sync.Mutex
	sent int
}

func newNotifBuffer() *notifBuffer {
	return &notifBuffer{subs: map[string]*notifSub{}}
}

// append records a frame, evicting the oldest when over capacity.
func (b *notifBuffer) append(f notificationFrame) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.frames = append(b.frames, f)
	if len(b.frames) > notifBufferCap {
		drop := len(b.frames) - notifBufferCap
		b.frames = b.frames[drop:]
		b.dropped += drop
	}
}

// subscribe registers a new subscription seeded at the current head, so the
// stream carries only frames appended after subscribe (reconnect re-syncs via
// notifications.list).
func (b *notifBuffer) subscribe() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextID++
	id := notifSubID(b.nextID)
	b.subs[id] = &notifSub{id: id, sent: b.dropped + len(b.frames)}
	return id
}

func (b *notifBuffer) unsubscribe(id string) {
	b.mu.Lock()
	delete(b.subs, id)
	b.mu.Unlock()
}

func (b *notifBuffer) lookup(id string) *notifSub {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.subs[id]
}

// since returns the frames a subscription has not yet seen and the new
// watermark, clamping a stale watermark forward past dropped frames.
func (b *notifBuffer) since(sent int) (frames []notificationFrame, newWatermark int) {
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
	out := make([]notificationFrame, len(tail))
	copy(out, tail)
	return out, head
}

func notifSubID(n int) string {
	return "notif-sub-" + strconv.Itoa(n)
}

// notificationRelay is the per-session [orchestrator.SessionObserver] that
// turns a background-turn completion into a notification frame on the shared
// buffer. It reads the session's latest notification row and the refreshed
// $inbox counts from the JobStore — OnBackgroundTurn hands us the outcome, not
// the notification, so we re-read the freshly-posted row.
type notificationRelay struct {
	buf  *notifBuffer
	sid  app.SessionID
	jobs *jobs.JobStore
	// publicID is the browser-facing session id (the registry's entry key)
	// that the SPA routes and teleports on. The orchestrator's own sid is an
	// internal id the RPC layer does not resolve, so the SSE frame — and the
	// notification it carries — must present publicID instead. When empty
	// (tests that attach without a registry id) the relay falls back to sid.
	publicID string
}

// OnBackgroundTurn implements [orchestrator.SessionObserver]. It is best-effort:
// a missing JobStore or a read error drops the frame rather than panicking
// (the orchestrator recovers observer panics, but we avoid them anyway).
func (r *notificationRelay) OnBackgroundTurn(sid app.SessionID, _ *orchestrator.TurnOutcome) {
	if r.jobs == nil {
		return
	}
	ctx := context.Background()

	// The freshly-posted row is the newest notification for this session.
	notifs, err := r.jobs.ListNotifications(ctx, sid, 1)
	if err != nil || len(notifs) == 0 {
		return
	}
	n := notifs[0]

	// Refreshed $inbox counts — the same projection the badge and views read.
	counts, err := r.jobs.UnreadCount(ctx, sid)
	if err != nil {
		return
	}
	sum := inbox.InboxSummary{}
	for sev, cnt := range counts {
		sum.Unread += cnt
		if sev == jobs.SeverityActionRequired {
			sum.NeedsAttention += cnt
		}
	}

	// Present the browser-facing session id on both the frame and the carried
	// notification. The SPA routes and teleports on this id (it never sees the
	// orchestrator's internal sid), so a frame tagged with sid would deep-link
	// to a session the RPC layer rejects with "unknown session_id".
	public := r.publicID
	if public == "" {
		public = string(sid)
	}
	n.SessionID = app.SessionID(public)

	r.buf.append(notificationFrame{
		SessionID:      public,
		Notification:   &n,
		Unread:         sum.Unread,
		NeedsAttention: sum.NeedsAttention,
	})
}

// Notifier is the seam the session registry uses to attach a notification relay
// to each new live session. The [Server] implements it; `kitsoki web` injects
// the server into the registry via SetNotifier after construction. Defining the
// seam here (rather than the registry importing the server's buffer directly)
// keeps the import direction one-way: package main depends on the server.
type Notifier interface {
	// AttachSession registers a SessionObserver on orch that relays sid's
	// background-turn notifications onto the cross-session feed. publicID is the
	// browser-facing session id the SPA routes/teleports on (the registry entry
	// key); pass "" to fall back to the orchestrator sid (tests).
	AttachSession(orch *orchestrator.Orchestrator, sid app.SessionID, publicID string, js *jobs.JobStore)

	// EmitCurrentSession pushes a new "current session" value onto the
	// current-session feed (session_current.go) so subscribers receive a
	// runstatus.session.changed frame. The registry calls it from its new/attach
	// code paths after the session id is determined. Pass ok=false to signal "no
	// current session" (a null session_id on the wire).
	EmitCurrentSession(sessionID string, ok bool)
}

// AttachSession implements [Notifier]: it registers a [notificationRelay] for
// the session on the orchestrator's observer set, feeding this server's buffer.
func (s *Server) AttachSession(orch *orchestrator.Orchestrator, sid app.SessionID, publicID string, js *jobs.JobStore) {
	if orch == nil || s.notifs == nil {
		return
	}
	orch.RegisterObserver(&notificationRelay{buf: s.notifs, sid: sid, publicID: publicID, jobs: js})
}
