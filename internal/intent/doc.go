// Package intent defines the turn vocabulary that crosses the LLM boundary:
// [IntentCall] (a typed, accepted intent invocation) and [ValidationError]
// (the structured rejection envelope), together with the canonical
// [ErrorCode] enum. It sits at the seam between the language tier and the
// deterministic engine — the harness in [kitsoki/internal/harness] and the
// MCP validator in [kitsoki/internal/mcp] produce an IntentCall, and
// [kitsoki/internal/machine] Validate/Turn consume it, returning a
// *ValidationError when the call cannot run. The package holds only the wire
// types; no validation logic lives here.
//
// # Algorithm
//
// There is no algorithm in this package — it is the shared vocabulary the
// validation flow speaks. The flow that produces these types lives in
// [kitsoki/internal/machine]:
//
//  1. The harness (or a deterministic match) yields an [IntentCall]: an
//     intent name, extracted [world.Slots], and an optional self-reported
//     Confidence.
//  2. machine.Validate checks the call against the current state: is the
//     intent defined, is it allowed here, are required slots present and
//     well-typed? The first failing check decides the [ErrorCode].
//  3. On failure it returns a *[ValidationError] whose omitempty fields are
//     populated only for that code (MissingSlots for MISSING_SLOTS,
//     Candidates for the disambiguation codes, GuardHint for GUARD_FAILED).
//     On success it returns nil and Turn applies the transition.
//
// The error envelope is the LLM's repair signal: each code names a distinct
// fix, so the model can correct the call on its next attempt rather than
// re-guessing blindly.
//
// # Invariants
//
//   - Code and Message are always set on a returned *ValidationError; the
//     slice/hint fields are nil unless the code carries them.
//   - The [ErrorCode] string set is canonical and asserted by Mode 1 replay
//     fixtures — recorded sessions compare on the exact strings, so renaming
//     or removing a code is a breaking change.
//   - Confidence is the LLM's self-report in the [0,1] range; it is advisory
//     metadata, not a gate — validation never rejects a call for low
//     confidence.
//
// # Worked example
//
// A "go" intent that requires a "destination" slot is called with no slots in
// a state where "go" is allowed. machine.validateSlots finds the required slot
// absent and returns:
//
//	in:  IntentCall{ Intent:"go", Slots:{} }
//	out: ValidationError{
//	       Code:         "MISSING_SLOTS",
//	       Message:      "intent requires slots: [destination]",
//	       MissingSlots: []string{"destination"},
//	     }
//	err.Error() == "MISSING_SLOTS: intent requires slots: [destination]"
//
// The LLM reads MissingSlots, fills "destination", and resubmits. A runnable
// form of this trace lives in [ExampleValidationError_Error].
//
// # Lifecycle
//
// These are plain value/pointer types with no shared mutable state. An
// [IntentCall] is constructed per turn and discarded after the turn is logged;
// a *[ValidationError] is constructed at the point of rejection and read once
// by the caller (and serialized once across MCP). There is no concurrency
// contract to honour because nothing here is shared across goroutines — each
// turn owns its own values. The zero [IntentCall] is valid input (it is
// rejected, not panicked on); the zero [ValidationError] is not a meaningful
// error (always set at least Code).
//
// # Non-goals
//
//   - No validation logic. The decision of which code applies belongs to
//     [kitsoki/internal/machine] so this package stays a dependency-light leaf
//     that both the engine and the MCP transport can import without cycles.
//   - No slot type coercion beyond what the schema validator does. This
//     package carries the verdict, it does not parse or coerce slot values —
//     typed parsing lives in [kitsoki/internal/slotparse].
//   - No confidence thresholding. Confidence is recorded for audit and replay;
//     turning it into an accept/reject gate would put an interpretive decision
//     in a deterministic type, which is exactly the boundary kitsoki keeps
//     separate.
//
// # Reference
//
// Where these types sit in the turn pipeline — translation, validation,
// decision — is docs/architecture/overview.md § 3 ("The journey of one turn"),
// and the package map in § 11.3 lists this package's role. The typed slot
// parsers that run upstream of validation are documented in
// [kitsoki/internal/slotparse].
package intent
