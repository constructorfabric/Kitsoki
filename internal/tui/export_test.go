// export_test.go — white-box helpers for tui package tests.
// Compiled only during `go test` (package tui, not tui_test).
package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"kitsoki/internal/clock"
	"kitsoki/internal/jobs"
	"kitsoki/internal/orchestrator"
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

// MouseOn reports whether the TUI currently has mouse capture enabled.
func MouseOn(m RootModel) bool { return m.mouseOn }

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

// View returns the rendered view.
func (w *TestClarifyModelWrapper) View() string {
	return w.m.View()
}

// IsUsingHuhForm returns true if the current slot is using a huh form (sub-mode A).
func (w *TestClarifyModelWrapper) IsUsingHuhForm() bool {
	return w.m.huhForm != nil
}

// ── System menu test helpers ──────────────────────────────────────────────────

// MenuSystemActive reports whether the Esc-triggered system menu overlay is open.
func MenuSystemActive(m RootModel) bool { return m.menuSystem.IsActive() }

// MenuSystemView returns the rendered overlay (empty when inactive).
func MenuSystemView(m RootModel) string { return m.menuSystem.View() }

// SetPromptValue sets the prompt input value for tests that need to start
// with pre-filled text (e.g. Ctrl+C clears the prompt).
func SetPromptValue(m *RootModel, v string) { m.prompt.SetValue(v) }

// GetPromptValue returns the current prompt input value.
func GetPromptValue(m RootModel) string { return m.prompt.Value() }

// ── Edit-mode test helpers ────────────────────────────────────────────────────

// EditPhaseInput / Thinking / Review / Applying re-export the unexported
// editPhase constants for use by tui_test code.
const (
	EditPhaseInput     = int(editPhaseInput)
	EditPhaseThinking  = int(editPhaseThinking)
	EditPhaseReview    = int(editPhaseReview)
	EditPhaseApplying  = int(editPhaseApplying)
)

// EditPhase returns the current edit-overlay phase as an int (cast to
// EditPhase* constants for assertions).
func EditPhase(m RootModel) int { return int(m.edit.phase) }

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

// View returns the rendered view.
func (w *TestDisambiguationModelWrapper) View() string {
	return w.m.View()
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
