package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// commands_world.go — single-pane-tui §"Phase 1.5": /world opens the
// dedicated hierarchical viewer/editor for the world object. v1 is
// read-only; the editor lands in a later iteration once the trace
// captures world edits cleanly.

// openWorldView snapshots the current world and switches the model
// into ModeWorldView. The chat-view sub-models stay alive underneath
// so closing the view returns the user to exactly the chat state they
// left.
func (m RootModel) openWorldView() (tea.Model, tea.Cmd) {
	snap := m.orch.CurrentWorld(m.sid)
	w := m.width
	h := m.height
	if w <= 0 {
		w = 80
	}
	if h <= 0 {
		h = 24
	}
	m.worldView = newWorldViewModel(snap, m.orch.AppDef().App.ID, w, h)
	m.worldView.theme = m.currentTheme()
	m.mode = ModeWorldView
	return m, nil
}

// closeWorldView returns the model to the chat view. Keeps the
// snapshot around (zero-value worldView would also work; this matches
// the meta-mode close pattern of leaving sub-model state in place for
// quick reopen).
func (m RootModel) closeWorldView() (tea.Model, tea.Cmd) {
	m.mode = ModeOnPath
	return m, nil
}
