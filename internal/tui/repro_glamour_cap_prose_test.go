package tui

// Reproduction for bug 2026-05-14T120000Z-glamour-cap-prose-views:
//
//	"Prose `view:` blocks don't expand past their hand-wrapped width on
//	 wide terminals"
//
// The TUI runs every legacy scalar `view: <markdown>` through Glamour with
// glamour.WithPreservedNewLines() (see transcript.go's renderer config and
// renderGlamour). That setting is required for structured views (menu-ish
// bullet lists, the Terminal Room's indented `propose "…"` examples) so each
// authored line stays on its own line — but it ALSO treats every author line
// break in pure prose as a hard break. The net effect: hand-wrapped prose
// (e.g. cloak's foyer, wrapped at ~65 chars) stays pinned at its authored
// column even on a 150-col terminal. Shrinking re-wraps; growing is a no-op.
//
// This test reproduces the cap deterministically by driving the EXACT Glamour
// configuration transcript.go uses, then contrasts it with the `prose:`
// element renderer (internal/render/elements) which reflows to the panel.
//
// Run: go test ./internal/tui/ -run ReproGlamourCapProse -v

import (
	"strings"
	"testing"

	"github.com/charmbracelet/glamour"
	"github.com/muesli/reflow/wordwrap"
)

// foyerProse is cloak's foyer narration hand-wrapped at ~65 chars/line —
// the literal authored body that the bug report points at.
const foyerProse = "You are in a spacious hall, splendidly decorated in red and\n" +
	"gold, with glittering chandeliers overhead. The entrance\n" +
	"from the street is to the north, and there are doorways\n" +
	"south and west."

// maxLineWidth returns the widest rendered line, ignoring Glamour's ANSI
// styling and its left margin padding.
func maxLineWidth(rendered string) int {
	max := 0
	for _, line := range strings.Split(rendered, "\n") {
		// Strip ANSI so we measure visible glyphs, and trim Glamour's
		// document margin so left padding isn't counted as content.
		trimmed := strings.TrimRight(strings.TrimLeft(stripANSIForTest(line), " "), " ")
		if n := len([]rune(trimmed)); n > max {
			max = n
		}
	}
	return max
}

// newReproRenderer builds a Glamour renderer with the SAME options
// transcript.go uses for the transcript pane: standard style, word-wrap at
// (panelWidth-2), and — the bug's root cause — WithPreservedNewLines().
func newReproRenderer(t *testing.T, panelWidth int) *glamour.TermRenderer {
	t.Helper()
	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("notty"), // deterministic: no TTY colour probing
		glamour.WithWordWrap(max(panelWidth-2, 40)),
		glamour.WithPreservedNewLines(),
	)
	if err != nil {
		t.Fatalf("glamour renderer init: %v", err)
	}
	return r
}

// TestReproGlamourCapProse_PreservedNewLinesCapsProse demonstrates the bug:
// on a wide (150-col) terminal the hand-wrapped prose does NOT grow — every
// line stays pinned near the ~65-char authored wrap.
func TestReproGlamourCapProse_PreservedNewLinesCapsProse(t *testing.T) {
	const panelWidth = 150

	r := newReproRenderer(t, panelWidth)
	out, err := r.Render(foyerProse)
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	got := maxLineWidth(out)
	t.Logf("panel width = %d; widest rendered prose line = %d chars\n--- rendered ---\n%s",
		panelWidth, got, out)

	// BUG ASSERTION: with WithPreservedNewLines the prose is capped at its
	// authored ~65-char wrap and never approaches the 150-col panel.
	// (When the bug is FIXED — legacy prose reflows — this assertion fails,
	// flipping the test red and signalling the regression is closed.)
	const authoredWrapCeiling = 75 // a little slack over the ~65ch hand-wrap
	if got > authoredWrapCeiling {
		t.Fatalf("expected prose to stay capped at the authored hand-wrap "+
			"(<=%d cols) to reproduce the bug, but it reflowed to %d cols on a "+
			"%d-col panel — bug appears fixed", authoredWrapCeiling, got, panelWidth)
	}
	if got >= panelWidth-10 {
		t.Fatalf("prose filled the panel (%d cols) — bug not reproduced", got)
	}
	t.Logf("REPRODUCED: prose capped at %d cols on a %d-col panel "+
		"(should have reflowed to ~%d)", got, panelWidth, panelWidth-2)
}

// TestReproGlamourCapProse_ShrinkingStillWorks confirms the report's claim
// that shrinking is fine — Glamour re-wraps lines longer than the panel.
// Only GROWING past the authored wrap is broken.
func TestReproGlamourCapProse_ShrinkingStillWorks(t *testing.T) {
	const panelWidth = 30

	r := newReproRenderer(t, panelWidth)
	out, err := r.Render(foyerProse)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	got := maxLineWidth(out)
	// Shrinking re-wraps: the widest line drops well below the ~59-char
	// authored wrap (it cannot stay pinned because the lines no longer fit).
	// We allow a few cols of slack for Glamour's word-boundary wrapping.
	const authoredWrap = 59
	if got >= authoredWrap {
		t.Fatalf("shrinking should re-wrap below the ~%d-char authored width, got %d", authoredWrap, got)
	}
	if got > panelWidth+8 {
		t.Fatalf("shrinking over-ran the panel badly: %d cols on a %d-col panel", got, panelWidth)
	}
	t.Logf("shrinking works: widest line = %d cols on a %d-col panel "+
		"(re-wrapped down from the ~%d-char authored width)", got, panelWidth, authoredWrap)
}

// TestReproGlamourCapProse_ProseElementReflows shows the intended (fixed)
// behaviour for contrast: the typed `prose:` element renderer reflows the
// SAME body to fill a wide panel. This is the target the legacy path fails
// to meet — proving the cap is specific to the Glamour/WithPreservedNewLines
// path, not the content.
func TestReproGlamourCapProse_ProseElementReflows(t *testing.T) {
	const panelWidth = 150

	// Mirror elements.Prose's contract: collapse author newlines to spaces,
	// then word-wrap to the panel width.
	collapsed := strings.Join(strings.Fields(foyerProse), " ")
	out := wordwrap.String(collapsed, panelWidth-2)

	got := maxLineWidth(out)
	t.Logf("prose-element reflow: widest line = %d cols on a %d-col panel\n--- rendered ---\n%s",
		got, panelWidth, out)

	if got <= 75 {
		t.Fatalf("prose element should reflow toward the panel width; "+
			"got only %d cols — reflow not happening", got)
	}
}

// stripANSIForTest removes ANSI escape sequences so line-width measurement
// counts visible glyphs only. Kept local to the repro file.
func stripANSIForTest(s string) string {
	var b strings.Builder
	inEsc := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == 0x1b {
			inEsc = true
			continue
		}
		if inEsc {
			if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
				inEsc = false
			}
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}
