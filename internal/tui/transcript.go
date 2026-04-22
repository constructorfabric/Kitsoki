package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

// transcriptEntry is one item in the transcript history.
type transcriptEntry struct {
	// header is the user-turn header, e.g. "> go south"
	header string
	// body is the rendered view/narrative.
	body string
}

// transcriptModel is the append-only scrollable history pane.
type transcriptModel struct {
	entries  []transcriptEntry
	vp       viewport.Model
	offPath  bool
	width    int
	height   int
	renderer *glamour.TermRenderer
	ready    bool
}

func newTranscriptModel(width, height int) transcriptModel {
	vp := viewport.New(width, height)
	vp.SetContent("")

	renderer, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		renderer = nil
	}

	return transcriptModel{
		vp:       vp,
		width:    width,
		height:   height,
		renderer: renderer,
		ready:    true,
	}
}

func (m transcriptModel) Init() tea.Cmd { return nil }

func (m transcriptModel) Update(msg tea.Msg) (transcriptModel, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		if m.ready {
			m.width = msg.Width
			m.height = msg.Height
			m.vp.Width = msg.Width
			m.vp.Height = msg.Height
			// Rebuild glamour renderer with new width.
			r, err := glamour.NewTermRenderer(
				glamour.WithAutoStyle(),
				glamour.WithWordWrap(max(40, msg.Width-4)),
			)
			if err == nil {
				m.renderer = r
			}
			m.vp.SetContent(m.render())
		}
	case offPathToggled:
		m.offPath = msg.on
	}

	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

// AppendSystem adds a system-level message to the transcript (no user header).
func (m *transcriptModel) AppendSystem(body string) {
	m.entries = append(m.entries, transcriptEntry{body: body})
	m.vp.SetContent(m.render())
	m.vp.GotoBottom()
}

// AppendTurn appends a user turn with header and rendered view.
func (m *transcriptModel) AppendTurn(userInput, view string) {
	header := "> " + userInput
	body := m.renderMarkdown(view)
	m.entries = append(m.entries, transcriptEntry{header: header, body: body})
	m.vp.SetContent(m.render())
	m.vp.GotoBottom()
}

// AppendError appends an error/rejection message.
func (m *transcriptModel) AppendError(userInput, msg string) {
	header := "> " + userInput
	body := errorStyle.Render("[blocked] " + msg)
	m.entries = append(m.entries, transcriptEntry{header: header, body: body})
	m.vp.SetContent(m.render())
	m.vp.GotoBottom()
}

// AppendGuardHint appends a guard-failure hint.
func (m *transcriptModel) AppendGuardHint(hint string) {
	if hint == "" {
		return
	}
	body := guardHintStyle.Render("[blocked] " + hint)
	m.entries = append(m.entries, transcriptEntry{body: body})
	m.vp.SetContent(m.render())
	m.vp.GotoBottom()
}

func (m *transcriptModel) renderMarkdown(text string) string {
	if text == "" {
		return ""
	}
	if m.renderer == nil {
		return text
	}
	out, err := m.renderer.Render(text)
	if err != nil {
		return text
	}
	return out
}

func (m *transcriptModel) render() string {
	var sb strings.Builder
	for _, entry := range m.entries {
		if entry.header != "" {
			sb.WriteString(turnHeaderStyle.Render(entry.header))
			sb.WriteString("\n")
		}
		if entry.body != "" {
			sb.WriteString(entry.body)
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

func (m transcriptModel) View() string {
	style := transcriptStyle
	if m.offPath {
		var parts []string
		parts = append(parts, offPathBannerStyle.Render("*** off the path — responses do not affect your story ***"))
		parts = append(parts, m.vp.View())
		content := strings.Join(parts, "\n")
		return transcriptOffPathStyle.Width(m.width).Height(m.height).Render(content)
	}

	w := m.width
	h := m.height
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}

	return style.Width(w).Height(h).Render(m.vp.View())
}

// AllContent returns all transcript text concatenated (for golden-file tests).
func (m *transcriptModel) AllContent() string {
	return m.render()
}

// EntryCount returns the number of transcript entries.
func (m *transcriptModel) EntryCount() int {
	return len(m.entries)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// scrollMsg is a generic viewport scroll message (for testing).
type scrollMsg struct {
	key string
}

// renderWidth returns the rendered width of a string via lipgloss measurement.
func renderWidth(s string) int {
	return lipgloss.Width(s)
}

func formatWidth(n int) string {
	return fmt.Sprintf("%d", n)
}
