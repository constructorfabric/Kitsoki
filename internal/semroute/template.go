package semroute

import (
	"fmt"
	"sort"
	"strings"

	"kitsoki/internal/app"
	"kitsoki/internal/lex"
	"kitsoki/internal/slotparse"
)

// Template grammar is deliberately tiny:
//
//   - A template is a sequence of segments. Each segment is either a
//     literal run of stemmed tokens or a `{slot_name}` capture.
//   - Captures must be separated by literal tokens. Adjacent captures
//     ("{a}{b}") are a compile error because the matcher cannot
//     unambiguously split the run between them.
//   - Leading and trailing captures are allowed: "{x}" alone, "{x} more
//     text", and "more text {x}" all compile.
//   - Every slot referenced by `{slot_name}` must be declared on the
//     owning intent (Intent.Slots[slot_name]); otherwise compile fails.

// templateSegment is one piece of a compiled template.
//
// One of (literalStems, captureSlot) is set per segment, mutually
// exclusive. The compiler emits segments in source order.
type templateSegment struct {
	// literalStems is the non-empty sorted stem sequence for a literal
	// run. Order is *positional* (the order the author wrote the tokens
	// after stopword removal and stemming), not sorted — the matcher
	// walks input tokens left-to-right and demands the literal stems
	// appear in this same sequence.
	literalStems []string
	// captureSlot, when non-empty, names the slot whose typed parser
	// receives the captured token run. literalStems is nil/empty on
	// capture segments.
	captureSlot string
}

// isCapture reports whether s is a capture (`{slot_name}`) segment.
func (s templateSegment) isCapture() bool {
	return s.captureSlot != ""
}

// compiledTemplate is one parsed template owned by a single intent.
//
// The compiler emits one of these per author-declared template synonym;
// the matcher walks the slice during [Matcher.Match] and tries each
// template against the input.
type compiledTemplate struct {
	// intent is the owning intent id.
	intent string
	// source is the verbatim template string from YAML (used for the
	// Verdict.MatchReason "template:<source>" tag and for the
	// disambiguation card).
	source string
	// segments is the ordered sequence of literal+capture parts. Order
	// matters: a literal must match in the order the author wrote it.
	segments []templateSegment
	// slotCount is the number of capture segments — used to break
	// most-specific-wins ties (more filled slots wins; see Match).
	slotCount int
	// slotDefs caches the intent's slot definitions keyed by name so
	// Match can hand the right [app.Slot] to slotparse.For without
	// re-resolving on each input. Populated once at compile.
	slotDefs map[string]app.Slot
}

// templateCompileError is the structured CompileError shape for template
// problems. We reuse the existing [CompileError] type so callers don't
// need to learn a new sentinel — only the Reason string changes shape
// across template vs. bare-string compile failures.
//
// Reason patterns the compiler may emit:
//
//   - "unknown slot %q referenced by template"
//   - "adjacent captures {a}{b} — templates require a literal token between captures"
//   - "unmatched brace"
//   - "empty capture {}"
//
// These shapes are pinned by tests (template_compile_test.go); changes
// must update the assertions together.

// compileTemplate parses src for the given intent. Returns nil + error
// on any syntactic issue, or a non-nil *compiledTemplate on success.
//
// The caller is responsible for handing src to compileBareSynonym
// instead when src contains neither `{` nor `}`. compileTemplate
// requires at least one `{slot}` reference; an all-literal "template"
// is treated as a programming error (the bare-string path is more
// efficient and the index byStem path is wired for it).
func compileTemplate(intentID, src string, intentDef app.Intent, stopExtras []string) (*compiledTemplate, error) {
	segs, err := parseTemplate(intentID, src)
	if err != nil {
		return nil, err
	}
	// Validate that every capture references a declared slot.
	for _, seg := range segs {
		if !seg.isCapture() {
			continue
		}
		if _, ok := intentDef.Slots[seg.captureSlot]; !ok {
			return nil, &CompileError{
				Intent:  intentID,
				Synonym: src,
				Reason: fmt.Sprintf("unknown slot %q referenced by template (intent declares slots %v)",
					seg.captureSlot, sortedSlotNames(intentDef.Slots)),
			}
		}
	}
	// Validate no-adjacent-captures: walk the segments and reject any
	// two captures in a row. The "literal must separate captures" rule
	// is the load-bearing constraint that lets the greedy-right
	// matcher work without ambiguity. Authors who want adjacent
	// captures write multiple templates with the separating literal
	// they actually intend ("buy {items} for {total_cost}" not "buy
	// {items}{total_cost}").
	for i := 1; i < len(segs); i++ {
		if segs[i].isCapture() && segs[i-1].isCapture() {
			return nil, &CompileError{
				Intent:  intentID,
				Synonym: src,
				Reason: fmt.Sprintf("adjacent captures {%s}{%s} — templates require a literal token between captures",
					segs[i-1].captureSlot, segs[i].captureSlot),
			}
		}
	}

	// Convert each literal segment to its sequence of stemmed,
	// stopword-filtered tokens. Captures are passed through.
	out := make([]templateSegment, 0, len(segs))
	captureCount := 0
	for _, seg := range segs {
		if seg.isCapture() {
			captureCount++
			out = append(out, seg)
			continue
		}
		stems := compileLiteralStems(seg.literalStems, stopExtras)
		if len(stems) == 0 {
			// Literal run was all whitespace, all stopwords, or all
			// punctuation. Silently drop it: a literal whose stems
			// reduce to nothing carries no positional signal, and
			// dropping it preserves the meaningful capture context.
			//
			// Note: if dropping a literal would create adjacent
			// captures we'd already have errored above (the check ran
			// on the *parsed* segments, including this one). The
			// post-stem drop only happens at the edges (leading/
			// trailing all-stopwords) or between two literals — both
			// safe.
			continue
		}
		out = append(out, templateSegment{literalStems: stems})
	}
	if captureCount == 0 {
		// No captures means the author wrote a template-looking string
		// (it had braces) but every capture was somehow stripped. We
		// treat this as a programming error — compileTemplate's
		// invariant is "at least one capture." The caller's branching
		// (only invoke compileTemplate when braces are present) makes
		// this unreachable in practice, but the guard keeps the
		// invariant honest if a future caller refactors the dispatch.
		return nil, &CompileError{
			Intent:  intentID,
			Synonym: src,
			Reason:  "template contains no {slot_name} captures",
		}
	}
	// Cache the slot defs the matcher will need.
	slotDefs := make(map[string]app.Slot, captureCount)
	for _, seg := range out {
		if seg.isCapture() {
			slotDefs[seg.captureSlot] = intentDef.Slots[seg.captureSlot]
		}
	}
	return &compiledTemplate{
		intent:    intentID,
		source:    src,
		segments:  out,
		slotCount: captureCount,
		slotDefs:  slotDefs,
	}, nil
}

// parseTemplate splits src into raw segments. Literal segments carry
// the original substring (still in literalStems[0] until we lex them);
// capture segments carry the slot name in captureSlot. The two-phase
// design lets the lex/stem pass run on each literal independently and
// re-use the same per-app stopword extras the bare-string compile uses.
//
// Errors: unmatched '{' or '}'; empty `{}` capture; nested braces
// `{a{b}}`.
func parseTemplate(intentID, src string) ([]templateSegment, error) {
	var out []templateSegment
	var lit strings.Builder
	flushLit := func() {
		s := lit.String()
		lit.Reset()
		if s == "" {
			return
		}
		out = append(out, templateSegment{literalStems: []string{s}})
	}

	i := 0
	for i < len(src) {
		c := src[i]
		switch c {
		case '{':
			// Find the matching '}'. Nested braces are not supported.
			end := strings.IndexByte(src[i+1:], '}')
			if end < 0 {
				return nil, &CompileError{
					Intent:  intentID,
					Synonym: src,
					Reason: fmt.Sprintf("unmatched '{' at position %d — every capture must close with '}'",
						i),
				}
			}
			name := src[i+1 : i+1+end]
			// Reject nested or further-open braces inside the capture
			// body. The check is a simple "no '{' inside" since we
			// already advanced past the opener.
			if strings.ContainsAny(name, "{}") {
				return nil, &CompileError{
					Intent:  intentID,
					Synonym: src,
					Reason: fmt.Sprintf("nested or malformed capture starting at position %d: %q",
						i, src[i:i+1+end+1]),
				}
			}
			trimmed := strings.TrimSpace(name)
			if trimmed == "" {
				return nil, &CompileError{
					Intent:  intentID,
					Synonym: src,
					Reason: fmt.Sprintf("empty capture {} at position %d — every capture must name a slot",
						i),
				}
			}
			flushLit()
			out = append(out, templateSegment{captureSlot: trimmed})
			i = i + 1 + end + 1
		case '}':
			return nil, &CompileError{
				Intent:  intentID,
				Synonym: src,
				Reason: fmt.Sprintf("unmatched '}' at position %d — every '}' must close a '{slot_name}' capture",
					i),
			}
		default:
			lit.WriteByte(c)
			i++
		}
	}
	flushLit()
	return out, nil
}

// compileLiteralStems takes the verbatim literal substring (the author's
// text between captures), tokenises it via [lex.Tokenize], and returns
// the *positional* (declaration-order) sequence of stemmed tokens.
// Unlike the bare-string compile helper, the result is NOT sorted —
// templates rely on input-order matching to decide where each capture's
// run ends.
//
// Stopwords are PRESERVED in template literals because they often
// carry the positional anchor the author wrote: "buy {items} for
// {total_cost}" relies on the literal "for" — itself a stopword —
// to know where the items capture ends. The matcher's literal-
// anchor finder accepts a stopword in the input slot when the
// template asked for it; non-template (bag-style) matching keeps
// the original drop-stopwords behaviour.
//
// Returns nil for an empty/all-whitespace run; the caller silently
// drops such segments (see compileTemplate).
func compileLiteralStems(raws []string, stopExtras []string) []string {
	// parseTemplate stores the verbatim literal in raws[0]; we accept a
	// slice in the signature so future refactors can split a literal
	// across passes without changing the API.
	var s string
	if len(raws) == 1 {
		s = raws[0]
	} else {
		s = strings.Join(raws, " ")
	}
	toks := lex.Tokenize(s, stopExtras)
	if len(toks) == 0 {
		return nil
	}
	out := make([]string, 0, len(toks))
	for _, t := range toks {
		if t.Norm == "" {
			continue
		}
		out = append(out, t.Norm)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// sortedSlotNames returns slots' keys in lexicographic order. Used to
// produce a deterministic "intent declares slots %v" message in
// CompileError.
func sortedSlotNames(slots map[string]app.Slot) []string {
	if len(slots) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(slots))
	for k := range slots {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// templateMatch is the per-template attempt result returned by
// matchTemplate. ok=false means the template's literal anchors did not
// align with the input at all (so the matcher should try the next
// template). When ok=true the caller decides between the 0.80 and 0.65
// bands by inspecting missingSlots.
type templateMatch struct {
	// ok is true when every literal segment was found in the input in
	// source order. A failed literal match short-circuits the attempt.
	ok bool
	// slots holds the parsed slot values, keyed by slot name. Only
	// populated when the slot's typed parser returned OK=true (or, for
	// string slots without a parser, when the capture was non-empty).
	slots map[string]any
	// missingSlots holds the names of slots whose captures could not be
	// parsed by the typed parser. An entry here triggers the 0.65 band.
	missingSlots []string
	// filledCount records len(slots) — exposed so the most-specific-
	// wins tie-break can pick a template without re-counting.
	filledCount int
	// totalSlots records len(slots) + len(missingSlots) — the number
	// of {slot} captures the template declared. Used together with
	// filledCount to drive the confidence-band selection.
	totalSlots int
}

// matchTemplate tries one compiled template against the input tokens.
// Returns a templateMatch with ok=true iff every literal segment was
// found in input order; the caller decides band based on missingSlots.
//
// Algorithm:
//
//  1. Walk the template's segments from left to right.
//  2. Each literal segment seeks forward in the input for a matching
//     stem run, starting from the current input cursor. The first
//     occurrence wins (greedy to the *right* for the preceding
//     capture, equivalently: each literal anchors at the earliest
//     position consistent with the prior anchor).
//  3. The input tokens BETWEEN the prior cursor and the literal
//     anchor's start are the captured range for the preceding
//     capture segment (if there was one).
//  4. After the last literal, any remaining input tokens form the
//     trailing capture range (if the last segment is a capture).
//  5. A leading capture absorbs every token before the first literal.
//  6. A single-capture template `{x}` absorbs every input token.
//
// Each capture range is then handed to slotparse.For(slotDef). String
// slots without a parser fall back to the joined Surface text. A capture
// whose typed parser returns OK=false goes into missingSlots; the
// verdict's band drops from 0.80 to 0.65.
func matchTemplate(t *compiledTemplate, tokens []lex.Token) templateMatch {
	var (
		// cursor is the index in tokens up to which we've already
		// consumed.
		cursor int
		// pending holds the slot name when the most recent segment
		// was a capture; the next literal anchor (or end-of-input)
		// closes it.
		pending    string
		hasPending bool

		slots        = make(map[string]any, t.slotCount)
		missingSlots []string
	)

	parseCapture := func(slotName string, captured []lex.Token) {
		slotDef := t.slotDefs[slotName]
		parser := slotparse.For(slotDef)
		if parser == nil {
			// A string slot without a typed parser returns the
			// captured raw text verbatim (joined surfaces).
			// Any other type with no parser (only "list"/"date" today)
			// is unparseable and becomes a missingSlot.
			if slotDef.Type == "string" || slotDef.Type == "" {
				val := joinSurfaces(captured)
				if val == "" {
					missingSlots = append(missingSlots, slotName)
					return
				}
				slots[slotName] = val
				return
			}
			missingSlots = append(missingSlots, slotName)
			return
		}
		// Even with a parser, an empty capture cannot succeed.
		if len(captured) == 0 {
			missingSlots = append(missingSlots, slotName)
			return
		}
		r := parser.Parse(captured, slotDef)
		if !r.OK {
			missingSlots = append(missingSlots, slotName)
			return
		}
		slots[slotName] = r.Value
	}

	for _, seg := range t.segments {
		if seg.isCapture() {
			if hasPending {
				// Two captures in a row should have been caught at
				// compile. The runtime guard returns a miss; failing
				// loud would crash on a malformed compiled template
				// the loader somehow let through.
				return templateMatch{}
			}
			pending = seg.captureSlot
			hasPending = true
			continue
		}
		// Literal segment — seek its first occurrence at or after cursor.
		// findLiteralAnchor returns the START index of the matched
		// literal run AND the END index (one past the last input
		// token the matcher consumed). The end may exceed
		// anchor+len(literalStems) when the matcher absorbed
		// stopwords between literal stems via rule (2); using END
		// as the new cursor keeps those absorbed tokens from leaking
		// into the next capture (C1 fix — see findLiteralAnchor).
		anchor, end, found := findLiteralAnchor(tokens, cursor, seg.literalStems)
		if !found {
			return templateMatch{}
		}
		if hasPending {
			// The captured tokens are the [cursor, anchor) slice.
			parseCapture(pending, tokens[cursor:anchor])
			hasPending = false
			pending = ""
		} else if anchor != cursor {
			// A literal that doesn't anchor at the cursor implies
			// there's input we didn't account for between the prior
			// literal and this one. That breaks the positional
			// contract — the template doesn't match.
			return templateMatch{}
		}
		// Consume the literal run, including any stopwords the
		// matcher skipped between its stems.
		cursor = end
	}

	// Resolve any trailing capture.
	if hasPending {
		parseCapture(pending, tokens[cursor:])
	} else if cursor != len(tokens) {
		// The template ended on a literal but the input has trailing
		// tokens after it. Two reasonable readings exist (drop the
		// tail, or fail the match); we fail. A literal "buy items" at
		// the end of a template should match input "buy items" but
		// not "buy items now" — otherwise templates degrade to bare-
		// string semantics and the explicit-positional contract is
		// lost. Authors who want trailing slack write "{leading} buy
		// items" or similar.
		//
		// EXCEPTION: a trailing run of pure stopwords. Stopwords are
		// already stripped from the input bag at compile time but
		// they survive in the [lex.Tokenize] slice we walk here.
		// Allow them so "press on please" still matches a template
		// ending in "press on".
		for j := cursor; j < len(tokens); j++ {
			if !tokens[j].IsStop {
				return templateMatch{}
			}
		}
	}

	return templateMatch{
		ok:           true,
		slots:        slots,
		missingSlots: missingSlots,
		filledCount:  len(slots),
		totalSlots:   t.slotCount,
	}
}

// findLiteralAnchor scans tokens[start:] for the first index where the
// input stems align with want in order. Returns (anchorIdx, endIdx,
// true) on match, where:
//
//   - anchorIdx is the index in tokens of the first matched token
//     (used by the caller to bound the preceding capture range).
//   - endIdx is one past the index of the last input token the matcher
//     consumed — including any stopwords skipped between literal stems
//     via rule (2). The caller advances its cursor to endIdx so those
//     absorbed stopwords don't leak into the next capture.
//
// Returns (-1, -1, false) on miss.
//
// Matching rules, designed to keep positional templates robust under
// natural typing:
//
//  1. A token whose Norm equals the current want stem is consumed,
//     regardless of whether it is a stopword. Templates that anchor
//     on a stopword (e.g. "buy {items} for {total_cost}" — "for" is
//     a stopword) only work because of this.
//  2. A non-matching stopword in the input is silently skipped — the
//     filler-tolerance rule. "wade across THE river" still matches
//     a template literal "across river". The skipped tokens are
//     absorbed by the literal run (counted in endIdx) and do NOT
//     leak into surrounding captures.
//  3. A non-matching non-stopword is a hard mismatch — the matcher
//     tries the next starting position.
//
// The asymmetry between (1) and (2) is the load-bearing design call:
// stopwords are only ignored when they don't carry the literal's
// positional signal.
//
// Returning endIdx (not just len(want)) is required: when rule (2)
// fires, the match consumes MORE input tokens than there are
// `want` stems, and the caller needs to know about every consumed
// position. See C1 in the semantic-routing test suite for the
// regression case.
func findLiteralAnchor(tokens []lex.Token, start int, want []string) (int, int, bool) {
	if len(want) == 0 {
		return start, start, true
	}
	for i := start; i <= len(tokens)-1; i++ {
		j := i
		k := 0
		anchor := -1
		for j < len(tokens) && k < len(want) {
			tok := tokens[j]
			if tok.Norm == want[k] {
				if anchor < 0 {
					anchor = j
				}
				j++
				k++
				continue
			}
			if tok.IsStop {
				j++
				continue
			}
			break
		}
		if k == len(want) {
			return anchor, j, true
		}
	}
	return -1, -1, false
}

// joinSurfaces concatenates the Surface fields of a token run,
// preserving the original case and inserting a single space between
// tokens. Stopwords ARE preserved here — the string-slot path quotes
// the user's exact phrasing, including filler
// (otherwise "name the wagon the rolling thunder" → "rolling thunder"
// drops the article the author may want to keep).
func joinSurfaces(toks []lex.Token) string {
	if len(toks) == 0 {
		return ""
	}
	var b strings.Builder
	for i, t := range toks {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(t.Surface)
	}
	return strings.TrimSpace(b.String())
}
