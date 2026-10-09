# Object-graph viewer: declutter the layer bar, add diff mode, local persona QA

**Status:** draft, child of [`project-object-graph.md`](../project-object-graph.md).
Graph node: [`proposal-object-graph-ui-declutter-and-diff-mode`](seed-objects.yaml)
(`child_of: proposal-project-object-graph`).

## Why

The catalog viewer (`tools/runstatus/src/components/objectgraph/CatalogPanel.vue`) is
the epic's own primary surface, and it has become the thing the epic most needs to fix:

1. **The layer bar is busy.** `.layer-map` renders one card per layer (Actors, Site,
   Capabilities, Delta, Proof), each duplicating a full type-count chip row and a
   lifecycle status-dot row already shown again in the object picker just below —
   `catalog.css`'s `overflow-x: auto` on a five-item row is a symptom, not the cause.
2. **There is no diff mode.** `internal/graph/diff.go` (W4.0) has computed
   added/modified/removed gaps between a current and desired catalog since the
   roadmap-as-delta slice shipped; nothing in the viewer renders that gap at node
   granularity. A reviewer today has to read two YAML files by hand to see what a
   proposal changes.
3. **No local, persona-lensed usability signal.** `kitsoki-ui-review` reviews the web
   UI heuristically (one generic Nielsen pass); it has never been pointed at this page,
   and has no persona dimension despite the catalog already modeling five personas
   (`persona-*` nodes, matching `tools/product-journey/personas.json`).

## What changes

- **Diff mode.** `internal/graph.DiffNodes` exposes the existing gap classification at
  node granularity (added/modified/removed), reused by `Diff()` so the two never drift.
  `LoadCatalogWithOverlay` builds a "desired" catalog from the shipped base file plus a
  small additive overlay (`seed-objects.overlay-*.yaml`) — this proposal's own overlay
  is the diff mode's first real exercise. A new `runstatus.objectgraph.diff` RPC serves
  the gapped graph to the viewer.
- **Diff mode UI.** `ObjectGraphPage.vue` gains an `?overlay=` param and an
  As-is/Proposed/Diff toggle; a diff panel generalizes `WorldDiffViewer.vue`'s
  added/removed/changed color language from world-state keys to graph nodes.
- **Decluttered layer bar.** `CatalogPanel.vue`'s `.layer-map` collapses to one line per
  layer (title + total count + a compact lifecycle summary); the per-type counts move to
  live only in the object picker's existing type-filter row instead of twice.
- **Local persona QA.** New `kitsoki-ui-review` tour steps for the object-graph route
  (current + diff mode); one review pass per `persona-*` node, design-intent generated
  from that node's `summary`/`risk_focus`. Everything lands under `.artifacts/ui-review/`
  and `.context/` — no GitHub filing, no upload, no scenario network calls.

## Graph nodes

See `seed-objects.yaml`: `proposal-object-graph-ui-declutter-and-diff-mode` and its
`creates_requirements` (`req-object-graph-layer-bar-no-scroll`,
`req-object-graph-diff-mode-visual-affordances`, `req-object-graph-persona-qa-stays-local`)
and `creates_use_cases` (`usecase-reviewer-scans-proposal-diff`).

## Non-goals

- Not a full changeset apply/merge implementation (epic slice 2) — the overlay loader is
  additive-only (this proposal has no modified/removed nodes to represent yet).
- Not a rewrite of the full-graph Cytoscape modal's layout — diff mode adds node coloring
  to the existing `GraphView.vue`, it does not change its layout engine.
