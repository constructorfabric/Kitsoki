# Proposal templates

A small set of **lightweight, read-first** templates for kitsoki
proposals. The goal is a proposal you can skim in two minutes and a
shape that's the same across authors — not a form to fill in.

These templates plug into the lifecycle already described in
[`../README.md`](../README.md): a proposal lands as a draft with a
**Status** line, sheds implemented sections into normal `docs/` as it
ships, and is **deleted** once everything has shipped or been
superseded. The templates don't change that — they just give the draft
a consistent skeleton.

## The shared spine

Every focused proposal opens the same way, then adds kind-specific
design sections, then closes the same way:

```
# {Kind}: {Title}

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   story | runtime | tui | tracing
**Epic:**   ../{epic}.md   (or "— standalone")

## Why            — the problem / motivation, in the reader's terms
## What changes   — the specific change, in one screen
## Impact         — what it touches: files, stories, hosts, docs, compat

… kind-specific design sections …

## Tasks          — phased checklist; the execution contract
## Open questions — decisions the author wants reviewed
## Non-goals      — what this explicitly does NOT do
```

`Why / What changes / Impact` is borrowed from
[OpenSpec](https://github.com/Fission-AI/OpenSpec); the `Status` line,
phasing, open-questions and non-goals are the conventions already in
use across this folder. Keep prose tight — link to the gold-standard
code and existing docs instead of re-explaining them.

## Which template

| If the change is about… | Use | Lands in |
|---|---|---|
| A new or reworked **operator story** (rooms, world, prompts, flows) | [`story.md`](story.md) | `stories/<name>/` |
| **Engine / runtime** behavior (gates, deciders, effects, host calls, world semantics) | [`runtime.md`](runtime.md) | `internal/…`, `docs/stories/state-machine.md`, `docs/architecture/` |
| **TUI** layout, typed-view rendering, slash commands, input | [`tui.md`](tui.md) | `internal/tui/`, `docs/tui/` |
| **Tracing / observability** — trace events, cassette fidelity, run-status surfaces | [`tracing.md`](tracing.md) | `internal/…`, `docs/tracing/` |
| A **large** change that spans several of the above | [`epic.md`](epic.md) | an umbrella that links focused children |

When a change doesn't fit one box cleanly, prefer the template whose
*design sections* you'll actually use, and note the spillover under
**Impact**. When it spans several boxes, it's an epic — decompose it
(below).

## Decomposing a large proposal

A proposal that touches a story **and** an engine seam **and** the TUI
is three reviews wearing a trenchcoat. Split it:

1. Start an **epic** ([`epic.md`](epic.md)) capturing the big-picture
   *Why / What changes / Impact* and the **slice table**.
2. For each slice, create a focused child proposal from the matching
   template. Each child is independently reviewable and shippable, and
   names its parent in the `**Epic:**` line.
3. The epic's slice table is the index: it tracks each child's kind,
   one-line scope, status, and sequencing (which slice must land
   first). Children carry the detail; the epic carries the seams
   *between* them and any decision that spans slices.

A slice is right-sized when it has one coherent `Why`, one reviewer can
hold it in their head, and it could ship without the others (or with a
named dependency on one that lands first).

The [`proposal-authoring`](../../skills/proposal-authoring/SKILL.md)
skill drives both flows — picking a template and decomposing an epic.

## Conventions

- **Placeholders** are `{like this}`. **Guidance** lives in
  `<!-- HTML comments -->`. Delete both as you write — a finished
  proposal has neither.
- **Drop sections that don't apply.** The templates are a menu, not a
  checklist. An empty "Migration" heading is noise; cut it.
- **Link, don't restate.** Reference `file:line`, existing docs, and
  the gold-standard stories rather than reproducing them.
- **One Status line, always.** It's the first thing a reader needs:
  what's implemented, what isn't, where the shipped pieces went.
