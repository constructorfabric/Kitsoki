// Package harness — shared prompt-building helpers (§10.5).
// Both LiveHarness and ClaudeCLIHarness use the same prompt structure so that
// the two harnesses produce identical system-prompt content given the same app.
package harness

import (
	"fmt"
	"strings"

	"hally/internal/app"
)

// buildStablePrefix renders the stable portion of the system prompt.
// This text does not change per-turn, so it is eligible for Anthropic prompt caching.
//
// Prompt cache threshold: Anthropic caches blocks ≥ 1024 tokens. For apps with
// minimal intent libraries the prefix may fall below this threshold on small apps;
// authors should add descriptions and examples to push over the threshold.
func buildStablePrefix(appDef *app.AppDef) string {
	var sb strings.Builder

	sb.WriteString("# Hally Intent Router\n\n")
	sb.WriteString("You are an intent-routing assistant for a structured application.\n")
	sb.WriteString("Your ONLY job is to call the `transition` tool exactly once with the\n")
	sb.WriteString("user's intent and any required slots. Do not explain. Do not converse.\n\n")

	sb.WriteString("## Application\n\n")
	title := appDef.App.Title
	if title == "" {
		title = appDef.App.ID
	}
	sb.WriteString(fmt.Sprintf("**%s** (id: %s)\n\n", title, appDef.App.ID))

	if len(appDef.Intents) > 0 {
		sb.WriteString("## Intent Library\n\n")
		for name, intent := range appDef.Intents {
			sb.WriteString(fmt.Sprintf("### `%s`", name))
			if intent.Title != "" {
				sb.WriteString(fmt.Sprintf(" — %s", intent.Title))
			}
			sb.WriteString("\n")
			if intent.Description != "" {
				sb.WriteString(intent.Description + "\n")
			}
			if len(intent.Examples) > 0 {
				sb.WriteString("Examples: " + strings.Join(intent.Examples, " | ") + "\n")
			}
			if len(intent.Slots) > 0 {
				sb.WriteString("Slots:\n")
				for slotName, slotDef := range intent.Slots {
					req := ""
					if slotDef.Required {
						req = " (required)"
					}
					sb.WriteString(fmt.Sprintf("  - `%s` [%s]%s", slotName, slotDef.Type, req))
					if len(slotDef.Values) > 0 {
						sb.WriteString(": one of " + strings.Join(slotDef.Values, "|"))
					}
					if slotDef.Description != "" {
						sb.WriteString(" — " + slotDef.Description)
					}
					sb.WriteString("\n")
				}
			}
			sb.WriteString("\n")
		}
	}

	sb.WriteString("## Tool Contract\n\n")
	sb.WriteString("Call `transition` with:\n")
	sb.WriteString("- `intent`: exactly one of the allowed intent names listed in the turn context below\n")
	sb.WriteString("- `slots`: a map of slot name → value, matching the intent's declared slots\n")
	sb.WriteString("- `confidence` (optional): 0..1 self-reported extraction confidence\n\n")
	sb.WriteString("If the user input is clearly ambiguous or maps to zero allowed intents,\n")
	sb.WriteString("still call `transition` with your best guess intent and set confidence < 0.5.\n")

	return sb.String()
}

// buildDynamicSuffix builds the per-turn portion of the system prompt.
func buildDynamicSuffix(in TurnInput) string {
	var sb strings.Builder
	sb.WriteString("\n---\n\n## Current Turn Context\n\n")
	sb.WriteString(fmt.Sprintf("**Current state:** `%s`\n\n", in.StatePath))

	if len(in.AllowedIntents) > 0 {
		sb.WriteString("**Allowed intents in this state:**\n")
		for _, name := range in.AllowedIntents {
			sb.WriteString(fmt.Sprintf("- `%s`\n", name))
		}
		sb.WriteString("\n")
	}

	if len(in.World.Vars) > 0 {
		sb.WriteString("**World context:**\n")
		for k, v := range in.World.Vars {
			sb.WriteString(fmt.Sprintf("- %s = %v\n", k, v))
		}
		sb.WriteString("\n")
	}

	if in.SystemPrompt != "" {
		sb.WriteString(in.SystemPrompt)
		sb.WriteString("\n")
	}

	return sb.String()
}
