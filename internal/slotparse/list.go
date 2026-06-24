package slotparse

import (
	"kitsoki/internal/app"
	"kitsoki/internal/lex"
)

// ParseList consumes a comma- or "and"-separated run of items, each
// parsed by inner. It returns Value as a []any of the inner parsers'
// Values, in declaration order.
//
// Separator handling. Commas vanish during tokenisation (lex only
// keeps wordlike segments), so a comma-separated input such as
// "6, 12, 3" arrives at this parser as three back-to-back numeric
// tokens with no separator surface at all. ParseList simply re-runs
// inner on the tail after each successful item, so the comma case
// is implicit.
//
// The word "and" survives tokenisation as a stopword (see
// internal/lex/stopwords.go). ParseList treats a token whose Norm
// equals "and" as an explicit separator: it's consumed (so it
// extends the reported [TokenRange] range) but does not contribute
// to the value list. Any other stopword between items is silently
// stepped over but is NOT consumed — the caller's wider matcher
// owns leading/trailing filler.
//
// Trailing junk. As soon as inner returns OK=false on the
// remaining suffix, parsing stops. The returned Consumed range
// covers tokens[firstItemStart:lastItemEnd) (extended through any
// already-eaten "and" tokens between items). Everything past
// lastItemEnd is left for the caller — matching the
// "list of [6, 12] · Consumed stops before 'then'" contract for
// list slots in docs/architecture/semantic-routing.md.
//
// All elements fail. When inner cannot parse a single item starting
// at any non-stop position, ParseList returns OK=false with the
// zero [Result]. Reason is "list:miss" so traces and tests can
// distinguish a structural miss from a partial parse.
//
// One element fails mid-stream. The clean choice this implementation
// pins (matching the "list of [6, 12]" example) is
// PREFIX wins: every item that parsed before the first failure is
// kept; the failure ends the list. This is friendlier to authors
// than "all-or-nothing" — "6, 12, blue, 3" gives [6, 12] rather than
// dropping the whole utterance.
//
// Reason is "list:<inner.Reason-of-first-item>" so callers can
// trace which inner-parser strategy fired first ("list:digit" for
// pure digit runs, "list:spelled" when the first item is spelled).
// Subsequent items may have different reasons but only the leader's
// is surfaced — by spec the matcher reports a single reason per
// slot.
func ParseList(tokens []lex.Token, inner Parser) Result {
	return ParseListWithSlot(tokens, inner, app.Slot{})
}

// ParseListWithSlot is the slot-aware variant: it threads the slot
// metadata through to inner.Parse so enum-typed inner parsers can
// see the value/synonym tables for their per-item dispatch. When
// the inner parser is type-free (int, money, bool) the slot is
// ignored.
//
// Callers that already know the slot — namely [For] — pass it
// explicitly. The bare [ParseList] entry point is the convenience
// for callers that only have an int/bool/money inner parser and
// no per-item metadata to supply (typical for the worked example
// "buy 6, 12, and 3").
func ParseListWithSlot(tokens []lex.Token, inner Parser, slot app.Slot) Result {
	if inner == nil || len(tokens) == 0 {
		return Result{}
	}

	values := make([]any, 0, 4)
	consumed := make([]TokenRange, 0, 4)
	var firstReason string

	// segmentEnd returns the half-open end of the next "segment" —
	// tokens[start:segmentEnd(start)] excludes the next "and"
	// separator (and everything after it). We feed each segment to
	// inner.Parse so the inner parser sees ONE bounded window at a
	// time. This is critical for ParseEnum, which reports ambiguity
	// when it sees multiple direct hits across an "and" boundary.
	segmentEnd := func(start int) int {
		for i := start; i < len(tokens); i++ {
			if tokens[i].Norm == "and" {
				return i
			}
		}
		return len(tokens)
	}

	// parseHead runs inner on tokens[start:end]. The strict flag
	// toggles the "prefix-wins" rule: when strict=true, the consumed
	// range must start at or before the first non-stop token (no
	// jumping past non-stop junk). When strict=false (used only for
	// the FIRST item in a list), the inner parser's normal
	// leading-filler tolerance applies — so "please buy six" still
	// parses 6 as the first item.
	parseHead := func(start, end int, strict bool) (Result, int, bool) {
		if start >= end {
			return Result{}, start, false
		}
		segment := tokens[start:end]
		r := inner.Parse(segment, slot)
		if !r.OK || len(r.Consumed) == 0 {
			return r, start, false
		}
		minStart := len(segment)
		var maxEnd int
		for _, cr := range r.Consumed {
			if cr.Start < minStart {
				minStart = cr.Start
			}
			if cr.End > maxEnd {
				maxEnd = cr.End
			}
		}
		if strict {
			firstNS := 0
			for firstNS < len(segment) && segment[firstNS].IsStop {
				firstNS++
			}
			if minStart > firstNS {
				return Result{OK: false}, start, false
			}
		}
		out := make([]TokenRange, 0, len(r.Consumed))
		for _, cr := range r.Consumed {
			rebased := TokenRange{Start: cr.Start + start, End: cr.End + start}
			if rebased.End > end {
				rebased.End = end
			}
			out = append(out, rebased)
		}
		r.Consumed = out
		return r, start + maxEnd, true
	}

	cursor := 0
	for cursor < len(tokens) {
		// Skip stopwords (other than "and") at the cursor — leading
		// filler within a list is invisible.
		if tokens[cursor].IsStop && tokens[cursor].Norm != "and" {
			cursor++
			continue
		}
		// A leading "and" with no items yet is a structural miss;
		// we just step past it and let the loop terminate cleanly.
		if tokens[cursor].Norm == "and" && len(values) == 0 {
			cursor++
			continue
		}

		end := segmentEnd(cursor)
		// The FIRST item in the list inherits the inner parser's
		// natural leading-filler tolerance ("please buy six" → 6).
		// Subsequent items are strict — no jumping past non-stop
		// junk — so the prefix-wins rule fires on mid-list garbage.
		strict := len(values) > 0
		r, headEnd, ok := parseHead(cursor, end, strict)
		if !ok {
			break
		}
		values = append(values, r.Value)
		consumed = append(consumed, r.Consumed...)
		if firstReason == "" {
			firstReason = r.Reason
		}
		cursor = headEnd

		// After the item, we may see:
		//   (a) An "and" separator (cursor < end == "and" index).
		//   (b) Adjacent next item (comma-separated case — commas
		//       vanish during tokenisation, so the next non-stop
		//       token is just the next item).
		//   (c) End of input or trailing non-stop junk inside the
		//       segment.
		//
		// Case (a): fold the "and" into Consumed if a parseable
		// item follows; otherwise break (trailing "and" goes nowhere).
		if cursor < len(tokens) && tokens[cursor].Norm == "and" {
			peekEnd := segmentEnd(cursor + 1)
			if _, _, peekOK := parseHead(cursor+1, peekEnd, true); peekOK {
				consumed = append(consumed, TokenRange{Start: cursor, End: cursor + 1})
				cursor++
				continue
			}
			break
		}
		// Case (b)+(c): walk forward from cursor up to the segment
		// end, skipping ONLY stopwords. If we land on a non-stop
		// token, try to parse the next item there (comma case).
		// If we run into non-stop non-numeric junk before finding a
		// parseable head, break — prefix-wins.
		for cursor < end && tokens[cursor].IsStop {
			cursor++
		}
		if cursor >= end {
			// End-of-segment with no junk: continue to the outer
			// loop, which will absorb any "and" separator and try
			// the next segment.
			continue
		}
		// We're at a non-stop token before the segment end.
		// parseHead will accept iff this token kicks off a clean
		// inner parse — if not, the list ends here.
		if _, _, peekOK := parseHead(cursor, end, true); !peekOK {
			break
		}
		// Loop back; the next iteration will parse and absorb.
	}

	if len(values) == 0 {
		return Result{OK: false, Reason: "list:miss"}
	}

	return Result{
		Value:    values,
		Consumed: consumed,
		OK:       true,
		Reason:   "list:" + firstReason,
	}
}

// listParser is the adapter [For] hands back for "list[<inner>]"
// slot types. The inner type is parsed once at construction so
// dispatch is one allocation per slot definition.
type listParser struct {
	inner Parser
	// innerSlot carries the metadata the inner parser needs (enum
	// values/synonyms in particular). For type-free inner parsers
	// the zero Slot is fine.
	innerSlot app.Slot
}

// Parse satisfies [Parser]. The slot argument is ignored for the
// outer list — its metadata travels through the captured innerSlot
// instead.
func (p listParser) Parse(tokens []lex.Token, _ app.Slot) Result {
	return ParseListWithSlot(tokens, p.inner, p.innerSlot)
}
