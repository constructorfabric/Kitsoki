package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"hally/internal/intent"
)

// disambiguationModel handles the §7.4 disambiguation UI.
// When the orchestrator returns AMBIGUOUS_INTENT candidates, this model
// renders a numbered menu of candidates. The user presses 1/2/3 to pick,
// or Esc to cancel.
type disambiguationModel struct {
	active     bool
	candidates []intent.Candidate
	// chosenIdx is set when the user picks; -1 means no pick yet.
	chosenIdx int
}

func newDisambiguationModel() disambiguationModel {
	return disambiguationModel{chosenIdx: -1}
}

// Open activates the disambiguation model with the given candidates.
func (m *disambiguationModel) Open(candidates []intent.Candidate) {
	m.active = true
	m.candidates = candidates
	m.chosenIdx = -1
}

// Close deactivates the model.
func (m *disambiguationModel) Close() {
	m.active = false
	m.candidates = nil
	m.chosenIdx = -1
}

// disambiguationChoiceMsg is sent when the user picks a candidate.
type disambiguationChoiceMsg struct {
	chosen intent.Candidate
}

func (m disambiguationModel) Init() tea.Cmd { return nil }

func (m disambiguationModel) Update(msg tea.Msg) (disambiguationModel, tea.Cmd) {
	if !m.active {
		return m, nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			m.active = false
			return m, nil
		}
		// Numeric key picks a candidate.
		for i := 1; i <= len(m.candidates) && i <= 9; i++ {
			if msg.String() == fmt.Sprintf("%d", i) {
				chosen := m.candidates[i-1]
				m.active = false
				m.chosenIdx = i - 1
				return m, func() tea.Msg {
					return disambiguationChoiceMsg{chosen: chosen}
				}
			}
		}
	}
	return m, nil
}

// View renders the disambiguation menu.
func (m disambiguationModel) View() string {
	if !m.active || len(m.candidates) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("Did you mean? (press number to pick, Esc to cancel)\n\n")
	for i, c := range m.candidates {
		if i >= 9 {
			break
		}
		title := c.Title
		if title == "" {
			title = c.Intent
		}
		desc := c.Why
		if desc == "" {
			desc = c.Description
		}
		sb.WriteString(fmt.Sprintf("  [%d] %s", i+1, title))
		if desc != "" {
			sb.WriteString(" — " + truncateStr(desc, 60))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// truncateStr truncates s to at most n runes.
func truncateStr(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}
