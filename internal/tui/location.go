package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"hally/internal/orchestrator"
)

// locationModel renders the §7.1 location indicator (breadcrumb + state description).
type locationModel struct {
	loc    orchestrator.Location
	offPath bool
	width  int
}

func newLocationModel() locationModel {
	return locationModel{
		loc: orchestrator.Location{OnPath: true},
	}
}

func (m locationModel) Init() tea.Cmd { return nil }

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
