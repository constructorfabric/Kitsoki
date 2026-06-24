# Unified artifact annotation

A room artifact — a **png, mp4, rrweb recording, static HTML, or a slidey deck**
— can be viewed and annotated with **location-tied feedback**: the operator
points at a place on the artifact, attaches an instruction, and the bundle rides
the existing read-only [off-path](operator-ask.md) surface to the agent (and the
feedback/refine loop). This generalises the rrweb-only [spatial
capture](../tui/spatial-capture.md) / [visual ambient](visual-ambient.md) seam to
every media kind through one **anchor union** and one **producer-agnostic plugin
contract**, so kitsoki never learns a producer's internals.

## The anchor union

Every pick — a drawn box, a video time-range, a resolved DOM node, a slidey scene
element — normalises to one `AnnotationAnchor` with a discriminated `target`. The
authoritative Go shape is `host.AnnotationAnchor` (`internal/host/annotation_anchor.go`);
the web mirror is `lib/annotationAnchor.ts`.

```
AnnotationAnchor {
  media_handle?  string          // the source artifact
  frame_handle?  string          // a captured still, when applicable
  media_kind     png|mp4|rrweb|html|slidey
  t_ms?          int             // position in time-based media
  route?         string          // human context
  target:
    | time_range      { start_ms, end_ms? }                 // mp4 / rrweb timeline
    | frame           { frame_handle, t_ms? }               // a still → treated as png
    | dom_node        { selector, role, text, bbox }        // rrweb / html / live DOM
    | region          { shape: box|freeform|highlight, path[], bbox }  // png / frame draw
    | semantic_element{ plugin, ref, bbox? }                // slidey & future plugins
}
```

The bundle is a **strict superset** of the v1 visual bundle (`VisualSchemaVersion`
bumped 1→2): a v1 payload that sends only the flat `point`/`element`/`t_ms`
fields is still accepted and **normalised** into a `dom_node`/`frame`/`time_range`
anchor, so every recorded cassette and the spatial-oracle paths run
byte-identically. An explicit `anchor` wins over synthesis. The anchor is recorded
as `input.visual.anchor` on the agent call (see [trace-format](../tracing/trace-format.md))
and exposed to prompts as `{{ args.visual.anchor.target.kind }}` + the kind's
sibling fields.

### One model, five surfaces

| media kind | substrate | targets the picker emits |
|---|---|---|
| **png** | `<img>` + canvas overlay | `region` (box / freeform / highlight) |
| **mp4** | `<video>` + timeline + frame-grab | `time_range`; grab a frame → `frame` → `region` |
| **rrweb** | rrweb Replayer iframe ([ReplayFrame](../tui/spatial-capture.md)) | `time_range`, `dom_node`, or `region` |
| **html** | sandboxed iframe (frozen) | `dom_node` or `region` |
| **slidey** | poster-backed semantic overlay | `semantic_element` (preferred), else `dom_node`/`region` |

rrweb/html/slidey resolve a DOM root the *same* way (`elementFromPoint`); png/frame
share the *same* canvas draw layer; mp4 is "grab a frame, then behave like png";
the timeline is shared by mp4 + rrweb. The web dispatch surface is
`components/ArtifactAnnotator.vue` (one `<Annotate>` affordance on a media
[`ViewElement`](../web/README.md)); the draw layer is `RegionDrawLayer.vue`; the
slidey overlay is `SemanticOverlay.vue`.

## The semantic-sidecar plugin contract

A producer that bakes addressable elements into its pixels (slidey, a diagram
renderer, …) ships a sibling **`<name>.semantic.json`** beside the rendered
artifact. Kitsoki is strictly producer-**agnostic**: it parses the envelope and
round-trips each element's `ref` **verbatim** — it never interprets it.

```json
{ "plugin": "slidey", "schema_version": 1,
  "elements": [
    { "ref": "1/card_0", "label": "Scene 1 · card 0",
      "selector": "[data-slidey-el='1/card_0']", "bbox": [140,518,535,114], "t_ms": 3200 }
  ] }
```

Only `plugin` + `elements[].ref` are load-bearing for kitsoki; `label` / `selector`
/ `bbox` / `t_ms` are optional picker hints. `ref` is an **opaque string** (slidey
uses `<sceneIndex>/<el>`, the same value as its `data-slidey-el` stamp, so the
generic `dom_node` resolver also works when no sidecar is present). A missing
sidecar is **not** an error — the artifact simply has no declared semantics and
the annotator falls back to the pixel/DOM picker (the graceful
[FrameResolver](visual-ambient.md) posture).

- **Reader (Go):** `host.SemanticSidecar` / `DiskSemanticSidecarReader`
  (`internal/host/semantic_sidecar.go`), DI-mirroring `FrameResolver`. Pairing
  rule: `out.mp4` → `out.semantic.json` (`SemanticSidecarPath`).
- **Client registry (web):** `lib/semanticPlugins.ts` maps `plugin` → an optional
  label formatter; an absent entry renders generically. This is the *only* plugin
  surface — everything else is data.
- **Producer (slidey):** `~/code/slidey/src/semantic.js` emits the sidecar and
  stamps `data-slidey-el` during render; deterministic (same spec → identical
  sidecar).

## Serving the companions

The web layer resolves an artifact's companions as **siblings of the resolved
artifact path**:

- `runstatus.artifact.semantic {session_id, handle}` → the parsed sidecar (or
  `null` when absent / unknown handle — never an error). `internal/runstatus/server/server.go`.
- `GET /artifact/<id>/poster` → the sibling `<stem>.poster.png` — the backdrop the
  slidey overlay floats markers over for a non-HTML (mp4-rendered) deck.

So a media file emitted into the artifacts root must bring its companions along.
[`host.artifacts_dir`](hosts.md#hostartifacts_dir) co-locates `<stem>.semantic.json`
and `<stem>.poster.png` next to the copied media (the same way it already travels a
video's `.chapters.json`), best-effort and kind-agnostic.

> **`--flow` caveat.** In the deterministic `--flow` posture every `host.*` call —
> including `host.artifacts_dir` — is stubbed, so artifacts are not journaled to a
> server-resolvable path. A no-LLM demo/tour serves the baked companions by
> intercepting `/artifact/*`, `/artifact/*/poster`, and the
> `runstatus.artifact.semantic` RPC at the network edge (transport only; the
> annotator/overlay/anchor path runs unmocked). See `stories/slidey-edit`.

## Read-only — the moat holds

The annotation bundle feeds a **read-only** agent call; there is no web-tier write
path off a click (the [visual-ambient moat](visual-ambient.md#read-only--the-moat-holds)).
Guidance is recorded as a decision with its `input.visual` alongside.

## The PoC: `stories/slidey-edit`

A story for creating/editing slidey presentations end-to-end:
`drafting` (author a deck spec) → `rendering` (`host.slidey.render` → mp4 +
`.semantic.json`) → `reviewing` (the deck as media + Annotate) → `refining`
(consume the anchored feedback, edit the exact scene element, re-render) →
`reviewing`. A deterministic flow + cassette + baked artifacts make it no-LLM
(`go run ./cmd/kitsoki test flows stories/slidey-edit/app.yaml`); the tour video
walks the annotate→refine loop.
