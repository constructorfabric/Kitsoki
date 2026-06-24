package intent

import (
	"kitsoki/internal/world"

	// Blank import keeps santhosh-tekuri/jsonschema/v6 in go.mod after tidy.
	_ "github.com/santhosh-tekuri/jsonschema/v6"
)

// IntentCall is the typed intent invocation produced by the LLM (or by a
// deterministic match) and checked by [kitsoki/internal/machine] Validate
// before any transition runs. It is the unit that gets logged and replayed, so
// its JSON shape is a stable contract across the MCP boundary. The zero value
// is valid input: an empty Intent with no Slots and zero Confidence — Validate
// rejects it with UNKNOWN_INTENT/INTENT_NOT_ALLOWED rather than panicking.
type IntentCall struct {
	// Intent is the name of the intent being called.
	Intent string `json:"intent"`
	// Slots holds the extracted slot values. May be empty for zero-arg intents.
	Slots world.Slots `json:"slots,omitempty"`
	// Confidence is the LLM's self-reported extraction confidence (0–1, optional).
	Confidence float64 `json:"confidence,omitempty"`
}

// ErrorCode is the discriminant of the structured error envelope: it tells
// the LLM (and replay tooling) which category of rejection occurred so it can
// repair the call rather than guess. The set of values is canonical and tested
// by Mode 1 replay fixtures — adding or renaming a code is a breaking change,
// because recorded sessions assert on the exact string.
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

// ValidationError is the structured error payload returned when an intent call
// is rejected (by [kitsoki/internal/machine] Validate/Turn, or by the MCP
// validator). It implements error, so it both crosses the MCP boundary as JSON
// and flows through ordinary Go error handling. JSON tags are load-bearing: the
// struct is serialized into the MCP tool result and read back by the LLM, so
// the omitempty fields are populated only for the codes that carry them
// (MissingSlots for MISSING_SLOTS, Candidates for the disambiguation codes,
// GuardHint for GUARD_FAILED). Code and Message are always set; the slice
// fields are nil otherwise. The zero value is not a meaningful error — always
// set at least Code.
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
	// Candidates is populated on AMBIGUOUS_INTENT and INTENT_UNKNOWN: the
	// shortlist of intents the LLM should disambiguate between.
	Candidates []Candidate `json:"candidates,omitempty"`
	// GuardHint is the author-declared hint populated on GUARD_FAILED, telling
	// the LLM why every transition arm's guard rejected the call.
	GuardHint string `json:"guard_hint,omitempty"`
}

// Candidate is one entry in the disambiguation list carried by Candidates on
// AMBIGUOUS_INTENT and INTENT_UNKNOWN errors. It gives the LLM enough author
// metadata (title/description) plus an optional model rationale to choose
// among the plausible intents on its next attempt.
type Candidate struct {
	// Intent is the candidate intent's name — the value the LLM should resubmit.
	Intent string `json:"intent"`
	// Title is the author's short label for the intent, for display.
	Title string `json:"title,omitempty"`
	// Description is the author's longer gloss of what the intent does.
	Description string `json:"description,omitempty"`
	// Why is an optional rationale from the LLM; falls back to the author's Description.
	Why string `json:"why,omitempty"`
}

// Error implements the error interface so a *ValidationError can flow through
// ordinary Go error handling as well as crossing the MCP boundary as JSON.
// The rendered form is "CODE: message" (e.g. "MISSING_SLOTS: intent requires
// slots: [destination]"); the structured fields remain available to callers
// that type-assert back to *ValidationError. The receiver must be non-nil.
func (e *ValidationError) Error() string {
	return string(e.Code) + ": " + e.Message
}
