package lex

// builtinStopwords is a tiny English stoplist — intentionally not
// comprehensive. Under-stripping is preferred over over-stripping so
// that intent-bearing words ("hunt", "buy", "ford") are never lost.
// Apps extend this list per-call via Tokenize's extraStops argument.
var builtinStopwords = map[string]struct{}{
	// articles, conjunctions, prepositions
	"a": {}, "an": {}, "the": {},
	"and": {}, "or": {}, "but": {},
	"of": {}, "in": {}, "on": {}, "at": {},
	"to": {}, "for": {}, "from": {}, "with": {}, "by": {},

	// pronouns
	"i": {}, "me": {}, "my": {}, "mine": {},
	"we": {}, "us": {}, "our": {}, "ours": {},
	"you": {}, "your": {}, "yours": {},
	"he": {}, "him": {}, "his": {},
	"she": {}, "her": {}, "hers": {},
	"it": {}, "its": {},
	"they": {}, "them": {}, "their": {}, "theirs": {},

	// auxiliaries / copula
	"is": {}, "are": {}, "was": {}, "were": {},
	"be": {}, "been": {}, "being": {}, "am": {},
	"do": {}, "does": {}, "did": {}, "done": {},

	// demonstratives
	"this": {}, "that": {}, "these": {}, "those": {},

	// common filler — keep this section small; these are the words
	// users sprinkle into commands ("let's just hunt", "please buy six").
	// Entries are matched against both the post-stem Norm AND the
	// pre-stem lowered Surface, so we include both the surface form
	// ("please") and the Porter2 stem ("pleas").
	"let": {}, "let's": {}, "lets": {},
	"please": {}, "pleas": {},
	"just": {}, "only": {}, "onli": {}, "also": {}, "too": {},

	// s-suffix of common contractions; "let's" segments as a single
	// token under UAX#29 but if a future tokeniser config splits it
	// into "let" + "s", we drop the lone "s" too.
	"s": {},
}

// IsStopword reports whether norm is in the builtin or extra stopword
// list. Each extraStops entry is matched literally (exact string
// equality), so callers must pass already-lowercased forms; mixed-case
// extras silently never match. A nil or empty extraStops checks only the
// builtin list. Safe for concurrent use: the builtin list is read-only
// and the function holds no state.
func IsStopword(norm string, extraStops []string) bool {
	if _, ok := builtinStopwords[norm]; ok {
		return true
	}
	for _, w := range extraStops {
		if w == norm {
			return true
		}
	}
	return false
}
