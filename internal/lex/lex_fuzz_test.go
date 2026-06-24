// Fuzz tests for the lex public surface. Each fuzz target is seeded with
// the tokenization corpus from docs/architecture/semantic-routing.md plus a
// few pathological inputs (control characters, deep unicode, very long
// strings).
//
// Invariants:
//   - Tokenize: must never panic; every returned token has Start<=End.
//   - Signature: must never panic; the return is either "" or exactly
//     16 lowercase hex characters.
package lex

import "testing"

// fuzzCorpus is the shared seed list. Pulled from the tokenisation and
// signature examples in docs/architecture/semantic-routing.md, plus a few
// stress inputs.
var fuzzCorpus = []string{
	// signature equivalence-group table
	"buy 6 oxen and 200 lbs of food",
	"let's buy six oxen, 200 lbs food",
	"purchase 6 oxen + 200lbs food",
	"buy 6 oxen",

	// tokenisation worked traces
	"wade across the river",
	"buy 6 oxen and 200 lbs food for 240",
	"let's hunt",
	"any advice for South Pass",

	// stopword-only / empty-ish
	"",
	"the",
	"the and i please",

	// unicode / NFKC
	"buy ６ oxen",
	"héllo wörld",
	"𝓊𝓃𝒾𝒸𝑜𝒹𝑒",

	// control chars / non-printables
	"\x00\x01\x02 control chars",
	"line1\nline2\rline3",

	// pathological lengths
	stringRepeat("a", 1024),
	stringRepeat("ox ", 200),
}

// stringRepeat is a stdlib-free strings.Repeat to avoid importing strings
// just for one helper at fuzz-corpus level.
func stringRepeat(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}

// FuzzTokenize asserts Tokenize doesn't panic and produces tokens with
// valid byte ranges for every input the fuzzer hands us.
func FuzzTokenize(f *testing.F) {
	for _, s := range fuzzCorpus {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		toks := Tokenize(s, nil)
		for i, tok := range toks {
			if tok.End < tok.Start {
				t.Fatalf("Tokenize(%q): token %d %+v has End<Start", s, i, tok)
			}
			if tok.Norm == "" && tok.Surface != "" {
				// Stems may equal the surface; an empty Norm against a
				// non-empty Surface is a bug.
				t.Fatalf("Tokenize(%q): token %d %+v has empty Norm but non-empty Surface", s, i, tok)
			}
		}
	})
}

// FuzzSignature asserts Signature returns either "" or exactly 16 hex
// chars for every input, and never panics.
func FuzzSignature(f *testing.F) {
	for _, s := range fuzzCorpus {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		sig := Signature(s, nil)
		if sig == "" {
			return
		}
		if len(sig) != 16 {
			t.Fatalf("Signature(%q)=%q: length %d, want 16 or empty", s, sig, len(sig))
		}
		for _, r := range sig {
			if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
				t.Fatalf("Signature(%q)=%q: contains non-hex rune %q", s, sig, r)
			}
		}
	})
}
