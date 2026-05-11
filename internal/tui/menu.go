package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"kitsoki/internal/orchestrator"
)

// menuModel renders the §7.2 available-actions menu (primary + blocked intents).
type menuModel struct {
	items    []orchestrator.MenuEntry
	blocked  []orchestrator.MenuEntry
	selected int
	width    int
	height   int
}

func newMenuModel(width, height int) menuModel {
	return menuModel{
		width:  width,
		height: height,
	}
}

func (m menuModel) Init() tea.Cmd { return nil }

func (m menuModel) Update(msg tea.Msg) (menuModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case menuItemsChanged:
		m.items = msg.items
		m.blocked = msg.blocked
		m.selected = 0
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if m.selected > 0 {
				m.selected--
			}
		case "down", "j":
			if m.selected < len(m.items)-1 {
				m.selected++
			}
		case "1", "2", "3", "4", "5", "6", "7", "8", "9":
			idx := int(msg.String()[0]-'0') - 1
			if idx >= 0 && idx < len(m.items) {
				m.selected = idx
			}
		}
	}
	return m, nil
}

// SelectedEntry returns the currently highlighted MenuEntry, or nil.
func (m menuModel) SelectedEntry() *orchestrator.MenuEntry {
	if m.selected >= 0 && m.selected < len(m.items) {
		e := m.items[m.selected]
		return &e
	}
	return nil
}

// SelectedIntent returns the currently highlighted intent name, or "".
// Kept for backward compat; prefer SelectedEntry.
func (m menuModel) SelectedIntent() string {
	if e := m.SelectedEntry(); e != nil {
		return e.Intent
	}
	return ""
}

// SelectedExample returns the Display string for the selected row, or "".
func (m menuModel) SelectedExample() string {
	if e := m.SelectedEntry(); e != nil {
		return e.Display
	}
	return ""
}

func (m menuModel) View() string {
	var sb strings.Builder
	sb.WriteString(lipgloss.NewStyle().Bold(true).Render("Actions"))
	sb.WriteString("\n")
	sb.WriteString(strings.Repeat("─", max(m.width-4, 4)))
	sb.WriteString("\n")

	// Primary items.
	for i, item := range m.items {
		var prefix string
		if i < 9 {
			prefix = fmt.Sprintf("%d. ", i+1)
		} else {
			prefix = "   "
		}
		label := item.Display
		var line string
		if item.DestinationHint != "" {
			line = prefix + label + lipgloss.NewStyle().Foreground(colorMuted).Render(
				" → "+item.DestinationHint,
			)
		} else {
			line = prefix + label
		}

		if i == m.selected {
			sb.WriteString(menuItemSelectedStyle.Render(line))
		} else {
			sb.WriteString(menuItemStyle.Render(line))
		}
		sb.WriteString("\n")
	}

	// Blocked items (collapsed summary + expanded list).
	if len(m.blocked) > 0 {
		sb.WriteString("\n")
		sb.WriteString(menuItemBlockedStyle.Render(fmt.Sprintf("+%d blocked", len(m.blocked))))
		sb.WriteString("\n")
		for _, item := range m.blocked {
			line := "  ✗ " + item.Display
			if item.Reason != "" {
				line += lipgloss.NewStyle().Foreground(colorMuted).Render(
					" — " + item.Reason,
				)
			}
			sb.WriteString(menuItemBlockedStyle.Render(line))
			sb.WriteString("\n")
		}
	}

	w := m.width - 2
	if w < 10 {
		w = 10
	}

	return menuStyle.Width(w).Height(m.height).Render(sb.String())
}

// menuItemsChanged is a message to update the menu items.
type menuItemsChanged struct {
	items   []orchestrator.MenuEntry
	blocked []orchestrator.MenuEntry
}
