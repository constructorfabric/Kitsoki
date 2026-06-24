// Runnable godoc example for the [render.Markdown] surface. The // Output:
// block is checked by `go test -run "^Example" ./internal/app/render/...`,
// so the documented output cannot drift from the renderer's behaviour.
package render_test

import (
	"fmt"
	"strings"

	"kitsoki/internal/app"
	"kitsoki/internal/app/render"
)

// ExampleMarkdown renders the two-room app from the package doc's worked
// example and prints the leading Title and Overview sections — the stable,
// identity-first head of every rendered document.
func ExampleMarkdown() {
	def := &app.AppDef{
		App:  app.AppMeta{ID: "doors", Title: "Two Doors", Version: "0.1.0"},
		Root: "start",
		States: map[string]*app.State{
			"start": {
				Description: "Pick a door.",
				On:          map[string][]app.Transition{"open": {{Target: "end"}}},
			},
			"end": {Terminal: true, Description: "Done."},
		},
		Intents: map[string]app.Intent{"open": {Title: "Open the door"}},
		World:   map[string]app.VarDef{"score": {Type: "int", Default: 0}},
	}

	out, err := render.Markdown(def)
	if err != nil {
		panic(err)
	}

	// Print through the "## State Diagram" header — the title and overview,
	// which are the deterministic head of every document.
	body := string(out)
	head := body[:strings.Index(body, "## State Diagram")]
	fmt.Print(strings.TrimRight(head, "\n"))
	// Output:
	// # Two Doors
	//
	// **Version** 0.1.0
	//
	// ## Overview
	//
	// - App ID: `doors`
	// - Entry room: [`start`](#room-start)
	// - Rooms: 2
	// - Intents: 1
	// - World variables: 1
}
