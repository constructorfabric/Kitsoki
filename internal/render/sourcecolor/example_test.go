// Runnable godoc examples for the sourcecolor surface. Each Example
// function's // Output: block is checked by
// `go test -run "^Example" ./internal/render/sourcecolor/...`.
package sourcecolor_test

import (
	"fmt"

	"kitsoki/internal/render/sourcecolor"
)

// ExampleWrap shows the operator-boundary half of the package: Wrap
// brackets an LLM result with zero-width sentinels, IsWrapped detects
// them, and Strip recovers the original plain text. The sentinels carry
// no visible width, so the wrapped and stripped forms print identically
// here even though the wrapped form holds four extra (invisible) runes
// on each side.
func ExampleWrap() {
	w := sourcecolor.Wrap("all clear")

	fmt.Println("isWrapped:", sourcecolor.IsWrapped(w))
	fmt.Printf("stripped:  %q\n", sourcecolor.Strip(w))
	fmt.Println("empty:    ", sourcecolor.Wrap("") == "")
	// Output:
	// isWrapped: true
	// stripped:  "all clear"
	// empty:     true
}

// ExampleColorize is the worked example from the package doc: a line
// whose templated prefix ("report: ") is followed by an LLM span ("all
// clear"). The output is printed with %q so the (otherwise invisible)
// ANSI escapes are legible: the line opens with the theme foreground and
// the cool-slate template background, switches to the warm-bronze LLM
// background for the span, pops back to slate at the close sentinel, and
// ends with the theme reset.
func ExampleColorize() {
	in := "report: " + sourcecolor.Wrap("all clear")
	out := sourcecolor.Colorize(in, sourcecolor.DarkTheme, sourcecolor.Options{})

	fmt.Printf("%q\n", out)
	// Output:
	// "\x1b[38;2;232;232;232m\x1b[48;2;42;53;80mreport: \x1b[48;2;92;62;40mall clear\x1b[48;2;42;53;80m\x1b[0m"
}

// ExampleColorize_plain shows the fast path: a string with no sentinels
// is returned untouched, so non-LLM text incurs no painting.
func ExampleColorize_plain() {
	out := sourcecolor.Colorize("just templated text", sourcecolor.DarkTheme, sourcecolor.Options{})

	fmt.Printf("%q\n", out)
	// Output:
	// "just templated text"
}

// ExampleWrapTree wraps every string leaf of a structured payload (the
// shape an output_format=json operator returns) while leaving non-string
// scalars alone. Strip recovers the original strings, demonstrating that
// only the string fields gained provenance sentinels.
func ExampleWrapTree() {
	out := sourcecolor.WrapTree(map[string]any{
		"summary": "wagons rolled out",
		"day":     12,
	})

	m := out.(map[string]any)
	fmt.Println("summary wrapped:", sourcecolor.IsWrapped(m["summary"].(string)))
	fmt.Printf("summary plain:   %q\n", sourcecolor.Strip(m["summary"].(string)))
	fmt.Println("day untouched:  ", m["day"])
	// Output:
	// summary wrapped: true
	// summary plain:   "wagons rolled out"
	// day untouched:   12
}
