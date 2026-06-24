package lex

import "strings"

// numUnits maps single-word English cardinals to their integer value.
// Covers 0–19 plus the tens 20–90.
var numUnits = map[string]int{
	"zero": 0, "one": 1, "two": 2, "three": 3, "four": 4,
	"five": 5, "six": 6, "seven": 7, "eight": 8, "nine": 9,
	"ten": 10, "eleven": 11, "twelve": 12, "thirteen": 13, "fourteen": 14,
	"fifteen": 15, "sixteen": 16, "seventeen": 17, "eighteen": 18, "nineteen": 19,
	"twenty": 20, "thirty": 30, "forty": 40, "fifty": 50,
	"sixty": 60, "seventy": 70, "eighty": 80, "ninety": 90,
}

// numScales maps multiplicative scale words to their multiplier.
var numScales = map[string]int{
	"hundred":  100,
	"thousand": 1000,
	"million":  1000000,
	"billion":  1000000000,
}

// SpelledNumber parses an English spelled-out cardinal made of one or
// more lowercase word tokens into an int. Recognises:
//
//   - units 0–19 ("six", "nineteen")
//   - tens 20–90 ("twenty", "ninety")
//   - tens-plus-unit ("twenty five", "ninety nine") — with or without
//     a hyphen ("twenty-five" should be split into ["twenty", "five"]
//     by the caller; SpelledNumber works on already-split tokens)
//   - scaled forms ("two hundred", "two hundred fifty",
//     "three thousand", "one million two hundred thousand")
//   - the filler word "and" is permitted ("two hundred and fifty")
//
// Returns (0, false) when the input doesn't parse cleanly, and (0, false)
// — not (0, true) — for empty or whitespace-only input; the boolean, not
// the int, is the only valid "did it parse" signal. Safe for concurrent
// use: holds no state and does not mutate words.
//
// The implementation is intentionally small: it walks tokens left to
// right, accumulating a "current" group that is flushed to "total"
// whenever a scale word ("thousand", "million", "billion") is seen.
// "hundred" multiplies the current group in place.
func SpelledNumber(words []string) (int, bool) {
	if len(words) == 0 {
		return 0, false
	}

	total := 0
	current := 0
	matched := false
	expectUnit := false // after a "ten" word, we may take one unit

	for _, raw := range words {
		w := strings.ToLower(strings.TrimSpace(raw))
		if w == "" {
			return 0, false
		}
		// allow "two hundred and fifty"
		if w == "and" {
			if !matched {
				return 0, false
			}
			expectUnit = false
			continue
		}

		if v, ok := numUnits[w]; ok {
			// units 1-19 may follow a tens word; tens words start a new group
			if v < 20 {
				current += v
				expectUnit = false
			} else {
				// tens word — replaces any pending tens; valid at group start
				// or after "hundred"/"thousand" boundary handled by flush.
				if expectUnit {
					// two tens in a row, e.g. "twenty thirty" — invalid
					return 0, false
				}
				current += v
				expectUnit = true
			}
			matched = true
			continue
		}

		if mul, ok := numScales[w]; ok {
			switch mul {
			case 100:
				// "hundred" multiplies whatever's in `current` (or implies 1)
				if current == 0 {
					current = 1
				}
				current *= 100
				expectUnit = false
			default:
				// thousand / million / billion flush the current group
				if current == 0 {
					current = 1
				}
				total += current * mul
				current = 0
				expectUnit = false
			}
			matched = true
			continue
		}

		// unknown word — not a numeral
		return 0, false
	}

	if !matched {
		return 0, false
	}
	return total + current, true
}
