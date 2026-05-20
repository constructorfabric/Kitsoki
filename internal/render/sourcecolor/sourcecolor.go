// Package sourcecolor distinguishes templated text from LLM-generated
// text in TUI output by switching the terminal background color at
// the source boundary.
//
// # The contract
//
// LLM operators wrap their string outputs with [Wrap] at the result
// boundary, before the value enters world state. The wrapped text
// flows through pongo rendering, world serialization, transcript
// queuing, and hard-wrapping unchanged: the sentinels are zero-width
// Unicode characters, so they have no visible footprint and do not
// disturb width-based layout. At the final write to the terminal,
// [Colorize] walks the string and converts each sentinel pair into an
// ANSI background-color switch:
//
//   - cool slate background for templated / deterministic text
//   - warm bronze background for LLM-generated text
//
// Nesting is handled with a background-color stack: entering an LLM
// span pushes the warm bg; exiting pops and restores the parent's bg
// (never a bare reset). Multi-line LLM spans are detected as "blocks"
// and each contained line is padded to the requested width so the
// warm band reads as a solid rectangle.
//
// # Why two sentinels of only zero-width characters
//
// Earlier drafts used a sentinel like "​⁣LLM⁣" where
// the visible "LLM" letters carried 3 columns of width. Any
// width-aware truncator (lipgloss, runewidth, ansi.Hardwrap) could
// then split the sentinel mid-letter and corrupt the marker. The
// production sentinels here use only U+2061 / U+2062 / U+2063
// (invisible math operators) — every rune in every sentinel is
// width-0, so no width-based pass can ever bisect one.
//
// # Author contract
//
// Story authors write nothing special. They keep using
// {{ llm.summary }} or whatever world var holds an LLM result. The
// engine handles the rest.
package sourcecolor

import (
	"strings"
	"unicode/utf8"
)

// Sentinels marking the boundary of LLM-sourced text.
//
// Each is a 4-rune sequence of zero-width Unicode characters:
//
//   - U+2063 INVISIBLE SEPARATOR
//   - U+2061 FUNCTION APPLICATION  (open uses two)
//   - U+2062 INVISIBLE TIMES        (close uses two)
//
// They have no visible width, so width-based wrapping and truncation
// cannot split them. They are exceedingly unlikely to occur in normal
// text — these particular characters appear only in mathematical
// typesetting and even there only one at a time.
const (
	llmOpen  = "⁣⁡⁡⁣"
	llmClose = "⁣⁢⁢⁣"
)

// LLMOpen and LLMClose are the public spellings of the sentinels, for
// callers that need to splice them in or scan for them directly.
const (
	LLMOpen  = llmOpen
	LLMClose = llmClose
)

// Wrap marks s as LLM-sourced. Empty strings are returned unchanged
// so the wrap is a no-op for empty LLM results.
func Wrap(s string) string {
	if s == "" {
		return s
	}
	return llmOpen + s + llmClose
}

// IsWrapped reports whether s contains at least one LLM sentinel
// pair. Useful for fast-pathing the colorize pass.
func IsWrapped(s string) bool {
	return strings.Contains(s, llmOpen)
}

// WrapTree walks a JSON-shaped value (map[string]any, []any, scalars)
// and returns a copy with every string leaf passed through [Wrap].
// Use this at the operator boundary when the LLM emits a *structured*
// payload (e.g. an MCP-validator submit() result, an output_format=json
// reply): every string field inside is LLM-generated text and should
// carry source-color provenance when later substituted into a view.
//
// Non-string scalars (numbers, bools, nil) and unknown types are
// returned as-is. Maps and slices are rebuilt to avoid mutating the
// caller's value.
//
// Empty strings are preserved as empty — see [Wrap].
func WrapTree(v any) any {
	switch t := v.(type) {
	case string:
		return Wrap(t)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, vv := range t {
			out[k] = WrapTree(vv)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, vv := range t {
			out[i] = WrapTree(vv)
		}
		return out
	}
	return v
}

// Strip removes every LLM sentinel from s and returns the plain text.
// Use this for consumers that must not see sentinels (logging in
// non-terminal contexts, plain-text exports, test assertions on the
// rendered text content).
func Strip(s string) string {
	if !strings.Contains(s, llmOpen) && !strings.Contains(s, llmClose) {
		return s
	}
	s = strings.ReplaceAll(s, llmOpen, "")
	s = strings.ReplaceAll(s, llmClose, "")
	return s
}

// Theme is a pair of ANSI background-color escape sequences plus the
// foreground and reset codes used between them.
//
// Background escapes should set ONLY the background (e.g.
// "\x1b[48;2;R;G;Bm") so that any foreground style applied by other
// layers (lipgloss, the renderer) is preserved when the bg toggles.
type Theme struct {
	Name  string
	TplBG string // background for templated/deterministic text
	LLMBG string // background for LLM-sourced text
	FG    string // optional foreground; empty = inherit caller's fg
	Reset string // SGR sequence that clears everything (defaults to ESC [0m)
}

// Predefined themes. Hex values are also documented in
// docs/story-style.md §8 — keep the two in sync.
var (
	DarkTheme = Theme{
		Name:  "dark",
		TplBG: "\x1b[48;2;42;53;80m", // #2a3550 — cool slate
		LLMBG: "\x1b[48;2;92;62;40m", // #5c3e28 — warm bronze
		FG:    "\x1b[38;2;232;232;232m",
		Reset: "\x1b[0m",
	}
	HighContrastTheme = Theme{
		Name:  "high-contrast",
		TplBG: "\x1b[48;2;32;48;112m", // #203070
		LLMBG: "\x1b[48;2;128;72;24m", // #804818
		FG:    "\x1b[38;2;255;255;255m",
		Reset: "\x1b[0m",
	}
	LightTheme = Theme{
		Name:  "light",
		TplBG: "\x1b[48;2;232;240;255m", // #e8f0ff
		LLMBG: "\x1b[48;2;255;244;224m", // #fff4e0
		FG:    "\x1b[38;2;20;20;20m",
		Reset: "\x1b[0m",
	}
)

// Options controls how [Colorize] renders a sentinel-laced string.
type Options struct {
	// Width is the column count to pad block-style LLM spans to. A
	// multi-line LLM span has each contained line padded with spaces
	// (carrying the LLM background) up to this width, so the warm
	// band reads as a solid rectangle. Set to the active layout's
	// inner width (e.g. transcript wrap column) so the band lines up.
	// If 0, block padding extends to the longest visible line within
	// the block instead — yields a content-shaped rectangle rather
	// than an edge-to-edge band.
	Width int

	// FillTemplate, if true, also pads template-bg lines to Width so
	// the entire view is a solid cool band. Off by default — leaving
	// template lines text-tight keeps the warm LLM band as the
	// visually loud element.
	FillTemplate bool
}

// Colorize converts every LLM sentinel pair in s into ANSI bg
// switches, returning the painted string.
//
// Strings with no sentinels are returned untouched (the fast path).
// Strings with sentinels are wrapped in t.FG + t.TplBG at every line
// start, switched to t.LLMBG inside each LLM span, and reset at every
// line end. The reset-then-reset-fg-and-bg pattern survives
// downstream SGR resets emitted by other renderers (lipgloss).
//
// Multi-line LLM spans get each contained line padded with spaces to
// opts.Width (or the block's longest-line if opts.Width == 0). The
// padding spaces carry the LLM bg, producing the solid warm band.
func Colorize(s string, t Theme, opts Options) string {
	if !strings.Contains(s, llmOpen) && !strings.Contains(s, llmClose) && !opts.FillTemplate {
		return s
	}
	reset := t.Reset
	if reset == "" {
		reset = "\x1b[0m"
	}

	blockWidth := opts.Width
	if blockWidth == 0 {
		blockWidth = longestBlockLine(s)
	}

	var out strings.Builder
	out.Grow(len(s) + 64)
	stack := []string{t.TplBG}

	lines := strings.Split(s, "\n")
	for li, line := range lines {
		if t.FG != "" {
			out.WriteString(t.FG)
		}
		out.WriteString(stack[len(stack)-1])

		// lastCharBG is the bg that the rightmost visible rune on this
		// line was — or would be — drawn in. We pad with this, NOT
		// with stack-top, so the warm band stays continuous across
		// three trickier cases:
		//
		//  - close at end of line: stack pops to template, but the
		//    last visible rune was on LLM bg — keep padding warm.
		//  - open at end of line: no visible runes, but the line is
		//    the "shoulder row" above a block — pad warm so the band
		//    has a top edge.
		//  - close-then-template-text mid-line: lastCharBG falls back
		//    to template only after a template char actually lands,
		//    so a bare "[/LLM]" with nothing after it still pads warm.
		lastCharBG := stack[len(stack)-1]
		visWidth := 0
		i := 0
		for i < len(line) {
			if strings.HasPrefix(line[i:], llmOpen) {
				stack = append(stack, t.LLMBG)
				out.WriteString(t.LLMBG)
				lastCharBG = t.LLMBG
				i += len(llmOpen)
				continue
			}
			if strings.HasPrefix(line[i:], llmClose) {
				if len(stack) > 1 {
					stack = stack[:len(stack)-1]
				}
				out.WriteString(stack[len(stack)-1])
				// Deliberately leave lastCharBG untouched — see the
				// comment above. Pop affects the stack used for the
				// *next* visible rune, not the visual fate of the
				// runes already drawn.
				i += len(llmClose)
				continue
			}
			// Pass-through for ANSI SGR sequences emitted by upstream
			// renderers (glamour markdown, lipgloss styles). They must
			// land in the output verbatim — but a full reset (\x1b[0m)
			// or default-bg reset (\x1b[49m) inside an LLM span would
			// kill the warm band mid-line. Detect and re-emit the
			// stack-top bg right after every such reset so glamour's
			// per-chunk resets don't disrupt our source-color band.
			if line[i] == 0x1b {
				seq, n := parseEscape(line[i:])
				out.WriteString(seq)
				if resetsBackground(seq) {
					out.WriteString(stack[len(stack)-1])
				}
				i += n
				continue
			}
			r, sz := utf8.DecodeRuneInString(line[i:])
			out.WriteRune(r)
			visWidth += runeVisibleWidth(r)
			lastCharBG = stack[len(stack)-1]
			i += sz
		}

		needPad := visWidth < blockWidth
		isLLM := lastCharBG == t.LLMBG
		if needPad && (isLLM || opts.FillTemplate) {
			// If a close-sentinel already shifted us off lastCharBG,
			// re-emit it so the padding spaces land in the right bg.
			if stack[len(stack)-1] != lastCharBG {
				out.WriteString(lastCharBG)
			}
			out.WriteString(strings.Repeat(" ", blockWidth-visWidth))
		}

		out.WriteString(reset)
		if li < len(lines)-1 {
			out.WriteByte('\n')
		}
	}
	return out.String()
}

// longestBlockLine scans s for the longest visible line that contains
// (or is inside) an LLM span. Used as the default block width when
// opts.Width is 0, so a multi-line LLM block paints as a rectangle
// snug to its content rather than running to the right margin.
func longestBlockLine(s string) int {
	max := 0
	depth := 0
	var cur int
	emit := func() {
		if cur > max {
			max = cur
		}
		cur = 0
	}
	for i := 0; i < len(s); {
		if strings.HasPrefix(s[i:], llmOpen) {
			depth++
			i += len(llmOpen)
			continue
		}
		if strings.HasPrefix(s[i:], llmClose) {
			if depth > 0 {
				depth--
			}
			i += len(llmClose)
			continue
		}
		r, sz := utf8.DecodeRuneInString(s[i:])
		i += sz
		if r == '\n' {
			emit()
			continue
		}
		if depth > 0 {
			cur += runeVisibleWidth(r)
		}
	}
	emit()
	return max
}

// parseEscape returns the ANSI escape sequence that starts at s[0]
// (must be ESC, 0x1b) and its byte length. CSI sequences (`ESC [ ...
// final-byte`) are returned in full; non-CSI escapes are returned as
// a single byte so the caller advances past the ESC and resumes.
//
// We need to walk these as opaque units for two reasons:
//   - emit them verbatim so the upstream renderer's intent survives
//   - skip their bytes when counting visible width (otherwise the
//     digits and final-byte of "\x1b[38;5;205m" would each add 1 to
//     visWidth and block padding would come up short)
func parseEscape(s string) (string, int) {
	if len(s) < 2 || s[0] != 0x1b {
		return s[:1], 1
	}
	if s[1] != '[' {
		// Non-CSI escape (single-char like ESC c, or OSC). Consume only
		// the ESC byte and let the next iteration handle the rest.
		return s[:1], 1
	}
	// CSI: ESC [ params... final-byte (final byte is in 0x40..0x7e).
	for i := 2; i < len(s); i++ {
		if s[i] >= 0x40 && s[i] <= 0x7e {
			return s[:i+1], i + 1
		}
	}
	// Truncated/malformed — consume the rest.
	return s, len(s)
}

// resetsBackground reports whether an SGR sequence clears the current
// background color. Three cases qualify:
//   - "\x1b[m"   — implicit "all attributes off"
//   - "\x1b[0m"  — explicit "all attributes off"
//   - sequences whose parameters include 0 or 49 — 49 is "default bg"
//
// Lipgloss / glamour emit "\x1b[0m" at the end of every styled chunk,
// which is the source of the "bg shows up only on empty lines"
// symptom: between two styled chunks our LLM bg gets cleared. Detecting
// these and re-emitting the active stack-top bg keeps the band solid.
func resetsBackground(seq string) bool {
	n := len(seq)
	if n < 3 || seq[0] != 0x1b || seq[1] != '[' || seq[n-1] != 'm' {
		return false
	}
	params := seq[2 : n-1]
	if params == "" {
		return true
	}
	for _, p := range strings.Split(params, ";") {
		if p == "" || p == "0" || p == "49" {
			return true
		}
	}
	return false
}

// runeVisibleWidth returns the column count a rune consumes. Zero
// for any sentinel-class character (zero-width / invisible math
// operators / ZWS / ZWJ / variation selectors). One for everything
// else — kitsoki views are predominantly ASCII; CJK width is left to
// the wrapping layer, not to colorize, since colorize only uses width
// for block padding (mild over- or under-padding is cosmetic, not a
// correctness issue).
func runeVisibleWidth(r rune) int {
	switch {
	case r < 0x20: // control
		return 0
	case r == 0x200B, r == 0x200C, r == 0x200D: // ZWS, ZWNJ, ZWJ
		return 0
	case r == 0x2060: // word joiner
		return 0
	case r >= 0x2061 && r <= 0x2064: // invisible math operators (our sentinels)
		return 0
	case r >= 0xFE00 && r <= 0xFE0F: // variation selectors
		return 0
	case r == 0xFEFF: // ZWNBSP / BOM
		return 0
	}
	return 1
}
