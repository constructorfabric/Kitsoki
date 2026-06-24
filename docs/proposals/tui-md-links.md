# TUI: terminal-friendly links to markdown artifacts

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   tui
**Epic:**   [`review-externally.md`](review-externally.md)

## Why

In the web UI an operator can click a `.md` artifact path in a `kv` block
and read it in a modal — `ViewElement.vue:165-177` detects the path
(`isMarkdownPath`, `/\S+\.md$/`, line 127) and renders it as a button into
`MarkdownModal.vue`. In the TUI the same kv value is inert plain text: the
renderer reflows the string and stops (`internal/render/elements/kv.go:82-97`).
An operator reviewing a proposal or design brief sees the most important
thing on screen — the artifact path — as dead text they must hand-copy,
leave the TUI, and `cat` or open themselves. The TUI has **zero** terminal-
hyperlink support today (no OSC 8 escape anywhere in `internal/tui/` or
`internal/render/`).

## What changes

Two affordances, chosen by terminal capability — never one that silently
fails:

1. **OSC 8 hyperlinks.** In the kv renderer, a value matching the web's
   `isMarkdownPath` is wrapped in an OSC 8 hyperlink so supporting terminals
   (iTerm2, kitty, WezTerm, modern xterm) render it clickable → the OS opens
   it. Terminals without OSC 8 ignore the escape and show the plain path —
   safe, automatic degradation.
2. **`/open <path>` slash command.** The universal, keyboard, terminal-
   agnostic fallback: resolve a relative artifact path against the run's
   artifact dir and open it via the OS opener (`open` / `xdg-open`) or
   `$EDITOR`. Also the verb the review-diff room ([`diff-open-fallback.md`](diff-open-fallback.md))
   points at for "open the changed files."

One sentence: **make the artifact path on screen actually openable — clickable
where the terminal allows, and `/open`-able everywhere.**

Rendering stays typed-elements + pongo2: the OSC 8 wrap is a *styling* step
applied to the already-computed value string inside the kv renderer, not a
hand-rolled Go view (CLAUDE.md / the view-rendering proposals). Layout is
still data.

## Mental model

The path you see is a door. If your terminal has hands (OSC 8), click it.
Otherwise, `/open` it. Either way kitsoki hands the file to the surface you
already read markdown in — it does not try to be that surface.

## Layout

```
Before:                                   After (OSC 8-capable terminal):
  Brief:  docs/proposals/.workspace/         Brief:  docs/proposals/.workspace/
          x/001-brief.md                             x/001-brief.md   ← underlined, clickable
                                                 (older terminal: identical plain text)
```

## Rendering changes

- `internal/render/elements/kv.go`: after `renderLeaf` produces the value
  string (line 46), if the value `isMarkdownPath`, wrap it in
  `\x1b]8;;file://<abs>\x1b\\<text>\x1b]8;;\x1b\\` and apply a lipgloss
  underline style. A small `internal/render/...` helper owns the escape so
  the sequence lives in one place (and the web/TUI share the *detection*
  predicate, per the epic's shared decision #2).
- **Width accounting is the trap.** The kv value reflows via
  `wordwrap.String(vl, valWidth)` (`kv.go:85`), which counts bytes — the
  OSC 8 escape has zero visible width but many bytes, so naive wrapping
  would mis-measure and corrupt the column. *Lean for v1: only linkify when
  the value is a single path token that already fits on the line; a path
  that would wrap stays plain and relies on `/open`.* (Open question 1
  weighs an escape-aware wrapper instead.)
- Nothing else changes: non-`.md` values, keys, and multi-line values render
  exactly as today.

## Input & commands

| Command / key | Does | Notes |
|---|---|---|
| `/open <path>` | Resolves `<path>` against the run artifact dir, opens via OS opener / `$EDITOR` | Always available; the universal fallback and the keyboard path |

`/open` lives alongside the existing `/ide` command family
(`internal/tui/commands_ide.go`) and uses the same artifact-dir resolution
the `media` element already relies on (`internal/render/elements/media.go`).

## Rendering tests

NON-NEGOTIABLE (template rule + CLAUDE.md). Using `CapturedIO` /
`NewRenderingAnalyzer`:

- **osc8-wrap** — a kv value with a `.md` path emits the OSC 8 bytes around
  the correct visible text, and the **visible** value text is byte-identical
  to the plain render (so the column/width math is unchanged). Assert the
  test **fails** without the change.
- **no-escape-for-non-md** — a non-`.md` value emits no escape.
- **width-unaffected** — a row containing a linkified value wraps at the same
  column as the same row rendered plain (guards the width trap above).

These capture `View()` output together so the escape-vs-layout interaction
can't hide — exactly the bug class the rendering-tests skill exists for.

## What we lose, honestly

- OSC 8 is invisible/unclickable on older terminals and behaves
  inconsistently under some multiplexers (tmux passthrough quirks). That's
  why `/open` exists as the guaranteed path.
- The OSC 8 click target is a `file://` URL the **OS** opens with its default
  `.md` handler — which may not be the operator's preferred markdown reader.
  Only `/open` (via `$EDITOR`) is operator-controllable.
- We do **not** replicate the web's in-app modal preview in the terminal —
  no TUI markdown pager. We hand the file to the OS / `$EDITOR` and stop.

## Open questions

1. **OSC 8 × wordwrap.** Linkify-only-when-it-fits (simple, v1 lean) vs. an
   escape-aware wrapper that ignores OSC 8 bytes when measuring (handles
   wrapped paths, more surface area). *Lean: when-it-fits for v1.*
2. **Detect-by-regex vs. explicit field.** Mirror the web's `isMarkdownPath`
   now (parity, no schema change) or add an explicit "this kv value is a
   file link" element field. *Lean: mirror the regex; promote to a field
   only when a non-`.md` artifact wants a link (epic shared decision #2).*
3. **`media` paths too?** The `media` element renders a pointer block
   (`media.go:56-83`). Should those paths also linkify? *Lean: out of scope
   here — separate change, different element.*

## Non-goals

- In-TUI markdown rendering / a pager — we open externally.
- Linkifying arbitrary URLs in `prose` — a separate concern.
- The diff-review host call and room pattern — that is the
  [`diff-open-fallback.md`](diff-open-fallback.md) slice.
