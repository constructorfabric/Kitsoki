package slotparse

import (
	"strconv"

	"kitsoki/internal/lex"
)

// maxSpelledWindow bounds how many consecutive alpha tokens the
// spelled-cardinal scan will fold into one number ("two hundred fifty"
// is three). The cap is an arbitrary upper bound that rejects
// word-problem-length inputs: a legitimate spelled cardinal in this
// dialect is a handful of words, so a 16-token run is far more likely
// to be prose than a number. [ParseMoney]'s spelled+unit scan reuses
// the same window.
const maxSpelledWindow = 16

// ParseInt accepts the first int-shaped run in tokens. It tries two
// strategies in order:
//
//  1. Digit form — the first token whose Surface is a clean decimal
//     run ("6", "200"). Mixed-alphanumeric tokens ("200lbs") are
//     deliberately NOT scooped up — lex emits them as a single
//     IsNum=false segment and we leave them alone (pinned: see
//     int_test.go).
//
//  2. Spelled cardinal — scans forward for the first run of pure-
//     letter tokens that [lex.SpelledNumber] accepts. Greedy: the
//     longest accepting prefix from each candidate start position
//     wins. "buy six oxen" produces Value=6 (the parser walks past
//     "buy" because SpelledNumber rejects it, lands on "six").
//
// Stopwords ("please", "the") are skipped wholesale. Trailing non-
// numeric tokens after a successful match are NOT consumed —
// "six purple" parses Value=6 with Consumed ending at "six".
//
// Returns OK=false when no int-shaped run exists. Reason is "digit"
// or "spelled".
func ParseInt(tokens []lex.Token) Result {
	if len(tokens) == 0 {
		return Result{}
	}

	// Strategy 1 — first digit-form token anywhere in the stream.
	// Walking the stream (rather than only checking the first
	// non-stop position) means "please buy six oxen for 240" still
	// finds 240. We prefer digit form over spelled form because
	// digit form is unambiguous and zero-allocation to validate.
	for i, t := range tokens {
		if t.IsStop {
			continue
		}
		if t.IsNum && isDigitFormSurface(t.Surface) {
			if n, err := strconv.Atoi(t.Surface); err == nil {
				return Result{
					Value:    n,
					Consumed: []TokenRange{{Start: i, End: i + 1}},
					OK:       true,
					Reason:   "digit",
				}
			}
		}
	}

	// Strategy 2 — spelled cardinal. Walk every potential starting
	// position; at each, grow a pure-alpha window forward and find
	// the longest SpelledNumber-accepting prefix. The greedy growth
	// is bounded to maxSpelledWindow tokens — spelled cardinals
	// beyond that mean the user pasted a word problem at us. We take the FIRST start
	// position that produces an accepting run so that "buy six oxen"
	// matches "six" without scanning forward past it.
	for start := 0; start < len(tokens); start++ {
		if tokens[start].IsStop {
			continue
		}
		// Require the candidate start to be a pure-letter word so
		// we don't pointlessly retry the digit-form path on tokens
		// like "200lbs" or "$".
		if !isAlphaSurface(tokens[start].Surface) {
			continue
		}
		// SpelledNumber needs the candidate start to actually be a
		// recognised cardinal word — otherwise we waste time on
		// every English word. Single-token check is cheap.
		if _, ok := lex.SpelledNumber([]string{tokens[start].Surface}); !ok {
			continue
		}
		end := start
		for end < len(tokens) && end-start < maxSpelledWindow && isAlphaSurface(tokens[end].Surface) {
			end++
		}
		for hi := end; hi > start; hi-- {
			words := make([]string, 0, hi-start)
			for i := start; i < hi; i++ {
				words = append(words, tokens[i].Surface)
			}
			if n, ok := lex.SpelledNumber(words); ok {
				return Result{
					Value:    n,
					Consumed: []TokenRange{{Start: start, End: hi}},
					OK:       true,
					Reason:   "spelled",
				}
			}
		}
	}

	return Result{}
}

// isDigitFormSurface reports whether s is a non-negative decimal
// integer surface ("0", "6", "200"). Mirrors lex.isDigitForm's
// shape but rejects decimals and negatives because ParseInt's
// strconv.Atoi step would refuse them anyway.
func isDigitFormSurface(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// isAlphaSurface reports whether every byte in s is an ASCII letter.
// Used to bound the spelled-cardinal window. Non-ASCII letters
// (accented vowels etc.) are out of scope — lex lowercases input but
// SpelledNumber's word table is ASCII-only by design.
func isAlphaSurface(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) {
			return false
		}
	}
	return true
}
