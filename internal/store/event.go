// Package store implements event-sourced session persistence on modernc.org/sqlite (§8).
// This file defines the Event type and the EventKind enum.
package store

import (
	"encoding/json"
	"time"

	"kitsoki/internal/app"
)

// EventKind is the discriminant of the event log. The exact set of values
// is the canonical surface that Mode 1 adversarial fixtures assert against (§5.2, §8).
// Changing or adding a kind is a breaking change for existing test cassettes.
type EventKind string

const (
	// TurnStarted is appended at the start of every user turn.
	TurnStarted EventKind = "TurnStarted"
	// LLMCalled is appended immediately before the harness is invoked.
	LLMCalled EventKind = "LLMCalled"
	// LLMToolCall is appended when the LLM produces a tool call result.
	LLMToolCall EventKind = "LLMToolCall"
	// ValidationFailed is appended when Machine.Validate rejects a tool call.
	ValidationFailed EventKind = "ValidationFailed"
	// TransitionApplied is appended after a successful transition fires.
	TransitionApplied EventKind = "TransitionApplied"
	// EffectApplied is appended once per effect executed in a transition.
	EffectApplied EventKind = "EffectApplied"
	// HostInvoked is appended when a host.* side effect is dispatched (§11).
	// Snapshots the up-front-resolved args at machine time (pre-bind for any
	// later step in the same on_enter block).  See HostDispatched for the
	// post-rerender, dispatch-time args the handler actually receives.
	HostInvoked EventKind = "HostInvoked"
	// HostDispatched is appended immediately before the orchestrator
	// invokes a host.* handler.  Its payload records the *rerendered* args
	// (what the handler actually receives) plus `rerender_fell_back: bool`
	// which is true when any leaf had to fall back to its pre-bind value
	// because its template failed to render against the current world.
	// Additive to HostInvoked; replayed as a no-op.
	HostDispatched EventKind = "HostDispatched"
	// HostReturned is appended when the host.* invocation completes.
	HostReturned EventKind = "HostReturned"
	// OffPathEntered is appended when the user activates the off-path mode (§7.7).
	OffPathEntered EventKind = "OffPathEntered"
	// OffPathExited is appended when the user returns from off-path mode.
	OffPathExited EventKind = "OffPathExited"
	// OffPathQuestion is appended when the user asks a free-form question
	// in off-path mode. Replay treats it as a no-op: off-path turns do not
	// mutate world or state.
	OffPathQuestion EventKind = "OffPathQuestion"
	// OffPathAnswer is appended when the oracle returns a reply to an
	// off-path question. Replay treats it as a no-op.
	OffPathAnswer EventKind = "OffPathAnswer"
	// TurnEnded is appended at the end of every user turn.
	TurnEnded EventKind = "TurnEnded"
	// StateExited is appended when the machine leaves a state (compound or leaf).
	StateExited EventKind = "StateExited"
	// StateEntered is appended when the machine enters a state (compound or leaf).
	StateEntered EventKind = "StateEntered"
	// IntentAccepted is appended when an intent call passes Validate.
	IntentAccepted EventKind = "IntentAccepted"
	// GuardRejected is appended when all guards for a transition failed.
	GuardRejected EventKind = "GuardRejected"
	// JobSubmitted is appended when a background job is dispatched to the
	// scheduler (background: true effect).
	JobSubmitted EventKind = "JobSubmitted"
	// JobCompleted is appended in the synthetic background-completion turn
	// when a background job reaches a terminal state (done/failed/cancelled).
	JobCompleted EventKind = "JobCompleted"
	// TimeoutFired is appended in the synthetic timeout turn when a state's
	// declared Timeout: elapses on the orchestrator's clock.  Replay treats
	// the accompanying TransitionApplied as authoritative for state update;
	// TimeoutFired is annotation-only so traces can distinguish a timeout
	// from a user-driven transition.
	TimeoutFired EventKind = "TimeoutFired"
	// HarnessError is appended when an orchestrator-side dispatch loop
	// fails loudly (e.g. settlePostBindEmits hit its recursion cap, or
	// machine.DispatchPostBindEmits returned an error).  Carries
	// payload{"phase": <string>, "error": <string>} so a journal reader
	// can see why the turn settled where it did.  Replay treats it as a
	// no-op — the accompanying TransitionApplied events (if any) are
	// authoritative for state; HarnessError exists to surface the
	// post-bind half-bound limbo case to operators.
	HarnessError EventKind = "HarnessError"
)

// Event is one row in the append-only event log (§8 DDL).
// JSON tags mirror the SQLite payload_json column structure.
type Event struct {
	// Turn is the monotonic turn number within a session.
	Turn app.TurnNumber `json:"turn"`
	// Seq is the per-turn sequence number (starts at 0).
	Seq int `json:"seq"`
	// Ts is the wall-clock time of the event (unix microseconds).
	Ts time.Time `json:"ts"`
	// Kind identifies the event type.
	Kind EventKind `json:"kind"`
	// Payload holds the event-specific data as raw JSON.
	Payload json.RawMessage `json:"payload,omitempty"`
	// ParentTurn is the foreground turn that was active when this event was
	// appended as a side-channel (off-path) batch. Zero for normal foreground
	// events. Carried in memory only; not persisted to the DB.
	ParentTurn app.TurnNumber `json:"-"`
}

// History is an ordered slice of events for a session, as returned by Store.LoadHistory.
type History []Event

// Snapshot is a materialized state snapshot, stored every N turns (default 20).
// JSON tags are used for SQLite serialization.
type Snapshot struct {
	// Turn is the turn number at which this snapshot was taken.
	Turn app.TurnNumber `json:"turn"`
	// StatePath is the serialized active state path at snapshot time.
	StatePath app.StatePath `json:"state_path"`
	// WorldJSON holds the world snapshot as a JSON object.
	WorldJSON json.RawMessage `json:"world_json"`
	// RNGSeed is reserved for deterministic replay of any randomness.
	RNGSeed int64 `json:"rng_seed"`
}
