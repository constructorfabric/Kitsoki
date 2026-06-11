# Video feedback mode (`/review`)

The `/review` route in the kitsoki web UI (the Vue 3 SPA under
`tools/runstatus/`, served by `kitsoki web`) is a **flag-and-instruct surface
over a recorded video**. Once a producer emits a video artifact plus a chapter
sidecar (see [`host.video.frame` and `host.slidey.render`](../architecture/hosts.md)),
the operator can scrub the video, see its scenes as a timeline, **flag a scene
or a `[start,end]` range**, grab that frame as a still, type an instruction,
and dispatch a structured **feedback note**. The
[`stories/mockup-video/`](../stories/mockup-video.md) authoring story drains
those notes and turns each into a recorded, source-targeted refine.

The panel **captures and dispatches; it never edits a spec or calls a
code-writing LLM itself**. The interpretive edit is a recorded story decision,
not a web-tier action.

## Mental model

The timeline is the map (each marker is a scene); a flag is an annotation
pinned to a moment with a still and an instruction; dispatching a flag hands a
structured note to whoever is producing the video. The operator never sees a
timestamp-to-scene lookup or an ffmpeg call — they see "flag scene 3, say
what's wrong, send."

## Layout

```
/review/:sessionId?video=<handle>

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

The route is `/review/:sessionId?video=<handle>` — the session id is a path
param so the panel can route its RPCs and per-flag chat to the right live
session; `video` is the artifact handle being reviewed.

## Components

All Vue components are data-driven (`tools/runstatus/src/`):

| Component | Data source | Renders |
|---|---|---|
| `views/ReviewPage.vue` | router (`?video=<handle>`) | two-column shell; loads chapters + flags; URL sync |
| `components/ChapterTimeline.vue` | `runstatus.video.chapters` | markers per chapter; click-to-seek; drag → `[start,end]` range; "flag" action |
| `components/FlagList.vue` | local flag store (`lib/flags.ts`) | flags ordered by `start_ms`; select pins to detail |
| `components/FlagDetail.vue` | selected flag | captured still (`media` image), resolved `source_ref` + IDE deep-link, per-flag chat, dispatch buttons |

- The player and the captured still are **`media` elements** rendered by the
  existing `ViewElement.vue`; `src` comes from the `DataSource.artifactUrl`
  resolver, not a world path.
- The per-flag chat reuses the off-path chat store + `session.offpath` surface
  used on the run surface and the story editor — read-only discussion, not the
  dispatched note.
- The IDE deep-link is the `vscode://file/{path}:{line}` pattern, pointed at
  the chapter's `source_ref` (`lib/flags.ts#vscodeLink`).
- A `[start,end]` range may span two chapters; it resolves to the **dominant**
  chapter (max overlap, point-flag containment — `lib/flags.ts#dominantChapter`).
- The still is grabbed **eagerly** the instant a flag is pinned, so the note
  always carries a `frame_handle`.

## Backend serving

Three RPCs on the existing `internal/runstatus/server/` server (same JSON
transport, no new process/port; route table in `server.go#Handler`,
implementations in `video.go`):

| RPC | Does | Notes |
|---|---|---|
| `runstatus.video.chapters {video}` | Read the chapter sidecar for a video handle | `[]` if no sidecar (degrade to "no chapters") |
| `runstatus.video.frame {video, t_ms}` | Grab a still at `t_ms`, return an artifact handle | Wraps `internal/video.Frame` (the one ffmpeg site) |
| `runstatus.feedback.add {video, source_ref, time_range, frame_handle, instruction}` | Persist a feedback note | Append-only to `<session>.feedback.jsonl` |

All three gate the `video` param through the **same `ArtifactResolver` seam**
that `/artifact/{id}` uses (`resolveVideoPath`): an unknown handle or a
non-clean / relative resolved path returns `codeNotFound`. No arbitrary path is
extractable — the escape-path test (`TestVideoFrame_404OnEscapingPath`) is
non-negotiable.

`runstatus.video.frame` is the one new server-side side effect with teeth: it
shells ffmpeg via `internal/video.Frame` and writes a PNG. The ffmpeg runner is
injected (`server.SetFrameRunnerForTest`), so CI never shells ffmpeg. The still
is recorded via the `FrameRecorder` DI seam on `server.Entry` (default
`JournalFrameRecorder`, which journals an `artifact.emitted` entry so the
existing `JournalArtifactResolver` serves the PNG with no special case).
Feedback notes go through the `FeedbackSink` seam (default `JSONLFeedbackSink`).
Both seams are wired in production by `cmd/kitsoki/registry.go`: each live
`Entry` stamps `Frames` (under `frames/` in the session dir) and `Feedback`
(`<sid>.feedback.jsonl`). The `server.FeedbackNote` shape is exported as the
dispatched-note contract; a live-session dispatch path can implement
`FeedbackSink` to drain notes straight into a running story (file-only in v1).

## Tests

Web surface — Vitest + Playwright (not the Go `CapturedIO` harness):

- **Vitest** `tests/unit/ChapterTimeline.test.ts`, `tests/unit/FlagDetail.test.ts`.
- **Playwright** `tests/playwright/review.spec.ts` — server-stubbed (a static
  server + `page.route` interception of the three RPCs + `/artifact/`); flag a
  scene → eager still grab → instruct → assert `runstatus.feedback.add` fired
  with the resolved `source_ref` + frame handle.
- **Go** `internal/runstatus/server/video_test.go` — frame resolves a recorded
  handle and 404s on an unknown/escaping video; `feedback.add` appends a
  well-formed note.

## Non-goals

- **Editing the video or its spec in the browser** — the panel dispatches
  feedback; the LLM edits in the story. No trim/reorder/splice.
- **Calling a code-writing LLM from the web tier** — the per-flag chat is the
  read-only off-path oracle for discussion; the refine that edits source is the
  story's recorded `oracle.task`.
- **A general video library / browser** — `/review` reviews one video by
  handle; discovery is the run surface / story's job.
- **TUI (terminal) support** — web-only.
