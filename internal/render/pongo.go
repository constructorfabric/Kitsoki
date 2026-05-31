package render

import (
	"fmt"
	"strings"

	"github.com/flosch/pongo2/v6"
	"github.com/muesli/reflow/wordwrap"

	"kitsoki/internal/expr"
)

// TemplateErrorSnippetLength bounds the template source echoed in a wrapped
// render error. Inline view leaves are usually short, but a multi-line
// {% extends %} body or a long prose paragraph would flood the error log;
// truncating to a fixed prefix keeps render failures scannable while still
// showing enough of the source for an author to recognise the offending
// template.
const TemplateErrorSnippetLength = 200

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

	// `col:N` / `rcol:N` pad-or-clip a string to exactly N visible
	// runes — the primitive for building aligned-column "tables"
	// inside a `code:` block without a dedicated table: element.
	//
	// col is left-aligned (padding goes on the right, used for text
	// columns); rcol is right-aligned (padding on the left, used for
	// numeric / index columns).
	//
	// Distinct from pongo2's built-in `ljust:N` / `rjust:N`, which
	// follow Django semantics (pad only, no clip). For a table column
	// the asymmetric overflow of pad-only is a foot-gun — a single
	// long value would push every following column out of alignment.
	// We pick distinct names rather than override the built-ins so
	// authors familiar with Django get the behaviour they expect from
	// ljust/rjust.
	//
	// The filters count runes (not bytes), so multibyte glyphs like
	// `●`/`○`/`★` count as one column, matching the visible width
	// the terminal will render. Clipping is hard-cut (no ellipsis);
	// authors who want ellipsis-on-overflow should chain
	// `truncatechars:N-1|col:N`.
	_ = pongo2.RegisterFilter("col", filterCol)
	_ = pongo2.RegisterFilter("rcol", filterRcol)

	// Override pongo2/v6's built-in `wordwrap`, which is doubly broken:
	//
	//  1. It wraps at a number of WORDS, not characters — the opposite of
	//     Django's `wordwrap` (which wraps to N columns, breaking on
	//     whitespace) that the name leads every author to expect. A
	//     `question|wordwrap:115` meant to hard-wrap a long line at ~115
	//     columns instead asks for "115 words per line" and does nothing.
	//
	//  2. Its line-count formula `wordsLen/wrapAt + wordsLen%wrapAt`
	//     over-counts whenever wrapAt exceeds the word count, then indexes
	//     past the end of the word slice. A 3-word string with
	//     `wordwrap:110` slices words[110:3] and PANICS with
	//     "slice bounds out of range [110:3]" — and because the panic
	//     unwinds through tpl.Execute it escapes the TUI's render-error
	//     fallback and crashes the whole bubbletea program.
	//
	// Replace it with muesli/reflow's character-width word wrap — the same
	// wrapper the prose/kv/list view elements already use — so `wordwrap:N`
	// means "wrap to N columns, breaking on whitespace" and never panics.
	_ = pongo2.ReplaceFilter("wordwrap", filterWordwrap)
}

// filterWordwrap wraps the input to param visible columns, breaking on
// whitespace, replacing pongo2's broken built-in (see the ReplaceFilter
// call in init()). A non-positive width returns the input unchanged.
// Backed by muesli/reflow/wordwrap so wrapping behaviour matches the
// prose/kv/list elements.
func filterWordwrap(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	width := param.Integer()
	if width <= 0 {
		return in, nil
	}
	return pongo2.AsValue(wordwrap.String(in.String(), width)), nil
}

// filterCol pads or truncates the input to exactly N runes,
// left-aligned (padding goes on the right). See the registration
// site in init() for the rationale on the col/rcol naming and the
// rune-counting + no-ellipsis trade-offs.
func filterCol(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	width := param.Integer()
	if width < 0 {
		width = 0
	}
	runes := []rune(in.String())
	if len(runes) >= width {
		return pongo2.AsValue(string(runes[:width])), nil
	}
	return pongo2.AsValue(string(runes) + strings.Repeat(" ", width-len(runes))), nil
}

// filterRcol is col's right-aligned twin: padding goes on the left.
// Useful for numeric / index columns where right-alignment makes a
// sorted column read more naturally.
func filterRcol(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	width := param.Integer()
	if width < 0 {
		width = 0
	}
	runes := []rune(in.String())
	if len(runes) >= width {
		return pongo2.AsValue(string(runes[:width])), nil
	}
	return pongo2.AsValue(strings.Repeat(" ", width-len(runes)) + string(runes)), nil
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

// PongoParse compiles a pongo2 template WITHOUT executing it. This is
// the load-time syntax probe — any non-nil error from this function is
// unambiguously a parse / syntax error (undefined-identifier and
// type-mismatch errors only surface during Execute). Callers that need
// to discriminate syntax-vs-runtime errors at load time use this
// instead of inspecting the message text of a full render call.
//
// If src contains no template delimiters the function returns nil
// (pure prose is by definition syntactically valid).
func PongoParse(src string) error {
	if !hasDelims(src) {
		return nil
	}
	if _, err := pongo2.FromString(preprocessCoalesce(src)); err != nil {
		return wrapTemplateError(src, err)
	}
	return nil
}

// Pongo renders an inline pongo2 template string against an expr.Env.
//
// If src contains no template delimiters ("{{" or "{%") the source is
// returned verbatim — this avoids paying the pongo2 parse cost on the many
// view leaf strings that are pure prose.
//
// The signature matches expr.Render so call-site swaps are mechanical.
func Pongo(src string, env expr.Env) (out string, err error) {
	if !hasDelims(src) {
		return src, nil
	}
	src = preprocessCoalesce(src)
	tpl, err := pongo2.FromString(src)
	if err != nil {
		return "", wrapTemplateError(src, err)
	}
	// pongo2 filters (built-in and our own) execute arbitrary Go against
	// author-controlled input that can panic rather than return an error —
	// the stock `wordwrap` did exactly this on a short string (see init()).
	// A panic here unwinds straight past every caller's render-error
	// fallback and crashes the bubbletea program. Recover at this seam and
	// turn the panic into an ordinary error so callers degrade gracefully
	// (the transcript view falls back to raw source).
	defer func() {
		if r := recover(); r != nil {
			out = ""
			err = wrapTemplateError(src, fmt.Errorf("panic during template execution: %v", r))
		}
	}()
	rendered, err := tpl.Execute(ToContext(env))
	if err != nil {
		return "", wrapTemplateError(src, err)
	}
	return rendered, nil
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

// hasDelims reports whether src contains pongo2 template syntax. The render
// fast path returns the source verbatim when no delimiters are present, so
// pure-prose leaves never pay the pongo2 parse cost.
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
	if len(snippet) > TemplateErrorSnippetLength {
		snippet = snippet[:TemplateErrorSnippetLength] + "…"
	}
	return fmt.Errorf("render: pongo2 template %q: %w", snippet, err)
}
