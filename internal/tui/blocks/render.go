package blocks

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Mode controls the prompt prefix glyph and (indirectly via the theme
// chosen by the caller) the prompt colour. The mode-prefix glyphs are
// the chat-view input vocabulary (docs/tui/README.md "Input, menu,
// inbox, meta-mode"): `> ` normal, `» ` meta, `# ` off-path,
// `? ` slot-filling, `… ` awaiting LLM.
type Mode int

const (
	ModeNormal Mode = iota
	ModeMeta
	ModeOffPath
	ModeSlotFilling
	ModeAwaitingLLM
)

func (m Mode) prefix() string {
	switch m {
	case ModeMeta:
		return "» "
	case ModeOffPath:
		return "# "
	case ModeSlotFilling:
		return "? "
	case ModeAwaitingLLM:
		return "… "
	default:
		return "> "
	}
}

// ─── Header ──────────────────────────────────────────────────────────────

// Header renders the one-line context bar at the top of the pane:
//
//	proposing · cypilot                                  on-path
//
// In the live TUI it's drawn outside the viewport; the preview prints
// it inline so the full chat view is one stream.
func (r *Renderer) Header(location, room string) string {
	left := strings.TrimSpace(location + " · " + room)
	style := r.style(r.Theme.Text, r.Theme.Primary, true, false).Padding(0, 1)
	bar := style.Render(left)
	// Pad to width so the background colour spans the full row when
	// rendered to a wide terminal.
	if w := r.Width - lipgloss.Width(bar); w > 0 {
		bar += r.style(r.Theme.Text, r.Theme.Primary, false, false).Render(strings.Repeat(" ", w))
	}
	return bar
}

// ─── Divider + status row (prompt chrome) ──────────────────────────────

// Divider renders a single full-width horizontal rule. Used above
// the prompt to mark "your input goes here" visually.
func (r *Renderer) Divider() string {
	w := r.Width
	if w < 1 {
		w = 1
	}
	return r.style(r.Theme.Border, nil, false, false).
		Render(strings.Repeat("─", w))
}

// StatusRow renders a single-line colour-banded status row.
//
// Invariant: the styled output is EXACTLY r.Width visible columns,
// composed as plain text BEFORE styling is applied. This avoids
// lipgloss's `Width(n)` padding from hard-wrapping or leaking the
// background colour into the next line — which was the source of
// the earlier "footer garbled into transcript" bug.
//
// Layout: left content, padding, right content (right-aligned).
// If they overflow w columns, the left side is truncated so the
// right side (typically the mode label — high-signal) stays
// visible.
func (r *Renderer) StatusRow(left, right string) string {
	w := r.Width
	if w < 1 {
		w = 1
	}
	// lipgloss.Width measures visible display columns (handles
	// ANSI + wide runes); using it instead of rune count avoids
	// off-by-one bleed on multi-cell glyphs.
	lW, rW := lipgloss.Width(left), lipgloss.Width(right)
	// Reserve at least 3 columns of gap between left and right.
	if lW+rW+3 > w {
		room := w - rW - 3
		if room < 1 {
			left, lW = "", 0
		} else {
			left = truncate(left, room)
			lW = lipgloss.Width(left)
		}
	}
	gap := w - lW - rW
	if gap < 1 {
		gap = 0
	}
	row := left + strings.Repeat(" ", gap) + right
	// One more safety pass: ensure the composed row is exactly w
	// display columns. Truncate any overflow before styling so
	// lipgloss never sees a row wider than w (which would trigger
	// hard-wrap + bleed). Pad with spaces if shorter (so the
	// background fills the row).
	if rowW := lipgloss.Width(row); rowW > w {
		row = truncate(row, w)
	} else if rowW < w {
		row += strings.Repeat(" ", w-rowW)
	}
	// Now apply colour — the row is exactly w wide, lipgloss won't
	// pad further, and the background ends cleanly at column w.
	return r.style(r.Theme.Text, r.Theme.Primary, true, false).Render(row)
}

// ─── Welcome block ───────────────────────────────────────────────────────

// Welcome holds the data shown in the startup welcome banner — a
// Claude-Code-style bordered intro that prints once into scrollback
// and scrolls off naturally as the user takes turns.
type Welcome struct {
	// Title is the headline (e.g. "kitsoki · cypilot"). Rendered bold.
	Title string
	// Subtitle is the second line, smaller (e.g. version + author).
	// Optional.
	Subtitle string
	// Hints is the short list of next-step commands (e.g.
	// "/help for commands · /world to inspect state"). Rendered
	// muted. Each entry becomes its own line.
	Hints []string
	// Status is the optional bottom-row context line (e.g.
	// "session: sess_42 · room: idle"). Rendered muted-italic.
	Status string
	// Logo, when true, renders the Kitsoki "Mesa Sun" pixel-art mark to
	// the left of the text column. Brand colours are fixed (the desert
	// palette) regardless of the active theme; see [mesaSunArt].
	Logo bool
}

// mesaSunArt renders the Kitsoki "Mesa Sun" mark (docs/branding/logo.md) as
// compact terminal pixel-art: a five-rayed desert sun above a terraced,
// stepped pueblo with a single dark doorway, on a rust ground band. Each
// line is padded to a fixed 10-column canvas so the column joins cleanly
// beside the welcome text. When noColor is set the desert tints drop but
// the block silhouette still reads.
func mesaSunArt(noColor bool) string {
	st := func(c lipgloss.Color) lipgloss.Style {
		if noColor {
			return lipgloss.NewStyle()
		}
		return lipgloss.NewStyle().Foreground(c)
	}
	gold, clay, adobe, rust, door := st(brandGold), st(brandClay), st(brandAdobe), st(brandRust), st(brandShadow)
	lines := []string{
		gold.Render(`  \  |  / `), // rays: up-left, up, up-right
		gold.Render(` ── ███ ──`), // horizontal rays + sun
		gold.Render(`    ███   `), // sun base
		clay.Render(`    ▟█▙   `), // raised central block
		adobe.Render(`   ▟███▙  `), // upper terrace (lighter tier)
		clay.Render(`  ▟█████▙ `), // lower terrace
		"  " + clay.Render("███") + door.Render("█") + clay.Render("███") + " ", // ground floor + doorway
		rust.Render(` █████████`), // ground band
	}
	return strings.Join(lines, "\n")
}

// WelcomeBlock renders the multi-line bordered welcome banner. Boxed
// with the active theme's accent so it visually anchors the top of
// the scrollback. Width is the available terminal columns; the box
// auto-fits with a small right margin.
func (r *Renderer) WelcomeBlock(w Welcome) string {
	innerW := r.Width - 4
	if innerW < 40 {
		innerW = 40
	}

	var lines []string
	if w.Title != "" {
		marker := "✻ "
		if r.NoColor {
			marker = "* "
		}
		title := r.style(r.Theme.Accent, nil, true, false).Render(marker + w.Title)
		lines = append(lines, title)
	}
	if w.Subtitle != "" {
		lines = append(lines, r.style(r.Theme.Muted, nil, false, false).Render("  "+w.Subtitle))
	}
	if len(w.Hints) > 0 || w.Status != "" {
		lines = append(lines, "") // blank separator
	}
	for _, h := range w.Hints {
		lines = append(lines, r.style(r.Theme.Muted, nil, false, false).Render("  "+h))
	}
	if w.Status != "" {
		if len(w.Hints) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, r.style(r.Theme.Muted, nil, false, true).Render("  "+w.Status))
	}

	body := strings.Join(lines, "\n")
	// Brand logo column to the left of the text, top-aligned so the mark's
	// crown lines up with the title. JoinHorizontal pads the shorter block
	// to match heights.
	if w.Logo {
		body = lipgloss.JoinHorizontal(lipgloss.Top, mesaSunArt(r.NoColor), "   ", body)
	}
	// Thick border that spans the full terminal width — gives the
	// welcome banner the heft the user asked for ("consume the
	// full width and have a thicker border"). Width includes the
	// border, so we set Width(r.Width) on the styled box.
	border := lipgloss.NewStyle().
		Border(lipgloss.ThickBorder()).
		Padding(0, 2).
		Width(r.Width - 2)
	if !r.NoColor {
		border = border.BorderForeground(r.Theme.Accent)
	}
	return border.Render(body)
}

// ─── User turn ───────────────────────────────────────────────────────────

// UserTurn renders the immediate echo of the user's submitted text.
// Printed the instant Enter is pressed, for immediate input feedback.
func (r *Renderer) UserTurn(text string) string {
	return r.style(r.Theme.Primary, nil, true, false).Render("> " + text)
}

// QueuedEcho renders a user submission that was captured by the
// input queue while a turn was in flight. Visually distinct from a
// normal UserTurn: warning-tinted, with a "↳ queued" tag and the
// current queue depth. The line lands in scrollback so the user has
// a permanent record of every queued submission.
func (r *Renderer) QueuedEcho(text string, depth int) string {
	style := r.style(r.Theme.Warning, nil, false, true)
	tag := "↳ queued"
	if depth > 1 {
		tag = fmt.Sprintf("↳ queued · %d in queue", depth)
	}
	return style.Render(tag + "   " + text)
}

// ─── Routing status (live-updated) ───────────────────────────────────────

// RoutingPhase is the in-flight phase of the routing pipeline. Rendered
// as an italic muted line directly under the user turn while routing
// is unresolved.
type RoutingPhase string

const (
	PhaseDeterministic RoutingPhase = "deterministic"
	PhaseSynonyms      RoutingPhase = "synonyms"
	PhaseSlotParser    RoutingPhase = "slot-parser"
	PhaseCache         RoutingPhase = "cache"
	PhaseLLM           RoutingPhase = "LLM"
)

// RoutingStatus renders an in-flight routing line attached to the user
// turn. Live-updated as the pipeline advances; settles into
// RoutingResolved once a tier hits.
func (r *Renderer) RoutingStatus(phase RoutingPhase) string {
	return r.style(r.Theme.Muted, nil, false, true).Render("  routing: " + string(phase) + "…")
}

// RoutingSource enumerates the tiers that can resolve an input. Drives
// the trailer on RoutingResolved (deterministic · 1.00, LLM · 0.84, …).
type RoutingSource string

const (
	SourceDeterministic RoutingSource = "deterministic"
	SourceSynonym       RoutingSource = "synonym"
	SourceSlotParser    RoutingSource = "slot-parser"
	SourceCache         RoutingSource = "cached"
	SourceLLM           RoutingSource = "LLM"
	SourceOffPath       RoutingSource = "off-path"
	SourceUnknown       RoutingSource = "unknown"
	SourceAmbiguous     RoutingSource = "ambiguous"
)

// Resolved is the settled routing line that replaces the in-flight
// RoutingStatus once the pipeline finishes. Kind is one of
// nav | view | system | in-room | off-path; detail varies per source —
// slots for slot-parser, confidence for LLM, blank for deterministic.
// The settled-line format is documented in docs/tui/README.md
// ("Observers: engine events → transcript").
type Resolved struct {
	Kind       string
	Intent     string
	Source     RoutingSource
	Confidence float64
	Detail     string
}

// RoutingResolved renders the settled resolution line. Format (see
// docs/tui/README.md "Observers: engine events → transcript"):
//
//	→ nav: back   (deterministic · 1.00)
//	→ in-room: pick_branch   (LLM · 0.84)   slots: {branch: "main"}
//	→ ?: need clarification
func (r *Renderer) RoutingResolved(res Resolved) string {
	var body string
	switch res.Source {
	case SourceAmbiguous:
		body = "? need clarification:"
	case SourceUnknown:
		body = "(unknown command: " + res.Intent + ") — try /help"
	case SourceOffPath:
		body = "→ off-path message"
	default:
		body = fmt.Sprintf("→ %s: %s   (%s", res.Kind, res.Intent, res.Source)
		switch res.Source {
		case SourceDeterministic, SourceSynonym:
			body += " · 1.00"
		case SourceLLM:
			body += fmt.Sprintf(" · %.2f", res.Confidence)
		}
		body += ")"
		if res.Detail != "" {
			body += "   " + res.Detail
		}
	}
	style := r.routingSourceStyle(res.Source)
	return style.Render("  " + body)
}

func (r *Renderer) routingSourceStyle(s RoutingSource) lipgloss.Style {
	switch s {
	case SourceDeterministic:
		return r.style(r.Theme.Accent, nil, false, false)
	case SourceSynonym:
		return r.style(r.Theme.Accent, nil, false, false)
	case SourceSlotParser:
		return r.style(r.Theme.Info, nil, false, false)
	case SourceCache:
		return r.style(r.Theme.Info, nil, false, false)
	case SourceLLM:
		return r.style(r.Theme.Primary, nil, false, false)
	case SourceOffPath:
		return r.style(r.Theme.Warning, nil, false, true)
	case SourceAmbiguous:
		return r.style(r.Theme.Warning, nil, false, true)
	case SourceUnknown:
		return r.style(r.Theme.Error, nil, false, true)
	default:
		return r.style(r.Theme.Muted, nil, false, false)
	}
}

// ─── Agent turn ──────────────────────────────────────────────────────────

// AgentTurn renders an agent response body. Phase 0 keeps this plain —
// the live transcript wires this back into the typed-element + Glamour
// pipeline, but the preview demonstrates layout, not Markdown styling.
//
// Multi-paragraph input is rendered with a hanging indent so it visually
// belongs to the preceding user turn.
func (r *Renderer) AgentTurn(body string) string {
	body = wrapPlain(body, r.Width-2)
	body = indent(body, "  ")
	return r.style(r.Theme.Text, nil, false, false).Render(body)
}

// ─── System notice ───────────────────────────────────────────────────────

// SystemNotice renders a single-line dim notice ("· session resumed,
// turn 4"). The leading "·" marks it as engine-side narration rather
// than user content.
func (r *Renderer) SystemNotice(text string) string {
	return r.style(r.Theme.Muted, nil, false, true).Render("· " + text)
}

// SlashOutput renders a "(...)"-style slash-command feedback line in
// the blue info colour. Mirrors slashOutputStyle from the live TUI.
func (r *Renderer) SlashOutput(text string) string {
	return r.style(r.Theme.Info, nil, true, false).Render(text)
}

// ─── Menu / actions block ───────────────────────────────────────────────

// MenuAction is one row in the current room's actions block. Available
// is the guard outcome — false rows render in muted/blocked style.
type MenuAction struct {
	Index     int
	Name      string
	Label     string
	Available bool
	GuardHint string
}

// Menu renders the room's actions block. By default it's a numbered
// list; rooms can later override the pongo template (docs/tui/README.md
// "The view pipeline: typed elements + pongo2" — rendering is
// room-provided). Phase 0 ships the default.
func (r *Renderer) Menu(actions []MenuAction) string {
	if len(actions) == 0 {
		return r.style(r.Theme.Muted, nil, false, true).Render("  (no actions available)")
	}
	var sb strings.Builder
	sb.WriteString(r.style(r.Theme.Info, nil, true, false).Render("  actions:"))
	sb.WriteString("\n")
	for _, a := range actions {
		num := fmt.Sprintf("  %d. ", a.Index)
		label := a.Label
		if label == "" {
			label = a.Name
		}
		var line string
		if a.Available {
			line = num + label
			sb.WriteString(r.style(r.Theme.Accent, nil, false, false).Render(line))
		} else {
			line = num + label
			sb.WriteString(r.style(r.Theme.Error, nil, false, true).Render(line))
			if a.GuardHint != "" {
				sb.WriteString("  ")
				sb.WriteString(r.style(r.Theme.Muted, nil, false, true).Render("(" + a.GuardHint + ")"))
			}
		}
		sb.WriteString("\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

// ─── Clarification (inline slot-fill prompt) ────────────────────────────

// ClarificationSlot describes one missing slot rendered into the inline
// "Clarification needed" block. Mirrors orchestrator.SlotNeed but lives
// in blocks so the renderer has no dependency on orchestrator types.
type ClarificationSlot struct {
	Name        string
	Prompt      string
	Description string
	Type        string
	Values      []string
	FormatHint  string
	Examples    []string
}

// Clarification renders the inline "Clarification needed" block that
// replaces the legacy clarify modal. For enum/bool slots it shows a
// numbered choice list (pick by number or by value); for free-form
// slots it shows the prompt + examples and tells the user to type a
// value. The user replies via the normal prompt — Enter submits.
//
// intentName is the intent we are gathering slots for, current is the
// 0-based index of the active slot, total is len(slots).
func (r *Renderer) Clarification(intentName string, current, total int, slot ClarificationSlot) string {
	var sb strings.Builder
	header := fmt.Sprintf("? clarification needed for '%s' (%d/%d)",
		intentName, current+1, total)
	sb.WriteString(r.style(r.Theme.Warning, nil, true, false).Render(header))
	sb.WriteString("\n")

	prompt := slot.Prompt
	if prompt == "" {
		prompt = slot.Description
	}
	if prompt == "" {
		prompt = "Please provide: " + slot.Name
	}
	sb.WriteString(r.style(r.Theme.Text, nil, false, false).Render("  " + prompt))
	sb.WriteString("\n")

	values := slot.Values
	if slot.Type == "bool" && len(values) == 0 {
		values = []string{"true", "false"}
	}
	if len(values) > 0 {
		// Numbered choice list — pick by number or by value.
		for i, v := range values {
			line := fmt.Sprintf("    %d. %s", i+1, v)
			sb.WriteString(r.style(r.Theme.Accent, nil, false, false).Render(line))
			sb.WriteString("\n")
		}
		hint := "  (type a number or the value, Esc to cancel)"
		sb.WriteString(r.style(r.Theme.Muted, nil, false, true).Render(hint))
	} else {
		if slot.FormatHint != "" {
			sb.WriteString(r.style(r.Theme.Muted, nil, false, true).
				Render("    hint: " + slot.FormatHint))
			sb.WriteString("\n")
		}
		if len(slot.Examples) > 0 {
			sb.WriteString(r.style(r.Theme.Muted, nil, false, true).
				Render("    examples: " + strings.Join(slot.Examples, ", ")))
			sb.WriteString("\n")
		}
		hint := "  (type a value, Esc to cancel)"
		sb.WriteString(r.style(r.Theme.Muted, nil, false, true).Render(hint))
	}
	return sb.String()
}

// ─── Disambiguation (inline candidate picker) ───────────────────────────

// DisambigCandidate describes one candidate rendered into the inline
// "Did you mean?" block. Mirrors intent.Candidate but lives in blocks
// so the renderer has no dependency on intent types.
type DisambigCandidate struct {
	Intent      string
	Title       string
	Description string
	Why         string
}

// Disambig renders the inline "Did you mean?" candidate list that
// replaces the legacy disambiguation modal. The user picks by typing
// the number (1-9) or the canonical intent name in the normal prompt.
func (r *Renderer) Disambig(candidates []DisambigCandidate) string {
	var sb strings.Builder
	sb.WriteString(r.style(r.Theme.Warning, nil, true, false).
		Render("? did you mean:"))
	sb.WriteString("\n")
	for i, c := range candidates {
		if i >= 9 {
			break
		}
		title := c.Title
		if title == "" {
			title = c.Intent
		}
		line := fmt.Sprintf("    %d. %s", i+1, title)
		sb.WriteString(r.style(r.Theme.Accent, nil, false, false).Render(line))

		desc := c.Why
		if desc == "" {
			desc = c.Description
		}
		if desc != "" {
			sb.WriteString(r.style(r.Theme.Muted, nil, false, true).
				Render(" — " + truncate(desc, 60)))
		}
		sb.WriteString("\n")
	}
	hint := "  (type a number or the intent name, Esc to cancel)"
	sb.WriteString(r.style(r.Theme.Muted, nil, false, true).Render(hint))
	return sb.String()
}

// ─── Inbox notification ─────────────────────────────────────────────────

// InboxNotification is one inline notification. Phase 0 renders only
// the inline form; the panel-mode renderings are intentionally not here
// (they're being removed in Phase 3).
type InboxNotification struct {
	ID       string
	Title    string
	Severity string // "info" | "action_required"
	Age      string // human-readable, e.g. "2m ago"
}

// Inbox renders a single notification line. Severity action_required
// gets a warning tint so it stands out without needing a panel badge.
func (r *Renderer) Inbox(n InboxNotification) string {
	prefix := "📨"
	if r.NoColor {
		prefix = "[!]"
	}
	body := fmt.Sprintf("%s  %s", prefix, n.Title)
	if n.Age != "" {
		body += "   "
		body += r.style(r.Theme.Muted, nil, false, true).Render("(" + n.Age + ")")
	}
	style := r.style(r.Theme.Info, nil, true, false)
	if n.Severity == "action_required" {
		style = r.style(r.Theme.Warning, nil, true, false)
	}
	return style.Render(body)
}

// BackgroundComplete renders the one-line completion summary printed
// in the user's current room when a different room's queue finishes
// in the background.
func (r *Renderer) BackgroundComplete(room, summary string) string {
	line := fmt.Sprintf("✓ %s · %s", room, summary)
	return r.style(r.Theme.Accent, nil, true, false).Render(line)
}

// ─── Routing trace ──────────────────────────────────────────────────────

// TraceEvent is one row in the full pipeline trace printed by /trace.
type TraceEvent struct {
	Tier   string // "deterministic" / "synonyms" / "slot-parser" / "cache" / "LLM"
	Result string // "miss" / "hit" / "ambiguous"
	Detail string
}

// RoutingTrace renders the multi-line pipeline trace. Each event is
// one line; the trace is intentionally low-decoration so the data
// reads as a log.
func (r *Renderer) RoutingTrace(events []TraceEvent) string {
	var sb strings.Builder
	sb.WriteString(r.style(r.Theme.Info, nil, true, false).Render("── routing trace ──"))
	sb.WriteString("\n")
	for _, ev := range events {
		var marker string
		var style lipgloss.Style
		switch ev.Result {
		case "hit":
			marker = "  ✓"
			style = r.style(r.Theme.Accent, nil, false, false)
		case "ambiguous":
			marker = "  ?"
			style = r.style(r.Theme.Warning, nil, false, true)
		default:
			marker = "  ·"
			style = r.style(r.Theme.Muted, nil, false, true)
		}
		line := fmt.Sprintf("%s %-13s %s   %s", marker, ev.Tier, ev.Result, ev.Detail)
		sb.WriteString(style.Render(strings.TrimRight(line, " ")))
		sb.WriteString("\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

// ─── Footer ─────────────────────────────────────────────────────────────

// Footer renders the two-line status footer above the prompt. line1 is
// the framework default (room · state · mode · queue · unread). line2
// is the story/room pongo2 template (empty by default).
//
// Both lines are truncated with an ellipsis at width — overflow is the
// author's problem to elide via pongo conditionals.
func (r *Renderer) Footer(line1, line2 string) string {
	out := r.style(r.Theme.Muted, nil, false, false).Render(truncate(line1, r.Width))
	if line2 != "" {
		out += "\n" + r.style(r.Theme.Muted, nil, false, true).Render(truncate(line2, r.Width))
	}
	return out
}

// ─── Prompt ─────────────────────────────────────────────────────────────

// Prompt renders the single-line input area. The prefix glyph reflects
// the current Mode. The body — a real textarea in the live TUI — is a
// placeholder underscore here.
func (r *Renderer) Prompt(mode Mode) string {
	prefix := mode.prefix()
	return r.style(r.Theme.Primary, nil, true, false).Render(prefix) + "_"
}

// ─── World (dedicated view stub) ────────────────────────────────────────

// WorldNode is one row in the hierarchical world view. Phase 0 ships
// the layout stub; Phase 1.5 wires it to the live world object.
type WorldNode struct {
	Key      string
	Value    string
	Expanded bool
	Depth    int
	HasKids  bool
	Selected bool
}

// World renders the dedicated /world view body. Header and footer hint
// are rendered separately so the preview can drive them as needed.
func (r *Renderer) World(nodes []WorldNode) string {
	var sb strings.Builder
	for i, n := range nodes {
		if i > 0 {
			sb.WriteString("\n")
		}
		indentStr := strings.Repeat("  ", n.Depth)
		var glyph string
		switch {
		case n.HasKids && n.Expanded:
			glyph = "▾"
		case n.HasKids:
			glyph = "▸"
		default:
			glyph = " "
		}
		line := fmt.Sprintf("%s%s %s", indentStr, glyph, n.Key)
		if n.Value != "" {
			line += ": " + n.Value
		}
		style := r.style(r.Theme.Text, nil, false, false)
		if n.Selected {
			style = r.style(r.Theme.Text, r.Theme.Primary, true, false)
		}
		sb.WriteString(style.Render(line))
	}
	return sb.String()
}

// WorldFooterHint renders the keybinding line at the bottom of the
// /world dedicated view.
func (r *Renderer) WorldFooterHint() string {
	hint := "view: world  ·  ↑/↓ navigate  ·  enter expand  ·  e edit  ·  q close"
	return r.style(r.Theme.Muted, nil, false, true).Render(hint)
}

// ─── Helpers ────────────────────────────────────────────────────────────

// truncate cuts s to width characters, appending an ellipsis. Pure
// rune-length truncation; no ANSI-aware truncation here because
// callers pass plain strings to Footer (lipgloss styling is applied
// after).
func truncate(s string, width int) string {
	if width <= 0 || len([]rune(s)) <= width {
		return s
	}
	runes := []rune(s)
	if width < 2 {
		return string(runes[:width])
	}
	return string(runes[:width-1]) + "…"
}

// wrapPlain wraps s at width using simple word-boundary wrapping. Good
// enough for the agent-turn preview; the live TUI uses Glamour for
// real markdown styling.
func wrapPlain(s string, width int) string {
	if width <= 0 {
		return s
	}
	var out strings.Builder
	for li, line := range strings.Split(s, "\n") {
		if li > 0 {
			out.WriteByte('\n')
		}
		words := strings.Fields(line)
		col := 0
		for wi, w := range words {
			runeW := len([]rune(w))
			if wi == 0 {
				out.WriteString(w)
				col = runeW
				continue
			}
			if col+1+runeW > width {
				out.WriteByte('\n')
				out.WriteString(w)
				col = runeW
				continue
			}
			out.WriteByte(' ')
			out.WriteString(w)
			col += 1 + runeW
		}
	}
	return out.String()
}

// indent prefixes every line of s with prefix.
func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}
