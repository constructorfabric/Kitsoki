package sourcecolor_test

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"kitsoki/internal/expr"
	"kitsoki/internal/render"
	"kitsoki/internal/render/sourcecolor"
)

// TestEndToEnd_OperatorToColorize simulates the production path:
//
//  1. The LLM operator wraps its string output with sourcecolor.Wrap
//     before storing it on Result.Data — done in
//     internal/host/oracle_ask.go and friends.
//  2. The orchestrator binds that string to a world var.
//  3. A view template substitutes the world var via pongo.
//  4. The TUI transcript runs the rendered view through Colorize at
//     flush time.
//
// This test exercises steps 1, 3, 4 directly and asserts the final
// painted string has the expected ANSI bg structure. If any link
// between operator-wrap and colorize-paint regresses, this test fails.
func TestEndToEnd_OperatorToColorize(t *testing.T) {
	// Step 1: operator wraps its reply.
	llmReply := sourcecolor.Wrap("Database migration failed on backfill step.")

	// Step 2 (simulated): orchestrator binds into world.
	env := expr.Env{
		World: map[string]any{
			"ticket": map[string]any{
				"title": llmReply,
				"id":    "BUG-1234",
			},
		},
	}

	// Step 3: pongo renders a view that uses the LLM-sourced field
	// alongside templated text. The sentinels must pass through pongo
	// unchanged — pongo's default auto-escape only escapes HTML-active
	// characters (< > & " '), so the zero-width Unicode sentinels are
	// preserved verbatim.
	tpl := "Ticket {{ world.ticket.id }}: {{ world.ticket.title }} (severity: P1)"
	rendered, err := render.Pongo(tpl, env)
	if err != nil {
		t.Fatalf("render.Pongo: %v", err)
	}
	if !sourcecolor.IsWrapped(rendered) {
		t.Fatalf("pongo stripped sentinels; rendered = %q", rendered)
	}

	// Step 4: colorize at the TUI flush boundary.
	painted := sourcecolor.Colorize(rendered, sourcecolor.DarkTheme, sourcecolor.Options{Width: 80})

	// The painted string must contain both bg colors — template bg for
	// the scaffold, LLM bg for the wrapped portion.
	if !strings.Contains(painted, sourcecolor.DarkTheme.TplBG) {
		t.Errorf("missing template bg in painted output: %q", painted)
	}
	if !strings.Contains(painted, sourcecolor.DarkTheme.LLMBG) {
		t.Errorf("missing LLM bg in painted output: %q", painted)
	}

	// Stripping ANSI from the painted output should yield the same
	// visible text as Strip(rendered) — no character data lost or
	// added (modulo trailing pad spaces).
	want := sourcecolor.Strip(rendered)
	got := strings.TrimRight(ansi.Strip(painted), " ")
	if got != want {
		t.Errorf("visible text mismatch after operator→render→paint:\n got  = %q\n want = %q", got, want)
	}
}

// TestEndToEnd_PongoPreservesSentinels confirms the load-bearing
// assumption that pongo's default HTML-escape does not touch our
// zero-width sentinel runes. If a future pongo upgrade or filter
// change broke this, the whole source-color pipeline would silently
// degrade to "everything paints template-bg" and we'd lose the LLM
// distinction without an obvious test signal — except this one.
func TestEndToEnd_PongoPreservesSentinels(t *testing.T) {
	value := sourcecolor.Wrap("warm content")
	env := expr.Env{World: map[string]any{"v": value}}
	out, err := render.Pongo("[{{ world.v }}]", env)
	if err != nil {
		t.Fatalf("render.Pongo: %v", err)
	}
	want := "[" + value + "]"
	if out != want {
		t.Fatalf("pongo modified sentinels:\n got  = %q\n want = %q", out, want)
	}
}

// TestEndToEnd_MultipleLLMSpansInOneView verifies that several
// LLM-wrapped values in a single template produce one warm region per
// substitution, with cool template text between them — the realistic
// case of a view that shows e.g. an LLM-rewritten title and an
// LLM-generated summary side by side.
func TestEndToEnd_MultipleLLMSpansInOneView(t *testing.T) {
	env := expr.Env{World: map[string]any{
		"title":   sourcecolor.Wrap("ALPHA"),
		"summary": sourcecolor.Wrap("BRAVO"),
	}}
	rendered, err := render.Pongo("h: {{ world.title }} | s: {{ world.summary }}", env)
	if err != nil {
		t.Fatalf("render.Pongo: %v", err)
	}
	painted := sourcecolor.Colorize(rendered, sourcecolor.DarkTheme, sourcecolor.Options{Width: 40})

	// Two warm transitions (one open per substitution) should appear.
	if n := strings.Count(painted, sourcecolor.DarkTheme.LLMBG); n < 2 {
		t.Errorf("expected ≥ 2 LLM-bg emissions for two wrapped values, got %d in %q", n, painted)
	}
}
