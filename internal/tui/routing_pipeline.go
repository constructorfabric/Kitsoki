package tui

// routing_pipeline.go — the live, multi-layer routing indicator. As a turn
// routes, the orchestrator emits per-tier miss/hit slog events that the
// RoutingObserver turns into RoutingTier{Miss,Hit}Msg; this model accumulates
// them into a pipeline the transcript renders in place:
//
//	in flight:  routing   ✗ deterministic   ✗ semantic   ▶ LLM
//	resolved:   ✓ routed via LLM · agent.local → read_email  (0.91)    [deterministic ✗  semantic ✗  LLM ✓]
//
// Three layers mirror the router's tiers (deterministic → semantic → LLM); the
// LLM layer's detail names the backend that actually answered (agent.local vs
// the cloud model), which is what tells the operator which model was used.
//
// To avoid double-settling, the observer messages only UPDATE the live line;
// the single FinalizeLive happens at turn completion (handleTurnOutcome), which
// also fills the winner from the authoritative TurnStarted provenance if no hit
// event arrived in time.

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type routeLayerState uint8

const (
	rlPending routeLayerState = iota // not reached yet
	rlActive                         // currently being tried
	rlMissed                         // tried, no confident match — passed through
	rlHit                            // the winning layer
)

const (
	glyphPending = "·"
	glyphActive  = "▶"
	glyphMissed  = "✗"
	glyphHit     = "✓"
)

type routeLayer struct {
	key   string // "deterministic" | "semantic" | "llm"
	label string
	state routeLayerState
}

func (l routeLayer) glyph() string {
	switch l.state {
	case rlActive:
		return glyphActive
	case rlMissed:
		return glyphMissed
	case rlHit:
		return glyphHit
	default:
		return glyphPending
	}
}

type routingPipeline struct {
	layers     []routeLayer
	winner     int // index of the hit layer; -1 until resolved
	intent     string
	detail     string // backend / reason on the winning layer (e.g. "agent.local")
	confidence float64
	showConf   bool
}

// newRoutingPipeline starts a fresh pipeline with the deterministic layer
// active and the rest pending. There are TWO LLM layers because the router
// tries a cheap local model (agent.local) on a no_match BEFORE falling through
// to the cloud main-turn model — and which one answered is the whole point.
func newRoutingPipeline() routingPipeline {
	return routingPipeline{
		layers: []routeLayer{
			{key: "deterministic", label: "deterministic", state: rlActive},
			{key: "semantic", label: "semantic", state: rlPending},
			{key: "local-llm", label: "local-LLM", state: rlPending},
			{key: "main-llm", label: "main-LLM", state: rlPending},
		},
		winner: -1,
	}
}

// layerKeyFor maps a RoutingTier (and, for the LLM tier, its backend detail)
// onto one of the pipeline layers. The LLM tier splits by backend: an
// agent-plugin backend (e.g. agent.local) is the local-LLM layer; anything
// else (a cloud model name like claude-…) is the main-LLM layer.
func layerKeyFor(t RoutingTier, detail string) string {
	switch t {
	case TierDeterministic:
		return "deterministic"
	case TierSemantic, TierTemplate, TierTurncache:
		return "semantic"
	default: // TierLLM, TierOffpath, unknown
		if strings.HasPrefix(detail, "agent.") {
			return "local-llm"
		}
		return "main-llm"
	}
}

func (p *routingPipeline) indexOf(key string) int {
	for i := range p.layers {
		if p.layers[i].key == key {
			return i
		}
	}
	return -1
}

// markMiss records that a tier was tried with no confident match and advances
// the active marker to the next pending layer.
func (p *routingPipeline) markMiss(t RoutingTier, detail string) {
	i := p.indexOf(layerKeyFor(t, detail))
	if i < 0 || p.layers[i].state == rlHit {
		return
	}
	p.layers[i].state = rlMissed
	for j := i + 1; j < len(p.layers); j++ {
		if p.layers[j].state == rlPending {
			p.layers[j].state = rlActive
			break
		}
	}
}

// markHit records the winning layer (and the backend detail / intent /
// confidence), marking everything before it as passed-through.
func (p *routingPipeline) markHit(t RoutingTier, intent, detail string, confidence float64, showConf bool) {
	i := p.indexOf(layerKeyFor(t, detail))
	if i < 0 {
		return
	}
	for j := 0; j < i; j++ {
		if p.layers[j].state != rlHit {
			p.layers[j].state = rlMissed
		}
	}
	p.layers[i].state = rlHit
	for j := i + 1; j < len(p.layers); j++ {
		p.layers[j].state = rlPending
	}
	p.winner = i
	p.intent = intent
	p.detail = detail
	p.confidence = confidence
	p.showConf = showConf
}

// resolved reports whether a winning layer has been recorded. The zero-value
// pipeline (winner==0 but no layers) is NOT resolved — guard on layers too.
func (p routingPipeline) resolved() bool { return len(p.layers) > 0 && p.winner >= 0 }

// winnerTier returns the winning layer's key ("deterministic" | "semantic" |
// "local-llm" | "main-llm"), or "" when unresolved. Used to snapshot the
// route's tier for the `/route` feedback command (WS-C C4) once the pipeline
// is about to be discarded/reset.
func (p routingPipeline) winnerTier() string {
	if !p.resolved() || p.winner >= len(p.layers) {
		return ""
	}
	return p.layers[p.winner].key
}

// hitDetailFor extracts the backend/reason to show on a winning layer from a
// hit message: the LLM layer shows the model/backend (e.g. agent.local), the
// cache layer shows "cache", deterministic/semantic show nothing.
func hitDetailFor(tm RoutingTierHitMsg) string {
	switch tm.Tier {
	case TierLLM:
		return tm.Reason // the model / backend plugin name
	case TierTurncache:
		return "cache"
	default:
		return ""
	}
}

// resolveFromProvenance marks the winning layer from the authoritative
// TurnStarted provenance (routed_by / match_type / confidence) plus the routed
// intent. Used at turn completion when no hit event arrived live. An empty
// routed_by means the turn fell through to the main-turn LLM (claude).
func (p *routingPipeline) resolveFromProvenance(routedBy, matchType string, confidence float64, intent string) {
	tier := TierLLM
	detail := ""
	switch routedBy {
	case "deterministic":
		tier = TierDeterministic
	case "semantic":
		tier = TierSemantic
	case "turncache":
		tier = TierTurncache
		detail = "cache"
	case "slot-fill":
		// Slot-fill continuations are deterministic — no LLM routing involved.
		tier = TierDeterministic
		detail = "slot-fill"
	case "llm":
		tier = TierLLM
		detail = matchType // the local-model backend, e.g. agent.local
	default:
		// No provenance stamped → the main-turn LLM (claude) handled it.
		tier = TierLLM
		detail = "claude"
	}
	p.markHit(tier, intent, detail, confidence, confidence > 0)
}

// renderProgress renders the in-flight pipeline line (one glyph per layer).
func (p routingPipeline) renderProgress() string {
	parts := make([]string, len(p.layers))
	for i, l := range p.layers {
		parts[i] = l.glyph() + " " + l.label
	}
	return "  routing   " + strings.Join(parts, "   ")
}

// renderResolved renders the final settled line: which layer won, the backend,
// the routed intent, and a compact trail of the whole pipeline.
func (p routingPipeline) renderResolved() string {
	if p.winner < 0 || p.winner >= len(p.layers) {
		return p.renderProgress()
	}
	w := p.layers[p.winner]
	via := w.label
	if p.detail != "" {
		via += " · " + p.detail
	}
	line := "  " + glyphHit + " routed via " + via
	if p.intent != "" {
		intentLabel := p.intent
		if strings.Contains(intentLabel, "__") {
			intentLabel = humanIntentLabel(intentLabel)
		}
		line += " → " + intentLabel
	}
	if p.showConf {
		line += fmt.Sprintf("  (%.2f)", p.confidence)
	}
	trail := make([]string, len(p.layers))
	for i, l := range p.layers {
		trail[i] = l.label + " " + l.glyph()
	}
	line += "    [" + strings.Join(trail, "  ") + "]"
	// The settled routing row is rendered green (colorAccent = the codebase's
	// success/green) so a resolved turn reads at a glance. In a non-colour
	// terminal (tests, pipes) Render is a no-op, so substring assertions hold.
	return lipgloss.NewStyle().Foreground(colorAccent).Render(line)
}
