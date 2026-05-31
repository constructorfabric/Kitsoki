// Runnable godoc examples for the [blocks.Renderer] surface. Each
// Example function's // Output: block is checked by
// `go test -run "^Example" ./internal/tui/blocks/...`. The examples
// force NoColor so the output is ANSI-free and stable.
package blocks_test

import (
	"fmt"
	"strings"

	"kitsoki/internal/tui/blocks"
)

// ExampleRenderer_RenderChatView is the canonical worked example: a
// minimal one-turn fixture rendered at width 60 with colour suppressed,
// matching the trace in the package doc. It shows how the transcript
// model and the preview CLI both turn a [blocks.ChatFixture] into one
// printable string.
//
// The header block pads itself with trailing spaces out to the wrap
// width so its background colour spans the row in a real terminal, and
// RenderChatView separates blocks with a blank line. The example trims
// that per-line trailing whitespace and collapses runs of blank lines
// to a single separator, so the structural block order is what the
// // Output: block pins (godoc itself normalises consecutive blank
// lines in Output blocks the same way).
func ExampleRenderer_RenderChatView() {
	r := blocks.New(60, "default").WithNoColor(true)
	fixture := blocks.ChatFixture{
		Location: "idle",
		Room:     "demo",
		Welcome:  "session started",
		Turns: []blocks.FixtureTurn{{
			UserInput: "go north",
			Resolved: blocks.Resolved{
				Kind:   "nav",
				Intent: "north",
				Source: blocks.SourceDeterministic,
			},
			AgentBody: "You head north.",
		}},
		PromptMode: blocks.ModeNormal,
	}
	blank := false
	for _, line := range strings.Split(r.RenderChatView(fixture), "\n") {
		line = strings.TrimRight(line, " ")
		if line == "" {
			if blank {
				continue // collapse consecutive blank separators
			}
			blank = true
		} else {
			blank = false
		}
		fmt.Println(line)
	}
	// Output:
	// idle · demo
	//
	// ────────────────────────────────────────────────────────────
	//
	// · session started
	//
	// > go north
	//
	//   → nav: north   (deterministic · 1.00)
	//
	//   You head north.
	//
	// ────────────────────────────────────────────────────────────
	//
	// > _
}

// ExampleRenderer_RoutingResolved shows a single settled routing line —
// the format the transcript prints once the routing pipeline finishes.
func ExampleRenderer_RoutingResolved() {
	r := blocks.New(80, "default").WithNoColor(true)
	fmt.Println(r.RoutingResolved(blocks.Resolved{
		Kind:       "in-room",
		Intent:     "pick_branch",
		Source:     blocks.SourceLLM,
		Confidence: 0.84,
		Detail:     `slots: {branch: "backup"}`,
	}))
	// Output:
	//   → in-room: pick_branch   (LLM · 0.84)   slots: {branch: "backup"}
}
