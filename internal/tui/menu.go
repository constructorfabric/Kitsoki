package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"kitsoki/internal/orchestrator"
)

// menuModel renders the §7.2 available-actions menu (primary + blocked intents).
//
// The primary list scroll-follows the selection: when the list of items is
// taller than the pane, View() renders a sliding window around the selected
// index and surfaces `↑ N more` / `↓ N more` muted affordances at the edges.
// Numeric 1–9 quick-select stays absolute (items[i] is always labelled "i+1.")
// so muscle memory survives scrolling.
type menuModel struct {
	items    []orchestrator.MenuEntry
	blocked  []orchestrator.MenuEntry
	selected int
	width    int
	height   int
	// topIdx is the first primary-item index rendered in the scroll
	// window. Maintained in Update so View() is a pure read.
	topIdx int
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
		// Clamp scroll on resize — don't snap to top, the user's
		// selection should stay visible across a window resize.
		m.topIdx = m.scrollToSelected(m.topIdx, m.primaryCapacity())
	case menuItemsChanged:
		// Reset selection + scroll only when the items slice has
		// actually changed shape. The orchestrator rebuilds the menu
		// every turn even when the available actions are identical;
		// snapping to top on each rebuild would yank the user's
		// position. Preserve scroll when items are the same.
		shapeChanged := !sameMenuItems(m.items, msg.items)
		m.items = msg.items
		m.blocked = msg.blocked
		if shapeChanged {
			m.selected = 0
			m.topIdx = 0
		} else {
			if m.selected >= len(m.items) {
				m.selected = max(0, len(m.items)-1)
			}
			m.topIdx = m.scrollToSelected(m.topIdx, m.primaryCapacity())
		}
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
			// Numeric quick-select was removed in phase 4 — numbers
			// are normal text in the prompt now (proposal §"Input
			// fixes"). The menu sub-model no longer needs to handle
			// "1".."9" because it isn't rendered after phase 3.
		}
		m.topIdx = m.scrollToSelected(m.topIdx, m.primaryCapacity())
	}
	return m, nil
}

// scrollToSelected adjusts topIdx so the selected item is visible in a
// window of `capacity` items starting at topIdx.
func (m menuModel) scrollToSelected(topIdx, capacity int) int {
	if capacity < 1 {
		capacity = 1
	}
	if m.selected < topIdx {
		topIdx = m.selected
	}
	if m.selected >= topIdx+capacity {
		topIdx = m.selected - capacity + 1
	}
	if topIdx < 0 {
		topIdx = 0
	}
	maxTop := len(m.items) - capacity
	if maxTop < 0 {
		maxTop = 0
	}
	if topIdx > maxTop {
		topIdx = maxTop
	}
	return topIdx
}

// primaryCapacity is how many primary rows fit after the panel chrome
// (border + "Actions" header + rule + blocked block + scroll indicators)
// is subtracted from m.height. Conservatively reserves space for both
// scroll indicators so the layout stays stable as the user scrolls.
func (m menuModel) primaryCapacity() int {
	// 2 border lines + 1 "Actions" header + 1 separator rule.
	chrome := 4
	if len(m.blocked) > 0 {
		chrome += 1 + len(m.blocked) // blank line + one row per blocked entry
	}
	// Reserve one line each for "↑ N more" / "↓ N more" — drawn only
	// when needed but accounted for unconditionally so capacity is
	// stable as the window scrolls and items don't reflow.
	chrome += 2
	c := m.height - chrome
	if c < 1 {
		c = 1
	}
	return c
}

// sameMenuItems reports whether two MenuEntry slices represent the same
// available-actions set. Identity is by length + ordered intent names;
// destination hints and reasons are derived state and don't gate reset.
func sameMenuItems(a, b []orchestrator.MenuEntry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Intent != b[i].Intent {
			return false
		}
	}
	return true
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

	capacity := m.primaryCapacity()
	topIdx := m.scrollToSelected(m.topIdx, capacity)
	endIdx := topIdx + capacity
	if endIdx > len(m.items) {
		endIdx = len(m.items)
	}
	hiddenAbove := topIdx
	hiddenBelow := len(m.items) - endIdx
	mutedStyle := lipgloss.NewStyle().Foreground(colorMuted)

	if hiddenAbove > 0 {
		sb.WriteString(mutedStyle.Render(fmt.Sprintf("  ↑ %d more", hiddenAbove)))
		sb.WriteString("\n")
	} else {
		// Blank spacer so the first item's vertical position is stable
		// regardless of scroll state.
		sb.WriteString("\n")
	}

	// Primary items — only the visible window. Numeric labels stay
	// absolute (items[i] is always "i+1.") so 1–9 quick-select keeps
	// working when the list is scrolled.
	for i := topIdx; i < endIdx; i++ {
		item := m.items[i]
		var prefix string
		if i < 9 {
			prefix = fmt.Sprintf("%d. ", i+1)
		} else {
			prefix = "   "
		}
		label := item.Display
		var line string
		if item.DestinationHint != "" {
			line = prefix + label + mutedStyle.Render(" → "+item.DestinationHint)
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

	if hiddenBelow > 0 {
		sb.WriteString(mutedStyle.Render(fmt.Sprintf("  ↓ %d more", hiddenBelow)))
		sb.WriteString("\n")
	} else {
		sb.WriteString("\n")
	}

	// Blocked items — declared in this state but guard currently fails.
	// Rendered inline (not collapsed) so the player sees both the action
	// they wanted and why it's not available yet.
	if len(m.blocked) > 0 {
		sb.WriteString("\n")
		for _, item := range m.blocked {
			line := "  ✗ " + item.Display
			if item.Reason != "" {
				line += " — " + item.Reason
			}
			sb.WriteString(menuItemBlockedStyle.Render(line))
			sb.WriteString("\n")
		}
	}

	w := m.width - 2
	if w < 10 {
		w = 10
	}

	return menuStyle.Width(w).Height(m.height).MaxHeight(m.height).Render(sb.String())
}

// menuItemsChanged is a message to update the menu items.
type menuItemsChanged struct {
	items   []orchestrator.MenuEntry
	blocked []orchestrator.MenuEntry
}
