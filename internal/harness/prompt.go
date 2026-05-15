// Package harness — shared prompt-building helpers (§10.5).
// Both LiveHarness and ClaudeCLIHarness use the same prompt structure so that
// the two harnesses produce identical system-prompt content given the same app.
package harness

import (
	"encoding/json"
	"fmt"
	"strings"

	"kitsoki/internal/app"
	"kitsoki/internal/expr"
	"kitsoki/internal/render"
	"kitsoki/internal/world"
)

// buildStablePrefix renders the stable portion of the system prompt.
// This text does not change per-turn, so it is eligible for Anthropic prompt caching.
//
// Prompt cache threshold: Anthropic caches blocks ≥ 1024 tokens. For apps with
// minimal intent libraries the prefix may fall below this threshold on small apps;
// authors should add descriptions and examples to push over the threshold.
func buildStablePrefix(appDef *app.AppDef) string {
	var sb strings.Builder

	sb.WriteString("# Kitsoki Intent Router\n\n")
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
//
// appDef is consulted to emit the current state's description and view text.
// The view is what the user sees on screen — including the menu labels that
// *define* how free-text maps to intents in this state (e.g. a main-room view
// saying "Start a new task (jira search)" tells the router that
// "start a new task" → `go_jira`). Without this, the router only has generic
// intent descriptions and misroutes state-specific phrasing.
func buildDynamicSuffix(appDef *app.AppDef, in TurnInput) string {
	var sb strings.Builder
	sb.WriteString("\n---\n\n## Current Turn Context\n\n")
	sb.WriteString(fmt.Sprintf("**Current state:** `%s`\n\n", in.StatePath))

	if state := lookupState(appDef, in.StatePath); state != nil {
		if state.Description != "" {
			sb.WriteString(fmt.Sprintf("**State description:** %s\n\n", state.Description))
		}
		if rendered := renderViewForPrompt(state.View.SourceString(), in.World); rendered != "" {
			sb.WriteString("**Current view (what the user sees — treat menu labels as authoritative for this state):**\n")
			sb.WriteString("```\n")
			sb.WriteString(rendered)
			if !strings.HasSuffix(rendered, "\n") {
				sb.WriteString("\n")
			}
			sb.WriteString("```\n\n")
		}
	}

	if len(in.AllowedIntents) > 0 {
		sb.WriteString("**Allowed intents in this state:**\n")
		for _, name := range in.AllowedIntents {
			sb.WriteString(fmt.Sprintf("- `%s`\n", name))
		}
		sb.WriteString("\n")
	}

	if len(in.RecentTurns) > 0 {
		sb.WriteString(buildRecentConversationBlock(in.RecentTurns))
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

// buildRecentConversationBlock renders the per-turn RecentTurns slice as a
// compact "Recent conversation" block for the system prompt. The block is
// ordered oldest → newest so the LLM reads it like a chat log; each row
// captures (user utterance, routed intent, post-turn state, rejected
// flag) — just enough to resolve "what I said before" or "the thing I
// tried that failed" without inflating the prompt.
//
// The convention is human prose annotated with a structured trailer:
//
//	**Recent conversation (oldest → newest):**
//	- turn 3 — user said "go south"; routed to `go(direction=south)`;
//	  ended in `bar.dark`
//	- turn 4 — user said "drink"; routed to `drink`; REJECTED
//	  (intent_not_allowed_in_state); state unchanged at `bar.dark`
//
// The trailer's exact phrasing is documented in TurnSummary; the leading
// "**Recent conversation**" heading is what the LLM is told to look for
// when resolving back-references.
func buildRecentConversationBlock(turns []TurnSummary) string {
	if len(turns) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("**Recent conversation (oldest → newest, for back-reference resolution like \"what I just said\"):**\n")
	for _, t := range turns {
		sb.WriteString(fmt.Sprintf("- turn %d — user said %q", int64(t.Turn), t.UserText))
		if t.Rejected {
			sb.WriteString("; REJECTED")
			if t.Intent != "" {
				sb.WriteString(fmt.Sprintf(" (router proposed `%s`)", t.Intent))
			}
		} else if t.Intent != "" {
			sb.WriteString(fmt.Sprintf("; routed to `%s`", t.Intent))
		}
		if len(t.Slots) > 0 {
			// Render the slots as a compact JSON object so the LLM can
			// re-extract them verbatim for back-reference inputs like
			// "same as before" or "yes — like I said".
			if b, err := json.Marshal(t.Slots); err == nil {
				sb.WriteString(fmt.Sprintf(" with slots %s", string(b)))
			}
		}
		if t.State != "" {
			if t.Rejected {
				sb.WriteString(fmt.Sprintf("; state unchanged at `%s`", t.State))
			} else {
				sb.WriteString(fmt.Sprintf("; ended in `%s`", t.State))
			}
		}
		sb.WriteString("\n")
	}
	sb.WriteString("\n")
	return sb.String()
}

// lookupState walks a dot-separated StatePath through AppDef's nested States
// map. Returns nil if the path is empty or any segment misses.
func lookupState(appDef *app.AppDef, path app.StatePath) *app.State {
	if appDef == nil || path == "" {
		return nil
	}
	segments := strings.Split(string(path), ".")
	states := appDef.States
	var st *app.State
	for _, seg := range segments {
		s, ok := states[seg]
		if !ok || s == nil {
			return nil
		}
		st = s
		states = s.States
	}
	return st
}

// renderViewForPrompt expands a view template against the world snapshot for
// inclusion in the LLM prompt. Best-effort: on render errors we return the
// literal template so the LLM at least sees the menu labels.
func renderViewForPrompt(view string, w world.World) string {
	view = strings.TrimSpace(view)
	if view == "" {
		return ""
	}
	rendered, err := render.Pongo(view, expr.Env{World: w.Vars, Slots: map[string]any{}})
	if err != nil || rendered == "" {
		return view
	}
	return strings.TrimSpace(rendered)
}
