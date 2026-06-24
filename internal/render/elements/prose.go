package elements

import (
	"strings"

	"github.com/muesli/reflow/wordwrap"

	"kitsoki/internal/app"
	"kitsoki/internal/expr"
)

// Prose is one reflowable narration paragraph. Source may carry pongo2
// template syntax; Render expands it through render.Pongo before
// reflowing. Author line breaks inside Source are collapsed into spaces —
// prose is a single logical paragraph that the renderer is free to
// rewrap to fit the viewport.
//
// Authors who want hard line breaks should use `code` (preserves layout
// exactly) or split into multiple `prose:` elements (each renders as its
// own paragraph with a blank line in between, courtesy of the dispatcher).
//
// The zero value (empty Source) is usable and renders to "".
type Prose struct {
	Source string
}

// Render reflows the prose body to the supplied width.
func (p Prose) Render(width int, env expr.Env, rr ViewRenderer) (string, error) {
	body, err := renderLeaf(rr, p.Source, env)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(body) == "" {
		return "", nil
	}
	// Collapse author-authored whitespace runs (including newlines) into
	// single spaces so the wrapper sees a single paragraph. YAML folded
	// scalars (`>`) already do this on load; this guard also handles
	// the literal (`|`) and quoted-string forms where the author may
	// have hand-wrapped at a width different from the viewport.
	body = collapseWhitespace(body)
	if width < 1 {
		return body, nil
	}
	return wordwrap.String(body, width), nil
}

// SplitLegacyView splits a legacy scalar `view:` Source — the original
// hand-authored markdown that app.LegacyView normalises to one
// {Kind:"template"} element — into a synthetic element list that lets pure
// prose reflow while keeping structured content byte-identical.
//
// The source is split on blank lines into blocks. Each block is classified
// with looksStructuredLine: a block that contains ANY structured line
// (bullets, headings, blockquotes, ordered-list markers, fences, table
// cells, or a line with ≥2 leading spaces of intentional indentation) is
// emitted as a {Kind:"template"} element, so it still renders through the
// caller's Glamour pipeline with WithPreservedNewLines and its author
// layout is preserved exactly. A block with no structured line is pure
// prose and is emitted as a {Kind:"prose"} element, so it renders through
// Prose — collapsing author line breaks and reflowing to the viewport
// width instead of staying pinned at the author's hand-wrap column.
//
// Returns nil when source is empty or splits into no non-blank blocks; the
// caller falls back to the original single-template view in that case.
func SplitLegacyView(source string) []app.ViewElement {
	blocks := splitLegacyBlocks(source)
	if len(blocks) == 0 {
		return nil
	}
	els := make([]app.ViewElement, 0, len(blocks))
	for _, b := range blocks {
		kind := "prose"
		if blockIsStructured(b) {
			kind = "template"
		}
		els = append(els, app.ViewElement{Kind: kind, Source: b})
	}
	return els
}

// splitLegacyBlocks splits source into blank-line-separated blocks. Blank
// (whitespace-only) lines are the separators and are dropped; the synthetic
// view's inter-element spacing re-inserts the paragraph break at render
// time. Internal (non-blank) lines of a block are preserved verbatim, so a
// structured block hands Glamour exactly the bytes it had before the split.
func splitLegacyBlocks(source string) []string {
	var (
		blocks []string
		cur    []string
	)
	flush := func() {
		if len(cur) > 0 {
			blocks = append(blocks, strings.Join(cur, "\n"))
			cur = nil
		}
	}
	for _, line := range strings.Split(source, "\n") {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		cur = append(cur, line)
	}
	flush()
	return blocks
}

// blockIsStructured reports whether any line in the block is a structured
// line — if so the whole block is kept on the template/Glamour path.
func blockIsStructured(block string) bool {
	for _, line := range strings.Split(block, "\n") {
		if looksStructuredLine(line) {
			return true
		}
	}
	return false
}

// looksStructuredLine classifies a single raw line as structured (markdown
// layout the author intends to keep) versus pure prose (free to reflow).
func looksStructuredLine(raw string) bool {
	// Intentional indentation: two or more leading spaces signal an author
	// who wants the layout preserved (Terminal Room's indented examples,
	// nested bullets, aligned columns).
	if strings.HasPrefix(raw, "  ") {
		return true
	}
	trimmed := strings.TrimLeft(raw, " \t")
	if trimmed == "" {
		return false
	}
	switch {
	case strings.HasPrefix(trimmed, "```"): // fenced code block
		return true
	case strings.HasPrefix(trimmed, "#"): // heading
		return true
	case strings.HasPrefix(trimmed, ">"): // blockquote
		return true
	case strings.HasPrefix(trimmed, "|"): // table cell
		return true
	}
	if isBulletPrefix(trimmed) {
		return true
	}
	return isOrderedListPrefix(trimmed)
}

// isBulletPrefix reports whether trimmed begins with a markdown bullet
// marker (`- `, `* `, `+ `). The trailing space is required so a hyphenated
// word or an `*emphasis*` run isn't misread as a list item.
func isBulletPrefix(trimmed string) bool {
	if len(trimmed) < 2 {
		return false
	}
	switch trimmed[0] {
	case '-', '*', '+':
		return trimmed[1] == ' '
	}
	return false
}

// isOrderedListPrefix reports whether trimmed begins with an ordered-list
// marker: one or more digits followed by `.` or `)` (e.g. `1.`, `12)`).
func isOrderedListPrefix(trimmed string) bool {
	i := 0
	for i < len(trimmed) && trimmed[i] >= '0' && trimmed[i] <= '9' {
		i++
	}
	if i == 0 || i >= len(trimmed) {
		return false
	}
	return trimmed[i] == '.' || trimmed[i] == ')'
}

// collapseWhitespace replaces runs of whitespace (including '\n') with a
// single ASCII space and trims the result. Mirrors the behaviour an
// author gets from a YAML folded scalar (`view: >`) — the contract a
// `prose:` element promises.
func collapseWhitespace(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	inSpace := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			if !inSpace {
				sb.WriteByte(' ')
				inSpace = true
			}
			continue
		}
		sb.WriteRune(r)
		inSpace = false
	}
	return strings.TrimSpace(sb.String())
}
