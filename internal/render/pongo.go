// Package render wraps github.com/flosch/pongo2/v6 with the surface the rest
// of kitsoki uses for templated string rendering (§3 of the view-elements
// proposal). It intentionally shadows the shape of expr.Render so the
// upcoming codemod can swap call sites one-for-one.
//
// # Engine split
//
// Pongo2 takes over every {{ }}-delimited or {% %}-delimited templated
// string. Bare-expression fields (when:, guard predicates, pure
// initial-child selectors) stay on internal/expr — see proposal §3.5.
//
// # Helpers
//
// The four view helpers from expr.Env (Available, Blocked, BlockedReason,
// IntentStatus) are exposed as callable values on the pongo2.Context. Pongo2
// resolves func-typed context entries as callables, so
//
//	{{ available('start_journey') }}
//	{{ blocked_reason('start_journey') }}
//
// both work without any extra registration step. Per-render binding avoids
// the global-mutable-state pitfall that pongo2.RegisterFilter would
// introduce (filters are process-wide; helper closures are per-env).
//
// Filter-style invocation ({{ 'start' | available }}) is NOT supported by
// design: pongo2 filters are registered globally on the engine, which can't
// see a per-render env. Authors should use the function-call form everywhere
// — the proposal's translation table (§3.1) uses that form exclusively.
//
// # Autoescape
//
// pongo2 defaults to HTML autoescape (escaping &, <, >, ', "). The kitsoki
// renderer targets a terminal UI, where HTML escaping would corrupt
// authored text like "A & B". This package disables autoescape globally
// in an init() since pongo2's escape flag is a package-level var
// (`pongo2.SetAutoescape`). Templates that DO want escaping can opt in
// with the `|escape` filter.
//
// **DO NOT call `pongo2.SetAutoescape` anywhere else in this binary.**
// pongo2 has no per-`TemplateSet` autoescape configuration — the only
// public hook is the global. Flipping it from another init() or at
// runtime would race with this package's renders (the global is read
// once per `tpl.Execute` to seed each ExecutionContext). The sentinel
// test `TestAutoescapeRemainsDisabledAcrossConcurrentRenders` in
// `pongo_test.go` catches accidental flips by asserting that the
// canonical HTML-escape characters survive a 100-way concurrent
// render. Add to that test (or to package-level documentation) if you
// introduce another caller of `pongo2.SetAutoescape`.
//
// # Undefined variables
//
// Pongo2 returns the empty string for undefined top-level variables and for
// missing map keys. Chained access on a missing field (`{{ world.foo.bar }}`
// when `foo` doesn't exist) short-circuits to nil instead of erroring,
// matching pongo2's Django-compatible semantics.
//
// # Pongo2/v6 syntax quirks (vs. the proposal §3.1 translation table)
//
// pongo2/v6 implements Django's template language rather than Jinja2. Two
// notable deviations from the proposal:
//
//  1. Filter arguments use Django's colon syntax, not Jinja parens:
//     {{ slots.foo|default:"(unset)" }}   ✓ pongo2/v6
//     {{ slots.foo|default('(unset)') }}  ✗ Jinja form, parser error
//
//  2. There is no expression-level ternary. Use the {% if %} … {% else %}
//     … {% endif %} block form even for tiny conditionals:
//     {% if x %}a{% else %}b{% endif %}    ✓
//     {{ 'a' if x else 'b' }}              ✗ parser error
//
// The for-loop counter variable is `forloop.Counter` (1-based) /
// `forloop.Counter0` (0-based) / `forloop.First` / `forloop.Last`, not
// `loop.index` (that's Jinja). See pongo2's tags_for.go for the full
// shape.
//
// These are pure syntax differences; the codemod (Phase C) must produce
// the pongo2/v6 forms above when rewriting YAML.
package render

import (
	"fmt"
	"strings"

	"github.com/flosch/pongo2/v6"

	"kitsoki/internal/expr"
)

func init() {
	// Disable HTML autoescape globally — see package doc.
	pongo2.SetAutoescape(false)
}

// Pongo renders an inline pongo2 template string against an expr.Env.
//
// If src contains no template delimiters ("{{" or "{%") the source is
// returned verbatim — this avoids paying the pongo2 parse cost on the many
// view leaf strings that are pure prose.
//
// The signature matches expr.Render so call-site swaps are mechanical.
func Pongo(src string, env expr.Env) (string, error) {
	if !hasDelims(src) {
		return src, nil
	}
	tpl, err := pongo2.FromString(src)
	if err != nil {
		return "", wrapTemplateError(src, err)
	}
	out, err := tpl.Execute(ToContext(env))
	if err != nil {
		return "", wrapTemplateError(src, err)
	}
	return out, nil
}

// ToContext converts an expr.Env into a pongo2.Context.
//
// Exposed keys mirror the expr.Env struct tags so author-visible variables
// (world, slots, event, run, args, menu, item) have identical names in
// pongo2 as they had under expr-lang. The four helper closures
// (available, blocked, blocked_reason, intent_status) are added as
// callable values; if env's closures are nil, no-op stubs returning
// false / "" are installed so templates that reference a helper outside a
// view-render context don't error.
//
// Run is converted from the typed RunCtx struct to a map so authors can
// write `{{ run.id }}` / `{{ run.turn }}` — pongo2 reflects struct fields
// by their Go name, not their struct tag, so the typed form would require
// `{{ run.ID }}`. Converting to a map at this seam keeps templates
// case-consistent with the rest of the env.
func ToContext(env expr.Env) pongo2.Context {
	state := env.State
	if state == nil {
		// Provide an empty map so `{{ state.path }}` / `{{ state.description }}`
		// render as empty string (per pongo2's missing-key semantics) rather
		// than `nil` errors. View-render call sites that have state info
		// populate env.State before this conversion.
		state = map[string]any{}
	}
	ctx := pongo2.Context{
		"world": env.World,
		"slots": env.Slots,
		"event": env.Event,
		"run": map[string]any{
			"id":   env.Run.ID,
			"turn": env.Run.Turn,
		},
		"args":  env.Args,
		"menu":  env.Menu,
		"item":  env.Item,
		"state": state,
	}

	if env.Available != nil {
		ctx["available"] = env.Available
	} else {
		ctx["available"] = func(string) bool { return false }
	}
	if env.Blocked != nil {
		ctx["blocked"] = env.Blocked
	} else {
		ctx["blocked"] = func(string) bool { return false }
	}
	if env.BlockedReason != nil {
		ctx["blocked_reason"] = env.BlockedReason
	} else {
		ctx["blocked_reason"] = func(string) string { return "" }
	}
	if env.IntentStatus != nil {
		ctx["intent_status"] = env.IntentStatus
	} else {
		ctx["intent_status"] = func(string) string { return "unknown" }
	}

	return ctx
}

// hasDelims reports whether src contains pongo2 template syntax. The
// proposal's fast path (§3) returns the source verbatim when no delimiters
// are present.
func hasDelims(src string) bool {
	return strings.Contains(src, "{{") || strings.Contains(src, "{%")
}

// wrapTemplateError annotates a pongo2 error with the template source so
// authors see what failed without spelunking through stack traces. Pongo2's
// own errors include line/column for file-loaded templates; for inline
// strings the source itself is the most useful context.
func wrapTemplateError(src string, err error) error {
	// Single-line shorthand for short templates; for multi-line templates
	// fall back to a quoted snippet to keep error logs scannable.
	snippet := src
	if len(snippet) > 200 {
		snippet = snippet[:200] + "…"
	}
	return fmt.Errorf("render: pongo2 template %q: %w", snippet, err)
}
