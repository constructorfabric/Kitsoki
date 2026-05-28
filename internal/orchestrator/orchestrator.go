// Package orchestrator implements the turn-loop brain (§4.2).
// It is the ONLY component that calls store.AppendEvents.
// The machine is pure (no I/O); the harness may call the LLM.
package orchestrator

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"kitsoki/internal/app"
	"kitsoki/internal/chats"
	"kitsoki/internal/clock"
	"kitsoki/internal/expr"
	"kitsoki/internal/harness"
	"kitsoki/internal/host"
	"kitsoki/internal/inbox"
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
// is collecting missing slots from the user (§5.3 option a: in-memory).
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

	// chatStore is the SQLite-backed chat store used by chat-aware oracle handlers
	// and the host.chat.* built-ins. Optional; nil disables chat persistence.
	chatStore host.ChatStore

	// roomEnterSink, when non-nil, receives a pre-rendered banner string
	// every time a turn transitions into a new room (top-level state).
	// Fired AFTER the machine collects on_enter side-effects but BEFORE
	// host calls dispatch, so the banner lands in the TUI transcript
	// before any oracle / Bash / etc. tool-use breadcrumbs from the
	// on_enter chain stream in. Optional; nil disables the hook.
	roomEnterSink RoomEnterSink

	// chatsConcrete is the concrete *chats.Store, set when callers want the
	// continue-mode resume path to surface pending drives and backgrounded PTY
	// chats. Distinct from chatStore (the host-interface flavour) because the
	// resume reads need methods (ListDrivesBySession, ListPTYForHost) that
	// aren't on host.ChatStore. Optional; nil disables the surfacing.
	chatsConcrete *chats.Store

	// journalWriter is the durable journal writer (continue-mode §4.9 Rule 1).
	// When nil, callers fall through to the legacy AppendEvents path.
	// Set via WithJournalWriter; individual turn-write call sites are migrated
	// by the next agent.
	journalWriter journal.Writer

	// journalReader is the read-side counterpart to journalWriter, used by the
	// AttachSession resume path (continue-mode §4.5).  When nil, AttachSession
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

	// matcher is the per-app semantic-routing index (semantic-routing
	// proposal §2.1 / Phase 2). Compiled lazily on first
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

	// cache is the optional turn-result cache (semantic-routing
	// proposal §2.2 + §7). Wired via WithTurnCache; nil disables
	// the cache tier entirely. The cache is per-orchestrator (and
	// therefore per-app), so InvalidateOtherHashes / SweepCold /
	// TrimLRU run at most once per orchestrator on first turn —
	// see internal/orchestrator/cache.go.
	cache          turncache.Cache
	cacheSweepOnce sync.Once
	appHashOnce    sync.Once
	appHashValue   string
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
	// Construct the timeout dispatcher.  Persistence is enabled iff the
	// orchestrator has a store with a *sql.DB attached (the production case);
	// pure-memory test rigs default to in-memory tracking.
	var db *sql.DB
	if s != nil {
		db = s.DB()
	}
	td, tdErr := newTimeoutDispatcher(o.clk, db, o.logger)
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

// Option is a functional option for Orchestrator.
type Option func(*Orchestrator)

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

// WithChatStore wires a host.ChatStore so that chat-aware oracle calls and the
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
// (continue-mode §4.9 Rule 1). When nil (the default), turn writes fall through
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
	state := lookupStateByPath(o.def, journey.State)
	if state == nil || len(state.OnEnter) == 0 {
		return nil
	}

	resolved, newWorld, hostCalls, _, effEvents, runErr := o.machine.RunEffectsAndState(ctx, journey.State, journey.World, state.OnEnter)
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
	jEntries := journalEntriesForEvents(sid, 0, time.Now(), events, journey.World, newWorld, "", resolved, "")
	if appendErr := o.store.AppendEventsAndJournal(sid, events, jEntries); appendErr != nil {
		return fmt.Errorf("orchestrator: RunInitialOnEnter: append events: %w", appendErr)
	}
	return nil
}

// startSessionListener subscribes to terminal job events for sid and routes
// them to handleJobTerminal in a background goroutine. The goroutine exits
// when the cancel func stored in cancelListeners is called.
func (o *Orchestrator) startSessionListener(sid app.SessionID) {
	listenerCtx, cancel := context.WithCancel(context.Background())
	o.mu.Lock()
	o.cancelListeners[sid] = cancel
	o.mu.Unlock()

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
// Steps (§4.2):
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

	// Semantic routing tier (semantic-routing proposal §1 Phase 2):
	// run BEFORE acquiring the session lock so TrySemantic's own
	// SubmitDirect call can take the lock without deadlocking. We
	// skip the semantic tier when the caller passed supplemental
	// slots — that path explicitly wants the LLM to classify the
	// utterance so the supplements can be merged into a properly
	// typed call. Routing-disabled apps short-circuit inside
	// TrySemantic.
	if len(cfg.supplementSlots) == 0 {
		if outcome, hit, semErr := o.TrySemantic(ctx, sid, input); semErr != nil {
			return nil, semErr
		} else if hit {
			return outcome, nil
		}
		// Turn-cache tier (semantic-routing proposal §5.4): after
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

	// Append TurnStarted event.
	startEvent := newOrchestratorEvent(store.TurnStarted, map[string]any{
		"turn":  int64(turnNum),
		"input": input,
	}, turnNum)

	// 3. Call harness.
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
			msg := strings.TrimSpace(clarify.Message)
			if msg == "" {
				msg = "The router didn't understand. Try rephrasing or pick an action from the menu."
			}
			return &TurnOutcome{
				Mode:         ModeRejected,
				NewState:     journey.State,
				ErrorCode:    intent.ErrorCode("LLM_CLARIFICATION"),
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
	// Semantic-routing proposal §1 / Phase 5 hook: emit a stable
	// `turn.llm_routed` breadcrumb so the future turncache writeback
	// (and the TUI route badges in §8) have a deterministic place to
	// observe LLM-resolved turns. The field schema is locked by §8 —
	// don't rename intent/confidence/state_path/model. The model
	// name is empty in Phase 2: the harness owns its model choice
	// and a future hook will plumb the resolved model up here.
	tl.Debug(ctx, trace.EvTurnLLMRouted,
		slog.String("intent", extractIntentName(params)),
		slog.Float64("confidence", harnessConfidence(params)),
		slog.String("model", ""),
	)

	// Append LLMCalled/LLMToolCall events.
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

	// Build a prefix of orchestrator-level events.
	prefix := []store.Event{startEvent, llmEvent}

	// 6. React to the result.
	if result.ValidationError != nil {
		ve := result.ValidationError
		switch ve.Code {
		case intent.ErrMissingSlots:
			// Do NOT persist events for clarify-required outcomes (§4.2 step 4).
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
			// INTENT_NOT_ALLOWED, GUARD_FAILED, etc.: persist the failure events.
			failureEvents := append(prefix, result.Events...)
			endEvent := newOrchestratorEvent(store.TurnEnded, map[string]any{
				"outcome": "rejected",
				"code":    string(ve.Code),
			}, turnNum)
			failureEvents = append(failureEvents, endEvent)

			// Site 1: dual-write journal entries for the rejection turn.
			jEntries := journalEntriesForEvents(sid, turnNum, time.Now(), failureEvents,
				journey.World, journey.World, "", journey.State, input)
			if appendErr := o.store.AppendEventsAndJournal(sid, failureEvents, jEntries); appendErr != nil {
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
	// Inject the turn number for oracle journal entries (§ oracle tracing).
	ctxWithTurn := host.WithOracleCallCtx(ctx, host.OracleCallCtx{
		SessionID: sid,
		Turn:      turnNum,
		StatePath: result.NewState,
	})
	hostEvents, hostWorld, hostView, hostRedirect, hostErr := o.dispatchHostCalls(ctxWithTurn, sid, result.HostCalls, result.World, result.NewState)
	if hostErr != nil {
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
	}

	// Post-bind emit_intent dispatch (see settlePostBindEmits doc).
	var harnessErrMsg string
	if hostRedirect == "" && result.ValidationError == nil {
		harnessErrMsg = o.settlePostBindEmits(ctx, sid, &result, tl, 0)
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
	endEvent := newOrchestratorEvent(store.TurnEnded, map[string]any{
		"outcome": "transitioned",
		"to":      string(result.NewState),
	}, turnNum)
	successEvents = append(successEvents, endEvent)

	// Stamp turn number on all events.
	for i := range successEvents {
		successEvents[i].Turn = turnNum
	}

	// Site 2: dual-write journal entries for the success turn.
	jEntries := journalEntriesForEvents(sid, turnNum, time.Now(), successEvents,
		journey.World, result.World, result.View, result.NewState, input)
	if appendErr := o.store.AppendEventsAndJournal(sid, successEvents, jEntries); appendErr != nil {
		return nil, fmt.Errorf("orchestrator: append events: %w", appendErr)
	}

	tl.Debug(ctx, trace.EvTurnPersisted,
		slog.Int("count", len(successEvents)),
		slog.String("outcome", "transitioned"),
	)

	// Cache writeback (semantic-routing proposal §5.4): record this
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

	emState, emWorld, emHostCalls, emSay, emEvents, emErr := o.machine.DispatchPostBindEmits(ctx, res.NewState, res.World)
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
				if emSay != "" {
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
	// template needs to be hardened — see docs/story-style.md §3.5.
	v, rErr := o.machine.RenderState(res.NewState, res.World)
	if rErr != nil {
		slog.Warn("orchestrator.render_after_bind_failed",
			"state", string(res.NewState), "err", rErr.Error())
	} else if v != "" {
		if emSay != "" {
			res.View = emSay + "\n\n" + v
		} else {
			res.View = v
		}
	}
	return ""
}

// dispatchHostCalls invokes each HostInvocation, applies bindings to world,
// and re-renders the view. Returns the new events, the updated world, the
// refreshed view (empty if no changes), an override state path (non-empty
// when an `on_error:` arc fires and the caller must redirect to the error
// state), and an error only when re-rendering fails.
//
// Individual handler failures without `on_error:` are folded into
// world.last_error and emitted as HostReturned events with error payloads —
// they do not stop dispatch of the remaining calls.  When an
// `on_error:` arc IS declared on the failing host call, dispatch of the
// remaining calls in the same on_enter block is aborted and the named error
// state is entered: its on_enter chain runs (including any host calls it
// emits), a TransitionApplied event is appended so replay restores the
// redirected state, and the view is rendered from the error state.
//
// When o.hosts is nil (deterministic flow tests), returns no events and the
// original world unchanged.
func (o *Orchestrator) dispatchHostCalls(ctx context.Context, sid app.SessionID, calls []machine.HostInvocation, w world.World, state app.StatePath) ([]store.Event, world.World, string, app.StatePath, error) {
	if o.hosts == nil || len(calls) == 0 {
		return nil, w, "", "", nil
	}

	if o.transports != nil {
		ctx = transport.WithRegistry(ctx, o.transports)
	}
	if o.jobStore != nil {
		ctx = host.WithClarificationAnswerer(ctx, o.jobStore)
		// Wire host.inbox.add to JobStore.InsertNotification.  The
		// adapter is per-(orchestrator, session) and is rebuilt for
		// each dispatch — cheap (two pointer copies) and avoids
		// holding a stale session ID across long-lived orchestrator
		// references.  Without this seam every host.inbox.add call
		// returns persisted:false and the notification is dropped.
		// (P1-C from the dev-story-bugfix-unify Opus review.)
		ctx = host.WithInboxAdder(ctx, inbox.NewJobStoreAdder(o.jobStore, sid))
	}
	if o.chatStore != nil {
		ctx = host.WithChatStore(ctx, o.chatStore)
	}
	// Inject the agents map so host.oracle.* invocations can resolve
	// `with: { agent: <name> }` references to a host.Agent value. Built
	// once per dispatch (cheap — translation is tag-equivalent).
	ctx = host.WithAgents(ctx, agentsForContext(o.def))

	// Inject journal writer and oracle call context so oracle handlers can
	// write KindOracleCall journal entries without importing the orchestrator.
	if o.journalWriter != nil {
		ctx = host.WithOracleJournalWriter(ctx, o.journalWriter)
	}
	// OracleCallCtx carries session/turn/state for journal Entry metadata.
	// Turn is not directly available here (it lives in the Turn() local), so
	// we inject a best-effort value of 0 when not provided; callers that
	// inject the turn via WithOracleCallCtx before dispatchHostCalls override
	// this default.
	if existing := host.OracleCallCtxFrom(ctx); existing.SessionID == "" {
		ctx = host.WithOracleCallCtx(ctx, host.OracleCallCtx{
			SessionID: sid,
			StatePath: state,
		})
	}

	var events []store.Event
	applied := false
	var redirect app.StatePath

	for _, hc := range calls {
		// Background invocations go to the scheduler; foreground go to the host registry.
		if hc.Background && o.scheduler != nil {
			bgEvents, bgWorld, bgErr := o.dispatchBackground(ctx, sid, state, hc, w)
			if bgErr != nil {
				o.logger.ErrorContext(ctx, trace.EvJobError,
					slog.String("session_id", string(sid)),
					slog.String("namespace", hc.Namespace),
					slog.String("phase", "dispatch_background"),
					slog.String("err", bgErr.Error()),
				)
				w.Vars["last_error"] = bgErr.Error()
			} else {
				w = bgWorld
			}
			events = append(events, bgEvents...)
			applied = true
			continue
		}

		// Re-render RawWith against the current world so downstream
		// effects in the same `on_enter:` block see prior binds.  Falls
		// back to hc.Args if RawWith isn't set (older HostInvocation
		// instances or test stubs).  See the corresponding machine-side
		// note on HostInvocation.RawWith.
		invokeArgs, fellBack := rerenderHostArgs(hc, w)

		// HostDispatched records the *actual* args the handler is about
		// to receive (post-rerender), so the event trace is honest even
		// when rerenderHostArgs had to fall back for one or more leaves.
		// Unlike HostInvoked (which snapshots pre-bind args at machine
		// time), this fires immediately before the handler is invoked.
		// Replay treats it as a no-op (see store/replay.go).
		events = append(events, newOrchestratorEvent(store.HostDispatched, map[string]any{
			"namespace":          hc.Namespace,
			"args":               invokeArgs,
			"rerender_fell_back": fellBack,
			"background":         hc.Background,
		}, 0))

		res, err := o.hosts.Invoke(ctx, hc.Namespace, invokeArgs)
		if err != nil {
			// Infrastructure failure (e.g. handler not registered): record and move on.
			w.Vars["last_error"] = err.Error()
			events = append(events, newOrchestratorEvent(store.HostReturned, map[string]any{
				"namespace": hc.Namespace,
				"error":     err.Error(),
			}, 0))
			applied = true
			// Honour on_error even on infrastructure failure: the
			// app-author's intent is "if this host call doesn't succeed,
			// route here", and "never registered" is a stronger failure
			// than a non-zero exit.  Stop processing further calls.
			if hc.OnError != "" {
				o.logger.DebugContext(ctx, trace.EvHostOnErrorRedirect,
					slog.String("session_id", string(sid)),
					slog.String("namespace", hc.Namespace),
					slog.String("from", string(state)),
					slog.String("to", hc.OnError),
					slog.String("error", err.Error()),
					slog.String("phase", "infra"),
				)
				redirect = app.StatePath(hc.OnError)
				break
			}
			continue
		}
		if res.Error != "" {
			w.Vars["last_error"] = res.Error
		}

		// Emit one EffectApplied event per binding so replay reconstructs
		// the final world deterministically from the event log.
		//
		// `dkey` is either a dot-separated path (e.g. `submitted.names[0]`)
		// resolved against res.Data, or — when it contains `{{` — an
		// expr-lang template rendered against an env that exposes res.Data
		// as the `result` root plus the current (post-prior-binds) world.
		// The template form lets authors derive values at bind time without
		// a follow-up subprocess (e.g.
		// `party_names: "{{ join(result.submitted.names, ',') }}"`).
		bindEnv, hasBindEnv := hc.Env.(expr.Env)
		for wkey, dkey := range hc.Bind {
			var (
				val any
				ok  bool
			)
			if containsTemplate(dkey) {
				if !hasBindEnv {
					continue
				}
				bindEnv.World = w.Vars
				bindEnv.Result = res.Data
				rendered, err := expr.RenderValue(dkey, bindEnv)
				if err != nil {
					o.logger.WarnContext(ctx, trace.EvHostBindError,
						slog.String("session_id", string(sid)),
						slog.String("namespace", hc.Namespace),
						slog.String("bind_key", wkey),
						slog.String("template", dkey),
						slog.String("err", err.Error()),
					)
					continue
				}
				val = rendered
				ok = true
			} else {
				if res.Data == nil {
					continue
				}
				val, ok = lookupBindPath(res.Data, dkey)
				if !ok {
					continue
				}
			}
			w.Vars[wkey] = val
			events = append(events, newOrchestratorEvent(store.EffectApplied, map[string]any{
				"set": map[string]any{wkey: val},
			}, 0))
			applied = true
		}

		payload := map[string]any{"namespace": hc.Namespace}
		if res.Error != "" {
			payload["error"] = res.Error
		}
		if res.Data != nil {
			payload["data"] = res.Data
		}
		events = append(events, newOrchestratorEvent(store.HostReturned, payload, 0))

		// If the call failed and the author declared an `on_error:` arc,
		// abort dispatch of the remaining calls in this on_enter block
		// and route to the error state.  This is what makes pass/fail
		// host scripts (the bugfix room's verifier, deploy, etc.)
		// actually block the pipeline instead of silently advancing.
		if res.Error != "" && hc.OnError != "" {
			o.logger.DebugContext(ctx, trace.EvHostOnErrorRedirect,
				slog.String("session_id", string(sid)),
				slog.String("namespace", hc.Namespace),
				slog.String("from", string(state)),
				slog.String("to", hc.OnError),
				slog.String("error", res.Error),
				slog.String("phase", "domain"),
			)
			redirect = app.StatePath(hc.OnError)
			break
		}
	}

	if redirect != "" {
		// Run the error state's on_enter chain and recursively dispatch
		// any host calls it emits.  Append a TransitionApplied event so
		// replay correctly lands the journey in the error state after a
		// process restart.  resolvedRedirect captures the emit_intent-
		// resolved leaf when the error state's on_enter chain emitted
		// onward (P1-D); when no emit fired it equals `redirect`.
		errEvents, errWorld, errView, resolvedRedirect, redirErr := o.enterRedirectState(ctx, sid, state, redirect, w)
		if redirErr != nil {
			// Even on cap-fire / infra error, enterRedirectState may have
			// produced events (notably the HarnessError carrying
			// reason=on_error.depth_cap_exceeded). Append them so the
			// failure surfaces in the persisted journal — without this
			// the SubmitDirect/Turn caller still gets a clean turn-end
			// event sequence but the operator-visible diagnostic
			// vanishes.
			events = append(events, errEvents...)
			return events, errWorld, "", "", redirErr
		}
		events = append(events, errEvents...)
		w = errWorld
		applied = true
		if errView == "" {
			// Fallback: render the resolved error state's view against the
			// post-on_enter world so callers always have a refreshed
			// view to show the user.
			v, rErr := o.machine.RenderState(resolvedRedirect, w)
			if rErr != nil {
				return events, w, "", "", fmt.Errorf("orchestrator: render redirect state %q: %w", resolvedRedirect, rErr)
			}
			errView = v
		}
		return events, w, errView, resolvedRedirect, nil
	}

	if !applied {
		return events, w, "", "", nil
	}

	view, err := o.machine.RenderState(state, w)
	if err != nil {
		return events, w, "", "", fmt.Errorf("orchestrator: re-render after host dispatch: %w", err)
	}
	// A nil-error empty view is the silent dead-end mode the user
	// witnessed on 2026-05-20: the post-bind RenderState returned
	// ("", nil) and result.View ended up empty (the initial machine.Turn
	// render skipped because the on_enter chain had host calls that
	// would bind). Surface the unusual case to the trace so a future
	// occurrence is diagnosable from the trace alone.
	if view == "" {
		o.logger.Warn("orchestrator.post_bind_render_empty",
			slog.String("state", string(state)),
			slog.Int("world_keys", len(w.Vars)),
		)
	}
	return events, w, view, "", nil
}

// redirectDepthKey is the context key holding the current
// on_error-redirect recursion depth for the active turn-side host
// dispatch. enterRedirectState increments it on each recursion and
// surfaces a HarnessError when it exceeds the cap; values at the
// top-level dispatch entry are zero.
type redirectDepthKey struct{}

func withRedirectDepth(ctx context.Context, d int) context.Context {
	return context.WithValue(ctx, redirectDepthKey{}, d)
}

func redirectDepthFromCtx(ctx context.Context) int {
	if v, ok := ctx.Value(redirectDepthKey{}).(int); ok {
		return v
	}
	return 0
}

// enterRedirectState runs the on_enter chain for the named error state and
// recursively dispatches any host calls it emits.  Used by dispatchHostCalls
// to land the session in the on_error: target after a host failure.
//
// Emits a TransitionApplied event (from prior → target) so the replayer
// updates the journey state, plus StateExited/StateEntered events to mirror
// the regular machine.Turn transition shape.  Returns the accumulated
// events, the post-on_enter world, the rendered view (empty if rendering
// is left to the caller), the resolved leaf state (which may differ from
// `target` when the error state's on_enter chain emit_intented onward, or
// when a nested on_error redirected to a deeper error state), and a
// non-nil error only on infrastructure failure.
//
// Calls RunEffectsAndState (not RunEffects) so emit_intent dispatched
// inside the error state's on_enter steers the resolved leaf — without
// this the session would land at `target` even when an emit_intent has
// already routed it onward.  (P1-D from the dev-story-bugfix-unify Opus
// review.)
func (o *Orchestrator) enterRedirectState(ctx context.Context, sid app.SessionID, prior, target app.StatePath, w world.World) ([]store.Event, world.World, string, app.StatePath, error) {
	// Bound recursion depth. Each on_error redirect that runs an
	// on_enter chain whose host calls fail with another on_error: arc
	// recurses through dispatchHostCalls → enterRedirectState. Without
	// a cap, a host that fails idempotently (e.g.
	// `git worktree add` against an existing dir) loops forever:
	// idle.on_enter creates a workspace that fails → on_error: idle →
	// idle.on_enter runs again → repeat. Cap at 4 redirects per
	// turn-side dispatch. On overflow we surface a HarnessError and
	// stay at the deepest resolved state rather than infinite-looping
	// or popping back to the original on_error target.
	const maxRedirectDepth = 4
	depth := redirectDepthFromCtx(ctx)
	if depth > maxRedirectDepth {
		o.logger.WarnContext(ctx, "orchestrator.on_error.depth_cap_exceeded",
			slog.String("session_id", string(sid)),
			slog.String("prior", string(prior)),
			slog.String("target", string(target)),
			slog.Int("depth", depth),
			slog.Int("cap", maxRedirectDepth),
		)
		ev := newOrchestratorEvent(store.HarnessError, map[string]any{
			"reason":  "on_error.depth_cap_exceeded",
			"prior":   string(prior),
			"target":  string(target),
			"depth":   depth,
			"cap":     maxRedirectDepth,
			"message": "on_error redirect chain exceeded depth cap; staying at the originating state. A host call's on_error: arc is looping (likely the same host failing repeatedly).",
		}, 0)
		return []store.Event{ev}, w, "", prior, fmt.Errorf("orchestrator: on_error redirect from %q to %q exceeded depth cap %d — host call's on_error chain is looping", prior, target, maxRedirectDepth)
	}
	ctx = withRedirectDepth(ctx, depth+1)

	// Resolve `../`-relative on_error targets against the prior state
	// path. The import rewriter (internal/app/imports.go) rewrites a
	// bare-name on_error target like `ticket_search` declared inside an
	// imported sub-story to `../ticket_search` so it resolves to a
	// sibling of the import wrapper. lookupStateByPath only understands
	// flat dotted paths, so without resolving the `..` first the
	// redirect would always fail with "on_error target state not found"
	// for any import-folded room with an `on_error:` arc — which is
	// every dev-story room with an on_enter invoke.
	target = app.StatePath(resolveOnCompleteTarget(string(prior), string(target)))

	// Validate target exists; if not, surface as an infrastructure error.
	tgtState := lookupStateByPath(o.def, target)
	if tgtState == nil {
		return nil, w, "", target, fmt.Errorf("orchestrator: on_error target state %q not found", target)
	}

	// Self-redirect: the on_error arc points back at the current room.
	// Re-firing on_enter would re-invoke the host call that just failed,
	// land here again, and loop. Treat this as "stay in place, surface
	// the failure via last_error" and return without re-running on_enter.
	// Authors writing `on_error: <self>` mean "don't bail out" — not
	// "re-enter and try again forever".
	if target == prior {
		o.logger.DebugContext(ctx, "orchestrator.on_error.self_redirect_skipped",
			slog.String("session_id", string(sid)),
			slog.String("state", string(target)),
		)
		return nil, w, "", target, nil
	}

	var events []store.Event

	// TransitionApplied is the event the replayer uses to update
	// js.State, so it must be emitted for the redirect to survive a
	// process restart.
	events = append(events, newOrchestratorEvent(store.TransitionApplied, map[string]any{
		"from":   string(prior),
		"to":     string(target),
		"intent": "on_error",
	}, 0))

	// Mirror the StateExited/StateEntered shape that machine.Turn emits
	// for a regular transition.  Single-level paths only — compound
	// state hierarchies are handled as a flat exit/enter pair, which
	// matches the on_error: arc's flat-target contract.
	events = append(events, newOrchestratorEvent(store.StateExited, map[string]any{
		"state": string(prior),
	}, 0))
	events = append(events, newOrchestratorEvent(store.StateEntered, map[string]any{
		"state": string(target),
	}, 0))

	resolved := target

	// Run the error state's on_enter via the machine.  This collects
	// any nested host calls so we can recurse below.  RunEffectsAndState
	// also returns the leaf state path the chain settled at — if the
	// error state's on_enter contains an emit_intent that fires at
	// machine time, the resolved leaf differs from target and the
	// orchestrator must surface it as the post-redirect state.
	if len(tgtState.OnEnter) > 0 {
		emitState, newWorld, hostCalls, _, effEvents, runErr := o.machine.RunEffectsAndState(ctx, target, w, tgtState.OnEnter)
		if runErr != nil {
			return events, w, "", target, fmt.Errorf("orchestrator: run on_enter for redirect %q: %w", target, runErr)
		}
		w = newWorld
		events = append(events, effEvents...)
		if emitState != "" && emitState != target {
			resolved = emitState
		}

		// Recursively dispatch.  A nested on_error redirect supersedes
		// this one — the caller will see the deepest target.
		if len(hostCalls) > 0 {
			nestedEvents, nestedWorld, nestedView, nestedRedirect, nestedErr := o.dispatchHostCalls(ctx, sid, hostCalls, w, resolved)
			if nestedErr != nil {
				// Propagate nested events even on error so the
				// HarnessError emitted by a deeper cap-fire reaches
				// the persisted journal. Mirrors the outer
				// dispatchHostCalls branch.
				events = append(events, nestedEvents...)
				return events, nestedWorld, "", resolved, nestedErr
			}
			events = append(events, nestedEvents...)
			w = nestedWorld
			if nestedRedirect != "" {
				// A deeper on_error fired; emit one more
				// TransitionApplied so replay lands at the
				// deepest target, but otherwise let the
				// nested events already capture the chain.
				events = append(events, newOrchestratorEvent(store.TransitionApplied, map[string]any{
					"from":   string(resolved),
					"to":     string(nestedRedirect),
					"intent": "on_error",
				}, 0))
				return events, w, nestedView, nestedRedirect, nil
			}
			return events, w, nestedView, resolved, nil
		}
	}

	return events, w, "", resolved, nil
}

// rerenderHostArgs re-renders the templates in hc.RawWith against the current
// world snapshot so a host call that runs after an earlier bind in the same
// `on_enter:` block sees the post-bind values.
//
// Falls back to the up-front-resolved hc.Args when:
//   - RawWith is empty (no templates to re-render)
//   - hc.Env is not the expected expr.Env type (older code paths or stubs)
//
// On a *leaf* template-render error the leaf is replaced with the
// corresponding pre-resolved leaf from hc.Args (per-leaf fallback), so a
// single bad nested template no longer poisons the entire `with:` block —
// the rest of the leaves still see the post-bind world.  Returns the
// rerendered args plus a fellBack flag that is true iff any leaf fell back
// (used by HostDispatched to make the diagnostic story honest).
//
// This keeps the behaviour compatible with code that doesn't supply RawWith
// while letting the bugfix room's 2-step `on_enter:` pattern compose
// cleanly.  See `internal/machine/machine.go` HostInvocation for the
// machine-side contract.
func rerenderHostArgs(hc machine.HostInvocation, w world.World) (map[string]any, bool) {
	if len(hc.RawWith) == 0 {
		return hc.Args, false
	}
	env, ok := hc.Env.(expr.Env)
	if !ok {
		return hc.Args, false
	}
	// Snapshot the env with the *current* world.
	env.World = w.Vars
	out := make(map[string]any, len(hc.RawWith))
	fellBack := false
	for k, raw := range hc.RawWith {
		// Look up the up-front-resolved leaf-equivalent for this top-level
		// key so per-leaf failures inside a nested map/slice can fall back
		// to the corresponding pre-bind leaf.
		existing, hasExisting := hc.Args[k]
		resolved, leafFell, err := resolveTemplateValueLeafFallback(raw, existing, hasExisting, env)
		if err != nil {
			// Unrecoverable shape mismatch between raw and existing at
			// the top level; preserve the legacy behaviour of falling
			// back to the up-front-resolved value for this key.
			if hasExisting {
				out[k] = existing
			} else {
				out[k] = raw
			}
			fellBack = true
			continue
		}
		if leafFell {
			fellBack = true
		}
		out[k] = resolved
	}
	return out, fellBack
}

// resolveTemplateValueLeafFallback recurses into maps/slices and renders any
// string that looks like an expr-lang template.  On a leaf-template render
// error it falls back to the corresponding leaf from `existing` (the
// up-front-resolved value for this position), if one exists and has a
// matching shape.  The returned bool is true iff any leaf in the subtree
// fell back to its pre-bind value.
//
// The shape-matching rule is:
//   - string leaf → fall back to `existing` (any type)
//   - map leaf    → recurse, matching keys against `existing` if it is a map
//   - slice leaf  → recurse, matching indices against `existing` if it is a
//     slice of the same length
//
// If shapes diverge mid-walk (e.g. raw says map, existing says string), the
// failing subtree falls back wholesale to `existing` and fellBack is set.
func resolveTemplateValueLeafFallback(v any, existing any, hasExisting bool, env expr.Env) (any, bool, error) {
	switch val := v.(type) {
	case string:
		if !containsTemplate(val) {
			return val, false, nil
		}
		r, err := expr.RenderValue(val, env)
		if err != nil {
			if hasExisting {
				return existing, true, nil
			}
			// No pre-bind leaf available; keep raw so the handler sees
			// the un-rendered template rather than nil.
			return val, true, nil
		}
		return r, false, nil
	case map[string]any:
		exMap, _ := existing.(map[string]any)
		out := make(map[string]any, len(val))
		fell := false
		for k, vv := range val {
			var (
				exVal any
				exOK  bool
			)
			if exMap != nil {
				exVal, exOK = exMap[k]
			}
			r, f, err := resolveTemplateValueLeafFallback(vv, exVal, exOK, env)
			if err != nil {
				return nil, fell, err
			}
			if f {
				fell = true
			}
			out[k] = r
		}
		return out, fell, nil
	case []any:
		exSlice, _ := existing.([]any)
		out := make([]any, len(val))
		fell := false
		for i, vv := range val {
			var (
				exVal any
				exOK  bool
			)
			if exSlice != nil && i < len(exSlice) {
				exVal, exOK = exSlice[i], true
			}
			r, f, err := resolveTemplateValueLeafFallback(vv, exVal, exOK, env)
			if err != nil {
				return nil, fell, err
			}
			if f {
				fell = true
			}
			out[i] = r
		}
		return out, fell, nil
	default:
		return v, false, nil
	}
}

// resolveTemplateValue mirrors machine.resolveEffectValue but lives here
// so the orchestrator's late re-render doesn't need to import machine
// internals.  Recurses into maps and slices and renders any string that
// looks like an expr-lang template.  Kept for callers that don't have a
// pre-bind fallback value; rerenderHostArgs uses the leaf-fallback variant
// above instead.
func resolveTemplateValue(v any, env expr.Env) (any, error) {
	switch val := v.(type) {
	case string:
		if !containsTemplate(val) {
			return val, nil
		}
		// expr.RenderValue preserves type when the entire string is a
		// single `{{ ... }}` (e.g. a nested object); falls back to text
		// rendering for inline interpolation.
		return expr.RenderValue(val, env)
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, vv := range val {
			r, err := resolveTemplateValue(vv, env)
			if err != nil {
				return nil, err
			}
			out[k] = r
		}
		return out, nil
	case []any:
		out := make([]any, len(val))
		for i, vv := range val {
			r, err := resolveTemplateValue(vv, env)
			if err != nil {
				return nil, err
			}
			out[i] = r
		}
		return out, nil
	default:
		return v, nil
	}
}

func containsTemplate(s string) bool {
	return strings.Contains(s, "{{")
}

// lookupBindPath resolves a dot-separated key path (e.g.
// `submitted.summary_markdown` or `submitted.names[0]`) inside a host
// result's `Data` map. Returns the leaf value and true on success, or
// (nil, false) if any segment is missing or hits a non-traversable
// value. Single-segment keys (the common case) are equivalent to a
// top-level lookup.
//
// Path segments are exact map keys, with an optional trailing `[N]`
// integer index for array fields (e.g. `names[0]` → first element of
// the names slice on the current node, or chained `outer[0].inner` to
// walk into an indexed element). N must be non-negative and in range.
// Whitespace is not stripped, so app authors should keep paths tight.
func lookupBindPath(data map[string]any, path string) (any, bool) {
	if data == nil || path == "" {
		return nil, false
	}
	var cur any = data
	for _, seg := range strings.Split(path, ".") {
		key, indices, ok := parseBindSegment(seg)
		if !ok {
			return nil, false
		}
		if key != "" {
			m, ok := cur.(map[string]any)
			if !ok {
				return nil, false
			}
			cur, ok = m[key]
			if !ok {
				return nil, false
			}
		}
		for _, idx := range indices {
			arr, ok := cur.([]any)
			if !ok {
				return nil, false
			}
			if idx < 0 || idx >= len(arr) {
				return nil, false
			}
			cur = arr[idx]
		}
	}
	return cur, true
}

// parseBindSegment splits a single dot-segment into its leading key and
// any trailing [N] indices. Returns (key, indices, true) on success or
// (_, _, false) on a malformed segment. An empty key (segment starts
// with `[`) is permitted so chains like `outer.[0]` could in principle
// work — in practice authors write `outer[0]` so the leading key is
// present.
func parseBindSegment(seg string) (string, []int, bool) {
	if seg == "" {
		return "", nil, false
	}
	openIdx := strings.IndexByte(seg, '[')
	if openIdx < 0 {
		return seg, nil, true
	}
	key := seg[:openIdx]
	rest := seg[openIdx:]
	var indices []int
	for len(rest) > 0 {
		if rest[0] != '[' {
			return "", nil, false
		}
		closeIdx := strings.IndexByte(rest, ']')
		if closeIdx < 0 {
			return "", nil, false
		}
		numStr := rest[1:closeIdx]
		if numStr == "" {
			return "", nil, false
		}
		n, err := strconv.Atoi(numStr)
		if err != nil {
			return "", nil, false
		}
		indices = append(indices, n)
		rest = rest[closeIdx+1:]
	}
	return key, indices, true
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
	return o.submitDirect(ctx, sid, intentName, slots, "")
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
	return o.submitDirect(ctx, sid, intentName, slots, userInput)
}

// submitDirect is the shared implementation behind [SubmitDirect] and
// [SubmitDirectFromInput]. userInput is recorded verbatim on TurnStarted
// when non-empty; when empty we fall back to "[direct] intent=<name>".
func (o *Orchestrator) submitDirect(ctx context.Context, sid app.SessionID, intentName string, slots map[string]any, userInput string) (*TurnOutcome, error) {
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

	call := intent.IntentCall{
		Intent: intentName,
		Slots:  world.Slots(slots),
	}

	result, machineErr := o.machine.Turn(ctx, journey.State, journey.World, call)
	if machineErr != nil {
		return nil, fmt.Errorf("orchestrator: SubmitDirect: machine.Turn: %w", machineErr)
	}

	for i := range result.Events {
		result.Events[i].Turn = turnNum
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
		startEvent := newOrchestratorEvent(store.TurnStarted, map[string]any{
			"turn":   int64(turnNum),
			"input":  recordedInput,
			"direct": true,
		}, turnNum)
		failureEvents := append([]store.Event{startEvent}, result.Events...)
		endEvent := newOrchestratorEvent(store.TurnEnded, map[string]any{
			"outcome": "rejected",
			"code":    string(ve.Code),
		}, turnNum)
		failureEvents = append(failureEvents, endEvent)
		for i := range failureEvents {
			failureEvents[i].Turn = turnNum
		}
		// Site 5: dual-write journal entries for the SubmitDirect rejection turn.
		sdFailJEntries := journalEntriesForEvents(sid, turnNum, time.Now(), failureEvents,
			journey.World, journey.World, "", journey.State, journalUserInput)
		if appendErr := o.store.AppendEventsAndJournal(sid, failureEvents, sdFailJEntries); appendErr != nil {
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
	startEvent := newOrchestratorEvent(store.TurnStarted, map[string]any{
		"turn":   int64(turnNum),
		"input":  successRecordedInput,
		"direct": true,
	}, turnNum)

	hostEvents, hostWorld, hostView, hostRedirect, hostErr := o.dispatchHostCalls(ctx, sid, result.HostCalls, result.World, result.NewState)
	if hostErr != nil {
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
	}

	successEvents := append([]store.Event{startEvent}, result.Events...)
	endEvent := newOrchestratorEvent(store.TurnEnded, map[string]any{
		"outcome": "transitioned",
		"to":      string(result.NewState),
	}, turnNum)
	successEvents = append(successEvents, endEvent)
	for i := range successEvents {
		successEvents[i].Turn = turnNum
	}

	// Site 6: dual-write journal entries for the SubmitDirect success turn.
	sdSuccJEntries := journalEntriesForEvents(sid, turnNum, time.Now(), successEvents,
		journey.World, result.World, result.View, result.NewState, successJournalUserInput)
	if appendErr := o.store.AppendEventsAndJournal(sid, successEvents, sdSuccJEntries); appendErr != nil {
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

// dispatchHostCallsDetailed is the same dispatch loop as dispatchHostCalls
// but additionally returns one HostCallSummary per invocation so callers
// (currently OneShot) can surface args/data/error to the user. The events
// returned here are identical to what dispatchHostCalls would have produced.
//
// Honours `on_error:` arcs the same way dispatchHostCalls does — when a
// host call with `on_error:` declared returns Result.Error != "", dispatch
// of the remaining calls in the batch is aborted and the named error
// state is entered (its on_enter chain runs and any nested host calls are
// dispatched).  The returned `redirect` is non-empty in that case so the
// caller can override `result.NewState`.
func (o *Orchestrator) dispatchHostCallsDetailed(ctx context.Context, calls []machine.HostInvocation, w world.World, state app.StatePath) ([]HostCallSummary, []store.Event, world.World, string, app.StatePath, error) {
	if o.hosts == nil || len(calls) == 0 {
		return nil, nil, w, "", "", nil
	}

	if o.transports != nil {
		ctx = transport.WithRegistry(ctx, o.transports)
	}
	if o.chatStore != nil {
		ctx = host.WithChatStore(ctx, o.chatStore)
	}
	ctx = host.WithAgents(ctx, agentsForContext(o.def))

	summaries := make([]HostCallSummary, 0, len(calls))
	var events []store.Event
	applied := false
	var redirect app.StatePath

	for _, hc := range calls {
		// Re-render templates against the current world so chained
		// `on_enter:` host calls compose — see rerenderHostArgs above.
		invokeArgs, fellBack := rerenderHostArgs(hc, w)
		summary := HostCallSummary{Namespace: hc.Namespace, Args: invokeArgs}
		events = append(events, newOrchestratorEvent(store.HostDispatched, map[string]any{
			"namespace":          hc.Namespace,
			"args":               invokeArgs,
			"rerender_fell_back": fellBack,
			"background":         hc.Background,
		}, 0))
		res, err := o.hosts.Invoke(ctx, hc.Namespace, invokeArgs)
		if err != nil {
			summary.Error = err.Error()
			summaries = append(summaries, summary)
			w.Vars["last_error"] = err.Error()
			events = append(events, newOrchestratorEvent(store.HostReturned, map[string]any{
				"namespace": hc.Namespace,
				"error":     err.Error(),
			}, 0))
			applied = true
			if hc.OnError != "" {
				o.logger.DebugContext(ctx, trace.EvHostOnErrorRedirect,
					slog.String("namespace", hc.Namespace),
					slog.String("from", string(state)),
					slog.String("to", hc.OnError),
					slog.String("error", err.Error()),
					slog.String("phase", "infra"),
				)
				redirect = app.StatePath(hc.OnError)
				break
			}
			continue
		}
		if res.Error != "" {
			w.Vars["last_error"] = res.Error
			summary.Error = res.Error
		}
		if res.Data != nil {
			summary.Data = res.Data
		}
		summaries = append(summaries, summary)

		for wkey, dkey := range hc.Bind {
			if res.Data == nil {
				continue
			}
			val, ok := lookupBindPath(res.Data, dkey)
			if !ok {
				continue
			}
			w.Vars[wkey] = val
			events = append(events, newOrchestratorEvent(store.EffectApplied, map[string]any{
				"set": map[string]any{wkey: val},
			}, 0))
			applied = true
		}

		payload := map[string]any{"namespace": hc.Namespace}
		if res.Error != "" {
			payload["error"] = res.Error
		}
		if res.Data != nil {
			payload["data"] = res.Data
		}
		events = append(events, newOrchestratorEvent(store.HostReturned, payload, 0))

		if res.Error != "" && hc.OnError != "" {
			o.logger.DebugContext(ctx, trace.EvHostOnErrorRedirect,
				slog.String("namespace", hc.Namespace),
				slog.String("from", string(state)),
				slog.String("to", hc.OnError),
				slog.String("error", res.Error),
				slog.String("phase", "domain"),
			)
			redirect = app.StatePath(hc.OnError)
			break
		}
	}

	if redirect != "" {
		errEvents, errWorld, errView, resolvedRedirect, redirErr := o.enterRedirectState(ctx, "", state, redirect, w)
		if redirErr != nil {
			return summaries, events, w, "", "", redirErr
		}
		events = append(events, errEvents...)
		w = errWorld
		applied = true
		if errView == "" {
			v, rErr := o.machine.RenderState(resolvedRedirect, w)
			if rErr != nil {
				return summaries, events, w, "", "", fmt.Errorf("render redirect state %q: %w", resolvedRedirect, rErr)
			}
			errView = v
		}
		return summaries, events, w, errView, resolvedRedirect, nil
	}

	if !applied {
		return summaries, events, w, "", "", nil
	}
	view, err := o.machine.RenderState(state, w)
	if err != nil {
		return summaries, events, w, "", "", fmt.Errorf("re-render after host dispatch: %w", err)
	}
	return summaries, events, w, view, "", nil
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
// collected from the clarification UI (§4.2 step 4 continuation).
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
		// Rejection path: TypedView/RenderEnv/Renderer intentionally
		// omitted. The state did not transition (NewState == journey.State),
		// so the TUI keeps rendering the current room's typed view from the
		// last successful outcome. Re-emitting them here would be a no-op at
		// best and risk shadowing in-progress widget focus at worst.
		return &TurnOutcome{
			Mode:         ModeRejected,
			NewState:     journey.State,
			Events:       result.Events,
			GuardHint:    ve.GuardHint,
			ErrorCode:    ve.Code,
			ErrorMessage: ve.Message,
			TurnNumber:   turnNum,
		}, nil
	}

	// Success: dispatch host calls then persist events.
	hostEvents, hostWorld, hostView, hostRedirect, hostErr := o.dispatchHostCalls(ctx, sid, result.HostCalls, result.World, result.NewState)
	if hostErr != nil {
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
		"turn":    int64(turnNum),
		"input":   fmt.Sprintf("[clarify-continue] intent=%s", call.Intent),
		"clarify": true,
	}, turnNum)

	successEvents := append([]store.Event{startEvent}, result.Events...)
	endEvent := newOrchestratorEvent(store.TurnEnded, map[string]any{
		"outcome": "transitioned",
		"to":      string(result.NewState),
	}, turnNum)
	successEvents = append(successEvents, endEvent)

	for i := range successEvents {
		successEvents[i].Turn = turnNum
	}

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

	if appendErr := o.store.AppendEventsAndJournal(sid, successEvents, ctJEntries); appendErr != nil {
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

// RenderState renders the view template for (state, world) without touching
// the store. Thin wrapper around machine.RenderState for symmetry with
// LoadJourney.
func (o *Orchestrator) RenderState(state app.StatePath, w world.World) (string, error) {
	return o.machine.RenderState(state, w)
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// loadJourney reconstructs the current state and world from the store.
func (o *Orchestrator) loadJourney(sid app.SessionID) (*store.JourneyState, error) {
	// Determine initial state and world from app defaults.
	initialState := o.InitialState()
	initialWorld := o.InitialWorld()

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
// or non-numeric. Used by the EvTurnLLMRouted trace event (semantic-
// routing proposal §1 / §8) so the TUI route badge can render the
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
			SystemPrompt: a.SystemPrompt,
			Model:        a.Model,
			DefaultCwd:   a.Cwd,
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

// convertBashProfile translates an app-layer BashProfileDecl into the
// host-layer BashProfile. The two types are structurally identical; the
// separation keeps the host package free of an app import.
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
