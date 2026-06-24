// Runnable godoc examples for the expr surface. Each Example function's
// // Output: block is checked by
// `go test -run "^Example" ./internal/expr/...`, so the documented behaviour
// cannot drift from the implementation.
package expr_test

import (
	"fmt"

	"kitsoki/internal/expr"
)

// ExampleCompileBool is the guard worked example from the package doc: a
// boolean expression over the `world` root, compiled once and evaluated
// against a world snapshot.
func ExampleCompileBool() {
	p, err := expr.CompileBool("world.wearing_cloak == false")
	if err != nil {
		panic(err)
	}

	got, err := expr.EvalBool(p, expr.Env{
		World: map[string]any{"wearing_cloak": false},
	})
	if err != nil {
		panic(err)
	}

	fmt.Println(got)
	// Output: true
}

// ExampleRender is the template worked example from the package doc: a view
// line that branches on a world variable through an {{ if }}/{{ else }} block.
func ExampleRender() {
	tmpl := "The hall is {{ if world.wearing_cloak }}dark{{ else }}lit{{ end }}."

	out, err := expr.Render(tmpl, expr.Env{
		World: map[string]any{"wearing_cloak": false},
	})
	if err != nil {
		panic(err)
	}

	fmt.Println(out)
	// Output: The hall is lit.
}

// ExamplePopulateMenuHelpers shows the view-render workflow: assemble a Menu,
// bind the helper closures, then render prose that asks "is this intent
// available, and if not, why?" via available / blocked_reason.
func ExamplePopulateMenuHelpers() {
	env := expr.Env{
		Menu: map[string]any{
			"primary": []any{
				map[string]any{"intent": "look", "display": "look around"},
			},
			"blocked": []any{
				map[string]any{"intent": "start_journey", "reason": "name your party first"},
			},
		},
	}
	expr.PopulateMenuHelpers(&env)

	tmpl := `{{ if available("start_journey") }}` +
		`- start the journey` +
		`{{ else }}` +
		`- (blocked: {{ blocked_reason("start_journey") }})` +
		`{{ end }}`

	out, err := expr.Render(tmpl, env)
	if err != nil {
		panic(err)
	}

	fmt.Println(out)
	// Output: - (blocked: name your party first)
}

// ExampleRender_range shows the {{ range }} block binding each list element
// to the bare-dot `.` form, the shape view prose uses to list a menu.
func ExampleRender_range() {
	env := expr.Env{
		Menu: map[string]any{
			"primary": []any{
				map[string]any{"intent": "look", "display": "look around"},
				map[string]any{"intent": "go_north", "display": "head north"},
			},
		},
	}

	out, err := expr.Render("{{ range menu.primary }}- {{ .display }}\n{{ end }}", env)
	if err != nil {
		panic(err)
	}

	fmt.Print(out)
	// Output:
	// - look around
	// - head north
}
