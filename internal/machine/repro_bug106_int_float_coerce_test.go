// Package machine_test — reproduction for bug 106.
//
// Bug: product-journey-qa TUI renders integer counts as 6-decimal floats
// ("3.000000" instead of "3"). Root cause lives at the set/bind coerce seam:
// a JSON number from a host stdout_json result decodes to float64 and is bound
// into an int-declared world key WITHOUT coercion (machine.coerceSetValue only
// coerces string RHS values, so a float64 whole number passes through
// unchanged). The pongo2 view renderer then formats float64 with %f, yielding
// "N.000000".
//
// This test drives the actual coerce seam (RunEffects → coerceSetValue) with a
// whole-number float64 bound into an int-declared world key, then renders the
// resulting value through the REAL TUI view renderer (render.Pongo) and asserts
// the user-visible output — the integer form, no decimal tail. It is RED on the
// unfixed tree (renders "3.000000") and GREEN once whole-number float64 values
// are coerced to int for int-declared world keys.
package machine_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/expr"
	"kitsoki/internal/render"
	"kitsoki/internal/world"
)

// bug106Def mirrors the product-journey-qa shape: an int-declared world key
// whose value is populated from a host result (a JSON number → float64), and a
// view template that renders that count for the TUI.
func bug106Def() *app.AppDef {
	return &app.AppDef{
		App:   app.AppMeta{ID: "bug106-int-float-coerce"},
		Root:  "s",
		Hosts: []string{"host.noop"},
		World: map[string]app.VarDef{
			// int-declared count, exactly like matrix_target_count in
			// stories/product-journey-qa/app.yaml.
			"matrix_target_count": {Type: "int", Default: 0},
		},
		Intents: map[string]app.Intent{},
		States: map[string]*app.State{
			"s": {View: app.LegacyView("targets: {{ world.matrix_target_count }}")},
		},
	}
}

// TestBug106IntDeclaredKeyRendersWithoutDecimalTail asserts the observable TUI
// outcome: an int-declared world key that receives a whole-number float64
// (as a JSON number would after stdout_json decode) renders as the integer
// "3", not "3.000000".
func TestBug106IntDeclaredKeyRendersWithoutDecimalTail(t *testing.T) {
	def := bug106Def()
	m := mustNew(t, def)

	w := world.New()
	w.Vars["matrix_target_count"] = 0

	// A JSON number from a host stdout_json result decodes to float64. Binding
	// it into the int-declared world key goes through the set/coerce seam.
	effects := []app.Effect{
		{Set: map[string]any{"matrix_target_count": float64(3)}},
	}
	nw, _, _, _, err := m.RunEffects(context.Background(), "s", w, effects)
	require.NoError(t, err)

	// Render the value through the REAL TUI view renderer (pongo2), the same
	// path that produced "N.000000" for the user.
	out, err := render.Pongo(
		"targets: {{ world.matrix_target_count }}",
		expr.Env{World: nw.Vars},
	)
	require.NoError(t, err)

	// Far-side, user-visible assertion: no 6-decimal float tail; the count
	// renders as a whole integer.
	require.NotContains(t, out, ".000000",
		"int-declared world key rendered with a float decimal tail: %q", out)
	require.Equal(t, "targets: 3", strings.TrimSpace(out),
		"int-declared count should render as the integer 3, got %q", out)
}
