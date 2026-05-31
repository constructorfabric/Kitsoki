// Test-only helpers shared across int_test.go, money_test.go,
// enum_test.go, bool_test.go, *_fuzz_test.go and parse_bench_test.go.
// Each helper calls t.Helper() so the failure line numbers point at
// the caller, not into this file.
package slotparse

import (
	"testing"

	"kitsoki/internal/app"
	"kitsoki/internal/lex"
)

// tok is the shared "tokenise + return slice" entry point used by every
// table-driven test in this package. Centralised so a future change to
// the lex pipeline (e.g. extraStops becoming non-nil by default) lands
// in one place.
func tok(tb testing.TB, s string) []lex.Token {
	tb.Helper()
	return lex.Tokenize(s, nil)
}

// fixtureProfessionSlot returns the canonical enum slot used by the
// ParseEnum tests — three values with three synonyms each, mirroring
// the synonym-tier worked example. Keep this in sync with the slot
// parser table in docs/architecture/semantic-routing.md so authors
// reading both stay oriented.
func fixtureProfessionSlot() app.Slot {
	return app.Slot{
		Type:   "enum",
		Values: []string{"banker", "carpenter", "farmer"},
		Synonyms: map[string][]string{
			"banker":    {"banker", "rich guy", "money man"},
			"carpenter": {"carpenter", "builder", "woodworker"},
			"farmer":    {"farmer", "farmhand"},
		},
	}
}

// rangesEqual reports whether a and b are equal as slices of
// TokenRange. Used by the *_test.go files to assert the Consumed
// field; written here so test failure messages render the structs
// rather than %#v noise.
func rangesEqual(a, b []TokenRange) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
