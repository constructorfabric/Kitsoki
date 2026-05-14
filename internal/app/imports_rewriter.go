// Package app — childRewriter rewrites every expression and identifier
// reference inside a child app before it is folded into the parent.
//
// Why rewrite rather than scope tag: the runtime expr-eval engine reads
// against a flat `world` map per evaluation. Splitting evaluation into
// per-alias scopes would require touching every effect/transition/guard
// dispatch site. Instead we flatten child world keys under
// <alias>__<key> and rewrite every `world.<key>` reference inside the
// child's bodies. Net effect: the runtime sees one app with one world;
// the loader does all the bookkeeping.
//
// Rewrite targets:
//
//   - Expressions inside `{{ … }}` braces (views, view templates,
//     transition.view, effect.say, slot.prompt, etc.) — every reference
//     to `world.<key>` is rewritten to `world.<alias>__<key>` for each
//     <key> declared in the child's world schema.
//   - Bare guard expressions (transition.when, effect.when) — same
//     identifier rewrite, no braces required.
//   - Effect.Set keys — rewritten to <alias>__<key> when <key> is in
//     the child's world schema. (Set RHS values, when strings, also
//     run through the expression rewriter.)
//   - On: intent-name keys — rewritten to <alias>__<intent> for each
//     intent declared in the child's intent table. Parent-exported
//     intents (imp.Intents.Export) are also rewritten to the alias-
//     prefixed mirror name; the parent's intent table holds both the
//     bare and the prefixed entry so the lookup hits.
//   - Effect.Invoke — host names are global, no rewrite needed. But
//     `with: { agent: <name> }` references are rewritten when <name>
//     is one of the child's agents.
package app

import (
	"regexp"
	"strings"
)

// childRewriter holds the lookup tables used to rewrite a child AppDef
// before it is folded under an alias.
type childRewriter struct {
	alias                 string
	childWorldKey         map[string]struct{}
	childIntent           map[string]struct{}
	childAgent            map[string]struct{}
	childIface            map[string]struct{}
	parentExportedIntents map[string]struct{}
}

// rewriteState mutates s in place: every expression / identifier / target
// reference is rewritten so the state, once placed under <alias>/..., reads
// against the correctly-prefixed world keys, intent names, and agents.
//
// Returns silently on nil input.
func (rw *childRewriter) rewriteState(s *State) {
	if rw == nil || s == nil {
		return
	}
	// View, Description.
	s.View = rw.rewriteExpr(s.View)
	s.Description = rw.rewriteExpr(s.Description)
	// Initial (templated child name).
	s.Initial = rw.rewriteExpr(s.Initial)

	// RelevantWorld: child wrote bare child-world keys; prefix them.
	if len(s.RelevantWorld) > 0 {
		out := make([]string, len(s.RelevantWorld))
		for i, k := range s.RelevantWorld {
			if _, ok := rw.childWorldKey[k]; ok {
				out[i] = rw.alias + "__" + k
			} else {
				out[i] = k
			}
		}
		s.RelevantWorld = out
	}

	// On: rewrite intent-name keys and the transition list.
	if len(s.On) > 0 {
		newOn := make(map[string][]Transition, len(s.On))
		for intent, list := range s.On {
			newIntent := rw.rewriteIntentRef(intent)
			for i := range list {
				rw.rewriteTransition(&list[i])
			}
			newOn[newIntent] = list
		}
		s.On = newOn
	}

	// OnEnter.
	for i := range s.OnEnter {
		rw.rewriteEffect(&s.OnEnter[i])
	}

	// Local intents.
	if len(s.Intents) > 0 {
		// State.Intents are addressable by bare name inside this state's
		// On: only — and that On: now has prefixed keys. To keep them in
		// sync we rewrite the local-intent map keys to prefixed names too.
		newIntents := make(map[string]Intent, len(s.Intents))
		for name, intent := range s.Intents {
			rw.rewriteIntent(&intent)
			newIntents[rw.rewriteIntentRef(name)] = intent
		}
		s.Intents = newIntents
	}

	// Menu items reference intent names — prefix where applicable.
	if len(s.Menu) > 0 {
		out := make([]string, len(s.Menu))
		for i, m := range s.Menu {
			out[i] = rw.rewriteIntentRef(m)
		}
		s.Menu = out
	}

	// Timeout.After is a duration; .Target is a state ref (left to
	// rewriteChildStateTransitions). No rewriting required here.

	// Recurse into nested states (compound/parallel).
	for _, c := range s.States {
		rw.rewriteState(c)
	}
}

// rewriteTransition walks one Transition and rewrites every string field
// (When, GuardHint, View) plus every Effect.
func (rw *childRewriter) rewriteTransition(tr *Transition) {
	if tr == nil {
		return
	}
	tr.When = rw.rewriteExpr(tr.When)
	tr.GuardHint = rw.rewriteExpr(tr.GuardHint)
	tr.View = rw.rewriteExpr(tr.View)
	for i := range tr.Effects {
		rw.rewriteEffect(&tr.Effects[i])
	}
}

// rewriteEffect rewrites every expression / identifier reference inside
// an Effect (in place).
func (rw *childRewriter) rewriteEffect(eff *Effect) {
	if eff == nil {
		return
	}
	eff.When = rw.rewriteExpr(eff.When)
	eff.Say = rw.rewriteExpr(eff.Say)

	// iface.<X>.<op> references: prefix the iface name with the alias so
	// the deferred resolution at top-level Load() can find the lifted
	// iface declaration under parent.HostInterfaces[<alias>__<X>].
	// Concrete-handler rewriting happens later, after all folds complete,
	// in resolveAllInterfaces — that's what enables multi-layer binding
	// composition (a grandparent can rebind a grandchild's iface).
	if strings.HasPrefix(eff.Invoke, "iface.") {
		rest := strings.TrimPrefix(eff.Invoke, "iface.")
		if dot := strings.IndexByte(rest, '.'); dot > 0 {
			name := rest[:dot]
			op := rest[dot+1:]
			if _, isChildIface := rw.childIface[name]; isChildIface {
				eff.Invoke = "iface." + rw.alias + "__" + name + "." + op
			}
		}
	}

	// Set: keys reference world keys (LHS); values may be expressions.
	if len(eff.Set) > 0 {
		newSet := make(map[string]any, len(eff.Set))
		for k, v := range eff.Set {
			newKey := k
			if _, ok := rw.childWorldKey[k]; ok {
				newKey = rw.alias + "__" + k
			}
			newSet[newKey] = rw.rewriteAny(v)
		}
		eff.Set = newSet
	}
	// Increment: keys reference world keys (LHS only).
	if len(eff.Increment) > 0 {
		newInc := make(map[string]int, len(eff.Increment))
		for k, v := range eff.Increment {
			newKey := k
			if _, ok := rw.childWorldKey[k]; ok {
				newKey = rw.alias + "__" + k
			}
			newInc[newKey] = v
		}
		eff.Increment = newInc
	}

	// With: arg values may be expressions; the `agent` arg is rewritten
	// when it names a child agent.
	if len(eff.With) > 0 {
		newWith := make(map[string]any, len(eff.With))
		for k, v := range eff.With {
			if k == "agent" {
				if name, ok := v.(string); ok && !strings.Contains(name, "{{") {
					if _, isChild := rw.childAgent[name]; isChild {
						newWith[k] = rw.alias + "__" + name
						continue
					}
				}
			}
			newWith[k] = rw.rewriteAny(v)
		}
		eff.With = newWith
	}

	// Bind: keys are world keys, values are result keys (no rewrite).
	if len(eff.Bind) > 0 {
		newBind := make(map[string]string, len(eff.Bind))
		for k, v := range eff.Bind {
			newKey := k
			if _, ok := rw.childWorldKey[k]; ok {
				newKey = rw.alias + "__" + k
			}
			newBind[newKey] = v
		}
		eff.Bind = newBind
	}

	// Nested on_complete effects.
	for i := range eff.OnComplete {
		rw.rewriteEffect(&eff.OnComplete[i])
	}
}

// rewriteIntent rewrites every expression / identifier reference inside an
// Intent definition (description, examples, slot prompts, slot validators).
func (rw *childRewriter) rewriteIntent(in *Intent) {
	if in == nil {
		return
	}
	in.Description = rw.rewriteExpr(in.Description)
	in.Title = rw.rewriteExpr(in.Title)
	for i := range in.Examples {
		in.Examples[i] = rw.rewriteExpr(in.Examples[i])
	}
	for name, slot := range in.Slots {
		slot.Description = rw.rewriteExpr(slot.Description)
		slot.Prompt = rw.rewriteExpr(slot.Prompt)
		slot.Validator = rw.rewriteExpr(slot.Validator)
		slot.FormatHint = rw.rewriteExpr(slot.FormatHint)
		in.Slots[name] = slot
	}
}

// rewriteIntentRef returns the rewritten form of a bare intent name
// reference (used in On: map keys, Menu lists, etc.):
//
//   - A name in childIntent → <alias>__<name>.
//   - A name in parentExportedIntents → <alias>__<name> (the mirror entry
//     installed by foldChild step 10).
//   - Anything else (wildcards, parent-global intents not exported) is
//     left untouched.
func (rw *childRewriter) rewriteIntentRef(name string) string {
	if name == "" || name == "*" {
		return name
	}
	if _, ok := rw.childIntent[name]; ok {
		return rw.alias + "__" + name
	}
	if _, ok := rw.parentExportedIntents[name]; ok {
		return rw.alias + "__" + name
	}
	return name
}

// rewriteAny applies rewriteExpr to string leaves of any (nested values
// in Set:, With:, etc.). Maps and slices recurse; non-string leaves pass
// through.
func (rw *childRewriter) rewriteAny(v any) any {
	switch x := v.(type) {
	case string:
		return rw.rewriteExpr(x)
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, vv := range x {
			out[k] = rw.rewriteAny(vv)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, vv := range x {
			out[i] = rw.rewriteAny(vv)
		}
		return out
	default:
		return v
	}
}

// worldIdentRE matches `world.<ident>` where <ident> is a Go/YAML-style
// identifier. We only rewrite identifiers that are declared in the
// child's world schema — everything else passes through unchanged so
// re-exported parent intents and parent-world refs (introduced via
// world_in projections at parent scope) are left alone.
var worldIdentRE = regexp.MustCompile(`\bworld\.([A-Za-z_][A-Za-z0-9_]*)`)

// rewriteExpr returns s with every `world.<X>` occurrence rewritten to
// `world.<alias>__<X>` when X is a child world key. Non-string-bearing
// inputs (empty) pass through. Works on bare expressions and on
// template-bearing strings ({{ … }}) alike — the regex doesn't care
// about delimiters.
func (rw *childRewriter) rewriteExpr(s string) string {
	if rw == nil || s == "" {
		return s
	}
	if len(rw.childWorldKey) == 0 {
		return s
	}
	return worldIdentRE.ReplaceAllStringFunc(s, func(match string) string {
		// match looks like "world.X" — split at the dot.
		key := strings.TrimPrefix(match, "world.")
		if _, ok := rw.childWorldKey[key]; ok {
			return "world." + rw.alias + "__" + key
		}
		return match
	})
}
