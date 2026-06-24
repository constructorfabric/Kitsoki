package shot

import (
	"image/color"
	"testing"
)

// rowText reconstructs the visible runes of a parsed row, for column assertions.
func rowText(row []Cell) string {
	rs := make([]rune, 0, len(row))
	for _, c := range row {
		rs = append(rs, c.Rune)
	}
	return string(rs)
}

// TestParse_BoldRedErrorOnPlainLine is the cell-grid regression test the
// proposal calls for: feed a bold-red "ERROR" embedded in an otherwise plain
// line and assert the right runes land in the right columns and that *only*
// the ERROR cells carry the bold-red fg attribute. This is the layer that can
// regress meaningfully (an SGR-parse bug would smear colour onto the wrong
// columns or drop the bold flag); the PNG is downstream of it.
func TestParse_BoldRedErrorOnPlainLine(t *testing.T) {
	// "saw: " then bold bright-red "ERROR" then " here", on one line.
	// \x1b[1;91m = bold + bright-red fg; \x1b[0m = reset.
	line := "saw: \x1b[1;91mERROR\x1b[0m here"
	g := Parse(line)

	if len(g.Rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(g.Rows))
	}
	row := g.Rows[0]

	const want = "saw: ERROR here"
	if got := rowText(row); got != want {
		t.Fatalf("row runes = %q, want %q", got, want)
	}

	brightRed := ansi16[9] // bright red, the SGR-91 colour

	// Columns 0..4 ("saw: ") are plain: no fg pinned, not bold.
	for col := 0; col < 5; col++ {
		c := row[col]
		if c.FG != nil {
			t.Errorf("col %d (%q): want default fg (nil), got %+v", col, string(c.Rune), *c.FG)
		}
		if c.Bold {
			t.Errorf("col %d (%q): want not bold", col, string(c.Rune))
		}
	}

	// Columns 5..9 ("ERROR") are bold bright-red.
	for col := 5; col < 10; col++ {
		c := row[col]
		if c.FG == nil {
			t.Errorf("col %d (%q): want bright-red fg, got nil (default)", col, string(c.Rune))
			continue
		}
		if *c.FG != brightRed {
			t.Errorf("col %d (%q): fg = %+v, want bright red %+v", col, string(c.Rune), *c.FG, brightRed)
		}
		if !c.Bold {
			t.Errorf("col %d (%q): want bold", col, string(c.Rune))
		}
	}

	// Columns 10.. (" here") are back to plain after the reset.
	for col := 10; col < len(row); col++ {
		c := row[col]
		if c.FG != nil {
			t.Errorf("col %d (%q): want default fg after reset, got %+v", col, string(c.Rune), *c.FG)
		}
		if c.Bold {
			t.Errorf("col %d (%q): want not bold after reset", col, string(c.Rune))
		}
	}
}

// TestParse_Reverse swaps fg/bg per SGR 7. The TUI's status row paints with a
// reverse/inverse run; this guards that the parser swaps the explicit colours.
func TestParse_Reverse(t *testing.T) {
	// bright-white fg (97) + bright-blue bg (104), then reverse (7): the cell's
	// fg should become the blue and its bg the white.
	g := Parse("\x1b[97;104m\x1b[7mX")
	row := g.Rows[0]
	if len(row) != 1 {
		t.Fatalf("want 1 cell, got %d", len(row))
	}
	c := row[0]
	wantFG := ansi16[12] // bright blue (was the bg)
	wantBG := ansi16[15] // bright white (was the fg)
	if c.FG == nil || *c.FG != wantFG {
		t.Errorf("reverse fg = %v, want %+v", c.FG, wantFG)
	}
	if c.BG == nil || *c.BG != wantBG {
		t.Errorf("reverse bg = %v, want %+v", c.BG, wantBG)
	}
}

// TestParse_256Color decodes the 38;5;n extended form. Index 196 is a pure red
// in the colour cube; assert the parser lands on the xterm value.
func TestParse_256Color(t *testing.T) {
	g := Parse("\x1b[38;5;196mR")
	c := g.Rows[0][0]
	if c.FG == nil {
		t.Fatal("want a pinned fg for 256-colour run, got nil")
	}
	want := xterm256(196)
	if *c.FG != want {
		t.Errorf("256-colour fg = %+v, want %+v", *c.FG, want)
	}
}

// TestParse_TrueColor decodes 38;2;r;g;b and lands on the exact RGB.
func TestParse_TrueColor(t *testing.T) {
	g := Parse("\x1b[38;2;124;58;237mV")
	c := g.Rows[0][0]
	want := color.RGBA{R: 124, G: 58, B: 237, A: 0xff}
	if c.FG == nil || *c.FG != want {
		t.Errorf("truecolour fg = %v, want %+v", c.FG, want)
	}
}

// TestParse_MultipleRowsAndOverflow proves the parser splits on newlines and
// keeps rows ragged — an over-long line stays over-long (no truncation), which
// is what lets shot surface overflow bugs visually.
func TestParse_MultipleRowsAndOverflow(t *testing.T) {
	g := Parse("short\nthis line is intentionally much longer than the first")
	if len(g.Rows) != 2 {
		t.Fatalf("want 2 rows, got %d", len(g.Rows))
	}
	if len(g.Rows[0]) >= len(g.Rows[1]) {
		t.Fatalf("rows should be ragged: row0=%d row1=%d", len(g.Rows[0]), len(g.Rows[1]))
	}
	if g.Cols() != len(g.Rows[1]) {
		t.Errorf("Cols() = %d, want widest row %d", g.Cols(), len(g.Rows[1]))
	}
}

// TestParse_NonSGRCSIStripped ensures a non-SGR CSI (e.g. cursor move) is
// consumed without leaking its bytes as visible glyphs.
func TestParse_NonSGRCSIStripped(t *testing.T) {
	g := Parse("a\x1b[2Kb") // \x1b[2K = erase line, must not print "2K"
	if got := rowText(g.Rows[0]); got != "ab" {
		t.Errorf("row = %q, want %q (CSI bytes leaked)", got, "ab")
	}
}
