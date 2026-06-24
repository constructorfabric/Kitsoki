# Runtime: Canonical typed view

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   runtime
**Epic:**   [view-rendering-readability.md](view-rendering-readability.md) (slice 1)

## Why

A view's canonical representation should be its **typed element tree** —
a width-free semantic document — not a pre-rendered string. Today it's
the inverse: `TurnOutcome.View` (a width-80 string) is primary, with a
`json` tag; `TurnOutcome.TypedView` is secondary (`json:"-"`,
`outcome.go:76`) and is `nil` for most views.

`machine.go:2326` (`typedViewIsElementArray`) only attaches a `TypedView`
for a **pure element array**:

```go
func typedViewIsElementArray(v app.View) bool {
    return v.TemplateFile == "" && v.Extends == "" && v.Source == "" && len(v.Elements) > 0
}
```

So legacy scalar `view: |` markdown, `extends:`/`blocks:`,
`template_file:`, and any view reached with `say:` prepended
(`machine.go:2310`, raw `say + "\n\n" + viewText`) all collapse to one
width-80 string (`blockRenderWidth = 80`, `machine.go:2215`) that no
surface can reflow. Every downstream readability bug in the epic traces
back to this `nil`. This slice is the keystone: make `TypedView` the
populated, canonical payload for **every** view shape.

## What changes

**One sentence:** every view shape normalizes to typed elements at load
time, and `renderViewWithTyped` returns a populated tree for every view —
`View string` becomes a derived width-80 projection, kept only for
back-compat callers.

## Impact

- **Code seams:** `internal/app/view_element.go` (load-time
  normalization), new `internal/render/elements/markdown` (markdown→
  elements adapter), `internal/machine/machine.go:2259`
  (`renderViewWithTyped`), `:2325` (delete `typedViewIsElementArray`),
  `:2310` (`say:` concat), `internal/orchestrator/outcome.go:67`
  (`View` demoted to derived).
- **Vocabulary:** no new authoring surface. The `prose|heading|code|
  list|kv|banner|choice` kinds (`view_element.go:106`) are unchanged; this
  slice changes only how non-element shapes *reach* them.
- **Stories affected:** none behaviorally — every existing view renders
  the same bytes at width 80; the round-trip test (below) is the contract.
- **Backward compat:** `View string` stays on the wire (Epic shared
  decision 3); `extends:`/`template_file:` keep rendering through their
  pongo chrome, wrapped as one `template` element (Epic shared decision 2).
- **Docs on ship:** `docs/embedded/app-schema.md` §"view:",
  `docs/stories/story-style.md`.

## Vocabulary changes

| Kind | Name | Shape | Notes |
|---|---|---|---|
| world key | — | — | none |
| effect | — | — | none |

No vocabulary change — this is an internal normalization. The authoring
surface is unchanged; the lint in slice 4 nudges authors toward typed
elements but nothing breaks.

## The model

Normalization happens once, at load, in `View.UnmarshalYAML` /
a `View.Normalize()` step:

```
view: |  (markdown)        ──parse──▶  [prose, heading, code, list, …]
view: "…\n\n…" (prose)     ──split──▶  [prose, prose, …]   (one per blank-line block)
say: "…" + view            ──prepend─▶ [prose(say), …view elements…]
extends:/template_file:    ──wrap────▶ [template(rendered-chrome)]   (deferred: typed layout)
```

- **INTERPRETIVE → DETERMINISTIC boundary is unchanged.** Leaf-string
  pongo expansion still happens at render time (`renderLeaf`,
  `elements/element.go:65`); normalization only restructures *layout*,
  not template evaluation.
- **The markdown→elements adapter** (`internal/render/elements/markdown`):
  blank-line-separated paragraphs → `prose`; ATX `#`/`##` → `heading`;
  fenced ```` ``` ```` → `code` (verbatim); `-`/`*`/`N.` runs → `list`.
  Pongo `{{ }}`/`{% %}` survives into element `Source` untouched. This
  adapter is the one risky piece — the round-trip test fences it.
- **`say:`** becomes a leading `prose` element instead of string concat,
  so it reflows like any other narration.

## Decision recording

No new interpretive decision is introduced, so no new trace event. But
the view that *is* recorded changes shape: slice 3 (tracing) records the
typed tree instead of the width-80 string. This slice makes that tree
available (always-populated `TypedView`); slice 3 records it. Linked
there rather than duplicated.

## Engine seams & invariants

- `renderViewWithTyped` (`machine.go:2259`) returns the normalized tree
  for every branch; `typedViewIsElementArray` (`:2325`) is deleted.
- **Load-time invariant:** the markdown adapter must produce a tree that
  re-renders (at width 80, identity glamour) byte-for-byte equal to the
  legacy `render.Pongo` output for the same source — asserted in the
  round-trip test, not at runtime. A source the adapter cannot model
  (rare exotic markdown) fails *load* with a clear
  `state → view: cannot normalize markdown (<reason>)` message rather
  than silently degrading.
- `extends:`/`template_file:` keep their existing
  `renderViewBody` paths (`machine.go:2168`,`:2174`); their output is
  wrapped as a single `template` element so `TypedView` is non-nil.

## Backward compatibility / migration

Default-on; no story migration required. Every existing view keeps
rendering identical bytes at width 80 (the round-trip test is the
guarantee). The legacy-markdown adapter is the only behavior change, and
it's invisible at width 80; the *new* benefit (reflow at other widths)
surfaces only once slices 2/3 consume the tree.

## Tasks

```
## 1. Engine
- [ ] 1.1 markdown→elements adapter (internal/render/elements/markdown)
- [ ] 1.2 View.Normalize(): scalar→elements, say→leading prose, extends/template_file→template element
- [ ] 1.3 renderViewWithTyped returns the tree for every shape; delete typedViewIsElementArray
- [ ] 1.4 Demote TurnOutcome.View to a derived width-80 projection (doc it)
- [ ] 1.5 Load-time fail-fast on un-normalizable markdown

## 2. Verification
- [ ] 2.1 Adapter round-trip: every stories/**/rooms/*.yaml view normalizes + renders == legacy @80
- [ ] 2.2 Unit: say-prefixed view yields a leading prose element (not string concat)
- [ ] 2.3 kitsoki turn on a legacy-scalar room emits a populated typed_view

## 3. Adopt + document
- [ ] 3.1 Update app-schema.md §view: + story-style.md (typed is canonical; scalar is sugar)
- [ ] 3.2 Trim/delete this proposal; update the epic slice row
```

## Verification

Stateless and LLM-free: `kitsoki turn --state <room> --intent <i>
--world @w.json` on representative rooms of each shape (legacy-scalar:
`stories/dev-story/rooms/code_review.yaml`; element-array:
`stories/prd/rooms/clarifying.yaml`; `say:`-prefixed: any) and assert
`typed_view` is populated and `view_rendered` is unchanged at width 80.
The corpus round-trip test (2.1) is the regression contract.

## Open questions

1. **Folded-scalar `prose` that already collapses correctly vs. literal
   `\n` prose** — both normalize identically post-adapter; do we *also*
   rewrite the YAML source to the cleaner form? *Lean: no — that's slice
   4's `--fix`, not a load-time concern.*

## Non-goals

- Migrating `extends:` chrome to a typed layout element (Epic shared
  decision 2; deferred follow-up).
- Deleting `TurnOutcome.View` (Epic shared decision 3; separate cleanup).
- Any TUI/web rendering change — this slice only makes the tree available
  (slices 2/3 consume it).
