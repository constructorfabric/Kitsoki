package main

import (
	"image/gif"
	"os"
	"path/filepath"
	"testing"
)

// TestRecordEndToEnd verifies the hally record pipeline end-to-end:
//   - Loads the cloak-of-darkness app
//   - Replays the "winning" flow (structured intents, no oracle needed)
//   - Writes to a temp GIF file
//   - Asserts the GIF is non-empty and has at least 2 frames
func TestRecordEndToEnd(t *testing.T) {
	// Locate testdata relative to the module root.
	// This test runs from cmd/hally/ so we walk up two levels.
	appYAML := filepath.Join("..", "..", "testdata", "apps", "cloak", "app.yaml")
	flowYAML := filepath.Join("..", "..", "testdata", "apps", "cloak", "flows", "winning.yaml")

	// Verify inputs exist.
	for _, p := range []string{appYAML, flowYAML} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("fixture not found: %s: %v", p, err)
		}
	}

	outGIF := filepath.Join(t.TempDir(), "test-record.gif")

	th := recordThemes["molokai"]
	cfg := recordConfig{
		appPath:     appYAML,
		flowFiles:   []string{flowYAML},
		outPath:     outGIF,
		width:       320,  // small for speed
		height:      240,
		theme:       th,
		frameDelay:  25,   // 250ms
		settleDelay: 15,   // 150ms
		oraclePath:  "",   // winning.yaml uses structured intents only
	}

	if err := runRecord(cfg); err != nil {
		t.Fatalf("runRecord: %v", err)
	}

	// GIF file must exist and be non-empty.
	fi, err := os.Stat(outGIF)
	if err != nil {
		t.Fatalf("output GIF not found: %v", err)
	}
	if fi.Size() == 0 {
		t.Fatal("output GIF is empty")
	}

	// Decode the GIF and check frame count.
	f, err := os.Open(outGIF)
	if err != nil {
		t.Fatalf("open GIF: %v", err)
	}
	defer f.Close()

	g, err := gif.DecodeAll(f)
	if err != nil {
		t.Fatalf("decode GIF: %v", err)
	}
	if len(g.Image) < 2 {
		t.Errorf("expected at least 2 frames, got %d", len(g.Image))
	}
	t.Logf("GIF: %d frames, size=%d bytes", len(g.Image), fi.Size())
}

// TestStripANSI verifies that ANSI escape sequences are removed.
func TestStripANSI(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"\x1b[31mred\x1b[0m", "red"},
		{"plain text", "plain text"},
		{"\x1b[1;32mbold green\x1b[0m end", "bold green end"},
		{"\x1bM\x1b[?25l hidden", " hidden"}, // two-char + CSI
		{"no escapes here", "no escapes here"},
	}
	for _, tc := range cases {
		got := stripANSI(tc.in)
		if got != tc.want {
			t.Errorf("stripANSI(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
}

// TestCentiseconds verifies ms → cs conversion.
func TestCentiseconds(t *testing.T) {
	if centiseconds(2500) != 250 {
		t.Errorf("2500ms should be 250cs, got %d", centiseconds(2500))
	}
	if centiseconds(0) != 1 {
		t.Errorf("0ms should floor to 1cs, got %d", centiseconds(0))
	}
}
