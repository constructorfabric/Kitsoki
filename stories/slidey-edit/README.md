# slidey-edit — author or revise a deck, render it, annotate the frame, refine the scene

A standalone PoC story for the **unified artifact annotation** feature: it
drives the create/edit → render → review → refine loop for a slidey deck, where the
review feedback is **location-tied**. A reviewer points at an exact place on the
rendered deck frame — a named scene element, a drawn region, or a single point —
and that **AnnotationAnchor** flows into a refine pass that edits the precise
scene the anchor targets.

It is the v2+ generalization of `spatial-oracle` (rrweb-only) to arbitrary
rendered artifacts (png / mp4 / rrweb / html / slidey), demonstrated end-to-end
on a slidey deck. It is the slidey cousin of `stories/mockup-video/` (walkthrough
video studio) — same producer (`host.slidey.render`), same media-review shape,
but with an **anchored** refine instead of a chapter/timecode flag.

```
kitsoki run stories/slidey-edit/app.yaml
```

## Rooms

```
idle ──start/edit──▶ preparing_workspace ──accept──▶ drafting ──accept──▶ rendering ──(auto)──▶ reviewing
                    (managed capsule clone)         (agent writes/edits deck)       (slidey → static HTML
                                                                                     + .semantic sidecar)
          │
          └──edit_existing/edit <deck>──▶ loading_existing ──accept──▶ rendering
                                          (resolve deck path)
                                                                                               │        revise
        ┌──────────────────────────────────────────────────────────────────────────────────────┤           │
        │ accept→done · rerender→rendering · quit→@exit:abandoned                                │           ▼
        │                                                                                        │   drafting (deck-wide edit)
        │                                                                                        ▼
        └──────────────────────────── rendering ◀──(re-render before/after)──────────────── refining
                                                                                 (agent edits the scene the
                                                                                  anchor points at)
```

| Room | Split | What it does |
|---|---|---|
| `idle` | deterministic | Choose a fresh draft with `start`, or pass an existing slidey JSON spec with `edit_existing spec_path=...`. |
| `preparing_workspace` | deterministic | Creates or reuses a session-scoped managed clone-backed capsule workspace through `iface.workspace.create`. Story state keeps repo-relative deck paths, while file-touching hosts receive the concrete `workdir` path. |
| `loading_existing` | deterministic | Resolves a bare deck name or path via `resolve_deck` before rendering. This keeps `edit kitsoki-pitch` deterministic while ensuring `host.slidey.render` receives the resolved spec path, not the baked sample. |
| `drafting` | interpretive | ONE `host.agent.task` (`drafter`) authors/edits the deck JSON. Existing input lives in `world.source_deck`; `world.deck` is the output/cache. Workspace-jailed, `once:`. |
| `rendering` | deterministic | `host.slidey.render` (`format: html`, `slidey bundle`) → a self-contained **static HTML deck** and, when available, a `.semantic.json` sidecar. Render failures stop in a visible troubleshooting checkpoint before any repair agent runs; deck-shaped failures can be repaired one explicit `troubleshoot` turn at a time, while external renderer/toolchain failures surface directly. `host.artifacts_dir` emits the deck handle and co-locates any sidecar/poster companions. Auto-advances on success. |
| `reviewing` | deterministic + interactive | `media(deck_handle)` embeds the static HTML deck inline, links to the direct artifact, and seeds a baked `semantic_element` annotation. Checkpoint: accept / revise / refine / rerender / quit. `revise` reopens `drafting` for whole-deck edits (add/remove/reorder scenes, template/layout swaps, format/theme updates), then re-renders. |
| `refining` | interpretive | ONE `host.agent.task` (`reviser`) consumes the annotation (`{{ args.visual.anchor }}` + the explicit `annotation` arg) and edits the targeted scene, then re-renders the before/after. |
| `done` | gallery | Final deck media + the annotations addressed per cycle. |

## The annotation contract

The reviewing → refining handoff carries an **annotation bundle** in
`world.annotation`:

```yaml
annotation:
  anchor:                       # the AnnotationAnchor union
    kind: semantic_element      # semantic_element | region | dom_node
    semantic_element:           # the canonical semantic_element target
      plugin: slidey            # the producer (kitsoki stays plugin-agnostic)
      ref: "1/card_0"           # OPAQUE "<sceneIndex>/<el>" string, round-tripped verbatim
      bbox: [140, 518, 535, 114]  # the element's box on the rendered frame
    label: "Scene 1 · card 0"
  instruction: "Make the semantic_element card stand out…"
  frame_handle: "slidey-edit#1"
```

This is the exact `semantic_element` target shape a real pick serializes (see
`.context/unified-artifact-annotation.md` and `host.AnchorSemanticElementTarget`):
`ref` is an OPAQUE string of the form `<sceneIndex>/<el>` (NOT an object) that
kitsoki round-trips verbatim back to slidey; slidey splits it on `/` to recover
the scene/element. It mirrors the `args.visual.anchor` shape a capturing surface
attaches (the generalized `VisualAmbient`). The refine prompt prefers the live
`args.visual.anchor` when present and falls back to the explicit `annotation`
arg. An inline `refine feedback="…"` overrides the *instruction* while keeping
the *anchor* (where the operator pointed).

The `.semantic.json` sidecar (`baked/deck.semantic.json`) is the **real**
canonical output of rendering `baked/deck.json` through slidey: the producer's
map of `{ ref, label, selector, bbox:[x,y,w,h], t_ms }` per addressable element,
so a `region` anchor resolves to the dominant element it overlaps. The seeded
demo anchor (`1/card_0`, bbox `[140,518,535,114]`) is a genuine element from this
sidecar, and `baked/deck.poster.png` is a real rendered frame, so the annotation
overlay's box aligns with the pixels.

## Baked demo artifacts

`baked/` holds a tiny pre-rendered deck so rendering/reviewing show **real
media** without invoking slidey live (and so `kitsoki tour`/web can drive the
loop without authoring — the *tour needs a baked world* lesson):

- `deck.json` — the deck spec: 3 slidey scenes (`title` → `cards` → `narrative`)
  whose types emit semantic elements. Rendered at 1920×1080.
- `deck.html` — the REAL self-contained static HTML deck, the output of
  `slidey bundle baked/deck.json baked/deck.html` (one file, opens straight off
  disk; no server, no ffmpeg, no narration render). This replaced the old
  `deck.mp4` — rendering to interactive HTML is far cheaper than a full video
  render, and the location-tied review works the same way (below). **Not
  committed** (it inlines the whole slidey SPA, ~4 MB — gitignored via
  `baked/.gitignore`); regenerate it for a flow/tour run with:
  `slidey bundle stories/slidey-edit/baked/deck.json stories/slidey-edit/baked/deck.html`.
- `deck.poster.png` — a REAL rendered frame (scene 1, the "One anchor union"
  cards row — where the seeded anchor lives), so overlay bboxes align. The deck
  is emitted as media kind `slideshow`: the room embeds the static HTML deck, and
  the annotation overlay uses this poster still when a semantic sidecar is
  available.
- `deck.semantic.json` — the canonical semantic sidecar, the REAL output of
  rendering `deck.json` through slidey (real `ref`s + real `bbox`es). Paired to
  the artifact by extension-swap (`deck.html` → `deck.semantic.json`), so the
  semantic-location mechanism is independent of the rendered format.

The render host calls are **stubbed** in the flows/cassette to point at these
files; `host.artifacts_dir` runs for real under `kitsoki web --flow` so the
handle resolves through the journal.

## Deterministic, no-LLM testing

```
kitsoki test flows stories/slidey-edit/app.yaml
```

| Fixture | Covers |
|---|---|
| `flows/happy_path.yaml` | idle → drafting → rendering → reviewing → done. |
| `flows/edit_existing.yaml` | `edit_existing spec_path=...` preserves the selected input in `source_deck`, runs the drafter, then renders/reviews the edited deck. |
| `flows/render_toolchain_error_no_agent.yaml` | `edit kitsoki-pitch` resolves the existing deck, then a Node/slidey toolchain failure surfaces without dispatching `host.agent.task`. |
| `flows/refine_from_anchor.yaml` | the **location-tied loop**: a baked `semantic_element` anchor flows into refine; asserts the anchor reached the refine task + the addressed record. |
| `flows/refine_inline_override.yaml` | inline `refine feedback="…"` overrides the instruction, anchor unchanged. |
| `flows/refine_budget_exhaust.yaml` | refine refused at budget. |
| `flows/revise_from_review.yaml` | open existing deck in `reviewing`, run `revise`, then assert re-render. |
| `flows/quit_at_review.yaml` | `@exit:abandoned`. |
| `flows/demo_web.yaml` | web/tour entry fixture (real baked media, stubbed agent/render). |

`cassettes/deck_review.cassette.yaml` supplies the same no-LLM posture for the
web/tour surface (matched by handler). Pair with `flows/demo_web.yaml`:

```
kitsoki web --flow stories/slidey-edit/flows/demo_web.yaml \
  --host-cassette stories/slidey-edit/cassettes/deck_review.cassette.yaml
```

## Exits

| Exit | `requires:` | When |
|---|---|---|
| `done` | `deck_handle` | accepted at reviewing/done — a real rendered deck exists. |
| `abandoned` | — | quit at idle / drafting / reviewing. |
