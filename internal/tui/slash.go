package tui

import tea "github.com/charmbracelet/bubbletea"

// RunSlashCommand routes cmd through the same slash-command dispatcher the live
// TUI uses when the operator presses Enter on a slash command.
//
// The returned tea.Cmd is non-nil for commands with asynchronous side effects
// such as terminal attach. Headless callers that only want a deterministic frame
// should reject non-nil commands rather than executing them.
func (m RootModel) RunSlashCommand(cmd string) (RootModel, tea.Cmd) {
	updated, tcmd := m.handleSlashCommand(cmd)
	if rm, ok := updated.(RootModel); ok {
		return rm, tcmd
	}
	return m, tcmd
}
