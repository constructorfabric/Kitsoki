package tui

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"kitsoki/internal/journal"
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
//
// Slash-command feedback lines (anything starting with "(") are styled
// in blue so / output is visually distinct from narrative — bypasses
// Markdown for those so the lipgloss ANSI survives.
func (m *transcriptModel) AppendSystem(body string) {
	if strings.HasPrefix(body, "(") {
		m.AppendSlashOutput(body)
		return
	}
	m.entries = append(m.entries, transcriptEntry{
		body:   m.renderMarkdown(body),
		source: body,
	})
	m.vp.SetContent(m.render())
	m.vp.GotoBottom()
}

// AppendSlashOutput renders body in the blue slash-command style and
// bypasses the Markdown pipeline so ANSI styles survive. Multi-line
// bodies are styled line-by-line so each line carries the color
// reset/start sequence (some terminals lose the colour on wrap
// otherwise).
func (m *transcriptModel) AppendSlashOutput(body string) {
	var sb strings.Builder
	for i, line := range strings.Split(body, "\n") {
		if i > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(slashOutputStyle.Render(line))
	}
	m.entries = append(m.entries, transcriptEntry{body: sb.String()})
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

// AppendOffPathAnswer appends an oracle reply styled with the soft off-path
// amber tint. The reply runs through the same Markdown pipeline as
// AppendTurn so formatting (lists, code blocks) renders cleanly. Pass
// userInput="" when the caller already appended the question header in a
// prior AppendTurn (the typical TUI submitOffPath sequence).
func (m *transcriptModel) AppendOffPathAnswer(userInput, answer string) {
	rendered := m.renderMarkdown(answer)
	if rendered == "" {
		rendered = answer
	}
	body := transcriptOffPathAnswerStyle.Render(rendered)
	entry := transcriptEntry{
		body:   body,
		source: answer,
	}
	if userInput != "" {
		entry.header = "> " + userInput
	}
	m.entries = append(m.entries, entry)
	m.vp.SetContent(m.render())
	m.vp.GotoBottom()
}

// AppendMetaList appends the /meta list output as one transcript
// entry: a blue title banner, a header row (column names), one
// blue-styled padded row per chat, then a closing rule. Column widths
// are computed across rows AND the header so things line up. Bypasses
// the Markdown pipeline so lipgloss ANSI survives.
func (m *transcriptModel) AppendMetaList(headers []string, rows [][]string) {
	// Per-column width = max(header, all rows in that column).
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, r := range rows {
		for i, c := range r {
			if i >= len(widths) {
				continue
			}
			if n := runeLen(c); n > widths[i] {
				widths[i] = n
			}
		}
	}

	render := func(cells []string) string {
		var sb strings.Builder
		for i, c := range cells {
			if i > 0 {
				sb.WriteString("  ")
			}
			if i == len(cells)-1 {
				// Last column doesn't need trailing padding.
				sb.WriteString(c)
			} else {
				sb.WriteString(padRight(c, widths[i]))
			}
		}
		return sb.String()
	}

	totalWidth := 0
	for i, w := range widths {
		totalWidth += w
		if i > 0 {
			totalWidth += 2
		}
	}
	if totalWidth < 20 {
		totalWidth = 20
	}

	var sb strings.Builder
	title := "meta chats"
	leftBar := (totalWidth - len(title) - 2) / 2
	if leftBar < 2 {
		leftBar = 2
	}
	rightBar := totalWidth - len(title) - 2 - leftBar
	if rightBar < 2 {
		rightBar = 2
	}
	sb.WriteString(metaListHeaderStyle.Render(strings.Repeat("─", leftBar) + " " + title + " " + strings.Repeat("─", rightBar)))
	sb.WriteString("\n")
	sb.WriteString(metaListHeaderStyle.Render(render(headers)))
	sb.WriteString("\n")
	if len(rows) == 0 {
		sb.WriteString(metaListItemStyle.Render("(no meta chats yet)"))
		sb.WriteString("\n")
	} else {
		for _, r := range rows {
			sb.WriteString(metaListItemStyle.Render(render(r)))
			sb.WriteString("\n")
		}
	}
	sb.WriteString(metaListHeaderStyle.Render(strings.Repeat("─", totalWidth)))
	m.entries = append(m.entries, transcriptEntry{body: sb.String()})
	m.vp.SetContent(m.render())
	m.vp.GotoBottom()
}

func runeLen(s string) int { return len([]rune(s)) }

func padRight(s string, n int) string {
	if d := n - runeLen(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

// ContentHeight returns the line count of the rendered transcript at
// the current viewport width. Used as a pre-append mark by callers
// that want to scroll the viewport to a specific section after a
// batch of appends — see ScrollToLine.
func (m *transcriptModel) ContentHeight() int {
	if len(m.entries) == 0 {
		return 0
	}
	return lipgloss.Height(m.render())
}

// ScrollToLine positions the viewport so that line `n` of the rendered
// content sits at the top of the visible window. Used by meta-mode
// exit/reload paths so the new on-path content lands at the top of
// the pane and the meta-mode chat scrolls off — still reachable by
// scrolling up.
func (m *transcriptModel) ScrollToLine(n int) {
	if n < 0 {
		n = 0
	}
	m.vp.SetYOffset(n)
}

// AppendError appends an error/rejection message. The leading arrow
// matches AppendGuardHint and AppendClarification so a user reading the
// scrollback sees a consistent prefix for engine-side feedback.
func (m *transcriptModel) AppendError(userInput, msg string) {
	header := "> " + userInput
	body := errorStyle.Render("→ " + msg)
	m.entries = append(m.entries, transcriptEntry{header: header, body: body})
	m.vp.SetContent(m.render())
	m.vp.GotoBottom()
}

// AppendGuardHint appends a guard-failure hint. The leading arrow softens
// the older "[blocked]" prefix: a guard refusal is information, not an
// alarm — the user picked a valid intent and the world said "not now",
// which is exactly the kind of friction the author wrote the hint for.
func (m *transcriptModel) AppendGuardHint(hint string) {
	if hint == "" {
		return
	}
	body := guardHintStyle.Render("→ " + hint)
	m.entries = append(m.entries, transcriptEntry{body: body})
	m.vp.SetContent(m.render())
	m.vp.GotoBottom()
}

// AppendClarification renders a friendly rejection: the user's input did
// not parse to any allowed intent (UNKNOWN_INTENT,
// INTENT_NOT_ALLOWED_IN_STATE, INVALID_SLOT_VALUE, …). The user header is
// included so the transcript carries the same "> input" anchor as a real
// turn; the body is styled with the muted clarificationStyle and a list
// of suggestions follows on subsequent lines.
//
// Both userInput and message may be empty: an empty header is omitted,
// and an empty message no-ops.
// AppendDisambig appends a single-line disambiguation breadcrumb. Style
// matches AppendGuardHint — a soft arrow plus muted body — because both
// describe path-disambiguation friction rather than failure.
func (m *transcriptModel) AppendDisambig(body string) {
	if body == "" {
		return
	}
	rendered := clarificationStyle.Render("→ " + body)
	m.entries = append(m.entries, transcriptEntry{body: rendered})
	m.vp.SetContent(m.render())
	m.vp.GotoBottom()
}

func (m *transcriptModel) AppendClarification(userInput, message string) {
	if message == "" {
		return
	}
	var header string
	if userInput != "" {
		header = "> " + userInput
	}
	body := clarificationStyle.Render(message)
	m.entries = append(m.entries, transcriptEntry{header: header, body: body})
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

// ReconstructFromEntries replays a slice of journal entries (ordered by turn,
// seq) into the transcript model, rehydrating it from durable journal data.
//
// This is the read side of the continue-mode transcript rehydration path
// (proposal §4.6).  Each recognised entry kind maps to the corresponding live
// constructor; unrecognised kinds are skipped with a debug log line — the
// method never panics on unknown kinds.
//
// Supported kinds:
//   - view.rendered    → AppendSystem with the journalled view text
//   - offpath.question → AppendTurn with the question as user input
//   - offpath.answer   → AppendOffPathAnswer with the answer text
//   - disambig.presented / disambig.chosen → skipped (no transcript constructor today)
//   - all others       → skipped
func (m *transcriptModel) ReconstructFromEntries(entries []journal.Entry) {
	for _, e := range entries {
		switch e.Kind {
		case journal.KindViewRendered:
			var body struct {
				ViewText  string `json:"view_text"`
				UserInput string `json:"user_input"`
			}
			if err := json.Unmarshal(e.Body, &body); err != nil {
				slog.Debug("transcript: failed to decode view.rendered body",
					"turn", e.Turn, "seq", e.Seq, "err", err)
				continue
			}
			switch {
			case body.UserInput != "":
				// User-driven turn — render with the "> input" header so the
				// resumed transcript matches what the user saw live.
				m.AppendTurn(body.UserInput, body.ViewText)
			case body.ViewText != "":
				// Synthetic turn (bg-job completion, timeout) — no input header,
				// just the new view. Matches live behaviour at those sites.
				m.AppendSystem(body.ViewText)
			}

		case journal.KindOffPathQuestion:
			var body struct {
				Question string `json:"question"`
			}
			if err := json.Unmarshal(e.Body, &body); err != nil {
				slog.Debug("transcript: failed to decode offpath.question body",
					"turn", e.Turn, "seq", e.Seq, "err", err)
				continue
			}
			// Render as a turn with the question as user input and no view body.
			m.AppendTurn(body.Question, "")

		case journal.KindOffPathAnswer:
			var body struct {
				Answer string `json:"answer"`
				Input  string `json:"input"`
			}
			if err := json.Unmarshal(e.Body, &body); err != nil {
				slog.Debug("transcript: failed to decode offpath.answer body",
					"turn", e.Turn, "seq", e.Seq, "err", err)
				continue
			}
			m.AppendOffPathAnswer(body.Input, body.Answer)

		case journal.KindChatsAppend:
			// Body shape: {"ops": [{"op":"add", "path":"/messages/-", "value":{...}}]}
			// We only extract the appended message; non-append patch ops
			// (e.g. metadata replaces on /meta/*) are skipped — they're not
			// user-visible chat-row content.
			var body struct {
				Ops []struct {
					Op    string          `json:"op"`
					Path  string          `json:"path"`
					Value json.RawMessage `json:"value"`
				} `json:"ops"`
			}
			if err := json.Unmarshal(e.Body, &body); err != nil {
				slog.Debug("transcript: failed to decode chats.append body",
					"turn", e.Turn, "seq", e.Seq, "err", err)
				continue
			}
			for _, op := range body.Ops {
				if op.Op != "add" || op.Path != "/messages/-" {
					continue
				}
				var msg struct {
					Role    string `json:"role"`
					Content string `json:"content"`
					Text    string `json:"text"`
				}
				if err := json.Unmarshal(op.Value, &msg); err != nil {
					continue
				}
				text := msg.Content
				if text == "" {
					text = msg.Text
				}
				if text == "" {
					continue
				}
				switch msg.Role {
				case "user":
					m.AppendTurn(text, "")
				case "assistant", "model":
					m.AppendOffPathAnswer("", text)
				default:
					m.AppendSystem(text)
				}
			}

		case journal.KindDisambigPresented:
			var body struct {
				Candidates []string `json:"candidates"`
			}
			if err := json.Unmarshal(e.Body, &body); err != nil {
				slog.Debug("transcript: failed to decode disambig.presented body",
					"turn", e.Turn, "seq", e.Seq, "err", err)
				continue
			}
			if len(body.Candidates) > 0 {
				m.AppendDisambig("disambiguating: " + strings.Join(body.Candidates, ", "))
			}

		case journal.KindDisambigChosen:
			var body struct {
				Intent         string `json:"intent"`
				CandidateLabel string `json:"candidate_label"`
			}
			if err := json.Unmarshal(e.Body, &body); err != nil {
				slog.Debug("transcript: failed to decode disambig.chosen body",
					"turn", e.Turn, "seq", e.Seq, "err", err)
				continue
			}
			label := body.CandidateLabel
			if label == "" {
				label = body.Intent
			}
			if label != "" {
				m.AppendDisambig("chose: " + label)
			}

		default:
			slog.Debug("transcript: no constructor for kind", "kind", e.Kind)
		}
	}
	if len(entries) > 0 {
		m.vp.GotoBottom()
	}
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
