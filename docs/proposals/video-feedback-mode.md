# TUI: Video feedback mode (`/review`)

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   tui
**Epic:**   ../mockup-video-studio.md

<!-- "tui" here is the kitsoki web UI (tools/runstatus, the Vue 3 SPA served
     by `kitsoki web`) plus its Go serving layer. -->

## Why

Once `visual-outputs` slice 3 lands, the web UI can *play* a produced video
inline (`media` element → `<video controls>` via `/artifact/{id}`,
`web-media-rendering.md`). But playing is all it can do. There is no way to
pause at 0:14, say "this transition is too fast" or "scene 3's heading is
too small," and turn that into something the LLM acts on. Improving a video
today means leaving the browser, finding the slidey scene or the mockup HTML
by hand, editing it, and re-running the producer — the same YAML-expedition
problem the story editor (`story-editor-view.md`) solves for authoring,
applied to video.

This slice adds the **review surface**: scrub the video, see its scenes as a
timeline, **flag a scene or a `[start,end]` range**, grab that frame as a
still, type an instruction in a per-flag chat, and dispatch a structured
**feedback note** that the authoring story (slice 3) turns into a recorded,
source-targeted refine.

## What changes

A new **`/review`** route in the SPA, sibling to the run surface `/` and the
`/editor` story editor (`story-editor-shell.md`). Two columns, mirroring the
editor's proven layout:

- **Left column:** the video player + a **chapter timeline** (markers from
  the slice-1 sidecar) + the **flags list**. Click a marker to seek; drag
  across the timeline to select a `[start,end]` range; "flag this" pins a
  flag at the current scene or range.
- **Right column:** the **selected flag** — its captured still (a `media`
  image grabbed via the frame RPC), the `source_ref` it resolves to (which
  slidey scene / tour step, with an IDE deep-link to the source), and a chat
  thread where you write the instruction. A **"Send to refine"** button
  dispatches the feedback note; **"Send all"** dispatches the batch.

The panel **captures and dispatches; it never edits a spec or calls a
code-writing LLM itself** (epic shared decision 3) — the interpretive edit is
a recorded story decision in slice 3.

## Impact

- **Code:**
  - Frontend — new route + `ReviewPage.vue`, `ChapterTimeline.vue`,
    `FlagList.vue`, `FlagDetail.vue` in `tools/runstatus/src/`; reuses
    `ViewElement.vue` (the `media` element from `web-media-rendering.md`) for
    the player and the still, the off-path chat store + `session.offpath`
    pattern (`story-editor-shell.md` left column) for the per-flag chat, and
    `MarkdownModal.vue`'s Teleport pattern for full-frame zoom.
  - Backend — `internal/runstatus/server/server.go` (`Handler()` route
    table): three read/capture RPCs (table below), beside the
    `/artifact/{id}` route from `web-media-rendering.md`.
- **Rendering:** data-driven, no hand-rolled HTML strings; the player and
  still are `media` elements, consistent with the existing dispatch.
- **Input:** mouse-driven (seek / drag-select / flag / dispatch) + the chat
  composer. No new slash commands or keybindings on the run surface.
- **Docs on ship:** `docs/tui/video-review.md`; cross-link from
  `docs/tui/web-ui.md`.

## Mental model

`/review` is a **flag-and-instruct surface over a recorded video**. The
timeline is the map (each marker is a scene); a flag is an annotation pinned
to a moment with a still and an instruction; dispatching a flag hands a
structured note to whoever is producing the video. The operator never sees a
timestamp-to-scene lookup or an ffmpeg call — they see "flag scene 3, say
what's wrong, send."

## Layout

```
/review route:

┌───────────────────────────────────┬───────────────────────────────────┐
│  ▶ [ video player ]               │  Flag #2  ·  scene 3 "Run view"     │
│                                    │  ┌─────────────────────────────┐   │
│  ├──┬─────┬───┬──────┬────┤  0:14 │  │  [ captured still @ 0:14 ]  │   │
│   1  2     3   4      5            │  └─────────────────────────────┘   │
│   chapter timeline (seek/select)   │  source: deck.json#scene:run_view  │
│                                    │          [↗ open]                   │
│  Flags                             │  ┌─────────────────────────────┐   │
│   ● #1  0:03  "logo too small"     │  │ chat                        │   │
│   ● #2  0:14  ← selected           │  │ > heading clips on mobile   │   │
│   ○ #3  0:22–0:27 (range)          │  └─────────────────────────────┘   │
│                                    │  [ Send to refine ]  [ Send all ]   │
└───────────────────────────────────┴───────────────────────────────────┘
```

## Rendering changes

New Vue components, all data-driven:

| Component | Data source | Renders |
|---|---|---|
| `ReviewPage.vue` | router (`?video=<handle>`) | two-column shell; loads chapters + flags; URL sync |
| `ChapterTimeline.vue` | `runstatus.video.chapters` | markers per chapter; click-to-seek; drag → `[start,end]` range; "flag" action |
| `FlagList.vue` | local flag store | flags ordered by `start_ms`; select pins to detail; severity/status dot |
| `FlagDetail.vue` | selected flag | captured still (`media` image), resolved `source_ref` + IDE deep-link, per-flag chat, dispatch buttons |

- The player and the captured still are **`media` elements** rendered by the
  existing `ViewElement.vue` (`web-media-rendering.md`); `src` comes from the
  `DataSource.artifactUrl(handle)` resolver, not a world path.
- The per-flag chat reuses the off-path chat store + `session.offpath`
  surface already used on the run surface and the editor
  (`story-editor-shell.md` task 4.1) — no new chat backend.
- The IDE deep-link is the same `vscode://file/{path}:{line}` pattern as the
  editor (`story-editor-shell.md`), pointed at the chapter's `source_ref`.

## Backend serving

Three new RPCs on the existing `internal/runstatus/server/` server (same
JSON transport; no new process/port — like `story-graph-api.md`'s editor
RPCs):

| RPC | Does | Notes |
|---|---|---|
| `runstatus.video.chapters {video}` | Read the slice-1 chapter sidecar for a video handle | 200 with `[]` if no sidecar (degrade to "no chapters") |
| `runstatus.video.frame {video, t_ms}` | Grab a still at `t_ms`, return an artifact handle for the still | Wraps the slice-1 `internal/video.Frame` extractor (epic decision 2 — one ffmpeg site); records the PNG via the substrate |
| `runstatus.feedback.add {video, source_ref, time_range, frame_handle, instruction}` | Persist + dispatch a feedback note | Append to `feedback.jsonl` always; if a live authoring session is registered, also dispatch into it (epic Q2) |

`runstatus.video.frame` is the one new server-side side effect with teeth: it
shells ffmpeg (via the slice-1 extractor) and writes a PNG under
`.artifacts/`. It is **gated to recorded video handles** (resolve through the
trace, root-guard the path) exactly as `/artifact/{id}` is
(`web-media-rendering.md` "What we lose, honestly") — no arbitrary path is
extractable.

## Input & commands

| Interaction | Effect |
|---|---|
| Click chapter marker | Seek player to `start_ms` |
| Drag across timeline | Select a `[start,end]` range |
| "Flag this" | Pin a flag at the current scene/range; grab the still (`runstatus.video.frame`) |
| Select a flag | Load it in `FlagDetail` |
| Type in flag chat | Off-path chat (`session.offpath`), scoped to the flag |
| "Send to refine" / "Send all" | `runstatus.feedback.add` (one / batch) |
| Click `[↗ open]` | Open `source_ref` in VS Code via `vscode://file/` |

No slash commands; the surface is mouse-driven.

## Rendering tests

Web surface — Vitest + Playwright, not the Go `CapturedIO` harness
(`web-media-rendering.md` "Rendering tests"):

- **Vitest: `ChapterTimeline.spec.ts`** — given a fixture chapter list,
  markers render at the right positions; click emits a `seek`; drag emits a
  `[start,end]`.
- **Vitest: `FlagDetail.spec.ts`** — a flag with a still handle + `source_ref`
  renders the `media` still, the deep-link, and the dispatch buttons; dispatch
  emits the correct feedback-note payload.
- **Playwright: `review.spec.ts`** — spawn `kitsoki web` against a fixture
  session carrying a `media` video + a stub `runstatus.video.chapters`;
  navigate to `/review`, flag a scene (stub `runstatus.video.frame` serves a
  fixture PNG), type an instruction, click "Send to refine", assert
  `runstatus.feedback.add` fired with the resolved `source_ref`. Confirm the
  spec fails against the current (route-less) server before the change.
- **Go: `server_test.go`** — `runstatus.video.frame` resolves a recorded
  handle, returns a still handle, and 404s on an unknown/escaping video;
  `runstatus.feedback.add` appends a well-formed note.

## Migration plan

Purely additive — a new route; no existing surface changes. Invisible until a
trace carries a `media` video with a chapter sidecar (which only appears once
slice 1 + a producer are in use).

## What we lose, honestly

`runstatus.video.frame` makes the server shell out to ffmpeg on an operator
action — a heavier side effect than the read-only editor RPCs. The
handle-resolution + root-guard contain it (no arbitrary path is grabbable),
and frames are produced on demand and cached by handle, but it is a real new
execution surface the read-only server didn't have — the `server_test`
escape case is non-negotiable. Feedback notes also introduce a second write
site under `.artifacts/` (the `feedback.jsonl`); it is append-only and
session-scoped.

## Open questions

1. **Live-session dispatch mechanism.** "Send to refine" into a *running*
   `stories/mockup-video/` session needs a channel from the web tier into the
   story's input. Options: (a) inject a `feedback feedback="…"` intent via the
   existing session RPC (like `session.offpath`), (b) file-only — the story
   drains `feedback.jsonl` on its next refine turn. *Lean: (b) for v1
   (always works, no session-coupling), (a) as the fast path once the session
   RPC seam is confirmed.* (Epic Q2.)
2. **Range flags vs. scene flags.** A `[start,end]` range may span two
   chapters. Resolve to the *dominant* chapter, or attach the note to all
   overlapped `source_ref`s? *Lean: dominant chapter (max overlap) in v1;
   list the others in the note's metadata.*
3. **Still capture: eager or on-flag?** Grab the frame the instant a flag is
   pinned, or lazily when the flag is opened? *Lean: on-flag (eager) so the
   note always carries a `frame_handle`; it's one cheap ffmpeg grab.*

## Non-goals

- **Editing the video or its spec in the browser** — the panel dispatches
  feedback; the LLM edits (slice 3). No trim/reorder/splice (epic non-goal).
- **Calling a code-writing LLM from the web tier** — the per-flag chat is the
  read-only off-path oracle for *discussion*; the refine that edits source is
  the story's recorded `oracle.task` (epic shared decision 3).
- **A general video library / browser** — `/review` reviews one video (by
  handle); discovery is the run surface / story's job.
- **TUI (terminal) support** — web-only; the terminal renders the `media`
  pointer (`visual-outputs` slice 1) and is unchanged.
