package tui

// Regression test for bug 2026-05-14T120000Z-glamour-cap-prose-views:
//
//	"Prose `view:` blocks don't expand past their hand-wrapped width on
//	 wide terminals"
//
// renderViewWith now intercepts the legacy scalar `view:` form (a single
// {Kind:"template"} element produced by app.LegacyView) and splits it into
// blank-line blocks: pure-prose blocks render through the reflowing `prose`
// element, while structured blocks (bullets, headings, indented examples)
// stay on the Glamour/WithPreservedNewLines path. This file pins both
// halves of that contract so the cap can't regress.
//
// Run: go test ./internal/tui/ -run LegacyProseReflow -v

import (
	"strings"
	"testing"

	"kitsoki/internal/app"
	"kitsoki/internal/expr"
	"kitsoki/internal/render/elements"
)

// reflowFoyerProse is cloak's foyer narration hand-wrapped at ~65 chars/line
// — the literal authored body the bug report points at.
const reflowFoyerProse = "You are in a spacious hall, splendidly decorated in red and\n" +
	"gold, with glittering chandeliers overhead. The entrance\n" +
	"from the street is to the north, and there are doorways\n" +
	"south and west."

// TestLegacyProseReflow_GrowsOnWidePanel proves the fix: a legacy scalar
// prose view rendered on a wide panel reflows toward the panel width instead
// of staying pinned at the ~65-char authored wrap.
func TestLegacyProseReflow_GrowsOnWidePanel(t *testing.T) {
	tm := newTranscriptModel(150, 40)

	out := tm.renderView(app.LegacyView(reflowFoyerProse))
	got := maxLineWidth(out)

	t.Logf("panel width = 150; widest rendered prose line = %d chars\n--- rendered ---\n%s", got, out)

	// The authored hand-wrap is ~59 chars; after the fix the prose reflows
	// well past that toward the panel's wrapWidth (vp.Width-4 = 146).
	if got <= 75 {
		t.Fatalf("legacy prose did not reflow on a 150-col panel: widest line = %d cols "+
			"(still pinned at the authored hand-wrap — cap not fixed)", got)
	}
}

// TestLegacyProseReflow_ShrinksOnNarrowPanel confirms the narrow direction
// still re-wraps below the authored width (the report says shrinking always
// worked; the fix must not break it).
func TestLegacyProseReflow_ShrinksOnNarrowPanel(t *testing.T) {
	tm := newTranscriptModel(34, 40)

	out := tm.renderView(app.LegacyView(reflowFoyerProse))
	got := maxLineWidth(out)

	t.Logf("panel width = 34; widest rendered prose line = %d chars\n--- rendered ---\n%s", got, out)

	if got >= 59 {
		t.Fatalf("legacy prose did not re-wrap on a 34-col panel: widest line = %d cols", got)
	}
}

// TestSplitLegacyView_ClassifiesBlocks pins the block classifier: a pure
// prose paragraph becomes a reflowing prose element, while a structured
// block (here an indented Terminal-Room-style example) stays a template
// element on the Glamour path.
func TestSplitLegacyView_ClassifiesBlocks(t *testing.T) {
	source := strings.Join([]string{
		"You stand at a crossroads, the wind tugging at your",
		"cloak as you weigh which way to go next.",
		"",
		"Try one of these:",
		"  propose \"north\"",
		"  propose \"south\"",
		"",
		"- a bulleted option",
		"- another option",
	}, "\n")

	els := elements.SplitLegacyView(source)
	if len(els) != 3 {
		t.Fatalf("expected 3 blocks, got %d: %+v", len(els), els)
	}
	if els[0].Kind != "prose" {
		t.Fatalf("block 0 (pure prose) should be prose, got %q", els[0].Kind)
	}
	if els[1].Kind != "template" {
		t.Fatalf("block 1 (indented examples) should be template, got %q", els[1].Kind)
	}
	if els[2].Kind != "template" {
		t.Fatalf("block 2 (bullet list) should be template, got %q", els[2].Kind)
	}
}

// TestSplitLegacyView_StructuredPreserved confirms an all-structured legacy
// view round-trips byte-for-byte through the splitter — the synthetic view
// is a single template element carrying the original source, so Glamour
// sees exactly what it saw before the interception existed.
func TestSplitLegacyView_StructuredPreserved(t *testing.T) {
	source := "## Heading\n  indented example line\n> a quote"

	els := elements.SplitLegacyView(source)
	if len(els) != 1 {
		t.Fatalf("expected 1 structured block, got %d", len(els))
	}
	if els[0].Kind != "template" || els[0].Source != source {
		t.Fatalf("structured block not preserved: kind=%q source=%q", els[0].Kind, els[0].Source)
	}

	// And it renders identically to the pre-fix single-template path.
	tm := newTranscriptModel(150, 40)
	viaSplit, err := elements.RenderAll(app.View{Source: source, Elements: els}, expr.Env{}, tm.wrapWidth(), tm.renderGlamour, nil)
	if err != nil {
		t.Fatalf("render split view: %v", err)
	}
	viaLegacy, err := elements.RenderAll(app.LegacyView(source), expr.Env{}, tm.wrapWidth(), tm.renderGlamour, nil)
	if err != nil {
		t.Fatalf("render legacy view: %v", err)
	}
	if viaSplit != viaLegacy {
		t.Fatalf("structured view diverged after split:\n--- split ---\n%s\n--- legacy ---\n%s", viaSplit, viaLegacy)
	}
}
