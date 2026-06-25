# Epic: Cross-environment view readability

**Status:** Partially implemented. Typed-view rendering is widely wired through
the TUI and web (`ViewElement.vue`, `TurnOutcome.TypedView`, TUI typed resize
tests, legacy prose reflow tests, media element rendering), but the proposal is
not complete: there is no `kitsoki view` proofing command, `TypedView` can still
be nil for some legacy/template paths, and the web still has the fallback
preformatted string branch for untyped entries.
**Kind:**   epic
**Slices:** 4 (partial; no slice fully closed)

## Why

A room view is prose an operator reads. Today that prose renders badly,
and the new `kitsoki web` surface (commits `153c3d6`, `2025b28`) made it
worse — the browser has no Glamour to paper over the seams, so it shows
the damage raw.

Two rendering models live in the codebase and fight. The **semantic
model** — typed elements (`prose`, `heading`, `list`, `kv`, `code`,
`banner`, `choice`) — is width-free and reflows
(`internal/render/elements/prose.go:40`); it renders cleanly in both
surfaces. The **pre-formatted-string model** — legacy scalar `view: |`
markdown, `extends:`/`template_file:` chrome, `say:` text, and `prose:`
bodies stuffed with literal `\n` — carries baked-in line breaks and
cannot reflow.

The split is load-bearing, not stylistic. `machine.go:2326`
(`typedViewIsElementArray`) attaches a `TypedView` **only** for pure
element-array views; every other shape collapses to a single width-80
string (`blockRenderWidth = 80`, `machine.go:2215`) with
`TypedView == nil`. Downstream, the TUI runs that fossil through Glamour
with `WithPreservedNewLines` (`transcript.go:158`) — capping prose at the
author's hand-wrap width (`transcript.go:571`) and hard-wrapping it
mid-token on narrow terminals (`transcript.go:196`) — and the web shows
the same 80-col string as `white-space: pre-wrap` monospace
(`ChatTranscript.vue:31`,`:211`): a terminal screenshot rendered as text.
No tooling exists to proof a view; `kitsoki render` renders app *docs*,
not room views.

## What changes

Once every slice ships: the **typed element tree is the single canonical
view representation**. The engine records it in the trace and ships it to
every surface; the TUI and web each project it at their own width; the
width-80 string is a deprecated derived projection. A `kitsoki view`
command + lint catalog lets any author — human or AI — proof a view
across widths and environments before shipping it, and the
`kitsoki-story-authoring` loop requires it.

## Impact

- **Spans:** runtime (canonical typed view), tui (render chain), tracing
  (trace fidelity + web viewer), tooling (proofing CLI + tests).
- **Net surface:** `internal/app/view_element.go`,
  `internal/machine/machine.go`, `internal/orchestrator/outcome.go`,
  `internal/render/elements/` (+ new `markdown`, `lint`),
  `internal/tui/transcript.go`, `internal/runstatus/server/driver.go`,
  `tools/runstatus/src/components/`, new `cmd/kitsoki/view.go`.
- **Docs on ship:** `docs/stories/story-style.md`,
  `docs/embedded/app-schema.md` §"view:", `docs/tui/`, `docs/tracing/`,
  `.agents/skills/kitsoki-story-authoring/SKILL.md`.

## Slices

| # | Slice | Kind | Scope (one line) | Depends on | Status | File |
|---|---|---|---|---|---|---|
| 1 | Canonical typed view | runtime | Normalize every view shape to typed elements at load; always populate `TypedView`; `say:`→leading prose; demote `View string` to derived | — | Draft | [`view-canonical-typed.md`](view-canonical-typed.md) |
| 2 | TUI render chain | tui | Collapse the four-stage width chain; render typed elements direct-to-styled; shrink Glamour to the code/raw escape hatch; drop `PreservedNewLines` for prose | 1 | Draft | [`view-tui-rendering.md`](view-tui-rendering.md) |
| 3 | Trace + web typed | tracing | Record the typed tree in the trace; web always renders typed elements; delete the 80-col fossil fallback | 1 | Draft | [`view-trace-and-web-typed.md`](view-trace-and-web-typed.md) |
| 4 | Proofing tooling | tui | `kitsoki view` + lint catalog + cross-env golden/property tests + authoring-skill wiring | 1 | Draft | [`view-proofing-tooling.md`](view-proofing-tooling.md) |

## Sequencing

Slice 1 is the keystone: once every view normalizes to a typed tree, the
rest are independent and can land in parallel.

```
#1 (runtime, canonical typed view) ─┬─▶ #2 (tui render chain)
                                     ├─▶ #3 (trace + web typed)   [parallel with #2]
                                     └─▶ #4 (proofing tooling)    [can start now; hardened by #1]
```

#4's lint and golden tests are the safety net the other slices verify
against — start it early, against the current typed corpus, then widen
its coverage as #1 lands.

## Shared decisions

These span slices; each child defers here rather than re-deciding.

1. **Canonical form = the typed element tree.** A rendered string is a
   *projection* of the tree for one environment at one width. The engine
   records and transmits the tree; surfaces project. This is the
   architecture moat applied to rendering (memory
   `kitsoki-moat-is-architecture`) and what `tools/runstatus/CLAUDE.md`
   demands — the trace records the semantic view, not a width fossil.
2. **Legacy markdown is *parsed* into elements**, not wrapped opaque —
   that's what buys reflow everywhere. The `extends:`/`template_file:`
   pongo chrome is the lone exception: short-term it's wrapped as one
   `template` element (so it has a typed home and the web stops special-
   casing); a full typed-layout migration is deferred (own follow-up).
3. **`TurnOutcome.View string` stays as a derived, deprecated projection
   through this epic** and is deleted in a separate cleanup once no
   consumer reads it. No slice may treat it as the source of truth.
4. **The element contract is frozen and shared.** prose reflows · code
   verbatim · list/kv collapse-and-align · heading · banner · choice. A
   single fixture corpus expresses it; both the Go (TUI/plain) and Vue
   (web) backends are tested against it (#4 owns the corpus; #2 and #3
   consume it).

## Cross-cutting open questions

1. **Parse vs. wrap legacy markdown** — parsing gives true reflow but the
   adapter must round-trip the corpus; wrapping is safe but keeps the web
   fossil. *Lean: parse, fenced by #4's round-trip test* (Shared
   decision 2).
2. **Migrate `extends:` chrome to typed now, or defer?** *Lean: defer* —
   #1 gives it a typed home; full typed-layout is its own design.
3. **Web proofing: headless browser vs. shared JSON fixture corpus?**
   *Lean: shared fixture corpus both backends snapshot, plus optional
   headless mode in `kitsoki view` for local use* (#4).

## Non-goals

- Rich markdown in the web viewer (tables, images, syntax highlighting).
  The contract stays the seven element kinds.
- Theming / localization (memory `demo-templates-localization`) — the
  canonical tree is its precondition, not its delivery.
- Routing, host calls, world semantics — untouched.
- Replacing pongo2. Leaf-string `{{ }}`/`{% %}` expansion (`renderLeaf`)
  is unchanged; only baked-in *layout* moves.
