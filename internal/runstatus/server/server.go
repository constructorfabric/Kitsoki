// Package server implements the live runstatus HTTP surface: it serves the
// bundled runstatus SPA and answers the JSON-RPC + SSE contract that the
// SPA's live data source (tools/runstatus/src/data/live-source.ts) expects.
//
// It is the read side of a kitsoki run. Given the JSONL trace a run writes
// (`kitsoki run --trace run.jsonl`) and the app definition, it parses the
// trace into a [runstatus.Snapshot] on demand and streams newly-appended
// events to connected browsers as the run grows the file. It never mutates
// anything.
//
// Why the JSONL trace and not the SQLite session store: the store persists
// only turn/seq/ts/kind/payload — it drops per-event state_path, call_id, and
// parent_turn, which the SPA needs (notably oracle-call pairing by call_id).
// The JSONL trace is the canonical, full-fidelity record. See
// tools/runstatus/CLAUDE.md: the trace itself must always be correct, so the
// live view is built from it directly.
//
// # Endpoints
//
//	GET  /                                     → the bundled SPA (index.html)
//	POST /rpc                                  → JSON-RPC 2.0 control
//	GET  /rpc/events?subscription_id=<id>      → text/event-stream notifications
//
// # JSON-RPC methods (POST /rpc)
//
//	runstatus.sessions.list      {}                                  → []SessionHeader
//	runstatus.session.get        {session_id}                        → SessionHeader
//	runstatus.session.app        {session_id}                        → AppDef
//	runstatus.session.mermaid    {session_id, detail?}               → {source, node_map}
//	runstatus.session.trace      {session_id, since_turn?, until_turn?, limit?}
//	                                                                 → {events, last_turn}
//	runstatus.session.subscribe  {session_id}                        → {subscription_id}
//	runstatus.session.unsubscribe {subscription_id}                  → {ok: true}
//
// v1 serves a single trace (one session). session_id params are accepted but
// the one trace is always served; sessions.list returns 0–1 entries.
//
// # Streaming
//
// After subscribe, the client opens the SSE stream with the returned
// subscription_id. The server polls the trace file and emits one JSON-RPC
// notification per newly-appended event:
//
//	{"jsonrpc":"2.0","method":"runstatus.event",
//	 "params":{"subscription_id":"…","event":{<TraceEvent>}}}
//
// A subscription remembers how many events it has already delivered, so an SSE
// reconnect with the same subscription_id resumes without re-sending events.
package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/runstatus"
	"kitsoki/internal/runstatus/web"
)

// defaultPollInterval is how often the SSE stream re-reads the trace for newly
// appended events. localhost debug tool; 500ms is responsive without busy-spin.
const defaultPollInterval = 500 * time.Millisecond

// Server answers the runstatus live contract for one JSONL trace + app def.
// It is safe for concurrent use.
type Server struct {
	tracePath string
	def       *app.AppDef
	poll      time.Duration

	mu     sync.Mutex
	subs   map[string]*subscription
	nextID int
}

// subscription tracks one SSE stream slot. sent is the number of events
// already delivered, so reconnects resume rather than replay.
type subscription struct {
	id   string
	mu   sync.Mutex
	sent int
}

// Option configures a Server.
type Option func(*Server)

// WithPollInterval overrides the SSE trace-poll interval.
func WithPollInterval(d time.Duration) Option {
	return func(s *Server) {
		if d > 0 {
			s.poll = d
		}
	}
}

// New builds a Server that serves the run recorded in the JSONL trace at
// tracePath, interpreted against def.
func New(tracePath string, def *app.AppDef, opts ...Option) *Server {
	s := &Server{
		tracePath: tracePath,
		def:       def,
		poll:      defaultPollInterval,
		subs:      make(map[string]*subscription),
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Handler returns the HTTP handler for the runstatus surface.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/rpc", s.handleRPC)
	mux.HandleFunc("/rpc/events", s.handleEvents)
	mux.HandleFunc("/", s.handleIndex)
	return mux
}

// ── Trace reads ─────────────────────────────────────────────────────────────

// readEvents parses the trace file into TraceEvents (with task detail
// aggregated). A not-yet-created trace (the run hasn't written anything) is
// treated as empty rather than an error, so the UI can connect first.
func (s *Server) readEvents() ([]runstatus.TraceEvent, error) {
	f, err := os.Open(s.tracePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()

	events, err := runstatus.ParseTrace(f, nil)
	if err != nil {
		return nil, err
	}
	runstatus.AggregateTaskDetails(events)
	return events, nil
}

// snapshot builds the full Snapshot (header + diagram + events) for the trace.
func (s *Server) snapshot() (runstatus.Snapshot, error) {
	events, err := s.readEvents()
	if err != nil {
		return runstatus.Snapshot{}, err
	}
	// AggregateTaskDetails already ran in readEvents; SnapshotFromTrace re-runs
	// it (idempotent — it never overwrites existing detail) and renders the
	// diagram + header.
	return runstatus.SnapshotFromTrace(s.def, events, runstatus.HeaderOverrides{}, true), nil
}

// ── Static SPA ────────────────────────────────────────────────────────────

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	index, err := web.IndexHTML()
	if err != nil {
		// SPA not bundled into this binary — actionable 503 rather than a 404.
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(index)
}

// ── JSON-RPC ─────────────────────────────────────────────────────────────────

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  map[string]any  `json:"params"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// JSON-RPC error codes (subset of the spec plus a generic server error).
const (
	codeParseError    = -32700
	codeMethodMissing = -32601
	codeServerError   = -32000
)

func (s *Server) handleRPC(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req rpcRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeRPC(w, rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: codeParseError, Message: "parse error: " + err.Error()}})
		return
	}

	result, rerr := s.dispatch(req.Method, req.Params)
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	if rerr != nil {
		resp.Error = rerr
	} else {
		resp.Result = result
	}
	writeRPC(w, resp)
}

func writeRPC(w http.ResponseWriter, resp rpcResponse) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// dispatch routes a JSON-RPC method to its handler. It returns either a result
// value or a *rpcError (never both). session_id params are accepted but
// ignored — this server serves the single configured trace.
func (s *Server) dispatch(method string, params map[string]any) (any, *rpcError) {
	if params == nil {
		params = map[string]any{}
	}
	switch method {
	case "runstatus.sessions.list":
		snap, err := s.snapshot()
		if err != nil {
			return nil, serverErr(err)
		}
		// 0 entries until the run has emitted at least one event; else the one
		// session this trace records.
		if len(snap.Events) == 0 {
			return []runstatus.SessionHeader{}, nil
		}
		return []runstatus.SessionHeader{snap.Session}, nil

	case "runstatus.session.get":
		snap, err := s.snapshot()
		if err != nil {
			return nil, serverErr(err)
		}
		return snap.Session, nil

	case "runstatus.session.app":
		return s.def, nil

	case "runstatus.session.mermaid":
		snap, err := s.snapshot()
		if err != nil {
			return nil, serverErr(err)
		}
		return snap.Mermaid, nil

	case "runstatus.session.trace":
		snap, err := s.snapshot()
		if err != nil {
			return nil, serverErr(err)
		}
		return filterTrace(snap, params), nil

	case "runstatus.session.subscribe":
		return s.subscribe()

	case "runstatus.session.unsubscribe":
		id, _ := params["subscription_id"].(string)
		s.unsubscribe(id)
		return map[string]any{"ok": true}, nil

	default:
		return nil, &rpcError{Code: codeMethodMissing, Message: "unknown method: " + method}
	}
}

func serverErr(err error) *rpcError {
	return &rpcError{Code: codeServerError, Message: err.Error()}
}

// traceResult is the runstatus.session.trace response shape.
type traceResult struct {
	Events   []runstatus.TraceEvent `json:"events"`
	LastTurn int                    `json:"last_turn"`
}

// filterTrace slices snap.Events by the optional since_turn / until_turn /
// limit params. last_turn is the high-water turn of the whole run so the
// client knows where to resume on reconnect.
func filterTrace(snap runstatus.Snapshot, params map[string]any) traceResult {
	since, hasSince := intParam(params, "since_turn")
	until, hasUntil := intParam(params, "until_turn")
	limit, hasLimit := intParam(params, "limit")

	out := make([]runstatus.TraceEvent, 0, len(snap.Events))
	for _, ev := range snap.Events {
		if hasSince && ev.Turn < since {
			continue
		}
		if hasUntil && ev.Turn > until {
			continue
		}
		out = append(out, ev)
		if hasLimit && limit > 0 && len(out) >= limit {
			break
		}
	}
	return traceResult{Events: out, LastTurn: snap.Session.Turn}
}

// intParam reads a numeric param (arrives as JSON float64) as an int.
func intParam(params map[string]any, key string) (int, bool) {
	switch v := params[key].(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	default:
		return 0, false
	}
}

// ── Subscriptions + SSE ─────────────────────────────────────────────────────

func (s *Server) subscribe() (map[string]any, *rpcError) {
	// Seed sent with the current event count so the stream carries only events
	// appended after subscribe; the initial load comes from session.trace.
	events, err := s.readEvents()
	if err != nil {
		return nil, serverErr(err)
	}
	s.mu.Lock()
	s.nextID++
	id := fmt.Sprintf("sub-%d", s.nextID)
	s.subs[id] = &subscription{id: id, sent: len(events)}
	s.mu.Unlock()
	return map[string]any{"subscription_id": id}, nil
}

func (s *Server) unsubscribe(id string) {
	s.mu.Lock()
	delete(s.subs, id)
	s.mu.Unlock()
}

func (s *Server) lookupSub(id string) *subscription {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.subs[id]
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("subscription_id")
	sub := s.lookupSub(id)
	if sub == nil {
		http.Error(w, "unknown subscription", http.StatusNotFound)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher.Flush()

	ticker := time.NewTicker(s.poll)
	defer ticker.Stop()

	ctx := r.Context()
	for {
		s.streamNew(w, flusher, sub)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// streamNew emits a runstatus.event notification for every event appended to
// the trace since the subscription's last delivery, then advances the
// watermark.
func (s *Server) streamNew(w http.ResponseWriter, flusher http.Flusher, sub *subscription) {
	sub.mu.Lock()
	defer sub.mu.Unlock()

	events, err := s.readEvents()
	if err != nil || len(events) <= sub.sent {
		return
	}
	for _, ev := range events[sub.sent:] {
		frame := map[string]any{
			"jsonrpc": "2.0",
			"method":  "runstatus.event",
			"params": map[string]any{
				"subscription_id": sub.id,
				"event":           ev,
			},
		}
		b, err := json.Marshal(frame)
		if err != nil {
			continue
		}
		_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
	}
	sub.sent = len(events)
	flusher.Flush()
}
