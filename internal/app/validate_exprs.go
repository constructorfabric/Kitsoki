package app

import (
	"fmt"
	"log/slog"
	"strings"

	"kitsoki/internal/expr"
)

// validateExprs compile-checks every effect value and guard expression in the
// loaded AppDef, aggregating all failures into errs. It is the load-time guard
// against the bug class where a malformed expr-lang expression — say a pongo-
// only filter like `{{ x|default:y }}` written into an effect value — parses as
// valid YAML and only explodes mid-turn the first time its transition fires.
// By compiling (never evaluating) each expression at load, an author sees every
// broken expression in one diagnostic the moment the app is loaded.
//
// What it walks, per state (recursing into nested states):
//   - every transition's When guard and every effect inside it;
//   - every on_enter effect;
//   - and within each effect, its own When guard plus the value side of set:,
//     with:, and emit_intent slots:, descending into nested on_complete: and
//     inline effects: chains and into list/map effect values (mirroring the
//     runtime's resolveEffectValue recursion).
//
// Expr-vs-template classification mirrors expr.RenderValue EXACTLY so the
// validator neither false-positives on a valid multi-interpolation template
// (`{{ a }}X{{ b }}`) nor false-negatives on a broken single expression:
//   - a value with no `{{` is a literal — nothing to check;
//   - a value that is a single typed expr (starts `{{`, ends `}}`, inner has
//     no further delimiters, and is not a block keyword) compiles via
//     expr.Compile on the inner text;
//   - anything else is a string template validated via expr.ValidateTemplate.
//
// Guards (When) always compile via expr.CompileBool, matching the runtime.
func validateExprs(file string, def *AppDef, errs *[]error) {
	addErr := func(format string, args ...any) {
		*errs = append(*errs, &ValidationError{File: file, Message: fmt.Sprintf(format, args...)})
	}
	validateStateExprs(file, "", def.States, addErr)
}

// validateStateExprs walks the state tree compiling guards and effect values.
func validateStateExprs(file, prefix string, states map[string]*State, addErr func(string, ...any)) {
	for _, name := range sortedKeys(states) {
		s := states[name]
		if s == nil {
			continue
		}
		statePath := joinPath(prefix, name)

		for i, pr := range s.Prerequisites {
			loc := fmt.Sprintf("prerequisites[%d]", i)
			if pr.When != "" {
				if _, err := expr.CompileBool(pr.When); err != nil {
					addErr("state %q %s: when %q: %v", statePath, loc, pr.When, err)
				}
			}
			if pr.SatisfiedWhen != "" {
				if _, err := expr.CompileBool(pr.SatisfiedWhen); err != nil {
					addErr("state %q %s: satisfied_when %q: %v", statePath, loc, pr.SatisfiedWhen, err)
				}
			}
			validateEffectValue(pr.Title, statePath, loc, "title", addErr)
			validateEffectValue(pr.Summary, statePath, loc, "summary", addErr)
			validateEffectValue(pr.Help, statePath, loc, "help", addErr)
			if pr.Action != nil {
				validateEffectValue(pr.Action.Label, statePath, loc, "action.label", addErr)
				validateEffectValue(pr.Action.Hint, statePath, loc, "action.hint", addErr)
				for _, key := range sortedKeys(pr.Action.Slots) {
					validateEffectValue(pr.Action.Slots[key], statePath, loc, fmt.Sprintf("action.slots.%s", key), addErr)
				}
			}
		}

		// on_enter effects.
		for i, eff := range s.OnEnter {
			validateEffectExprs(eff, statePath, fmt.Sprintf("on_enter[%d]", i), addErr)
		}

		// Transition guards + effects, grouped by intent for stable ordering.
		for _, intentName := range sortedKeys(s.On) {
			for ti, tr := range s.On[intentName] {
				if tr.When != "" {
					if _, err := expr.CompileBool(tr.When); err != nil {
						addErr("state %q intent %q transition[%d]: guard when %q: %v",
							statePath, intentName, ti, tr.When, err)
					}
				}
				for ei, eff := range tr.Effects {
					validateEffectExprs(eff, statePath,
						fmt.Sprintf("intent %q transition[%d] effects[%d]", intentName, ti, ei), addErr)
				}
			}
		}

		// Recurse into nested states.
		if len(s.States) > 0 {
			validateStateExprs(file, statePath, s.States, addErr)
		}
	}
}

// validateEffectExprs compile-checks one effect's guard plus every templated
// value it carries (set:, with:, emit_intent slots:), descending into nested
// on_complete: / inline effects: chains. loc identifies the effect within its
// state for the diagnostic (e.g. `on_enter[2]` or
// `intent "accept" transition[0] effects[1]`).
func validateEffectExprs(eff Effect, statePath, loc string, addErr func(string, ...any)) {
	if eff.When != "" {
		if _, err := expr.CompileBool(eff.When); err != nil {
			addErr("state %q %s: effect when %q: %v", statePath, loc, eff.When, err)
		}
	}
	for _, key := range sortedKeys(eff.Set) {
		validateEffectValue(eff.Set[key], statePath, loc, fmt.Sprintf("set %q", key), addErr)
	}
	for _, key := range sortedKeys(eff.With) {
		validateEffectValue(eff.With[key], statePath, loc, fmt.Sprintf("with %q", key), addErr)
	}
	for _, key := range sortedKeys(eff.EmitSlots) {
		validateEffectValue(eff.EmitSlots[key], statePath, loc, fmt.Sprintf("emit_intent slot %q", key), addErr)
	}
	if eff.CommitOperation != nil {
		for _, key := range sortedKeys(eff.CommitOperation.World) {
			validateEffectValue(eff.CommitOperation.World[key], statePath, loc, fmt.Sprintf("commit_operation world %q", key), addErr)
		}
	}
	if eff.PersistDraft != nil {
		if eff.PersistDraft.ID != "" {
			validateEffectValue(eff.PersistDraft.ID, statePath, loc, "persist_draft id", addErr)
		}
		if eff.PersistDraft.Title != "" {
			validateEffectValue(eff.PersistDraft.Title, statePath, loc, "persist_draft title", addErr)
		}
	}
	// emit_intent itself may be a template value resolved at fire time.
	if eff.EmitIntent != "" {
		validateEffectValue(eff.EmitIntent, statePath, loc, "emit_intent", addErr)
	}

	for i, child := range eff.OnComplete {
		validateEffectExprs(child, statePath, fmt.Sprintf("%s on_complete[%d]", loc, i), addErr)
	}
	for i, child := range eff.Effects {
		validateEffectExprs(child, statePath, fmt.Sprintf("%s effects[%d]", loc, i), addErr)
	}
}

// validateEffectValue compile-checks a single effect value, recursing through
// list/map structures exactly as the runtime's resolveEffectValue does so a
// templated string nested inside a host-call args list is checked too. The
// expr-vs-template classification mirrors expr.RenderValue (see validateExprs).
func validateEffectValue(v any, statePath, loc, key string, addErr func(string, ...any)) {
	switch val := v.(type) {
	case string:
		if !strings.Contains(val, "{{") {
			return // pure literal
		}
		if inner, ok := singleTypedExpr(val); ok {
			if _, err := expr.Compile(inner); err != nil {
				addErr("state %q %s: effect %s: %v", statePath, loc, key, err)
			}
			return
		}
		if err := expr.ValidateTemplate(val); err != nil {
			addErr("state %q %s: effect %s: %v", statePath, loc, key, err)
		}
	case []any:
		for i, item := range val {
			validateEffectValue(item, statePath, loc, fmt.Sprintf("%s[%d]", key, i), addErr)
		}
	case map[string]any:
		for _, k := range sortedKeys(val) {
			validateEffectValue(val[k], statePath, loc, fmt.Sprintf("%s.%s", key, k), addErr)
		}
	}
}

// singleTypedExpr reports whether s is a single typed expression — a value
// wrapped in exactly one {{ }} block with no surrounding text and no further
// delimiters inside — and returns the inner expression source when so. It
// mirrors the gate in expr.RenderValue byte-for-byte: a multi-interpolation
// template like "{{ a }}X{{ b }}" begins with "{{" and ends with "}}" yet is
// NOT a single expr (its inner contains further "{{"/"}}"), and a block-keyword
// form ({{ if }} / {{ range }}) is a template, not a typed value.
func singleTypedExpr(s string) (string, bool) {
	stripped := strings.TrimSpace(s)
	if !strings.HasPrefix(stripped, "{{") || !strings.HasSuffix(stripped, "}}") {
		return "", false
	}
	inner := stripped[2 : len(stripped)-2]
	if strings.Contains(inner, "{{") || strings.Contains(inner, "}}") {
		return "", false
	}
	inner = strings.TrimSpace(inner)
	switch firstWord(inner) {
	case "if", "else", "end", "range":
		return "", false
	}
	return inner, true
}

// firstWord returns the leading space-delimited token of s, used to detect
// template block keywords.
func firstWord(s string) string {
	if idx := strings.IndexByte(s, ' '); idx >= 0 {
		return s[:idx]
	}
	return s
}

// ── view ↔ on_enter bind-target fallback diagnostic ──────────────────────────
//
// The bug: a state's inline view reads a world key whose value is only filled
// in by an on_enter `invoke:` … `bind:` host call (e.g. `world.feature_branch_diff`).
// The first view frame can render against a PRE-bind world snapshot, so the
// schema default (e.g. "(pending)") leaks into frame one. The runtime now
// defends this (machine.Turn skips the pre-bind render when host calls will
// bind), but the template is still fragile: it loads with zero diagnostic even
// though it only renders correctly because the runtime happens to defend the
// frame. validateViewBindFallbacks restores an authoring-time signal — an
// advisory (NON-FATAL) warning — so the author knows to add an explicit
// `?? "(pending)"` fallback (or `| default(...)` filter) rather than relying on
// the runtime's frame defence.
//
// Limitation: states whose view uses an external standalone template
// (`View.TemplateFile`) are skipped — that template body is not inline-scannable
// from the AppDef, so no diagnostic is emitted for it.

// viewBindFallbackWarning is one advisory finding: state StatePath's inline view
// references the on_enter bind-target world key Key without a fallback operator.
type viewBindFallbackWarning struct {
	StatePath string
	Key       string
}

// message renders the human-readable advisory line for a finding.
func (w viewBindFallbackWarning) message() string {
	return fmt.Sprintf("state %q: view references on_enter bind-target world.%s "+
		"without a fallback; add a `?? \"(pending)\"` fallback (or a `| default(...)` "+
		"filter), or rely on the post-bind re-render", w.StatePath, w.Key)
}

// validateViewBindFallbacks emits advisory (NON-FATAL) warnings when a state's
// inline view template references an on_enter `invoke`/`bind` target world key
// without a fallback operator. It is invoked from validateDef alongside the
// other validate* passes, but — unlike them — it does NOT append to errs: these
// are warnings, not load errors, so a fragile template still loads. The errs
// parameter is accepted only to match the validate*-pass call signature.
func validateViewBindFallbacks(file string, def *AppDef, _ *[]error) {
	for _, w := range collectViewBindFallbackWarnings("", def.States) {
		slog.Warn("view references an on_enter bind-target without a fallback; "+
			"a pre-bind frame would render its schema default",
			"file", file, "state", w.StatePath, "key", w.Key,
			"hint", "add a `?? \"(pending)\"` fallback or rely on the post-bind re-render")
	}
}

// collectViewBindFallbackWarnings walks the state tree (recursing into nested
// States) collecting findings. For each state it (1) gathers every on_enter
// bind-target world key, (2) scans the inline view text, and (3) flags any
// bind-target referenced without a fallback. States using an external
// TemplateFile are skipped (not inline-scannable). Findings are returned in a
// stable order (states sorted, then keys sorted).
func collectViewBindFallbackWarnings(prefix string, states map[string]*State) []viewBindFallbackWarning {
	var out []viewBindFallbackWarning
	for _, name := range sortedKeys(states) {
		s := states[name]
		if s == nil {
			continue
		}
		statePath := joinPath(prefix, name)

		// Skip external standalone templates — not inline-scannable.
		if s.View.TemplateFile == "" {
			targets := map[string]struct{}{}
			collectBindTargets(s.OnEnter, targets)
			if len(targets) > 0 {
				if text := inlineViewText(s.View); text != "" {
					for _, key := range sortedKeys(targets) {
						if referencesBindTargetWithoutFallback(text, key) {
							out = append(out, viewBindFallbackWarning{StatePath: statePath, Key: key})
						}
					}
				}
			}
		}

		if len(s.States) > 0 {
			out = append(out, collectViewBindFallbackWarnings(statePath, s.States)...)
		}
	}
	return out
}

// collectBindTargets walks effs (and their nested OnComplete/Effects chains)
// accumulating, into the set, every world-key bind target — the keys of
// Effect.Bind — for effects that carry an Invoke. Mirrors the runtime's notion
// of "this host call fills these world keys".
func collectBindTargets(effs []Effect, into map[string]struct{}) {
	for _, eff := range effs {
		if eff.Invoke != "" {
			for worldKey := range eff.Bind {
				into[worldKey] = struct{}{}
			}
		}
		collectBindTargets(eff.OnComplete, into)
		collectBindTargets(eff.Effects, into)
	}
}

// inlineViewText concatenates every inline-scannable template body of a View:
// its legacy scalar Source, the Source of each element in Elements, and the
// Source of each element nested inside Blocks. The pieces are joined with
// newlines so a reference at the end of one piece can't accidentally fuse with
// the start of the next.
func inlineViewText(v View) string {
	var parts []string
	if v.Source != "" {
		parts = append(parts, v.Source)
	}
	for _, el := range v.Elements {
		if el.Source != "" {
			parts = append(parts, el.Source)
		}
	}
	for _, blockName := range sortedKeys(v.Blocks) {
		for _, el := range v.Blocks[blockName] {
			if el.Source != "" {
				parts = append(parts, el.Source)
			}
		}
	}
	return strings.Join(parts, "\n")
}

// referencesBindTargetWithoutFallback reports whether text references the world
// key in one of the bare forms — `world.<key>`, `world["<key>"]`, or
// `world['<key>']` — that is NOT immediately followed (modulo whitespace) by a
// fallback: the `??` operator or a `| default(...)` filter. A single fragile
// reference is enough to flag the key.
func referencesBindTargetWithoutFallback(text, key string) bool {
	refs := []string{
		"world." + key,
		`world["` + key + `"]`,
		`world['` + key + `']`,
		"world[`" + key + "`]",
	}
	dotForm := "world." + key
	for _, ref := range refs {
		from := 0
		for {
			idx := strings.Index(text[from:], ref)
			if idx < 0 {
				break
			}
			pos := from + idx
			end := pos + len(ref)
			from = end // advance past this occurrence for the next scan

			// For the dot form, guard against matching a longer identifier
			// (world.feature_x when key is "feature") or a nested member
			// access (world.feature.diff) — neither is a bare reference to key.
			if ref == dotForm && end < len(text) {
				if c := text[end]; c == '_' || c == '.' || isIdentByte(c) {
					continue
				}
			}

			rest := strings.TrimLeft(text[end:], " \t\r\n")
			if strings.HasPrefix(rest, "??") {
				continue // `?? …` fallback present
			}
			if strings.HasPrefix(rest, "|") {
				after := strings.TrimLeft(rest[1:], " \t\r\n")
				if strings.HasPrefix(after, "default") {
					continue // `| default(…)` filter present
				}
			}
			// No fallback guarding this reference.
			return true
		}
	}
	return false
}

// isIdentByte reports whether c can appear inside an expr-lang identifier
// (used to avoid matching a key as a prefix of a longer identifier).
func isIdentByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}
