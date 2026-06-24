package orchestrator

// Engine-driven LLM decider for intent gates.
//
// When the synthetic emit chain rests at a multi-way decision gate that owes
// an autonomous decision — one-shot mode (or a `decider: llm` pin) with no
// conditional-default emit having fired — the engine asks an LLM judge to
// choose among the gate's available intents, then drives the chosen intent.
// If the judge is not confident / returns "uncertain" / names an intent that
// is not a candidate / errors, the engine BAILS to human: it leaves the turn
// rested at the gate so an operator decides. Every outcome is recorded as a
// GateDecided event.
//
// This is the generalisation of the bugfix story's hand-rolled
// `agent.decide → emit_intent "{{ llm_verdict.intent }}"` shape: the author
// no longer wires a judge into every room — the engine drives it at any gate.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"kitsoki/internal/app"
	"kitsoki/internal/intent"
	"kitsoki/internal/judges"
	"kitsoki/internal/machine"
	"kitsoki/internal/store"
	"kitsoki/internal/trace"
	"kitsoki/internal/world"
)

// deciderMaxDepth bounds the engine decider loop (judge → fire → settle →
// judge again) so a misbehaving judge that keeps choosing self-recycling
// intents can't spin forever.
const deciderMaxDepth = 8

// defaultDeciderThreshold is the confidence floor below which a verdict bails
// to human, when the DeciderConfig doesn't set one.
const defaultDeciderThreshold = 0.8

// DeciderConfig configures the engine-driven LLM decider. Wired via
// WithDecider; nil disables engine-driven decisions (one-shot gates with no
// firing default simply rest, as before).
type DeciderConfig struct {
	// Agent is the name of the judge agent (declared in the app's agents:)
	// the engine invokes via host.agent.decide. Required.
	Agent string
	// Schema is the path to the decision schema the agent enforces MCP-side.
	// The submitted object must carry {intent, confidence, reason}. Required
	// (host.agent.decide rejects an empty schema).
	Schema string
	// Prompt, when set, is the path to a decision-prompt template; the engine
	// passes the gate candidates + world as template args. When empty the
	// engine synthesises an inline prompt from the gate's own vocabulary.
	Prompt string
	// Threshold is the confidence floor for auto-firing a verdict. Defaults
	// to defaultDeciderThreshold when zero.
	Threshold float64
}

func (c *DeciderConfig) threshold() float64 {
	if c == nil || c.Threshold <= 0 {
		return defaultDeciderThreshold
	}
	return c.Threshold
}

// WithDecider installs the engine-driven LLM decider.
func WithDecider(cfg DeciderConfig) Option {
	return func(o *Orchestrator) { o.decider = &cfg }
}

// gateWantsLLM reports whether the engine should run the LLM decider at the
// rested state: a decider is configured, the state is a real decision gate,
// and the effective decider for this gate is the LLM — i.e. the per-state
// `decider:` is "llm", or it's unset and the run is one-shot. A
// `decider: human` pin (or staged mode with no llm pin — which would already
// have stopped in the machine) keeps the human in the loop.
func (o *Orchestrator) gateWantsLLM(state app.StatePath, w world.World) bool {
	if o.decider == nil {
		return false
	}
	override := ""
	if st := lookupStateByPath(o.def, state); st != nil {
		override = strings.TrimSpace(st.Decider)
	}
	switch override {
	case "human":
		return false
	case "llm":
		return o.machine.IsDecisionGate(state, w)
	}
	return o.execMode == ExecOneShot && o.machine.IsDecisionGate(state, w)
}

// resolveAutoGate runs the engine decider loop after the emit chain has
// settled. While the turn rests at an LLM-decidable gate, it asks the judge,
// records the decision, and (on a confident, valid verdict) drives the chosen
// intent — re-settling and looping for the next gate. Bounded by
// deciderMaxDepth. A bail (low confidence / uncertain / invalid / error)
// leaves the turn rested at the gate for a human.
func (o *Orchestrator) resolveAutoGate(ctx context.Context, sid app.SessionID, res *machine.TurnResult, tl *trace.TurnLogger, depth int) {
	if o.decider == nil || res == nil {
		return
	}
	if depth > deciderMaxDepth {
		o.recordGate(res, res.NewState, nil, "llm", "", 0, true,
			fmt.Sprintf("decider loop exceeded depth %d", deciderMaxDepth), nil, o.decider.threshold())
		return
	}
	state := res.NewState
	if !o.gateWantsLLM(state, res.World) {
		return
	}
	candidates := o.machine.DecisionCandidates(state, res.World)
	if len(candidates) < 2 {
		// 0 → terminal/no choice; 1 → the machine would have advanced it.
		return
	}

	verdict, verr := o.invokeDecider(ctx, sid, res, candidates, tl)
	names := candidateNames(candidates)
	threshold := o.decider.threshold()
	valid := verr == nil &&
		verdict.ShouldAutoFire(threshold) &&
		containsStr(names, verdict.Intent)

	reason := ""
	if verr != nil {
		reason = verr.Error()
	} else if !verdict.ShouldAutoFire(threshold) {
		reason = fmt.Sprintf("verdict not auto-fireable (verdict=%q intent=%q confidence=%.2f < %.2f)",
			verdict.Verdict, verdict.Intent, verdict.Confidence, threshold)
	} else if !containsStr(names, verdict.Intent) {
		reason = fmt.Sprintf("chosen intent %q is not a gate candidate %v", verdict.Intent, names)
	}

	o.recordGate(res, state, names, "llm", verdict.Intent, verdict.Confidence, !valid, reason, verdict.Alternatives, threshold)

	if tl != nil {
		tl.Debug(ctx, trace.EvIntentEmitted,
			slog.String("state", string(state)),
			slog.String("kind", "engine_decider"),
			slog.String("chosen", verdict.Intent),
			slog.Bool("fired", valid),
		)
	}

	if !valid {
		return // bail to human — rest at the gate
	}

	if err := o.applyDeciderIntent(ctx, sid, res, verdict.Intent, tl); err != nil {
		// Applying the chosen intent failed (e.g. it needed slots the judge
		// can't supply). Leave the turn rested at the gate for a human.
		if tl != nil {
			tl.Debug(ctx, trace.EvHarnessError,
				slog.String("phase", "apply_decider_intent"),
				slog.String("intent", verdict.Intent),
				slog.String("error", err.Error()))
		}
		return
	}

	// The chosen intent may land on another gate — recurse.
	o.resolveAutoGate(ctx, sid, res, tl, depth+1)
}

// invokeDecider builds and dispatches a host.agent.decide call for the gate,
// then parses the agent's submitted object into a judges.Verdict.
func (o *Orchestrator) invokeDecider(ctx context.Context, sid app.SessionID, res *machine.TurnResult, candidates []machine.AllowedIntent, tl *trace.TurnLogger) (judges.Verdict, error) {
	const verdictKey = "__engine_decider_verdict"

	args := map[string]any{
		"schema": o.decider.Schema,
		"agent":  o.decider.Agent,
	}
	if strings.TrimSpace(o.decider.Prompt) != "" {
		args["prompt_path"] = o.decider.Prompt
	} else {
		args["prompt"] = buildDeciderPrompt(o.def, res.NewState, candidates, res.World)
	}
	// Template args the prompt (file or inline) can interpolate.
	args["with"] = map[string]any{
		"state":      string(res.NewState),
		"candidates": candidatePayload(candidates),
	}

	hc := machine.HostInvocation{
		Namespace: "host.agent.decide",
		Args:      args,
		Bind:      map[string]string{verdictKey: "submitted"},
	}

	events, w, _, _, err := o.dispatchHostCalls(ctx, sid, []machine.HostInvocation{hc}, res.World, res.NewState)
	res.Events = append(res.Events, events...)
	res.World = w
	if err != nil {
		return judges.Verdict{}, err
	}

	raw, ok := res.World.Vars[verdictKey]
	// Clean the scratch key out of world so it never persists to the journal.
	delete(res.World.Vars, verdictKey)
	if !ok || raw == nil {
		return judges.Verdict{}, fmt.Errorf("decider: agent returned no submitted verdict")
	}

	b, mErr := json.Marshal(raw)
	if mErr != nil {
		return judges.Verdict{}, fmt.Errorf("decider: marshal submitted: %w", mErr)
	}
	var v judges.Verdict
	if uErr := json.Unmarshal(b, &v); uErr != nil {
		return judges.Verdict{}, fmt.Errorf("decider: parse verdict: %w", uErr)
	}
	return v, nil
}

// applyDeciderIntent drives the judge's chosen intent as a transition on the
// CURRENT turn (no new turn number): run machine.Turn, dispatch its host
// calls, then settle its emit chain — exactly the sequence the normal turn
// path uses. Mutates res in place.
func (o *Orchestrator) applyDeciderIntent(ctx context.Context, sid app.SessionID, res *machine.TurnResult, intentName string, tl *trace.TurnLogger) error {
	call := intent.IntentCall{Intent: intentName}
	tr, err := o.machine.Turn(ctx, res.NewState, res.World, call)
	if err != nil {
		return err
	}
	if tr.ValidationError != nil {
		return fmt.Errorf("decider intent %q rejected: %s", intentName, tr.ValidationError.Message)
	}

	res.Events = append(res.Events, tr.Events...)
	res.World = tr.World
	res.NewState = tr.NewState
	if tr.View != "" {
		res.View = tr.View
	}

	if len(tr.HostCalls) > 0 {
		he, hw, hv, hr, _ := o.dispatchHostCalls(ctx, sid, tr.HostCalls, res.World, res.NewState)
		res.Events = append(res.Events, he...)
		res.World = hw
		if hv != "" {
			res.View = hv
		}
		if hr != "" {
			res.NewState = hr
		}
	}

	o.settlePostBindEmits(ctx, sid, res, tl, 0)
	return nil
}

// recordGate appends a GateDecided event describing how a gate resolved.
// threshold is recorded so consumers can reproduce the auto-fire decision.
// alternatives, when non-empty, carries the ranked runner-up scores from
// the LLM judge so reviewers can see the full decision landscape.
func (o *Orchestrator) recordGate(res *machine.TurnResult, state app.StatePath, candidates []string, decider, chosen string, confidence float64, bailed bool, reason string, alternatives []judges.IntentScore, threshold float64) {
	payload := map[string]any{
		"state":             string(state),
		"available_intents": candidates,
		"decider":           decider,
		"chosen_intent":     chosen,
		"confidence":        confidence,
		"bailed_to_human":   bailed,
		"threshold":         threshold,
	}
	if reason != "" {
		payload["reason"] = reason
	}
	if len(alternatives) > 0 {
		payload["alternatives"] = alternatives
	}
	res.Events = append(res.Events, newOrchestratorEvent(store.GateDecided, payload, 0))
}

// buildDeciderPrompt synthesises an inline decision prompt from the gate's own
// vocabulary, so a decider works with zero per-room prompt authoring.
func buildDeciderPrompt(def *app.AppDef, state app.StatePath, candidates []machine.AllowedIntent, w world.World) string {
	var b strings.Builder
	b.WriteString("You are the decision operator for an automated workflow.\n\n")
	if st := lookupStateByPath(def, state); st != nil && st.Description != "" {
		fmt.Fprintf(&b, "Current step: %q — %s\n\n", string(state), st.Description)
	} else {
		fmt.Fprintf(&b, "Current step: %q\n\n", string(state))
	}
	b.WriteString("Choose exactly one of the available actions by calling submit with a JSON object {intent, confidence (0.0-1.0), reason}. The `intent` MUST be one of the action names below. If you cannot decide confidently, set intent to \"uncertain\".\n\n")
	b.WriteString("Available actions:\n")
	for _, c := range candidates {
		desc := c.Description
		if desc == "" {
			desc = c.Title
		}
		fmt.Fprintf(&b, "  - %s: %s\n", c.Name, desc)
	}
	return b.String()
}

func candidatePayload(candidates []machine.AllowedIntent) []map[string]any {
	out := make([]map[string]any, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, map[string]any{
			"name":        c.Name,
			"title":       c.Title,
			"description": c.Description,
		})
	}
	return out
}

func candidateNames(candidates []machine.AllowedIntent) []string {
	out := make([]string, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, c.Name)
	}
	return out
}

func containsStr(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
