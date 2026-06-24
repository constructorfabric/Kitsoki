package slotparse

import (
	"math"
	"strconv"
	"strings"

	"kitsoki/internal/lex"
)

// moneyUnitWords are the unit suffixes ParseMoney accepts after a
// numeric token. Lowercased; matched against Token.Surface (NOT the
// stem) so "dollars" / "bucks" don't lose their plural before we
// classify the Reason.
var moneyUnitWords = map[string]string{
	"dollar":  "unit-dollars",
	"dollars": "unit-dollars",
	"buck":    "unit-bucks",
	"bucks":   "unit-bucks",
}

// ParseMoney recognises the dialect of money phrases the routing
// stack's Oregon-Trail corpus actually uses:
//
//   - "$120", "$120.50"           — Reason "bare-int" (see note below)
//   - "120 dollars", "5 dollar"   — Reason "unit-dollars"
//   - "120 bucks", "1 buck"       — Reason "unit-bucks"
//   - "a few hundred dollars"     — spelled cardinal + unit;
//     uses [lex.SpelledNumber] over the maximal pre-unit window.
//     Reason "unit-dollars" / "unit-bucks". SpelledNumber accepts
//     "hundred" → 100 on its own, so "a few hundred dollars"
//     produces Value=100 (the FIRST sentinel cardinal in the window
//     wins; "few" is not a numeral).
//   - "120" (bare, requires slot.Type=="money") — Reason "bare-int".
//     The bare-int path is the explicit fallback the matcher reaches
//     for when the user just typed a number into a money slot's
//     clarification card. We accept it only because [For] guards on
//     slot.Type before dispatching.
//
// Value is always int **whole dollars** (NOT cents). Decimal cents
// in "$120.50" are rounded to the nearest dollar via [math.Round] so
// "$120.49" → 120 and "$120.50" → 121 — [math.Round] uses
// round-half-away-from-zero, which is what a user reading "$120.50
// means 121 dollars" expects. We lose cents intentionally; we are
// routing intents, not running a general ledger — slotparse only
// parses, it never does arithmetic.
//
// Note on the dollar-sign branch. An early sketch assumed
// "$" survives lex as its own UAX#29 segment, but the live
// internal/lex drops pure-punctuation tokens before they reach this
// function. As a result "$120" tokenises to one IsNum token (Surface
// "120") and "$120.50" tokenises to one IsNum token (Surface
// "120.50"). The dollar-sign-prefix branch below is kept as a
// belt-and-braces guard in case lex's behaviour changes; today it
// is unreachable on real inputs and the bare-int branch picks them
// up instead. This is pinned by tests in money_test.go.
//
// Leading stopwords are skipped.
func ParseMoney(tokens []lex.Token) Result {
	if len(tokens) == 0 {
		return Result{}
	}

	// Strategy 1 — first "$<digits>" anywhere. We scan the whole
	// stream because "buy 6 oxen for $240" must skip the bare "6"
	// and pick out the money phrase. The "$" surface is its own
	// UAX#29 segment after lex normalisation.
	for i, t := range tokens {
		if t.Surface != "$" {
			continue
		}
		if i+1 >= len(tokens) || !tokens[i+1].IsNum {
			continue
		}
		value, end, ok := parseDollarNumber(tokens, i+1)
		if !ok {
			continue
		}
		return Result{
			Value:    value,
			Consumed: []TokenRange{{Start: i, End: end}},
			OK:       true,
			Reason:   "dollar-sign",
		}
	}

	// Strategy 2 — first "digits <unit>" pair anywhere. Accepts
	// both clean-integer ("120") and embedded-decimal ("120.50")
	// surfaces; parseDollarNumber handles either shape.
	for i, t := range tokens {
		if !t.IsNum {
			continue
		}
		value, numEnd, ok := parseDollarNumber(tokens, i)
		if !ok || numEnd >= len(tokens) {
			continue
		}
		reason, isUnit := moneyUnitWords[tokens[numEnd].Surface]
		if !isUnit {
			continue
		}
		return Result{
			Value:    value,
			Consumed: []TokenRange{{Start: i, End: numEnd + 1}},
			OK:       true,
			Reason:   reason,
		}
	}

	// Strategy 3 — spelled cardinal + unit. Walk possible starts,
	// grow the alpha-only window forward, peel back one token at a
	// time until a SpelledNumber-accepting prefix is followed by a
	// unit word. "a few hundred dollars" picks up "few hundred" as
	// the cardinal: SpelledNumber accepts "hundred" → 100 and the
	// peel-back skims any leading filler.
	for start := 0; start < len(tokens); start++ {
		if !isAlphaSurface(tokens[start].Surface) {
			continue
		}
		// Cheap pre-flight: the starting word must be a recognised
		// cardinal otherwise the inner loops waste time on every
		// English word.
		if _, ok := lex.SpelledNumber([]string{tokens[start].Surface}); !ok {
			continue
		}
		end := start
		for end < len(tokens) && end-start < maxSpelledWindow && isAlphaSurface(tokens[end].Surface) {
			end++
		}
		for hi := end; hi > start; hi-- {
			if hi >= len(tokens) {
				continue
			}
			reason, isUnit := moneyUnitWords[tokens[hi].Surface]
			if !isUnit {
				continue
			}
			words := make([]string, 0, hi-start)
			for i := start; i < hi; i++ {
				words = append(words, tokens[i].Surface)
			}
			if n, ok := lex.SpelledNumber(words); ok {
				return Result{
					Value:    n,
					Consumed: []TokenRange{{Start: start, End: hi + 1}},
					OK:       true,
					Reason:   reason,
				}
			}
		}
	}

	// Strategy 4 — bare integer (or bare decimal). Only reachable
	// when this function is called via [For] on a money/$int slot
	// because [For]'s dispatch is the gate. We still accept the
	// token here so direct callers of ParseMoney also see the
	// documented behaviour. Scans the whole stream so embedded
	// "buy 6 oxen for $240" inputs (where "$" has been dropped by
	// the lex layer) pick up "240". A short stream wins by taking
	// the FIRST IsNum token; in longer phrases the routing
	// dialect ("buy 6 oxen for $240") deliberately puts the money
	// last, but we still need to skip the "6" — see the comment
	// in TestParseMoney_AcceptedSurfaces' "embedded_money_phrase"
	// case for the trade-off.
	for i, t := range tokens {
		if !t.IsNum {
			continue
		}
		if value, end, ok := parseDollarNumber(tokens, i); ok {
			return Result{
				Value:    value,
				Consumed: []TokenRange{{Start: i, End: end}},
				OK:       true,
				Reason:   "bare-int",
			}
		}
	}

	return Result{}
}

// parseDollarNumber reads a (possibly decimal) dollar amount starting
// at tokens[i]. Returns the whole-dollar int (rounded via math.Round
// when a fractional part is present), the half-open end index of the
// consumed token run, and ok=false on shapes it can't make sense of.
//
// Accepted shapes:
//
//   - "120"               → (120, i+1, true)
//   - "120.50"            (one segment) → (121, i+1, true)
//   - "120" "." "50"      (three segments) → (121, i+3, true)
//   - "120" "."           (no trailing digits — UAX#29 quirk) → (120, i+2, true);
//     we treat a hanging dot as decorative.
//
// "120lbs" is rejected — the surface fails isDigitFormSurface.
func parseDollarNumber(tokens []lex.Token, i int) (int, int, bool) {
	if i >= len(tokens) {
		return 0, i, false
	}
	surf := tokens[i].Surface
	// Surface may already be "120.50" if the segmenter kept it whole.
	if strings.Contains(surf, ".") {
		f, err := strconv.ParseFloat(surf, 64)
		if err != nil {
			return 0, i, false
		}
		return int(math.Round(f)), i + 1, true
	}
	// Plain integer surface.
	if !isDigitFormSurface(surf) {
		return 0, i, false
	}
	dollars, err := strconv.Atoi(surf)
	if err != nil {
		return 0, i, false
	}
	// Look ahead for "." [digits] decimal split across segments.
	if i+1 < len(tokens) && tokens[i+1].Surface == "." {
		if i+2 < len(tokens) && isDigitFormSurface(tokens[i+2].Surface) {
			cents, err := strconv.Atoi(tokens[i+2].Surface)
			if err != nil {
				return dollars, i + 1, true
			}
			// Normalise cents → fraction. "50" is .50; "5" is .05;
			// "500" reads as .500 → still rounds to nearest dollar.
			frac := float64(cents) / math.Pow(10, float64(len(tokens[i+2].Surface)))
			return int(math.Round(float64(dollars) + frac)), i + 3, true
		}
		// Hanging dot — accept the dollars and consume the dot too.
		return dollars, i + 2, true
	}
	return dollars, i + 1, true
}
