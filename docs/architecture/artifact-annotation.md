# Unified artifact annotation

A room artifact — a **png, mp4, rrweb recording, static HTML, or a slidey deck**
— can be viewed and annotated with **location-tied feedback**: the operator
points at a place on the artifact, attaches an instruction, and the bundle rides
the existing read-only [off-path](operator-ask.md) surface to the agent (and the
feedback/refine loop). This generalises the rrweb-only [spatial
capture](../tui/spatial-capture.md) / [visual ambient](visual-ambient.md) seam to
every media kind through one **anchor union** and two **producer-agnostic plugin
contracts**, so kitsoki never learns a producer's internals:

- a **static** [semantic-sidecar](#the-semantic-sidecar-plugin-contract) beside a
  rendered artifact (for baked, frozen media), and
- a **live** [embed protocol](#the-embed-protocol-live-producer-surfaces) for an
  interactive producer that owns its own surface (the **slidey deck** — the
  reference example).

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
| **slidey** | the **live interactive deck** ([embed protocol](#the-embed-protocol-live-producer-surfaces)) | `semantic_element` (`<scene>/<el>` picked on the real slide) |

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

This static contract suits a **baked, frozen** artifact (an mp4-rendered deck, a
diagram PNG). An *interactive* producer like the slidey HTML deck instead owns its
own surface and reports picks **live** — see the [embed
protocol](#the-embed-protocol-live-producer-surfaces) below, which is how
`stories/slidey-edit` actually works.

## The embed protocol (live producer surfaces)

A producer that renders an **interactive** surface (the slidey HTML deck is the
reference example) doesn't need a baked sidecar: the live artifact already knows
which view is on screen and which element the operator clicked. kitsoki embeds it
in an iframe and exchanges three **producer-neutral** `postMessage` events — it
interprets none of the producer's internals, only round-trips the opaque
`scope`/`ref` tokens into the refine.

```
host  → producer   embed:annotate  { enabled }                         // toggle pick mode
producer → host    embed:view      { producer, scope, label, count }   // which view is on screen
producer → host    embed:pick      { producer, scope, ref, label, bbox }// the element pointed at
```

- `scope` — the opaque view token (slidey: the scene index). kitsoki tracks the
  latest in the run store (`embedScope`) and rides it on the refine as the
  `current_scene` slot, so the edit targets **the slide the operator is looking
  at**. This holds **even with no annotation**: a plain free-text refine typed in
  the chat carries `embedScope` as a `current_scene` *supplement slot* on the turn
  (`sendText` → the `turn` RPC's `slots` → `WithTurnSupplements` →
  `orchestrator.WithSupplementSlots`, gap-filling only so the router's
  classification wins). So "make the title bolder" lands on the viewed slide
  without pointing at anything. `ref` — the opaque element id (slidey: `<scene>/<field>`), turned into a
  `semantic_element` anchor (same wire shape as a sidecar pick, via
  `serializeAnchor`), so it flows through the identical anchor pipeline.

| side | where |
|---|---|
| **host (kitsoki)** | `lib/embedView.ts` (`parseEmbedView`/`parseEmbedPick`/`installEmbed*Listener`/`sendAnnotateMode`); `components/ArtifactAnnotator.vue` mounts the live deck for `mediaKind: "slidey"`, enables pick mode on load, and emits the anchor; `stores/run.ts` tracks `embedScope`; `components/ViewElement.vue` rides `current_scene` on the dispatch. |
| **producer (slidey)** | **the reference plugin** — `~/code/slidey/web/useDeck.js` posts `embed:view` on every scene render; `~/code/slidey/web/embed-annotate.js` discovers pickable blocks **straight from the rendered layout** — every *revealed* `.reveal` block under the active scene region (the deck's own structural/animation units), so coverage tracks the templates automatically (every scene type, every meaningful block) and follows the in-scene reveal transitions. It draws pick markers over those real elements and posts `embed:pick`; rebuilt on `slidey:scene-changed`/resize; wired in `web/components/App.vue`. A template overrides the id-derived `ref`/`label` per block with optional `data-embed-field`/`data-embed-label` attributes (e.g. the image frame edits `src`). |

kitsoki knows **nothing** about slidey: a second producer (a notebook, a CAD
viewer) becomes annotatable by implementing the same three messages. The
slidey-specific resolution — which scene the `scope` names, which field the `ref`
edits, and the **gate** that rejects an edit straying off the viewed slide — lives
entirely in the *story* (`stories/slidey-edit/scripts/{resolve_scene,gate_edited_scene}.star`),
not in kitsoki core.

> **Gotcha (story authors):** a `when:` guard on the *same* effect item as an
> `invoke:` silently skips the invoke. Keep the guard off invoke effects (the
> scene gate is reached only on the success path, so it needs none).

> **Gotcha (story authors):** `host.starlark.run` does **not** expr-evaluate its
> `inputs:` — the machine resolves each value first, and only a string wrapping
> the whole value in `{{ }}` is evaluated (a sole `{{ expr }}` preserves the typed
> value, so an `int` stays an `int`). A **bare** `world.foo` reaches the script as
> the literal string `"world.foo"`, which silently breaks resolution — the symptom
> that "the deck never shows the edits": resolve_scene looks up a deck literally
> named `world.deck.spec_path` (not found → `scene_index -1` → reviser told to make
> no change) and the gate gets a string `scene_index` → `expected int, got string`
> → `on_error` → the rerender never fires. Always template these:
> `spec_path: "{{ world.deck.spec_path }}"`. The same applies to a value a
> transition-effect script needs from world — pass it as a resolved input, not via
> `ctx.world` (which is a stale snapshot inside a transition effect). This is now
> caught **at load**: `validateStarlarkEffects` rejects a bare-expression `inputs:`
> value (and a literal that can't satisfy its declared sidecar type), so
> `kitsoki validate <app.yaml>` — or any run/flow load — fails fast with a
> "did you mean `{{ … }}`?" message instead of a silent runtime no-op. `kitsoki
> trace --turn <n>` also prints each host call's resolved inputs + outputs, so an
> unevaluated input shows verbatim there. Regression: `stories/slidey-edit/flows/refine_resolves_viewed_scene.yaml`
> + `internal/app/loader_starlark_inputs_test.go`.

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

A story for creating/editing slidey presentations end-to-end. A **new** deck goes
`drafting` (author a spec) → `rendering`; editing an **existing** deck
(`edit kitsoki-pitch`) skips straight to `rendering` with **no authoring agent**.
Then `rendering` (`host.slidey.render --format html` → a self-contained interactive
deck) → `reviewing` (the live deck as media + Annotate) → `refining` → `reviewing`.

`refining` is where slide-correctness is enforced:

1. `resolve_scene.star` picks the target slide — the **viewed** scene
   (`current_scene`, from the deck's `embed:view`) over the picked anchor's ref;
   with no signal it returns `-1` rather than guessing — and hands the reviser that
   scene's current JSON.
2. the reviser edits the exact element the operator pointed at (the live
   `embed:pick` ref).
3. `gate_edited_scene.star` rejects any edit that strayed to another slide, so the
   reply never falsely reads "done".

A deterministic flow + cassette + baked artifacts make it no-LLM
(`go run ./cmd/kitsoki test flows stories/slidey-edit/app.yaml`, incl.
`refine_wrong_slide_gate.yaml`). The scene scripts are unit-tested against the real
35-scene `kitsoki-pitch` deck (`stories/slidey-edit/scripts/scene_gate_scripts_test.go`).
See the [embed annotation protocol](#the-embed-protocol-live-producer-surfaces) for
the live-pick mechanism.
