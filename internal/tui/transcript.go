package tui

import (
	"encoding/json"
	"log/slog"
	"strings"
	"sync"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"kitsoki/internal/app"
	"kitsoki/internal/expr"
	"kitsoki/internal/journal"
	"kitsoki/internal/render"
	"kitsoki/internal/render/elements"
	"kitsoki/internal/render/sourcecolor"
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

// sourceColorThemeFor maps a detectAutoStyle() result to the
// sourcecolor palette. A light terminal gets LightTheme so the
// LLM/template bands read as pale tints; dark and notty (test / piped)
// terminals keep DarkTheme — the historical default — so existing
// output and test assertions are unchanged.
//
// Without this, FlushPending hard-coded DarkTheme, painting a dark
// bronze band behind LLM-sourced text even on a light terminal: the
// "dark-mode background with no dark background" regression a
// light-terminal operator hit after the source-color feature landed.
func sourceColorThemeFor(styleName string) sourcecolor.Theme {
	if styleName == "light" {
		return sourcecolor.LightTheme
	}
	return sourcecolor.DarkTheme
}

// sourceColorTheme resolves the active sourcecolor palette from the
// detected terminal background.
func sourceColorTheme() sourcecolor.Theme {
	return sourceColorThemeFor(detectAutoStyle())
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
	// typedView is the parsed typed View payload, populated by
	// AppendTurnTyped / AppendSystemTyped. When non-nil, SetSize /
	// WindowSizeMsg-driven rebuilds re-render the typed elements at
	// the new viewport width (Issue 4 / option (a) in the brief).
	// renderEnv and appRenderer are the matching expr.Env and per-app
	// pongo2 renderer captured at append time; they don't move
	// thereafter (the view is locked to the world snapshot of its
	// turn — re-rendering reflects layout only, not new world state).
	typedView   *app.View
	renderEnv   expr.Env
	appRenderer *render.AppRenderer
}

// transcriptModel is the append-only chat history. Historic entries
// are emitted to the terminal's scrollback via tea.Println (queued in
// `pending` and flushed by the root model on each Update), so the
// terminal's native scroll (wheel, Cmd+↑) walks history. Only the
// in-flight `liveLine` (routing-status placeholder) is rendered as
// part of View() — settled lines move into pending and clear
// liveLine.
type transcriptModel struct {
	// entries keeps the rendered history for AllContent() / journal
	// reconstruction / resume. NOT consulted by View().
	entries []transcriptEntry
	// pending is the queue of newly-appended entries waiting to print
	// to scrollback. The root model drains it after each Update via
	// FlushPending().
	pending []string
	// liveLine is the single in-place-updatable bottom line rendered
	// above the prompt while routing/etc. is in flight. Empty when
	// nothing live is showing.
	liveLine string

	// Legacy viewport — retained only because resume/replay paths and
	// the world view still touch SetSize. No longer rendered into the
	// main chat View().
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
		// Markdown formatting is degraded: Render() will fall back to
		// emitting raw text (see renderMarkdown's nil-renderer guard).
		// Surface it so operators know why the transcript looks plain.
		slog.Warn("transcript: glamour renderer init failed; markdown formatting disabled",
			"err", err)
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

// queue marks rendered content for emission to scrollback on the next
// FlushPending. Always called alongside an entries append so AllContent
// (journal reconstruction, test inspection) stays consistent.
//
// Hard-wraps each line of body to the transcript's current viewport
// width using ansi.Hardwrap — Glamour respects word boundaries when
// wrapping markdown, but long unbroken tokens (ticket IDs, URLs)
// produce lines wider than the terminal. Those overflow lines cause
// the terminal to auto-wrap, which makes Bubble Tea's live-region row
// accounting drift; the visible symptom is the coloured status row
// overwriting the wrapped scrollback content. Hardwrap breaks
// anywhere — including mid-token — so every line is bounded by the
// terminal width and Bubble Tea's row count matches what's rendered.
func (m *transcriptModel) queue(body string) {
	if body == "" {
		return
	}
	if w := m.queueWrapWidth(); w > 0 {
		body = ansi.Hardwrap(body, w, true)
	}
	// Invariant: the live spinner/queue indicator (⏳) must never reach
	// scrollback — it belongs to the View() bottom region only. This is
	// guarded by a test (TestTranscriptQueue_NoIndicatorLeak) rather than
	// a live-path slog.Warn, so the prod queue() stays free of BUG noise.
	m.pending = append(m.pending, body)
}

// queueWrapWidth returns the column count to hard-wrap scrollback
// content to. Matches the viewport width when set, otherwise uses
// the model's overall width, with a 40-column floor so very narrow
// terminals still get readable output.
func (m *transcriptModel) queueWrapWidth() int {
	w := m.vp.Width
	if w <= 0 {
		w = m.width
	}
	if w < 40 {
		w = 40
	}
	return w
}

// FlushPending returns a tea.Cmd that emits every queued entry via
// tea.Println (printed above the live bottom region) and clears the
// queue. Returns nil when there's nothing to flush.
//
// Joins the queue into a single tea.Println call rather than batching
// per-entry println cmds — the test harness's processCommands doesn't
// recurse into nested tea.BatchMsg values, so a nested batch would
// silently drop prints. One join, one println, no batch nesting.
//
// Source-color: the joined buffer is run through [sourcecolor.Colorize]
// here, at the last moment before bytes leave the model. This is the
// single chokepoint where LLM-sourced runs (marked at the host
// operator boundary with zero-width sentinels) become visible warm-bg
// bands. Plain template content gets the cool-bg paint at the same
// time. Entries without sentinels still go through colorize: it's the
// rendering layer's job to make every transcript line consistent.
func (m *transcriptModel) FlushPending() tea.Cmd {
	if len(m.pending) == 0 {
		return nil
	}
	items := m.pending
	m.pending = nil
	joined := strings.Join(items, "\n")
	joined = sourcecolor.Colorize(joined, sourceColorTheme(), sourcecolor.Options{
		Width: m.queueWrapWidth(),
	})
	return tea.Println(joined)
}

// LiveLine returns the currently-displayed in-flight line, or "" when
// nothing live is showing. RootModel.View() places this just above
// the prompt while it's non-empty.
func (m *transcriptModel) LiveLine() string { return m.liveLine }

// AppendUserInputEcho writes the immediate-on-Enter echo for the user's
// submitted input. The transcript model becomes the source of truth for
// "what the user said and when" — no input-area echo, no waiting for
// the orchestrator. Pairs with AppendAgentBody for the body that lands
// once the orchestrator finishes.
//
// Used by submitInput to give immediate input feedback.
func (m *transcriptModel) AppendUserInputEcho(input string) {
	if input == "" {
		return
	}
	header := turnHeaderStyle.Render("> " + input)
	m.entries = append(m.entries, transcriptEntry{body: header})
	m.queue(header)
}

// AppendAgentBody appends a body-only entry — no "> input" header,
// because the header was already echoed via AppendUserInputEcho. Use
// this from handleTurnOutcome instead of AppendTurn when the immediate
// echo path is active.
func (m *transcriptModel) AppendAgentBody(view string) {
	if view == "" {
		return
	}
	body := m.renderViewSource(view)
	m.entries = append(m.entries, transcriptEntry{body: body, source: view})
	m.queue(body)
}

// AppendAgentBodyTyped is AppendAgentBody's typed-view variant. Mirrors
// AppendTurnTyped but emits no header — the echo went out at submit
// time.
func (m *transcriptModel) AppendAgentBodyTyped(fallbackView string, typed *app.View, env expr.Env, rr *render.AppRenderer) {
	body := m.renderViewWith(*typed, env, rr)
	if body == "" {
		body = m.renderViewSource(fallbackView)
	}
	m.entries = append(m.entries, transcriptEntry{
		body:        body,
		source:      fallbackView,
		typedView:   typed,
		renderEnv:   env,
		appRenderer: rr,
	})
	m.queue(body)
}

// AppendLive sets the in-flight bottom-line content that View() renders
// just above the prompt. Until FinalizeLive lands, UpdateLive may
// replace the line in-place. The body is NOT queued for scrollback —
// it's a live indicator, not a historic entry. Returns 0 for API
// compatibility with old callers (was the legacy entry index).
func (m *transcriptModel) AppendLive(body string) int {
	m.liveLine = body
	return 0
}

// hasLive reports whether an in-flight live line is active (not yet settled).
// Callers guard FinalizeLive on it so a turn-completion finalizer and an
// observer hit event can't both commit the same routing line.
func (m *transcriptModel) hasLive() bool { return m.liveLine != "" }

// UpdateLive replaces the in-flight line. No-op when no live line is
// active — defensive against late-arriving tier events that fire
// after settlement.
func (m *transcriptModel) UpdateLive(body string) {
	if m.liveLine == "" {
		return
	}
	m.liveLine = body
}

// FinalizeLive turns the in-flight line into a permanent scrollback
// entry: queues `body` (or the current live line if body is empty)
// for tea.Println on the next flush, then clears the live slot.
func (m *transcriptModel) FinalizeLive(body string) {
	settled := body
	if settled == "" {
		settled = m.liveLine
	}
	m.liveLine = ""
	if settled != "" {
		m.entries = append(m.entries, transcriptEntry{body: settled})
		m.queue(settled)
	}
}

// AppendBlock appends a pre-rendered styled multi-line body verbatim —
// no Markdown pipeline, no extra styling. Used by slash commands that
// render their own output (e.g. /help, /intents) via internal/tui/blocks.
func (m *transcriptModel) AppendBlock(body string) {
	if body == "" {
		return
	}
	m.entries = append(m.entries, transcriptEntry{body: body})
	m.queue(body)
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
			// Matches SetSize's `viewportWidth - 2` budget; the
			// dispatcher's wrapWidth() picks up `vp.Width - 4` so its
			// output stays inside glamour's content area after the
			// 2-char document margin is accounted for.
			if r, err := glamour.NewTermRenderer(
				glamour.WithStandardStyle(detectAutoStyle()),
				glamour.WithWordWrap(max(msg.Width-2, 40)),
				glamour.WithPreservedNewLines(),
			); err == nil {
				m.renderer = r
			}
			// Re-render typed view entries at the new wrap width
			// (Issue 4 / option (a)). String-source entries are
			// re-rendered by SetSize below when the root model
			// invokes it; this path only fires for the standalone
			// transcriptModel test harness which delivers the
			// WindowSizeMsg directly.
			for i := range m.entries {
				if m.entries[i].typedView != nil {
					m.entries[i].body = m.renderViewWith(
						*m.entries[i].typedView,
						m.entries[i].renderEnv,
						m.entries[i].appRenderer,
					)
				}
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
	rendered := m.renderViewSource(body)
	m.entries = append(m.entries, transcriptEntry{
		body:   rendered,
		source: body,
	})
	m.queue(rendered)
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
	rendered := sb.String()
	m.entries = append(m.entries, transcriptEntry{body: rendered})
	m.queue(rendered)
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
		// Re-render entries at the new wrap width. Typed-view entries
		// re-run the elements dispatcher so prose / list / kv reflow
		// to the new column width (Issue 4 / option (a)); legacy
		// string-sourced entries re-run through Glamour at the new
		// width.
		for i := range m.entries {
			if m.entries[i].typedView != nil {
				m.entries[i].body = m.renderViewWith(
					*m.entries[i].typedView,
					m.entries[i].renderEnv,
					m.entries[i].appRenderer,
				)
				continue
			}
			if m.entries[i].source != "" {
				m.entries[i].body = m.renderViewSource(m.entries[i].source)
			}
		}
		m.vp.SetContent(m.render())
	}
}

// SetHeightOnly adjusts the transcript pane's outer + viewport heights
// WITHOUT rebuilding the Glamour renderer or re-rendering existing
// entries. Use this when only the vertical layout shifts (e.g. the
// prompt grew downward as the user typed past wrap) — the wrap width
// hasn't changed, so the cached entry bodies are still correct.
// Calling SetSize on every keystroke would re-Glamour every transcript
// entry, which is O(entries × Glamour-cost) and noticeably stutters on
// long sessions.
func (m *transcriptModel) SetHeightOnly(outerHeight, viewportHeight int) {
	m.height = outerHeight
	m.vp.Height = viewportHeight
}

// AppendTurn appends a user turn with header and rendered view.
// The view is passed through glamour for Markdown styling, with
// glamour.WithPreservedNewLines() so hand-wrapped views don't get reflowed
// into single paragraphs.
func (m *transcriptModel) AppendTurn(userInput, view string) {
	header := turnHeaderStyle.Render("> " + userInput)
	body := m.renderViewSource(view)
	m.entries = append(m.entries, transcriptEntry{
		header: "> " + userInput,
		body:   body,
		source: view,
	})
	m.queue(header)
	m.queue(body)
}

// AppendTurnTyped is the typed-view variant of AppendTurn (Issue 4 /
// option (a)). It stores the parsed View, the runtime env, and the
// per-app renderer so the entry can be re-rendered at the new
// viewport width on WindowSizeMsg without losing the "templating
// happens before element layout" contract.
//
// The fallback string view (rendered at the machine's stable width)
// is used immediately for the initial paint; subsequent resize-driven
// rebuilds replace it with a width-aware render of the typed View.
func (m *transcriptModel) AppendTurnTyped(userInput, fallbackView string, typed *app.View, env expr.Env, rr *render.AppRenderer) {
	header := "> " + userInput
	body := m.renderViewWith(*typed, env, rr)
	if body == "" {
		body = m.renderViewSource(fallbackView)
	}
	m.entries = append(m.entries, transcriptEntry{
		header:      header,
		body:        body,
		source:      fallbackView,
		typedView:   typed,
		renderEnv:   env,
		appRenderer: rr,
	})
	m.queue(turnHeaderStyle.Render(header))
	m.queue(body)
}

// AppendSystemTyped is AppendSystem's typed-view variant. Used for
// no-input synthetic turns (bg-job completion, timeout, initial view)
// when the orchestrator emits a typed View.
func (m *transcriptModel) AppendSystemTyped(fallbackView string, typed *app.View, env expr.Env, rr *render.AppRenderer) {
	body := m.renderViewWith(*typed, env, rr)
	if body == "" {
		body = m.renderViewSource(fallbackView)
	}
	m.entries = append(m.entries, transcriptEntry{
		body:        body,
		source:      fallbackView,
		typedView:   typed,
		renderEnv:   env,
		appRenderer: rr,
	})
	m.queue(body)
}

// renderView styles a parsed app.View for the transcript pane. It is
// the Phase-D dispatch site for the typed element pipeline (see
// internal/tui/elements/element.go): every element in the view is
// guard-filtered, pongo-expanded, and laid out at the current viewport
// wrap width.
//
// For the legacy scalar `view: <markdown>` form — which today's loader
// normalises to a single {Kind: "template", Source: <original>} element —
// the dispatcher delegates back into renderGlamour below, preserving
// every byte of the pre-Phase-D behaviour: Glamour with
// WithPreservedNewLines, preserveLeadingIndent on the source, and so on.
//
// TECH DEBT (2026-04-23): WithPreservedNewLines is the right call for
// structured views (Terminal Room's indented examples, menu-ish lists)
// but it also caps prose views at the author's hand-wrap width — a
// cloak foyer view authored at ~65 chars/line won't grow past 65 even
// on a 150-col terminal. Phase E/F migrate apps onto the typed
// `prose:` element which reflows to the viewport.
func (m *transcriptModel) renderView(v app.View) string {
	return m.renderViewWith(v, expr.Env{}, nil)
}

// renderViewWith is the typed-view-aware renderer the resize seam and
// the typed-view append paths share. env carries the runtime expr
// snapshot (world / slots / menu); rr is the per-app pongo2 renderer
// so {% include %} / {% extends %} inside leaf strings resolve via the
// app's per-app loader (Issue 1). Either may be the zero value / nil
// for legacy entry points — the elements dispatcher falls back to the
// package-level render.Pongo in that case (preserves the pre-Phase-D
// behaviour for system messages and reconstructed transcripts).
func (m *transcriptModel) renderViewWith(v app.View, env expr.Env, rr *render.AppRenderer) string {
	if v.IsEmpty() {
		return ""
	}
	wrap := m.wrapWidth()
	// Bridge: *render.AppRenderer satisfies elements.ViewRenderer via
	// its Render method, but a typed-nil pointer must be passed as
	// the interface nil so renderLeaf's nil check triggers correctly.
	var leafRR elements.ViewRenderer
	if rr != nil {
		leafRR = rr
	}
	// Legacy scalar `view:` interception. app.LegacyView normalises the
	// hand-authored markdown to one {Kind:"template"} element that renders
	// through renderGlamour — which uses WithPreservedNewLines and so caps
	// pure prose at the author's hand-wrap column (it never grows on a wide
	// panel). Split the source into blank-line blocks and route pure-prose
	// blocks through the reflowing `prose` element while leaving structured
	// blocks (lists, headings, indented examples) on the Glamour path. The
	// original view is kept for the on-error SourceString fallback.
	renderView := v
	if v.Source != "" && len(v.Elements) == 1 && v.Elements[0].Kind == "template" {
		if split := elements.SplitLegacyView(v.Source); len(split) > 0 {
			renderView = app.View{Source: v.Source, Elements: split}
		}
	}
	out, err := elements.RenderAll(renderView, env, wrap, m.renderGlamour, leafRR)
	if err != nil {
		// On a render error fall back to the raw source — better to show
		// the un-styled template body than to drop the turn entirely.
		// The error is non-fatal for the UI; the orchestrator log already
		// captures the typed view round-trip.
		return v.SourceString()
	}
	return out
}

// renderViewSource is the back-compat shim for call sites that today
// pass a pre-rendered view string (the orchestrator's result.View
// pipeline — every Append* entry point on this model). Wraps the string
// in a LegacyView so it goes through renderView's dispatcher (which
// routes single-template-element views back to the Glamour path).
func (m *transcriptModel) renderViewSource(text string) string {
	if text == "" {
		return ""
	}
	return m.renderView(app.LegacyView(text))
}

// renderGlamour is the post-Pongo Glamour step for the `template`
// element kind. Lifted out of the old renderMarkdown so the elements
// dispatcher can call it without importing glamour itself.
//
// Strips ANSI escape sequences from the input before rendering: when
// Glamour-styled content gets fed back through Glamour (the
// replayMetaTranscript path — assistant messages stored after a
// prior render), Glamour drops the leading 0x1b byte but leaves the
// bracket-code as literal text, producing output like
// `[1;38;2;16;185;129mStatus[0m`. Stripping ANSI on input ensures
// Glamour always sees clean markdown.
func (m *transcriptModel) renderGlamour(text string) string {
	if text == "" {
		return ""
	}
	if strings.ContainsRune(text, '\x1b') {
		text = ansi.Strip(text)
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

// wrapWidth returns the wrap width to use for the elements dispatcher.
// Must be ≤ Glamour's wrap budget MINUS Glamour's document margin, or
// long element lines (a wide-label list, a 78-char banner divider)
// land inside Glamour as paragraph text that exceeds its content
// budget and get re-wrapped onto a second line with the doc-margin
// indent. The visible symptom: the action list's hint column splits
// at ~22 chars even on a 150-col terminal.
//
// SetSize hands Glamour `viewportWidth - 2`; Glamour's auto/dark style
// reserves 2 cols of left margin out of that wrap budget for its own
// chrome. So the effective content area is `viewportWidth - 4`, which
// is what we hand the dispatcher here. The 40-char floor keeps a
// narrow terminal from collapsing to a one-word-per-line column.
func (m *transcriptModel) wrapWidth() int {
	w := m.vp.Width - 4
	if w < 40 {
		w = 40
	}
	return w
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

// AppendOffPathAnswer appends an agent reply styled with the soft off-path
// amber tint. The reply runs through the same Markdown pipeline as
// AppendTurn so formatting (lists, code blocks) renders cleanly. Pass
// userInput="" when the caller already appended the question header in a
// prior AppendTurn (the typical TUI submitOffPath sequence).
func (m *transcriptModel) AppendOffPathAnswer(userInput, answer string) {
	rendered := m.renderViewSource(answer)
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
		m.queue(turnHeaderStyle.Render(entry.header))
	}
	m.entries = append(m.entries, entry)
	m.queue(body)
}

// AppendMetaList appends the /meta list output as one transcript
// entry: a blue title banner, a header row (column names), one
// blue-styled padded row per chat, then a closing rule. Column widths
// are computed across rows AND the header so things line up. Bypasses
// the Markdown pipeline so lipgloss ANSI survives.
//
// Thin wrapper around AppendStyledTable so /sessions list (and any
// future list-style slash command) get the same look — see the
// generalized helper below for the styling source of truth.
func (m *transcriptModel) AppendMetaList(headers []string, rows [][]string) {
	m.AppendStyledTable("meta chats", headers, rows, "(no meta chats yet)")
}

// AppendStyledTable appends a rectangular table styled like
// /meta list — blue title banner, blue-bold column headers, blue
// rows — as one transcript entry. The Markdown pipeline is bypassed
// so lipgloss ANSI survives.
//
// Column widths are computed across both rows and the header so the
// columns line up. emptyMsg is rendered (in the row style) when rows
// is empty, otherwise one styled line per row.
//
// Used by /meta list and /sessions list; either caller can pass a
// different title to keep the section header semantically accurate
// while sharing the look.
func (m *transcriptModel) AppendStyledTable(title string, headers []string, rows [][]string, emptyMsg string) {
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
	leftBar := (totalWidth - runeLen(title) - 2) / 2
	if leftBar < 2 {
		leftBar = 2
	}
	rightBar := totalWidth - runeLen(title) - 2 - leftBar
	if rightBar < 2 {
		rightBar = 2
	}
	sb.WriteString(metaListHeaderStyle.Render(strings.Repeat("─", leftBar) + " " + title + " " + strings.Repeat("─", rightBar)))
	sb.WriteString("\n")
	sb.WriteString(metaListHeaderStyle.Render(render(headers)))
	sb.WriteString("\n")
	if len(rows) == 0 {
		if emptyMsg == "" {
			emptyMsg = "(no rows)"
		}
		sb.WriteString(metaListItemStyle.Render(emptyMsg))
		sb.WriteString("\n")
	} else {
		for _, r := range rows {
			sb.WriteString(metaListItemStyle.Render(render(r)))
			sb.WriteString("\n")
		}
	}
	sb.WriteString(metaListHeaderStyle.Render(strings.Repeat("─", totalWidth)))
	rendered := sb.String()
	m.entries = append(m.entries, transcriptEntry{body: rendered})
	m.queue(rendered)
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

// AppendMetaStreamLine appends a single muted "→ <text>" line to the
// transcript.  Used by the meta-mode stream observer to render live
// progress from a streaming agent call (tool calls, narration,
// retry notes) above the eventual final assistant reply.  The leading
// arrow matches AppendError / AppendGuardHint / AppendDisambig so the
// reader sees a consistent prefix for engine-side breadcrumbs.
//
// Empty text is a no-op; an empty muted arrow alone would be visual
// noise without information.
func (m *transcriptModel) AppendMetaStreamLine(text string) {
	if text == "" {
		return
	}
	body := lipgloss.NewStyle().
		Foreground(colorMuted).
		Italic(true).
		Render("→ " + text)
	m.entries = append(m.entries, transcriptEntry{body: body})
	m.queue(body)
}

// AppendRoomBanner emits a room-entry banner above the in-flight
// tool-call breadcrumbs. The orchestrator fires this when a turn
// lands in a new room (top-level state change) BEFORE its on_enter
// host calls dispatch, so a long agent / Bash / Read stream lands
// beneath the banner instead of leading it.
//
// banner is the pre-styled output from elements.Banner.Render (ANSI
// escapes intact); we don't re-style it here, only frame it with a
// leading blank line so it stands clear of the preceding routing
// breadcrumb.
func (m *transcriptModel) AppendRoomBanner(banner string) {
	if banner == "" {
		return
	}
	body := "\n" + banner
	m.entries = append(m.entries, transcriptEntry{body: body})
	m.queue(body)
}

// AppendMetaThinking renders an agent narration / "thinking" line —
// muted italic prose without a tool label. Visually distinct from
// tool-use rows (which call AppendMetaToolUse) so the scrollback
// reads as "what the agent is reasoning about" vs "what it ran."
func (m *transcriptModel) AppendMetaThinking(text string) {
	if text == "" {
		return
	}
	styled := lipgloss.NewStyle().
		Foreground(colorPrimary).
		Bold(true).
		Italic(true).
		Render("🧠 " + text)
	body := "\n" + styled
	m.entries = append(m.entries, transcriptEntry{body: body})
	m.queue(body)
}

// AppendMetaToolUse renders a tool-invocation breadcrumb: an accent-
// coloured bold tool name with a muted-grey args preview. Lands with
// a leading blank line so consecutive tool calls get breathing room
// between themselves and any prior thinking lines.
//
// Examples:
//
//	▸ Read /path/to/file.go
//	▸ Bash ls /worktrees
//
// args is allowed to be empty (some tool_use events ship without a
// preview); in that case only the tool name is shown.
func (m *transcriptModel) AppendMetaToolUse(tool, args string) {
	if tool == "" {
		return
	}
	toolPart := lipgloss.NewStyle().
		Foreground(colorAccent).
		Bold(true).
		Render(tool)
	row := "  ▸ " + toolPart
	if args != "" {
		row += " " + lipgloss.NewStyle().
			Foreground(colorMuted).
			Render(args)
	}
	// Leading newline for the visual gap; embedded in the queued body
	// so tea.Println produces a blank scrollback row before the
	// breadcrumb.
	body := "\n" + row
	m.entries = append(m.entries, transcriptEntry{body: body})
	m.queue(body)
}

// AppendMetaSystemNotice renders a parenthesised system breadcrumb
// like "(retrying claude request…)". Muted, no italic — the
// parentheses already mark it as engine-side narration.
func (m *transcriptModel) AppendMetaSystemNotice(text string) {
	if text == "" {
		return
	}
	body := lipgloss.NewStyle().
		Foreground(colorMuted).
		Render("  " + text)
	m.entries = append(m.entries, transcriptEntry{body: body})
	m.queue(body)
}

// AppendError appends an error/rejection message. The leading arrow
// matches AppendGuardHint and AppendClarification so a user reading the
// scrollback sees a consistent prefix for engine-side feedback.
//
// When userInput is empty, the header is omitted — the single-pane
// redesign already echoed the user's input via AppendUserInputEcho,
// so a second header would be a duplicate.
func (m *transcriptModel) AppendError(userInput, msg string) {
	body := errorStyle.Render("→ " + msg)
	entry := transcriptEntry{body: body}
	if userInput != "" {
		entry.header = "> " + userInput
		m.queue(turnHeaderStyle.Render(entry.header))
	}
	m.entries = append(m.entries, entry)
	m.queue(body)
}

// AppendWarning appends a non-fatal warning: something didn't take
// (e.g. a story edit failed to reload), but the session keeps running on
// its previous state. Styled amber with a ⚠ prefix so it reads as a
// heads-up rather than the error-red of a hard failure. Mirrors
// AppendError's header handling for a consistent "> input" anchor.
func (m *transcriptModel) AppendWarning(userInput, msg string) {
	body := warningStyle.Render("⚠ " + msg)
	entry := transcriptEntry{body: body}
	if userInput != "" {
		entry.header = "> " + userInput
		m.queue(turnHeaderStyle.Render(entry.header))
	}
	m.entries = append(m.entries, entry)
	m.queue(body)
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
	m.queue(body)
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
	m.queue(rendered)
}

func (m *transcriptModel) AppendClarification(userInput, message string) {
	if message == "" {
		return
	}
	var header string
	if userInput != "" {
		header = "> " + userInput
		m.queue(turnHeaderStyle.Render(header))
	}
	body := clarificationStyle.Render(message)
	m.entries = append(m.entries, transcriptEntry{header: header, body: body})
	m.queue(body)
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
// This is the read side of the continue-mode transcript rehydration path.
// Each recognised entry kind maps to the corresponding live
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

// LastBody returns the styled body of the most recently appended entry,
// or "" when the transcript is empty. The live TUI never re-renders this
// into View() (settled bodies live in scrollback via tea.Println); it is
// exposed so the frame composer can include the current room body in a
// single still Frame for headless callers (see ComposeFrame). Entries
// that carry only a header (a bare user-input echo) contribute no body,
// so this walks backwards to the last entry whose body is non-empty.
func (m *transcriptModel) LastBody() string {
	for i := len(m.entries) - 1; i >= 0; i-- {
		if m.entries[i].body != "" {
			return m.entries[i].body
		}
	}
	return ""
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
