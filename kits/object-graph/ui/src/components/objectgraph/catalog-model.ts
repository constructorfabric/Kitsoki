// Shared model helpers for the catalog (list/detail) projection of the
// project object graph. Ported from the deleted engine SPA's
// tools/runstatus/src/components/objectgraph/catalog-model.ts (S5 — see
// .context/kits-implementation-plan.md D3/D4) and adapted to read the
// kitsoki.graph/v1 wire shape (ObjectGraph, served by
// kit.object-graph.graph.project) instead of runstatus.objectgraph.load —
// the per-type scalars live in `node.attrs.fields` (internal/host's
// objectCatalogGraph puts the substrate's Node.Fields there verbatim).
import type { ObjectGraph, ObjectGraphNode } from "../../objectgraph-types.js";

export interface Layer {
  id: string;
  title: string;
  short: string;
  description: string;
  types: string[];
}

// `layers` is `let`, not `const`: it starts as this file's bundled default
// (identical to what the deleted engine SPA hardcoded) so the viewer works
// even before the first RPC round-trip, but ObjectGraphPage.vue overwrites
// it via setLayers() on mount from kit.object-graph.graph.presentation — the
// starlark-served endpoint (scripts/presentation.star, D2.1) that is now the
// real source of truth for this taxonomy. A kit consumer that never calls
// setLayers (e.g. this file's own unit tests) still gets a sane default.
export function setLayers(next: Layer[]): void {
  if (next.length > 0) layers = next;
}

export let layers: Layer[] = [
  {
    id: "actors",
    title: "Actors, agents, and responsibilities",
    short: "Actors",
    description:
      "Who uses, owns, plans, implements, reviews, or automates the work represented in the graph — and the concrete personas that drive persona-based QA.",
    types: ["actor", "agent", "persona"],
  },
  {
    id: "site",
    title: "Public product site",
    short: "Site",
    description:
      "Editable public pages generated from graph-backed capability copy, demo media, and consistency rules.",
    types: ["site-page"],
  },
  {
    id: "capabilities",
    title: "Product capabilities and requirements",
    short: "Capabilities",
    description:
      "What exists or is desired: product features, requirements, and the user scenarios they support.",
    types: ["feature", "requirement", "use-case"],
  },
  {
    id: "delta",
    title: "Change and roadmap work",
    short: "Delta",
    description: "How the project moves from current state to desired state: proposals and work items.",
    types: ["proposal", "change"],
  },
  {
    id: "proof",
    title: "Implementation and proof",
    short: "Proof",
    description:
      "Where shipped capabilities live and what verifies them: code, stories, demos, fixtures, and evidence.",
    types: ["evidence", "implementation"],
  },
];

export function nodeFields(node: ObjectGraphNode): Record<string, unknown> {
  return (node.attrs?.fields as Record<string, unknown>) ?? {};
}

export function nodeVisibility(node: ObjectGraphNode): string {
  return (node.attrs?.visibility as string) ?? "internal";
}

// Diff mode's per-node classification (runstatus.objectgraph.diff,
// internal/graph.GapKind) — "" when the graph wasn't loaded in diff mode.
export type DiffKind = "added" | "modified" | "removed" | "unchanged" | "";

export function diffKind(node: ObjectGraphNode): DiffKind {
  return (node.attrs?.diff_kind as DiffKind) ?? "";
}

export function diffKindLabel(kind: DiffKind): string {
  switch (kind) {
    case "added":
      return "Added";
    case "modified":
      return "Modified";
    case "removed":
      return "Removed";
    default:
      return "";
  }
}

export function nodeSources(node: ObjectGraphNode): string[] {
  return (node.attrs?.sources as string[]) ?? [];
}

export function nodeTypeChain(node: ObjectGraphNode): string[] {
  return (node.attrs?.type_chain as string[]) ?? [node.kind];
}

export function nodeLayerId(node: ObjectGraphNode): string {
  return layers.find((layer) => layer.types.includes(node.kind))?.id ?? "capabilities";
}

// --- Data-driven "group by area" mode (design doc §4.4) -------------------
//
// Unlike the hardcoded type layers above, this grouping is computed from
// whatever `area` nodes and `in_area`/`part_of` edges the catalog actually
// has — it does not hardcode area ids. A node's primary area is:
//   - feature: the first `in_area` edge target (convention — see the
//     `feature` type_registry summary in seed-objects.yaml: the first entry
//     is primary for tree-shaped rollups; a feature listing more than one is
//     spanning by convention, not schema).
//   - area: its `part_of` parent (areas nest under their parent's bucket),
//     or the root bucket if it has none.
//   - everything else: one hop to a feature via the type's own obvious edge
//     (e.g. requirement.required_by, evidence.demonstrates), then that
//     feature's primary area — or the unassigned bucket if no such hop
//     exists or resolves.
export const UNASSIGNED_AREA_ID = "unassigned-area";
export const AREA_ROOT_ID = "area-root";

// The one-hop edge (by kind) each non-feature, non-area type uses to reach a
// feature. Kept to the "obvious" hops named in the design doc — anything
// without one falls into the unassigned bucket rather than guessing.
const FEATURE_HOP_EDGE: Record<string, string> = {
  requirement: "required_by",
  "use-case": "exercises",
  proposal: "proposes",
  evidence: "demonstrates",
  implementation: "implements",
  change: "implements",
  "site-page": "presents",
  actor: "uses",
  agent: "uses",
};

export function hasAreaNodes(graph: ObjectGraph): boolean {
  return graph.nodes.some((node) => node.kind === "area");
}

// Builds a per-graph node -> primary-area-id resolver. Pass the returned
// function as `groupByLayer` (graph-elements.ts's compound-parent mechanism
// doesn't care whether the grouping key is a type layer or an area id).
export function buildAreaGroupResolver(graph: ObjectGraph): (node: ObjectGraphNode) => string {
  const targetsByEdgeKind = new Map<string, Map<string, string[]>>();
  for (const edge of graph.edges) {
    let bySource = targetsByEdgeKind.get(edge.kind);
    if (!bySource) {
      bySource = new Map();
      targetsByEdgeKind.set(edge.kind, bySource);
    }
    const targets = bySource.get(edge.source);
    if (targets) targets.push(edge.target);
    else bySource.set(edge.source, [edge.target]);
  }
  const firstTarget = (kind: string, sourceId: string): string | undefined =>
    targetsByEdgeKind.get(kind)?.get(sourceId)?.[0];

  const cache = new Map<string, string>();

  function resolve(node: ObjectGraphNode, visiting: Set<string>): string {
    const cached = cache.get(node.id);
    if (cached) return cached;
    if (visiting.has(node.id)) return UNASSIGNED_AREA_ID; // defensive cycle guard
    visiting.add(node.id);

    let result: string;
    if (node.kind === "area") {
      result = firstTarget("part_of", node.id) ?? AREA_ROOT_ID;
    } else if (node.kind === "feature") {
      result = firstTarget("in_area", node.id) ?? UNASSIGNED_AREA_ID;
    } else {
      const hopEdge = FEATURE_HOP_EDGE[node.kind];
      const featureId = hopEdge ? firstTarget(hopEdge, node.id) : undefined;
      result = (featureId && firstTarget("in_area", featureId)) || UNASSIGNED_AREA_ID;
    }

    cache.set(node.id, result);
    return result;
  }

  return (node: ObjectGraphNode) => resolve(node, new Set());
}

// Friendly label for a group id produced by buildAreaGroupResolver: the
// area node's own title when the id is a real area, otherwise the fixed
// bucket labels.
export function areaGroupLabel(graph: ObjectGraph): (groupId: string) => string {
  const nodeById = new Map(graph.nodes.map((node) => [node.id, node]));
  return (groupId: string): string => {
    if (groupId === UNASSIGNED_AREA_ID) return "Unassigned";
    if (groupId === AREA_ROOT_ID) return "Areas";
    return nodeById.get(groupId)?.label ?? groupId;
  };
}

export function typeLabel(type: string): string {
  const labels: Record<string, string> = {
    actor: "Actors",
    agent: "Agents",
    persona: "Personas",
    "site-page": "Site pages",
    feature: "Features",
    requirement: "Requirements",
    "use-case": "Use cases",
    proposal: "Proposals",
    evidence: "Evidence",
    implementation: "Implementations",
    change: "Work items",
  };
  return labels[type] ?? type;
}

export function edgeLabel(edge: string): string {
  const labels: Record<string, string> = {
    actor: "actor",
    uses: "uses",
    owns: "owns",
    participates_in: "participates in",
    assigned_changes: "assigned changes",
    owned_by: "owned by",
    assigned_to: "assigned to",
    requirements: "must satisfy",
    use_cases: "used by",
    evidence: "proved by",
    proposed_by: "proposed by",
    implemented_by: "implemented by",
    required_by: "required by",
    motivated_by: "motivated by",
    verified_by: "verified by",
    exercises: "exercises",
    acceptance: "acceptance",
    demonstrates: "demonstrates",
    verifies: "verifies",
    implements: "implements",
    creates_requirements: "creates requirements",
    creates_use_cases: "creates use cases",
    proposes: "proposes",
    decomposes_to: "breaks into",
    satisfies: "satisfies",
    persona_of: "persona of",
    qa_evidence: "QA evidence",
    from_persona: "from persona",
    in_area: "in area",
    part_of: "part of",
    owns_area: "owns area",
    targets: "targets",
    includes: "includes",
    proposals: "proposals",
  };
  return labels[edge] ?? edge.split("_").join(" ");
}

export function typeOrder(type: string): number {
  const index = [
    "actor",
    "agent",
    "persona",
    "site-page",
    "feature",
    "requirement",
    "use-case",
    "proposal",
    "change",
    "evidence",
    "implementation",
  ].indexOf(type);
  return index === -1 ? 99 : index;
}

export function lifecycleBucket(node: ObjectGraphNode): string {
  const status = node.status ?? "";
  if (["shipped", "satisfied", "supported"].includes(status)) return "available";
  if (status === "active") return "active";
  if (status === "current") return "proof";
  if (status === "proposed") return "roadmap";
  return "candidate";
}

export function lifecycleLabel(bucket: string): string {
  const labels: Record<string, string> = {
    available: "Available",
    active: "Active",
    proof: "Proof",
    roadmap: "Roadmap",
    candidate: "Candidate",
  };
  return labels[bucket] ?? bucket;
}

export function nodeText(node: ObjectGraphNode): string {
  const fields = nodeFields(node);
  return (
    (fields.summary as string) ??
    (fields.statement as string) ??
    (fields.desired_outcome as string) ??
    (fields.goal as string) ??
    (fields.rationale as string) ??
    "No description yet."
  );
}

export interface RelationshipGroup {
  name: string;
  label: string;
  nodes: ObjectGraphNode[];
}

// Outgoing/incoming are computed from the wire graph's edge list (edge.kind
// is the edge field name, e.g. "presents", "evidence", "persona_of" — see
// internal/app/graph's ObjectCatalogGraph) rather than from a node's own
// `edges` map, since the wire shape flattens all edges to one list shared
// with story-room graphs.
export function outgoingGroups(graph: ObjectGraph, nodeId: string): RelationshipGroup[] {
  const nodeById = new Map(graph.nodes.map((n) => [n.id, n]));
  const grouped = new Map<string, ObjectGraphNode[]>();
  for (const edge of graph.edges) {
    if (edge.source !== nodeId) continue;
    const target = nodeById.get(edge.target);
    if (!target) continue;
    if (!grouped.has(edge.kind)) grouped.set(edge.kind, []);
    grouped.get(edge.kind)?.push(target);
  }
  return [...grouped.entries()].map(([name, nodes]) => ({ name, label: edgeLabel(name), nodes }));
}

export function incomingGroups(graph: ObjectGraph, nodeId: string): RelationshipGroup[] {
  const nodeById = new Map(graph.nodes.map((n) => [n.id, n]));
  const grouped = new Map<string, ObjectGraphNode[]>();
  for (const edge of graph.edges) {
    if (edge.target !== nodeId) continue;
    const source = nodeById.get(edge.source);
    if (!source) continue;
    if (!grouped.has(edge.kind)) grouped.set(edge.kind, []);
    grouped.get(edge.kind)?.push(source);
  }
  return [...grouped.entries()].map(([name, nodes]) => ({ name, label: edgeLabel(name), nodes }));
}

export function edgeTargetsByKind(graph: ObjectGraph, nodeId: string, kind: string): ObjectGraphNode[] {
  const nodeById = new Map(graph.nodes.map((n) => [n.id, n]));
  return graph.edges
    .filter((edge) => edge.source === nodeId && edge.kind === kind)
    .map((edge) => nodeById.get(edge.target))
    .filter((node): node is ObjectGraphNode => Boolean(node));
}
