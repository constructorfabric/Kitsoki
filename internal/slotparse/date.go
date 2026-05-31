package slotparse

import (
	"strings"
	"time"

	"github.com/araddon/dateparse"

	"kitsoki/internal/lex"
)

// Date-parsing bounds. These name the magic numbers the hand-coded
// date patterns lean on so they are discoverable in one place; none
// is a tunable knob, they are the semantic limits of the dialect.
const (
	// maxDateNumberWindow bounds the spelled-cardinal scan inside a
	// date context ("in three days"). It is smaller than
	// [maxSpelledWindow] because a date's number is a count of days or
	// weeks — short by construction — so a wider window would only
	// invite false positives from surrounding prose.
	maxDateNumberWindow = 8

	// minYear4Digit is the smallest value a 4-digit numeric year is
	// allowed to take. It exists to reject 3-digit-and-shorter runs
	// that happen to fall in the 1..999 range from being read as years
	// in the numeric "year month day" / "month day year" forms.
	minYear4Digit = 1000

	// minMonthValue / maxMonthValue bound a numeric month component.
	minMonthValue = 1
	maxMonthValue = 12

	// minDayValue / maxDayValue bound a numeric day-of-month component.
	// The upper bound is a coarse sanity gate (not per-month) — an
	// invalid day like "feb 31" still normalises through time.Date.
	minDayValue = 1
	maxDayValue = 31

	// minYearTwoFourDigit is the floor for the optional year in the
	// "month day [year]" pattern; it accepts both 3- and 4-digit years
	// declared explicitly, so the gate is lower than minYear4Digit.
	minYearTwoFourDigit = 100
)

// ParseDate accepts a small whitelist of date phrasings and returns
// a [time.Time] (UTC, midnight) as the Value. Strategies in order, first
// hit wins:
//
//  1. Bare relative — "today", "tomorrow", "yesterday".
//  2. "in N days" / "in N weeks" — N is parsed from a digit-form or
//     spelled cardinal token, * matches the singular/plural unit.
//  3. "next <weekday>" — case-folded to today's-weekday relative offset.
//  4. "next week" / "last week" — ±7d from today.
//  5. Month-name patterns: "march 3", "march 15, 2026", "march 15
//     2026". A bare "march 3" without a year defaults to the current
//     calendar year UNLESS the resulting date is strictly in the past,
//     in which case the parser rolls forward to next year. This makes
//     "schedule a meeting for march 3" mean the *next* march 3 rather
//     than a date in the past.
//  6. ISO and slash forms — anything [github.com/araddon/dateparse]
//     parses cleanly. Strict mode is used so ambiguous junk doesn't
//     spuriously match.
//
// Reason carries a stable diagnostic prefix:
//
//   - "date:today" / "date:tomorrow" / "date:yesterday"
//   - "date:in_days"
//   - "date:next_weekday"
//   - "date:next_week" / "date:last_week"
//   - "date:month_day" (bare "march 3", year inferred)
//   - "date:month_day_year"
//   - "date:iso" (anything dateparse handled)
//
// On miss, Result.OK is false and Reason is "".
//
// Determinism note. ParseDate calls [time.Now] internally. For tests
// and replay tools that need a fixed reference point, call
// [ParseDateAt] with an injectable now.
func ParseDate(tokens []lex.Token) Result {
	return ParseDateAt(tokens, time.Now())
}

// ParseDateAt is the testable variant of [ParseDate]: now is the
// monday") and the month-day year-rollover heuristic (strategy 5
// above). Tests pin now; production callers go through ParseDate.
//
// now is normalised to its date (year/month/day) before relative
// arithmetic — sub-day components are ignored. The returned time is
// always at midnight UTC.
func ParseDateAt(tokens []lex.Token, now time.Time) Result {
	if len(tokens) == 0 {
		return Result{}
	}
	today := dateOnly(now)

	// Build a non-stop surface slice for the hand-coded patterns.
	// We KEEP "in" and "next" — both have semantic load — by checking
	// surfaces directly rather than relying on IsStop. Index back to
	// the original token positions so Consumed reports accurate ranges.
	type word struct {
		idx     int
		surface string
		norm    string
		isNum   bool
	}
	words := make([]word, 0, len(tokens))
	for i, t := range tokens {
		words = append(words, word{idx: i, surface: t.Surface, norm: t.Norm, isNum: t.IsNum})
	}

	// ---- Strategy 1: bare relative ---------------------------------
	for _, w := range words {
		switch w.surface {
		case "today":
			return Result{Value: today, OK: true, Reason: "date:today", Consumed: []TokenRange{{Start: w.idx, End: w.idx + 1}}}
		case "tomorrow":
			return Result{Value: today.AddDate(0, 0, 1), OK: true, Reason: "date:tomorrow", Consumed: []TokenRange{{Start: w.idx, End: w.idx + 1}}}
		case "yesterday":
			return Result{Value: today.AddDate(0, 0, -1), OK: true, Reason: "date:yesterday", Consumed: []TokenRange{{Start: w.idx, End: w.idx + 1}}}
		}
	}

	// ---- Strategy 2: "in N days|weeks" ----------------------------
	// Look for "in" followed (after optional stopwords) by a numeric
	// token and a unit. The numeric may be digit-form ("3") or a
	// spelled cardinal ("three"); we re-use [lex.SpelledNumber] for
	// the latter.
	for i := 0; i < len(tokens); i++ {
		if tokens[i].Surface != "in" {
			continue
		}
		j := i + 1
		// Skip any subsequent stopwords (defensive — none expected).
		for j < len(tokens) && tokens[j].IsStop && tokens[j].Surface != "and" {
			j++
		}
		if j >= len(tokens) {
			continue
		}
		// Parse a number off tokens[j:].
		n, nEnd, ok := readNumberAt(tokens, j)
		if !ok {
			continue
		}
		if nEnd >= len(tokens) {
			continue
		}
		unit := tokens[nEnd].Norm // Porter2 stem: "days"→"day", "weeks"→"week"
		var delta int
		switch unit {
		case "day":
			delta = n
		case "week":
			delta = n * 7
		default:
			continue
		}
		consumed := []TokenRange{
			{Start: i, End: i + 1},       // "in"
			{Start: j, End: nEnd},        // number
			{Start: nEnd, End: nEnd + 1}, // unit
		}
		return Result{
			Value:    today.AddDate(0, 0, delta),
			OK:       true,
			Reason:   "date:in_days",
			Consumed: consumed,
		}
	}

	// ---- Strategy 3 + 4: "next <weekday>" / "next week" / "last week" ----
	for i := 0; i+1 < len(tokens); i++ {
		s := tokens[i].Surface
		if s != "next" && s != "last" {
			continue
		}
		next := tokens[i+1].Surface
		if next == "week" {
			delta := 7
			reason := "date:next_week"
			if s == "last" {
				delta = -7
				reason = "date:last_week"
			}
			return Result{
				Value:    today.AddDate(0, 0, delta),
				OK:       true,
				Reason:   reason,
				Consumed: []TokenRange{{Start: i, End: i + 2}},
			}
		}
		if wd, ok := weekdayName(next); ok && s == "next" {
			// Days from today's weekday to wd, in [1..7] so "next
			// monday" from a Monday is 7 days out, not 0.
			delta := int(wd - today.Weekday())
			if delta <= 0 {
				delta += 7
			}
			return Result{
				Value:    today.AddDate(0, 0, delta),
				OK:       true,
				Reason:   "date:next_weekday",
				Consumed: []TokenRange{{Start: i, End: i + 2}},
			}
		}
	}

	// ---- Strategy 5: "month day [year]" ---------------------------
	for i, t := range tokens {
		mon, ok := monthName(t.Surface)
		if !ok {
			continue
		}
		// day is the next non-stop digit-form token.
		j := i + 1
		for j < len(tokens) && tokens[j].IsStop {
			j++
		}
		if j >= len(tokens) || !tokens[j].IsNum {
			continue
		}
		day, dayOK := atoiSurface(tokens[j].Surface)
		if !dayOK || day < minDayValue || day > maxDayValue {
			continue
		}
		// Optional year — same path.
		yearVal := 0
		yearEnd := j + 1
		k := j + 1
		for k < len(tokens) && tokens[k].IsStop {
			k++
		}
		if k < len(tokens) && tokens[k].IsNum {
			if y, ok2 := atoiSurface(tokens[k].Surface); ok2 && y >= minYearTwoFourDigit {
				yearVal = y
				yearEnd = k + 1
			}
		}
		if yearVal > 0 {
			val := time.Date(yearVal, mon, day, 0, 0, 0, 0, time.UTC)
			return Result{
				Value:    val,
				OK:       true,
				Reason:   "date:month_day_year",
				Consumed: []TokenRange{{Start: i, End: yearEnd}},
			}
		}
		// Bare month+day — apply year-rollover heuristic.
		val := time.Date(today.Year(), mon, day, 0, 0, 0, 0, time.UTC)
		if val.Before(today) {
			val = val.AddDate(1, 0, 0)
		}
		return Result{
			Value:    val,
			OK:       true,
			Reason:   "date:month_day",
			Consumed: []TokenRange{{Start: i, End: j + 1}},
		}
	}

	// ---- Strategy 6: numeric date forms --------------------------
	// Lex strips '-' / '/' separators on tokenisation, so a date
	// like "2026-03-15" arrives as three back-to-back digit tokens
	// ["2026"]["03"]["15"]. Detect the two canonical orderings:
	//
	//   year-month-day (Y is 4 digits)  — "2026 03 15"
	//   month-day-year (Y is 4 digits)  — "3 15 2026"
	//
	// 2-digit years are deliberately NOT accepted: the date
	// whitelist is small and "yy" forms are an ambiguity magnet
	// ("03 15 26" — what year?). Stick to 4-digit years so the
	// matcher's surface stays predictable.
	for i := 0; i+2 < len(tokens); i++ {
		if tokens[i].IsStop {
			continue
		}
		a, aOK := atoiSurface(tokens[i].Surface)
		b, bOK := atoiSurface(tokens[i+1].Surface)
		c, cOK := atoiSurface(tokens[i+2].Surface)
		if !aOK || !bOK || !cOK {
			continue
		}
		// year-month-day (ISO ordering): a is 4-digit year, b in
		// [1..12], c in [1..31].
		if len(tokens[i].Surface) == 4 && a >= minYear4Digit && b >= minMonthValue && b <= maxMonthValue && c >= minDayValue && c <= maxDayValue {
			val := time.Date(a, time.Month(b), c, 0, 0, 0, 0, time.UTC)
			return Result{
				Value:    val,
				OK:       true,
				Reason:   "date:iso",
				Consumed: []TokenRange{{Start: i, End: i + 3}},
			}
		}
		// month-day-year (US slash ordering): a in [1..12], b in
		// [1..31], c is 4-digit year.
		if a >= minMonthValue && a <= maxMonthValue && b >= minDayValue && b <= maxDayValue && len(tokens[i+2].Surface) == 4 && c >= minYear4Digit {
			val := time.Date(c, time.Month(a), b, 0, 0, 0, 0, time.UTC)
			return Result{
				Value:    val,
				OK:       true,
				Reason:   "date:iso",
				Consumed: []TokenRange{{Start: i, End: i + 3}},
			}
		}
	}

	// ---- Strategy 7: anything else dateparse handles cleanly -----
	// Reconstruct the input substring from the first non-stop token
	// onward and let github.com/araddon/dateparse handle it (its
	// surface area covers RFC 3339, "Jan 2, 2006 3:04 PM", and a
	// few other forms we haven't pinned above). Strict mode is on
	// so ambiguous junk doesn't slip through.
	start := -1
	for i, t := range tokens {
		if !t.IsStop {
			start = i
			break
		}
	}
	if start >= 0 {
		var b strings.Builder
		for i := start; i < len(tokens); i++ {
			if i > start {
				b.WriteByte(' ')
			}
			b.WriteString(tokens[i].Surface)
		}
		raw := strings.TrimSpace(b.String())
		if raw != "" {
			if t, err := dateparse.ParseStrict(raw); err == nil {
				return Result{
					Value:    dateOnly(t),
					OK:       true,
					Reason:   "date:iso",
					Consumed: []TokenRange{{Start: start, End: len(tokens)}},
				}
			}
		}
	}

	return Result{}
}

// dateOnly truncates a time.Time to its UTC year/month/day, with zero
// time-of-day. Used so equal calendar dates compare equal regardless
// of clock skew or the caller's timezone.
func dateOnly(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// readNumberAt parses a single number starting at tokens[start]. It
// tries digit form first (single token), then a spelled cardinal run
// (up to maxDateNumberWindow tokens forward; the same window
// [ParseInt] uses, scaled down for date contexts). Returns (value, endIdx, ok) where tokens[end] is
// the first NON-consumed token. Stopwords are NOT skipped — the
// caller has already stepped past them.
func readNumberAt(tokens []lex.Token, start int) (int, int, bool) {
	if start >= len(tokens) {
		return 0, 0, false
	}
	// Digit form.
	if tokens[start].IsNum && isDigitFormSurface(tokens[start].Surface) {
		if n, ok := atoiSurface(tokens[start].Surface); ok {
			return n, start + 1, true
		}
	}
	// Spelled cardinal — greedy from start, up to 8 alpha words.
	if !isAlphaSurface(tokens[start].Surface) {
		return 0, 0, false
	}
	end := start
	for end < len(tokens) && end-start < maxDateNumberWindow && isAlphaSurface(tokens[end].Surface) {
		end++
	}
	for hi := end; hi > start; hi-- {
		words := make([]string, 0, hi-start)
		for i := start; i < hi; i++ {
			words = append(words, tokens[i].Surface)
		}
		if n, ok := lex.SpelledNumber(words); ok {
			return n, hi, true
		}
	}
	return 0, 0, false
}

// atoiSurface returns the int-parse of a digit-form surface. Wraps the
// strconv call so callers can shadow on the (int, bool) shape used by
// the other helpers in this file.
func atoiSurface(s string) (int, bool) {
	n := 0
	if s == "" {
		return 0, false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}

// monthName returns the [time.Month] for a full or three-letter month
// name. Matches the case-folded surface (lex lowercases input).
func monthName(s string) (time.Month, bool) {
	switch s {
	case "january", "jan":
		return time.January, true
	case "february", "feb":
		return time.February, true
	case "march", "mar":
		return time.March, true
	case "april", "apr":
		return time.April, true
	case "may":
		return time.May, true
	case "june", "jun":
		return time.June, true
	case "july", "jul":
		return time.July, true
	case "august", "aug":
		return time.August, true
	case "september", "sep", "sept":
		return time.September, true
	case "october", "oct":
		return time.October, true
	case "november", "nov":
		return time.November, true
	case "december", "dec":
		return time.December, true
	}
	return 0, false
}

// weekdayName returns the [time.Weekday] for a full weekday surface.
func weekdayName(s string) (time.Weekday, bool) {
	switch s {
	case "sunday":
		return time.Sunday, true
	case "monday":
		return time.Monday, true
	case "tuesday":
		return time.Tuesday, true
	case "wednesday":
		return time.Wednesday, true
	case "thursday":
		return time.Thursday, true
	case "friday":
		return time.Friday, true
	case "saturday":
		return time.Saturday, true
	}
	return 0, false
}
