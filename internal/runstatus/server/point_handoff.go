// point_handoff.go — the transient, one-time-token `/point` window that gives a
// terminal operator spatial parity (docs/tui/spatial-handoff.md).
//
// The terminal can't render pixels, so when the TUI wants the operator to point
// at something it prints an OSC 8 link to `/point?token=…&chromeless=1`. This
// file owns the serving half of that handoff on the runstatus surface:
//
//	GET  /point?token=…&chromeless=1   → the bundled SPA (chrome-less mode); the
//	                                     token is validated but NOT consumed (a
//	                                     reload mustn't 404 mid-point)
//	POST /point/return?token=…         → validates + CONSUMES the token and hands
//	                                     the submitted visual bundle to the
//	                                     waiting turn (the operator-ask return
//	                                     path, carrying a spatial answer)
//
// A token is minted per request keyed to {session, frame, t_ms}; it 404s once
// consumed or after a short TTL, and resolves the channel the parked TUI turn is
// blocked on — exactly mirroring questionRegistry, but the "answer" is a
// host.VisualAmbient bundle, not an option-label map.
package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"kitsoki/internal/host"
	"kitsoki/internal/runstatus/web"
)

// pointTokenTTL bounds how long a minted token is valid before the route 404s.
// The operator may take a beat to switch to the browser and point; a couple of
// minutes is generous without leaving a stale execution surface open. The parked
// turn's own ctx timeout (operatorAskWaitTimeout on the bridge side) is the
// outer bound — this is the inner "the link went stale" guard.
const pointTokenTTL = 5 * time.Minute

// pointHandoff is the one-time-token registry behind the `/point` window. Each
// token maps to a pending bundle channel (the parked turn is blocked on it) plus
// the request that minted it (so the served window knows which frame to show).
// Safe for concurrent use; mirrors questionRegistry's register/answer/cancel
// shape, with a TTL + consumed guard layered on (the "transient" property the
// proposal's open-question 1 lean assigns to the token rather than a second
// process).
type pointHandoff struct {
	mu      sync.Mutex
	pending map[string]*pointSlot
}

// pointSlot is one in-flight handoff: the request context the window displays,
// the channel the parked turn reads the bundle from, when the token expires, and
// whether it has already been consumed (a second return 404s).
type pointSlot struct {
	req      host.SpatialRequest
	ch       chan host.VisualAmbient
	expires  time.Time
	consumed bool
}

func newPointHandoff() *pointHandoff {
	return &pointHandoff{pending: map[string]*pointSlot{}}
}

// mint allocates a one-time token for req and returns it with the buffered
// (cap-1, so resolve never blocks) channel the caller blocks on. The token is
// keyed to {session, frame, t_ms} via the random id plus the stored request, and
// expires after pointTokenTTL.
func (h *pointHandoff) mint(req host.SpatialRequest) (token string, ch chan host.VisualAmbient) {
	token = newPointToken()
	ch = make(chan host.VisualAmbient, 1)
	h.mu.Lock()
	h.pending[token] = &pointSlot{
		req:     req,
		ch:      ch,
		expires: time.Now().Add(pointTokenTTL),
	}
	h.mu.Unlock()
	return token, ch
}

// valid reports whether token names a live (unconsumed, unexpired) slot, and
// returns its request so the served window knows which frame to display. It does
// NOT consume the token — GET /point must survive a reload.
func (h *pointHandoff) valid(token string) (host.SpatialRequest, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	slot, ok := h.pending[token]
	if !ok || slot.consumed || time.Now().After(slot.expires) {
		return host.SpatialRequest{}, false
	}
	return slot.req, true
}

// consume validates token, marks it consumed (so a second return 404s), and
// delivers bundle to the parked turn. Returns false if the token is unknown,
// already consumed, or expired.
func (h *pointHandoff) consume(token string, bundle host.VisualAmbient) bool {
	h.mu.Lock()
	slot, ok := h.pending[token]
	if !ok || slot.consumed || time.Now().After(slot.expires) {
		h.mu.Unlock()
		return false
	}
	slot.consumed = true
	ch := slot.ch
	delete(h.pending, token)
	h.mu.Unlock()
	ch <- bundle
	return true
}

// cancel drops a pending token without delivering a bundle (the parked turn gave
// up — ctx cancelled / timed out / no browser). Idempotent.
func (h *pointHandoff) cancel(token string) {
	h.mu.Lock()
	delete(h.pending, token)
	h.mu.Unlock()
}

// newPointToken returns a fresh 128-bit random token, hex-encoded. crypto/rand
// so a token is unguessable (the route is an execution surface; the proposal
// makes the consumed-token 404 test non-negotiable, and an unguessable token is
// the matching mint-side guard).
func newPointToken() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// handlePoint serves `GET /point?token=…&chromeless=1` — the bundled SPA in
// chrome-less mode. The token is validated (404 on unknown/consumed/expired) but
// NOT consumed, so a reload mid-point still works; the return POST consumes it.
// The SPA itself reads the `chromeless` query flag to suppress nav/timeline/
// editor and render only the picker + chat (see App.vue / chromeless flag).
func (s *Server) handlePoint(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	token := r.URL.Query().Get("token")
	if _, ok := s.points.valid(token); !ok {
		// 404 (not 403) so a consumed/expired/forged token leaks nothing about
		// whether it ever existed — the proposal's non-negotiable guard.
		http.NotFound(w, r)
		return
	}
	index, err := web.IndexHTML()
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(index)
}

// pointReturnRequest is the body the chrome-less window POSTs on Send: the same
// visual bundle shape session.offpath accepts (mirrors host.VisualAmbient's JSON
// tags), so the SPA reuses its existing visualParams serializer.
type pointReturnRequest struct {
	Visual map[string]any `json:"visual"`
}

// handlePointReturn serves `POST /point/return?token=…`. It validates +
// CONSUMES the token and hands the submitted bundle to the parked turn via the
// handoff registry (the operator-ask return path, carrying a spatial answer
// rather than an option label). A consumed/expired/unknown token 404s.
func (s *Server) handlePointReturn(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	token := r.URL.Query().Get("token")
	var body pointReturnRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRPCBodyBytes)).Decode(&body); err != nil {
		http.Error(w, "malformed bundle", http.StatusBadRequest)
		return
	}
	bundle := visualAmbientFromParams(body.Visual)
	if !s.points.consume(token, bundle) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}

// PointServer is a self-contained spatial-handoff window surface: the `/point`
// + `/point/return` routes and the one-time-token registry, with no
// SessionProvider behind it. The TUI (`kitsoki run`, which has no `kitsoki web`
// server in-process) constructs one on demand so it can hand a terminal operator
// the same chrome-less picker the web surface serves — the proposal's
// open-question-1 lean ("`kitsoki run` with no web server attached would need to
// start one on demand").
//
// It reuses the exact handlers + registry the full Server uses by embedding a
// minimal *Server carrying only the pointHandoff, so the serving + token logic
// lives in one place.
type PointServer struct {
	srv *Server
}

// NewPointServer returns a PointServer with a fresh token registry.
func NewPointServer() *PointServer {
	return &PointServer{srv: &Server{points: newPointHandoff()}}
}

// Handler returns the HTTP handler exposing `/point` + `/point/return`.
func (p *PointServer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/point/return", p.srv.handlePointReturn)
	mux.HandleFunc("/point", p.srv.handlePoint)
	return mux
}

// Mint allocates a one-time token for req and returns it plus the channel a
// caller blocks on for the operator's bundle (resolved by the return endpoint).
func (p *PointServer) Mint(req host.SpatialRequest) (token string, ch chan host.VisualAmbient) {
	return p.srv.points.mint(req)
}

// Cancel drops a pending token (the parked turn gave up). Idempotent.
func (p *PointServer) Cancel(token string) { p.srv.points.cancel(token) }
