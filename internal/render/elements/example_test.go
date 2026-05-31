// Runnable godoc examples for the elements surface. Each Example
// function's // Output: block is checked by
// `go test -run "^Example" ./internal/render/elements/...`.
package elements_test

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
	goyaml "github.com/goccy/go-yaml"
	"github.com/muesli/termenv"

	"kitsoki/internal/app"
	"kitsoki/internal/expr"
	"kitsoki/internal/render/elements"
)

// ExampleRenderAll is the canonical worked example from the package doc:
// a heading, a prose paragraph with two world substitutions, and a
// two-row kv block, rendered at width 40. Heading uses a lipgloss accent
// elsewhere; here the color profile is forced to Ascii so the Output
// block carries no escape sequences. The kv colon column is sized to the
// longest key ("Cash"), so "Oxen" pads to align.
func ExampleRenderAll() {
	lipgloss.SetColorProfile(termenv.Ascii)

	view := app.View{
		Elements: []app.ViewElement{
			{Kind: "heading", Source: "River Crossing"},
			{Kind: "prose", Source: "The {{ world.river }} is wide and {{ world.depth }} feet deep."},
			{Kind: "kv", Pairs: goyaml.MapSlice{
				{Key: "Cash", Value: "{{ world.cash }}"},
				{Key: "Oxen", Value: "3"},
			}},
		},
	}
	env := expr.Env{World: map[string]any{
		"river": "Kansas",
		"depth": 4,
		"cash":  "$42",
	}}

	// nil glamour → IdentityGlamour; nil rr → loader-less render.Pongo.
	out, err := elements.RenderAll(view, env, 40, nil, nil)
	if err != nil {
		panic(err)
	}
	fmt.Println(out)
	// Output:
	// River Crossing
	//
	// The Kansas is wide and 4 feet deep.
	//
	// Cash:  $42
	// Oxen:  3
}

// ExampleRenderAll_guard shows the truthy `when:` guard. The first prose
// element's guard resolves to an empty slice (falsy), so it is suppressed
// with no leftover blank line; only the second paragraph renders.
func ExampleRenderAll_guard() {
	lipgloss.SetColorProfile(termenv.Ascii)

	view := app.View{
		Elements: []app.ViewElement{
			{Kind: "prose", When: "world.blockers", Source: "You have blockers."},
			{Kind: "prose", Source: "The trail is clear."},
		},
	}
	env := expr.Env{World: map[string]any{"blockers": []any{}}}

	out, err := elements.RenderAll(view, env, 40, nil, nil)
	if err != nil {
		panic(err)
	}
	fmt.Printf("%q\n", out)
	// Output:
	// "The trail is clear."
}

// ExampleBanner_Render renders a phase-marker banner: a divider, the
// title plus subtitle on the centre line, and a closing divider. The
// color profile is forced to Ascii so the three lines carry no escape
// sequences; the divider is floored to bannerMinWidth (40) for a short
// title.
func ExampleBanner_Render() {
	lipgloss.SetColorProfile(termenv.Ascii)

	b := elements.Banner{Source: "Departure", Subtitle: "Phase 1 / 7"}
	out, err := b.Render(40, expr.Env{}, nil)
	if err != nil {
		panic(err)
	}
	fmt.Println(out)
	// Output:
	// ════════════════════════════════════════
	//   Departure  ·  Phase 1 / 7
	// ════════════════════════════════════════
}
