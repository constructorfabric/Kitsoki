// export_test.go — white-box helpers for tui package tests.
// Compiled only during `go test` (package tui, not tui_test).
package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"hally/internal/orchestrator"
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

// StartLLMTurnForTest calls the internal startLLMTurn function so tests can
// exercise the full async path without going through submitInput.
func StartLLMTurnForTest(m RootModel, input string) (RootModel, tea.Cmd) {
	return startLLMTurn(m, input)
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
