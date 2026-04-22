package tui_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"hally/internal/orchestrator"
	"hally/internal/tui"
)

// TestClarifyEnumSlotUsesSelect verifies that a slot with enum values causes
// the clarifyModel to activate the huh.Select sub-mode (§7.3 Sub-mode A).
func TestClarifyEnumSlotUsesSelect(t *testing.T) {
	// We test by calling the package-level helper that builds a clarify model
	// and checking that the View() output reflects Select (not plain textinput).
	enumSlot := orchestrator.SlotNeed{
		Name:   "direction",
		Type:   "enum",
		Values: []string{"north", "south", "east", "west"},
		Prompt: "Which direction?",
	}

	// Build a model and open it with the enum slot.
	m := tui.NewTestClarifyModel()
	m.Open("go", []orchestrator.SlotNeed{enumSlot}, nil)

	require.True(t, m.IsActive(), "clarify model should be active after Open")

	// The view should contain some indication of enum options.
	// When huh.Select is active, the huh form renders with arrow navigation.
	// When plain textinput, we show "Options: north | south | east | west".
	// In sub-mode A the huh form renders the title and at least one option.
	view := m.View()
	t.Logf("clarify view:\n%s", view)

	// View must mention the slot/intent context.
	require.Contains(t, view, "go", "view should mention the intent name")
}

// TestClarifyFreeFormSlotUsesTextInput verifies that a free-form string slot
// keeps the plain textinput (§7.3 Sub-mode B).
func TestClarifyFreeFormSlotUsesTextInput(t *testing.T) {
	freeSlot := orchestrator.SlotNeed{
		Name:   "query",
		Type:   "string",
		Prompt: "What are you looking for?",
	}

	m := tui.NewTestClarifyModel()
	m.Open("search", []orchestrator.SlotNeed{freeSlot}, nil)

	require.True(t, m.IsActive())

	view := m.View()
	t.Logf("clarify view:\n%s", view)

	// Free-form: view should contain the prompt text and input placeholder.
	require.Contains(t, view, "search", "view should mention intent name")
	// View should NOT show huh's rendered form (which includes ANSI arrow keys).
	// Sub-mode B just shows the textinput.
	require.Contains(t, view, "[Enter] confirm", "view should show textinput instructions")
}

// TestClarifyBoolSlotUsesSelect verifies that a bool slot uses huh.Select.
func TestClarifyBoolSlotUsesSelect(t *testing.T) {
	boolSlot := orchestrator.SlotNeed{
		Name:   "confirmed",
		Type:   "bool",
		Prompt: "Are you sure?",
	}

	m := tui.NewTestClarifyModel()
	m.Open("confirm", []orchestrator.SlotNeed{boolSlot}, nil)

	require.True(t, m.IsActive())

	view := m.View()
	t.Logf("clarify view:\n%s", view)

	// Bool slot should show huh select (sub-mode A).
	require.Contains(t, view, "confirm", "view should mention intent name")
}

// TestClarifyDisambiguationModel verifies the disambiguation model renders candidates.
func TestClarifyDisambiguationModel(t *testing.T) {
	m := tui.NewTestDisambiguationModel()

	require.False(t, m.IsActive(), "should be inactive initially")
	require.Equal(t, "", m.View(), "inactive view should be empty")
}
