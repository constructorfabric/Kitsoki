package shot

import (
	"fmt"
	"image/png"
	"io"

	"kitsoki/internal/tui/blocks"
)

// Options configures a one-shot rasterisation: the theme whose palette colours
// the default text/background, and the requested terminal geometry (cols×rows).
// The geometry sizes the canvas; an over-long line still paints past it (shot
// never truncates).
type Options struct {
	Theme *blocks.Theme
	Cols  int
	Rows  int
}

// RenderPNG parses an ANSI frame string, rasterises it under opts, and writes
// the PNG to w. It is the single entry point both `kitsoki shot` and the smoke
// test use, so the command and the tests exercise the same path. Metrics are
// the DefaultMetrics resolved from the embedded font.
func RenderPNG(w io.Writer, ansi string, opts Options) error {
	grid := Parse(ansi)
	pal := PaletteFromTheme(opts.Theme)
	cols, rows := opts.Cols, opts.Rows
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	img, err := Rasterise(grid, pal, cols, rows, DefaultMetrics())
	if err != nil {
		return err
	}
	if err := png.Encode(w, img); err != nil {
		return fmt.Errorf("shot: encode PNG: %w", err)
	}
	return nil
}
