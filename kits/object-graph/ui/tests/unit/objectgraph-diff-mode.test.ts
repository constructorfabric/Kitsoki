/**
 * Unit tests for diff mode's presentation logic
 * (src/components/objectgraph/catalog-model.ts's diffKind/diffKindLabel).
 * Synthetic fixtures — this is presentation logic over the kitsoki.graph/v1
 * wire shape (runstatus.objectgraph.diff's diff_kind attr), no live server.
 */
import { describe, it, expect } from "vitest";
import { diffKind, diffKindLabel } from "../../src/components/objectgraph/catalog-model.js";
import type { ObjectGraphNode } from "../../src/objectgraph-types.js";

function node(id: string, diff_kind?: string): ObjectGraphNode {
  return { id, kind: "requirement", label: id, ref: { kind: "requirement", ref: id }, attrs: diff_kind ? { diff_kind } : {} };
}

describe("diffKind", () => {
  it("is empty for a node with no diff_kind attr (non-diff-mode load)", () => {
    expect(diffKind(node("req-a"))).toBe("");
  });

  it("reads added/modified/removed/unchanged straight from attrs.diff_kind", () => {
    expect(diffKind(node("req-a", "added"))).toBe("added");
    expect(diffKind(node("req-a", "modified"))).toBe("modified");
    expect(diffKind(node("req-a", "removed"))).toBe("removed");
    expect(diffKind(node("req-a", "unchanged"))).toBe("unchanged");
  });
});

describe("diffKindLabel", () => {
  it("titlecases the three badge-worthy kinds", () => {
    expect(diffKindLabel("added")).toBe("Added");
    expect(diffKindLabel("modified")).toBe("Modified");
    expect(diffKindLabel("removed")).toBe("Removed");
  });

  it("is empty for unchanged/empty (nothing to badge)", () => {
    expect(diffKindLabel("unchanged")).toBe("");
    expect(diffKindLabel("")).toBe("");
  });
});
