package store

// event.go defines the [Event] type and the [EventKind] enum — the on-disk
// vocabulary every sink writes and [BuildJourney] reads. See doc.go for the
// package overview.

import (
	"encoding/json"
	"time"

	"kitsoki/internal/app"
)

// EventKind is the discriminant of the event log. Values use the dotted form
// that the SPA's subsystem chip logic already consumes, so writer and reader
// agree on one vocabulary without a translation layer. The Go identifier is
// stable; only the on-disk string value changed in wave 2b.
type EventKind string

const (
	// TurnStarted is appended at the start of every user turn.
	TurnStarted EventKind = "turn.start"
	// UserInputReceived is appended at the moment user input is received for a
	// turn, before the harness is invoked. Its turn number matches the
	// TurnStarted that follows it. Replaces the exporter-side synthesised
	// turn.input row — the real event is now in the history.
	UserInputReceived EventKind = "turn.input"
	// LLMToolCall is appended when the LLM produces a tool call result.
	LLMToolCall EventKind = "oracle.tool_call"
	// ValidationFailed is appended when Machine.Validate rejects a tool call.
	ValidationFailed EventKind = "machine.validation_failed"
	// TransitionApplied is appended after a successful transition fires.
	TransitionApplied EventKind = "machine.transition"
	// EffectApplied is appended once per effect executed in a transition.
	// It carries ONLY world mutations
	// (`set:` / `increment:`); operator narration (`say:`) is split into the
	// dedicated MachineSay kind below so `world.update` unambiguously means a
	// world mutation.
	EffectApplied EventKind = "world.update"
	// MachineSay is appended once per `say:` effect that resolves. Payload
	// carries {"text": "<narration>"}. Split out of EffectApplied
	// so a runstatus timeline can render
	// operator narration as its own row instead of a textless world.update.
	// Replay treats it as a no-op — say does not mutate world or state.
	MachineSay EventKind = "machine.say"
	// HostInvoked is appended when a host.* side effect is dispatched.
	// Snapshots the up-front-resolved args at machine time (pre-bind for any
	// later step in the same on_enter block).  See HostDispatched for the
	// post-rerender, dispatch-time args the handler actually receives.
	HostInvoked EventKind = "harness.called"
	// HostDispatched is appended immediately before the orchestrator
	// invokes a host.* handler.  Its payload records the *rerendered* args
	// (what the handler actually receives) plus `rerender_fell_back: bool`
	// which is true when any leaf had to fall back to its pre-bind value
	// because its template failed to render against the current world.
	// Additive to HostInvoked; replayed as a no-op.
	HostDispatched EventKind = "harness.dispatched"
	// HostReturned is appended when the host.* invocation completes.
	HostReturned EventKind = "harness.returned"
	// OffPathEntered is appended when the user activates the off-path mode.
	OffPathEntered EventKind = "machine.off_path_entered"
	// OffPathExited is appended when the user returns from off-path mode.
	OffPathExited EventKind = "machine.off_path_exited"
	// OffPathQuestion is appended when the user asks a free-form question
	// in off-path mode. Replay treats it as a no-op: off-path turns do not
	// mutate world or state.
	OffPathQuestion EventKind = "oracle.off_path.question"
	// OffPathAnswer is appended when the oracle returns a reply to an
	// off-path question. Replay treats it as a no-op.
	OffPathAnswer EventKind = "oracle.off_path.answer"
	// TurnEnded is appended at the end of every user turn.
	TurnEnded EventKind = "turn.end"
	// StateExited is appended when the machine leaves a state (compound or leaf).
	StateExited EventKind = "machine.state_exited"
	// StateEntered is appended when the machine enters a state (compound or leaf).
	StateEntered EventKind = "machine.state_entered"
	// IntentAccepted is appended when an intent call passes Validate.
	IntentAccepted EventKind = "machine.intent_accepted"
	// GuardRejected is appended when all guards for a transition failed.
	GuardRejected EventKind = "machine.guard_rejected"
	// JobSubmitted is appended when a background job is dispatched to the
	// scheduler (background: true effect).
	JobSubmitted EventKind = "scheduler.submitted"
	// JobCompleted is appended in the synthetic background-completion turn
	// when a background job reaches a terminal state (done/failed/cancelled).
	JobCompleted EventKind = "scheduler.completed"
	// TimeoutFired is appended in the synthetic timeout turn when a state's
	// declared Timeout: elapses on the orchestrator's clock.  Replay treats
	// the accompanying TransitionApplied as authoritative for state update;
	// TimeoutFired is annotation-only so traces can distinguish a timeout
	// from a user-driven transition.
	TimeoutFired EventKind = "machine.timeout"
	// HarnessError is appended when an orchestrator-side dispatch loop
	// fails loudly (e.g. settlePostBindEmits hit its recursion cap, or
	// machine.DispatchPostBindEmits returned an error).  Carries
	// payload{"phase": <string>, "error": <string>} so a journal reader
	// can see why the turn settled where it did.  Replay treats it as a
	// no-op — the accompanying TransitionApplied events (if any) are
	// authoritative for state; HarnessError exists to surface the
	// post-bind half-bound limbo case to operators.
	HarnessError EventKind = "harness.error"

	// GateDecided is appended when the engine resolves an intent gate — the
	// set of advancing intents available at the end of a room/phase's turn,
	// and which decider (human/llm/default) resolved it. Payload
	// carries {"state": <path>, "available_intents": [<string>],
	// "decider": "human"|"llm"|"default", "chosen_intent": <string>,
	// "bailed_to_human": <bool>}. Replay treats it as a no-op — the
	// accompanying TransitionApplied events (if any) are authoritative for
	// state; GateDecided records *why* the turn advanced or stopped so the
	// TUI/runstatus can explain a one-shot auto-advance or a staged stop.
	GateDecided EventKind = "machine.gate_decided"

	// OracleCalled is appended at the moment an oracle verb is dispatched.
	// Payload carries the full prompt, with-args, schema-ref, deadline,
	// call_id, and verb. Replay treats this as a no-op — state reconstruction
	// uses EffectApplied events for the submission bind. Exists for audit and
	// the runstatus SPA which pairs by call_id.
	OracleCalled EventKind = "oracle.call.start"

	// OracleReturned is appended when the oracle verb response lands.
	// Payload carries the full submission body, meta (tokens/cost/model —
	// opaque), duration_ms, the matching call_id, and verb. Replay no-op.
	OracleReturned EventKind = "oracle.call.complete"

	// OracleError is appended instead of OracleReturned when the oracle verb
	// returns an error. Payload carries the error string, call_id, verb.
	// Replay no-op.
	OracleError EventKind = "oracle.call.error"
)

// Event is one row in the append-only event log.
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
	// StatePath is the active state path at the moment this event was written.
	// Populated by the orchestrator/machine at write time; no exporter back-fill.
	StatePath app.StatePath `json:"state_path,omitempty"`
	// Payload holds the event-specific data as raw JSON.
	Payload json.RawMessage `json:"payload,omitempty"`
	// ParentTurn is the foreground turn that was active when this event was
	// appended as a side-channel (off-path) batch. Zero for normal foreground
	// events. Persisted to JSONL as parent_turn.
	// Note: parent_turn=0 is semantically identical to absent in the on-disk
	// JSONL because TurnNumber is int64 and omitempty omits the zero value.
	// Valid turn numbers start at 1, so zero unambiguously means "no parent".
	ParentTurn app.TurnNumber `json:"parent_turn,omitempty"`
	// CallID is the deterministic oracle call identifier for OracleCalled,
	// OracleReturned, and OracleError events. Empty for all other event kinds.
	// Derived via DeriveCallID in internal/host/callid.go. The runstatus SPA
	// pairs OracleCalled with OracleReturned by this field.
	CallID string `json:"call_id,omitempty"`
	// EpisodeID is the cassette episode identifier for cassette-backed oracle
	// calls. Present only on OracleCalled events emitted by the cassette
	// dispatcher. Together with MatchIdx it allows post-resume reconstruction
	// of the per-episode match counter so resume generates collision-free
	// call_ids.
	EpisodeID string `json:"episode_id,omitempty"`
	// MatchIdx is the 0-based match counter for replay:any cassette episodes.
	// For a normal (non-replay:any) episode it is always 0. Present only on
	// OracleCalled events emitted by the cassette dispatcher alongside EpisodeID.
	MatchIdx int `json:"match_idx,omitempty"`
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
