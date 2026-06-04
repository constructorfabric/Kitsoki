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
     (retina), `recordVideo: { dir, size }`. Close the context to flush the
     `.webm`, then copy it to a stable name.
   - **Pacing** — gate every delay on `WEB_CHAT_PACE` (typing delay, a beat
     before each click, a dwell on each settled scene) so the same spec runs
     fast for CI and slow for the camera.
   - **Hash routing** — URLs are `#/`, `#/s/:id`, `#/s/:id/chat`.

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

## Pointers

- Template spec: `tools/runstatus/tests/playwright/multi-story.spec.ts`
- Playwright config + globalSetup: `tools/runstatus/playwright.config.ts`,
  `tools/runstatus/tests/playwright/_helpers/`
- No-LLM posture + UI surfaces: `docs/web/README.md`
- File:// snapshot artifacts (static, no server — different from this live
  recording): the `artifact`/`bugfix` specs + `_helpers/artifact.ts`

## Maintenance

Exposed to Claude Code via a symlink (skills under `docs/` aren't auto-discovered):

```
ln -s "$(pwd)/docs/skills/kitsoki-ui-demo" ~/.claude/skills/kitsoki-ui-demo
```
