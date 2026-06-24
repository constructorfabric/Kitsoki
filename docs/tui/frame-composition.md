# Frame composition — the full screen as a value

The TUI knows how to paint a screen, but historically it did so in **two
places** that could drift:

- the **room body** went to terminal scrollback via `tea.Println` out of
  the transcript flush, never returned to a caller; and
- the **bottom chrome** (live routing line, action-required banner,
  divider, mode-prefixed prompt, per-room footer, framework status row)
  was assembled inline inside `RootModel.View()`.

The only artifact a headless caller could get — the trace's
`view_rendered` — was a width-80, ANSI-stripped projection of the *body
only*: neither the right width, the right styling, nor the whole screen.

## The composer

`internal/tui/frame.go` introduces one seam that returns **"the full
screen a human sees"** as a value:

```go
frame := tui.ComposeFrame(&m, width, height) // body + chrome, at this width
```

`Frame` carries both projections plus a typed sidecar:

| field            | meaning                                              |
|------------------|------------------------------------------------------|
| `ANSI`           | styled, screenshot-ready screen                      |
| `Text`           | `ansi.Strip(ANSI)` — agent-readable; the twin holds  |
| `Width`/`Height` | geometry the frame is paint-equivalent to            |
| `Metadata`       | `FrameMeta{State, Mode, AllowedIntents, WorldDigest}`|

`Metadata` is read straight from the model and the machine — the
`AllowedIntents` are exactly what `Orchestrator.AllowedIntents(state,
world)` reports (the same source `inspect`/`turn` use), so a consumer
trusts the sidecar without re-parsing `Text`.

### One paint, two consumers

`ComposeFrame` is **width-parameterised**: it resizes a *copy* of the
model to the requested geometry through the live `resize()` seam, so a
screenshot at 100 cols is byte-identical to a real 100-col terminal. The
body region is the transcript's last flushed entry
(`transcriptModel.LastBody()`); the chrome region is built by
`composeChromeParts` + `joinChromeParts`.

`RootModel.View()` now routes its own bottom region through those **same**
two helpers — so the live screen and every headless capture are the same
bytes. `View()` emits chrome only (the body lives in scrollback), so its
output is byte-identical to the pre-composer assembly.

```
Frame (what ComposeFrame returns)         Source renderer (unchanged)
┌─────────────────────────────────┐
│ <last room body, reflowed @W>   │  ◀── transcript.LastBody()
│ <live routing/thinking line>    │  ◀── transcript.LiveLine()
│ <action-required banner>        │  ◀── inbox.ActionRequiredBanner()
│ ─────────────────────────────── │  ◀── blocks.Renderer.Divider()
│ > <prompt + mode prefix>        │  ◀── composePromptAndBanner()
│ <per-room footer>               │  ◀── footerStoryLine()
│ room · state · mode · queue     │  ◀── blocks.Renderer.StatusRow()
└─────────────────────────────────┘
```

## Why this matters

`ComposeFrame` is the seam the rest of the `mcp-studio` epic consumes —
the headless driver, the screenshot tool, and the MCP `render.*` tools
all read one `Frame` instead of re-deriving the screen. Because the live
TUI is the *first* consumer of its own frame, a headless capture and a
real terminal can never disagree.

See `internal/tui/frame_test.go` for the contract: frame-equals-live-paint
(body + every chrome element, in order), width fidelity (no chrome
overflows the requested width; a wide frame reflows past the 80-col
fossil), metadata-matches-the-machine, and `ansi.Strip(ANSI) == Text`.
