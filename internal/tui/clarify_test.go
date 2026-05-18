package tui_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/intent"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/tui"
)

// TestClarifyEnumSlotInlineBlock verifies that a slot with enum values
// renders an inline numbered choice list (§7.3 Sub-mode A, Phase 2
// inline overlay). The legacy huh.Select sub-model is gone; enum/bool
// slots are now presented as a numbered list and the user picks via the
// normal prompt by typing a number or the canonical value.
func TestClarifyEnumSlotInlineBlock(t *testing.T) {
	enumSlot := orchestrator.SlotNeed{
		Name:   "direction",
		Type:   "enum",
		Values: []string{"north", "south", "east", "west"},
		Prompt: "Which direction?",
	}

	m := tui.NewTestClarifyModel()
	m.Open("go", []orchestrator.SlotNeed{enumSlot}, nil)
	require.True(t, m.IsActive(), "clarify model should be active after Open")

	block := m.InlineBlock()
	t.Logf("inline clarification block:\n%s", block)

	// Header mentions the intent name and the slot position.
	require.Contains(t, block, "go", "block should mention the intent name")
	require.Contains(t, block, "1/1", "block should show the slot position")
	// Prompt text appears.
	require.Contains(t, block, "Which direction?", "block should show the slot prompt")
	// Each enum value appears as a numbered entry.
	require.Contains(t, block, "1. north")
	require.Contains(t, block, "2. south")
	require.Contains(t, block, "3. east")
	require.Contains(t, block, "4. west")
	// Hint mentions both pick methods.
	require.True(t, strings.Contains(block, "number") || strings.Contains(block, "value"),
		"hint should mention picking by number or by value")
}

// TestClarifyEnumSlotSubmitByNumber verifies that the user can pick an
// enum slot value by typing the 1-based index into the normal prompt.
func TestClarifyEnumSlotSubmitByNumber(t *testing.T) {
	enumSlot := orchestrator.SlotNeed{
		Name:   "direction",
		Type:   "enum",
		Values: []string{"north", "south", "east", "west"},
	}
	m := tui.NewTestClarifyModel()
	m.Open("go", []orchestrator.SlotNeed{enumSlot}, nil)

	value, done, collected, err := m.SubmitValue("2")
	require.NoError(t, err)
	require.True(t, done, "single-slot fill should be done after one pick")
	require.Equal(t, "south", value)
	require.Equal(t, map[string]any{"direction": "south"}, collected)
}

// TestClarifyEnumSlotSubmitByValue verifies that the user can pick an
// enum slot value by typing the canonical name (case-insensitive).
func TestClarifyEnumSlotSubmitByValue(t *testing.T) {
	enumSlot := orchestrator.SlotNeed{
		Name:   "direction",
		Type:   "enum",
		Values: []string{"north", "south"},
	}
	m := tui.NewTestClarifyModel()
	m.Open("go", []orchestrator.SlotNeed{enumSlot}, nil)

	value, done, _, err := m.SubmitValue("NORTH")
	require.NoError(t, err)
	require.True(t, done)
	require.Equal(t, "north", value, "match should normalise to the canonical value")
}

// TestClarifyEnumSlotInvalidPickKeepsActive verifies that an out-of-range
// number or unknown value is rejected, leaving the model active so the
// caller can re-render the same slot's choice block.
func TestClarifyEnumSlotInvalidPickKeepsActive(t *testing.T) {
	enumSlot := orchestrator.SlotNeed{
		Name:   "direction",
		Type:   "enum",
		Values: []string{"north", "south"},
	}
	m := tui.NewTestClarifyModel()
	m.Open("go", []orchestrator.SlotNeed{enumSlot}, nil)

	_, done, _, err := m.SubmitValue("9")
	require.Error(t, err, "out-of-range index should fail")
	require.False(t, done)
	require.True(t, m.IsActive(), "model stays active after invalid pick")
	require.Equal(t, "direction", m.CurrentSlotName(), "current slot is unchanged")

	_, _, _, err = m.SubmitValue("up")
	require.Error(t, err, "unknown value should fail")
}

// TestClarifyFreeFormSlotInlineBlock verifies that a free-form string
// slot's inline block shows the prompt, any examples / hints, and the
// expected typing hint (no numbered list).
func TestClarifyFreeFormSlotInlineBlock(t *testing.T) {
	freeSlot := orchestrator.SlotNeed{
		Name:     "query",
		Type:     "string",
		Prompt:   "What are you looking for?",
		Examples: []string{"cats", "dogs"},
	}
	m := tui.NewTestClarifyModel()
	m.Open("search", []orchestrator.SlotNeed{freeSlot}, nil)

	block := m.InlineBlock()
	t.Logf("inline clarification block:\n%s", block)

	require.Contains(t, block, "search")
	require.Contains(t, block, "What are you looking for?")
	require.Contains(t, block, "cats")
	require.Contains(t, block, "dogs")
	// No numbered choice list — free-form slots show only the prompt.
	require.NotContains(t, block, "1. ", "free-form slot must not render a choice list")
}

// TestClarifyFreeFormSlotSubmit verifies that any non-empty string is
// accepted for a free-form slot.
func TestClarifyFreeFormSlotSubmit(t *testing.T) {
	freeSlot := orchestrator.SlotNeed{
		Name: "query",
		Type: "string",
	}
	m := tui.NewTestClarifyModel()
	m.Open("search", []orchestrator.SlotNeed{freeSlot}, nil)

	value, done, collected, err := m.SubmitValue("kittens please")
	require.NoError(t, err)
	require.True(t, done)
	require.Equal(t, "kittens please", value)
	require.Equal(t, map[string]any{"query": "kittens please"}, collected)
}

// TestClarifyBoolSlotInlineBlock verifies that a bool slot renders a
// numbered choice list with true/false values (§7.3 Sub-mode A).
func TestClarifyBoolSlotInlineBlock(t *testing.T) {
	boolSlot := orchestrator.SlotNeed{
		Name:   "confirmed",
		Type:   "bool",
		Prompt: "Are you sure?",
	}
	m := tui.NewTestClarifyModel()
	m.Open("confirm", []orchestrator.SlotNeed{boolSlot}, nil)

	block := m.InlineBlock()
	t.Logf("inline clarification block:\n%s", block)

	require.Contains(t, block, "confirm")
	require.Contains(t, block, "Are you sure?")
	require.Contains(t, block, "1. true")
	require.Contains(t, block, "2. false")

	// Submit by value.
	value, done, _, err := m.SubmitValue("true")
	require.NoError(t, err)
	require.True(t, done)
	require.Equal(t, "true", value)
}

// TestClarifyMultiSlotAdvances verifies that filling one slot advances
// to the next and that the model only signals done once every slot has
// a value.
func TestClarifyMultiSlotAdvances(t *testing.T) {
	slots := []orchestrator.SlotNeed{
		{Name: "direction", Type: "enum", Values: []string{"north", "south"}},
		{Name: "speed", Type: "string"},
	}
	m := tui.NewTestClarifyModel()
	m.Open("go", slots, nil)

	require.Equal(t, "direction", m.CurrentSlotName())
	_, done, _, err := m.SubmitValue("north")
	require.NoError(t, err)
	require.False(t, done, "still one slot remaining")
	require.Equal(t, "speed", m.CurrentSlotName(), "model advanced to next slot")

	value, done, collected, err := m.SubmitValue("fast")
	require.NoError(t, err)
	require.True(t, done)
	require.Equal(t, "fast", value)
	require.Equal(t, map[string]any{"direction": "north", "speed": "fast"}, collected)
}

// TestDisambiguationInlineBlock verifies that the disambiguation block
// renders a numbered candidate list with titles and rationales.
func TestDisambiguationInlineBlock(t *testing.T) {
	m := tui.NewTestDisambiguationModel()
	require.False(t, m.IsActive(), "should be inactive initially")
	require.Equal(t, "", m.InlineBlock(), "inactive block is empty")

	m.Open([]intent.Candidate{
		{Intent: "go_north", Title: "Go north", Why: "head toward the mountains"},
		{Intent: "go_south", Title: "Go south", Why: "head toward the river"},
	})
	require.True(t, m.IsActive())

	block := m.InlineBlock()
	t.Logf("inline disambig block:\n%s", block)
	require.Contains(t, block, "did you mean")
	require.Contains(t, block, "1. Go north")
	require.Contains(t, block, "2. Go south")
	require.Contains(t, block, "head toward the mountains")
	require.Contains(t, block, "head toward the river")
}

// TestDisambiguationSubmitByNumber verifies that the user can pick a
// candidate by typing the 1-based number into the normal prompt.
func TestDisambiguationSubmitByNumber(t *testing.T) {
	m := tui.NewTestDisambiguationModel()
	m.Open([]intent.Candidate{
		{Intent: "go_north"},
		{Intent: "go_south"},
	})

	chosen, err := m.SubmitValue("2")
	require.NoError(t, err)
	require.Equal(t, "go_south", chosen.Intent)
}

// TestDisambiguationSubmitByIntentName verifies that the user can pick a
// candidate by typing the canonical intent name (case-insensitive).
func TestDisambiguationSubmitByIntentName(t *testing.T) {
	m := tui.NewTestDisambiguationModel()
	m.Open([]intent.Candidate{
		{Intent: "go_north", Title: "Go north"},
		{Intent: "go_south", Title: "Go south"},
	})

	chosen, err := m.SubmitValue("GO_NORTH")
	require.NoError(t, err)
	require.Equal(t, "go_north", chosen.Intent)

	// Title also matches.
	chosen, err = m.SubmitValue("Go south")
	require.NoError(t, err)
	require.Equal(t, "go_south", chosen.Intent)
}

// TestDisambiguationInvalidPick verifies that an unknown pick returns an
// error so the caller can re-render the candidate list.
func TestDisambiguationInvalidPick(t *testing.T) {
	m := tui.NewTestDisambiguationModel()
	m.Open([]intent.Candidate{
		{Intent: "go_north"},
		{Intent: "go_south"},
	})

	_, err := m.SubmitValue("9")
	require.Error(t, err)
	_, err = m.SubmitValue("teleport")
	require.Error(t, err)
}
