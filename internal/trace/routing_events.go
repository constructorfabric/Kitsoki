// Package trace — additional routing-tier trace event constants
// (see docs/architecture/semantic-routing.md). The core semroute / LLM
// events live in trace.go; this file fills in the turncache, off-path,
// and TUI-cancel events that the TUI route-badge sub-model also
// subscribes to.
//
// Naming follows the existing dotted-string taxonomy in trace.go.
// Event-field expectations (slog attrs) are documented inline.
package trace

const (
	// EvTurnTurncacheHit fires when the per-(app, state, signature)
	// cache short-circuits the resolution (the turn-cache tier; see
	// docs/architecture/semantic-routing.md). Expected
	// fields (slog attrs):
	//
	//   intent      string  — cached canonical intent
	//   confidence  float64 — the originating verdict's confidence
	//   hits        int     — running hit-count on this row
	//   age         string  — Duration since the row was first written
	//   state_path  string
	//
	// The TUI renders this as the `⟲` (yellow) tier.
	EvTurnTurncacheHit = "turn.turncache_hit"

	// EvTurnOffpathRouted fires when the resolver classifies the turn
	// as off-path / agent rather than a state-machine transition.
	// Fields: state_path, reason. Chip icon `◇` (grey).
	EvTurnOffpathRouted = "turn.offpath_routed"

	// EvTurnCancelled fires when the user presses ESC while a turn is
	// in flight. Fields: state_path, tier (the in-flight tier
	// name at cancel time). Chip resolves to `[✕ cancelled]`.
	EvTurnCancelled = "turn.cancelled"

	// EvTurnContextRouteDecided fires when the contextual router commits to
	// a class+verdict after calling the host helper. Expected fields:
	//
	//   class       string  — one of intent|help|room_request|meta_edit
	//   intent      string  — resolved intent name (class=intent only)
	//   confidence  float64
	//   reason      string
	//
	// This event is the replay anchor for 1.4: a recorded verdict + this
	// event prove the turn was contextually routed without a live LLM.
	EvTurnContextRouteDecided = "turn.context_route_decided"

	// EvTurnContextRouteApplied fires after the lane dispatch succeeds for a
	// help/room_request/meta_edit verdict (slice 2). Expected fields:
	//
	//   lane     string  — the LaneKind string (help|work|meta)
	//   chat_id  string  — the resolved lane chat id
	EvTurnContextRouteApplied = "turn.context_route_applied"

	// EvTurnContextRouteOverridden fires when the operator rewinds/switches a
	// contextual route via RewindRoute. Expected fields:
	//
	//   from_decision_id  string  — the decision id that was overridden
	//   old_class         string  — the original routing class
	//   new_class         string  — the replacement routing class
	//   reason            string  — operator-supplied reason for the override
	EvTurnContextRouteOverridden = "turn.context_route_overridden"

	// EvTurnRoutingFeedback fires when an operator records an up/down verdict
	// on a routed turn's outcome (WS-C C4: routing-dissatisfaction substrate;
	// see docs/testing/routing-tuning.md). Mirrors the standalone
	// journal.KindRoutingFeedback write. Fields: phrase, state, intent, tier,
	// verdict ("up"|"down"). Emitted by
	// Orchestrator.RecordRoutingFeedback — never part of a machine
	// transition, so it carries no world/state effect.
	EvTurnRoutingFeedback = "turn.routing_feedback"
)
