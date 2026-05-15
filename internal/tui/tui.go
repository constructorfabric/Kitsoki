// Package tui implements the full-screen Bubble Tea interface (§9a).
// It composes sub-models for the location header, transcript pane,
// menu list, inbox panel (§2.2), graph overlay, prompt input, and slot-fill modals.
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
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"kitsoki/internal/app"
	"kitsoki/internal/chats"
	"kitsoki/internal/clock"
	"kitsoki/internal/host"
	"kitsoki/internal/inbox"
	"kitsoki/internal/intent"
	"kitsoki/internal/jobs"
	"kitsoki/internal/journal"
	"kitsoki/internal/metamode"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/trace"
	"kitsoki/internal/viz"
	"kitsoki/internal/world"
)

// Mode describes which interaction mode the TUI is currently in (§7.3, §7.7).
type Mode int

const (
	// ModeOnPath is the default on-path mode.
	ModeOnPath Mode = iota
	// ModeOffPath is the visually-framed free-form mode (§7.7).
	ModeOffPath
	// ModeSlotFilling is active while the user fills missing slots (§7.3).
	ModeSlotFilling
	// ModeDisambiguating is active while the disambiguation menu is shown (§7.4).
	ModeDisambiguating
	// ModeAwaitingLLM is active while the LLM harness is processing a turn.
	// Input is disabled and a spinner is shown.
	ModeAwaitingLLM
	// ModeMenu is active while the Esc-triggered system menu is on screen.
	ModeMenu
	// ModeMeta is active while a /meta overlay owns the prompt and
	// transcript (named, persistent sidebar conversation against a
	// declared meta mode). See docs/proposals/meta-mode-proposal.md §3.
	// Replaces the former ModeEdit / edit-mode overlay (Phase B).
	ModeMeta
	// ModeMetaSessions is active while the "Meta sessions" foyer
	// overlay (proposal §2.1) is on screen. Arrow keys navigate the
	// listed chats; Enter resumes one; Esc closes the overlay and
	// returns to ModeOnPath.
	ModeMetaSessions
)

// ctrlCQuitWindow is how long after a Ctrl+C the next Ctrl+C will quit
// rather than just re-clearing the prompt.
const ctrlCQuitWindow = 2 * time.Second

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

// RootModel is the top-level Bubble Tea model composing all sub-models (§9a.1).
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

	location       locationModel
	transcript     transcriptModel
	menu           menuModel
	inbox          inboxModel
	offPath        offPathModel
	clarify        clarifyModel
	disambiguation disambiguationModel
	menuSystem     menuSystemModel
	metaMode       metaModel
	sessionsPanel  sessionsPanelModel
	prompt         textinput.Model

	// chatStore is the persistent chat row backend; used by the
	// metamode controller to resolve / append. nil disables /meta
	// (no controller is constructed in NewRootModel).
	chatStore *chats.Store

	// metaController is the wire harness for /meta. nil disables /meta.
	// Production wires this in NewRootModel from chatStore +
	// host.AgentRegistry() + AppDef. Tests can inject a fake controller
	// via WithMetaController.
	metaController *metamode.Controller

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
	lastNotifications []jobs.Notification

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

	// mouseOn reflects whether the TUI is currently capturing mouse events.
	// Controlled at runtime via the /mouse slash command. Defaults to true
	// because the program is launched with tea.WithMouseCellMotion.
	mouseOn bool

	// journalWriter, when non-nil, receives typed journal entries for
	// inbox read/dismiss (sites 27) and disambiguation (site 31)
	// lifecycle events (continue-mode §4.9 dual-write).
	// Nil disables journal writes (back-compat default for tests).
	journalWriter journal.Writer

	// traceRing is the always-on in-memory ring buffer built by
	// BuildTraceLogger.  buildMetaTurnContext snapshots it to
	// traceFilePath on every meta-mode Send so the agent can Read
	// the file for session-history context. Nil disables the dump
	// (tests / headless callers).
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
}

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

// WithMetaController injects a pre-built *metamode.Controller into the
// RootModel. Tests use this to bypass the production wiring (which
// requires a real agent registry, chat store, and oracle adapter)
// and inject a controller pointed at fakes.
//
// When non-nil, this controller is used regardless of what
// NewRootModel would otherwise build from chatStore + AppDef +
// host.AgentRegistry().
func WithMetaController(c *metamode.Controller) RootModelOption {
	return func(m *RootModel) { m.metaController = c }
}

// WithTraceRingBuffer wires the always-on in-memory trace ring into
// the RootModel.  buildMetaTurnContext snapshots it to disk on every
// meta-mode Send so the agent can Read the recent session history.
// Production passes the ring built by BuildTraceLogger; tests pass nil
// (the dump is silently skipped) unless they explicitly want to assert
// on the on-disk file.
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
// emit typed journal entries for continue-mode durability (§4.9 dual-write).
// When nil (the default), no journal entries are written — this preserves
// backward compatibility for tests and headless callers.
func WithJournalWriter(jw journal.Writer) RootModelOption {
	return func(m *RootModel) { m.journalWriter = jw }
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
// NOTE: A full Bubble Tea overlay picker (§5.4) is a follow-up; this option
// is phase-A plumbing only.
func WithResumedJourney(state app.StatePath, w world.World, turn app.TurnNumber) RootModelOption {
	return func(m *RootModel) {
		m.currentState = state
		computedMenu := orchestrator.ComputeMenu(m.orch.AppDef(), m.orch.Machine(), state, w)
		m.menu, _ = m.menu.Update(menuItemsChanged{items: computedMenu.Primary, blocked: computedMenu.Blocked})
		loc := orchestrator.ComputeLocation(m.orch.AppDef(), state, w, turn)
		m.location, _ = m.location.Update(locationUpdated{loc: loc})
	}
}

// WithResumedTranscript seeds the transcript pane from journal entries
// collected by AttachSession (continue-mode §4.6 transcript rehydration).
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

	ti := textinput.New()
	ti.Placeholder = "what now?"
	ti.Focus()
	ti.CharLimit = 512

	sp := spinner.New()
	sp.Spinner = spinner.MiniDot
	sp.Style = lipgloss.NewStyle().Foreground(colorPrimary)

	m := RootModel{
		orch:           orch,
		sid:            sid,
		appPath:        appPath,
		mode:           ModeOnPath,
		width:          defaultWidth,
		height:         defaultHeight,
		location:       newLocationModel(),
		transcript:     newTranscriptModel(defaultWidth-menuWidth-6, transcriptHeight-inboxHeight),
		menu:           newMenuModel(menuWidth, transcriptHeight-inboxHeight),
		inbox:          newInboxModel(menuWidth, inboxHeight),
		offPath:        newOffPathModel(offPathBannerFromApp(orch.AppDef())),
		clarify:        newClarifyModel(),
		disambiguation: newDisambiguationModel(),
		menuSystem:     newMenuSystemModel(metaMenuEntries(orch.AppDef())),
		metaMode:       newMetaModel(),
		sessionsPanel:  newSessionsPanelModel(),
		prompt:         ti,
		spinner:        sp,
		mouseOn:        true,
	}

	// Set initial state.
	m.currentState = orch.InitialState()

	// Show initial view in transcript.
	if initialView != "" {
		m.transcript.AppendSystem(initialView)
	}

	// Populate initial menu.
	w := orch.InitialWorld()
	computedMenu := orchestrator.ComputeMenu(orch.AppDef(), orch.Machine(), m.currentState, w)
	m.menu, _ = m.menu.Update(menuItemsChanged{items: computedMenu.Primary, blocked: computedMenu.Blocked})

	// Set initial location.
	loc := orchestrator.ComputeLocation(orch.AppDef(), m.currentState, w, 0)
	m.location, _ = m.location.Update(locationUpdated{loc: loc})

	// Apply functional options (e.g. WithTUIClock from tests).
	for _, opt := range opts {
		opt(&m)
	}

	// Wire the metamode controller from production seams when the
	// caller didn't inject one. Requires both a chat store and at
	// least one declared meta_mode; without either, /meta stays
	// unavailable and the slash command surfaces a polite hint.
	if m.metaController == nil && m.chatStore != nil && len(orch.AppDef().MetaModes) > 0 {
		if reg := host.AgentRegistry(); reg != nil {
			m.metaController = &metamode.Controller{
				Chats:         metamode.NewChatStoreAdapter(m.chatStore),
				Agents:        reg,
				AppDef:        orch.AppDef(),
				Oracle:        metamode.NewOracleCallerAdapter(),
				JournalWriter: m.journalWriter, // site 28-30: wire ledger journal writes
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
// `<Group> › <Trigger>` (proposal §1.1 cosmetic), ungrouped keys keep
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
	cmds := []tea.Cmd{textinput.Blink, m.spinner.Tick}
	if m.jobStore != nil {
		cmds = append(cmds, m.scheduleInboxPoll(2*time.Second))
	}
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
func (m RootModel) pollInbox() tea.Msg {
	if m.jobStore == nil {
		return nil
	}
	ctx := context.Background()
	ns, err := m.jobStore.ListNotifications(ctx, m.sid, 20)
	if err != nil {
		slog.Warn("tui: inbox poll error", "err", err)
		return nil
	}
	return inboxRefreshed{notifications: ns}
}

// Update implements tea.Model.
func (m RootModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// If slot-filling is active, route to clarify model first.
	if m.mode == ModeSlotFilling {
		return m.updateSlotFilling(msg)
	}
	// If disambiguating, route to disambiguation model first.
	if m.mode == ModeDisambiguating {
		return m.updateDisambiguating(msg)
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

	case sessionsPanelLoadedMsg:
		return m.handleSessionsPanelLoaded(msg)

	case sessionsPanelChoiceMsg:
		return m.handleSessionsPanelChoice(msg)

	case spinner.TickMsg:
		// Only animate when awaiting LLM.
		if m.mode == ModeAwaitingLLM {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil

	case inboxPollMsg:
		// Fire the actual DB read as a new Cmd, then schedule the next tick.
		if m.jobStore == nil {
			return m, nil
		}
		return m, func() tea.Msg { return m.pollInbox() }

	case inboxRefreshed:
		m.lastNotifications = msg.notifications
		m.inbox, _ = m.inbox.Update(msg)
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

func (m RootModel) updateSlotFilling(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case supplementSlotsMsg:
		m.mode = ModeOnPath
		m.clarify.Close()
		return m.handleSupplementSlots(msg)

	case tea.KeyMsg:
		if msg.Type == tea.KeyEsc {
			// Cancel clarify.
			m.mode = ModeOnPath
			m.clarify.Close()
			m.transcript.AppendSystem("(slot fill cancelled)")
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.clarify, cmd = m.clarify.Update(msg)
	return m, cmd
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

	// Esc opens the system menu from the default interactive modes.
	// It deliberately does not fire during ModeAwaitingLLM (Ctrl+C cancels
	// the turn first) or while a slot-filling / disambiguation overlay is
	// already using Esc to back out.
	if msg.Type == tea.KeyEsc {
		if m.mode == ModeOnPath || m.mode == ModeOffPath {
			m.mode = ModeMenu
			m.menuSystem.Open()
			return m, nil
		}
	}

	// Scroll keys forward to the transcript viewport so prior turns can be
	// re-read. Allowed in every mode (including ModeAwaitingLLM) because
	// scrollback never mutates state.
	if isScrollKey(msg) {
		var cmd tea.Cmd
		m.transcript, cmd = m.transcript.Update(msg)
		return m, cmd
	}

	// Slash commands always route, even during ModeAwaitingLLM.
	if msg.Type == tea.KeyEnter {
		input := strings.TrimSpace(m.prompt.Value())
		if strings.HasPrefix(input, "/") {
			m.prompt.SetValue("")
			m.appendHistory(input)
			return m.handleSlashCommand(input)
		}
	}

	// While awaiting LLM: show a notice and swallow other input.
	if m.mode == ModeAwaitingLLM {
		if msg.Type == tea.KeyEnter {
			// Flash a transient notice — appended as a system message.
			m.transcript.AppendSystem("hold on — still thinking about the previous turn")
		}
		// All other keys are silently ignored while in-flight.
		return m, nil
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
				ctx := context.Background()
				_ = m.jobStore.MarkNotificationRead(ctx, n.ID)
				// Site 27 (c): inbox item dismissed (Esc / snooze semantics).
				m.emitInboxDismissed(n.ID, n.Title)
				// Trigger a re-poll so the inbox snapshot updates immediately.
				return m, func() tea.Msg { return m.pollInbox() }
			}
			return m, nil
		}
	}

	switch msg.String() {
	case "enter":
		// If the prompt is empty and a menu row is highlighted, dispatch it directly.
		if strings.TrimSpace(m.prompt.Value()) == "" {
			if entry := m.menu.SelectedEntry(); entry != nil {
				return m.dispatchMenuEntry(entry)
			}
		}
		return m.submitInput()

	case "up":
		// Plain Up arrow walks input history backward (newer → older).
		// Modified Up (shift+up / alt+up) is already routed to the
		// transcript viewport by isScrollKey above, so we only see the
		// unmodified key here.
		return m.historyPrev(), nil

	case "down":
		// Plain Down arrow walks input history forward (older → newer);
		// stepping past the newest entry restores the saved draft.
		return m.historyNext(), nil

	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		// When the prompt is empty, number keys are menu hotkeys — they select a row
		// without feeding the digit into the text input.
		if strings.TrimSpace(m.prompt.Value()) == "" {
			var menuCmd tea.Cmd
			m.menu, menuCmd = m.menu.Update(msg)
			_ = menuCmd
			return m, nil
		}
		// Prompt has text — fall through to normal text input handling.
	}

	// Any non-arrow, non-Enter keystroke while walking history should
	// "commit" the navigation: the currently-displayed entry becomes
	// the active draft and we leave history-walk mode. We don't mutate
	// the prompt text — the textinput.Update call below will fold the
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

	// Handle slash commands.
	if strings.HasPrefix(input, "/") {
		return m.handleSlashCommand(input)
	}

	// Off-path mode: free-form chat that does NOT touch world or state.
	// Routes directly to host.oracle.talk via Orchestrator.AskOffPath
	// rather than through MatchDeterministic → harness → machine.
	if m.mode == ModeOffPath {
		return m.submitOffPath(input)
	}

	m.lastInput = input

	// Cheap, side-effect-free match against the current menu. This avoids the
	// LLM round-trip when the user typed something we can route locally — but
	// we still dispatch the resulting transition asynchronously so a slow
	// on_enter host call (e.g. host.oracle.ask, host.run on a long command)
	// doesn't freeze the TUI.
	orch := m.orch
	sid := m.sid
	ctx := context.Background()
	intent, slots, hit, err := orch.MatchDeterministic(ctx, sid, input)
	if err != nil {
		m.transcript.AppendError(input, fmt.Sprintf("error: %v", err))
		return m, nil
	}
	if hit {
		return startAsyncTurn(m, input, asyncSubmitDirect(orch, sid, intent, slots), pendingDeterministic)
	}

	// No deterministic match — call the LLM router asynchronously.
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
	m.inFlightCancel = cancel
	m.mode = ModeAwaitingLLM
	m.pendingKind = kind

	cmd := func() tea.Msg {
		out, err := run(ctx)
		if err != nil {
			// If the context was cancelled, return a ModeCancelled outcome
			// rather than a raw error.
			if ctx.Err() != nil {
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
		return m.handleTraceToggle(), nil

	case "/mouse":
		return m.handleMouseCommand(parts[1:])

	case "/inbox":
		m.inbox.ToggleExpanded()
		return m, nil

	case "/meta":
		return m.handleMetaSlash(parts[1:])

	case "/sessions":
		return m.handleSessionsSlash(parts[1:])

	case "/warp":
		return m.handleWarpCommand(parts[1:])

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

// handleMouseCommand toggles the TUI's mouse capture on or off at runtime.
// Mouse capture enables scroll-wheel events on the transcript viewport but
// hijacks native click-drag text selection. Without an argument it toggles;
// "/mouse on" and "/mouse off" force a state.
//
// When mouse is off, standard terminal selection works again; keyboard
// scroll bindings (Shift+↑/↓, Ctrl+U/Ctrl+D, Ctrl+B/Ctrl+F) still work.
func (m RootModel) handleMouseCommand(args []string) (tea.Model, tea.Cmd) {
	want := !m.mouseOn // default: toggle
	if len(args) > 0 {
		switch strings.ToLower(args[0]) {
		case "on", "enable", "true", "1":
			want = true
		case "off", "disable", "false", "0":
			want = false
		case "toggle", "":
			want = !m.mouseOn
		default:
			m.transcript.AppendSystem(fmt.Sprintf("(mouse: unknown arg %q — use on|off|toggle)", args[0]))
			return m, nil
		}
	}
	if want == m.mouseOn {
		state := "off"
		if m.mouseOn {
			state = "on"
		}
		m.transcript.AppendSystem(fmt.Sprintf("(mouse: already %s)", state))
		return m, nil
	}
	m.mouseOn = want
	if want {
		m.transcript.AppendSystem("(mouse: ON — scroll wheel works; hold Option/Shift while dragging to select text)")
		return m, tea.EnableMouseCellMotion
	}
	m.transcript.AppendSystem("(mouse: OFF — native click-drag text selection works; keyboard scroll still active)")
	return m, tea.DisableMouse
}

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
		// so a slow on_enter host call (e.g. host.oracle.ask) does not freeze
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
	if len(needs) > 0 {
		slot := needs[0]
		prompt := slot.Prompt
		if prompt == "" {
			prompt = "Please provide: " + slot.Name
		}
		m.transcript.AppendSystem("> " + entry.Display + "\n" + prompt)
	}
	return m, nil
}

func (m RootModel) runTurn(input string) tea.Cmd {
	orch := m.orch
	sid := m.sid
	return func() tea.Msg {
		ctx := context.Background()
		out, err := orch.Turn(ctx, sid, input)
		return turnOutcomeMsg{outcome: out, input: input, err: err}
	}
}

func (m RootModel) handleTurnOutcome(msg turnOutcomeMsg) (tea.Model, tea.Cmd) {
	// Clear in-flight state (safe to call even if already cleared).
	if m.inFlightCancel != nil {
		m.inFlightCancel = nil
	}
	if m.mode == ModeAwaitingLLM {
		m.mode = ModeOnPath
	}

	if msg.err != nil {
		m.transcript.AppendError(msg.input, fmt.Sprintf("error: %v", msg.err))
		return m, nil
	}

	out := msg.outcome

	switch out.Mode {
	case orchestrator.ModeCancelled:
		m.transcript.AppendSystem("(cancelled)")
		return m, nil

	case orchestrator.ModeTransitioned, orchestrator.ModeCompleted:
		m.currentState = out.NewState
		m.transcript.AppendTurn(msg.input, out.View)

		// Update menu.
		w := m.orch.InitialWorld() // only used for initial world; menu comes from allowed list
		m = m.updateMenuFromAllowed(out.AllowedIntents, w)

		// Update location.
		m = m.updateLocation(out)

		if out.Mode == orchestrator.ModeCompleted {
			m.transcript.AppendSystem("\n[Game over — start a new session to play again]")
			m.prompt.Placeholder = "(game over)"
		}

	case orchestrator.ModeClarify:
		// Enter slot-filling mode.
		m.mode = ModeSlotFilling
		m.clarify.Open(out.PendingIntent, out.SlotsNeeded, out.PendingSlots)
		// Show a prompt asking for the first slot.
		if len(out.SlotsNeeded) > 0 {
			slot := out.SlotsNeeded[0]
			prompt := slot.Prompt
			if prompt == "" {
				prompt = "Please provide: " + slot.Name
			}
			m.transcript.AppendSystem("> " + msg.input + "\n" + prompt)
		}

	case orchestrator.ModeRejected:
		m.currentState = out.NewState

		// If there are disambiguation candidates, enter disambiguation mode.
		if len(out.Candidates) > 0 {
			m.mode = ModeDisambiguating
			m.disambiguation.Open(out.Candidates)
			m.transcript.AppendTurn(msg.input, "")
			m.transcript.AppendSystem(m.disambiguation.View())
			// Site 31: emit disambig.presented when the overlay is shown.
			m.emitDisambigPresented(out.Candidates)
			return m, nil
		}

		m.renderRejection(msg.input, out)
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
		m.transcript.AppendTurn(userInput, "")
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
		m.transcript.AppendError("(slot-fill)", fmt.Sprintf("error: %v", msg.err))
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
	return candidate
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
		m.transcript.AppendError("(meta done)", fmt.Sprintf("error: %v", msg.err))
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

// openSessionsPanel kicks off the foyer "meta sessions" overlay
// (proposal §2.1). The controller call is async (it touches the
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
		m.transcript.AppendError("(meta new)", fmt.Sprintf("error: %v", msg.err))
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
	// the new chat starts visually fresh. First-entry from on-path
	// keeps the prior transcript so the user sees the context they
	// came from.
	if m.mode == ModeMeta {
		m.transcript = newTranscriptModel(m.transcript.vp.Width, m.transcript.vp.Height)
	}

	m.metaMode.Enter(msg.session)
	m.mode = ModeMeta
	m.prompt.Placeholder = "meta chat — /onpath to return"

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
func (m RootModel) handleMetaSendDone(msg metaSendDoneMsg) (tea.Model, tea.Cmd) {
	m.metaMode.inFlight = false

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
		m.transcript.AppendError("(meta)", fmt.Sprintf("error: %v", msg.err))
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

// reloadOrchestratorAfterMeta is the no-file-list overload used by
// the dormant legacy authoring-token dispatcher.
func (m RootModel) reloadOrchestratorAfterMeta() (tea.Model, tea.Cmd) {
	return m.reloadOrchestratorAfterMetaWithFiles(nil)
}

// reloadOrchestratorAfterMetaWithFiles runs the same reload-and-rerender
// path the edit overlay uses on apply and prints the list of files the
// agent touched so the user can see whether the change landed in
// app.yaml, an include, a prompt, or a script.
func (m RootModel) reloadOrchestratorAfterMetaWithFiles(changed []string) (tea.Model, tea.Cmd) {
	res, err := m.orch.Reload(m.appPath, m.currentState)
	if err != nil {
		m.transcript.AppendError("(meta)",
			"applied to disk but reload failed: "+err.Error())
		return m, nil
	}
	w := m.orch.CurrentWorld(m.sid)
	computed := orchestrator.ComputeMenu(m.orch.AppDef(), m.orch.Machine(), m.currentState, w)
	m.menu, _ = m.menu.Update(menuItemsChanged{items: computed.Primary, blocked: computed.Blocked})
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
		// Feed the prompt textinput. Don't pass keys while a turn is
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
	// meta chat (proposal §4.2 / §9.3); /sessions list and
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
	return m, tea.Batch(m.spinner.Tick,
		metaSendCmd(context.Background(), m.metaController, m.metaMode.session, text, turn))
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
	w := m.orch.CurrentWorld(m.sid)
	view, _ := m.orch.Machine().RenderState(m.currentState, w)

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

	// Surface the imported-manifest paths so the metamode controller's
	// file-watch tree includes every sibling story's directory. Without
	// this, an edit in stories/robbery/ while running stories/oregon-trail/
	// would not auto-reload. (Story-imports proposal §16.4.)
	var importedPaths []string
	if def := m.orch.AppDef(); def != nil {
		importedPaths = append(importedPaths, def.LoadedManifests...)
	}

	return metamode.TurnContext{
		StatePath:             string(m.currentState),
		AppFile:               m.appPath,
		RenderedView:          view,
		World:                 w.Vars,
		TracePath:             tracePath,
		ImportedManifestPaths: importedPaths,
	}
}

// exitMetaMode tears down the overlay and pops back to ModeOnPath.
// Calls Controller.Exit so future workstreams that want to land
// per-mode cleanup (discard-on-exit for non-persistent modes, for
// example) have a single seam to extend.
func (m RootModel) exitMetaMode() RootModel {
	ctrl := m.metaController
	sess := m.metaMode.session
	if ctrl != nil && sess != nil {
		// Exit is currently a no-op (proposal §4.3: drafts survive).
		// Capture the error for diagnostics but don't block the UX.
		if err := ctrl.Exit(context.Background(), sess); err != nil {
			slog.Warn("tui.meta: controller.Exit error", "err", err)
		}
	}

	// Mark where the post-exit section starts so we can scroll the
	// meta-mode chat off the top of the viewport (still reachable via
	// scroll-up).
	mark := m.transcript.ContentHeight()

	// Summarise the session so the user knows whether changes landed.
	// Edits already triggered an in-flight reload on each Send that
	// touched the story tree; this just makes the totals visible at
	// /onpath time and lists every file the agent touched (dedup'd
	// across turns) so the user can spot includes/prompts/scripts
	// that wouldn't be obvious from the chat alone.
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
	m.prompt.Placeholder = "what now?"
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
	}
	return m, nil
}

func (m RootModel) updateDisambiguating(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case disambiguationChoiceMsg:
		return m.handleDisambiguationChoice(msg)
	case tea.KeyMsg:
		if msg.Type == tea.KeyEsc {
			m.mode = ModeOnPath
			m.disambiguation.Close()
			m.transcript.AppendSystem("(disambiguation cancelled)")
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.disambiguation, cmd = m.disambiguation.Update(msg)
	return m, cmd
}

func (m RootModel) handleDisambiguationChoice(msg disambiguationChoiceMsg) (tea.Model, tea.Cmd) {
	m.mode = ModeOnPath
	m.disambiguation.Close()
	chosen := msg.chosen
	m.transcript.AppendSystem(fmt.Sprintf("(chose: %s)", chosen.Intent))
	// Site 31: emit disambig.chosen when the user picks an option.
	m.emitDisambigChosen(chosen)
	// Re-run the turn with the chosen intent directly.
	// We synthesise the input as the intent name so the harness is bypassed
	// and the machine receives the choice directly.
	orch := m.orch
	sid := m.sid
	return m, func() tea.Msg {
		// Note: for a real implementation this would call a ContinueWithIntent method.
		// For the PoC, we surface the choice in the transcript.
		_ = orch
		_ = sid
		return nil
	}
}

func (m RootModel) handleSupplementSlots(msg supplementSlotsMsg) (tea.Model, tea.Cmd) {
	m.mode = ModeOnPath
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
	return m
}

func (m RootModel) updateLocation(out *orchestrator.TurnOutcome) RootModel {
	w := m.orch.CurrentWorld(m.sid)
	loc := orchestrator.ComputeLocation(m.orch.AppDef(), out.NewState, w, out.TurnNumber)
	m.location, _ = m.location.Update(locationUpdated{loc: loc})
	return m
}

func (m RootModel) resize() RootModel {
	const minMenuWidth = 28
	const locationHeight = 2
	const promptHeight = 3
	const minInboxHeight = 8

	menuWidth := minMenuWidth

	// Allocate the full remaining horizontal space to the transcript.
	// lipgloss.JoinHorizontal concatenates panes with no gap, so the
	// transcript panel's total width must be m.width - menuWidth for both
	// panes together to fill the terminal. Previously this subtracted an
	// extra 4 chars "for safety", which just left a visible dead zone on
	// the right and made the body text wrap 4 chars early.
	transcriptWidth := m.width - menuWidth
	if transcriptWidth < 40 {
		transcriptWidth = 40
	}
	totalRightHeight := m.height - locationHeight - promptHeight - 4
	if totalRightHeight < 5 {
		totalRightHeight = 5
	}

	// Split the right column vertically: menu on top, inbox below.
	// Inbox gets minInboxHeight; menu gets the remainder.
	inboxHeight := minInboxHeight
	menuHeight := totalRightHeight - inboxHeight
	if menuHeight < 5 {
		menuHeight = 5
		inboxHeight = max(0, totalRightHeight-menuHeight)
	}

	// Inner content area = panel width minus border (1 each side) and
	// padding (1 each side) from transcriptStyle. That's 4 chars total.
	innerWidth := transcriptWidth - 4

	// Apply size through SetSize so the Glamour renderer rebuilds with the
	// correct wrap width. The root intercepts tea.WindowSizeMsg before it
	// reaches the transcript, so this is the only place the transcript
	// learns its real size.
	// Transcript height = entire right-column height (it fills the same
	// vertical space; it is placed opposite the stacked menu+inbox).
	m.transcript.SetSize(transcriptWidth, totalRightHeight, innerWidth, totalRightHeight-2)

	m.menu.width = menuWidth
	m.menu.height = menuHeight

	m.inbox.width = menuWidth
	m.inbox.height = inboxHeight

	m.location, _ = m.location.Update(tea.WindowSizeMsg{Width: m.width, Height: 1})

	// Prompt width — set so textinput clips & horizontally scrolls once the
	// value exceeds the visible area. With Width == 0 the bubbles textinput
	// renders the entire string with no clipping, so long input bleeds past
	// the right terminal edge and disappears.
	//
	// The prompt line is a full-width row at the bottom of the screen,
	// preceded by a 2-column prefix ("> ", "» ", or off-path "> "). The
	// prefix style (promptStyle / promptOffPathStyle) is foreground-only
	// (see styles.go) so it adds no padding beyond the 2 visible glyphs.
	// We still subtract a small safety margin so the cursor + any terminal-
	// edge quirks don't push past the last column.
	const promptPrefixCols = 2 // "> " / "» "
	const promptSafetyMargin = 2
	const promptMinWidth = 20
	promptWidth := m.width - promptPrefixCols - promptSafetyMargin
	if promptWidth < promptMinWidth {
		promptWidth = promptMinWidth
	}
	m.prompt.Width = promptWidth

	return m
}

// View implements tea.Model.
func (m RootModel) View() string {
	if m.quitting {
		return "Goodbye!\n"
	}

	// Location bar (full width).
	locationBar := m.location.View()

	// Main body: transcript (left) + [menu + inbox] (right, stacked vertically).
	// In ModeMeta the on-path actions menu is irrelevant (FSM is paused),
	// so swap it for the meta-mode side panel that documents the meta
	// slash commands.
	transcriptView := m.transcript.View()
	menuView := m.menu.View()
	if m.mode == ModeMeta {
		menuView = m.metaMode.MenuView(m.menu.width, m.menu.height)
	}
	inboxView := m.inbox.View()
	rightCol := lipgloss.JoinVertical(lipgloss.Left, menuView, inboxView)
	body := lipgloss.JoinHorizontal(lipgloss.Top, transcriptView, rightCol)

	// Action-required banner above the prompt (when applicable).
	var bannerLine string
	if banner := m.inbox.ActionRequiredBanner(); banner != "" {
		bannerLine = banner
	}

	// Prompt line.
	// Spinner placement: right of the prompt prefix on the input line.
	// This keeps the location bar uncluttered and puts the spinner
	// where the user's eye is already focused (the input area).
	var promptLine string
	if m.mode == ModeSlotFilling {
		promptLine = m.clarify.View()
	} else if m.mode == ModeDisambiguating {
		promptLine = m.disambiguation.View()
	} else if m.mode == ModeMenu {
		promptLine = m.menuSystem.View()
	} else if m.mode == ModeMetaSessions {
		promptLine = m.sessionsPanel.View()
	} else if m.mode == ModeAwaitingLLM {
		spinnerStr := m.spinner.View()
		caption := "thinking via claude… (Ctrl+C to cancel)"
		if m.pendingKind == pendingDeterministic {
			caption = "running… (Ctrl+C to cancel)"
		}
		promptLine = promptStyle.Render("> ") + spinnerStr + " " +
			lipgloss.NewStyle().Foreground(colorMuted).Render(caption)
	} else if m.mode == ModeMeta {
		if m.metaMode.inFlight {
			promptLine = promptStyle.Render("» ") + m.spinner.View() + " " +
				lipgloss.NewStyle().Foreground(colorMuted).Render("agent is thinking… (Esc to cancel)")
		} else {
			promptLine = promptStyle.Render("» ") + m.prompt.View()
		}
	} else {
		prefix := promptStyle.Render("> ")
		if m.mode == ModeOffPath {
			prefix = promptOffPathStyle.Render("> ")
		}
		promptLine = prefix + m.prompt.View()
	}

	// Key hints — shown under the prompt so users know how to scroll back
	// through prior turns. Bindings are picked to work on MacBook keyboards
	// without PgUp/PgDn/Home/End physical keys. When mouse capture is off,
	// drop "mouse wheel" from the hint and advertise the /mouse command.
	// Prepend inbox badge when there are unread notifications.
	var scrollHint string
	if m.mouseOn {
		scrollHint = "scroll: mouse wheel · Shift+↑/↓ · Ctrl+U/Ctrl+D (half) · Ctrl+B/Ctrl+F (page) · /mouse off to select text"
	} else {
		scrollHint = "scroll: Shift+↑/↓ · Ctrl+U/Ctrl+D (half) · Ctrl+B/Ctrl+F (page) · /mouse on to re-enable wheel"
	}
	if badge := m.inboxBadge(); badge != "" {
		scrollHint = badge + "  ·  " + scrollHint
	}
	hints := lipgloss.NewStyle().Foreground(colorMuted).Render(scrollHint)

	// Stack vertically, inserting banner when present.
	parts := []string{locationBar, body}
	if bannerLine != "" {
		parts = append(parts, bannerLine)
	}
	parts = append(parts, promptLine, hints)
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

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
