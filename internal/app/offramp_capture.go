// Package app — off-ramp free-text capture (`agent_off_ramp.capture_free_text`)
// desugaring.
//
// A room that declares `agent_off_ramp: {agent: X, capture_free_text: true}`
// opts its off-ramp conversation in as the room's deterministic free-text
// sink. This pass synthesizes the two story-side pieces that makes work:
//
//   - a top-level `<room>_discuss` intent (one required string slot,
//     `message`) registered in def.Intents, and
//   - `default_intent: <room>_discuss` on the state, so the deterministic
//     free-text tier (orchestrator routeViaDefaultIntent) sinks unmatched
//     prose into it without any main-turn LLM classification.
//
// The synthesized intent also gets a self-target no-op arc so menus,
// default_intent validation, semantic routing, and bare-machine drivers
// (flow fixtures) all see a legal intent. On live surfaces that arc never
// fires: the orchestrator diverts the intent to the off-ramp conversation
// lane BEFORE machine.Turn (see orchestrator submitDirect /
// maybeConversationDivert) — no transition, no world mutation, no room
// re-render. Driven through the bare machine (kitsoki test flows) it is a
// harmless self-loop, mirroring how dev-story's landing_off_ramp fixture
// pins the machine-level invariant underneath the live off-ramp.
//
// This mirrors expandWorkbenches (workbench.go) structurally, and the two
// compose by exclusion: `workbench:` synthesizes its own off-ramp (without
// capture) and already rejects a hand-authored agent_off_ramp beside it, so
// this pass — which runs after expandWorkbenches — only ever sees
// author-written capture declarations.
package app

import (
	"fmt"
	"strings"
)

// OffRampCaptureIntentSuffix is the fixed suffix of the synthesized capture
// intent name (`<room>_discuss`). Exported for the orchestrator's divert and
// for tests; the name is always synthesized from the room's leaf name, never
// author-chosen (the same no-invented-naming rule workbench: follows).
const OffRampCaptureIntentSuffix = "_discuss"

// expandOffRampCaptures walks def.States and desugars every enabled
// agent_off_ramp with CaptureFreeText into the synthesized intent + no-op
// arc + default_intent described in the package comment above. States
// without the flag are byte-for-byte untouched.
func expandOffRampCaptures(def *AppDef, file string) []error {
	if def == nil {
		return nil
	}
	var errs []error
	var walk func(prefix string, states map[string]*State)
	walk = func(prefix string, states map[string]*State) {
		for _, name := range sortedKeys(states) {
			s := states[name]
			if s == nil {
				continue
			}
			statePath := joinPath(prefix, name)
			if s.AgentOffRamp.Enabled() && s.AgentOffRamp.CaptureFreeText {
				if cErrs := expandOneOffRampCapture(def, file, statePath, name, s); len(cErrs) > 0 {
					errs = append(errs, cErrs...)
				}
			}
			if len(s.States) > 0 {
				walk(statePath, s.States)
			}
		}
	}
	walk("", def.States)
	return errs
}

// expandOneOffRampCapture desugars a single state's capture declaration in
// place. room is the state's own (leaf) name — the source of the synthesized
// intent name; statePath is the full dotted path, used for the arc target
// and error messages.
func expandOneOffRampCapture(def *AppDef, file, statePath, room string, s *State) []error {
	var errs []error
	addErr := func(msg string) {
		errs = append(errs, &ValidationError{File: file, Message: fmt.Sprintf("state %q: agent_off_ramp.capture_free_text: %s", statePath, msg)})
	}

	// Mutual exclusion: capture sets default_intent itself, so a
	// hand-authored value beside it is an unresolvable ambiguity — a load
	// error, not a silent override (the workbench: rule, applied here).
	if strings.TrimSpace(s.DefaultIntent) != "" {
		addErr(fmt.Sprintf("cannot combine with hand-authored default_intent: %q (capture_free_text sets default_intent to the synthesized %s%s intent itself)", s.DefaultIntent, room, OffRampCaptureIntentSuffix))
		return errs
	}

	captureIntent := room + OffRampCaptureIntentSuffix

	if def.Intents == nil {
		def.Intents = map[string]Intent{}
	}
	if _, exists := def.Intents[captureIntent]; exists {
		addErr(fmt.Sprintf("synthesized intent name %q collides with a declared intent; rename the declared intent (the capture intent name is always <room>%s)", captureIntent, OffRampCaptureIntentSuffix))
		return errs
	}
	def.Intents[captureIntent] = Intent{
		Title:       "Discuss",
		Description: fmt.Sprintf("Talk with the %q room's assistant — questions, follow-ups, anything the room's commands don't cover.", room),
		Slots: map[string]Slot{
			"message": {Type: "string", Required: true, Description: "What you want to say or ask."},
		},
	}

	if s.On == nil {
		s.On = map[string][]Transition{}
	}
	if _, exists := s.On[captureIntent]; exists {
		addErr(fmt.Sprintf("room already declares an %q arc; the capture arc is synthesized", captureIntent))
		return errs
	}
	// No-op self-arc: keeps the intent legal for validation/menus/flows.
	// Live surfaces divert before it ever fires.
	s.On[captureIntent] = []Transition{{Target: statePath}}

	s.DefaultIntent = captureIntent
	return errs
}
