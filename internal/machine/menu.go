// Menu computation for the progressive-disclosure surface.
//
// Lives in internal/machine so view-render call sites can populate
// env.Menu without depending on internal/orchestrator (which would
// produce an import cycle — orchestrator already imports machine).
//
// orchestrator.ComputeMenu is a thin wrapper over Machine.Menu.
package machine

import (
	"sort"
	"strings"

	"kitsoki/internal/app"
	"kitsoki/internal/world"
)

// MenuSlotRef describes a single required slot that has not been pre-filled.
// It is used in MenuEntry.MissingSlots to drive the clarification flow.
type MenuSlotRef struct {
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

// MenuEntry is one concrete row in the menu. Unlike a bare AllowedIntent
// (just the intent name), each MenuEntry represents a fully qualified
// (intent + prefilled-slots) action that can either be submitted directly
// (MissingSlots empty) or launched into the clarification flow.
//
// Design rationale:
//   - Enum slots are expanded: "go north", "go south", ... become separate rows.
//   - Free-form required slots remain as placeholders in Display; the row still
//     appears but requires clarification before dispatch.
//   - Optional slots are NOT shown as placeholders: they are discoverable via
//     the clarification flow.
type MenuEntry struct {
	// Intent is the intent name.
	Intent string
	// PrefilledSlots are the slots that have been pre-resolved for this row.
	PrefilledSlots map[string]any
	// MissingSlots are required slots that are not yet known. If non-empty,
	// selecting this row enters the clarification flow.
	MissingSlots []MenuSlotRef
	// Display is the human-readable label for this row.
	Display string
	// Reason is the guard_hint or description shown when Primary is false.
	Reason string
	// Primary is true when the guard dry-run passed (or was unresolved).
	Primary bool
	// DestinationHint is the resolved target state path when Primary is true
	// and a guard matched.
	DestinationHint string
}

// MenuView is the computed "where can I go" surface — the primary and blocked
// action rows for a state, ready for the progressive-disclosure menu.
type MenuView struct {
	// Primary is the sorted list of available actions (guards pass or unresolved).
	Primary []MenuEntry
	// Blocked is the list of actions whose guards currently fail.
	Blocked []MenuEntry
}

// Menu returns the computed MenuView (primary + blocked) for the given
// state and world. Equivalent to the orchestrator.ComputeMenu of prior
// versions, but lives here so view templates can reference it via
// env.Menu without an import cycle.
func (m *machineImpl) Menu(state app.StatePath, w world.World) MenuView {
	allowed := m.AllowedIntents(state, w)

	// Build a label override map from the state's typed choice view.
	// When a choice item carries an explicit label for an intent that
	// expands to a placeholder row (required free-form slot, no enum),
	// the machine-generated display "capture_idea <idea:string>" doesn't
	// match what the user sees and types — the view label ("capture") does.
	// Overriding the Display makes deterministic routing work without LLM.
	viewLabels := m.viewLabelsForState(state)

	menu := MenuView{}
	for _, ai := range allowed {
		if ai.Hidden {
			continue
		}

		intentDef, hasIntentDef := m.lookupIntent(state, ai.Name)
		if !hasIntentDef {
			display := ai.Name
			if l, ok := viewLabels[ai.Name]; ok {
				display = l
			}
			menu.Primary = append(menu.Primary, MenuEntry{
				Intent:  ai.Name,
				Display: display,
				Primary: true,
			})
			continue
		}

		entries := m.expandIntent(ai.Name, intentDef, state, w)
		// Override Display with the view label for single placeholder rows
		// (required free-form slots, no enum expansion). Don't override
		// enum-expanded rows — each carries a specific value in its Display.
		if l, ok := viewLabels[ai.Name]; ok && len(entries) == 1 && len(entries[0].MissingSlots) > 0 {
			entries[0].Display = l
		}
		for _, e := range entries {
			if e.Primary {
				menu.Primary = append(menu.Primary, e)
			} else {
				menu.Blocked = append(menu.Blocked, e)
			}
		}
	}

	// Sort primary by priority desc then display asc.
	sort.SliceStable(menu.Primary, func(i, j int) bool {
		pi := menuIntentPriority(menu.Primary[i].Intent, allowed)
		pj := menuIntentPriority(menu.Primary[j].Intent, allowed)
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

// menuIntentPriority looks up the priority for an intent from the allowed list.
func menuIntentPriority(name string, allowed []AllowedIntent) int {
	for _, ai := range allowed {
		if ai.Name == name {
			return ai.Priority
		}
	}
	return 0
}

// expandIntent expands one intent into one or more MenuEntry rows based on
// its slot schema and the guard dry-run results.
//
// Expansion rules:
//  1. No required slots → one row, no guard dry-run needed (unless blocked).
//  2. Exactly one required slot, with enum → one row per enum value; guard
//     dry-run with synthetic slots map to classify each row as primary/blocked.
//  3. Multiple required slots, first enum slot in declaration order → one row
//     per enum value with other required slots shown as <name:type>
//     placeholders.
//  4. Required slots but no enums → one placeholder row; always primary.
func (m *machineImpl) expandIntent(name string, intentDef app.Intent, state app.StatePath, w world.World) []MenuEntry {
	var required []menuSlotEntry
	for sname, sdef := range intentDef.Slots {
		if sdef.Required {
			required = append(required, menuSlotEntry{sname, sdef})
		}
	}
	sort.Slice(required, func(i, j int) bool { return required[i].name < required[j].name })

	// Case 1: no required slots → single row, dry-run guards.
	if len(required) == 0 {
		result := m.TryGuards(state, w, name, nil)
		entry := MenuEntry{
			Intent:  name,
			Display: name,
		}
		switch {
		case result.Blocked:
			entry.Primary = false
			entry.Reason = result.BlockedReason
			if entry.Reason == "" {
				entry.Reason = intentDef.Description
			}
		case result.MatchedDefault && result.WhenArmFailed && result.BlockedReason != "":
			// "A when arm failed but a default caught it" only demotes the
			// entry to blocked when the default arm — or some failing when
			// arm — actually carries a guard_hint. That hint is the author's
			// signal that the catch-all is a no-op rejection (e.g.
			// `start_journey` falling through to `target: intro` with a
			// "Name the party first" hint). Without a hint the catch-all is
			// the normal progression path (e.g. `continue` on a leg's
			// _awaiting_reply where the only when: arm is the rarely-true
			// snow-blocked guard); demoting it would mislead the player into
			// thinking their primary action is unavailable when it isn't.
			entry.Primary = false
			entry.Reason = result.BlockedReason
		default:
			entry.Primary = true
			entry.DestinationHint = result.DestinationHint
		}
		return []MenuEntry{entry}
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
		var missing []MenuSlotRef
		for _, se := range required {
			missing = append(missing, MenuSlotRef{
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
		prefill := map[string]any{enumSlot.name: val}
		display := formatEnumRow(name, required, enumIdx, val)

		var missing []MenuSlotRef
		for i, se := range required {
			if i == enumIdx {
				continue
			}
			missing = append(missing, MenuSlotRef{
				Name:        se.name,
				Type:        se.def.Type,
				Values:      se.def.Values,
				Description: se.def.Description,
				Prompt:      se.def.Prompt,
			})
		}

		result := m.TryGuards(state, w, name, prefill)

		// Omit pure-default-arm rows (catch-all is a runtime safety net,
		// not a menu entry the author wants to surface).
		if result.MatchedDefault {
			continue
		}

		var entry MenuEntry
		switch {
		case result.Unresolved:
			entry = MenuEntry{
				Intent:          name,
				PrefilledSlots:  prefill,
				MissingSlots:    missing,
				Display:         display,
				Primary:         true,
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

// menuSlotEntry is a required slot name+def pair used during expansion.
type menuSlotEntry struct {
	name string
	def  app.Slot
}

// viewLabelsForState returns a map of intent name → author label from the
// state's typed choice view elements. Used to override the machine-generated
// placeholder Display ("capture_idea <idea:string>") with the label the user
// actually sees and types ("capture").
//
// Only static labels are returned — items whose label contains "{{" are
// template expressions that evaluate to dynamic text at render time and
// would not match user-typed input.
//
// The first occurrence per intent wins; blocked variants like
// "✗ drive — …" appear after the available form in author YAML so
// the override is always the user-typeable label.
func (m *machineImpl) viewLabelsForState(state app.StatePath) map[string]string {
	cs, ok := m.states[string(state)]
	if !ok || cs == nil || cs.s == nil {
		return nil
	}
	labels := make(map[string]string)
	for _, elem := range cs.s.View.Elements {
		if elem.Kind != "choice" {
			continue
		}
		for _, item := range elem.ChoiceItems {
			if item.Intent == "" || item.Label == "" {
				continue
			}
			if _, exists := labels[item.Intent]; exists {
				continue
			}
			// Skip labels that contain template expressions.
			if strings.Contains(item.Label, "{{") {
				continue
			}
			labels[item.Intent] = item.Label
		}
	}
	if len(labels) == 0 {
		return nil
	}
	return labels
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

// MenuToTemplateMap converts a MenuView into the shape templates see via
// env.Menu — a map[string]any with "primary" and "blocked" lists of plain
// map entries. Plain-map shape keeps JSON serialization and template
// iteration (`{{ range menu.primary }}{{ .display }}{{ end }}`) working
// without bespoke marshalling.
func MenuToTemplateMap(m MenuView) map[string]any {
	out := map[string]any{
		"primary": menuEntriesToMaps(m.Primary),
		"blocked": menuEntriesToMaps(m.Blocked),
	}
	return out
}

func menuEntriesToMaps(entries []MenuEntry) []any {
	out := make([]any, 0, len(entries))
	for _, e := range entries {
		out = append(out, map[string]any{
			"intent":           e.Intent,
			"display":          e.Display,
			"reason":           e.Reason,
			"destination_hint": e.DestinationHint,
			"primary":          e.Primary,
		})
	}
	return out
}
