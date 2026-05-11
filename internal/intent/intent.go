// Package intent defines IntentCall, the MCP error envelope, and the
// JSON-schema validation layer for slot payloads (§5.2, §3.5).
package intent

import (
	"kitsoki/internal/world"

	// Blank import keeps santhosh-tekuri/jsonschema/v6 in go.mod after tidy.
	_ "github.com/santhosh-tekuri/jsonschema/v6"
)

// IntentCall is the typed, accepted intent invocation produced by the LLM
// and validated by Machine.Validate. It is what gets logged and replayed.
// JSON tags allow it to cross the MCP boundary.
type IntentCall struct {
	// Intent is the name of the intent being called.
	Intent string `json:"intent"`
	// Slots holds the extracted slot values. May be empty for zero-arg intents.
	Slots world.Slots `json:"slots,omitempty"`
	// Confidence is the LLM's self-reported extraction confidence (0–1, optional).
	Confidence float64 `json:"confidence,omitempty"`
}

// ErrorCode is the discriminant of the structured error envelope (§5.2).
// The set of values is canonical and tested by Mode 1 fixtures — changes are breaking.
type ErrorCode string

const (
	// ErrUnknownIntent means the supplied intent name is not defined anywhere.
	ErrUnknownIntent ErrorCode = "UNKNOWN_INTENT"
	// ErrIntentNotAllowed means the intent is defined but not allowed in the current state.
	ErrIntentNotAllowed ErrorCode = "INTENT_NOT_ALLOWED_IN_STATE"
	// ErrMissingSlots means one or more required slots were absent from the call.
	ErrMissingSlots ErrorCode = "MISSING_SLOTS"
	// ErrInvalidSlotValue means a slot value failed type/enum/regex validation.
	ErrInvalidSlotValue ErrorCode = "INVALID_SLOT_VALUE"
	// ErrGuardFailed means every guard for the matching transition evaluated false.
	ErrGuardFailed ErrorCode = "GUARD_FAILED"
	// ErrAmbiguousIntent means the LLM reported multiple plausible intents.
	ErrAmbiguousIntent ErrorCode = "AMBIGUOUS_INTENT"
	// ErrIntentUnknown means the LLM could not map the utterance to any allowed intent.
	ErrIntentUnknown ErrorCode = "INTENT_UNKNOWN"
)

// ValidationError is the structured error payload returned by Machine.Validate
// when an intent call is rejected (§5.2). JSON tags are load-bearing: this
// struct is serialized into the MCP tool result and read by the LLM.
type ValidationError struct {
	// Code is the machine-readable error category.
	Code ErrorCode `json:"code"`
	// Message is a human-readable summary for the LLM.
	Message string `json:"message"`
	// MissingSlots lists required slot names that were absent.
	MissingSlots []string `json:"missing_slots,omitempty"`
	// Suggestions are LLM-readable hints on how to fix the call.
	Suggestions []string `json:"suggestions,omitempty"`
	// AllowedIntents lists the intent names currently valid in this state.
	AllowedIntents []string `json:"allowed_intents,omitempty"`
	// Candidates is populated on AMBIGUOUS_INTENT and INTENT_UNKNOWN (§5.2, §7.4).
	Candidates []Candidate `json:"candidates,omitempty"`
	// GuardHint is the author-declared hint populated on GUARD_FAILED (§5.2, §7.5).
	GuardHint string `json:"guard_hint,omitempty"`
}

// Candidate is one entry in the disambiguation list populated on
// AMBIGUOUS_INTENT and INTENT_UNKNOWN errors (§7.4).
type Candidate struct {
	Intent      string `json:"intent"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	// Why is an optional rationale from the LLM; falls back to the author's Description.
	Why string `json:"why,omitempty"`
}

// Error implements the error interface so ValidationError can be returned as error.
func (e *ValidationError) Error() string {
	return string(e.Code) + ": " + e.Message
}
