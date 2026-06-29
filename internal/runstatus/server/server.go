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
// parent_turn, which the SPA needs (notably agent-call pairing by call_id).
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
//	runstatus.session.staleness  {session_id}                        → {stale, diff}
//	runstatus.sessions.list      {}                                  → []SessionHeader
//	runstatus.work.list          {}                                  → {summary, sessions[], items[]}
//	runstatus.chat.show          {session_id, chat_id, since_seq?}   → {ok, chat, pty?, messages[]}
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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/dynamicworkflow"
	"kitsoki/internal/helpdocs"
	"kitsoki/internal/host"
	"kitsoki/internal/jobs"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/runstatus"
	"kitsoki/internal/runstatus/harrec"
	"kitsoki/internal/runstatus/web"
	"kitsoki/internal/store"
	"kitsoki/internal/userfacing"
	"kitsoki/internal/world"
)

// bugRecorderCapacity is the number of most-recent /rpc request/response pairs
// the HAR ring buffer retains for a bug report. Sized to comfortably cover the
// interactions leading up to a "report a bug" click without unbounded growth.
const bugRecorderCapacity = 256

// defaultPollInterval is how often the SSE stream re-reads the trace for newly
// appended events. localhost debug tool; 500ms is responsive without busy-spin.
const defaultPollInterval = 500 * time.Millisecond

// actorHeader is the request header a fronting proxy / future auth layer sets to
// attribute a drive turn to a real operator. It is the highest-precedence
// identity source (above the `actor` RPC param and the configured default).
const actorHeader = "X-Kitsoki-Actor"

// actorCtxKey keys the request's resolved operator identity in the dispatch
// context (set from actorHeader in handleRPC).
type actorCtxKey struct{}

// authorSlot is the reserved slot name a story's `last_reply_author` effect
// reads (`slots.author ?? 'human'`). The server injects the resolved operator
// identity here before a drive turn so the recorded author is a real principal.
const authorSlot = "author"

// resolveActor picks the operator identity for a drive turn, highest precedence
// first: the X-Kitsoki-Actor request header (carried on ctx) > an explicit
// `actor` RPC param > the server's configured default actor. ok is false when
// none of the three supplies a non-empty value.
func (s *Server) resolveActor(ctx context.Context, params map[string]any) (string, bool) {
	if v, ok := ctx.Value(actorCtxKey{}).(string); ok && v != "" {
		return v, true
	}
	if v, ok := params["actor"].(string); ok {
		if v = strings.TrimSpace(v); v != "" {
			return v, true
		}
	}
	if s.defaultActor != "" {
		return s.defaultActor, true
	}
	return "", false
}

// injectAuthor returns slots with the resolved operator identity set under
// authorSlot, unless the caller already supplied an explicit author (an
// author the operator typed wins over the ambient identity). A nil slots map is
// allocated only when there is an author to record. When no identity resolves,
// slots is returned untouched — the story falls back to its own default.
func (s *Server) injectAuthor(ctx context.Context, params map[string]any, slots map[string]any) map[string]any {
	author, ok := s.resolveActor(ctx, params)
	if !ok {
		return slots
	}
	if slots == nil {
		slots = map[string]any{}
	}
	if existing, present := slots[authorSlot]; !present || existing == nil || existing == "" {
		slots[authorSlot] = author
	}
	return slots
}

// Server answers the runstatus live contract by routing every RPC to the right
// session via a [SessionProvider]. It is safe for concurrent use.
type Server struct {
	provider SessionProvider
	poll     time.Duration

	// defaultActor is the lowest-precedence operator identity injected as
	// slots.author on a drive turn (see WithDefaultActor). Empty = none.
	defaultActor string

	mu     sync.Mutex
	subs   map[string]*subscription
	nextID int

	// notifs is the cross-session notification feed buffer (notifications.go).
	// Always non-nil; a relay is attached per live session via AttachSession.
	notifs *notifBuffer

	// questions is the per-session forwarded-question feed (operator_questions.go)
	// and qreg is the pending-answer registry that lets answer_question unblock a
	// parked agent turn. Both always non-nil.
	questions *questionBuffer
	qreg      *questionRegistry

	// current is the "current session" feed buffer (session_current.go). It holds
	// the id of the most recently created/attached session so trace-only and
	// graph-only surfaces (no chat) can discover and follow it. Always non-nil; the
	// registry pushes values through EmitCurrentSession.
	current *currentBuffer

	// points is the one-time-token registry behind the transient `/point`
	// spatial-handoff window (point_handoff.go): it mints a token per request,
	// serves the chrome-less SPA, and resolves the parked turn when the window
	// POSTs its visual bundle. Always non-nil.
	points *pointHandoff

	// recorder is the HAR ring buffer capturing the last N /rpc request/response
	// pairs. runstatus.bug.report snapshots + scrubs it into the bug's artifacts.
	// Always non-nil.
	recorder *harrec.Recorder

	// bugRoot is the repo root under which runstatus.bug.report writes
	// issues/bugs/<id>.md (+ sibling <id>.artifacts/). Empty => resolved per
	// request (story_path's repo dir, else cwd). Set via WithBugRoot by
	// `kitsoki web`.
	bugRoot string

	// ticketRepo, when set (WithTicketRepo / `kitsoki web --ticket-repo`), routes
	// runstatus.bug.report to a GitHub issue on that owner/repo (via
	// host.GitHubFileBug — evidence saved under .artifacts for developer-local
	// review) INSTEAD of a local issues/bugs/<id>.md file.
	ticketRepo string

	// workflowRoot is the repo root dynamic workflow drafts should use for
	// their scratch package and promotion/export defaults. Empty means the
	// workflow RPC family stays disabled on this surface.
	workflowRoot string

	// captureStore holds scrubbed HAR snapshots between runstatus.bug.preview
	// and the confirming runstatus.bug.report so the filed capture is identical
	// to the one reviewed. Bounded by captureCap + captureTTL, swept on put.
	// Guarded by captureMu. captureSeq disambiguates ids minted in the same
	// nanosecond. See bug_capture.go.
	captureMu    sync.Mutex
	captureStore map[string]*capSnap
	captureSeq   uint64

	// activeTurns maps a live session id → the cancel func for its in-flight
	// streamed turn, so runstatus.session.cancel can abort a running agent (the
	// "Stop" button in the web chat). The streamed turn runs on a context
	// deliberately DETACHED from the HTTP request (handleTurnStream's
	// WithoutCancel), so a client disconnect can't cancel it — this registry is
	// the ONLY lever that does. Last-write-wins per session (turns are serialised
	// per session by the orchestrator, so at most one is meaningfully in flight).
	// Guarded by turnMu. See handleTurnStream / cancelActiveTurn.
	turnMu      sync.Mutex
	activeTurns map[string]*activeTurn
}

// activeTurn is one in-flight streamed turn's cancel handle. Stored by pointer
// so the release func can compare identity (did a newer turn replace me?)
// without comparing func values.
type activeTurn struct {
	cancel context.CancelFunc
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
	poll         time.Duration
	driver       Driver
	defaultActor string
	bugRoot      string
	ticketRepo   string
	workflowRoot string
}

// WithBugRoot sets the repo root under which runstatus.bug.report writes
// issues/bugs/<id>.md and the sibling <id>.artifacts/ dir. `kitsoki web` wires
// this to its primary stories repo root so web-filed bugs land in the same
// repo the CLI `kitsoki bug` command targets. Empty (the default) means the
// handler resolves a root per request: the story_path's directory when given,
// else the process cwd.
func WithBugRoot(dir string) Option {
	return func(c *serverConfig) { c.bugRoot = strings.TrimSpace(dir) }
}

// WithTicketRepo routes runstatus.bug.report to a GitHub issue on the given
// owner/repo with evidence saved under .artifacts for developer-local review,
// instead of a local issues/bugs/<id>.md file. Wired by `kitsoki web
// --ticket-repo`. Empty (the default) keeps the local-file behaviour.
func WithTicketRepo(repo string) Option {
	return func(c *serverConfig) { c.ticketRepo = strings.TrimSpace(repo) }
}

// WithWorkflowRoot sets the repo root dynamic workflow RPCs use for draft
// creation, validation, and export defaults. Empty disables the workflow RPC
// family on this server.
func WithWorkflowRoot(dir string) Option {
	return func(c *serverConfig) { c.workflowRoot = strings.TrimSpace(dir) }
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

// WithDefaultActor configures the operator identity recorded on a drive turn
// (session.submit / session.continue) when no other source supplies one. It is
// the lowest-precedence identity source: the `X-Kitsoki-Actor` request header
// wins over it, and an explicit `actor` RPC param wins over the header. The
// resolved value is injected as `slots.author` before the Driver advances the
// turn, so a story's `last_reply_author` records a known principal instead of
// the literal `'human'` fallback. `kitsoki web --actor <name>` sets it; empty
// means "no configured default" (turns fall back to whatever the story does
// with an absent `slots.author`).
//
// See the drive-vs-transport model in docs/architecture/transports.md.
func WithDefaultActor(actor string) Option {
	return func(c *serverConfig) { c.defaultActor = actor }
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
		provider:     provider,
		poll:         cfg.poll,
		defaultActor: cfg.defaultActor,
		subs:         make(map[string]*subscription),
		notifs:       newNotifBuffer(),
		questions:    newQuestionBuffer(),
		qreg:         newQuestionRegistry(),
		current:      newCurrentBuffer(),
		points:       newPointHandoff(),
		recorder:     harrec.New(bugRecorderCapacity),
		bugRoot:      cfg.bugRoot,
		ticketRepo:   cfg.ticketRepo,
		workflowRoot: cfg.workflowRoot,
		captureStore: make(map[string]*capSnap),
		activeTurns:  make(map[string]*activeTurn),
	}
}

// registerActiveTurn records cancel as the abort lever for the in-flight
// streamed turn on session sid (keyed by the session_id the client sent, which
// it reuses verbatim for runstatus.session.cancel). Returns an unregister func
// that removes THIS registration (only if it's still the current one) WITHOUT
// firing cancel — call it on handler exit so a finished turn (or one whose
// client disconnected) frees its slot without clobbering a newer turn that raced
// in. Crucially it must NOT cancel: the handler also returns on a client
// disconnect, and a disconnected turn is meant to run to completion on its
// detached context (see handleTurnStream's WithoutCancel). The context's cancel
// is fired only by cancelActiveTurn (operator Stop) or by the turn goroutine's
// own defer once it finishes.
func (s *Server) registerActiveTurn(sid string, cancel context.CancelFunc) (unregister func()) {
	at := &activeTurn{cancel: cancel}
	s.turnMu.Lock()
	s.activeTurns[sid] = at
	s.turnMu.Unlock()
	return func() {
		s.turnMu.Lock()
		if s.activeTurns[sid] == at {
			delete(s.activeTurns, sid)
		}
		s.turnMu.Unlock()
	}
}

// cancelActiveTurn aborts the in-flight streamed turn for sid, if any. Returns
// true when a turn was registered (and thus cancelled). The cancellation
// propagates down the detached execution context to the agent subprocess
// (exec.CommandContext → SIGKILL), so the agent actually stops — not just the
// frontend. Idempotent: a second call after the turn already cleared its slot is
// a no-op returning false.
func (s *Server) cancelActiveTurn(sid string) bool {
	s.turnMu.Lock()
	at, ok := s.activeTurns[sid]
	s.turnMu.Unlock()
	if !ok {
		return false
	}
	at.cancel()
	return true
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

// AnnotationSource is an optional extension of [Source] that exposes the path
// to the session's annotation sidecar JSONL. Sources that know their on-disk
// location (traceFileSource, LiveSession backed by a JSONLSink) implement it;
// in-memory / test sources do not, and annotation RPCs are silently skipped for
// those.
type AnnotationSource interface {
	AnnotationPath() string
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

// AnnotationPath implements [AnnotationSource]: the sidecar lives at
// <trace-base>.annotations.jsonl, e.g. if the trace is run.jsonl the sidecar
// is run.annotations.jsonl. This is the same name structure the trace-file
// scheme uses; LiveSession instead derives its path from store.SessionsDir.
func (t *traceFileSource) AnnotationPath() string {
	// Strip the .jsonl extension (if present) and append .annotations.jsonl so
	// the sidecar sits next to the trace in the same directory.
	base := t.path
	if len(base) > 6 && base[len(base)-6:] == ".jsonl" {
		base = base[:len(base)-6]
	}
	return base + ".annotations.jsonl"
}

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
	snap := runstatus.SnapshotFromTrace(t.def, events, runstatus.HeaderOverrides{}, true)
	// Load annotations from the sidecar (silently ignored if absent).
	if anns, aerr := runstatus.LoadAnnotations(t.AnnotationPath()); aerr == nil && len(anns) > 0 {
		snap.Annotations = anns
	}
	return snap, nil
}

// Handler returns the HTTP handler for the runstatus surface.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/rpc", s.handleRPC)
	mux.HandleFunc("/rpc/events", s.handleEvents)
	mux.HandleFunc("/rpc/notifications", s.handleNotifications)
	mux.HandleFunc("/rpc/session-current", s.handleSessionCurrent)
	mux.HandleFunc("/rpc/questions", s.handleQuestions)
	mux.HandleFunc("/artifact/", s.handleArtifact)
	// Transient spatial-handoff window: the chrome-less SPA + its one-time-token
	// return endpoint (point_handoff.go). The longer "/point/return" pattern is
	// registered first so it wins over "/point" for the POST path.
	mux.HandleFunc("/point/return", s.handlePointReturn)
	mux.HandleFunc("/point", s.handlePoint)
	mux.HandleFunc("/rpc/meta-stream", s.handleMetaStream)
	mux.HandleFunc("/rpc/turn-stream", s.handleTurnStream)
	// Embedded help-docs site (make site-embed). Serves an actionable
	// placeholder when not staged — never an error (see internal/helpdocs).
	mux.Handle("/help/", http.StripPrefix("/help/", helpdocs.Handler()))
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
	// Data carries the full, raw error chain for debugging (logs, dev tools). The
	// user-facing Message is sanitized via userfacing.Error; Data preserves detail
	// the SPA can surface behind a "details" affordance without leaking it into the
	// red banner. Omitted when empty.
	Data string `json:"data,omitempty"`
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

	// maxRPCBodyBytes caps a single /rpc request body. The largest legitimate
	// payload is a bug.report with a base64'd rrweb session buffer (~last 30s of
	// DOM mutations); 32 MiB leaves generous headroom while preventing an
	// unbounded read.
	maxRPCBodyBytes = 32 << 20
)

func (s *Server) handleRPC(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Capture the raw request body before decode so the HAR recorder sees the
	// exact bytes the client sent; then re-feed them to the JSON decoder. Bound
	// the read: bug.report carries a base64 rrweb buffer that can run to a few MB,
	// but nothing legitimate exceeds maxRPCBodyBytes — cap it so a runaway/hostile
	// payload can't be slurped whole into memory.
	r.Body = http.MaxBytesReader(w, r.Body, maxRPCBodyBytes)
	reqBody, _ := io.ReadAll(r.Body)
	started := time.Now().UTC()

	var req rpcRequest
	if err := json.Unmarshal(reqBody, &req); err != nil {
		resp := rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: codeParseError, Message: "parse error: " + err.Error()}}
		respBody := writeRPC(w, resp)
		s.recordRPC(r, reqBody, http.StatusOK, respBody, started)
		return
	}

	// Carry the request's operator identity header (if any) into the dispatch
	// context so the drive RPCs can resolve slots.author. `kitsoki web` has no
	// authentication (trusted localhost); this header is the hook a fronting
	// proxy / future auth layer uses to attribute a turn to a real principal.
	ctx := r.Context()
	if actor := strings.TrimSpace(r.Header.Get(actorHeader)); actor != "" {
		ctx = context.WithValue(ctx, actorCtxKey{}, actor)
	}

	result, rerr := s.dispatch(ctx, req.Method, req.Params)
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	if rerr != nil {
		resp.Error = rerr
	} else {
		resp.Result = result
	}
	respBody := writeRPC(w, resp)
	s.recordRPC(r, reqBody, http.StatusOK, respBody, started)
}

// recordRPC pushes one /rpc request/response pair into the HAR ring buffer.
// It is best-effort and never affects the response already written.
func (s *Server) recordRPC(r *http.Request, reqBody []byte, status int, respBody []byte, started time.Time) {
	if s.recorder == nil {
		return
	}
	durMs := float64(time.Since(started).Microseconds()) / 1000.0
	url := r.URL.RequestURI()
	s.recorder.Record(
		r.Method, url,
		headerMap(r.Header), reqBody,
		status, map[string]string{"Content-Type": "application/json"}, respBody,
		started, durMs,
	)
}

// headerMap flattens an http.Header into a single-value map (last value wins),
// sufficient for the recorder's deterministic header rendering.
func headerMap(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, vs := range h {
		if len(vs) > 0 {
			out[k] = vs[len(vs)-1]
		}
	}
	return out
}

// writeRPC encodes resp as the /rpc JSON response and returns the exact bytes
// written, so the caller can hand them to the HAR recorder.
func writeRPC(w http.ResponseWriter, resp rpcResponse) []byte {
	w.Header().Set("Content-Type", "application/json")
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	_ = enc.Encode(resp)
	body := buf.Bytes()
	_, _ = w.Write(body)
	return body
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

	case "runstatus.work.list":
		out, err := s.listWork(ctx)
		if err != nil {
			return nil, serverErr(err)
		}
		return out, nil

	case "runstatus.chat.show":
		entry, rerr := s.resolve(params)
		if rerr != nil {
			return nil, rerr
		}
		chatID, _ := params["chat_id"].(string)
		if chatID == "" {
			return nil, &rpcError{Code: codeServerError, Message: "chat.show: missing 'chat_id'"}
		}
		sinceSeq, _ := intParam(params, "since_seq")
		cs, ok := entry.Driver.(ChatShower)
		if !ok {
			return nil, readOnlyErr(method)
		}
		out, err := cs.ShowChat(ctx, chatID, sinceSeq)
		if err != nil {
			return nil, serverErr(err)
		}
		sessionID, _ := params["session_id"].(string)
		if sessionID != "" {
			out.Context = &ChatShowContext{SessionID: sessionID}
		}
		return out, nil

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

	// ── Dynamic workflows (shared draft lifecycle) ──────────────────────────
	case "runstatus.workflow.create":
		svc := s.workflowService()
		if svc == nil {
			return nil, readOnlyErr(method)
		}
		goal, _ := params["goal"].(string)
		if strings.TrimSpace(goal) == "" {
			return nil, &rpcError{Code: codeServerError, Message: "workflow.create: missing 'goal'"}
		}
		slug, _ := params["slug"].(string)
		receipt, err := svc.Create(ctx, dynamicworkflow.CreateRequest{Goal: goal, Slug: slug})
		if err != nil {
			return nil, serverErr(err)
		}
		return receipt, nil

	case "runstatus.workflow.validate":
		svc := s.workflowService()
		if svc == nil {
			return nil, readOnlyErr(method)
		}
		workflowID, _ := params["workflow_id"].(string)
		if strings.TrimSpace(workflowID) == "" {
			return nil, &rpcError{Code: codeServerError, Message: "workflow.validate: missing 'workflow_id'"}
		}
		receipt, err := svc.ReadReceipt(workflowID)
		if err != nil {
			return nil, serverErr(err)
		}
		receipt.Validation = svc.ValidateDraft(receipt.AppPath, receipt.ManifestPath)
		if err := dynamicworkflow.WriteReceipt(filepath.Join(svc.OutputDir, receipt.WorkflowID, "receipt.json"), receipt); err != nil {
			return nil, serverErr(err)
		}
		if err := appendWorkflowValidationEvent(receipt); err != nil {
			return nil, serverErr(err)
		}
		return receipt, nil

	case "runstatus.workflow.launch":
		svc := s.workflowService()
		if svc == nil {
			return nil, readOnlyErr(method)
		}
		workflowID, _ := params["workflow_id"].(string)
		if strings.TrimSpace(workflowID) == "" {
			return nil, &rpcError{Code: codeServerError, Message: "workflow.launch: missing 'workflow_id'"}
		}
		receipt, err := svc.Launch(ctx, workflowID)
		if err != nil {
			return nil, serverErr(err)
		}
		sid, err := s.provider.NewSession(ctx, filepath.Join(receipt.AppPath, "app.yaml"))
		if err != nil {
			return nil, lifecycleErr(err)
		}
		receipt.SessionID = sid
		receipt.URL = "/s/" + sid
		if err := dynamicworkflow.WriteReceipt(filepath.Join(svc.OutputDir, receipt.WorkflowID, "receipt.json"), receipt); err != nil {
			return nil, serverErr(err)
		}
		if err := dynamicworkflow.AppendWorkflowEvent(receipt.EventsPath, map[string]any{
			"kind":            "dynamic.workflow.launched",
			"workflow_id":     receipt.WorkflowID,
			"at":              time.Now().UTC(),
			"app_path":        receipt.AppPath,
			"manifest_path":   receipt.ManifestPath,
			"trace_path":      receipt.TracePath,
			"session_id":      receipt.SessionID,
			"session_handle":  receipt.SessionHandle,
			"url":             receipt.URL,
			"receipt_path":    filepath.Join(svc.OutputDir, receipt.WorkflowID, "receipt.json"),
			"receipt_hash":    dynamicworkflow.HashFile(filepath.Join(svc.OutputDir, receipt.WorkflowID, "receipt.json")),
			"validation_path": receipt.ValidationPath,
			"validation_hash": dynamicworkflow.HashFile(receipt.ValidationPath),
		}); err != nil {
			return nil, serverErr(err)
		}
		if receipt.URL != "" {
			if err := dynamicworkflow.AppendWorkflowEvent(receipt.EventsPath, map[string]any{
				"kind":        "dynamic.workflow.url_assigned",
				"workflow_id": receipt.WorkflowID,
				"at":          time.Now().UTC(),
				"url":         receipt.URL,
				"server_id":   receipt.SessionID,
			}); err != nil {
				return nil, serverErr(err)
			}
		}
		return receipt, nil

	case "runstatus.workflow.status":
		svc := s.workflowService()
		if svc == nil {
			return nil, readOnlyErr(method)
		}
		workflowID, _ := params["workflow_id"].(string)
		if strings.TrimSpace(workflowID) == "" {
			return nil, &rpcError{Code: codeServerError, Message: "workflow.status: missing 'workflow_id'"}
		}
		receipt, err := svc.ReadReceipt(workflowID)
		if err != nil {
			return nil, serverErr(err)
		}
		return receipt, nil

	case "runstatus.workflow.export":
		svc := s.workflowService()
		if svc == nil {
			return nil, readOnlyErr(method)
		}
		workflowID, _ := params["workflow_id"].(string)
		if strings.TrimSpace(workflowID) == "" {
			return nil, &rpcError{Code: codeServerError, Message: "workflow.export: missing 'workflow_id'"}
		}
		target, _ := params["target"].(string)
		allowBase, _ := params["allow_base_story"].(bool)
		receipt, err := svc.Export(ctx, workflowID, dynamicworkflow.ExportRequest{
			TargetDir:      target,
			AllowBaseStory: allowBase,
		})
		if err != nil {
			return nil, serverErr(err)
		}
		return receipt, nil

	case "runstatus.session.attach":
		storyPath, _ := params["story_path"].(string)
		if storyPath == "" {
			return nil, &rpcError{Code: codeServerError, Message: "session.attach: missing 'story_path'"}
		}
		key, _ := params["key"].(string)
		if key == "" {
			return nil, &rpcError{Code: codeServerError, Message: "session.attach: missing 'key' (transport:thread)"}
		}
		ap, ok := s.provider.(ExternalAttachProvider)
		if !ok {
			return nil, readOnlyErr(method)
		}
		sid, err := ap.AttachExternal(ctx, storyPath, key)
		if err != nil {
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

	case "runstatus.session.staleness":
		sid, rerr := sessionIDParam(params)
		if rerr != nil {
			return nil, rerr
		}
		stale, diff, err := s.provider.Staleness(ctx, sid)
		if err != nil {
			return nil, lifecycleErr(err)
		}
		return map[string]any{"stale": stale, "diff": diff}, nil

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
		ctx = s.withOperatorPrompter(ctx, params)
		// A free-text turn may carry lightweight ambient slots (e.g. `current_scene`
		// — the slide the operator is viewing) so the routed intent targets it with
		// no annotation; gap-fill only (the harness classification wins).
		if sl, ok := params["slots"].(map[string]any); ok && len(sl) > 0 {
			ctx = WithTurnSupplements(ctx, world.Slots(sl))
		}
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
		slots = s.injectAuthor(ctx, params, slots)
		ctx = s.withOperatorPrompter(ctx, params)
		// A web/tui surface may attach a visual ambient bundle + picked anchor on
		// the submit call — the generic media-annotation seam: the Annotate
		// composer dispatches a real intent (e.g. a slidey deck's `refine`) and
		// the pointed-at element rides the turn so the agent edits exactly there.
		// Mirrors offpath's lift; the top-level `anchor` is folded into the visual
		// map (where visualAmbientFromParams reads it). A no-op when neither rode
		// the call, so explicit-intent submits stay byte-identical.
		if ctx2, ok := liftVisualAmbient(ctx, params); ok {
			ctx = ctx2
		}
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
		slots = s.injectAuthor(ctx, params, slots)
		ctx = s.withOperatorPrompter(ctx, params)
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

	case "runstatus.session.harness":
		// Read the declared harness profiles + live selection so the web header
		// picker can render. Empty profiles when the session exposes no
		// HarnessController (read-only / artifact surfaces, or no profiles
		// declared). No secrets: ProfileInfo omits env.
		entry, rerr := s.resolve(params)
		if rerr != nil {
			return nil, rerr
		}
		return harnessState(entry.Driver), nil

	case "runstatus.session.set_selection":
		// Switch the session's active profile (and optional model), effective
		// next turn. Mirrors the TUI /provider /model commands.
		entry, rerr := s.resolve(params)
		if rerr != nil {
			return nil, rerr
		}
		if entry.Driver == nil {
			return nil, readOnlyErr(method)
		}
		hc, ok := entry.Driver.(HarnessController)
		if !ok {
			return nil, &rpcError{Code: codeServerError, Message: "session.set_selection: no harness profiles for this session"}
		}
		profile, _ := params["profile"].(string)
		if profile == "" {
			return nil, &rpcError{Code: codeServerError, Message: "session.set_selection: missing 'profile'"}
		}
		model, _ := params["model"].(string)
		effort, _ := params["effort"].(string)
		if err := hc.SetHarnessSelection(profile, model, effort); err != nil {
			return nil, serverErr(err)
		}
		return harnessState(entry.Driver), nil

	case "runstatus.session.cancel":
		// Abort the in-flight streamed turn for this session (the web chat "Stop"
		// button). Cancels the detached execution context registered by
		// handleTurnStream, which propagates to the agent subprocess so the agent
		// actually stops. No driver/turn machinery is touched here — the running
		// turn observes the cancel and aborts cleanly, persisting nothing. Returns
		// {cancelled:false} when no turn was in flight (idempotent / already done).
		sid, _ := params["session_id"].(string)
		return map[string]any{"cancelled": s.cancelActiveTurn(sid)}, nil

	case "runstatus.session.offpath":
		entry, rerr := s.resolve(params)
		if rerr != nil {
			return nil, rerr
		}
		if entry.Driver == nil {
			return nil, readOnlyErr(method)
		}
		input, _ := params["input"].(string)
		// A web/tui surface may attach a visual ambient bundle (the frame the
		// operator was looking at + the element they pointed at) on the offpath
		// call. Lift it onto the ctx so the converse handler renders it into the
		// oracle prompt (see internal/host/visual_ambient.go). A no-op when no
		// bundle rode the call — the legacy offpath path is byte-identical.
		if vm, ok := params["visual"].(map[string]any); ok {
			ctx = host.WithVisualAmbient(ctx, visualAmbientFromParams(vm))
		}
		answer, err := entry.Driver.AskOffPath(ctx, input)
		if err != nil {
			return nil, serverErr(err)
		}
		return map[string]any{"answer": answer}, nil

	case "runstatus.artifact.semantic":
		// Read an artifact's sibling `<name>.semantic.json` sidecar (the
		// producer-declared clickable-element envelope) so the web annotator's
		// SemanticOverlay can offer semantic-element picks. We resolve the opaque
		// handle to its on-disk path via the same ArtifactResolver the /artifact
		// route uses, then read the sidecar beside it. kitsoki stays
		// producer-AGNOSTIC: the parsed envelope (plugin + opaque element refs) is
		// returned verbatim. A handle with no sidecar (or an unknown handle)
		// resolves to null so the annotator falls back to the dom_node/region
		// picker — never an error (the FrameResolver/SemanticSidecar graceful
		// posture). READ-ONLY: no session driver required.
		handle, _ := params["handle"].(string)
		if strings.TrimSpace(handle) == "" {
			return nil, &rpcError{Code: -32602, Message: "handle is required"}
		}
		absPath, _, found := s.resolveArtifact(handle)
		if !found {
			return nil, nil
		}
		sc, ok, err := host.DiskSemanticSidecarReader{}.ReadSemanticSidecar(absPath)
		if err != nil {
			return nil, serverErr(err)
		}
		if !ok || len(sc.Elements) == 0 {
			return nil, nil
		}
		return sc, nil

	// ── Inbox (background-job notifications) ────────────────────────────────
	case "runstatus.session.notifications.list":
		entry, rerr := s.resolve(params)
		if rerr != nil {
			return nil, rerr
		}
		if entry.Driver == nil {
			return nil, readOnlyErr(method)
		}
		notifs, err := entry.Driver.ListNotifications(ctx)
		if err != nil {
			return nil, serverErr(err)
		}
		limit, ok := intParam(params, "limit")
		if ok && limit > 0 && limit < len(notifs) {
			notifs = notifs[:limit]
		}
		if notifs == nil {
			notifs = []jobs.Notification{}
		}
		return map[string]any{"notifications": notifs}, nil

	case "runstatus.session.notifications.read":
		entry, rerr := s.resolve(params)
		if rerr != nil {
			return nil, rerr
		}
		if entry.Driver == nil {
			return nil, readOnlyErr(method)
		}
		id, _ := params["id"].(string)
		if id == "" {
			return nil, &rpcError{Code: codeServerError, Message: "notifications.read: missing 'id'"}
		}
		if err := entry.Driver.MarkNotificationRead(ctx, id); err != nil {
			return nil, serverErr(err)
		}
		return map[string]any{"ok": true}, nil

	case "runstatus.session.notifications.dismiss":
		entry, rerr := s.resolve(params)
		if rerr != nil {
			return nil, rerr
		}
		if entry.Driver == nil {
			return nil, readOnlyErr(method)
		}
		id, _ := params["id"].(string)
		if id == "" {
			return nil, &rpcError{Code: codeServerError, Message: "notifications.dismiss: missing 'id'"}
		}
		if err := entry.Driver.DismissNotification(ctx, id); err != nil {
			return nil, serverErr(err)
		}
		return map[string]any{"ok": true}, nil

	case "runstatus.session.inbox.sync_github":
		entry, rerr := s.resolve(params)
		if rerr != nil {
			return nil, rerr
		}
		if entry.Driver == nil {
			return nil, readOnlyErr(method)
		}
		syncer, ok := entry.Driver.(GitHubInboxSyncer)
		if !ok {
			return nil, readOnlyErr(method)
		}
		includeIssues := true
		if v, ok := params["include_issues"].(bool); ok {
			includeIssues = v
		}
		includePRs := true
		if v, ok := params["include_prs"].(bool); ok {
			includePRs = v
		}
		if !includeIssues && !includePRs {
			return nil, &rpcError{Code: codeServerError, Message: "inbox.sync_github: at least one of include_issues or include_prs must be true"}
		}
		limit, _ := intParam(params, "limit")
		repo, _ := params["repo"].(string)
		assignee, _ := params["assignee"].(string)
		reviewRequested, _ := params["review_requested"].(string)
		teleportState, _ := params["teleport_state"].(string)
		out, err := syncer.SyncGitHubInbox(ctx, GitHubInboxSyncOptions{
			Repo:            repo,
			IncludeIssues:   includeIssues,
			IncludePRs:      includePRs,
			Assignee:        assignee,
			ReviewRequested: reviewRequested,
			Limit:           limit,
			TeleportState:   teleportState,
		})
		if err != nil {
			return nil, serverErr(err)
		}
		return out, nil

	case "runstatus.session.teleport":
		entry, rerr := s.resolve(params)
		if rerr != nil {
			return nil, rerr
		}
		if entry.Driver == nil {
			return nil, readOnlyErr(method)
		}
		nid, _ := params["notification_id"].(string)
		if nid == "" {
			return nil, &rpcError{Code: codeServerError, Message: "session.teleport: missing 'notification_id'"}
		}
		out, err := entry.Driver.Teleport(ctx, nid)
		if err != nil {
			return nil, serverErr(err)
		}
		return newTurnResult(out, entry.Driver), nil

	case "runstatus.session.rewind_route":
		// Reverse one contextual-routing (CRR) decision and re-dispatch the
		// original utterance under new_class. Backs the web route-receipt chip's
		// "rewind" affordance. The engine reverses the lane classes today; an
		// intent-class rewind isn't recoverable from the journal yet and returns a
		// userfacing error (via serverErr) the UI shows in its red banner rather
		// than a raw 500 — the chip disables the control for intent receipts up
		// front, so this is the defence-in-depth path.
		entry, rerr := s.resolve(params)
		if rerr != nil {
			return nil, rerr
		}
		if entry.Driver == nil {
			return nil, readOnlyErr(method)
		}
		decisionID, _ := params["decision_id"].(string)
		if decisionID == "" {
			return nil, &rpcError{Code: codeServerError, Message: "session.rewind_route: missing 'decision_id'"}
		}
		newClass, _ := params["new_class"].(string)
		reason, _ := params["reason"].(string)
		out, err := entry.Driver.RewindRoute(ctx, decisionID, orchestrator.ContextRouteClass(newClass), reason)
		if err != nil {
			return nil, serverErr(err)
		}
		return newTurnResult(out, entry.Driver), nil

	case "runstatus.notifications.subscribe":
		// Cross-session feed: no session_id — the browser home chrome opens one
		// of these for the global badge/toast. Returns a subscription_id the
		// client opens against GET /rpc/notifications.
		return map[string]any{"subscription_id": s.notifs.subscribe()}, nil

	case "runstatus.notifications.unsubscribe":
		id, _ := params["subscription_id"].(string)
		s.notifs.unsubscribe(id)
		return map[string]any{"ok": true}, nil

	// ── Current session (active-session discovery) ──────────────────────────
	// Trace-only / graph-only surfaces have no chat to start a session, so they
	// discover and follow the single active session. session.current returns the
	// most recently created/attached session id (or null); the subscribe/stream
	// pair pushes a runstatus.session.changed frame whenever it changes.
	case "runstatus.session.current":
		id, ok := s.currentSession()
		if ok {
			return map[string]any{"session_id": id}, nil
		}
		return map[string]any{"session_id": nil}, nil

	case "runstatus.session.current.subscribe":
		return map[string]any{"subscription_id": s.current.subscribe()}, nil

	case "runstatus.session.current.unsubscribe":
		id, _ := params["subscription_id"].(string)
		s.current.unsubscribe(id)
		return map[string]any{"ok": true}, nil

	// ── Operator questions (forwarded agent questions) ──────────────────────
	// A subscriber opens runstatus.questions.subscribe and streams GET
	// /rpc/questions; when an agent agent forwards a question, a runstatus.question
	// frame arrives and the SPA shows a modal. The operator's choice comes back via
	// session.answer_question, which unblocks the parked agent turn.
	case "runstatus.questions.subscribe":
		return map[string]any{"subscription_id": s.questions.subscribe()}, nil

	case "runstatus.questions.unsubscribe":
		id, _ := params["subscription_id"].(string)
		s.questions.unsubscribe(id)
		return map[string]any{"ok": true}, nil

	case "runstatus.session.answer_question":
		qid, _ := params["question_id"].(string)
		if qid == "" {
			return nil, &rpcError{Code: codeServerError, Message: "session.answer_question: missing 'question_id'"}
		}
		answers, _ := params["answers"].(map[string]any)
		if !s.qreg.answer(qid, answers) {
			return nil, &rpcError{Code: codeNotFound, Message: "session.answer_question: unknown or already-answered question_id: " + qid}
		}
		return map[string]any{"ok": true}, nil

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

	// ── Annotation sidecar ────────────────────────────────────────────────────
	case "runstatus.annotation.add":
		sid, rerr := sessionIDParam(params)
		if rerr != nil {
			return nil, rerr
		}
		entry, rerr := s.resolve(params)
		if rerr != nil {
			return nil, rerr
		}
		as, ok := entry.Source.(AnnotationSource)
		if !ok {
			return nil, &rpcError{Code: codeReadOnly, Message: "annotation.add: this source does not support annotation sidecar"}
		}
		a := runstatus.Annotation{
			Ts:        time.Now().UTC(),
			SessionID: sid,
		}
		if v, ok := params["target_call_id"].(string); ok {
			a.TargetCallID = v
		}
		if v, ok := params["target_turn"].(float64); ok {
			a.TargetTurn = int(v)
		}
		if v, ok := params["score"].(float64); ok {
			score := v
			a.Score = &score
		}
		if v, ok := params["label"].(string); ok {
			a.Label = v
		}
		if v, ok := params["comment"].(string); ok {
			a.Comment = v
		}
		if v, ok := params["annotator"].(string); ok {
			a.Annotator = v
		}
		if err := runstatus.AppendAnnotation(as.AnnotationPath(), a); err != nil {
			return nil, serverErr(err)
		}
		return map[string]any{"ok": true}, nil

	// ── Call replay ───────────────────────────────────────────────────────────
	// runstatus.call.replay reconstructs one agent call from the recorded trace
	// and returns a stub result describing its replayability. Actual re-dispatch
	// against a live operator is not wired in v1 (no LLM cost in tests); the
	// stub confirms the call is replayable and returns a note.
	//
	// Request params: {session_id, call_id, operator: "claude"|"local"}
	// Response: {call_id, original_verb, replayable, note}
	case "runstatus.call.replay":
		entry, rerr := s.resolve(params)
		if rerr != nil {
			return nil, rerr
		}
		callID, _ := params["call_id"].(string)
		if callID == "" {
			return nil, &rpcError{Code: codeServerError, Message: "call.replay: missing 'call_id'"}
		}
		snap, err := entry.Source.Snapshot()
		if err != nil {
			return nil, serverErr(err)
		}
		// Map TraceEvents back to store.Events so ExtractReplayableCall can scan them.
		storeEvents := traceEventsToStoreEvents(snap.Events)
		rc, replayErr := runstatus.ExtractReplayableCall(storeEvents, callID)
		if replayErr != nil {
			return nil, &rpcError{Code: codeServerError, Message: "call.replay: " + replayErr.Error()}
		}
		// v1 stub: confirm replayability without dispatching a live agent call.
		return map[string]any{
			"call_id":       rc.CallID,
			"original_verb": rc.Verb,
			"replayable":    true,
			"note":          "replay dispatch not yet wired (v1 stub)",
		}, nil

	// ── Local file read (markdown preview) ───────────────────────────────────
	// runstatus.file.read reads a local .md file by absolute path and returns its
	// raw content. Only .md files are served; any other extension returns an error.
	// This is intentionally unrestricted beyond the .md check — kitsoki web is a
	// trusted localhost-only tool.
	//
	// Request params: {path}
	// Response: {content}
	case "runstatus.file.read":
		filePath, _ := params["path"].(string)
		if filePath == "" {
			return nil, &rpcError{Code: codeServerError, Message: "file.read: missing 'path'"}
		}
		if !strings.HasSuffix(strings.ToLower(filePath), ".md") {
			return nil, &rpcError{Code: codeServerError, Message: "file.read: only .md files are served"}
		}
		data, err := os.ReadFile(filepath.Clean(filePath))
		if err != nil {
			return nil, serverErr(fmt.Errorf("file.read %s: %w", filePath, err))
		}
		return map[string]any{"content": string(data)}, nil

	// ── Agent-action transcript sidecar ────────────────────────────────────────
	// runstatus.session.transcript reads one agent call's agent-action sidecar
	// (the verbatim backend-native event stream the host teed at the wire) LAZILY
	// from disk — never folded into the snapshot, because a task run can be
	// megabytes. The sidecar pair is <TranscriptsDir>/<call_id>.jsonl (one JSON
	// event per line, byte-verbatim) + <call_id>.timings ("<idx> <ms>" per line,
	// powering the waterfall). See docs/tracing/run-status-ui.md (Agent actions drawer).
	//
	// Request params: {session_id, call_id}
	// Response: {format, events:[…parsed lines…], timings:[…ms by index…], schema_version}
	//
	// A call with no sidecar (a verb that produced no transcript, or a static /
	// in-memory source that exposes no transcripts dir) is NOT an error — it
	// returns an empty events list, so the SPA simply shows no "Agent actions"
	// affordance rather than surfacing a 500.
	case "runstatus.session.transcript":
		entry, rerr := s.resolve(params)
		if rerr != nil {
			return nil, rerr
		}
		callID, _ := params["call_id"].(string)
		if callID == "" {
			return nil, &rpcError{Code: codeServerError, Message: "session.transcript: missing 'call_id'"}
		}
		td, ok := entry.Source.(interface{ TranscriptsDir() string })
		if !ok {
			// Source has no on-disk transcripts dir (in-memory/test source).
			return runstatus.EmptyTranscript(), nil
		}
		out, err := runstatus.ReadTranscriptSidecar(td.TranscriptsDir(), callID)
		if err != nil {
			return nil, serverErr(err)
		}
		return out, nil

	// ── Video feedback mode (/review panel) ────────────────────────────────────
	// Three read/capture RPCs the slice-2 feedback panel drives, all gated to
	// recorded video handles (resolved through the session's ArtifactResolver).
	// See video.go.
	case "runstatus.video.chapters":
		entry, rerr := s.resolve(params)
		if rerr != nil {
			return nil, rerr
		}
		return videoChapters(entry, params)

	case "runstatus.video.frame":
		entry, rerr := s.resolve(params)
		if rerr != nil {
			return nil, rerr
		}
		return videoFrame(ctx, entry, params)

	case "runstatus.feedback.add":
		entry, rerr := s.resolve(params)
		if rerr != nil {
			return nil, rerr
		}
		return feedbackAdd(entry, params)

	case "runstatus.bug.preview":
		return s.bugPreview(params)

	case "runstatus.bug.report":
		return s.bugReport(params)

	default:
		// ── Story editor (per-story, no session) ─────────────────────────────
		// The editor.* family operates on a story selected from the registry
		// catalogue rather than a live session; dispatchEditor reports handled
		// so unknown non-editor methods still fall through to method-missing.
		if result, rerr, handled := s.dispatchEditor(method, params); handled {
			return result, rerr
		}
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

// workflowService resolves the shared dynamic-workflow service for this
// server. The workflow RPC family is intentionally disabled when no workflow
// root was configured, so read-only surfaces do not accidentally mint drafts in
// an arbitrary cwd.
func (s *Server) workflowService() *dynamicworkflow.Service {
	if strings.TrimSpace(s.workflowRoot) == "" {
		return nil
	}
	svc := dynamicworkflow.NewService(s.workflowRoot)
	return svc
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

// harnessState renders a Driver's harness profiles + live selection for the
// runstatus.session.harness / set_selection RPCs. A nil driver or one without
// the optional HarnessController (read-only / artifact surfaces, no profiles
// declared) yields empty profiles and a zero selection so the SPA simply hides
// the picker. ProfileInfo carries no env, so no secret reaches the client.
func harnessState(d Driver) map[string]any {
	hc, ok := d.(HarnessController)
	if !ok {
		return map[string]any{"profiles": []orchestrator.ProfileInfo{}, "selection": orchestrator.ProfileSelection{}}
	}
	profiles := hc.HarnessProfiles()
	if profiles == nil {
		profiles = []orchestrator.ProfileInfo{}
	}
	return map[string]any{"profiles": profiles, "selection": hc.HarnessSelection()}
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

func appendWorkflowValidationEvent(receipt *dynamicworkflow.Receipt) error {
	if receipt == nil || receipt.EventsPath == "" {
		return nil
	}
	return dynamicworkflow.AppendWorkflowEvent(receipt.EventsPath, map[string]any{
		"kind":            "dynamic.workflow.validated",
		"workflow_id":     receipt.WorkflowID,
		"at":              time.Now().UTC(),
		"app_path":        receipt.AppPath,
		"manifest_path":   receipt.ManifestPath,
		"validation_path": receipt.ValidationPath,
		"validation_hash": dynamicworkflow.HashFile(receipt.ValidationPath),
		"ok":              receipt.Validation.OK,
		"errors":          receipt.Validation.Errors,
		"warnings":        receipt.Validation.Warnings,
	})
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
	// The user sees a sanitized summary (no temp paths, %w fragments, or
	// orchestrator internals); the full chain rides along in Data for logs/dev
	// tools. Without this the red banner showed raw wrapped Go errors verbatim.
	return &rpcError{Code: codeServerError, Message: userfacing.Error(err), Data: err.Error()}
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

// traceEventsToStoreEvents converts []runstatus.TraceEvent back to
// []store.Event with the minimal fields ExtractReplayableCall needs:
// Kind (from Msg), CallID (from attrs.call_id), and Payload (from Attrs
// marshalled to JSON). This bridge avoids exposing a full store.History on
// the Source interface; the replay handler is the only consumer.
func traceEventsToStoreEvents(tevs []runstatus.TraceEvent) []store.Event {
	out := make([]store.Event, 0, len(tevs))
	for _, te := range tevs {
		callID, _ := te.Attrs["call_id"].(string)
		raw, _ := json.Marshal(te.Attrs)
		out = append(out, store.Event{
			Kind:    store.EventKind(te.Msg),
			CallID:  callID,
			Payload: json.RawMessage(raw),
		})
	}
	return out
}

// liftVisualAmbient lifts an optional visual bundle + picked anchor off an RPC's
// params onto the ctx (host.WithVisualAmbient), returning the enriched ctx and
// true when something was attached. The `anchor` may arrive nested inside the
// `visual` object OR as a top-level sibling (the media-annotation composer sends
// it top-level alongside `slots`); a top-level anchor is folded into the visual
// map so visualAmbientFromParams reads it uniformly. Returns (ctx, false) when
// neither rode the call so callers keep their byte-identical legacy path.
func liftVisualAmbient(ctx context.Context, params map[string]any) (context.Context, bool) {
	vm, _ := params["visual"].(map[string]any)
	an, hasAnchor := params["anchor"].(map[string]any)
	if vm == nil && !hasAnchor {
		return ctx, false
	}
	if vm == nil {
		vm = map[string]any{}
	}
	if hasAnchor {
		if _, nested := vm["anchor"]; !nested {
			vm["anchor"] = an
		}
	}
	return host.WithVisualAmbient(ctx, visualAmbientFromParams(vm)), true
}

// visualAmbientFromParams decodes the optional `visual` object on a
// session.offpath RPC into a host.VisualAmbient. The shape mirrors
// host.VisualAmbient's JSON tags; numbers arrive as JSON float64. Missing or
// malformed fields decode to their zero value (host.WithVisualAmbient is a
// no-op when nothing meaningful was attached), so a partial bundle never errors
// the turn — the surface owns producing a well-formed bundle (slices 2/4).
func visualAmbientFromParams(m map[string]any) host.VisualAmbient {
	var v host.VisualAmbient
	v.FrameHandle, _ = m["frame_handle"].(string)
	v.MediaHandle, _ = m["media_handle"].(string)
	v.Route, _ = m["route"].(string)
	if t, ok := mapNum(m, "t_ms"); ok {
		v.TMs = t
	}
	if pt, ok := m["point"].(map[string]any); ok {
		v.Point.X, _ = mapNum(pt, "x")
		v.Point.Y, _ = mapNum(pt, "y")
	}
	if el, ok := m["element"].(map[string]any); ok {
		var e struct {
			Selector string `json:"selector"`
			Role     string `json:"role"`
			Text     string `json:"text"`
			Bbox     [4]int `json:"bbox"`
		}
		e.Selector, _ = el["selector"].(string)
		e.Role, _ = el["role"].(string)
		e.Text, _ = el["text"].(string)
		if bb, ok := el["bbox"].([]any); ok {
			for i := 0; i < len(bb) && i < 4; i++ {
				e.Bbox[i] = numOf(bb[i])
			}
		}
		v.Element = &e
	}
	// v2: the discriminated anchor (annotation_anchor.go). Absent or unkinded ⇒
	// zero anchor; host.VisualAmbient.normalizedAnchor synthesizes one from the v1
	// flat fields above so a legacy surface that sends no `anchor` still works.
	if an, ok := m["anchor"].(map[string]any); ok {
		v.Anchor = host.AnchorFromParams(an)
	}
	return v
}

// mapNum reads a numeric field (JSON float64) from m as an int.
func mapNum(m map[string]any, key string) (int, bool) {
	switch n := m[key].(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	default:
		return 0, false
	}
}

// numOf coerces a single JSON number to an int (zero when not numeric).
func numOf(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	default:
		return 0
	}
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

// handleNotifications streams the cross-session notification feed as SSE. It
// mirrors handleEvents: poll the server-level buffer for frames appended since
// the subscription's watermark and emit one runstatus.notification frame each.
// The subscription_id comes from runstatus.notifications.subscribe.
func (s *Server) handleNotifications(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("subscription_id")
	sub := s.notifs.lookup(id)
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
		s.streamNotifications(w, flusher, sub)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// streamNotifications emits a runstatus.notification frame for every buffered
// notification appended since the subscription's last delivery, then advances
// the watermark.
func (s *Server) streamNotifications(w http.ResponseWriter, flusher http.Flusher, sub *notifSub) {
	sub.mu.Lock()
	defer sub.mu.Unlock()

	frames, watermark := s.notifs.since(sub.sent)
	for _, fr := range frames {
		frame := map[string]any{
			"jsonrpc": "2.0",
			"method":  "runstatus.notification",
			"params": map[string]any{
				"session_id":      fr.SessionID,
				"notification":    fr.Notification,
				"unread":          fr.Unread,
				"needs_attention": fr.NeedsAttention,
			},
		}
		b, err := json.Marshal(frame)
		if err != nil {
			continue
		}
		_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
	}
	sub.sent = watermark
	flusher.Flush()
}

// ── Current session feed ─────────────────────────────────────────────────────

// currentSession resolves the current (most recently created/attached) session
// id. It prefers the provider's authoritative answer when the provider implements
// [CurrentSessionProvider]; otherwise it falls back to the last value pushed onto
// the current-session feed via EmitCurrentSession. ok is false when there is no
// current session.
func (s *Server) currentSession() (string, bool) {
	if cp, ok := s.provider.(CurrentSessionProvider); ok {
		return cp.CurrentSession()
	}
	if v, ok := s.current.currentValue(); ok && v != nil {
		return *v, true
	}
	return "", false
}

// handleSessionCurrent streams the current-session feed as SSE. It mirrors
// handleNotifications: poll the server-level buffer for frames appended since the
// subscription's watermark and emit one runstatus.session.changed frame each. The
// subscription_id comes from runstatus.session.current.subscribe; a fresh
// subscription is seeded with the current value so a late subscriber syncs.
func (s *Server) handleSessionCurrent(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("subscription_id")
	sub := s.current.lookup(id)
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
		s.streamSessionCurrent(w, flusher, sub)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// streamSessionCurrent emits a runstatus.session.changed frame for every buffered
// current-session value appended since the subscription's last delivery, then
// advances the watermark.
func (s *Server) streamSessionCurrent(w http.ResponseWriter, flusher http.Flusher, sub *currentSub) {
	sub.mu.Lock()
	defer sub.mu.Unlock()

	frames, watermark := s.current.since(sub.sent)
	for _, fr := range frames {
		frame := map[string]any{
			"jsonrpc": "2.0",
			"method":  "runstatus.session.changed",
			"params": map[string]any{
				"session_id": fr.SessionID,
			},
		}
		b, err := json.Marshal(frame)
		if err != nil {
			continue
		}
		_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
	}
	sub.sent = watermark
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
	// A `/artifact/<id>/poster` request serves the sibling `<stem>.poster.png`
	// still beside the media (a slideshow/video's annotation backdrop), keyed by
	// the SAME opaque handle as the media itself — no separate journal entry. The
	// only sub-path we accept is this `/poster` suffix; anything else is a flat
	// handle ID and a stray slash is a 404 (we don't serve arbitrary sub-paths).
	poster := false
	if rest, ok := strings.CutSuffix(id, "/poster"); ok {
		poster = true
		id = rest
	}
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

	// Redirect a poster request to the sibling `<stem>.poster.png` on disk; the
	// media handle resolves the media path, the poster lives beside it. When no
	// poster exists the request 404s (the annotator then has no still backdrop).
	if poster {
		absPath = host.PosterSidecarPath(absPath)
		mime = "image/png"
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
