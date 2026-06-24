// Package elements implements the per-element renderers and the
// dispatcher that turn a typed [app.View] into a final transcript string.
// It sits below internal/render in the view pipeline: the orchestrator
// (and the TUI, on re-render) hand a normalised View to [RenderAll],
// which guard-filters and pongo-expands each element's leaf strings and
// lays out the result at a caller-supplied width. The package owns layout
// (column alignment, reflow, dividers); it deliberately does not own
// styling beyond the small lipgloss accents documented per element.
//
// Two layout families share one dispatcher:
//
//   - Plain-text layout — prose, heading, list, kv, banner, choice are
//     laid out here at the caller's width and never re-flowed downstream.
//   - The template escape hatch — the `template` kind hands its expanded
//     body to the caller's Glamour callback, preserving the pre-typed-
//     element markdown path verbatim.
//
// # Algorithm
//
// [RenderAll] turns a View into one string. For each element in
// view.Elements, in author order:
//
//  1. If the element carries a `when:` guard (expr-lang source), compile
//     it (cached) and evaluate it against the env. A false (or falsy —
//     see [Invariants]) result drops the element entirely: no blank stub
//     row, no extra blank line between the surviving siblings.
//  2. Every leaf string on the element (prose / heading / code / template
//     body, list-item label and hint, kv pair value, choice prompt /
//     item / field) is rendered through pongo2 BEFORE the element-kind
//     renderer sees it. Element renderers operate on concrete text, never
//     on un-substituted templates. Leaf rendering routes through the
//     per-app [ViewRenderer] when one is supplied (so {% include %} /
//     {% extends %} resolve against <appDir>/views/) and falls back to
//     the loader-less package-level render.Pongo otherwise.
//  3. The element-kind renderer lays the rendered leaves out at the
//     supplied width and returns its block (or "" to suppress itself).
//
// The non-empty blocks are joined by [joinElements]: one blank line
// between adjacent elements, except two consecutive `kv` elements
// coalesce with a single newline so an author who splits one logical
// status block across two `kv:` declarations still reads it as one
// aligned column.
//
// When view.Extends is set, RenderAll takes the typed-extends branch
// instead: it recursively renders each named block at the same width,
// asks the [ViewRenderer] to splice the rendered blocks into the base
// template, then applies the Glamour callback to the composite. This is
// what lets the TUI re-render an extends-shaped view at the real viewport
// width rather than the orchestrator's pre-render default.
//
// # Invariants
//
//   - Suppression carries no spacing. Any element (or list / choice row,
//     or choice field) whose guard fails — or whose rendered body is
//     empty after trimming — contributes nothing: it is removed before
//     column-width measurement and before inter-element joining, so a
//     guarded-away wide row never reserves a column for the survivors.
//   - `when:` is truthy, not strict-bool. A guard's value is coerced via
//     JavaScript/Python-style rules (nil/empty-string/empty-collection/
//     zero → false; any other non-nil value → true) by [truthy]. Authors
//     write `when: "world.blockers"` meaning "render if present," not
//     "render if strictly a Go bool." A runtime eval error (commonly
//     `world.x.y` where `world.x` is nil) is treated as falsy and hides
//     the element rather than failing the whole view; a compile error is
//     an authoring bug and propagates.
//   - Column widths are caller-supplied, never sensed. Every renderer
//     lays out against the `width` argument it is given. There is no
//     terminal probing inside this package; the TUI passes its viewport,
//     the orchestrator passes its block-render default.
//   - Leaves are expanded exactly once, before layout. A renderer never
//     re-runs pongo on already-substituted text, so an expression that
//     emits layout-significant characters cannot be re-interpreted.
//
// # Worked example
//
// A three-element view — a heading, a prose paragraph with two `world`
// substitutions, and a two-row kv block — rendered at width 40 with
// World={river:"Kansas", depth:4, cash:"$42"} and no styling profile:
//
//	in:  [ {heading "River Crossing"},
//	       {prose "The {{ world.river }} is wide and {{ world.depth }} feet deep."},
//	       {kv  Cash:"{{ world.cash }}"  Oxen:"3"} ]
//
//	out (blank line between each element; emerald accent on the heading
//	     elided here):
//
//	    River Crossing
//
//	    The Kansas is wide and 4 feet deep.
//
//	    Cash:  $42
//	    Oxen:  3
//
// The kv colon column is sized to the longest key ("Cash"), so "Oxen"
// pads to align. A runnable form of this trace lives in [ExampleRenderAll].
//
// # Lifecycle
//
// There is no compile step that returns a reusable object: RenderAll is
// called once per view render. The only persistent state is the package-
// global [whenCache] of compiled guard programs, populated lazily on
// first use of each distinct guard source and shared across every view.
// Element structs ([Prose], [KV], [Banner], …) are constructed per call
// inside [renderOne] from the typed [app.ViewElement] fields and discarded
// after Render returns; they hold no state between calls.
//
// # Non-goals
//
//   - No styling vocabulary for authors. Authors choose elements; the
//     renderer paints. There is no inline bold/color in author strings —
//     emphasis comes from picking the right element kind. The few accents
//     (emerald heading, banner divider) live as package constants, not as
//     an author-facing style DSL. See docs/stories/story-style.md.
//   - No terminal sensing or responsive layout. Width is always a caller
//     argument; the package never reads $COLUMNS or a TTY. This keeps the
//     same renderer usable from the TUI, the orchestrator pre-render, and
//     the headless transports without divergent behaviour.
//   - No markdown engine of its own. Rich markdown (tables, fenced code
//     chrome) is delegated to the caller's Glamour pipeline via the
//     `template` kind; the package never imports glamour directly, so the
//     transcript model keeps owning its one expensive TermRenderer.
//   - No interactive widget state. The `choice` renderer emits the static
//     body only — cursor/checkbox/underline affordances are overlaid by
//     the TUI widget on top of this layout. The renderer reserves gutter
//     columns so that overlay never has to re-measure.
//   - No per-view guard-cache eviction. [whenCache] only grows; guard
//     sources are authored, finite, and small, so a process-global cache
//     that never evicts is the right trade rather than a sized LRU.
//
// # Reference
//
//   - Author-facing element catalogue, color rules, and the room shape:
//     docs/stories/story-style.md and the embedded schema
//     (`kitsoki docs app-schema`).
//   - The `choice:` author cookbook (mode selection, recurring patterns):
//     docs/stories/choice-widget.md.
//   - The guard-expression and pongo bridge this package consumes:
//     [kitsoki/internal/expr] and [kitsoki/internal/render].
package elements
