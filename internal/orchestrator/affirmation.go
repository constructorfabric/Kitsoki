// affirmation.go — deterministic affirmation lexicon for the plan-continuation
// routing guard. Used by routeViaContextualRouter (semantic.go) when a plan is
// pending: an exact-match affirmation deterministically routes to the configured
// plan_accept_intent without an LLM call; a content-bearing utterance routes to
// plan_refine_intent instead.
package orchestrator

// affirmationPhrases is the closed set of bare-verb affirmations that signal
// "proceed with the pending plan as-is". Exact-match after normalisation (see
// normalizeForCompare): "ok but skip closed issues" does NOT match because it
// carries content beyond the matched phrase.
var affirmationPhrases = map[string]struct{}{
	"ok":          {},
	"ok go ahead": {},
	"go ahead":    {},
	"do it":       {},
	"apply it":    {},
	"apply":       {},
	"accept":      {},
	"accept it":   {},
	"yes":         {},
	"yep":         {},
	"yes please":  {},
	"proceed":     {},
	"ship it":     {},
	"lgtm":        {},
	"looks good":  {},
	"do that":     {},
	"go for it":   {},
}

// IsAffirmation reports whether s is a bare affirmation that should be
// interpreted as "proceed with the pending plan without changes". It performs
// exact-match after normalisation (lower-case, trimmed, collapsed whitespace)
// so "ok but skip closed issues" (content-bearing) is NOT an affirmation.
func IsAffirmation(s string) bool {
	_, ok := affirmationPhrases[normalizeForCompare(s)]
	return ok
}
