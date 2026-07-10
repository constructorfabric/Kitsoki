# tui-bridge — live pty-over-websocket bridge for the kitsoki TUI

Lets Playwright and claude-in-chrome drive the **real, running** `kitsoki` TUI
with full fidelity: real keystrokes in, real ANSI render out. This is the live
counterpart to `tools/mcp-demo`, which replays a committed cassette (no pty, no
websocket, no-LLM by construction) — good for demo videos and fixed-scenario
QA, but not for driving a live session. Use this bridge when you need genuine
interactive/live-model or agent-in-the-loop terminal testing.

## Pieces

- **Server** — `kitsoki tui-serve` (`cmd/kitsoki/tui_bridge.go`,
  `internal/tuibridge`). Spawns a command in a real pty
  (`github.com/creack/pty`) and bridges its bytes over a websocket
  (`github.com/coder/websocket`, already vendored for the VS Code IDE bridge)
  at `/pty`. One connection == one spawned process == one pty; a new
  connection always gets a fresh process.
  The spawned process is given the terminal profile the browser actually
  provides (`TERM=xterm-256color`, `COLORTERM=truecolor`, forced color) and
  does not inherit a parent `NO_COLOR`; otherwise headless Playwright/CI
  environments can make the child TUI emit plain text even though xterm.js can
  render the real ANSI stream.
- **Browser page** — `player/index.html`, forked from `tools/mcp-demo`'s
  xterm.js scaffolding. Instead of replaying a cassette via `window.__feed`,
  it opens a real `WebSocket` to the bridge: incoming bytes go straight to
  `term.write()`, and `term.onData` sends real keystroke bytes back over the
  socket. If the page opens before the bridge starts listening, it retries the
  websocket until the first successful connection, so Playwright captures do not
  need a separate TCP wait loop. `window.__dump()` returns the exact current
  screen text via xterm's buffer API — no OCR or vision needed for test
  assertions. `window.__textAttrs("text")` returns xterm buffer attributes for
  matching visible text, so tests can prove color/bold fidelity without OCR.
- **Static server** — `player/serve.mjs`, same dependency-free `node:http`
  static server pattern as `tools/mcp-demo/player/serve.mjs`.

## Wire protocol

- Binary frames carry raw pty bytes in both directions (keystrokes client→
  server, rendered output server→client).
- A single text frame shape is understood, for resizing the pty:
  `{"type":"resize","cols":<n>,"rows":<n>}`.

## Run it live

```bash
# terminal 1 — spawn the real TUI in a pty, bridge it on :4700
go run ./cmd/kitsoki tui-serve --addr 127.0.0.1:4700

# terminal 2 — serve the browser page
cd tools/tui-bridge && pnpm install && pnpm run serve
```

Open `http://localhost:4320/player/?ws=ws://127.0.0.1:4700/pty` — you're now
looking at (and can type into) the real onboarding TUI. Click into the
terminal first; xterm only captures keystrokes once its hidden input has
focus.

On a fresh checkout, `make setup` installs the bridge's pnpm dependencies and
the Chromium browser revision Playwright expects. If you only need to refresh
this bridge, run `make tui-bridge-deps`.

To drive a specific app/harness instead of the bare onboarding TUI, pass the
command after `--` (it's spawned as `<current kitsoki binary> <args...>`):

```bash
go run ./cmd/kitsoki tui-serve --addr 127.0.0.1:4700 -- run myapp.yaml --harness replay --recording rec.yaml
```

`--exec <path>` spawns an arbitrary binary instead (the trailing args are
passed through as-is) — this is what the test suite uses to drive `/bin/cat`
deterministically, with no kitsoki or LLM involved.

## Scenario-backed dogfood marathon TUI recording

The dogfood marathon scenario is owned by the universal product-journey
mechanism, not by this bridge. The canonical case list, success criteria, and
capture routes live in `tools/product-journey/scenarios.json` under
`dogfood-marathon-tui`; `tools/product-journey/run.py --emit-run --transport tui`
creates the run bundle and `driver-plan.json`. This bridge is only the TUI
transport capture adapter for that emitted leg:

```bash
cd tools/tui-bridge
pnpm run validate:dogfood
pnpm run record:dogfood
```

The browser connects to a real `kitsoki` TUI process and sends real xterm
keystrokes through the PTY bridge. The recording starts from the `kitsoki-dev`
landing room, types `I want to do a dogfood marathon`, shows the dogfood room's
full pre-start message, then types the clean `start the marathon` command. The
story's free-text routing and LLM-bearing host outcomes are replayed from
`tools/tui-bridge/fixtures/`, so the recording is deterministic and does not
spend LLM tokens. The fixture preserves real issue titles/URLs for the 15-case
demo, but it is not evidence that a paid GPT-5.5 marathon fixed those bugs.
The Playwright spec reads those 15 cases from the scenario run's
`driver-plan.json`, writes MP4/frames under that run's evidence directory, and
attaches `key_interaction_video`, `png-sequence`, `rendered_tui_frame`, and a
driver journal event back to the same run. A stable pointer for humans is written
to `.artifacts/tui-bridge/dogfood-marathon-real-tui/latest-run.json`; the
canonical artifacts live under `.artifacts/product-journey/<run-id>/`.

The default recording is intentionally paced for review: the two user commands
are typed visibly, but the camera also exercises the real TUI `choice` widget
with `ArrowDown`/`ArrowUp` keystrokes on both the `kitsoki-dev` landing surface
and the dogfood marathon landing room. This matters because the widget starts
active in a real terminal; pressing `Tab` is the explicit "chat instead" gesture,
so a recording that tabs away without showing the widget omits a bug-prone
surface. The dogfood landing menu starts on `start the marathon`, moves down to
the real `refine plan` affordance, and hides `resume the marathon` in a fresh
session because there is no durable journal to resume yet. After the clean
`start the marathon` command is submitted, the player chrome turns on a
`FAST FORWARD` badge labelled as cassette replay for the autonomous 15-case
loop. The opening setup therefore reads as real-time terminal interaction,
while the sped-up cassette section is visibly marked as accelerated replay. The
serious-exception checkpoint is also
handled through the same choice widget: the warning stays on screen with a
distinct question, impact, Issue link, and trace link; the cursor moves to
"park case and continue"; and Enter commits that actual intent instead of
typing a staged free-text answer. Each submitted command holds briefly before
Enter, every
processed bug settles on a visible autonomous running/report state that names
the current case and its source details, and the spec fails a watch-speed
recording if any chapter is shorter than the readable floor. The saved MP4 is
trimmed to the first stable `kitsoki-dev` frame and holds that populated TUI
briefly before typing, so bridge startup never becomes leading dead air; the
chapter sidecar is shifted to the trimmed video clock. Use
`WEB_CHAT_PACE=0 pnpm run validate:dogfood` only for fast assertions; it writes a
`.fast.mp4` and is not a user-facing demo.

## Project onboarding rrweb capture

TUI proof recordings should use xterm.js plus rrweb, not `kitsoki record`.
`kitsoki record` renders static flow frames and is not evidence of a live PTY,
real keyboard input, or xterm rendering.

Set `KITSOKI_RRWEB_OUT` to enable native rrweb capture. In this mode the helper
disables Playwright's video recorder and writes:

- `<name>.rrweb.json` — canonical rrweb event stream.
- `<name>.rrweb.capture.json` — viewport and event-count sidecar.
- `<name>.rrweb.json.chapters.json` — tour chapter sidecar when the spec uses
  `ChapterRecorder`.
- PNG checkpoints and bridge logs beside the rrweb artifact.

The presentation-service onboarding demo is driven by the real TUI bridge and a
disposable copy of the target checkout:

```bash
cd tools/tui-bridge
KITSOKI_RRWEB_OUT=/path/to/platform-presentation/.artifacts/kitsoki-onboarding-demo/presentation-onboarding-real-tui.rrweb.json \
  KITSOKI_PRESENTATION_TARGET=/path/to/platform-presentation \
  pnpm run record:presentation-onboarding
```

The fast gate form is:

```bash
KITSOKI_RRWEB_OUT=/tmp/presentation-onboarding.rrweb.json \
  KITSOKI_PRESENTATION_TARGET=/path/to/platform-presentation \
  pnpm run validate:presentation-onboarding
```

## Driving it from claude-in-chrome

No special wiring needed — it's a plain page, so the standard
`mcp__claude-in-chrome__*` tools work directly (verified end-to-end: real
keystrokes typed via `computer` landed in a live `kitsoki run` session and
`Escape` correctly opened its menu):

1. `tabs_context_mcp` (once, per conversation) → `navigate` to
   `http://localhost:4320/player/?ws=ws://127.0.0.1:4700/pty`.
2. `computer` with `action: "left_click"` on the terminal body to focus it —
   xterm only captures keystrokes once its hidden textarea has focus, same as
   Playwright.
3. `computer` with `action: "type"` for text, or `action: "key"` for control
   keys (`Escape`, `Enter`, `Tab`, arrows, `ctrl+c` via `modifiers`) — both
   reach the real pty.
4. `computer` with `action: "screenshot"` for vision evidence (feed straight
   into [[kitsoki-ui-qa]]'s `--frames` pipeline), or `javascript_tool` with
   `window.__dump()` for exact-text readback of the visible screen with no
   vision pass needed.

One gotcha worth knowing: take the first screenshot only *after* the initial
frame has painted (a screenshot fired immediately on page load, before the
first pty output arrives over the socket, can be blank) — poll `window.__status()
=== "connected"` and then poll `window.__dump()` for the text you need, or just
screenshot again if the first one looks empty. For scrollback evidence, prefer
the deterministic helpers over mouse wheel deltas: `window.__scrollLines(n)`,
`window.__scrollToTop()`, and `window.__scrollToBottom()` all return the visible
viewport text after scrolling.

## Tests

- `internal/tuibridge/server_test.go` — Go-level bridge tests (`go test
  ./internal/tuibridge/...`): byte round-trip and resize-before-input
  ordering, both against `/bin/cat` and `/bin/sh` — no LLM, no browser.
- `tools/tui-bridge/tests/live-bridge.e2e.spec.ts` — Playwright end-to-end:
  boots the real Go bridge server (spawning `/bin/cat`) and the static player,
  then drives the page with real keyboard input and asserts the round-tripped
  screen contents via `window.__dump()`. Run with `pnpm install && pnpm test`
  or, from the repo root, `make tui-bridge-test`.
- `tools/tui-bridge/tests/dogfood-marathon-real-tui.e2e.spec.ts` — launches a
  real dogfood-marathon TUI under `--harness replay --host-cassette`, drives one
  continuous xterm session, and writes MP4, chapters, screenshots, bridge logs,
  a PNG-sequence manifest, attached scenario evidence, and a driver journal
  event under `.artifacts/product-journey/<run-id>/` with a pointer at
  `.artifacts/tui-bridge/dogfood-marathon-real-tui/latest-run.json`.
- `tools/tui-bridge/tests/presentation-onboarding-real-tui.e2e.spec.ts` —
  copies a presentation-service checkout to a disposable fresh target, drives
  project onboarding through the real xterm.js TUI, and writes rrweb/PNG/trace
  evidence to the target's `.artifacts/kitsoki-onboarding-demo/` directory.

Neither test path spawns a real kitsoki session or an LLM — per repo policy,
that only happens when explicitly requested (point `-- run ...` or `--exec`
at whatever you actually want to drive live).

## Recording

Traffic through this bridge can be captured to seed new termcast cassettes for
`tools/mcp-demo`, subsuming that surface's replay path rather than replacing
it — record once here, replay for free there.

## Security note

`websocket.Accept` is configured with `OriginPatterns: []string{"*"}` because
the player page and the bridge are served from different origins by design,
and non-browser drivers (Playwright, claude-in-chrome) may send no `Origin`
header at all. This is a local dev/test tool — `--addr` defaults to loopback;
don't bind it to a non-loopback address on a shared or untrusted network.
