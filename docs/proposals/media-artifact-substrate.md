# Runtime: Media-artifact substrate

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   runtime
**Epic:**   ../visual-outputs.md

## Why

A step that produces a visual file (MP4, PNG, PDF, slidey HTML) can do it
today only by shelling out via `host.run` (`internal/host/handlers.go:43`)
— the file lands on disk but the engine, the trace, and every consumer are
blind to it. There is no recorded fact "this step produced *that* visual,"
no stable way for a later room to refer to it, and no typed view element to
show it. `host.artifacts_dir` (`internal/host/artifacts_dir_transport.go:50`)
already owns the `.artifacts/` convention for **text** artifacts and the
root-resolution order (args → `$KITSOKI_ARTIFACTS_ROOT` → `cwd/.artifacts`,
lines 151–163), but it only writes markdown bodies and returns
`{ok, path, message_id}` — it has no notion of a binary/media artifact, a
mime type, or a recorded datapoint.

This slice is the producer-agnostic core the rest of the epic builds on: a
recorded **artifact** datapoint, a world **handle**, and a `media` typed
view element — none of which care *how* the file was rendered.

## What changes

One sentence: **a step declares a file it produced as a typed media
artifact; the engine writes it under the artifacts root, records an
`artifact` datapoint in the trace, binds an opaque handle into world, and a
`media` view element renders that handle.**

- Extend the artifacts-dir transport (or add a sibling `host.artifact.emit`)
  to accept a **source file path** + `mime`/`kind` + `label` in addition to
  today's markdown `body`. It copies/moves the file under the artifacts
  root and returns `{ok, handle, path, mime, kind}`.
- Emit a new `artifact` trace datapoint recording the produced artifact
  (see *Decision recording*).
- A new `media` ViewElement kind (`internal/app/view_element.go:106`) holds
  an artifact `handle` (+ optional caption); the canonical TUI renderer
  shows it as a labeled pointer line to the `.artifacts/` path. The web
  renderer (slice 3) shows it inline.

## Impact

- **Code seams:**
  `internal/host/artifacts_dir_transport.go:50` (extend or add sibling),
  `internal/app/view_element.go:106` (new `media` kind + unmarshal),
  `internal/journal/types.go:42-76` (new `artifact` event kind),
  `internal/tui/` (render `media` as a pointer; see `internal/tui/tui.go`
  view path).
- **Vocabulary:** one host-call extension/sibling, one world-key shape
  (artifact handle), one view element, one trace kind — table below.
- **Stories affected:** none change behavior; this is additive. A story
  *opts in* by emitting a media artifact and adding a `media` element.
- **Backward compat:** fully additive. Existing `host.artifacts_dir`
  markdown calls keep their exact `{ok, path, message_id}` shape; the media
  path is reached only when a source file / `kind: media` is supplied.
- **Docs on ship:** `docs/architecture/hosts.md` (emission),
  `docs/tracing/trace-format.md` (the `artifact` datapoint),
  `docs/stories/story-style.md` (the `media` element).

## Vocabulary changes

| Kind | Name | Shape | Notes |
|---|---|---|---|
| host call | `host.artifacts_dir` (extended) | `{src_path, mime, kind, label, thread?} → {ok, handle, path, mime, kind}` | When `src_path` is set, copies the file under the root instead of writing `body`. |
| world key | `<bind>` artifact handle | `{id, kind, mime, label}` | A step binds the returned handle; opaque + replay-stable (no absolute path). |
| view element | `media` | `{handle, caption?, when?}` | One element kind; discriminates video/image/pdf/html at render time on the recorded mime. |
| trace kind | `artifact` | see below | The recorded datapoint: one per produced artifact. |

## The model

```
step (host.run renders foo.mp4)
   └▶ invoke host.artifacts_dir {src_path: foo.mp4, kind: media, mime: video/mp4, label: "Walkthrough"}
          ├▶ copy → .artifacts/<session>/<id>.mp4        (deterministic)
          ├▶ record  artifact datapoint                  (the moat: a labeled fact)
          └▶ bind   world.deck = {id, kind, mime, label}  (opaque handle)

later room view:
   - media: { handle: "{{ world.deck.id }}", caption: "Architecture walkthrough" }
          TUI  → "📹 Walkthrough → .artifacts/<session>/<id>.mp4"
          web  → <video controls src="/artifact/<id>">   (slice 3)
```

Everything left of the world boundary is **deterministic** engine I/O
(file copy, record, patch) — no interpretation. The decision of *what to
render* lives upstream in the step that produced the file; this substrate
only records and references it.

## Decision recording

The produced artifact lands as one `artifact` datapoint in the journal —
mirroring how `host.invoked`/`host.returned`/`world.patch`
(`internal/journal/types.go:70,75,42`) already record host activity, but
making the artifact a **first-class, queryable fact** rather than something
a consumer must reconstruct from a `host.returned` payload.

Fields (reconstructable without touching the filesystem):

- `id` — stable handle (also the served URL key in slice 3).
- `kind` — `video | image | pdf | html | slideshow`.
- `mime` — e.g. `video/mp4`, `image/png`, `application/pdf`.
- `label` — human caption.
- `path` — absolute `.artifacts/` path (for the TUI pointer + the web
  server to resolve; not crossed into world).
- `state` / `producer` — the room/step that emitted it (the datapoint's
  provenance, per the moat).
- `bytes`, `created_at` — size + stamp for display.

This is a **new recorded event** → coordinate the field names with
`docs/tracing/trace-format.md` and slice 3's consumer. Slice 3 reads this
datapoint; it does not re-derive paths from world.

## Engine seams & invariants

- Emission hooks the existing host-call return path, so the `artifact`
  datapoint is recorded in the same place `host.returned` is today
  (`internal/journal/`). One write site keeps record/patch ordering sane.
- **Load-time invariant:** a `media` view element must name a `handle`
  source; a `media` element with neither `handle` nor a literal artifact
  ref fails fast at load with a clear message (mirror the choice-element
  cross-ref validation in `internal/app/choice.go:730`).
- **Path safety:** the resolved destination must stay under the artifacts
  root; reject `..` escapes at emit time (the root resolver already centralizes
  this — `artifacts_dir_transport.go:151-163`).
- **TUI render:** the `media` case is added to the canonical element
  renderer so an unhandled kind can't reach `View()` — defining the kind
  without rendering it would break the renderer (this is why the TUI
  pointer ships in *this* slice, not slice 3).

## Backward compatibility / migration

Additive and opt-in. No existing story, cassette, or flow fixture changes.
The extended `host.artifacts_dir` keeps its current return shape on the
markdown-`body` path; the media path is a distinct branch keyed on
`src_path`/`kind`. No migration needed.

## Tasks

```
## 1. Engine
- [ ] 1.1 Extend host.artifacts_dir (or add host.artifact.emit) for src_path/mime/kind/label → handle
- [ ] 1.2 Add the `artifact` journal event kind + record it on emit (internal/journal/types.go)
- [ ] 1.3 Add the `media` ViewElement kind + YAML unmarshal (internal/app/view_element.go)
- [ ] 1.4 Load-time invariant: media element must resolve a handle; clear error
- [ ] 1.5 Path-escape guard at emit (stay under artifacts root)

## 2. Render (TUI)
- [ ] 2.1 Render `media` as a labeled pointer to the .artifacts path (typed element path, no hand-rolled strings)

## 3. Verification
- [ ] 3.1 Stateless: `kitsoki turn` emits a media artifact; assert handle bind + recorded datapoint
- [ ] 3.2 Flow fixture: a room renders a `media` element pointer (no LLM)
- [ ] 3.3 Rendering test for the TUI pointer (combined I/O; verified to fail without the change)

## 4. Document
- [ ] 4.1 hosts.md (emission), trace-format.md (artifact datapoint), story-style.md (media element)
- [ ] 4.2 Update epic slice table; trim/delete this proposal
```

## Verification

No LLM required. A stateless `kitsoki turn --state … --intent … --world @w.json`
probe drives a room that emits a media artifact (point `src_path` at a
checked-in tiny fixture file) and asserts (a) the world handle bind and (b)
the recorded `artifact` datapoint in the journal. A flow fixture renders a
room with a `media` element and asserts the TUI pointer line. The TUI
rendering test uses `CapturedIO` per the `rendering-tests` skill.

## Open questions

1. **Extend `host.artifacts_dir` vs. new `host.artifact.emit`?** Extending
   reuses the root resolver and the `.artifacts/` semantics but overloads
   one verb across markdown + media. *Lean: extend for v1 (one transport
   owns the root), split later if the branches diverge.*
2. **Handle scheme.** Content hash, monotonic counter (`message_id` already
   uses `<basename>#<counter>` — `artifacts_dir_transport.go`), or UUID?
   *Lean: short content-addressed id so replay is stable and the served URL
   is cacheable.*
3. **Manifest vs. inline record.** Does the artifact also get a frontmatter
   sidecar (per `artifact-format.md`) or only the trace datapoint? *Lean:
   trace datapoint is canonical; a sidecar manifest is a slice-2/3
   convenience, not required by the substrate.*

## Non-goals

- Producing visuals — that's slice 2 (`visual-producers.md`); this slice
  takes a finished file and records it.
- Web rendering / binary serving — slice 3 (`web-media-rendering.md`).
- Inline terminal graphics (sixel/kitty) — the TUI shows a pointer only.
