// Package lex provides the tokenisation, normalisation, Porter2
// stemming, stopword filtering, and deterministic lexical signature that
// the semantic-routing subsystem is built on. It sits beneath
// [kitsoki/internal/semroute] (which tokenises synonyms and inputs into
// stem bags), [kitsoki/internal/turncache] (which keys cached turns by
// [Signature]), and [kitsoki/internal/slotparse] (which reuses
// [SpelledNumber] for numeral slots) — every one of those packages funnels
// its raw text through this one before doing anything routing-specific.
//
// # Algorithm
//
// [Tokenize] is the single entry point; the rest of the surface
// ([Signature], [SpelledNumber], [IsStopword]) either composes it or
// feeds it. Per call, Tokenize runs a fixed seven-stage pipeline:
//
//  1. NFKC-normalise the input via golang.org/x/text/unicode/norm — this
//     collapses fullwidth digits, ligatures and compatibility forms so
//     downstream stages see a canonical UTF-8 string.
//  2. Lowercase via golang.org/x/text/cases with language.English, done
//     BEFORE segmentation so byte offsets stay reconstructible.
//  3. Segment into words using UAX#29 word boundaries
//     (github.com/clipperhouse/uax29/v2/words).
//  4. Drop pure-whitespace / pure-punctuation segments; keep any segment
//     containing at least one letter or digit.
//  5. Stem alphabetic surfaces via Porter2
//     (github.com/kljensen/snowball, "english", true). Non-alphabetic
//     surfaces (digit runs, "200lbs") pass through unchanged.
//  6. Flag IsStop using the union of the builtin stopword list and the
//     caller's extraStops slice, tested against both the surface and the
//     stem (Porter2 mutates some stoplist entries, e.g. "please" → "pleas").
//  7. Flag IsNum when the surface is a digit form OR a single-word
//     spelled cardinal recognised by [SpelledNumber].
//
// [Signature] composes the pipeline into a turncache key: tokenise, drop
// stopwords, fold spelled numerals to their decimal form, sort+dedupe,
// then hash. Two utterances that differ only in word order, stopwords, or
// "six" vs "6" collapse to the same signature.
//
// # Invariants
//
//   - State-free. Every exported function is pure and deterministic
//     across processes; the only inputs are the source string and an
//     optional per-app extraStops list. There is no package-level mutable
//     state and nothing to construct or close.
//   - Byte offsets are into the NORMALISED source. [Token.Start] and
//     [Token.End] index the NFKC-normalised, lowercased string, not the
//     raw caller-provided string (which may have a different byte
//     length). Callers that need to echo the exact original text must
//     keep their own raw input; [Token.Surface] is the segment of the
//     normalised string. Start <= End always holds.
//   - Signature is either "" or exactly [HexSignatureLength] lowercase
//     hex characters — never anything else.
//
// # Worked example
//
//	in:  "let's wade across the river"
//	tok: let's(stop) wade across the(stop) river
//	     (NFKC + lowercase, then UAX#29 segmentation)
//	"let's" and "the" carry IsStop=true; the rest are content words.
//
// And the signature collapse — these two phrasings key the turncache
// identically because stopwords drop, "six"→"6", and order is sorted away:
//
//	"buy 6 oxen and 200 lbs of food"   ─┐
//	"let's buy six oxen, 200 lbs food" ─┴─> same 16-hex Signature
//
// Both traces have runnable, output-checked forms in [ExampleTokenize]
// and [ExampleSignature].
//
// # Non-goals
//
//   - No lemmatisation beyond Porter2 stemming. [Token.Lemma] falls back
//     to [Token.Norm]; there is no dictionary lemmatiser. Reason: stems
//     are sufficient to collapse inflections for intent routing, and a
//     real lemmatiser would add a dictionary dependency for no routing
//     gain.
//   - No language-specific customisation at runtime. The package is
//     English-only (English lowercaser, English Porter2, English
//     stoplist). Reason: per-app vocabulary is customised through the
//     extraStops argument, not by swapping languages; multilingual
//     routing is a future spec, not a config knob.
//   - No caching or memoisation. Reason: tokenisation is cheap and the
//     state-free contract is worth more than shaving a hash; callers that
//     want to cache results cache the [Signature], not the tokens.
//   - No character n-gram, fuzzy, or phonetic matching. Reason: routing
//     operates on word-level stem bags; sub-word similarity is out of
//     scope and would blur the auditable "which stems matched" story
//     that semroute depends on.
//
// # Reference
//
// The tokenisation contract, the signature equivalence-group rules, and
// the four-tier routing stack that consumes them are documented in
// docs/architecture/semantic-routing.md. The typed slot parsers that
// reuse [SpelledNumber] live in [kitsoki/internal/slotparse].
package lex
