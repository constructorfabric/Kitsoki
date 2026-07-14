package tui

import (
	"bufio"
	"os"
	"strings"
	"sync"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// terminalOutputSender is the subset of tea.Program used by
// TerminalOutputCapture. Keeping the boundary this small lets tests prove the
// capture contract without spinning up a renderer.
type terminalOutputSender interface {
	Send(tea.Msg)
}

// terminalOutputMsg carries process output that would otherwise write directly
// to the terminal while Bubble Tea owns the cursor. Direct stdout/stderr writes
// move the cursor behind Bubble Tea's back; the next repaint then stamps slices
// of the live bottom chrome into scrollback. Routing visible text back through
// Update keeps all terminal writes under the renderer's row accounting.
type terminalOutputMsg struct {
	text string
}

// TerminalOutputCapture owns a pipe used as process stdout/stderr while the TUI
// is running. Attach must be called before callers write to Writer. Close is
// idempotent and waits for the reader to drain.
type TerminalOutputCapture struct {
	reader *os.File
	writer *os.File

	mu     sync.Mutex
	sender terminalOutputSender

	closeOnce sync.Once
	done      chan struct{}
}

// NewTerminalOutputCapture creates a process-output capture. The returned pipe
// writer is suitable for os.Stdout/os.Stderr and Cobra command writers.
func NewTerminalOutputCapture() (*TerminalOutputCapture, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	return &TerminalOutputCapture{
		reader: r,
		writer: w,
		done:   make(chan struct{}),
	}, nil
}

// Writer returns the file process output should be redirected to while Bubble
// Tea owns the terminal.
func (c *TerminalOutputCapture) Writer() *os.File { return c.writer }

// Attach begins draining captured output into the Bubble Tea message loop.
func (c *TerminalOutputCapture) Attach(sender terminalOutputSender) {
	c.mu.Lock()
	c.sender = sender
	c.mu.Unlock()
	go c.readLoop()
}

func (c *TerminalOutputCapture) readLoop() {
	defer close(c.done)
	reader := bufio.NewReader(c.reader)
	for {
		line, err := reader.ReadString('\n')
		if text := sanitizeTerminalOutput(line); text != "" {
			c.mu.Lock()
			sender := c.sender
			c.mu.Unlock()
			if sender != nil {
				sender.Send(terminalOutputMsg{text: text})
			}
		}
		if err != nil {
			// The capture is best-effort UI isolation. A broken pipe must not
			// recursively write another warning to the same terminal stream.
			return
		}
	}
}

func sanitizeTerminalOutput(s string) string {
	s = ansi.Strip(s)
	s = strings.Map(func(r rune) rune {
		if r == '\t' {
			return ' '
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	return strings.TrimSpace(s)
}

// Close stops the capture and waits for a final partial line to drain.
func (c *TerminalOutputCapture) Close() error {
	var closeErr error
	c.closeOnce.Do(func() {
		closeErr = c.writer.Close()
		<-c.done
		if err := c.reader.Close(); closeErr == nil {
			closeErr = err
		}
	})
	return closeErr
}

func (m *transcriptModel) appendTerminalOutput(text string) {
	if text == "" {
		return
	}
	body := lipgloss.NewStyle().
		Foreground(colorMuted).
		Italic(true).
		Render("→ process: " + text)
	m.entries = append(m.entries, transcriptEntry{body: body})
	m.queue(body)
}
