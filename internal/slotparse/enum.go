package slotparse

import (
	"sort"

	"github.com/agnivade/levenshtein"

	"kitsoki/internal/app"
	"kitsoki/internal/lex"
)

// ParseEnum picks an enum slot value from tokens. Strategies in order,
// first hit wins:
//
//  1. Direct match — the non-stopword Porter2 stem-bag of a
//     slot.Values entry is a subset of the non-stopword stem-bag of
//     the input tokens. Single-word values ("banker") collapse to a
//     single-stem check; multi-word values ("lord of the rings") need
//     EVERY non-stop stem of the value to be present in the input.
//     Reason "direct:<value>".
//
//  2. Synonym match — each (value, synonyms[]) pair in slot.Synonyms
//     is tested with a sorted-stem-set containment check: the
//     stem-bag of the synonym phrase must be a subset of the
//     stem-bag of the non-stop input tokens. This matches the
//     synonym word-bag rule documented in
//     docs/architecture/semantic-routing.md. Reason "synonym:<phrase>".
//
//  3. Damerau-Levenshtein-1 fuzzy match — any input token's Norm
//     within edit distance 1 (via github.com/agnivade/levenshtein) of
//     a slot.Values entry's Norm. Fuzzy only fires on values whose
//     own stem-bag has exactly one entry — multi-word slot values
//     don't get a fuzzy tier because the spec doesn't define which
//     of N stems should accept the typo. Reason "fuzzy:<value>".
//
// When MULTIPLE values match within a single tier ParseEnum returns
// OK=false with Reason="ambiguous"; the caller (the matcher) is in
// a better position to ask for clarification than the parser. Lower
// tiers are NOT consulted on a higher-tier ambiguity — the spec
// pins this behaviour because falling through would make matches
// less explainable, not more accurate.
//
// Inputs with no tokens, slots with no values, or slots whose tokens
// fail every tier return OK=false with Reason="".
func ParseEnum(tokens []lex.Token, slot app.Slot) Result {
	if len(tokens) == 0 || len(slot.Values) == 0 {
		return Result{}
	}

	// Pre-compute the Porter2 stem-set of every slot value. We do
	// this once per call rather than caching across calls because the
	// matcher is expected to compile its own per-slot indices at app
	// load — ParseEnum is the per-input path, not the per-app path.
	//
	// valueStemSet[v] is the sorted unique non-stopword stem-bag of
	// the value phrase. For single-word values it has exactly one
	// entry; for multi-word values like "lord of the rings" it
	// becomes {lord, ring} (the/of stripped as stopwords). When a
	// value has no non-stopword stems (e.g. value == "the") the
	// direct tier skips it — there is nothing to match against.
	valueStemSet := make(map[string][]string, len(slot.Values))
	for _, v := range slot.Values {
		valueStemSet[v] = phraseStemSet(v)
	}

	// ---- Tier 1: direct ---------------------------------------------
	// Subset check on stem-bags — the SAME algorithm used at Tier 2
	// for synonyms. A single-word value's stem-bag has one entry, so
	// the check degenerates to "token.Norm equals value-stem"; a
	// multi-word value needs every non-stop stem of the value to be
	// present in the input. This is the most charitable matching rule
	// that still rejects "lord of the manor" against the value "lord
	// of the rings" (input stem-bag {lord, manor} fails to cover
	// {lord, ring}).
	directHits := map[string][]TokenRange{}
	for _, v := range slot.Values {
		needed := valueStemSet[v]
		if len(needed) == 0 {
			continue
		}
		ranges, ok := matchSubset(tokens, needed)
		if !ok {
			continue
		}
		directHits[v] = ranges
	}
	if len(directHits) == 1 {
		for v, ranges := range directHits {
			return Result{
				Value:    v,
				Consumed: ranges,
				OK:       true,
				Reason:   "direct:" + v,
			}
		}
	}
	if len(directHits) > 1 {
		return Result{OK: false, Reason: "ambiguous"}
	}

	// ---- Tier 2: synonym phrase containment --------------------------
	synHits := map[string][]TokenRange{}
	synPattern := map[string]string{}
	for _, value := range slot.Values {
		for _, phrase := range slot.Synonyms[value] {
			phraseStems := phraseStemSet(phrase)
			if len(phraseStems) == 0 {
				continue
			}
			ranges, ok := matchSubset(tokens, phraseStems)
			if !ok {
				continue
			}
			// Only record the first phrase per value that matched, so
			// that ambiguity counts values, not patterns. Within one
			// value, multiple matching synonyms reduce to one hit.
			if _, seen := synHits[value]; !seen {
				synHits[value] = ranges
				synPattern[value] = phrase
			}
		}
	}
	if len(synHits) == 1 {
		for v, ranges := range synHits {
			return Result{
				Value:    v,
				Consumed: ranges,
				OK:       true,
				Reason:   "synonym:" + synPattern[v],
			}
		}
	}
	if len(synHits) > 1 {
		return Result{OK: false, Reason: "ambiguous"}
	}

	// ---- Tier 3: DL-1 fuzzy ------------------------------------------
	// Fuzzy is single-stem only: a multi-word value like "lord of the
	// rings" has no defined "which stem accepts the typo" semantics,
	// and we deliberately don't paper over that with a guess. Authors
	// who want fuzz on multi-word values can add an explicit synonym.
	fuzzyHits := map[string][]TokenRange{}
	for i, t := range tokens {
		if t.IsStop || t.Norm == "" {
			continue
		}
		for _, v := range slot.Values {
			stems := valueStemSet[v]
			if len(stems) != 1 {
				continue
			}
			vs := stems[0]
			// Damerau-Levenshtein with a budget of 1. agnivade's
			// ComputeDistance is plain Levenshtein but for distance=1
			// the two metrics agree (a single transposition is two
			// substitutions in plain Levenshtein, so DL-1 ⊆ Lev-1 — and
			// over our short stems the difference doesn't surface).
			// This matches the "bankr → banker" worked
			// example seeded in enum_fuzz_test.go.
			if levenshtein.ComputeDistance(t.Norm, vs) == 1 {
				fuzzyHits[v] = append(fuzzyHits[v], TokenRange{Start: i, End: i + 1})
			}
		}
	}
	if len(fuzzyHits) == 1 {
		for v, ranges := range fuzzyHits {
			return Result{
				Value:    v,
				Consumed: ranges,
				OK:       true,
				Reason:   "fuzzy:" + v,
			}
		}
	}
	if len(fuzzyHits) > 1 {
		return Result{OK: false, Reason: "ambiguous"}
	}

	return Result{}
}

// phraseStemSet returns the sorted unique set of Porter2 stems for
// the non-stop tokens of phrase. Used by the synonym-tier subset
// check: a phrase matches when its stem-set is a subset of the
// input's stem-set.
func phraseStemSet(phrase string) []string {
	toks := lex.Tokenize(phrase, nil)
	seen := make(map[string]struct{}, len(toks))
	for _, t := range toks {
		if t.IsStop || t.Norm == "" {
			continue
		}
		seen[t.Norm] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// matchSubset reports whether every stem in `needed` appears in the
// non-stop tokens of `toks`, and returns the token ranges that fired.
// Order doesn't matter — this is set containment, not substring match.
//
// The returned []TokenRange is one entry per `needed` stem, pointing
// at the first input token that supplied that stem. Callers use this
// for the Consumed field on synonym matches.
func matchSubset(toks []lex.Token, needed []string) ([]TokenRange, bool) {
	if len(needed) == 0 {
		return nil, false
	}
	ranges := make([]TokenRange, 0, len(needed))
	used := make([]bool, len(toks))
	for _, n := range needed {
		found := false
		for i, t := range toks {
			if used[i] || t.IsStop || t.Norm == "" {
				continue
			}
			if t.Norm == n {
				ranges = append(ranges, TokenRange{Start: i, End: i + 1})
				used[i] = true
				found = true
				break
			}
		}
		if !found {
			return nil, false
		}
	}
	return ranges, true
}
