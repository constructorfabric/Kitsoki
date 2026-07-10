// export_test.go — white-box helpers for tui package tests.
// Compiled only during `go test` (package tui, not tui_test).
package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"kitsoki/internal/app"
	"kitsoki/internal/chats"
	"kitsoki/internal/clock"
	"kitsoki/internal/expr"
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

// SimulateMetaTurnInFlight puts the model into ModeMeta with the
// meta-mode turn marked in-flight, so the meta "agent is thinking…"
// caption renders without driving a real meta turn. Used by the
// cancel-copy frame test.
func SimulateMetaTurnInFlight(m RootModel) RootModel {
	m.mode = ModeMeta
	m.metaMode.inFlight = true
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

// OpenMenuSystemForTest activates the Esc menu overlay so its rendered
// rows can be asserted without simulating the Esc keypress sequence.
func OpenMenuSystemForTest(m *RootModel) { m.menuSystem.Open() }

// PromptPlaceholderForTest exposes the prompt textarea's placeholder so
// tests can assert the first-run typing/help hint.
func PromptPlaceholderForTest(m RootModel) string { return m.prompt.Placeholder }

// ── Sessions panel test helpers ───────────────────────────────────────────────

// SessionsPanelActive reports whether the foyer "meta sessions" overlay is
// currently visible. Used by the end-to-end flow test to observe the
// async ListChats → handleSessionsPanelLoaded transition without
// reaching into private fields.
func SessionsPanelActive(m RootModel) bool { return m.sessionsPanel.IsActive() }

// SessionsPanelView returns the rendered overlay (empty when inactive).
func SessionsPanelView(m RootModel) string { return m.sessionsPanel.View() }

// CachedSessionListForTest returns the current /sessions attach cache.
func CachedSessionListForTest(m RootModel) []chats.PtySession {
	out := make([]chats.PtySession, len(m.sessionList))
	copy(out, m.sessionList)
	return out
}

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

// ── Choice-widget integration test helpers ───────────────────────────────────

// SetPendingDraftForTest seeds m.pendingDraft so /input tests can
// assert the slash command's restore behaviour without first having to
// drive a choice widget to seize focus.
func SetPendingDraftForTest(m *RootModel, v string) { m.pendingDraft = v }

// GetPendingDraftForTest exposes m.pendingDraft so tests can assert
// /input cleared the snapshot (or that handleTurnOutcome captured one
// before opening the picker).
func GetPendingDraftForTest(m RootModel) string { return m.pendingDraft }

// HandleSlashCommandForTest routes a slash command through the
// production dispatcher. Slash commands are normally handled inside
// submitInput, but tests targeting the /input restore path want the
// dispatch in isolation (no prompt-echo / no Update plumbing).
func HandleSlashCommandForTest(m RootModel, cmd string) (RootModel, tea.Cmd) {
	updated, tcmd := m.handleSlashCommand(cmd)
	rm, _ := updated.(RootModel)
	return rm, tcmd
}

// SetOpenArtifactForTest installs a recording opener seam so /open tests
// observe which absolute path the command resolved and opened without
// launching a real OS opener / $EDITOR.
func SetOpenArtifactForTest(m *RootModel, fn func(path string) error) {
	m.openArtifact = fn
}

// ChoiceWidgetIsActive reports whether the inline choice widget owns
// focus. Mirrors the production check at handleTurnOutcome's
// auto-focus site.
func ChoiceWidgetIsActive(m RootModel) bool { return m.choice.IsActive() }

// ViewWithoutChoiceForTest exposes the package-private helper that
// strips choice elements from a typed View. The regression test pins
// its shallow-copy semantics so future tweaks can't quietly mutate the
// caller's view.
func ViewWithoutChoiceForTest(v *app.View) *app.View { return viewWithoutChoice(v) }

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

// ── Choice widget test helpers ───────────────────────────────────────────────

// TestChoiceWidget wraps a choiceWidgetModel for unit tests that need
// to drive the inline picker without spinning up a full RootModel.
// Tests in the tui_test package use this rather than poking at
// unexported fields directly.
type TestChoiceWidget struct {
	m *choiceWidgetModel
}

// NewTestChoiceWidget creates a new, inactive choice widget.
func NewTestChoiceWidget() *TestChoiceWidget {
	m := newChoiceWidgetModel()
	return &TestChoiceWidget{m: &m}
}

// OpenChoice initialises the widget from a typed ViewElement. World is
// a map exposed to expr-lang's `world.*` namespace for When-guard
// evaluation; pass nil when no When filtering is needed.
func (w *TestChoiceWidget) OpenChoice(el app.ViewElement, world map[string]any) error {
	env := expr.Env{World: world}
	return w.m.Open(el, env, nil)
}

// IsActive reports widget activity for assertions.
func (w *TestChoiceWidget) IsActive() bool { return w.m.IsActive() }

// View renders the widget at the given width.
func (w *TestChoiceWidget) View(width int) string { return w.m.View(width) }

// SendKey forwards a tea.KeyMsg through the widget's Update path and
// returns the resulting commit (nil when the widget stays open).
func (w *TestChoiceWidget) SendKey(msg tea.KeyMsg) *ChoiceCommit {
	next, _, commit := w.m.Update(msg)
	*w.m = next
	return commit
}

// SendRune is a convenience that wraps a single rune as a tea.KeyMsg
// of type KeyRunes and forwards through SendKey.
func (w *TestChoiceWidget) SendRune(r rune) *ChoiceCommit {
	return w.SendKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
}

// FieldBuffers returns a snapshot of the form-mode editable buffers
// (one per field in field order).
func (w *TestChoiceWidget) FieldBuffers() []string {
	out := make([]string, len(w.m.fieldBuffers))
	copy(out, w.m.fieldBuffers)
	return out
}

// Cursor returns the current selection cursor (single / multi modes).
func (w *TestChoiceWidget) Cursor() int { return w.m.cursor }

// FieldCursor returns the current focused field index (form mode).
func (w *TestChoiceWidget) FieldCursor() int { return w.m.fieldCursor }

// ParamMode reports whether the widget is in param-capture mode.
func (w *TestChoiceWidget) ParamMode() bool { return w.m.paramMode }

// ParamBuf returns the current param-mode input buffer. Used by tests
// that need to confirm whether enum params seed with Values[0] and
// whether printable letters mutate the buffer.
func (w *TestChoiceWidget) ParamBuf() string { return w.m.paramBuf }

// ItemCount returns the number of items visible after When filtering.
func (w *TestChoiceWidget) ItemCount() int { return len(w.m.items) }

// ErrMsg returns the transient error footer text.
func (w *TestChoiceWidget) ErrMsg() string { return w.m.errMsg }

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
// SendResult.ReloadRequested path without running a real agent.
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

// SessionIDForTest returns the app session id owned by the model.
func (m RootModel) SessionIDForTest() app.SessionID { return m.sid }

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

// LiveLineForTest exposes the current in-flight live line that View()
// renders just above the prompt. Empty when nothing live is showing.
// Used by inline-routing tests that previously asserted on the live
// entry's body — the entry no longer lives in m.entries, only in
// m.liveLine.
func LiveLineForTest(m RootModel) string { return m.transcript.LiveLine() }

// AppendLiveForTest exposes the production live-line path for tests that need
// to simulate routing/awaiting chrome without driving a full turn.
func AppendLiveForTest(m *RootModel, body string) {
	m.transcript.AppendLive(body)
}

// AppendMetaThinkingForTest exposes the narration-line append.
func AppendMetaThinkingForTest(m *RootModel, text string) {
	m.transcript.AppendMetaThinking(text)
}

// AppendMetaToolUseForTest exposes the tool-use breadcrumb append.
func AppendMetaToolUseForTest(m *RootModel, tool, args string) {
	m.transcript.AppendMetaToolUse(tool, args)
}

// AppendSystemForTest drives the production AppendSystem path so
// regression tests can exercise the assistant-replay rendering site.
// (Renders the input through Glamour, queues to scrollback pending.)
func AppendSystemForTest(m *RootModel, body string) {
	m.transcript.AppendSystem(body)
}

// QueueAgentBodyForTest pushes a rendered agent body through the
// AppendAgentBody path and returns the new pending queue. Used by
// chrome regression tests that need to validate what scrollback
// content the live TUI would emit via tea.Println.
func QueueAgentBodyForTest(m *RootModel, view string) []string {
	m.transcript.AppendAgentBody(view)
	out := make([]string, len(m.transcript.pending))
	copy(out, m.transcript.pending)
	return out
}

// ClearTranscriptPendingForTest empties the pending queue without
// running FlushPending. Used to isolate per-test scrollback contents.
func ClearTranscriptPendingForTest(m *RootModel) {
	m.transcript.pending = nil
}

// PendingTranscriptForTest exposes the transcript's pending-print
// queue so tests can assert what will land in scrollback on the
// next FlushPending — used by the welcome-block test that needs to
// see content queued in NewRootModel before any Update fires.
func PendingTranscriptForTest(m RootModel) []string {
	out := make([]string, len(m.transcript.pending))
	copy(out, m.transcript.pending)
	return out
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

// GetTranscriptLiveLine returns the current in-flight liveLine from the transcript.
func GetTranscriptLiveLine(m *transcriptModel) string {
	return m.liveLine
}

// GetTranscriptPending returns a copy of the pending queue from the transcript.
func GetTranscriptPending(m *transcriptModel) []string {
	pending := make([]string, len(m.pending))
	copy(pending, m.pending)
	return pending
}

// NewTranscriptModel creates a transcriptModel for testing.
func NewTranscriptModel(width int, appDef app.AppDef) *transcriptModel {
	tm := newTranscriptModel(width, 10) // 10-line viewport for testing
	tm.entries = []transcriptEntry{}    // ensure clean state
	tm.pending = []string{}
	return &tm
}
