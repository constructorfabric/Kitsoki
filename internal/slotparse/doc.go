// Package slotparse holds the typed slot parsers the semantic matcher
// hands each template capture to. It sits one layer below
// [kitsoki/internal/semroute]: when a template synonym captures a
// `{slot_name}` run, semroute calls [For](slot).Parse on the captured
// tokens to turn raw surface text into a typed value (int, money,
// enum, bool, date, or a list of those). Each parser is a small
// in-process grammar keyed off [app.Slot.Type] — they are NOT
// general-purpose NLP. The dialect they recognise is the one the
// routing stack's worked examples and the Oregon-Trail slot tests
// exercise.
//
// # Algorithm
//
// Every parser advances left-to-right over the [lex.Token] slice the
// caller supplies. Stopwords ([lex.Token.IsStop]) are skipped at the
// start but never midway — once a parser is "in" a numeric run or a
// synonym match it consumes contiguous tokens. The per-type strategy
// order, first hit wins:
//
//   - [ParseInt] tries: (a) the first digit-form token; (b) failing
//     that, the maximal run of tokens whose surfaces feed
//     [lex.SpelledNumber] cleanly ("two hundred fifty" → 250). Reason
//     is "digit" or "spelled".
//
//   - [ParseMoney] tries, in this order: (a) "$<digits>" — a literal
//     "$" token immediately followed by a digit-form token, with an
//     optional ".<digits>" decimal that rounds to the nearest dollar
//     via [math.Round]; (b) a digit-form token followed by
//     "dollar"/"dollars"/"buck"/"bucks"; (c) when slot.Type is "money"
//     or "$int", a bare digit token. Spelled+unit forms ("a few
//     hundred dollars") feed through SpelledNumber's largest-prefix
//     read and require a trailing unit word. Reason is one of
//     "dollar-sign" / "unit-dollars" / "unit-bucks" / "bare-int".
//
//   - [ParseEnum] tries three tiers, first hit wins:
//     (1) direct: a slot.Values entry's stem-bag is a subset of the
//     input's stem-bag;
//     (2) synonym: per-value synonym phrases from slot.Synonyms run
//     through the same sorted-stem-set containment check;
//     (3) fuzzy: any input token's Norm within Damerau-Levenshtein-1
//     of a single-stem slot.Values entry's Norm, via
//     github.com/agnivade/levenshtein.
//     Multiple matches at the same tier yield OK=false with
//     Reason="ambiguous"; lower tiers are NOT consulted when a higher
//     tier produced a unique hit.
//
//   - [ParseBool] is a flat keyword table. The yes-list is
//     {yes, y, true, sure, ok, okay, affirmative}; the no-list is
//     {no, n, false, nope, nah, negative}. Reason is the exact
//     keyword that matched.
//
//   - [ParseDate] walks a small whitelist of phrasings — bare relative
//     ("today"), "in N days/weeks", "next <weekday>", "next/last
//     week", "month day [year]", and 4-digit numeric forms — then
//     falls back to github.com/araddon/dateparse in strict mode.
//     Reason carries a "date:<form>" prefix.
//
//   - [ParseList] consumes a comma- or "and"-separated run of items,
//     each parsed by an inner [Parser]; prefix-wins on junk (every
//     item that parsed before the first failure is kept). Reason is
//     "list:<first-item-reason>".
//
// All parsers share one [Result] / [Parser] interface so the matcher
// can dispatch off slot.Type via [For] without a type switch at every
// call site:
//
//	parser := slotparse.For(slot)
//	if parser == nil {
//	    // unsupported type (e.g. "string"); caller falls back to LLM
//	} else if r := parser.Parse(tokens, slot); r.OK {
//	    // r.Value carries the parsed value; r.Consumed records the
//	    // token ranges that were eaten; r.Reason explains why.
//	}
//
// For callers that already know the target type, the direct entry
// points are exported too: [ParseInt], [ParseMoney], [ParseEnum],
// [ParseBool], [ParseDate], [ParseList].
//
// # Invariants
//
// The [Result] / [Reason] contract every parser upholds:
//
//   - When OK is true, Consumed is non-empty and covers the consumed
//     token ranges as half-open intervals (tokens[Start:End]); a
//     parser may report multiple disjoint ranges (money "$120" eats
//     the "$" token plus the digit token). When OK is false, Value
//     and Consumed are zero-valued and the caller falls through to
//     the next strategy.
//   - Reason is a stable diagnostic string with a documented prefix
//     ("digit", "spelled", "synonym:rich guy", "fuzzy:banker",
//     "dollar-sign", "date:month_day", "list:digit", …). It exists for
//     trace events and tests; callers must not branch on its exact
//     format beyond the documented prefixes.
//   - Parsers are pure: Parse depends only on its arguments and
//     produces no side effects, so it is safe for concurrent use. The
//     one exception is [ParseDate], which reads the wall clock via
//     [time.Now]; [ParseDateAt] takes an injectable now for
//     deterministic tests and replay.
//
// # Worked example
//
// A template capture "{total_cost}" against the utterance "please buy
// two hundred fifty oxen" reaches [ParseInt] with the tokenised run:
//
//	in:  "please buy two hundred fifty oxen"
//	tok: please, buy, two, hundred, fifty, oxen   ("please" is a stopword)
//	digit-form pass: none found
//	spelled pass:    first cardinal start is "two"; longest accepting
//	                 prefix is "two hundred fifty" → 250
//	result: { Value:250, OK:true, Reason:"spelled",
//	          Consumed:[{Start:2, End:5}] }
//
// A runnable form of this trace lives in [ExampleParseInt]; the enum,
// money, date, and list parsers each have a sibling Example.
//
// # Non-goals
//
//   - No general-purpose NLP or free-form date/number understanding —
//     the parsers recognise a fixed, auditable dialect so a routing
//     decision stays explainable. Inputs outside the dialect return
//     OK=false and route to the LLM tier rather than being guessed at.
//   - No arithmetic or cent-level money precision: money values are
//     whole dollars (decimals round via [math.Round]) because the
//     matcher is routing intents, not running a ledger.
//   - No "string" slot parsing here. A bare string slot has no typed
//     grammar to apply — [For] returns nil and the matcher hands the
//     captured raw text straight through — so adding a string parser
//     would be a no-op that only obscures the fallback path.
//   - The matcher itself, and the orchestrator wiring that calls into
//     it, deliberately live elsewhere ([kitsoki/internal/semroute] and
//     internal/orchestrator). Keeping composition out of slotparse
//     leaves these parsers pure and reusable by any caller that already
//     has [lex.Token]s in hand.
//
// # Reference
//
// The user-facing reference for the routing stack — the four tiers,
// the slot-parser type table, and confidence bands — is
// docs/architecture/semantic-routing.md (see "Synonym templates" and
// its "Slot parser types" table). The matcher that drives these
// parsers is documented in [kitsoki/internal/semroute].
package slotparse
