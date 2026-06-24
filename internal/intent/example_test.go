// Runnable godoc examples for the intent envelope types. Each Example
// function's // Output: block is checked by
// `go test -run "^Example" ./internal/intent/...`.
package intent_test

import (
	"fmt"

	"kitsoki/internal/intent"
)

// ExampleValidationError_Error is the canonical rejection worked example: a
// MISSING_SLOTS envelope (the same trace shown in the package doc) renders as
// "CODE: message" while keeping its structured fields for the caller.
func ExampleValidationError_Error() {
	err := &intent.ValidationError{
		Code:         intent.ErrMissingSlots,
		Message:      "intent requires slots: [destination]",
		MissingSlots: []string{"destination"},
	}

	fmt.Println(err.Error())
	fmt.Println("missing:", err.MissingSlots)
	// Output:
	// MISSING_SLOTS: intent requires slots: [destination]
	// missing: [destination]
}

// ExampleCandidate shows how Candidate entries populate the disambiguation
// list on an AMBIGUOUS_INTENT envelope: each carries the intent name to
// resubmit plus author metadata the LLM uses to choose.
func ExampleCandidate() {
	err := &intent.ValidationError{
		Code:    intent.ErrAmbiguousIntent,
		Message: "multiple plausible intents",
		Candidates: []intent.Candidate{
			{Intent: "ford", Title: "Ford the river", Why: "shallow crossing"},
			{Intent: "ferry", Title: "Take the ferry", Why: "safer but costs cash"},
		},
	}

	fmt.Println(err.Error())
	for _, c := range err.Candidates {
		fmt.Printf("- %s (%s): %s\n", c.Intent, c.Title, c.Why)
	}
	// Output:
	// AMBIGUOUS_INTENT: multiple plausible intents
	// - ford (Ford the river): shallow crossing
	// - ferry (Take the ferry): safer but costs cash
}

// ExampleIntentCall shows the zero-value-valid contract: a fully populated
// call is just a struct, and JSON-shaped fields are plain Go values.
func ExampleIntentCall() {
	call := intent.IntentCall{
		Intent:     "go",
		Slots:      map[string]any{"destination": "north"},
		Confidence: 0.92,
	}

	fmt.Println("intent:    ", call.Intent)
	fmt.Println("destination:", call.Slots["destination"])
	fmt.Printf("confidence: %.2f\n", call.Confidence)
	// Output:
	// intent:     go
	// destination: north
	// confidence: 0.92
}
