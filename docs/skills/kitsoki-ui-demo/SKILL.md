---
name: kitsoki-ui-demo
description: Produce a deterministic, no-LLM demo video (plus per-scene screenshots, a shareable MP4/GIF, and a contact sheet) of the kitsoki web UI by driving a real `kitsoki web` server through Playwright. Use when asked to record, refresh, or author a demo / walkthrough / screen-capture of the kitsoki browser UI (home screen, a story being driven, reload, etc.).
---

# Kitsoki UI demo videos

This skill records the **kitsoki web UI** as a deterministic, **no-LLM** video:
a Playwright spec spawns the real `kitsoki web` binary in the `--flow` /
`--host-cassette` posture (nil harness — intents are submitted explicitly, host
calls come from a cassette/stub), drives the SPA scene-by-scene at a
human-watchable pace, and records a MacBook-resolution video + per-scene
screenshots into `.artifacts/`. Bundled scripts then render shareable
**MP4 / GIF / contact-sheet** artifacts from the raw `.webm`.

Why no-LLM: the recording must be **reproducible and free** — same input, same
frames, no API cost, no flakiness. This is the same posture the engine uses for
flow tests (see [[feedback_no_llm_tests]] and `docs/web/README.md` →
"Deterministic, no-LLM"). **Never** record against a live LLM.

> The worked, maintained reference is
> `tools/runstatus/tests/playwright/multi-story.spec.ts` — it drives the whole
> product (home → new session → drive/observe → reload → active sessions) and is
> the template to copy. The single-purpose chat drive lives there too.

## Prerequisites (once)

```bash
make build                                  # bundle the SPA into ./kitsoki + bin/kitsoki
cp ./kitsoki bin/kitsoki                    # the specs spawn bin/kitsoki
pnpm -C tools/runstatus playwright:install  # chromium + ffmpeg for Playwright (once)
```

`make build` is **mandatory before every recording** — the SPA is `go:embed`'d
into the binary, so an un-rebuilt binary serves a stale UI. Rebuild after any
change under `tools/runstatus/src/`.

## Deterministic recording (read this first)

A demo recording has a few non-obvious traps. They're solved once, in
`tests/playwright/_helpers/demo.ts` — **use those helpers; don't re-derive
them.** The reference spec is `tests/playwright/diagram-showcase.spec.ts`.

- **`recordVideo` captures from PAGE CREATION**, so off-camera setup (home
  screen, the new-session click, live RPC room-flips) flashes by, rushed, at the
  head of the video. There is no "off camera" — the camera rolls from frame 0.
  → `installCurtain(page, title)` **before the first `goto`** drops a full-screen
  title card that survives `page.reload` (sessionStorage), then `liftCurtain(page)`
  once the scene is fully staged. Drive all setup behind it.
- **Drive setup off-camera via RPC, advance on-camera via a real UI click.**
  RPC (`runstatus.session.submit` / `patch_world`) from the test is a *different*
  client; its turns reach the page only via cross-client SSE — a timing race
  (sometimes live, sometimes needs a reload → nondeterministic frames). A real
  `intent-btn-*` click in the *driving* page renders the turn result directly:
  one deterministic visual path, no reload. Use RPC behind the curtain; click on
  camera. (Match the flow's `initial_world` with `patch_world` if a gate needs
  it — e.g. `judge_mode: human`.)
- **Captions/overlays must be `pointer-events: none`** or they silently
  intercept clicks on the UI beneath (an opaque banner over the tab bar = every
  tab click times out). `makeCaption(page)` already is; so is the curtain.
- **Playwright's default `actionTimeout` is 0 (INFINITE)** — a click on a
  missing/covered element hangs the whole run with no error. The config now caps
  it (15s); keep it. Don't write un-timeouted `.click()` in a loop.
- **The Claude Code harness suppresses Playwright's stdout** — a failing
  recording prints only "Exit code 1". `captureDiagnostics(page, artifactDir)`
  writes the failure + a `mark(step)` breadcrumb to `<artifactDir>/ERROR.txt`;
  read that file and the `NN-*.png` screenshots after the run. (Run in the
  background and read the task-output file, or redirect to a repo file.)
- **Unique `ADDR` port per spec; never `pkill -f kitsoki`.** A broad kill takes
  down the user's `make web-dev` servers (`/tmp/kitsoki-fixed web …`). Give each
  spec its own port and let `afterAll` stop only its own server.

The shared helpers (`_helpers/demo.ts`): `installCurtain` / `liftCurtain`,
`makeCaption` → `beat`, `captureDiagnostics`, `dwell` (PACE-scaled),
`DEMO_VIEWPORT`. For the recording lifecycle use `_helpers/server.ts`'s
`prepareVideoDir` (beforeAll) + `saveAndRemuxVideo` (after `context.close`) — the
remux pattern documented below, **not** a plain copy from the video dir.

## The loop

1. **Pick / author the spec.** Copy `multi-story.spec.ts` and trim to the scenes
   you want. The anatomy that matters (all proven in that file):
   - **Server lifecycle** — `spawn(BIN, ["web", "--stories-dir", DIR, "--flow",
     FLOW, "--addr", ADDR, "--db", tmpDb])`, then poll `GET /` until healthy.
     `--stories-dir stories` shows the **whole catalogue**; a single story dir
     shows just that one. The `--flow` fixture is **story-specific** (it encodes
     that story's intents + host stubs) and is applied to *every* session the
     home screen creates, so only that story can be driven no-LLM — drive its
     card, browse the rest.
   - **Drive by `data-testid`** — home: `home-view`, `story-card` (+
     `[data-story-path]`), `story-title`, `new-session-btn`, `rescan-btn`,
     `session-row`, `session-open`; observe (`RunView`): `breadcrumb`,
     `reload-button`, `reload-warning`, `drive-link`; drive (`InteractiveView`):
     `chat-section`, `current-state`, `state-badge`, `chat-transcript` →
     `chat-row-agent`, `composer-select`, `composer-input`, `composer-send`,
     `intent-btn-<intent>`, `observe-link`. Assert the `current-state` /
     `state-badge` after each turn — that is the hard signal the turn landed.
   - **A `SCENES` table** mapping each turn to a UI action (type into the
     composer for text-slot intents, click `intent-btn-*` for slot-less ones)
     and the state it should reach.
   - **Recording context** — `viewport {1440,900}`, `deviceScaleFactor: 2`
     (retina), `recordVideo: { dir: VIDEO_DIR, size }`.
   - **Video save pattern** — always use the three-step pattern from
     `_helpers/server.ts` (see below). Never `fs.copyFileSync` from `VIDEO_DIR`
     — that picks a stale file from a previous run.
   - **Pacing** — gate every delay on `WEB_CHAT_PACE` (typing delay, a beat
     before each click, a dwell on each settled scene) so the same spec runs
     fast for CI and slow for the camera. This applies to the **opening
     orchestration too**, not just the tour/scene loop: navigation that sits
     outside the paced loop (home → new session → observer) flashes past in
     well under a second if you `page.goto` straight through it. Use the shared
     `_helpers/server.ts` primitives — `cinematicGoto(page, url, {waitForTestId})`
     (goto + render-anchor + settle), `pacedClick(page, locator)` (beat before,
     settle after), and the shared `dwell` / `SETTLE_MS` — for every
     surface change, so the camera arrives and rests rather than lurching.
   - **Tour-driven intro (feature-spotlight specs)** — for a dedicated
     feature tour, prefer making the WHOLE video tour-narrated rather than
     silently `cinematicGoto`-ing through the opening. Start the tour on the
     home story library (`__startTourWithSteps` while on `#/`) and prepend
     `home`/`interactive` intro steps that explain where the feature lives and
     frame the demo story, then let `kind: "action"` + `advance: "route-match"`
     steps perform the navigation (home → `new-session-btn` → `observe-link` →
     observer) — each one narrated by its own popover. The spec walks these
     exactly like the observer steps: click the target, then `waitForURL` the
     route change. Order matters — click `observe-link` while the chat view is
     STATIC, navigate to the observer, and only THEN fire the `patch_world` /
     `submit` RPCs (mirrors `tour-video.spec.ts`); submitting before the click
     re-renders the chat view under the click and the route-match advance is
     lost. The trace then streams into the observer's live trace ahead of the
     introspection steps. The single backdrop only blanks the page for
     anchorless (`center`) steps; targeted steps leave a click-through hole.
   - **Hash routing** — URLs are `#/`, `#/s/:id`, `#/s/:id/chat`.

## Video recording — the correct pattern

Playwright's VP8 webm omits `DURATION` and `CUES` container atoms. Most players
(VLC, browsers, QuickTime, Keynote) render only the first frame for the full
clip duration when these are missing. The fix is a cheap ffmpeg `-c copy` remux
(no re-encode) that rebuilds the container with proper metadata.

Two shared helpers in `_helpers/server.ts` wrap this correctly:

```ts
// 1. In beforeAll — clear VIDEO_DIR so stale files never pollute the run:
prepareVideoDir(VIDEO_DIR);

// 2. In the context setup — capture the Video reference BEFORE context.close():
const page = await context.newPage();
const video = page.video();   // ← must happen before close()

// 3. In finally — save + remux AFTER context.close(), BEFORE browser.close():
await context.close();        // finalises the recording
await saveAndRemuxVideo(video, ARTIFACT_DIR, "my-demo");  // save + ffmpeg remux
await browser.close();
```

`saveAndRemuxVideo(video, artifactDir, name)` writes `<name>-raw.webm`, remuxes
to `<name>.webm`, removes the raw on success. If ffmpeg isn't available or
fails, it falls back to the raw file with a warning (never silently loses the
recording). Both helpers are already imported in `tour-video.spec.ts` and
`multi-story.spec.ts` — copy that pattern for any new recording spec.

**Why `video.saveAs()` and not `fs.readdirSync(VIDEO_DIR)[0]`?**
`readdirSync` picks the alphabetically-first file, which is the OLDEST webm in
the dir if the dir was never cleared. `video.saveAs()` gives you the specific
page's recording regardless of what else is in the dir.

2. **Validate fast (assertions only).** Iterate here until green — no waiting on
   dwells:
   ```bash
   cd tools/runstatus && WEB_CHAT_PACE=0 pnpm exec playwright test <name> --project=chromium
   ```
   Capture the real exit code — **never pipe the runner through `tail`**, it
   masks the exit status (see [[feedback_integration_verify_not_per_package]]).

3. **Record at watch-speed.** Drop `WEB_CHAT_PACE` (defaults to 1):
   ```bash
   cd tools/runstatus && pnpm exec playwright test <name> --project=chromium
   ```
   Output lands in `.artifacts/<name>/`: `video/*.webm`, a stable
   `*-demo.webm`, and numbered `NN-<scene>.png` screenshots.

4. **Render shareable artifacts** with the bundled tools (all write to
   `.artifacts/`, never committed — [[feedback_artifacts_dir]]):
   ```bash
   S=docs/skills/kitsoki-ui-demo/scripts
   $S/render.sh .artifacts/<name>/<name>-demo.webm   # mp4 + gif + contact sheet in one go
   # …or individually:
   $S/webm-to-mp4.sh   .artifacts/<name>/<name>-demo.webm            # H.264 MP4 (Slack/Keynote/web)
   $S/webm-to-gif.sh   .artifacts/<name>/<name>-demo.webm --width 900 # looping GIF for PRs/docs
   $S/contact-sheet.sh .artifacts/<name>/                            # NN-*.png → one contact sheet
   ```

5. **Verify the frames.** Open a couple of the `NN-*.png` (or the contact sheet)
   and confirm each scene renders correctly. The kitsoki rule holds in video too
   (`tools/runstatus/CLAUDE.md`): if a room view looks wrong, **fix the
   trace/render, not the recording** — never a UI hack to paper over a bad trace.

## The tools (`scripts/`)

| Script | Does | Notes |
|---|---|---|
| `render.sh <demo.webm>` | One-shot: MP4 + GIF + contact sheet (the sibling `NN-*.png` from the webm's dir) | Convenience wrapper over the three below |
| `webm-to-mp4.sh <in.webm> [out.mp4] [--fps N] [--width W]` | H.264 + `yuv420p` + `+faststart` — the universally-playable share format | `.webm` is poorly supported in Keynote/Slack/iMessage; ship the MP4 |
| `webm-to-gif.sh <in.webm> [out.gif] [--fps N] [--width W]` | Two-pass palettegen/paletteuse high-quality looping GIF | For embedding in PRs / markdown; keep `--width ≤ 900` |
| `contact-sheet.sh <dir> [out.png] [--cols N] [--tile-width W]` | Tiles the numbered scene screenshots into one image | A storyboard for quick review / PR description |

All require `ffmpeg` on PATH (Playwright's browser install or a system ffmpeg).

## Tour walkthrough recording

The onboarding tour has a dedicated, maintained spec that records it as a
first-class demo mode:

```
tools/runstatus/tests/playwright/tour-video.spec.ts
```

The spec imports the **same step manifest** the live overlay uses
(`src/tour/manifest.ts`) and asserts the popover title against it for every
step — a drift guard baked into the recording. It drives all 13 tour steps in
Oregon Trail no-LLM mode, submits one intent during the input-bar step so the
trace lights up, and captures a labeled `NN-<step-id>.png` per step.

**One-liner record** (rebuild + record + render MP4/GIF/contact-sheet):

```bash
make demo-tour
```

**Validate fast** (CI — assertions only, no dwells):

```bash
make demo-tour-fast
```

**Record + run the vision QA gate** (requires `claude` CLI):

```bash
make demo-tour-qa
```

Or step-by-step:

```bash
# 1. Validate
cd tools/runstatus && WEB_CHAT_PACE=0 pnpm exec playwright test tour-video --project=chromium

# 2. Record at watch-speed
cd tools/runstatus && pnpm exec playwright test tour-video --project=chromium

# 3. Render
S=docs/skills/kitsoki-ui-demo/scripts
$S/render.sh .artifacts/tour-video/tour-video-demo.webm
```

Output lands in `.artifacts/tour-video/`: raw `video/*.webm`, stable
`tour-video-demo.webm`, rendered `tour-video-demo.mp4` / `.gif`, contact sheet,
and numbered `NN-<step-id>.png` screenshots.

To QA the recording against the tour scenarios:

```bash
docs/skills/kitsoki-ui-qa/scripts/qa.sh \
  .artifacts/tour-video/tour-video-demo.mp4 \
  --frames .artifacts/tour-video \
  --feature docs/skills/kitsoki-ui-qa/templates/tour-feature.md \
  --scenarios docs/skills/kitsoki-ui-qa/templates/tour-scenarios.yaml
```

The `--frames` flag passes the labeled PNGs directly (one per step, highest
fidelity — skips the ffmpeg scene-detection pass).

To add a new step to the tour, append a `TourStep` to `TOUR_STEPS` in
`src/tour/manifest.ts` — data-only, no other code changes:

```ts
{
  id: "my-feature",           // stable id; also the screenshot label
  route: "any",               // "home" | "interactive" | "any" (/s/:id observer)
  target: "my-testid",        // data-testid to spotlight (must be universal)
  waitForTarget: "my-testid", // wait for DOM presence before showing
  title: "Short feature name",
  body: "One sentence explaining what this does and why it matters.",
  placement: "bottom",        // top | bottom | left | right | center
  kind: "explain",            // explain (Next advances) | action (clicking advances)
  advance: "next",
  dwellMs: 4000,              // ms the video spec pauses on this step
},
```

Route mapping: `/` → `"home"`, `/s/:id/chat` → `"interactive"`,
`/s/:id` (observe) → `"any"`. An `"any"` step shows on all three routes in the
live overlay, so only anchor to elements that exist there (e.g. `view-mode-tabs`,
`trace-event-row`, `confidence-bar`).

## Pointers

- Template spec: `tools/runstatus/tests/playwright/multi-story.spec.ts`
- Tour spec: `tools/runstatus/tests/playwright/tour-video.spec.ts`
- Tour robustness test: `tools/runstatus/tests/playwright/tour-onboarding.spec.ts`
- Tour manifest (shared by live overlay + video spec): `tools/runstatus/src/tour/manifest.ts`
- Tour QA templates: `docs/skills/kitsoki-ui-qa/templates/tour-{feature,scenarios}.*`
- Shared helpers (video, server, pacing): `tests/playwright/_helpers/server.ts`
- Playwright config + globalSetup: `tools/runstatus/playwright.config.ts`,
  `tools/runstatus/tests/playwright/_helpers/`
- No-LLM posture + UI surfaces: `docs/web/README.md`
- File:// snapshot artifacts (static, no server): `_helpers/artifact.ts`

## Maintenance

Exposed to Claude Code via a symlink (skills under `docs/` aren't auto-discovered):

```
ln -s "$(pwd)/docs/skills/kitsoki-ui-demo" ~/.claude/skills/kitsoki-ui-demo
```
