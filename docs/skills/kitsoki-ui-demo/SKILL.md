---
name: kitsoki-ui-demo
description: Produce a deterministic, no-LLM demo / tour video of the kitsoki web UI (plus per-scene screenshots and a shareable MP4 / GIF / contact sheet) by driving a real `kitsoki web` server through Playwright. Use when asked to make, record, refresh, or author a tour demo video, feature-spotlight tour, walkthrough video, demo, or screen-capture of the kitsoki browser UI — whether a tour of one feature (golden example: agent-actions), the generic onboarding tour, or a full-product walkthrough. Triggers on phrasings like "make a tour demo video", "record a demo of <feature>", "feature tour video", "walkthrough video".
---

# Kitsoki UI demo videos

This skill records the **kitsoki web UI** as a deterministic, **no-LLM** video:
a Playwright spec spawns the real `kitsoki web` binary in the `--flow` /
`--host-cassette` posture (nil harness — intents are submitted explicitly, host
calls come from a cassette/stub), drives the SPA scene-by-scene at a
human-watchable pace, and records a MacBook-resolution video + per-scene
screenshots into `.artifacts/`. The recording is saved as a shareable **MP4**
(never `.webm` — it must play inline in VS Code / Keynote / Slack); bundled
scripts render an optional **GIF / contact-sheet** alongside it.

Why no-LLM: the recording must be **reproducible and free** — same input, same
frames, no API cost, no flakiness. This is the same posture the engine uses for
flow tests (see [[feedback_no_llm_tests]] and `docs/web/README.md` →
"Deterministic, no-LLM"). **Never** record against a live LLM.

> **Pick the worked reference that matches the ask — copy it, don't start blank:**
> - **A tour demo video of one feature** (the usual ask — "make a tour demo
>   video of X") → the **golden example** is
>   `tools/runstatus/tests/playwright/agent-actions-video.spec.ts` +
>   `src/tour/agent-actions-manifest.ts`. The *whole* video is tour-narrated: it
>   opens on the home story library, frames the demo story, drives home → new
>   session → observer via narrated action steps, then walks the feature. See
>   **[Feature tour demo video — the golden example](#feature-tour-demo-video--the-golden-example)**.
> - **The generic onboarding tour** → `tour-video.spec.ts` + `src/tour/manifest.ts`.
> - **A full-product walkthrough** (home → new session → drive/observe → reload →
>   active sessions) → `multi-story.spec.ts`. The single-purpose chat drive lives
>   there too.

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
   - **Tour-driven intro (feature tours)** — for a feature tour, make the WHOLE
     video tour-narrated (including the opening) rather than silently
     `cinematicGoto`-ing into the observer. This is the golden pattern below —
     see **[Feature tour demo video](#feature-tour-demo-video--the-golden-example)**.
   - **Hash routing** — URLs are `#/`, `#/s/:id`, `#/s/:id/chat`.

## Video recording — the correct pattern

**Always emit MP4, never `.webm`.** Playwright records VP8 `.webm`, which (a)
omits the `DURATION`/`CUES` container atoms so most players show only the first
frame, and (b) does **not** play inline in VS Code, Keynote, Slack, or iMessage.
So the canonical recording artifact is the `.mp4` — the helper transcodes the
intermediate webm away. Never ship or commit a `.webm` as the deliverable.

Two shared helpers in `_helpers/server.ts` wrap this correctly:

```ts
// 1. In beforeAll — clear VIDEO_DIR so stale files never pollute the run:
prepareVideoDir(VIDEO_DIR);

// 2. In the context setup — capture the Video reference BEFORE context.close():
const page = await context.newPage();
const video = page.video();   // ← must happen before close()

// 3. In finally — save + transcode to MP4 AFTER context.close(), BEFORE browser.close():
await context.close();        // finalises the recording
await saveVideoAsMp4(video, ARTIFACT_DIR, "my-demo");  // → my-demo.mp4
await browser.close();
```

`saveVideoAsMp4(video, artifactDir, name)` saves `<name>-raw.webm`, transcodes
it to `<name>.mp4` (libx264 / yuv420p / +faststart / 30fps — the same settings
as `scripts/webm-to-mp4.sh`), and removes the raw webm on success. Only if ffmpeg
is unavailable or fails does it fall back to a `<name>.webm` with a warning (so a
recording is never silently lost). The helper is already imported in
`tour-video.spec.ts`, `agent-actions-video.spec.ts`, `trace-features-video.spec.ts`,
and `multi-story.spec.ts` — copy that pattern for any new recording spec.

**Emitting a chapter sidecar (optional, for the video-review loop).**
A recorded tour can also emit a producer-agnostic **chapter sidecar**
(`<video>.chapters.json`) mapping each step's dwell window back to its
`TourStep` — the same shape `host.slidey.render` writes for slidey decks (see
[`hosts.md` → the chapter sidecar](../../architecture/hosts.md#the-chapter-sidecar)).
This is what lets the `/review` feedback panel flag a moment and resolve it to
the step that produced it. Use the `ChapterRecorder` + `writeChapters` helpers
in `_helpers/server.ts`:

```ts
import { ChapterRecorder, writeChapters } from "./_helpers/server.js";

const chapters = new ChapterRecorder();          // start the clock at record time
for (const step of TOUR_STEPS) {
  // … once the step's spotlight is settled and on-screen:
  chapters.open(step.id, step.title, "tools/runstatus/src/tour/manifest.ts");
  await dwell(page, step.dwellMs ?? 3000);        // this dwell becomes the window
}
// after the MP4 is saved:
const mp4 = await saveVideoAsMp4(video, ARTIFACT_DIR, "tour-video-demo");
writeChapters(mp4, chapters.list());              // → <mp4>.chapters.json (kind=tour)
```

`tour-video.spec.ts` is the worked example. The clock starts when the
`ChapterRecorder` is constructed, so call it right after the recording context
is created; each `open()` auto-closes the prior chapter.

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
   Output lands in `.artifacts/<name>/`: the canonical **`<name>-demo.mp4`**
   (the spec transcodes the raw webm away — never ship the webm) and numbered
   `NN-<scene>.png` screenshots.

4. **(Optional) Render GIF + contact sheet.** The MP4 is already the shareable
   deliverable; only run this if you also want a looping GIF or a storyboard.
   All write to `.artifacts/`, never committed ([[feedback_artifacts_dir]]):
   ```bash
   S=docs/skills/kitsoki-ui-demo/scripts
   $S/render.sh .artifacts/<name>/<name>-demo.mp4    # gif + contact sheet (mp4 already made)
   # …or individually:
   $S/webm-to-gif.sh   .artifacts/<name>/<name>-demo.mp4 --width 900 # looping GIF for PRs/docs
   $S/contact-sheet.sh .artifacts/<name>/                            # NN-*.png → one contact sheet
   ```

5. **Verify the frames.** Open a couple of the `NN-*.png` (or the contact sheet)
   and confirm each scene renders correctly. The kitsoki rule holds in video too
   (`tools/runstatus/CLAUDE.md`): if a room view looks wrong, **fix the
   trace/render, not the recording** — never a UI hack to paper over a bad trace.

## The tools (`scripts/`)

The recording spec already emits the canonical MP4 (via `saveVideoAsMp4`), so
these are post-production extras, not part of the critical path.

| Script | Does | Notes |
|---|---|---|
| `render.sh <demo.(mp4\|webm)>` | One-shot: GIF + contact sheet (the sibling `NN-*.png` from the video's dir); transcodes to MP4 first only if handed a legacy webm | Convenience wrapper over the two below |
| `webm-to-mp4.sh <in.webm> [out.mp4] [--fps N] [--width W]` | H.264 + `yuv420p` + `+faststart` — the universally-playable share format | Only needed to convert a stray/legacy `.webm`; specs already emit MP4 |
| `webm-to-gif.sh <in.(mp4\|webm)> [out.gif] [--fps N] [--width W]` | Two-pass palettegen/paletteuse high-quality looping GIF | For embedding in PRs / markdown; keep `--width ≤ 900` |
| `contact-sheet.sh <dir> [out.png] [--cols N] [--tile-width W]` | Tiles the numbered scene screenshots into one image | A storyboard for quick review / PR description |

All require `ffmpeg` on PATH (Playwright's browser install or a system ffmpeg).

## Feature tour demo video — the golden example

When the ask is **"make a tour demo video"** of a specific feature (a drawer, a
new panel, a capability), copy the **agent-actions** demo — it is the golden,
maintained reference:

- spec:     `tools/runstatus/tests/playwright/agent-actions-video.spec.ts`
- manifest: `tools/runstatus/src/tour/agent-actions-manifest.ts`
- (sibling) `trace-features-video.spec.ts` + `trace-manifest.ts` — same shape,
  for the trace-introspection feature.

What makes it the template:

- **The whole video is tour-driven.** The manifest's first four steps live on
  `home`/`interactive` routes: a centered welcome that names the feature and the
  demo story (the bug-fix pipeline), a spotlight on the `story-card`, then two
  `kind: "action"` + `advance: "route-match"` steps that click `new-session-btn`
  then `observe-link`. Navigation is narrated by popovers, never a silent
  `page.goto`. The remaining steps walk the feature on the observer (route
  `"any"`).
- **One manifest drives both the live overlay and the video.** The spec injects
  the array via `window.__startTourWithSteps(JSON.stringify(STEPS))` and asserts
  each popover `title` against the manifest, so the recording can't drift from
  what users actually see.
- **Submit AFTER you reach the observer.** Capture the session id at the
  `new-session-btn` step, click `observe-link` while the chat view is STATIC,
  `waitForURL` the observer, and only THEN fire `patch_world` / `submit` so the
  trace streams into the observer's live trace. Submitting *before* the click
  re-renders the chat under the click and the route-match advance is lost
  (mirrors `tour-video.spec.ts`).
- **Pre-step hooks open the surfaces a step needs.** Before a step whose target
  lives inside a drawer/pane, the spec opens that pane (e.g. `openDrawerForCall`,
  `openTaskDetail`) and `dwell(page, SETTLE_MS)` so the spotlight lands on a
  composed frame, not a half-rendered flicker.
- **The single backdrop only blanks the page for anchorless (`center`) steps;**
  targeted steps leave a click-through hole over the real control.

**Author + record** (the four commands — MP4 is the deliverable):

```bash
# 1. Rebuild the SPA into the binary (mandatory — go:embed)
make build && cp ./kitsoki bin/kitsoki

# 2. Validate fast (assertions only, no dwells)
cd tools/runstatus && WEB_CHAT_PACE=0 pnpm exec playwright test agent-actions-video --project=chromium

# 3. Record at watch-speed → .artifacts/agent-actions/agent-actions-demo.mp4
cd tools/runstatus && pnpm exec playwright test agent-actions-video --project=chromium

# 4. (optional) GIF + contact sheet from the MP4
docs/skills/kitsoki-ui-demo/scripts/render.sh .artifacts/agent-actions/agent-actions-demo.mp4
```

**To make a tour demo video for a NEW feature:** copy `agent-actions-manifest.ts`
→ `<feature>-manifest.ts` and rewrite the step `title`/`body`/`target` for your
feature — **keep the four-step home → observer intro** so the whole video stays
tour-narrated. Copy `agent-actions-video.spec.ts` → `<feature>-video.spec.ts`,
point it at the new manifest and a fresh `ADDR` port, adjust the pre-step hooks
to open your feature's surfaces, then run the four commands above with the new
spec name. Anchor every `target` to a `data-testid` the feature actually ships.

## Onboarding tour recording

The generic onboarding tour has a dedicated, maintained spec that records it as a
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

# 3. (optional) GIF + contact sheet — the MP4 is already produced by step 2
S=docs/skills/kitsoki-ui-demo/scripts
$S/render.sh .artifacts/tour-video/tour-video-demo.mp4
```

Output lands in `.artifacts/tour-video/`: the canonical `tour-video-demo.mp4`,
an optional `.gif` + contact sheet, and numbered `NN-<step-id>.png` screenshots.

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

- **Golden feature-tour spec + manifest:**
  `tools/runstatus/tests/playwright/agent-actions-video.spec.ts` +
  `tools/runstatus/src/tour/agent-actions-manifest.ts`
- Sibling feature tour: `trace-features-video.spec.ts` + `src/tour/trace-manifest.ts`
- Onboarding tour spec + manifest: `tour-video.spec.ts` + `src/tour/manifest.ts`
- Tour robustness test: `tools/runstatus/tests/playwright/tour-onboarding.spec.ts`
- Full-product walkthrough spec: `tools/runstatus/tests/playwright/multi-story.spec.ts`
- Tour QA templates: `docs/skills/kitsoki-ui-qa/templates/tour-{feature,scenarios}.*`
- Shared helpers (video→MP4, server, pacing): `tests/playwright/_helpers/server.ts`
- Playwright config + globalSetup: `tools/runstatus/playwright.config.ts`,
  `tools/runstatus/tests/playwright/_helpers/`
- No-LLM posture + UI surfaces: `docs/web/README.md`
- File:// snapshot artifacts (static, no server): `_helpers/artifact.ts`

## Maintenance

Exposed to Claude Code via a symlink (skills under `docs/` aren't auto-discovered):

```
ln -s "$(pwd)/docs/skills/kitsoki-ui-demo" ~/.claude/skills/kitsoki-ui-demo
```
