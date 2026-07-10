/**
 * Unit tests for the data-driven "group by area" mode
 * (src/components/objectgraph/catalog-model.ts). Builds small synthetic
 * ObjectGraph fixtures rather than loading the real seed catalog — this is
 * presentation logic over the kitsoki.graph/v1 wire shape, so it doesn't need
 * a live server, an LLM, or the full catalog to exercise correctly.
 */
import { describe, it, expect } from "vitest";
import {
  AREA_ROOT_ID,
  UNASSIGNED_AREA_ID,
  areaGroupLabel,
  buildAreaGroupResolver,
  hasAreaNodes,
} from "../../src/components/objectgraph/catalog-model.js";
import type { ObjectGraph, ObjectGraphEdge, ObjectGraphNode } from "../../src/objectgraph-types.js";

function node(id: string, kind: string, label = id): ObjectGraphNode {
  return { id, kind, label, ref: { kind, ref: id } };
}

function edge(kind: string, source: string, target: string, i = 0): ObjectGraphEdge {
  return { id: `${source}-${kind}-${target}-${i}`, kind, source, target };
}

function graph(nodes: ObjectGraphNode[], edges: ObjectGraphEdge[]): ObjectGraph {
  return { schema: "kitsoki.graph/v1", graph_id: "test", kind: "object-graph", directed: true, cyclic: false, nodes, edges };
}

describe("hasAreaNodes", () => {
  it("is false for a catalog with no area nodes", () => {
    const g = graph([node("feature-a", "feature")], []);
    expect(hasAreaNodes(g)).toBe(false);
  });

  it("is true once at least one area node exists", () => {
    const g = graph([node("area-web", "area"), node("feature-a", "feature")], []);
    expect(hasAreaNodes(g)).toBe(true);
  });
});

describe("buildAreaGroupResolver", () => {
  it("resolves a feature to the first in_area edge's target (primary-area convention)", () => {
    const g = graph(
      [node("area-web", "area"), node("area-dev", "area"), node("feature-a", "feature")],
      [edge("in_area", "feature-a", "area-web", 0), edge("in_area", "feature-a", "area-dev", 1)],
    );
    const resolve = buildAreaGroupResolver(g);
    expect(resolve(g.nodes.find((n) => n.id === "feature-a")!)).toBe("area-web");
  });

  it("falls back to unassigned for a feature with no in_area edge", () => {
    const g = graph([node("feature-a", "feature")], []);
    const resolve = buildAreaGroupResolver(g);
    expect(resolve(g.nodes[0])).toBe(UNASSIGNED_AREA_ID);
  });

  it("nests an area node under its part_of parent", () => {
    const g = graph(
      [node("area-root-area", "area"), node("area-child", "area")],
      [edge("part_of", "area-child", "area-root-area")],
    );
    const resolve = buildAreaGroupResolver(g);
    expect(resolve(g.nodes.find((n) => n.id === "area-child")!)).toBe("area-root-area");
  });

  it("buckets a top-level area (no part_of) into the root bucket", () => {
    const g = graph([node("area-web", "area")], []);
    const resolve = buildAreaGroupResolver(g);
    expect(resolve(g.nodes[0])).toBe(AREA_ROOT_ID);
  });

  it("walks one hop through a feature for other node types", () => {
    const g = graph(
      [node("area-web", "area"), node("feature-a", "feature"), node("req-1", "requirement")],
      [edge("in_area", "feature-a", "area-web"), edge("required_by", "req-1", "feature-a")],
    );
    const resolve = buildAreaGroupResolver(g);
    expect(resolve(g.nodes.find((n) => n.id === "req-1")!)).toBe("area-web");
  });

  it("buckets a hop-type node as unassigned when its feature has no area", () => {
    const g = graph(
      [node("feature-a", "feature"), node("req-1", "requirement")],
      [edge("required_by", "req-1", "feature-a")],
    );
    const resolve = buildAreaGroupResolver(g);
    expect(resolve(g.nodes.find((n) => n.id === "req-1")!)).toBe(UNASSIGNED_AREA_ID);
  });

  it("buckets a type with no known feature hop as unassigned", () => {
    const g = graph([node("persona-1", "persona")], []);
    const resolve = buildAreaGroupResolver(g);
    expect(resolve(g.nodes[0])).toBe(UNASSIGNED_AREA_ID);
  });
});

describe("areaGroupLabel", () => {
  it("labels a real area group with the area node's title", () => {
    const g = graph([node("area-web", "area", "Web experience")], []);
    expect(areaGroupLabel(g)("area-web")).toBe("Web experience");
  });

  it("labels the fixed unassigned and root buckets", () => {
    const g = graph([], []);
    expect(areaGroupLabel(g)(UNASSIGNED_AREA_ID)).toBe("Unassigned");
    expect(areaGroupLabel(g)(AREA_ROOT_ID)).toBe("Areas");
  });
});
