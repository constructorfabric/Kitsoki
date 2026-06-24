// Runnable godoc examples for the render surface. Each Example function's
// // Output: block is checked by
// `go test -run "^Example" ./internal/render/...`.
package render_test

import (
	"fmt"

	"kitsoki/internal/expr"
	"kitsoki/internal/render"
)

// ExamplePongo is the package's worked example: a single leaf with a ` ?? `
// null-coalesce fallback, rendered against a world that has the variable
// and against one that does not. The author-friendly `??` is rewritten to
// pongo2's `|default:` before execution, so the present case yields the
// value and the absent case falls back to the literal.
func ExamplePongo() {
	src := `{{ world.x ?? "(none)" }}`

	have, err := render.Pongo(src, expr.Env{World: map[string]any{"x": "value"}})
	if err != nil {
		panic(err)
	}
	missing, err := render.Pongo(src, expr.Env{World: map[string]any{}})
	if err != nil {
		panic(err)
	}

	fmt.Printf("have:    %q\n", have)
	fmt.Printf("missing: %q\n", missing)
	// Output:
	// have:    "value"
	// missing: "(none)"
}

// ExamplePongo_prose shows the delimiter fast path: a string with no
// {{ }} / {% %} delimiters is pure prose and is returned verbatim, never
// paying the pongo2 parse cost — note the literal "??" survives untouched
// because the rewrite only fires inside template spans.
func ExamplePongo_prose() {
	out, err := render.Pongo("Are we there yet??", expr.Env{})
	if err != nil {
		panic(err)
	}

	fmt.Printf("%q\n", out)
	// Output:
	// "Are we there yet??"
}

// ExamplePongoParse shows the load-time syntax probe: it compiles without
// executing, so a malformed template is caught at load while undefined
// variables (a runtime concern) are not. Pure prose is always valid.
func ExamplePongoParse() {
	fmt.Println("prose:    ", render.PongoParse("no delimiters here") == nil)
	fmt.Println("valid:    ", render.PongoParse(`{{ world.x }}`) == nil)
	fmt.Println("malformed:", render.PongoParse(`{{ world.x `) == nil)
	// Output:
	// prose:     true
	// valid:     true
	// malformed: false
}
