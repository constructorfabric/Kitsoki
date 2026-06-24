package shot

import (
	"bytes"
	"image/png"
	"strings"
	"testing"

	"kitsoki/internal/tui/blocks"
)

// TestImageSize_Deterministic checks the size contract the proposal calls for:
// a cols×rows grid yields a PNG of cols*CellW × rows*CellH plus padding, with
// the cell geometry resolved from the embedded font (no hinting → identical on
// every platform). A 100×30 frame must size exactly, every run.
func TestImageSize_Deterministic(t *testing.T) {
	m, err := ResolveMetrics(DefaultMetrics())
	if err != nil {
		t.Fatalf("ResolveMetrics: %v", err)
	}
	if m.CellW < 1 || m.CellH < 1 {
		t.Fatalf("resolved cell geometry invalid: %+v", m)
	}

	const cols, rows = 100, 30
	wantW := cols*m.CellW + 2*m.PadX
	wantH := rows*m.CellH + 2*m.PadY

	// Build a grid that is exactly cols wide × rows tall (no overflow) so the
	// canvas matches the requested geometry.
	var b strings.Builder
	for r := 0; r < rows; r++ {
		b.WriteString(strings.Repeat("x", cols))
		if r < rows-1 {
			b.WriteByte('\n')
		}
	}
	grid := Parse(b.String())
	pal := PaletteFromTheme(blocks.ThemeByName("default"))

	img, err := Rasterise(grid, pal, cols, rows, DefaultMetrics())
	if err != nil {
		t.Fatalf("Rasterise: %v", err)
	}
	got := img.Bounds()
	if got.Dx() != wantW || got.Dy() != wantH {
		t.Fatalf("image size = %dx%d, want %dx%d (cellW=%d cellH=%d)",
			got.Dx(), got.Dy(), wantW, wantH, m.CellW, m.CellH)
	}
}

// TestRasterise_OverflowGrowsCanvas proves a row wider than --cols is painted
// fully (the canvas grows to the widest row) — overflow is surfaced, never
// truncated. This is the core "show me the bug" guarantee.
func TestRasterise_OverflowGrowsCanvas(t *testing.T) {
	m, err := ResolveMetrics(DefaultMetrics())
	if err != nil {
		t.Fatalf("ResolveMetrics: %v", err)
	}
	const cols = 10
	// One row of 40 cells, requested at 10 cols: canvas must grow to 40.
	grid := Parse(strings.Repeat("y", 40))
	pal := PaletteFromTheme(blocks.ThemeByName("default"))

	img, err := Rasterise(grid, pal, cols, 1, DefaultMetrics())
	if err != nil {
		t.Fatalf("Rasterise: %v", err)
	}
	wantW := 40*m.CellW + 2*m.PadX
	if img.Bounds().Dx() != wantW {
		t.Fatalf("overflow not surfaced: width = %d, want %d (canvas should grow to widest row)",
			img.Bounds().Dx(), wantW)
	}
}

// TestRenderPNG_Smoke rasterises a representative styled ANSI frame (the same
// SGR vocabulary `kitsoki drive` emits with colour forced on: bright-red
// italic error, grey divider, reverse hint, bright-white-on-blue status row)
// and asserts the result is a decodable, non-empty PNG. Visual correctness is
// for human/Claude review by design; this proves the bytes are faithful and
// the pipeline produces a valid image.
func TestRenderPNG_Smoke(t *testing.T) {
	frameANSI := strings.Join([]string{
		"\x1b[3;91m→ error: harness recording miss for state=\"foyer\"\x1b[0m",
		"\x1b[90m" + strings.Repeat("─", 80) + "\x1b[0m",
		"\x1b[1;94m> \x1b[0m\x1b[7m↵\x1b[0m\x1b[90m go north · what now?\x1b[0m",
		"\x1b[1;97;104mfoyer · The entrance hall of the opera house.   normal\x1b[0m",
	}, "\n")

	var buf bytes.Buffer
	err := RenderPNG(&buf, frameANSI, Options{
		Theme: blocks.ThemeByName("default"),
		Cols:  100,
		Rows:  30,
	})
	if err != nil {
		t.Fatalf("RenderPNG: %v", err)
	}
	if buf.Len() == 0 {
		t.Fatal("RenderPNG produced 0 bytes")
	}

	img, err := png.Decode(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("decode PNG: %v", err)
	}
	if img.Bounds().Empty() {
		t.Fatal("decoded PNG has empty bounds")
	}
}

// TestPaletteFromTheme_LightVsDark checks the theme palette wiring: the default
// theme yields a dark canvas, the light pseudo-theme a near-white one, and the
// foreground tracks the theme Text colour. This is the seam that keeps a
// screenshot's default colours matching the live TUI's theme.
func TestPaletteFromTheme_LightVsDark(t *testing.T) {
	dark := PaletteFromTheme(blocks.ThemeByName("default"))
	if dark.BG.R > 0x40 {
		t.Errorf("default theme bg should be dark, got %+v", dark.BG)
	}
	// default theme Text is #F9FAFB (near white).
	if dark.FG.R < 0xf0 {
		t.Errorf("default theme fg should track near-white Text, got %+v", dark.FG)
	}

	light := PaletteFromTheme(&blocks.Theme{Name: "light", Text: "#1e1e1e"})
	if light.BG.R < 0xf0 {
		t.Errorf("light theme bg should be near-white, got %+v", light.BG)
	}
	// #1e1e1e → R=0x1e, dark text — sanity that the hex parse fed through.
	if light.FG.R > 0x40 {
		t.Errorf("light theme fg should track dark Text #1e1e1e, got %+v", light.FG)
	}
}
