package tui

import tea "github.com/charmbracelet/bubbletea"

// offPathModel manages the §7.7 off-path mode state.
type offPathModel struct {
	active bool
	banner string
}

func newOffPathModel(banner string) offPathModel {
	if banner == "" {
		banner = "*** off the path — responses do not affect your story ***"
	}
	return offPathModel{banner: banner}
}

func (m offPathModel) Init() tea.Cmd { return nil }

func (m offPathModel) Update(msg tea.Msg) (offPathModel, tea.Cmd) {
	switch msg.(type) {
	case enterOffPathMsg:
		m.active = true
	case exitOffPathMsg:
		m.active = false
	}
	return m, nil
}

// Active returns true when in off-path mode.
func (m offPathModel) Active() bool { return m.active }

// Banner returns the off-path banner text.
func (m offPathModel) Banner() string { return m.banner }

// enterOffPathMsg activates off-path mode.
type enterOffPathMsg struct{}

// exitOffPathMsg deactivates off-path mode.
type exitOffPathMsg struct{}
