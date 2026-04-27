package tui

import (
	"fmt"
	"strings"
	"sync"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// cachedAutoStyle holds the result of a one-time terminal background probe.
// Glamour's WithAutoStyle() internally queries the terminal via termenv for
// its background colour. When that query runs after Bubble Tea has taken
// over stdin/stdout (e.g. during a WindowSizeMsg-driven renderer rebuild),
// the terminal's response bytes — something like
// "\x1b]11;rgb:ffff/ffff/ffff\x1b\\" — leak into the input stream and get
// rendered as literal characters ("b:ffff/ffff/ffff\") in the prompt area.
//
// We probe once on the first renderer build (which happens in
// newTranscriptModel, before tea.NewProgram takes over stdin) and then use
// glamour.WithStandardStyle() for every subsequent rebuild, which does not
// re-query the terminal.
var (
	autoStyleOnce sync.Once
	autoStyleName string // "dark" or "light"
)

func detectAutoStyle() string {
	autoStyleOnce.Do(func() {
		// When stdout isn't a colour terminal (e.g. a pipe during `go test`,
		// or stdout=/dev/null), glamour should emit plain text rather than
		// ANSI escapes. Otherwise its colour codes pollute captured output
		// and break test assertions.
		if termenv.DefaultOutput().Profile == termenv.Ascii {
			autoStyleName = "notty"
			return
		}
		if lipgloss.HasDarkBackground() {
			autoStyleName = "dark"
		} else {
			autoStyleName = "light"
		}
	})
	return autoStyleName
}

// transcriptEntry is one item in the transcript history.
type transcriptEntry struct {
	// header is the user-turn header, e.g. "> go south"
	header string
	// body is the styled body ready for the viewport.
	body string
	// source is the raw Markdown source (if the body was produced via
	// Glamour). Empty for pre-styled entries like errors and guard hints.
	// Kept so SetSize can re-render the body at a new wrap width.
	source string
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

	// Rebind the viewport key map so scroll works without PgUp/PgDn/Home/End
	// physical keys (most MacBook keyboards lack them). Bare letters like
	// b/f/u/d/j/k are removed because they would otherwise be consumed by
	// the viewport while the user is typing in the prompt.
	vp.KeyMap.PageUp.SetKeys("pgup", "ctrl+b")
	vp.KeyMap.PageDown.SetKeys("pgdown", "ctrl+f")
	vp.KeyMap.HalfPageUp.SetKeys("ctrl+u")
	vp.KeyMap.HalfPageDown.SetKeys("ctrl+d")
	vp.KeyMap.Up.SetKeys("shift+up", "alt+up")
	vp.KeyMap.Down.SetKeys("shift+down", "alt+down")

	renderer, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle(detectAutoStyle()),
		glamour.WithWordWrap(max(width-2, 40)),
		// WithPreservedNewLines treats author line breaks as hard breaks,
		// so indented example lines and multi-line prompts don't get
		// reflowed into a single paragraph.
		glamour.WithPreservedNewLines(),
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
			// Rebuild the glamour renderer at the new wrap width.
			if r, err := glamour.NewTermRenderer(
				glamour.WithStandardStyle(detectAutoStyle()),
				glamour.WithWordWrap(max(msg.Width-4, 40)),
				glamour.WithPreservedNewLines(),
			); err == nil {
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
// The body goes through the same Markdown+preserve-newlines pipeline as
// AppendTurn so the initial view renders identically to subsequent ones.
func (m *transcriptModel) AppendSystem(body string) {
	m.entries = append(m.entries, transcriptEntry{
		body:   m.renderMarkdown(body),
		source: body,
	})
	m.vp.SetContent(m.render())
	m.vp.GotoBottom()
}

// SetSize resizes the viewport and rebuilds the Glamour renderer at the new
// wrap width. Called from the root model's resize() path because the root
// intercepts tea.WindowSizeMsg before it reaches this model.
func (m *transcriptModel) SetSize(outerWidth, outerHeight, viewportWidth, viewportHeight int) {
	m.width = outerWidth
	m.height = outerHeight
	m.vp.Width = viewportWidth
	m.vp.Height = viewportHeight
	// Glamour's auto style adds ~2 chars of left padding on body text, so
	// wrap 2 chars narrower than the viewport so rendered lines fill the
	// full width without clipping the last word. A larger safety margin
	// just wastes horizontal space — the transcript panel is already
	// shrunk by the menu and the outer border.
	wrap := viewportWidth - 2
	if wrap < 40 {
		wrap = 40
	}
	if r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle(detectAutoStyle()),
		glamour.WithWordWrap(wrap),
		glamour.WithPreservedNewLines(),
	); err == nil {
		m.renderer = r
		// Re-render Markdown-sourced entries at the new wrap width so
		// lines that were wrapped for a larger viewport don't now clip.
		for i := range m.entries {
			if m.entries[i].source != "" {
				m.entries[i].body = m.renderMarkdown(m.entries[i].source)
			}
		}
		m.vp.SetContent(m.render())
	}
}

// AppendTurn appends a user turn with header and rendered view.
// The view is passed through glamour for Markdown styling, with
// glamour.WithPreservedNewLines() so hand-wrapped views don't get reflowed
// into single paragraphs.
func (m *transcriptModel) AppendTurn(userInput, view string) {
	header := "> " + userInput
	body := m.renderMarkdown(view)
	m.entries = append(m.entries, transcriptEntry{
		header: header,
		body:   body,
		source: view,
	})
	m.vp.SetContent(m.render())
	m.vp.GotoBottom()
}

// renderMarkdown styles view text via glamour. The renderer is built with
// glamour.WithPreservedNewLines() so author line breaks survive; then
// leading ASCII spaces on continuation lines are promoted to non-breaking
// spaces (U+00A0) because CommonMark otherwise strips them as inline
// whitespace.
//
// TECH DEBT (2026-04-23): WithPreservedNewLines is the right call for
// structured views (Terminal Room's indented examples, menu-ish lists) but
// it also caps prose views at the author's hand-wrap width — a cloak
// foyer view authored at ~65 chars/line won't grow past 65 even on a
// 150-col terminal. Fix direction is a typed "view element" system
// (prose | list | code | kv | template) so the renderer knows whether to
// reflow or preserve per block. See ideas.md § "View rendering: unify
// structured + prose content".
func (m *transcriptModel) renderMarkdown(text string) string {
	if text == "" {
		return ""
	}
	text = preserveLeadingIndent(text)
	if m.renderer == nil {
		return text
	}
	out, err := m.renderer.Render(text)
	if err != nil {
		return text
	}
	return strings.TrimRight(out, "\n")
}

// preserveLeadingIndent swaps runs of two-or-more leading ASCII spaces on
// each line for the equivalent count of non-breaking spaces (U+00A0), so a
// Markdown renderer that would otherwise collapse them keeps the author's
// intentional indentation (indented example lines in a Terminal Room view,
// nested bullets, etc.). Lines with zero or one leading space are left
// alone. Four-plus leading spaces would otherwise be treated as a code
// block; callers who actually want that should use fenced blocks.
func preserveLeadingIndent(s string) string {
	var b strings.Builder
	for i, line := range strings.Split(s, "\n") {
		if i > 0 {
			b.WriteByte('\n')
		}
		j := 0
		for j < len(line) && line[j] == ' ' {
			j++
		}
		if j < 2 {
			b.WriteString(line)
			continue
		}
		rest := line[j:]
		if startsWithListMarker(rest) {
			// Keep ASCII spaces so Markdown recognises this as a list item.
			b.WriteString(line)
			continue
		}
		for k := 0; k < j; k++ {
			b.WriteRune(' ')
		}
		b.WriteString(rest)
	}
	return b.String()
}

// startsWithListMarker reports whether s begins with an unordered or
// ordered Markdown list marker ("- ", "* ", "+ ", "N. ", or "N) ").
func startsWithListMarker(s string) bool {
	if len(s) < 2 {
		return false
	}
	if (s[0] == '-' || s[0] == '*' || s[0] == '+') && s[1] == ' ' {
		return true
	}
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i > 0 && i+1 < len(s) && (s[i] == '.' || s[i] == ')') && s[i+1] == ' ' {
		return true
	}
	return false
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
