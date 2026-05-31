// Package render wraps github.com/flosch/pongo2/v6 with the small surface
// the rest of kitsoki uses for templated string rendering. It sits at the
// view seam between the story author's raw template text and the typed
// element renderers in [kitsoki/internal/render/elements]: every
// {{ }}-delimited or {% %}-delimited leaf string flows through this
// package, while bare-expression fields (when: guards, pure initial-child
// selectors) stay on [kitsoki/internal/expr]. Its [Pongo] signature
// deliberately mirrors expr.Render so call sites swap one-for-one.
//
// Two entry shapes share one engine:
//
//   - Loader-less rendering — [Pongo] / [PongoParse] render an inline
//     string against an [expr.Env] with no on-disk template root. Used by
//     callers that only need {{ }}/{% %} substitution, never
//     {% extends %} / {% include %}.
//   - App-scoped rendering — an [AppRenderer] owns a per-app
//     pongo2.TemplateSet rooted at <appDir>/views/, so {% extends %} and
//     {% include %} resolve names against that directory.
//
// # Algorithm
//
// Every render takes the same two-stage path, with a fast path in front:
//
//  1. Delimiter probe. If the source contains no "{{" or "{%" it is pure
//     prose; the source is returned verbatim, skipping the pongo2 parse
//     cost entirely. The vast majority of view leaf strings are prose, so
//     this fast path dominates.
//  2. Coalesce preprocess. The kitsoki-author-friendly ` ?? ` null-coalesce
//     operator is rewritten to pongo2's `|default:` filter form, but only
//     inside {{ }}/{% %} spans, so a literal "??" in prose survives. Chained
//     `A ?? B ?? "fallback"` becomes `A|default:B|default:"fallback"`,
//     evaluated left-to-right with pongo2's falsy semantics.
//  3. Compile + execute. The rewritten source is compiled to a pongo2
//     template and executed against the [pongo2.Context] built by
//     [ToContext] from the env. Any error is wrapped by [wrapTemplateError]
//     with a source snippet so authors see what failed.
//
// # Invariants
//
//   - Autoescape is OFF, process-wide. pongo2's escape flag is a
//     package-level global ([pongo2.SetAutoescape]) with no per-TemplateSet
//     override. This package disables it in init() because the renderer
//     targets a terminal UI where HTML-escaping would corrupt authored text
//     like "A & B". DO NOT call pongo2.SetAutoescape anywhere else in this
//     binary: flipping the global at runtime would race every concurrent
//     render (the flag is read once per Execute to seed each
//     ExecutionContext). The sentinel test
//     TestAutoescapeRemainsDisabledAcrossConcurrentRenders catches accidental
//     flips. Templates that DO want escaping opt in with the `|escape` filter.
//   - Undefined variables render empty, not error. An undefined top-level
//     variable or a missing map key yields the empty string; chained access
//     on a missing field (`{{ world.foo.bar }}` when `foo` is absent)
//     short-circuits to nil rather than erroring — matching pongo2's
//     Django-compatible semantics.
//   - Helpers bind per-render, never globally. The four view helpers
//     (available, blocked, blocked_reason, intent_status) are installed as
//     callable values on the per-render Context by [ToContext], never via
//     pongo2.RegisterFilter (which is process-wide and cannot see a
//     per-render env). Filter-style invocation ({{ 'x' | available }}) is
//     therefore unsupported by design; authors use the function-call form
//     {{ available('x') }} everywhere.
//   - Leaves are pongo2/v6 (Django), not Jinja2. Filter arguments use the
//     colon syntax ({{ slots.foo|default:"(unset)" }}, not the parens form);
//     there is no expression-level ternary (use {% if %}…{% else %}…{% endif %});
//     the for-loop counter is `forloop.Counter` (1-based) / `forloop.Counter0`
//     / `forloop.First` / `forloop.Last`, not Jinja's `loop.index`.
//
// # Worked example
//
// A single leaf string with a null-coalesce fallback, rendered against a
// world that has the variable and against one that does not:
//
//	src: {{ world.x ?? "(none)" }}
//
//	env World={x:"value"}      → preprocess → {{ world.x|default:"(none)" }}
//	                           → execute    → "value"
//
//	env World={}               → preprocess → {{ world.x|default:"(none)" }}
//	                           → execute    → "(none)"
//
// A runnable form of this trace lives in [ExamplePongo].
//
// # Lifecycle
//
// [Pongo] / [PongoParse] are package-level, stateless, and safe for
// concurrent use — they own no mutable state beyond the process-global
// autoescape flag set once at init(). [NewAppRenderer] / [NewCachedAppRenderer]
// build a renderer once per app at machine load: the uncached variant
// re-reads templates on every render so app-file edits take effect without a
// restart (dev / interactive mode), while the cached variant compiles each
// loaded template once for tests and non-interactive production runs. An
// [AppRenderer]'s methods perform only reads against its TemplateSet; the
// zero value is NOT usable — always go through a constructor.
//
// # Non-goals
//
//   - No custom template DSL. pongo2/v6 is the abstraction boundary; the
//     only kitsoki-specific surface is the ` ?? ` → `|default:` rewrite and
//     the col/rcol/reverse filters. Authors learn Django's template grammar,
//     not a bespoke one, so the engine stays swappable and the docs stay
//     thin.
//   - No per-template syntax customization. Every render uses the same
//     grammar and the same globally-disabled autoescape; there is no
//     per-call escape mode or delimiter override, so a leaf string means the
//     same thing wherever it appears.
//   - No caching beyond pongo2.TemplateSet. The cached AppRenderer reuses
//     pongo2's own compiled-template cache; this package adds no result
//     memoization. Callers that want to avoid re-rendering identical leaves
//     own that strategy (the typed-element pipeline expands each leaf exactly
//     once per view render).
//   - No styling or layout. This package substitutes variables and nothing
//     more. Column alignment, reflow, and lipgloss accents are the typed
//     element renderers' job — see [kitsoki/internal/render/elements].
//
// # Reference
//
// The author-facing template and view contract — the engine split, the
// translation table from the legacy expr-lang forms, and the col/rcol/reverse
// filters — is documented in docs/stories/story-style.md. The typed element
// renderers that consume this package's output live in
// [kitsoki/internal/render/elements]; the guard-expression evaluator that owns
// the bare-expression fields is [kitsoki/internal/expr].
package render
