// Command terminal-output-probe is a real-renderer fixture for the xterm TUI
// regression test. It deliberately writes blank lines to process stderr while
// Bubble Tea maintains a multi-line bottom region. Without
// TerminalOutputCapture, xterm scrollback contains one extra copy of the
// operation row per write.
package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"kitsoki/internal/tui"
)

type tick int

type model struct{ count int }

func (m model) Init() tea.Cmd { return nextTick(0) }

func nextTick(n int) tea.Cmd {
	return tea.Tick(40*time.Millisecond, func(time.Time) tea.Msg { return tick(n) })
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if n, ok := msg.(tick); ok {
		m.count = int(n) + 1
		fmt.Fprintln(os.Stderr)
		if m.count < 4 {
			return m, tea.Batch(nextTick(m.count), tea.Println(fmt.Sprintf("content %d", m.count)))
		}
		return m, tea.Println("PROBE-DONE")
	}
	return m, nil
}

func (m model) View() string {
	return strings.Join([]string{
		"operation: Fix bug (gated) · running · phase root bf reproduction",
		strings.Repeat("─", 80),
		"Actions:",
		"",
		"▸ accept  record the verdict",
		"  refine  re-run triage with feedback",
		"  quit    abandon",
		"  look    re-render",
		"",
		"[↑/↓ move • Enter pick • Tab chat • Esc cancel]",
	}, "\n")
}

func main() {
	program := tea.NewProgram(model{})
	capture, err := tui.NewTerminalOutputCapture()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	priorStdout, priorStderr := os.Stdout, os.Stderr
	capture.Attach(program)
	os.Stdout = capture.Writer()
	os.Stderr = capture.Writer()
	_, runErr := program.Run()
	os.Stdout, os.Stderr = priorStdout, priorStderr
	_ = capture.Close()
	if runErr != nil {
		fmt.Fprintln(os.Stderr, runErr)
		os.Exit(1)
	}
}
