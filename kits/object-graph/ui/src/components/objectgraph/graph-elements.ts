// ObjectGraph -> Cytoscape element/style conversion shared by every
// GraphView.vue instance (the full-graph view and each object's inline
// relationship graph).
import type cytoscape from "cytoscape";
import type { ObjectGraph, ObjectGraphNode } from "../../objectgraph-types.js";
import { edgeLabel, lifecycleBucket, typeLabel } from "./catalog-model.js";

export interface ToElementsOptions {
  // When set, nodes get a `parent` pointing at their layer id so compound
  // layouts (fcose, cola-compound) can draw a layer boundary around them.
  // The grouping is presentation-only and pluggable — the hardcoded
  // type-layer taxonomy (catalog-model's `nodeLayerId`) and the data-driven
  // area grouping (`buildAreaGroupResolver`) are both just functions of this
  // shape.
  groupByLayer?: (node: ObjectGraphNode) => string;
  // Friendly label for a group id produced by groupByLayer above. Defaults
  // to the raw id (fine for the short type-layer ids; area grouping passes
  // `areaGroupLabel` to show the area's title instead of its node id).
  groupLabel?: (groupId: string) => string;
}

export function toElements(graph: ObjectGraph, opts: ToElementsOptions = {}): cytoscape.ElementDefinition[] {
  const groups = opts.groupByLayer ? new Set(graph.nodes.map((n) => opts.groupByLayer!(n))) : null;
  const groupNodes: cytoscape.ElementDefinition[] = groups
    ? [...groups].map((id) => ({
        data: { id: `layer:${id}`, label: opts.groupLabel ? opts.groupLabel(id) : id, isLayer: true },
      }))
    : [];

  const nodes: cytoscape.ElementDefinition[] = graph.nodes.map((node) => ({
    data: {
      id: node.id,
      label: node.label,
      kind: node.kind,
      status: node.status ?? "",
      lifecycle: lifecycleBucket(node),
      typeLabel: typeLabel(node.kind),
      // diff mode (runstatus.objectgraph.diff): "" when the graph wasn't
      // loaded in diff mode, otherwise one of added/modified/removed/
      // unchanged (internal/graph.GapKind) — styled below.
      diffKind: (node.attrs?.diff_kind as string) ?? "",
      parent: opts.groupByLayer ? `layer:${opts.groupByLayer(node)}` : undefined,
    },
  }));

  const edges: cytoscape.ElementDefinition[] = graph.edges.map((edge) => ({
    data: {
      id: edge.id,
      source: edge.source,
      target: edge.target,
      label: edgeLabel(edge.kind),
      kind: edge.kind,
    },
  }));

  return [...groupNodes, ...nodes, ...edges];
}

// One-hop neighborhood of `nodeId`: the node itself plus everything it
// points to and everything that points to it.
export function neighborhood(graph: ObjectGraph, nodeId: string): ObjectGraph {
  const neighborIds = new Set<string>([nodeId]);
  const edges = graph.edges.filter((edge) => {
    const touches = edge.source === nodeId || edge.target === nodeId;
    if (touches) {
      neighborIds.add(edge.source);
      neighborIds.add(edge.target);
    }
    return touches;
  });
  const nodes = graph.nodes.filter((node) => neighborIds.has(node.id));
  return { ...graph, nodes, edges };
}

// Exported so GraphView's legend can render the exact same swatches used by
// the node style below — one source of truth for the color coding.
export const LIFECYCLE_COLORS: Record<string, string> = {
  available: "#2f7a4f",
  active: "#1d6fb8",
  proof: "#7a4fd6",
  roadmap: "#c98a1a",
  candidate: "#7a8896",
};

// Diff mode's added/modified/removed border language — the same colors
// WorldDiffViewer.vue uses for world-state keys (--k-success/--k-warning/
// --k-error), reused here so "what changed" reads the same everywhere in
// the viewer rather than inventing a second color vocabulary.
export const DIFF_COLORS: Record<string, string> = {
  added: "#22c55e",
  modified: "#f59e0b",
  removed: "#ef4444",
};

export const cytoscapeStyle: cytoscape.StylesheetJson = [
  {
    selector: "node",
    style: {
      "background-color": (el: cytoscape.NodeSingular) => LIFECYCLE_COLORS[el.data("lifecycle")] ?? "#5b6b7a",
      label: "data(label)",
      color: "#1c2530",
      "font-size": 10,
      "text-valign": "bottom",
      "text-margin-y": 6,
      width: 22,
      height: 22,
      "border-width": 2,
      "border-color": "#ffffff",
      "text-wrap": "wrap",
      "text-max-width": "110px",
    },
  },
  {
    selector: "node[isLayer]",
    style: {
      "background-opacity": 0.06,
      "background-color": "#28405c",
      "border-color": "#b9c6d4",
      "border-width": 1,
      label: "data(label)",
      "text-valign": "top",
      "text-halign": "center",
      "font-weight": 700,
      "font-size": 11,
      color: "#46534d",
    },
  },
  {
    selector: "node.focused",
    style: {
      "border-color": "#e0463c",
      "border-width": 4,
      width: 30,
      height: 30,
      "font-weight": 700,
    },
  },
  {
    // Listed after node.focused so a gapped node's added/modified/removed
    // color always shows, even while focused (Cytoscape resolves the same
    // property from the LAST matching rule in the stylesheet array) — focus
    // still reads via the width/height/font-weight bump above, just not a
    // second competing border color.
    selector: 'node[diffKind = "added"]',
    style: { "border-color": DIFF_COLORS.added, "border-width": 3 },
  },
  {
    selector: 'node[diffKind = "modified"]',
    style: { "border-color": DIFF_COLORS.modified, "border-width": 3 },
  },
  {
    selector: 'node[diffKind = "removed"]',
    style: { "border-color": DIFF_COLORS.removed, "border-width": 3, "border-style": "dashed", "background-opacity": 0.5 },
  },
  {
    selector: "edge",
    style: {
      width: 1.4,
      "line-color": "#b9c6d4",
      "target-arrow-color": "#b9c6d4",
      "target-arrow-shape": "triangle",
      "curve-style": "bezier",
      label: "data(label)",
      "font-size": 8,
      color: "#667",
      "text-rotation": "autorotate",
      "text-background-color": "#fff",
      "text-background-opacity": 0.85,
      "text-background-padding": "1px",
    },
  },
];
