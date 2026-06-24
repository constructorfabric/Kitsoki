// Package orchestrator provides the turn-loop brain that wires together
// the harness, machine, and store. See docs/architecture/overview.md
// "The journey of one turn" for the narrative.
package orchestrator

import (
	"kitsoki/internal/app"
	"kitsoki/internal/expr"
	"kitsoki/internal/intent"
	"kitsoki/internal/render"
	"kitsoki/internal/store"
)

// OutcomeMode is the discriminant of a TurnOutcome.
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
	// ModeOffPath indicates a free-form off-path chat turn.
	// Off-path turns do NOT mutate world or state; they route through
	// Orchestrator.AskOffPath, which fires host.agent.talk against a
	// per-session chat thread keyed by (app_id, room="off_path",
	// scope_key=session_id). No TransitionApplied event is emitted; only
	// OffPathEntered/Exited and OffPathQuestion/Answer are appended.
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

// TurnOutcome is the result of a single turn, ready for the TUI to render.
// One struct that the TUI can pattern-match on.
type TurnOutcome struct {
	// Mode indicates what happened.
	Mode OutcomeMode
	// View is the rendered narrative (set on Transitioned/Completed/Rejected).
	// Pre-rendered at the machine's stable width (80); the TUI uses this
	// when no typed view is available. When TypedView is non-nil the
	// TUI re-renders it at the live viewport width on every resize
	// (Issue 4 / option (a) — see internal/tui/transcript.go).
	View string
	// TypedView, RenderEnv, and Renderer carry the typed-view payload
	// for views that survived as typed elements (no extends, no
	// template_file). Populated by machine.renderView when the state's
	// view shape is element-array — the "templating happens before
	// element layout" pipeline. TUI inspects TypedView
	// to decide whether to use AppendTurnTyped (lipgloss reflow on
	// resize) or fall back to AppendTurn with View (Glamour at
	// width-time).
	TypedView *app.View           `json:"-"`
	RenderEnv expr.Env            `json:"-"`
	Renderer  *render.AppRenderer `json:"-"`
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
	// ContextRoute is the routing receipt for a contextually-routed turn
	// (nil for deterministic/semantic/LLM turns). Surfaced to TUI/web.
	ContextRoute *ContextRouteReceipt
	// HarnessError is the (optional) human-readable description of an
	// orchestrator-side dispatch loop failure that fired during this turn
	// — e.g. settlePostBindEmits hit its recursion cap, or
	// machine.DispatchPostBindEmits returned an error against a malformed
	// guard.  When set, the corresponding store.HarnessError event is
	// also recorded in Events.  The turn is NOT aborted; state settles
	// at the pre-emit resting place rather than vanishing into a
	// half-bound limbo (P1-A/B in the dev-story-bugfix-unify code review).
	HarnessError string
}

// OneShotInput configures a stateless one-shot turn (Orchestrator.OneShot).
//
// Exactly one of Intent or Input must be set. With Intent, the call goes
// directly to the machine (no harness, no LLM). With Input, the harness
// routes the free-text first.
type OneShotInput struct {
	State  app.StatePath
	World  map[string]any
	Intent string
	Slots  map[string]any
	Input  string
}

// HostCallSummary captures one host.* invocation made during a OneShot turn,
// in a form convenient for JSON output.
type HostCallSummary struct {
	Namespace string         `json:"namespace"`
	Args      map[string]any `json:"args,omitempty"`
	Data      map[string]any `json:"data,omitempty"`
	Error     string         `json:"error,omitempty"`
}

// EffectSummary captures one EffectApplied event in a flattened form.
type EffectSummary struct {
	Set       map[string]any `json:"set,omitempty"`
	Increment map[string]int `json:"increment,omitempty"`
	Say       string         `json:"say,omitempty"`
}

// OneShotResult is the rich, persistence-free outcome of Orchestrator.OneShot.
//
// `kitsoki turn` serialises this directly as JSON: the field set is designed
// to give an outside observer (an AI collaborator, a flow-test author, a
// CI compliance check) everything they need to answer "what happened?"
// without re-running the turn.
type OneShotResult struct {
	Mode           OutcomeMode       `json:"mode"`
	Intent         string            `json:"intent"`
	Slots          map[string]any    `json:"slots,omitempty"`
	PrevState      app.StatePath     `json:"prev_state"`
	NextState      app.StatePath     `json:"next_state"`
	WorldBefore    map[string]any    `json:"world_before"`
	WorldAfter     map[string]any    `json:"world_after"`
	Effects        []EffectSummary   `json:"effects_applied,omitempty"`
	HostCalls      []HostCallSummary `json:"host_calls,omitempty"`
	View           string            `json:"view_rendered"`
	AllowedIntents []string          `json:"allowed_intents,omitempty"`
	ErrorCode      string            `json:"error_code,omitempty"`
	ErrorMessage   string            `json:"error_message,omitempty"`
	GuardHint      string            `json:"guard_hint,omitempty"`
	SlotsNeeded    []SlotNeed        `json:"slots_needed,omitempty"`
}

// SlotNeed describes a single missing slot for the clarification UI.
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
