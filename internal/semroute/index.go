package semroute

import (
	"fmt"
	"sort"
	"strings"

	"github.com/cloudflare/ahocorasick"

	"kitsoki/internal/app"
	"kitsoki/internal/lex"
)

// synonymEntry is one compiled synonym/example. It is the unit the
// matcher's subset check operates on. Stems is the sorted, deduplicated
// stem set of the source string; len(Stems) > 0 is an invariant.
type synonymEntry struct {
	// Intent is the intent id the synonym was declared on.
	Intent string
	// Source is the original source string, used for MatchReason and
	// future Phase 5 hit-tracking. Carried verbatim from the YAML.
	Source string
	// Kind discriminates "synonym:" from "example:" hits in
	// MatchReason; both behave identically under the subset rule.
	Kind synonymKind
	// Stems is the synonym's sorted, deduplicated stem set. The
	// matcher tests whether every member is present in the input's
	// stem set.
	Stems []string
	// stemSet is Stems hoisted into a map for O(1) membership checks
	// during the subset test. Built once at Compile and never mutated.
	stemSet map[string]struct{}
}

type synonymKind uint8

const (
	kindSynonym synonymKind = iota
	kindExample
)

func (k synonymKind) tag() string {
	switch k {
	case kindExample:
		return "example"
	default:
		return "synonym"
	}
}

// cacheKind returns the [turncache.SynonymKey.Kind] taxonomy value
// for this matcher kind. Distinct from [tag] because the
// MatchReason format ("synonym:wade", "example:foo") is a separate
// vocabulary from the cache's hit-tracking taxonomy ("bare",
// "example", "template", "enum_value"). The orchestrator sets
// [Verdict.MatchKind] from this method so RecordSynonymHit writes
// the cache-canonical kind without the orchestrator having to know
// the matcher's internal nomenclature.
func (k synonymKind) cacheKind() string {
	switch k {
	case kindExample:
		return "example"
	default:
		return "bare"
	}
}

// CompileError is returned by [Compile] when a synonym is malformed.
//
// Bare-string synonyms compile clean unless they're empty after
// stopword stripping (which the loader catches first). Template
// synonyms (Phase 4 — those containing `{slot_name}` captures) have
// several structural failure modes, each surfaced via a distinct
// Reason string:
//
//   - "unknown slot %q referenced by template" — the template names a
//     slot the owning intent does not declare.
//   - "adjacent captures {a}{b} — templates require a literal token
//     between captures" — the matcher cannot unambiguously split a
//     run between two side-by-side captures.
//   - "unmatched '{' …" / "unmatched '}' …" — brace mismatch.
//   - "empty capture {} …" — `{}` with no slot name.
//
// Callers surface this error the same way they surface guard/view
// compile errors from [machine.New].
type CompileError struct {
	// Intent is the intent id the offending synonym was declared on.
	Intent string
	// Synonym is the verbatim source string from YAML.
	Synonym string
	// Reason is the human-readable cause.
	Reason string
}

// Error implements the error interface.
func (e *CompileError) Error() string {
	return fmt.Sprintf("semroute: intent %q: synonym %q: %s",
		e.Intent, e.Synonym, e.Reason)
}

// compiledIndex is the immutable result of compiling an [app.AppDef].
// Held by [Matcher]; never mutated after Compile returns.
type compiledIndex struct {
	// entries holds every successfully compiled synonym/example,
	// indexed by a globally unique id. Match uses the id to recover
	// the source string and intent. Order is "intents sorted by id;
	// within an intent, synonyms before examples; within each list,
	// source order from the AppDef" — stable and deterministic.
	entries []synonymEntry
	// byStem maps a stem string to the list of entry ids that
	// contain it. Used to convert ahocorasick hits (which are per-
	// stem) into a candidate entry set.
	byStem map[string][]int
	// ac is the Aho-Corasick scanner over the stem dictionary. It
	// runs on the input's joined stem stream (space-delimited) to
	// flag which stems appear in the input; we then walk byStem to
	// recover candidate entries. nil when there are no entries.
	ac *ahocorasick.Matcher
	// stemDict is the dictionary in the order ac was built — used to
	// translate ac.Match() indices back into stem strings.
	stemDict []string
	// stopExtras is a snapshot of app.Routing.StopwordsExtra used to
	// build this index. Match tokenises input with the same extras
	// so input bags align with synonym bags.
	stopExtras []string
	// bareMaxUncovered bounds how many input content stems may fall
	// OUTSIDE a matched bare-string synonym and still count as a
	// "whole-utterance" hit (see [bareMaxUncoveredDefault] and the
	// guard in matchBare). 0 disables the guard.
	bareMaxUncovered int
	// templates groups every successfully compiled template synonym
	// by owning intent id. Order within each slice mirrors the
	// declaration order from YAML, which is what the most-specific-
	// wins tie-break consults.
	templates map[string][]*compiledTemplate
	// templateIntents is the sorted intent-id slice for the templates
	// map — iterating it gives a deterministic walk order for the
	// matcher.
	templateIntents []string
}

// Compile builds an index for the AppDef's declared synonyms and
// examples. See the package doc for the algorithm.
//
// Synonyms that contain '{' or '}' are rejected with a *CompileError;
// the loader wraps the error to surface it the same way it surfaces
// guard/view compile errors.
//
// An empty AppDef (no intents, or every intent declares no synonyms
// and no examples) yields a valid Matcher that always returns the
// zero Verdict. Callers do not need to nil-check.
func Compile(def *app.AppDef) (*Matcher, error) {
	if def == nil {
		return &Matcher{idx: &compiledIndex{}}, nil
	}

	var stopExtras []string
	if def.Routing != nil {
		stopExtras = append([]string(nil), def.Routing.StopwordsExtra...)
	}

	// Collect intents from global library + state-local definitions.
	// Use a map keyed by intent id, preferring state-local over global
	// when both define the same id (state-local wins via insertion
	// order: globals first, then states). Phase 2 doesn't model
	// state-scoping; intent ids are globally unique by the loader's
	// invariant anyway, so the collation just defends against double-
	// counting.
	intents := collectIntents(def)

	entries := make([]synonymEntry, 0, 16)
	byStem := make(map[string][]int)
	stemSet := make(map[string]struct{})
	templates := make(map[string][]*compiledTemplate)

	// Stable ordering: sort intent ids so the resulting entry order
	// is deterministic across processes (important for trace events
	// and the Candidates field in tie verdicts).
	ids := make([]string, 0, len(intents))
	for id := range intents {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		def := intents[id]
		// First the explicit synonyms, in declaration order.
		for _, syn := range def.Synonyms {
			if isTemplateSyn(syn) {
				// Phase 4: braces signal a {slot_name} template.
				ct, err := compileTemplate(id, syn, def, stopExtras)
				if err != nil {
					return nil, err
				}
				templates[id] = append(templates[id], ct)
				continue
			}
			stems := compileStems(syn, stopExtras)
			if len(stems) == 0 {
				// Tokeniser produced no content tokens — either an
				// empty string or all-stopwords. Skip silently:
				// matching against an empty stem set is meaningless
				// and would always trivially succeed.
				continue
			}
			entries = appendEntry(entries, id, syn, kindSynonym, stems, byStem, stemSet)
		}
		// Then implicit synonyms from examples, in declaration order.
		for _, ex := range def.Examples {
			// Examples are not validated against '{' / '}' — they
			// are intended for the LLM as natural-language prompts
			// and may legitimately contain braces. We simply skip
			// any whose stem set is empty.
			stems := compileStems(ex, stopExtras)
			if len(stems) == 0 {
				continue
			}
			entries = appendEntry(entries, id, ex, kindExample, stems, byStem, stemSet)
		}
	}

	// Build the Aho-Corasick dictionary in a deterministic order so
	// the matcher's Index() return values are reproducible across
	// processes. We don't actually depend on the order — Match()
	// converts hits via byStem — but stable order helps debugging.
	stemDict := make([]string, 0, len(stemSet))
	for s := range stemSet {
		stemDict = append(stemDict, s)
	}
	sort.Strings(stemDict)

	var ac *ahocorasick.Matcher
	if len(stemDict) > 0 {
		ac = ahocorasick.NewStringMatcher(stemDict)
	}

	// Templates: collect intent ids that own templates in sorted order
	// so Match's walk is reproducible across processes.
	templateIntents := make([]string, 0, len(templates))
	for id := range templates {
		templateIntents = append(templateIntents, id)
	}
	sort.Strings(templateIntents)

	return &Matcher{
		idx: &compiledIndex{
			entries:          entries,
			byStem:           byStem,
			ac:               ac,
			stemDict:         stemDict,
			stopExtras:       stopExtras,
			templates:        templates,
			templateIntents:  templateIntents,
			bareMaxUncovered: bareMaxUncoveredDefault,
		},
	}, nil
}

// isTemplateSyn reports whether src is a template synonym (contains
// `{` or `}`). Phase 4 dispatches templates to compileTemplate; bare
// strings continue through the Phase 2 stem-bag path.
func isTemplateSyn(src string) bool {
	return strings.ContainsAny(src, "{}")
}

// collectIntents walks the AppDef and returns a flat map of
// intent-id → Intent definition with examples/synonyms UNIONed across
// every place the intent is declared (global library + every state-
// local override).
//
// We union rather than picking a winner because Phase 2 only cares
// about which utterances should resolve to which intent id; the
// runtime allowed-intents filter is what makes a per-state intent
// "different" from a global one. A user typing "look for meat" should
// route to `hunt` regardless of whether the live state's intent block
// happened to omit that example.
//
// Title and Description are intentionally NOT unioned — semroute
// doesn't use them.
func collectIntents(def *app.AppDef) map[string]app.Intent {
	out := make(map[string]app.Intent)
	addExamples := make(map[string]map[string]struct{})
	addSynonyms := make(map[string]map[string]struct{})

	merge := func(id string, ix app.Intent) {
		exs, ok := addExamples[id]
		if !ok {
			exs = make(map[string]struct{})
			addExamples[id] = exs
		}
		syns, ok := addSynonyms[id]
		if !ok {
			syns = make(map[string]struct{})
			addSynonyms[id] = syns
		}
		for _, e := range ix.Examples {
			exs[e] = struct{}{}
		}
		for _, s := range ix.Synonyms {
			syns[s] = struct{}{}
		}
		// Carry a single Intent in `out` so callers can read other
		// fields if they ever need to; the union of strings lives in
		// the side maps and is folded back below.
		out[id] = ix
	}

	for id, ix := range def.Intents {
		merge(id, ix)
	}

	type frame struct{ states map[string]*app.State }
	work := []frame{{states: def.States}}
	for len(work) > 0 {
		f := work[len(work)-1]
		work = work[:len(work)-1]
		for _, st := range f.states {
			if st == nil {
				continue
			}
			for id, ix := range st.Intents {
				merge(id, ix)
			}
			if len(st.States) > 0 {
				work = append(work, frame{states: st.States})
			}
		}
	}

	// Fold the unioned strings back into out, with deterministic
	// ordering so the resulting entry slice is reproducible.
	for id, ix := range out {
		ix.Examples = sortedKeys(addExamples[id])
		ix.Synonyms = sortedKeys(addSynonyms[id])
		out[id] = ix
	}
	return out
}

// sortedKeys returns the keys of set in lexicographic order.
func sortedKeys(set map[string]struct{}) []string {
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// appendEntry pushes one entry into the index, updating both the
// stem dictionary and the per-stem inverted index.
func appendEntry(
	entries []synonymEntry,
	intent, source string,
	kind synonymKind,
	stems []string,
	byStem map[string][]int,
	stemSet map[string]struct{},
) []synonymEntry {
	id := len(entries)
	set := make(map[string]struct{}, len(stems))
	for _, s := range stems {
		set[s] = struct{}{}
		byStem[s] = append(byStem[s], id)
		stemSet[s] = struct{}{}
	}
	entries = append(entries, synonymEntry{
		Intent:  intent,
		Source:  source,
		Kind:    kind,
		Stems:   stems,
		stemSet: set,
	})
	return entries
}

// compileStems tokenises s with the app's stopwords-extra, drops
// stopwords, deduplicates, and returns a sorted stem slice. Returns
// nil when no content tokens survive.
func compileStems(s string, stopExtras []string) []string {
	toks := lex.Tokenize(s, stopExtras)
	if len(toks) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(toks))
	for _, t := range toks {
		if t.IsStop {
			continue
		}
		if t.Norm == "" {
			continue
		}
		set[t.Norm] = struct{}{}
	}
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
