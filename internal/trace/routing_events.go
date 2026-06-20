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
)
