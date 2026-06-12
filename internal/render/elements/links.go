package elements

import (
	"path/filepath"
	"regexp"

	"github.com/charmbracelet/lipgloss"
)

// links.go — the single home for terminal-hyperlink (OSC 8) construction
// in the renderer. The kv element calls these to turn a markdown artifact
// path into a clickable link on terminals that support OSC 8; everything
// else (older terminals, multiplexers without passthrough) sees the plain
// path and falls back to the `/open` slash command.
//
// Detection mirrors the web's isMarkdownPath
// (tools/runstatus/src/components/ViewElement.vue:127) so web and TUI agree
// on what a "linkable artifact" is without inventing a new schema field —
// per the review-externally epic's shared decision #2.

// markdownPathRe matches a value whose (trimmed) text ends in ".md" — the
// same predicate the web uses to decide a kv value is a clickable artifact
// path (`/\S+\.md$/`). Mirrored, not shared, so the two surfaces stay in
// lock-step without a cross-package dependency.
var markdownPathRe = regexp.MustCompile(`\S+\.md$`)

// isMarkdownPath reports whether s names a markdown artifact the operator
// can open — the TUI mirror of the web's isMarkdownPath. The value is
// expected pre-trimmed by the caller (the kv renderer linkifies only a
// single, already-fitting path token).
func isMarkdownPath(s string) bool {
	return markdownPathRe.MatchString(s)
}

// osc8Underline style — a lipgloss underline applied to the visible link
// text so a clickable path reads as a link even before the operator hovers.
// Foreground is left to the terminal so the link inherits the surrounding
// kv color.
var osc8Underline = lipgloss.NewStyle().Underline(true)

// osc8Link wraps visible text in an OSC 8 hyperlink pointing at the
// absolute file:// URL for path, with a lipgloss underline on the visible
// run. The escape sequence is:
//
//	\x1b]8;;file://<abs>\x1b\<text>\x1b]8;;\x1b\
//
// The opening and closing OSC 8 introducers carry zero visible width — the
// only on-screen glyphs are text — so callers must do their column math on
// text, never on the returned string. Terminals without OSC 8 support drop
// the escape and render text plain.
//
// path is resolved to an absolute path for the file:// target; the visible
// text is left exactly as supplied (typically the original relative path)
// so the column/width math is byte-identical to the plain render.
func osc8Link(text, path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		// Can't form a stable target — render plain rather than a link to
		// a relative URL the OS would resolve unpredictably.
		return text
	}
	const (
		osc = "\x1b]8;;"
		st  = "\x1b\\"
	)
	return osc + "file://" + abs + st + osc8Underline.Render(text) + osc + st
}
