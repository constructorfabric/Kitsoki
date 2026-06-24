package shot

import (
	"fmt"
	"image"
	"image/color"
	"strconv"
	"strings"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/gomonobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"

	"kitsoki/internal/tui/blocks"
)

// Palette is the resolved theme colour pair the rasteriser fills behind the
// grid and pairs with cells that left fg/bg at the default (nil). It is derived
// from the same blocks.Theme the live TUI paints with (PaletteFromTheme), so a
// screenshot's default text matches what the operator sees.
type Palette struct {
	// FG is the default foreground (theme Text colour).
	FG color.RGBA
	// BG is the default background — themes don't carry an explicit screen
	// background, so a near-black/near-white is chosen to match the terminal
	// the theme was designed for.
	BG color.RGBA
}

// PaletteFromTheme resolves a blocks.Theme into the rasteriser's default fg/bg.
// Text comes straight from the theme; the background is a fixed dark canvas
// (the TUI is designed against dark terminals) unless the theme is the "light"
// pseudo-theme, which uses a near-white canvas. Mapping through blocks.Theme —
// the same struct the renderers reach for — keeps screenshot colours from
// drifting away from the live palette.
func PaletteFromTheme(th *blocks.Theme) Palette {
	bg := color.RGBA{0x1c, 0x1c, 0x1c, 0xff} // matches record.go molokai bg
	if th != nil && th.Name == "light" {
		bg = color.RGBA{0xfd, 0xfd, 0xfd, 0xff}
	}
	fg := color.RGBA{0xf8, 0xf8, 0xf2, 0xff}
	if th != nil {
		if c, ok := hexToRGBA(string(th.Text)); ok {
			fg = c
		}
	}
	return Palette{FG: fg, BG: bg}
}

// Metrics describes the monospace cell geometry and image padding the
// rasteriser lays the grid on. Exported so the dimensions test can assert the
// PNG size deterministically rather than eyeballing pixels.
type Metrics struct {
	// CellW / CellH are the advance width and line height of one cell, in px.
	CellW int
	CellH int
	// PadX / PadY are the canvas margins on each side, in px.
	PadX int
	PadY int
	// FontSize is the gomono point size the faces are built at.
	FontSize float64
	// Ascent is the baseline offset within a cell (px from the cell top).
	Ascent int
}

// DefaultMetrics is the cell geometry shot rasterises at: a 14pt Go Mono face
// at 72 DPI. The numbers are derived once from the face metrics in newFaces and
// are deterministic across platforms because the embedded font and the
// no-hinting rasteriser are fixed.
func DefaultMetrics() Metrics {
	return Metrics{
		FontSize: 14,
		PadX:     12,
		PadY:     12,
	}
}

// ImageSize returns the PNG dimensions a grid of the given cols×rows
// rasterises to under m: width is cols*CellW + 2*PadX, height is
// rows*CellH + 2*PadY. m must carry resolved CellW/CellH — call ResolveMetrics
// first (DefaultMetrics leaves them zero until the faces are built).
func ImageSize(m Metrics, cols, rows int) (w, h int) {
	return cols*m.CellW + 2*m.PadX, rows*m.CellH + 2*m.PadY
}

// ResolveMetrics builds the faces once to fill in CellW/CellH/Ascent from the
// embedded Go Mono metrics and returns the completed Metrics. It exists so
// callers (and the dimensions test) can predict ImageSize without painting,
// and so Rasterise and a caller agree on geometry. The padding/font fields of
// the input are preserved; only the face-derived fields are filled.
func ResolveMetrics(m Metrics) (Metrics, error) {
	fc, err := newFaces(m)
	if err != nil {
		return Metrics{}, err
	}
	return fc.metrics, nil
}

// faces bundles the regular and bold monospace faces plus the cell metrics
// resolved from them. Built once per Rasterise call.
type faces struct {
	regular font.Face
	bold    font.Face
	metrics Metrics
}

// newFaces builds the regular and bold Go Mono faces at m.FontSize and fills in
// the cell geometry (CellW from the 'M' advance, CellH from the face height,
// Ascent from the face ascent). Go Mono is shipped embedded in the
// golang.org/x/image dependency, so no external asset or //go:embed file is
// needed and the binary stays self-contained.
func newFaces(m Metrics) (*faces, error) {
	reg, err := buildFace(gomono.TTF, m.FontSize)
	if err != nil {
		return nil, fmt.Errorf("shot: build regular face: %w", err)
	}
	bold, err := buildFace(gomonobold.TTF, m.FontSize)
	if err != nil {
		return nil, fmt.Errorf("shot: build bold face: %w", err)
	}

	// Cell advance: a monospace face reports the same advance for every glyph;
	// measure 'M'. Round up so adjacent cells never overlap.
	adv, ok := reg.GlyphAdvance('M')
	if !ok {
		return nil, fmt.Errorf("shot: face has no 'M' glyph advance")
	}
	met := reg.Metrics()
	m.CellW = ceilFixed(adv)
	m.CellH = ceilFixed(met.Height)
	m.Ascent = ceilFixed(met.Ascent)
	if m.CellW < 1 {
		m.CellW = 1
	}
	if m.CellH < 1 {
		m.CellH = 1
	}

	return &faces{regular: reg, bold: bold, metrics: m}, nil
}

// buildFace parses TrueType bytes and returns a no-hinting face at size pt. No
// hinting keeps glyph positioning identical across platforms, which matters for
// the dimensions test's determinism (hinting would nudge advances per-OS).
func buildFace(ttf []byte, size float64) (font.Face, error) {
	f, err := opentype.Parse(ttf)
	if err != nil {
		return nil, err
	}
	return opentype.NewFace(f, &opentype.FaceOptions{
		Size:    size,
		DPI:     72,
		Hinting: font.HintingNone,
	})
}

// Rasterise paints a parsed Grid onto an RGBA image using the theme palette. It
// does NOT lay out, wrap, or truncate — each row is painted at its own length,
// so an over-long row paints past the nominal width (visibly the bug). cols and
// rows are the requested geometry: they size the canvas so empty trailing
// space is included and the image dimensions are predictable, but a row wider
// than cols still paints fully (the canvas grows to the widest row).
func Rasterise(g Grid, pal Palette, cols, rows int, m Metrics) (*image.RGBA, error) {
	fc, err := newFaces(m)
	if err != nil {
		return nil, err
	}
	m = fc.metrics

	// Canvas: max(requested, actual) on each axis so overflow is never clipped.
	gridCols := g.Cols()
	if gridCols > cols {
		cols = gridCols
	}
	if len(g.Rows) > rows {
		rows = len(g.Rows)
	}
	w, h := ImageSize(m, cols, rows)
	img := image.NewRGBA(image.Rect(0, 0, w, h))

	// Fill the default background.
	fill(img, pal.BG)

	regDrawer := &font.Drawer{Dst: img, Face: fc.regular}
	boldDrawer := &font.Drawer{Dst: img, Face: fc.bold}

	for rowIdx, row := range g.Rows {
		cellTop := m.PadY + rowIdx*m.CellH
		baseline := cellTop + m.Ascent
		for colIdx, cell := range row {
			cellLeft := m.PadX + colIdx*m.CellW

			fg := pal.FG
			if cell.FG != nil {
				fg = *cell.FG
			}
			bg := pal.BG
			painted := false
			if cell.BG != nil {
				bg = *cell.BG
				painted = true
			}
			// Paint the cell background only when it differs from the canvas
			// (an explicit bg, or a reverse-video cell). Skipping the common
			// transparent case keeps the fill cheap.
			if painted {
				rect := image.Rect(cellLeft, cellTop, cellLeft+m.CellW, cellTop+m.CellH)
				fillRect(img, rect, bg)
			}

			if cell.Rune == 0 || cell.Rune == ' ' {
				continue
			}
			drawer := regDrawer
			if cell.Bold {
				drawer = boldDrawer
			}
			drawer.Src = image.NewUniform(fg)
			drawer.Dot = fixed.P(cellLeft, baseline)
			drawer.DrawString(string(cell.Rune))
		}
	}

	return img, nil
}

// fill paints the whole image one colour.
func fill(img *image.RGBA, c color.RGBA) {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			img.SetRGBA(x, y, c)
		}
	}
}

// fillRect paints a sub-rectangle one colour, clipped to the image bounds.
func fillRect(img *image.RGBA, r image.Rectangle, c color.RGBA) {
	r = r.Intersect(img.Bounds())
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			img.SetRGBA(x, y, c)
		}
	}
}

// ceilFixed rounds a fixed.Int26_6 up to the next whole pixel.
func ceilFixed(v fixed.Int26_6) int {
	return int((v + 63) >> 6)
}

// hexToRGBA parses a "#rrggbb" (or "#rgb") lipgloss colour into RGBA. Returns
// ok=false for non-hex colours (e.g. ANSI-index lipgloss colours), letting the
// caller fall back to a default.
func hexToRGBA(s string) (color.RGBA, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	switch len(s) {
	case 3:
		// Expand #rgb to #rrggbb.
		s = string([]byte{s[0], s[0], s[1], s[1], s[2], s[2]})
	case 6:
	default:
		return color.RGBA{}, false
	}
	n, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return color.RGBA{}, false
	}
	return color.RGBA{
		R: uint8(n >> 16),
		G: uint8(n >> 8),
		B: uint8(n),
		A: 0xff,
	}, true
}
