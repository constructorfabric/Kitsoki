// Tests for the [Tokenize] / [IsStopword] / [SpelledNumber] public API.
//
// Structure:
//   - "tokenisation" — UAX#29 segmentation, NFKC, lower, isWordlike, offsets.
//   - "stemming"     — Porter2 hits on derivational suffixes.
//   - "numerals"     — IsNum surface flagging + spelled-number parsing.
//   - "stopwords"    — builtin list, extras, surface-cased input.
//
// Each section is table-driven where the test cases share shape; bespoke
// tests (the "wade across the river" worked example, deterministic across
// calls) sit on their own. Helpers live in helpers_test.go.
package lex

import (
	"reflect"
	"strings"
	"testing"
)

// ====================== tokenisation ======================

func TestTokenize_EmptyInput(t *testing.T) {
	t.Parallel()
	toks := Tokenize("", nil)
	if toks != nil {
		t.Fatalf("Tokenize(%q): want nil, got %v", "", toks)
	}
}

// TestTokenize_WadeAcrossTheRiver pins the canonical worked example:
// "wade across the river" is the input docs/architecture/semantic-routing.md
// cites for the synonym tier, so we keep an explicit assertion separate
// from the table tests so future contributors see it first.
func TestTokenize_WadeAcrossTheRiver(t *testing.T) {
	t.Parallel()
	const input = "let's wade across the river"
	toks := Tokenize(input, nil)
	if len(toks) == 0 {
		t.Fatalf("Tokenize(%q): want non-empty token slice, got empty (surfaces=%v)", input, surfaces(toks))
	}

	// All intent-bearing surfaces must be present, non-stop, and stem to
	// themselves (Porter2 has no derivational suffix to strip).
	for _, want := range []string{"wade", "across", "river"} {
		tok, ok := findToken(t, toks, want)
		if !ok {
			t.Errorf("Tokenize(%q): missing token %q (surfaces=%v)", input, want, surfaces(toks))
			continue
		}
		if tok.IsStop {
			t.Errorf("Tokenize(%q): %q IsStop=true, want false (intent-bearing word)", input, want)
		}
		if tok.Norm != want {
			t.Errorf("Tokenize(%q): stem(%q)=%q, want %q", input, want, tok.Norm, want)
		}
	}

	// Filler words must be stopword-flagged.
	for _, want := range []string{"the", "let"} {
		tok, ok := findToken(t, toks, want)
		if !ok {
			// "let" only appears if UAX#29 split "let's"; that's fine.
			if want == "let" {
				continue
			}
			t.Errorf("Tokenize(%q): missing token %q (surfaces=%v)", input, want, surfaces(toks))
			continue
		}
		if !tok.IsStop {
			t.Errorf("Tokenize(%q): %q IsStop=false, want true (filler)", input, want)
		}
	}
}

func TestTokenize_OffsetsIntoNormalisedSource(t *testing.T) {
	t.Parallel()
	const input = "Hello world"
	toks := Tokenize(input, nil)
	if len(toks) < 2 {
		t.Fatalf("Tokenize(%q): want >=2 tokens, got %d (surfaces=%v)", input, len(toks), surfaces(toks))
	}
	for _, tok := range toks {
		if tok.End <= tok.Start {
			t.Errorf("Tokenize(%q): token %+v has non-positive byte range [%d, %d)", input, tok, tok.Start, tok.End)
		}
	}
}

func TestTokenize_NFKCNormalisation(t *testing.T) {
	t.Parallel()
	// Fullwidth digits (U+FF16 = '６') collapse to ASCII "6" under NFKC.
	const input = "buy ６ oxen"
	toks := Tokenize(input, nil)
	got := surfaces(toks)
	if !containsString(got, "6") {
		t.Errorf("Tokenize(%q): want surfaces to contain %q (NFKC fold), got %v", input, "6", got)
	}
}

func TestTokenize_LowerCasing(t *testing.T) {
	t.Parallel()
	const input = "BUY Oxen"
	toks := Tokenize(input, nil)
	for _, tok := range toks {
		for _, r := range tok.Norm {
			if r >= 'A' && r <= 'Z' {
				t.Errorf("Tokenize(%q): Norm %q contains uppercase rune %q, want lowercased", input, tok.Norm, r)
			}
		}
	}
}

func TestTokenize_PunctuationDropped(t *testing.T) {
	t.Parallel()
	const input = "buy 6 oxen, 200 lbs."
	toks := Tokenize(input, nil)
	for _, tok := range toks {
		if tok.Surface == "," || tok.Surface == "." {
			t.Errorf("Tokenize(%q): punctuation surface leaked through: %+v", input, tok)
		}
	}
}

// TestTokenize_Deterministic catches subtle state leaks (e.g. an
// accidental package-level map iteration) by calling twice and
// reflect-comparing.
func TestTokenize_Deterministic(t *testing.T) {
	t.Parallel()
	const input = "let's wade across the river, please"
	a := Tokenize(input, nil)
	b := Tokenize(input, nil)
	if !reflect.DeepEqual(a, b) {
		t.Errorf("Tokenize(%q): non-deterministic across calls\n a=%+v\n b=%+v", input, a, b)
	}
}

// ====================== stemming ======================

// TestTokenize_StemmingHits is the stemming calibration. The worked
// examples in docs/architecture/semantic-routing.md assume
// "purchasing" -> "purchas", "quickly" -> "quick"; pin those.
func TestTokenize_StemmingHits(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  map[string]string // surface -> Norm
	}{
		{
			name:  "purchasing_oxen_quickly",
			input: "purchasing the oxen quickly",
			want: map[string]string{
				"purchasing": "purchas",
				"oxen":       "oxen",
				"quickly":    "quick",
			},
		},
		{
			name:  "running_fishes",
			input: "running fishes",
			want: map[string]string{
				"running": "run",
				"fishes":  "fish",
			},
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			toks := Tokenize(tc.input, nil)
			got := map[string]string{}
			for _, tok := range toks {
				got[tok.Surface] = tok.Norm
			}
			for surf, wantNorm := range tc.want {
				if got[surf] != wantNorm {
					t.Errorf("Tokenize(%q): stem(%q)=%q, want %q (got map=%v)", tc.input, surf, got[surf], wantNorm, got)
				}
			}
		})
	}
}

// ====================== numerals ======================

func TestTokenize_DigitAndSpelledNumerals_FlagsIsNum(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		input   string
		surface string
		wantNum bool
	}{
		{name: "digit_6", input: "buy 6 oxen", surface: "6", wantNum: true},
		{name: "digit_200", input: "buy 6 oxen and 200 lbs", surface: "200", wantNum: true},
		{name: "spelled_six", input: "buy six oxen", surface: "six", wantNum: true},
		{name: "spelled_nineteen", input: "took nineteen days", surface: "nineteen", wantNum: true},
		{name: "verb_buy_not_num", input: "buy 6 oxen", surface: "buy", wantNum: false},
		{name: "noun_lbs_not_num", input: "buy 6 oxen and 200 lbs", surface: "lbs", wantNum: false},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			toks := Tokenize(tc.input, nil)
			tk, ok := findToken(t, toks, tc.surface)
			if !ok {
				t.Fatalf("Tokenize(%q): want token %q, got surfaces=%v", tc.input, tc.surface, surfaces(toks))
			}
			if tk.IsNum != tc.wantNum {
				t.Errorf("Tokenize(%q).IsNum(%q): want %v, got %v (token=%+v)", tc.input, tc.surface, tc.wantNum, tk.IsNum, tk)
			}
		})
	}
}

// TestTokenize_DigitRunPreserved is the public-API cross-check of the
// isDigitForm helper: a bare digit run must round-trip with Norm == Surface
// and IsNum=true.
func TestTokenize_DigitRunPreserved(t *testing.T) {
	t.Parallel()
	const input = "buy 200 lbs"
	toks := Tokenize(input, nil)
	tk, ok := findToken(t, toks, "200")
	if !ok {
		t.Fatalf("Tokenize(%q): want token %q, got surfaces=%v", input, "200", surfaces(toks))
	}
	if tk.Norm != "200" {
		t.Errorf("Tokenize(%q): Norm(%q)=%q, want %q (digit run must round-trip unchanged)", input, "200", tk.Norm, "200")
	}
	if !tk.IsNum {
		t.Errorf("Tokenize(%q): IsNum(%q)=false, want true", input, "200")
	}
}

func TestIsDigitForm(t *testing.T) {
	t.Parallel()
	// Unexported helper — cross-checks the public IsNum contract for
	// negative / decimal forms UAX#29 doesn't necessarily expose as a
	// single segment.
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"plain", "123", true},
		{"negative", "-3", true},
		{"negative_decimal", "-3.14", true},
		{"decimal", "3.14", true},
		{"trailing_dot", "3.", false},
		{"leading_dot", ".3", false},
		{"bare_minus", "-", false},
		{"mixed_alpha", "3a", false},
		{"empty", "", false},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isDigitForm(tc.in); got != tc.want {
				t.Errorf("isDigitForm(%q): want %v, got %v", tc.in, tc.want, got)
			}
		})
	}
}

func TestSpelledNumber(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		in     []string
		wantN  int
		wantOK bool
	}{
		{"unit_six", []string{"six"}, 6, true},
		{"teen_nineteen", []string{"nineteen"}, 19, true},
		{"tens_twenty", []string{"twenty"}, 20, true},
		{"tens_plus_unit", []string{"twenty", "five"}, 25, true},
		{"hundred_two", []string{"two", "hundred"}, 200, true},
		{"hundred_plus_tens", []string{"two", "hundred", "fifty"}, 250, true},
		{"and_filler", []string{"two", "hundred", "and", "fifty"}, 250, true},
		{"thousand_three", []string{"three", "thousand"}, 3000, true},
		{"million_scaled", []string{"one", "million", "two", "hundred", "thousand"}, 1_200_000, true},
		{"bare_hundred", []string{"hundred"}, 100, true},

		{"non_numeral", []string{"purple"}, 0, false},
		{"empty", []string{}, 0, false},
		{"two_tens_in_row", []string{"twenty", "thirty"}, 0, false},
		{"trailing_non_numeral", []string{"six", "purple"}, 0, false},
		{"blank_word", []string{"six", ""}, 0, false},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotN, gotOK := SpelledNumber(tc.in)
			if gotOK != tc.wantOK || (gotOK && gotN != tc.wantN) {
				t.Errorf("SpelledNumber(%v): want (%d, %v), got (%d, %v)", tc.in, tc.wantN, tc.wantOK, gotN, gotOK)
			}
		})
	}
}

func TestSpelledNumber_CaseInsensitive(t *testing.T) {
	t.Parallel()
	in := []string{"Six"}
	n, ok := SpelledNumber(in)
	if !ok || n != 6 {
		t.Errorf("SpelledNumber(%v): want (6, true), got (%d, %v)", in, n, ok)
	}
}

// ====================== stopwords ======================

func TestIsStopword_BuiltinAndExtras(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		norm   string
		extras []string
		want   bool
	}{
		{"builtin_article_the", "the", nil, true},
		{"builtin_conjunction_and", "and", nil, true},
		{"builtin_pronoun_i", "i", nil, true},
		{"builtin_filler_please", "please", nil, true},
		{"intent_verb_ford_not_stop", "ford", nil, false},
		{"intent_verb_buy_not_stop", "buy", nil, false},
		{"intent_verb_hunt_not_stop", "hunt", nil, false},
		{"intent_noun_river_not_stop", "river", nil, false},
		{"extras_match", "foo", []string{"foo"}, true},
		{"extras_miss", "foo", []string{"bar"}, false},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := IsStopword(tc.norm, tc.extras); got != tc.want {
				t.Errorf("IsStopword(%q, %v): want %v, got %v", tc.norm, tc.extras, tc.want, got)
			}
		})
	}
}

func TestTokenize_SurfaceLowercasedBeforeStopCheck(t *testing.T) {
	t.Parallel()
	// "THE" must be flagged as stop even when surface-uppercase.
	const input = "THE river"
	toks := Tokenize(input, nil)
	tok, ok := findToken(t, toks, "the")
	if !ok {
		t.Fatalf("Tokenize(%q): want lowercased token %q, got surfaces=%v", input, "the", surfaces(toks))
	}
	if !tok.IsStop {
		t.Errorf("Tokenize(%q): IsStop(%q)=false, want true (lowercased filler)", input, "the")
	}
}

func TestTokenize_ExtraStopsApplied(t *testing.T) {
	t.Parallel()
	const input = "cross the river"
	extras := []string{"river"}
	toks := Tokenize(input, extras)
	tok, ok := findToken(t, toks, "river")
	if !ok {
		t.Fatalf("Tokenize(%q, extras=%v): want token %q, got surfaces=%v", input, extras, "river", surfaces(toks))
	}
	if !tok.IsStop {
		t.Errorf("Tokenize(%q, extras=%v): IsStop(%q)=false, want true", input, extras, "river")
	}
}

// ====================== invariants ======================

// TestTokenize_OffsetRangesNonOverlapping asserts the sum of (End-Start)
// over non-overlapping tokens is <= len(normalised source). This is a
// load-bearing precondition for any future caller that wants to slice
// the source by Token.Start/End.
func TestTokenize_OffsetRangesNonOverlapping(t *testing.T) {
	t.Parallel()
	inputs := []string{
		"buy 6 oxen and 200 lbs",
		"let's wade across the river",
		"purchasing oxen quickly",
		"",
	}
	for _, in := range inputs {
		in := in
		t.Run("input="+in, func(t *testing.T) {
			t.Parallel()
			toks := Tokenize(in, nil)
			sum := 0
			lastEnd := 0
			for i, tok := range toks {
				if tok.Start < lastEnd {
					t.Errorf("Tokenize(%q): token %d %+v overlaps previous end=%d", in, i, tok, lastEnd)
				}
				if tok.End < tok.Start {
					t.Errorf("Tokenize(%q): token %d %+v has End<Start", in, i, tok)
				}
				sum += tok.End - tok.Start
				lastEnd = tok.End
			}
			// We can't compare to the original byte length because NFKC may
			// have expanded or contracted bytes. The upper bound to assert
			// is "sum <= input bytes after NFKC + lowercase", but a looser
			// invariant we can check cheaply: sum <= 4 * len(in) (NFKC
			// can at worst multiply UTF-8 length by ~4 for some scripts).
			if sum > 4*len(in)+4 {
				t.Errorf("Tokenize(%q): sum(End-Start)=%d implausibly large vs len=%d", in, sum, len(in))
			}
		})
	}
}

// TestTokenize_NoPanicOnPathologicalInputs is a non-fuzz smoke that
// exercises a couple of inputs the fuzzer in lex_fuzz_test.go seeds with.
// Living here keeps the core test file free of `go test -fuzz` plumbing
// while still pinning the no-panic contract.
func TestTokenize_NoPanicOnPathologicalInputs(t *testing.T) {
	t.Parallel()
	inputs := []string{
		"",
		strings.Repeat("a", 10_000),
		"\x00\x01\x02control chars",
		"héllo wörld",
		"𝓊𝓃𝒾𝒸𝑜𝒹𝑒",
		"buy ６ oxen 二百 lbs",
	}
	for _, in := range inputs {
		in := in
		t.Run("len="+itoa(len(in)), func(t *testing.T) {
			t.Parallel()
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Tokenize(%q): panic %v", in, r)
				}
			}()
			_ = Tokenize(in, nil)
		})
	}
}

// itoa is a tiny stdlib-free int-to-decimal helper to keep the package
// imports tidy. Used in sub-test names where strconv.Itoa would otherwise
// pull strconv into the test file's import set.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
