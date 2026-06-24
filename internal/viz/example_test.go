// Runnable godoc examples for the viz surface. Each Example function's
// // Output: block is checked by
// `go test -run "^Example" ./internal/viz/...`, so the documented output
// cannot drift from the emitters. The two-room "tavern" app mirrors the
// worked example in doc.go.
package viz_test

import (
	"fmt"

	"kitsoki/internal/app"
	"kitsoki/internal/viz"
)

// tavern is the worked-example app from doc.go: a compound `bar` (initial
// child `dark`, `light_lamp` → `lit`, then `leave` → terminal `street`).
func tavern() *app.AppDef {
	return &app.AppDef{
		App:  app.AppMeta{ID: "tavern"},
		Root: "bar.dark",
		States: map[string]*app.State{
			"bar": {
				Type:    "compound",
				Initial: "dark",
				States: map[string]*app.State{
					"dark": {On: map[string][]app.Transition{
						"light_lamp": {{Target: "lit"}},
					}},
					"lit": {On: map[string][]app.Transition{
						"leave": {{Target: "street"}},
					}},
				},
			},
			"street": {Terminal: true},
		},
	}
}

// ExampleGroupRooms shows the room-detection heuristic: the compound `bar`
// and its two leaves form one room; the bare `street` state forms another.
func ExampleGroupRooms() {
	rooms := viz.GroupRooms(tavern())

	fmt.Println("order:", rooms.Order)
	for _, room := range rooms.Order {
		fmt.Printf("%s: %v\n", room, rooms.Members[room])
	}
	// Output:
	// order: [bar street]
	// bar: [bar bar.dark bar.lit]
	// street: [street]
}

// ExampleFlowchartBytes shows the DetailRooms flowchart: each room collapses
// to one node, and only the cross-room edge (`leave`) survives.
func ExampleFlowchartBytes() {
	src, err := viz.FlowchartBytes(tavern(), viz.DetailRooms, viz.FlowchartFilter{})
	if err != nil {
		panic(err)
	}

	// Print just the structural lines (skip the trailing classDef styling).
	for _, line := range splitNonClassDef(string(src)) {
		fmt.Println(line)
	}
	// Output:
	// %% kitsoki viz --flowchart --detail rooms
	// flowchart LR
	//
	//   Start(["<b>Start</b>"]):::input
	//
	//   RI_bar[/"phase 0 · bar (3 states)"/]:::room
	//   RI_street[/"phase 1 · street (1 state)"/]:::room
	//
	//   Start --> RI_bar
	//   RI_bar -- "leave" --> RI_street
}

// ExampleMermaidBytes shows the flat stateDiagram-v2 topology: the two leaves
// nest inside the `bar` compound block, `street` is terminal, and every intent
// edge is drawn.
func ExampleMermaidBytes() {
	src, err := viz.MermaidBytes(tavern())
	if err != nil {
		panic(err)
	}
	fmt.Print(string(src))
	// Output:
	// stateDiagram-v2
	//   direction LR
	//   [*] --> bar_dark
	//   state "bar" as bar {
	//     direction LR
	//     [*] --> bar_dark
	//     state "bar.dark" as bar_dark
	//     state "bar.lit" as bar_lit
	//   }
	//   state "street" as street
	//   street --> [*]
	//   bar_dark --> lit : light_lamp
	//   bar_lit --> street : leave
}

// splitNonClassDef returns the diagram lines up to (but not including) the
// trailing classDef style block, so the example asserts on structure rather
// than on cosmetic colour definitions.
func splitNonClassDef(s string) []string {
	var out []string
	for _, line := range splitLines(s) {
		if len(line) >= len("  classDef") && line[:len("  classDef")] == "  classDef" {
			break
		}
		out = append(out, line)
	}
	// Drop trailing blank lines left before the classDef block.
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
