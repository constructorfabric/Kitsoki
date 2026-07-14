// registry.go — SessionRegistry, the concrete server.SessionProvider for
// `kitsoki web`'s multi-story surface.
//
// The registry lives in package main BECAUSE it must call buildSessionRuntime
// (runtime.go) and use runtimeConfig, and an internal/ package cannot import
// package main. This is the import-direction inversion the decomposition calls
// out: the server package DEFINES server.SessionProvider, and this concrete
// implementation DEPENDS on it.
//
// One registry owns:
//
//   - a catalogue of discovered stories (webconfig.StoryMeta), refreshed by an
//     explicit Rescan — there is no fsnotify watch (decided lean);
//   - a set of live sessions, each an *entry* keyed by a fresh UUID, holding the
//     story it runs, its *sessionRuntime, the read Source and write Driver the
//     server routes against, and enough state to drive a TUI-parity Reload.
//
// Sessions are in-memory only: they die with the process (no persistence
// across restarts, no kill action — decided leans for the PoC). Live count IS
// now bounded (swarm-session-cap): session.new / AttachExternal evict the
// least-recently-active IDLE session once the configurable cap is reached
// (server.SessionRegistry.ensureCapacityLocked), so dozens of churning swarm
// UI-QA sessions can't leak an orchestrator per session for the life of the
// process the way an uncapped registry would.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/pmezard/go-difflib/difflib"

	"kitsoki/internal/agents"
	"kitsoki/internal/app"
	"kitsoki/internal/chats"
	"kitsoki/internal/metamode"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/runstatus"
	"kitsoki/internal/runstatus/server"
	"kitsoki/internal/store"
	"kitsoki/internal/testrunner"
	"kitsoki/internal/webconfig"
)

// entry is one live session as the registry owns it. The server only needs the
// Source + Driver (exposed via server.Entry); the registry retains the rest to
// drive the lifecycle:
//
//   - StoryPath is the absolute app.yaml path — the Reload target and the key
//     that links a session back to its StoryHeader for the active-session count.
//   - Def is the (possibly reloaded) app definition for display.
//   - rt is the owned *sessionRuntime; Close releases it on shutdown.
//   - sid is the orchestrator session id Reload / driver calls bind to.
//   - source is the LiveSession; Reload reads currentState from its snapshot.
type entry struct {
	StoryPath     string
	Def           *app.AppDef
	synthetic     bool
	externalKey   string // transport:thread for store-attached sessions; "" for fresh ones
	loadedContent []byte // raw app.yaml bytes at last load/reload, for staleness check
	rt            *sessionRuntime
	sid           app.SessionID
	source        *server.LiveSession
	driver        server.Driver
	sink          *store.JSONLSink
	sessionDir    string // directory holding this session's trace + sidecars

	// metaController is the lazily-built meta-mode controller for this
	// session, cached so the persistent chat store / agent registry / AppDef
	// binding survives across turns. Reload nils it so the next meta turn
	// rebuilds against the reloaded AppDef.
	metaController *metamode.Controller

	// frames / feedback are the lazily-built /review feedback-mode seams,
	// cached so the still recorder's seq counter and the feedback sidecar path
	// stay stable across RPCs for this session.
	frames   *server.JournalFrameRecorder
	feedback *server.JSONLFeedbackSink

	// turnsInFlight counts driver calls currently executing against this
	// session (Turn/SubmitDirect/ContinueTurn/AskOffPath/Teleport/RewindRoute,
	// via trackingDriver). >0 means "mid-turn" — ensureCapacityLocked never
	// picks such a session as an eviction victim. Accessed with sync/atomic so
	// trackingDriver's begin/end calls don't need the registry mutex held for
	// the call's whole duration.
	turnsInFlight int32

	// lastActive is stamped by trackingDriver when a turn-advancing call
	// completes (and seeded to session-creation time), so
	// ensureCapacityLocked's "least-recently-active idle session" choice has a
	// meaningful signal — a session mid-conversation ranks recent even between
	// turns; one nobody has touched since it was opened ranks oldest. Guarded
	// by the registry mutex (all entry field access is).
	lastActive time.Time
}

type storyLoad struct {
	path      string
	def       *app.AppDef
	raw       []byte
	reloader  func() (*app.AppDef, error)
	synthetic bool
	repoRoot  string
}

// frameRecorderLocked returns the session's still recorder, building it on first
// use. Stills land under <sessionDir>/frames; the journal writer makes them
// resolve through the existing /artifact/{id} route. Caller holds the registry
// mutex (Get does).
func (e *entry) frameRecorderLocked() *server.JournalFrameRecorder {
	if e.frames == nil {
		e.frames = &server.JournalFrameRecorder{
			Writer:    e.rt.Journal,
			SID:       e.sid,
			FramesDir: filepath.Join(e.sessionDir, "frames"),
		}
	}
	return e.frames
}

// feedbackSinkLocked returns the session's append-only feedback sink, building
// it on first use. Notes land in <sessionDir>/<sid>.feedback.jsonl, the file the
// slice-3 authoring story drains on its next refine turn. Caller holds the
// registry mutex.
func (e *entry) feedbackSinkLocked() *server.JSONLFeedbackSink {
	if e.feedback == nil {
		e.feedback = &server.JSONLFeedbackSink{
			Path: filepath.Join(e.sessionDir, string(e.sid)+".feedback.jsonl"),
		}
	}
	return e.feedback
}

// SessionRegistry implements [server.SessionProvider]. It is safe for concurrent
// use: a single mutex guards both the story catalogue and the live-session map,
// since the SSE pollers and RPC handlers call in from many goroutines.
type SessionRegistry struct {
	cfg  webconfig.WebConfig
	base runtimeBase
	dirs []string

	// notifier, when set, attaches a cross-session notification relay to each new
	// session's orchestrator (server.Notifier, injected by web.go via
	// SetNotifier). Read without the lock — set once at startup before any
	// NewSession call, never mutated after.
	notifier server.Notifier

	mu       sync.Mutex
	stories  []webconfig.StoryMeta
	sessions map[string]*entry

	// maxSessions caps the live-session count (swarm-session-cap). Set from
	// $KITSOKI_WEB_MAX_SESSIONS (or DefaultMaxLiveSessions) at construction;
	// SetMaxSessions overrides it (e.g. from a future --max-sessions flag, or
	// a test tightening it to exercise eviction cheaply). Guarded by mu.
	maxSessions int

	// currentSessionID is the id of the most recently created (NewSession) or
	// attached (AttachExternal) session — the "current" session trace-only and
	// graph-only surfaces follow (server.CurrentSessionProvider). Empty means no
	// session yet. Guarded by mu.
	currentSessionID string

	// Meta-mode shared resources, all guarded by mu. agentReg is the builtin
	// agent registry every meta controller resolves names against. The self*
	// fields back the home-screen (session-less) meta driver for the cross-app
	// kitsoki.* modes; they are opened lazily on first home-screen meta use.
	agentReg     agents.Registry
	metaSelfStr  store.Store
	metaSelfChat *chats.Store
	metaSelfCtrl *metamode.Controller
}

// NewRegistry constructs a registry over the resolved story dirs. cfg carries
// the (already loaded) WebConfig; dirs is the resolved story-dir list (flags >
// config > default — resolved by the caller via webconfig.Resolve). base is the
// session-invariant construction posture every new session inherits. The
// initial catalogue is empty until the caller runs Rescan.
func NewRegistry(cfg webconfig.WebConfig, dirs []string, base runtimeBase) *SessionRegistry {
	return &SessionRegistry{
		cfg:         cfg,
		base:        base,
		dirs:        dirs,
		sessions:    map[string]*entry{},
		maxSessions: maxSessionsFromEnv(),
	}
}

// DefaultMaxLiveSessions is the fallback cap on concurrently live in-memory
// sessions (swarm-session-cap) when neither $KITSOKI_WEB_MAX_SESSIONS nor
// SetMaxSessions configures one explicitly. Picked generous enough that
// ordinary single-operator usage — a handful of story tabs open in a
// browser — never brushes against it, while still bounding the swarm-scale
// churn scenario (dozens of short-lived persona/UI-QA sessions started back
// to back) that would otherwise leak an orchestrator per session for the life
// of the process, per the package doc's now-revisited "no cap" lean.
const DefaultMaxLiveSessions = 128

// maxSessionsFromEnv resolves the configured cap from $KITSOKI_WEB_MAX_SESSIONS,
// falling back to DefaultMaxLiveSessions when unset or not a positive integer.
func maxSessionsFromEnv() int {
	v := os.Getenv("KITSOKI_WEB_MAX_SESSIONS")
	if v == "" {
		return DefaultMaxLiveSessions
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return DefaultMaxLiveSessions
	}
	return n
}

// SetMaxSessions overrides the live-session cap after construction — the
// injection point a future `kitsoki web --max-sessions` flag would call, and
// what tests use to exercise eviction without starting 128 real sessions. n<=0
// restores DefaultMaxLiveSessions rather than disabling the cap (a cap of zero
// or negative would either always-evict or panic the eviction search).
func (r *SessionRegistry) SetMaxSessions(n int) {
	if n <= 0 {
		n = DefaultMaxLiveSessions
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.maxSessions = n
}

// ErrNoEvictableSession is returned by NewSession/AttachExternal when the live
// -session cap is reached and every live session is mid-turn (turnsInFlight >
// 0) — there is nothing safe to evict. Returning this beats the alternatives:
// silently exceeding the cap (defeats the point of having one) or blocking
// until something finishes (an operator-facing hang with no visible cause).
var ErrNoEvictableSession = errors.New("kitsoki web: live-session cap reached and every session is mid-turn; no idle session is evictable")

// ensureCapacityLocked makes room for one more live session when the cap is
// already met: it picks the least-recently-active session with no turn in
// flight and evicts it. Caller MUST hold r.mu for the whole
// check-evict-then-insert sequence (both NewSession and AttachExternal do),
// so two concurrent session.new calls can't both observe "under cap" and both
// insert, exceeding it.
//
// The returned *entry (nil when no eviction was needed) is the victim; the
// caller must release it via cleanupEvicted AFTER unlocking r.mu (Close/sink
// I/O has no business running under the map lock).
func (r *SessionRegistry) ensureCapacityLocked() (*entry, error) {
	if len(r.sessions) < r.maxSessions {
		return nil, nil
	}
	var victimID string
	var victim *entry
	for id, e := range r.sessions {
		if atomic.LoadInt32(&e.turnsInFlight) > 0 {
			continue // never evict a session mid-turn
		}
		if victim == nil || e.lastActive.Before(victim.lastActive) {
			victimID, victim = id, e
		}
	}
	if victim == nil {
		return nil, ErrNoEvictableSession
	}
	delete(r.sessions, victimID)
	if r.currentSessionID == victimID {
		r.currentSessionID = ""
	}
	return victim, nil
}

// cleanupEvicted releases everything an evicted session held: its trace sink
// and its sessionRuntime (which owns the orchestrator, its store handles, and
// any agent/IDE connections — rt.Close's usual shutdown path). e is already
// unreachable from r.sessions by the time this runs (ensureCapacityLocked
// deleted it under the lock), so:
//
//   - a subsequent RPC against its id resolves via Get with ok=false, which
//     the server surface turns into a clear "unknown session_id" error — not a
//     hang or a panic (see server.resolve);
//   - its notification relay (registered on e.rt.Orch via
//     r.notifier.AttachSession at NewSession/AttachExternal time — the leak
//     named in the package doc) needs no separate UnregisterObserver call: the
//     relay is reachable only through that orchestrator's observer list, and
//     once e.rt.Close() runs and e is dropped here, nothing in the process
//     still references the orchestrator, so the relay (and the orchestrator
//     itself) become eligible for garbage collection together. It will never
//     fire again because nothing can drive e.sid to produce a background turn
//     for it to relay.
//
// Called after r.mu is released (Close/sink I/O has no business running under
// the map lock).
func (r *SessionRegistry) cleanupEvicted(e *entry) {
	if e == nil {
		return
	}
	if e.sink != nil {
		_ = e.sink.Close()
	}
	if e.rt != nil {
		e.rt.Close()
	}
}

// beginTurn marks id as mid-turn (turnsInFlight+1), protecting it from
// idle eviction for the duration of the call. No-op if id is no longer live
// (e.g. it raced an eviction — vanishingly unlikely since the caller can only
// reach beginTurn through a Driver obtained from a still-registered entry, but
// defensive rather than a nil-deref). Called by trackingDriver.
func (r *SessionRegistry) beginTurn(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.sessions[id]; ok {
		atomic.AddInt32(&e.turnsInFlight, 1)
	}
}

// endTurn clears the mid-turn mark and stamps lastActive to now — the signal
// ensureCapacityLocked ranks idle victims on. Called by trackingDriver via
// defer, so it runs whether the call succeeded or errored.
func (r *SessionRegistry) endTurn(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.sessions[id]; ok {
		atomic.AddInt32(&e.turnsInFlight, -1)
		e.lastActive = time.Now()
	}
}

// trackingDriver wraps a live session's [server.Driver] so the registry can
// enforce swarm-session-cap eviction safely: it marks the session mid-turn
// (via beginTurn/endTurn) around every call that advances or could race the
// session's teardown, so ensureCapacityLocked never picks a busy session as a
// victim. Read-only / no-advance methods (View, IntentInfo, DefaultIntent,
// PatchWorld, the inbox listing methods) are promoted unchanged from the
// embedded Driver, matching lockingDriver's split between advancing and
// read-only calls.
//
// The optional Driver extensions (HarnessController, WorkLister, ChatShower,
// GitHubInboxSyncer) are forwarded explicitly, mirroring lockingDriver, so
// wrapping a session's driver for tracking introduces no feature regression —
// a Driver that doesn't implement one of these reports the same
// not-configured shape a read-only surface would.
type trackingDriver struct {
	server.Driver
	reg *SessionRegistry
	id  string
}

// newTrackingDriver wraps inner so its turn-advancing calls bump the
// registry's mid-turn / lastActive bookkeeping for session id.
func newTrackingDriver(reg *SessionRegistry, id string, inner server.Driver) server.Driver {
	return &trackingDriver{Driver: inner, reg: reg, id: id}
}

func (d *trackingDriver) Turn(ctx context.Context, input string) (*orchestrator.TurnOutcome, error) {
	d.reg.beginTurn(d.id)
	defer d.reg.endTurn(d.id)
	return d.Driver.Turn(ctx, input)
}

func (d *trackingDriver) SubmitDirect(ctx context.Context, intent string, slots map[string]any) (*orchestrator.TurnOutcome, error) {
	d.reg.beginTurn(d.id)
	defer d.reg.endTurn(d.id)
	return d.Driver.SubmitDirect(ctx, intent, slots)
}

func (d *trackingDriver) ContinueTurn(ctx context.Context, slots map[string]any) (*orchestrator.TurnOutcome, error) {
	d.reg.beginTurn(d.id)
	defer d.reg.endTurn(d.id)
	return d.Driver.ContinueTurn(ctx, slots)
}

func (d *trackingDriver) AskOffPath(ctx context.Context, input string) (string, error) {
	d.reg.beginTurn(d.id)
	defer d.reg.endTurn(d.id)
	return d.Driver.AskOffPath(ctx, input)
}

func (d *trackingDriver) Teleport(ctx context.Context, notificationID string) (*orchestrator.TurnOutcome, error) {
	d.reg.beginTurn(d.id)
	defer d.reg.endTurn(d.id)
	return d.Driver.Teleport(ctx, notificationID)
}

func (d *trackingDriver) RewindRoute(ctx context.Context, decisionID string, newClass orchestrator.ContextRouteClass, reason string, workspacePath string) (*orchestrator.TurnOutcome, error) {
	d.reg.beginTurn(d.id)
	defer d.reg.endTurn(d.id)
	return d.Driver.RewindRoute(ctx, decisionID, newClass, reason, workspacePath)
}

func (d *trackingDriver) HarnessProfiles() []orchestrator.ProfileInfo {
	if hc, ok := d.Driver.(server.HarnessController); ok {
		return hc.HarnessProfiles()
	}
	return nil
}

func (d *trackingDriver) HarnessSelection() orchestrator.ProfileSelection {
	if hc, ok := d.Driver.(server.HarnessController); ok {
		return hc.HarnessSelection()
	}
	return orchestrator.ProfileSelection{}
}

func (d *trackingDriver) SetHarnessSelection(profile, model, effort string) error {
	if hc, ok := d.Driver.(server.HarnessController); ok {
		return hc.SetHarnessSelection(profile, model, effort)
	}
	return nil
}

func (d *trackingDriver) CurrentWorld(ctx context.Context) (map[string]any, error) {
	if wr, ok := d.Driver.(server.WorldReader); ok {
		return wr.CurrentWorld(ctx)
	}
	return nil, fmt.Errorf("session driver exposes no world reader")
}

func (d *trackingDriver) ListWork(ctx context.Context) (server.SessionWork, error) {
	if wl, ok := d.Driver.(server.WorkLister); ok {
		return wl.ListWork(ctx)
	}
	return server.SessionWork{}, nil
}

func (d *trackingDriver) ShowChat(ctx context.Context, chatID string, sinceSeq int) (server.ChatShowResult, error) {
	if cs, ok := d.Driver.(server.ChatShower); ok {
		return cs.ShowChat(ctx, chatID, sinceSeq)
	}
	return server.ChatShowResult{}, fmt.Errorf("chat.show: no chat store configured")
}

func (d *trackingDriver) SyncGitHubInbox(ctx context.Context, opts server.GitHubInboxSyncOptions) (server.GitHubInboxSyncResult, error) {
	if sy, ok := d.Driver.(server.GitHubInboxSyncer); ok {
		return sy.SyncGitHubInbox(ctx, opts)
	}
	return server.GitHubInboxSyncResult{}, fmt.Errorf("inbox.sync_github: not supported")
}

func (r *SessionRegistry) implicitRootPath() (string, error) {
	repoRoot, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve implicit root cwd: %w", err)
	}
	return filepath.Join(repoRoot, ".kitsoki", "implicit-root.app.yaml"), nil
}

func (r *SessionRegistry) synthesizeImplicitRoot() (*storyLoad, error) {
	repoRoot, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("resolve implicit root cwd: %w", err)
	}
	path := filepath.Join(repoRoot, ".kitsoki", "implicit-root.app.yaml")
	load := func() (*app.AppDef, error) {
		return app.SynthesizeRootWithResolver(r.cfg.Root.RootSpec(), repoRoot, buildImportResolver())
	}
	def, err := load()
	if err != nil {
		return nil, err
	}
	return &storyLoad{
		path:      path,
		def:       def,
		reloader:  load,
		synthetic: true,
		repoRoot:  repoRoot,
	}, nil
}

func (r *SessionRegistry) loadStory(storyPath string) (*storyLoad, error) {
	abs, err := filepath.Abs(storyPath)
	if err != nil {
		return nil, fmt.Errorf("resolve story path %q: %w", storyPath, err)
	}
	implicit, impErr := r.implicitRootPath()
	if impErr != nil {
		return nil, impErr
	}
	if abs == implicit {
		return r.synthesizeImplicitRoot()
	}

	def, err := loadAppWithEnv(abs)
	if err != nil {
		return nil, err
	}
	rawContent, _ := os.ReadFile(abs)
	return &storyLoad{
		path:     abs,
		def:      def,
		raw:      rawContent,
		repoRoot: filepath.Dir(abs),
	}, nil
}

// SetNotifier injects the cross-session notification relay sink (the running
// server). It must be called before the first NewSession so every live session
// registers its relay. Idempotent-safe to call once at startup.
func (r *SessionRegistry) SetNotifier(n server.Notifier) {
	r.notifier = n
}

// Close releases every live session's runtime and sink, in arbitrary order. The
// `kitsoki web` entrypoint defers this on shutdown.
func (r *SessionRegistry) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.sessions {
		if e.sink != nil {
			_ = e.sink.Close()
		}
		if e.rt != nil {
			e.rt.Close()
		}
	}
	if r.metaSelfStr != nil {
		_ = r.metaSelfStr.Close()
	}
}

// NewSession starts a fresh session for the story at storyPath, mirroring the
// single-session bootstrap web.go performed: build the runtime, create an
// orchestrator session, wire a concurrency-safe LiveSession event sink, record
// the effective story, and fire the initial on_enter chain so the first frame
// the browser renders reflects on-enter-bound world keys. It FAILS FAST with a
// structured error on an invalid story (app.Load / build / session errors) so
// the UI can surface it before navigating — no session is registered on failure.
func (r *SessionRegistry) NewSession(ctx context.Context, storyPath string) (string, error) {
	return r.newSession(ctx, storyPath, nil)
}

// NewSessionSeeded implements [server.SeededSessionProvider].
func (r *SessionRegistry) NewSessionSeeded(ctx context.Context, storyPath string, initialWorld map[string]any) (string, error) {
	return r.newSession(ctx, storyPath, initialWorld)
}

func (r *SessionRegistry) newSession(ctx context.Context, storyPath string, initialWorld map[string]any) (string, error) {
	loaded, err := r.loadStory(storyPath)
	if err != nil {
		return "", err
	}
	abs := loaded.path
	def := loaded.def

	// Fail fast (never silently no-op a guarded turn): if the story gates a turn
	// on an author ACL but the server was started with no configured operator
	// identity, a browser-driven `continue` would record the anonymous fallback
	// and the guard would reject it. Surface that at session start instead.
	if err := r.checkAuthorIdentity(def); err != nil {
		return "", err
	}

	rtCfg := r.base.config(abs, def)
	if loaded.reloader != nil {
		rtCfg.Reloader = loaded.reloader
		rtCfg.MiningRepoPath = loaded.repoRoot
	}
	rt, err := buildSessionRuntime(rtCfg)
	if err != nil {
		return "", err
	}
	// On any error after construction, release what we opened so a failed
	// NewSession leaks nothing.
	ok := false
	defer func() {
		if !ok {
			rt.Close()
		}
	}()

	orch := rt.Orch
	sid, err := orch.NewSession(ctx)
	if err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}

	// In flow posture (--flow / --host-cassette), honor the fixture's
	// initial_state / initial_world exactly as `test flows` and `record` do:
	// teleport the freshly created session onto the fixture's starting state and
	// seed its world keys before any frame is observed. Gated on a fixture being
	// present so default (no-flow) web sessions still start at the app root.
	// Returns the effective initial state so the LiveSession + the first frame
	// reflect the seed (it is orch.InitialState() when no seed applied).
	seedFixture := r.base.Flow
	if seedFixture == nil {
		// Live-harness replay posture: no flow fixture, but a seed-only fixture
		// may still teleport the session onto a mid-graph start state + world.
		seedFixture = r.base.SeedFixture
	}
	if len(initialWorld) > 0 {
		seedFixture = mergeSeedFixture(seedFixture, initialWorld)
	}
	initialState, err := seedFlowInitialState(orch, rt.Store, sid, seedFixture)
	if err != nil {
		return "", fmt.Errorf("seed flow initial state: %w", err)
	}

	tracePath := store.DefaultTracePath(def.App.ID, "web", string(sid))
	if mkErr := os.MkdirAll(filepath.Dir(tracePath), 0o755); mkErr != nil {
		return "", fmt.Errorf("create trace directory: %w", mkErr)
	}
	sink, err := store.OpenJSONL(tracePath)
	if err != nil {
		return "", fmt.Errorf("open trace sink: %w", err)
	}
	sinkOK := false
	defer func() {
		if !sinkOK {
			_ = sink.Close()
		}
	}()

	// Wrap the sink so the orchestrator's appends and the HTTP server's reads
	// share one lock — the JSONLSink underneath is not safe for concurrent
	// Append + History (mirrors web.go).
	live := server.NewLiveSession(sink, def, string(sid), string(initialState))
	orch.SetEventSink(live)
	// Bind the cassette deferred agent sink now that the real sink is ready.
	if rt.DeferredAgentSink != nil {
		rt.DeferredAgentSink.SetSink(live)
	}

	// Record the effective story as the first event so the trace self-describes
	// even after a later hot-reload (matches `kitsoki run` and web.go).
	if err := orch.RecordEffectiveStory(ctx, sid); err != nil {
		return "", fmt.Errorf("record effective story: %w", err)
	}

	// Fire the initial state's on_enter chain before the browser observes the
	// session so the first frame reflects on-enter-bound world keys (web.go).
	if err := orch.RunInitialOnEnter(ctx, sid); err != nil {
		return "", fmt.Errorf("run initial on_enter: %w", err)
	}

	id := uuid.NewString()
	e := &entry{
		StoryPath:     abs,
		Def:           def,
		synthetic:     loaded.synthetic,
		loadedContent: loaded.raw,
		rt:            rt,
		sid:           sid,
		sessionDir:    filepath.Dir(tracePath),
		source:        live,
		driver:        server.OrchestratorDriver{Orch: orch, SID: sid, Jobs: rt.JobStore, Chats: rt.ChatStore, TraceHistory: live.History},
		sink:          sink,
		lastActive:    time.Now(),
	}
	e.driver = newTrackingDriver(r, id, e.driver)

	// Register a per-session notification relay so the orchestrator's
	// background-turn fan-out reaches the cross-session SSE feed. The relay is
	// never explicitly unregistered here — it lives as long as the
	// orchestrator does. That used to mean "as long as the process" (the
	// original PoC leak this package doc named), but sessions are now bounded
	// (swarm-session-cap): once ensureCapacityLocked evicts this entry and
	// cleanupEvicted closes rt, the orchestrator (and the relay registered on
	// it) become unreachable and are collected together — see cleanupEvicted's
	// doc. The notifier is injected by web.go via SetNotifier after the server
	// is constructed; it is nil in tests that build a registry without a
	// server.
	if r.notifier != nil {
		r.notifier.AttachSession(orch, sid, id, rt.JobStore)
	}

	r.mu.Lock()
	victim, evictErr := r.ensureCapacityLocked()
	if evictErr != nil {
		r.mu.Unlock()
		return "", evictErr
	}
	r.sessions[id] = e
	r.currentSessionID = id
	r.mu.Unlock()
	r.cleanupEvicted(victim)

	// The current session changed: notify subscribers (trace-only / graph-only
	// surfaces follow this). Emitted outside the lock, after the value is
	// committed. Nil in tests that build a registry without a server.
	if r.notifier != nil {
		r.notifier.EmitCurrentSession(id, true)
	}

	ok = true
	sinkOK = true
	return id, nil
}

func mergeSeedFixture(base *testrunner.FlowFixture, initialWorld map[string]any) *testrunner.FlowFixture {
	out := &testrunner.FlowFixture{}
	if base != nil {
		out.InitialState = base.InitialState
		if len(base.InitialWorld) > 0 {
			out.InitialWorld = make(map[string]any, len(base.InitialWorld)+len(initialWorld))
			for k, v := range base.InitialWorld {
				out.InitialWorld[k] = v
			}
		}
	}
	if out.InitialWorld == nil {
		out.InitialWorld = make(map[string]any, len(initialWorld))
	}
	for k, v := range initialWorld {
		out.InitialWorld[k] = v
	}
	return out
}

// AttachExternal implements [server.ExternalAttachProvider]: it binds a live web
// session to a session in the PERSISTED store addressed by an external key
// (`transport:thread`). If a session is already bound to that key it attaches to
// it (the browser co-drives the same session a `kitsoki session continue` /
// loop.py process drives); otherwise it creates a session and binds the key. The
// returned session is driven under the per-session writer lock, so concurrent
// drivers (browser, inbound bridge, a separate continue process) serialise
// rather than interleave — a loser gets store.ErrSessionBusy (EX_TEMPFAIL) and
// retries, never a corrupted journey.
//
// Live SSE reflects turns this process drives. A turn another process commits is
// visible after a session reload (it is read from the shared store), not pushed
// over SSE — the exclusive trace-file flock means two processes cannot share one
// live trace stream. That cross-process live-stream is the remaining engine work
// noted in docs/architecture/transports.md.
func (r *SessionRegistry) AttachExternal(ctx context.Context, storyPath, key string) (string, error) {
	transportID, thread, err := parseExternalKey(key)
	if err != nil {
		return "", err
	}

	// Attaching the same external key twice in one process must return the live
	// session already bound to it, not open a second trace sink (the trace file
	// flock is exclusive — a second open would fail) and not split one ticket
	// across two in-process sessions.
	if id, found := r.liveByExternalKey(key); found {
		// Re-attaching to an already-live session makes it the current session
		// again (an operator opening that ticket's surface is now following it).
		r.mu.Lock()
		r.currentSessionID = id
		r.mu.Unlock()
		if r.notifier != nil {
			r.notifier.EmitCurrentSession(id, true)
		}
		return id, nil
	}

	loaded, err := r.loadStory(storyPath)
	if err != nil {
		return "", err
	}
	abs := loaded.path
	def := loaded.def
	if err := r.checkAuthorIdentity(def); err != nil {
		return "", err
	}

	rtCfg := r.base.config(abs, def)
	if loaded.reloader != nil {
		rtCfg.Reloader = loaded.reloader
		rtCfg.MiningRepoPath = loaded.repoRoot
	}
	rt, err := buildSessionRuntime(rtCfg)
	if err != nil {
		return "", err
	}
	ok := false
	defer func() {
		if !ok {
			rt.Close()
		}
	}()

	orch := rt.Orch

	// Resolve the external key to a persisted session, or create+bind one. Both
	// the lookup and the create run against the shared store the persisted
	// drivers use.
	sid, lookErr := rt.Store.LookupByKey(ctx, transportID, thread)
	created := false
	switch {
	case lookErr == nil:
		// Attach to the existing persisted session — nothing to create.
	case errors.Is(lookErr, store.ErrSessionNotFound):
		sid, err = orch.NewSession(ctx)
		if err != nil {
			return "", fmt.Errorf("create session: %w", err)
		}
		if err := rt.Store.BindExternalKey(ctx, sid, transportID, thread); err != nil {
			return "", fmt.Errorf("bind external key %q: %w", key, err)
		}
		created = true
	default:
		return "", fmt.Errorf("lookup external key %q: %w", key, lookErr)
	}

	// Open a live trace over the deterministic per-(app,transport,thread) path so
	// any history a prior in-process run wrote is loaded and the browser sees it.
	tracePath := store.DefaultTracePath(def.App.ID, transportID, thread)
	if mkErr := os.MkdirAll(filepath.Dir(tracePath), 0o755); mkErr != nil {
		return "", fmt.Errorf("create trace directory: %w", mkErr)
	}
	sink, err := store.OpenJSONL(tracePath)
	if err != nil {
		return "", fmt.Errorf("open trace sink: %w", err)
	}
	sinkOK := false
	defer func() {
		if !sinkOK {
			_ = sink.Close()
		}
	}()

	live := server.NewLiveSession(sink, def, string(sid), string(orch.InitialState()))
	orch.SetEventSink(live)
	if rt.DeferredAgentSink != nil {
		rt.DeferredAgentSink.SetSink(live)
	}

	if err := orch.RecordEffectiveStory(ctx, sid); err != nil {
		return "", fmt.Errorf("record effective story: %w", err)
	}
	// Fire the initial on_enter only for a freshly-created session; an existing
	// persisted session is already past its initial frame and re-firing would
	// re-run on_enter effects against a mid-flight world.
	if created {
		if err := orch.RunInitialOnEnter(ctx, sid); err != nil {
			return "", fmt.Errorf("run initial on_enter: %w", err)
		}
	}

	// The driver advances the session under the store's per-session writer lock,
	// so co-driving (browser + bridge + a continue process) serialises.
	lockedSID := sid
	lock := func(lctx context.Context, fn func() error) error {
		return rt.Store.WithWriterLock(lctx, lockedSID, fn)
	}
	driver := server.NewLockingDriver(server.OrchestratorDriver{Orch: orch, SID: sid, Jobs: rt.JobStore, Chats: rt.ChatStore, TraceHistory: live.History}, lock)

	id := uuid.NewString()
	e := &entry{
		StoryPath:     abs,
		Def:           def,
		synthetic:     loaded.synthetic,
		externalKey:   key,
		loadedContent: loaded.raw,
		rt:            rt,
		sid:           sid,
		source:        live,
		driver:        driver,
		sink:          sink,
		lastActive:    time.Now(),
	}
	e.driver = newTrackingDriver(r, id, e.driver)

	r.mu.Lock()
	victim, evictErr := r.ensureCapacityLocked()
	if evictErr != nil {
		r.mu.Unlock()
		return "", evictErr
	}
	r.sessions[id] = e
	r.currentSessionID = id
	r.mu.Unlock()
	r.cleanupEvicted(victim)

	// The current session changed: notify subscribers (same as NewSession).
	if r.notifier != nil {
		r.notifier.EmitCurrentSession(id, true)
	}

	// Attach the cross-session notification relay, same as NewSession — see
	// the note there for why eviction needs no matching UnregisterObserver.
	if r.notifier != nil {
		r.notifier.AttachSession(orch, sid, id, rt.JobStore)
	}

	ok = true
	sinkOK = true
	return id, nil
}

// liveByExternalKey returns the id of a live session already bound to key, if
// any. Used so a re-attach to the same ticket reuses the live session.
func (r *SessionRegistry) liveByExternalKey(key string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, e := range r.sessions {
		if e.externalKey == key {
			return id, true
		}
	}
	return "", false
}

// checkAuthorIdentity enforces the operator-identity invariant: a story that
// reads an author ACL key in a guard (today none ship one — stories/bugfix
// declares `allowed_authors` but never reads it) requires a configured server
// identity, because a browser turn with no identity would record the anonymous
// fallback and bounce off the guard with no operator-facing reason. The
// per-request X-Kitsoki-Actor header / actor RPC param are NOT "configured" —
// they are optional and may be absent on any turn — so the only thing that
// satisfies the invariant at start time is a server-level default actor.
func (r *SessionRegistry) checkAuthorIdentity(def *app.AppDef) error {
	if r.base.DefaultActor != "" {
		return nil
	}
	if def.ReadsWorldKeyInGuard("allowed_authors") {
		return fmt.Errorf(
			"story %q gates a turn on the author ACL key 'allowed_authors' but the web server has no configured operator identity; start with --actor <name> so browser-driven turns record a real principal",
			storyTitle(def))
	}
	return nil
}

// Get resolves a live session id to the server.Entry the server routes against.
// ok is false for an unknown id; the server turns that into a structured
// not-found error rather than a nil-deref.
func (r *SessionRegistry) Get(sessionID string) (server.Entry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.sessions[sessionID]
	if !ok {
		return server.Entry{}, false
	}
	return server.Entry{
		Source:    e.source,
		Driver:    e.driver,
		Meta:      &metaDriver{ctrl: r.metaControllerForLocked(e), chats: e.rt.ChatStore, entry: e},
		Artifacts: &server.JournalArtifactResolver{Reader: e.rt.JournalRead, SID: e.sid},
		Frames:    e.frameRecorderLocked(),
		Feedback:  e.feedbackSinkLocked(),
	}, true
}

// CurrentSession implements [server.CurrentSessionProvider]: it returns the id of
// the most recently created (NewSession) or attached (AttachExternal) session.
// Trace-only and graph-only surfaces, which have no chat to start a session, read
// this to discover and follow the active session. ok is false when no session has
// been created yet, or when the tracked id is no longer live (defensive — the PoC
// never deletes entries).
func (r *SessionRegistry) CurrentSession() (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.currentSessionID == "" {
		return "", false
	}
	if _, ok := r.sessions[r.currentSessionID]; !ok {
		return "", false
	}
	return r.currentSessionID, true
}

// List returns a runstatus.SessionHeader per live session, for
// runstatus.sessions.list. The header is read from each session's live snapshot
// so the current state / turn reflect where the session actually is.
func (r *SessionRegistry) List() []runstatus.SessionHeader {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]runstatus.SessionHeader, 0, len(r.sessions))
	for id, e := range r.sessions {
		snap, err := e.source.Snapshot()
		if err != nil {
			continue
		}
		// The header's SessionID must be the registry's entry key (the UUID
		// NewSession returned and Get routes on), NOT the orchestrator's
		// internal session id the snapshot carries — otherwise the home
		// screen's Open link / auto-nav would push a non-routable id.
		hdr := snap.Session
		hdr.SessionID = id
		out = append(out, hdr)
	}
	return out
}

// Reload mirrors the FULL TUI /reload path (tui.go handleReloadSlash), which is
// more than a bare Orchestrator.Reload:
//
//  1. read currentState from the session's live snapshot;
//  2. orch.Reload(StoryPath, currentState) → swaps the def + machine in;
//  3. RecordEffectiveStory so the trace stays self-contained across the reload;
//  4. when the prior state still exists, RerunOnEnter to re-fire the entered
//     state's on_enter chain (view-template / on_enter / prompt edits take
//     effect) — skipped when the state was removed by the edit.
//
// It introduces no new reload mechanism — it reuses the orchestrator methods the
// TUI already uses. prevStateExists is returned so the server can report
// {ok, prev_state_exists} and the UI can show the "current state removed;
// staying put" warning.
func (r *SessionRegistry) Reload(ctx context.Context, sessionID string) (bool, error) {
	r.mu.Lock()
	e, ok := r.sessions[sessionID]
	r.mu.Unlock()
	if !ok {
		return false, fmt.Errorf("reload: unknown session %q", sessionID)
	}

	// (1) Read the current state from the live snapshot — this is where the
	// session actually is, including after a prior reload.
	snap, err := e.source.Snapshot()
	if err != nil {
		return false, fmt.Errorf("reload: read session snapshot: %w", err)
	}
	currentState := app.StatePath(snap.Session.CurrentState)

	orch := e.rt.Orch

	// (2) Swap the freshly loaded def + machine into the orchestrator.
	res, err := orch.Reload(e.StoryPath, currentState)
	if err != nil {
		return false, fmt.Errorf("reload: %w", err)
	}

	// (3) Record the story change so the trace replay stays self-contained.
	if err := orch.RecordEffectiveStory(ctx, e.sid); err != nil {
		return res.PrevStateExists, fmt.Errorf("reload: record effective story: %w", err)
	}

	// Keep the entry's display def in sync with the reloaded definition, and
	// drop the cached meta controller so the next meta turn rebuilds against
	// the reloaded AppDef (a story edit may have changed meta_modes).
	freshContent := e.loadedContent
	if !e.synthetic {
		freshContent, _ = os.ReadFile(e.StoryPath)
	}
	r.mu.Lock()
	e.Def = res.Def
	e.loadedContent = freshContent
	e.metaController = nil
	r.mu.Unlock()

	// (4) Re-fire on_enter only when the current state survived the edit; a
	// removed state means there is nothing to re-enter (UI stays put).
	if res.PrevStateExists {
		if _, err := orch.RerunOnEnter(ctx, e.sid); err != nil {
			return true, fmt.Errorf("reload: rerun on_enter: %w", err)
		}
	}

	return res.PrevStateExists, nil
}

// ListStories returns the cached catalogue mapped onto server.StoryHeader, with
// active_sessions populated by scanning live entries whose StoryPath matches.
func (r *SessionRegistry) ListStories() []server.StoryHeader {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.storyHeadersLocked()
}

// Staleness compares the session's currently-loaded app.yaml bytes against the
// file on disk. stale is true when they differ; diff is a unified-diff string
// (context-3 lines) the UI can render in a modal. Returns no error on a missing
// file — that is treated as stale (content = "") rather than an error.
func (r *SessionRegistry) Staleness(_ context.Context, sessionID string) (stale bool, diff string, err error) {
	r.mu.Lock()
	e, ok := r.sessions[sessionID]
	if !ok {
		r.mu.Unlock()
		return false, "", fmt.Errorf("staleness: unknown session %q", sessionID)
	}
	loaded := e.loadedContent
	path := e.StoryPath
	synthetic := e.synthetic
	r.mu.Unlock()

	if synthetic {
		return false, "", nil
	}

	disk, readErr := os.ReadFile(path)
	if readErr != nil {
		disk = nil
	}

	if bytes.Equal(loaded, disk) {
		return false, "", nil
	}

	ud := difflib.UnifiedDiff{
		A:        difflib.SplitLines(string(loaded)),
		B:        difflib.SplitLines(string(disk)),
		FromFile: "loaded",
		ToFile:   "on-disk",
		Context:  3,
	}
	text, _ := difflib.GetUnifiedDiffString(ud)
	// Build a short summary: count added/removed lines.
	added, removed := 0, 0
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			added++
		} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			removed++
		}
	}
	_ = added
	_ = removed
	return true, text, nil
}

// Rescan re-walks the configured story dirs (DiscoverStories), replaces the
// cached catalogue, and returns the refreshed headers. Live sessions are left
// untouched — a session keeps running against the story it was started with even
// if that story's manifest changed or disappeared from the catalogue.
func (r *SessionRegistry) Rescan() ([]server.StoryHeader, error) {
	metas, err := webconfig.DiscoverStories(r.dirs, buildImportResolver())
	if err != nil {
		if !defaultStoryDirsMissing(r.dirs, err) {
			return nil, err
		}
		metas = nil
	}
	if len(metas) == 0 && defaultStoryDirs(r.dirs) {
		implicit, impErr := r.synthesizeImplicitRoot()
		if impErr != nil {
			return nil, fmt.Errorf("synthesize implicit root: %w", impErr)
		}
		metas = append(metas, webconfig.StoryMeta{Path: implicit.path, Def: implicit.def})
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stories = metas
	return r.storyHeadersLocked(), nil
}

func defaultStoryDirsMissing(dirs []string, err error) bool {
	if !defaultStoryDirs(dirs) || !errors.Is(err, os.ErrNotExist) {
		return false
	}
	return true
}

func defaultStoryDirs(dirs []string) bool {
	if len(dirs) != 1 {
		return false
	}
	clean := filepath.Clean(dirs[0])
	return clean == "stories"
}

// storyHeadersLocked maps the cached StoryMeta catalogue onto server.StoryHeader,
// filling ActiveSessions with the ids of live sessions started from each story.
// Caller holds r.mu.
func (r *SessionRegistry) storyHeadersLocked() []server.StoryHeader {
	// Index live session ids by the story they run.
	byStory := map[string][]string{}
	for id, e := range r.sessions {
		snap, err := e.source.Snapshot()
		if err != nil {
			continue
		}
		if snap.Session.Terminal {
			continue
		}
		byStory[e.StoryPath] = append(byStory[e.StoryPath], id)
	}
	out := make([]server.StoryHeader, 0, len(r.stories))
	for _, m := range r.stories {
		active := byStory[m.Path]
		if active == nil {
			active = []string{}
		}
		out = append(out, server.StoryHeader{
			Path:           m.Path,
			AppID:          m.Def.App.ID,
			Title:          storyTitle(m.Def),
			ActiveSessions: active,
		})
	}
	return out
}

// storyTitle picks the human-facing story title: the app's declared title when
// present, falling back to its id (which is always set).
func storyTitle(def *app.AppDef) string {
	if def == nil {
		return ""
	}
	if def.App.Title != "" {
		return def.App.Title
	}
	return def.App.ID
}

// EditorApp implements [server.EditorProvider]: it loads the story at
// storyPath fresh from disk and compiles it to an app.App for the read-only
// story-editor RPCs. Loading fresh (rather than reusing a cached catalogue
// Def) keeps the editor reflecting the on-disk story even between rescans, and
// keeps the editor independent of any live session. ok is false (no error) for
// an unknown story path or one that fails to load/validate — the server maps
// that to a structured not-found error.
//
// storyDir is the directory containing the manifest, used to resolve cassette
// globs for the agent workbench.
func (r *SessionRegistry) EditorApp(storyPath string) (app.App, string, bool) {
	abs, err := filepath.Abs(storyPath)
	if err != nil {
		return nil, "", false
	}
	// Only serve stories in the catalogue, so the editor surface cannot be used
	// to load arbitrary files off disk.
	r.mu.Lock()
	known := false
	for _, m := range r.stories {
		if m.Path == abs {
			known = true
			break
		}
	}
	r.mu.Unlock()
	if !known {
		return nil, "", false
	}

	loaded, err := r.loadStory(abs)
	if err != nil {
		return nil, "", false
	}
	return app.Compile(loaded.def), loaded.repoRoot, true
}

// ── Meta mode wiring ───────────────────────────────────────────────────────

// agentRegistryLocked returns the shared builtin agent registry every meta
// controller resolves agent names against, building it once. Caller holds r.mu.
//
// Builtins cover the modes the web surface exposes (story-author / -explainer,
// kitsoki-explainer); per-app agent overrides for meta modes are out of scope
// for the web surface.
func (r *SessionRegistry) agentRegistryLocked() agents.Registry {
	if r.agentReg == nil {
		r.agentReg = agents.NewBuiltins()
	}
	return r.agentReg
}

// agentForMeta picks the meta-mode agent: the deterministic no-LLM stub when
// the server runs in flow posture (--flow / --host-cassette), else the real
// claude-CLI adapter. This is the seam that keeps `kitsoki web --flow` (and the
// Playwright demo) free of any LLM call.
//
// When in stub posture, KITSOKI_META_STREAM_DELAY_MS sets the per-event pause
// the stub injects while emitting streaming events. Set it to 60-100 for demo
// recordings; leave unset (or 0) for fast tests.
func (r *SessionRegistry) agentForMeta() metamode.AgentCaller {
	// Deterministic posture ⇒ the no-LLM meta stub. This covers the nil-harness
	// flow posture (Flow != nil) AND the live-harness replay/recording posture
	// backed by a host cassette (--harness replay --recording --host-cassette):
	// in both, a story room's on_enter coding-agent task (e.g. dev-story's
	// landing_agent) must NOT spend a live LLM. Without this, replay tours that
	// pass through such a room dispatch a real agent backend mid-capture.
	deterministic := r.base.Flow != nil ||
		(r.base.HostCassette != "" &&
			(r.base.HarnessType == "replay" || r.base.HarnessType == "recording"))
	if deterministic {
		var opts []metamode.StubOption
		if v := os.Getenv("KITSOKI_META_STREAM_DELAY_MS"); v != "" {
			if ms, err := strconv.Atoi(v); err == nil && ms > 0 {
				opts = append(opts, metamode.WithStubStreamDelay(time.Duration(ms)*time.Millisecond))
			}
		}
		return metamode.NewStubAgentCaller(opts...)
	}
	return metamode.NewAgentCallerAdapter()
}

// metaControllerForLocked returns e's meta controller, building it lazily and
// caching it on the entry. Caller holds r.mu.
func (r *SessionRegistry) metaControllerForLocked(e *entry) *metamode.Controller {
	if e.metaController == nil {
		e.metaController = &metamode.Controller{
			Chats:  metamode.NewChatStoreAdapter(e.rt.ChatStore),
			Agents: r.agentRegistryLocked(),
			AppDef: e.rt.Orch.AppDef(),
			Agent:  r.agentForMeta(),
		}
	}
	return e.metaController
}

// MetaSelf returns the session-less ("self") meta driver for the home screen —
// the cross-app kitsoki.* modes that need no running story. It is opened lazily
// on first use; ok is false when the resources can't be built (e.g. DB open
// failure), in which case home-screen meta reports not-available.
func (r *SessionRegistry) MetaSelf() (server.MetaDriver, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.ensureSelfMetaLocked(); err != nil {
		return nil, false
	}
	return &metaDriver{ctrl: r.metaSelfCtrl, chats: r.metaSelfChat, entry: nil}, true
}

// ensureSelfMetaLocked lazily opens the self-meta store + chat store and builds
// the self controller over a synthetic AppDef carrying the builtin meta_modes.
// Caller holds r.mu. Idempotent: a no-op once built.
func (r *SessionRegistry) ensureSelfMetaLocked() error {
	if r.metaSelfCtrl != nil {
		return nil
	}
	s, err := store.Open(r.base.DBPath)
	if err != nil {
		return fmt.Errorf("meta self: open store: %w", err)
	}
	cs, err := chats.NewStore(s.DB())
	if err != nil {
		_ = s.Close()
		return fmt.Errorf("meta self: open chat store: %w", err)
	}
	// Synthetic AppDef: the self modes (kitsoki.*) key under metamode.SelfAppID
	// at resolve time, so the App.ID here is only a fallback label. Injecting
	// the builtins gives the controller the kitsoki.* mode declarations.
	def := &app.AppDef{}
	def.App.ID = metamode.SelfAppID
	app.InjectBuiltinMetaModes(def)

	r.metaSelfStr = s
	r.metaSelfChat = cs
	r.metaSelfCtrl = &metamode.Controller{
		Chats:  metamode.NewChatStoreAdapter(cs),
		Agents: r.agentRegistryLocked(),
		AppDef: def,
		Agent:  r.agentForMeta(),
	}
	return nil
}

// Compile-time assertions that SessionRegistry satisfies the provider seams.
var (
	_ server.SessionProvider        = (*SessionRegistry)(nil)
	_ server.MetaSelfProvider       = (*SessionRegistry)(nil)
	_ server.EditorProvider         = (*SessionRegistry)(nil)
	_ server.SeededSessionProvider  = (*SessionRegistry)(nil)
	_ server.ExternalAttachProvider = (*SessionRegistry)(nil)
	_ server.CurrentSessionProvider = (*SessionRegistry)(nil)
)

// seedFlowInitialState honors a flow fixture's initial_state / initial_world on
// a freshly created web session, mirroring what `kitsoki test flows` and
// `kitsoki record` do (internal/testrunner.seedInitialState,
// cmd/kitsoki/record.go). It is the runtime fix for `kitsoki web --flow`
// previously ignoring the fixture seed: a conversation-driven dev-story tour
// can now START at the fixture's state with its world pre-seeded.
//
// It writes synthetic turn-0 seed events — a TransitionApplied to teleport the
// journey onto initial_state, and one EffectApplied per initial_world key — and
// arms any Timeout declared on the seeded state. The orchestrator's loadJourney
// replays the event log, so persisting these before any turn (and before
// on_enter / the first observed frame) bootstraps the session exactly as the
// seed path does.
//
// fixture == nil (no --flow / --host-cassette) or an empty seed is a no-op:
// the session keeps starting at orch.InitialState(), so default web behavior is
// unchanged. Returns the effective initial state (the seeded state when one was
// applied, else orch.InitialState()) so the caller stamps the LiveSession and
// first frame with it.
func seedFlowInitialState(orch *orchestrator.Orchestrator, st store.Store, sid app.SessionID, fixture *testrunner.FlowFixture) (app.StatePath, error) {
	if fixture == nil || (fixture.InitialState == "" && len(fixture.InitialWorld) == 0) {
		return orch.InitialState(), nil
	}

	var events []store.Event
	if fixture.InitialState != "" {
		events = append(events, store.Event{
			Kind: store.TransitionApplied,
			Turn: 0,
			Payload: mustSeedJSON(map[string]any{
				"from":   "",
				"to":     fixture.InitialState,
				"intent": "__seed__",
			}),
		})
	}
	for k, v := range fixture.InitialWorld {
		events = append(events, store.Event{
			Kind:    store.EffectApplied,
			Turn:    0,
			Payload: mustSeedJSON(map[string]any{"set": map[string]any{k: v}}),
		})
	}

	if fixture.InitialState != "" {
		for i := range events {
			if events[i].StatePath == "" {
				events[i].StatePath = app.StatePath(fixture.InitialState)
			}
		}
	}

	sink := store.NewStoreSinkAdapter(st, sid)
	if err := sink.AppendBatch(events); err != nil {
		return "", err
	}

	// Arm any Timeout on the seeded state, matching seedInitialState — seed
	// events bypass the normal transition path that would otherwise arm it.
	if fixture.InitialState != "" {
		orch.ArmTimeoutForInitialState(sid, app.StatePath(fixture.InitialState))
		return app.StatePath(fixture.InitialState), nil
	}
	return orch.InitialState(), nil
}

func mustSeedJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("seedFlowInitialState: marshal: %v", err))
	}
	return b
}
