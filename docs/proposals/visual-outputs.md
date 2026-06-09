# Epic: Visual outputs from steps

**Status:** Implemented.
**Kind:**   epic
**Slices:** 3 (3/3 shipped)

## Why

A kitsoki step can already emit text artifacts — `host.artifacts_dir`
lands markdown under `.artifacts/` (`internal/host/artifacts_dir_transport.go:50`).
But a growing class of steps produce **visual** output: an MP4 walkthrough,
a PNG slideshow / contact sheet, a slidey deck (MP4 + PDF + interactive
HTML from one JSON spec). Today those only exist as side effects of
`host.run` shelling out to a tool — the engine doesn't know a visual
output was produced, the trace doesn't record it, the web UI can't show
it, and the TUI can't point an operator at it. The render tooling is
real and proven (the standalone `slidey` pipeline; the `contact-sheet.sh`
in `docs/skills/kitsoki-ui-demo/scripts/`) but lives entirely outside the
story machinery, driven by humans and skills.

The moat says every meaningful thing a step does should land as a
**recorded, labeled datapoint** — and consumers should *display* what was
recorded, never reconstruct it from mutable world state
(see memory: *kitsoki-moat-is-architecture*, *narration-belongs-in-trace*).
A visual output is exactly such a datapoint. This epic makes a produced
visual a first-class, recorded **media artifact**: emitted by a step,
written under `.artifacts/`, recorded in the trace, shown inline in the
web UI, and surfaced as a pointer in the TUI.

## What changes

Once every slice has shipped:

- A step can declare it produced a visual file. The engine writes/copies
  it under the artifacts root, records an `artifact` datapoint in the
  trace (kind, mime, label, producing step, path), and binds a stable
  **handle** into world — independent of *how* the file was rendered.
- A new `media` typed-view element references an artifact handle, so a
  room's view can say "here is the deck I just rendered." The TUI renders
  it as a labeled pointer to the `.artifacts/` path (terminals can't play
  video); the web UI renders it inline as `<video>` / `<img>` / a PDF/HTML
  embed.
- First-class producers wrap the existing render tools as host calls —
  `host.slidey.render` (JSON spec → MP4/PDF/HTML) and a contact-sheet /
  PNG-slideshow producer — deterministically and with no LLM in the render
  loop, returning an artifact for the substrate to register.
- The `kitsoki web` server gains a route that serves artifact binaries to
  the browser (live mode) and inlines/sidecars them for the static
  snapshot/artifact mode.

## Impact

- **Spans:** runtime (substrate + producers), tui (web rendering + TUI
  pointer).
- **Net surface:** one new recorded artifact datapoint + world handle
  shape; one new `media` ViewElement kind rendered in both the TUI
  (`internal/tui/`) and the Vue web UI (`tools/runstatus/`); one new
  binary-serving HTTP route on the `kitsoki web` server; two producer host
  calls wrapping existing standalone tools.
- **Docs on ship:** `docs/architecture/hosts.md` (the producer host calls
  + artifact emission), `docs/tracing/trace-format.md` (the artifact
  datapoint), `docs/stories/story-style.md` (the `media` element),
  `docs/tui/` and `tools/runstatus/README.md` (rendering).

## Slices

| # | Slice | Kind | Scope (one line) | Depends on | Status | File |
|---|---|---|---|---|---|---|
| 1 | media-artifact-substrate | runtime | Recorded `artifact` datapoint + world handle + `media` view element + minimal TUI pointer rendering; producer-agnostic | — | Shipped | — |
| 2 | visual-producers | runtime | `host.slidey.render` + contact-sheet/PNG-slideshow host calls wrapping the existing render tools | 1 | Shipped | — |
| 3 | web-media-rendering | tui | Vue `media` element + `kitsoki web` route serving artifact binaries (live) and inline/sidecar (snapshot) | 1 | Shipped | — |

## Sequencing

```
#1 (runtime: substrate) ──┬──▶ #2 (runtime: producers)   parallel once #1 lands
                          └──▶ #3 (tui: web + serving)    parallel once #1 lands
```

Slice 1 is the substrate every other slice consumes — it defines the
artifact record, the world handle shape, and the `media` element. Once it
lands, #2 (producers) and #3 (web rendering) are independent and can ship
in either order. A story can exercise #1 + #3 end-to-end using `host.run`
as the render escape hatch even before #2's first-class producers exist.

## Shared decisions

1. **The artifact record is the source of truth, not world.** Consumers
   (web, TUI) render from the recorded `artifact` datapoint, never by
   re-deriving paths from story files (memory: *narration-belongs-in-trace*).
   Slice 1 owns the record shape; #2 produces it; #3 consumes it.
2. **Handles, not raw paths, cross the world boundary.** A step binds an
   opaque artifact handle (id) into world; the absolute `.artifacts/` path
   lives in the record. This keeps world replay-stable across machines and
   lets the web server resolve a handle → served URL without trusting a
   world-supplied filesystem path.
3. **`media` is one ViewElement kind, not per-format kinds.** A single
   `media` element discriminates on the artifact's mime/kind (video / image
   / pdf / html-embed) at render time, mirroring how `banner` and `choice`
   are one kind each (`internal/app/view_element.go:106`). Avoids a
   combinatorial element vocabulary.
4. **No LLM in any render loop.** Producers are deterministic subprocesses
   (slidey, ffmpeg) — consistent with the cassette/flow test discipline
   (CLAUDE.md; memory: *no-llm-tests*).

## Cross-cutting open questions

1. **slidey dependency model.** slidey lives as a separate repo at
   `~/code/slidey` (not committed here); the `slidey-authoring` skill drives
   it. Options for slice 2: (a) git submodule under `tools/slidey`, (b)
   vendored copy, (c) treat as an optional PATH/`SLIDEY_HOME` dependency the
   producer host call resolves and skips gracefully when absent. *Lean: (c)
   for v1 (matches how `ffmpeg`/`edge-tts` are already external deps), with
   a follow-up to submodule it if it becomes core.*
2. **Snapshot/artifact-mode media transport.** The static `export-status`
   HTML inlines a snapshot JSON (`internal/runstatus/`); a 50 MB MP4 cannot
   sanely base64-inline. Options: sidecar files next to the HTML with
   relative URLs, or a size threshold (small PNGs inline, video sidecar).
   *Lean: sidecar directory; decided in slice 3.*
3. **Relationship to `artifact-format.md`.** That proposal adds a
   schema-verified markdown-with-frontmatter artifact format and a central
   `internal/artifact/` package. A media artifact is binary, not
   markdown — but both want a single notion of "an artifact kitsoki
   produced." *Lean: media artifacts get a frontmatter **manifest**
   (`.md` sidecar or registry entry) in that package's format; the binary
   sits beside it. Coordinate the record shape with that proposal rather
   than forking a second artifact concept.*

## Non-goals

- **Authoring/editing visuals in kitsoki.** Producing a deck spec is the
  author's / a story's job; this epic renders and records, it does not
  build a visual editor.
- **In-terminal video/image rendering.** The TUI surfaces a pointer to the
  `.artifacts/` file; it does not attempt sixel/kitty-graphics inline
  playback (a possible later slice, explicitly out of scope here).
- **Streaming / progressive render.** Producers run to completion and emit
  a finished file; no live frame streaming to the web UI.
- **A new general blob store.** Artifacts stay on the local filesystem
  under the artifacts root; no object storage / upload.
