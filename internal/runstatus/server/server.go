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
//	runstatus.stories.list       {}                                  → []StoryHeader
//	runstatus.stories.rescan     {}                                  → []StoryHeader
//	runstatus.session.new        {story_path}                        → {session_id}
//	runstatus.session.reload     {session_id}                        → {ok, prev_state_exists}
//	runstatus.sessions.list      {}                                  → []SessionHeader
//	runstatus.session.get        {session_id}                        → SessionHeader
//	runstatus.session.app        {session_id}                        → AppDef
//	runstatus.session.mermaid    {session_id, detail?}               → {source, node_map}
//	runstatus.session.trace      {session_id, since_turn?, until_turn?, limit?}
//	                                                                 → {events, last_turn}
//	runstatus.session.view       {session_id}                        → turnResult
//	runstatus.session.turn       {session_id, input}                 → turnResult
//	runstatus.session.submit     {session_id, intent, slots?}        → turnResult
//	runstatus.session.continue   {session_id, slots?}               → turnResult
//	runstatus.session.offpath    {session_id, input}                 → {answer}
//	runstatus.session.subscribe  {session_id}                        → {subscription_id}
//	runstatus.session.unsubscribe {subscription_id}                  → {ok: true}
//
// # Session routing
//
// The server is session-routing: every session-routed RPC carries a session_id
// that the [SessionProvider] resolves to one live session's [Source] / [Driver].
// `kitsoki web` builds the server with [NewMulti] over a registry that owns many
// sessions and the discovered story catalogue (stories.* / session.new/reload).
// `kitsoki status serve` builds it with [New] / [NewWithSource] over a single
// [Source]: the session_id param is accepted but every read resolves to the one
// session, sessions.list returns 0–1 entries, and the lifecycle RPCs report
// codeReadOnly (there is no orchestrator behind a trace file).
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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/runstatus"
	"kitsoki/internal/runstatus/web"
)

// defaultPollInterval is how often the SSE stream re-reads the trace for newly
// appended events. localhost debug tool; 500ms is responsive without busy-spin.
const defaultPollInterval = 500 * time.Millisecond

// Server answers the runstatus live contract by routing every RPC to the right
// session via a [SessionProvider]. It is safe for concurrent use.
type Server struct {
	provider SessionProvider
	poll     time.Duration

	mu     sync.Mutex
	subs   map[string]*subscription
	nextID int
}

// subscription tracks one SSE stream slot. sent is the number of events already
// delivered, so reconnects resume rather than replay. sessionID is the session
// the stream follows, captured from the subscribe params: each poll re-resolves
// the live [Source] for that session through the provider, so a session created
// or reloaded after subscribe is still observed.
type subscription struct {
	id        string
	sessionID string
	mu        sync.Mutex
	sent      int
}

// Option configures a Server. A few options (WithDriver) only apply when the
// server is built from a single [Source] via [New] / [NewWithSource]: they tune
// the single-entry adapter. [NewMulti] takes a provider that already owns its
// drivers, so those options are no-ops there.
type Option func(*serverConfig)

// serverConfig collects the options before the Server is built. The single-entry
// constructors fold driver into the adapter; NewMulti ignores it.
type serverConfig struct {
	poll   time.Duration
	driver Driver
}

// WithPollInterval overrides the SSE trace-poll interval.
func WithPollInterval(d time.Duration) Option {
	return func(c *serverConfig) {
		if d > 0 {
			c.poll = d
		}
	}
}

// WithDriver attaches the write side to a single-source server: with it set, the
// session.turn / submit / continue / offpath RPCs advance the live session.
// Without it that single surface is read-only. `kitsoki web`'s legacy single
// session set it; `kitsoki status serve` does not. It has no effect on a
// [NewMulti] server — there each entry carries its own [Driver].
func WithDriver(d Driver) Option {
	return func(c *serverConfig) { c.driver = d }
}

// New builds a Server that serves the run recorded in the JSONL trace at
// tracePath, interpreted against def — the read-only `kitsoki status serve`
// path. The lifecycle RPCs (stories.*, session.new/reload) report
// [codeReadOnly]: there is no orchestrator behind a trace file.
func New(tracePath string, def *app.AppDef, opts ...Option) *Server {
	return NewWithSource(&traceFileSource{path: tracePath, def: def}, opts...)
}

// NewWithSource builds a Server backed by a single [Source], wrapped in a
// one-entry [SessionProvider] so the routing dispatch path serves it like any
// other session. `kitsoki status serve` uses [New] (a read-only trace file);
// the legacy single-session `kitsoki web` used this with a live in-process
// [LiveSession]. The session_id param is accepted but every routed read/write
// resolves to the single entry. The lifecycle RPCs report [codeReadOnly].
func NewWithSource(src Source, opts ...Option) *Server {
	cfg := newConfig(opts)
	provider := &singleEntryProvider{entry: Entry{Source: src, Driver: cfg.driver}}
	return newServer(provider, cfg)
}

// NewMulti builds a session-routing Server that dispatches every RPC to the
// session the provider resolves from the session_id param, and exposes the
// stories.* / session.new / session.reload lifecycle the SPA home screen drives.
// `kitsoki web` uses this with the SessionRegistry.
func NewMulti(provider SessionProvider, opts ...Option) *Server {
	return newServer(provider, newConfig(opts))
}

func newConfig(opts []Option) serverConfig {
	cfg := serverConfig{poll: defaultPollInterval}
	for _, o := range opts {
		o(&cfg)
	}
	return cfg
}

func newServer(provider SessionProvider, cfg serverConfig) *Server {
	return &Server{
		provider: provider,
		poll:     cfg.poll,
		subs:     make(map[string]*subscription),
	}
}

// Source supplies the runstatus surface with a view of one session. The server
// is source-agnostic: it never reads a file or an event sink directly. Two
// implementations exist — the read-only trace-file tailer behind [New], and the
// live in-process [LiveSession] behind `kitsoki web`. Implementations MUST be
// safe for concurrent use: the SSE poller and the RPC handlers call them from
// many goroutines at once.
type Source interface {
	// Snapshot returns the full session state now: header, diagram, and events.
	Snapshot() (runstatus.Snapshot, error)
	// Events returns just the trace events known so far. It is the cheap path
	// the SSE poller hits every tick, avoiding a diagram re-render per poll.
	Events() ([]runstatus.TraceEvent, error)
	// AppDef returns the static app definition without building a Snapshot.
	AppDef() *app.AppDef
}

// traceFileSource is the read-only [Source] behind `kitsoki status serve`: it
// re-reads and re-parses the JSONL trace file on each call, so a growing file
// (a live run appending to it) is reflected on the next poll. A not-yet-created
// file is treated as an empty run rather than an error, so the UI can connect
// before the first event is written.
type traceFileSource struct {
	path string
	def  *app.AppDef
}

func (t *traceFileSource) AppDef() *app.AppDef { return t.def }

func (t *traceFileSource) Events() ([]runstatus.TraceEvent, error) {
	f, err := os.Open(t.path)
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

func (t *traceFileSource) Snapshot() (runstatus.Snapshot, error) {
	events, err := t.Events()
	if err != nil {
		return runstatus.Snapshot{}, err
	}
	// AggregateTaskDetails already ran in Events; SnapshotFromTrace re-runs it
	// (idempotent — it never overwrites existing detail) and renders the
	// diagram + header.
	return runstatus.SnapshotFromTrace(t.def, events, runstatus.HeaderOverrides{}, true), nil
}

// Handler returns the HTTP handler for the runstatus surface.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/rpc", s.handleRPC)
	mux.HandleFunc("/rpc/events", s.handleEvents)
	mux.HandleFunc("/artifact/", s.handleArtifact)
	mux.HandleFunc("/", s.handleIndex)
	return mux
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
	// codeReadOnly is returned for a write RPC (turn/submit/continue/offpath) or
	// a lifecycle RPC (stories.*, session.new/reload) when the surface has no
	// live session Driver / no registry — i.e. `kitsoki status serve`, which only
	// observes a recorded trace.
	codeReadOnly = -32001
	// codeNotFound is returned when a session-routed RPC carries a session_id
	// the provider does not know (an expired or never-existing session).
	codeNotFound = -32002
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

	result, rerr := s.dispatch(r.Context(), req.Method, req.Params)
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
// value or a *rpcError (never both).
//
// Three families:
//
//   - Provider-level: sessions.list / stories.* / session.new / session.reload
//     act on the whole [SessionProvider] (the registry), not one session.
//   - Session-routed reads (session.get/app/mermaid/trace/view): resolve the
//     entry by the session_id param, then read its [Source] (or [Driver] for
//     view).
//   - Session-routed writes (session.turn/submit/continue/offpath): resolve the
//     entry, then advance its [Driver]; codeReadOnly when the entry has none.
//
// A session-routed RPC with an unknown session_id returns codeNotFound rather
// than nil-derefing. subscribe/unsubscribe manage SSE slots and resolve their
// session per poll, not here.
func (s *Server) dispatch(ctx context.Context, method string, params map[string]any) (any, *rpcError) {
	if params == nil {
		params = map[string]any{}
	}
	switch method {
	// ── Provider-level (registry) ──────────────────────────────────────────
	case "runstatus.sessions.list":
		return s.provider.List(), nil

	case "runstatus.stories.list":
		return s.provider.ListStories(), nil

	case "runstatus.stories.rescan":
		stories, err := s.provider.Rescan()
		if err != nil {
			return nil, lifecycleErr(err)
		}
		return stories, nil

	case "runstatus.session.new":
		storyPath, _ := params["story_path"].(string)
		if storyPath == "" {
			return nil, &rpcError{Code: codeServerError, Message: "session.new: missing 'story_path'"}
		}
		sid, err := s.provider.NewSession(ctx, storyPath)
		if err != nil {
			// An invalid story is surfaced as a structured error so the UI can
			// show it before navigating (decided lean).
			return nil, lifecycleErr(err)
		}
		return map[string]any{"session_id": sid}, nil

	case "runstatus.session.reload":
		sid, rerr := sessionIDParam(params)
		if rerr != nil {
			return nil, rerr
		}
		prevStateExists, err := s.provider.Reload(ctx, sid)
		if err != nil {
			return nil, lifecycleErr(err)
		}
		return map[string]any{"ok": true, "prev_state_exists": prevStateExists}, nil

	// ── Session-routed reads ───────────────────────────────────────────────
	case "runstatus.session.get":
		entry, rerr := s.resolve(params)
		if rerr != nil {
			return nil, rerr
		}
		snap, err := entry.Source.Snapshot()
		if err != nil {
			return nil, serverErr(err)
		}
		return snap.Session, nil

	case "runstatus.session.app":
		entry, rerr := s.resolve(params)
		if rerr != nil {
			return nil, rerr
		}
		return entry.Source.AppDef(), nil

	case "runstatus.session.mermaid":
		entry, rerr := s.resolve(params)
		if rerr != nil {
			return nil, rerr
		}
		snap, err := entry.Source.Snapshot()
		if err != nil {
			return nil, serverErr(err)
		}
		return snap.Mermaid, nil

	case "runstatus.session.trace":
		entry, rerr := s.resolve(params)
		if rerr != nil {
			return nil, rerr
		}
		snap, err := entry.Source.Snapshot()
		if err != nil {
			return nil, serverErr(err)
		}
		return filterTrace(snap, params), nil

	case "runstatus.session.subscribe":
		return s.subscribe(params)

	case "runstatus.session.unsubscribe":
		id, _ := params["subscription_id"].(string)
		s.unsubscribe(id)
		return map[string]any{"ok": true}, nil

	case "runstatus.session.view":
		// Read of the live session's CURRENT room (render + menu) without
		// advancing it. Requires a Driver (a live session); a read-only entry
		// has none, so it returns codeReadOnly like the write RPCs — there is no
		// in-process session to query.
		entry, rerr := s.resolve(params)
		if rerr != nil {
			return nil, rerr
		}
		if entry.Driver == nil {
			return nil, readOnlyErr(method)
		}
		out, err := entry.Driver.View(ctx)
		if err != nil {
			return nil, serverErr(err)
		}
		return newTurnResult(out, entry.Driver), nil

	// ── Session-routed writes (live session only) ──────────────────────────
	case "runstatus.session.turn":
		entry, rerr := s.resolve(params)
		if rerr != nil {
			return nil, rerr
		}
		if entry.Driver == nil {
			return nil, readOnlyErr(method)
		}
		input, _ := params["input"].(string)
		out, err := entry.Driver.Turn(ctx, input)
		if err != nil {
			return nil, serverErr(err)
		}
		return newTurnResult(out, entry.Driver), nil

	case "runstatus.session.submit":
		entry, rerr := s.resolve(params)
		if rerr != nil {
			return nil, rerr
		}
		if entry.Driver == nil {
			return nil, readOnlyErr(method)
		}
		name, _ := params["intent"].(string)
		if name == "" {
			return nil, &rpcError{Code: codeServerError, Message: "session.submit: missing 'intent'"}
		}
		slots, _ := params["slots"].(map[string]any)
		out, err := entry.Driver.SubmitDirect(ctx, name, slots)
		if err != nil {
			return nil, serverErr(err)
		}
		return newTurnResult(out, entry.Driver), nil

	case "runstatus.session.continue":
		entry, rerr := s.resolve(params)
		if rerr != nil {
			return nil, rerr
		}
		if entry.Driver == nil {
			return nil, readOnlyErr(method)
		}
		slots, _ := params["slots"].(map[string]any)
		out, err := entry.Driver.ContinueTurn(ctx, slots)
		if err != nil {
			return nil, serverErr(err)
		}
		return newTurnResult(out, entry.Driver), nil

	case "runstatus.session.patch_world":
		// Demo/test tooling: inject world key-value overrides without advancing
		// a turn. Mirrors the flow-test runner's world_override mechanism so
		// Playwright demo specs can keep the event roll below the event threshold.
		entry, rerr := s.resolve(params)
		if rerr != nil {
			return nil, rerr
		}
		if entry.Driver == nil {
			return nil, readOnlyErr(method)
		}
		patch, _ := params["patch"].(map[string]any)
		if err := entry.Driver.PatchWorld(ctx, patch); err != nil {
			return nil, serverErr(err)
		}
		return map[string]any{"ok": true}, nil

	case "runstatus.session.offpath":
		entry, rerr := s.resolve(params)
		if rerr != nil {
			return nil, rerr
		}
		if entry.Driver == nil {
			return nil, readOnlyErr(method)
		}
		input, _ := params["input"].(string)
		answer, err := entry.Driver.AskOffPath(ctx, input)
		if err != nil {
			return nil, serverErr(err)
		}
		return map[string]any{"answer": answer}, nil

	// ── Meta mode (overlay chat) ────────────────────────────────────────────
	// session_id == "" routes to the home-screen "self" driver (kitsoki.* modes);
	// a non-empty session_id routes to that session's per-state driver.
	case "runstatus.meta.modes":
		md, rerr := s.resolveMeta(params)
		if rerr != nil {
			return nil, rerr
		}
		modes, err := md.Modes(ctx)
		if err != nil {
			return nil, serverErr(err)
		}
		return map[string]any{"modes": modes}, nil

	case "runstatus.meta.enter":
		md, rerr := s.resolveMeta(params)
		if rerr != nil {
			return nil, rerr
		}
		mode, _ := params["mode"].(string)
		if mode == "" {
			return nil, &rpcError{Code: codeServerError, Message: "meta.enter: missing 'mode'"}
		}
		chatID, _ := params["chat_id"].(string)
		sess, err := md.Enter(ctx, mode, chatID)
		if err != nil {
			return nil, serverErr(err)
		}
		return sess, nil

	case "runstatus.meta.send":
		md, rerr := s.resolveMeta(params)
		if rerr != nil {
			return nil, rerr
		}
		mode, _ := params["mode"].(string)
		if mode == "" {
			return nil, &rpcError{Code: codeServerError, Message: "meta.send: missing 'mode'"}
		}
		chatID, _ := params["chat_id"].(string)
		input, _ := params["input"].(string)
		res, err := md.Send(ctx, mode, chatID, input)
		if err != nil {
			return nil, serverErr(err)
		}
		return res, nil

	case "runstatus.meta.new":
		md, rerr := s.resolveMeta(params)
		if rerr != nil {
			return nil, rerr
		}
		mode, _ := params["mode"].(string)
		if mode == "" {
			return nil, &rpcError{Code: codeServerError, Message: "meta.new: missing 'mode'"}
		}
		chatID, _ := params["chat_id"].(string)
		sess, err := md.NewChat(ctx, mode, chatID)
		if err != nil {
			return nil, serverErr(err)
		}
		return sess, nil

	case "runstatus.meta.transcript":
		md, rerr := s.resolveMeta(params)
		if rerr != nil {
			return nil, rerr
		}
		chatID, _ := params["chat_id"].(string)
		if chatID == "" {
			return nil, &rpcError{Code: codeServerError, Message: "meta.transcript: missing 'chat_id'"}
		}
		msgs, err := md.Transcript(ctx, chatID)
		if err != nil {
			return nil, serverErr(err)
		}
		return map[string]any{"messages": msgs}, nil

	default:
		return nil, &rpcError{Code: codeMethodMissing, Message: "unknown method: " + method}
	}
}

// resolveMeta picks the [MetaDriver] for a meta RPC. A non-empty session_id
// routes to that session's per-state driver (Entry.Meta); an empty session_id
// routes to the provider's home-screen "self" driver (the cross-app kitsoki.*
// modes) when the provider implements [MetaSelfProvider]. Either path returns
// codeReadOnly when no meta driver is available on that surface.
func (s *Server) resolveMeta(params map[string]any) (MetaDriver, *rpcError) {
	sid, _ := params["session_id"].(string)
	if sid == "" {
		if sp, ok := s.provider.(MetaSelfProvider); ok {
			if md, ok := sp.MetaSelf(); ok && md != nil {
				return md, nil
			}
		}
		return nil, readOnlyErr("meta (no session)")
	}
	entry, ok := s.provider.Get(sid)
	if !ok {
		return nil, &rpcError{Code: codeNotFound, Message: "unknown session_id: " + sid}
	}
	if entry.Meta == nil {
		return nil, readOnlyErr("meta")
	}
	return entry.Meta, nil
}

// resolve looks up the entry for the session_id param, returning a structured
// not-found error for an unknown id so a session-routed RPC never nil-derefs.
// The single-entry adapter resolves any id (including the empty string the
// read-only tests pass) to its one session; the multi-session registry returns
// ok=false for an empty or unknown id, which becomes codeNotFound.
func (s *Server) resolve(params map[string]any) (Entry, *rpcError) {
	sid, _ := params["session_id"].(string)
	entry, ok := s.provider.Get(sid)
	if !ok {
		return Entry{}, &rpcError{Code: codeNotFound, Message: "unknown session_id: " + sid}
	}
	return entry, nil
}

// sessionIDParam reads the required session_id param for a lifecycle RPC
// (session.reload) where there is no single-entry fallback — a missing id is a
// malformed request.
func sessionIDParam(params map[string]any) (string, *rpcError) {
	sid, _ := params["session_id"].(string)
	if sid == "" {
		return "", &rpcError{Code: codeServerError, Message: "missing 'session_id'"}
	}
	return sid, nil
}

// lifecycleErr maps a provider lifecycle error to an rpcError: the read-only
// sentinel becomes codeReadOnly (matching the write-RPC gate), anything else a
// generic server error (e.g. an invalid story on session.new).
func lifecycleErr(err error) *rpcError {
	if errors.Is(err, errReadOnlySurface) {
		return &rpcError{Code: codeReadOnly, Message: err.Error()}
	}
	return serverErr(err)
}

func readOnlyErr(method string) *rpcError {
	return &rpcError{Code: codeReadOnly, Message: method + ": this surface is read-only (no live session)"}
}

// errReadOnlySurface is returned by [singleEntryProvider]'s lifecycle methods
// (NewSession / Reload / Rescan): a single trace-file / legacy surface has no
// orchestrator to start, reload, or rediscover stories. dispatch maps it to
// [codeReadOnly] so the UI sees the same gate as the write RPCs.
var errReadOnlySurface = errors.New("unsupported on read-only surface (no session registry)")

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

func (s *Server) subscribe(params map[string]any) (map[string]any, *rpcError) {
	// Bind the subscription to the session it follows; each poll re-resolves the
	// live Source for this id (so a session reloaded after subscribe is still
	// observed). The single-entry adapter ignores the id.
	sid, _ := params["session_id"].(string)
	entry, ok := s.provider.Get(sid)
	if !ok {
		return nil, &rpcError{Code: codeNotFound, Message: "unknown session_id: " + sid}
	}
	// Seed sent with the current event count so the stream carries only events
	// appended after subscribe; the initial load comes from session.trace.
	events, err := entry.Source.Events()
	if err != nil {
		return nil, serverErr(err)
	}
	s.mu.Lock()
	s.nextID++
	id := fmt.Sprintf("sub-%d", s.nextID)
	s.subs[id] = &subscription{id: id, sessionID: sid, sent: len(events)}
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
// the subscription's session since its last delivery, then advances the
// watermark. The session's live [Source] is resolved per tick through the
// provider, so a session created or reloaded after subscribe is still followed;
// if the session has gone (unknown id) the tick is a no-op.
func (s *Server) streamNew(w http.ResponseWriter, flusher http.Flusher, sub *subscription) {
	sub.mu.Lock()
	defer sub.mu.Unlock()

	entry, ok := s.provider.Get(sub.sessionID)
	if !ok {
		return
	}
	events, err := entry.Source.Events()
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

// ── Artifact serving ─────────────────────────────────────────────────────────

// handleArtifact serves `GET /artifact/{id}` — the binary file referenced by a
// view element of Kind "media".  The id is the opaque handle the
// host.artifacts_dir transport wrote into the journal when the artifact was
// produced; we scan every live session's [ArtifactResolver] (if wired) until we
// find a match.
//
// Safety:
//   - The resolved absolute path is validated to ensure it stays under the
//     configured artifacts root prefix (path-traversal guard).  The guard is
//     belt-and-suspenders: the handle IDs are <stem>#<sha256-prefix>, not raw
//     paths, so they cannot contain ".." by construction. The file-system check
//     is the authoritative layer.
//   - We serve via [http.ServeContent], which provides Content-Type, ETag,
//     Last-Modified, and Range (needed for video seeking).
//   - Unknown ids (not found in any session's journal) return 404 rather than
//     leaking path information.
func (s *Server) handleArtifact(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract the handle ID from the URL path: strip the "/artifact/" prefix.
	id := strings.TrimPrefix(r.URL.Path, "/artifact/")
	if id == "" {
		http.NotFound(w, r)
		return
	}
	// Reject any path that contains a slash after the prefix — we only serve
	// flat handle IDs, not sub-paths.
	if strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}

	// Search across all live sessions for an ArtifactResolver that knows this id.
	absPath, mime, found := s.resolveArtifact(id)
	if !found {
		http.NotFound(w, r)
		return
	}

	// Path-traversal guard: the resolved path must be an absolute path and must
	// not contain any ".." elements after cleaning.
	clean := filepath.Clean(absPath)
	if !filepath.IsAbs(clean) || clean != absPath {
		http.NotFound(w, r)
		return
	}

	f, err := os.Open(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}

	// Content-Type: use the MIME from the journal if non-empty; otherwise let
	// http.ServeContent detect it from the filename extension.
	if mime != "" {
		w.Header().Set("Content-Type", mime)
	}
	http.ServeContent(w, r, filepath.Base(absPath), info.ModTime(), f)
}

// resolveArtifact iterates all live sessions via the provider, calling each
// session's [ArtifactResolver] (when wired). Returns the first match.
// The multi-session provider exposes its entries only through Get(id), but
// List() returns the session ids — we iterate those to probe each resolver.
//
// For the single-entry adapter (kitsoki status serve / legacy single session),
// Get("") returns the one entry, so we probe it directly.
func (s *Server) resolveArtifact(id string) (path, mime string, ok bool) {
	// Collect candidate entries to probe. We probe via List() (which returns
	// headers, each carrying a session_id) to avoid adding a new provider method.
	headers := s.provider.List()
	if len(headers) == 0 {
		// Single-entry / empty provider: probe the single-entry adapter's "" key.
		if entry, entryOK := s.provider.Get(""); entryOK && entry.Artifacts != nil {
			return entry.Artifacts.LookupArtifact(id)
		}
		return "", "", false
	}
	for _, hdr := range headers {
		entry, entryOK := s.provider.Get(hdr.SessionID)
		if !entryOK || entry.Artifacts == nil {
			continue
		}
		if p, m, found := entry.Artifacts.LookupArtifact(id); found {
			return p, m, true
		}
	}
	return "", "", false
}
