// Package app — phase-template expansion.
//
// See docs/stories/state-machine.md "Phase templates" for the authoring model.
//
// Phase templates declare reusable shapes; the `phases:` block instantiates
// the template once per phase. The expander runs at load time, before
// referential validation, and produces concrete State entries in def.States
// with parameter substitution applied.
//
// Substitution rules:
//
//   - State-name keys may reference parameters as `{paramname}`. Example:
//     `"{id}_executing"` with id="phase_0" expands to `"phase_0_executing"`.
//
//   - State body strings may reference parameters as `{{ tpl.paramname }}`
//     and the next-arc graph as `{{ phase.next.<arc> }}`. The latter
//     resolves to `<next_phase_id>_executing` (the template's entry-state
//     suffix); literals like "terminated" pass through unchanged.
//
//   - cycle_budgets: { <arc>: N } synthesizes for each arc:
//
//     1. An Effect.Increment on the arc transition's effects, against
//     a counter `cycle__<phase_id>__<arc>` in world.
//     2. A `when:` guard `world.cycle__<phase_id>__<arc> < N` on the arc.
//     3. A trailing fall-through transition with `default: true` and
//     target `<phase_id>_error`, carrying the guard hint.
//
// `host.transport.post` invocations inside the template are not interpreted
// here — the loader only substitutes; the orchestrator dispatches.
package app

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	goyaml "github.com/goccy/go-yaml"
)

// expandPhases populates def.States from def.PhaseTemplates + def.Phases.
// Returns ValidationError-shaped errors for missing templates, missing
// required parameters, name collisions, and malformed substitutions.
//
// If def.Phases is nil this is a no-op.
func expandPhases(def *AppDef, file string) []error {
	if def.Phases == nil || len(def.Phases.Graph) == 0 {
		return nil
	}
	if def.PhaseTemplates == nil {
		return []error{&ValidationError{File: file, Message: "phases: declared but no phase_templates: present"}}
	}

	tplName := def.Phases.Template
	if tplName == "" {
		return []error{&ValidationError{File: file, Message: "phases.template: required"}}
	}
	tpl, ok := def.PhaseTemplates[tplName]
	if !ok {
		return []error{&ValidationError{File: file, Message: fmt.Sprintf("phases.template %q: not found in phase_templates", tplName)}}
	}

	if def.States == nil {
		def.States = make(map[string]*State)
	}

	// Iterate phase IDs in deterministic order so error messages and
	// expanded state ordering are stable.
	phaseIDs := make([]string, 0, len(def.Phases.Graph))
	for id := range def.Phases.Graph {
		phaseIDs = append(phaseIDs, id)
	}
	sort.Strings(phaseIDs)

	var errs []error
	for _, phaseID := range phaseIDs {
		entry := def.Phases.Graph[phaseID]
		if entry == nil {
			errs = append(errs, &ValidationError{File: file, Message: fmt.Sprintf("phases.graph[%q]: empty entry", phaseID)})
			continue
		}
		if e := expandOnePhase(def, file, tpl, phaseID, entry); len(e) > 0 {
			errs = append(errs, e...)
		}
	}
	return errs
}

// expandOnePhase generates the states for one phase entry.
func expandOnePhase(def *AppDef, file string, tpl *PhaseTemplate, phaseID string, entry map[string]any) []error {
	// Build the parameter table: defaults < entry overrides < phase id.
	params := make(map[string]any, len(tpl.Parameters)+len(entry))

	// Defaults from template parameter declarations.
	for name, p := range tpl.Parameters {
		if p.Default != nil {
			params[name] = p.Default
		}
	}

	// Override with entry values, except for the structured keys.
	for k, v := range entry {
		switch k {
		case "next", "cycle_budgets", "on_enter":
			// Structured; not a template param.
			continue
		default:
			params[k] = v
		}
	}

	// `id` is always the phase ID (overrides any explicit value to keep
	// state-name uniqueness invariant).
	params["id"] = phaseID

	// Verify required params are populated.
	var errs []error
	requiredNames := make([]string, 0, len(tpl.Parameters))
	for name := range tpl.Parameters {
		requiredNames = append(requiredNames, name)
	}
	sort.Strings(requiredNames)
	for _, name := range requiredNames {
		p := tpl.Parameters[name]
		if !p.Required {
			continue
		}
		if _, ok := params[name]; !ok {
			errs = append(errs, &ValidationError{
				File:    file,
				Message: fmt.Sprintf("phases.graph[%q]: missing required template parameter %q", phaseID, name),
			})
		}
	}
	if len(errs) > 0 {
		return errs
	}

	// Pull structured next/cycle_budgets.
	next := pullStringMap(entry, "next")
	cycles := pullIntMap(entry, "cycle_budgets")

	// Pull the optional per-phase on_enter override. When present, it
	// REPLACES the template's on_enter on the {id}_executing state. This is
	// how script-driven phases (e.g. host.run) override LLM-driven templates
	// without having to declare a separate phase template. Substitution
	// against `params` and `next` still applies — the override may reference
	// `{{ tpl.X }}` and `{{ phase.next.X }}` like the template body.
	onEnterOverride, oeErr := pullEffectList(entry, "on_enter")
	if oeErr != nil {
		errs = append(errs, &ValidationError{
			File:    file,
			Message: fmt.Sprintf("phases.graph[%q]: on_enter: %v", phaseID, oeErr),
		})
		return errs
	}

	// Iterate template state names in deterministic order.
	tplStateNames := make([]string, 0, len(tpl.States))
	for name := range tpl.States {
		tplStateNames = append(tplStateNames, name)
	}
	sort.Strings(tplStateNames)

	for _, tplName := range tplStateNames {
		expandedName, err := substituteStateName(tplName, params)
		if err != nil {
			errs = append(errs, &ValidationError{
				File:    file,
				Message: fmt.Sprintf("phases.graph[%q]: state name %q: %v", phaseID, tplName, err),
			})
			continue
		}
		if existing, exists := def.States[expandedName]; exists {
			// Hand-written state overrides templated expansion. The state
			// already declared in the top-level `states:` block wins for
			// its body (on_enter, on, view, …). Override is implicit —
			// just declare the state in `states:`. No new YAML keyword.
			//
			// Cycle budgets are an exception. When the phase declares
			// `cycle_budgets:` and the override is `_executing`, the
			// synthesized retry arcs MUST still apply: they protect the
			// state machine from runaway loops independently of whatever
			// the hand-written body looks like, and `applyCycleBudgets`
			// merges into existing arcs idempotently (existing template
			// arcs get guard-decorated; arcs the hand-written state
			// doesn't declare get fresh retry/error transitions).
			// Silently dropping them was a real gap — see
			// `TestPhases_HandWritten_ExecutingOverride_AppliesCycleBudgets`.
			//
			// Other overrides (`_awaiting_reply`, `_error`) don't carry
			// per-phase retry arcs, so they stay a pure skip.
			if strings.HasSuffix(expandedName, "_executing") && len(cycles) > 0 {
				if cerr := applyCycleBudgets(existing, phaseID, cycles, next); cerr != nil {
					errs = append(errs, &ValidationError{
						File:    file,
						Message: fmt.Sprintf("phases.graph[%q]: %v", phaseID, cerr),
					})
				}
			}
			continue
		}

		// Deep-copy the template state so we can substitute without
		// mutating the original template (other phase entries reuse it).
		instance, err := substituteState(tpl.States[tplName], params, next)
		if err != nil {
			errs = append(errs, &ValidationError{
				File:    file,
				Message: fmt.Sprintf("phases.graph[%q]: state %q: %v", phaseID, tplName, err),
			})
			continue
		}

		// Synthesize cycle_budgets for arcs declared on this phase.
		// We only modify the "_executing" state (the canonical entry
		// point for arc dispatch). Awaiting-reply states only carry
		// continue/quit/refine, not the per-phase arcs.
		if strings.HasSuffix(expandedName, "_executing") {
			if cerr := applyCycleBudgets(instance, phaseID, cycles, next); cerr != nil {
				errs = append(errs, &ValidationError{
					File:    file,
					Message: fmt.Sprintf("phases.graph[%q]: %v", phaseID, cerr),
				})
				continue
			}

			// Apply per-phase on_enter override (if any). Substitution
			// is applied against the phase's params + next-arc graph so
			// the override may use `{{ tpl.X }}` / `{{ world.X }}` / etc.
			// just like the template body.
			if onEnterOverride != nil {
				out := make([]Effect, 0, len(onEnterOverride))
				for _, eff := range onEnterOverride {
					out = append(out, substEffect(eff, params, next))
				}
				instance.OnEnter = out
			}
		}

		// Merge checkpoint_intents into _awaiting_reply states.
		if strings.HasSuffix(expandedName, "_awaiting_reply") && len(def.CheckpointIntents) > 0 {
			if instance.Intents == nil {
				instance.Intents = make(map[string]Intent)
			}
			for k, v := range def.CheckpointIntents {
				if _, present := instance.Intents[k]; !present {
					instance.Intents[k] = v
				}
			}
		}

		def.States[expandedName] = instance
	}

	return errs
}

// stateNameRE matches `{paramname}` placeholders inside a state name.
var stateNameRE = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// substituteStateName replaces `{paramname}` placeholders.
func substituteStateName(name string, params map[string]any) (string, error) {
	var subErr error
	out := stateNameRE.ReplaceAllStringFunc(name, func(match string) string {
		key := match[1 : len(match)-1] // strip braces
		v, ok := params[key]
		if !ok {
			subErr = fmt.Errorf("unknown parameter %q", key)
			return match
		}
		return fmt.Sprint(v)
	})
	return out, subErr
}

// substituteState deep-copies tpl, expanding `{{ tpl.X }}` and
// `{{ phase.next.X }}` placeholders inside string fields and recursively in
// nested transitions/effects.
func substituteState(tpl *State, params map[string]any, next map[string]string) (*State, error) {
	if tpl == nil {
		return nil, nil
	}
	out := &State{
		Type:          tpl.Type,
		Mode:          tpl.Mode,
		Description:   substString(tpl.Description, params, next),
		View:          substView(tpl.View, params, next),
		Terminal:      tpl.Terminal,
		Initial:       substString(tpl.Initial, params, next),
		RelevantWorld: append([]string(nil), tpl.RelevantWorld...),
		RelevantSlots: append([]string(nil), tpl.RelevantSlots...),
		Menu:          append([]string(nil), tpl.Menu...),
	}
	if tpl.Timeout != nil {
		t := *tpl.Timeout
		t.After = substString(t.After, params, next)
		t.Target = substString(t.Target, params, next)
		out.Timeout = &t
	}

	if len(tpl.OnEnter) > 0 {
		out.OnEnter = make([]Effect, 0, len(tpl.OnEnter))
		for _, eff := range tpl.OnEnter {
			out.OnEnter = append(out.OnEnter, substEffect(eff, params, next))
		}
	}

	if len(tpl.On) > 0 {
		out.On = make(map[string][]Transition, len(tpl.On))
		for arc, transitions := range tpl.On {
			expandedArc := substString(arc, params, next)
			tgts := make([]Transition, 0, len(transitions))
			for _, tr := range transitions {
				tgts = append(tgts, substTransition(tr, params, next))
			}
			out.On[expandedArc] = tgts
		}
	}

	if len(tpl.Intents) > 0 {
		out.Intents = make(map[string]Intent, len(tpl.Intents))
		for k, v := range tpl.Intents {
			out.Intents[k] = v
		}
	}

	// Nested States: not expected inside phase templates today; pass through.
	if len(tpl.States) > 0 {
		out.States = make(map[string]*State, len(tpl.States))
		for name, child := range tpl.States {
			expanded, err := substituteState(child, params, next)
			if err != nil {
				return nil, err
			}
			out.States[name] = expanded
		}
	}

	return out, nil
}

func substTransition(tr Transition, params map[string]any, next map[string]string) Transition {
	out := Transition{
		Target:    substString(tr.Target, params, next),
		When:      substString(tr.When, params, next),
		Default:   tr.Default,
		GuardHint: substString(tr.GuardHint, params, next),
		View:      substView(tr.View, params, next),
		Emit:      append([]string(nil), tr.Emit...),
	}
	if tr.PushHistory != nil {
		v := *tr.PushHistory
		out.PushHistory = &v
	}
	if len(tr.Effects) > 0 {
		out.Effects = make([]Effect, 0, len(tr.Effects))
		for _, eff := range tr.Effects {
			out.Effects = append(out.Effects, substEffect(eff, params, next))
		}
	}
	return out
}

func substEffect(eff Effect, params map[string]any, next map[string]string) Effect {
	out := Effect{
		When:    substString(eff.When, params, next),
		Invoke:  substString(eff.Invoke, params, next),
		Say:     substString(eff.Say, params, next),
		Emit:    substString(eff.Emit, params, next),
		OnError: substString(eff.OnError, params, next),
		// Background is a plain bool; no template substitution.
		// Without copying it through, a per-phase on_enter: override
		// declaring `background: true` would silently dispatch
		// synchronously and HostInvoked would fire with Background=false.
		Background: eff.Background,
		// Target is a state name; treat it like Invoke — it may carry
		// `{{ tpl.X }}` / `{{ phase.next.X }}` references (e.g.
		// `target: "{{ phase.next.continue }}"` inside on_complete).
		// substString returns "" for "", so an unset Target stays "".
		Target: substString(eff.Target, params, next),
	}
	if len(eff.Set) > 0 {
		out.Set = make(map[string]any, len(eff.Set))
		for k, v := range eff.Set {
			out.Set[substString(k, params, next)] = substAnyString(v, params, next)
		}
	}
	if len(eff.Increment) > 0 {
		out.Increment = make(map[string]int, len(eff.Increment))
		for k, v := range eff.Increment {
			out.Increment[substString(k, params, next)] = v
		}
	}
	if len(eff.With) > 0 {
		out.With = make(map[string]any, len(eff.With))
		for k, v := range eff.With {
			out.With[k] = substAnyString(v, params, next)
		}
	}
	if len(eff.Bind) > 0 {
		out.Bind = make(map[string]string, len(eff.Bind))
		for k, v := range eff.Bind {
			out.Bind[substString(k, params, next)] = substString(v, params, next)
		}
	}
	// OnComplete is a list of follow-up effects fired when a background
	// job terminates. Recurse so each child gets full substitution —
	// including its own Target / nested OnComplete / With / etc. Order
	// is preserved by appending in iteration order.
	if len(eff.OnComplete) > 0 {
		out.OnComplete = make([]Effect, 0, len(eff.OnComplete))
		for _, child := range eff.OnComplete {
			out.OnComplete = append(out.OnComplete, substEffect(child, params, next))
		}
	}
	return out
}

func substAnyString(v any, params map[string]any, next map[string]string) any {
	switch x := v.(type) {
	case string:
		return substString(x, params, next)
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = substAnyString(item, params, next)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, item := range x {
			out[k] = substAnyString(item, params, next)
		}
		return out
	default:
		return v
	}
}

// substView applies the same `{{ tpl.X }}` / `{{ phase.next.X }}` rewrites
// as substString to every author-supplied string inside a View. The scalar
// string form (View.Source non-empty) is rebuilt via LegacyView so the
// substituted source text round-trips through the normalised element. The
// array and {extends, blocks} forms walk each element's string fields
// (prose/heading/code/template Source, list labels/hints, kv pair string
// values).
func substView(v View, params map[string]any, next map[string]string) View {
	if v.IsEmpty() && v.Source == "" {
		return View{}
	}
	// Scalar string form: substitute and re-wrap.
	if v.Source != "" {
		return LegacyView(substString(v.Source, params, next))
	}
	// Array / inheritance forms: walk elements.
	out := View{Extends: v.Extends}
	if len(v.Elements) > 0 {
		out.Elements = make([]ViewElement, len(v.Elements))
		for i, el := range v.Elements {
			out.Elements[i] = substViewElement(el, params, next)
		}
	}
	if len(v.Blocks) > 0 {
		out.Blocks = make(map[string][]ViewElement, len(v.Blocks))
		for name, els := range v.Blocks {
			newEls := make([]ViewElement, len(els))
			for i, el := range els {
				newEls[i] = substViewElement(el, params, next)
			}
			out.Blocks[name] = newEls
		}
	}
	return out
}

func substViewElement(el ViewElement, params map[string]any, next map[string]string) ViewElement {
	out := ViewElement{
		Kind:     el.Kind,
		Source:   substString(el.Source, params, next),
		Marker:   el.Marker,
		Subtitle: substString(el.Subtitle, params, next),
		Color:    el.Color,
		When:     substString(el.When, params, next),
		// Media fields — handle and kind are not param-substituted (they
		// come from world slots via pongo2 at render time); pass through as-is.
		MediaHandle:          el.MediaHandle,
		MediaCaption:         substString(el.MediaCaption, params, next),
		MediaKind:            el.MediaKind,
		MediaPath:            substString(el.MediaPath, params, next),
		AnnotateIntent:       el.AnnotateIntent,
		AnnotateFeedbackSlot: el.AnnotateFeedbackSlot,
	}
	if len(el.Items) > 0 {
		out.Items = make([]ListItem, len(el.Items))
		for i, item := range el.Items {
			out.Items[i] = ListItem{
				Label: substString(item.Label, params, next),
				Hint:  substString(item.Hint, params, next),
				When:  substString(item.When, params, next),
			}
		}
	}
	if len(el.Pairs) > 0 {
		pairs := make(goyaml.MapSlice, len(el.Pairs))
		for i, p := range el.Pairs {
			pairs[i] = p
			if s, ok := p.Value.(string); ok {
				pairs[i].Value = substString(s, params, next)
			}
		}
		out.Pairs = pairs
	}
	return out
}

// templateRE matches `{{ tpl.X }}` and `{{ phase.next.X }}` inside a single string.
// Whitespace inside the braces is tolerated.
var templateRE = regexp.MustCompile(`\{\{\s*(tpl|phase)\.([A-Za-z_][A-Za-z0-9_]*)(?:\.([A-Za-z_][A-Za-z0-9_]*))?\s*\}\}`)

// substString applies all `{{ tpl.X }}` / `{{ phase.next.X }}` substitutions
// to a single string. Unknown references pass through unchanged so app
// authors can mix template substitution with expr-lang strings.
func substString(s string, params map[string]any, next map[string]string) string {
	if s == "" {
		return ""
	}
	return templateRE.ReplaceAllStringFunc(s, func(match string) string {
		groups := templateRE.FindStringSubmatch(match)
		// groups[1] = "tpl" | "phase"
		// groups[2] = first identifier after dot
		// groups[3] = second identifier (optional)
		switch groups[1] {
		case "tpl":
			if v, ok := params[groups[2]]; ok {
				return fmt.Sprint(v)
			}
		case "phase":
			if groups[2] == "next" && groups[3] != "" {
				if target, ok := next[groups[3]]; ok {
					return rewritePhaseTarget(target)
				}
			}
		}
		return match
	})
}

// rewritePhaseTarget converts a phase ID into its concrete entry-state name.
// "phase_0" → "phase_0_executing"; "terminated" / "ended" pass through.
// Names beginning with `ended_` (e.g. `ended_won`, `ended_lost`) also pass
// through unchanged so authors can declare multiple terminal endings without
// the expander pretending they're phases.
func rewritePhaseTarget(t string) string {
	switch t {
	case "", "terminated", "ended", "completed", "abandoned", "failed":
		return t
	}
	if strings.HasPrefix(t, "ended_") {
		return t
	}
	return t + "_executing"
}

// applyCycleBudgets mutates state.On so each arc with a declared cycle budget
// gets {Increment + when-guard + fall-through-to-error}.
//
// For each arc with budget N we:
//
//   - If the template already wrote a transition for that arc, decorate
//     each non-default branch with `cycle__<phase>__<arc> < N` and prepend
//     an Effect.Increment of the counter. Append a default fall-through
//     to <phase>_error.
//   - If the template did not write a transition (the common case for
//     bug-fix's L2 feedback arcs), and `next[arc]` declares a target,
//     synthesize a fresh transition pair: success branch carrying the
//     bound + increment + target, plus the error fall-through.
//   - If neither template nor `next[arc]` provides a target, the arc only
//     gets the error fall-through (so the budget exceeded path is at
//     least observable, and the runtime will reject any external call to
//     that arc once the cycle is exhausted).
func applyCycleBudgets(state *State, phaseID string, cycles map[string]int, next map[string]string) error {
	if len(cycles) == 0 {
		return nil
	}
	if state.On == nil {
		state.On = make(map[string][]Transition)
	}
	errs := make([]string, 0)
	for arc, n := range cycles {
		if n <= 0 {
			errs = append(errs, fmt.Sprintf("cycle_budgets[%q] must be > 0", arc))
			continue
		}
		counter := "cycle__" + phaseID + "__" + arc
		// expr-lang's whitelist requires bare identifiers to live under
		// one of the allowed roots (slots/world/event/run/args). The
		// counter slot lives in `world`, so prefix the guard with
		// `world.` — without the prefix expr.Render fails at eval-time
		// with "cannot fetch <counter> from expr.Env".
		guard := "world." + counter + " < " + strconv.Itoa(n)
		errorTarget := phaseID + "_error"

		existing, present := state.On[arc]
		if !present || len(existing) == 0 {
			// No template transition. If the phase declares a next-arc
			// retry target, synthesize the success branch with bound +
			// increment. Otherwise just emit the error fall-through.
			retryTarget := ""
			if next != nil {
				if t, ok := next[arc]; ok && t != "" {
					retryTarget = rewritePhaseTarget(t)
				}
			}
			if retryTarget == "" {
				state.On[arc] = []Transition{{
					Target:    errorTarget,
					Default:   true,
					GuardHint: "cycle budget exceeded for " + arc,
				}}
				continue
			}
			state.On[arc] = []Transition{
				{
					Target:  retryTarget,
					When:    guard,
					Effects: []Effect{{Increment: map[string]int{counter: 1}}},
				},
				{
					Target:    errorTarget,
					Default:   true,
					GuardHint: "cycle budget exceeded for " + arc,
				},
			}
			continue
		}

		// Template already had transitions for this arc — decorate each
		// non-default branch and append the error fall-through.
		out := make([]Transition, 0, len(existing)+1)
		for _, tr := range existing {
			if tr.Default {
				out = append(out, tr)
				continue
			}
			tr.When = combineGuards(tr.When, guard)
			tr.Effects = append([]Effect{{
				Increment: map[string]int{counter: 1},
			}}, tr.Effects...)
			out = append(out, tr)
		}
		out = append(out, Transition{
			Target:    errorTarget,
			Default:   true,
			GuardHint: "cycle budget exceeded for " + arc,
		})
		state.On[arc] = out
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// combineGuards returns "(a) && (b)" when both are present, or whichever is
// non-empty. Wrapping in parens keeps expr-lang precedence safe.
func combineGuards(a, b string) string {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	switch {
	case a == "" && b == "":
		return ""
	case a == "":
		return b
	case b == "":
		return a
	default:
		return "(" + a + ") && (" + b + ")"
	}
}

// pullEffectList reads m[key] (expected to be a list of effect maps) and
// re-marshals through goccy/go-yaml so the result is typed as []Effect.
//
// Returns (nil, nil) when the key is absent. When the key is present but
// not a list, returns an error so the load fails loudly with a useful
// message instead of silently dropping the override.
func pullEffectList(m map[string]any, key string) ([]Effect, error) {
	v, ok := m[key]
	if !ok {
		return nil, nil
	}
	if v == nil {
		return nil, nil
	}
	if _, ok := v.([]any); !ok {
		return nil, fmt.Errorf("expected a list of effects, got %T", v)
	}
	// Round-trip through YAML so goccy/go-yaml does the typed decoding for
	// us. Cheaper than reimplementing the Effect schema by hand and keeps
	// behaviour aligned with the loader's primary parse.
	b, err := goyaml.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	var out []Effect
	if err := goyaml.UnmarshalWithOptions(b, &out, goyaml.Strict()); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	return out, nil
}

// pullStringMap reads m[key] and returns it as map[string]string.
func pullStringMap(m map[string]any, key string) map[string]string {
	v, ok := m[key]
	if !ok {
		return nil
	}
	raw, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(raw))
	for k, vv := range raw {
		out[k] = fmt.Sprint(vv)
	}
	return out
}

// pullIntMap reads m[key] and returns it as map[string]int.
func pullIntMap(m map[string]any, key string) map[string]int {
	v, ok := m[key]
	if !ok {
		return nil
	}
	raw, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]int, len(raw))
	for k, vv := range raw {
		switch x := vv.(type) {
		case int:
			out[k] = x
		case int64:
			out[k] = int(x)
		case uint64:
			out[k] = int(x)
		case float64:
			out[k] = int(x)
		default:
			if n, err := strconv.Atoi(fmt.Sprint(vv)); err == nil {
				out[k] = n
			}
		}
	}
	return out
}
