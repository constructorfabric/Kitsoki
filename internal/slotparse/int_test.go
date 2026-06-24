// Tests for [ParseInt]. Structure mirrors lex_test.go: a banner per
// strategy block, table-driven sub-cases, then property/invariant
// tests at the bottom. Each sub-test runs in parallel.
package slotparse

import (
	"fmt"
	"strconv"
	"testing"
)

// ====================== ParseInt: happy path ======================

func TestParseInt_DigitAndSpelled(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		input     string
		wantValue int
		wantOK    bool
		wantReas  string
	}{
		// Digit form (Strategy 1).
		{"single_digit_6", "6", 6, true, "digit"},
		{"three_digits", "200", 200, true, "digit"},
		{"zero", "0", 0, true, "digit"},

		// Spelled cardinal (Strategy 2).
		{"spelled_six", "six", 6, true, "spelled"},
		{"spelled_nineteen", "nineteen", 19, true, "spelled"},
		{"spelled_twenty_five", "twenty five", 25, true, "spelled"},
		{"spelled_two_hundred", "two hundred", 200, true, "spelled"},
		{"spelled_two_hundred_fifty", "two hundred fifty", 250, true, "spelled"},
		{"spelled_two_hundred_and_fifty", "two hundred and fifty", 250, true, "spelled"},

		// Leading filler — stopwords + non-stop non-numerals are
		// skipped until the first int-shaped run.
		{"leading_stopword_please", "please six", 6, true, "spelled"},
		{"leading_non_stop_buy", "buy six", 6, true, "spelled"},
		{"leading_filler_please_buy", "please buy six", 6, true, "spelled"},
		{"leading_filler_then_digit", "please buy 6 oxen", 6, true, "digit"},

		// Trailing junk after a successful match does NOT extend
		// Consumed past the int run.
		{"trailing_non_numeral", "six purple", 6, true, "spelled"},
		{"trailing_oxen", "6 oxen", 6, true, "digit"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ParseInt(tok(t, tc.input))
			if got.OK != tc.wantOK {
				t.Fatalf("ParseInt(%q): OK=%v, want %v (got=%+v)", tc.input, got.OK, tc.wantOK, got)
			}
			if !tc.wantOK {
				return
			}
			if got.Value != tc.wantValue {
				t.Errorf("ParseInt(%q): Value=%v, want %v (got=%+v)", tc.input, got.Value, tc.wantValue, got)
			}
			if got.Reason != tc.wantReas {
				t.Errorf("ParseInt(%q): Reason=%q, want %q", tc.input, got.Reason, tc.wantReas)
			}
			if len(got.Consumed) == 0 {
				t.Errorf("ParseInt(%q): Consumed empty on OK=true (got=%+v)", tc.input, got)
			}
		})
	}
}

// ====================== ParseInt: misses ======================

func TestParseInt_MissesAndEdgeCases(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"only_stopwords", "the and please"},
		{"no_number", "buy oxen"},
		{"mixed_alnum_200lbs", "200lbs"}, // pinned: lex emits one IsNum=false token
		{"trailing_word", "buy purple"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ParseInt(tok(t, tc.input))
			if got.OK {
				t.Errorf("ParseInt(%q): want OK=false, got %+v", tc.input, got)
			}
			if got.Value != nil {
				t.Errorf("ParseInt(%q) miss: Value should be zero-value (nil), got %#v", tc.input, got.Value)
			}
		})
	}
}

// TestParseInt_TrailingJunkConsumedRangeEndsAtSix encodes the
// proposal's spec line: "trailing junk `"six purple"` (OK=true,
// Value=6, Consumed range ends at "six")."
func TestParseInt_TrailingJunkConsumedRangeEndsAtSix(t *testing.T) {
	t.Parallel()
	toks := tok(t, "six purple")
	got := ParseInt(toks)
	if !got.OK || got.Value != 6 {
		t.Fatalf("ParseInt(%q): want (6, true), got Value=%v OK=%v", "six purple", got.Value, got.OK)
	}
	if !rangesEqual(got.Consumed, []TokenRange{{Start: 0, End: 1}}) {
		t.Errorf("ParseInt(%q): Consumed=%+v, want [{0 1}] — range must end at \"six\"", "six purple", got.Consumed)
	}
}

// ====================== ParseInt: 200lbs pinning ======================

// TestParseInt_200lbsBehaviour pins the documented behaviour: lex
// emits "200lbs" as a single IsNum=false token, so the digit-prefix
// is NOT picked up. The matcher relies on this exact behaviour to
// route such inputs to the LLM tier (see the slot parser table in
// docs/architecture/semantic-routing.md).
func TestParseInt_200lbsBehaviour(t *testing.T) {
	t.Parallel()
	toks := tok(t, "200lbs")
	if len(toks) != 1 {
		t.Fatalf("precondition: lex.Tokenize(%q) should emit 1 token, got %d (%+v)", "200lbs", len(toks), toks)
	}
	if toks[0].IsNum {
		t.Fatalf("precondition: lex.Tokenize(%q)[0].IsNum should be false, got true (token=%+v)", "200lbs", toks[0])
	}
	got := ParseInt(toks)
	if got.OK {
		t.Errorf("ParseInt(%q): want OK=false (mixed alnum surface), got %+v", "200lbs", got)
	}
}

// ====================== ParseInt: property ======================

// TestParseInt_DigitRoundTrip is the property test from the spec:
// for any int n in [0, 1_000_000], ParseInt(Tokenize(strconv.Itoa(n)))
// returns n with OK=true. We sample 1000 values across the range
// rather than running 1M ints in CI.
func TestParseInt_DigitRoundTrip(t *testing.T) {
	t.Parallel()
	// Deterministic sampling: 0, 1, then a stride that hits 1000
	// distinct values across [0, 1_000_000].
	for i, n := range sampleInts(1000, 0, 1_000_000) {
		i, n := i, n
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			t.Parallel()
			got := ParseInt(tok(t, strconv.Itoa(n)))
			if !got.OK {
				t.Fatalf("sample %d: ParseInt(%q): want OK=true (round-trip), got %+v", i, strconv.Itoa(n), got)
			}
			if got.Value != n {
				t.Errorf("sample %d: ParseInt(%q): Value=%v, want %d", i, strconv.Itoa(n), got.Value, n)
			}
			if got.Reason != "digit" {
				t.Errorf("sample %d: ParseInt(%q): Reason=%q, want \"digit\"", i, strconv.Itoa(n), got.Reason)
			}
		})
	}
}

// sampleInts returns count integers evenly spaced over [lo, hi]
// inclusive. lo and hi are always included.
func sampleInts(count, lo, hi int) []int {
	if count <= 1 {
		return []int{lo}
	}
	out := make([]int, 0, count)
	for i := 0; i < count; i++ {
		// integer-linear interpolation; closed at both ends
		x := lo + (hi-lo)*i/(count-1)
		out = append(out, x)
	}
	return out
}
