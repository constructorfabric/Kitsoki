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
	"encoding/json"
	"regexp"
	"strings"

	goyaml "github.com/goccy/go-yaml"
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
	s.View = rw.rewriteView(s.View)
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
	//
	// Each renamed key (e.g. `accept` → `bf__accept`) is also recorded
	// in s.IntentAliases so the runtime emit_intent dispatcher can
	// resolve a bare LLM-emitted name against the actually-renamed arc.
	// On a second fold (e.g. `bf__accept` → `core__bf__accept`), the
	// existing alias entries that pointed to the now-stale name are
	// chased forward to the new fully-prefixed key, and the
	// intermediate name is itself recorded as an alias — so a state
	// that has been folded N times answers to any of N+1 spellings of
	// the same intent.
	if len(s.On) > 0 {
		newOn := make(map[string][]Transition, len(s.On))
		for intent, list := range s.On {
			newIntent := rw.rewriteIntentRef(intent)
			for i := range list {
				rw.rewriteTransition(&list[i])
			}
			newOn[newIntent] = list
			if newIntent != intent {
				if s.IntentAliases == nil {
					s.IntentAliases = make(map[string]string)
				}
				// Chase forward: any existing alias entries that point
				// to the pre-rewrite name (e.g. accept → bf__accept
				// from the prior fold) now point to the post-rewrite
				// name (accept → core__bf__accept).
				for k, v := range s.IntentAliases {
					if v == intent {
						s.IntentAliases[k] = newIntent
					}
				}
				// Record the bare → renamed mapping for THIS fold.
				// On the first fold this captures e.g.
				// `accept → bf__accept`; on the second, it captures
				// `bf__accept → core__bf__accept` so the intermediate
				// name remains resolvable too.
				s.IntentAliases[intent] = newIntent
			}
		}
		s.On = newOn
	}

	// DefaultIntent is an intent ref (the state's free-text sink); prefix it
	// like any other so the folded state names the renamed arc directly. The
	// orchestrator also resolves it through IntentAliases at runtime, so a bare
	// name still works, but rewriting here keeps the folded def self-consistent.
	if s.DefaultIntent != "" {
		s.DefaultIntent = rw.rewriteIntentRef(s.DefaultIntent)
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
	tr.View = rw.rewriteView(tr.View)
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

	// emit_intent: the value is a template (e.g.
	// "{{ world.llm_verdict.intent }}") whose evaluation result names
	// the intent to dispatch. world.X refs inside MUST be rewritten so
	// the rendered intent reads the prefixed world key after fold.
	// Without this, an autonomous bugfix walk under dev-story / kitsoki-
	// dev evaluates world.llm_verdict against an empty key and the
	// auto-fire silently no-ops (Wave 3 / Phase 3 hit this on the
	// pickup_autonomous_then_bail flow).
	eff.EmitIntent = rw.rewriteExpr(eff.EmitIntent)

	// emit_intent slot values are templates too; rewrite each.
	if len(eff.EmitSlots) > 0 {
		newSlots := make(map[string]any, len(eff.EmitSlots))
		for k, v := range eff.EmitSlots {
			newSlots[k] = rw.rewriteAny(v)
		}
		eff.EmitSlots = newSlots
	}

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

// rewriteView applies rewriteExpr to every author-supplied template
// string inside a View — Source on the scalar form, and every element
// leaf (Source, list labels/hints, kv pair values) on the array /
// {extends, blocks} forms. The scalar form is rebuilt via LegacyView so
// Elements stays in sync with the rewritten Source.
func (rw *childRewriter) rewriteView(v View) View {
	if rw == nil {
		return v
	}
	if v.IsEmpty() && v.Source == "" {
		return v
	}
	if v.Source != "" {
		return LegacyView(rw.rewriteExpr(v.Source))
	}
	out := View{Extends: v.Extends, TemplateFile: v.TemplateFile}
	if len(v.Elements) > 0 {
		out.Elements = make([]ViewElement, len(v.Elements))
		for i, el := range v.Elements {
			out.Elements[i] = rw.rewriteViewElement(el)
		}
	}
	if len(v.Blocks) > 0 {
		out.Blocks = make(map[string][]ViewElement, len(v.Blocks))
		for name, els := range v.Blocks {
			newEls := make([]ViewElement, len(els))
			for i, el := range els {
				newEls[i] = rw.rewriteViewElement(el)
			}
			out.Blocks[name] = newEls
		}
	}
	return out
}

func (rw *childRewriter) rewriteViewElement(el ViewElement) ViewElement {
	out := ViewElement{
		Kind:     el.Kind,
		Source:   rw.rewriteExpr(el.Source),
		Marker:   el.Marker,
		Subtitle: rw.rewriteExpr(el.Subtitle),
		Color:    el.Color,
		When:     rw.rewriteExpr(el.When),
	}
	if len(el.Items) > 0 {
		out.Items = make([]ListItem, len(el.Items))
		for i, item := range el.Items {
			out.Items[i] = ListItem{
				Label: rw.rewriteExpr(item.Label),
				Hint:  rw.rewriteExpr(item.Hint),
				When:  rw.rewriteExpr(item.When),
			}
		}
	}
	if len(el.Pairs) > 0 {
		pairs := make(goyaml.MapSlice, len(el.Pairs))
		for i, p := range el.Pairs {
			pairs[i] = p
			if s, ok := p.Value.(string); ok {
				pairs[i].Value = rw.rewriteExpr(s)
			}
		}
		out.Pairs = pairs
	}
	// Choice element fields (Phase A). The post-import validate() call
	// re-walks every view, including imported substory rooms, and
	// validateChoice insists on a non-empty ChoiceRaw + typed field
	// shape. Without this propagation the choice-widget canary on
	// robbery breaks every importer of robbery (frontier_event,
	// oregon-trail) at load time. Templated leaves inside item slots
	// / param placeholders / form field defaults are rewritten through
	// rw.rewriteExpr so importer aliases land correctly; the ChoiceRaw
	// JSON is rewritten with the same identifier substitution so the
	// re-validated subtree matches the typed fields.
	if el.Kind == "choice" {
		out.ChoiceMode = el.ChoiceMode
		out.ChoicePrompt = rw.rewriteExpr(el.ChoicePrompt)
		out.ChoiceIntent = rw.rewriteIntentRef(el.ChoiceIntent)
		out.ChoiceSlot = el.ChoiceSlot
		out.ChoiceMin = el.ChoiceMin
		out.ChoiceMax = el.ChoiceMax
		out.ChoiceMinSet = el.ChoiceMinSet
		out.ChoiceMaxSet = el.ChoiceMaxSet
		out.ChoiceTemplate = rw.rewriteExpr(el.ChoiceTemplate)
		if len(el.ChoiceItems) > 0 {
			items := make([]ChoiceItem, len(el.ChoiceItems))
			for i, it := range el.ChoiceItems {
				items[i] = ChoiceItem{
					Value:  it.Value,
					Label:  rw.rewriteExpr(it.Label),
					Hint:   rw.rewriteExpr(it.Hint),
					Intent: rw.rewriteIntentRef(it.Intent),
					When:   rw.rewriteExpr(it.When),
				}
				if len(it.Slots) > 0 {
					slots := make(map[string]any, len(it.Slots))
					for k, v := range it.Slots {
						if s, ok := v.(string); ok {
							slots[k] = rw.rewriteExpr(s)
						} else {
							slots[k] = v
						}
					}
					items[i].Slots = slots
				}
				if it.Param != nil {
					p := *it.Param
					p.Placeholder = rw.rewriteExpr(p.Placeholder)
					items[i].Param = &p
				}
			}
			out.ChoiceItems = items
		}
		if len(el.ChoiceFields) > 0 {
			fields := make([]ChoiceField, len(el.ChoiceFields))
			for i, f := range el.ChoiceFields {
				ff := f
				ff.Hint = rw.rewriteExpr(f.Hint)
				ff.Placeholder = rw.rewriteExpr(f.Placeholder)
				ff.Expr = rw.rewriteExpr(f.Expr)
				ff.When = rw.rewriteExpr(f.When)
				if s, ok := f.Default.(string); ok {
					ff.Default = rw.rewriteExpr(s)
				}
				if s, ok := f.Min.(string); ok {
					ff.Min = rw.rewriteExpr(s)
				}
				if s, ok := f.Max.(string); ok {
					ff.Max = rw.rewriteExpr(s)
				}
				fields[i] = ff
			}
			out.ChoiceFields = fields
		}
		// Preserve the JSON-marshalled raw subtree for the post-import
		// validate() call. We don't re-marshal the rewritten typed
		// fields back to JSON here — the validate() path only consults
		// ChoiceRaw for structural shape (which the rewriter does not
		// change), and reads typed-field invariants from the lifted
		// fields above. If a future importer aliases a templated leaf
		// inside the raw subtree, the typed fields are authoritative
		// for runtime; the raw stays at parent-source identifier
		// shapes, which is fine for the JSON Schema's structural
		// checks.
		if len(el.ChoiceRaw) > 0 {
			raw := make(json.RawMessage, len(el.ChoiceRaw))
			copy(raw, el.ChoiceRaw)
			out.ChoiceRaw = raw
		}
	}
	if el.Kind == "media" {
		out.MediaHandle = el.MediaHandle
		out.MediaCaption = rw.rewriteExpr(el.MediaCaption)
		out.MediaKind = el.MediaKind
		out.MediaPath = rw.rewriteExpr(el.MediaPath)
	}
	return out
}
