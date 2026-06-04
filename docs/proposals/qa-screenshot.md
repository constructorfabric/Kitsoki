# TUI: `kitsoki shot` — render a frame to PNG

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   tui
**Epic:**   [story-qa-agent.md](story-qa-agent.md) (slice 3)

## Why

The QA agent should be able to *look* at a screen, not only read its text
— rendering bugs (overlap, misalignment, color clashes, a banner colliding
with the divider, a status row wider than the terminal) are precisely the
ones that survive a text read but jump out visually. Claude can review an
image; the agent needs a way to hand it one for any state.

The repo already rasterizes frames to a GIF in `record.go` (`framesToGIF`,
`record.go:495`) — but with `basicfont` (a 7×13 bitmap), tied to flow
replay, and producing a low-fidelity multi-frame animation, not a single
faithful still. For visual QA we want a **proper monospace + ANSI-color**
still of one `Frame`.

## What changes

**One sentence:** `kitsoki shot` takes a `Frame` (slice 1) — or a trace +
turn index — and rasterizes its ANSI string to a PNG with an embedded
monospace font and the theme's color palette, so any state is a reviewable
image.

## Impact

- **Code:** new `cmd/kitsoki/shot.go`; a small `internal/tui/shot/`
  (or reuse `internal/render/`) ANSI→image rasterizer. Reuses the theme
  palette already in `internal/tui/blocks/` so screenshot colors match the
  live TUI.
- **Rendering:** consumes `Frame.ANSI` from slice 1; does **not** re-layout
  — it only paints already-composed bytes. The frame is the single source
  of layout truth (epic shared decision 2).
- **Input:** none — it's a one-shot command.
- **Docs on ship:** `docs/tui/` (a short "screenshots" note); artifacts go
  to `.artifacts/` per CLAUDE.md, never committed.

## Mental model

A terminal is a grid of styled cells. `shot` is a tiny terminal *emulator
for one frame*: parse the ANSI into cells (fg/bg/bold per rune), lay them
on a monospace grid, emit a PNG. No state machine, no story — pixels from
bytes.

## Layout

```
Frame.ANSI ──▶ parse SGR into cells ──▶ draw grid (monospace glyph + fg/bg) ──▶ PNG
              (\x1b[…m runs)            (font face + theme palette)
```

## Rendering changes

- **Command:**

  ```
  kitsoki shot --frame frame.json -o shot.png            # a Frame emitted by `drive`
  kitsoki shot <app.yaml> --trace t.jsonl --turn 3 -o shot.png   # compose a past turn's frame
  kitsoki shot ... --cols 100 --rows 30 --theme molokai
  ```

  The `--frame` form is the agent's path: `drive` already emitted the
  `Frame` JSON, so `shot` just rasterizes its `.ANSI`. The trace form
  re-composes a historical frame via slice 1's composer from the recorded
  view (useful for after-the-fact review).
- **Rasterizer:** a real TrueType/OTF monospace face (embedded via
  `embed`), not `basicfont`, drawn with `golang.org/x/image/font` +
  `font/opentype` (`golang.org/x/image` is already in `go.mod`). Color
  comes from parsing SGR escape runs and mapping the 16/256 palette to the
  theme's RGB (the same palette `blocks` uses), so the PNG matches what the
  operator sees.
- **No layout in `shot`.** It must not wrap, truncate, or re-flow — if a
  line overflows the requested `--cols`, that overflow is *the bug we want
  to see* in the image. `shot` paints faithfully and lets the reviewer
  catch it.

## Input & commands

| Command / key | Does | Notes |
|---|---|---|
| `kitsoki shot --frame f.json` | rasterize a `drive`-emitted frame to PNG | agent's primary path |
| `kitsoki shot app --trace t --turn N` | re-compose + rasterize a past turn | review after a run |

## Rendering tests

Image output is hard to golden byte-for-byte across platforms (font
hinting), so assert on **structure**, not pixels:

- **Cell-grid parse** — feed a known ANSI string (bold red "ERROR" on a
  plain line); assert the parsed cell grid has the right runes at the right
  columns and the right fg attribute on those cells. This is the layer
  that can regress meaningfully; test it directly, not the PNG.
- **Dimensions** — a 100×30 frame yields a PNG of `100*cellW × 30*cellH`
  (± the chosen padding). Deterministic.
- **Smoke** — rasterizing a real `drive` frame produces a decodable,
  non-empty PNG. (Visual correctness is for human/Claude review, by design
  — this is a *visual* QA tool; its own correctness bar is "the bytes are
  faithful," proven by the cell-grid test.)

## Migration plan

New command, nothing to migrate. The existing `record.go` GIF path stays
(it serves demos); `shot` is the higher-fidelity still. A later cleanup
could re-base `record`'s rasterizer on `shot`'s, but that's out of scope.

## Tasks

```
## 1. Render
- [ ] 1.1 ANSI→cell-grid parser (SGR runs → per-cell rune + fg/bg/bold)
- [ ] 1.2 Monospace rasterizer (embedded OTF via x/image/font/opentype); theme palette → RGB
- [ ] 1.3 shot.go: --frame and --trace/--turn inputs; --cols/--rows/--theme; -o PNG

## 2. Drive
- [ ] 2.1 Wire the trace form through slice-1 ComposeFrame for a historical turn

## 3. Prove + document
- [ ] 3.1 Cell-grid parse test + dimensions test + PNG smoke test
- [ ] 3.2 Rasterize a real bugfix-story frame; eyeball it; drop in .artifacts/
- [ ] 3.3 docs/tui/ screenshots note; update epic slice row
```

## What we lose, honestly

A faithful rasterizer is more code than shelling to `vhs`, and it will
never be a perfect terminal emulator — exotic SGR (blink, underline
styles, true-color gradients) may render approximately. The trade is no
external-tool dependency, deterministic dimensions, and colors that match
our own themes exactly. We cover the SGR subset the TUI actually emits and
document the rest as out of scope.

## Open questions

1. **Embedded font choice.** A widely-available monospace with good glyph
   coverage for the box-drawing/spinner runes the TUI uses (`─ ▸ ⏳ »`).
   *Lean: a Nerd-Font-free DejaVu Sans Mono or JetBrains Mono subset
   embedded; verify the specific glyphs render.*
2. **256-color / true-color mapping.** *Lean: support 16 + 256; map
   true-color to nearest-256 if the theme ever emits it (it currently
   doesn't).*

## Non-goals

- A session recorder / animated capture (that's `record`'s GIF, or vhs).
- Pixel-exact cross-platform goldens (font hinting makes this brittle;
  structure tests instead).
- Re-implementing layout — `shot` paints a `Frame`, it does not compose
  one (slice 1 does).
