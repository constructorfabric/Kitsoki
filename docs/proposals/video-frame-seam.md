# Runtime: Video frame seam (chapter sidecar + still grab)

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   runtime
**Epic:**   ../mockup-video-studio.md

## Why

The feedback mode (slice 2) and the authoring story (slice 3) both need two
things a rendered video does not give them today:

1. **A map from a moment in the video back to the scene that produced it.**
   When a reviewer flags 0:14, the refine step has to know *which slidey
   scene or which tour step* that frame came from — otherwise "make this
   bigger" has no source to edit. slidey knows its scene boundaries; the
   `kitsoki-ui-demo` recorder knows each `TourStep`'s dwell window
   (`.agents/skills/kitsoki-ui-demo/SKILL.md`, `WEB_CHAT_PACE`/`dwellMs`). That
   knowledge is thrown away at render time.
2. **A still PNG at an arbitrary timestamp.** To give the LLM visual context
   ("here's the frame you're being asked about") the panel must turn a video
   position into an image. ffmpeg does this in one call; nothing in kitsoki
   wraps it.

Both are **deterministic execution** — no interpretation — so they belong in
a host seam, not the web tier or a prompt.

## What changes

One sentence: **every producer emits a producer-agnostic `chapters` sidecar
mapping each scene to a `[start_ms, end_ms]` window + its `source_ref`, and a
new deterministic `host.video.frame` grabs a still PNG at any timestamp —
backed by one `internal/video` extractor that the slice-2 web RPC reuses.**

- A **chapter sidecar** type: `[{index, id, label, start_ms, end_ms,
  source_ref}]`, `source_ref = {kind: "slidey"|"tour", spec_path,
  scene_id|step_id, line?}` (epic shared decision 1). Emitted as a sibling
  file recorded as its own `artifact`; the video's media handle gains an
  optional `chapters_handle` (epic cross-cutting Q1, leaning sibling-file).
- `host.slidey.render` is extended to write the sidecar from the scenes it
  already iterates; the tour recorder is taught to write the same shape from
  its known step dwell windows.
- `host.video.frame {video: handle|path, t_ms} → {ok, path, mime:
  image/png, kind: image}` — deterministic single-frame grab; returns the PNG
  path for the substrate (`visual-outputs` slice 1) to register, exactly like
  the producers hand off their output.

## Impact

- **Code seams:**
  - `internal/video/` (new) — the extractor (`Frame(videoPath, tMs) →
    pngPath`) and the `Chapter`/`SourceRef` types + sidecar read/write. Pure,
    DI-friendly, no engine deps; reused by the host call and the slice-2 RPC.
  - `internal/host/` — `host.video.frame` handler beside the producers,
    reusing `RunHandler`'s subprocess machinery
    (`internal/host/handlers.go:88-135`) the same way
    `host.slidey.render`/`host.contact_sheet` do
    (`internal/host/visual_producers.go`, `visual-producers.md`).
  - `host.slidey.render` (`internal/host/visual_producers.go`,
    `visual-producers.md` task 1.1) — also emit the sidecar.
  - Tour recorder (`.agents/skills/kitsoki-ui-demo/` Playwright spec) — also
    emit the sidecar.
- **Vocabulary:** one host call (table below); one sidecar format.
- **Stories affected:** none today; opt-in. Slice 3 is the first consumer.
- **Backward compat:** additive. A video without a sidecar still plays
  (slice 2 degrades to "no chapters"); producers without the extension still
  render — they just don't emit chapters.
- **Docs on ship:** `docs/architecture/hosts.md` (`host.video.frame` +
  sidecar shape).

## Vocabulary changes

| Kind | Name | Shape | Notes |
|---|---|---|---|
| host call | `host.video.frame` | `{video: handle \| path, t_ms} → {ok, path, mime: image/png, kind: image}` | Shells `ffmpeg -ss <t> -i <video> -frames:v 1 <out.png>`; deterministic, no LLM. Path hands to slice-1 substrate to register. |
| sidecar | `chapters.json` | `[{index, id, label, start_ms, end_ms, source_ref:{kind, spec_path, scene_id\|step_id, line?}}]` | Producer-agnostic; one shape for slidey + tour (epic decision 1). |

## The model

```
host.slidey.render {spec, format: mp4}                      (visual-outputs #2)
   ├▶ render out.mp4                                          deterministic
   └▶ write out.mp4.chapters.json from the scene list         deterministic, NEW
          source_ref = {kind: slidey, spec_path, scene_id, line}

tour recorder (host.run kitsoki-ui-demo Playwright)          (epic decision 4)
   ├▶ record walkthrough.mp4
   └▶ write walkthrough.mp4.chapters.json from TourStep dwell windows  NEW
          source_ref = {kind: tour, spec_path, step_id}

review flags t_ms = 14_300
   └▶ host.video.frame {video: <handle>, t_ms}               deterministic, NEW
          └▶ ffmpeg single-frame grab ──▶ frame.png
   └▶ host.artifacts_dir {src_path: frame.png, kind: image}  (substrate, #1)
```

The extractor and the chapter math are pure functions of (video, scene
list). Given the same inputs they produce the same sidecar and the same PNG
bytes — no interpretive step (the moat's deterministic side; memory:
*kitsoki-moat-is-architecture*).

## Engine seams & invariants

- **One extractor, two callers** (epic shared decision 2). `internal/video`
  exports `Frame(ctx, videoPath, tMs) (pngPath, error)`. The `host.video.frame`
  handler and the slice-2 web RPC both call it; ffmpeg is invoked from exactly
  one place. Injected, not global, so tests substitute a fake that copies a
  fixture PNG.
- **Tool resolution** mirrors the producers: ffmpeg via `PATH` (already
  assumed by `contact-sheet.sh` and slidey), returning a clear "ffmpeg not
  found" `Result.Error` when absent (`visual-producers.md` Engine seams).
- **No LLM, ever.** Frame grab and sidecar emission are subprocess/pure only.
  Tests stub the extractor to return a checked-in 1×1 PNG and feed a canned
  scene list so CI never runs ffmpeg (CLAUDE.md; memory: *no-llm-tests*,
  *fast-tests*).
- **Substrate owns the write site.** `host.video.frame` returns a path; the
  `artifact` datapoint is recorded by `host.artifacts_dir`
  (`internal/host/artifacts_dir_transport.go:238`), not by this handler —
  same discipline as the producers (`visual-producers.md` "The model").

## Decision recording

Nothing interpretive here, so no gate decision. The host call lands the
usual `host.invoked`/`host.returned` pair (`internal/journal/types.go:70,75`)
for provenance, and the produced PNG + the sidecar are recorded as `artifact`
datapoints by the substrate. The sidecar makes the *video itself* a labeled,
queryable object — every moment tagged with the source that produced it.

## Backward compatibility / migration

Additive. Existing videos (no sidecar) play with chapters disabled in
slice 2. The producer extensions are opt-in render-side additions; the
standalone tools and their skills are untouched.

## Tasks

```
## 1. Extractor + types (internal/video)
- [ ] 1.1 Frame(ctx, videoPath, tMs) → pngPath (ffmpeg -ss -frames:v 1), injectable runner
- [ ] 1.2 Chapter / SourceRef types + chapters.json read/write helpers
- [ ] 1.3 Graceful "ffmpeg not found" error

## 2. Host call
- [ ] 2.1 host.video.frame handler (resolve handle|path → Frame → return path/mime/kind)
- [ ] 2.2 Stub the extractor to a fixture PNG (no ffmpeg in CI); unit test the handler

## 3. Producer sidecar emission
- [ ] 3.1 host.slidey.render: write <out>.chapters.json from the scene list (source_ref kind=slidey)
- [ ] 3.2 kitsoki-ui-demo recorder: write <out>.chapters.json from TourStep dwell windows (kind=tour)
- [ ] 3.3 Flow fixture: render (stub) → sidecar present → host.video.frame (stub) → handle bound, no ffmpeg/LLM

## 4. Document
- [ ] 4.1 hosts.md entry (host.video.frame) + chapter sidecar shape; trim/delete this proposal
```

## Verification

A flow fixture stubs the slidey producer to drop a fixture mp4 + a canned
`chapters.json`, stubs `host.video.frame` to return a fixture PNG, and
asserts the bound frame handle + recorded `artifact` — no ffmpeg/LLM. A
gated (not-by-default) integration test runs the real ffmpeg grab against a
checked-in tiny mp4 to catch drift (memory: *no-llm-tests*,
*e2e-fidelity-and-boundary*).

## Open questions

1. **Timestamp vs. scene addressing in `host.video.frame`.** Accept only
   `t_ms`, or also `scene_id` (resolve to the scene's `start_ms` via the
   sidecar)? *Lean: `t_ms` is canonical; `scene_id` is sugar the slice-2
   RPC resolves before calling — keep the host call timestamp-only.*
2. **Sidecar for the tour path lives in JS (the recorder), not Go.** That
   splits the "emit chapters" logic across two languages. *Lean: accept it
   for v1 — the recorder already owns the dwell windows; a shared JSON schema
   (checked-in) keeps the shapes honest. Consolidating under a Go
   `host.tour.render` is the epic's non-goal.*

## Non-goals

- **Recording the artifacts** — `visual-outputs` slice 1 owns the single
  write site for `artifact`.
- **Producing the video** — `visual-outputs` slice 2 + the tour recorder.
- **Displaying frames or chapters** — slice 2 (`video-feedback-mode.md`).
- **Re-encoding / trimming / concatenating video** — only single-frame
  extraction; no editing.
