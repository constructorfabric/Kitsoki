package app

import (
	"fmt"
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
