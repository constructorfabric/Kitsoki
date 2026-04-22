// Package orchestrator provides the turn-loop brain that wires together
// the harness, machine, and store (§4.2).
package orchestrator

import (
	"hally/internal/app"
	"hally/internal/intent"
	"hally/internal/store"
)

// OutcomeMode is the discriminant of a TurnOutcome (§5.3).
type OutcomeMode int

const (
	// ModeTransitioned means a transition fired successfully.
	ModeTransitioned OutcomeMode = iota
	// ModeClarify means required slots were missing; the TUI should gather them.
	ModeClarify
	// ModeRejected means the intent was rejected (guard failed or not allowed).
	ModeRejected
	// ModeCompleted means the new state is terminal.
	ModeCompleted
	// ModeOffPath is not yet implemented (Stage 7).
	ModeOffPath
	// ModeCancelled means the turn was cancelled (context cancelled by user).
	ModeCancelled
)

// String returns a human-readable name for the outcome mode.
func (m OutcomeMode) String() string {
	switch m {
	case ModeTransitioned:
		return "transitioned"
	case ModeClarify:
		return "clarify"
	case ModeRejected:
		return "rejected"
	case ModeCompleted:
		return "completed"
	case ModeOffPath:
		return "offpath"
	case ModeCancelled:
		return "cancelled"
	default:
		return "unknown"
	}
}

// TurnOutcome is the result of a single turn, ready for the TUI to render (§5.3, §9a.1).
// One struct that the TUI can pattern-match on.
type TurnOutcome struct {
	// Mode indicates what happened.
	Mode OutcomeMode
	// View is the rendered narrative (set on Transitioned/Completed/Rejected).
	View string
	// NewState is the state after the turn (unchanged on Clarify/Rejected guard-fail).
	NewState app.StatePath
	// Events are the events appended to the store this turn.
	Events []store.Event
	// AllowedIntents lists allowed intent names in the new state (for menu refresh).
	AllowedIntents []string
	// SlotsNeeded is populated on ModeClarify: the names of missing required slots.
	SlotsNeeded []SlotNeed
	// Candidates is populated on disambiguation (future Stage 7).
	Candidates []intent.Candidate
	// GuardHint is the author-declared hint on ModeRejected with GUARD_FAILED.
	GuardHint string
	// ErrorCode is the rejection code on ModeRejected.
	ErrorCode intent.ErrorCode
	// ErrorMessage is the human-readable rejection message.
	ErrorMessage string
	// PendingIntent is the intent name waiting for slot completion (set on ModeClarify).
	PendingIntent string
	// PendingSlots are the already-provided slots for a pending clarification.
	PendingSlots map[string]any
	// TurnNumber is the turn that just completed.
	TurnNumber app.TurnNumber
}

// SlotNeed describes a single missing slot for the clarification UI (§7.3).
type SlotNeed struct {
	// Name is the slot name.
	Name string
	// Prompt is the author-provided prompt string.
	Prompt string
	// Description explains what the slot means.
	Description string
	// Type is the slot type ("string", "enum", "bool", "int", "float").
	Type string
	// Values is the enum value list (non-nil only for type=="enum").
	Values []string
	// FormatHint is an optional formatting hint for the UI.
	FormatHint string
	// Examples are 2–3 canonical values for display.
	Examples []string
}
