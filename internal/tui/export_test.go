// export_test.go — white-box helpers for tui package tests.
// Compiled only during `go test` (package tui, not tui_test).
package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"kitsoki/internal/app"
	"kitsoki/internal/clock"
	"kitsoki/internal/intent"
	"kitsoki/internal/jobs"
	"kitsoki/internal/metamode"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/tui/blocks"
)

// GetTranscriptContent extracts the transcript content from a RootModel
// stored in a tea.Model interface.
func GetTranscriptContent(m RootModel) string {
	return m.transcript.AllContent()
}

// GetMode returns the current mode from a RootModel.
func GetMode(m RootModel) Mode {
	return m.mode
}

// ExtractRootModel type-asserts a tea.Model to RootModel.
// Returns (RootModel, true) on success or (zero, false) if not a RootModel.
func ExtractRootModel(m interface{}) (RootModel, bool) {
	rm, ok := m.(RootModel)
	return rm, ok
}

// GetMenuPrimaryItems returns the Display strings for all primary menu items.
func GetMenuPrimaryItems(m RootModel) []string {
	items := make([]string, len(m.menu.items))
	for i, e := range m.menu.items {
		items[i] = e.Display
	}
	return items
}

// IsScrollKey exposes isScrollKey for testing.
func IsScrollKey(msg tea.KeyMsg) bool { return isScrollKey(msg) }

// TranscriptYOffset returns the transcript viewport's current scroll offset.
func TranscriptYOffset(m RootModel) int { return m.transcript.vp.YOffset }

// TranscriptAtBottom reports whether the transcript viewport is scrolled
// all the way to the bottom.
func TranscriptAtBottom(m RootModel) bool { return m.transcript.vp.AtBottom() }

// AppendTranscriptForTest appends a system message to the transcript,
// bypassing the turn pipeline. Used to set up scrollable history in tests.
func AppendTranscriptForTest(m *RootModel, body string) {
	m.transcript.AppendSystem(body)
}

// AppendTurnForTest appends a user turn with header + view, bypassing the
// orchestrator. Lets tests exercise view-rendering behaviour in isolation.
func AppendTurnForTest(m *RootModel, input, view string) {
	m.transcript.AppendTurn(input, view)
}

// SetTranscriptSizeForTest resizes the transcript viewport directly.
// Goes through SetSize so the Glamour renderer is rebuilt at the new wrap
// width — matches what resize() does in production.
func SetTranscriptSizeForTest(m *RootModel, width, height int) {
	m.transcript.SetSize(width, height, width, height)
	m.transcript.vp.GotoBottom()
}


// PreserveLeadingIndent exposes the internal leading-indent preprocessor.
func PreserveLeadingIndent(s string) string { return preserveLeadingIndent(s) }

// MouseOn — removed in phase 7. Mouse support is gone (phase 5);
// tests that scraped the toggle now assert on /mouse's removal notice
// via GetTranscriptContent.

// IsInFlight returns true if the model is in ModeAwaitingLLM.
func IsInFlight(m RootModel) bool {
	return m.mode == ModeAwaitingLLM
}

// HasInFlightCancel returns true if inFlightCancel is non-nil.
func HasInFlightCancel(m RootModel) bool {
	return m.inFlightCancel != nil
}

// TriggerTurnOutcomeMsg injects a turnOutcomeMsg into the model for testing.
// This simulates the async turn completing.
func TriggerTurnOutcomeMsg(m tea.Model, outcome *orchestrator.TurnOutcome, input string, err error) (tea.Model, tea.Cmd) {
	return m.Update(turnOutcomeMsg{outcome: outcome, input: input, err: err})
}

// SimulateSlowHarnessTurnStart puts the model into ModeAwaitingLLM with a no-op
// cancel func (for testing single-flight behavior without an actual goroutine).
func SimulateSlowHarnessTurnStart(m RootModel) RootModel {
	m.mode = ModeAwaitingLLM
	m.inFlightCancel = func() {}
	return m
}

// CancelInFlight calls the in-flight cancel func if set. Returns true if it was called.
func CancelInFlight(m RootModel) bool {
	if m.inFlightCancel != nil {
		m.inFlightCancel()
		return true
	}
	return false
}

// StartLLMTurnForTest calls the internal async-turn helper for the LLM
// router path so tests can exercise the full async path without going
// through submitInput.
func StartLLMTurnForTest(m RootModel, input string) (RootModel, tea.Cmd) {
	return startAsyncTurn(m, input, asyncTurn(m.orch, m.sid, input), pendingLLM)
}

// MockCancelCtx creates a context that is already cancelled, useful for tests.
func MockCancelCtx() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx, cancel
}

// Quitting returns true if the model is in quitting state.
func (m RootModel) Quitting() bool {
	return m.quitting
}

// ── Clarify test helpers ──────────────────────────────────────────────────────

// TestClarifyModelWrapper wraps a clarifyModel for testing from the _test package.
type TestClarifyModelWrapper struct {
	m *clarifyModel
}

// NewTestClarifyModel creates a new clarifyModel for testing.
func NewTestClarifyModel() *TestClarifyModelWrapper {
	m := newClarifyModel()
	return &TestClarifyModelWrapper{m: &m}
}

// Open calls clarifyModel.Open.
func (w *TestClarifyModelWrapper) Open(intent string, slots []orchestrator.SlotNeed, existing map[string]any) {
	w.m.Open(intent, slots, existing)
}

// IsActive returns true if the model is active.
func (w *TestClarifyModelWrapper) IsActive() bool {
	return w.m.active
}

// InlineBlock returns the inline-rendered "clarification needed" block
// (Phase 2 single-pane redesign). Replaces the legacy View(), which is
// gone now that the clarify model no longer owns the prompt area.
func (w *TestClarifyModelWrapper) InlineBlock() string {
	return w.m.RenderInlineBlock(blocks.New(80, "default").WithNoColor(true))
}

// SubmitValue exposes clarifyModel.SubmitValue for unit testing.
func (w *TestClarifyModelWrapper) SubmitValue(input string) (string, bool, map[string]any, error) {
	return w.m.SubmitValue(input)
}

// CurrentSlotName returns the name of the slot the user is currently
// being asked about; used by tests to assert advance between slots.
func (w *TestClarifyModelWrapper) CurrentSlotName() string {
	slot, ok := w.m.CurrentSlot()
	if !ok {
		return ""
	}
	return slot.Name
}

// ── System menu test helpers ──────────────────────────────────────────────────

// MenuSystemActive reports whether the Esc-triggered system menu overlay is open.
func MenuSystemActive(m RootModel) bool { return m.menuSystem.IsActive() }

// MenuSystemView returns the rendered overlay (empty when inactive).
func MenuSystemView(m RootModel) string { return m.menuSystem.View() }

// ── Sessions panel test helpers ───────────────────────────────────────────────

// SessionsPanelActive reports whether the foyer "meta sessions" overlay is
// currently visible. Used by the end-to-end flow test to observe the
// async ListChats → handleSessionsPanelLoaded transition without
// reaching into private fields.
func SessionsPanelActive(m RootModel) bool { return m.sessionsPanel.IsActive() }

// SessionsPanelView returns the rendered overlay (empty when inactive).
func SessionsPanelView(m RootModel) string { return m.sessionsPanel.View() }

// MetaSessionChatID returns the chat ID of the currently-active meta
// session, or "" when no /meta overlay is open. Used by the
// sessions-panel flow test to assert resume targeted the right row
// without depending on the fake store's internal bookkeeping.
func MetaSessionChatID(m RootModel) string {
	if m.metaMode.session == nil || m.metaMode.session.Chat == nil {
		return ""
	}
	return m.metaMode.session.Chat.ID()
}

// SetPromptValue sets the prompt input value for tests that need to start
// with pre-filled text (e.g. Ctrl+C clears the prompt).
func SetPromptValue(m *RootModel, v string) { m.prompt.SetValue(v) }

// GetPromptValue returns the current prompt input value.
func GetPromptValue(m RootModel) string { return m.prompt.Value() }

// GetPromptWidth returns the textarea inner content width set on the
// prompt by resize(). Tests use this to assert resize() reserves a
// usable input column count after the prompt prefix and safety margin.
func GetPromptWidth(m RootModel) int { return m.prompt.Width() }

// GetPromptHeight returns the textarea.Model.Height() set on the prompt.
// Used by the wrap golden test to assert multi-line content grows the
// prompt vertically.
func GetPromptHeight(m RootModel) int { return m.prompt.Height() }

// GetPromptView returns the rendered textarea view (with the mode
// prefix prepended on row 0). The wrap golden test inspects this to
// confirm long input visually flows onto multiple display rows.
func GetPromptView(m RootModel) string {
	// Mirror View()'s pre-render setup so the test observes exactly
	// the same lipgloss output the user sees.
	m.prompt.SetPromptFunc(promptPrefixCols, m.promptLineFunc())
	m.prompt.SetHeight(promptHeightFor(&m.prompt))
	return m.prompt.View()
}

// ResizeRootModel forwards a tea.WindowSizeMsg into the model so tests
// can drive the production resize() path without poking private fields.
func ResizeRootModel(m RootModel, width, height int) RootModel {
	m.width = width
	m.height = height
	return m.resize()
}

// GetInputHistory returns a copy of the in-memory prompt history (oldest
// first). Used by tui_history_test.go to assert append / dedupe semantics.
func GetInputHistory(m RootModel) []string {
	out := make([]string, len(m.inputHistory))
	copy(out, m.inputHistory)
	return out
}

// HistoryNavigating reports whether the user is currently walking the
// input history with arrow keys (i.e. historyIdx points at a stored
// entry rather than the "draft" sentinel).
func HistoryNavigating(m RootModel) bool { return m.historyNavigating() }

// ── Disambiguation test helpers ───────────────────────────────────────────────

// TestDisambiguationModelWrapper wraps a disambiguationModel for testing.
type TestDisambiguationModelWrapper struct {
	m *disambiguationModel
}

// NewTestDisambiguationModel creates a new disambiguationModel for testing.
func NewTestDisambiguationModel() *TestDisambiguationModelWrapper {
	m := newDisambiguationModel()
	return &TestDisambiguationModelWrapper{m: &m}
}

// IsActive returns true if the model is active.
func (w *TestDisambiguationModelWrapper) IsActive() bool {
	return w.m.active
}

// Open calls disambiguationModel.Open.
func (w *TestDisambiguationModelWrapper) Open(candidates []intent.Candidate) {
	w.m.Open(candidates)
}

// InlineBlock returns the inline-rendered "did you mean?" block (Phase 2
// single-pane redesign). Replaces the legacy View().
func (w *TestDisambiguationModelWrapper) InlineBlock() string {
	return w.m.RenderInlineBlock(blocks.New(80, "default").WithNoColor(true))
}

// SubmitValue exposes disambiguationModel.SubmitValue for unit tests.
func (w *TestDisambiguationModelWrapper) SubmitValue(input string) (intent.Candidate, error) {
	return w.m.SubmitValue(input)
}

// ── Inbox test helpers ────────────────────────────────────────────────────────

// inboxModelWrapper wraps inboxModel to satisfy tea.Model (whose Update returns
// (tea.Model, tea.Cmd) rather than the concrete type).
type inboxModelWrapper struct{ m inboxModel }

func (w inboxModelWrapper) Init() tea.Cmd { return w.m.Init() }
func (w inboxModelWrapper) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	updated, cmd := w.m.Update(msg)
	return inboxModelWrapper{m: updated}, cmd
}
func (w inboxModelWrapper) View() string { return w.m.View() }

// NewInboxModelForTest creates an inboxModel pre-populated with notifications
// and returns it as a tea.Model interface.  Width and height set the panel
// dimensions (matching what resize() would compute in production).
func NewInboxModelForTest(width, height int, ns []jobs.Notification) tea.Model {
	m := newInboxModel(width, height)
	if len(ns) > 0 {
		m, _ = m.Update(inboxRefreshed{notifications: ns})
	}
	return inboxModelWrapper{m: m}
}

// InboxRefreshedMsg constructs an inboxRefreshed message for injection into
// Update().  Used by tests that simulate polling results without a real ticker.
func InboxRefreshedMsg(ns []jobs.Notification) tea.Msg {
	return inboxRefreshed{notifications: ns}
}

// ExtractInboxItemSelected type-asserts a tea.Msg to inboxItemSelected and
// returns the embedded Notification.  Returns (zero, false) on mismatch.
func ExtractInboxItemSelected(msg tea.Msg) (jobs.Notification, bool) {
	sel, ok := msg.(inboxItemSelected)
	if !ok {
		return jobs.Notification{}, false
	}
	return sel.notification, true
}

// InboxActionRequiredBannerForTest constructs an inboxModel with the given
// notifications and returns its ActionRequiredBanner() string.
func InboxActionRequiredBannerForTest(width, height int, ns []jobs.Notification) string {
	m := newInboxModel(width, height)
	if len(ns) > 0 {
		m, _ = m.Update(inboxRefreshed{notifications: ns})
	}
	return m.ActionRequiredBanner()
}

// HumanizeDurationForTest exposes the package-private humanizeDuration helper.
func HumanizeDurationForTest(d time.Duration) string {
	return humanizeDuration(d)
}

// ── Clock / inbox-ticker test helpers ────────────────────────────────────────

// InboxPollMsg returns a synthetic inboxPollMsg for injection into Update().
// This lets tests simulate the polling tick without waiting for a real or
// fake-clock timer to fire.
func InboxPollMsg() tea.Msg { return inboxPollMsg{} }

// RootModelClock returns the clock stored in m.clk, or nil if none was set.
// Used by inbox_clock_test.go to verify that WithTUIClock was applied.
func RootModelClock(m RootModel) clock.Clock { return m.clk }

// ScheduleInboxPollForTest exposes scheduleInboxPoll so tests can call it
// directly and capture the returned tea.Cmd (which wraps the fake-clock
// After channel).
func ScheduleInboxPollForTest(m RootModel, d time.Duration) tea.Cmd {
	return m.scheduleInboxPoll(d)
}

// ── Meta-mode test helpers ───────────────────────────────────────────────────

// NewMetaSendDoneMsgForTest constructs a metaSendDoneMsg for injection
// into Update(). Used by reload-flag tests to exercise the
// SendResult.ReloadRequested path without running a real oracle.
func NewMetaSendDoneMsgForTest(userText, assistantText string, reload bool, err error) tea.Msg {
	return metaSendDoneMsg{
		userText: userText,
		result: metamode.SendResult{
			Assistant:       assistantText,
			ReloadRequested: reload,
		},
		err: err,
	}
}

// AppID returns the orchestrator-bound app ID. Phase A.5 tests use
// this to seed chats under the same AppID the controller will read.
func (m RootModel) AppID() string {
	if def := m.orch.AppDef(); def != nil {
		return def.App.ID
	}
	return ""
}

// ── Routing observer test helpers ────────────────────────────────────────

// Phase 7 removed the legacy routing-chip Bubble Tea sub-model and the
// pendingLine single-slot queue. The chip's tier-event message types
// survive (routing_events.go) because routing_observer still emits
// them and the inline-routing transcript block consumes them.
// PendingLineForTest, RoutingChipActive, RoutingChipTier, and
// RoutingChipView are gone — tests now assert on transcript content
// via GetTranscriptContent.

// SetRoutingObserverForTest installs a *RoutingObserver on the model
// for tests that need to exercise the observer-driven inline routing
// block without going through NewRootModel's options.
func SetRoutingObserverForTest(m *RootModel, obs *RoutingObserver) {
	m.routingObserver = obs
}

// RoutingTraceOpen / RenderRoutingTraceOverlayForTest — removed in
// phase 7 along with the overlay itself. Tests assert on inline
// /trace block content via GetTranscriptContent.

// ── Per-room transcript / theme test helpers (phase 6) ──────────────────────

// CurrentStateForTest returns the model's currentState path. Used
// by rooms_test.go to assert navigation landed in the expected room
// without poking at unexported fields from the _test package.
func (m RootModel) CurrentStateForTest() app.StatePath { return m.currentState }

// ActiveRoomForTest returns the active room key — useful for
// asserting which transcript buffer is currently bound to
// m.transcript after a state change.
func (m RootModel) ActiveRoomForTest() app.StatePath { return m.activeRoom }

// ActivateRoomForTest exposes the package-private activateRoom helper
// so the transient-scroll test can exercise it without driving a full
// turn through the orchestrator.
func ActivateRoomForTest(m *RootModel, room app.StatePath, transient bool) {
	m.activateRoom(room, transient)
}

// ResolveRoomThemeForTest exposes themeNameForRoom + roomDecl so the
// theme-honouring test can assert resolution without going through
// the RootModel's runtime theme accessor (which folds in the meta /
// off-path overrides).
func ResolveRoomThemeForTest(def *app.AppDef, room app.StatePath) string {
	return themeNameForRoom(roomDecl(def, room))
}

// CurrentThemeForTest exposes the runtime theme accessor used by
// every blocks.New call site. Tests assert it changes after a
// per-room swap.
func CurrentThemeForTest(m RootModel) string { return m.currentTheme() }

// MetaRoomKeyForTest returns the synthetic meta-mode room key so
// tests can compare ActiveRoomForTest() against it without
// duplicating the constant.
func MetaRoomKeyForTest() app.StatePath { return metaRoomKey }

// InputQueue returns a copy of the live input queue used for tests
// asserting on Phase 4 queue behaviour. Order is FIFO.
func InputQueue(m RootModel) []string {
	out := make([]string, len(m.inputQueue))
	copy(out, m.inputQueue)
	return out
}

// SetInputQueueForTest seeds the input queue directly. Bypasses the
// usual enqueue path so tests can construct queue scenarios cheaply.
func SetInputQueueForTest(m *RootModel, items ...string) {
	m.inputQueue = append([]string(nil), items...)
}

// BackgroundCompletions returns a copy of the background-completion
// log keyed by /jump. Newest-first.
func BackgroundCompletions(m RootModel) []string {
	out := make([]string, len(m.backgroundCompletions))
	for i, bc := range m.backgroundCompletions {
		out[i] = bc.Room + " · " + bc.Summary
	}
	return out
}

// SetActionsAutoForTest flips the auto-print flag without going
// through the slash dispatcher.
func SetActionsAutoForTest(m *RootModel, on bool) { m.actionsAuto = on }

// SetModeForTest forces the Mode field. Used by promptPrefix /
// footer tests that need to observe every mode without driving the
// underlying state machine into each.
func SetModeForTest(m *RootModel, mode Mode) { m.mode = mode }

// PromptPrefixForTest exposes the rendered prompt prefix string for
// the current mode.
func PromptPrefixForTest(m RootModel) string { return m.promptPrefix() }

// FooterLine1ForTest exposes the framework footer line builder so
// tests can assert what shows in line 1 without scraping the View().
func FooterLine1ForTest(m RootModel) string { return footerFrameworkLine(m) }

// MaybeSwitchRoomOnStateForTest exposes the room-swap entry so tests
// that don't go through a full TurnOutcome can drive the swap
// manually.
func MaybeSwitchRoomOnStateForTest(m *RootModel, prev, curr app.StatePath) {
	m.maybeSwitchRoomOnState(prev, curr)
}

// CandidateForTest mirrors intent.Candidate so callers in tui_test
// don't have to import internal/intent.
type CandidateForTest struct {
	Intent string
	Title  string
}

// OpenDisambiguationForTest opens the disambig overlay with the
// given candidates and switches the model into ModeDisambiguating.
// Bypasses the orchestrator-driven path so the
// handleDisambiguationChoice dispatch behaviour can be exercised in
// isolation.
func OpenDisambiguationForTest(m *RootModel, candidates []CandidateForTest) {
	cs := make([]intent.Candidate, len(candidates))
	for i, c := range candidates {
		cs[i] = intent.Candidate{Intent: c.Intent, Title: c.Title}
	}
	m.disambiguation.Open(cs)
	m.mode = ModeDisambiguating
}
