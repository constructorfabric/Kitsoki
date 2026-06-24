# Epic: {Title}

**Status:** Draft v1. No slices implemented yet.
**Kind:**   epic
**Slices:** {N} ({done}/{N} shipped)

<!--
  An "epic" is an umbrella for a change too big for one review. It owns the
  big-picture Why/What/Impact and the SEAMS BETWEEN slices; the focused
  child proposals own the detail. The epic is an index + a sequencing plan,
  not a place to design — push design down into the children.

  Each child is a normal focused proposal (story.md / runtime.md / tui.md /
  tracing.md) that names this epic in its `**Epic:**` line. A child is
  right-sized when it has one coherent Why, fits in one reviewer's head,
  and could ship alone or with one named dependency.
-->

## Why

<!-- The big-picture motivation. The whole change in one paragraph — the
     thing none of the individual slices fully captures on its own. -->

## What changes

<!-- The end state across all slices, in one screen. What's true once every
     slice has shipped. -->

## Impact

- **Spans:** {which kinds — story / runtime / tui / tracing}
- **Net surface:** {the combined footprint at a glance}
- **Docs on ship:** {the eventual home(s) for the shipped pieces}

## Slices

<!-- The index. One row per child proposal. Status mirrors each child's
     Status line. Keep this table current — it's the source of truth for
     "where is this epic." -->

| # | Slice | Kind | Scope (one line) | Depends on | Status | File |
|---|---|---|---|---|---|---|
| 1 | {name} | runtime | {what it adds to the engine} | — | Draft | [`{file}.md`]({file}.md) |
| 2 | {name} | story | {the story that uses #1} | 1 | Draft | [`{file}.md`]({file}.md) |
| 3 | {name} | tui | {the surface that drives #2} | 2 | Draft | [`{file}.md`]({file}.md) |

## Sequencing

<!-- The order slices must land and why. Usually: runtime substrate first,
     then the story that uses it, then the TUI that drives it, with tracing
     slotted wherever its events are produced. Call out anything that can
     ship in parallel. -->

```
#1 (runtime) ──▶ #2 (story) ──▶ #3 (tui)
                     └──────────▶ #4 (tracing, parallel once #1 emits)
```

## Shared decisions

<!-- Decisions that span slices and so don't belong in any single child:
     a naming convention, a shared schema, a compat boundary. Each child
     defers to this section rather than re-litigating. -->

1. {Cross-slice decision} — {resolution / lean}.

## Cross-cutting open questions

<!-- Questions that affect more than one slice. Per-slice questions live in
     the child proposals. -->

1. {Question} — {options}. *Lean: {x}.*

## Non-goals

- {What the epic as a whole explicitly excludes.}

<!--
  Lifecycle: as each slice ships, update its row's Status and migrate its
  detail into docs/ per that child's own plan, then delete the child file.
  When every slice has shipped, the epic is just an empty index — delete it
  too. Git history preserves the decomposition.
-->
