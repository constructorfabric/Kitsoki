// Package orchestrator implements the turn-loop brain (see
// docs/architecture/overview.md "The journey of one turn").
// It is the ONLY component that calls store.AppendEvents.
// The machine is pure (no I/O); the harness may call the LLM.
package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/ansi"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"kitsoki/internal/agent"
	"kitsoki/internal/app"
	"kitsoki/internal/chats"
	"kitsoki/internal/clock"
	"kitsoki/internal/expr"
	"kitsoki/internal/harness"
	"kitsoki/internal/host"
	"kitsoki/internal/intent"
	"kitsoki/internal/jobs"
	"kitsoki/internal/journal"
	"kitsoki/internal/machine"
	"kitsoki/internal/render"
	"kitsoki/internal/semroute"
	"kitsoki/internal/store"
	"kitsoki/internal/trace"
	"kitsoki/internal/transport"
	"kitsoki/internal/turncache"
	"kitsoki/internal/world"
)

// pendingClarify holds the in-flight slot-fill state while the TUI
// is collecting missing slots from the user (held in-memory).
type pendingClarify struct {
	intentName string
	slots      map[string]any // already-collected slots
}

// Orchestrator drives a single session from raw input to applied events.
type Orchestrator struct {
	def        *app.AppDef
	machine    machine.Machine
	store      store.Store
	harness    harness.Harness
	hosts      *host.Registry
	transports *transport.Registry
	logger     *slog.Logger
	// scheduler is the background-job scheduler (optional; nil means background
	// effects are ignored and the invocation is dispatched synchronously).
	scheduler jobs.Scheduler
	// jobStore is the SQLite-backed job store used to load job rows for
	// on_complete processing and to post notifications.
	jobStore *jobs.JobStore

	// chatStore is the SQLite-backed chat store used by chat-aware agent handlers
	// and the host.chat.* built-ins. Optional; nil disables chat persistence.
	chatStore host.ChatStore

	// promptRenderer renders agent prompt files through the story's prompt
	// search path (overlay → story) so a prompt can {% extends %} / {% include %}
	// the story's base prompts and a project can extend a story without forking
	// it. Built once from def.BaseDir + def.Prompts; nil when there's no
	// on-disk story dir (LoadBytes / tests), in which case agent handlers use
	// the legacy KITSOKI_APP_DIR + render.Pongo path. See docs/stories/prompts.md.
	promptRenderer *render.AppRenderer

	// promptOverlay is a run-time prompt-overlay dir (kitsoki run
	// --prompt-overlay) that overrides def.Prompts.Overlay when set.
	promptOverlay string

	// agentBackendName selects the coding-agent CLI every host.agent.* call
	// (and the intent-routing harness) forks: "" / "claude" (default) or
	// "copilot". Installed into the dispatch context via
	// host.WithAgentBackendNamed alongside the agents/providers maps. Set via
	// WithAgentBackendName (kitsoki --agent / $KITSOKI_AGENT). When a harness
	// profile is selected (below) its backend supersedes this per-dispatch; this
	// remains the fallback for the no-profile path.
	agentBackendName string

	// harnessProfiles / defaultProfile / selection make the backend/provider/model
	// a session-mutable, profile-named choice resolved per-dispatch instead of the
	// static agentBackendName. Seeded by WithHarnessProfiles from .kitsoki.yaml;
	// empty leaves the legacy static path untouched. selection is read on every
	// dispatch (resolveSelection) and written from a surface goroutine
	// (SetSelection) — guarded by selMu. See docs/architecture/harness-profiles.md.
	harnessProfiles map[string]HarnessProfile
	defaultProfile  string
	selection       ProfileSelection
	selMu           sync.RWMutex

	// modelCache memoises the always-on model ids fetched from a profile's
	// ModelsEndpoint (keyed by profile name), guarded by its own mutex so a fetch
	// never blocks the dispatch-hot selMu.
	modelCache map[string][]string
	modelMu    sync.Mutex

	// roomEnterSink, when non-nil, receives a pre-rendered banner string
	// every time a turn transitions into a new room (top-level state).
	// Fired AFTER the machine collects on_enter side-effects but BEFORE
	// host calls dispatch, so the banner lands in the TUI transcript
	// before any agent / Bash / etc. tool-use breadcrumbs from the
	// on_enter chain stream in. Optional; nil disables the hook.
	roomEnterSink RoomEnterSink

	// chatsConcrete is the concrete *chats.Store, set when callers want the
	// continue-mode resume path to surface pending drives and backgrounded PTY
	// chats. Distinct from chatStore (the host-interface flavour) because the
	// resume reads need methods (ListDrivesBySession, ListPTYForHost) that
	// aren't on host.ChatStore. Optional; nil disables the surfacing.
	chatsConcrete *chats.Store

	// agentRegistry holds the per-app agent plugin registry.
	// When non-nil, injected into the dispatch context via host.WithAgentRegistry
	// so agent handlers can route through Agent.Ask. When nil, handlers fall
	// through to their existing direct claude-CLI logic (backwards compat).
	// Set via WithAgentRegistry.
	agentRegistry *agent.Registry

	// semanticOverride is the process-level kill switch for the deterministic
	// semantic-routing stack (semroute + turn-cache + default_intent sink +
	// free-form fallback). nil means "no override — defer to the per-app
	// routing.enabled config"; a non-nil value overrides it. Set via
	// WithSemanticRouting from the CLI (--semantic-routing / KITSOKI_SEMANTIC_ROUTING),
	// which defaults it to false so production routing is an isolated main-model
	// decision (harness.RunTurn). The zero-cost exact display/example match
	// (TryDeterministic) runs regardless of this gate. See
	// semanticStackEnabled and docs/architecture/semantic-routing.md.
	semanticOverride *bool

	// journalWriter is the durable journal writer (continue-mode dual-write).
	// When nil, callers fall through to the legacy AppendEvents path.
	// Set via WithJournalWriter; individual turn-write call sites are migrated
	// by the next agent.
	journalWriter journal.Writer

	// journalReader is the read-side counterpart to journalWriter, used by the
	// AttachSession resume path (continue-mode).  When nil, AttachSession
	// falls back to LoadJourney-only (no transcript / no clarify rehydration).
	// Set via WithJournalReader.
	journalReader journal.Reader

	// clk is the injectable time source used by the timeout dispatcher.
	// Defaults to clock.Real() when no WithClock option is supplied.
	clk clock.Clock

	// timeouts owns per-session pending Timeout entries.  Lazily constructed
	// in New so orchestrators without a clock/store still get a working
	// in-memory dispatcher.
	timeouts *timeoutDispatcher

	// eventSink, when non-nil, receives event writes for every turn.
	// Set via WithEventSink.  Wave 3-entry seam: kitsoki turn --trace wires a
	// *store.JSONLSink here so events are appended to the JSONL file.
	// When sinkIsAuthority is true, loadJourney reads from eventSink.History()
	// rather than o.store.LoadHistory — this is the pure-JSONL path used by
	// kitsoki turn --trace.  When sinkIsAuthority is false (dual-write mode for
	// session continue / TUI), SQLite remains the read source.
	eventSink       store.EventSink
	sinkIsAuthority bool // true → JSONL is the sole source of truth for loadJourney

	// decider, when non-nil, is the engine-driven LLM decider config used to
	// resolve one-shot (or decider:llm) decision gates. nil disables it.
	decider *DeciderConfig

	// execMode is the run's execution mode.
	// The zero value (ExecOneShot) preserves the historical behaviour:
	// synthetic emit_intent chains auto-advance through every gate within a
	// turn. ExecStaged makes a multi-way decision gate end the turn so a
	// human decides. Set via WithExecutionMode; the `run` TUI defaults to
	// staged while `kitsoki turn` / tests stay one-shot.
	execMode ExecutionMode

	// pending tracks in-flight clarifications keyed by session ID.
	mu      sync.Mutex
	pending map[app.SessionID]*pendingClarify
	// cancelListeners holds the cancel funcs for per-session listener goroutines.
	// Goroutines are torn down when the session is closed.
	cancelListeners map[app.SessionID]context.CancelFunc
	// sessionLocks serialises read-modify-write of (journey → events) for one
	// session.  The foreground turn path (Turn/SubmitDirect/ContinueTurn) and
	// the background-job-terminal path (handleJobTerminal) both compute
	// turn = journey.Turn + 1 from the live event log and then call
	// store.AppendEvents under that turn number.  Without this lock, a
	// background job whose handler runs to completion before the foreground
	// Turn finishes appending its events races: the listener goroutine reads
	// journey.Turn = N-1 (Turn hasn't committed yet) and tries to write
	// turn = N, colliding with the foreground writer's UNIQUE
	// (session_id, turn, seq) PK.  Held across the entire load → mutate →
	// AppendEvents critical section in every writer path so no two writers
	// can compute the same turn number for the same session.  Per-session,
	// not global — concurrent turns for *different* sessions remain
	// unserialised.
	sessionLocks map[app.SessionID]*sync.Mutex

	// obsMu guards observers.  observers receive OnBackgroundTurn
	// callbacks after handleJobTerminal commits the synthetic turn —
	// see observer.go.  Held only for the slice copy in
	// notifyBackgroundTurn, never across the observer callback itself,
	// so an observer that re-enters Register/UnregisterObserver cannot
	// deadlock.
	obsMu     sync.Mutex
	observers []SessionObserver

	// embedTier is the optional embedding routing tier (Slice 3). When
	// non-nil and cfg.Enabled is true, TrySemantic tries it between the
	// deterministic synonyms miss and the LLM hop. Set via WithEmbedTier.
	embedTier *EmbedTier

	// matcher is the per-app semantic-routing index (see
	// docs/architecture/semantic-routing.md). Compiled lazily on first
	// TrySemantic call; subsequent calls reuse the cached *Matcher.
	// matcherErr remembers a compile failure so we don't retry on
	// every turn — the orchestrator surfaces the error once via
	// trace and treats the matcher as a no-op thereafter.
	//
	// Both fields are guarded by matcherOnce. A nil matcher after
	// matcherOnce.Do has run means "no semantic routing for this
	// app" — either disabled in app.Routing, the AppDef declares no
	// synonyms/examples, or compilation failed.
	matcherOnce sync.Once
	matcher     *semroute.Matcher
	matcherErr  error

	// cache is the optional turn-result cache (see
	// docs/architecture/semantic-routing.md "Turn cache"). Wired via
	// WithTurnCache; nil disables
	// the cache tier entirely. The cache is per-orchestrator (and
	// therefore per-app), so InvalidateOtherHashes / SweepCold /
	// TrimLRU run at most once per orchestrator on first turn —
	// see internal/orchestrator/cache.go.
	cache          turncache.Cache
	cacheSweepOnce sync.Once
	appHashOnce    sync.Once
	appHashValue   string

	// ideLink is the process-lifetime IDE connection (an *ide.Link, held as the
	// host.IDELink interface so orchestrator never imports internal/ide). It is
	// injected into the host-dispatch ctx via host.WithIDELink so host.ide.*
	// handlers can resolve the live editor, and its Connected() status seeds the
	// `ide.connected` world key each turn. nil is the default and is safe:
	// host.ide.* handlers then return the typed not-connected Result and the
	// agent env-scrub gate stays off (headless / flow tests are byte-identical
	// to before). Set by the TUI (slice 2) via SetIDELink once it owns a Link.
	//
	// ideMu guards ideLink: the TUI mutates it from its bubbletea Update loop
	// (on /ide connect|disconnect) while the per-turn dispatch path reads it on
	// a different goroutine. The interface value is swapped under the lock; the
	// *ide.Link it points at has its own internal locking for the live socket.
	ideMu   sync.RWMutex
	ideLink host.IDELink

	// reloader, when non-nil, supplies the freshly-loaded *app.AppDef that
	// Reload swaps in — overriding the default app.Load(appPath). It exists so a
	// config-synthesized root (rung 0/1: no app.yaml on disk) can re-synthesize
	// from .kitsoki.yaml on /reload, while a file-backed (rung 2) root keeps the
	// historical app.Load(path) behaviour via the closure
	// `func() { return app.Load(appPath) }`. A rung-1 edit and a rung-2 edit thus
	// travel the IDENTICAL Reload + RerunOnEnter path. Set via WithReloader; nil
	// preserves today's path-based reload exactly. See
	// docs/stories/imports.md "The blank root that grows".
	reloader func() (*app.AppDef, error)

	// miner, when non-nil and mining is enabled, is the ambient session miner
	// (docs/proposals/ambient-session-miner.md). NewSession Starts it (which fires
	// the first-launch history seed iff the slug is unmined); a finished turn /
	// landed background job pings Notify so the debounced live pass mines the new
	// transcripts. Held as the narrow SessionMiner interface so the orchestrator
	// never imports internal/mining (which would invert the dependency — the wire
	// adapter package is the only orchestrator↔mining edge). nil ⇒ no ambient
	// mining; off in every flow/test path, so no fixture ever spends LLM.
	miner SessionMiner
	// minerRepoPath is the repo path the miner resolves transcripts for (the
	// working dir / instance root). Empty ⇒ os.Getwd at Start time.
	minerRepoPath string
}

// SessionMiner is the narrow lifecycle seam the orchestrator drives the ambient
// session miner through (*mining.Miner in production, a fake in tests). Start
// fires the first-launch seed; Notify debounces a live pass over new
// transcripts. Both are non-blocking and survive the turn (the pass runs on the
// background-jobs runner). Kept in the orchestrator package so internal/mining
// stays free of an orchestrator import edge. See WithMiner.
type SessionMiner interface {
	Start(ctx context.Context, sid app.SessionID, repoPath string) error
	Notify(ctx context.Context)
}

// SetMiner installs the ambient session miner after construction. Used by the
// runtime where the orchestrator itself is the miner's EventSink (so the miner
// cannot be built until the orchestrator exists). repoPath is the dir the miner
// resolves transcripts for; empty ⇒ the process working dir at Start time. Safe
// to call before the first NewSession; not safe concurrently with one. Pass a
// nil miner to disable. See WithMiner for the option-time equivalent.
func (o *Orchestrator) SetMiner(m SessionMiner, repoPath string) {
	o.miner = m
	o.minerRepoPath = repoPath
}

// HasMiner reports whether an ambient session miner is wired. It exists so the
// flow/test harness can assert the no-LLM invariant: a flow-posture runtime must
// build NO miner (no fixture ever spends LLM via ambient mining).
func (o *Orchestrator) HasMiner() bool { return o.miner != nil }

// SetIDELink installs the process IDE connection so host.ide.* handlers resolve
// the live editor and the `ide.connected` world key reflects its status. Pass
// nil to detach (e.g. on /ide disconnect); nil is safe everywhere. Slice 2's
// TUI calls this with its *ide.Link. Safe to call concurrently with in-flight
// turns (the swap is guarded by ideMu).
func (o *Orchestrator) SetIDELink(l host.IDELink) {
	o.ideMu.Lock()
	defer o.ideMu.Unlock()
	o.ideLink = l
}

// currentIDELink returns the installed IDE link under the read lock. Used by
// every reader of ideLink (host dispatch, off-path dispatch, seedIDEConnected)
// so a concurrent SetIDELink can never race the interface value. Returns nil
// when none is installed (the headless / flow-test default).
func (o *Orchestrator) currentIDELink() host.IDELink {
	o.ideMu.RLock()
	defer o.ideMu.RUnlock()
	return o.ideLink
}

// seedIDEConnected reflects the live IDE link's connectivity into the world as
// a NESTED `ide.connected` gate so stories and views can branch on
// `world.ide.connected` (expr-lang resolves that path as
// World["ide"]["connected"], never a flat "ide.connected" key — a flat dotted
// key is unreachable from the expression engine).
//
// This is deliberately EPHEMERAL liveness, not a journaled effect: it is
// recomputed every turn in loadJourney (after BuildJourney rebuilds the world
// from the event log) so a mid-session connect/disconnect is always visible,
// and it is never emitted as an EffectApplied event — so it is not carried into
// the journaled world or any snapshot built from it (the reconstructed world
// never contains `ide`; we re-seed it fresh each load). Recomputing live
// liveness is the correct model here: a stale "connected" pinned from a prior
// turn would lie about the editor's current state.
//
// It merges into any existing `ide` map (e.g. the ambient selection map other
// code may place there) rather than clobbering it.
func (o *Orchestrator) seedIDEConnected(w world.World) {
	if w.Vars == nil {
		return
	}
	l := o.currentIDELink()
	connected := l != nil && l.Connected()
	if existing, ok := w.Vars["ide"].(map[string]any); ok {
		existing["connected"] = connected
		return
	}
	w.Vars["ide"] = map[string]any{"connected": connected}
}

// seedSessionID projects the orchestrator's per-session SessionID into the
// world as `world.session_id` so stories can derive a session-scoped identity
// (e.g. the bugfix story keys its worktree dir on `bf-{ticket}-{session}` so
// two concurrent sessions on the same ticket can never share one checkout —
// the destructive shared-tree bug
// 2026-06-03T121409Z-concurrent-dogfood-sessions-share-checkout-destructive-git).
//
// Like seedIDEConnected, this is deliberately EPHEMERAL liveness rather than a
// journaled effect: the SessionID is a stable per-session constant, so
// re-seeding it fresh on every loadJourney is correct and replay-safe (the
// reconstructed world never needs to carry it). It only fills the key when
// absent, so a story that explicitly set `session_id` in world keeps its value.
func (o *Orchestrator) seedSessionID(w world.World, sid app.SessionID) {
	if w.Vars == nil {
		return
	}
	if existing, ok := w.Vars["session_id"].(string); ok && existing != "" {
		return
	}
	w.Vars["session_id"] = string(sid)
}

// New creates an Orchestrator.
func New(def *app.AppDef, m machine.Machine, s store.Store, h harness.Harness, opts ...Option) *Orchestrator {
	o := &Orchestrator{
		def:             def,
		machine:         m,
		store:           s,
		harness:         h,
		logger:          slog.Default(),
		pending:         make(map[app.SessionID]*pendingClarify),
		cancelListeners: make(map[app.SessionID]context.CancelFunc),
		sessionLocks:    make(map[app.SessionID]*sync.Mutex),
		clk:             clock.Real(),
	}
	for _, opt := range opts {
		opt(o)
	}
	// Build the prompt renderer from the story's base dir + prompts config so
	// agent prompt files render through a search-path TemplateSet ({% extends %}
	// / {% include %}, @story / @shared, overlay-first). nil when there's no
	// on-disk story dir (LoadBytes / tests) — handlers then use the legacy path.
	o.promptRenderer = buildPromptRenderer(def, o.promptOverlay)
	// Construct the timeout dispatcher.  Build a SQLite-backed TimeoutStore
	// from the session store's shared *sql.DB so pending timeouts survive a
	// process restart.  If the store has no DB (e.g. in-memory test rig), or
	// if table creation fails, fall back to the noop store so tests that do
	// not care about persistence still work.
	var ts host.TimeoutStore
	if db := s.DB(); db != nil {
		if sqlTS, tsErr := host.NewSQLiteTimeoutStore(db); tsErr == nil {
			ts = sqlTS
		} else {
			o.logger.Warn(trace.EvTimeoutError,
				slog.String("phase", "timeout_store_init"),
				slog.String("err", tsErr.Error()),
			)
			ts = host.NewNoopTimeoutStore()
		}
	} else {
		ts = host.NewNoopTimeoutStore()
	}
	td, tdErr := newTimeoutDispatcher(o.clk, ts, o.logger)
	if tdErr != nil {
		// A schema failure is recoverable: log and proceed with no Timeout
		// support.  Apps that don't use Timeout: still work.
		o.logger.Warn(trace.EvTimeoutError,
			slog.String("phase", "dispatcher_init"),
			slog.String("err", tdErr.Error()),
		)
	} else {
		o.timeouts = td
		td.setOrchestrator(o)
		o.rearmPersistedTimeouts()
	}
	return o
}

// ExecutionMode selects how the engine resolves intent gates — the set of
// advancing intents available at the end of a room/phase's turn. Every
// room/phase ends in an intent gate resolved by a decider (default /
// LLM / human).
type ExecutionMode int

const (
	// ExecOneShot advances autonomously: synthetic emit_intent chains run
	// through every gate within a turn (the historical behaviour, and the
	// zero value so existing callers/tests are unaffected). A multi-way gate
	// with no firing emit still rests, as before.
	ExecOneShot ExecutionMode = iota
	// ExecStaged ends the turn at a multi-way decision gate so a human picks
	// the next intent. Single-intent rooms still auto-advance.
	ExecStaged
)

// staged reports whether the run-level mode is staged.
func (m ExecutionMode) staged() bool { return m == ExecStaged }

// String renders the mode for flags/trace.
func (m ExecutionMode) String() string {
	if m == ExecStaged {
		return "staged"
	}
	return "one-shot"
}

// Option is a functional option for Orchestrator.
type Option func(*Orchestrator)

// WithExecutionMode sets the run's execution mode (one-shot vs staged).
func WithExecutionMode(mode ExecutionMode) Option {
	return func(o *Orchestrator) { o.execMode = mode }
}

// WithReloader injects the closure Reload uses to fetch the fresh app
// definition, overriding the default app.Load(appPath). A config-synthesized
// root passes `func() { cfg, _ := webconfig.Load(...); return
// app.SynthesizeRoot(cfg.Root.RootSpec(), repoRoot) }` so a /reload after a
// rung-1 `.kitsoki.yaml` edit re-synthesizes the root; a file-backed root may
// pass `func() { return app.Load(appPath) }` (or leave it unset for the
// historical path-based behaviour). Nil is the default and is safe — Reload
// then re-reads appPath exactly as before. See Reload.
func WithReloader(fn func() (*app.AppDef, error)) Option {
	return func(o *Orchestrator) { o.reloader = fn }
}

// WithMiner injects the ambient session miner (*mining.Miner in production)
// plus the repo path it resolves transcripts for. NewSession Starts it; a
// finished turn / landed background job pings Notify. nil (the default) disables
// ambient mining entirely — the path every flow/test fixture takes, so no
// fixture ever spends LLM. repoPath empty ⇒ the miner resolves against the
// process working directory at Start time.
func WithMiner(m SessionMiner, repoPath string) Option {
	return func(o *Orchestrator) {
		o.miner = m
		o.minerRepoPath = repoPath
	}
}

// WithPromptOverlay sets a project prompt-overlay directory for this run,
// overriding any overlay declared in the app's prompts: block. The overlay's
// prompt files shadow the story's (resolved overlay-first) and may
// {% extends "@story/…" %} the base they shadow — letting a project specialize
// a story's prompts without forking it. Empty is a no-op. See
// docs/stories/prompts.md.
func WithPromptOverlay(dir string) Option {
	return func(o *Orchestrator) { o.promptOverlay = dir }
}

// WithAgentBackendName selects the coding-agent CLI backend ("claude" default,
// or "copilot") for every host.agent.* call. An empty/"claude" name keeps the
// default; an unrecognized name degrades safely to claude.
func WithAgentBackendName(name string) Option {
	return func(o *Orchestrator) { o.agentBackendName = name }
}

// WithLogger sets the logger used for structured tracing.
func WithLogger(l *slog.Logger) Option {
	return func(o *Orchestrator) {
		if l != nil {
			o.logger = l
		}
	}
}

// WithHostRegistry enables dispatch of machine HostCalls. When unset, host
// invocations collected by the machine are ignored (the event log still records
// HostInvoked but no side-effect fires). Enable this for live sessions;
// deterministic flow tests typically leave it off.
func WithHostRegistry(r *host.Registry) Option {
	return func(o *Orchestrator) {
		o.hosts = r
	}
}

// WithTransportRegistry installs a transport.Registry that is injected into
// the dispatch context so the host.transport.post bridge handler can find
// it. When unset, `host.transport.post` invocations error with "no
// transport registry installed" and route via on_error: as configured.
func WithTransportRegistry(r *transport.Registry) Option {
	return func(o *Orchestrator) {
		o.transports = r
	}
}

// WithScheduler wires a background-job scheduler into the orchestrator.
// When set, effects with background: true are submitted to the scheduler
// instead of being dispatched synchronously. When nil (the default),
// background effects are dispatched synchronously and the Background flag
// is silently ignored.
func WithScheduler(s jobs.Scheduler) Option {
	return func(o *Orchestrator) {
		o.scheduler = s
	}
}

// WithJobStore wires a *jobs.JobStore so the orchestrator can load job rows
// for on_complete processing and post notifications on job termination.
func WithJobStore(js *jobs.JobStore) Option {
	return func(o *Orchestrator) {
		o.jobStore = js
	}
}

// WithChatStore wires a host.ChatStore so that chat-aware agent calls and the
// host.chat.* built-in handlers have access to the persistent chat transcript.
// When nil (the default), chat persistence is silently disabled and handlers
// that require a store return Result{Error: "…no chat store wired"}.
func WithChatStore(cs host.ChatStore) Option {
	return func(o *Orchestrator) {
		o.chatStore = cs
	}
}

// WithChatsConcrete wires a concrete *chats.Store for continue-mode resume
// reads (drives + backgrounded PTY chats). Optional; when nil, AttachSession
// returns a bundle without PendingDrives or BackgroundedChats populated.
// Distinct from WithChatStore — the host-interface flavour serves host
// handlers, this serves the resume read path.
func WithChatsConcrete(cs *chats.Store) Option {
	return func(o *Orchestrator) {
		o.chatsConcrete = cs
	}
}

// WithJournalWriter wires a journal.Writer for durable session journalling
// (continue-mode dual-write). When nil (the default), turn writes fall through
// to the legacy AppendEvents path. Individual call sites are migrated by the
// next wave agent; this option only stores the writer for later use.
func WithJournalWriter(w journal.Writer) Option {
	return func(o *Orchestrator) {
		o.journalWriter = w
	}
}

// WithClock injects a clock.Clock used by the timeout dispatcher.  Defaults
// to clock.Real() when not supplied.  Pass a *clock.Fake in tests to drive
// Timeout: firings deterministically alongside background-job stubs.
func WithClock(c clock.Clock) Option {
	return func(o *Orchestrator) {
		if c != nil {
			o.clk = c
		}
	}
}

// WithAgentRegistry wires an agent.Registry into the orchestrator so the
// dispatch context carries agent plugin resolution. When nil (the default),
// agent handlers fall through to their existing direct claude-CLI logic.
// For B-2/B-7: pass a registry built from the app's hosts: declarations.
func WithAgentRegistry(reg *agent.Registry) Option {
	return func(o *Orchestrator) {
		o.agentRegistry = reg
	}
}

// WithSemanticRouting sets the process-level override for the deterministic
// semantic-routing stack. Passing false makes every free-text turn route via
// the main model as an isolated decision (harness.RunTurn), skipping semroute,
// the turn-cache, the default_intent sink, and the app free-form fallback; the
// zero-cost exact display/example match still runs. Passing true forces the
// full stack on regardless of per-app config. Not calling this option at all
// leaves routing deferred to the per-app routing.enabled config (the posture
// used by tests and the flow runner). The CLI wires this from
// --semantic-routing / KITSOKI_SEMANTIC_ROUTING, defaulting to false.
func WithSemanticRouting(enabled bool) Option {
	return func(o *Orchestrator) { o.semanticOverride = &enabled }
}

// WithEmbedTier injects a pre-constructed EmbedTier for the embedding routing
// tier. If nil or tier.cfg.Enabled is false, TrySemantic skips the embed hop.
func WithEmbedTier(t *EmbedTier) Option {
	return func(o *Orchestrator) { o.embedTier = t }
}

// WithEventSink wires a store.EventSink that receives every event appended
// during a turn.  When set, appendEventsAndJournal routes writes to this sink
// instead of constructing a StoreSinkAdapter over the SQLite store; loadJourney
// reads history from sink.History() instead of o.store.LoadHistory.
//
// Wave 3-entry: used by kitsoki turn --trace and (for new sessions) by
// session continue / the TUI, where the sink is a *store.JSONLSink backed by
// the default JSONL path.  The SQLite store (o.store) may still be non-nil for
// subcommands that need it for session metadata (external_keys, locks, etc.);
// only the event-write and event-read paths are redirected.
// WithEventSink wires a store.EventSink that receives every event appended
// during a turn.  When set, events are written to the sink in addition to
// the SQLite store (dual-write).  loadJourney still reads from SQLite so
// existing subcommands (session show, attach-session) keep working.
//
// To make the JSONL sink the sole authority for loadJourney (pure-JSONL mode,
// used by kitsoki turn --trace), pass WithEventSinkAuthority(true) as well.
func WithEventSink(s store.EventSink) Option {
	return func(o *Orchestrator) {
		o.eventSink = s
	}
}

// WithEventSinkAuthority, when true, instructs loadJourney to read history
// from the eventSink rather than from SQLite.  Set this for the pure-JSONL
// path (kitsoki turn --trace) where the JSONL file is the sole trace and the
// in-memory store has no prior events.  Leave false (the default) for the
// dual-write path (session continue, TUI) where SQLite is still the read source.
func WithEventSinkAuthority(auth bool) Option {
	return func(o *Orchestrator) {
		o.sinkIsAuthority = auth
	}
}

// SetEventSink installs an EventSink after the orchestrator has been
// constructed.  Safe to call before the first turn; not safe to call
// concurrently with a running turn.  Used by the TUI run path where the
// session ID (needed to compute the default trace path) is not known until
// after orchestrator construction.
func (o *Orchestrator) SetEventSink(s store.EventSink) {
	o.eventSink = s
}

// NewSession opens a session in the store and returns its ID.
// If a background-job scheduler is configured, it also spawns a per-session
// listener goroutine that forwards terminal JobEvents to handleJobTerminal.
func (o *Orchestrator) NewSession(ctx context.Context) (app.SessionID, error) {
	sid, err := o.store.CreateSession(ctx, o.def)
	if err != nil {
		return "", err
	}
	if o.scheduler != nil {
		o.startSessionListener(sid)
	}
	if o.miner != nil {
		repoPath := o.minerRepoPath
		if repoPath == "" {
			if wd, wdErr := os.Getwd(); wdErr == nil {
				repoPath = wd
			}
		}
		// Start fires the first-launch history seed (iff the slug is unmined) as a
		// detached background job — never blocks NewSession. A resolver/infra
		// failure is logged, not fatal: a session without ambient mining is still
		// a working session.
		if startErr := o.miner.Start(ctx, sid, repoPath); startErr != nil {
			o.logger.Warn("orchestrator: ambient miner start failed",
				slog.String("session_id", string(sid)),
				slog.String("err", startErr.Error()),
			)
		}
	}
	return sid, nil
}

// RunInitialOnEnter dispatches the initial state's on_enter chain for a
// freshly-created session, so on-enter-bound world keys (e.g.
// `iface.ticket.list_mine`'s ticket queue on dev-story's main view) are
// populated before the first frame renders. Machine.Turn already fires
// on_enter when a transition LANDS in a new state, but the initial
// state is not arrived at via a transition — without this method any
// app whose root room has an on_enter chain renders the first frame
// against the default (empty) world, and the user sees a blank list
// until they navigate away and back.
//
// Safe to call multiple times — but only the first call within a fresh
// session is meaningful; subsequent transitions own their own on_enter
// dispatch. No-ops when the initial state declares no on_enter
// effects, when the orchestrator has no host registry, when the
// session has already taken at least one turn (guarded by checking
// journey.Turn), or when the initial state can't be looked up.
//
// Errors only on infrastructure failure (store / host registry).
// Host call failures route through the state's on_error: arc and are
// surfaced via world.last_error — same as a normal transition.
func (o *Orchestrator) RunInitialOnEnter(ctx context.Context, sid app.SessionID) error {
	sessMu := o.sessionLock(sid)
	sessMu.Lock()
	defer sessMu.Unlock()

	journey, err := o.loadJourney(sid)
	if err != nil {
		return fmt.Errorf("orchestrator: RunInitialOnEnter: load journey: %w", err)
	}
	// Only fire on a fresh session; subsequent on_enter chains are owned
	// by Machine.Turn after their respective transitions land.
	if journey.Turn > 0 {
		return nil
	}
	// Fire the on_enter chain for EVERY state entered at boot — the
	// ancestor compounds AND the leaf, in root→leaf order — not just the
	// leaf. The initial state is reached by resolving the root compound's
	// `initial:` chain, not by a transition, so nothing here walks the
	// compound ancestors the way Machine.Turn's entered-path loop does.
	// That matters because import-folding parks an import's `world_in:`
	// setters on the import wrapper's compound on_enter (see app/imports.go
	// §6). For a NON-root import a real entry transition fires them; for the
	// ROOT import (`root: core`) there is no such transition, so an instance
	// that retargets a profile key via the root import's `world_in:` got
	// none of them — the child kept its own defaults. Walking the prefix
	// chain here mirrors stateEnterPaths("", journey.State): the same
	// root→leaf on_enter sequence a real entry transition would fire.
	p := string(journey.State)
	if idx := strings.Index(p, "#"); idx >= 0 {
		p = p[:idx]
	}
	var onEnter []app.Effect
	parts := strings.Split(p, ".")
	for i := range parts {
		if anc := lookupStateByPath(o.def, app.StatePath(strings.Join(parts[:i+1], "."))); anc != nil {
			onEnter = append(onEnter, anc.OnEnter...)
		}
	}
	if len(onEnter) == 0 {
		return nil
	}

	resolved, newWorld, hostCalls, _, effEvents, runErr := o.machine.RunEffectsAndState(ctx, journey.State, journey.World, onEnter)
	if runErr != nil {
		return fmt.Errorf("orchestrator: RunInitialOnEnter: run on_enter for %q: %w", journey.State, runErr)
	}

	events := effEvents
	if len(hostCalls) > 0 {
		hostEvents, hostWorld, _, hostRedirect, hostErr := o.dispatchHostCalls(ctx, sid, hostCalls, newWorld, resolved)
		if hostErr != nil {
			return fmt.Errorf("orchestrator: RunInitialOnEnter: dispatch host calls: %w", hostErr)
		}
		events = append(events, hostEvents...)
		newWorld = hostWorld
		if hostRedirect != "" {
			resolved = hostRedirect
		}
	}

	// Follow any boot emit_intent whose value / `when:` depends on a world
	// key the on_enter host calls just bound — e.g. git-ops idle's
	// `emit_intent: "{{ world.route }}"`, where the detect_context host call
	// binds world.route. RunEffectsAndState above ran with the pre-host-call
	// world, so that emit could not yet resolve; without settling it here the
	// session sits at the router room despite its on_enter promising an
	// automatic route ("routed to hub automatically"). The normal turn path
	// runs this same settle after its host calls (see settlePostBindEmits);
	// mirroring it here lets a fresh interactive session land on the routed
	// hub at boot with no user turn. settlePostBindEmits is a no-op (empty
	// res.Events, state unchanged) for the common case of a story with no
	// boot emit_intent.
	res := &machine.TurnResult{NewState: resolved, World: newWorld}
	if emitErr := o.settlePostBindEmits(ctx, sid, res, nil, 0); emitErr != "" {
		// settlePostBindEmits already recorded a HarnessError into res.Events
		// and left res at the known pre-emit resting place. Persist what we
		// have so the session lands on a real state rather than half-bound
		// limbo.
		slog.Warn("orchestrator.run_initial_on_enter.settle_emits_failed",
			"session", string(sid), "state", string(resolved), "err", emitErr)
	}
	events = append(events, res.Events...)
	newWorld = res.World
	resolved = res.NewState

	// No events to persist? Nothing to do.
	if len(events) == 0 {
		return nil
	}

	// Persist as a synthetic turn-0 event slice. Stamp turn=0 so the
	// journal replay path sees these as part of session initialisation,
	// not as a real user turn.
	for i := range events {
		events[i].Turn = 0
	}
	// Stamp state_path so the synthetic turn-0 init events record the active
	// state (matches the turn paths). finding 2.1.
	stampStatePathPerEvent(events)
	stampStatePath(events, journey.State, o.InitialState())
	jEntries := journalEntriesForEvents(sid, 0, time.Now(), events, journey.World, newWorld, "", resolved, "")
	if appendErr := o.appendEventsAndJournal(sid, events, jEntries); appendErr != nil {
		return fmt.Errorf("orchestrator: RunInitialOnEnter: append events: %w", appendErr)
	}
	return nil
}

// startSessionListener subscribes to terminal job events for sid and routes
// them to handleJobTerminal in a background goroutine. The goroutine exits
// when the cancel func stored in cancelListeners is called.
func (o *Orchestrator) startSessionListener(sid app.SessionID) {
	// The listener must outlive the call that started it: NewSession's request
	// context is cancelled as soon as NewSession returns, and
	// EnsureSessionListener has no context at all. There is no orchestrator- or
	// session-scoped lifetime context to derive from, so we root the listener at
	// context.Background() and govern its lifetime explicitly via the cancel func
	// stored in cancelListeners (cancelled by stopSessionListener on session
	// close). Deriving from a request context here would orphan the cancel path
	// AND prematurely tear the listener down. See stopSessionListener.
	listenerCtx, cancel := context.WithCancel(context.Background())
	o.mu.Lock()
	o.cancelListeners[sid] = cancel
	o.mu.Unlock()
	o.logger.Debug("orchestrator: started session listener",
		slog.String("session_id", string(sid)),
	)

	ch, ack, unsub := o.scheduler.SubscribeSession(sid)
	go func() {
		defer unsub()
		for {
			select {
			case <-listenerCtx.Done():
				return
			case ev, ok := <-ch:
				if !ok {
					return
				}
				// Process the event, then ack via the scheduler so
				// WaitSessionDrained can detect quiescence.  ack is called in a
				// defer so it fires even if a handler panics or listenerCtx
				// is cancelled mid-dispatch.
				func() {
					defer ack()

					switch ev.Status {
					case jobs.JobDone, jobs.JobFailed, jobs.JobCancelled:
						if err := o.handleJobTerminal(listenerCtx, sid, ev); err != nil {
							o.logger.ErrorContext(listenerCtx, trace.EvJobError,
								slog.String("session_id", string(sid)),
								slog.String("job_id", ev.JobID),
								slog.String("phase", "handle_terminal"),
								slog.String("err", err.Error()),
							)
						}
					case jobs.JobAwaitingInput:
						if err := o.handleJobAwaitingInput(listenerCtx, sid, ev); err != nil {
							o.logger.ErrorContext(listenerCtx, trace.EvJobError,
								slog.String("session_id", string(sid)),
								slog.String("job_id", ev.JobID),
								slog.String("phase", "handle_awaiting_input"),
								slog.String("err", err.Error()),
							)
						}
					}
				}()
			}
		}
	}()
}

// WaitListenerIdle blocks until the session listener has finished processing
// all events the scheduler has fanned out for sid.  Returns ctx.Err() if the
// context is cancelled first.
//
// Implemented as a thin wrapper over Scheduler.WaitSessionDrained: the
// scheduler tracks per-subscription pending counts (incremented before each
// channel send, decremented when the consumer's ack callback fires after
// processing), which closes the receive→process race window that a
// listener-side counter alone cannot.
//
// The typical call sequence in a test is:
//
//	sched.WaitIdle(ctx)            // scheduler-side: jobs all terminal/awaiting
//	orch.WaitListenerIdle(ctx, sid) // consumer-side: events all processed
func (o *Orchestrator) WaitListenerIdle(ctx context.Context, sid app.SessionID) error {
	if o.scheduler == nil {
		return nil
	}
	return o.scheduler.WaitSessionDrained(ctx, sid)
}

// EnsureSessionListener spawns the per-session terminal-event listener for
// sid if one is not already running.  NewSession does this automatically for
// fresh sessions, but CLI entry points that resolve an existing session by
// (transport, thread) key — `kitsoki session continue` — never call
// NewSession and therefore must wire the listener themselves before any
// background dispatch happens.  No-op when the orchestrator was built
// without a scheduler.
func (o *Orchestrator) EnsureSessionListener(sid app.SessionID) {
	if o.scheduler == nil {
		return
	}
	o.mu.Lock()
	_, already := o.cancelListeners[sid]
	o.mu.Unlock()
	if already {
		return
	}
	o.startSessionListener(sid)
}

// stopSessionListener cancels the listener goroutine for sid (if any) and
// reclaims the per-session lock map entry so long-running orchestrators do
// not accumulate stale entries.
func (o *Orchestrator) stopSessionListener(sid app.SessionID) {
	o.mu.Lock()
	cancel, ok := o.cancelListeners[sid]
	if ok {
		delete(o.cancelListeners, sid)
	}
	delete(o.sessionLocks, sid)
	o.mu.Unlock()
	if ok {
		cancel()
	}
	// Drop any pending Timeout entries: a terminal session should not
	// have a stale timer hanging around firing into a dead session.
	if o.timeouts != nil {
		o.timeouts.cancelAll(sid)
	}
}

// sessionLock returns the per-session mutex, lazily creating it on first use.
// See the comment on Orchestrator.sessionLocks for why this lock exists.
//
// Callers MUST hold the returned mutex from before loadJourney through to
// AppendEvents in any path that writes new events; otherwise the read-then-
// write of (journey.Turn → AppendEvents at turn N) is racy across the
// foreground Turn path and the background handleJobTerminal path.
func (o *Orchestrator) sessionLock(sid app.SessionID) *sync.Mutex {
	o.mu.Lock()
	defer o.mu.Unlock()
	mu, ok := o.sessionLocks[sid]
	if !ok {
		mu = &sync.Mutex{}
		o.sessionLocks[sid] = mu
	}
	return mu
}

// TurnOption configures a Turn call.
type TurnOption func(*turnConfig)

type turnConfig struct {
	// supplementSlots are merged into the intent call returned by the
	// harness, before machine.Turn runs.  Slot keys already populated by
	// the harness are NOT overwritten — this is for orchestrator-injected
	// metadata (e.g. last_reply_author) that the LLM routing would not
	// know about.
	supplementSlots world.Slots
}

// WithSupplementSlots returns a TurnOption that injects per-turn slot
// metadata alongside the harness's resolved intent.  Useful for
// orchestrators that classify the human reply via the LLM (so they
// can't pre-populate the intent's slots) but still need to attach
// known metadata such as the comment author for the ACL guard.
func WithSupplementSlots(slots world.Slots) TurnOption {
	return func(c *turnConfig) { c.supplementSlots = slots }
}

// Turn processes one user utterance and returns a TurnOutcome.
// Steps:
//  1. Load journey (state + world) from the store.
//  2. Call harness.RunTurn → mcp.CallToolParams.
//  3. Parse the intent call from the params.
//  4. Call machine.Turn.
//  5. React to the result: persist events and build the outcome.
func (o *Orchestrator) Turn(ctx context.Context, sid app.SessionID, input string, opts ...TurnOption) (*TurnOutcome, error) {
	var cfg turnConfig
	for _, opt := range opts {
		opt(&cfg)
	}

	// Semantic routing tier (see docs/architecture/semantic-routing.md):
	// run BEFORE acquiring the session lock so TrySemantic's own
	// SubmitDirect call can take the lock without deadlocking. We
	// skip the semantic tier when the caller passed supplemental
	// slots — that path explicitly wants the LLM to classify the
	// utterance so the supplements can be merged into a properly
	// typed call. Routing-disabled apps short-circuit inside
	// TrySemantic.
	// Routing tiers run BEFORE acquiring the session lock so their own
	// SubmitDirect calls can take the lock without deadlocking. We skip them
	// when the caller passed supplemental slots — that path explicitly wants the
	// LLM to classify the utterance so the supplements merge into a typed call.
	//
	// When the deterministic semantic-routing stack is disabled (the LLM-only
	// default; see semanticStackEnabled) only the zero-cost exact match (display
	// strings / unique examples) survives so typed menu labels and canonical
	// command text still resolve without an LLM hop; everything else falls
	// straight through to the main-model interpreter (harness.RunTurn) as an
	// isolated routing decision. The TUI already runs MatchDeterministic before
	// Turn, so for it this is a cheap no-op; other surfaces (kitsoki turn, MCP
	// drive/submit) gain the same fast path here. Set --semantic-routing /
	// KITSOKI_SEMANTIC_ROUTING (or per-app routing.enabled when no override is
	// wired) to develop/test the full stack. See
	// docs/architecture/semantic-routing.md.
	if len(cfg.supplementSlots) == 0 && !o.semanticStackEnabled() {
		if outcome, hit, detErr := o.TryDeterministic(ctx, sid, input); detErr != nil {
			return nil, detErr
		} else if hit {
			return outcome, nil
		}
	}

	// Full deterministic semantic-routing stack: exact match (via the semroute
	// synonym index), semroute semantic, turn-cache, default_intent sink, and
	// the app free-form fallback. Only runs when the stack is enabled.
	if len(cfg.supplementSlots) == 0 && o.semanticStackEnabled() {
		if outcome, hit, semErr := o.TrySemantic(ctx, sid, input); semErr != nil {
			return nil, semErr
		} else if hit {
			return outcome, nil
		}
		// Turn-cache tier: after
		// semroute misses and before the LLM, check whether this
		// (state, signature) was resolved on a prior turn. On a
		// successful re-Validate the cache short-circuits the LLM
		// call; on a Validate failure the strike count increments
		// and we fall through.
		if outcome, hit, cacheErr := o.tryTurnCache(ctx, sid, input); cacheErr != nil {
			return nil, cacheErr
		} else if hit {
			return outcome, nil
		}
		// Default free-text tier: after all match-based tiers miss, if the
		// state declares a default_intent, sink the whole utterance into it
		// deterministically (one required string slot) instead of letting the
		// main-turn LLM classify — which can mis-pick a near-miss command. Only
		// fires when default_intent is declared; otherwise falls through.
		if outcome, hit, defErr := o.routeViaDefaultIntent(ctx, sid, input); defErr != nil {
			return nil, defErr
		} else if hit {
			return outcome, nil
		}
		// App-level free-form fallback: after command-like tiers and any
		// room-local default sink miss, send unmatched prose from strict/menu
		// rooms to the configured work-intake intent before the main LLM can
		// guess a generic navigation intent.
		if outcome, hit, fallbackErr := o.routeViaFreeFormFallback(ctx, sid, input); fallbackErr != nil {
			return nil, fallbackErr
		} else if hit {
			return outcome, nil
		}
	}

	// Serialise foreground turn against any concurrent handleJobTerminal for
	// this session: both compute turnNum = journey.Turn + 1 from the event
	// log, so without this lock they can both pick the same N and collide on
	// the events PK.  Per-session — turns for other sessions still run in
	// parallel.
	sessMu := o.sessionLock(sid)
	sessMu.Lock()
	defer sessMu.Unlock()

	// 1. Reconstruct the journey from the event log.
	journey, err := o.loadJourney(sid)
	if err != nil {
		return nil, fmt.Errorf("orchestrator: load journey: %w", err)
	}

	// 2. Build TurnInput for the harness.
	allowedIntents := o.machine.AllowedIntents(journey.State, journey.World)
	allowedNames := make([]string, len(allowedIntents))
	for i, ai := range allowedIntents {
		allowedNames[i] = ai.Name
	}

	turnNum := journey.Turn + 1
	tl := trace.NewTurnLogger(o.logger, sid, turnNum, journey.State)

	// Emit turn.start.
	tl.Debug(ctx, trace.EvTurnStart,
		slog.String("input", input),
		slog.String("mode", "normal"),
	)

	// Build RecentTurns from the event history. The store call here is a
	// second pass over the same rows loadJourney already read, but the slice
	// isn't carried on JourneyState today and snapshotting through the
	// journey type would be a bigger refactor. Bounded to RecentTurnsLimit
	// so prompt size stays predictable.
	//
	// On error: log and pass nil. RecentTurns is purely advisory — a missing
	// history must not abort the turn.
	history, histErr := o.store.LoadHistory(sid)
	if histErr != nil {
		tl.Debug(ctx, trace.EvTurnStart,
			slog.String("recent_turns_load_error", histErr.Error()),
		)
		history = nil
	}
	recent := extractRecentTurns(history)

	in := harness.TurnInput{
		SessionID:      app.SessionID(sid),
		TurnNumber:     turnNum,
		UserText:       input,
		StatePath:      journey.State,
		World:          journey.World,
		AllowedIntents: allowedNames,
		RecentTurns:    recent,
	}

	// UserInputReceived is emitted at the moment input arrives, with the turn
	// number it belongs to (same as the TurnStarted that follows). This replaces
	// the exporter-side synthesised turn.input row.
	// G6: unified payload {"input": <text>, "intent": <name>} on both the Turn
	// (chat-text) and SubmitDirect paths. On the Turn path intent is "" (the
	// harness has not yet resolved the intent); on SubmitDirect both are populated.
	// SPA consumers can ignore intent when empty.
	var inputEvent store.Event
	if input != "" {
		inputPayload, _ := json.Marshal(map[string]any{"input": input, "intent": ""})
		inputEvent = store.Event{
			Kind:      store.UserInputReceived,
			Turn:      turnNum,
			Ts:        time.Now(),
			StatePath: journey.State,
			Payload:   inputPayload,
		}
	}

	// TurnStarted payload. Built here, but the routing provenance is stamped
	// only AFTER the harness resolves (below), once we know this turn truly
	// reached the paid main-turn interpreter. The deterministic / semantic /
	// turn-cache / default / fallback tiers all stamp `routed_by` via
	// RouteProvenance before they ever reach this code; the main-turn path is
	// the LAST tier, so without stamping it here a turn that fell through every
	// earlier tier would persist a TurnStarted with NO routed_by at all — an
	// unattributable "which tier handled this?" hole in the trace (the bug that
	// produced the empty `{intent:""}` rows). The event is materialised at the
	// `prefix` build site once provenance is known; the early-return paths above
	// (nil harness, clarify, harness error) never persist it.
	startPayload := map[string]any{
		"turn":  int64(turnNum),
		"input": input,
	}

	// 3. Call harness.
	//
	// A free-text turn requires an interpreter (the harness) to classify the
	// utterance into an intent. In a no-harness posture — e.g. the deterministic
	// `kitsoki web --flow` UI, where the operator drives by submitting explicit
	// intents (SubmitDirect) — there is no interpreter, so a free-text turn is
	// unsupported. Surface that as a clean error instead of dereferencing a nil
	// harness and panicking the RPC handler.
	if o.harness == nil {
		return nil, fmt.Errorf("free-text turn requires an interpreter harness; none is configured (submit an explicit intent instead)")
	}
	harnessStart := time.Now()
	params, err := o.harness.RunTurn(ctx, in)
	harnessDur := time.Since(harnessStart)
	if err != nil {
		// LLM answered but didn't call the expected tool — surface its
		// free-form text as a soft clarification rather than bubbling a
		// red technical error. The TUI's renderRejection picks the
		// LLM_CLARIFICATION code up and renders via AppendClarification.
		var clarify *harness.ClarifyResponse
		if errors.As(err, &clarify) {
			tl.Debug(ctx, trace.EvTurnRouted,
				slog.Duration("dur", harnessDur),
				slog.String("outcome", "clarify"),
				slog.String("error", err.Error()),
			)
			// A ClarifyResponse is the LLM/router saying "I couldn't map this
			// free text to any allowed intent" — a genuine no-match. For a room
			// that opted into the agent off-ramp, intercept it here and hand the
			// user's ORIGINAL free text to the off-ramp converse turn instead of
			// returning the soft clarify. For every other (non-opted-in) room the
			// behavior below is byte-identical to before: maybeOffRamp is scoped
			// to off-ramp rooms via the State.AgentOffRamp gate, so it returns
			// (nil, false) in the common case (see offpath.go's isNoMatchCode).
			if outcome, ok := o.maybeOffRamp(ctx, sid, journey.State, input,
				codeLLMClarification, 0, allowedNames, turnNum); ok {
				return outcome, nil
			}
			msg := strings.TrimSpace(clarify.Message)
			if msg == "" {
				msg = "The router didn't understand. Try rephrasing or pick an action from the menu."
			}
			return &TurnOutcome{
				Mode:         ModeRejected,
				NewState:     journey.State,
				ErrorCode:    codeLLMClarification,
				ErrorMessage: msg,
			}, nil
		}
		tl.Debug(ctx, trace.EvTurnRouted,
			slog.Duration("dur", harnessDur),
			slog.String("outcome", "error"),
			slog.String("error", err.Error()),
		)
		return nil, fmt.Errorf("orchestrator: harness.RunTurn: %w", err)
	}
	tl.Debug(ctx, trace.EvTurnRouted,
		slog.Duration("dur", harnessDur),
		slog.String("outcome", "hit"),
		slog.String("intent", extractIntentName(params)),
	)
	// Routing breadcrumb: emit a stable
	// `turn.llm_routed` breadcrumb so the future turncache writeback
	// (and the TUI route badges) have a deterministic place to
	// observe LLM-resolved turns. The field schema is locked (see
	// docs/tracing/trace-format.md) — don't rename
	// intent/confidence/state_path/model. The model
	// name is empty in Phase 2: the harness owns its model choice
	// and a future hook will plumb the resolved model up here.
	tl.Debug(ctx, trace.EvTurnLLMRouted,
		slog.String("intent", extractIntentName(params)),
		slog.Float64("confidence", harnessConfidence(params)),
		slog.String("model", ""),
	)

	// Append the LLMToolCall event recording the harness's resolved tool call.
	llmEvent := newOrchestratorEvent(store.LLMToolCall, map[string]any{
		"tool":   params.Name,
		"intent": extractIntentName(params),
	}, turnNum)

	// 4. Parse the intent call from params.
	call, parseErr := parseIntentCall(params)
	if parseErr != nil {
		return nil, fmt.Errorf("orchestrator: parse intent call: %w", parseErr)
	}

	// 4b. Merge supplemental slots (orchestrator-provided metadata).
	// Existing slot keys from the harness win — supplemental slots only
	// fill gaps so the harness's classification is authoritative.
	if len(cfg.supplementSlots) > 0 {
		if call.Slots == nil {
			call.Slots = world.Slots{}
		}
		for k, v := range cfg.supplementSlots {
			if _, exists := call.Slots[k]; !exists {
				call.Slots[k] = v
			}
		}
	}

	// 5. Run the machine.
	result, machineErr := o.machine.Turn(ctx, journey.State, journey.World, call)
	if machineErr != nil {
		o.journalTurnError(ctx, tl, sid, turnNum, journey.State, call, journey.World, machineErr)
		return nil, fmt.Errorf("orchestrator: machine.Turn: %w", machineErr)
	}

	// Trace machine step.
	tl.Debug(ctx, trace.EvTurnStepped,
		slog.String("intent", call.Intent),
		slog.Any("slots", slotsToMap(call.Slots)),
	)

	// Stamp the turn number onto all machine events.
	for i := range result.Events {
		result.Events[i].Turn = turnNum
	}

	// This turn reached the main-turn interpreter — every earlier
	// deterministic/semantic/turn-cache/default/fallback tier missed. Stamp
	// that on TurnStarted so the persisted trace ALWAYS records the resolving
	// tier (routed_by:"llm" — the one paid tier), with the interpreter seam as
	// match_type and the harness's self-reported confidence. This is the
	// guarantee that no free-text turn lands in the trace without an
	// attributable route (see the startPayload comment above).
	llmProv := RouteProvenance{
		Source:     "llm",
		MatchType:  "main-turn",
		Confidence: harnessConfidence(params),
	}
	emitRoutingStream(ctx, turnNum, extractIntentName(params), llmProv)
	llmProv.stampOn(startPayload)
	startEvent := newOrchestratorEvent(store.TurnStarted, startPayload, turnNum)

	// Build a prefix of orchestrator-level events.
	prefix := []store.Event{startEvent, llmEvent}

	// 6. React to the result.
	if result.ValidationError != nil {
		ve := result.ValidationError
		switch ve.Code {
		case intent.ErrMissingSlots:
			// Do NOT persist events for clarify-required outcomes.
			// Store the pending intent in memory.
			slotsSoFar := slotsToMap(call.Slots)
			o.mu.Lock()
			o.pending[sid] = &pendingClarify{
				intentName: call.Intent,
				slots:      slotsSoFar,
			}
			o.mu.Unlock()

			tl.Debug(ctx, trace.EvTurnDone,
				slog.String("mode", "clarify"),
				slog.String("pending_intent", call.Intent),
			)

			missingSlots := ve.MissingSlots
			clarification := ComputeClarification(o.def, journey.State, call.Intent, missingSlots)
			tl.Debug(ctx, trace.EvSlotFillRequested,
				slog.String("intent", call.Intent),
				slog.Int("missing_count", len(missingSlots)),
				slog.Any("missing", missingSlots),
				slog.String("origin", "turn"),
			)
			// Site 8 (Turn path): emit clarify.requested via standalone journal
			// write — no events row to pair with on this path.
			slotsNeededNames := make([]string, len(missingSlots))
			copy(slotsNeededNames, missingSlots)
			o.appendJournal(journalEntry(sid, turnNum, 0, time.Now(),
				journal.KindClarifyRequested, "",
				map[string]any{
					"origin":       "foreground",
					"intent":       call.Intent,
					"slots_so_far": slotsSoFar,
					"slots_needed": slotsNeededNames,
				}))
			return &TurnOutcome{
				Mode:           ModeClarify,
				NewState:       journey.State,
				PendingIntent:  call.Intent,
				PendingSlots:   slotsSoFar,
				SlotsNeeded:    clarification.Slots,
				AllowedIntents: allowedNames,
				TurnNumber:     turnNum,
			}, nil

		default:
			// Agent off-ramp: on a genuine no-match in a room that declared
			// agent_off_ramp, hand the original free text to a converse turn
			// instead of persisting a rejection (Task 1.3/1.4). Inert for every
			// other code flowing through here (GUARD_FAILED, INVALID_SLOT_VALUE,
			// INTENT_NOT_ALLOWED_IN_STATE, …) and when the room has no off-ramp.
			if outcome, ok := o.maybeOffRamp(ctx, sid, journey.State, input, ve.Code, harnessConfidence(params), allowedNames, turnNum); ok {
				return outcome, nil
			}

			// INTENT_NOT_ALLOWED, GUARD_FAILED, etc.: persist the failure events.
			failureEvents := append(prefix, result.Events...)
			endEvent := newOrchestratorEvent(store.TurnEnded, map[string]any{
				"outcome": "rejected",
				"code":    string(ve.Code),
			}, turnNum)
			failureEvents = append(failureEvents, endEvent)
			if inputEvent.Kind != "" {
				failureEvents = append([]store.Event{inputEvent}, failureEvents...)
			}
			// G5: pre-stamp StateEntered/StateExited with their per-event state.
			stampStatePathPerEvent(failureEvents)
			// Finding 2.1: fall back to InitialState when journey.State is "" (e.g. new
			// session whose AppDef.Root didn't parse) so every event has non-empty state_path.
			stampStatePath(failureEvents, journey.State, o.InitialState())

			// Site 1: dual-write journal entries for the rejection turn.
			jEntries := journalEntriesForEvents(sid, turnNum, time.Now(), failureEvents,
				journey.World, journey.World, "", journey.State, input)
			if appendErr := o.appendEventsAndJournal(sid, failureEvents, jEntries); appendErr != nil {
				return nil, fmt.Errorf("orchestrator: append failure events: %w", appendErr)
			}

			tl.Debug(ctx, trace.EvTurnPersisted,
				slog.Int("count", len(failureEvents)),
				slog.String("outcome", "rejected"),
			)
			tl.Debug(ctx, trace.EvTurnDone,
				slog.String("mode", "rejected"),
				slog.String("error_code", string(ve.Code)),
			)

			return &TurnOutcome{
				Mode:           ModeRejected,
				NewState:       journey.State,
				Events:         failureEvents,
				AllowedIntents: allowedNames,
				GuardHint:      ve.GuardHint,
				ErrorCode:      ve.Code,
				ErrorMessage:   ve.Message,
				TurnNumber:     turnNum,
			}, nil
		}
	}

	// Pre-dispatch room-entry hook: when the machine landed us in a new
	// state, fire the RoomEnterSink BEFORE the on_enter chain's host
	// calls so a live TUI can paint the new room's banner above the
	// tool-call breadcrumbs about to stream in.
	//
	// The trigger is ANY state change — including bf.proposing →
	// bf.implementing where both states share the same TopLevel("core")
	// but each maps to a different "room" from the user's perspective.
	// Rooms without a banner element are filtered by the helper
	// returning "", so non-banner rooms never fire.
	if o.roomEnterSink != nil && result.NewState != "" && result.NewState != journey.State {
		if st := o.def.States[string(result.NewState)]; st != nil {
			env := expr.Env{World: result.World.Vars}
			if banner := renderRoomBanner(o.def, st, env); banner != "" {
				o.roomEnterSink.OnRoomEnter(result.NewState, banner)
			}
		}
	}

	// Success path: dispatch any host calls collected by the machine, apply
	// their bindings to world, and refresh the view so the user sees the
	// updated state on the same turn.
	// Stamp the foreground turn on ctx so every agent.call.* event this turn
	// emits — including those fired by the post-bind emit recursion
	// (settlePostBindEmits), which is how the bugfix story advances phase to
	// phase — carries the real foreground turn rather than turn=0
	// (turn=0). dispatchHostCalls overwrites StatePath
	// per call with the destination phase, so only Turn need be set here.
	ctx = host.WithAgentCallCtx(ctx, host.AgentCallCtx{
		SessionID: sid,
		Turn:      turnNum,
		StatePath: result.NewState,
	})
	hostEvents, hostWorld, hostView, hostRedirect, hostErr := o.dispatchHostCalls(ctx, sid, result.HostCalls, result.World, result.NewState)
	if hostErr != nil {
		// A cancelled execution context (operator hit Stop — see
		// runstatus.session.cancel) aborts the turn cleanly: persist nothing and
		// leave the session at its pre-turn state. Without this the turn falls
		// through to appendEventsAndJournal and bakes the cancelled/partial outcome
		// into the journal, so every later reopen replays it.
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		tl.Debug(ctx, trace.EvHarnessError, slog.String("host_dispatch_error", hostErr.Error()))
	}
	if len(hostEvents) > 0 {
		result.Events = append(result.Events, hostEvents...)
		result.World = hostWorld
		if hostView != "" {
			result.View = hostView
		}
	}
	// Honour an on_error: redirect from the host dispatch.  The redirect
	// state's on_enter has already run via dispatchHostCalls, and a
	// TransitionApplied event was appended for replay; here we update
	// result.NewState so subsequent allowed-intent / terminal-state /
	// turn-end logic targets the redirected state, not the original.
	if hostRedirect != "" {
		result.NewState = hostRedirect

		// Usability safety-net: an on_error: redirect routed this turn to a
		// destination room. If that room's view does not itself surface the
		// failure (most stories don't reference {{ world.last_error }}), the
		// operator would see a silently re-rendered room with no clue why the
		// turn bounced. Append a concise, consistently-formatted banner so the
		// reason is ALWAYS visible. Gated on (redirect happened AND last_error
		// is set), and skipped when the view already shows the error text, so
		// it never fires on success and never double-shows for the good
		// citizens that already render last_error.
		if msg, ok := result.World.Vars["last_error"].(string); ok && msg != "" {
			if !strings.Contains(result.View, msg) {
				result.View = appendErrorBanner(result.View, msg)
			}
		}
	}

	// Post-bind emit_intent dispatch (see settlePostBindEmits doc).
	var harnessErrMsg string
	if hostRedirect == "" && result.ValidationError == nil {
		harnessErrMsg = o.settlePostBindEmits(ctx, sid, &result, tl, 0)
		if harnessErrMsg == "" {
			o.resolveAutoGate(ctx, sid, &result, tl, 0)
		}
	}

	// Safety net: if no path along the way set result.View (machine.Turn
	// skipped the initial render because on_enter had binding host calls;
	// dispatchHostCalls's post-bind render returned ""; settlePostBindEmits
	// likewise returned ""), force-render the current state here so the
	// user is never left with a blank transcript entry. Failures are
	// logged but non-fatal — the operator still gets a turn outcome.
	if result.View == "" && result.NewState != "" {
		if v, rErr := o.machine.RenderState(result.NewState, result.World); rErr != nil {
			o.logger.Warn("orchestrator.fallback_render_failed",
				slog.String("state", string(result.NewState)),
				slog.String("err", rErr.Error()),
			)
		} else if v != "" {
			result.View = v
		}
	}

	successEvents := append(prefix, result.Events...)
	endEvent := newOrchestratorEvent(store.TurnEnded,
		transitionedTurnEnd(result.NewState, result.View), turnNum)
	successEvents = append(successEvents, endEvent)
	if inputEvent.Kind != "" {
		successEvents = append([]store.Event{inputEvent}, successEvents...)
	}

	// Stamp turn number on all events.
	for i := range successEvents {
		successEvents[i].Turn = turnNum
	}
	// G5: pre-stamp StateEntered/StateExited with their per-event state before
	// the uniform FROM-state fill, so machine.state_entered carries the TO state.
	stampStatePathPerEvent(successEvents)
	// Stamp state_path on events that don't already have one.
	// Finding 2.1: fall back to InitialState when journey.State is "" so every event has non-empty state_path.
	stampStatePath(successEvents, journey.State, o.InitialState())

	// Final cancellation chokepoint: if the operator hit Stop while the post-bind
	// emit recursion or the fallback render was running (after the main host
	// dispatch returned), bail before persisting so a cancelled turn never lands
	// in the journal. The host-dispatch guard above catches the common case (Stop
	// during the agent call itself); this covers the narrow tail.
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	// Site 2: dual-write journal entries for the success turn.
	jEntries := journalEntriesForEvents(sid, turnNum, time.Now(), successEvents,
		journey.World, result.World, result.View, result.NewState, input)
	if appendErr := o.appendEventsAndJournal(sid, successEvents, jEntries); appendErr != nil {
		return nil, fmt.Errorf("orchestrator: append events: %w", appendErr)
	}

	tl.Debug(ctx, trace.EvTurnPersisted,
		slog.Int("count", len(successEvents)),
		slog.String("outcome", "transitioned"),
	)

	// Cache writeback: record this
	// LLM-resolved verdict against the original input so subsequent
	// turns at the same state with the same lexical signature can
	// short-circuit. We deliberately key on journey.State (the state
	// BEFORE the transition) — that's the state the user was in
	// when they typed the input, which is what re-Validate runs
	// against on a future hit.
	o.putTurnCache(ctx, journey.State, input, call.Intent, slotsToMap(call.Slots), harnessConfidence(params), "", "")

	// Clear any pending clarification for this session.
	o.mu.Lock()
	delete(o.pending, sid)
	o.mu.Unlock()

	// (Re-)arm any Timeout: declared on the new state, cancelling any
	// pre-existing timeout on the state we just exited.
	o.armTimeoutForState(sid, journey.State, result.NewState)

	// Compute updated allowed intents in the new state.
	newAllowed := o.machine.AllowedIntents(result.NewState, result.World)
	newAllowedNames := make([]string, len(newAllowed))
	for i, ai := range newAllowed {
		newAllowedNames[i] = ai.Name
	}

	mode := ModeTransitioned

	// Check if the new state is terminal.
	newState := lookupStateByPath(o.def, result.NewState)
	if newState != nil && newState.Terminal {
		mode = ModeCompleted
		// Tear down the session's background-job listener goroutine.
		o.stopSessionListener(sid)
	}

	// Entering a room whose on_enter binds leaves result.TypedView nil
	// (machine.Turn skipped its typed render; dispatchHostCalls only
	// re-rendered the text). Re-render the typed view against the bound
	// world so the browser gets typed_view instead of falling back to the
	// 80-col plain-text blob. See refreshTypedViewAfterBind.
	o.refreshTypedViewAfterBind(&result)

	tl.Debug(ctx, trace.EvTurnDone,
		slog.String("mode", mode.String()),
		slog.Int("view_bytes", len(result.View)),
		slog.String("view_rendered", result.View),
		slog.String("new_state", string(result.NewState)),
	)

	return &TurnOutcome{
		Mode:           mode,
		View:           result.View,
		TypedView:      result.TypedView,
		RenderEnv:      result.RenderEnv,
		Renderer:       result.Renderer,
		NewState:       result.NewState,
		Events:         successEvents,
		AllowedIntents: newAllowedNames,
		TurnNumber:     turnNum,
		HarnessError:   harnessErrMsg,
	}, nil
}

// OrchestratorPostBindMaxDepth caps how deeply settlePostBindEmits may
// recurse within a single turn.  Each iteration of the outer loop —
// on_enter → host call → bind → emit_intent → target's on_enter →
// host call → bind → emit_intent → … — re-enters settlePostBindEmits
// against the freshly-bound world, which RESETS the machine-side
// EmitIntentMaxDepth=8 counter (that cap only protects ONE
// dispatchEmittedIntents chain).  Without this orchestrator-side cap
// a YAML bug forming a cycle of "host call binds key, key gates
// emit_intent, emit lands on state whose on_enter has another host
// call that binds the same key" would loop until the turn timed out.
//
// 4 is a tight budget that still permits legitimate composition: the
// machine cap of 8 multiplied by 4 outer iterations gives a total of
// 32 emits per turn before we bail out — far above the deepest known
// real story (the bugfix room's 2-step LLM-judge chain uses 1).
const OrchestratorPostBindMaxDepth = 4

// settlePostBindEmits re-evaluates emit_intent: effects on the
// just-entered state's on_enter chain against the post-bind world,
// dispatches any whose `when:` guard now passes, and folds the
// resulting events / state / world / view into `res`. The machine's
// applyEffectsTraced silently defers emit_intent: effects whose
// machine-time guard eval errors against an unbound world key
// (typical when the same on_enter chain has a host.* invoke that
// binds the key the emit's guard reads); this helper picks them up
// after dispatchHostCalls has run the host call and applied the bind.
//
// emit_intent dispatches can themselves queue host calls (a target
// state's on_enter may invoke); we dispatch those nested calls
// synchronously so the whole chain settles in the same externally-
// initiated turn.
//
// `depth` is the recursion count of THIS outer settle pass.  When it
// exceeds OrchestratorPostBindMaxDepth, the function appends a
// HarnessError event to res.Events and returns the cap message —
// the caller surfaces this in TurnOutcome.HarnessError so the
// failure is loud rather than silently logged.
//
// When machine.DispatchPostBindEmits itself returns an error (e.g.
// a `when:` guard that fails to evaluate against the post-bind
// world), the function emits a HarnessError event for the journal
// and returns the error message.  The turn continues — `res` is
// left at the pre-emit resting place — so the session lands on a
// known state instead of in a half-bound limbo. (P1-A / P1-B from
// the dev-story-bugfix-unify Opus review.)
func (o *Orchestrator) settlePostBindEmits(ctx context.Context, sid app.SessionID, res *machine.TurnResult, tl *trace.TurnLogger, depth int) string {
	if depth > OrchestratorPostBindMaxDepth {
		msg := fmt.Sprintf("settlePostBindEmits: orchestrator recursion depth %d exceeded cap %d (likely YAML cycle: host-call binds key that gates emit_intent that lands on state with another host-call binding the same key)", depth, OrchestratorPostBindMaxDepth)
		if tl != nil {
			tl.Debug(ctx, trace.EvHarnessError,
				slog.String("phase", "settle_post_bind_emits"),
				slog.Int("depth", depth),
				slog.Int("max_depth", OrchestratorPostBindMaxDepth),
				slog.String("error", msg),
			)
		}
		res.Events = append(res.Events, newOrchestratorEvent(store.HarnessError, map[string]any{
			"phase":     "settle_post_bind_emits",
			"depth":     depth,
			"max_depth": OrchestratorPostBindMaxDepth,
			"error":     msg,
		}, 0))
		return msg
	}

	// When a live sink is wired, stream each synthetic hop's say-text as a
	// per-room progress breadcrumb so a one-shot chain narrates what it's
	// doing instead of jumping silently to the final room. The say is then
	// NOT prepended to the final view (it already streamed) — see the
	// `streamed` guard on the renders below.
	streamed := o.roomEnterSink != nil
	var onEnter func(state string, say string)
	if streamed {
		onEnter = func(state string, say string) {
			o.roomEnterSink.OnRoomEnter(app.StatePath(state), say)
		}
	}

	emState, emWorld, emHostCalls, emSay, emEvents, emErr := o.machine.DispatchPostBindEmits(ctx, res.NewState, res.World, o.execMode.staged(), onEnter)
	if emErr != nil {
		msg := emErr.Error()
		if tl != nil {
			tl.Debug(ctx, trace.EvHarnessError,
				slog.String("phase", "dispatch_post_bind_emits"),
				slog.String("error", msg),
			)
		}
		// Surface in the event log so the journal captures the why.
		// Continue: res still carries the pre-emit state, which is the
		// known resting place rather than half-bound limbo.
		res.Events = append(res.Events, newOrchestratorEvent(store.HarnessError, map[string]any{
			"phase": "dispatch_post_bind_emits",
			"error": msg,
		}, 0))
		return msg
	}
	if len(emEvents) == 0 {
		return ""
	}
	res.NewState = emState
	res.World = emWorld
	res.Events = append(res.Events, emEvents...)
	if len(emHostCalls) > 0 {
		ehe, ehw, ehv, ehr, _ := o.dispatchHostCalls(ctx, sid, emHostCalls, res.World, res.NewState)
		if len(ehe) > 0 {
			res.Events = append(res.Events, ehe...)
			res.World = ehw
			if ehv != "" {
				res.View = ehv
			}
		}
		if ehr != "" {
			res.NewState = ehr
		}
		// After nested host dispatch the new state may itself have an
		// emit_intent waiting on a freshly-bound world key — recurse
		// once, bumping depth.  The machine's EmitIntentMaxDepth still
		// caps each individual dispatch chain; this cap protects the
		// orchestrator-side loop that resets the inner counter.
		if nestedErr := o.settlePostBindEmits(ctx, sid, res, tl, depth+1); nestedErr != "" {
			// Refresh the view from whatever state we landed at before
			// returning the nested error so the caller still has
			// something to render.
			v, rErr := o.machine.RenderState(res.NewState, res.World)
			if rErr != nil {
				slog.Warn("orchestrator.render_after_bind_failed",
					"state", string(res.NewState), "err", rErr.Error())
			} else if v != "" {
				if emSay != "" && !streamed {
					res.View = emSay + "\n\n" + v
				} else {
					res.View = v
				}
			}
			return nestedErr
		}
	}
	// Refresh the view so it reflects the final settled state. A
	// render error here used to silently zero the view (the user
	// described it as "dumped into nothingness"). Log it so the
	// failure is at least visible in the trace; the upstream view
	// template needs to be hardened — see docs/stories/story-style.md
	// "The view MUST always render to something visible".
	v, rErr := o.machine.RenderState(res.NewState, res.World)
	if rErr != nil {
		slog.Warn("orchestrator.render_after_bind_failed",
			"state", string(res.NewState), "err", rErr.Error())
	} else if v != "" {
		if emSay != "" && !streamed {
			res.View = emSay + "\n\n" + v
		} else {
			res.View = v
		}
	}
	return ""
}

// SubmitDirect submits an intent call directly to the machine, bypassing the
// LLM harness entirely. This is the "direct path" for menu rows where all
// required slots are already known (e.g. enum-expanded rows like "go south").
// It mirrors the success path of Turn but skips harness.RunTurn.
//
// When called externally (CLI / TUI menu pick / programmatic intent), no
// user free-text exists, so the recorded TurnStarted.input field carries a
// synthetic "[direct] intent=<name>" marker. Routing tiers that DO have the
// user's original text should call [SubmitDirectFromInput] instead so the
// recorded input survives into inspect.LastTurns and any replay path.
func (o *Orchestrator) SubmitDirect(ctx context.Context, sid app.SessionID, intentName string, slots map[string]any) (*TurnOutcome, error) {
	return o.submitDirect(ctx, sid, intentName, slots, "", RouteProvenance{})
}

// RouteProvenance records HOW an intent was resolved before a direct
// submission — the missing "why" on the audit trail. The auto-routing
// tiers (deterministic / semantic / turncache) submit an intent without
// an LLM call and previously left only a bare `direct: true` on the
// TurnStarted event, so a reader of the persisted session trace could
// not tell why (say) `quit` fired. Carrying the source, the human-
// readable match reason, and the confidence onto the journal event lets
// the trace explain a transition like "a stray 'cancel' token in a paste
// routed to quit via the semantic tier at 0.90". The zero value (empty
// Source) records nothing extra — used by genuinely caller-chosen direct
// submissions (CLI --intent, programmatic) where there is no routing to
// explain.
type RouteProvenance struct {
	// Source is the routing tier: "deterministic", "semantic",
	// "turncache", "llm", "context_route", etc. Empty means "not routed" (omit).
	Source string
	// MatchType is the tier-specific reason, e.g. "display" / "example"
	// for the deterministic tier or "synonym:cancel" / "example:…" for
	// the semantic tier. Optional.
	MatchType string
	// Confidence is the routing confidence band (0 when not applicable).
	Confidence float64
	// ContextRouteClass carries the contextual-router class
	// (intent|help|room_request|meta_edit) when the contextual tier resolved
	// this turn. Stamped as "context_route_class" on TurnStarted so the trace
	// records which class was decided for replay.
	ContextRouteClass string
}

// stampOn writes the non-empty provenance fields onto a TurnStarted
// payload map. A zero-value provenance adds nothing, so the event shape
// is unchanged for genuinely caller-chosen direct submissions.
func (p RouteProvenance) stampOn(payload map[string]any) {
	if p.Source == "" {
		return
	}
	payload["routed_by"] = p.Source
	if p.MatchType != "" {
		payload["match_type"] = p.MatchType
	}
	if p.Confidence > 0 {
		payload["confidence"] = p.Confidence
	}
	if p.ContextRouteClass != "" {
		payload["context_route_class"] = p.ContextRouteClass
	}
}

func emitRoutingStream(ctx context.Context, turnNum app.TurnNumber, intentName string, prov RouteProvenance) {
	if prov.Source == "" {
		return
	}
	sink := host.StreamSinkFrom(ctx)
	if sink == nil {
		return
	}
	sink.OnStreamEvent(ctx, host.StreamEvent{
		Type:       "routing",
		Turn:       int64(turnNum),
		Intent:     intentName,
		RoutedBy:   prov.Source,
		MatchType:  prov.MatchType,
		Confidence: prov.Confidence,
	})
}

// SubmitDirectRouted is [SubmitDirectFromInput] plus routing provenance:
// the resolving tier stamps how the intent was chosen onto the
// TurnStarted journal event so the persisted trace explains the
// transition. Internal routing tiers call this; external callers that
// have no routing to explain use [SubmitDirect] / [SubmitDirectFromInput].
func (o *Orchestrator) SubmitDirectRouted(ctx context.Context, sid app.SessionID, intentName string, slots map[string]any, userInput string, prov RouteProvenance) (*TurnOutcome, error) {
	return o.submitDirect(ctx, sid, intentName, slots, userInput, prov)
}

// SubmitDirectFromInput is identical to [SubmitDirect] except it records
// userInput verbatim on the TurnStarted event. Internal routing tiers
// (deterministic, semantic) call this so the user's original text — not a
// "[direct] intent=…" marker — is what gets stored on the turn's audit
// trail (cmd/kitsoki/inspect.LastTurns[].Input and the replay path read
// this field).
//
// Pass userInput="" to fall back to the synthetic marker (equivalent to
// calling SubmitDirect).
func (o *Orchestrator) SubmitDirectFromInput(ctx context.Context, sid app.SessionID, intentName string, slots map[string]any, userInput string) (*TurnOutcome, error) {
	return o.submitDirect(ctx, sid, intentName, slots, userInput, RouteProvenance{})
}

// submitDirect is the shared implementation behind [SubmitDirect] and
// [SubmitDirectFromInput]. userInput is recorded verbatim on TurnStarted
// when non-empty; when empty we fall back to "[direct] intent=<name>".
func (o *Orchestrator) submitDirect(ctx context.Context, sid app.SessionID, intentName string, slots map[string]any, userInput string, prov RouteProvenance) (*TurnOutcome, error) {
	// Serialise against handleJobTerminal — see Turn for rationale.
	sessMu := o.sessionLock(sid)
	sessMu.Lock()
	defer sessMu.Unlock()

	journey, err := o.loadJourney(sid)
	if err != nil {
		return nil, fmt.Errorf("orchestrator: SubmitDirect: load journey: %w", err)
	}

	turnNum := journey.Turn + 1
	tl := trace.NewTurnLogger(o.logger, sid, turnNum, journey.State)
	tl.Debug(ctx, trace.EvTurnStart,
		slog.String("intent", intentName),
		slog.String("mode", "submit-direct"),
	)
	emitRoutingStream(ctx, turnNum, intentName, prov)

	call := intent.IntentCall{
		Intent: intentName,
		Slots:  world.Slots(slots),
	}

	result, machineErr := o.machine.Turn(ctx, journey.State, journey.World, call)
	if machineErr != nil {
		o.journalTurnError(ctx, tl, sid, turnNum, journey.State, call, journey.World, machineErr)
		return nil, fmt.Errorf("orchestrator: SubmitDirect: machine.Turn: %w", machineErr)
	}

	for i := range result.Events {
		result.Events[i].Turn = turnNum
	}

	// Finding 2.6: emit UserInputReceived for --intent mode (SubmitDirect) too.
	// The Turn() path already emits it; SubmitDirect must also emit it so every
	// entry point produces a universal UserInputReceived event in the trace.
	// Payload uses intent + input so the SPA renders a user-input chip regardless
	// of the entry point used.
	sdInputPayload, _ := json.Marshal(map[string]any{
		"input":  userInput,
		"intent": intentName,
	})
	sdInputEvent := store.Event{
		Kind:      store.UserInputReceived,
		Turn:      turnNum,
		StatePath: journey.State,
		Payload:   sdInputPayload,
	}

	if result.ValidationError != nil {
		ve := result.ValidationError
		if ve.Code == intent.ErrMissingSlots {
			sdSlotsSoFar := slotsToMap(call.Slots)
			o.mu.Lock()
			o.pending[sid] = &pendingClarify{
				intentName: call.Intent,
				slots:      sdSlotsSoFar,
			}
			o.mu.Unlock()

			missingSlots := ve.MissingSlots
			clarification := ComputeClarification(o.def, journey.State, call.Intent, missingSlots)
			tl.Debug(ctx, trace.EvSlotFillRequested,
				slog.String("intent", call.Intent),
				slog.Int("missing_count", len(missingSlots)),
				slog.Any("missing", missingSlots),
				slog.String("origin", "submit_direct"),
			)
			// Site 8 (SubmitDirect path): emit clarify.requested via standalone journal write.
			sdMissingNames := make([]string, len(missingSlots))
			copy(sdMissingNames, missingSlots)
			o.appendJournal(journalEntry(sid, turnNum, 0, time.Now(),
				journal.KindClarifyRequested, "",
				map[string]any{
					"origin":       "foreground",
					"intent":       call.Intent,
					"slots_so_far": sdSlotsSoFar,
					"slots_needed": sdMissingNames,
				}))
			return &TurnOutcome{
				Mode:          ModeClarify,
				NewState:      journey.State,
				PendingIntent: call.Intent,
				PendingSlots:  sdSlotsSoFar,
				SlotsNeeded:   clarification.Slots,
				TurnNumber:    turnNum,
			}, nil
		}
		// Recorded `input` prefers the user's original text when supplied
		// (semantic / deterministic routing tiers); otherwise we keep the
		// "[direct] intent=…" synthetic marker so external SubmitDirect
		// callers (TUI menu pick, CLI --intent, etc.) still have a
		// non-empty audit-trail value.
		recordedInput := userInput
		if recordedInput == "" {
			recordedInput = fmt.Sprintf("[direct] intent=%s", intentName)
		}
		// view.rendered.user_input mirrors the same rule, falling back to
		// the intent name (not the marker) so resumed transcripts render
		// "> go" rather than "> [direct] intent=go" on external direct
		// submissions — see TestAttachSession_SubmitDirectUsesIntentName.
		journalUserInput := userInput
		if journalUserInput == "" {
			journalUserInput = intentName
		}
		startPayload := map[string]any{
			"turn":   int64(turnNum),
			"input":  recordedInput,
			"direct": true,
		}
		prov.stampOn(startPayload)
		startEvent := newOrchestratorEvent(store.TurnStarted, startPayload, turnNum)
		failureEvents := append([]store.Event{sdInputEvent, startEvent}, result.Events...)
		endEvent := newOrchestratorEvent(store.TurnEnded, map[string]any{
			"outcome": "rejected",
			"code":    string(ve.Code),
		}, turnNum)
		failureEvents = append(failureEvents, endEvent)
		for i := range failureEvents {
			failureEvents[i].Turn = turnNum
		}
		stampStatePathPerEvent(failureEvents)
		stampStatePath(failureEvents, journey.State, o.InitialState())
		// Site 5: dual-write journal entries for the SubmitDirect rejection turn.
		sdFailJEntries := journalEntriesForEvents(sid, turnNum, time.Now(), failureEvents,
			journey.World, journey.World, "", journey.State, journalUserInput)
		if appendErr := o.appendEventsAndJournal(sid, failureEvents, sdFailJEntries); appendErr != nil {
			return nil, fmt.Errorf("orchestrator: SubmitDirect: append failure events: %w", appendErr)
		}
		allowedNames := make([]string, 0)
		for _, ai := range o.machine.AllowedIntents(journey.State, journey.World) {
			allowedNames = append(allowedNames, ai.Name)
		}
		return &TurnOutcome{
			Mode:           ModeRejected,
			NewState:       journey.State,
			Events:         failureEvents,
			GuardHint:      ve.GuardHint,
			ErrorCode:      ve.Code,
			ErrorMessage:   ve.Message,
			AllowedIntents: allowedNames,
			TurnNumber:     turnNum,
		}, nil
	}

	// Build and persist events (same as Turn success path).
	// See the rejection branch above for the userInput → recorded
	// input/journal mapping rules; mirror them here for the success path.
	successRecordedInput := userInput
	if successRecordedInput == "" {
		successRecordedInput = fmt.Sprintf("[direct] intent=%s", intentName)
	}
	successJournalUserInput := userInput
	if successJournalUserInput == "" {
		successJournalUserInput = intentName
	}
	successStartPayload := map[string]any{
		"turn":   int64(turnNum),
		"input":  successRecordedInput,
		"direct": true,
	}
	prov.stampOn(successStartPayload)
	startEvent := newOrchestratorEvent(store.TurnStarted, successStartPayload, turnNum)

	// Stamp the foreground turn on ctx so agent.call.* events fired by the
	// on_enter chain and the post-bind emit recursion carry the real turn (not
	// turn=0).
	ctx = host.WithAgentCallCtx(ctx, host.AgentCallCtx{
		SessionID: sid,
		Turn:      turnNum,
		StatePath: result.NewState,
	})
	hostEvents, hostWorld, hostView, hostRedirect, hostErr := o.dispatchHostCalls(ctx, sid, result.HostCalls, result.World, result.NewState)
	if hostErr != nil {
		// A cancelled execution context (operator hit Stop — see
		// runstatus.session.cancel) aborts the turn cleanly: persist nothing and
		// leave the session at its pre-turn state. Without this the turn falls
		// through to appendEventsAndJournal and bakes the cancelled/partial outcome
		// into the journal, so every later reopen replays it.
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		tl.Debug(ctx, trace.EvHarnessError, slog.String("host_dispatch_error", hostErr.Error()))
	}
	if len(hostEvents) > 0 {
		result.Events = append(result.Events, hostEvents...)
		result.World = hostWorld
		if hostView != "" {
			result.View = hostView
		}
	}
	if hostRedirect != "" {
		result.NewState = hostRedirect
	}

	// Post-bind emit_intent dispatch — see settlePostBindEmits doc.
	var harnessErrMsg string
	if hostRedirect == "" && result.ValidationError == nil {
		harnessErrMsg = o.settlePostBindEmits(ctx, sid, &result, tl, 0)
		if harnessErrMsg == "" {
			o.resolveAutoGate(ctx, sid, &result, tl, 0)
		}
	}

	successEvents := append([]store.Event{sdInputEvent, startEvent}, result.Events...)
	endEvent := newOrchestratorEvent(store.TurnEnded,
		transitionedTurnEnd(result.NewState, result.View), turnNum)
	successEvents = append(successEvents, endEvent)
	for i := range successEvents {
		successEvents[i].Turn = turnNum
	}
	stampStatePathPerEvent(successEvents)
	stampStatePath(successEvents, journey.State, o.InitialState())

	// Site 6: dual-write journal entries for the SubmitDirect success turn.
	sdSuccJEntries := journalEntriesForEvents(sid, turnNum, time.Now(), successEvents,
		journey.World, result.World, result.View, result.NewState, successJournalUserInput)
	if appendErr := o.appendEventsAndJournal(sid, successEvents, sdSuccJEntries); appendErr != nil {
		return nil, fmt.Errorf("orchestrator: SubmitDirect: append events: %w", appendErr)
	}

	tl.Debug(ctx, trace.EvTurnPersisted,
		slog.Int("count", len(successEvents)),
		slog.String("outcome", "transitioned"),
	)

	o.mu.Lock()
	delete(o.pending, sid)
	o.mu.Unlock()

	// (Re-)arm any Timeout: declared on the new state.
	o.armTimeoutForState(sid, journey.State, result.NewState)

	newAllowed := o.machine.AllowedIntents(result.NewState, result.World)
	newAllowedNames := make([]string, len(newAllowed))
	for i, ai := range newAllowed {
		newAllowedNames[i] = ai.Name
	}

	mode := ModeTransitioned
	newStateDef := lookupStateByPath(o.def, result.NewState)
	if newStateDef != nil && newStateDef.Terminal {
		mode = ModeCompleted
		// Tear down the session's background-job listener goroutine, mirroring
		// the equivalent call in Turn's terminal-state branch.
		o.stopSessionListener(sid)
	}

	// Entering a room whose on_enter binds leaves result.TypedView nil
	// (machine.Turn skipped its typed render; dispatchHostCalls only
	// re-rendered the text). Re-render the typed view against the bound
	// world so the browser gets typed_view instead of falling back to the
	// 80-col plain-text blob. See refreshTypedViewAfterBind.
	o.refreshTypedViewAfterBind(&result)

	tl.Debug(ctx, trace.EvTurnDone,
		slog.String("mode", mode.String()),
		slog.Int("view_bytes", len(result.View)),
		slog.String("view_rendered", result.View),
		slog.String("new_state", string(result.NewState)),
	)

	return &TurnOutcome{
		Mode:           mode,
		View:           result.View,
		TypedView:      result.TypedView,
		RenderEnv:      result.RenderEnv,
		Renderer:       result.Renderer,
		NewState:       result.NewState,
		Events:         successEvents,
		AllowedIntents: newAllowedNames,
		TurnNumber:     turnNum,
		HarnessError:   harnessErrMsg,
	}, nil
}

// OneShot runs a single turn against (state, world) without touching the
// store: no journey load, no event append, no snapshot. It is the building
// block for `kitsoki turn`. Returns the diff (state, world, events, host calls,
// rendered view) so callers can answer "what happens if I do X in state Y
// with world Z?" without spinning up a real session.
//
// Routing:
//   - in.Intent set → direct path: the call goes straight to the machine.
//   - in.Input set  → LLM path: harness.RunTurn is called first to translate
//     the free text into an intent. Requires the orchestrator to be built
//     with a real harness (the replay harness works fine for tests).
//
// Host calls are dispatched the same way Turn dispatches them, so binding
// effects on world are visible in WorldAfter and the View reflects the
// post-binding state.
func (o *Orchestrator) OneShot(ctx context.Context, in OneShotInput) (*OneShotResult, error) {
	w := world.World{Vars: make(map[string]any, len(in.World))}
	for k, v := range in.World {
		w.Vars[k] = v
	}
	worldBefore := make(map[string]any, len(w.Vars))
	for k, v := range w.Vars {
		worldBefore[k] = v
	}

	var (
		call intent.IntentCall
		err  error
	)
	switch {
	case in.Intent != "":
		call = intent.IntentCall{
			Intent: in.Intent,
			Slots:  world.Slots(in.Slots),
		}
	case in.Input != "":
		allowed := o.machine.AllowedIntents(in.State, w)
		allowedNames := make([]string, len(allowed))
		for i, a := range allowed {
			allowedNames[i] = a.Name
		}
		params, runErr := o.harness.RunTurn(ctx, harness.TurnInput{
			SessionID:      app.SessionID("oneshot"),
			TurnNumber:     1,
			UserText:       in.Input,
			StatePath:      in.State,
			World:          w,
			AllowedIntents: allowedNames,
		})
		if runErr != nil {
			return nil, fmt.Errorf("orchestrator: OneShot: harness.RunTurn: %w", runErr)
		}
		call, err = parseIntentCall(params)
		if err != nil {
			return nil, fmt.Errorf("orchestrator: OneShot: parse intent call: %w", err)
		}
	default:
		return nil, fmt.Errorf("orchestrator: OneShot: exactly one of Intent or Input must be set")
	}

	result, machineErr := o.machine.Turn(ctx, in.State, w, call)
	if machineErr != nil {
		return nil, fmt.Errorf("orchestrator: OneShot: machine.Turn: %w", machineErr)
	}

	out := &OneShotResult{
		Intent:      call.Intent,
		Slots:       slotsToMap(call.Slots),
		PrevState:   in.State,
		NextState:   result.NewState,
		WorldBefore: worldBefore,
	}

	if result.ValidationError != nil {
		ve := result.ValidationError
		if ve.Code == intent.ErrMissingSlots {
			clarification := ComputeClarification(o.def, in.State, call.Intent, ve.MissingSlots)
			out.Mode = ModeClarify
			out.SlotsNeeded = clarification.Slots
		} else {
			out.Mode = ModeRejected
		}
		out.ErrorCode = string(ve.Code)
		out.ErrorMessage = ve.Message
		out.GuardHint = ve.GuardHint
		out.NextState = in.State
		out.WorldAfter = worldBefore
		out.AllowedIntents = allowedNamesFromMachine(o.machine, in.State, w)
		// View is whatever the unchanged state would render.
		view, _ := o.machine.RenderState(in.State, w)
		out.View = view
		return out, nil
	}

	// Capture EffectApplied events from the machine before host dispatch so
	// `kitsoki turn` can show effect-by-effect diffs.
	out.Effects = effectsFromEvents(result.Events)

	hostSummaries, hostEvents, hostWorld, hostView, hostRedirect, hostErr := o.dispatchHostCallsDetailed(ctx, result.HostCalls, result.World, result.NewState)
	if hostErr != nil {
		return nil, fmt.Errorf("orchestrator: OneShot: %w", hostErr)
	}
	if len(hostEvents) > 0 {
		result.Events = append(result.Events, hostEvents...)
		// Re-collect effects after host dispatch produced more EffectApplied events.
		out.Effects = effectsFromEvents(result.Events)
		result.World = hostWorld
		if hostView != "" {
			result.View = hostView
		}
	}
	if hostRedirect != "" {
		result.NewState = hostRedirect
		out.NextState = hostRedirect
	}

	out.HostCalls = hostSummaries

	out.Mode = ModeTransitioned
	if newState := lookupStateByPath(o.def, result.NewState); newState != nil && newState.Terminal {
		out.Mode = ModeCompleted
	}
	out.View = result.View

	out.WorldAfter = make(map[string]any, len(result.World.Vars))
	for k, v := range result.World.Vars {
		out.WorldAfter[k] = v
	}
	out.AllowedIntents = allowedNamesFromMachine(o.machine, result.NewState, result.World)

	return out, nil
}

// effectsFromEvents flattens EffectApplied events into EffectSummary form.
func effectsFromEvents(events []store.Event) []EffectSummary {
	var out []EffectSummary
	for _, ev := range events {
		if ev.Kind != store.EffectApplied {
			continue
		}
		var es EffectSummary
		if err := json.Unmarshal(ev.Payload, &es); err != nil {
			continue
		}
		out = append(out, es)
	}
	return out
}

// allowedNamesFromMachine collects intent names allowed in (state, world).
func allowedNamesFromMachine(m machine.Machine, state app.StatePath, w world.World) []string {
	allowed := m.AllowedIntents(state, w)
	out := make([]string, len(allowed))
	for i, ai := range allowed {
		out[i] = ai.Name
	}
	return out
}

// ContinueTurn retries the pending intent with supplemental slot values
// collected from the clarification UI (the slot-fill continuation of a turn).
func (o *Orchestrator) ContinueTurn(ctx context.Context, sid app.SessionID, supplementSlots map[string]any) (*TurnOutcome, error) {
	// Serialise against handleJobTerminal — see Turn for rationale.
	sessMu := o.sessionLock(sid)
	sessMu.Lock()
	defer sessMu.Unlock()

	o.mu.Lock()
	pend, ok := o.pending[sid]
	o.mu.Unlock()

	if !ok {
		return nil, fmt.Errorf("orchestrator: no pending clarification for session %s", sid)
	}

	// Merge the supplement into the pending slots.
	merged := make(world.Slots, len(pend.slots)+len(supplementSlots))
	for k, v := range pend.slots {
		merged[k] = v
	}
	for k, v := range supplementSlots {
		merged[k] = v
	}

	// Reconstruct the journey.
	journey, err := o.loadJourney(sid)
	if err != nil {
		return nil, fmt.Errorf("orchestrator: load journey: %w", err)
	}

	call := intent.IntentCall{
		Intent: pend.intentName,
		Slots:  merged,
	}

	turnNum := journey.Turn + 1
	tl := trace.NewTurnLogger(o.logger, sid, turnNum, journey.State)
	tl.Debug(ctx, trace.EvTurnStart,
		slog.String("intent", call.Intent),
		slog.String("mode", "clarify-continue"),
	)
	tl.Debug(ctx, trace.EvSlotFillContinued,
		slog.String("intent", call.Intent),
		slog.Int("supplement_count", len(supplementSlots)),
		slog.Any("supplement_keys", supplementKeys(supplementSlots)),
	)

	result, machineErr := o.machine.Turn(ctx, journey.State, journey.World, call)
	if machineErr != nil {
		o.journalTurnError(ctx, tl, sid, turnNum, journey.State, call, journey.World, machineErr)
		return nil, fmt.Errorf("orchestrator: machine.Turn (continue): %w", machineErr)
	}

	// Stamp turn number.
	for i := range result.Events {
		result.Events[i].Turn = turnNum
	}

	if result.ValidationError != nil {
		ve := result.ValidationError
		if ve.Code == intent.ErrMissingSlots {
			// Still missing slots; update the pending state.
			o.mu.Lock()
			o.pending[sid] = &pendingClarify{
				intentName: call.Intent,
				slots:      map[string]any(merged),
			}
			o.mu.Unlock()

			clarification := ComputeClarification(o.def, journey.State, call.Intent, ve.MissingSlots)
			tl.Debug(ctx, trace.EvSlotFillRequested,
				slog.String("intent", call.Intent),
				slog.Int("missing_count", len(ve.MissingSlots)),
				slog.Any("missing", ve.MissingSlots),
				slog.String("origin", "continue_turn"),
			)
			return &TurnOutcome{
				Mode:          ModeClarify,
				NewState:      journey.State,
				PendingIntent: call.Intent,
				PendingSlots:  map[string]any(merged),
				SlotsNeeded:   clarification.Slots,
				TurnNumber:    turnNum,
			}, nil
		}

		// Other validation error.
		allowedNames := make([]string, 0)
		if ai := o.machine.AllowedIntents(journey.State, journey.World); len(ai) > 0 {
			for _, a := range ai {
				allowedNames = append(allowedNames, a.Name)
			}
		}
		// Agent off-ramp: routed through the same helper so the rejection
		// sites can't drift (Task 1.3). This is the slot-continuation path —
		// it carries no fresh free-text utterance, so maybeOffRamp's empty-input
		// guard makes it inert here; the call exists for parity, not effect.
		if outcome, ok := o.maybeOffRamp(ctx, sid, journey.State, "", ve.Code, call.Confidence, allowedNames, turnNum); ok {
			return outcome, nil
		}
		// Rejection path: TypedView/RenderEnv/Renderer intentionally
		// omitted. The state did not transition (NewState == journey.State),
		// so the TUI keeps rendering the current room's typed view from the
		// last successful outcome. Re-emitting them here would be a no-op at
		// best and risk shadowing in-progress widget focus at worst.
		return &TurnOutcome{
			Mode:           ModeRejected,
			NewState:       journey.State,
			Events:         result.Events,
			GuardHint:      ve.GuardHint,
			ErrorCode:      ve.Code,
			ErrorMessage:   ve.Message,
			AllowedIntents: allowedNames,
			TurnNumber:     turnNum,
		}, nil
	}

	// Success: dispatch host calls then persist events.
	hostEvents, hostWorld, hostView, hostRedirect, hostErr := o.dispatchHostCalls(ctx, sid, result.HostCalls, result.World, result.NewState)
	if hostErr != nil {
		// A cancelled execution context (operator hit Stop — see
		// runstatus.session.cancel) aborts the turn cleanly: persist nothing and
		// leave the session at its pre-turn state. Without this the turn falls
		// through to appendEventsAndJournal and bakes the cancelled/partial outcome
		// into the journal, so every later reopen replays it.
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		tl.Debug(ctx, trace.EvHarnessError, slog.String("host_dispatch_error", hostErr.Error()))
	}
	if len(hostEvents) > 0 {
		result.Events = append(result.Events, hostEvents...)
		result.World = hostWorld
		if hostView != "" {
			result.View = hostView
		}
	}
	if hostRedirect != "" {
		result.NewState = hostRedirect
	}

	startEvent := newOrchestratorEvent(store.TurnStarted, map[string]any{
		"turn":       int64(turnNum),
		"input":      fmt.Sprintf("[clarify-continue] intent=%s", call.Intent),
		"clarify":    true,
		"routed_by":  "slot-fill",
		"match_type": "clarify-continue",
	}, turnNum)

	successEvents := append([]store.Event{startEvent}, result.Events...)
	endEvent := newOrchestratorEvent(store.TurnEnded,
		transitionedTurnEnd(result.NewState, result.View), turnNum)
	successEvents = append(successEvents, endEvent)

	for i := range successEvents {
		successEvents[i].Turn = turnNum
	}
	// Stamp state_path so every on-disk event records the active state
	// (matches the Turn/SubmitDirect paths). finding 2.1.
	stampStatePathPerEvent(successEvents)
	stampStatePath(successEvents, journey.State, o.InitialState())

	// Site 7: dual-write journal entries for the ContinueTurn success turn.
	// Prepend a clarify.answered entry before the standard set.
	ctNow := time.Now()
	ctJEntries := journalEntriesForEvents(sid, turnNum, ctNow, successEvents,
		journey.World, result.World, result.View, result.NewState, call.Intent)
	// Prepend clarify.answered (seq 0; other entries shift up by bumping seq on the fly via the slice).
	clarifyAnsweredEntry := journalEntry(sid, turnNum, 0, ctNow,
		journal.KindClarifyAnswered, "",
		map[string]any{
			"intent":      call.Intent,
			"slots_final": map[string]any(merged),
		})
	// Shift existing seq values to make room for the prepended entry.
	for i := range ctJEntries {
		ctJEntries[i].Seq++
	}
	ctJEntries = append([]journal.Entry{clarifyAnsweredEntry}, ctJEntries...)

	if appendErr := o.appendEventsAndJournal(sid, successEvents, ctJEntries); appendErr != nil {
		return nil, fmt.Errorf("orchestrator: append continue events: %w", appendErr)
	}

	tl.Debug(ctx, trace.EvTurnPersisted,
		slog.Int("count", len(successEvents)),
		slog.String("outcome", "transitioned"),
	)

	// Clear pending.
	o.mu.Lock()
	delete(o.pending, sid)
	o.mu.Unlock()

	newAllowed := o.machine.AllowedIntents(result.NewState, result.World)
	newAllowedNames := make([]string, len(newAllowed))
	for i, ai := range newAllowed {
		newAllowedNames[i] = ai.Name
	}

	mode := ModeTransitioned
	newStateDef := lookupStateByPath(o.def, result.NewState)
	if newStateDef != nil && newStateDef.Terminal {
		mode = ModeCompleted
	}

	// See refreshTypedViewAfterBind: a clarify→continue that lands in a
	// room whose on_enter binds would otherwise ship typed_view=nil and the
	// browser would fall back to the plain-text view.
	o.refreshTypedViewAfterBind(&result)

	tl.Debug(ctx, trace.EvTurnDone,
		slog.String("mode", mode.String()),
		slog.Int("view_bytes", len(result.View)),
		slog.String("view_rendered", result.View),
		slog.String("new_state", string(result.NewState)),
	)

	return &TurnOutcome{
		Mode:           mode,
		View:           result.View,
		TypedView:      result.TypedView,
		RenderEnv:      result.RenderEnv,
		Renderer:       result.Renderer,
		NewState:       result.NewState,
		Events:         successEvents,
		AllowedIntents: newAllowedNames,
		TurnNumber:     turnNum,
	}, nil
}

// InitialView returns the view for the initial state (to display at session start).
//
// Routes through machine.RenderState so the same env-population path the turn
// loop uses runs here too — in particular env.Menu and the available() /
// blocked() / blocked_reason() helper closures land populated. Bypassing the
// machine (as the prior render.go shortcut did) left the helpers nil and any
// template calling `available("...")` panicked with a nil-pointer dereference
// on the first frame.
func (o *Orchestrator) InitialView(w world.World) (string, error) {
	text, _, _, _, err := o.InitialViewTyped(w)
	return text, err
}

// InitialViewTyped is InitialView with the typed-view payload surfaced
// so the TUI's initial-paint seam can call AppendSystemTyped (which
// re-runs the typed-element pipeline on resize) instead of
// AppendSystem (which routes pre-rendered ANSI back through Glamour
// and corrupts the escape bytes). Returns the rendered text plus the
// typed View / env / renderer when the resolved leaf's view shape is
// a pure element-array form; typed is nil for legacy string,
// extends, template_file, parallel, and empty-view leaves — callers
// fall back to AppendSystem in that case.
func (o *Orchestrator) InitialViewTyped(w world.World) (string, *app.View, expr.Env, *render.AppRenderer, error) {
	initialState := o.InitialState()
	s := lookupStateByPath(o.def, initialState)
	if s == nil {
		return "", nil, expr.Env{}, nil, nil
	}
	if s.View.IsEmpty() {
		return s.Description, nil, expr.Env{}, nil, nil
	}
	return o.machine.RenderStateTyped(initialState, w)
}

// InitialState returns the initial state path for the app, descending
// into any compound root to its initial leaf. This matters for dogfood
// instances that import a sub-story under an alias and declare that
// alias as the root (e.g. kitsoki-dev's `root: core`, where `core` is
// the import wrapper compound with `initial: main`). Without the
// descent, the first frame renders against the bare wrapper — which
// carries no view block — and the operator sees an empty intro with
// no available intents.
func (o *Orchestrator) InitialState() app.StatePath {
	s, ok := o.def.Root.(string)
	if !ok {
		return ""
	}
	rootPath := app.StatePath(s)
	if o.machine == nil {
		return rootPath
	}
	leaf, err := o.machine.ResolveInitialLeaf(rootPath, o.InitialWorld())
	if err != nil || leaf == "" {
		return rootPath
	}
	return leaf
}

// InitialWorld returns a world initialised from the app's schema defaults.
func (o *Orchestrator) InitialWorld() world.World {
	return machine.WorldFromSchema(o.def.World)
}

// LoadJourney reconstructs the current state and world from the store.
// Exported for read-only callers (e.g. `kitsoki session show`); the Turn-loop
// path uses the unexported alias `loadJourney`.
func (o *Orchestrator) LoadJourney(sid app.SessionID) (*store.JourneyState, error) {
	return o.loadJourney(sid)
}

// PatchWorld injects world-key overrides into the session's event log without
// advancing a turn. Mirrors the flow-test runner's injectWorldOverride
// mechanism: each key-value pair is written as an EffectApplied event at
// turn = journey.Turn + 1 so the next RunIntent sees the patched values.
//
// Intended for demo/test tooling only (the runstatus web server exposes this
// as runstatus.session.patch_world). Never call from production story paths.
func (o *Orchestrator) PatchWorld(ctx context.Context, sid app.SessionID, patch map[string]any) error {
	if len(patch) == 0 {
		return nil
	}
	j, err := o.loadJourney(sid)
	if err != nil {
		return fmt.Errorf("PatchWorld: load journey: %w", err)
	}
	overrideTurn := j.Turn + 1
	events := make([]store.Event, 0, len(patch))
	for k, v := range patch {
		payload, _ := json.Marshal(map[string]any{"set": map[string]any{k: v}})
		events = append(events, store.Event{
			Kind:    store.EffectApplied,
			Turn:    overrideTurn,
			Payload: payload,
		})
	}
	sink := store.NewStoreSinkAdapter(o.store, sid)
	return sink.AppendBatch(events)
}

// RenderState renders the view template for (state, world) without touching
// the store. Thin wrapper around machine.RenderState for symmetry with
// LoadJourney.
func (o *Orchestrator) RenderState(state app.StatePath, w world.World) (string, error) {
	return o.machine.RenderState(state, w)
}

// refreshTypedViewAfterBind re-renders the typed view for the settled
// (NewState, World) when the machine left TypedView nil because the
// transition's on_enter host calls bind. machine.Turn deliberately skips
// its typed render in that case (see machine.go hostCallsWillBind: the
// pre-bind world would make bound-field templates error), and the post-bind
// re-render in dispatchHostCalls only produces the *text* view — so without
// this step result.TypedView stays nil all the way to the browser. The web
// surface (newTurnResult) then receives typed_view=null and falls back to
// the ANSI-stripped 80-col text, collapsing the room's typed elements
// (banner, kv, prose paragraphs, choice→buttons) into one monospace blob.
//
// No-op when a typed view is already present (the non-binding fast path) or
// when the state has no element-array view (RenderStateTyped returns a nil
// typed view for legacy string / extends / template_file views — those are
// served as text by design). Pure render; safe to call after all post-bind
// settling (emit recursion, auto-gate) has fixed the final state/world.
func (o *Orchestrator) refreshTypedViewAfterBind(res *machine.TurnResult) {
	if res == nil || res.TypedView != nil {
		return
	}
	if _, tv, env, rr, err := o.machine.RenderStateTyped(res.NewState, res.World); err == nil && tv != nil {
		res.TypedView = tv
		res.RenderEnv = env
		res.Renderer = rr
	}
}

// LookupIntent resolves an intent definition by name scoped to the given state.
// Read-only wrapper over machine.LookupIntent for callers (the runstatus web
// surface) that need each allowed intent's slot schema without importing the
// machine package directly.
func (o *Orchestrator) LookupIntent(state app.StatePath, name string) (app.Intent, bool) {
	return o.machine.LookupIntent(state, name)
}

// StateDefaultIntent returns the resolved (import-folded) name of the given
// state's free-text sink — its `default_intent` — or "" when the state
// declares none. The web composer uses this to default its text-input box to
// the room's free-text sink (e.g. `answer` in the PRD `clarifying` room)
// rather than to an arbitrary first text-slot intent, so a typed reply routes
// the way the room author intended. Mirrors resolveDefaultIntentName: the
// authored name may be bare while the folded machine uses an import-prefixed
// key, so we resolve through the state's IntentAliases.
func (o *Orchestrator) StateDefaultIntent(state app.StatePath) string {
	st := lookupStateByPath(o.def, state)
	if st == nil {
		return ""
	}
	di := strings.TrimSpace(st.DefaultIntent)
	if di == "" {
		return ""
	}
	return resolveIntentAlias(o.def, state, st, di)
}

// CurrentView reconstructs the current state/world for a session and returns a
// read-only TurnOutcome describing it — the "what does the room look like right
// now" frame the browser asks for on load (runstatus.session.view) without
// advancing the session. It never mutates state, world, or the trace.
//
// Mode is always ModeTransitioned. When the session sits at the initial state
// the typed view is surfaced via InitialViewTyped (matching the first frame
// `kitsoki web` paints); otherwise the text is rendered via RenderState and the
// typed view is best-effort (RenderStateTyped, which returns a nil TypedView for
// non-element-array view shapes — acceptable per the contract). AllowedIntents
// is the menu for the current state.
func (o *Orchestrator) CurrentView(_ context.Context, sid app.SessionID) (*TurnOutcome, error) {
	j, err := o.loadJourney(sid)
	if err != nil {
		return nil, fmt.Errorf("current view: load journey: %w", err)
	}

	allowed := o.machine.AllowedIntents(j.State, j.World)
	allowedNames := make([]string, 0, len(allowed))
	for _, ai := range allowed {
		allowedNames = append(allowedNames, ai.Name)
	}

	out := &TurnOutcome{
		Mode:           ModeTransitioned,
		NewState:       j.State,
		AllowedIntents: allowedNames,
		TurnNumber:     j.Turn,
	}

	// Populate View (rendered text) and TypedView (structured elements).
	// TypedView is now pre-evaluated by newTurnResult before sending to the
	// browser (Sources evaluated via pongo, when: guards applied), so it is
	// safe to carry it here for element-array views. Non-element-array views
	// (extends, source, template_file) leave TypedView nil and the browser
	// falls back to the ANSI-stripped View text. See tools/runstatus/CLAUDE.md.
	if j.State == o.InitialState() {
		text, tv, env, rr, verr := o.InitialViewTyped(j.World)
		if verr != nil {
			return nil, fmt.Errorf("current view: initial view: %w", verr)
		}
		out.View = text
		out.TypedView = tv
		out.RenderEnv = env
		out.Renderer = rr
		return out, nil
	}

	// Arbitrary (non-initial) state: rendered text + optional typed view.
	if text, tv, env, rr, verr := o.machine.RenderStateTyped(j.State, j.World); verr == nil {
		out.View = text
		out.TypedView = tv
		out.RenderEnv = env
		out.Renderer = rr
	} else if text, rerr := o.machine.RenderState(j.State, j.World); rerr == nil {
		out.View = text
	} else {
		return nil, fmt.Errorf("current view: render state %q: %w", j.State, rerr)
	}
	return out, nil
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// loadJourney reconstructs the current state and world from the store.
//
// Wave 3-entry: when o.eventSink is non-nil AND o.store is nil (the pure-JSONL
// path used by kitsoki turn --trace), history is read from o.eventSink.History().
// When both o.eventSink and o.store are set (the dual-write path used by
// session continue and the TUI), SQLite remains the read source for backward-
// compat with session show, attach-session, etc.  The JSONL sink is the
// write path in both cases; this read preference keeps existing subcommands
// working until phase B removes SQLite event storage.
func (o *Orchestrator) loadJourney(sid app.SessionID) (*store.JourneyState, error) {
	// Determine initial state and world from app defaults.
	initialState := o.InitialState()
	initialWorld := o.InitialWorld()

	if o.eventSink != nil && o.sinkIsAuthority {
		// Pure-JSONL path (kitsoki turn --trace): JSONL is authoritative.
		// Read history from the in-memory slice kept by JSONLSink.
		history := o.eventSink.History()
		js, err := store.BuildJourney(o.def, initialState, initialWorld, history)
		if err != nil {
			return nil, fmt.Errorf("build journey (jsonl): %w", err)
		}
		o.seedIDEConnected(js.World)
		o.seedSessionID(js.World, sid)
		return js, nil
	}

	// Try to load from the latest snapshot first.
	snap, hasSnap, err := o.store.LatestSnapshot(sid)
	if err != nil {
		return nil, fmt.Errorf("load snapshot: %w", err)
	}

	startState := initialState
	startWorld := initialWorld
	if hasSnap {
		startState = snap.StatePath
		if err := json.Unmarshal(snap.WorldJSON, &startWorld.Vars); err != nil {
			return nil, fmt.Errorf("unmarshal snapshot world: %w", err)
		}
	}

	// Load events since the snapshot.
	history, err := o.store.LoadHistory(sid)
	if err != nil {
		return nil, fmt.Errorf("load history: %w", err)
	}

	js, err := store.BuildJourney(o.def, startState, startWorld, history)
	if err != nil {
		return nil, fmt.Errorf("build journey: %w", err)
	}
	// A snapshot at turn N means N turns have already happened. BuildJourney
	// derives js.Turn solely from the post-snapshot events it is handed, so when
	// a snapshot has no later events (e.g. RewindRoute re-baselines at turnN and
	// immediately re-dispatches) it would reset the counter to 0 and the next
	// turn would collide on (session, turn). Floor it at the snapshot turn.
	if hasSnap && js.Turn < snap.Turn {
		js.Turn = snap.Turn
	}
	o.seedIDEConnected(js.World)
	o.seedSessionID(js.World, sid)

	return js, nil
}

// parseIntentCall extracts an IntentCall from the harness's CallToolParams.
func parseIntentCall(params mcp.CallToolParams) (intent.IntentCall, error) {
	if params.Name != "transition" {
		return intent.IntentCall{}, fmt.Errorf("unexpected tool name %q (want \"transition\")", params.Name)
	}
	if params.Arguments == nil {
		return intent.IntentCall{}, fmt.Errorf("nil arguments in CallToolParams")
	}

	// Arguments may be map[string]any or need JSON round-trip.
	argsMap, err := toStringMap(params.Arguments)
	if err != nil {
		return intent.IntentCall{}, fmt.Errorf("arguments: %w", err)
	}

	intentName, _ := argsMap["intent"].(string)
	if intentName == "" {
		return intent.IntentCall{}, fmt.Errorf("missing 'intent' field in transition args")
	}

	var slots world.Slots
	if sv, ok := argsMap["slots"]; ok && sv != nil {
		slots, err = toSlots(sv)
		if err != nil {
			return intent.IntentCall{}, fmt.Errorf("slots: %w", err)
		}
	}

	confidence, _ := argsMap["confidence"].(float64)

	return intent.IntentCall{
		Intent:     intentName,
		Slots:      slots,
		Confidence: confidence,
	}, nil
}

// toStringMap converts an interface{} to map[string]any via JSON round-trip if needed.
func toStringMap(v any) (map[string]any, error) {
	if m, ok := v.(map[string]any); ok {
		return m, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// toSlots converts an interface{} to world.Slots.
func toSlots(v any) (world.Slots, error) {
	m, err := toStringMap(v)
	if err != nil {
		return nil, err
	}
	return world.Slots(m), nil
}

// slotsToMap converts world.Slots to map[string]any.
func slotsToMap(s world.Slots) map[string]any {
	if s == nil {
		return make(map[string]any)
	}
	m := make(map[string]any, len(s))
	for k, v := range s {
		m[k] = v
	}
	return m
}

// extractIntentName extracts the intent name from CallToolParams without erroring.
func extractIntentName(params mcp.CallToolParams) string {
	if m, ok := params.Arguments.(map[string]any); ok {
		if n, ok := m["intent"].(string); ok {
			return n
		}
	}
	return ""
}

// harnessConfidence extracts the LLM's self-reported confidence from
// CallToolParams without erroring. Returns 0 when the field is absent
// or non-numeric. Used by the EvTurnLLMRouted trace event so the
// TUI route badge can render the
// LLM's own confidence number next to the magenta ✦ chip.
func harnessConfidence(params mcp.CallToolParams) float64 {
	if m, ok := params.Arguments.(map[string]any); ok {
		if c, ok := m["confidence"].(float64); ok {
			return c
		}
	}
	return 0
}

// newOrchestratorEvent creates an orchestrator-level event.
func newOrchestratorEvent(kind store.EventKind, payload map[string]any, turn app.TurnNumber) store.Event {
	b, _ := json.Marshal(payload)
	return store.Event{
		Kind:    kind,
		Turn:    turn,
		Payload: b,
	}
}

// transitionedTurnEnd builds the turn.end payload for a successful transition.
// It records the rendered operator-facing view ("view") alongside the outcome
// and destination state, so the trace carries the deterministic room narration
// the operator saw — see the store.TurnEnded doc for why this must live in the
// trace rather than be reconstructed from the (mutable, un-pinned) story files.
// The view is omitted when empty so rejected/background turns stay lean.
//
// Presentation ANSI is stripped before recording: the view's lipgloss element
// styling (banner/heading colour) is emitted only when stdout is a colour
// terminal, so leaving it in would make the same session record different bytes
// in a TTY vs. headless — a non-deterministic trace. The zero-width
// source-color sentinels are NOT ANSI and survive the strip, so the recorded
// narration stays deterministic AND keeps its LLM/template provenance for
// consumers to re-style. See recordedView.
func transitionedTurnEnd(to app.StatePath, view string) map[string]any {
	p := map[string]any{
		"outcome": "transitioned",
		"to":      string(to),
	}
	if v := recordedView(view); v != "" {
		p["view"] = v
	}
	return p
}

// recordedView normalises a rendered view for the trace: it strips presentation
// ANSI (terminal-profile-dependent, hence non-deterministic) while preserving
// the source-color sentinels. Both trace recording boundaries — turn.end.view
// and the journal's view.rendered entry — funnel through here so the recorded
// narration is identical regardless of the color profile it was rendered under.
func recordedView(view string) string {
	return ansi.Strip(view)
}

// stampStatePathPerEvent pre-stamps StateExited and StateEntered events with
// the state they "happened in", fixing finding G5 (machine.state_entered
// carrying the FROM state instead of the TO state).
//
// Machine events carry the state path in their payload:
//   - StateExited{state: "foyer"}  → state_path = "foyer"  (the exited state)
//   - StateEntered{state: "cloakroom"} → state_path = "cloakroom" (the entered state)
//
// This must be called BEFORE stampStatePath so these events get the correct
// per-event value rather than the uniform FROM-state default.
func stampStatePathPerEvent(evs []store.Event) {
	for i := range evs {
		ev := &evs[i]
		if ev.StatePath != "" {
			continue // already set
		}
		switch ev.Kind {
		case store.StateExited, store.StateEntered:
			// Extract the "state" field from the payload.
			var p struct {
				State string `json:"state"`
			}
			if len(ev.Payload) > 0 {
				if err := json.Unmarshal(ev.Payload, &p); err == nil && p.State != "" {
					ev.StatePath = app.StatePath(p.State)
				}
			}
		}
	}
}

// stampStatePath sets StatePath on every event in evs that does not already
// have one set. Called before appendEventsAndJournal so the on-disk JSONL
// records the active state without exporter-side back-fill.
//
// Finding 2.1: when state is "" (rejection before a journey is fully built,
// e.g. when AppDef.Root is not a valid string), fall back to fallback. Pass
// o.InitialState() as fallback to ensure every event carries a non-empty
// state_path on disk.
//
// Finding G5: StateEntered and StateExited events should be pre-stamped via
// stampStatePathPerEvent before this is called, so this only fills events that
// don't have a per-event state assigned.
func stampStatePath(evs []store.Event, state, fallback app.StatePath) {
	effective := state
	if effective == "" {
		effective = fallback
	}
	for i := range evs {
		if evs[i].StatePath == "" {
			evs[i].StatePath = effective
		}
	}
}

// buildPromptRenderer constructs the prompt renderer for a story from its
// base dir and optional prompts: config. Returns nil when def has no on-disk
// base dir (LoadBytes / tests) so agent handlers fall back to the legacy
// KITSOKI_APP_DIR + render.Pongo path. Shared/overlay paths are resolved
// relative to BaseDir when not absolute. The renderer is uncached so an
// author's prompt edits take effect on the next turn without a restart, the
// same hot-reload behavior NewAppRenderer gives views.
func buildPromptRenderer(def *app.AppDef, overlayOverride string) *render.AppRenderer {
	if def == nil || def.BaseDir == "" {
		return nil
	}
	pp := render.PromptPath{Story: def.BaseDir}
	if pc := def.Prompts; pc != nil {
		for _, s := range pc.Shared {
			pp.Shared = append(pp.Shared, resolveUnderBase(def.BaseDir, s))
		}
		if pc.Overlay != "" {
			pp.Overlay = resolveUnderBase(def.BaseDir, pc.Overlay)
		}
	}
	// Expose each immediate import's prompt root as @import/<alias>/… so a
	// parent override prompt can extend the imported story's base instead of
	// swapping it wholesale (docs/stories/imports.md). SourcePath is the
	// child manifest; its dir is the child's prompt base.
	for alias, w := range def.ImportWrappers {
		if w == nil || w.SourcePath == "" {
			continue
		}
		if pp.Imports == nil {
			pp.Imports = map[string]string{}
		}
		pp.Imports[alias] = filepath.Dir(w.SourcePath)
	}
	// A run-time --prompt-overlay wins over a story-declared default. It is
	// resolved against the process cwd when relative (it's project-supplied,
	// not part of the shared story).
	if overlayOverride != "" {
		if filepath.IsAbs(overlayOverride) {
			pp.Overlay = overlayOverride
		} else if abs, err := filepath.Abs(overlayOverride); err == nil {
			pp.Overlay = abs
		} else {
			pp.Overlay = overlayOverride
		}
	}
	r, err := render.NewPromptRenderer(pp, false)
	if err != nil {
		return nil
	}
	return r
}

// resolveUnderBase joins a possibly-relative path against base; absolute paths
// pass through unchanged.
func resolveUnderBase(base, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(base, p)
}

// agentsForContext translates the app-side AgentDef map into the host-side
// Agent map used by the context shim. Returns nil when the app declares no
// agents so handlers see a clean "no agents wired" signal rather than an
// empty allocated map. Description is dropped — it's documentation-only and
// the host package doesn't need it for runtime resolution.
func agentsForContext(def *app.AppDef) map[string]host.Agent {
	if def == nil || len(def.Agents) == 0 {
		return nil
	}
	out := make(map[string]host.Agent, len(def.Agents))
	for name, a := range def.Agents {
		agent := host.Agent{
			SystemPrompt:         a.SystemPrompt,
			Model:                a.Model,
			Effort:               a.Effort,
			DefaultCwd:           a.Cwd,
			InheritClaudeDefault: a.InheritClaudeDefault,
			Provider:             a.Provider,
		}
		if len(a.Tools) > 0 {
			agent.Tools = append([]string(nil), a.Tools...)
		}
		if a.BashProfile != nil {
			agent.BashProfile = convertBashProfile(a.BashProfile)
		}
		if a.ExternalSideEffect != nil {
			v := *a.ExternalSideEffect
			agent.ExternalSideEffect = &v
		}
		out[name] = agent
	}
	return out
}

// providersForContext translates the app-side ProviderDecl map into the
// host-side Provider map injected per dispatch (via host.WithProviders) so
// agent handlers can resolve an agent's Provider / an effect's `provider:` arg
// to its env overrides + default model. Returns nil when the app declares no
// providers so handlers see a clean "no providers wired" signal.
func providersForContext(def *app.AppDef) map[string]host.Provider {
	if def == nil || len(def.Providers) == 0 {
		return nil
	}
	out := make(map[string]host.Provider, len(def.Providers))
	for name, p := range def.Providers {
		if p == nil {
			continue
		}
		prov := host.Provider{Model: p.Model, Effort: p.Effort}
		if len(p.Env) > 0 {
			prov.Env = make(map[string]string, len(p.Env))
			for k, v := range p.Env {
				prov.Env[k] = v
			}
		}
		out[name] = prov
	}
	return out
}

// projectContextFor translates the app's Layer-2 authoring fields (app.context
// / context_path) into the host-side ProjectContext injected per dispatch, so
// every agent call composes the project grounding into its system prompt. A
// nil def or an app with neither field set yields a zero ProjectContext (the
// host then falls back to the prompts/_project.md convention, then to no
// project layer).
func projectContextFor(def *app.AppDef) host.ProjectContext {
	if def == nil {
		return host.ProjectContext{}
	}
	return host.ProjectContext{
		Inline: def.App.Context,
		Path:   def.App.ContextPath,
	}
}

// convertBashProfile translates an app-layer BashProfileDecl into the
// host-layer BashProfile. The two types are structurally identical; the
// separation keeps the host package free of an app import.
// errorBannerFormat is the single, consistent shape for the on_error redirect
// banner the runtime appends when a redirected room does not surface the
// failure itself. Kept as one format so every story bounce looks identical.
const errorBannerFormat = "⚠ Action failed: %s"

func appendErrorBanner(view, msg string) string {
	banner := fmt.Sprintf(errorBannerFormat, msg)
	if view == "" {
		return banner
	}
	return view + "\n\n" + banner
}

func convertBashProfile(d *app.BashProfileDecl) *host.BashProfile {
	if d == nil {
		return nil
	}
	p := &host.BashProfile{}
	switch d.Kind {
	case app.BashProfileReadOnly:
		p.Kind = host.BashProfileReadOnly
	case app.BashProfileCommands:
		p.Kind = host.BashProfileCommands
		if len(d.Commands) > 0 {
			p.Commands = append([]string(nil), d.Commands...)
		}
	case app.BashProfileSandboxWrite:
		p.Kind = host.BashProfileSandboxWrite
		p.ScratchDir = d.ScratchDir
	}
	return p
}
