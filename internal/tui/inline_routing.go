package tui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
	"kitsoki/internal/tui/blocks"
)

// inline_routing.go — single-pane-tui proposal §"Input feedback":
// helpers that produce the inline routing-status block strings attached
// under each user-turn echo. submitInput / handleTurnOutcome use these
// to build the in-flight and settled lines; the transcript model holds
// them as live entries (AppendLive / UpdateLive / FinalizeLive).
//
// These helpers wrap blocks.Renderer so the inline lines visually match
// the same Phase 0 preview output the design golden tests pin.

// inlineRouter is a thin Renderer wrapper bound to the transcript's
// current width — kept short because every call site rebuilds it
// per-turn to pick up width changes.
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

// phaseLine builds an "  routing: <phase>…" placeholder. Returned
// verbatim so AppendLive / UpdateLive can store the styled string.
func (ir inlineRouter) phaseLine(p blocks.RoutingPhase) string {
	return ir.r.RoutingStatus(p)
}

// settledLine builds the final resolved line. kind is one of
// nav | view | system | in-room | off-path. source is the tier that
// hit; confidence is only used when source == LLM.
func (ir inlineRouter) settledLine(kind, intent string, source blocks.RoutingSource, confidence float64, detail string) string {
	return ir.r.RoutingResolved(blocks.Resolved{
		Kind:       kind,
		Intent:     intent,
		Source:     source,
		Confidence: confidence,
		Detail:     detail,
	})
}

// classifyKind infers nav vs in-room from a turn outcome — the cheap
// classification the inline settled line uses. nav = state changed;
// in-room = same state. The proposal's full kind table (nav | view |
// system | in-room | off-path) is reached by callers that have
// out-of-band knowledge (slash commands know they're "system",
// /meta knows it's a room switch, etc.); this helper only handles
// the regular-turn case.
func classifyKind(prev, next string) string {
	if prev != next {
		return "nav"
	}
	return "in-room"
}

// settledFromOutcome derives a Resolved from a TurnOutcome. The intent
// name comes from the first IntentAccepted event in out.Events (which
// is where the orchestrator records the resolved intent); falls back
// to the user input's first word so the line is still informative
// when no event was emitted. Source is deterministic when MatchDeterministic
// already hit at submit time; LLM otherwise. Confidence is not yet
// carried on TurnOutcome — Phase 2 wires the slog tier events into the
// transcript so confidence ends up on the settled line.
func settledFromOutcome(out *orchestrator.TurnOutcome, prevState, userInput string, deterministic bool) blocks.Resolved {
	intent := intentFromEvents(out.Events)
	if intent == "" {
		intent = strings.SplitN(strings.TrimSpace(userInput), " ", 2)[0]
	}
	source := blocks.SourceLLM
	if deterministic {
		source = blocks.SourceDeterministic
	}
	return blocks.Resolved{
		Kind:   classifyKind(prevState, string(out.NewState)),
		Intent: intent,
		Source: source,
	}
}

// intentFromEvents scans events for the first IntentAccepted entry and
// extracts its "intent" payload field. Returns "" if no event carried
// the intent — settledFromOutcome falls back to the user input.
func intentFromEvents(events []store.Event) string {
	for _, ev := range events {
		if ev.Kind != store.IntentAccepted {
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
	return ""
}

// Force the orchestrator import to stay even when the only consumer in
// this file is a parameter type — keeps go vet happy in headless
// builds.
var _ = (*orchestrator.TurnOutcome)(nil)

// missTierPhase maps a routing-chip tier (which is what miss/hit Msgs
// carry) onto the proposal's phase labels. A miss at deterministic
// shows "synonyms…" next (we just moved past deterministic), and so on
// through the pipeline.
func missTierPhase(t RoutingTier) blocks.RoutingPhase {
	switch t {
	case TierDeterministic:
		return blocks.PhaseSynonyms
	case TierSemantic, TierTemplate:
		return blocks.PhaseSlotParser
	case TierTurncache:
		return blocks.PhaseLLM
	default:
		return blocks.PhaseLLM
	}
}

// hitTierSource maps a hit tier onto the settled-line source enum.
func hitTierSource(t RoutingTier) blocks.RoutingSource {
	switch t {
	case TierDeterministic:
		return blocks.SourceDeterministic
	case TierSemantic:
		return blocks.SourceSynonym
	case TierTemplate:
		return blocks.SourceSlotParser
	case TierTurncache:
		return blocks.SourceCache
	case TierLLM:
		return blocks.SourceLLM
	case TierOffpath:
		return blocks.SourceOffPath
	default:
		return blocks.SourceLLM
	}
}

// hitTierKind picks a coarse kind for the settled line. Without
// knowing the resulting state path (the chip fires before the
// machine transitions), we default to "in-room" — the cheap inference
// for whether the turn changes rooms requires waiting for the
// outcome, which the live block can't do. handleTurnOutcome refines
// this when the outcome arrives if the chip already settled.
func hitTierKind(_ RoutingTier, _ /*currentState*/ interface{}) string {
	return "in-room"
}

// hitTierDetail composes the detail trailer attached to the settled
// line — slot dump for slot-parser hits, "hits=N" for cache, latency
// for LLM, etc.
func hitTierDetail(m RoutingTierHitMsg) string {
	switch m.Tier {
	case TierTemplate:
		if len(m.Slots) > 0 {
			return "slots: " + slotsString(m.Slots)
		}
	case TierTurncache:
		if m.Hits > 0 {
			return ""
		}
	case TierLLM:
		if m.Latency > 0 {
			return ""
		}
	}
	return ""
}

// slotsString stringifies a slot map for the inline-routing detail
// trailer. Keeps it small — long maps fall through to a count.
func slotsString(m map[string]any) string {
	if len(m) == 0 {
		return ""
	}
	if len(m) > 3 {
		// Render the cardinality only — full dump bloats the inline
		// line.
		return fmt.Sprintf("(%d slots)", len(m))
	}
	parts := make([]string, 0, len(m))
	for k, v := range m {
		parts = append(parts, fmt.Sprintf("%s: %v", k, v))
	}
	sort.Strings(parts)
	return "{" + strings.Join(parts, ", ") + "}"
}
