# TUI: Web media rendering + artifact serving

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   tui
**Epic:**   ../visual-outputs.md

<!-- "tui" here is the kitsoki web UI (tools/runstatus, the Vue 3 SPA served
     by `kitsoki web`) plus its Go serving layer — the operator-facing
     surface. The in-terminal pointer rendering ships in slice 1. -->

## Why

The substrate slice records a produced visual as a `media` view element
referencing an artifact handle, and the TUI shows it as a pointer to the
`.artifacts/` file. But the web UI — the surface where a visual output is
actually *worth* showing — can't render it. The Vue renderer is text-only:
`ViewElement.vue` dispatches `prose`/`heading`/`code`/`list`/`kv`/`banner`
(choice goes to the input bar), with no image/video/embed path, and there
is **no media handling anywhere** in `tools/runstatus/src`. Worse, the
`kitsoki web` server can't even deliver a binary: `server.Handler()` routes
only `/rpc`, `/rpc/events`, and `/` (the embedded SPA) — there is no
static-file or artifact route, and assets are `//go:embed`-baked into the
binary (`internal/runstatus/web/web.go`). A `<video src=…>` would 404.

## What changes

One sentence: **render the `media` view element inline in the web UI
(`<video>`/`<img>`/PDF+HTML embed), fed by a new `kitsoki web` route that
streams artifact binaries by handle in live mode and by sidecar files in
the static snapshot mode.**

- Add a `media` case to the `ViewElement` TS type + `ViewElement.vue`
  renderer, discriminating on the recorded `kind`/`mime`.
- Add a `GET /artifact/{id}` route to the runstatus server that resolves a
  handle to its recorded `.artifacts/` path (from the slice-1 datapoint)
  and streams the bytes with the right `Content-Type` (+ range support for
  video scrubbing).
- For the static `export-status` artifact mode (no server), copy referenced
  artifacts into a sidecar directory beside the exported HTML and rewrite
  `media` handles to relative URLs.

## Impact

- **Code:**
  - Frontend — `tools/runstatus/src/types.ts` (ViewElement kind),
    `tools/runstatus/src/components/ViewElement.vue` (render),
    `tools/runstatus/src/data/source.ts` (resolve handle → URL across the
    live/snapshot `DataSource` split, `createDataSource()`).
  - Backend — `internal/runstatus/server/server.go` (`Handler()` route
    table; add `/artifact/{id}` next to `/rpc`, `/rpc/events`, `/`), and the
    `export-status` path (`cmd/kitsoki/export_status.go`,
    `internal/runstatus/`) for snapshot sidecars.
- **Rendering:** one new typed element; stays data-driven (no hand-rolled
  HTML strings — the renderer maps `kind` → element, consistent with the
  existing dispatch).
- **Input:** none — media is display-only (no new commands/keys).
- **Docs on ship:** `tools/runstatus/README.md`,
  `docs/proposals/runstatus-proposal.md` consumer notes, `docs/tui/`.

## Mental model

A media element is "an attachment the run produced, shown where it was
produced." In the live UI it plays/scrolls inline in the transcript or
detail drawer; in a shared static export it's a sidecar file the HTML
points at. The operator never sees a handle — they see the video.

## Layout

```
ChatTranscript / detail drawer:

┌──────────────────────────────────────┐
│ assistant turn                         │
│  …prose elements…                      │
│  ┌──────────────────────────────────┐ │
│  │ ▶  [ video player ]              │ │   kind=video  → <video controls>
│  │    Architecture walkthrough       │ │   caption from element
│  └──────────────────────────────────┘ │
└──────────────────────────────────────┘

kind=image  → <img>          kind=pdf → <embed>/<iframe>
kind=html   → sandboxed <iframe>  (slidey interactive export)
```

## Rendering changes

- **TS type** (`tools/runstatus/src/types.ts`): add `"media"` to the
  ViewElement `Kind` union with fields `{ Handle, Kind, Mime, Label }`
  mirroring the recorded datapoint (PascalCase, as the Go server already
  sends — the file notes this convention).
- **`ViewElement.vue`**: add `v-else-if="el.Kind === 'media'"` branches by
  mime family: `video/*` → `<video controls preload="metadata">`, `image/*`
  → `<img loading="lazy">`, `application/pdf` → `<iframe>`, `text/html`
  (slidey export) → sandboxed `<iframe>`. `src` comes from the data source's
  handle→URL resolver, not from a world path.
- **`source.ts`**: add `artifactUrl(handle)` to the `DataSource` interface.
  `LiveSource` returns `/artifact/{id}`; `SnapshotSource` returns the
  relative sidecar path. This is the one place the live/snapshot difference
  is absorbed (`createDataSource()` already forks here).
- Unchanged: every existing element kind; the transcript/drawer layout; the
  input bar.

## Backend serving

- **Live** (`server.go`): `GET /artifact/{id}` looks the id up in the
  session's recorded `artifact` datapoints (slice 1), validates the
  resolved path is under the artifacts root, and serves it via
  `http.ServeContent` (gets `Content-Type`, `ETag`, and HTTP range for free
  — needed for video seeking). 404 on unknown/escaping id. This is the
  first non-`/rpc`, non-SPA route on the server.
- **Snapshot** (`export-status`): when inlining the snapshot JSON, also
  collect every referenced artifact, copy it into `<export>.artifacts/`,
  and emit relative URLs (epic Q2 — sidecar over base64-inline; a tiny PNG
  *may* inline, but video must sidecar).

## Input & commands

| Command / key | Does | Notes |
|---|---|---|
| (none) | media is display-only | No new commands or keybindings. |

## Rendering tests

The Vue renderer is covered by the existing Vitest unit suites
(`tools/runstatus/tests/unit/`) and Playwright specs
(`tools/runstatus/tests/playwright/`), not the Go `CapturedIO` harness
(that guards the *terminal* TUI — the in-terminal media pointer's combined-I/O
test lives in slice 1).

- **Vitest** — `ViewElement` with a `media` element of each mime family
  renders the right tag with the resolved URL; unknown mime degrades to a
  labeled link (no crash).
- **Playwright** — drive a session whose turn carries a `media` element
  against a stub artifact route serving a fixture file; assert the
  `<video>`/`<img>` mounts with a non-404 `src`. Confirm the spec fails
  against the current (route-less) server before the change.
- **Go** — `server_test.go`: `/artifact/{id}` serves a fixture under the
  root, sets `Content-Type`, honors a `Range` request, and 404s on an id
  that escapes the root.

## Migration plan

Purely additive — no surface is replaced. Ships behind the presence of
`media` elements in the trace (which only appear once slice 1 + a producer
are in use), so the UI change is invisible until a story emits one.

## Tasks

```
## 1. Render (web)
- [ ] 1.1 `media` ViewElement TS type (tools/runstatus/src/types.ts)
- [ ] 1.2 ViewElement.vue media branches (video/image/pdf/html by mime)
- [ ] 1.3 DataSource.artifactUrl(handle) — live URL vs snapshot sidecar (source.ts)

## 2. Serve (Go)
- [ ] 2.1 GET /artifact/{id} on server.Handler() — resolve handle, ServeContent, range, root-guard
- [ ] 2.2 export-status: copy referenced artifacts to sidecar dir + relative URLs

## 3. Prove + document
- [ ] 3.1 Vitest (media element renders per mime) + Playwright (mounts non-404 src; fails pre-change)
- [ ] 3.2 Go server_test: serve/range/404-escape
- [ ] 3.3 Manual `kitsoki web` run with a real emitted artifact; screenshot
- [ ] 3.4 Update tools/runstatus/README.md + docs/tui/; trim/delete this proposal
```

## What we lose, honestly

Serving artifact binaries makes the runstatus server stateful about the
filesystem for the first time (today it only emits JSON + the embedded
SPA). The root-guard + handle-indirection contain it, but it is a real new
attack surface (path traversal) that the route-less server didn't have —
the `server_test` escape case is non-negotiable. Static exports also stop
being a single self-contained HTML file once a video sidecar exists
(epic Q2).

## Open questions

1. **html (slidey interactive) embed safety.** A slidey HTML export is
   arbitrary JS. *Lean: sandboxed `<iframe sandbox>` for `kind: html`, or
   gate it behind a click-to-open in v1 rather than auto-mounting.*
2. **Detail-drawer vs. inline transcript placement.** Show media inline in
   the turn, or as a chip that opens the drawer? *Lean: inline for image,
   chip-to-drawer for large video, by `bytes` threshold from the datapoint.*

## Non-goals

- In-terminal media rendering — the TUI pointer is slice 1; no sixel/kitty.
- Producing the media — slices 1 & 2.
- A general static file server — only recorded artifact handles are
  resolvable; arbitrary paths are not.
