package lex

import (
	"strings"
	"unicode"

	"github.com/clipperhouse/uax29/v2/words"
	"github.com/kljensen/snowball"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"golang.org/x/text/unicode/norm"
)

// Token is one normalised word produced by Tokenize. It is a plain value
// with no pointers or invariants beyond Start <= End; the zero Token is a
// valid empty token and Tokenize never emits one. Norm is the routing key
// (callers match on it), Surface is what to echo back to a user, and the
// IsStop/IsNum flags let consumers filter without re-deriving the
// classification. Start/End are byte offsets into the NORMALISED source
// (see the package doc), not the raw caller string.
type Token struct {
	Surface string // original (NFKC + lowercased) text, for echoing
	Norm    string // lowercased + stemmed; the routing key
	Lemma   string // lemma if available, else == Norm (no dictionary source)
	IsStop  bool   // stopword (builtin list ∪ extraStops)
	IsNum   bool   // looks like a number (digit form or single-word numeral)
	Start   int    // byte offset in the NFKC+lowered source
	End     int    // byte offset in the NFKC+lowered source (exclusive)
}

// englishLower is a reusable lowercaser for English. cases.Caser is
// documented as safe for concurrent use.
var englishLower = cases.Lower(language.English)

// Tokenize splits s into Tokens using UAX#29 word boundaries.
//
// Pipeline: NFKC-normalise -> lowercase (English) -> UAX#29 segment
// -> drop whitespace/punctuation-only segments -> Porter2 stem alphabetic
// surfaces -> flag IsStop / IsNum. See the package doc for details.
//
// extraStops is appended to the builtin stopword list; pass nil for the
// default. Each extraStops entry is matched literally against the
// post-stem Norm string, so callers should provide lowercased forms.
//
// Returns nil for the empty string and for input that segments into no
// word-like tokens (the empty and nil slices are interchangeable to
// callers). Safe for concurrent use: holds no state and never mutates its
// arguments.
func Tokenize(s string, extraStops []string) []Token {
	if s == "" {
		return nil
	}

	// 1. NFKC normalisation collapses fullwidth digits, ligatures, and
	//    compatibility characters into their canonical forms.
	s = norm.NFKC.String(s)
	// 2. Lowercase using language-aware Caser. Doing this before
	//    tokenisation keeps Token.Start/End consistent with the
	//    string callers can reconstruct deterministically from the
	//    same input + the same pipeline.
	s = englishLower.String(s)

	var out []Token
	iter := words.FromString(s)
	for iter.Next() {
		surface := iter.Value()
		start, end := iter.Start(), iter.End()
		if !isWordlike(surface) {
			continue
		}

		stem := stemWord(surface)
		isNum := looksNumeric(surface)
		// Check stopword status against BOTH the pre-stem surface and
		// the post-stem norm — Porter2 mutates some stoplist entries
		// (e.g. "please" -> "pleas") so a stem-only check misses them.
		// We also catch single-token contractions ("let's") whose
		// surface contains an apostrophe and won't appear in any stem
		// form.
		isStop := IsStopword(stem, extraStops) || IsStopword(surface, extraStops)

		out = append(out, Token{
			Surface: surface,
			Norm:    stem,
			Lemma:   stem, // no dictionary lemma source; spec says fall back to Norm
			IsStop:  isStop,
			IsNum:   isNum,
			Start:   start,
			End:     end,
		})
	}
	return out
}

// isWordlike reports whether a UAX#29 segment is something we want to
// keep as a Token. Pure-whitespace and pure-punctuation segments are
// dropped; anything with at least one letter or digit is kept.
func isWordlike(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

// stemWord applies Porter2 (English) to alphabetic surfaces. Non-
// alphabetic surfaces (digit runs, mixed alphanumerics like "200lbs")
// are returned unchanged — Porter2 doesn't define behaviour on those.
func stemWord(surface string) string {
	if surface == "" {
		return surface
	}
	if !isAllAlpha(surface) {
		return surface
	}
	stem, err := snowball.Stem(surface, "english", true)
	if err != nil || stem == "" {
		return surface
	}
	return stem
}

// isAllAlpha reports whether every rune in s is a Unicode letter.
func isAllAlpha(s string) bool {
	for _, r := range s {
		if !unicode.IsLetter(r) {
			return false
		}
	}
	return s != ""
}

// looksNumeric reports whether surface is a digit form (e.g. "6",
// "-3.14") or a single-word spelled numeral recognised by
// SpelledNumber (e.g. "six", "nineteen"). Multi-word spelled cardinals
// ("two hundred") are recognised at parse-time by internal/slotparse.
func looksNumeric(surface string) bool {
	if surface == "" {
		return false
	}
	if isDigitForm(surface) {
		return true
	}
	if _, ok := SpelledNumber([]string{surface}); ok {
		return true
	}
	return false
}

// isDigitForm matches ^-?\d+(\.\d+)?$ without pulling in regexp for a
// loop-hot path.
func isDigitForm(s string) bool {
	if s == "" {
		return false
	}
	i := 0
	if s[0] == '-' {
		i = 1
		if len(s) == 1 {
			return false
		}
	}
	sawDigit := false
	for ; i < len(s); i++ {
		c := s[i]
		if c >= '0' && c <= '9' {
			sawDigit = true
			continue
		}
		if c == '.' {
			// require at least one digit before and after
			if !sawDigit {
				return false
			}
			rest := s[i+1:]
			if rest == "" {
				return false
			}
			for j := 0; j < len(rest); j++ {
				d := rest[j]
				if d < '0' || d > '9' {
					return false
				}
			}
			return true
		}
		return false
	}
	return sawDigit
}

// stringsJoinNorms is an internal helper used by Signature; declared
// here to keep signature.go free of trivia.
func stringsJoinNorms(toks []string) string {
	return strings.Join(toks, " ")
}
