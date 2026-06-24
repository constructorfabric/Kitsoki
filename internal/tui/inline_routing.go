package tui

import (
	"encoding/json"

	"kitsoki/internal/store"
	"kitsoki/internal/tui/blocks"
)

// inline_routing.go — helpers for the inline routing-status strings shown under
// each user-turn echo. The live, multi-layer routing PIPELINE lives in
// routing_pipeline.go; this file keeps the thin Renderer wrapper used for
// one-off settled lines (slash commands, off-path) plus the event readers the
// pipeline's completion path uses to recover routing provenance and the
// resolved intent from a TurnOutcome.

// inlineRouter is a thin Renderer wrapper bound to the transcript's current
// width — rebuilt per call site to pick up width changes.
type inlineRouter struct {
	r *blocks.Renderer
}

func (m RootModel) newInlineRouter() inlineRouter {
	w := m.transcript.width
	if w <= 0 {
		w = m.width
	}
	if w <= 0 {
		w = 80
	}
	return inlineRouter{r: blocks.New(w, m.currentTheme())}
}

// settledLine builds a one-off resolved line for paths that bypass the routing
// pipeline (slash commands, off-path). kind is one of nav | view | system |
// in-room | off-path; source is the tier; confidence is only used for LLM.
func (ir inlineRouter) settledLine(kind, intent string, source blocks.RoutingSource, confidence float64, detail string) string {
	return ir.r.RoutingResolved(blocks.Resolved{
		Kind:       kind,
		Intent:     intent,
		Source:     source,
		Confidence: confidence,
		Detail:     detail,
	})
}

// ideSelectionEcho renders the one-per-turn ambient editor-context echo
// (`⧉ Selected N lines from <file>`) as a clean settled system line. It uses
// the same Renderer the routing settled lines do, but the SystemNotice style
// (muted, no routing decoration) so the line reads exactly as the operator
// expects — the echo is the source of truth for what selection rode the turn
// (slice 2). Kept beside settledLine because it is the same "settled line for a
// path that bypasses the routing pipeline" idiom.
func (ir inlineRouter) ideSelectionEcho(text string) string {
	return ir.r.SystemNotice(text)
}

// provenanceFromEvents reads the RouteProvenance stamped on the TurnStarted
// event: routed_by (the tier), match_type (tier detail — for the LLM tier the
// backend plugin name), and confidence. Zero values when none was recorded
// (e.g. a main-turn LLM route). The routing pipeline uses this at turn
// completion to attribute the win to the right layer.
func provenanceFromEvents(events []store.Event) (routedBy, matchType string, confidence float64) {
	for _, ev := range events {
		if ev.Kind != store.TurnStarted {
			continue
		}
		var p struct {
			RoutedBy   string  `json:"routed_by"`
			MatchType  string  `json:"match_type"`
			Confidence float64 `json:"confidence"`
		}
		if err := json.Unmarshal(ev.Payload, &p); err == nil {
			return p.RoutedBy, p.MatchType, p.Confidence
		}
	}
	return "", "", 0
}

// intentFromEvents returns the resolved intent for a turn: IntentAccepted first,
// then the machine.transition event. "" when neither carried an intent.
func intentFromEvents(events []store.Event) string {
	for _, want := range []store.EventKind{store.IntentAccepted, store.TransitionApplied} {
		for _, ev := range events {
			if ev.Kind != want {
				continue
			}
			var p struct {
				Intent string `json:"intent"`
			}
			if err := json.Unmarshal(ev.Payload, &p); err != nil {
				continue
			}
			if p.Intent != "" {
				return p.Intent
			}
		}
	}
	return ""
}
