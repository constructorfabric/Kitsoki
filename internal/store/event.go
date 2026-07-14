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
	LLMToolCall EventKind = "agent.tool_call"
	// AgentStreamEvent is appended while an agent call is running to surface
	// compact thinking/tool/narration breadcrumbs before the terminal
	// agent.call.complete event lands. Replay treats it as a no-op; the full raw
	// provider stream remains in the transcript sidecar.
	AgentStreamEvent EventKind = "agent.stream"
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
	// OperationStarted records entry into an operation-local world overlay.
	OperationStarted EventKind = "operation.started"
	// OperationCommitted records the durable patch copied out of an overlay.
	OperationCommitted EventKind = "operation.committed"
	// OperationAbandoned records an overlay discarded without entering durable world.
	OperationAbandoned EventKind = "operation.abandoned"
	// OperationDraftPersisted records an explicit draft handle produced from an overlay.
	OperationDraftPersisted EventKind = "operation.draft_persisted"
	// OperationRunStarted records the start of a session-level operation run.
	// Distinct from OperationStarted, which is a state-local world overlay.
	OperationRunStarted EventKind = "operation.run_started"
	// OperationRunPhaseStarted records a named phase becoming active inside a
	// session-level operation run.
	OperationRunPhaseStarted EventKind = "operation.phase_started"
	// OperationRunPhaseCompleted records a named phase completing inside a
	// session-level operation run.
	OperationRunPhaseCompleted EventKind = "operation.phase_completed"
	// OperationRunWaiting records an operation stopping for a typed wait reason.
	OperationRunWaiting EventKind = "operation.waiting"
	// OperationRunCompleted records successful terminal completion of an
	// operation run.
	OperationRunCompleted EventKind = "operation.completed"
	// OperationRunFailed records a terminal operation error not converted into
	// a waiting state.
	OperationRunFailed EventKind = "operation.failed"
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
	OffPathQuestion EventKind = "agent.off_path.question"
	// OffPathAnswer is appended when the agent returns a reply to an
	// off-path question. Replay treats it as a no-op.
	OffPathAnswer EventKind = "agent.off_path.answer"
	// TurnEnded is appended at the end of every user turn. Payload carries
	// {"outcome", "to"} and, on a successful transition, "view": the rendered
	// operator-facing room view (the deterministic narration the operator saw
	// at the end of the turn — banner/prose/kv/headings/questions, expanded
	// from the room's view template against world state). Recording it here
	// makes the trace self-contained: the view templates can change mid-run
	// and run-to-run and are NOT pinned to a git sha, so the rendered
	// narration cannot be reconstructed after the fact from the story files —
	// it must be captured at render time. Exactly one view per turn, which is
	// why it rides turn.end rather than its own event. Omitted when empty
	// (rejected turns, background turns). Replay ignores the payload.
	//
	// The recorded view has presentation ANSI stripped (the room's lipgloss
	// banner/heading colour, which lipgloss only emits to a colour terminal)
	// so the bytes are deterministic regardless of the color profile the
	// session ran under. The zero-width source-color sentinels (which mark
	// LLM- vs template-generated spans) are NOT ANSI and are preserved, so a
	// consumer can still re-paint provenance. See orchestrator.recordedView.
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

	// MachineError is appended when machine.Turn itself fails — e.g. an
	// effect's `set:` / `when:` expression does not compile or evaluate, so
	// the turn aborts before any transition is applied. Distinct from
	// ValidationFailed (a cleanly *rejected* intent) and HarnessError (an
	// orchestrator-side dispatch-loop failure): MachineError is a turn-fatal
	// fault in the state machine itself. Without it an aborted turn leaves
	// NO row in the session trace, making a bounce-to-idle impossible to
	// diagnose from the trace alone. Payload carries
	// {"intent", "slots", "state", "error"}. Replay treats it as a no-op —
	// no world or state change occurred.
	MachineError EventKind = "machine.error"

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

	// WriteModeGranted is appended when the write-mode gate resolves a mutating
	// step in a write_mode: read_only agent room — the operator's recorded opt-in
	// (or a headless denial). Sibling of GateDecided: the deterministic engine
	// decides *which* step needs a grant (a class check over the tool call), the
	// operator makes the *grant* (this recorded interpretive decision). Payload
	// carries {"state", "action", "effect", "scope", "by", "granted"} where
	// action is the gated tool call ("Edit ./x.go" | "Bash: git push"), effect is
	// the mutating class ("write" | "external"), scope is the operator's chosen
	// breadth ("action" | "turn" | "session"), by is "operator" | "headless_denied",
	// and granted is true/false. Payload is json.RawMessage; replay treats it as a
	// no-op for world/state (the gated tool call's own effects are authoritative —
	// this event records *why* a mutation was permitted). See
	// docs/architecture/operator-ask.md and the write-mode gate in hosts.md.
	WriteModeGranted EventKind = "machine.write_mode_granted"

	// AgentCalled is appended at the moment an agent verb is dispatched.
	// Payload carries the full prompt, with-args, schema-ref, deadline,
	// call_id, and verb. Replay treats this as a no-op — state reconstruction
	// uses EffectApplied events for the submission bind. Exists for audit and
	// the runstatus SPA which pairs by call_id.
	AgentCalled EventKind = "agent.call.start"

	// AgentReturned is appended when the agent verb response lands.
	// Payload carries the full submission body, meta (tokens/cost/model —
	// opaque), duration_ms, the matching call_id, and verb. Replay no-op.
	AgentReturned EventKind = "agent.call.complete"

	// AgentError is appended instead of AgentReturned when the agent verb
	// returns an error. Payload carries the error string, call_id, verb.
	// Replay no-op.
	AgentError EventKind = "agent.call.error"

	// AgentDispatchBudgetChecked records the pre-dispatch budget-gate
	// decision for one host.agent.* call (dispatch-context-floor proposal,
	// task 1.4): {verb, estimated_tokens, budget_warn_tokens,
	// budget_refuse_tokens, decision: proceed|escalate|refuse, reason,
	// rung}. Appended BEFORE the underlying claude CLI subprocess is
	// dispatched — a "refuse" decision means no subprocess ran at all for
	// that call, and a "escalate" decision records which (if any) stronger
	// ladder rung the walk was steered to start from. Replay no-op (falls
	// through the replay switch's default case): it is a labeled
	// pre-dispatch datapoint, not a world/state mutation.
	AgentDispatchBudgetChecked EventKind = "agent.dispatch.budget_checked"

	// IDEContextCaptured records one host.ide.get_* pull whose result feeds a
	// decision. Payload carries {verb, request, response_digest, port,
	// workspace}: the IDE provenance (which workspace/port served it) plus a
	// sha256-prefix digest of the response — never the raw selection/diagnostic
	// text (selection-privacy lean). The raw request/response is already on
	// HostInvoked/HostReturned; this entry pins the editor input behind a
	// decision so it is auditable without re-opening the socket. Replay no-op.
	// Mirrors journal.KindIDEContextCaptured (same dotted string).
	IDEContextCaptured EventKind = "ide.context_captured"

	// StorySnapshot is the base snapshot of the *effective story* — every
	// file the loader touches to build the running machine (manifests +
	// views + prompts + scripts + fixtures under the story tree and any
	// imported sibling trees). It is appended exactly once per session, at
	// session start (turn 0), as the first event after the header.
	//
	// Recording the story IN the trace is what makes a trace a
	// self-contained, deterministic replay: the story files on disk can be
	// edited mid-run (/reload, /meta) and after the session ends, and are
	// not guaranteed to be pinned to a git sha — so a replay that re-reads
	// disk no longer reproduces what happened, and you cannot rewind to a
	// turn and branch onto a new path because the story effective at that
	// turn is gone. With the story embedded, replay reconstructs the
	// AppDef from the trace (materialise the files to a temp dir + app.Load).
	//
	// Payload (see storySnapshotPayload in story.go): {"app_id", "entry",
	// "hash", "files"} where `files` maps a path-relative-to-capture-root to
	// the base64 of the file's raw bytes (base64 sidesteps the JSONL
	// NFC/NUL/CRLF write constraints and is byte-faithful), `entry` is the
	// root manifest path relative to the same capture root, and `hash` is
	// the sha256 over the canonical sorted file map. Replay folds it as a
	// no-op (state/world unchanged); the story is consumed only when
	// reconstructing the machine, not when folding the journey.
	StorySnapshot EventKind = "session.story"

	// StoryChanged is a diff against the previous story state, appended
	// whenever the effective story's hash changes mid-run — i.e. after a
	// /reload or a /meta edit (both funnel through orchestrator.Reload).
	// Recording the change in the trace (rather than relying on a git sha)
	// is required because /reload picks up *uncommitted* edits a sha cannot
	// name. Reconstruction = the latest StorySnapshot then every StoryChanged
	// up to the target turn, applied in order.
	//
	// Payload (see storyChangedPayload in story.go): {"hash", "prev_hash",
	// "changed", "removed"} where `changed` maps relpath → base64 of the new
	// bytes (added or modified files) and `removed` lists deleted relpaths.
	// Replay folds it as a no-op.
	StoryChanged EventKind = "story.changed"

	// MiningProposalRaised is appended once per surfaced mining proposal — the
	// ambient miner turned a scored recipe into a concrete, staged YAML delta
	// against the running instance's regenerated inventory. The draft lives at
	// draft_path (a staging dir under .artifacts/mining/<recipe_id>/, never the
	// live tree); the operator decides its fate, recorded later as
	// MiningProposalDecided. Sibling of GateDecided in spirit: the engine does
	// the deterministic work (dedup, rung choice, staging), the operator makes
	// the interpretive accept/refine/reject.
	//
	// Payload (see MiningProposalRaisedPayload in mining_event.go):
	// {"recipe_id", "kind", "target", "priority", "rung", "draft_path"} where
	// kind ∈ binding|world|intent|stub-wire|gate|dev-story-enrich, target ∈
	// root-instance|dev-story, and rung ∈ 1|2. Replay folds it as a no-op (no
	// world/state change — the accept's own Reload emits StoryChanged for the
	// edit itself). See docs/architecture/ambient-mining.md.
	MiningProposalRaised EventKind = "mining.proposal_raised"

	// MiningProposalDecided is appended when a mining proposal is accepted,
	// refined, or rejected — the recorded verdict that is the mining moat
	// datapoint. flows_green is the no-LLM flow-gate result; reverted is true
	// when a green-gate failure rolled the applied edit back byte-for-byte.
	// A rejected proposal is equally recorded (the negative suppresses
	// re-surfacing).
	//
	// Payload (see MiningProposalDecidedPayload in mining_event.go):
	// {"recipe_id", "verdict", "by", "flows_green", "reverted"} where verdict ∈
	// accept|refine|reject and by ∈ human|llm. Replay folds it as a no-op (the
	// accept's reload, if any, is authoritative for state via StoryChanged).
	// See docs/architecture/ambient-mining.md.
	MiningProposalDecided EventKind = "mining.proposal_decided"

	// MiningPassRan is appended once per COMPLETED ambient-miner pass — the
	// ambient session miner (docs/proposals/ambient-session-miner.md) ran the
	// stateless pipeline over a transcript sample and emitted scored recipes.
	// trigger distinguishes the first-launch history seed (seed) from a debounced
	// live pass over new transcripts (live); paused records that the miner was
	// disabled (mining.enabled=false) when the pass would have fired, so the
	// trace shows the gap. It pins which pass surfaced the recipe a later
	// MiningProposalRaised was drafted from.
	//
	// Payload (see MiningPassRanPayload in mining_event.go): {"trigger", "slug",
	// "sessions", "recipes", "job_id", "paused"} where trigger ∈ seed|live.
	// Replay folds it as a no-op (annotation-only, no world/state effect — like
	// MiningProposalRaised). Additive optional payload, so older cassettes replay
	// unchanged. See docs/architecture/ambient-mining.md.
	MiningPassRan EventKind = "mining.pass_ran"

	// TurnContextRouteOverridden is recorded when the operator rewinds/switches a
	// contextual routing decision via RewindRoute. Fields: from_decision_id,
	// old_class, new_class, reason. Replay folds it as a no-op (annotation-only).
	TurnContextRouteOverridden EventKind = "turn.context_route_overridden"
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
	// CallID is the deterministic agent call identifier for AgentCalled,
	// AgentReturned, and AgentError events. Empty for all other event kinds.
	// Derived via DeriveCallID in internal/host/callid.go. The runstatus SPA
	// pairs AgentCalled with AgentReturned by this field.
	CallID string `json:"call_id,omitempty"`
	// EpisodeID is the cassette episode identifier for cassette-backed agent
	// calls. Present only on AgentCalled events emitted by the cassette
	// dispatcher. Together with MatchIdx it allows post-resume reconstruction
	// of the per-episode match counter so resume generates collision-free
	// call_ids.
	EpisodeID string `json:"episode_id,omitempty"`
	// MatchIdx is the 0-based match counter for replay:any cassette episodes.
	// For a normal (non-replay:any) episode it is always 0. Present only on
	// AgentCalled events emitted by the cassette dispatcher alongside EpisodeID.
	MatchIdx int `json:"match_idx,omitempty"`
	// SinkFlushed is a transient, in-memory-only marker (never serialized — see
	// the json:"-" tag). It is set true on an event that was already written to
	// the live EventSink BEFORE the turn-end batch flush — currently only the
	// HostDispatched event, which the orchestrator flushes to the JSONL sink
	// immediately before a (possibly long-blocking) host invoke so the trace /
	// SSE stream isn't frozen mid-call. The event still travels in the turn's
	// returned batch (so expect_host_calls assertions and the SQLite write see
	// it), but appendEventsAndJournal MUST skip re-appending a SinkFlushed event
	// to the sink, or the JSONL would carry a duplicate line. See
	// orchestrator.dispatchHostCalls and appendEventsAndJournal.
	SinkFlushed bool `json:"-"`
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
