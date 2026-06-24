// Fuzz target for [ParseList]. Two invariants matter:
//
//  1. No-panic: any input that survives lex.Tokenize must survive
//     ParseList(tokens, intParser{}) without panic.
//  2. Structural shape: on OK=true, Value must always be []any with
//     ≥1 element; on OK=false, Value must be nil and the Reason
//     must be "list:miss" (the sentinel for structural misses).
//
// We seed with the list worked examples plus the trickier shapes from
// list_test.go so the fuzzer starts from cases we already understand.
package slotparse

import (
	"testing"
)

var listFuzzCorpus = []string{
	// list worked examples.
	"6",
	"6, 12, 3",
	"6 and 12 and 3",
	"6, 12, and 3",
	"6 and 12 then drink",

	// Spelled mixed forms.
	"six and twelve",
	"two hundred and three hundred",

	// Mid-stream junk for the prefix-wins path.
	"6, 12, blue, 3",
	"6 12 purple 100",

	// Leading-filler stress.
	"please buy 6 and 12",
	"buy 6 oxen and 12",

	// Pathologicals — must not panic.
	"",
	" ",
	"and",
	"and and and",
	",,,",
	"oxen oxen oxen",
	"\x00\x01control",
	"héllo wörld 6",
}

func FuzzParseList(f *testing.F) {
	for _, s := range listFuzzCorpus {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		tokens := tok(t, in)
		got := ParseList(tokens, intParser{})

		if got.OK {
			vals, ok := got.Value.([]any)
			if !ok {
				t.Fatalf("ParseList(%q): OK=true but Value type %T is not []any", in, got.Value)
			}
			if len(vals) == 0 {
				t.Fatalf("ParseList(%q): OK=true with empty Value slice — invariant violation", in)
			}
			if got.Reason == "" {
				t.Fatalf("ParseList(%q): OK=true but Reason is empty — invariant violation", in)
			}
		} else {
			if got.Value != nil {
				t.Fatalf("ParseList(%q): OK=false but Value=%v (not nil) — invariant violation", in, got.Value)
			}
		}
	})
}
