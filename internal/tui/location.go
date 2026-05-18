package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"kitsoki/internal/orchestrator"
	"kitsoki/internal/tui/blocks"
)

// locationModel renders the §7.1 location indicator (breadcrumb + state description).
type locationModel struct {
	loc     orchestrator.Location
	offPath bool
	width   int
	// theme is the active blocks theme used to colour the location
	// bar (single-pane-tui proposal §"Per-room theme swap"). Empty
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
	var parts []string

	// On/off-path indicator.
	if m.offPath {
		parts = append(parts, "○")
	} else {
		parts = append(parts, "●")
	}

	// Breadcrumb.
	if m.loc.Breadcrumb != "" {
		parts = append(parts, m.loc.Breadcrumb)
	}

	// State description.
	if m.loc.StateDescription != "" {
		parts = append(parts, "—")
		parts = append(parts, m.loc.StateDescription)
	}

	// Turn counter.
	if m.loc.TurnNumber > 0 {
		parts = append(parts, fmt.Sprintf("(turn %d)", m.loc.TurnNumber))
	}

	line := strings.Join(parts, " ")

	// World context (compact).
	if len(m.loc.RelevantWorld) > 0 {
		keys := make([]string, 0, len(m.loc.RelevantWorld))
		for k := range m.loc.RelevantWorld {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		worldParts := make([]string, 0, len(keys))
		for _, k := range keys {
			worldParts = append(worldParts, fmt.Sprintf("%s=%v", k, m.loc.RelevantWorld[k]))
		}
		line += "  [" + strings.Join(worldParts, ", ") + "]"
	}

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
