// context_route.go — verdict type and parser for the contextual-routing tier.
//
// The contextual router is a final-tier router that fires AFTER deterministic
// and embedding tiers miss, on rooms that have declared contextual_routing.enabled.
// Unlike the LLM routing tier (which returns a flat {intent,confidence} verdict),
// the contextual router returns a richer verdict with a class discriminant so it
// can route to intent/help/room/meta lanes.
//
// Slice 1 covers 1.1 (type + parser) and 1.2 (intent class wiring).
// Group-2 lanes (help/room_request/meta_edit) are wired as stubs here.
package orchestrator

import "fmt"

// ContextRouteClass is the discriminant of a contextual-router verdict.
// Exactly four classes are recognised; any other value is rejected by ParseContextRouteVerdict.
type ContextRouteClass string

const (
	// ClassIntent means the user's input matched an on-path intent; the state machine advances.
	ClassIntent ContextRouteClass = "intent"
	// ClassHelp means the user asked for help or guidance (group-2 lane; stub in slice 1).
	ClassHelp ContextRouteClass = "help"
	// ClassRoomRequest means the user asked to navigate to a different room (group-2 lane; stub in slice 1).
	ClassRoomRequest ContextRouteClass = "room_request"
	// ClassMetaEdit means the user asked to edit app configuration (group-2 lane; stub in slice 1).
	ClassMetaEdit ContextRouteClass = "meta_edit"
)

// validContextRouteClasses is the closed set of accepted class values.
var validContextRouteClasses = map[ContextRouteClass]struct{}{
	ClassIntent:      {},
	ClassHelp:        {},
	ClassRoomRequest: {},
	ClassMetaEdit:    {},
}

// ContextRouteAlt is a lower-confidence alternative carried alongside the primary verdict.
type ContextRouteAlt struct {
	Class      ContextRouteClass `json:"class"`
	Intent     string            `json:"intent,omitempty"`
	Confidence float64           `json:"confidence"`
}

// ContextRouteVerdict is the structured output of the contextual router.
// It mirrors semroute.Verdict in style but carries a Class discriminant so the
// orchestrator can dispatch to the correct lane without re-inspecting the raw map.
type ContextRouteVerdict struct {
	Class        ContextRouteClass `json:"class"`
	Intent       string            `json:"intent,omitempty"`
	Slots        map[string]any    `json:"slots,omitempty"`
	ChatID       string            `json:"chat_id,omitempty"`
	Confidence   float64           `json:"confidence"`
	Reason       string            `json:"reason,omitempty"`
	Alternatives []ContextRouteAlt `json:"alternatives,omitempty"`
}

// ContextRouteReceipt is the compact, queryable record of one contextual
// routing decision, surfaced to TUI/web and persisted on the turn outcome.
// DecisionID is "<session_id>:<turn_number>" and is used as a stable rewind target.
type ContextRouteReceipt struct {
	Class        string            `json:"class"`
	Intent       string            `json:"intent,omitempty"`
	Reason       string            `json:"reason,omitempty"`
	Confidence   float64           `json:"confidence"`
	TargetChatID string            `json:"target_chat_id,omitempty"`
	TargetLane   string            `json:"target_lane,omitempty"`
	Alternatives []ContextRouteAlt `json:"alternatives,omitempty"`
	DecisionID   string            `json:"decision_id"`
}

// ParseContextRouteVerdict parses a raw submission map (as decoded from JSON)
// into a ContextRouteVerdict. It rejects any class value outside the four
// recognised classes (intent | help | room_request | meta_edit) — including an
// empty string — with a descriptive error. Confidence is clamped to [0, 1].
func ParseContextRouteVerdict(raw map[string]any) (ContextRouteVerdict, error) {
	classStr, _ := raw["class"].(string)
	cls := ContextRouteClass(classStr)
	if _, ok := validContextRouteClasses[cls]; !ok {
		return ContextRouteVerdict{}, fmt.Errorf(
			"contextual router: unknown class %q (must be intent|help|room_request|meta_edit)",
			classStr,
		)
	}

	v := ContextRouteVerdict{Class: cls}
	v.Intent, _ = raw["intent"].(string)
	v.ChatID, _ = raw["chat_id"].(string)
	v.Reason, _ = raw["reason"].(string)
	v.Confidence, _ = raw["confidence"].(float64)
	if v.Confidence < 0 {
		v.Confidence = 0
	}
	if v.Confidence > 1 {
		v.Confidence = 1
	}
	if slots, ok := raw["slots"].(map[string]any); ok {
		v.Slots = slots
	}
	if alts, ok := raw["alternatives"].([]any); ok {
		for _, a := range alts {
			m, ok := a.(map[string]any)
			if !ok {
				continue
			}
			alt := ContextRouteAlt{}
			alt.Class = ContextRouteClass(func() string { s, _ := m["class"].(string); return s }())
			alt.Intent, _ = m["intent"].(string)
			alt.Confidence, _ = m["confidence"].(float64)
			v.Alternatives = append(v.Alternatives, alt)
		}
	}
	return v, nil
}
