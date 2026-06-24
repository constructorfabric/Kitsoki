# TUI: Collapse the view render chain

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   tui
**Epic:**   [view-rendering-readability.md](view-rendering-readability.md) (slice 2)

## Why

The TUI lays out a view through a **four-stage width chain**, each stage
assuming a different width:

```
machine @80  ──▶  elements dispatcher @vp.Width-4  ──▶  Glamour @vp.Width-2  ──▶  ansi.Hardwrap @vp.Width
(machine.go:2215)  (transcript.go:666 wrapWidth)      (transcript.go:453)       (transcript.go:196 queue)
```

Two things go wrong. For **typed views**, the dispatcher already laid out
plain text (prose reflowed, list/kv aligned) at `vp.Width-4`; feeding
that through Glamour at `vp.Width-2` is double work that can re-break
already-correct lines, and the `-4`/`-2` dance exists only to undo
Glamour's own document margin (`transcript.go:653` comment). For
**non-typed views**, Glamour runs with `WithPreservedNewLines`
(`transcript.go:158`) — which the tech-debt comment at
`transcript.go:571` says caps a 65-col-authored view at 65 even on a
150-col terminal — and on a narrow terminal `ansi.Hardwrap` breaks it
**mid-token**. That mid-word break is the visible "corruption."

Once slice 1 makes every view a typed tree, the non-typed Glamour path is
mostly dead weight; this slice retires it and collapses the chain to one
width budget.

## What changes

**One sentence:** typed elements render straight to styled text at a
single width budget; Glamour is reserved for the `code`/raw escape hatch
only; `WithPreservedNewLines` no longer caps prose.

## Impact

- **Code:** `internal/tui/transcript.go` — `renderViewWith` (`:589`),
  `wrapWidth` (`:666`), the Glamour renderer build (`:152`,`:370`,`:457`),
  `renderGlamour` (`:635`), `queue`/`ansi.Hardwrap` (`:191`).
- **Rendering:** typed `prose/heading/list/kv/banner/choice` render to
  styled text via the elements package (lipgloss styling lives in the
  element renderers, not Glamour); `code`/`template` keep the Glamour
  callback for markdown chrome. Nothing hand-rolled in Go strings — layout
  stays in typed elements.
- **Input:** unchanged.
- **Docs on ship:** `docs/tui/`.

## Mental model

The dispatcher is the renderer. The transcript hands it one width
(`vp.Width - margin`) and prints what comes back. Glamour stops being a
second wrapping pass over the whole view and becomes a styling helper the
`code`/`template` element calls when it needs markdown chrome.

## Layout

```
Before (per view):                       After (per view):
typed tree                               typed tree
  → dispatcher @W-4                         → dispatcher @W   (styled here)
  → Glamour @W-2  (re-wraps everything)     → queue/Hardwrap @W  (safety net only)
  → Hardwrap @W
```

## Rendering changes

- **Single width budget.** Pick one content width and hand it to
  `elements.RenderAll`. Remove the `-4`/`-2` coordination — once Glamour
  no longer post-processes the whole view, there's no doc-margin to
  pre-compensate for. The 40-col floor stays.
- **Glamour scope shrinks** to the `code`/`template` element path only.
  `renderGlamour` (`transcript.go:635`) is invoked solely by the
  `template` element's `GlamourFunc` (`elements/element.go:22`), never
  over a whole view. The terminal-background probe and ANSI-strip guards
  it carries stay relevant only for that path.
- **Drop `WithPreservedNewLines` for prose.** Prose reflows via the
  dispatcher's `wordwrap` (`prose.go:44`); preserved newlines are no
  longer needed to protect prose, and removing them lifts the 65-col cap.
  Verbatim layout that authors used to get from preserved newlines now
  comes from the `code` element (slice 1 parses fenced blocks into it).
- **`ansi.Hardwrap` stays** as the scrollback row-accounting safety net
  (`transcript.go:191` — Bubble Tea's live-region row count drifts if a
  line exceeds the terminal width). After this change it only ever fires
  on genuinely unbreakable over-long tokens (a URL, a ticket id), never
  on whole paragraphs.

## Input & commands

No command or keybinding changes.

| Command / key | Does | Notes |
|---|---|---|
| (resize) | re-renders typed entries at the new width | `SetSize` (`transcript.go:443`) already re-runs the dispatcher per typed entry; now there's no Glamour re-wrap to coordinate |

## Rendering tests

Non-negotiable (CLAUDE.md / `rendering-tests` skill). Each verified to
**fail before** the change:

- **Width-chain collapse** — render a long prose view at width 50, assert
  no line exceeds 50 and no mid-token break appears in the combined
  scrollback (`CapturedIO` + `NewRenderingAnalyzer`). Fails today because
  the 80→Glamour→Hardwrap chain hard-breaks mid-token at narrow widths.
- **Prose grows past the author width** — a view authored hand-wrapped at
  ~60 cols renders to ~110-col lines on a 120-col terminal. Fails today
  (`WithPreservedNewLines` caps at 60).
- **Code stays verbatim** — a `code` element with a 90-col line is
  unchanged at width 50 (horizontal scroll, not reflow).
- **Resize idempotence** — render @120, resize→60, resize→120 yields the
  original @120 output (combined-I/O capture across two `WindowSizeMsg`).

These reuse slice 4's golden corpus where possible.

## Migration plan

No parallel-run needed — the change is internal to the render path. Land
behind slice 1 (every view is typed). The non-typed Glamour-over-whole-
view path is removed only after slice 1 guarantees `TypedView` is always
populated; until then keep it as the `v.IsEmpty()`/error fallback in
`renderViewWith` (`transcript.go:601`).

## Tasks

```
## 1. Render
- [ ] 1.1 Single width budget into elements.RenderAll; drop the -4/-2 dance
- [ ] 1.2 Confine Glamour to the code/template element path; stop re-wrapping whole views
- [ ] 1.3 Remove WithPreservedNewLines for prose (keep verbatim via code element)

## 2. Drive
- [ ] 2.1 Verify SetSize/WindowSizeMsg re-render still single-pass per typed entry

## 3. Prove + document
- [ ] 3.1 Rendering tests above (combined I/O; each verified to fail without the change)
- [ ] 3.2 Manual run at 40/80/120; screenshot before/after
- [ ] 3.3 Update docs/tui/; trim/delete this proposal; update the epic slice row
```

## What we lose, honestly

Authors lose the ability to place a hard line break in prose and trust it
survives — prose reflows, full stop. The explicit escape hatch is the
`code` element (verbatim). This is the intended trade: readability across
widths over author-controlled wrapping.

## Open questions

1. **One width budget value** — `vp.Width - 2`? `- 1`? The old `-4` was
   pure Glamour-margin compensation. *Lean: `vp.Width - 1` for a thin
   gutter; settle empirically against the golden corpus.*

## Non-goals

- The web render path (slice 3).
- Source-color banding (`sourcecolor.Colorize`, `transcript.go:245`) —
  unchanged; it runs after this chain regardless.
