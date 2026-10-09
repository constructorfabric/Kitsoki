// Package orchestrator — the off-ramp conversation lane
// (agent_off_ramp.capture_free_text).
//
// A room that opts in gets ONE persistent, engine-owned conversation: the
// loader synthesizes a `<room>_discuss` default intent
// (internal/app/offramp_capture.go), the deterministic free-text tier sinks
// unmatched prose into it, and maybeConversationDivert intercepts that
// intent in submitDirect BEFORE machine.Turn — so a conversational turn
// never fires a transition, never mutates world, and never re-renders the
// room. The answer comes back as a ModeOffPath outcome exactly like the
// clarify-triggered off-ramp; the chat thread is per-room and resumed
// across turns (offRampScope); the converse system prompt carries the
// engine-composed room context (composeRoomContext) so the story author
// passes nothing.
package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"kitsoki/internal/app"
	"kitsoki/internal/world"
)

// conversationCaptureIntent reports the resolved capture-intent name for a
// state that opted into the conversation lane, or "" when the state has no
// lane (no off-ramp, no capture_free_text, or the synthesized default intent
// is missing). The loader pass guarantees DefaultIntent holds the
// synthesized `<room>_discuss` name for opted-in rooms, so the lane's
// identity is exactly "the room's default intent" resolved through the same
// alias machinery routeViaDefaultIntent uses.
func conversationCaptureIntent(def *app.AppDef, state app.StatePath, st *app.State) string {
	// Runtime convention: a non-nil State.AgentOffRamp means "the off-ramp
	// fires" (the loader's normalize pass nils disabled ones) — same
	// nil-check maybeOffRamp uses, deliberately not Enabled(), so Go-built
	// defs (tests) behave like loaded ones.
	if st == nil || st.AgentOffRamp == nil || !st.AgentOffRamp.CaptureFreeText {
		return ""
	}
	di := strings.TrimSpace(st.DefaultIntent)
	if di == "" {
		return ""
	}
	return resolveIntentAlias(def, state, st, di)
}

// maybeConversationDivert is the conversation lane's single interception
// point, consulted by submitDirect after the journey loads and before
// machine.Turn runs. All live surfaces (free-text routing via
// routeViaDefaultIntent → SubmitDirectRouted, TUI/web menu submits, MCP
// session_drive) converge on submitDirect, so the divert covers every entry
// path; only the bare machine (flow fixtures) bypasses it and takes the
// synthesized no-op self-arc instead.
//
// On a hit it records OffPathEntered (reason: "conversation"), converses on
// the room's persistent chat thread with the engine-composed room context,
// and returns a ModeOffPath outcome — resting state, menu, and world all
// unchanged. On a converse failure it returns the error so the caller
// surfaces a soft failure rather than silently re-rendering the room.
//
// The caller must already hold the per-session lock (submitDirect does);
// like maybeOffRamp, the ctx is marked so the off-path append path skips
// re-locking.
func (o *Orchestrator) maybeConversationDivert(
	ctx context.Context,
	sid app.SessionID,
	state app.StatePath,
	w world.World,
	intentName string,
	slots map[string]any,
	userInput string,
	turnNum app.TurnNumber,
) (*TurnOutcome, bool, error) {
	st := lookupStateByPath(o.def, state)
	capture := conversationCaptureIntent(o.def, state, st)
	if capture == "" || intentName != capture {
		return nil, false, nil
	}

	question, _ := slots["message"].(string)
	if strings.TrimSpace(question) == "" {
		question = userInput
	}
	if strings.TrimSpace(question) == "" {
		// Nothing to converse over — let the machine handle it (the no-op
		// arc's MISSING_SLOTS rejection tells the caller what's wrong).
		return nil, false, nil
	}

	allowedIntents := o.machine.AllowedIntents(state, w)
	allowedNames := make([]string, len(allowedIntents))
	for i, ai := range allowedIntents {
		allowedNames[i] = ai.Name
	}

	ctx = withOffRampLockHeld(ctx)
	if err := o.markOffRampEnteredReason(ctx, sid, state, offPathReasonConversation, "", 0); err != nil {
		o.logger.WarnContext(ctx, "offpath.ask.error",
			"session_id", string(sid),
			"phase", "conversation_mark_entered",
			"err", err.Error(),
		)
	}

	answer, err := o.askOffPathVoiced(ctx, sid, question, o.offRampVoice(st), o.offRampScopeForState(state, st, w, allowedNames))
	if err != nil {
		// Surface the failure — never silently fall through to the no-op arc
		// (a re-rendered room with no answer is exactly the silent bounce the
		// never-silent rule exists to prevent).
		return nil, false, fmt.Errorf("orchestrator: conversation lane: %w", err)
	}

	return &TurnOutcome{
		Mode:           ModeOffPath,
		View:           answer,
		NewState:       state,
		AllowedIntents: allowedNames,
		TurnNumber:     turnNum,
	}, true, nil
}

// composeRoomContext renders the engine-composed context block fed to an
// off-ramp / conversation-lane converse call as the room_context arg: the
// room's purpose (state description), the commands available this turn
// (with their intent descriptions), and the room's relevant_world values.
// The block is what lets a story drop all hand-rolled context plumbing —
// the agent starts every turn already knowing where it is and what the
// operator could do instead of asking.
func composeRoomContext(def *app.AppDef, state app.StatePath, st *app.State, w world.World, allowedNames []string) string {
	if def == nil || st == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Room context\n")
	title := def.App.Title
	if title == "" {
		title = def.App.ID
	}
	fmt.Fprintf(&b, "Story: %s — room: %s\n", title, state)
	if desc := strings.TrimSpace(st.Description); desc != "" {
		fmt.Fprintf(&b, "Room purpose: %s\n", desc)
	}
	b.WriteString("Deterministic story commands perform the mutations; you answer and advise. When the operator wants a mutating action, point them at the matching command below instead of doing it yourself.\n")

	capture := conversationCaptureIntent(def, state, st)
	names := append([]string(nil), allowedNames...)
	sort.Strings(names)
	wroteHeader := false
	for _, name := range names {
		if name == capture {
			continue // the conversation lane itself — not an operator command
		}
		intentDef, ok := lookupIntentByPath(def, state, name)
		if !ok {
			continue
		}
		if !wroteHeader {
			b.WriteString("Available commands in this room:\n")
			wroteHeader = true
		}
		desc := strings.TrimSpace(intentDef.Description)
		if desc == "" {
			desc = strings.TrimSpace(intentDef.Title)
		}
		if desc != "" {
			fmt.Fprintf(&b, "- %s: %s\n", name, desc)
		} else {
			fmt.Fprintf(&b, "- %s\n", name)
		}
	}

	wroteHeader = false
	for _, key := range st.RelevantWorld {
		val, ok := w.Vars[key]
		if !ok {
			continue
		}
		if !wroteHeader {
			b.WriteString("Current room state:\n")
			wroteHeader = true
		}
		fmt.Fprintf(&b, "- %s: %s\n", key, compactWorldValue(val))
	}
	return b.String()
}

// compactWorldValue renders one world value for the room-context block:
// scalars as-is, everything else as compact JSON, truncated so a large
// object can't blow up the converse system prompt.
func compactWorldValue(v any) string {
	const maxLen = 400
	var s string
	switch t := v.(type) {
	case string:
		s = t
	case nil:
		s = ""
	default:
		raw, err := json.Marshal(t)
		if err != nil {
			s = fmt.Sprintf("%v", t)
		} else {
			s = string(raw)
		}
	}
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > maxLen {
		s = s[:maxLen] + "…"
	}
	return s
}
