package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"

	"kitsoki/internal/orchestrator"
)

// clarifyModel handles the §7.3 slot-fill clarification UI.
//
// For enum/bool slots (Sub-mode A per §7.3):
//
//	We build a huh.Select form and host it as a sub-model. The form runs
//	embedded in the BubbleTea model — no separate program is launched.
//
// For free-form string slots (Sub-mode B per §7.3):
//
//	We use a plain textinput.
//
//	TODO(future): replace Sub-mode B textinput with an auxiliary LLM call
//	that generates a natural-language clarification question (§7.3).
type clarifyModel struct {
	active     bool
	slots      []orchestrator.SlotNeed
	current    int
	collected  map[string]any
	intentName string

	// Sub-mode A: huh form for the current enum/bool slot.
	huhForm    *huh.Form
	huhValue   string // bound to huhForm's Select

	// Sub-mode B: textinput for free-form strings.
	input textinput.Model
}

func newClarifyModel() clarifyModel {
	ti := textinput.New()
	ti.Placeholder = "value..."
	return clarifyModel{
		input:     ti,
		collected: make(map[string]any),
	}
}

// isEnumSlot returns true if the slot should use Sub-mode A (huh.Select).
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
	m.activateCurrentSlot()
}

// Close deactivates the clarify model.
func (m *clarifyModel) Close() {
	m.active = false
	m.input.SetValue("")
	m.huhForm = nil
	m.huhValue = ""
}

// activateCurrentSlot prepares the input or huh form for the current slot.
func (m *clarifyModel) activateCurrentSlot() {
	if m.current >= len(m.slots) {
		return
	}
	slot := m.slots[m.current]

	if isEnumSlot(slot) {
		// Sub-mode A: build a huh.Select for this enum/bool slot.
		values := slot.Values
		if slot.Type == "bool" {
			values = []string{"true", "false"}
		}
		m.huhValue = ""
		if len(values) > 0 {
			m.huhValue = values[0]
		}
		opts := make([]huh.Option[string], len(values))
		for i, v := range values {
			opts[i] = huh.NewOption(v, v)
		}

		title := slot.Prompt
		if title == "" {
			title = slot.Name
		}
		sel := huh.NewSelect[string]().
			Title(title).
			Options(opts...).
			Value(&m.huhValue)

		m.huhForm = huh.NewForm(huh.NewGroup(sel))
		m.input.SetValue("")
		_ = m.huhForm.Init()
	} else {
		// Sub-mode B: plain textinput.
		m.huhForm = nil
		m.huhValue = ""
		prompt := slot.Prompt
		if prompt == "" {
			prompt = slot.Name
		}
		m.input.Placeholder = prompt
		m.input.SetValue("")
		m.input.Focus()
	}
}

func (m clarifyModel) Init() tea.Cmd { return nil }

func (m clarifyModel) Update(msg tea.Msg) (clarifyModel, tea.Cmd) {
	if !m.active {
		return m, nil
	}

	// If we have a huh form active, route to it first.
	if m.huhForm != nil {
		switch msg := msg.(type) {
		case tea.KeyMsg:
			if msg.Type == tea.KeyEsc {
				m.active = false
				m.huhForm = nil
				return m, nil
			}
		}

		newModel, cmd := m.huhForm.Update(msg)
		if f, ok := newModel.(*huh.Form); ok {
			m.huhForm = f
			// Check if the form is done.
			if m.huhForm.State == huh.StateCompleted {
				slot := m.slots[m.current]
				m.collected[slot.Name] = m.huhValue
				m.current++
				if m.current >= len(m.slots) {
					collected := m.collected
					m.active = false
					m.huhForm = nil
					return m, func() tea.Msg {
						return supplementSlotsMsg{slots: collected}
					}
				}
				m.activateCurrentSlot()
			} else if m.huhForm.State == huh.StateAborted {
				m.active = false
				m.huhForm = nil
			}
		}
		return m, cmd
	}

	// Sub-mode B: plain textinput.
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyEnter:
			val := strings.TrimSpace(m.input.Value())
			if val == "" {
				return m, nil
			}
			slot := m.slots[m.current]
			m.collected[slot.Name] = val
			m.current++

			if m.current >= len(m.slots) {
				collected := m.collected
				m.active = false
				m.input.SetValue("")
				return m, func() tea.Msg {
					return supplementSlotsMsg{slots: collected}
				}
			}
			m.activateCurrentSlot()

		case tea.KeyEsc:
			m.active = false
			m.input.SetValue("")
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m clarifyModel) View() string {
	if !m.active || m.current >= len(m.slots) {
		return ""
	}

	slot := m.slots[m.current]
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Fill slot for intent '%s' (%d/%d)\n",
		m.intentName, m.current+1, len(m.slots)))

	if m.huhForm != nil {
		// Sub-mode A: delegate to huh form.
		sb.WriteString(m.huhForm.View())
		sb.WriteString("\n[Enter] confirm  [Esc] cancel")
		return sb.String()
	}

	// Sub-mode B: plain textinput view.
	prompt := slot.Prompt
	if prompt == "" {
		prompt = slot.Description
		if prompt == "" {
			prompt = slot.Name
		}
	}
	sb.WriteString(prompt + "\n")

	if len(slot.Values) > 0 {
		sb.WriteString("Options: " + strings.Join(slot.Values, " | ") + "\n")
	}
	if slot.FormatHint != "" {
		sb.WriteString("Hint: " + slot.FormatHint + "\n")
	}
	if len(slot.Examples) > 0 {
		sb.WriteString("Examples: " + strings.Join(slot.Examples, ", ") + "\n")
	}

	sb.WriteString(m.input.View())
	sb.WriteString("\n[Enter] confirm  [Esc] cancel")

	return sb.String()
}

// supplementSlotsMsg is sent when the user has filled all missing slots.
type supplementSlotsMsg struct {
	slots map[string]any
}
