// Package tui — routing tier event types.
//
// These are the contract between the slog→bubbletea routing observer
// (internal/tui/routing_observer.go) and the inline routing-status
// block in the transcript (internal/tui/inline_routing.go). The
// observer translates orchestrator slog events into RoutingTier*Msg
// deliveries; the RootModel handles them by updating the live
// transcript entry.
//
// The legacy `RoutingChip` Bubble Tea sub-model that previously
// consumed these messages has been deleted (single-pane-tui §"Phase 7"
// cleanup) — the chip's in-flight rendering moved into the transcript
// as an inline live entry. The tier enum and message shapes survive
// because they're the wire format both the observer and the
// new inline-routing code rely on.
package tui

import (
	"os"
	"time"
)

// RoutingTier identifies one of the routing tiers. Order matters: a
// tier with a higher numeric value is "later" in the resolver and
// implies every earlier tier missed.
type RoutingTier int

const (
	// TierNone is the zero state — nothing has resolved or missed yet.
	TierNone RoutingTier = iota
	// TierDeterministic — matched the menu / synonym pre-pass.
	TierDeterministic
	// TierSemantic — matched the bare-synonym tier.
	TierSemantic
	// TierTemplate — matched a synonym template with captured slots.
	TierTemplate
	// TierTurncache — served from the per-(app,state,signature) cache.
	TierTurncache
	// TierLLM — resolved by the LLM harness.
	TierLLM
	// TierOffpath — routed to the off-path side-channel.
	TierOffpath
	// TierCancelled — the user pressed ESC mid-flight.
	TierCancelled
	// TierAmbiguous — the semantic matcher returned ≥2 candidates in
	// the tie band; surfaced as an inline disambig prompt.
	TierAmbiguous
)

// RoutingTierMissMsg advances the inline routing-status block past a
// tier without resolving. Sent on turn.deterministic_miss /
// turn.semantic_miss / equivalent slog events.
type RoutingTierMissMsg struct {
	Tier RoutingTier
}

// RoutingTierHitMsg finalises the inline routing block at a tier with
// the resolved intent and detail.
type RoutingTierHitMsg struct {
	Tier       RoutingTier
	Intent     string
	Slots      map[string]any
	Confidence float64
	// Reason carries the originating tier detail. Convention:
	//   synonym:wade      — bare synonym match
	//   template:0        — template index N
	//   cache             — turncache hit
	//   claude-haiku      — model name
	Reason string
	// Hits is the cache hit count, when Tier==TierTurncache.
	Hits int
	// Latency is the resolver wall-time, when Tier==TierLLM.
	Latency time.Duration
}

// RoutingAmbiguousMsg signals a ≥2-way tie. Candidates is the
// canonical intent name list. The inline block renders a "need
// clarification" line and the disambig flow takes over.
type RoutingAmbiguousMsg struct {
	Candidates []string
}

// RoutingCancelMsg drops the live routing block when the user
// cancels mid-flight.
type RoutingCancelMsg struct{}

// noColourEnabled returns true when NO_COLOR or KITSOKI_NO_COLOR is
// set to a non-empty value in the environment. Preserved here because
// a few legacy call sites still consult it; new code should use
// blocks.Renderer.NoColor instead.
func noColourEnabled() bool {
	if v := os.Getenv("NO_COLOR"); v != "" && v != "0" {
		return true
	}
	if v := os.Getenv("KITSOKI_NO_COLOR"); v != "" && v != "0" {
		return true
	}
	return false
}
