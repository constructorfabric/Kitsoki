// Package judges is the typed contract-encoder for LLM-judge verdicts in
// the dev-story / bugfix pipeline. It sits between the host's
// `oracle.ask_with_mcp` call (which produces a schema-validated JSON
// verdict) and the story runtime's intent gate: the runtime hands the
// raw payload to [Parse], then asks the resulting [Verdict] whether it
// [Verdict.ShouldAutoFire]. The package owns the single canonical place
// where the "is this verdict auto-fireable?" rule lives, so the gate
// clause in stories/bugfix/rooms/*.yaml, the story flow tests, and any
// future tooling all agree on the same semantics.
//
// # Algorithm
//
// There are two interpretive steps, kept deliberately separate:
//
//  1. Decode + validate ([Parse]). The raw bytes are JSON-decoded with
//     [encoding/json.Decoder.UseNumber] (so a confidence literal is not
//     silently widened past the schema's range before validation), then
//     validated against the embedded judge_verdict JSON Schema. Only a
//     payload that is both valid JSON and schema-conformant unmarshals
//     into a typed [Verdict]; anything else returns an error wrapping
//     [ErrMalformedJSON] or [ErrSchemaViolation].
//
//  2. Gate ([Verdict.ShouldAutoFire]). Given a confidence threshold, the
//     verdict auto-fires iff its confidence is at or above the threshold
//     AND neither its verdict nor its intent is "uncertain". The
//     comparison is >= so a verdict landing exactly on the threshold
//     fires. An uncertain verdict never auto-fires regardless of how high
//     its confidence is — uncertainty is a routing signal, not a score.
//
// # Invariants
//
//   - Parse is total over byte slices: it never panics on caller input.
//     A malformed or non-conformant payload is always an error, never a
//     partially-populated [Verdict].
//   - The embedded schema is the source of truth inside this package and
//     is kept in lockstep with stories/bugfix/schemas/judge_verdict.json.
//     If the embedded schema is itself malformed, that is a programmer
//     error in this package and package init panics (see Lifecycle).
//   - ShouldAutoFire and AutoFireIntent are pure reads of the receiver;
//     they have no side effects and do not consult package state.
//
// # Worked example
//
// A confident "accept" verdict from the judge call, gated at the default
// 0.80 threshold:
//
//	raw:       {"verdict":"pass","intent":"accept",
//	            "reason":"All checks passed.","confidence":0.92}
//	Parse:     Verdict{Verdict:"pass", Intent:"accept",
//	                    Reason:"All checks passed.", Confidence:0.92}
//	ShouldAutoFire(0.80): 0.92 >= 0.80, verdict/intent not "uncertain" → true
//	AutoFireIntent():     "accept"   (the runtime dispatches this intent)
//
// A runnable form of this trace lives in [ExampleParse]. The boundary
// behaviour of the gate is shown in [ExampleVerdict_ShouldAutoFire].
//
// # Lifecycle
//
// The judge_verdict schema is compiled exactly once, at package init,
// into compiledSchema, so [Parse] is cheap on the hot path and never
// re-parses the schema. A failure to compile the embedded schema is a
// drifted-constant bug in this package, so init panics loudly rather
// than carrying a "validator unavailable" branch through Parse.
//
// # Non-goals
//
//   - No host handler registration. Wiring `oracle.ask_with_mcp` into the
//     host call table is the orchestrator/host layer's job; this package
//     is import-only and never touches the host registry, so it stays
//     usable from tests and tooling without standing up a host.
//   - No LLM invocation. The judge call itself — including MCP-side
//     schema enforcement — is performed by `oracle.ask_with_mcp`. This
//     package only interprets the result, so the expensive, non-
//     deterministic step stays out of the typed layer and out of tests.
//   - No custom field transforms or coercion. Parse returns the verdict
//     as the schema describes it (e.g. restart_from passes through
//     verbatim); it is a thin typed layer, not a place to reshape or
//     remap the judge's decision.
//   - No knowledge of judge modes. The judge_mode policy
//     (human / llm / llm_then_human) lives in the caller; this package
//     only owns the confidence-and-uncertainty half of the gate so the
//     YAML, the flow harness, and future tooling share one rule.
//
// # Reference
//
// The judge polymorphism (the judge_mode flag and its three policies),
// the artifact-then-verdict shape, and the confidence-threshold gate are
// documented in docs/case-studies/bug-fix.md. How the runtime binds the
// verdict and resolves the intent gate is in docs/stories/state-machine.md.
package judges
