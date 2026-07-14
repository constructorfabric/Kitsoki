// Package shot rasterises a kitsoki Frame.ANSI into a faithful monospace +
// ANSI-colour PNG.
//
// # Why
//
// The QA agent should be able to *look* at a screen, not only read its text.
// Rendering bugs — overlap, misalignment, a banner colliding with the divider,
// a status row wider than the terminal — survive a text read but jump out
// visually. shot turns any composed Frame (slice 1) into a reviewable image.
//
// # Mental model
//
// A terminal is a grid of styled cells. shot is a tiny terminal *emulator for
// one frame*: parse the ANSI into cells (fg/bg/bold per rune), lay them on a
// monospace grid, emit a PNG. No state machine, no story — pixels from bytes.
//
//	Frame.ANSI ─▶ parse SGR into cells ─▶ draw grid (glyph + fg/bg) ─▶ PNG
//	            (\x1b[…m runs)            (gomono face + theme palette)
//
// # No layout
//
// shot does NOT wrap, truncate, or re-flow. The Frame is the single source of
// layout truth (the composer in slice 1 owns it). If a line overflows the
// requested --cols, that overflow is *the bug we want to see* in the image:
// shot paints faithfully and lets the reviewer catch it.
//
// # SGR coverage
//
// shot parses the SGR subset the TUI actually emits: reset (0), bold (1),
// reverse (7), the 8 standard + 8 bright foreground/background colours
// (30–37 / 90–97, 40–47 / 100–107), the 256-colour form (38;5;n / 48;5;n) and
// 24-bit truecolour (38;2;r;g;b / 48;2;r;g;b). Exotic SGR (blink, underline
// styles) is ignored rather than mis-rendered — see Non-goals in the proposal.
package shot

import "image/color"

// Cell is one character cell of the parsed grid: its rune plus the resolved
// foreground/background colours and bold flag. A cell whose fg/bg is nil
// inherits the theme default at raster time — the parser only pins a colour
// when an SGR run set one, so the rasteriser owns the "default" decision and
// the parser stays palette-agnostic.
type Cell struct {
	// Rune is the character to paint. A space (or zero) paints only the
	// background.
	Rune rune
	// FG is the explicit foreground colour, or nil to mean "theme default fg".
	FG *color.RGBA
	// BG is the explicit background colour, or nil to mean "theme default bg".
	BG *color.RGBA
	// Bold selects the bold face when painting Rune.
	Bold bool
}

// Grid is the parsed screen: a slice of rows, each a slice of cells. Rows are
// ragged on purpose — a row is exactly as wide as its composed line, so an
// over-long line stays over-long (no truncation to --cols). The rasteriser
// reads Cols()/len(Rows) to size the canvas.
type Grid struct {
	Rows [][]Cell
}

// Cols returns the width of the widest row in cells. The canvas is sized to the
// widest row so an overflowing line is fully painted (and visibly past the
// nominal --cols guide), which is the whole point of the tool.
func (g Grid) Cols() int {
	max := 0
	for _, row := range g.Rows {
		if len(row) > max {
			max = len(row)
		}
	}
	return max
}

// sgrState is the running attribute state threaded across an ANSI string as the
// parser walks it. It mirrors a terminal's current-attributes: every printable
// rune snapshots the current fg/bg/bold into its Cell.
type sgrState struct {
	fg   *color.RGBA
	bg   *color.RGBA
	bold bool
	// reverse swaps fg/bg at snapshot time (SGR 7). The TUI's status row and
	// the inverse "↵" hint use it.
	reverse bool
}

// snapshot resolves the running attributes into a Cell for rune r, applying the
// reverse-video swap. Resolution of nil (default) colours is left to the
// rasteriser; reverse with a nil side is encoded by leaving that side nil and
// flipping the *other* — the rasteriser then fills the missing default. To keep
// reverse faithful even when one side is the theme default, snapshot only swaps
// the two explicit pointers; a nil stays nil and the rasteriser pairs it with
// the opposite default.
func (s sgrState) snapshot(r rune) Cell {
	fg, bg := s.fg, s.bg
	if s.reverse {
		fg, bg = bg, fg
	}
	return Cell{Rune: r, FG: fg, BG: bg, Bold: s.bold}
}

// Parse walks an ANSI string and produces a cell Grid. It is a minimal terminal
// emulator: it tracks SGR attributes (Parse handles only the m-terminated CSI),
// splits on newlines into rows, and snapshots the running attributes onto each
// printable rune. Carriage returns are dropped (the composer emits \n line
// ends). Any CSI that is not an SGR (m) run is consumed and ignored, and OSC
// runs (notably OSC 8 terminal hyperlinks) are consumed as zero-width control
// sequences, so their bytes never leak into the grid as visible glyphs; a lone
// ESC is dropped.
//
// reverse (SGR 7) is resolved against the theme defaults by the rasteriser, so
// Parse stays palette-agnostic — it never needs to know the theme's fg/bg.
func Parse(s string) Grid {
	var g Grid
	var row []Cell
	var st sgrState

	runes := []rune(s)
	i := 0
	for i < len(runes) {
		r := runes[i]
		switch {
		case r == '\n':
			g.Rows = append(g.Rows, row)
			row = nil
			i++
		case r == '\r':
			i++ // drop
		case r == 0x1b: // ESC
			// CSI (ESC [ … final): consume the whole CSI; apply it iff it is
			// an SGR (final byte 'm').
			if i+1 < len(runes) && runes[i+1] == '[' {
				j := i + 2
				for j < len(runes) && !isCSIFinal(runes[j]) {
					j++
				}
				if j < len(runes) {
					final := runes[j]
					if final == 'm' {
						applySGR(&st, string(runes[i+2:j]))
					}
					i = j + 1
					continue
				}
				// Unterminated CSI: drop the rest.
				i = len(runes)
				continue
			}
			// OSC (ESC ] … BEL | ESC ] … ESC \): terminal hyperlinks are OSC 8.
			// They carry zero visible width; only the linked label between the
			// opening and closing OSC runs should paint.
			if i+1 < len(runes) && runes[i+1] == ']' {
				i = consumeOSC(runes, i+2)
				continue
			}
			// Lone ESC or two-char escape: drop the ESC and (if present) one byte.
			i++
			if i < len(runes) {
				i++
			}
		default:
			row = append(row, st.snapshot(r))
			i++
		}
	}
	// Flush the final row even if the string did not end in \n (the composer
	// trims the trailing newline, so the last line would otherwise be lost).
	if len(row) > 0 || len(g.Rows) == 0 {
		g.Rows = append(g.Rows, row)
	}
	return g
}

// isCSIFinal reports whether r is a CSI final byte (0x40–0x7e), which ends a
// CSI sequence.
func isCSIFinal(r rune) bool { return r >= 0x40 && r <= 0x7e }

// consumeOSC returns the index just after an OSC sequence body that started at
// start, where the ESC ] introducer has already been consumed. OSC terminates
// with BEL or the ST two-rune sequence ESC \. If the sequence is unterminated,
// consume the rest so partial control bytes never paint as visible text.
func consumeOSC(runes []rune, start int) int {
	for j := start; j < len(runes); j++ {
		switch runes[j] {
		case 0x07: // BEL
			return j + 1
		case 0x1b:
			if j+1 < len(runes) && runes[j+1] == '\\' {
				return j + 2
			}
		}
	}
	return len(runes)
}
