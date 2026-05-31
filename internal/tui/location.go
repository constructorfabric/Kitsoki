package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"kitsoki/internal/orchestrator"
	"kitsoki/internal/tui/blocks"
)

// locationModel renders the location indicator (breadcrumb + state description).
type locationModel struct {
	loc     orchestrator.Location
	offPath bool
	width   int
	// theme is the active blocks theme used to colour the location
	// bar, swapped as the active context's accent changes. Empty
	// falls back to the package-level locationStyle / locationOffPathStyle
	// pair to preserve the pre-rooms behaviour for tests that build a
	// bare locationModel.
	theme string
}

func newLocationModel() locationModel {
	return locationModel{
		loc: orchestrator.Location{OnPath: true},
	}
}

func (m locationModel) Init() tea.Cmd { return nil }

// LocationLine returns a compact one-line summary of the current
// location — breadcrumb + description — without the on-path glyph or
// world dump. Used by the single-pane redesign's two-line footer to
// surface "where you are" on framework line 1.
func (m locationModel) LocationLine() string {
	var parts []string
	if m.loc.Breadcrumb != "" {
		parts = append(parts, m.loc.Breadcrumb)
	}
	if m.loc.StateDescription != "" {
		parts = append(parts, m.loc.StateDescription)
	}
	return strings.Join(parts, " · ")
}

func (m locationModel) Update(msg tea.Msg) (locationModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
	case locationUpdated:
		m.loc = msg.loc
	case offPathToggled:
		m.offPath = msg.on
	}
	return m, nil
}

func (m locationModel) View() string {
	// Single-pane redesign §"Mode visualization": header is one
	// short line. The full world dump, the turn counter, and the
	// relevant-world bracketed bag all moved off — the footer
	// carries ambient state (mode, queue, unread) and /world is the
	// way to inspect the world. ideas.md:56,69 specifically called
	// out the header world dump as "crowding everything"; this is
	// the kill site.
	var parts []string
	if m.offPath {
		parts = append(parts, "○")
	} else {
		parts = append(parts, "●")
	}
	if m.loc.Breadcrumb != "" {
		parts = append(parts, m.loc.Breadcrumb)
	}
	if m.loc.StateDescription != "" {
		parts = append(parts, "—", m.loc.StateDescription)
	}
	line := strings.Join(parts, " ")

	style := locationStyle
	if m.offPath {
		style = locationOffPathStyle
	}
	// Theme-aware override: when the active room declares a theme,
	// repaint the bar with the theme's Primary/Text pair so the
	// colour swap on /meta entry or per-room declaration is visible
	// at a glance. Falls through to the legacy palette when no theme
	// is set (preserves test behaviour for bare locationModel
	// constructions).
	if m.theme != "" && !m.offPath {
		t := blocks.ThemeByName(m.theme)
		style = lipgloss.NewStyle().
			Background(t.Primary).
			Foreground(t.Text).
			Bold(true).
			Padding(0, 1)
	}

	if m.width > 0 {
		// Pad to full width.
		return style.Width(m.width).Render(line)
	}
	return style.Render(line)
}

// locationUpdated is a message to update the location indicator.
type locationUpdated struct {
	loc orchestrator.Location
}

// offPathToggled is a message to toggle the off-path mode indicator.
type offPathToggled struct {
	on bool
}
