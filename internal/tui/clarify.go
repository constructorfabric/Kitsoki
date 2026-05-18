package tui

import (
	"fmt"
	"strconv"
	"strings"

	"kitsoki/internal/orchestrator"
	"kitsoki/internal/tui/blocks"
)

// clarifyModel tracks the state of an in-progress slot-fill clarification
// (§7.3). It no longer owns the prompt area — the user types values into
// the normal textarea with the `?` prefix, and the inline "Clarification
// needed" block is rendered into the transcript. The legacy huh.Select /
// embedded textinput rendering paths have been removed; enum / bool slots
// are presented as numbered choice lists (pick by number or by canonical
// value) and free-form slots accept whatever the user types.
type clarifyModel struct {
	active     bool
	slots      []orchestrator.SlotNeed
	current    int
	collected  map[string]any
	intentName string
}

func newClarifyModel() clarifyModel {
	return clarifyModel{
		collected: make(map[string]any),
	}
}

// isEnumSlot returns true if the slot should render as a numbered choice
// list (Sub-mode A per §7.3). Free-form slots return false.
func isEnumSlot(slot orchestrator.SlotNeed) bool {
	return len(slot.Values) > 0 || slot.Type == "bool"
}

// Open activates the clarify model with the given slot needs.
func (m *clarifyModel) Open(intentName string, slots []orchestrator.SlotNeed, existingSlots map[string]any) {
	m.active = true
	m.intentName = intentName
	m.slots = slots
	m.current = 0
	m.collected = make(map[string]any)
	for k, v := range existingSlots {
		m.collected[k] = v
	}
}

// Close deactivates the clarify model.
func (m *clarifyModel) Close() {
	m.active = false
	m.slots = nil
	m.current = 0
	m.collected = make(map[string]any)
	m.intentName = ""
}

// CurrentSlot returns the slot the user is currently being asked about,
// or (zero, false) if the model is inactive or all slots are filled.
func (m *clarifyModel) CurrentSlot() (orchestrator.SlotNeed, bool) {
	if !m.active || m.current >= len(m.slots) {
		return orchestrator.SlotNeed{}, false
	}
	return m.slots[m.current], true
}

// RenderInlineBlock returns the styled "Clarification needed" transcript
// block for the current slot, ready to pass to transcript.AppendBlock.
// Returns empty when no current slot is active.
func (m *clarifyModel) RenderInlineBlock(r *blocks.Renderer) string {
	slot, ok := m.CurrentSlot()
	if !ok {
		return ""
	}
	return r.Clarification(m.intentName, m.current, len(m.slots), blocks.ClarificationSlot{
		Name:        slot.Name,
		Prompt:      slot.Prompt,
		Description: slot.Description,
		Type:        slot.Type,
		Values:      slot.Values,
		FormatHint:  slot.FormatHint,
		Examples:    slot.Examples,
	})
}

// SubmitValue accepts a typed value for the current slot. For enum/bool
// slots it accepts either a 1-based index ("2") or the canonical value
// ("south"); invalid picks return an error so the caller can surface a
// hint in the transcript and leave the clarify model intact for retry.
// For free-form slots any non-empty value is accepted. When the final
// slot is filled the collected map is returned alongside done=true and
// the model is closed.
func (m *clarifyModel) SubmitValue(input string) (value string, done bool, collected map[string]any, err error) {
	if !m.active || m.current >= len(m.slots) {
		return "", false, nil, fmt.Errorf("clarify: no active slot")
	}
	input = strings.TrimSpace(input)
	if input == "" {
		return "", false, nil, fmt.Errorf("clarify: empty value")
	}
	slot := m.slots[m.current]

	// Determine the choice list (enum values, or true/false for bool).
	values := slot.Values
	if slot.Type == "bool" && len(values) == 0 {
		values = []string{"true", "false"}
	}

	if len(values) > 0 {
		// Numbered pick or canonical value.
		if n, perr := strconv.Atoi(input); perr == nil {
			if n < 1 || n > len(values) {
				return "", false, nil, fmt.Errorf("clarify: choice %d out of range (1..%d)", n, len(values))
			}
			value = values[n-1]
		} else {
			matched := ""
			for _, v := range values {
				if strings.EqualFold(v, input) {
					matched = v
					break
				}
			}
			if matched == "" {
				return "", false, nil, fmt.Errorf("clarify: %q is not a valid choice (expected one of: %s)",
					input, strings.Join(values, ", "))
			}
			value = matched
		}
	} else {
		// Free-form — accept verbatim.
		value = input
	}

	m.collected[slot.Name] = value
	m.current++
	if m.current >= len(m.slots) {
		out := m.collected
		// Don't reset state here; the caller invokes Close() after it
		// receives the supplementSlotsMsg / collected map.
		return value, true, out, nil
	}
	return value, false, nil, nil
}

// supplementSlotsMsg is sent when the user has filled all missing slots.
type supplementSlotsMsg struct {
	slots map[string]any
}
