package orchestrator

import (
	"fmt"
	"sort"
	"strings"

	"kitsoki/internal/app"
	"kitsoki/internal/machine"
	"kitsoki/internal/world"
)

// Location is the computed "where am I" context for §7.1.
type Location struct {
	// Breadcrumb is the dot-separated state path, e.g. "bar.dark".
	Breadcrumb string
	// StateDescription is pulled from the state's description: field.
	StateDescription string
	// RelevantWorld maps pinned world keys to their current values.
	RelevantWorld map[string]any
	// TurnNumber is the monotonic turn counter.
	TurnNumber app.TurnNumber
	// OnPath is false when in off-path mode (§7.7).
	OnPath bool
}

// SlotRef describes a single required slot that has not been pre-filled.
// It is used in MenuEntry.MissingSlots to drive the clarification flow.
type SlotRef struct {
	// Name is the slot name.
	Name string
	// Type is the slot type ("string", "enum", "bool", "int", "float").
	Type string
	// Values lists the enum values (non-empty only for type=="enum").
	Values []string
	// Description is the author-provided slot description.
	Description string
	// Prompt is the author-provided prompt string.
	Prompt string
}

// MenuEntry is one concrete row in the §7.2 menu. Unlike the old MenuItem
// which represented a bare intent name, each MenuEntry represents a fully
// qualified (intent + prefilled-slots) action that can either be submitted
// directly (MissingSlots empty) or launched into the clarification flow.
//
// Design rationale:
//   - Enum slots are expanded: "go north", "go south", ... become separate rows.
//   - Free-form required slots remain as placeholders in Display; the row still
//     appears but requires clarification before dispatch.
//   - Optional slots are NOT shown as placeholders: they are discoverable via
//     the clarification flow. This keeps the menu compact for the common case
//     where the author has declared a few required params and many optional ones.
type MenuEntry struct {
	// Intent is the intent name.
	Intent string
	// PrefilledSlots are the slots that have been pre-resolved for this row
	// (e.g. {"direction": "south"} for a "go south" row).
	PrefilledSlots map[string]any
	// MissingSlots are required slots that are not yet known. If non-empty,
	// selecting this row enters the clarification flow.
	MissingSlots []SlotRef
	// Display is the human-readable label for this row (e.g. "go south",
	// "ask <person:string> about <topic:string>").
	Display string
	// Reason is the guard_hint or description shown when Primary is false.
	Reason string
	// Primary is true when the guard dry-run passed (or was unresolved).
	Primary bool
	// DestinationHint is the resolved target state path (e.g. "bar") when
	// Primary is true and a guard matched.
	DestinationHint string
}

// Menu is the computed "where can I go" surface for §7.2.
type Menu struct {
	// Primary is the sorted list of available actions (guards pass or unresolved).
	Primary []MenuEntry
	// Blocked is the list of actions whose guards currently fail.
	Blocked []MenuEntry
}

// MenuItem is kept for backward-compatibility with the TUI's menuItemsChanged message.
// New code should use MenuEntry directly.
type MenuItem struct {
	machine.AllowedIntent
	// BlockedReason is the guard_hint when this intent is blocked.
	BlockedReason string
}

// Clarification is the slot-fill context for §7.3.
type Clarification struct {
	// IntentName is the intent awaiting slot completion.
	IntentName string
	// Slots is the list of missing slots that need filling.
	Slots []SlotNeed
	// UseForm is true when all missing slots are constrained (sub-mode A).
	UseForm bool
}

// ComputeLocation derives the §7.1 location indicator from the current journey.
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

// ComputeMenu derives the §7.2 menu from allowed intents in the current state.
// It expands enum slots into concrete rows and dry-runs guards per enum value
// to classify each row as primary or blocked.
//
// Expansion rules (§spec):
//  1. No required slots → one row, no guard dry-run needed (always primary).
//  2. Exactly one required slot, with enum → one row per enum value; guard dry-run
//     with synthetic slots map to determine primary vs blocked.
//  3. Multiple required slots, first enum slot in declaration order → one row per
//     enum value with other required slots shown as <name:type> placeholders.
//     Guards are dry-run with {enumSlot: value} only; guards referencing other
//     (unprefilled) slots will be "unresolved" — treated as primary. This is
//     intentional: the guard is re-evaluated at submission time.
//  4. Required slots but no enums → one row with all required slots as placeholders;
//     always primary (we have no values to dry-run).
//  5. Optional slots are NOT shown in placeholder lines (keeps the menu compact).
func ComputeMenu(def *app.AppDef, m machine.Machine, state app.StatePath, w world.World) Menu {
	allowed := m.AllowedIntents(state, w)

	menu := Menu{}
	for _, ai := range allowed {
		if ai.Hidden {
			continue
		}

		intentDef, hasIntentDef := lookupIntentByPath(def, state, ai.Name)
		if !hasIntentDef {
			// Intent has no definition; treat as a no-slot intent.
			menu.Primary = append(menu.Primary, MenuEntry{
				Intent:  ai.Name,
				Display: ai.Name,
				Primary: true,
			})
			continue
		}

		entries := expandIntent(ai.Name, intentDef, state, m, w)
		for _, e := range entries {
			if e.Primary {
				menu.Primary = append(menu.Primary, e)
			} else {
				menu.Blocked = append(menu.Blocked, e)
			}
		}
	}

	// Sort primary by priority desc then display asc.
	// We use the underlying AllowedIntent priority (already sorted by machine) but
	// since we've broken intents into multiple rows we need stable ordering.
	sort.SliceStable(menu.Primary, func(i, j int) bool {
		pi := intentPriority(def, state, menu.Primary[i].Intent, allowed)
		pj := intentPriority(def, state, menu.Primary[j].Intent, allowed)
		if pi != pj {
			return pi > pj
		}
		return menu.Primary[i].Display < menu.Primary[j].Display
	})
	sort.SliceStable(menu.Blocked, func(i, j int) bool {
		return menu.Blocked[i].Display < menu.Blocked[j].Display
	})

	return menu
}

// intentPriority looks up the priority for an intent from the allowed list.
func intentPriority(def *app.AppDef, state app.StatePath, name string, allowed []machine.AllowedIntent) int {
	for _, ai := range allowed {
		if ai.Name == name {
			return ai.Priority
		}
	}
	return 0
}

// expandIntent expands one intent into one or more MenuEntry rows based on
// its slot schema and the guard dry-run results.
func expandIntent(name string, intentDef app.Intent, state app.StatePath, m machine.Machine, w world.World) []MenuEntry {
	// Collect required slots in deterministic order (sorted by name for stability
	// across map iteration; YAML map order is not guaranteed at runtime).
	var required []menuSlotEntry
	for sname, sdef := range intentDef.Slots {
		if sdef.Required {
			required = append(required, menuSlotEntry{sname, sdef})
		}
	}
	sort.Slice(required, func(i, j int) bool { return required[i].name < required[j].name })

	// Case 1: no required slots → one row, no expansion needed.
	if len(required) == 0 {
		return []MenuEntry{{
			Intent:  name,
			Display: name,
			Primary: true,
		}}
	}

	// Find the first enum slot in declaration order.
	enumIdx := -1
	for i, se := range required {
		if len(se.def.Values) > 0 {
			enumIdx = i
			break
		}
	}

	// Case 4: required slots but no enum → single placeholder row.
	if enumIdx < 0 {
		display := formatPlaceholderRow(name, required)
		// No guard dry-run possible without values; always primary.
		var missing []SlotRef
		for _, se := range required {
			missing = append(missing, SlotRef{
				Name:        se.name,
				Type:        se.def.Type,
				Values:      se.def.Values,
				Description: se.def.Description,
				Prompt:      se.def.Prompt,
			})
		}
		return []MenuEntry{{
			Intent:       name,
			Display:      display,
			MissingSlots: missing,
			Primary:      true,
		}}
	}

	enumSlot := required[enumIdx]

	// Cases 2 & 3: expand on the enum slot dimension.
	var entries []MenuEntry
	for _, val := range enumSlot.def.Values {
		// Build the prefilled slots map for this enum value.
		prefill := map[string]any{enumSlot.name: val}

		// Build display string.
		display := formatEnumRow(name, required, enumIdx, val)

		// Collect remaining (non-enum) required slots as MissingSlots.
		var missing []SlotRef
		for i, se := range required {
			if i == enumIdx {
				continue
			}
			missing = append(missing, SlotRef{
				Name:        se.name,
				Type:        se.def.Type,
				Values:      se.def.Values,
				Description: se.def.Description,
				Prompt:      se.def.Prompt,
			})
		}

		// Dry-run guards. For Case 3 (other required slots unprefilled), guards
		// that reference those slots will be "unresolved" → primary by default.
		result := m.TryGuards(state, w, name, prefill)

		// If the only arm that would fire is a default: catch-all branch, omit
		// this enum value from the menu entirely. The default: arm is a runtime
		// safety net for free-form input (e.g. "You can't go that way."); it is
		// not a real transition the author intends to surface. The user can still
		// type the direction directly and the runtime will handle it gracefully.
		// (If there are NO when: branches at all — not even a default — the result
		// is Blocked, which surfaces as a blocked row, not an omission.)
		if result.MatchedDefault {
			continue
		}

		var entry MenuEntry
		switch {
		case result.Unresolved:
			// Unresolved guards are treated as primary: the guard references a slot
			// that isn't prefilled yet. We'll re-evaluate at submission time.
			entry = MenuEntry{
				Intent:         name,
				PrefilledSlots: prefill,
				MissingSlots:   missing,
				Display:        display,
				Primary:        true,
				DestinationHint: result.DestinationHint,
			}
		case result.Primary:
			entry = MenuEntry{
				Intent:          name,
				PrefilledSlots:  prefill,
				MissingSlots:    missing,
				Display:         display,
				Primary:         true,
				DestinationHint: result.DestinationHint,
			}
		default: // Blocked
			reason := result.BlockedReason
			if reason == "" {
				reason = intentDef.Description
			}
			entry = MenuEntry{
				Intent:         name,
				PrefilledSlots: prefill,
				MissingSlots:   missing,
				Display:        display,
				Primary:        false,
				Reason:         reason,
			}
		}
		entries = append(entries, entry)
	}
	return entries
}

// menuSlotEntry is used only within this file to hold a required slot name+def pair.
type menuSlotEntry struct {
	name string
	def  app.Slot
}

// formatPlaceholderRow builds a display string for a no-enum intent:
//
//	ask <person:string> about <topic:string>
func formatPlaceholderRow(intentName string, required []menuSlotEntry) string {
	var sb strings.Builder
	sb.WriteString(intentName)
	for _, se := range required {
		sb.WriteString(" <")
		sb.WriteString(se.name)
		sb.WriteString(":")
		sb.WriteString(se.def.Type)
		sb.WriteString(">")
	}
	return sb.String()
}

// formatEnumRow builds a display string for one row of an enum expansion.
// The enum slot at enumIdx is replaced by its concrete value; other required
// slots appear as <name:type> placeholders.
//
// Examples:
//
//	go south              (single required enum slot, value=south)
//	give <item:string> to butler  (enum slot recipient=butler, free slot item)
func formatEnumRow(intentName string, required []menuSlotEntry, enumIdx int, enumVal string) string {
	var sb strings.Builder
	sb.WriteString(intentName)
	for i, se := range required {
		sb.WriteString(" ")
		if i == enumIdx {
			sb.WriteString(enumVal)
		} else {
			sb.WriteString("<")
			sb.WriteString(se.name)
			sb.WriteString(":")
			sb.WriteString(se.def.Type)
			sb.WriteString(">")
		}
	}
	return sb.String()
}

// ComputeClarification builds the §7.3 slot-fill context for a MISSING_SLOTS outcome.
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
func lookupStateByPath(def *app.AppDef, path app.StatePath) *app.State {
	parts := strings.Split(string(path), ".")
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
func lookupIntentByPath(def *app.AppDef, state app.StatePath, intentName string) (app.Intent, bool) {
	// Check state-local intents along the path.
	path := string(state)
	states := def.States
	var s *app.State
	for _, part := range strings.Split(path, ".") {
		s, _ = states[part]
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
