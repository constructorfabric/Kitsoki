package shot

import (
	"image/color"
	"strconv"
	"strings"
)

// applySGR folds a single SGR parameter run (the bytes between ESC[ and the
// terminating m) into the running attribute state. Parameters are
// semicolon-separated decimal codes; an empty run ("ESC[m") is treated as a
// full reset, matching terminal behaviour.
//
// The 38;5;n / 48;5;n (256-colour) and 38;2;r;g;b / 48;2;r;g;b (truecolour)
// extended forms consume their trailing parameters from the same param slice,
// so the loop indexes manually rather than ranging.
func applySGR(st *sgrState, run string) {
	if run == "" {
		reset(st)
		return
	}
	parts := strings.Split(run, ";")
	codes := make([]int, 0, len(parts))
	for _, p := range parts {
		// A missing/blank sub-parameter is 0 per the spec.
		if p == "" {
			codes = append(codes, 0)
			continue
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			// Unknown junk param — skip it rather than abort the whole run.
			continue
		}
		codes = append(codes, n)
	}

	for i := 0; i < len(codes); i++ {
		c := codes[i]
		switch {
		case c == 0:
			reset(st)
		case c == 1:
			st.bold = true
		case c == 22:
			st.bold = false
		case c == 7:
			st.reverse = true
		case c == 27:
			st.reverse = false
		case c >= 30 && c <= 37:
			col := ansi16[c-30]
			st.fg = &col
		case c == 39:
			st.fg = nil
		case c >= 40 && c <= 47:
			col := ansi16[c-40]
			st.bg = &col
		case c == 49:
			st.bg = nil
		case c >= 90 && c <= 97:
			col := ansi16[8+(c-90)]
			st.fg = &col
		case c >= 100 && c <= 107:
			col := ansi16[8+(c-100)]
			st.bg = &col
		case c == 38:
			col, n := extendedColor(codes[i+1:])
			if col != nil {
				st.fg = col
			}
			i += n
		case c == 48:
			col, n := extendedColor(codes[i+1:])
			if col != nil {
				st.bg = col
			}
			i += n
		}
		// All other codes (italic 3, underline 4, blink, etc.) are ignored —
		// shot covers only the SGR subset the TUI emits visibly as colour/bold.
	}
}

// reset clears every attribute back to the terminal default (SGR 0).
func reset(st *sgrState) {
	st.fg = nil
	st.bg = nil
	st.bold = false
	st.reverse = false
}

// extendedColor decodes a 38/48 extended-colour tail. params is the slice of
// codes *after* the 38/48 selector. It returns the resolved colour (or nil if
// the tail is malformed) and the number of params it consumed, so the caller
// can advance its index past them.
//
//	5;n          → 256-colour index n   (consumes 2)
//	2;r;g;b      → 24-bit truecolour    (consumes 4)
func extendedColor(params []int) (*color.RGBA, int) {
	if len(params) == 0 {
		return nil, 0
	}
	switch params[0] {
	case 5:
		if len(params) < 2 {
			return nil, 1
		}
		col := xterm256(params[1])
		return &col, 2
	case 2:
		if len(params) < 4 {
			return nil, len(params)
		}
		col := color.RGBA{
			R: clamp8(params[1]),
			G: clamp8(params[2]),
			B: clamp8(params[3]),
			A: 0xff,
		}
		return &col, 4
	}
	return nil, 1
}

// clamp8 clamps an int into a uint8 channel value.
func clamp8(n int) uint8 {
	if n < 0 {
		return 0
	}
	if n > 255 {
		return 255
	}
	return uint8(n)
}

// ansi16 is the 16-colour ANSI palette (indices 0–15: the 8 standard colours
// then the 8 bright variants). These RGB values are the widely used xterm
// defaults — the same ones xterm256[0:16] below resolve to — so a frame styled
// with bright-red (SGR 91) and one styled with 256-index 9 land on the same
// pixel colour.
var ansi16 = [16]color.RGBA{
	{0x00, 0x00, 0x00, 0xff}, // 0 black
	{0xcd, 0x00, 0x00, 0xff}, // 1 red
	{0x00, 0xcd, 0x00, 0xff}, // 2 green
	{0xcd, 0xcd, 0x00, 0xff}, // 3 yellow
	{0x00, 0x00, 0xee, 0xff}, // 4 blue
	{0xcd, 0x00, 0xcd, 0xff}, // 5 magenta
	{0x00, 0xcd, 0xcd, 0xff}, // 6 cyan
	{0xe5, 0xe5, 0xe5, 0xff}, // 7 white (light grey)
	{0x7f, 0x7f, 0x7f, 0xff}, // 8 bright black (grey)
	{0xff, 0x00, 0x00, 0xff}, // 9 bright red
	{0x00, 0xff, 0x00, 0xff}, // 10 bright green
	{0xff, 0xff, 0x00, 0xff}, // 11 bright yellow
	{0x5c, 0x5c, 0xff, 0xff}, // 12 bright blue
	{0xff, 0x00, 0xff, 0xff}, // 13 bright magenta
	{0x00, 0xff, 0xff, 0xff}, // 14 bright cyan
	{0xff, 0xff, 0xff, 0xff}, // 15 bright white
}

// xterm256 resolves an xterm 256-colour index to RGB. 0–15 reuse ansi16; 16–231
// are the 6×6×6 colour cube; 232–255 are the 24-step greyscale ramp.
func xterm256(idx int) color.RGBA {
	switch {
	case idx < 0:
		return ansi16[0]
	case idx < 16:
		return ansi16[idx]
	case idx < 232:
		n := idx - 16
		r := n / 36
		g := (n / 6) % 6
		b := n % 6
		return color.RGBA{cubeChannel(r), cubeChannel(g), cubeChannel(b), 0xff}
	case idx < 256:
		v := uint8(8 + (idx-232)*10)
		return color.RGBA{v, v, v, 0xff}
	default:
		return ansi16[15]
	}
}

// cubeChannel maps a 0–5 colour-cube coordinate to its 8-bit channel value
// using the standard xterm cube steps (0, 95, 135, 175, 215, 255).
func cubeChannel(n int) uint8 {
	if n <= 0 {
		return 0
	}
	return uint8(55 + n*40)
}
