// Package semroute implements the semantic-routing tier. It sits in the
// orchestrator between [orchestrator.Orchestrator.TryDeterministic] and
// [orchestrator.Orchestrator.Turn]: when the deterministic menu-display
// match misses, the orchestrator consults the per-app [Matcher] before
// calling the LLM harness.
//
// Two compiled-synonym shapes share one Matcher:
//
//   - Bare-string synonyms — stem-bag containment, band 0.90.
//   - Template synonyms — positional `{slot_name}` capture feeding typed
//     slot parsers, bands 0.80 / 0.65.
//
// # Algorithm
//
// The matcher operates on stem bags. For every (intent, synonym) pair
// declared on the app — plus every (intent, example) pair, which the
// matcher treats as an implicit synonym — the compiler:
//
//  1. Tokenises the synonym via [lex.Tokenize] with the app's
//     [app.RoutingConfig.StopwordsExtra] (so a per-app stopword like
//     "wagon" never enters a synonym's stem set).
//  2. Drops stopwords and deduplicates, keeping the surviving
//     [lex.Token.Norm] strings (Porter2 stems) as the synonym's
//     "stem set."
//  3. Records the stem set, together with its source string, against
//     the owning intent id.
//
// A synonym matches an input I iff its stem set is a (non-empty)
// subset of I's stem set. The input is tokenised through the same
// pipeline, then candidate intents are pre-filtered with an
// Aho-Corasick scan: only intents whose declared synonyms touch at
// least one of the input's stems get the (cheap) subset check. The
// pre-filter keeps Match closer to O(input + matched stems) than
// O(synonyms × input).
//
// # Confidence bands
//
// Match emits four non-zero bands plus a tie band:
//
//   - 0.90 — exactly one allowed intent matched by bare-string
//     synonym or example.
//   - 0.80 — template match, every {slot_name} capture parsed cleanly.
//   - 0.65 — template match, ≥1 capture's typed parser returned
//     OK=false; the caller surfaces a clarification.
//   - 0.50 — two or more allowed intents matched the same input
//     (ambiguous; caller surfaces a disambiguation card).
//   - 0    — no match; the input is empty after tokenisation; or
//     every match landed on a non-allowed intent.
//
// Band 1.00 is owned by the deterministic tier (display/example exact
// match) and is never emitted from semroute. The bands are fixed
// constants, not learned — see [ConfidenceWholeSynonym] and siblings.
//
// # Templates
//
// A template synonym contains one or more `{slot_name}` captures
// alongside literal anchor tokens. The compiler parses each template
// into an alternating sequence of literal runs and captures and
// validates the template invariants:
//
//   - Every {slot_name} must reference a declared slot on the owning
//     [app.Intent.Slots] map; otherwise [Compile] returns a
//     *CompileError naming the offender.
//   - Captures must be separated by literal tokens. Adjacent captures
//     ({a}{b}) are a compile error — the matcher cannot
//     unambiguously split a run between two captures with no
//     anchor between them.
//   - Leading and trailing captures are allowed ({x} alone, "{x} text",
//     "text {x}"). The single-capture form captures the entire input.
//
// Match (positional) algorithm:
//
//  1. Tokenise the input through [lex.Tokenize] under the same
//     stopword regime the compiler used.
//  2. Walk the template's segments left to right. Each literal seeks
//     forward in the input for its first stem-aligned occurrence.
//     Non-matching stopwords in the input are skipped silently; a
//     non-matching non-stopword is a hard miss.
//  3. The input tokens between the prior anchor and the current
//     literal's anchor form the previous capture's range.
//  4. After the last literal, any remaining tokens form the trailing
//     capture's range (if the last segment is a capture).
//  5. Each captured range is handed to [slotparse.For](slot).Parse.
//     A string slot with no parser specialisation gets the captured
//     raw text (joined surfaces). A parser OK=false outcome lists the
//     slot in MissingSlots and drops the band from 0.80 to 0.65.
//
// "Greedy to the right" means: when two literals could anchor at
// multiple positions, each literal lands at its EARLIEST occurrence at
// or after the previous anchor. Equivalently, each preceding capture
// takes the SHORTEST run that satisfies the next literal. Authors who
// want a longer capture write more literal context.
//
// Stopwords that appear BETWEEN the stems of a single literal segment
// (e.g. "the" sitting inside the input run that matches the literal
// "across river") are absorbed by the literal — they advance the
// matcher's cursor past the end of the matched run, and do NOT leak
// into either the preceding or trailing capture. Stopwords that
// appear INSIDE a capture range (e.g. between the last literal and
// a trailing {slot}) are preserved verbatim in the slot's surface
// text via joinSurfaces.
//
// Most-specific-wins: when multiple templates from the SAME intent
// match an input, the matcher keeps the one with the most filled
// slots; ties are broken by declaration order. Across intents the
// matcher does NOT pick a winner by fill count — different intents
// declare different slot counts, so a higher count is not a "better"
// match. Cross-intent matches surface as 0.50 ties with both
// candidates listed.
//
// # Worked example
//
// Given a river-crossing state (allowed intents ford / caulk / ferry /
// wait) and the synonym list on `ford: [wade, walk it, drive through
// the water]`:
//
//	in:  "wade across the river"
//	tok: wade, acros, river       (stopwords let, the dropped)
//	bag: {wade, acros, river}
//	candidates via AC:            ford  (stem "wade" hit)
//	stem set of "wade":           {wade}   ⊂ bag → ok
//	matched intents:              [ford]
//	verdict: { Intent:"ford", Confidence:0.90,
//	           MatchReason:"synonym:wade", Slots:{}, Candidates:[] }
//
// A runnable form of this trace lives in [ExampleMatcher_Match].
//
// # Lifecycle
//
// [Compile] builds the index for an [app.AppDef] once at machine load.
// The returned [*Matcher] is safe for concurrent use; [Matcher.Match]
// performs only reads. Compile returns *CompileError when a declared
// synonym is structurally malformed (unknown slot reference, adjacent
// captures, unmatched braces); callers surface this error the same
// way they surface guard/view compile errors.
//
// # Non-goals
//
//   - No alternation, regex, or optional segments in templates — the
//     design commits to "more templates over more DSL features."
//   - No partial-utterance bag-style match for templates; bag-style
//     matching is what bare-string synonyms are for.
//   - No learned ranking. The bands are constants.
//   - No state-scoped synonyms. Allowed-intents come from the caller;
//     declared synonyms live on the intent and apply uniformly.
//
// All four restrictions are deliberate. The matcher is meant to be
// auditable and explainable in well under 1k LoC of mental model;
// future phases pay for additional expressivity with additional spec.
//
// # Reference
//
// The user-facing reference for the routing stack — the four tiers,
// growing the synonym library, and the turn cache — is
// docs/architecture/semantic-routing.md. The typed slot parsers each
// template capture is handed to are documented in
// [kitsoki/internal/slotparse].
package semroute
