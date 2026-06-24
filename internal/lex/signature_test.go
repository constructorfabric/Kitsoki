// Tests for the [Signature] public API.
//
// The worked-example table in docs/architecture/semantic-routing.md is
// the calibration corpus. Beyond fixed inputs we also encode the stated
// invariants as property tests: order, whitespace, case, punctuation, and
// stopword content must not affect the signature.
package lex

import (
	"strings"
	"testing"
	"testing/quick"
)

// ====================== fixed corpus ======================

// TestSignature_EquivalenceGroups pins the worked-example table.
// Inputs A1 and A2 belong to the same equivalence group; B and C are
// distinct. We don't pin exact hex bytes — only the equivalence shape.
func TestSignature_EquivalenceGroups(t *testing.T) {
	t.Parallel()
	const (
		a1 = "buy 6 oxen and 200 lbs of food"
		a2 = "let's buy six oxen, 200 lbs food"
		b  = "purchase 6 oxen + 200lbs food"
		c  = "buy 6 oxen"
	)
	sigA1 := Signature(a1, nil)
	sigA2 := Signature(a2, nil)
	sigB := Signature(b, nil)
	sigC := Signature(c, nil)

	if sigA1 == "" || sigA2 == "" || sigB == "" || sigC == "" {
		t.Fatalf("all corpus signatures must be non-empty:\n a1=%q sig=%q\n a2=%q sig=%q\n b =%q sig=%q\n c =%q sig=%q",
			a1, sigA1, a2, sigA2, b, sigB, c, sigC)
	}
	if sigA1 != sigA2 {
		t.Errorf("group A: Signature(%q)=%q must equal Signature(%q)=%q", a1, sigA1, a2, sigA2)
	}
	if sigA1 == sigB {
		t.Errorf("group A vs B: signatures must differ (purchase->purchas != buy); a=%q b=%q both=%q", a1, b, sigA1)
	}
	if sigA1 == sigC {
		t.Errorf("group A vs C: signatures must differ (extra content tokens); a=%q c=%q both=%q", a1, c, sigA1)
	}
	if sigB == sigC {
		t.Errorf("group B vs C: signatures must differ; b=%q c=%q both=%q", b, c, sigB)
	}
}

// ====================== shape ======================

func TestSignature_EmptyInput(t *testing.T) {
	t.Parallel()
	if got := Signature("", nil); got != "" {
		t.Errorf("Signature(%q): want %q, got %q", "", "", got)
	}
}

// TestSignature_OnlyStopwordsYieldsEmpty pins the documented sentinel
// for an input that produces no content tokens: the empty string. This
// is the value the orchestrator uses to skip the turncache when there's
// nothing to key on.
func TestSignature_OnlyStopwordsYieldsEmpty(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
	}{
		{"single_article", "the"},
		{"and_i_please", "the and i please"},
		{"all_stopwords", "let's please just the and of to"},
		{"whitespace_only", "   \t  "},
		{"punctuation_only", ",. ;!"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Signature(tc.input, nil); got != "" {
				t.Errorf("Signature(%q): want %q (no content tokens), got %q", tc.input, "", got)
			}
		})
	}
}

func TestSignature_Length(t *testing.T) {
	t.Parallel()
	const input = "hunt the deer"
	sig := Signature(input, nil)
	if got := len(sig); got != 16 {
		t.Errorf("Signature(%q): want length 16 (64-bit hex prefix), got %d (sig=%q)", input, got, sig)
	}
}

func TestSignature_HexAlphabet(t *testing.T) {
	t.Parallel()
	const input = "hunt the deer"
	sig := Signature(input, nil)
	for i, r := range sig {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			t.Errorf("Signature(%q): rune %d (%q) is not in [0-9a-f]; sig=%q", input, i, r, sig)
			break
		}
	}
}

// ====================== invariants ======================

func TestSignature_OrderInsensitive(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		a, b string
	}{
		{"two_words", "hunt deer", "deer hunt"},
		{"three_words", "hunt deer please", "deer hunt please"},
		{"five_words", "buy 6 oxen food lbs", "lbs food oxen 6 buy"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			x := Signature(tc.a, nil)
			y := Signature(tc.b, nil)
			if x != y {
				t.Errorf("Signature must be word-order insensitive:\n Signature(%q)=%q\n Signature(%q)=%q", tc.a, x, tc.b, y)
			}
		})
	}
}

func TestSignature_FillerAndStopwordInsensitive(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		a, b string
	}{
		{"add_please_just", "buy oxen", "please just buy the oxen"},
		{"add_letsgo_filler", "buy oxen", "let's please and buy the oxen for me"},
		{"strip_articles_and_pronouns", "wade river", "i wade the river"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			x := Signature(tc.a, nil)
			y := Signature(tc.b, nil)
			if x != y {
				t.Errorf("Signature must ignore stopword filler:\n Signature(%q)=%q\n Signature(%q)=%q", tc.a, x, tc.b, y)
			}
		})
	}
}

func TestSignature_WhitespaceCasePunctuationInsensitive(t *testing.T) {
	t.Parallel()
	const base = "buy 6 oxen and 200 lbs of food"
	jitters := []string{
		"BUY 6 OXEN AND 200 LBS OF FOOD",
		"buy  6   oxen and 200 lbs of food",  // extra whitespace
		"buy 6, oxen and 200 lbs, of food",   // extra punctuation
		"buy 6 oxen!!! and 200 lbs of food.", // trailing punct
		"\tbuy 6 oxen and 200 lbs of food\n", // surrounding whitespace
		"Buy 6 Oxen And 200 Lbs Of Food",     // title case
	}
	want := Signature(base, nil)
	for _, j := range jitters {
		if got := Signature(j, nil); got != want {
			t.Errorf("Signature must be jitter-insensitive: Signature(%q)=%q vs Signature(%q)=%q", base, want, j, got)
		}
	}
}

func TestSignature_SpelledNumberFoldsToDigit(t *testing.T) {
	t.Parallel()
	x := Signature("buy six oxen", nil)
	y := Signature("buy 6 oxen", nil)
	if x != y {
		t.Errorf("Signature(spelled) must fold to Signature(digit): %q vs %q", x, y)
	}
}

func TestSignature_ExtraStopsAffect(t *testing.T) {
	t.Parallel()
	const input = "ford the river"
	base := Signature(input, nil)
	withExtra := Signature(input, []string{"ford"})
	if base == withExtra {
		t.Errorf("extraStops must affect the signature; Signature(%q, nil)=%q == Signature(%q, [ford])=%q", input, base, input, withExtra)
	}
}

func TestSignature_Deterministic(t *testing.T) {
	t.Parallel()
	const input = "buy 6 oxen and 200 lbs of food"
	a := Signature(input, nil)
	b := Signature(input, nil)
	if a != b {
		t.Errorf("Signature(%q): non-deterministic across calls: %q vs %q", input, a, b)
	}
}

// TestSignature_StopwordContentDoesNotChangeSig pins stopword-invariance:
// two inputs that differ only in *which* stopwords they include must
// produce the same signature.
func TestSignature_StopwordContentDoesNotChangeSig(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		a, b string
	}{
		{"replace_let_with_please", "let's buy oxen", "please buy oxen"},
		{"add_or_remove_just", "i hunt deer", "i just hunt the deer"},
		{"swap_an_for_the", "buy an ox", "buy the ox"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			x := Signature(tc.a, nil)
			y := Signature(tc.b, nil)
			if x != y {
				t.Errorf("Signature must ignore stopword content:\n Signature(%q)=%q\n Signature(%q)=%q", tc.a, x, tc.b, y)
			}
		})
	}
}

// ====================== property tests ======================

// TestSignature_PermutationInvariant samples N random permutations of a
// fixed content-word multiset and asserts every permutation produces the
// same signature. This encodes word-order insensitivity as a property
// rather than a fixed table.
func TestSignature_PermutationInvariant(t *testing.T) {
	t.Parallel()
	base := []string{"buy", "6", "oxen", "200", "lbs", "food"}
	want := Signature(strings.Join(base, " "), nil)
	if want == "" {
		t.Fatalf("Signature(%q): want non-empty baseline, got empty", strings.Join(base, " "))
	}

	const samples = 50
	for i := 0; i < samples; i++ {
		perm := permuted(base, int64(i)*1103515245+12345)
		joined := strings.Join(perm, " ")
		if got := Signature(joined, nil); got != want {
			t.Errorf("Signature permutation invariant violated:\n base=%q -> %q\n perm=%q -> %q", strings.Join(base, " "), want, joined, got)
		}
	}
}

// permuted returns a Fisher-Yates shuffle of ss using a deterministic
// LCG keyed by seed. Tests use a fixed seed sequence so failures are
// reproducible.
func permuted(ss []string, seed int64) []string {
	out := append([]string(nil), ss...)
	rng := seed
	for i := len(out) - 1; i > 0; i-- {
		rng = rng*1103515245 + 12345
		j := int(uint64(rng)>>32) % (i + 1)
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// TestSignature_Length_QuickCheck samples arbitrary strings via
// testing/quick and asserts the invariant: the signature is either
// exactly 16 hex chars or the empty string (the no-content sentinel).
func TestSignature_Length_QuickCheck(t *testing.T) {
	t.Parallel()
	prop := func(s string) bool {
		sig := Signature(s, nil)
		if sig == "" {
			return true
		}
		if len(sig) != 16 {
			t.Logf("Signature(%q)=%q: length %d, want 16 or empty", s, sig, len(sig))
			return false
		}
		for _, r := range sig {
			if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
				t.Logf("Signature(%q)=%q: contains non-hex %q", s, sig, r)
				return false
			}
		}
		return true
	}
	cfg := &quick.Config{MaxCount: 200}
	if err := quick.Check(prop, cfg); err != nil {
		t.Errorf("quick.Check failed: %v", err)
	}
}
