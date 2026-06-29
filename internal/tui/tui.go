// Package tui implements the full-screen Bubble Tea interface.
// It composes sub-models for the location header, transcript pane,
// menu list, inbox panel, graph overlay, prompt input, and slot-fill modals.
// See docs/tui/README.md for the surface overview.
//
// The inbox panel displays background-job notifications below the Actions menu in
// the right column. It polls a *jobs.JobStore every 2 s (200 ms when a job is
// running) and supports i1–i9 key selection and teleport via the orchestrator.
package tui

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"kitsoki/internal/app"
	"kitsoki/internal/chats"
	"kitsoki/internal/clock"
	"kitsoki/internal/expr"
	"kitsoki/internal/host"
	"kitsoki/internal/inbox"
	"kitsoki/internal/intent"
	"kitsoki/internal/jobs"
	"kitsoki/internal/journal"
	"kitsoki/internal/metamode"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/render"
	"kitsoki/internal/store"
	"kitsoki/internal/trace"
	"kitsoki/internal/tui/blocks"
	"kitsoki/internal/userfacing"
	"kitsoki/internal/viz"
	"kitsoki/internal/world"
)

const githubInboxPollInterval = 5 * time.Minute

// Mode describes which interaction mode the TUI is currently in.
type Mode int

const (
	// ModeOnPath is the default on-path mode.
	ModeOnPath Mode = iota
	// ModeOffPath is the visually-framed free-form mode. See
	// docs/stories/state-machine.md §11 "Off-path: the global escape hatch".
	ModeOffPath
	// ModeSlotFilling is active while the user fills missing slots.
	ModeSlotFilling
	// ModeDisambiguating is active while the disambiguation menu is shown.
	ModeDisambiguating
	// ModeAwaitingLLM is active while the LLM harness is processing a turn.
	// Input is disabled and a spinner is shown.
	ModeAwaitingLLM
	// ModeMenu is active while the Esc-triggered system menu is on screen.
	ModeMenu
	// ModeMeta is active while a /meta overlay owns the prompt and
	// transcript (named, persistent sidebar conversation against a
	// declared meta mode). See docs/stories/meta-mode.md.
	// Replaces the former ModeEdit / edit-mode overlay (Phase B).
	ModeMeta
	// ModeMetaSessions is active while the "Meta sessions" foyer
	// overlay is on screen. Arrow keys navigate the
	// listed chats; Enter resumes one; Esc closes the overlay and
	// returns to ModeOnPath.
	ModeMetaSessions
	// ModeWorldView is active while /world's dedicated hierarchical
	// viewer owns the pane. Arrow keys move the cursor; Enter expands
	// or collapses nodes; q/Esc returns to chat.
	ModeWorldView
	// ModeChoosing is active while an inline choice widget owns the
	// keyboard focus. Arrow keys / Space / Tab / Enter drive the
	// picker; Esc cancels; a printable letter defocuses the widget
	// back to the prompt textarea so the user always has the free-
	// text escape hatch. See docs/stories/choice-widget.md §2.10
	// "Coexisting with a free-text verb".
	ModeChoosing
	// ModeOperatorQuestion is active while a dispatched agent's forwarded
	// AskUserQuestion owns the keyboard. Arrow keys move the cursor; Space
	// toggles a multi-select option; Enter confirms the current question
	// (advancing through a multi-question batch); Esc lets the agent decide
	// on its own. Unlike ModeChoosing this overlays a turn that is still
	// in flight — the agent is blocked waiting for the answer — so on commit
	// the model resumes ModeAwaitingLLM rather than returning to ModeOnPath.
	// See internal/tui/operator_question.go.
	ModeOperatorQuestion
)

// ctrlCQuitWindow is how long after a Ctrl+C the next Ctrl+C will quit
// rather than just re-clearing the prompt.
const ctrlCQuitWindow = 2 * time.Second

// promptPrefixCols and promptMaxHeight are owned by prompt.go (main's
// textarea helper module); the duplicates that lived here in the
// proposal branch's textarea swap were dropped during the rebase.

// TurnComplete is the tea.Msg sent by the orchestrator when a turn finishes.
type TurnComplete struct {
	// View is the rendered narrative from the transition.
	View string
	// Allowed is the list of currently-allowed intent names for the menu.
	Allowed []string
	// NewState is the new state path, used to highlight the graph pane.
	NewState string
}

// MenuChanged is sent when the allowed-intent list changes outside of a turn
// (e.g., after a guard re-evaluation).
type MenuChanged struct {
	Allowed []string
}

// turnOutcomeMsg wraps a TurnOutcome for delivery as a tea.Msg.
type turnOutcomeMsg struct {
	outcome *orchestrator.TurnOutcome
	input   string // the user input that triggered this turn
	err     error
}

// continueTurnOutcomeMsg wraps the result of ContinueTurn.
type continueTurnOutcomeMsg struct {
	outcome *orchestrator.TurnOutcome
	err     error
}

// RootModel is the top-level Bubble Tea model composing all sub-models.
// It uses pointer receivers so the same *RootModel can be type-asserted after
// being returned from Update as a tea.Model interface.
type RootModel struct {
	orch     *orchestrator.Orchestrator
	sid      app.SessionID
	appPath  string // path to app.yaml, needed for meta-mode orchestrator reloads ("" disables reload)
	mode     Mode
	width    int
	height   int
	quitting bool

	location         locationModel
	transcript       transcriptModel
	menu             menuModel
	inbox            inboxModel
	offPath          offPathModel
	clarify          clarifyModel
	disambiguation   disambiguationModel
	choice           choiceWidgetModel
	operatorQuestion operatorQuestionModel
	menuSystem       menuSystemModel
	metaMode         metaModel
	sessionsPanel    sessionsPanelModel
	worldView        worldViewModel
	prompt           textarea.Model

	// pendingDraft holds whatever was in the prompt textarea when an
	// interactive choice widget seized focus. Open() clears the
	// textarea so the user can't keep typing into an unresponsive
	// field; /input restores this draft so the user can resume
	// composing.
	pendingDraft string

	// routing tracks the live routing-pipeline state for the in-flight turn:
	// which tiers were tried/missed and which one won. Reset at submit time,
	// updated by RoutingTier{Miss,Hit}Msg, and finalized at turn completion.
	routing routingPipeline

	// chatStore is the persistent chat row backend; used by the
	// metamode controller to resolve / append. nil disables /meta
	// (no controller is constructed in NewRootModel).
	chatStore *chats.Store

	// metaController is the wire harness for /meta. nil disables /meta.
	// Production wires this in NewRootModel from chatStore +
	// host.AgentRegistry() + AppDef. Tests can inject a fake controller
	// via WithMetaController.
	metaController *metamode.Controller

	// metaStreamSink, when non-nil, tees streaming agent events from
	// each meta-mode Send into the transcript pane via MetaStreamMsg.
	// The sink is allocated up-front (NewMetaStreamSink) and bound to
	// the tea.Program post-construction via Attach() — mirrors the
	// RoutingObserver lifecycle (see WithRoutingObserver above).  Nil
	// leaves stream events on the slog trace only; the user falls
	// back to the buffered "agent is thinking…" UX.
	metaStreamSink *MetaStreamSink

	// operatorPrompter, when non-nil, is injected into each turn ctx so a
	// dispatched agent agent that forwards an AskUserQuestion surfaces the
	// question as an inline widget (ModeOperatorQuestion) and blocks for the
	// operator's answer. Allocated up-front (NewTUIOperatorPrompter) and bound
	// to the tea.Program post-construction via Attach() — mirrors
	// metaStreamSink's lifecycle. Nil leaves the headless posture: the agent
	// is told to proceed on its own.
	operatorPrompter *TUIOperatorPrompter

	// spatialPrompter, when non-nil, is injected into each turn ctx so a
	// dispatched oracle that requests a spatial ambient (host.SpatialPrompterFrom)
	// surfaces an OSC 8 link to a transient chrome-less `/point` window and
	// blocks the turn until the operator submits a visual bundle. Allocated
	// up-front (NewTUISpatialPrompter) and bound to the tea.Program
	// post-construction via Attach() — mirrors operatorPrompter's lifecycle. Nil
	// leaves the headless posture: no spatial ambient is requested, the turn runs
	// text-only.
	spatialPrompter *TUISpatialPrompter

	// metaStreamPending holds a pure-narration ("thinking") assistant
	// message that has streamed in but not yet been committed to the
	// transcript. A text-only assistant message is ambiguous while the
	// stream is live: it is either intermediate narration (more model
	// activity follows) or the model's FINAL answer (the terminal
	// `result` event follows). handleMetaStreamEvent defers each such
	// message by one event — flushing it as thinking the moment further
	// model activity proves it was intermediate, and DROPPING it when
	// `result` arrives first. Dropping is the point: the room (on-path)
	// or metaSendDone's AppendSystem (meta) presents the final answer,
	// so streaming it here too would duplicate it as muted thinking.
	// Always cleared at turn boundaries so it can never leak across
	// turns if the `result` stream event is lost to backpressure.
	metaStreamPending string

	// initialTypedView carries the typed-view payload for the very
	// first frame so NewRootModel can paint via AppendSystemTyped (the
	// typed-element-aware path) instead of AppendSystem (which re-runs
	// pre-rendered ANSI through Glamour and corrupts the escapes).
	// Set by WithInitialTypedView; nil when the root state's view is a
	// legacy string / extends / template_file / parallel composition.
	initialTypedView *app.View
	initialTypedEnv  expr.Env
	initialTypedRR   *render.AppRenderer

	// clk is the injectable time source used by the inbox polling ticker.
	// When nil, clock.Real() is used. Tests inject a *clock.Fake so they can
	// drive ticks deterministically without wall-clock waits.
	clk clock.Clock

	// jobStore is the SQLite-backed notification store.  When nil (headless
	// tests, serve mode) the inbox ticker is a no-op and the panel stays
	// hidden so no database is required.
	jobStore *jobs.JobStore

	// lastNotifications is the most-recent polling snapshot, used to build
	// the status-line badge without re-querying the database on every View().
	lastNotifications   []jobs.Notification
	lastGitHubInboxSync time.Time

	// minerService is the ambient miner's control seam (ad-hoc-workbench slice
	// 4). When non-nil, /mine drives pause/resume/scope/now/decide through it
	// and reads its live state. nil (the default until the runtime sibling
	// lands) leaves the surface read-only against mineStateValue and turns
	// every control verb into a "miner not wired" hint. See mine_command.go.
	minerService MinerService

	// mineStateValue is the last-pushed MineState snapshot — the proposal
	// queue + miner status the footer badge and /mine status read when no
	// service is wired (or as the post-decision echo cache when one is).
	mineStateValue MineState

	// traceHistory reads the session event history for read-only surfaces such
	// as /work. It is optional; production wires the SQLite store history and
	// studio tests can wire JSONL history. When omitted, trace-backed proposal
	// rows are simply absent and the miner-service queue remains authoritative.
	traceHistory func() (store.History, error)

	// lastCtrlC is the time the most recent Ctrl+C was pressed, used to
	// detect a double-tap quit. Zero means no recent press (or the window
	// has expired).
	lastCtrlC time.Time

	// spinner is shown while ModeAwaitingLLM is active.
	spinner spinner.Model

	// inFlightCancel cancels the running turn's context. Non-nil only while
	// ModeAwaitingLLM is active.
	inFlightCancel context.CancelFunc

	// pendingKind classifies the in-flight async work so the spinner caption
	// can describe what is actually happening (LLM routing vs. host call).
	pendingKind pendingKind

	// currentState tracks the current state path for location updates.
	currentState app.StatePath

	// lastInput remembers the input that triggered the current turn.
	lastInput string

	// inputHistory is an in-memory, per-session ring of past prompt
	// submissions (oldest at index 0, newest at the end). Up/Down arrows
	// in the main prompt walk this list. We deliberately do NOT persist
	// it to disk — history vanishes when the TUI exits.
	//
	// Slash commands ARE included (matches most interactive shells:
	// bash, zsh, fish all keep their builtins in history).
	inputHistory []string

	// historyIdx is the cursor into inputHistory while the user is
	// navigating with arrow keys. The sentinel value
	// len(inputHistory) means "not currently navigating; show the
	// in-progress draft". Any other value 0..len-1 indexes the entry
	// currently displayed in the prompt.
	historyIdx int

	// historyDraft stashes whatever the user was typing before they
	// first pressed Up, so a subsequent Down past the newest entry
	// can restore it (matches bash/readline semantics).
	historyDraft string

	// traceFile is the open trace file when /trace is active (nil otherwise).
	traceFile *os.File
	// traceWriter is the buffered writer for the trace file.
	traceWriter *bufio.Writer

	// mouseOn — removed in phase 7. Mouse support is gone (phase 5);
	// the field and the handleMouseCommand toggle are both deleted.

	// journalWriter, when non-nil, receives typed journal entries for
	// inbox read/dismiss (sites 27) and disambiguation (site 31)
	// lifecycle events (continue-mode dual-write).
	// Nil disables journal writes (back-compat default for tests).
	journalWriter journal.Writer

	// traceRing is an optional in-memory ring buffer.  When non-nil and
	// traceFileExternal is false, buildMetaTurnContext snapshots it to
	// traceFilePath on every meta-mode Send so the agent can Read the file
	// for session-history context.  Production kitsoki run uses the
	// EventSink JSONL path directly (traceFileExternal=true); the ring
	// is wired in tests that want a lightweight fallback without a real file.
	traceRing *trace.RingBuffer
	// traceFileExternal is true when traceFilePath points at a file an
	// external writer is keeping current (e.g. --trace /path.jsonl).  In
	// that case the TUI does NOT rewrite the file from the ring on each
	// meta-mode turn — it just hands the agent the path that slog is
	// already streaming to.  When false, the path is a TUI-owned temp
	// file and the ring buffer is dumped to it on every Send.
	traceFileExternal bool
	// traceFilePath is the session-scoped temp file path the TUI
	// rewrites with the ring snapshot. Empty disables the dump.
	traceFilePath string

	// sessionList caches the most recent /sessions list output so
	// /sessions attach <N> can resolve a 1-indexed position back to a
	// chat_pty_sessions row without the user typing chat IDs.
	// Replaced wholesale on every /sessions list; nil between
	// invocations.
	sessionList []chats.PtySession

	// routingObserver is the slog→tea.Msg bridge that converts
	// orchestrator routing-tier events into RoutingTier*Msg
	// deliveries — consumed by the inline routing-status block in
	// the transcript (Phase 1/2 wiring). nil disables the bridge
	// (headless / observer-less tests).
	routingObserver *RoutingObserver
	// Legacy fields removed in Phase 7:
	//   - routingChip      (chip Bubble Tea model — no longer rendered)
	//   - routingChipActive
	//   - routingHistory   (per-turn settled-line storage — the
	//                       transcript live entry is now the
	//                       authoritative record)
	//   - routingTraceOpen / routingTraceTurn (overlay state)

	// actionsAuto, when true, auto-prints the room's /intents block at
	// the end of every successful turn — single-pane-tui proposal
	// §"/intents auto on|off". Toggled by `/intents auto on` /
	// `/intents auto off`. Persists for the session. The default is
	// off; rooms may later declare an override in YAML.
	actionsAuto bool

	// backgroundCompletions is the newest-first log of bg-job /
	// other-room completions the user has been notified about but
	// not yet visited via /jump. Bounded at recentBackgroundCap so
	// the slice doesn't grow unbounded across a long session.
	backgroundCompletions []backgroundCompletion

	// inputQueue is the room-local FIFO of in-room submissions
	// captured while another turn is in flight. Esc clears the queue
	// and stashes each item back into inputHistory. Phase 4 ships
	// the queue scaffolding; phase 6 splits it per-room.
	inputQueue []string

	// transcripts is the per-room transcript buffer map. The active room's
	// transcript lives on m.transcript; on every room change the
	// outgoing buffer is saved here under its room key and the
	// incoming room's buffer is loaded back into m.transcript. See
	// rooms.go for the swap helpers (activateRoom / maybeSwitchRoomOnState)
	// and the per-room transcript-mode resolution
	// (transcriptKindForRoom).
	transcripts map[app.StatePath]transcriptModel

	// activeRoom is the top-level room key whose transcript currently
	// lives on m.transcript. Empty until the first room-aware swap
	// fires (which is also the first state-change after construction);
	// once non-empty it tracks the on-path top-level segment, or
	// metaRoomKey while a /meta overlay owns the pane.
	activeRoom app.StatePath

	// resumed is set by the resume options (WithResumedJourney /
	// WithResumedTranscript) so construction can suppress start-of-session
	// boilerplate — chiefly the welcome banner, which is already in the
	// prior session's scrollback and otherwise re-appears mid-transcript
	// (inheriting the preceding agent body's styling) on --continue.
	resumed bool

	// ideLink is the process-lifetime IDE connection the `/ide` command
	// starts and stops. nil until the first `/ide connect` succeeds; once
	// non-nil it is the same handle pushed onto the orchestrator via
	// SetIDELink so per-turn host.ide.* dispatch resolves it. The TUI also
	// reads it directly at turn-submit to capture the active selection as
	// ambient context (handleSubmit → captureIDEAmbient). Held as the
	// host.IDELink interface plus the lifecycle methods the command needs
	// (Candidates/ConnectLock/Close); *ide.Link is the production value, and
	// tests inject an in-memory fake so the footer + ambient-capture paths
	// can be exercised without a real socket. The orchestrator only needs
	// the host.IDELink subset. See docs/tui/README.md ("Editor awareness: /ide").
	ideLink ideLinkHandle

	// pendingIDEAmbient is the editor selection captured at the current
	// turn's submit (read-at-submit, slice 2 open question #2), waiting to
	// be injected onto the turn ctx in startAsyncTurn so the agent prompt
	// scope exposes it as `args.ide`. Zero (empty File) when the link is
	// off, the selection was empty, or the active file is deny-ruled.
	// Cleared every submit so it never leaks across turns.
	pendingIDEAmbient host.IDEAmbient

	// lastIDEAmbient is the selection that most recently rode a turn, used to
	// inject only on change: a selection the operator holds across several
	// turns must not silently re-shape every follow-up. captureIDEAmbient
	// skips injection + echo when the live selection equals this, and resets
	// it to zero whenever there is no usable selection (off / empty / denied)
	// so that re-selecting the same range later counts as new.
	lastIDEAmbient host.IDEAmbient

	// ideDeny is the kitsoki-side deny list gating ambient selection
	// attach: when the active file matches any of these patterns the
	// selection is NOT attached to the turn and no `⧉ Selected …` echo is
	// emitted. kitsoki cannot read Claude Code's own Read deny-rules and
	// must not assume parity, so this is an explicit, minimal local
	// setting (default empty — nothing denied). Patterns are matched with
	// path/filepath.Match against both the absolute path and its base name.
	ideDeny []string

	// openArtifact opens a resolved artifact path in the OS's default handler
	// (or $EDITOR). It is the seam the `/open` slash command drives; tests
	// inject a recording fake so the command's path-resolution and reporting
	// run without launching a real opener. Nil means "use the default OS
	// opener" (set in NewRootModel) — the production behavior.
	openArtifact func(path string) error
}

const recentBackgroundCap = 8

// RootModelOption is a functional option for RootModel construction.
type RootModelOption func(*RootModel)

// WithTUIClock injects an alternative clock.Clock into the RootModel's inbox
// polling path.  Under the real application clock.Real() is used (the default
// when this option is omitted).  Tests inject a *clock.Fake so they can drive
// inbox ticks deterministically without any wall-clock waits:
//
//	fakeClk := clock.NewFake(time.Now())
//	m := NewRootModel(orch, sid, "", "", WithJobStore(store), WithTUIClock(fakeClk))
//	fakeClk.Advance(2 * time.Second) // fires the pending inbox tick
func WithTUIClock(c clock.Clock) RootModelOption {
	return func(m *RootModel) { m.clk = c }
}

// WithJobStore wires a *jobs.JobStore into the RootModel for inbox panel
// polling.  When omitted (or nil), the inbox panel stays hidden and no
// database connection is required (headless tests, serve mode).
func WithJobStore(js *jobs.JobStore) RootModelOption {
	return func(m *RootModel) { m.jobStore = js }
}

// WithChatStore wires a *chats.Store into the RootModel so the /meta
// overlay can resolve and persist meta-mode chat rows. When omitted,
// /meta is unavailable and the slash command surfaces a polite hint.
//
// Production passes the same *chats.Store that backs the chathost
// adapter so meta-mode chats sit in the same SQLite file as everything
// else.
func WithChatStore(cs *chats.Store) RootModelOption {
	return func(m *RootModel) { m.chatStore = cs }
}

// WithTraceHistory wires a read-only event-history source into the RootModel.
// /work uses it to surface trace-backed mining proposals even when a miner
// service snapshot is not available.
func WithTraceHistory(fn func() (store.History, error)) RootModelOption {
	return func(m *RootModel) { m.traceHistory = fn }
}

// WithIDEDenyList seeds the kitsoki-side deny list that gates ambient editor
// selection attach (the `/ide` slice). When the active file matches any pattern
// the selection never rides a turn and no `⧉ Selected …` echo is emitted.
// kitsoki cannot read Claude Code's own Read deny-rules and must not assume
// parity, so the list is explicit and local; the default (option omitted) is
// empty — nothing denied. Patterns are filepath.Match globs tried against both
// the absolute path and the base name (so "*.env" and "/secrets/*" both work).
func WithIDEDenyList(patterns []string) RootModelOption {
	return func(m *RootModel) { m.ideDeny = patterns }
}

// WithMetaController injects a pre-built *metamode.Controller into the
// RootModel. Tests use this to bypass the production wiring (which
// requires a real agent registry, chat store, and agent adapter)
// and inject a controller pointed at fakes.
//
// When non-nil, this controller is used regardless of what
// NewRootModel would otherwise build from chatStore + AppDef +
// host.AgentRegistry().
func WithMetaController(c *metamode.Controller) RootModelOption {
	return func(m *RootModel) { m.metaController = c }
}

// WithTraceRingBuffer wires an in-memory trace ring into the RootModel.
// buildMetaTurnContext snapshots it to disk on every meta-mode Send when
// no external trace file is available.  Tests that want a lightweight
// fallback without a real EventSink file pass a ring here; tests and
// production callers that hand the agent a real JSONL path via
// WithExternalTraceFile do not need to set a ring.
func WithTraceRingBuffer(rb *trace.RingBuffer) RootModelOption {
	return func(m *RootModel) { m.traceRing = rb }
}

// WithTraceFilePath sets the absolute path the TUI rewrites on every
// meta-mode Send with the current ring snapshot. Production passes
// the path returned by os.CreateTemp at session startup; tests pass
// t.TempDir()+"/meta-trace.jsonl" to assert on the contents.
func WithTraceFilePath(path string) RootModelOption {
	return func(m *RootModel) {
		m.traceFilePath = path
		m.traceFileExternal = false
	}
}

// WithExternalTraceFile points the meta-mode agent at a trace file an
// external writer is already keeping current (typically the --trace
// JSONL path). The TUI does NOT rewrite the file — it just surfaces
// the path in TurnContext.TracePath so the agent can Read it. Takes
// precedence over WithTraceFilePath when both are set.
func WithExternalTraceFile(path string) RootModelOption {
	return func(m *RootModel) {
		m.traceFilePath = path
		m.traceFileExternal = true
	}
}

// WithJournalWriter injects a journal.Writer into the RootModel. When non-nil,
// inbox read/dismiss events (site 27) and disambiguation choice events (site 31)
// emit typed journal entries for continue-mode durability (dual-write).
// When nil (the default), no journal entries are written — this preserves
// backward compatibility for tests and headless callers.
func WithJournalWriter(jw journal.Writer) RootModelOption {
	return func(m *RootModel) { m.journalWriter = jw }
}

// WithInitialTypedView wires the typed-view payload for the initial
// frame (typed View / runtime env / per-app renderer) so NewRootModel
// can call transcript.AppendSystemTyped instead of AppendSystem when
// the root state's view is a pure element-array form. Without it, the
// pre-rendered ANSI string is re-routed through Glamour, which strips
// ESC bytes and surfaces literal `[1;…m` codes in the rendered output.
// typed may be nil — the option is a no-op then and the legacy
// AppendSystem path is used.
func WithInitialTypedView(typed *app.View, env expr.Env, rr *render.AppRenderer) RootModelOption {
	return func(m *RootModel) {
		m.initialTypedView = typed
		m.initialTypedEnv = env
		m.initialTypedRR = rr
	}
}

// WithMetaStreamSink wires a *MetaStreamSink into the RootModel so
// meta-mode Send calls stream live progress lines into the chat
// transcript. The sink itself is unbound at construction time; the
// caller binds it to the *tea.Program post-construction via
// sink.Attach(prog), exactly like RoutingObserver.Attach. When
// omitted (or nil), meta-mode falls back to the buffered "agent is
// thinking…" spinner UX — slog still records the stream-json events.
func WithMetaStreamSink(sink *MetaStreamSink) RootModelOption {
	return func(m *RootModel) { m.metaStreamSink = sink }
}

// WithOperatorPrompter wires a *TUIOperatorPrompter into the RootModel so a
// dispatched agent agent that forwards an AskUserQuestion surfaces it as an
// inline question widget and blocks for the operator's answer. Like
// WithMetaStreamSink the prompter is unbound at construction; the caller binds
// it to the *tea.Program post-construction via prompter.Attach(prog). When
// omitted (or nil) the headless tool-denied posture applies: the agent is told
// to proceed on its own.
func WithOperatorPrompter(prompter *TUIOperatorPrompter) RootModelOption {
	return func(m *RootModel) { m.operatorPrompter = prompter }
}

// WithSpatialPrompter wires a *TUISpatialPrompter into the RootModel so a
// dispatched oracle that requests a spatial ambient surfaces an OSC 8 link to a
// transient chrome-less `/point` window and blocks the turn for the operator's
// visual bundle. Like WithOperatorPrompter the prompter is unbound at
// construction; the caller binds it to the *tea.Program post-construction via
// prompter.Attach(prog). When omitted (or nil) no spatial ambient is requested
// and the turn runs text-only (the headless posture).
func WithSpatialPrompter(prompter *TUISpatialPrompter) RootModelOption {
	return func(m *RootModel) { m.spatialPrompter = prompter }
}

// WithRoutingObserver wires a *RoutingObserver into the RootModel. The
// observer is the slog→tea.Msg bridge that converts orchestrator
// routing events into RoutingTier*Msg deliveries for the progressive
// resolution chip (see docs/architecture/semantic-routing.md). The caller is
// responsible for inserting the observer as one handler in the slog
// pipeline (typically alongside the trace ring buffer) and for
// invoking obs.Attach(prog) once the *tea.Program is constructed.
//
// When omitted (or nil), the chip stays inactive — submitInput still
// works; only the routing-tier UX is disabled.
func WithRoutingObserver(obs *RoutingObserver) RootModelOption {
	return func(m *RootModel) { m.routingObserver = obs }
}

// WithResumedJourney overrides the initial state, world, and menu/location
// to match a previously-loaded journey (--continue resume path). Without
// this option, NewRootModel starts from the app's declared initial state.
//
// The option overwrites the currentState, menu, and location that
// NewRootModel set from orch.InitialState() / orch.InitialWorld() so the
// TUI opens in the same state the session was in when the user last quit.
// The initialView passed to NewRootModel should already be the view
// rendered from the journey (via orch.RenderState) by the caller.
//
// NOTE: A full Bubble Tea overlay picker is a follow-up; this option
// is initial plumbing only.
func WithResumedJourney(state app.StatePath, w world.World, turn app.TurnNumber) RootModelOption {
	return func(m *RootModel) {
		m.resumed = true
		m.currentState = state
		computedMenu := orchestrator.ComputeMenu(m.orch.AppDef(), m.orch.Machine(), state, w)
		m.menu, _ = m.menu.Update(menuItemsChanged{items: computedMenu.Primary, blocked: computedMenu.Blocked})
		m.refreshPromptPlaceholder()
		loc := orchestrator.ComputeLocation(m.orch.AppDef(), state, w, turn)
		m.location, _ = m.location.Update(locationUpdated{loc: loc})
	}
}

// WithResumedTranscript seeds the transcript pane from journal entries
// collected by AttachSession (continue-mode transcript rehydration).
//
// The entries slice must be ordered by (turn, seq) — the order AttachSession
// returns them in.  Each entry is mapped to the matching live transcript
// constructor (view.rendered → AppendSystem, offpath.question → AppendTurn,
// offpath.answer → AppendOffPathAnswer, etc.).  Unrecognised entry kinds are
// silently skipped.
//
// This option is applied after NewRootModel runs its own initialView append so
// the reconstructed history appears before any "fresh session" boilerplate.
// Callers that pass both an initialView and WithResumedTranscript should pass
// an empty initialView string to avoid duplicating the last view.rendered row.
func WithResumedTranscript(entries []journal.Entry) RootModelOption {
	return func(m *RootModel) {
		m.resumed = true
		if len(entries) == 0 {
			return
		}
		m.transcript.ReconstructFromEntries(entries)
	}
}

// NewRootModel creates the root TUI model.  appPath is the path to the
// app.yaml backing this session — required for the Esc-menu "Edit mode"
// reload flow.  Pass "" to disable edit mode (e.g., from tests).
//
// Optional RootModelOption values control additional wiring:
//   - WithJobStore — enables the inbox panel (background-job notifications).
//   - WithTUIClock — replaces the real wall clock for deterministic tests.
func NewRootModel(orch *orchestrator.Orchestrator, sid app.SessionID, appPath, initialView string, opts ...RootModelOption) RootModel {
	const defaultWidth = 120
	const defaultHeight = 40
	const menuWidth = 30
	const transcriptHeight = 30
	const inboxHeight = 12

	ti := newPromptTextarea()

	sp := spinner.New()
	sp.Spinner = spinner.MiniDot
	sp.Style = lipgloss.NewStyle().Foreground(colorPrimary)

	m := RootModel{
		orch:             orch,
		sid:              sid,
		appPath:          appPath,
		mode:             ModeOnPath,
		width:            defaultWidth,
		height:           defaultHeight,
		location:         newLocationModel(),
		transcript:       newTranscriptModel(defaultWidth-menuWidth-6, transcriptHeight-inboxHeight),
		menu:             newMenuModel(menuWidth, transcriptHeight-inboxHeight),
		inbox:            newInboxModel(menuWidth, inboxHeight),
		offPath:          newOffPathModel(offPathBannerFromApp(orch.AppDef())),
		clarify:          newClarifyModel(),
		disambiguation:   newDisambiguationModel(),
		choice:           newChoiceWidgetModel(),
		operatorQuestion: newOperatorQuestionModel(),
		menuSystem:       newMenuSystemModel(metaMenuEntries(orch.AppDef())),
		metaMode:         newMetaModel(),
		sessionsPanel:    newSessionsPanelModel(),
		prompt:           ti,
		spinner:          sp,
		openArtifact:     osOpenArtifact,
	}

	// Set initial state.
	m.currentState = orch.InitialState()

	// Apply functional options FIRST so option-supplied state (e.g. the
	// typed-view payload from WithInitialTypedView) is in place before
	// any setup step reads it. The initial-view paint below branches on
	// m.initialTypedView; running options afterward would always paint
	// via the legacy AppendSystem path no matter what callers wired in.
	for _, opt := range opts {
		opt(&m)
	}

	// Print a Claude-Code-style welcome banner into scrollback once
	// at startup. It scrolls off naturally as content grows. Suppressed
	// on resume (--continue): the banner is start-of-session boilerplate
	// already in the prior session's scrollback, and re-emitting it AFTER
	// the reconstructed transcript drops a mis-styled box (it picks up the
	// trailing agent body's background) into the middle of the history.
	if !m.resumed {
		if welcome := buildWelcome(orch, sid, appPath, defaultWidth); welcome != "" {
			m.transcript.pending = append(m.transcript.pending, welcome)
		}
	}

	// Show initial view in transcript. When the root state's view is a
	// typed element-array, prefer AppendSystemTyped so the elements
	// dispatcher renders fresh at viewport width — feeding the
	// pre-rendered ANSI string through AppendSystem instead would
	// double-route it through Glamour and corrupt the escape bytes.
	if m.initialTypedView != nil {
		slog.Info("tui.initial_paint",
			"path", "typed",
			"elements", len(m.initialTypedView.Elements),
			"fallback_bytes", len(initialView),
			"fallback_has_ansi", strings.Contains(initialView, "\x1b["),
		)
		// If the initial frame carries an interactive choice element,
		// open the widget straight away so the first thing the user
		// sees is a focused picker. Mirrors handleTurnOutcome's auto-
		// focus branch. Strip the choice element from the typed view
		// passed to AppendSystemTyped so the body doesn't render the
		// static picker on top of the live widget — only the
		// interactive overlay shows the choice rows.
		typedForBody := m.initialTypedView
		if el, ok := findChoiceElement(m.initialTypedView); ok {
			if err := m.choice.Open(el, m.initialTypedEnv, m.initialTypedRR); err != nil {
				slog.Warn("tui.choice.open_initial", "err", err)
			} else {
				m.mode = ModeChoosing
				typedForBody = viewWithoutChoice(m.initialTypedView)
				// Snapshot whatever was in the prompt textarea (if
				// anything) so /input can restore it later, then clear
				// the textarea so the user can't keep typing into an
				// inert field while the widget owns focus.
				m.pendingDraft = m.prompt.Value()
				m.prompt.SetValue("")
			}
		}
		m.transcript.AppendSystemTyped(initialView, typedForBody, m.initialTypedEnv, m.initialTypedRR)
		if m.mode == ModeChoosing {
			m.transcript.AppendLive(m.choice.View(m.transcript.wrapWidth()))
		}
	} else if initialView != "" {
		slog.Info("tui.initial_paint",
			"path", "legacy_system",
			"bytes", len(initialView),
			"has_ansi", strings.Contains(initialView, "\x1b["),
		)
		m.transcript.AppendSystem(initialView)
	}

	// Populate initial menu.
	w := orch.InitialWorld()
	computedMenu := orchestrator.ComputeMenu(orch.AppDef(), orch.Machine(), m.currentState, w)
	m.menu, _ = m.menu.Update(menuItemsChanged{items: computedMenu.Primary, blocked: computedMenu.Blocked})
	m.refreshPromptPlaceholder()

	// Set initial location.
	loc := orchestrator.ComputeLocation(orch.AppDef(), m.currentState, w, 0)
	m.location, _ = m.location.Update(locationUpdated{loc: loc})

	// Wire the metamode controller from production seams when the
	// caller didn't inject one. Requires both a chat store and at
	// least one declared meta_mode; without either, /meta stays
	// unavailable and the slash command surfaces a polite hint.
	if m.metaController == nil && m.chatStore != nil && len(orch.AppDef().MetaModes) > 0 {
		if reg := host.AgentRegistry(); reg != nil {
			m.metaController = &metamode.Controller{
				Chats:  metamode.NewChatStoreAdapter(m.chatStore),
				Agents: reg,
				AppDef: orch.AppDef(),
				Agent:  metamode.NewAgentCallerAdapter(),
			}
		}
	}

	return m
}

// metaMenuEntries enumerates every declared meta mode (sorted by name)
// so the Esc-menu overlay can list one row per mode. Empty slice when
// the AppDef declares (or has injected) no meta_modes.
//
// For grouped keys (`story.edit`, `kitsoki.bug`, …) the label
// defaults to a `<Group> › <Verb>` breadcrumb when the mode declares no
// explicit `label:`. Authors can still override via the YAML `label:`
// field and that wins verbatim.
func metaMenuEntries(def *app.AppDef) []metaMenuEntry {
	if def == nil || len(def.MetaModes) == 0 {
		return nil
	}
	names := sortedMetaModeNames(def)
	out := make([]metaMenuEntry, 0, len(names))
	for _, name := range names {
		mode := def.MetaModes[name]
		if mode == nil {
			continue
		}
		label := mode.Label
		if label == "" {
			label = defaultMetaLabel(name, mode)
		}
		out = append(out, metaMenuEntry{Name: name, Label: label})
	}
	return out
}

// defaultMetaLabel synthesises a display label for a meta mode that
// declares no explicit `label:`. Grouped keys render as
// `<Group> › <Trigger>` (cosmetic), ungrouped keys keep
// the legacy `meta: <name>` form so back-compat apps look the same.
func defaultMetaLabel(name string, mode *app.MetaModeDef) string {
	if mode != nil && mode.Group != "" && mode.Trigger != "" {
		return titleCaseFirst(mode.Group) + " › " + titleCaseFirst(mode.Trigger)
	}
	// Grouped key without explicit Group field — fall back to splitting
	// the map key on '.'.
	if dot := strings.Index(name, "."); dot > 0 {
		return titleCaseFirst(name[:dot]) + " › " + titleCaseFirst(name[dot+1:])
	}
	return "meta: " + name
}

// titleCaseFirst uppercases the first rune of s and lowercases the
// rest. Tiny helper for human-facing labels — Go's strings package has
// no equivalent that isn't deprecated.
func titleCaseFirst(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	upper := strings.ToUpper(string(runes[0]))
	if len(runes) == 1 {
		return upper
	}
	return upper + strings.ToLower(string(runes[1:]))
}

// pickDefaultMetaMode chooses the meta-mode key that bare `/meta` (no
// args) resolves to. Rules:
//
//   - If any grouped modes (Group set on the def) exist, prefer the
//     lex-first GROUP's default verb. Without this special-case the
//     raw-lex first key for the builtin set would be `kitsoki.ask` —
//     surprising, since `ask` is the read-only sibling of `edit`.
//   - Otherwise (or no group declares default), fall back to
//     firstMetaModeName(names) — the legacy lex-first behaviour.
//
// names must be the def's keys in lex order (caller pre-sorts via
// sortedMetaModeNames).
func pickDefaultMetaMode(def *app.AppDef, names []string) string {
	if def == nil || len(names) == 0 {
		return ""
	}
	// Find lex-first group; map[Group] → default key.
	type groupRow struct {
		defaultKey string
		anyKey     string
	}
	groups := map[string]*groupRow{}
	var groupKeys []string
	for _, n := range names {
		mode := def.MetaModes[n]
		if mode == nil || mode.Group == "" {
			continue
		}
		row := groups[mode.Group]
		if row == nil {
			row = &groupRow{anyKey: n}
			groups[mode.Group] = row
			groupKeys = append(groupKeys, mode.Group)
		}
		if mode.Default && row.defaultKey == "" {
			row.defaultKey = n
		}
	}
	if len(groupKeys) > 0 {
		sort.Strings(groupKeys)
		// Prefer the story group so bare `/meta` targets the running
		// story (engine-targeting via `/meta kitsoki …` is explicit);
		// otherwise fall back to the lex-first group. Without this the
		// lex-first builtin group is `kitsoki` (< `story`), so bare
		// `/meta` would open the engine editor whenever $KITSOKI_REPO is
		// set — surprising and contrary to the story-by-default rule.
		if story := groups[defaultMetaGroup]; story != nil {
			if story.defaultKey != "" {
				return story.defaultKey
			}
			if story.anyKey != "" {
				return story.anyKey
			}
		}
		first := groups[groupKeys[0]]
		if first.defaultKey != "" {
			return first.defaultKey
		}
		if first.anyKey != "" {
			return first.anyKey
		}
	}
	return firstMetaModeName(names)
}

// sortedMetaModeNames returns def.MetaModes keys in lexicographic
// order. Used wherever we need a deterministic "first meta mode"
// (Esc-menu entry label, bare /meta target).
func sortedMetaModeNames(def *app.AppDef) []string {
	if def == nil {
		return nil
	}
	names := make([]string, 0, len(def.MetaModes))
	for k := range def.MetaModes {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// Init implements tea.Model.
func (m RootModel) Init() tea.Cmd {
	cmds := []tea.Cmd{textarea.Blink, m.spinner.Tick}
	if m.jobStore != nil {
		cmds = append(cmds, m.scheduleInboxPoll(2*time.Second))
	}
	// The location-bar header + seeded initial view print on the
	// first Update — NewRootModel populates transcript.pending and
	// the Update wrapper flushes it via tea.Println. We don't flush
	// from Init because Init runs on a value copy of the model;
	// any pending mutation here wouldn't clear the source's slice
	// and we'd double-print on the first Update.
	return tea.Batch(cmds...)
}

// inboxPollMsg is the internal tick message fired by the inbox poller.
type inboxPollMsg struct{}

// inboxClock returns the clock to use for inbox polling.  clock.Real() is the
// default; tests inject a *clock.Fake via WithTUIClock.
func (m RootModel) inboxClock() clock.Clock {
	if m.clk != nil {
		return m.clk
	}
	return clock.Real()
}

// scheduleInboxPoll returns a tea.Cmd that fires inboxPollMsg after delay.
// It uses the model's injectable clock (real by default, fake in tests) so
// unit tests can drive ticks deterministically with Fake.Advance instead of
// sleeping real wall-clock time.
func (m RootModel) scheduleInboxPoll(delay time.Duration) tea.Cmd {
	clk := m.inboxClock()
	return func() tea.Msg {
		<-clk.After(delay)
		return inboxPollMsg{}
	}
}

// pollInbox reads notifications from the job store and returns an inboxRefreshed
// message.  Runs inline (called from the Update goroutine via tea.Cmd).
func (m RootModel) pollInbox(syncGitHub bool) tea.Msg {
	if m.jobStore == nil {
		return nil
	}
	ctx := context.Background()
	if syncGitHub {
		if _, err := syncGitHubInboxNotifications(ctx, m.jobStore, m.sid, ""); err != nil {
			slog.Debug("tui: github inbox sync skipped", "err", err)
		}
	}
	ns, err := m.jobStore.ListNotifications(ctx, m.sid, 20)
	if err != nil {
		slog.Warn("tui: inbox poll error", "err", err)
		return nil
	}
	return inboxRefreshed{notifications: ns}
}

// Update implements tea.Model. Wraps updateInner so any
// transcript.Append* calls queued by the inner handler are flushed
// to scrollback via tea.Println before returning.
func (m RootModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	nm, cmd := m.updateInner(msg)
	if rm, ok := nm.(RootModel); ok {
		if flush := rm.transcript.FlushPending(); flush != nil {
			if cmd == nil {
				cmd = flush
			} else {
				cmd = tea.Batch(cmd, flush)
			}
		}
		return rm, cmd
	}
	return nm, cmd
}

func (m RootModel) updateInner(msg tea.Msg) (tea.Model, tea.Cmd) {
	// If slot-filling is active, route to clarify model first.
	if m.mode == ModeSlotFilling {
		return m.updateSlotFilling(msg)
	}
	// If disambiguating, route to disambiguation model first.
	if m.mode == ModeDisambiguating {
		return m.updateDisambiguating(msg)
	}
	// If a choice widget is active, route keys through it.
	if m.mode == ModeChoosing {
		return m.updateChoosing(msg)
	}
	// If a forwarded agent question is on screen, it owns the keyboard until
	// the operator answers (or hits Esc to let the agent decide).
	if m.mode == ModeOperatorQuestion {
		return m.updateOperatorQuestion(msg)
	}
	// If the system menu overlay is active, it owns the keyboard.
	if m.mode == ModeMenu {
		return m.updateMenuSystem(msg)
	}
	// If meta mode is active, it owns the keyboard and prompt.
	if m.mode == ModeMeta {
		return m.updateMeta(msg)
	}
	// If the meta-sessions panel overlay is active, it owns the
	// keyboard until the user picks a row or hits Esc.
	if m.mode == ModeMetaSessions {
		return m.updateSessionsPanel(msg)
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		slog.Debug("tui.resize",
			slog.Int("width", msg.Width),
			slog.Int("height", msg.Height),
		)
		m.width = msg.Width
		m.height = msg.Height
		m = m.resize()
		return m, nil

	case tea.KeyMsg:
		return m.routeKey(msg)

	case turnOutcomeMsg:
		// Clear in-flight state before handling.
		m.mode = ModeOnPath
		m.inFlightCancel = nil
		return m.handleTurnOutcome(msg)

	case operatorQuestionMsg:
		return m.handleOperatorQuestion(msg)

	case minePassDoneMsg:
		return m.handleMinePassDone(msg)

	case spatialPointMsg:
		return m.handleSpatialPoint(msg)

	case spatialClearMsg:
		return m.handleSpatialClear(msg)

	case offPathReplyMsg:
		return m.handleOffPathReply(msg)

	case continueTurnOutcomeMsg:
		return m.handleContinueTurnOutcome(msg)

	case supplementSlotsMsg:
		return m.handleSupplementSlots(msg)

	case disambiguationChoiceMsg:
		return m.handleDisambiguationChoice(msg)

	case menuSystemChoiceMsg:
		return m.handleMenuSystemChoice(msg)

	case metaEnterDoneMsg:
		return m.handleMetaEnterDone(msg)

	case metaSendDoneMsg:
		return m.handleMetaSendDone(msg)

	case metaListDoneMsg:
		return m.handleMetaListDone(msg)

	case metaNewDoneMsg:
		return m.handleMetaNewDone(msg)

	case metaDoneDoneMsg:
		return m.handleMetaDoneDone(msg)

	case ideConnectDoneMsg:
		return m.handleIDEConnectDone(msg)

	case MetaStreamMsg:
		// Route to the shared handler. Its own in-flight gate
		// (ModeMeta+metaMode.inFlight OR ModeAwaitingLLM) decides
		// whether to render the event or drop it as stale — the
		// previous unconditional drop here meant on-path agent calls
		// (e.g. bf.reproducing_executing's host.agent.ask_with_mcp)
		// never surfaced their tool-use trail in the transcript even
		// though the runner was streaming the events all along.
		return m.handleMetaStreamEvent(msg), nil

	case roomEnteredMsg:
		// Paint the room banner above the in-flight tool-call
		// breadcrumbs. Fires mid-turn from the orchestrator the moment
		// a transition lands in a new room (top-level state change),
		// before on_enter host calls dispatch — so the banner reads
		// like a section header for everything that follows. Flush
		// immediately so the queued banner reaches scrollback now,
		// not when the turn completes.
		m.transcript.AppendRoomBanner(msg.Banner)
		return m, m.transcript.FlushPending()

	case sessionsPanelLoadedMsg:
		return m.handleSessionsPanelLoaded(msg)

	case sessionsPanelChoiceMsg:
		return m.handleSessionsPanelChoice(msg)

	case spinner.TickMsg:
		// Two consumers can want spinner ticks: the awaiting-LLM
		// spinner on the prompt line, and the routing chip's "⋯
		// resolving…" spinner. Forward unconditionally to both so
		// neither stalls if the other is idle.
		var cmds []tea.Cmd
		if m.mode == ModeAwaitingLLM {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		if len(cmds) == 0 {
			return m, nil
		}
		return m, tea.Batch(cmds...)

	case RoutingTierMissMsg, RoutingTierHitMsg, RoutingAmbiguousMsg, RoutingCancelMsg:
		// Update the inline routing-status block in the transcript
		// so the user sees the tier advance live, in place, beneath
		// their echoed input. Single-pane-tui §"Input feedback"
		// step 2. The legacy RoutingChip sub-model that previously
		// also consumed these messages was deleted in Phase 7.
		ir := m.newInlineRouter()
		switch tm := msg.(type) {
		case RoutingTierMissMsg:
			// A tier was tried with no confident match — advance the pipeline.
			// Reason carries the backend for a local-LLM miss so the right LLM
			// layer is marked.
			m.routing.markMiss(tm.Tier, tm.Reason)
			m.transcript.UpdateLive(m.routing.renderProgress())
		case RoutingTierHitMsg:
			// Mark the winning layer (the LLM layer's detail names the backend,
			// e.g. agent.local) and settle the resolved line. Guard on hasLive so
			// this and the turn-completion finalizer can't both commit (whichever
			// runs first settles; the other sees no live line and skips).
			m.routing.markHit(tm.Tier, tm.Intent, hitDetailFor(tm), tm.Confidence, tm.Tier == TierLLM)
			if m.transcript.hasLive() {
				m.transcript.FinalizeLive(m.routing.renderResolved())
			}
		case RoutingAmbiguousMsg:
			m.transcript.UpdateLive(ir.r.RoutingResolved(blocks.Resolved{
				Source: blocks.SourceAmbiguous,
			}))
		case RoutingCancelMsg:
			m.transcript.FinalizeLive("")
		}
		return m, nil

	case inboxPollMsg:
		// Fire the actual DB read as a new Cmd, then schedule the next tick.
		if m.jobStore == nil {
			return m, nil
		}
		now := m.inboxClock().Now()
		syncGitHub := m.lastGitHubInboxSync.IsZero() || now.Sub(m.lastGitHubInboxSync) >= githubInboxPollInterval
		if syncGitHub {
			m.lastGitHubInboxSync = now
		}
		return m, func() tea.Msg { return m.pollInbox(syncGitHub) }

	case inboxRefreshed:
		// Single-pane redesign: print a transcript line for each
		// genuinely new notification. "New" = an unread notification
		// that wasn't in the previous snapshot. This makes the
		// transcript the primary surface for inbox awareness; the
		// panel still updates underneath until phase 3 removes it.
		newOnes := newInboxNotifications(m.lastNotifications, msg.notifications)
		m.lastNotifications = msg.notifications
		m.inbox, _ = m.inbox.Update(msg)
		for _, n := range newOnes {
			r := blocks.New(m.transcript.width, m.currentTheme())
			m.transcript.AppendBlock(r.Inbox(blocks.InboxNotification{
				ID:       n.ID,
				Title:    n.Title,
				Severity: string(n.Severity),
				Age:      "just now",
			}))
			// Push the arrival onto the background-completion log so
			// /jump has a target. Newest-first; bounded by
			// recentBackgroundCap.
			room := n.TeleportState
			if room == "" {
				room = "inbox"
			}
			m.backgroundCompletions = append(
				[]backgroundCompletion{{
					NotificationID: n.ID,
					Room:           room,
					Summary:        n.Title,
				}},
				m.backgroundCompletions...,
			)
			if len(m.backgroundCompletions) > recentBackgroundCap {
				m.backgroundCompletions = m.backgroundCompletions[:recentBackgroundCap]
			}
		}
		// Schedule next poll — faster when jobs are running.
		if m.jobStore != nil {
			delay := 2 * time.Second
			ctx := context.Background()
			running, _ := m.jobStore.ListJobsByStatus(ctx, m.sid, jobs.JobRunning)
			if len(running) > 0 {
				delay = 200 * time.Millisecond
			}
			return m, m.scheduleInboxPoll(delay)
		}
		return m, nil

	case inboxItemSelected:
		return m.handleInboxItemSelected(msg)

	default:
		// Pass to sub-models.
		var cmd tea.Cmd
		m.location, _ = m.location.Update(msg)
		m.transcript, cmd = m.transcript.Update(msg)
		m.menu, _ = m.menu.Update(msg)
		return m, cmd
	}
}

// handleInboxItemSelected teleports to the notification's target state and
// marks the notification read.
func (m RootModel) handleInboxItemSelected(msg inboxItemSelected) (tea.Model, tea.Cmd) {
	n := msg.notification
	target := inbox.FromNotification(n)
	if target.State == "" {
		// Notification has no teleport target — just mark it read and move on.
		if m.jobStore != nil {
			ctx := context.Background()
			_ = m.jobStore.MarkNotificationRead(ctx, n.ID)
		}
		// Site 27 (a): inbox item opened (no teleport target).
		m.emitInboxOpened(n.ID, n.Title)
		return m, nil
	}

	orch := m.orch
	sid := m.sid
	js := m.jobStore
	jw := m.journalWriter
	tuiSID := m.sid

	return m, func() tea.Msg {
		ctx := context.Background()
		// Mark notification read (best-effort; ignore error).
		if js != nil {
			_ = js.MarkNotificationRead(ctx, n.ID)
		}
		// Site 27 (b): inbox item opened (before teleport fires).
		tuiEmitInboxOpened(jw, tuiSID, n.ID, n.Title)
		out, err := orch.Teleport(ctx, sid, target)
		return turnOutcomeMsg{outcome: out, input: "(teleport)", err: err}
	}
}

// updateSlotFilling handles input while the clarify model is collecting
// missing slot values. The prompt area is the normal textarea (with the
// `?` prefix); Enter intercepts the typed value and routes it through
// clarify.SubmitValue. Esc cancels. Window/spinner/tea messages fall
// through to the default Update path so the rest of the model keeps
// updating normally.
func (m RootModel) updateSlotFilling(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case supplementSlotsMsg:
		m.mode = ModeOnPath
		m.clarify.Close()
		return m.handleSupplementSlots(msg)

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m = m.resize()
		return m, nil

	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyEsc:
			m.mode = ModeOnPath
			m.clarify.Close()
			m.transcript.AppendSystem("(slot fill cancelled)")
			return m, nil

		case tea.KeyCtrlC:
			if strings.TrimSpace(m.prompt.Value()) != "" {
				m.prompt.SetValue("")
				return m, nil
			}
			m.quitting = true
			return m, tea.Quit

		case tea.KeyEnter:
			// Alt+Enter is a newline-in-textarea binding (Phase 4
			// textarea swap); falls through to the prompt so users can
			// compose multi-line slot values.
			if msg.Alt {
				break
			}
			input := strings.TrimSpace(m.prompt.Value())
			if input == "" {
				return m, nil
			}
			m.prompt.SetValue("")
			m.appendHistory(input)
			// Echo what the user typed so the transcript is the source
			// of truth for "what just happened" (matches the single-pane
			// proposal's input-feedback rule).
			m.transcript.AppendUserInputEcho(input)

			value, done, collected, err := m.clarify.SubmitValue(input)
			if err != nil {
				m.transcript.AppendSystem("(" + err.Error() + ")")
				// Re-render the same slot's block so the user sees the
				// choice list again without scrolling.
				if block := m.clarify.RenderInlineBlock(blocks.New(m.transcript.width, m.currentTheme())); block != "" {
					m.transcript.AppendBlock(block)
				}
				return m, nil
			}
			if done {
				m.transcript.AppendSystem(fmt.Sprintf("(accepted: %s)", value))
				m.mode = ModeOnPath
				m.clarify.Close()
				return m.handleSupplementSlots(supplementSlotsMsg{slots: collected})
			}
			// Advance to the next slot — render its block.
			m.transcript.AppendSystem(fmt.Sprintf("(accepted: %s)", value))
			if block := m.clarify.RenderInlineBlock(blocks.New(m.transcript.width, m.currentTheme())); block != "" {
				m.transcript.AppendBlock(block)
			}
			return m, nil
		}
	}

	// Everything else — printable keys, arrows, etc. — falls through to
	// the prompt textinput so the user can edit their pending value.
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		var cmd tea.Cmd
		m.prompt, cmd = m.prompt.Update(keyMsg)
		return m, cmd
	}
	return m, nil
}

func (m RootModel) routeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Ctrl+C tiered behaviour:
	//   1) in-flight LLM turn  → cancel the turn (no quit, no timer bump)
	//   2) prompt has text     → clear the prompt
	//   3) empty prompt, first press → remember the time, warn user
	//   4) empty prompt, second press within ctrlCQuitWindow → quit
	if msg.Type == tea.KeyCtrlC {
		if m.mode == ModeAwaitingLLM && m.inFlightCancel != nil {
			m.inFlightCancel()
			// Don't quit; wait for the turnOutcomeMsg to arrive with the cancel error.
			return m, nil
		}
		if strings.TrimSpace(m.prompt.Value()) != "" {
			m.prompt.SetValue("")
			m.lastCtrlC = time.Time{}
			return m, nil
		}
		now := time.Now()
		if !m.lastCtrlC.IsZero() && now.Sub(m.lastCtrlC) <= ctrlCQuitWindow {
			m.quitting = true
			return m, tea.Quit
		}
		m.lastCtrlC = now
		m.transcript.AppendSystem("(press Ctrl+C again to exit, or Esc for menu)")
		return m, nil
	}

	// Ctrl+R is the legacy routing-trace overlay shortcut. The
	// single-pane redesign moves this surface into /trace (chat
	// block), so the shortcut now prints the trace inline instead
	// of opening an overlay. The overlay state stays addressable
	// for the (few) tests that exercise it directly; phase 7
	// cleanup deletes both the state and the overlay renderer.
	if msg.Type == tea.KeyCtrlR {
		m.transcript.AppendBlock(renderTraceBlock(m))
		return m, nil
	}

	// /world dedicated view owns its key handling while active. q
	// and Esc close it; everything else (arrows, enter, e/c, h/l)
	// goes to the worldView model.
	if m.mode == ModeWorldView {
		switch msg.String() {
		case "q", "esc":
			return m.closeWorldView()
		}
		updated, cmd := m.worldView.Update(msg)
		m.worldView = updated
		return m, cmd
	}

	// Single-pane redesign (phase 4): Esc with items queued drops
	// them all and stashes each into inputHistory so the user can
	// recover with the ↑ arrow. Non-destructive cancel — the
	// in-flight turn keeps running. Esc with an empty queue
	// continues to the legacy system-menu behaviour below.
	if msg.Type == tea.KeyEsc && len(m.inputQueue) > 0 {
		for _, q := range m.inputQueue {
			m.appendHistory(q)
		}
		m.inputQueue = nil
		r := blocks.New(m.transcript.width, m.currentTheme())
		m.transcript.AppendBlock(r.SlashOutput("(queue cleared — items recovered with ↑ in the prompt)"))
		return m, nil
	}

	// Esc opens the system menu from the default interactive modes.
	// It does not fire while a slot-filling / disambiguation overlay is
	// already using Esc to back out.
	if msg.Type == tea.KeyEsc {
		if m.mode == ModeOnPath || m.mode == ModeOffPath {
			m.mode = ModeMenu
			m.menuSystem.Open()
			return m, nil
		}
		if m.mode == ModeAwaitingLLM {
			if m.inFlightCancel != nil {
				m.inFlightCancel()
			}
			m.mode = ModeMenu
			m.menuSystem.Open()
			return m, nil
		}
	}

	// Scroll keys (Shift+↑/↓, Ctrl+U/D/B/F, PgUp/PgDn) used to drive
	// the in-app viewport. With the no-alt-screen design, the
	// terminal's native scrollback owns scroll — wheel, Cmd+↑, the
	// terminal's own keybindings. We swallow the keys here so they
	// don't fall through to the textarea and insert characters.
	if isScrollKey(msg) {
		return m, nil
	}

	// Alt+Enter inserts a literal newline into the textarea — short-
	// circuit before Enter-based dispatch so the keystroke reaches
	// the textarea's InsertNewline binding ("alt+enter"). Without
	// this guard the plain Enter cases below would consume the event.
	if msg.Type == tea.KeyEnter && msg.Alt {
		var cmd tea.Cmd
		m.prompt, cmd = m.prompt.Update(msg)
		return m, cmd
	}

	// Slash commands previously had a routeKey-level shortcut that
	// bypassed submitInput, so they never got the immediate echo +
	// "→ system: ..." settled-line block. The single-pane redesign
	// routes all Enter through submitInput so the echo / routing /
	// dispatch happen at one site. The Phase-4 ModeAwaitingLLM
	// branch below still allows slash commands during in-flight by
	// going through submitInput, which slash-detects and bypasses
	// the queue.

	// While awaiting LLM: route Enter through submitInput so the
	// Phase 4 queue check fires (in-room free-text enqueues; the
	// drain pops items when the in-flight turn completes). Non-Enter
	// keystrokes still fall through to the prompt so users can keep
	// composing the next message — the textarea handles its own
	// editing during in-flight.
	if m.mode == ModeAwaitingLLM {
		if msg.Type == tea.KeyEnter && !msg.Alt {
			return m.submitInput()
		}
	}

	// i1–i9: inbox selection keys (only when prompt is empty).
	if s := msg.String(); len(s) == 2 && s[0] == 'i' && s[1] >= '1' && s[1] <= '9' {
		if strings.TrimSpace(m.prompt.Value()) == "" {
			var cmd tea.Cmd
			m.inbox, cmd = m.inbox.Update(msg)
			return m, cmd
		}
	}

	// action_required banner: enter/esc while banner is visible and prompt is empty.
	if banner := m.inbox.ActionRequiredBanner(); banner != "" && strings.TrimSpace(m.prompt.Value()) == "" {
		switch msg.Type {
		case tea.KeyEnter:
			// Teleport to the action_required notification.
			if n := m.inbox.ActionRequiredNotification(); n != nil {
				return m.handleInboxItemSelected(inboxItemSelected{notification: *n})
			}
		case tea.KeyEsc:
			// Snooze: mark it read in-memory by removing from model (dismiss from
			// display).  We call MarkNotificationRead for persistence — it's the
			// closest semantic fit for "later" (the notification won't reappear
			// until a new one is posted; there is no dedicated snooze-with-timer
			// path in the store today).
			if n := m.inbox.ActionRequiredNotification(); n != nil && m.jobStore != nil {
				nID := n.ID
				js := m.jobStore
				// Site 27 (c): inbox item dismissed (Esc / snooze semantics).
				m.emitInboxDismissed(n.ID, n.Title)
				// Trigger a re-poll so the inbox snapshot updates immediately.
				// MarkNotificationRead is called inside the Cmd to avoid blocking
				// Bubble Tea's Update loop on a potentially slow SQLite call.
				return m, func() tea.Msg {
					ctx := context.Background()
					_ = js.MarkNotificationRead(ctx, nID)
					return m.pollInbox(false)
				}
			}
			return m, nil
		}
	}

	switch msg.String() {
	case "enter":
		// Backslash-Enter inserts a literal newline (bash-style line
		// continuation). The terminal can't always distinguish
		// Shift+Enter from plain Enter, so we also honour the typed
		// "\<Enter>" idiom: if the prompt ends with an unescaped
		// trailing "\", strip it and fold the Enter into a real
		// newline rather than submitting.
		if submit, after := shouldSubmitOnEnter(m.prompt.Value()); !submit {
			m.prompt.SetValue(after)
			m.prompt.CursorEnd()
			m.prompt.InsertString("\n")
			return m, nil
		}
		// If the prompt is empty and a menu row is highlighted, dispatch it directly.
		if strings.TrimSpace(m.prompt.Value()) == "" {
			if entry := m.menu.SelectedEntry(); entry != nil {
				return m.dispatchMenuEntry(entry)
			}
		}
		return m.submitInput()

	case "alt+enter", "ctrl+j", "shift+enter":
		// Explicit "insert a literal newline" shortcuts. We forward to
		// the textarea, whose remapped InsertNewline binding matches
		// these aliases. shift+enter is included for the (small) set
		// of terminals that surface it as a distinct key sequence;
		// alt+enter and ctrl+j cover everyone else.
		if m.historyNavigating() {
			m.commitHistoryNavigation()
		}
		var cmd tea.Cmd
		m.prompt, cmd = m.prompt.Update(msg)
		return m, cmd

	case "up":
		// Plain Up arrow: with a textarea-based prompt, plain ↑ moves
		// the cursor up within the wrapped buffer when there is a
		// previous row; only when the cursor is already on the topmost
		// wrapped row of the first logical line does it walk input
		// history backward. Modified Up (shift+up / alt+up) is already
		// routed to the transcript viewport by isScrollKey above.
		if m.promptCursorAtTop() {
			return m.historyPrev(), nil
		}
		var cmdUp tea.Cmd
		m.prompt, cmdUp = m.prompt.Update(msg)
		return m, cmdUp

	case "down":
		// Plain Down arrow: mirror of Up — move the cursor down within
		// the wrapped buffer unless we're already on the bottommost
		// wrapped row of the last logical line, in which case walk
		// history forward (older → newer). Stepping past the newest
		// entry restores the saved draft.
		if m.promptCursorAtBottom() {
			return m.historyNext(), nil
		}
		var cmd2 tea.Cmd
		m.prompt, cmd2 = m.prompt.Update(msg)
		return m, cmd2

		// Single-pane redesign (phase 4): numeric quick-select is gone.
		// Numbers are normal text in the prompt — "1.5" or "10 tickets"
		// no longer trip the menu hotkey. Action selection moves to
		// /intents <n> (already wired in phase 1) and synonym/intent
		// names (semantic routing handles the rest).
	}

	// Any non-arrow, non-Enter keystroke while walking history should
	// "commit" the navigation: the currently-displayed entry becomes
	// the active draft and we leave history-walk mode. We don't mutate
	// the prompt text — the textarea.Update call below will fold the
	// new key into whatever's already shown.
	if m.historyNavigating() {
		m.commitHistoryNavigation()
	}

	// Pass key to prompt.
	var cmd tea.Cmd
	m.prompt, cmd = m.prompt.Update(msg)
	return m, cmd
}

// historyPrev walks one step back into inputHistory (toward older
// entries). On the first Up press it stashes the current prompt value
// as historyDraft so a subsequent Down past the newest entry can
// restore it. At the oldest entry it's a no-op (matches bash/zsh).
func (m RootModel) historyPrev() RootModel {
	if len(m.inputHistory) == 0 {
		return m
	}
	if !m.historyNavigating() {
		// First Up press — capture the in-progress draft so Down can
		// restore it later.
		m.historyDraft = m.prompt.Value()
		m.historyIdx = len(m.inputHistory)
	}
	if m.historyIdx == 0 {
		// Already at oldest — bash-style no-op.
		return m
	}
	m.historyIdx--
	m.prompt.SetValue(m.inputHistory[m.historyIdx])
	m.prompt.CursorEnd()
	return m
}

// historyNext walks one step forward into inputHistory (toward newer
// entries). Stepping past the newest entry restores the saved draft
// and exits history-walk mode.
func (m RootModel) historyNext() RootModel {
	if !m.historyNavigating() {
		// Not currently walking history — Down is a no-op (matches
		// bash; readline would beep here but the TUI has no bell).
		return m
	}
	m.historyIdx++
	if m.historyIdx >= len(m.inputHistory) {
		// Stepped past newest — restore the saved draft and leave
		// history-walk mode.
		m.historyIdx = len(m.inputHistory)
		m.prompt.SetValue(m.historyDraft)
		m.prompt.CursorEnd()
		m.historyDraft = ""
		return m
	}
	m.prompt.SetValue(m.inputHistory[m.historyIdx])
	m.prompt.CursorEnd()
	return m
}

// appendHistory records a submitted prompt line in the in-memory input
// history. Slash commands ARE included (matching bash/zsh/fish behaviour).
// Consecutive duplicates collapse to a single entry so spamming the same
// command doesn't bury older lines. Empty / whitespace-only lines are
// ignored. After append we always reset the navigation cursor back to
// the "not navigating, show draft" sentinel.
func (m *RootModel) appendHistory(input string) {
	trimmed := strings.TrimSpace(input)
	if trimmed != "" {
		if n := len(m.inputHistory); n == 0 || m.inputHistory[n-1] != trimmed {
			m.inputHistory = append(m.inputHistory, trimmed)
		}
	}
	m.historyIdx = len(m.inputHistory)
	m.historyDraft = ""
}

// historyNavigating reports whether the user is currently walking
// inputHistory with arrow keys (i.e. historyIdx points at a stored entry
// rather than the "draft" sentinel).
func (m RootModel) historyNavigating() bool {
	return m.historyIdx >= 0 && m.historyIdx < len(m.inputHistory)
}

// promptCursorAtTop reports whether the textarea cursor is on the
// topmost wrapped row of the first logical line — the edge condition
// where ↑ should walk input history instead of moving the cursor.
// While history-walking we're always "at top" semantically so the
// caller can keep stepping back without first having to dismiss
// history-walk mode.
func (m RootModel) promptCursorAtTop() bool {
	if m.historyNavigating() {
		return true
	}
	return m.prompt.Line() == 0 && m.prompt.LineInfo().RowOffset == 0
}

// promptCursorAtBottom reports whether the textarea cursor is on the
// bottommost wrapped row of the last logical line — the edge
// condition where ↓ should walk input history forward instead of
// moving the cursor.
func (m RootModel) promptCursorAtBottom() bool {
	if m.historyNavigating() {
		return true
	}
	li := m.prompt.LineInfo()
	lastLine := m.prompt.Line() == m.prompt.LineCount()-1
	lastRow := li.Height == 0 || li.RowOffset >= li.Height-1
	return lastLine && lastRow
}

// commitHistoryNavigation drops the user out of history-walk mode without
// touching the prompt text. Used when any non-arrow, non-Enter key
// arrives while navigating: the currently-shown entry effectively
// becomes the new draft.
func (m *RootModel) commitHistoryNavigation() {
	m.historyIdx = len(m.inputHistory)
	m.historyDraft = ""
}

func (m RootModel) submitInput() (tea.Model, tea.Cmd) {
	input := strings.TrimSpace(m.prompt.Value())
	if input == "" {
		return m, nil
	}
	m.prompt.SetValue("")
	m.appendHistory(input)
	return m.dispatchInput(input)
}

// dispatchInput runs the post-history portion of submitInput: queue
// check → immediate echo → routing classify → dispatch. Split out from
// submitInput so the queue-drain path can re-run dispatch without
// double-appending the input to history (the original submission
// already recorded it before going into the queue).
func (m RootModel) dispatchInput(input string) (tea.Model, tea.Cmd) {
	// Phase 4: if a turn is in flight and the new input is in-room
	// (no leading slash, not off-path), enqueue it. Nav / view /
	// system inputs pre-empt — they go through the normal path.
	// /<commands> are always immediate by definition; deciding
	// in-room vs nav for free-text without routing would need a
	// cheap pre-classification, so phase 4 conservatively enqueues
	// every free-text submission and lets a future refinement
	// upgrade nav-like phrases to immediate dispatch.
	if m.mode == ModeAwaitingLLM && !strings.HasPrefix(input, "/") {
		m.inputQueue = append(m.inputQueue, input)
		r := blocks.New(m.transcript.width, m.currentTheme())
		// Queued-echo block: shows the user's text + a queue tag so
		// scrollback carries "this is what you queued and when". The
		// styled prefix `↳` mirrors the prompt-row glyph during
		// in-flight so the visual cue is consistent.
		m.transcript.AppendBlock(r.QueuedEcho(input, len(m.inputQueue)))
		return m, nil
	}

	// Input feedback: echo the
	// user's input into the transcript immediately, before any routing
	// or dispatch. The transcript — not the input area — is the source
	// of truth for "what just happened."
	m.transcript.AppendUserInputEcho(input)
	ir := m.newInlineRouter()

	// Handle slash commands. Settled-line classification is "system"
	// — slash commands bypass routing.
	if strings.HasPrefix(input, "/") {
		settled := ir.settledLine("system", strings.Fields(input)[0], blocks.SourceDeterministic, 0, "")
		m.transcript.AppendBlock(settled)
		return m.handleSlashCommand(input)
	}

	// Off-path mode: free-form chat that does NOT touch world or state.
	// Routes directly to host.agent.talk via Orchestrator.AskOffPath
	// rather than through MatchDeterministic → harness → machine.
	if m.mode == ModeOffPath {
		m.transcript.AppendBlock(ir.settledLine("", "", blocks.SourceOffPath, 0, ""))
		return m.submitOffPath(input)
	}

	m.lastInput = input

	// Ambient editor context: when the IDE link is connected, read the
	// active selection NOW (read-at-submit) and stash it for injection onto
	// the turn ctx in startAsyncTurn. The `⧉ Selected N lines from <file>`
	// echo is appended here so the operator sees exactly what rode the turn
	// (the echo is the source of truth, slice 2 open question #2). A
	// deny-ruled file attaches nothing and emits no echo.
	m = m.captureIDEAmbient()

	// Live in-place routing block: starts at the deterministic phase
	// and is replaced by UpdateLive / FinalizeLive as the pipeline
	// progresses. routingObserver translates orchestrator slog
	// events into RoutingTier{Miss,Hit,Ambiguous,Cancel}Msg
	// deliveries which the dispatcher above feeds through
	// UpdateLive. The settled line stays in the transcript as a
	// permanent record.
	m.routing = newRoutingPipeline()
	m.transcript.AppendLive(m.routing.renderProgress())

	// Cheap, side-effect-free match against the current menu. This avoids the
	// LLM round-trip when the user typed something we can route locally — but
	// we still dispatch the resulting transition asynchronously so a slow
	// on_enter host call (e.g. host.agent.ask, host.run on a long command)
	// doesn't freeze the TUI.
	orch := m.orch
	sid := m.sid
	ctx := context.Background()
	intent, slots, hit, err := orch.MatchDeterministic(ctx, sid, input)
	if err != nil {
		// Drop the placeholder before printing the error.
		m.transcript.FinalizeLive("")
		m.transcript.AppendError("", fmt.Sprintf("error: %s", userfacing.Error(err)))
		return m, nil
	}
	if hit {
		// Deterministic match at submit time — the pipeline resolves on the
		// first layer and settles immediately (this path has no completion-time
		// finalizer because handleTurnOutcome skips deterministic turns).
		m.routing.markHit(TierDeterministic, intent, "", 0, false)
		m.transcript.FinalizeLive(m.routing.renderResolved())
		return startAsyncTurn(m, input, asyncSubmitDirectFromInput(orch, sid, intent, slots, input, orchestrator.RouteProvenance{Source: "deterministic"}), pendingDeterministic)
	}

	// No deterministic match — the deterministic layer is passed-through; the
	// async router (semantic → LLM) drives the rest via RoutingTier*Msg, and
	// handleTurnOutcome finalizes. Advance the live pipeline now.
	m.routing.markMiss(TierDeterministic, "")
	m.transcript.UpdateLive(m.routing.renderProgress())
	return startAsyncTurn(m, input, asyncTurn(orch, sid, input), pendingLLM)
}

// pendingKind classifies why the TUI is currently in ModeAwaitingLLM, so the
// spinner caption can describe the right thing (intent routing vs. effects
// running).
type pendingKind int

const (
	// pendingLLM means the LLM router is currently mapping user text → intent.
	pendingLLM pendingKind = iota
	// pendingDeterministic means the input matched a menu entry locally; the
	// async work is the post-transition effect dispatch (host calls, etc.).
	pendingDeterministic
)

// asyncTurn returns a function that runs orch.Turn for the LLM-router path.
// It is the closure shape that startAsyncTurn understands.
func asyncTurn(orch *orchestrator.Orchestrator, sid app.SessionID, input string) func(context.Context) (*orchestrator.TurnOutcome, error) {
	return func(ctx context.Context) (*orchestrator.TurnOutcome, error) {
		return orch.Turn(ctx, sid, input)
	}
}

// asyncSubmitDirect returns a function that runs orch.SubmitDirect for the
// deterministic-hit path.
func asyncSubmitDirect(orch *orchestrator.Orchestrator, sid app.SessionID, intent string, slots map[string]any) func(context.Context) (*orchestrator.TurnOutcome, error) {
	return func(ctx context.Context) (*orchestrator.TurnOutcome, error) {
		return orch.SubmitDirect(ctx, sid, intent, slots)
	}
}

// asyncSubmitDirectFromInput is asyncSubmitDirect's user-input-preserving
// variant: the deterministic match has the user's original text, so we
// thread it through onto the TurnStarted audit record. prov records which
// surface resolved the intent (deterministic menu match, disambiguation
// pick, …) so the persisted trace explains the transition.
func asyncSubmitDirectFromInput(orch *orchestrator.Orchestrator, sid app.SessionID, intent string, slots map[string]any, userInput string, prov orchestrator.RouteProvenance) func(context.Context) (*orchestrator.TurnOutcome, error) {
	return func(ctx context.Context) (*orchestrator.TurnOutcome, error) {
		return orch.SubmitDirectRouted(ctx, sid, intent, slots, userInput, prov)
	}
}

// startAsyncTurn puts the model into ModeAwaitingLLM and returns a tea.Cmd that
// runs the supplied turn function asynchronously. The caller must replace its
// own model with the returned model before returning from Update.
func startAsyncTurn(
	m RootModel,
	input string,
	run func(ctx context.Context) (*orchestrator.TurnOutcome, error),
	kind pendingKind,
) (RootModel, tea.Cmd) {
	ctx, cancel := context.WithCancel(context.Background())
	// Wire the meta-stream sink into the turn context so any
	// host.agent.ask_with_mcp call fired by an on_enter chain streams
	// live tool-use / narration events into the chat transcript — same
	// surface meta-mode already uses. The agent handler auto-enables
	// stream-json output when StreamSinkFrom(ctx) != nil, and the
	// orchestrator passes this ctx straight through to host.Invoke.
	if m.metaStreamSink != nil {
		ctx = host.WithStreamSink(ctx, m.metaStreamSink)
	}
	// Ambient editor context captured at submit (captureIDEAmbient). A
	// no-op (empty File) leaves the prompt scope byte-identical to a turn
	// with no editor; otherwise the agent handlers expose it as args.ide.
	ctx = host.WithIDEAmbient(ctx, m.pendingIDEAmbient)
	// Wire the operator prompter so a dispatched agent agent that forwards an
	// AskUserQuestion can surface it as an inline widget and block for the
	// operator's answer. Nil-safe: WithOperatorPrompter no-ops on a nil
	// prompter, leaving the headless tool-denied posture.
	if m.operatorPrompter != nil {
		ctx = host.WithOperatorPrompter(ctx, m.operatorPrompter)
	}
	// Wire the spatial prompter so a dispatched oracle that requests a spatial
	// ambient surfaces an OSC 8 link to a transient `/point` window and blocks
	// the turn for the operator's visual bundle. Nil-safe: WithSpatialPrompter
	// no-ops on a nil prompter, leaving the text-only headless posture.
	if m.spatialPrompter != nil {
		ctx = host.WithSpatialPrompter(ctx, m.spatialPrompter)
	}
	m.inFlightCancel = cancel
	m.mode = ModeAwaitingLLM
	m.pendingKind = kind
	// Fresh turn: never carry a deferred thought from a prior turn (the
	// previous turn's `result` event may have been dropped to
	// backpressure before it could clear the buffer).
	m.metaStreamPending = ""

	cmd := func() tea.Msg {
		out, err := run(ctx)
		if err != nil {
			// Only treat this as a cancellation when the error itself is a
			// context cancellation/deadline. Checking ctx.Err() alone is wrong:
			// the ctx may have been cancelled asynchronously after a genuine
			// non-cancellation error was produced, which would misclassify a
			// real failure as a clean cancel.
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return turnOutcomeMsg{
					outcome: &orchestrator.TurnOutcome{Mode: orchestrator.ModeCancelled},
					input:   input,
					err:     nil,
				}
			}
			return turnOutcomeMsg{outcome: nil, input: input, err: err}
		}
		return turnOutcomeMsg{outcome: out, input: input, err: nil}
	}
	return m, tea.Batch(m.spinner.Tick, cmd)
}

func (m RootModel) handleSlashCommand(cmd string) (tea.Model, tea.Cmd) {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return m, nil
	}

	// Resolve the app-declared off_path triggers, falling back to the
	// engine defaults when the app declares no off_path block.
	enterCmd, exitCmd := offPathTriggers(m.orch.AppDef())

	if parts[0] == enterCmd {
		return m.enterOffPath()
	}
	if parts[0] == exitCmd {
		return m.exitOffPath()
	}

	switch parts[0] {
	case "/quit", "/q":
		m.quitting = true
		return m, tea.Quit

	case "/input":
		// Restore the pre-widget draft into the prompt textarea. When
		// a choice widget seizes focus the textarea contents are
		// snapshotted to pendingDraft and cleared; /input re-hydrates
		// the buffer so the user can continue composing.
		if m.pendingDraft == "" {
			m.transcript.AppendSystem("(no pending draft to restore)")
			return m, nil
		}
		m.prompt.SetValue(m.pendingDraft)
		m.pendingDraft = ""
		m.transcript.AppendSystem("(restored your prior draft into the prompt)")
		return m, nil

	case "/help":
		body, next, cmd := HelpCommand{}.Run(m, parts[1:])
		next.transcript.AppendBlock(body)
		return next, cmd

	case "/intents":
		body, next, cmd := ActionsCommand{}.Run(m, parts[1:])
		if body != "" {
			next.transcript.AppendBlock(body)
		}
		return next, cmd

	case "/ideas":
		body, next, cmd := IdeasCommand{}.Run(m, parts[1:])
		if body != "" {
			next.transcript.AppendBlock(body)
		}
		return next, cmd

	case "/chat":
		body, next, cmd := ChatCommand{}.Run(m, parts[1:])
		if body != "" {
			next.transcript.AppendBlock(body)
		}
		return next, cmd

	case "/provider":
		body, next, cmd := ProviderCommand{}.Run(m, parts[1:])
		if body != "" {
			next.transcript.AppendBlock(body)
		}
		return next, cmd

	case "/model":
		body, next, cmd := ModelCommand{}.Run(m, parts[1:])
		if body != "" {
			next.transcript.AppendBlock(body)
		}
		return next, cmd

	case "/effort":
		body, next, cmd := EffortCommand{}.Run(m, parts[1:])
		if body != "" {
			next.transcript.AppendBlock(body)
		}
		return next, cmd

	case "/mine":
		body, next, cmd := MineCommand{}.Run(m, parts[1:])
		if body != "" {
			next.transcript.AppendBlock(body)
		}
		return next, cmd

	case "/jump":
		body, next, cmd := HandleJumpCommand(m, parts[1:])
		if body != "" {
			next.transcript.AppendBlock(body)
		}
		return next, cmd

	case "/menu":
		// Toggle menu visibility — for Stage 6 just echo.
		m.transcript.AppendSystem("(menu toggle — use arrow keys to navigate)")
		return m, nil

	case "/viz":
		// Write a DOT file for the current app.
		dotPath := m.orch.AppDef().App.ID + "-viz.dot"
		f, err := os.Create(dotPath)
		if err != nil {
			m.transcript.AppendSystem(fmt.Sprintf("(viz: could not create %s: %v)", dotPath, err))
		} else {
			if exportErr := viz.Export(m.orch.AppDef(), f); exportErr != nil {
				m.transcript.AppendSystem(fmt.Sprintf("(viz: export error: %v)", exportErr))
			} else {
				m.transcript.AppendSystem(fmt.Sprintf("(viz: wrote %s — render with: dot -Tpng %s -o graph.png)", dotPath, dotPath))
			}
			_ = f.Close()
		}
		return m, nil

	case "/trace":
		// /trace alone prints the last turn's routing trace as a
		// chat block (single-pane-tui §"/trace"). `/trace file`
		// toggles the JSONL file trace — preserved for back-compat.
		if len(parts) > 1 && parts[1] == "file" {
			return m.handleTraceToggle(), nil
		}
		m.transcript.AppendBlock(renderTraceBlock(m))
		return m, nil

	case "/mouse":
		// Single-pane redesign (phase 5): mouse support is removed.
		// /mouse stays as a friendly notice for muscle memory.
		m.transcript.AppendBlock(blocks.New(m.transcript.width, m.currentTheme()).SlashOutput(
			"(mouse capture was removed — native terminal selection works without modifiers)"))
		return m, nil

	case "/world":
		return m.openWorldView()

	case "/ide":
		return m.handleIDESlash(parts[1:])

	case "/open":
		return m.handleOpenSlash(parts[1:])

	case "/inbox":
		// Single-pane redesign: print the inbox inline as a chat
		// block. The legacy panel toggle is preserved for back-compat
		// until phase 3 deletes the panel.
		var block string
		var cmd tea.Cmd
		m, block, cmd = renderInboxBlock(m, parts[1:])
		m.transcript.AppendBlock(block)
		m.inbox.ToggleExpanded()
		return m, cmd

	case "/work":
		var block string
		m, block = renderWorkBlock(m, parts[1:])
		m.transcript.AppendBlock(block)
		return m, nil

	case "/meta":
		return m.handleMetaSlash(parts[1:])

	case "/sessions":
		return m.handleSessionsSlash(parts[1:])

	case "/warp":
		return m.handleWarpCommand(parts[1:])

	case "/reload":
		return m.handleReloadSlash(parts[1:])

	case "/workflow":
		return handleWorkflowSlash(m, parts[1:])

	default:
		m.transcript.AppendSystem(fmt.Sprintf("(unknown command: %s)", parts[0]))
		return m, nil
	}
}

// handleWarpCommand implements `/warp`, a generic developer teleport for
// smoke-testing flows. Three input shapes:
//
//  1. Inline:    /warp <state> [world.<k>=<v> ...]
//  2. File:      /warp file:<path>     (or any arg ending in .yaml/.yml/.json)
//  3. (Future) /warp trace:<path>      (planned: replay a trace.jsonl then warp)
//
// File form lets authors commit reusable "warp bases" alongside the app
// — `/warp file:./scenarios/chimney-robbery.yaml` is a one-liner that
// teleports into a fully-primed mid-game state. Same file YAML the
// flow-test fixtures use for initial_state + initial_world, but
// callable interactively against a live session.
//
// File schema:
//
//	# Optional: human-readable metadata
//	name: "Chimney Rock encounter"
//	description: "Primed party at Chimney Rock for the robbery flow."
//
//	# Required: destination state path (dot-separated)
//	state: leg_c_awaiting_reply
//
//	# Optional: world overrides; merged via Teleport.Slots
//	world:
//	  money: 400
//	  current_landmark: "Chimney Rock"
//	  party_alive: 5
//
// Inline value parsing: int → bool → string. Quoted values for spaces.
// Routes through Orchestrator.Teleport so world overrides become
// EffectApplied events the replay path reconstructs deterministically.
func (m RootModel) handleWarpCommand(args []string) (tea.Model, tea.Cmd) {
	if len(args) == 0 {
		m.transcript.AppendSystem("(warp: usage: /warp <state-path> [world.<k>=<v> ...]   or   /warp file:<path>)")
		return m, nil
	}

	// Detect file form. `file:<path>` is the explicit prefix; anything
	// else that looks like a YAML/JSON path falls through too so the
	// common case `/warp scenarios/foo.yaml` works without ceremony.
	first := args[0]
	if strings.HasPrefix(first, "file:") || isWarpFilePath(first) {
		path := strings.TrimPrefix(first, "file:")
		return m.handleWarpFromFile(path)
	}

	// Inline form. Reassemble args into a single line so we can
	// re-split with quote handling.
	raw := strings.Join(args, " ")
	tokens, err := shellLikeSplit(raw)
	if err != nil {
		m.transcript.AppendSystem(fmt.Sprintf("(warp: parse error: %v)", err))
		return m, nil
	}
	if len(tokens) == 0 {
		m.transcript.AppendSystem("(warp: missing state path)")
		return m, nil
	}

	target := app.StatePath(tokens[0])
	if !stateExistsInApp(m.orch.AppDef(), target) {
		m.transcript.AppendSystem(fmt.Sprintf("(warp: state %q not found in app)", target))
		return m, nil
	}

	slots := map[string]any{}
	for _, arg := range tokens[1:] {
		eq := strings.IndexByte(arg, '=')
		if eq <= 0 {
			m.transcript.AppendSystem(fmt.Sprintf("(warp: skipping %q — expected key=value)", arg))
			continue
		}
		key := strings.TrimPrefix(arg[:eq], "world.")
		val := parseWarpValue(arg[eq+1:])
		slots[key] = val
	}

	return m.dispatchWarp(target, slots, fmt.Sprintf("/warp %s", target))
}

// handleWarpFromFile reads a warp-basis YAML at path, validates the
// declared target state, and dispatches the teleport. The path is
// resolved relative to the app directory first, then relative to cwd,
// so `file:scenarios/foo.yaml` works both when run from the repo root
// and when run from a checkout of the app.
func (m RootModel) handleWarpFromFile(path string) (tea.Model, tea.Cmd) {
	resolved, basis, err := loadWarpBasis(path, m.appPath)
	if err != nil {
		m.transcript.AppendSystem(fmt.Sprintf("(warp file: %v)", err))
		return m, nil
	}
	if basis.State == "" {
		m.transcript.AppendSystem(fmt.Sprintf("(warp file %s: missing required `state:` field)", resolved))
		return m, nil
	}
	target := app.StatePath(basis.State)
	if !stateExistsInApp(m.orch.AppDef(), target) {
		m.transcript.AppendSystem(fmt.Sprintf("(warp file %s: state %q not found in app)", resolved, target))
		return m, nil
	}
	slots := make(map[string]any, len(basis.World))
	for k, v := range basis.World {
		slots[k] = v
	}
	label := resolved
	if basis.Name != "" {
		label = fmt.Sprintf("%s — %s", resolved, basis.Name)
	}
	return m.dispatchWarp(target, slots, fmt.Sprintf("/warp file:%s", label))
}

// dispatchWarp is the shared finish-line for inline and file-based
// warps. Appends the status line to the transcript and returns the
// tea.Cmd that runs the orchestrator's Teleport off the event loop.
func (m RootModel) dispatchWarp(target app.StatePath, slots map[string]any, inputLabel string) (tea.Model, tea.Cmd) {
	tgt := inbox.TeleportTarget{State: target, Slots: slots}
	m.transcript.AppendSystem(fmt.Sprintf("[/warp] → %s (world overrides: %d)", target, len(slots)))

	orch := m.orch
	sid := m.sid
	return m, func() tea.Msg {
		ctx := context.Background()
		out, err := orch.Teleport(ctx, sid, tgt)
		return turnOutcomeMsg{outcome: out, input: inputLabel, err: err}
	}
}

// isWarpFilePath reports whether s looks like a path to a YAML/JSON
// warp-basis file (used to detect the file form without the explicit
// `file:` prefix). Conservative: only triggers on file extensions and
// the presence of a path separator — bare names without an extension
// fall through to state-path parsing.
func isWarpFilePath(s string) bool {
	if strings.HasSuffix(s, ".yaml") || strings.HasSuffix(s, ".yml") || strings.HasSuffix(s, ".json") {
		return true
	}
	return false
}

// parseWarpValue coerces a raw `=`-separated value into the most natural Go
// type: int → bool → fallback string. Authors writing `money=400` get an
// int64; `bailed=true` becomes a bool; `current_landmark="Chimney Rock"`
// retains its (already-unquoted) string form.
func parseWarpValue(raw string) any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return n
	}
	if raw == "true" {
		return true
	}
	if raw == "false" {
		return false
	}
	return raw
}

// shellLikeSplit splits a string into tokens, honoring double-quoted
// segments. Single quotes are not treated specially. Escape sequences are
// not interpreted — the only goal is to allow `key="value with spaces"`.
func shellLikeSplit(s string) ([]string, error) {
	var out []string
	var cur strings.Builder
	inQuote := false
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			inQuote = !inQuote
		case c == ' ' && !inQuote:
			flush()
		default:
			cur.WriteByte(c)
		}
	}
	if inQuote {
		return nil, fmt.Errorf("unterminated quote")
	}
	flush()
	return out, nil
}

// stateExistsInApp reports whether the given dot-separated state path
// resolves through the AppDef's nested state tree. Used by /warp to reject
// typos with a clear message instead of letting Teleport fail downstream.
func stateExistsInApp(def *app.AppDef, path app.StatePath) bool {
	if def == nil || path == "" {
		return false
	}
	segments := strings.Split(string(path), ".")
	states := def.States
	for i, seg := range segments {
		s, ok := states[seg]
		if !ok || s == nil {
			return false
		}
		if i == len(segments)-1 {
			return true
		}
		states = s.States
	}
	return false
}

// handleMouseCommand was the /mouse on|off|toggle handler. Phase 5
// removed mouse support entirely; the /mouse command now just prints
// a removal notice (see the case in handleSlashCommand). The toggle
// helper is deleted.

// handleTraceToggle opens (first invocation) or closes (second invocation) a live
// JSONL trace file in /tmp. It wires a slog.JSONHandler into the orchestrator.
func (m RootModel) handleTraceToggle() RootModel {
	if m.traceFile != nil {
		// Close the trace — restore default (no-op) logger.
		_ = m.traceWriter.Flush()
		_ = m.traceFile.Close()
		m.orch.SetLogger(slog.Default())
		m.transcript.AppendSystem(fmt.Sprintf("(trace: closed %s)", m.traceFile.Name()))
		m.traceFile = nil
		m.traceWriter = nil
		return m
	}

	// Open a new temp file.
	f, err := os.CreateTemp("", "kitsoki-trace-*.jsonl")
	if err != nil {
		m.transcript.AppendSystem(fmt.Sprintf("(trace: could not create temp file: %v)", err))
		return m
	}
	bw := bufio.NewWriter(f)
	handler := slog.NewJSONHandler(bw, &slog.HandlerOptions{Level: slog.LevelDebug})
	logger := slog.New(handler)
	m.orch.SetLogger(logger)

	m.traceFile = f
	m.traceWriter = bw
	m.transcript.AppendSystem(fmt.Sprintf("(trace: writing to %s — /trace again to stop)", f.Name()))
	return m
}

// dispatchMenuEntry handles a menu row selection:
//   - If MissingSlots is empty: submit the full IntentCall directly via SubmitDirect
//     (bypasses the LLM harness entirely — we already know the intent + all slots).
//   - If MissingSlots is non-empty: open the clarification flow with PrefilledSlots
//     as the starting values.
func (m RootModel) dispatchMenuEntry(entry *orchestrator.MenuEntry) (tea.Model, tea.Cmd) {
	if len(entry.MissingSlots) == 0 {
		// All slots are known — submit directly. Run async with the spinner
		// so a slow on_enter host call (e.g. host.agent.ask) does not freeze
		// the TUI.
		slots := entry.PrefilledSlots
		if slots == nil {
			slots = make(map[string]any)
		}
		displayText := entry.Display
		m.lastInput = displayText
		return startAsyncTurn(m, displayText, asyncSubmitDirect(m.orch, m.sid, entry.Intent, slots), pendingDeterministic)
	}

	// Missing slots remain — open the clarification flow.
	// Convert SlotRef list to SlotNeed list.
	needs := make([]orchestrator.SlotNeed, len(entry.MissingSlots))
	for i, sr := range entry.MissingSlots {
		needs[i] = orchestrator.SlotNeed{
			Name:        sr.Name,
			Type:        sr.Type,
			Values:      sr.Values,
			Description: sr.Description,
			Prompt:      sr.Prompt,
		}
	}
	m.mode = ModeSlotFilling
	m.clarify.Open(entry.Intent, needs, entry.PrefilledSlots)
	// Echo the menu pick + render the inline "Clarification needed"
	// block. The user supplies values via the normal prompt (with the
	// `?` prefix) — see submitInput's ModeSlotFilling branch.
	m.transcript.AppendSystem("> " + entry.Display)
	if block := m.clarify.RenderInlineBlock(blocks.New(m.transcript.width, m.currentTheme())); block != "" {
		m.transcript.AppendBlock(block)
	}
	return m, nil
}

func (m RootModel) handleTurnOutcome(msg turnOutcomeMsg) (tea.Model, tea.Cmd) {
	// Clear in-flight state (safe to call even if already cleared).
	if m.inFlightCancel != nil {
		m.inFlightCancel = nil
	}
	if m.mode == ModeAwaitingLLM {
		m.mode = ModeOnPath
	}
	// Turn finished: drop any deferred final-answer thought so it can't
	// leak into a later turn's thinking. The room presents the response
	// in the view it renders below.
	m.metaStreamPending = ""

	prevState := string(m.currentState)

	if msg.err != nil {
		// Drop any in-flight routing placeholder; the user already saw
		// their echo, the error is a complete narrative.
		m.transcript.FinalizeLive("")
		m.transcript.AppendError("", fmt.Sprintf("error: %s", userfacing.Error(msg.err)))
		return m, nil
	}

	out := msg.outcome

	switch out.Mode {
	case orchestrator.ModeCancelled:
		m.transcript.FinalizeLive("")
		m.transcript.AppendSystem("(cancelled)")
		return m, nil

	case orchestrator.ModeTransitioned, orchestrator.ModeCompleted:
		m.currentState = out.NewState
		// A choice widget belongs to exactly one rendered room. A natural
		// free-text turn can advance while a prior room picker is active; close
		// it before inspecting the new typed view so stale choices cannot render
		// over the destination state.
		if m.choice.IsActive() {
			m.choice.Close()
			m.transcript.FinalizeLive("")
			if m.mode == ModeChoosing {
				m.mode = ModeOnPath
			}
		}
		// Swap the transcript buffer if this transition crossed a
		// room boundary (each room keeps its own transcript buffer).
		// No-op when prev/new share a top-level segment.
		m.maybeSwitchRoomOnState(app.StatePath(prevState), out.NewState)
		// Settle the inline routing block if it's still in flight
		// (deterministic-hit paths already settled at submit time;
		// LLM paths settle here). Then append the agent's body as a
		// separate entry — the header was already echoed at submit
		// time so AppendAgentBody / AppendAgentBodyTyped omit it.
		// pendingKind tracks why the model was AwaitingLLM. Deterministic turns
		// already settled their pipeline at submit time; the LLM path settles
		// here — the SINGLE finalizer, so a late hit event can't double-settle.
		deterministic := m.pendingKind == pendingDeterministic
		// Finalize the routing line only if an observer hit hasn't already (the
		// hit handler clears the live line when it settles). hasLive guards
		// against a double-commit regardless of which runs first.
		if !deterministic && m.transcript.hasLive() {
			// Turns that bypassed submitInput (e.g. a warp) never initialized the
			// pipeline — give it layers before resolving.
			if len(m.routing.layers) == 0 {
				m.routing = newRoutingPipeline()
			}
			// No hit event arrived live — fill the winner from the authoritative
			// TurnStarted provenance (or main-turn claude).
			if !m.routing.resolved() {
				routedBy, matchType, conf := provenanceFromEvents(out.Events)
				m.routing.resolveFromProvenance(routedBy, matchType, conf, intentFromEvents(out.Events))
			}
			m.transcript.FinalizeLive(m.routing.renderResolved())
		}
		// If the new view declares an interactive choice widget,
		// open it FIRST so we can strip the choice element from the
		// typed view passed to AppendAgentBodyTyped — otherwise the
		// body re-renders the static picker on top of the live widget.
		// Off-path takes precedence (the help banner owns the pane).
		typedForBody := out.TypedView
		if m.mode != ModeOffPath {
			if el, ok := findChoiceElement(out.TypedView); ok {
				if err := m.choice.Open(el, out.RenderEnv, out.Renderer); err != nil {
					slog.Warn("tui.choice.open", "err", err)
				} else {
					m.mode = ModeChoosing
					typedForBody = viewWithoutChoice(out.TypedView)
					// Snapshot the textarea draft (if any) so /input
					// can restore it; then clear so the user can't
					// type into an inert field while the widget owns
					// focus.
					m.pendingDraft = m.prompt.Value()
					m.prompt.SetValue("")
				}
			}
		}
		if typedForBody != nil {
			m.transcript.AppendAgentBodyTyped(out.View, typedForBody, out.RenderEnv, out.Renderer)
		} else {
			m.transcript.AppendAgentBody(out.View)
		}
		if m.mode == ModeChoosing {
			m.transcript.AppendLive(m.choice.View(m.transcript.wrapWidth()))
		}

		// Update menu.
		w := m.orch.InitialWorld() // only used for initial world; menu comes from allowed list
		m = m.updateMenuFromAllowed(out.AllowedIntents, w)

		// Update location.
		m = m.updateLocation(out)

		// Auto-print the intents block at end of turn when /intents
		// auto on was issued. The block follows the agent body so the
		// user sees: their input → resolution → result → next-step
		// actions, in scrollback-friendly order.
		if body := m.maybeAutoActions(); body != "" {
			m.transcript.AppendBlock(body)
		}

		if out.Mode == orchestrator.ModeCompleted {
			m.transcript.AppendSystem("\n[Game over — start a new session to play again]")
			m.prompt.Placeholder = "(game over)"
		}

	case orchestrator.ModeClarify:
		// Settle the routing block — the LLM router did its job,
		// the agent needs more slots.
		m.transcript.FinalizeLive("")
		// Enter slot-filling mode and render the inline "Clarification
		// needed" block. The user supplies values via the normal prompt
		// (with the `?` prefix) — see submitInput's ModeSlotFilling
		// branch.
		m.mode = ModeSlotFilling
		m.clarify.Open(out.PendingIntent, out.SlotsNeeded, out.PendingSlots)
		if block := m.clarify.RenderInlineBlock(blocks.New(m.transcript.width, m.currentTheme())); block != "" {
			m.transcript.AppendBlock(block)
		}

	case orchestrator.ModeOffPath:
		// A SYNCHRONOUS off-path outcome — the agent off-ramp fired on a
		// no-match free-text utterance in a room that declared
		// `agent_off_ramp:`. The orchestrator already ran the converse
		// turn and handed back the free-form answer in out.View WITHOUT
		// advancing the state machine or mutating world. We render that
		// answer as the soft off-path-themed agent bubble and leave the
		// user exactly where they were: same room, same menu.
		//
		// This is DISTINCT from the typed `/freeform` flow (offpath.go),
		// which flips the model into the persistent ModeOffPath *view
		// mode* and gates further input behind the async AskOffPath loop.
		// Here we must NOT enter that mode — the room is still on-path and
		// the next turn should route normally — so we keep m.mode at
		// ModeOnPath (already restored at the top of handleTurnOutcome).
		m.transcript.FinalizeLive("")
		// State is unchanged by contract; carry NewState through so a
		// non-empty resting path keeps currentState honest (it equals the
		// room the user is standing in).
		if out.NewState != "" {
			m.currentState = out.NewState
		}
		answer := out.View
		if answer == "" {
			answer = "(no reply)"
		}
		// Soft amber off-path answer styling — mirrors the typed-trigger
		// off-path reply (handleOffPathReply → AppendOffPathAnswer) so a
		// no-match answer reads as the same free-form voice. The user
		// echo already happened at submit time, so pass userInput="".
		m.transcript.AppendOffPathAnswer("", answer)
		// Re-assert the menu from the echoed (unchanged) allowed list so
		// the room's actions stay advertised, then refresh the location
		// bar against the unchanged resting state.
		w := m.orch.InitialWorld()
		m = m.updateMenuFromAllowed(out.AllowedIntents, w)
		m = m.updateLocation(out)

	case orchestrator.ModeRejected:
		m.transcript.FinalizeLive("")
		m.currentState = out.NewState

		// If there are disambiguation candidates, enter disambiguation
		// mode and render the inline "Did you mean?" block. The user
		// picks by typing the number or canonical intent name into the
		// normal prompt — see submitInput's ModeDisambiguating branch.
		if len(out.Candidates) > 0 {
			m.mode = ModeDisambiguating
			m.disambiguation.Open(out.Candidates)
			if block := m.disambiguation.RenderInlineBlock(blocks.New(m.transcript.width, m.currentTheme())); block != "" {
				m.transcript.AppendBlock(block)
			}
			// Site 31: emit disambig.presented when the overlay is shown.
			m.emitDisambigPresented(out.Candidates)
			return m, nil
		}

		// User echo already happened; rejection rendering writes only
		// the engine-side message body.
		m.renderRejection("", out)
	}

	// Drain the next queued in-room submission via dispatchInput
	// (skips the appendHistory step — the original enqueue already
	// recorded the line). The dispatch goes async so the UI
	// re-renders between items.
	if len(m.inputQueue) > 0 && m.mode == ModeOnPath {
		next := m.inputQueue[0]
		m.inputQueue = m.inputQueue[1:]
		return m.dispatchInput(next)
	}

	return m, nil
}

// renderRejection writes a friendly clarification or guard message into
// the transcript for a ModeRejected outcome. It branches on out.ErrorCode:
//
//   - UNKNOWN_INTENT / INTENT_NOT_ALLOWED_IN_STATE / INVALID_SLOT_VALUE:
//     the user said something the router could not map to an allowed
//     intent. Show "I didn't catch that. Try one of:" followed by the
//     current intent menu (one title + first example per entry). If the
//     author authored a guard_hint we still show it on a trailing line.
//
//   - GUARD_FAILED: an arm matched but a real guard refused. The author's
//     guard_hint (or the intent description, via Reason fallback in the
//     orchestrator path) is the canonical user-facing message; we render
//     it via AppendGuardHint which adds a soft "→ " prefix.
//
//   - any other code: fall back to AppendError with the message or code,
//     still prefixed with "→ ".
func (m *RootModel) renderRejection(userInput string, out *orchestrator.TurnOutcome) {
	switch out.ErrorCode {
	case intent.ErrUnknownIntent, intent.ErrIntentNotAllowed, intent.ErrInvalidSlotValue:
		menuText := m.intentMenuText()
		var msg strings.Builder
		msg.WriteString("I didn't catch that.")
		if menuText != "" {
			msg.WriteString(" Try one of:\n")
			msg.WriteString(menuText)
		}
		m.transcript.AppendClarification(userInput, msg.String())
		if out.GuardHint != "" {
			m.transcript.AppendGuardHint(out.GuardHint)
		}

	case "LLM_CLARIFICATION":
		// The LLM answered but didn't call the expected tool — its
		// free-form text was preserved as ErrorMessage. Render it as a
		// clarification (soft style) so the player sees the model
		// asking a follow-up question rather than a red technical error.
		m.transcript.AppendClarification(userInput, out.ErrorMessage)

	case intent.ErrGuardFailed:
		hint := out.GuardHint
		if hint == "" {
			hint = out.ErrorMessage
		}
		if hint == "" {
			hint = string(out.ErrorCode)
		}
		// Single-pane redesign: the user's input was already echoed
		// by submitInput via AppendUserInputEcho, so no extra header
		// here — just the guard hint.
		if userInput != "" {
			m.transcript.AppendTurn(userInput, "")
		}
		m.transcript.AppendGuardHint(hint)

	default:
		errMsg := out.GuardHint
		if errMsg == "" {
			errMsg = out.ErrorMessage
		}
		if errMsg == "" {
			errMsg = string(out.ErrorCode)
		}
		m.transcript.AppendError(userInput, errMsg)
	}
}

// intentMenuText returns a compact "Try one of:" suggestion list built
// from the current menu state (Primary entries only). Each line is the
// MenuEntry display (e.g. "go south") indented two spaces; we cap at
// a small N so a state with a long allowed-intent list does not flood
// the transcript on every misroute.
func (m *RootModel) intentMenuText() string {
	const maxSuggestions = 6
	w := m.orch.CurrentWorld(m.sid)
	menu := orchestrator.ComputeMenu(m.orch.AppDef(), m.orch.Machine(), m.currentState, w)
	if len(menu.Primary) == 0 {
		return ""
	}
	var sb strings.Builder
	n := len(menu.Primary)
	if n > maxSuggestions {
		n = maxSuggestions
	}
	for i := 0; i < n; i++ {
		entry := menu.Primary[i]
		sb.WriteString("  • ")
		sb.WriteString(entry.Display)
		sb.WriteString("\n")
	}
	if len(menu.Primary) > maxSuggestions {
		sb.WriteString(fmt.Sprintf("  …and %d more\n", len(menu.Primary)-maxSuggestions))
	}
	return strings.TrimRight(sb.String(), "\n")
}

func (m RootModel) handleContinueTurnOutcome(msg continueTurnOutcomeMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		// Reset mode so the user isn't stranded in ModeAwaitingLLM
		// after a clarify-continue failure. (handleSupplementSlots
		// parks the model in AwaitingLLM to keep impatient
		// double-Enter from triggering a competing Turn.)
		m.mode = ModeOnPath
		m.inFlightCancel = nil
		m.transcript.AppendError("(slot-fill)", fmt.Sprintf("error: %s", userfacing.Error(msg.err)))
		return m, nil
	}
	out := msg.outcome

	fakeMsg := turnOutcomeMsg{outcome: out, input: "(slot-fill)", err: nil}
	return m.handleTurnOutcome(fakeMsg)
}

// handleMetaSlash dispatches the `/meta ...` family. Recognised forms
// (all relative to the current app):
//
//	/meta                — enter the lex-first group's default verb
//	                       (back-compat: first lex meta-mode key when no
//	                       grouped modes exist)
//	/meta <group>        — enter the group's default verb (the mode
//	                       flagged `default: true`). Falls back to a
//	                       literal key match if no group named <group>
//	                       exists — that's the un-namespaced back-compat
//	                       path.
//	/meta <group> <verb> — enter the named meta mode keyed
//	                       `<group>.<verb>`
//	/meta list           — inline-list this app's meta chats
//	/meta new            — archive the active chat + open a fresh one
//	/meta resume <id...> — resume a past meta chat by id prefix (≥3)
//	/meta done           — archive the active chat + exit meta mode
//
// The list / new / resume / done subcommands close the discovery gap
// surfaced during manual smoke. Subcommand identity is by exact
// match on the first arg, so a meta mode literally named "list" or
// "done" would be unreachable via this surface — pick mode names
// outside the reserved set.
func (m RootModel) handleMetaSlash(args []string) (tea.Model, tea.Cmd) {
	if len(args) == 0 {
		return m.startMetaMode("")
	}
	switch args[0] {
	case "list":
		return m.handleMetaList()
	case "new":
		return m.handleMetaNew()
	case "resume":
		return m.handleMetaResume(args[1:])
	case "done":
		return m.handleMetaDone()
	}
	// Resolve as `<group> [<verb>]`. For one arg, prefer the group's
	// default verb; fall back to a literal map-key match (the
	// un-namespaced back-compat path). For two args, always look up
	// `<group>.<verb>`.
	return m.startMetaMode(m.resolveMetaName(args))
}

// resolveMetaName converts a /meta <args> tail into a `MetaModes` map
// key. Rules (proposal Phase B):
//
//   - len(args)==2 → "<group>.<verb>", verbatim. If not found,
//     startMetaMode will print the standard "unknown mode" error.
//   - len(args)==1 → if a mode literally keyed under args[0] exists,
//     return that (un-namespaced back-compat). Otherwise look for the
//     default verb of the group named args[0] and return its key.
//     Falling through with args[0] preserves the unknown-mode error
//     message the user expects to see.
//
// AppDef-less callers (no orchestrator AppDef yet) get the args joined
// by "." so the downstream error path still names what they typed.
func (m RootModel) resolveMetaName(args []string) string {
	def := m.orch.AppDef()
	if def == nil || len(args) == 0 {
		return strings.Join(args, ".")
	}
	if len(args) >= 2 {
		return args[0] + "." + args[1]
	}
	candidate := args[0]
	if _, ok := def.MetaModes[candidate]; ok {
		return candidate
	}
	// Look for `<candidate>.<verb>` where verb is the group's default.
	for key, mode := range def.MetaModes {
		if mode == nil {
			continue
		}
		if mode.Group == candidate && mode.Default {
			return key
		}
	}
	// Bare verb: `candidate` is not a group, but a grouped mode may use
	// it as its TRIGGER — `/meta bug`, `/meta ask`, `/meta edit`. These
	// are engine builtins keyed `story.bug` / `kitsoki.bug` etc.; the
	// grouping exists to avoid the trigger colliding with a story's
	// intent of the same name (see validateMetaModes), so the verbs are
	// not flat keys and need this lookup. Resolve to the matching verb,
	// preferring the story group so a bare `/meta <verb>` targets the
	// RUNNING STORY by default; engine-targeted variants are reached
	// explicitly via `/meta kitsoki <verb>`.
	if key := metaVerbKey(def, candidate); key != "" {
		return key
	}
	return candidate
}

// defaultMetaGroup is the meta-mode group that bare `/meta` and bare
// `/meta <verb>` resolve into by default — the story-targeting builtins.
// Engine-targeting (`kitsoki.*`) is always explicit (`/meta kitsoki …`).
const defaultMetaGroup = "story"

// metaVerbKey resolves a bare verb (the trigger of a grouped meta mode)
// to a MetaModes key, preferring [defaultMetaGroup] so bare `/meta bug`
// targets the running story. Returns "" when no mode declares trigger.
// Iterates in lex order so the non-story fallback is deterministic.
func metaVerbKey(def *app.AppDef, trigger string) string {
	var fallback string
	for _, name := range sortedMetaModeNames(def) {
		m := def.MetaModes[name]
		if m == nil || m.Trigger != trigger {
			continue
		}
		if m.Group == defaultMetaGroup {
			return name
		}
		if fallback == "" {
			fallback = name
		}
	}
	return fallback
}

// handleMetaList kicks off a transcript-inline listing of this app's
// meta chats. Works from any mode (ModeOnPath or ModeMeta) — the
// caller can browse without leaving / before entering meta.
func (m RootModel) handleMetaList() (tea.Model, tea.Cmd) {
	if m.metaController == nil {
		m.transcript.AppendSystem("(meta list: unavailable — no chat store or meta_modes wired to this session)")
		return m, nil
	}
	appID := ""
	if def := m.orch.AppDef(); def != nil {
		appID = def.App.ID
	}
	return m, metaListCmd(context.Background(), m.metaController, appID)
}

// handleMetaNew is only valid while in ModeMeta — outside, there is
// no "active chat" to archive. Outside the surface prints a hint.
func (m RootModel) handleMetaNew() (tea.Model, tea.Cmd) {
	if m.mode != ModeMeta {
		m.transcript.AppendSystem("(meta new: only valid inside meta mode — use /meta to enter first)")
		return m, nil
	}
	if m.metaController == nil || m.metaMode.session == nil {
		m.transcript.AppendSystem("(meta new: unavailable — no active session)")
		return m, nil
	}
	return m, metaNewCmd(context.Background(), m.metaController, m.metaMode.session)
}

// handleMetaDone archives the active chat and exits meta mode. Only
// valid while in ModeMeta — outside, there's no active chat to close.
// Differs from /onpath (which exits without archiving — the chat
// persists for resume) and from /meta new (which archives + opens a
// fresh row in the same scope).
func (m RootModel) handleMetaDone() (tea.Model, tea.Cmd) {
	if m.mode != ModeMeta {
		m.transcript.AppendSystem("(meta done: only valid inside meta mode — use /meta to enter first)")
		return m, nil
	}
	if m.metaController == nil || m.metaMode.session == nil {
		m.transcript.AppendSystem("(meta done: unavailable — no active session)")
		return m, nil
	}
	return m, metaDoneCmd(context.Background(), m.metaController, m.metaMode.session)
}

// handleMetaDoneDone reacts to the async archive completing. On
// success the overlay closes, mode returns to ModeOnPath, and the
// transcript carries an "archived" confirmation that surfaces the
// 8-char id prefix so the user can recover via /meta resume if they
// regret it.
func (m RootModel) handleMetaDoneDone(msg metaDoneDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.transcript.AppendError("(meta done)", fmt.Sprintf("error: %s", userfacing.Error(msg.err)))
		return m, nil
	}
	id := msg.archivedID
	short := id
	if len(short) > 8 {
		short = short[:8]
	}
	m.metaMode.Exit()
	m.mode = ModeOnPath
	m.transcript.AppendSystem(fmt.Sprintf(
		"(meta done: archived chat %s — resume with /meta resume %s if you change your mind)",
		short, short))
	return m, nil
}

// handleMetaResume resolves an id prefix to a chat ID then enters
// (or re-enters) meta mode against that chat. Works from any mode.
// On ambiguity the matches are listed and no mode change happens.
func (m RootModel) handleMetaResume(args []string) (tea.Model, tea.Cmd) {
	if m.metaController == nil {
		m.transcript.AppendSystem("(meta resume: unavailable — no chat store or meta_modes wired to this session)")
		return m, nil
	}
	if len(args) == 0 {
		m.transcript.AppendSystem("(meta resume: usage — /meta resume <id-prefix> (≥3 chars))")
		return m, nil
	}
	prefix := args[0]
	appID := ""
	if def := m.orch.AppDef(); def != nil {
		appID = def.App.ID
	}
	fullID, err := m.metaController.ResolveChatIDPrefix(context.Background(), appID, prefix)
	if err != nil {
		var amb *metamode.AmbiguousPrefixError
		if errors.As(err, &amb) {
			m.transcript.AppendSystem(fmt.Sprintf("(meta resume: prefix %q matched %d chats — retype with more characters:)", amb.Prefix, len(amb.Matches)))
			for _, id := range amb.Matches {
				m.transcript.AppendSystem("  - " + id)
			}
			return m, nil
		}
		m.transcript.AppendSystem(fmt.Sprintf("(meta resume: %v)", err))
		return m, nil
	}

	// Look up the chat row to discover its mode (the room key).
	listings, lerr := m.metaController.ListChats(context.Background(), appID)
	if lerr != nil {
		m.transcript.AppendSystem(fmt.Sprintf("(meta resume: %v)", lerr))
		return m, nil
	}
	var modeName string
	for _, l := range listings {
		if l.ID == fullID {
			modeName = l.ModeName
			break
		}
	}
	if modeName == "" {
		m.transcript.AppendSystem(fmt.Sprintf("(meta resume: chat %s not present in this app's meta-chat list)", fullID))
		return m, nil
	}

	snap := metamode.Snapshot{
		SessionID: m.sid,
		State:     m.currentState,
		World:     m.orch.CurrentWorld(m.sid),
		EnteredAt: time.Now(),
	}
	return m, metaResumeCmd(context.Background(), m.metaController, snap, modeName, fullID)
}

// handleMetaListDone renders the listing into the transcript. Called
// from updateMeta (when listing was triggered from inside meta) and
// from the on-path update path (handled below).
func (m RootModel) handleMetaListDone(msg metaListDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.transcript.AppendSystem(fmt.Sprintf("(meta list: %v)", msg.err))
		return m, nil
	}
	rows := make([][]string, 0, len(msg.listings))
	for _, l := range msg.listings {
		rows = append(rows, metaListingCells(l))
	}
	m.transcript.AppendMetaList(metaListColumns(), rows)
	return m, nil
}

// metaListColumns is the header row for /meta list. Order matches
// metaListingCells / formatMetaListing.
func metaListColumns() []string {
	return []string{"ID", "MODE", "SCOPE", "UPDATED", "PREVIEW"}
}

// openSessionsPanel kicks off the foyer "meta sessions" overlay.
// The controller call is async (it touches the
// chats DB), so we leave the model in ModeOnPath until the
// sessionsPanelLoadedMsg comes back. If meta plumbing is missing
// we surface a hint via the transcript instead of opening an empty
// panel — the user gets the same diagnostic the inline `/meta list`
// would have produced.
func (m RootModel) openSessionsPanel() (tea.Model, tea.Cmd) {
	if m.metaController == nil {
		m.transcript.AppendSystem("(meta sessions: unavailable — no chat store or meta_modes wired to this session)")
		return m, nil
	}
	appID := ""
	if def := m.orch.AppDef(); def != nil {
		appID = def.App.ID
	}
	if appID == "" {
		m.transcript.AppendSystem("(meta sessions: unavailable — no app loaded)")
		return m, nil
	}
	return m, sessionsPanelLoadCmd(context.Background(), m.metaController, appID)
}

// sessionsPanelLoadCmd asynchronously fetches the listing for the
// foyer panel. Errors are surfaced via the err field of the loaded
// msg; the handler renders them as a transcript-system line so the
// user knows why the panel didn't open.
func sessionsPanelLoadCmd(ctx context.Context, ctrl *metamode.Controller, appID string) tea.Cmd {
	return func() tea.Msg {
		listings, err := ctrl.ListChats(ctx, appID)
		return sessionsPanelLoadedMsg{listings: listings, err: err}
	}
}

// handleSessionsPanelLoaded is invoked when the async ListChats call
// completes. On error the panel stays closed and the user sees the
// error in the transcript. On success we Open() the panel with the
// loaded rows and switch the mode so the overlay owns the keyboard.
func (m RootModel) handleSessionsPanelLoaded(msg sessionsPanelLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.transcript.AppendSystem(fmt.Sprintf("(meta sessions: %v)", msg.err))
		return m, nil
	}
	m.sessionsPanel.Open(msg.listings)
	m.mode = ModeMetaSessions
	return m, nil
}

// handleSessionsPanelChoice resumes the chat the user picked from the
// foyer panel. Identical code path to `/meta resume <id>` past the
// snapshot construction.
func (m RootModel) handleSessionsPanelChoice(msg sessionsPanelChoiceMsg) (tea.Model, tea.Cmd) {
	m.mode = ModeOnPath
	if m.metaController == nil {
		m.transcript.AppendSystem("(meta sessions: unavailable — no chat store or meta_modes wired to this session)")
		return m, nil
	}
	if msg.chatID == "" || msg.modeName == "" {
		m.transcript.AppendSystem("(meta sessions: panel returned an empty selection)")
		return m, nil
	}
	snap := metamode.Snapshot{
		SessionID: m.sid,
		State:     m.currentState,
		World:     m.orch.CurrentWorld(m.sid),
		EnteredAt: time.Now(),
	}
	return m, metaResumeCmd(context.Background(), m.metaController, snap, msg.modeName, msg.chatID)
}

// updateSessionsPanel routes keyboard input to the foyer overlay and
// — when the user closes it without picking — returns the mode to
// ModeOnPath so the prompt is ready to receive input again.
func (m RootModel) updateSessionsPanel(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case sessionsPanelChoiceMsg:
		return m.handleSessionsPanelChoice(msg)
	}
	var cmd tea.Cmd
	m.sessionsPanel, cmd = m.sessionsPanel.Update(msg)
	if !m.sessionsPanel.IsActive() && m.mode == ModeMetaSessions {
		m.sessionsPanel.Close()
		m.mode = ModeOnPath
	}
	return m, cmd
}

// dedupSorted returns the unique values from in, sorted lexicographically.
// Used by the meta-mode exit summary to list every file the agent
// touched across the session without repeats.
func dedupSorted(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// handleMetaNewDone swaps the active session's chat to the freshly
// resolved one, clears the transcript pane, and stays in ModeMeta.
func (m RootModel) handleMetaNewDone(msg metaNewDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.transcript.AppendError("(meta new)", fmt.Sprintf("error: %s", userfacing.Error(msg.err)))
		return m, nil
	}
	if msg.session == nil {
		m.transcript.AppendError("(meta new)", "controller returned no session")
		return m, nil
	}
	m.metaMode.session = msg.session
	// Reset the transcript so the new chat starts clean. The banner
	// is re-emitted below so the user sees they're still in meta.
	m.transcript = newTranscriptModel(m.transcript.vp.Width, m.transcript.vp.Height)
	if banner := m.metaMode.Banner(); banner != "" {
		m.transcript.AppendSystem(banner)
	}
	m.transcript.AppendSystem("(meta: opened a fresh chat — the previous one was archived)")
	return m, nil
}

// metaListingCells renders one /meta list row as a slice of column
// cells. AppendMetaList does the width-aware padding so columns
// align across rows AND with metaListColumns()'s header.
//
// id is truncated to its first 8 chars; the preview to 50 chars
// (which is shorter than the 100-char preview the controller
// returns — listings stay scannable while resume keeps enough
// context to disambiguate).
func metaListingCells(l metamode.ChatListing) []string {
	id8 := l.ID
	if len(id8) > 8 {
		id8 = id8[:8]
	}
	scope := l.ScopeKey
	if scope == "" {
		scope = "(no scope)"
	}
	ts := l.UpdatedAt.Local().Format("2006-01-02 15:04")
	preview := l.FirstUserMessage
	if preview == "" {
		preview = "(no user turn yet)"
	}
	if r := []rune(preview); len(r) > 50 {
		preview = string(r[:50])
	}
	return []string{id8, l.ModeName, scope, ts, preview}
}

// startMetaMode dispatches a /meta <name> entry. When name is empty,
// the lexicographically-first declared GROUP's default verb is chosen
// (or, when no grouped modes exist, the lex-first key — back-compat
// behaviour). Surfaces a polite hint when the controller is not wired
// (no chat store, no meta_modes declared, or no agent registry
// installed).
func (m RootModel) startMetaMode(name string) (tea.Model, tea.Cmd) {
	if m.metaController == nil {
		m.transcript.AppendSystem("(meta mode: unavailable — no chat store or meta_modes wired to this session)")
		return m, nil
	}
	def := m.orch.AppDef()
	if def == nil || len(def.MetaModes) == 0 {
		m.transcript.AppendSystem("(meta mode: this app declares no meta_modes)")
		return m, nil
	}
	names := sortedMetaModeNames(def)
	if name == "" {
		name = pickDefaultMetaMode(def, names)
	}
	if _, ok := def.MetaModes[name]; !ok {
		m.transcript.AppendSystem(fmt.Sprintf("(meta mode: unknown mode %q — declared: %s)",
			name, strings.Join(names, ", ")))
		return m, nil
	}

	// Build the snapshot from the current orchestrator state. The
	// session id is captured so a future "drift on Exit" check has
	// something to compare against.
	snap := metamode.Snapshot{
		SessionID: m.sid,
		State:     m.currentState,
		World:     m.orch.CurrentWorld(m.sid),
		EnteredAt: time.Now(),
	}

	ctrl := m.metaController
	return m, metaEnterCmd(context.Background(), ctrl, snap, name)
}

// handleMetaEnterDone activates the overlay (or surfaces an error) when
// Controller.Enter / EnterByChatID returns asynchronously.
//
// When already in ModeMeta (e.g. /meta resume from inside meta), the
// transcript pane is reset before the banner + replay so the prior
// session's content doesn't bleed into the new one.
func (m RootModel) handleMetaEnterDone(msg metaEnterDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.transcript.AppendError("/meta "+msg.modeName,
			fmt.Sprintf("error opening meta mode: %v", msg.err))
		return m, nil
	}
	if msg.session == nil {
		m.transcript.AppendError("/meta "+msg.modeName, "meta controller returned no session")
		return m, nil
	}

	// Re-entering meta from inside meta (resume) clears the pane so
	// the new chat starts visually fresh; the prior meta-chat buffer
	// is dropped on the floor. First-entry from on-path swaps to the
	// synthetic meta room with a fresh transcript so the on-path
	// buffer is preserved for /onpath. The proposal calls meta entry
	// "always a transient-style entry with theme swap" — the
	// activateRoom call below realises both.
	if m.mode == ModeMeta {
		// Already in a meta room; drop the saved buffer so the
		// fresh-chat behaviour matches the pre-rooms code path.
		if m.transcripts != nil {
			delete(m.transcripts, metaRoomKey)
		}
		m.activeRoom = ""
	}
	m.activateRoom(metaRoomKey, true)

	// Stash any queued on-path input back into history before
	// entering the meta room. The queue is single-instance (not
	// per-room); without this the items would dispatch in the meta
	// context on /onpath return, which the user did not intend.
	// Stashing keeps them recoverable with the ↑ arrow.
	if len(m.inputQueue) > 0 {
		for _, q := range m.inputQueue {
			m.appendHistory(q)
		}
		m.transcript.AppendSystem(fmt.Sprintf("(%d queued items stashed to history — recover with ↑)", len(m.inputQueue)))
		m.inputQueue = nil
	}

	m.metaMode.Enter(msg.session)
	m.mode = ModeMeta
	m.prompt.Placeholder = "meta chat — /onpath to return"
	// Swap the textarea's per-line prefix to "» " for meta mode. The
	// continuation indent (line idx > 0) is the same 2-column gutter
	// so wrapped meta-chat input stays visually aligned. Style stays
	// on the on-path violet/bold.
	setPromptPrefix(&m.prompt, promptPrefixMeta)
	setPromptStyle(&m.prompt, promptStyle)

	// Banner first so the user sees the mode they entered.
	if banner := m.metaMode.Banner(); banner != "" {
		m.transcript.AppendSystem(banner)
	}

	// Replay any prior transcript persisted on the chat row. Without
	// this the in-memory transcript starts blank even when the chat
	// row already has messages from a previous /meta session.
	if m.chatStore != nil {
		m.replayMetaTranscript(msg.session.Chat.ID())
	}
	return m, nil
}

// replayMetaTranscript loads the chat-row messages for the given chat
// id and appends each one to the in-memory transcript. Errors are
// non-fatal — a missing transcript just means an empty replay.
func (m *RootModel) replayMetaTranscript(chatID string) {
	if m.chatStore == nil || chatID == "" {
		return
	}
	ctx := context.Background()
	msgs, err := m.chatStore.Transcript(ctx, chatID, 0)
	if err != nil {
		slog.Warn("tui.meta: replay transcript failed", "err", err, "chat_id", chatID)
		return
	}
	for _, msg := range msgs {
		switch msg.Role {
		case "user":
			m.transcript.AppendTurn(msg.Content, "")
		case "assistant":
			m.transcript.AppendSystem(msg.Content)
		default:
			// system / tool / anything else — surface verbatim so the
			// user sees the same history the chat row holds.
			m.transcript.AppendSystem(msg.Content)
		}
	}
}

// handleMetaSendDone reacts to one completed meta-mode turn. The user
// message was already appended to the in-memory transcript when the
// user hit Enter; here we append the assistant reply and, when the
// authoring tools fired, reload the orchestrator.
// handleMetaStreamEvent renders one streaming agent event into the
// chat transcript as a muted "→ …" line. Called from updateMeta only
// (the top-level Update silently drops stale events from cancelled or
// already-finished sends).
//
// Per-event rules:
//   - assistant + tool name → "Tool args" (e.g. "Read prompt.md")
//   - assistant + narration → just the preview text
//   - system.api_retry      → "(retrying claude request…)"
//   - user                  → SKIPPED (tool results: too noisy)
//   - result                → SKIPPED (final reply lands via
//     metaSendDoneMsg's AppendSystem; the stream-side result event
//     would duplicate it)
//
// The handler fires whenever an agent call is in flight — that's
// either a meta-mode turn (ModeMeta + metaMode.inFlight) or an
// on-path turn that's invoking host.agent.ask_with_mcp from an
// on_enter chain (ModeAwaitingLLM). Both surface the live tool-use
// trail so the user sees progress instead of a silent spinner.
// Defensive: events arriving when neither path is in flight are
// dropped — they'd land underneath whatever the user is doing now
// and just confuse them.
func (m RootModel) handleMetaStreamEvent(msg MetaStreamMsg) RootModel {
	// Only render while some turn is in flight. Stale events from a
	// cancelled or just-finished call would otherwise land in the
	// transcript after the final reply.
	inFlight := (m.mode == ModeMeta && m.metaMode.inFlight) ||
		m.mode == ModeAwaitingLLM
	if !inFlight {
		return m
	}
	ev := msg.Event
	switch ev.Type {
	case "assistant":
		if ev.Thinking != "" {
			// Extended-thinking prose. Unlike pure narration (which is
			// deferred below because the FINAL reply also arrives as a
			// plain assistant text message), thinking is never the reply —
			// render it immediately, above whatever this event also
			// carries. Any earlier deferred thought is proven intermediate
			// by this fresh assistant event, so flush it first.
			m = m.flushPendingThought()
			m.transcript.AppendMetaThinking(ev.Thinking)
		}
		if ev.Tool != "" {
			// A thought paired with a tool call is unambiguously
			// intermediate — a tool round-trip still follows, so it is
			// never the final answer. Any earlier deferred thought is
			// therefore also intermediate; flush it, then render this
			// one in full plus the compact tool breadcrumb. Render the
			// thought first so it reads above the action it explains.
			m = m.flushPendingThought()
			if ev.Text != "" {
				// Narration / "thinking" prose, in full — the transcript
				// word-wraps it. Tight (no leading blank line) so
				// consecutive thoughts read as one paragraph.
				m.transcript.AppendMetaThinking(ev.Text)
			}
			// Tool use: compact one-line args breadcrumb (Preview is
			// already clipped upstream), separate styling, leading blank
			// line for breathing room (inside AppendMetaToolUse). One
			// assistant event can batch several parallel tool calls, so
			// render each on its own line (ev.Tools); fall back to the
			// scalar ev.Tool for events that predate the slice.
			if len(ev.Tools) > 0 {
				for _, tc := range ev.Tools {
					m.transcript.AppendMetaToolUse(tc.Name, tc.Preview)
				}
			} else {
				m.transcript.AppendMetaToolUse(ev.Tool, ev.Preview)
			}
		} else if ev.Text != "" {
			// Pure narration with no tool call. Ambiguous until the next
			// event: intermediate thought (flush) or final answer (drop
			// on `result`). A previously deferred thought is now proven
			// intermediate — this fresh assistant message followed it —
			// so flush that one, then defer this one in its place.
			m = m.flushPendingThought()
			m.metaStreamPending = ev.Text
		}
	case "system":
		if ev.Subtype == "api_retry" {
			// A retry means more model output follows; any held thought
			// was intermediate.
			m = m.flushPendingThought()
			m.transcript.AppendMetaSystemNotice("(retrying claude request…)")
		}
	case "user":
		// tool_result: noisy content we don't render, but its arrival
		// proves any deferred thought preceded more work — flush it.
		m = m.flushPendingThought()
	case "result":
		// Terminal event: a deferred thought was the model's FINAL
		// answer. DROP it — the room (on-path) or metaSendDone's
		// AppendSystem (meta) presents the final reply, so echoing it
		// here as thinking would duplicate it.
		m.metaStreamPending = ""
	}
	return m
}

// flushPendingThought commits any deferred pure-narration message to the
// transcript as "thinking" and clears the buffer. A no-op when nothing
// is pending. See metaStreamPending for why narration is deferred.
func (m RootModel) flushPendingThought() RootModel {
	if m.metaStreamPending != "" {
		m.transcript.AppendMetaThinking(m.metaStreamPending)
		m.metaStreamPending = ""
	}
	return m
}

func (m RootModel) handleMetaSendDone(msg metaSendDoneMsg) (tea.Model, tea.Cmd) {
	m.metaMode.inFlight = false
	// Turn finished: drop any thought still deferred (it was the final
	// answer, surfaced below via AppendSystem) so it can't leak into the
	// next turn's thinking if the `result` stream event was lost.
	m.metaStreamPending = ""

	if msg.err != nil {
		if errors.Is(msg.err, metamode.ErrChatBusy) {
			// Another driver (a queued drive dispatch, a parallel
			// `kitsoki chat continue`, or an in-progress
			// `kitsoki chat attach`) is holding the per-chat lock.
			// Surface a TUI-friendly note rather than the raw
			// "metamode: chat busy" wrapper.
			m.transcript.AppendError("(meta)",
				"this chat is currently held by another driver — wait for it to release, or run `kitsoki chat unlock --force <chat-id>` if you know it's stuck")
			return m, nil
		}
		m.transcript.AppendError("(meta)", fmt.Sprintf("error: %s", userfacing.Error(msg.err)))
		return m, nil
	}
	m.metaMode.turns++
	if msg.result.Assistant != "" {
		m.transcript.AppendSystem(msg.result.Assistant)
	}
	// Surface the /attach affordance once per overlay so the user
	// knows the in-TUI hand-off: type /attach to drop into a real
	// `claude --resume` session for this chat, detach with Ctrl-B
	// then d, and come back here with the conversation intact.
	if m.metaMode.turns == 1 && msg.result.ChatID != "" {
		m.transcript.AppendSystem(
			"tip: /attach drops you into the full claude UI for this chat; Ctrl-B then d leaves claude running in the background",
		)
	}
	if msg.result.ReloadRequested && m.appPath != "" {
		m.metaMode.edits++
		m.metaMode.changedFiles = append(m.metaMode.changedFiles, msg.result.ChangedFiles...)
		return m.reloadOrchestratorAfterMetaWithFiles(msg.result.ChangedFiles)
	}
	return m, nil
}

// handleReloadSlash implements `/reload`. It hot-swaps the app
// definition from disk and re-fires the current state's on_enter
// chain so view-template edits, on_enter additions, or agent-prompt
// changes take effect without restarting the session.
//
// The flow:
//
//  1. Swap the app def via Orchestrator.Reload (synchronous, fast
//     file I/O). On error the session is left untouched and the
//     transcript surfaces the load failure.
//  2. Refresh the menu, location, and prompt placeholder so they
//     reflect the new app graph.
//  3. Dispatch Orchestrator.RerunOnEnter asynchronously — the
//     current room's on_enter typically calls an agent and we don't
//     want the TUI to freeze for ~60s. The TurnOutcome flows back
//     through the existing turnOutcomeMsg handler, which re-renders
//     the view and resets the prompt.
//
// `/reload` is destructive on side-effect-bearing on_enter chains
// (it WILL re-invoke an agent, re-post to a transport, etc.). The
// trade-off is intentional — the operator explicitly asked for "redo
// whatever actions" so that the dogfood "edit story externally,
// observe the new behaviour" loop closes without a TUI restart.
func (m RootModel) handleReloadSlash(args []string) (tea.Model, tea.Cmd) {
	forceOnce := false
	for _, arg := range args {
		switch arg {
		case "--force", "-f":
			forceOnce = true
		default:
			m.transcript.AppendSlashOutput(fmt.Sprintf("(/reload: unknown option %q; supported: --force)", arg))
			return m, nil
		}
	}
	if m.appPath == "" {
		m.transcript.AppendSlashOutput(
			"(/reload disabled — no app path was passed to NewRootModel; restart with `kitsoki run <app.yaml>` to enable hot-reload)")
		return m, nil
	}
	if m.mode == ModeAwaitingLLM {
		m.transcript.AppendSlashOutput(
			"(/reload: a turn is in flight — wait for it to settle or press Ctrl+C to cancel, then try again)")
		return m, nil
	}

	res, err := m.orch.Reload(m.appPath, m.currentState)
	if err != nil {
		m.transcript.AppendWarning("/reload",
			fmt.Sprintf("Attempting to reload the story failed due to syntax errors — "+
				"keeping the previous version running. Fix the file and reload again.\n  %v", err))
		return m, nil
	}

	// Record the story change into the trace so the replay stays
	// self-contained even after a hot-reload (see store.StoryChanged).
	if recErr := m.orch.RecordEffectiveStory(context.Background(), m.sid); recErr != nil {
		m.transcript.AppendError("/reload",
			fmt.Sprintf("reloaded, but recording the story change failed: %v", recErr))
	}

	// Refresh the menu, location, and prompt placeholder against the
	// post-reload world so they reflect any new/removed intents or
	// menu labels declared in the freshly loaded definition.
	w := m.orch.CurrentWorld(m.sid)
	computed := orchestrator.ComputeMenu(m.orch.AppDef(), m.orch.Machine(), m.currentState, w)
	m.menu, _ = m.menu.Update(menuItemsChanged{items: computed.Primary, blocked: computed.Blocked})
	m.refreshPromptPlaceholder()
	loc := orchestrator.ComputeLocation(m.orch.AppDef(), m.currentState, w, 0)
	m.location, _ = m.location.Update(locationUpdated{loc: loc})

	if !res.PrevStateExists {
		m.transcript.AppendSlashOutput(
			"(/reload: current state was removed in the new definition — re-render only, no on_enter rerun)")
		if view, vErr := m.orch.Machine().RenderState(m.currentState, w); vErr == nil && view != "" {
			m.transcript.AppendSystem(view)
		}
		return m, nil
	}

	if forceOnce {
		m.transcript.AppendSlashOutput("(/reload --force: definition reloaded — re-firing on_enter and bypassing once: cache)")
	} else {
		m.transcript.AppendSlashOutput("(/reload: definition reloaded — re-firing on_enter)")
	}

	// Dispatch RerunOnEnter via the same async-turn machinery the
	// menu uses. The resulting TurnOutcome flows back through
	// handleTurnOutcome which renders the view and resets state.
	return startAsyncTurn(m, "/reload", asyncRerunOnEnter(m.orch, m.sid, forceOnce), pendingDeterministic)
}

// asyncRerunOnEnter returns the closure shape startAsyncTurn expects
// for the `/reload` path. RerunOnEnter has no user-input string of
// its own, so the closure ignores the surrounding input plumbing.
func asyncRerunOnEnter(orch *orchestrator.Orchestrator, sid app.SessionID, forceOnce bool) func(context.Context) (*orchestrator.TurnOutcome, error) {
	return func(ctx context.Context) (*orchestrator.TurnOutcome, error) {
		return orch.RerunOnEnterWithOptions(ctx, sid, orchestrator.RerunOnEnterOptions{
			ForceOnce: forceOnce,
		})
	}
}

// reloadOrchestratorAfterMetaWithFiles runs the same reload-and-rerender
// path the edit overlay uses on apply and prints the list of files the
// agent touched so the user can see whether the change landed in
// app.yaml, an include, a prompt, or a script.
func (m RootModel) reloadOrchestratorAfterMetaWithFiles(changed []string) (tea.Model, tea.Cmd) {
	res, err := m.orch.Reload(m.appPath, m.currentState)
	if err != nil {
		m.transcript.AppendWarning("(meta)",
			"Attempting to reload the story failed due to syntax errors — "+
				"your edit is saved, but I'm keeping the previous version running. "+
				"Fix the file and reload again.\n  "+err.Error())
		return m, nil
	}
	// Record the meta edit into the trace so the replay stays self-contained
	// (the edit may be uncommitted, so a git sha can't name it).
	if recErr := m.orch.RecordEffectiveStory(context.Background(), m.sid); recErr != nil {
		m.transcript.AppendError("(meta)",
			"reloaded, but recording the story change failed: "+recErr.Error())
	}
	w := m.orch.CurrentWorld(m.sid)
	computed := orchestrator.ComputeMenu(m.orch.AppDef(), m.orch.Machine(), m.currentState, w)
	m.menu, _ = m.menu.Update(menuItemsChanged{items: computed.Primary, blocked: computed.Blocked})
	m.refreshPromptPlaceholder()
	loc := orchestrator.ComputeLocation(m.orch.AppDef(), m.currentState, w, 0)
	m.location, _ = m.location.Update(locationUpdated{loc: loc})

	// Mark the line where the post-reload section begins so we can scroll
	// the meta-mode chat off the top of the viewport while keeping it
	// reachable via scroll-up.
	mark := m.transcript.ContentHeight()

	var note string
	if len(changed) == 0 {
		note = fmt.Sprintf("(✓ reloaded — edit #%d this session)", m.metaMode.edits)
	} else {
		note = fmt.Sprintf("(✓ saved + reloaded — edit #%d this session)\n  changed: %s",
			m.metaMode.edits, strings.Join(changed, ", "))
	}
	if !res.PrevStateExists {
		note += "\n(your current state no longer exists in the new app — restart to enter the new graph)"
	}
	m.transcript.AppendSlashOutput(note)

	if res.PrevStateExists {
		if view, vErr := m.orch.Machine().RenderState(m.currentState, w); vErr == nil && view != "" {
			m.transcript.AppendSystem(view)
		}
	}
	m.transcript.ScrollToLine(mark)
	return m, nil
}

// updateMeta owns the keyboard while a /meta overlay is active.
//
//   - Enter submits the current prompt as a chat turn through
//     Controller.Send, surfaced via metaSendCmd.
//   - "/onpath" (or the mode's configured exit intent) calls
//     Controller.Exit and pops back to ModeOnPath.
//   - Esc and Ctrl+C exit the overlay (same affordance as off-path
//     plus edit mode).
//   - Other slash commands are not processed while in meta mode —
//     they would conflict with the meta agent. The user is hinted to
//     /onpath first.
//   - While inFlight (a turn is mid-flight), Enter is ignored.
func (m RootModel) updateMeta(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case metaSendDoneMsg:
		return m.handleMetaSendDone(msg)
	case metaEnterDoneMsg:
		return m.handleMetaEnterDone(msg)
	case metaListDoneMsg:
		return m.handleMetaListDone(msg)
	case metaNewDoneMsg:
		return m.handleMetaNewDone(msg)
	case metaDoneDoneMsg:
		return m.handleMetaDoneDone(msg)
	case metaAttachDoneMsg:
		return m.handleMetaAttachDone(msg)
	case MetaStreamMsg:
		return m.handleMetaStreamEvent(msg), nil
	case spinner.TickMsg:
		if m.metaMode.inFlight {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil
	}

	keyMsg, isKey := msg.(tea.KeyMsg)
	if !isKey {
		return m, nil
	}

	// Ctrl+C / Esc exit the overlay (same affordance as edit mode).
	if keyMsg.Type == tea.KeyCtrlC || keyMsg.Type == tea.KeyEsc {
		return m.exitMetaMode(), nil
	}

	// Scroll keys pass through to the transcript even mid-turn.
	if isScrollKey(keyMsg) {
		var cmd tea.Cmd
		m.transcript, cmd = m.transcript.Update(keyMsg)
		return m, cmd
	}

	if keyMsg.Type != tea.KeyEnter {
		// Feed the prompt textarea. Don't pass keys while a turn is
		// in flight so the user can't queue typing into a stale input.
		if m.metaMode.inFlight {
			return m, nil
		}
		var cmd tea.Cmd
		m.prompt, cmd = m.prompt.Update(keyMsg)
		return m, cmd
	}

	// Enter pressed.
	if m.metaMode.inFlight {
		m.transcript.AppendSystem("hold on — still thinking about the previous turn")
		return m, nil
	}

	// Bash-style line continuation: trailing unescaped "\" + Enter
	// inserts a literal newline instead of submitting. Mirrors the
	// on-path / off-path behaviour in routeKey.
	if submit, after := shouldSubmitOnEnter(m.prompt.Value()); !submit {
		m.prompt.SetValue(after)
		m.prompt.CursorEnd()
		m.prompt.InsertString("\n")
		return m, nil
	}

	text := strings.TrimSpace(m.prompt.Value())
	if text == "" {
		return m, nil
	}
	m.prompt.SetValue("")

	// Exit intent comes from the mode declaration; default is /onpath.
	exitCmd := "/onpath"
	if m.metaMode.session != nil && m.metaMode.session.Mode != nil {
		exitCmd = "/" + m.metaMode.session.Mode.ExitIntentOrDefault()
	}
	if text == exitCmd {
		return m.exitMetaMode(), nil
	}

	// The discovery subcommands /meta list, /meta new, /meta resume
	// <id>, and /meta done are allowed inside meta mode — they
	// pivot between meta chats (or close the current one) without
	// forcing /onpath first. /attach hands the terminal to a
	// tmux-hosted `claude --resume` session against the active
	// meta chat; /sessions list and
	// /sessions attach <N> work too so the user can hop to any
	// background claude conversation without leaving /meta.
	// Anything else with a "/" prefix is still discouraged.
	if strings.HasPrefix(text, "/") {
		parts := strings.Fields(text)
		if len(parts) > 0 && parts[0] == "/meta" && len(parts) > 1 {
			switch parts[1] {
			case "list", "new", "resume", "done":
				return m.handleMetaSlash(parts[1:])
			}
		}
		if len(parts) > 0 && parts[0] == "/sessions" {
			return m.handleSessionsSlash(parts[1:])
		}
		if text == "/attach" {
			return m.handleMetaAttach()
		}
		m.transcript.AppendSystem(fmt.Sprintf("(slash commands are paused in meta mode — use %s to return first; or /attach / /sessions to jump into claude)", exitCmd))
		return m, nil
	}

	// Submit the user turn.
	m.transcript.AppendTurn(text, "")
	m.metaMode.inFlight = true
	turn := m.buildMetaTurnContext()
	// m.metaStreamSink is *MetaStreamSink (or nil); host.WithStreamSink
	// inside metaSendCmd is nil-safe so either path works.
	return m, tea.Batch(m.spinner.Tick,
		metaSendCmd(context.Background(), m.metaController, m.metaMode.session, text, turn, m.metaStreamSink))
}

// buildMetaTurnContext snapshots the ambient state (state path, app
// file, rendered view, world variables, trace path) the controller
// injects into the agent's user message. Called once per Enter-key
// press while the meta overlay is active. RenderState errors are
// swallowed — a missing view just leaves the field empty rather than
// aborting the turn.
//
// When both traceRing and traceFilePath are wired (production path),
// the ring is snapshotted and written truncate-then-write to the temp
// file with 0o600 perms so the agent can Read it for full session
// history. Write failures are logged but non-fatal — TracePath is set
// only when the write succeeds so the agent never sees a stale or
// missing file path in the preamble.
func (m RootModel) buildMetaTurnContext() metamode.TurnContext {
	tracePath := ""
	switch {
	case m.traceFilePath == "":
		// No trace plumbing wired — common in tests; silent skip.
	case m.traceFileExternal:
		// slog is streaming to this path already; just hand the agent
		// the pointer. Skip the ring dump entirely (it would be stale
		// relative to whatever slog has written since the last snapshot
		// and would clobber the live file).
		tracePath = m.traceFilePath
	case m.traceRing != nil:
		// TUI-owned temp file fed by the always-on ring buffer.
		snap := m.traceRing.Snapshot()
		if err := os.WriteFile(m.traceFilePath, snap, 0o600); err != nil {
			slog.Warn("tui.meta: failed to write trace dump for agent",
				"path", m.traceFilePath, "err", err)
		} else {
			tracePath = m.traceFilePath
		}
	}

	// World, the rendered view, and the imported-manifest watch set are
	// derived from the live orchestrator by the shared builder so this
	// surface and the `kitsoki web` meta driver produce identical context.
	return metamode.BuildTurnContext(m.orch, m.sid, m.currentState, m.appPath, tracePath)
}

// exitMetaMode tears down the overlay and pops back to ModeOnPath.
// Calls Controller.Exit so future workstreams that want to land
// per-mode cleanup (discard-on-exit for non-persistent modes, for
// example) have a single seam to extend.
func (m RootModel) exitMetaMode() RootModel {
	ctrl := m.metaController
	sess := m.metaMode.session
	if ctrl != nil && sess != nil {
		// Exit is currently a no-op (drafts survive).
		// Capture the error for diagnostics but don't block the UX.
		if err := ctrl.Exit(context.Background(), sess); err != nil {
			slog.Warn("tui.meta: controller.Exit error", "err", err)
		}
	}

	// Build the post-exit summary up front so we can append it to the
	// on-path transcript after the room swap. Doing the heavy lifting
	// here keeps the same content the pre-rooms code produced; the
	// only behavioural change is WHERE it lands (on-path buffer, not
	// the meta-room buffer).
	turns, edits := m.metaMode.turns, m.metaMode.edits
	files := dedupSorted(m.metaMode.changedFiles)
	var summary string
	switch {
	case edits > 0 && turns == 1:
		summary = fmt.Sprintf("(✓ meta session: 1 turn, %d edit applied + reloaded)", edits)
	case edits > 0:
		summary = fmt.Sprintf("(✓ meta session: %d turns, %d edit(s) applied + reloaded)", turns, edits)
	case turns == 0:
		summary = "(meta session: no turns)"
	case turns == 1:
		summary = "(meta session: 1 turn, no file changes)"
	default:
		summary = fmt.Sprintf("(meta session: %d turns, no file changes)", turns)
	}
	if len(files) > 0 {
		summary += "\n  files touched: " + strings.Join(files, ", ")
	}

	// Swap back to the on-path room — restores the transcript the
	// user was looking at before /meta took over. The meta-mode chat
	// buffer is saved under metaRoomKey by activateRoom; the user
	// can still scroll the chat back if they re-enter via /meta
	// resume (a fresh enter clears it; see handleMetaEnterDone).
	onPathRoom := roomKey(m.currentState)
	// Persistent on-path returns scroll preserves position; transient
	// (e.g. an on-path room declared transcript: transient) lands at
	// the top of the visible window so the new view stands out.
	transient := transcriptKindForRoom(roomDecl(m.orch.AppDef(), onPathRoom)) == "transient"
	m.activateRoom(onPathRoom, transient)

	// Mark where the post-exit section starts in the on-path buffer
	// so the summary lands at the top of the viewport (still
	// reachable via scroll-up).
	mark := m.transcript.ContentHeight()
	m.transcript.AppendSlashOutput(summary)

	// Surface the configured return message after the summary so the
	// user-authored greeting (if any) sits closest to the new view.
	if sess != nil && sess.Mode != nil && sess.Mode.Return != nil && sess.Mode.Return.Message != "" {
		m.transcript.AppendSystem(sess.Mode.Return.Message)
	} else {
		m.transcript.AppendSystem("(returned to on-path mode)")
	}
	// Re-render the current FSM view so the user sees what they're
	// looking at on-path, fresh.
	w := m.orch.CurrentWorld(m.sid)
	if view, vErr := m.orch.Machine().RenderState(m.currentState, w); vErr == nil && view != "" {
		m.transcript.AppendSystem(view)
	}
	m.transcript.ScrollToLine(mark)

	m.metaMode.Exit()
	m.mode = ModeOnPath
	// Restore the on-path prefix glyph + a placeholder that advertises
	// the room's default action (so a bare Enter has a discoverable
	// meaning).
	setPromptPrefix(&m.prompt, promptPrefixOnPath)
	setPromptStyle(&m.prompt, promptStyle)
	m.refreshPromptPlaceholder()
	return m
}

// updateMenuSystem routes keys to the overlay and reverts to ModeOnPath when
// the user dismisses it without choosing.
func (m RootModel) updateMenuSystem(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.menuSystem, cmd = m.menuSystem.Update(msg)
	if !m.menuSystem.IsActive() && m.mode == ModeMenu {
		// Dismissed without picking (Esc/q) — return to on-path.
		m.mode = ModeOnPath
	}
	return m, cmd
}

// handleMenuSystemChoice dispatches the action selected in the overlay.
// The overlay closed itself before emitting the message; this function is
// responsible for reverting mode and performing the effect.
func (m RootModel) handleMenuSystemChoice(msg menuSystemChoiceMsg) (tea.Model, tea.Cmd) {
	m.mode = ModeOnPath
	switch msg.action {
	case menuActionExit:
		m.quitting = true
		return m, tea.Quit
	case menuActionMetaMode:
		// Same code path as `/meta <name>`. The Esc menu enumerates
		// every declared mode, so msg.modeName is always set; the
		// bare-form fallback (first declared mode) is reserved for the
		// `/meta` slash command.
		return m.startMetaMode(msg.modeName)
	case menuActionMetaSessions:
		return m.openSessionsPanel()
	case menuActionHelp:
		// Same block `/help` produces, surfaced from the Esc menu.
		body, next, cmd := HelpCommand{}.Run(m, nil)
		next.transcript.AppendBlock(body)
		return next, cmd
	case menuActionWorld:
		return m.openWorldView()
	}
	return m, nil
}

// updateDisambiguating handles input while the disambiguation model is
// presenting a candidate list. The prompt area is the normal textarea;
// Enter intercepts the typed pick (number or canonical intent name) and
// routes it through disambiguation.SubmitValue. Esc cancels.
func (m RootModel) updateDisambiguating(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case disambiguationChoiceMsg:
		return m.handleDisambiguationChoice(msg)

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m = m.resize()
		return m, nil

	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyEsc:
			m.mode = ModeOnPath
			m.disambiguation.Close()
			m.transcript.AppendSystem("(disambiguation cancelled)")
			return m, nil

		case tea.KeyCtrlC:
			if strings.TrimSpace(m.prompt.Value()) != "" {
				m.prompt.SetValue("")
				return m, nil
			}
			m.quitting = true
			return m, tea.Quit

		case tea.KeyEnter:
			// Alt+Enter falls through to the textarea for newline-in-input.
			if msg.Alt {
				break
			}
			input := strings.TrimSpace(m.prompt.Value())
			if input == "" {
				return m, nil
			}
			m.prompt.SetValue("")
			m.appendHistory(input)
			m.transcript.AppendUserInputEcho(input)

			chosen, err := m.disambiguation.SubmitValue(input)
			if err != nil {
				m.transcript.AppendSystem("(" + err.Error() + ")")
				// Re-render the candidate list so the user can retry
				// without scrolling.
				if block := m.disambiguation.RenderInlineBlock(blocks.New(m.transcript.width, m.currentTheme())); block != "" {
					m.transcript.AppendBlock(block)
				}
				return m, nil
			}
			// Synthesise a disambiguationChoiceMsg so the existing
			// handleDisambiguationChoice site stays the single source of
			// truth for what happens next (telemetry emit + dispatch).
			return m.handleDisambiguationChoice(disambiguationChoiceMsg{chosen: chosen})
		}
	}

	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		var cmd tea.Cmd
		m.prompt, cmd = m.prompt.Update(keyMsg)
		return m, cmd
	}
	return m, nil
}

// updateChoosing handles input while an inline choice widget owns the
// keyboard. Routes tea.KeyMsg through choiceWidgetModel.Update; resize
// / turn-outcome / observer messages fall through to the default Update
// path so the rest of the model keeps reacting (mirror updateSlotFilling).
//
// The widget surfaces decisions via *ChoiceCommit:
//
//   - Cancel == true && ToChat == true: Tab was pressed — explicit
//     off-ramp. Close the widget and focus the prompt textarea so the
//     user can type freely; the prior draft remains in m.pendingDraft.
//   - Cancel == true: Esc was pressed. Close the widget and return to
//     ModeOnPath, restoring the pre-widget draft.
//   - Cancel == false: the user finalised. Dispatch through
//     asyncSubmitDirect, the same call dispatchMenuEntry uses for the
//     right-pane menu (dispatch parity).
func (m RootModel) updateChoosing(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m = m.resize()
		// Re-render the widget at the new width so the live region
		// reflows in place.
		m.transcript.UpdateLive(m.choice.View(m.transcript.wrapWidth()))
		return m, nil

	case turnOutcomeMsg:
		// A turn outcome arriving while ModeChoosing means an
		// off-widget submission completed (e.g. the user defocused +
		// pressed Enter on the prompt). Close the widget and fall
		// through to the normal handler.
		m.choice.Close()
		m.transcript.FinalizeLive("")
		m.mode = ModeOnPath
		return m.handleTurnOutcome(msg)

	case continueTurnOutcomeMsg:
		return m.handleContinueTurnOutcome(msg)

	case tea.KeyMsg:
		// Ctrl+C is a hard exit hatch even inside the widget.
		if msg.Type == tea.KeyCtrlC {
			if strings.TrimSpace(m.prompt.Value()) != "" {
				m.prompt.SetValue("")
				return m, nil
			}
			m.quitting = true
			return m, tea.Quit
		}

		var commit *ChoiceCommit
		var cmd tea.Cmd
		m.choice, cmd, commit = m.choice.Update(msg)

		// Always refresh the live region after a key — even a no-op
		// arrow may have moved the cursor.
		m.transcript.UpdateLive(m.choice.View(m.transcript.wrapWidth()))

		if commit == nil {
			return m, cmd
		}

		// Surface the choice as a permanent entry, then dispatch.
		body := m.choice.View(m.transcript.wrapWidth())

		if commit.Cancel {
			m.choice.Close()
			m.mode = ModeOnPath
			if commit.ToChat {
				// Explicit off-ramp (Tab pressed) — focus the prompt
				// textarea for free-text chat. Leave the textarea
				// empty so the user can start typing fresh; the prior
				// draft remains in m.pendingDraft, reachable via
				// /input.
				slog.Debug("tui.choice.to_chat")
				m.transcript.FinalizeLive("")
				m.transcript.AppendSystem("(picker dismissed — type to chat. /input restores your prior draft.)")
			} else {
				m.transcript.FinalizeLive(body)
				m.transcript.AppendSystem("(picker cancelled)")
				// Cancel via Esc — restore the pre-widget draft so
				// the user can continue editing where they left off.
				if m.pendingDraft != "" {
					m.prompt.SetValue(m.pendingDraft)
					m.pendingDraft = ""
				}
			}
			// cmd is currently always nil from the widget; revisit if
			// choiceWidgetModel.Update ever returns an async cmd.
			return m, cmd
		}

		// Commit — finalise the widget and dispatch.
		m.choice.Close()
		m.transcript.FinalizeLive(body)
		display := commit.Intent
		m.lastInput = display
		next, asyncCmd := startAsyncTurn(m, display,
			asyncSubmitDirect(m.orch, m.sid, commit.Intent, commit.Slots),
			pendingDeterministic,
		)
		if cmd == nil {
			return next, asyncCmd
		}
		return next, tea.Batch(cmd, asyncCmd)
	}

	// Everything else (spinner ticks, inbox poll, routing observer
	// messages) falls through to the default branch so the model
	// keeps reacting underneath the widget overlay.
	return m, nil
}

// handleOperatorQuestion opens the inline question widget when a dispatched
// agent agent forwards an AskUserQuestion into kitsoki (operatorQuestionMsg,
// dispatched by TUIOperatorPrompter.Ask). The turn is still in flight — the
// agent is blocked on msg.answerCh — so we overlay the question on the live
// region and switch to ModeOperatorQuestion without disturbing the awaiting
// state we restore on commit.
//
// A malformed (empty) batch can't be answered: we hand the agent a nil answer
// straight back so it proceeds on its own, rather than trapping the operator.
func (m RootModel) handleOperatorQuestion(msg operatorQuestionMsg) (tea.Model, tea.Cmd) {
	if err := m.operatorQuestion.Open(msg.questions, msg.answerCh); err != nil {
		slog.Warn("tui.operator_question.open_failed", slog.String("err", err.Error()))
		if msg.answerCh != nil {
			msg.answerCh <- nil
		}
		return m, nil
	}
	// Settle any in-flight live line (e.g. a resolved routing line) before the
	// question takes the slot, then paint the question.
	if m.transcript.hasLive() {
		m.transcript.FinalizeLive("")
	}
	m.transcript.AppendLive(m.operatorQuestion.View(m.transcript.wrapWidth()))
	m.mode = ModeOperatorQuestion
	return m, nil
}

// updateOperatorQuestion handles input while a forwarded agent question owns the
// keyboard. Routes tea.KeyMsg through operatorQuestionModel.Update; resize /
// turn-outcome messages fall through so the rest of the model keeps reacting
// (mirror updateChoosing).
//
// On commit the answer is sent back over the channel the agent is parked on and
// the model RESUMES ModeAwaitingLLM (the same turn continues) — unlike the
// choice widget, which starts a fresh turn. A nil answer (Esc) tells the host to
// let the agent decide on its own.
func (m RootModel) updateOperatorQuestion(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m = m.resize()
		m.transcript.UpdateLive(m.operatorQuestion.View(m.transcript.wrapWidth()))
		return m, nil

	case turnOutcomeMsg:
		// The turn completed/cancelled while the question was on screen — the
		// only way this happens is a ctx cancel (Ctrl+C) unblocking the parked
		// Ask. Tear the widget down (the agent already returned ctx.Err(); no
		// answer is owed) and fall through to the normal handler.
		m.operatorQuestion.Close()
		m.transcript.FinalizeLive("")
		m.mode = ModeOnPath
		m.inFlightCancel = nil
		return m.handleTurnOutcome(msg)

	case continueTurnOutcomeMsg:
		return m.handleContinueTurnOutcome(msg)

	case tea.KeyMsg:
		// Ctrl+C cancels the whole turn — the parked agent's ctx is cancelled,
		// Ask returns, and a turnOutcomeMsg (cancelled) will follow.
		if msg.Type == tea.KeyCtrlC {
			if m.inFlightCancel != nil {
				m.inFlightCancel()
			}
			return m, nil
		}

		var result *operatorQuestionResult
		var cmd tea.Cmd
		m.operatorQuestion, cmd, result = m.operatorQuestion.Update(msg)
		m.transcript.UpdateLive(m.operatorQuestion.View(m.transcript.wrapWidth()))
		if result == nil {
			return m, cmd
		}

		// Hand the answer (or nil on cancel) back to the parked agent and
		// resume awaiting the in-flight turn's completion.
		answerCh := m.operatorQuestion.answerCh
		body := m.operatorQuestion.View(m.transcript.wrapWidth())
		m.operatorQuestion.Close()
		m.transcript.FinalizeLive(body)
		m.mode = ModeAwaitingLLM
		if result.Cancel {
			m.transcript.AppendSystem("(left the answer to the agent)")
			if answerCh != nil {
				answerCh <- nil
			}
		} else {
			m.transcript.AppendSystem("(answer sent to the agent)")
			if answerCh != nil {
				answerCh <- result.Answers
			}
		}
		// Resume the spinner so the awaiting-LLM caption animates again.
		if cmd == nil {
			return m, m.spinner.Tick
		}
		return m, tea.Batch(cmd, m.spinner.Tick)
	}

	// Everything else falls through so the model keeps reacting underneath.
	return m, nil
}

// findChoiceElement returns the first Kind=="choice" element in a
// typed View, if any. Choice elements are loader-restricted to one per
// view (see internal/app/view_element.go), so callers can rely on this
// returning the *only* choice if it returns true. Returns the zero
// ViewElement + false when v is nil or contains no choice element.
func findChoiceElement(v *app.View) (app.ViewElement, bool) {
	if v == nil {
		return app.ViewElement{}, false
	}
	for _, el := range v.Elements {
		if el.Kind == "choice" {
			return el, true
		}
	}
	return app.ViewElement{}, false
}

// viewWithoutChoice returns a shallow copy of v with any choice
// elements stripped from Elements. Used at the auto-focus sites
// (NewRootModel initial paint + handleTurnOutcome) so the transcript
// body doesn't re-render the static choice picker on top of the live
// interactive widget. Returns nil for nil input.
//
// INVARIANT: Callers MUST also call m.choice.Open(...) so the user has
// a way to interact with the stripped choice element. Calling
// viewWithoutChoice without Open will silently hide an action the
// user needs to take — the room appears to have no choice surface at
// all. The auto-focus path in handleTurnOutcome enforces this pairing;
// any future caller must do the same.
func viewWithoutChoice(v *app.View) *app.View {
	if v == nil {
		return nil
	}
	filtered := make([]app.ViewElement, 0, len(v.Elements))
	for _, el := range v.Elements {
		if el.Kind == "choice" {
			continue
		}
		filtered = append(filtered, el)
	}
	out := *v
	out.Elements = filtered
	return &out
}

func (m RootModel) handleDisambiguationChoice(msg disambiguationChoiceMsg) (tea.Model, tea.Cmd) {
	m.disambiguation.Close()
	chosen := msg.chosen
	m.transcript.AppendSystem(fmt.Sprintf("(chose: %s)", chosen.Intent))
	// Site 31: emit disambig.chosen when the user picks an option.
	m.emitDisambigChosen(chosen)

	// Dispatch the chosen intent through the deterministic-direct
	// path. Candidates carry no Slots, so we pass an empty map —
	// missing required slots trigger the clarify flow on the
	// orchestrator side just as if the user had typed the intent
	// name into the prompt and hit a menu row with empty
	// PrefilledSlots. SubmitDirectFromInput threads the candidate
	// title onto the TurnStarted audit record so the trace shows
	// what the user picked.
	label := chosen.Title
	if label == "" {
		label = chosen.Intent
	}
	return startAsyncTurn(m, label,
		asyncSubmitDirectFromInput(m.orch, m.sid, chosen.Intent, map[string]any{}, label, orchestrator.RouteProvenance{Source: "disambiguation"}),
		pendingDeterministic,
	)
}

func (m RootModel) handleSupplementSlots(msg supplementSlotsMsg) (tea.Model, tea.Cmd) {
	// Stay in an in-flight mode while ContinueTurn runs. Setting
	// ModeOnPath here was a race: any subsequent KeyEnter (e.g. the
	// user impatiently double-tapping) would dispatch the menu's
	// default intent via SubmitDirect, whose successful Turn clears
	// the orchestrator's pending clarification — and the in-flight
	// ContinueTurn arriving moments later then errored with
	// "no pending clarification for session ...".
	//
	// ModeAwaitingLLM routes follow-up Enter keys to submitInput's
	// queue (which drains when the in-flight turn finishes) instead
	// of starting a competing Turn. handleContinueTurnOutcome resets
	// the mode based on the outcome's own Mode field.
	m.mode = ModeAwaitingLLM
	orch := m.orch
	sid := m.sid
	return m, func() tea.Msg {
		ctx := context.Background()
		out, err := orch.ContinueTurn(ctx, sid, msg.slots)
		return continueTurnOutcomeMsg{outcome: out, err: err}
	}
}

func (m RootModel) updateMenuFromAllowed(allowedNames []string, w interface{}) RootModel {
	// Recompute the full menu (with enum expansion and guard dry-runs).
	world := m.orch.CurrentWorld(m.sid)
	computedMenu := orchestrator.ComputeMenu(m.orch.AppDef(), m.orch.Machine(), m.currentState, world)
	m.menu, _ = m.menu.Update(menuItemsChanged{items: computedMenu.Primary, blocked: computedMenu.Blocked})
	m.refreshPromptPlaceholder()
	return m
}

// refreshPromptPlaceholder syncs m.prompt.Placeholder to advertise
// the menu's first primary action — what Enter on an empty prompt
// will dispatch. Without this hint the placeholder just says "what
// now?" and the user has no way to know that a bare Enter triggers
// the room's default action (typically `continue`).
//
// The placeholder reads "↵ <intent> · describe what you want, or /help"
// when there's a primary entry to advertise; falls back to "describe
// what you want, or /help" otherwise (e.g. a terminal state where no
// action is in the menu) — a new user gets both the NL-typing signal
// and the /help discoverability cue.
//
// No-op when the prompt is in a mode that owns its own placeholder
// — meta-mode ("meta chat — /onpath to return") and the game-over
// state ("(game over)") shouldn't be silently overwritten by a
// stale menu refresh.
func (m *RootModel) refreshPromptPlaceholder() {
	if m.mode == ModeMeta {
		return
	}
	if m.prompt.Placeholder == "(game over)" {
		return
	}
	def := m.menu.SelectedEntry()
	if def == nil {
		m.prompt.Placeholder = "describe what you want, or /help"
		return
	}
	label := def.Intent
	if def.Display != "" {
		label = def.Display
	}
	m.prompt.Placeholder = "↵ " + label + " · describe what you want, or /help"
}

func (m RootModel) updateLocation(out *orchestrator.TurnOutcome) RootModel {
	w := m.orch.CurrentWorld(m.sid)
	loc := orchestrator.ComputeLocation(m.orch.AppDef(), out.NewState, w, out.TurnNumber)
	m.location, _ = m.location.Update(locationUpdated{loc: loc})
	return m
}

func (m RootModel) resize() RootModel {
	const locationHeight = 2

	// Prompt height is variable now that the input is a multi-line
	// textarea: 1 row when the value fits on one line, growing by 1
	// per wrapped/literal newline up to promptMaxHeight. Anchor the
	// transcript layout to (prompt height + 2) so the footer + banner
	// have room beneath the prompt.
	promptInnerWidth := m.width - promptPrefixCols - promptSafetyMargin
	if promptInnerWidth < promptMinWidth {
		promptInnerWidth = promptMinWidth
	}
	promptHeight := promptVisualHeight(m.prompt.Value(), promptInnerWidth) + 2

	// Single-pane redesign (phase 3): the menu + inbox right column
	// is gone — transcript fills the full terminal width. menu.go /
	// inbox.go are still imported because /intents and /inbox read
	// from them, but their View() output is no longer composed into
	// the screen.
	transcriptWidth := m.width
	if transcriptWidth < 40 {
		transcriptWidth = 40
	}
	totalHeight := m.height - locationHeight - promptHeight - 4
	if totalHeight < 5 {
		totalHeight = 5
	}

	// Inner content area = panel width minus border (1 each side) and
	// padding (1 each side) from transcriptStyle. That's 4 chars total.
	innerWidth := transcriptWidth - 4

	m.transcript.SetSize(transcriptWidth, totalHeight, innerWidth, totalHeight-2)

	// Resize every saved per-room transcript too so a room swap
	// doesn't surface a buffer rendered at a stale width (single-
	// pane-tui phase 6). The active one was just resized above;
	// the saved ones get the same dimensions so swap-in lands at
	// the live terminal size.
	for k, t := range m.transcripts {
		t.SetSize(transcriptWidth, totalHeight, innerWidth, totalHeight-2)
		m.transcripts[k] = t
	}

	// World-view sub-model tracks the full pane.
	m.worldView.SetSize(transcriptWidth, totalHeight)

	// menu / inbox sub-models are kept for the /intents and /inbox
	// commands but no longer painted. We still size them so any
	// future inline rendering pulls coherent widths.
	m.menu.width = transcriptWidth
	m.menu.height = totalHeight

	m.inbox.width = transcriptWidth
	m.inbox.height = totalHeight

	m.location, _ = m.location.Update(tea.WindowSizeMsg{Width: m.width, Height: 1})

	// Prompt sizing — the textarea wraps long values and grows
	// downward; main's prompt.go owns the SetPromptFunc / SetWidth
	// contract. We pass the outer width (terminal minus a small
	// safety margin) and let the textarea reserve the prefix columns
	// internally. Height is computed against the resulting inner
	// width so wrap counts match what's rendered.
	promptOuterWidth := m.width - promptSafetyMargin
	if promptOuterWidth < promptMinWidth+promptPrefixCols {
		promptOuterWidth = promptMinWidth + promptPrefixCols
	}
	m.prompt.SetWidth(promptOuterWidth)
	m.prompt.SetHeight(promptVisualHeight(m.prompt.Value(), m.prompt.Width()))

	return m
}

// View implements tea.Model.
func (m RootModel) View() string {
	if m.quitting {
		return "Goodbye!\n"
	}

	// /world dedicated view fully replaces the bottom region until
	// the user dismisses it (q / Esc). The chat keeps running
	// underneath in scrollback (single-pane-tui §"Phase 1.5").
	if m.mode == ModeWorldView {
		return m.worldView.View()
	}

	// Sync the textarea's rendered height to its current value before
	// rendering. resize() — which sets the initial height — only fires
	// on WindowSizeMsg, not on every keystroke.
	promptH := promptVisualHeight(m.prompt.Value(), m.prompt.Width())
	m.prompt.SetHeight(promptH)

	// Action-required banner above the prompt — one always-on inbox
	// signal that doesn't need a panel.
	var bannerLine string
	if banner := m.inbox.ActionRequiredBanner(); banner != "" {
		bannerLine = banner
	}

	// Prompt line.
	// Spinner placement: right of the prompt prefix on the input line.
	// This keeps the location bar uncluttered and puts the spinner
	// where the user's eye is already focused (the input area).
	//
	// Mode-specific prefix (single-pane-tui §"Mode visualization"):
	//   normal     >
	//   meta       »
	//   off-path   #
	//   slot-fill  ?
	//   awaiting   …
	prefix := m.promptPrefix()
	// Ensure the textarea has up-to-date prefix + height before View()
	// pulls it. m.prompt is a value receiver here; we mutate a copy and
	// view that — production state isn't disturbed because View() is
	// itself a value receiver too.
	m.prompt.SetPromptFunc(promptPrefixCols, m.promptLineFunc())
	m.prompt.SetHeight(promptHeightFor(&m.prompt))
	var promptLine string
	switch m.mode {
	case ModeChoosing:
		// While the choice widget owns focus the prompt textarea is
		// inert — keystrokes route to the widget, not the buffer.
		// Suppress the textarea entirely; the widget's own footer
		// (above the divider) advertises its keymap, so a second
		// hint here would either repeat it or — worse — mislead in
		// modes where typed letters get absorbed by the picker
		// (paramMode, form mode). When there's a draft worth
		// restoring, surface a single line about /input.
		if m.pendingDraft != "" {
			promptLine = lipgloss.NewStyle().
				Foreground(colorMuted).
				Italic(true).
				Render("(picker active — /input restores your prior draft)")
		} else {
			promptLine = lipgloss.NewStyle().
				Foreground(colorMuted).
				Italic(true).
				Render("(picker active)")
		}
	case ModeMenu:
		promptLine = m.menuSystem.View()
	case ModeMetaSessions:
		promptLine = m.sessionsPanel.View()
	case ModeAwaitingLLM:
		// Keep the textarea visible during in-flight so the user
		// can type the next message — Enter enqueues. A muted
		// "⏳ thinking · queue: N" row sits above the textarea so
		// the queue affordance is obvious. The hourglass icon also
		// replaces the textarea's normal "> " prefix on row 0 so
		// the visual cue is unmistakable.
		// Backend-neutral: the pendingLLM path runs the whole router, which may
		// resolve via the local-model routing tier (agent.local) OR fall through
		// to the main-turn claude. Naming a specific backend here mislabels a
		// local-model route as "claude", so the caption stays neutral.
		caption := "thinking… (Ctrl+C to cancel)"
		if m.pendingKind == pendingDeterministic {
			caption = "running…  (Ctrl+C to cancel)"
		}
		indicator := lipgloss.NewStyle().
			Foreground(colorMuted).
			Render("⏳ " + m.spinner.View() + " " + caption +
				"  ·  queue: " + queueDepthLabel(len(m.inputQueue)) +
				"  ·  Enter to queue · Esc to cancel queue")
		// Swap the textarea's prefix to a queue icon so the row 0
		// glyph signals "this Enter queues" without changing the
		// prompt's value semantics.
		m.prompt.SetPromptFunc(promptPrefixCols, func(int) string { return "↳ " })
		promptLine = indicator + "\n" + m.prompt.View()
	case ModeMeta:
		if m.metaMode.inFlight {
			promptLine = prefix + m.spinner.View() + " " +
				lipgloss.NewStyle().Foreground(colorMuted).Render("agent is thinking… (Esc to cancel)")
		} else {
			// In meta mode the textarea's per-line prefix function is
			// pointed at "» " on entry; View() renders the marker
			// in-place at the top-left of the input block (so wrapped
			// rows align without repeating the marker).
			promptLine = m.prompt.View()
		}
	default:
		// Textarea owns the prefix via SetPromptFunc (see
		// newPromptTextarea + the mode-prefix helpers). View()
		// renders the marker at the input's top-left and indents
		// wrapped rows by the same 2-column gutter.
		promptLine = m.prompt.View()
	}

	// Bottom chrome — historic entries print to scrollback via
	// tea.Println, so the View() footprint is the live indicator +
	// banner + divider + prompt + per-room status row + framework
	// footer row. The terminal's native scroll walks the rest.
	//
	// Layout from top to bottom:
	//
	//   [in-flight line, if any]
	//   [action-required banner, if any]
	//   ─────────────────────────────────────────
	//   > [prompt textarea]
	//   [per-room status row, if the state declares Footer]
	//   [coloured framework status row: room · state · mode · queue]
	// Bottom-chrome assembly routes through the frame composer so the
	// live screen and every headless capture (kitsoki drive / shot) are
	// the same bytes. composeChromeParts builds the exact part list that
	// View() used to inline; joinChromeParts performs the same
	// trim-and-newline join. The live path passes m.width (chrome only —
	// the body lives in scrollback via tea.Println), so this output is
	// byte-identical to the pre-composer assembly.
	parts := composeChromeParts(m, m.width, promptLine, bannerLine)
	return joinChromeParts(parts)
}

// promptPrefix returns the styled mode-specific prompt prefix.
// Single-pane-tui §"Mode visualization" table.
func (m RootModel) promptPrefix() string {
	switch m.mode {
	case ModeMeta:
		return promptStyle.Render("» ")
	case ModeOffPath:
		return promptOffPathStyle.Render("# ")
	case ModeSlotFilling:
		return promptStyle.Render("? ")
	case ModeDisambiguating:
		// Phase 2 inline overlay: disambiguation shares the `?`
		// glyph with slot-filling — both prompts are asking the
		// user to clarify a previous turn rather than to act.
		return promptStyle.Render("? ")
	case ModeAwaitingLLM:
		return lipgloss.NewStyle().Foreground(colorMuted).Render("… ")
	default:
		return promptStyle.Render("> ")
	}
}

// promptHangPad is the blank prefix the textarea renders on every
// wrapped continuation line so the soft-wrapped text visually hangs
// under (not flush with) the first-row mode glyph. It must be exactly
// promptPrefixCols wide to keep the inner wrap column stable.
const promptHangPad = "  "

// promptLineFunc returns the per-row prompt callback the textarea uses
// for its SetPromptFunc hook. Row 0 carries the mode-specific prefix
// ("> ", "» ", …); subsequent rows are blank-padded so wrapped lines
// hang under the first-row prefix. The styling is recomputed every
// frame because m.mode can change between View() calls (off-path,
// meta, slot-fill).
func (m RootModel) promptLineFunc() func(lineIdx int) string {
	prefix := m.promptPrefix()
	return func(lineIdx int) string {
		if lineIdx == 0 {
			return prefix
		}
		return promptHangPad
	}
}

// promptHeightFor returns the height (in display rows) the prompt
// textarea should occupy given its current value. Empty / single-line
// values stay at 1 row; longer content grows up to promptMaxHeight,
// past which the textarea internally scrolls.
func promptHeightFor(ta *textarea.Model) int {
	// Count display rows by walking each logical line and dividing by
	// the input width — textarea.LineCount only reports logical
	// (newline-split) lines, not wrapped display rows. We need
	// display rows to size the on-screen viewport correctly.
	w := ta.Width()
	if w <= 0 {
		return 1
	}
	value := ta.Value()
	if value == "" {
		return 1
	}
	rows := 0
	for _, line := range strings.Split(value, "\n") {
		// One row minimum per logical line (even empty ones), then
		// add a row for every full wrap.
		runeLen := len([]rune(line))
		if runeLen == 0 {
			rows++
			continue
		}
		// Ceil-divide rune-length by width to get wrapped row count.
		rows += (runeLen + w - 1) / w
	}
	if rows < 1 {
		rows = 1
	}
	if rows > promptMaxHeight {
		rows = promptMaxHeight
	}
	return rows
}

// newPromptTextarea constructs the bottom-row textarea sub-model with
// kitsoki-specific defaults: line numbers off, the cursor-line and
// end-of-buffer styles flattened (no per-row highlight), the focus
// newPromptTextarea is owned by prompt.go on main — the proposal
// branch's duplicate (with a `placeholder` arg and slightly different
// keymap) was dropped during the rebase. Main's version handles every
// requirement: Enter→submit, Alt+Enter/Ctrl+J newline, line numbers
// off, cursor-line highlight off, per-line prefix via SetPromptFunc.

// footerFrameworkLine assembles the location-and-counters portion of
// the framework footer: room · state · queue · unread. Mode label
// lives on the right side of the status row, so it's NOT included
// here (was causing a duplicate "awaiting awaiting" pattern).
func footerFrameworkLine(m RootModel) string {
	var parts []string
	if loc := strings.TrimSpace(m.location.LocationLine()); loc != "" {
		parts = append(parts, loc)
	}
	if n := len(m.inputQueue); n > 0 {
		parts = append(parts, queueDepthLabel(n))
	}
	if badge := m.inboxBadge(); badge != "" {
		parts = append(parts, badge)
	}
	if badge := m.proposalsBadge(); badge != "" {
		parts = append(parts, badge)
	}
	if chip := ideFooterChip(m); chip != "" {
		parts = append(parts, chip)
	}
	return strings.Join(parts, " · ")
}

// discoverabilityHint is the persistent first-run cue re-advertised on
// every frame, so a user isn't stranded once the welcome banner (which
// lists /help, /world, …) scrolls off. Rendered on its own faint line in
// the bottom chrome rather than the status row, so it never crowds out
// the high-signal location/mode content on narrow terminals.
const discoverabilityHint = "? help · Esc menu"

// ideFooterChipTemplate is the pongo2 source for the IDE footer indicator.
// Rendered only when connected so the chip is hidden/off otherwise — no
// hand-rolled string concatenation builds the operator-visible text.
const ideFooterChipTemplate = `{% if args.ide.connected %}⧉ ide: {{ args.ide.name }} ✓{% endif %}`

// ideFooterChip renders the footer's IDE indicator through the footer pongo2
// template against the live link state. Returns "" (hidden) when no editor is
// connected. The decorative footer never bubbles a template error to the user —
// a render failure degrades to no chip, matching footerStoryLine.
func ideFooterChip(m RootModel) string {
	connected := m.ideConnected()
	name := ""
	if connected {
		name = displayIDEName(m.ideLink.IDEName())
	}
	env := expr.Env{
		Slots: map[string]any{},
		World: map[string]any{},
		Event: map[string]any{},
		Args: map[string]any{
			"ide": map[string]any{
				"connected": connected,
				"name":      name,
			},
		},
	}
	out, err := render.Pongo(ideFooterChipTemplate, env)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// footerStoryLine evaluates the active room's State.Footer pongo2
// template against the current world. Returns "" when the state
// declares no footer (the framework line is enough on its own) or
// when evaluation errors — the footer is decorative, not load-bearing,
// so we never bubble template errors up to the user.
func footerStoryLine(m RootModel) string {
	if m.orch == nil {
		return ""
	}
	def := m.orch.AppDef()
	if def == nil {
		return ""
	}
	state := lookupState(def, m.currentState)
	if state == nil || strings.TrimSpace(state.Footer) == "" {
		return ""
	}
	w := m.orch.CurrentWorld(m.sid)
	env := expr.Env{
		Slots: map[string]any{},
		World: w.Vars,
		Event: map[string]any{},
	}
	out, err := render.Pongo(state.Footer, env)
	if err != nil {
		return ""
	}
	// Single-line guarantee: footer renders as one row beneath
	// the prompt. Multi-line pongo output would push the prompt
	// up unpredictably and (worse) bleed into the colored status
	// row's line via Bubble Tea's inline renderer.
	line := strings.TrimSpace(out)
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	return line
}

// lookupState walks the AppDef's nested state map by the dot-separated
// path and returns the matching State, or nil when any segment is
// missing.
func lookupState(def *app.AppDef, path app.StatePath) *app.State {
	if def == nil || path == "" {
		return nil
	}
	segments := strings.Split(string(path), ".")
	states := def.States
	for i, seg := range segments {
		s, ok := states[seg]
		if !ok || s == nil {
			return nil
		}
		if i == len(segments)-1 {
			return s
		}
		states = s.States
	}
	return nil
}

// modeLabel returns the human-readable footer label for each Mode.
func modeLabel(mode Mode) string {
	switch mode {
	case ModeOffPath:
		return "off-path"
	case ModeMeta:
		return "meta"
	case ModeMetaSessions:
		return "sessions"
	case ModeSlotFilling:
		return "slot-fill"
	case ModeDisambiguating:
		return "disambig"
	case ModeAwaitingLLM:
		return "awaiting"
	case ModeMenu:
		return "menu"
	case ModeWorldView:
		return "world"
	default:
		return "normal"
	}
}

// renderRoutingTraceOverlay was the Ctrl+R full-screen routing-trace
// overlay's body builder. Phase 2 moved the data inline via /trace and
// stopped rendering the overlay; phase 7 removes the helper. The test
// seam in export_test.go is gone too — tests for /trace assert on
// transcript content via GetTranscriptContent.

// inboxBadge builds the status-line badge text from the latest notification
// snapshot.  Returns "" when there are no unread notifications.
func (m RootModel) inboxBadge() string {
	if len(m.lastNotifications) == 0 {
		return ""
	}
	unread := 0
	actionRequired := 0
	for _, n := range m.lastNotifications {
		if n.ReadAt == nil {
			unread++
			if n.Severity == jobs.SeverityActionRequired {
				actionRequired++
			}
		}
	}
	if unread == 0 {
		return ""
	}
	badge := fmt.Sprintf("inbox: %d unread", unread)
	if actionRequired > 0 {
		badge += fmt.Sprintf(" · %d action_required", actionRequired)
	}
	return badge
}

// isScrollKey reports whether a key event is a transcript-scroll key.
// Scroll keys are forwarded to the viewport rather than the prompt so the
// user can re-read earlier turns. Bindings are picked to work on MacBook
// keyboards (no PgUp/PgDn/Home/End) while still honouring those keys when
// present on external keyboards.
//
// Primary set (MacBook-friendly):
//   - Shift+↑ / Shift+↓ — one line up / down
//   - Ctrl+B / Ctrl+F   — full page up / down
//   - Ctrl+U / Ctrl+D   — half page up / down
//
// Also accepted: Opt+↑/↓ (alias for Shift+↑/↓) and PgUp/PgDn.
func isScrollKey(msg tea.KeyMsg) bool {
	switch msg.String() {
	case "pgup", "pgdown", "page up", "page down",
		"ctrl+b", "ctrl+f",
		"ctrl+u", "ctrl+d",
		"shift+up", "shift+down",
		"alt+up", "alt+down":
		return true
	}
	return false
}

// ─── journal helpers ───────────────────────────────────────────────────────────

// tuiMustJSON marshals v to JSON, returning an empty object on error.
func tuiMustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("{}")
	}
	return b
}

// tuiEmitInboxOpened emits an inbox.item.opened journal entry via jw (standalone).
// This package-level variant is used from closures that capture jw/sid by value.
func tuiEmitInboxOpened(jw journal.Writer, sid app.SessionID, notificationID, title string) {
	if jw == nil {
		return
	}
	_ = jw.Append(journal.Entry{
		Ts:      time.Now(),
		Session: sid,
		Kind:    journal.KindInboxItemOpened,
		Body: tuiMustJSON(map[string]any{
			"notification_id":    notificationID,
			"notification_title": title,
			"opened_at":          time.Now().Format(time.RFC3339Nano),
		}),
	})
}

// emitInboxOpened is the method receiver variant for use where m is available.
func (m RootModel) emitInboxOpened(notificationID, title string) {
	tuiEmitInboxOpened(m.journalWriter, m.sid, notificationID, title)
}

// emitInboxDismissed emits an inbox.item.dismissed journal entry (standalone write).
func (m RootModel) emitInboxDismissed(notificationID, title string) {
	if m.journalWriter == nil {
		return
	}
	_ = m.journalWriter.Append(journal.Entry{
		Ts:      time.Now(),
		Session: m.sid,
		Kind:    journal.KindInboxItemDismissed,
		Body: tuiMustJSON(map[string]any{
			"notification_id":    notificationID,
			"notification_title": title,
			"dismissed_at":       time.Now().Format(time.RFC3339Nano),
		}),
	})
}

// emitDisambigPresented emits a disambig.presented journal entry when the
// disambiguation overlay is shown to the user (site 31 — presented half).
func (m RootModel) emitDisambigPresented(candidates []intent.Candidate) {
	if m.journalWriter == nil {
		return
	}
	labels := make([]string, 0, len(candidates))
	for _, c := range candidates {
		labels = append(labels, c.Intent)
	}
	_ = m.journalWriter.Append(journal.Entry{
		Ts:      time.Now(),
		Session: m.sid,
		Kind:    journal.KindDisambigPresented,
		Body: tuiMustJSON(map[string]any{
			"candidates": labels,
		}),
	})
}

// emitDisambigChosen emits a disambig.chosen journal entry when the user
// picks a disambiguation candidate (site 31 — chosen half).
func (m RootModel) emitDisambigChosen(chosen intent.Candidate) {
	if m.journalWriter == nil {
		return
	}
	_ = m.journalWriter.Append(journal.Entry{
		Ts:      time.Now(),
		Session: m.sid,
		Kind:    journal.KindDisambigChosen,
		Body: tuiMustJSON(map[string]any{
			"intent":          chosen.Intent,
			"candidate_label": chosen.Title,
		}),
	})
}
