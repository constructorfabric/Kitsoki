// Package store implements event-sourced session persistence on modernc.org/sqlite (§8).
// This file defines the Event type and the EventKind enum.
package store

import (
	"encoding/json"
	"time"

	"hally/internal/app"
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
	HostInvoked EventKind = "HostInvoked"
	// HostReturned is appended when the host.* invocation completes.
	HostReturned EventKind = "HostReturned"
	// OffPathEntered is appended when the user activates the off-path mode (§7.7).
	OffPathEntered EventKind = "OffPathEntered"
	// OffPathExited is appended when the user returns from off-path mode.
	OffPathExited EventKind = "OffPathExited"
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
