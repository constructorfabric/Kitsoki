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
// Sessions are in-memory only: they die with the process (no persistence across
// restarts, no cap, no kill action — all decided leans for the PoC).
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/google/uuid"

	"kitsoki/internal/agents"
	"kitsoki/internal/app"
	"kitsoki/internal/chats"
	"kitsoki/internal/metamode"
	"kitsoki/internal/runstatus"
	"kitsoki/internal/runstatus/server"
	"kitsoki/internal/store"
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
	StoryPath string
	Def       *app.AppDef
	rt        *sessionRuntime
	sid       app.SessionID
	source    *server.LiveSession
	driver    server.Driver
	sink      *store.JSONLSink

	// metaController is the lazily-built meta-mode controller for this
	// session, cached so the persistent chat store / agent registry / AppDef
	// binding survives across turns. Reload nils it so the next meta turn
	// rebuilds against the reloaded AppDef.
	metaController *metamode.Controller
}

// SessionRegistry implements [server.SessionProvider]. It is safe for concurrent
// use: a single mutex guards both the story catalogue and the live-session map,
// since the SSE pollers and RPC handlers call in from many goroutines.
type SessionRegistry struct {
	cfg  webconfig.WebConfig
	base runtimeBase
	dirs []string

	mu       sync.Mutex
	stories  []webconfig.StoryMeta
	sessions map[string]*entry

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
		cfg:      cfg,
		base:     base,
		dirs:     dirs,
		sessions: map[string]*entry{},
	}
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
	abs, err := filepath.Abs(storyPath)
	if err != nil {
		return "", fmt.Errorf("resolve story path %q: %w", storyPath, err)
	}

	def, err := loadAppWithEnv(abs)
	if err != nil {
		return "", err
	}

	rt, err := buildSessionRuntime(r.base.config(abs, def))
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
	live := server.NewLiveSession(sink, def, string(sid), string(orch.InitialState()))
	orch.SetEventSink(live)

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
		StoryPath: abs,
		Def:       def,
		rt:        rt,
		sid:       sid,
		source:    live,
		driver:    server.OrchestratorDriver{Orch: orch, SID: sid},
		sink:      sink,
	}

	r.mu.Lock()
	r.sessions[id] = e
	r.mu.Unlock()

	ok = true
	sinkOK = true
	return id, nil
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
	}, true
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
	r.mu.Lock()
	e.Def = res.Def
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

// Rescan re-walks the configured story dirs (DiscoverStories), replaces the
// cached catalogue, and returns the refreshed headers. Live sessions are left
// untouched — a session keeps running against the story it was started with even
// if that story's manifest changed or disappeared from the catalogue.
func (r *SessionRegistry) Rescan() ([]server.StoryHeader, error) {
	metas, err := webconfig.DiscoverStories(r.dirs)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stories = metas
	return r.storyHeadersLocked(), nil
}

// storyHeadersLocked maps the cached StoryMeta catalogue onto server.StoryHeader,
// filling ActiveSessions with the ids of live sessions started from each story.
// Caller holds r.mu.
func (r *SessionRegistry) storyHeadersLocked() []server.StoryHeader {
	// Index live session ids by the story they run.
	byStory := map[string][]string{}
	for id, e := range r.sessions {
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

// oracleForMeta picks the meta-mode oracle: the deterministic no-LLM stub when
// the server runs in flow posture (--flow / --host-cassette), else the real
// claude-CLI adapter. This is the seam that keeps `kitsoki web --flow` (and the
// Playwright demo) free of any LLM call.
func (r *SessionRegistry) oracleForMeta() metamode.OracleCaller {
	if r.base.Flow != nil {
		return metamode.NewStubOracleCaller()
	}
	return metamode.NewOracleCallerAdapter()
}

// metaControllerForLocked returns e's meta controller, building it lazily and
// caching it on the entry. Caller holds r.mu.
func (r *SessionRegistry) metaControllerForLocked(e *entry) *metamode.Controller {
	if e.metaController == nil {
		e.metaController = &metamode.Controller{
			Chats:  metamode.NewChatStoreAdapter(e.rt.ChatStore),
			Agents: r.agentRegistryLocked(),
			AppDef: e.rt.Orch.AppDef(),
			Oracle: r.oracleForMeta(),
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
		Oracle: r.oracleForMeta(),
	}
	return nil
}

// Compile-time assertions that SessionRegistry satisfies the provider seams.
var (
	_ server.SessionProvider  = (*SessionRegistry)(nil)
	_ server.MetaSelfProvider = (*SessionRegistry)(nil)
)
