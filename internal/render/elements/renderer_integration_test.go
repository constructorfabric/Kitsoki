package elements

import (
	"path/filepath"
	"strings"
	"testing"

	goyaml "github.com/goccy/go-yaml"

	"kitsoki/internal/app"
	"kitsoki/internal/expr"
	"kitsoki/internal/render"
)

// TestRenderAll_IncludeResolvesViaAppRenderer covers Issue 1 — when an
// element's leaf string carries `{% include "<name>" %}`, the
// dispatcher's per-app renderer (AppRenderer) must resolve the template
// against the per-app views/ directory. Before the fix, every element
// renderer called the loader-less render.Pongo and the include failed
// with "no app views/ directory configured (cannot load template …)".
func TestRenderAll_IncludeResolvesViaAppRenderer(t *testing.T) {
	// Reuse the render package's testdata views/ tree
	// (partials/stock.pongo prints world.oxen / world.food).
	appDir, err := filepath.Abs("../testdata")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	rr, err := render.NewCachedAppRenderer(appDir)
	if err != nil {
		t.Fatalf("NewCachedAppRenderer: %v", err)
	}
	view := app.View{
		Elements: []app.ViewElement{
			{Kind: "code", Source: `{% include "partials/stock.pongo" %}`},
		},
	}
	env := expr.Env{World: map[string]any{"oxen": 3, "food": 200}}
	out, err := RenderAll(view, env, 80, IdentityGlamour, rr)
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}
	if !strings.Contains(out, "Oxen: 3") || !strings.Contains(out, "Food: 200") {
		t.Errorf("include did not resolve through AppRenderer:\n%s", out)
	}
}

// TestRenderAll_IncludeFailsWithoutAppRenderer is the negative — passing
// nil for the renderer means the loader-less render.Pongo runs and
// {% include %} fails. Documents the fallback behaviour so future
// refactors can spot the deliberate boundary.
func TestRenderAll_IncludeFailsWithoutAppRenderer(t *testing.T) {
	view := app.View{
		Elements: []app.ViewElement{
			{Kind: "code", Source: `{% include "partials/stock.pongo" %}`},
		},
	}
	env := expr.Env{World: map[string]any{"oxen": 3, "food": 200}}
	_, err := RenderAll(view, env, 80, IdentityGlamour, nil)
	if err == nil {
		t.Fatalf("expected error when no AppRenderer is supplied; got nil")
	}
}

// TestRenderAll_KVUsesAppRenderer cross-checks that kv pair values are
// expanded via the supplied renderer too — every leaf type was reachable
// from the loader-less path before the fix.
func TestRenderAll_KVUsesAppRenderer(t *testing.T) {
	appDir, err := filepath.Abs("../testdata")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	rr, err := render.NewCachedAppRenderer(appDir)
	if err != nil {
		t.Fatalf("NewCachedAppRenderer: %v", err)
	}
	view := app.View{
		Elements: []app.ViewElement{
			{Kind: "kv", Pairs: goyaml.MapSlice{
				{Key: "Stock", Value: `{% include "partials/stock.pongo" %}`},
			}},
		},
	}
	env := expr.Env{World: map[string]any{"oxen": 7, "food": 12}}
	out, err := RenderAll(view, env, 80, IdentityGlamour, rr)
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}
	if !strings.Contains(out, "Oxen: 7") {
		t.Errorf("kv value include did not resolve:\n%s", out)
	}
}
