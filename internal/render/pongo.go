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

	// Register the `reverse` filter. pongo2/v6 ships `sort` but not
	// `reverse`; YAML authors reach for `|reverse` to flip an ASC-sorted
	// host result into newest-first (e.g. ticket lists where filenames
	// are ISO timestamps). Without it, every view using the idiom dies
	// with "Filter 'reverse' does not exist." at render time.
	//
	// The filter accepts any slice-shaped value (the underlying Go type
	// can be []any, []map[string]any, []string, etc.); a non-slice
	// input is returned unchanged so author typos don't crash render.
	_ = pongo2.RegisterFilter("reverse", filterReverse)
}

// filterReverse returns a new slice with the input's elements in
// reverse order. Strings reverse rune-by-rune (so "abc" → "cba"). Any
// other type is passed through unchanged so a misapplied filter
// degrades to a no-op rather than a render error.
func filterReverse(in *pongo2.Value, _ *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	if in == nil || in.IsNil() {
		return in, nil
	}
	if in.IsString() {
		runes := []rune(in.String())
		for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
			runes[i], runes[j] = runes[j], runes[i]
		}
		return pongo2.AsValue(string(runes)), nil
	}
	if !in.CanSlice() {
		return in, nil
	}
	n := in.Len()
	out := make([]any, n)
	for i := 0; i < n; i++ {
		out[n-1-i] = in.Index(i).Interface()
	}
	return pongo2.AsValue(out), nil
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
	src = preprocessCoalesce(src)
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

// preprocessCoalesce rewrites the kitsoki-author-friendly ` ?? ` null-
// coalesce operator into pongo2's `|default:<expr>` filter form so the
// many existing story view templates that read like
//
//	{{ world.x ?? "(none)" }}
//
// parse under pongo2 (which has no `??` operator — Django's template
// language gates falsy fallback through the `|default:` filter). The
// rewrite happens only between `{{` / `}}` and `{%` / `%}` delimiters
// so a literal `??` in prose (e.g. an authored question "really??")
// is left intact.
//
// Chained `A ?? B ?? "fallback"` becomes `A|default:B|default:"fallback"`,
// which pongo2 evaluates left-to-right with the right falsy semantics.
// `??` inside string literals is preserved by skipping over quoted spans.
func preprocessCoalesce(src string) string {
	if !strings.Contains(src, "??") {
		return src
	}
	var out strings.Builder
	out.Grow(len(src))
	i := 0
	for i < len(src) {
		open := indexOfAny(src[i:], "{{", "{%")
		if open < 0 {
			out.WriteString(src[i:])
			return out.String()
		}
		// Emit verbatim up to the opening delimiter.
		out.WriteString(src[i : i+open])
		// Identify the matching closing delimiter.
		closeTok := "}}"
		if src[i+open:i+open+2] == "{%" {
			closeTok = "%}"
		}
		end := strings.Index(src[i+open+2:], closeTok)
		if end < 0 {
			// Unmatched delimiter — emit the rest verbatim and let
			// pongo2 surface the parser error.
			out.WriteString(src[i+open:])
			return out.String()
		}
		body := src[i+open+2 : i+open+2+end]
		out.WriteString(src[i+open : i+open+2])
		out.WriteString(rewriteCoalesceBody(body))
		out.WriteString(closeTok)
		i = i + open + 2 + end + len(closeTok)
	}
	return out.String()
}

// rewriteCoalesceBody replaces every top-level ` ?? ` inside a
// template expression with `|default:`. String literals (single or
// double quoted) are passed through unchanged.
func rewriteCoalesceBody(s string) string {
	if !strings.Contains(s, "??") {
		return s
	}
	var out strings.Builder
	out.Grow(len(s))
	i := 0
	for i < len(s) {
		c := s[i]
		// Skip string literals.
		if c == '"' || c == '\'' {
			quote := c
			out.WriteByte(c)
			i++
			for i < len(s) {
				out.WriteByte(s[i])
				if s[i] == '\\' && i+1 < len(s) {
					out.WriteByte(s[i+1])
					i += 2
					continue
				}
				if s[i] == quote {
					i++
					break
				}
				i++
			}
			continue
		}
		if c == '?' && i+1 < len(s) && s[i+1] == '?' {
			// Eat surrounding whitespace so `A ?? B` and `A??B` both
			// collapse cleanly into `A|default:B`.
			trimRight(&out)
			out.WriteString("|default:")
			i += 2
			for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
				i++
			}
			continue
		}
		out.WriteByte(c)
		i++
	}
	return out.String()
}

// trimRight strips trailing ASCII whitespace from a strings.Builder.
// We rebuild the buffer because strings.Builder doesn't expose
// length-adjustable mutation directly.
func trimRight(b *strings.Builder) {
	s := b.String()
	t := strings.TrimRight(s, " \t")
	if len(t) == len(s) {
		return
	}
	b.Reset()
	b.WriteString(t)
}

// indexOfAny returns the smallest index at which any of subs starts in
// s, or -1 when none are present. Used to find the next `{{` or `{%`
// delimiter in preprocessCoalesce.
func indexOfAny(s string, subs ...string) int {
	best := -1
	for _, sub := range subs {
		if idx := strings.Index(s, sub); idx >= 0 && (best == -1 || idx < best) {
			best = idx
		}
	}
	return best
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
