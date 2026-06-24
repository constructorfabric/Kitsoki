# TUI: {Title}

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   tui
**Epic:**   — standalone   <!-- or ../{epic}.md -->

<!--
  A "TUI" proposal changes what the operator sees and how they drive it:
  layout, typed-view rendering, slash commands, input handling,
  keybindings, footers, mode visualization.

  Two hard rules for this kind:
  1. Keep rendering going through typed elements + pongo2 templates — do
     not hand-roll strings in Go (see memory: demo-templates-localization,
     view-elements proposal). Layout is data, not printf.
  2. Rendering bugs hide from unit tests. Anything touching concurrent I/O
     (slog + render, input queue + transcript) MUST have a test that
     captures combined I/O — see CLAUDE.md and the rendering-tests skill.

  References: docs/tui/README.md; docs/embedded/app-schema.md; internal/tui/;
  rendering-tests skill; CapturedIO in rendering_test_utils.go.
-->

## Why

<!-- The operator-experience problem. What's confusing, noisy, or
     impossible to see in the current TUI? Screenshot-in-prose if it helps. -->

## What changes

<!-- The change in one screen. What the operator will do differently, and
     the one sentence that frames it ("replace the multi-pane mouse-driven
     TUI with a single-pane chat + slash commands"). -->

## Impact

- **Code:** `internal/tui/{…}` — {model, update, view, components touched}
- **Rendering:** {new/changed typed elements; pongo2 templates; what stays vs. goes}
- **Input:** {slash commands, keybindings, queue vs. immediate}
- **Docs on ship:** `docs/tui/{…}.md`

## Mental model

<!-- The metaphor the operator holds. One short paragraph. -->

## Layout

```
Before:                          After:
┌───────┬───────┐                ┌───────────────┐
│ pane  │ pane  │                │  transcript   │
│       │       │       ──▶      │               │
└───────┴───────┘                ├───────────────┤
                                 │ > input       │
                                 └───────────────┘
```

<!-- Before/after ASCII. Show the regions and where the active state,
     menu, and input live. -->

## Rendering changes

<!-- What renders, via which typed elements, against which world. What
     stays unchanged, what's removed. Keep it data-driven — call out any
     place tempted toward hand-rolled string building and reject it. -->

## Input & commands

<!-- Slash commands (name → effect), keybindings, and the input pipeline
     (echo, live routing, queue vs. immediate). Note feedback the operator
     gets while routing. -->

| Command / key | Does | Notes |
|---|---|---|
| `/{cmd}` | {effect} | {availability} |

## Rendering tests

<!-- NON-NEGOTIABLE for anything touching concurrent I/O. Name the tests
     and what combined output they assert on. Confirm each test FAILS
     without the change. Use CapturedIO / NewRenderingAnalyzer. -->

- {test} — captures {files/stderr/View() together}, asserts {no corruption / correct layout}.

## Migration plan

<!-- If this replaces an existing surface: the steps, what runs in parallel
     during the transition, and the cutover. -->

## Tasks

```
## 1. Render
- [ ] 1.1 Typed element(s) / pongo2 template(s)
- [ ] 1.2 Layout wired into View()

## 2. Drive
- [ ] 2.1 Slash commands / keybindings / input pipeline

## 3. Prove + document
- [ ] 3.1 Rendering tests (combined I/O; verified to fail without the change)
- [ ] 3.2 Manual run; screenshot the new surface
- [ ] 3.3 Update docs/tui/; trim/delete this proposal
```

## What we lose, honestly

<!-- Every TUI change trades something away. Name it plainly — the
     single-pane proposal's "What we lose, honestly" section is the model. -->

## Open questions

1. {Question} — {options}. *Lean: {x}.*

## Non-goals

- {Adjacent surface this explicitly leaves alone.}
