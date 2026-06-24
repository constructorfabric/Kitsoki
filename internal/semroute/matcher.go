package semroute

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"kitsoki/internal/lex"
)

// Matcher is the per-app compiled synonym index. Safe for concurrent
// use; all state lives in the immutable [compiledIndex] returned by
// [Compile].
//
// The zero value is NOT useful — always go through Compile. A nil
// Matcher does, however, behave like an empty matcher (every Match
// returns a zero Verdict). That keeps orchestrator wiring tidy when
// routing is disabled on the app.
type Matcher struct {
	idx *compiledIndex
}

// IsEmpty reports whether the matcher has no compiled entries.
// Callers can short-circuit ahead of [Matcher.Match] when they want
// to skip the (cheap) lex pipeline on an app that declares no
// synonyms and no examples.
//
// An app that declares ONLY template synonyms is NOT empty: the
// template path is a peer of the bare-string entries path, so a
// matcher with zero bare entries but ≥1 template will still produce
// a Verdict on a matching input.
func (m *Matcher) IsEmpty() bool {
	if m == nil || m.idx == nil {
		return true
	}
	if len(m.idx.entries) > 0 {
		return false
	}
	for _, ts := range m.idx.templates {
		if len(ts) > 0 {
			return false
		}
	}
	return true
}

// Match attempts to resolve input under the given (state, allowed-
// intents). Returns a zero-Confidence Verdict when nothing matches
// (caller falls through to the next tier).
//
// Match never errors on "no match" — the zero Verdict is the signal.
// An error is returned only for context cancellation; the function
// performs only one ctx.Err() check at entry to avoid scattering
// cancellation checks through what is otherwise a hot path.
//
// statePath is currently unused at the matching level: Phase 2 has
// no state-scoped synonyms (an open design question). It's accepted on
// the signature because callers already have it, and reserving the
// slot avoids a Phase-4 API change.
func (m *Matcher) Match(ctx context.Context, statePath string, allowed []string, input string) (Verdict, error) {
	if err := ctx.Err(); err != nil {
		return Verdict{}, err
	}
	if m == nil || m.idx == nil || m.IsEmpty() {
		return Verdict{}, nil
	}
	if strings.TrimSpace(input) == "" {
		return Verdict{}, nil
	}

	// Build an allow-set once; it is consulted by both the bare-string
	// and template paths below.
	var allow map[string]struct{}
	if len(allowed) > 0 {
		allow = make(map[string]struct{}, len(allowed))
		for _, id := range allowed {
			allow[id] = struct{}{}
		}
	}

	// Try the bare-string path first. A whole-utterance synonym hit
	// (Confidence 0.90) outranks any template hit (0.80/0.65), so we
	// only consult templates when bare matching missed.
	// We keep the bare-string fast-path inline (a separate helper
	// would split state across two methods unnecessarily).
	if v, ok := m.matchBare(allow, input); ok {
		return v, nil
	}

	// Bare-string miss. Try templates.
	return m.matchTemplates(allow, input)
}

// bareMaxUncoveredDefault bounds how many input content stems may fall
// outside a matched bare-string synonym and still count as a
// "whole-utterance" hit. The bare-string tier deliberately fires when a
// synonym is a SUBSET of the input ("wade across" → ford via the synonym
// "wade"), but without a bound a one-word synonym wins on an arbitrarily
// long paste that merely happens to contain it. That is exactly the
// dogfood game-over this guards: a pasted bug report containing the
// choice footer "Esc cancel" routed to the quit intent (synonym
// "cancel") at 0.90 and exited the session. 6 comfortably clears every
// legitimate superset in the corpus (max 1-2 uncovered stems) while
// rejecting prose pastes (tens-to-hundreds of uncovered stems), which
// then fall through to the LLM router — the safe path.
const bareMaxUncoveredDefault = 6

// matchBare runs the Phase-2 bare-string subset check. Returns the
// Verdict (and ok=true) when at least one allowed intent matched or
// when a tie was produced; returns ok=false to signal "no bare-string
// hit; caller should try templates."
//
// A tie verdict counts as "ok=true" — ties from Phase 2 still win
// over any template match because they signal authored ambiguity
// the user should disambiguate before the template path muddies it.
func (m *Matcher) matchBare(allow map[string]struct{}, input string) (Verdict, bool) {
	if len(m.idx.entries) == 0 || m.idx.ac == nil {
		return Verdict{}, false
	}
	// Tokenise the input under the same stopword regime the index
	// was compiled with. A whitespace-only or all-stopwords input
	// yields an empty bag; nothing can match against an empty bag
	// because every synonym entry has len(Stems) > 0.
	inputBag := compileStems(input, m.idx.stopExtras)
	if len(inputBag) == 0 {
		return Verdict{}, false
	}

	// Run the AC pre-filter on the joined stem stream. Spaces
	// between stems prevent cross-stem substring hits (so the
	// dictionary entry "river" doesn't fire on stem "riverboat" —
	// not that Porter2 would yield "riverboat", but the
	// space delimiter is a cheap structural guarantee).
	//
	// We use MatchThreadSafe because [github.com/cloudflare/ahocorasick]
	// .Matcher.Match mutates internal per-node bookkeeping that is
	// safe in single-threaded use but races under concurrent reads.
	// The Matcher is a per-app cached index that any goroutine can
	// call; MatchThreadSafe is the documented form for that pattern.
	joined := strings.Join(inputBag, " ")
	hits := m.idx.ac.MatchThreadSafe([]byte(joined))

	// Recover candidate entry ids from the hit stems. A single hit
	// stem may belong to several entries; we use a set to dedupe.
	candidates := make(map[int]struct{})
	for _, hitIdx := range hits {
		if hitIdx < 0 || hitIdx >= len(m.idx.stemDict) {
			continue
		}
		stem := m.idx.stemDict[hitIdx]
		for _, eid := range m.idx.byStem[stem] {
			candidates[eid] = struct{}{}
		}
	}
	if len(candidates) == 0 {
		return Verdict{}, false
	}

	// Subset check. For each candidate entry, verify that every
	// stem in its set is present in the input bag. We iterate over
	// the input bag once to build an input-set; cheaper than calling
	// sort.SearchStrings per stem when entries have multiple stems.
	inputSet := make(map[string]struct{}, len(inputBag))
	for _, s := range inputBag {
		inputSet[s] = struct{}{}
	}

	// Walk candidate ids in deterministic order so a tie's
	// Candidates list is stable across processes.
	ids := make([]int, 0, len(candidates))
	for id := range candidates {
		ids = append(ids, id)
	}
	sort.Ints(ids)

	// matched[intent] = the first entry id that resolved for that
	// intent. We keep the first because it preserves declaration
	// order: "synonyms before examples; within each, source order"
	// (see Compile). Subsequent matches on the same intent are
	// folded — semroute reports one MatchReason per intent.
	matched := make(map[string]int)
	intentOrder := make([]string, 0)
	for _, eid := range ids {
		entry := m.idx.entries[eid]
		if allow != nil {
			if _, ok := allow[entry.Intent]; !ok {
				continue
			}
		}
		if !subsetOf(entry.stemSet, inputSet) {
			continue
		}
		// Whole-utterance guard. The synonym's stems are a subset of the
		// input bag (checked above), so the number of input content stems
		// the synonym does NOT cover is exactly len(inputSet)-len(stemSet).
		// Reject when that exceeds the bound: a stray synonym token buried
		// in a long paste is not a command. See bareMaxUncoveredDefault.
		if maxUncov := m.idx.bareMaxUncovered; maxUncov > 0 && len(inputSet)-len(entry.stemSet) > maxUncov {
			continue
		}
		if _, exists := matched[entry.Intent]; exists {
			continue
		}
		matched[entry.Intent] = eid
		intentOrder = append(intentOrder, entry.Intent)
	}

	switch len(matched) {
	case 0:
		return Verdict{}, false
	case 1:
		intentID := intentOrder[0]
		entry := m.idx.entries[matched[intentID]]
		return Verdict{
			Intent:       intentID,
			Slots:        map[string]any{},
			Confidence:   ConfidenceWholeSynonym,
			MatchReason:  formatReason(entry),
			MatchPattern: entry.Source,
			MatchKind:    entry.Kind.cacheKind(),
		}, true
	default:
		// Leading-verb tie-break. Bag-of-stems subset matching ignores word
		// order, so an imperative like "commit the staged fix" ties commit
		// (via "commit") against stage (via the "staged"→stage stem) even
		// though a human reads "commit" as the command and "staged fix" as its
		// object. We recover that signal from the ONE structural cue a typed
		// command reliably carries: the verb leads. If the input's first
		// content stem belongs to exactly one tied candidate's matched entry,
		// that candidate is the command and the others are incidental — resolve
		// to it at the whole-synonym band. When the leading stem is in zero or
		// ≥2 candidates' entries the cue is absent or itself ambiguous, so the
		// tie stands and the disambiguation card below fires (preserving the
		// "ties signal authored ambiguity the user should disambiguate"
		// contract for genuinely ambiguous input).
		if lead := leadingContentStem(input, m.idx.stopExtras); lead != "" {
			winner, hits := "", 0
			for _, intentID := range intentOrder {
				if _, ok := m.idx.entries[matched[intentID]].stemSet[lead]; ok {
					winner, hits = intentID, hits+1
				}
			}
			if hits == 1 {
				entry := m.idx.entries[matched[winner]]
				return Verdict{
					Intent:       winner,
					Slots:        map[string]any{},
					Confidence:   ConfidenceWholeSynonym,
					MatchReason:  "leading-verb:" + lead,
					MatchPattern: entry.Source,
					MatchKind:    entry.Kind.cacheKind(),
				}, true
			}
		}

		// Tie. Build a stable Candidate list ordered by intent id so
		// the disambiguation card is deterministic.
		sort.Strings(intentOrder)
		cands := make([]Candidate, 0, len(intentOrder))
		for _, id := range intentOrder {
			entry := m.idx.entries[matched[id]]
			cands = append(cands, Candidate{
				Intent:      id,
				MatchReason: formatReason(entry),
			})
		}
		return Verdict{
			Confidence: ConfidenceTie,
			Candidates: cands,
			// MatchReason summarises the tie for trace output.
			MatchReason: fmt.Sprintf("ambiguous:%d", len(cands)),
		}, true
	}
}

// leadingContentStem returns the Norm (Porter2 stem) of the first
// non-stopword, non-numeric token in input — the imperative verb that
// opens a typed command. Returns "" when the input has no content token
// (all stopwords/numbers/empty), which disables the leading-verb
// tie-break for that input. Uses the same lex pipeline + stopword regime
// the index was compiled with so the stem matches entry stem sets.
func leadingContentStem(input string, stopExtras []string) string {
	for _, tok := range lex.Tokenize(input, stopExtras) {
		if tok.IsStop || tok.IsNum {
			continue
		}
		return tok.Norm
	}
	return ""
}

// matchTemplates is the Phase-4 path. For each allowed intent that
// owns ≥1 template, it tries every template in declaration order and
// keeps the *most specific* per-intent result (more filled slots
// wins; tie-broken by declaration order). Across intents, the
// matcher then applies:
//
//  1. If exactly one intent's best template matched → 0.80 (all
//     captures parsed) or 0.65 (≥1 capture named but unparseable).
//  2. If multiple intents matched with the SAME filled-count → the
//     0.50 tie band, mirroring the bare-string tie behaviour.
//  3. If intents matched with different filled counts → the matcher
//     refuses to pick a winner across intents (the most-specific-
//     wins rule applies *within* an intent only; across intents a
//     tie at slot-count is the safer signal). The tie path produces
//     Confidence 0.50 with all matched candidates.
//
// (3) is a deliberate guardrail. A template that fills more slots
// on intent A than the matched template on intent B is not
// automatically a "better" match — it might just be that A's
// templates declare more slots. Most-specific-wins applies only
// within an intent; we keep cross-intent ties as ties so the
// disambiguation card surfaces both options.
func (m *Matcher) matchTemplates(allow map[string]struct{}, input string) (Verdict, error) {
	if len(m.idx.templateIntents) == 0 {
		return Verdict{}, nil
	}
	tokens := lex.Tokenize(input, m.idx.stopExtras)
	if len(tokens) == 0 {
		return Verdict{}, nil
	}

	type perIntentBest struct {
		template *compiledTemplate
		match    templateMatch
	}
	best := make(map[string]perIntentBest)
	matchedIntents := make([]string, 0)

	for _, intentID := range m.idx.templateIntents {
		if allow != nil {
			if _, ok := allow[intentID]; !ok {
				continue
			}
		}
		for _, tpl := range m.idx.templates[intentID] {
			res := matchTemplate(tpl, tokens)
			if !res.ok {
				continue
			}
			cur, exists := best[intentID]
			// Most-specific-wins: prefer the template with the most
			// filled slots; tie-broken by declaration order
			// (existing entry wins, because we walk in source order).
			if !exists || res.filledCount > cur.match.filledCount {
				best[intentID] = perIntentBest{template: tpl, match: res}
				if !exists {
					matchedIntents = append(matchedIntents, intentID)
				}
			}
		}
	}

	switch len(matchedIntents) {
	case 0:
		return Verdict{}, nil
	case 1:
		id := matchedIntents[0]
		entry := best[id]
		return verdictFromTemplate(id, entry.template, entry.match), nil
	default:
		// Cross-intent disambiguation. If every matched intent fills
		// the same number of slots we surface the standard tie band.
		// Otherwise (different fill counts) we ALSO tie — see the
		// function docstring. Either way the candidate list carries
		// each intent's template source for the disambiguation card.
		sort.Strings(matchedIntents)
		cands := make([]Candidate, 0, len(matchedIntents))
		for _, id := range matchedIntents {
			cands = append(cands, Candidate{
				Intent:      id,
				MatchReason: "template:" + best[id].template.source,
			})
		}
		return Verdict{
			Confidence:  ConfidenceTie,
			Candidates:  cands,
			MatchReason: fmt.Sprintf("ambiguous:%d", len(cands)),
		}, nil
	}
}

// verdictFromTemplate builds the band-appropriate Verdict for a single
// template hit. All-captures-parsed → 0.80; ≥1 missing → 0.65.
//
// MatchPattern carries the verbatim template source so the
// orchestrator's hit-tracking can key by the author's string
// (e.g. "buy {items} for {total_cost}"). MatchKind is always
// "template" for this path — the bare-string path is the only place
// "bare" and "example" originate.
func verdictFromTemplate(intent string, tpl *compiledTemplate, m templateMatch) Verdict {
	if len(m.missingSlots) == 0 {
		return Verdict{
			Intent:       intent,
			Slots:        m.slots,
			Confidence:   ConfidenceTemplateAllSlots,
			MatchReason:  "template:" + tpl.source,
			MatchPattern: tpl.source,
			MatchKind:    "template",
		}
	}
	// Sort missingSlots for deterministic test assertions; map
	// iteration is non-deterministic.
	missing := make([]string, len(m.missingSlots))
	copy(missing, m.missingSlots)
	sort.Strings(missing)
	return Verdict{
		Intent:       intent,
		Slots:        m.slots,
		MissingSlots: missing,
		Confidence:   ConfidenceTemplateMissingSlot,
		MatchReason:  "template_partial:" + tpl.source,
		MatchPattern: tpl.source,
		MatchKind:    "template",
	}
}

// subsetOf reports whether every key of small appears in big.
func subsetOf(small, big map[string]struct{}) bool {
	if len(small) > len(big) {
		return false
	}
	for k := range small {
		if _, ok := big[k]; !ok {
			return false
		}
	}
	return true
}

// formatReason builds the MatchReason for an entry. The shape is
// "synonym:<source>" or "example:<source>"; the leading kind tag
// lets trace consumers and TUI badges differentiate the two without
// rebuilding the matcher.
func formatReason(e synonymEntry) string {
	return e.Kind.tag() + ":" + e.Source
}
