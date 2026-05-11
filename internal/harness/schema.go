// Package harness — BuildTransitionSchema is the single source of truth for
// the transition-tool input schema. Both LiveHarness and ClaudeCLIHarness
// derive their tool schema from here so semantic formats declared on slots
// (e.g. `format: jql`) flow through to slot extraction in either harness.
package harness

import (
	"encoding/json"
	"fmt"

	"kitsoki/internal/app"
)

// BuildTransitionSchema returns a JSON Schema (draft 2020-12) describing the
// arguments to the transition tool. Top-level shape:
//
//	{intent: <enum>, slots: {<flat union of all slot defs>}, confidence}
//
// Slots from every allowed intent are unioned into one `slots.properties`
// map keyed by slot name. Per-intent `required` and `additionalProperties:
// false` are intentionally dropped: Anthropic's tool input_schema rejects
// `oneOf`, `allOf`, `anyOf` at the top level, which rules out the obvious
// `if/then` discriminator pattern. The semantic checks that actually catch
// drift (per-slot `format`, type, enum) survive the flattening.
//
// If two intents declare a slot with the same name, the first declaration
// in `allowedIntents` order wins. The harness already runs in an
// LLM-driven extraction loop where slot-shape collisions across intents
// would be a router-level authoring smell, not a runtime correctness issue.
//
// An empty `allowedIntents` slice yields an empty enum but is otherwise valid.
func BuildTransitionSchema(appDef *app.AppDef, allowedIntents []string) ([]byte, error) {
	if appDef == nil {
		return nil, fmt.Errorf("harness/schema: appDef must not be nil")
	}

	enum := make([]any, 0, len(allowedIntents))
	for _, name := range allowedIntents {
		enum = append(enum, name)
	}

	slotProps := map[string]any{}
	for _, name := range allowedIntents {
		intent, ok := appDef.Intents[name]
		if !ok {
			continue
		}
		for slotName, slot := range intent.Slots {
			if _, exists := slotProps[slotName]; exists {
				continue
			}
			slotProps[slotName] = buildSlotProperty(slot)
		}
	}

	schema := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"intent"},
		"properties": map[string]any{
			"intent": map[string]any{
				"type": "string",
				"enum": enum,
			},
			"slots": map[string]any{
				"type":       "object",
				"properties": slotProps,
			},
			"confidence": map[string]any{
				"type":    "number",
				"minimum": 0,
				"maximum": 1,
			},
		},
	}

	return json.Marshal(schema)
}

// buildSlotProperty renders one slot's JSON-Schema property.
func buildSlotProperty(slot app.Slot) map[string]any {
	prop := map[string]any{
		"type": mapSlotType(slot.Type),
	}
	if slot.Description != "" {
		prop["description"] = slot.Description
	}
	if len(slot.Examples) > 0 {
		examples := make([]any, len(slot.Examples))
		for i, ex := range slot.Examples {
			examples[i] = ex
		}
		prop["examples"] = examples
	}
	if len(slot.Values) > 0 {
		enum := make([]any, len(slot.Values))
		for i, v := range slot.Values {
			enum[i] = v
		}
		prop["enum"] = enum
	}
	if slot.Format != "" {
		prop["format"] = slot.Format
	}
	return prop
}

// mapSlotType maps a Slot.Type string to a JSON-Schema primitive type.
// Anything unrecognised (including the empty string and authoring shorthands
// like "enum") falls back to "string", which is the right default for the
// LLM-facing extraction surface.
func mapSlotType(t string) string {
	switch t {
	case "string":
		return "string"
	case "number":
		return "number"
	case "integer":
		return "integer"
	case "boolean":
		return "boolean"
	default:
		return "string"
	}
}
