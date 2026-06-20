package store

// observation.go defines the ObservationKind taxonomy — a closed, semantic set
// of categories derived from EventKind. It is the single source of truth for
// "what kind of thing is this event" and is consumed by the runstatus SPA and
// any other display layer.
//
// The mapping is a pure total function: same input, same output, no I/O. It
// adds no field to the on-disk event format (the category is computed at read
// time). The companion test in observation_test.go guards against drift when
// new EventKind constants are added.

// Kind is the observation-kind taxonomy — the closed set of semantic categories
// that every EventKind maps to.
type Kind string

const (
	// KindDecision covers interpretive choices with available/chosen/confidence.
	// These are the primary "moat" events: gate resolutions and off-path Q&A.
	KindDecision Kind = "decision"

	// KindRouting covers events that advance the turn or record how it was routed.
	KindRouting Kind = "routing"

	// KindAgentCall covers LLM/operator calls with prompt, response, cost and latency.
	KindAgentCall Kind = "agent-call"

	// KindHostCall covers deterministic side-effecting host execution.
	KindHostCall Kind = "host-call"

	// KindNarration covers operator-facing text: room narration and rendered views.
	KindNarration Kind = "narration"

	// KindWorldMutation covers set: world writes.
	KindWorldMutation Kind = "world-mutation"

	// KindLifecycle covers structural and bookkeeping events that do not fit
	// the above categories.
	KindLifecycle Kind = "lifecycle"
)

// ObservationKind returns the semantic ObservationKind for a given EventKind.
// The function is a pure total function: every declared EventKind maps to
// exactly one non-empty Kind, and any unrecognised string falls back to
// KindLifecycle. It never returns an empty string.
func ObservationKind(kind EventKind) Kind {
	switch kind {
	// decision — interpretive choices with available/chosen/confidence
	case GateDecided, WriteModeGranted, OffPathQuestion, OffPathAnswer:
		return KindDecision

	// routing — what advanced the turn and how it was routed
	case TurnStarted, IntentAccepted:
		return KindRouting

	// agent-call — LLM/operator calls with prompt/response/cost/latency
	case AgentCalled, AgentReturned, AgentError, LLMToolCall:
		return KindAgentCall

	// host-call — deterministic side-effecting execution
	case HostInvoked, HostDispatched, HostReturned, HarnessError:
		return KindHostCall

	// narration — operator-facing text
	case MachineSay, TurnEnded:
		return KindNarration

	// world-mutation — set: world writes
	case EffectApplied:
		return KindWorldMutation

	// lifecycle — structural/bookkeeping
	case ValidationFailed, TransitionApplied, StateExited, StateEntered,
		GuardRejected, JobSubmitted, JobCompleted, TimeoutFired, MachineError,
		OffPathEntered, OffPathExited, IDEContextCaptured,
		StorySnapshot, StoryChanged, UserInputReceived:
		return KindLifecycle

	default:
		// Fallback: unknown kinds are lifecycle. The test in observation_test.go
		// asserts every declared constant is explicitly handled, so this branch
		// fires only for EventKind values not yet declared.
		return KindLifecycle
	}
}
