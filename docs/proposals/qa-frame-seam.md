# TUI: One frame composer — the full screen a human sees

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   tui
**Epic:**   [story-qa-agent.md](story-qa-agent.md) (slice 1)

## Why

There is no function in the codebase that returns "the screen a human
sees." The pieces live apart:

- The **room body** is printed to terminal scrollback via `tea.Println`
  out of the transcript flush (`transcript.go:238`), never returned to a
  caller.
- The **bottom chrome** is assembled inline in `RootModel.View()`
  (`tui.go:4092`): the live routing line (`m.transcript.LiveLine()`), the
  action-required banner, the divider, the mode-prefixed prompt
  (`> » # ? …`), the per-room footer (`footerStoryLine`), and the
  framework status row (`room · state · mode · queue`, `tui.go:4216-4242`).
- The trace's `view_rendered` is a width-80, ANSI-stripped,
  `IdentityGlamour` projection of the **body only**
  (`machine.go:2215`, `journal_write.go:344`).

So the only artifact a headless caller can get today (`view_rendered`) is
neither the right width, the right styling, nor the whole screen. A QA
agent reviewing it reviews something no operator ever sees. Slice 2
(`kitsoki drive`) and slice 3 (`kitsoki shot`) both need the *assembled*
screen; this slice is the seam that produces it.

## What changes

**One sentence:** extract a single `Frame` composer that, given the
current model state and a target width/height, returns the assembled
screen — room body + all chrome — as `{text, ansi, metadata}`, and route
the live TUI's own painting through it so the two cannot drift.

## Impact

- **Code:** `internal/tui/` — a new `frame.go` (the composer + `Frame`
  type); `RootModel.View()` (`tui.go:4092`) refactored to build its bottom
  region by calling the composer; the transcript's last-body accessor
  (`transcript.go`) exposed so the composer can include the current body.
- **Rendering:** no new layout. The composer calls the **existing**
  element/transcript render path (`renderViewWith`, `transcript.go:589`)
  and the **existing** chrome assembly. Nothing hand-rolled in Go strings.
- **Input:** unchanged.
- **Docs on ship:** `docs/tui/` (a short "frame composition" note).

## Mental model

The TUI already knows how to paint a screen — it just does it in two
places (scrollback + live `View()`) and throws the bytes at the terminal.
The composer is that same paint, captured into a value instead of emitted:
*one width in, one `Frame` out.* The live TUI becomes the first consumer
of its own frame.

## Layout

```
Frame (what the composer returns)         Sources (unchanged renderers)
┌─────────────────────────────────┐
│ <last room body, reflowed @W>   │  ◀── transcript.renderViewWith (transcript.go:589)
│                                 │
│ <live routing/thinking line>    │  ◀── transcript.LiveLine()
│ <action-required banner>        │  ◀── inbox.ActionRequiredBanner()
│ ────────────────────────────── │  ◀── blocks.Divider()
│ > <prompt + mode prefix>        │  ◀── promptLine (tui.go:4128-4214)
│ <per-room footer>               │  ◀── footerStoryLine
│ room · state · mode · queue     │  ◀── blocks.StatusRow
└─────────────────────────────────┘
```

## Rendering changes

- **New `Frame` type** (shared decision 1 of the epic):

  ```go
  type Frame struct {
      Text     string   // ANSI-stripped, agent-readable
      ANSI     string   // styled, screenshot-ready (slice 3)
      Width    int
      Height   int
      Metadata FrameMeta
  }
  type FrameMeta struct {
      State          string   // current state path
      Mode           string   // normal | meta | off-path | slot-fill | awaiting
      AllowedIntents []string // the menu the operator could pick
      WorldDigest    map[string]any // small, for the agent to reason on
  }
  ```

- **`ComposeFrame(m *RootModel, width, height int) Frame`** assembles body
  + chrome by calling the existing renderers at `width`. It produces both
  the styled string and its ANSI-stripped twin (`ansi.Strip`, already used
  at `journal_write.go:344`). `Metadata` is read from the model
  (`allowedIntents`, `m.mode`, current state, world) — data the model
  already holds.
- **`RootModel.View()` calls the composer** for its bottom region rather
  than re-assembling `parts` inline. The live path keeps emitting
  scrollback via `tea.Println` as today; the composer's *body* portion is
  the current view (last flushed entry), so a single still frame is whole.
- The composer is **width-parameterized** — the live TUI passes
  `m.width`; a headless caller passes `--cols`. Same code, so a screenshot
  at 100 cols is byte-identical to a real 100-col terminal.

## Input & commands

No command or keybinding changes — this is internal plumbing.

| Command / key | Does | Notes |
|---|---|---|
| (resize) | live TUI re-composes at the new width | `View()` already re-runs on `WindowSizeMsg`; now via the composer |

## Rendering tests

Non-negotiable (CLAUDE.md / `rendering-tests` skill). Each verified to
**fail before** the change (today there is no composer to assert against):

- **Frame equals live paint** — drive the model to a known state; assert
  `ComposeFrame(m, 100, 30).Text` contains the room body **and** every
  chrome element (divider, prompt prefix, status row) that
  `RootModel.View()` emits, with the same line ordering. Guards against the
  composer drifting from the real screen. Uses `CapturedIO` +
  `NewRenderingAnalyzer`.
- **Width fidelity** — `ComposeFrame(m, 50, …)` produces no line longer
  than 50 (no chrome overflow); `…, 120, …` reflows prose past the 80-col
  fossil width. Proves the headless frame matches a real terminal at that
  width, not the width-80 trace.
- **Metadata matches the machine** — `Frame.Metadata.AllowedIntents`
  equals the intents `inspect`/`turn` report for that state; `State` and
  `Mode` match. Lets the agent trust metadata without re-parsing text.
- **ANSI/Text twins agree** — `ansi.Strip(Frame.ANSI) == Frame.Text`.

## Migration plan

Internal refactor, no parallel run. `RootModel.View()` keeps its current
output byte-for-byte (a golden test pins it before the change); the
composer is introduced as the implementation behind it, then exposed for
slices 2/3. Land after — or alongside — `view-rendering-readability`
slice 2 so the body the composer captures is already width-clean; if this
lands first, it simply captures today's (less clean) output and improves
for free when that slice lands.

## Tasks

```
## 1. Render
- [ ] 1.1 Frame / FrameMeta types in internal/tui/frame.go
- [ ] 1.2 ComposeFrame: assemble body + chrome via existing renderers at a given width
- [ ] 1.3 Produce ANSI + ANSI-stripped twins; populate metadata from the model

## 2. Drive
- [ ] 2.1 Route RootModel.View()'s bottom region through ComposeFrame (output unchanged)

## 3. Prove + document
- [ ] 3.1 Rendering tests above (combined I/O; each verified to fail without the change)
- [ ] 3.2 Golden-pin RootModel.View() output before/after to prove no live-TUI regression
- [ ] 3.3 Short docs/tui/ note; update the epic slice row
```

## What we lose, honestly

A little indirection: `View()` no longer reads as one linear `parts`
builder — the bottom region routes through a composer. The trade is that
the live screen and every headless capture become the *same* bytes, which
is the entire point of the epic. Net: more testable, marginally less
glanceable.

## Open questions

1. **Include the current body in the `Frame`, or chrome only?** A still
   screenshot wants the body; the live `View()` doesn't re-emit it (it's
   in scrollback). *Lean: the composer includes the last flushed body for
   headless callers; `RootModel.View()` takes only the chrome portion of
   the returned `Frame` so live output is unchanged.*

## Non-goals

- Scrollback history in the frame (the driver's JSONL keeps per-turn
  frames — epic open question 1).
- Any change to *how* elements render (that's `view-rendering-readability`).
