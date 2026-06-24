// Runnable godoc examples. Each Example function's // Output: block is
// checked by `go test -run "^Example" ./internal/lex/...` so the
// documentation can't drift out of sync with the implementation.
package lex_test

import (
	"fmt"

	"kitsoki/internal/lex"
)

// ExampleTokenize shows the canonical tokenisation trace from the package
// doc (see docs/architecture/semantic-routing.md for the routing context).
func ExampleTokenize() {
	toks := lex.Tokenize("let's wade across the river", nil)
	for _, t := range toks {
		fmt.Printf("%s stop=%v num=%v\n", t.Surface, t.IsStop, t.IsNum)
	}
	// Output:
	// let's stop=true num=false
	// wade stop=false num=false
	// across stop=false num=false
	// the stop=true num=false
	// river stop=false num=false
}

// ExampleSignature shows a signature equivalence group: two semantically
// equivalent phrasings produce the same signature, a third distinct
// phrasing does not (the equivalence rules are documented in
// docs/architecture/semantic-routing.md).
func ExampleSignature() {
	a := lex.Signature("buy 6 oxen and 200 lbs of food", nil)
	b := lex.Signature("let's buy six oxen, 200 lbs food", nil)
	c := lex.Signature("buy 6 oxen", nil)
	fmt.Println("a == b:", a == b)
	fmt.Println("a == c:", a == c)
	fmt.Println("len(a) =", len(a))
	// Output:
	// a == b: true
	// a == c: false
	// len(a) = 16
}
