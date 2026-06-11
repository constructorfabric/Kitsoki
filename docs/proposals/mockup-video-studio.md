# Epic: Mockup Video Studio

**Status:** Implemented. All 3 slices shipped; narrative docs landed and the
focused slice proposals are deleted. Remaining work is a live end-to-end
demo + adversarial QA (see "Remaining" below).
**Kind:**   epic
**Slices:** 3 (3/3 shipped)

## Why

We pitch and design kitsoki in our **tour-based walkthrough video style**
(the `kitsoki-ui-demo` skill drives Playwright over a real `kitsoki web`
and emits an MP4 + per-scene PNGs; slidey turns a JSON deck into a narrated
MP4). But producing a *design-proposal* video — "here's what this feature
would look like, walked through three user scenarios" — is a manual
expedition: hand-write static HTML mockups, hand-write a tour manifest or
slidey deck, run the recorder, watch the result, notice scene 3's text is
too small, hand-edit, re-run. There is **no structured, recorded process**
for it, and once a video exists there is **no surface to improve it**: the
web UI can play a video (once `visual-outputs` slice 3 lands) but you can't
point at 0:14, say "this transition is too fast," and have that become an
instruction the LLM acts on.

This epic builds both halves of that loop: a **story** that produces a UI
mockup walkthrough video from a scenario brief, and a **web feedback mode**
where an operator scrubs the video, flags a scene or a time range, captures
the frame as a still, and instructs the LLM — closing the
produce→review→refine loop the way `stories/ui-fix/` closes
audit→fix→showcase.

## What changes

Once all three slices ship:

- `kitsoki run stories/mockup-video/app.yaml` walks you from a scenario
  brief to a rendered walkthrough MP4 — authoring static HTML mockups (or a
  slidey deck; `medium: tour | deck`), assembling a walkthrough over your
  user scenarios, rendering it through the shipped `visual-outputs`
  producers, and looping through a **refine** checkpoint until you accept,
  ending in a gallery.
- Every rendered video carries a **chapter sidecar** mapping each moment to
  the scene that produced it (slidey scene id / tour step id + source path),
  and any video position can be turned into a still PNG deterministically.
- `kitsoki web` gains a **`/review`** surface: play the video, see its
  chapters as a timeline, flag a scene or a `[start,end]` range, grab the
  frame, type an instruction in a per-flag chat, and dispatch a structured
  **feedback note** — which, when a live authoring session is running,
  drives the story's refine step against the *exact* source (HTML page or
  slidey scene) that produced the flagged moment.

The user-visible promise: **flag a moment, instruct the LLM, watch the
video improve** — for any kitsoki-produced video, with the mockup story as
the first producer of them.

## Impact

- **Spans:** runtime, tui (web), story.
- **Net surface:** one new deterministic host call + a producer-agnostic
  chapter sidecar (`internal/host/`, `internal/video/`); one new web route +
  panel + three read/capture RPCs (`tools/runstatus/`,
  `internal/runstatus/server/`); one new story (`stories/mockup-video/`).
  No engine state-machine or orchestrator hot-path change.
- **Hard dependency — `visual-outputs.md`:** this epic *consumes* that one.
  Slice 1 (media substrate) is **shipped**; **slices 2 (`visual-producers`)
  and 3 (`web-media-rendering`) are still Draft** and are prerequisites —
  `host.slidey.render` + the `media` element + the `/artifact/{id}` route
  must exist before this epic's frame seam, player, and story have anything
  to stand on. Sequencing below treats them as upstream.
- **Docs on ship:** `docs/architecture/hosts.md` (the frame host call),
  `docs/tui/video-review.md` (the feedback mode), `docs/stories/mockup-video.md`
  (the story); cross-links from `docs/tui/web-ui.md` and the
  `kitsoki-ui-demo` / `slidey-authoring` skills.

## Slices

| # | Slice | Kind | Scope (one line) | Depends on | Status | File |
|---|---|---|---|---|---|---|
| 1 | Video frame seam | runtime | Producer-agnostic chapter sidecar (scene↔timestamp) + deterministic `host.video.frame` still-grab, shared by a host call and a web RPC | `visual-outputs` #1 (shipped); coordinates with #2 | Shipped | [`docs/architecture/hosts.md`](../architecture/hosts.md) |
| 2 | Video feedback mode | tui (web) | `/review` panel: player + chapter timeline + flag-scene/range + per-flag PNG + chat → structured feedback notes | 1; `visual-outputs` #3 | Shipped | [`docs/tui/video-review.md`](../tui/video-review.md) |
| 3 | Mockup video authoring | story | `stories/mockup-video/`: brief → author HTML/deck → render → review → refine-loop → gallery (`medium: tour\|deck`) | 1; `visual-outputs` #2; soft-dep 2 | Shipped | [`docs/stories/mockup-video.md`](../stories/mockup-video.md) |

## Remaining (post-integration)

All three slices are code-complete, build green, and covered by no-LLM flow /
unit / Playwright fixtures. Deferred because they need a real LLM and/or a live
recorder (CLAUDE.md no-auto-LLM rule):

- **Live end-to-end run** — author a real 3-scenario mockup video, flag a scene
  in `/review`, refine, watch the re-render improve (slice 3 task 4.1).
- **`record_tour.sh` ↔ concrete recorder** — wired only when
  `$KITSOKI_UI_DEMO_RECORDER` points at the slice-1 chapters.json-emitting
  harness; verify live.
- **Tour demo of `/review` + adversarial QA** (epic tasks 5 / 6).
- **Optional hardening** — replace `review.spec.ts`'s stubbed RPCs with a true
  `kitsoki web --flow` live session now that slice 3 emits a video + sidecar.

## Sequencing

```
visual-outputs #2 (producers) ─┐
visual-outputs #3 (web media) ─┤
                               ▼
            #1 (runtime: frame seam + chapter sidecar)
                  ├──────────────▶ #2 (web: feedback mode)
                  └──────────────▶ #3 (story: authoring) ◀── soft-dep ── #2
```

Slice 1 is the substrate both #2 and #3 build on. #2 and #3 can then proceed
in parallel: #3 can ship with the video shown inline (`visual-outputs` #3)
and feedback notes appended to a file, gaining the live web feedback loop
once #2 lands.

## Shared decisions

1. **The chapter sidecar is the seam that makes "feedback targets either"
   work.** Every producer (slidey deck, tour walkthrough) emits the *same*
   `chapters` shape — `[{index, id, label, start_ms, end_ms, source_ref}]`
   where `source_ref` names the producing unit (`{kind: slidey|tour,
   spec_path, scene_id|step_id, line?}`). The feedback panel (#2) reads one
   uniform chapter list regardless of source; the refine step (#3) dispatches
   on `source_ref.kind`. Defined once in slice 1.

2. **Deterministic frame extraction is defined once and exposed twice.** A
   single internal extractor (ffmpeg `-ss t -frames:v 1`) backs **both** the
   `host.video.frame` host call (for the story) **and** the web RPC the panel
   calls (for interactive grabs). No second ffmpeg invocation site
   (DI; principle of least surprise). Slice 1 owns the extractor; slice 2's
   RPC wraps it.

3. **The web panel captures feedback; the story performs the refine.** The
   `/review` panel never edits a spec or calls a code-writing LLM itself — it
   produces a structured, recorded **feedback note**
   `{video_handle, source_ref, time_range, frame_handle, instruction}` and
   *dispatches* it. The interpretive edit (`host.oracle.task` over the flagged
   HTML/scene) is a **recorded story decision** in slice 3, not a web
   side-effect. This keeps the moat's interpretive/deterministic split intact
   (memory: *kitsoki-moat-is-architecture*) and stops the web tier from
   acquiring a write path.

4. **No new producer for the tour path in v1.** The slidey deck renders via
   the shipped `host.slidey.render`; the tour walkthrough renders via
   `host.run` wrapping the existing `kitsoki-ui-demo` Playwright recorder.
   Slice 1 teaches *both* to emit the chapter sidecar. Promoting the tour to
   a first-class `host.tour.render` producer is a non-goal (below).

## Cross-cutting open questions

1. **Where does the chapter sidecar live relative to the video?** Options:
   (a) a sibling file (`<video>.chapters.json`) recorded as its own small
   `artifact`, with the video handle gaining an optional `chapters_handle`;
   (b) inline `chapters: [...]` on the media handle metadata. *Lean: (a) —
   keeps the handle shape stable and the sidecar independently servable;
   resolved in slice 1.*

2. **Feedback-note transport when no session is live.** The panel dispatches
   a note; if no authoring session is running there's nothing to receive the
   refine intent. Options: (a) append to a `feedback.jsonl` the story drains
   on next run, (b) require a live session and grey out dispatch otherwise.
   *Lean: support both — file append always works; live dispatch
   (`session.offpath`-style RPC into the running story) is the fast path.
   Settled jointly by #2 and #3.*

3. **Does refine target one flagged scene or a batch?** A reviewer often
   flags several scenes before re-rendering. *Lean: the story collects a
   batch of feedback notes, then one refine pass edits each note's
   `source_ref` and re-renders once — cheaper than per-flag re-renders. The
   panel can dispatch singly or "send all." Detailed in #3.*

## Non-goals

- **A first-class `host.tour.render` producer** — the tour path reuses the
  existing Playwright recorder via `host.run` (shared decision 4). Promoting
  it is a separate runtime slice if dogfood demands it.
- **A timeline *editor*** — `/review` flags-and-instructs; it does not
  re-order scenes, trim, or splice in the browser. Editing is the LLM's job
  via refine, or the author's via the source files.
- **Real video transcoding / effects in the web tier** — the panel plays the
  produced MP4 and grabs stills; it does not re-encode. All render work stays
  in the deterministic producers.
- **Replacing the demo/QA skills** — `kitsoki-ui-demo` (record),
  `kitsoki-ui-qa` (validate), `kitsoki-ui-review` (audit) keep their roles;
  this epic adds *authoring* and *interactive feedback*, and reuses their
  recorder + media seam.
