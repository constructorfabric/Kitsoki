package orchestrator

import (
	"fmt"
	"strings"

	"kitsoki/internal/app"
	"kitsoki/internal/machine"
	"kitsoki/internal/world"
)

// Location is the computed "where am I" context (the location indicator).
type Location struct {
	// Breadcrumb is the dot-separated state path, e.g. "bar.dark".
	Breadcrumb string
	// StateDescription is pulled from the state's description: field.
	StateDescription string
	// RelevantWorld maps pinned world keys to their current values.
	RelevantWorld map[string]any
	// TurnNumber is the monotonic turn counter.
	TurnNumber app.TurnNumber
	// OnPath is false when in off-path mode.
	OnPath bool
}

// SlotRef describes a single required slot that has not been pre-filled.
// It is used in MenuEntry.MissingSlots to drive the clarification flow.
//
// Aliased to machine.MenuSlotRef so the menu type lives in the package that
// owns its computation (avoiding an import cycle when view-render needs to
// populate env.Menu) while existing orchestrator consumers (TUI) continue
// to import the orchestrator type unchanged.
type SlotRef = machine.MenuSlotRef

// MenuEntry is one concrete row in the menu. Each entry represents a
// fully qualified (intent + prefilled-slots) action that can either be
// submitted directly (MissingSlots empty) or launched into the clarification
// flow.
//
// Aliased to machine.MenuEntry — see SlotRef above for the cycle-avoidance
// rationale.
type MenuEntry = machine.MenuEntry

// Menu is the computed "where can I go" surface. Alias to
// machine.MenuView (the canonical definition lives in machine).
type Menu = machine.MenuView

// MenuItem is kept for backward-compatibility with the TUI's menuItemsChanged message.
// New code should use MenuEntry directly.
type MenuItem struct {
	machine.AllowedIntent
	// BlockedReason is the guard_hint when this intent is blocked.
	BlockedReason string
}

// Clarification is the slot-fill context for a missing-slots turn.
type Clarification struct {
	// IntentName is the intent awaiting slot completion.
	IntentName string
	// Slots is the list of missing slots that need filling.
	Slots []SlotNeed
	// UseForm is true when all missing slots are constrained (sub-mode A).
	UseForm bool
}

// ComputeLocation derives the location indicator from the current journey.
func ComputeLocation(def *app.AppDef, state app.StatePath, w world.World, turn app.TurnNumber) Location {
	loc := Location{
		Breadcrumb: string(state),
		TurnNumber: turn,
		OnPath:     true,
	}

	// Look up the state to get its description and relevant_world.
	s := lookupStateByPath(def, state)
	if s != nil {
		loc.StateDescription = s.Description

		// Build relevant world subset.
		if len(s.RelevantWorld) > 0 {
			loc.RelevantWorld = make(map[string]any, len(s.RelevantWorld))
			for _, key := range s.RelevantWorld {
				loc.RelevantWorld[key] = w.Vars[key]
			}
		}
	}

	// Fall back to full world snapshot if nothing was pinned.
	if loc.RelevantWorld == nil && len(w.Vars) > 0 {
		loc.RelevantWorld = make(map[string]any, len(w.Vars))
		for k, v := range w.Vars {
			loc.RelevantWorld[k] = v
		}
	}

	return loc
}

// ComputeMenu derives the menu from allowed intents in the current state.
//
// As of the in-view-menu-rendering refactor this is a thin wrapper over
// machine.Machine.Menu — the actual expansion logic lives in
// internal/machine so view-render call sites can compute the menu without
// an import cycle. The wrapper exists so existing orchestrator/TUI
// consumers continue to type-check unchanged.
func ComputeMenu(_ *app.AppDef, m machine.Machine, state app.StatePath, w world.World) Menu {
	return m.Menu(state, w)
}

// ComputeClarification builds the slot-fill context for a MISSING_SLOTS outcome.
func ComputeClarification(def *app.AppDef, state app.StatePath, intentName string, missingSlotNames []string) Clarification {
	clarification := Clarification{IntentName: intentName}

	// Find the intent definition.
	intentDef, ok := lookupIntentByPath(def, state, intentName)
	if !ok {
		// Fallback: return plain slot names without metadata.
		for _, name := range missingSlotNames {
			clarification.Slots = append(clarification.Slots, SlotNeed{Name: name})
		}
		return clarification
	}

	allConstrained := true
	for _, slotName := range missingSlotNames {
		slotDef, exists := intentDef.Slots[slotName]
		if !exists {
			clarification.Slots = append(clarification.Slots, SlotNeed{Name: slotName})
			allConstrained = false
			continue
		}

		need := SlotNeed{
			Name:        slotName,
			Prompt:      slotDef.Prompt,
			Description: slotDef.Description,
			Type:        slotDef.Type,
			Values:      slotDef.Values,
			FormatHint:  slotDef.FormatHint,
			Examples:    slotDef.Examples,
		}
		clarification.Slots = append(clarification.Slots, need)

		// A slot is constrained if it has an enum, bool type, or a numeric type.
		isConstrained := len(slotDef.Values) > 0 ||
			slotDef.Type == "bool" ||
			slotDef.Type == "int" ||
			slotDef.Type == "float"
		if !isConstrained {
			allConstrained = false
		}
	}

	clarification.UseForm = allConstrained && len(clarification.Slots) > 0
	return clarification
}

// FormatLocation renders the location as a compact one-line string for display.
func FormatLocation(loc Location) string {
	var parts []string
	parts = append(parts, loc.Breadcrumb)
	if loc.StateDescription != "" {
		parts = append(parts, loc.StateDescription)
	}
	if loc.TurnNumber > 0 {
		parts = append(parts, fmt.Sprintf("turn %d", loc.TurnNumber))
	}
	return strings.Join(parts, " | ")
}

// lookupStateByPath walks the nested state map using a dot-separated path.
//
// Parallel-encoded paths (the "region#leaf" form) are stripped to their structural
// parent — the parallel parent state — so callers that want terminal/mode
// checks or timeout config see a sensible result. Region-internal state
// lookup requires the orchestrator to pick a specific region leaf and call
// this with that leaf path directly.
func lookupStateByPath(def *app.AppDef, path app.StatePath) *app.State {
	p := string(path)
	if idx := strings.Index(p, "#"); idx >= 0 {
		p = p[:idx]
	}
	parts := strings.Split(p, ".")
	states := def.States
	var s *app.State
	for _, part := range parts {
		var ok bool
		s, ok = states[part]
		if !ok || s == nil {
			return nil
		}
		states = s.States
	}
	return s
}

// lookupIntentByPath finds an intent definition for the given state path,
// checking local intents first, then the global library.
//
// Parallel-encoded paths (the "region#leaf" form) are stripped to the parallel
// parent for state-local intent lookup; per-region scoped intents need
// the orchestrator to pass the region leaf path directly.
func lookupIntentByPath(def *app.AppDef, state app.StatePath, intentName string) (app.Intent, bool) {
	// Check state-local intents along the path.
	path := string(state)
	if idx := strings.Index(path, "#"); idx >= 0 {
		path = path[:idx]
	}
	states := def.States
	var s *app.State
	for _, part := range strings.Split(path, ".") {
		s = states[part]
		if s == nil {
			break
		}
		if i, ok := s.Intents[intentName]; ok {
			return i, true
		}
		states = s.States
	}
	// Fall back to global intent library.
	if i, ok := def.Intents[intentName]; ok {
		return i, true
	}
	return app.Intent{}, false
}
