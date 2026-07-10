/**
 * Unit tests for src/lib/annotationAnchor.ts — the v2 normalizer that maps the
 * legacy spatial-oracle picker bundle (point/box/element) into the discriminated
 * AnnotationAnchor target union, plus the region builder, the back-compat
 * projection, and the semantic-sidecar name derivation. Pure functions, no DOM.
 */
import { describe, it, expect } from "vitest";
import {
  normalizeAnchor,
  regionToTarget,
  anchorToVisualBundle,
  serializeAnchor,
} from "../../src/lib/annotationAnchor.js";
import { semanticSidecarName } from "../../src/lib/semanticPlugins.js";
import type { ResolvedElement } from "../../src/lib/resolveElement.js";

const ELEM: ResolvedElement = {
  selector: '[data-testid="intent-btn-run"]',
  role: "button",
  text: "Run",
  bbox: { x: 100, y: 50, width: 80, height: 30 },
};

describe("normalizeAnchor", () => {
  it("maps a resolved element to a dom_node target (keeping the point + meta)", () => {
    const anchor = normalizeAnchor(
      { point: { x: 320, y: 180 }, element: ELEM },
      { media_handle: "deck#abc", media_kind: "rrweb", route: "/review/s1" }
    );
    expect(anchor.media_handle).toBe("deck#abc");
    expect(anchor.media_kind).toBe("rrweb");
    expect(anchor.route).toBe("/review/s1");
    expect(anchor.target).toEqual({
      kind: "dom_node",
      element: ELEM,
      point: { x: 320, y: 180 },
    });
  });

  it("maps a drag box (no element) to a region/box target anchored at top-left", () => {
    const anchor = normalizeAnchor({
      point: { x: 20, y: 20 },
      box: { x: 20, y: 20, width: 200, height: 100 },
    });
    expect(anchor.target).toEqual({
      kind: "region",
      shape: "box",
      bbox: { x: 20, y: 20, width: 200, height: 100 },
      point: { x: 20, y: 20 },
    });
  });

  it("maps a bare point (no element, no box) to a region/highlight pin", () => {
    const anchor = normalizeAnchor({ point: { x: 7, y: 9 } });
    expect(anchor.target).toEqual({
      kind: "region",
      shape: "highlight",
      bbox: { x: 7, y: 9, width: 1, height: 1 },
      point: { x: 7, y: 9 },
    });
  });
});

describe("regionToTarget", () => {
  it("keeps the path for a freeform region and derives the anchor point", () => {
    const path = [
      { x: 5, y: 5 },
      { x: 30, y: 40 },
      { x: 10, y: 60 },
    ];
    const t = regionToTarget({
      shape: "freeform",
      bbox: { x: 5, y: 5, width: 25, height: 55 },
      path,
    });
    expect(t).toEqual({
      kind: "region",
      shape: "freeform",
      bbox: { x: 5, y: 5, width: 25, height: 55 },
      path,
      point: { x: 5, y: 5 },
    });
  });

  it("omits the path for a box region", () => {
    const t = regionToTarget({
      shape: "box",
      bbox: { x: 0, y: 0, width: 10, height: 10 },
    });
    expect(t.path).toBeUndefined();
    expect(t.shape).toBe("box");
  });
});

describe("anchorToVisualBundle (back-compat projection)", () => {
  it("projects a dom_node anchor to point + element + frame/media", () => {
    const v = anchorToVisualBundle({
      media_handle: "m",
      frame_handle: "f",
      route: "/r",
      target: { kind: "dom_node", element: ELEM, point: { x: 3, y: 4 } },
    });
    expect(v).toEqual({
      media_handle: "m",
      frame_handle: "f",
      route: "/r",
      point: { x: 3, y: 4 },
      element: ELEM,
    });
  });

  it("projects a time_range anchor to t_ms", () => {
    const v = anchorToVisualBundle({
      media_handle: "m",
      target: { kind: "time_range", start_ms: 1200 },
    });
    expect(v.t_ms).toBe(1200);
    expect(v.point).toBeUndefined();
  });

  it("projects a semantic_element anchor to its point", () => {
    const v = anchorToVisualBundle({
      target: {
        kind: "semantic_element",
        id: "title",
        plugin: "slidey",
        point: { x: 12, y: 34 },
      },
    });
    expect(v.point).toEqual({ x: 12, y: 34 });
  });
});

describe("serializeAnchor (on-wire shape, mirrors host.AnchorFromParams)", () => {
  it("projects a dom_node to {kind, dom_node:{…, bbox:[x,y,w,h]}}", () => {
    const wire = serializeAnchor(
      normalizeAnchor({ point: { x: 1, y: 2 }, element: ELEM })
    );
    expect(wire).toEqual({
      kind: "dom_node",
      dom_node: {
        selector: '[data-testid="intent-btn-run"]',
        role: "button",
        text: "Run",
        bbox: [100, 50, 80, 30],
      },
    });
  });

  it("projects a freeform region to {kind, region:{shape, path:[[x,y]], bbox}}", () => {
    const wire = serializeAnchor({
      target: regionToTarget({
        shape: "freeform",
        bbox: { x: 5, y: 5, width: 25, height: 55 },
        path: [
          { x: 5, y: 5 },
          { x: 30, y: 40 },
        ],
      }),
    });
    expect(wire).toEqual({
      kind: "region",
      region: {
        shape: "freeform",
        path: [
          [5, 5],
          [30, 40],
        ],
        bbox: [5, 5, 25, 55],
      },
    });
  });

  it("projects a semantic_element to {kind, semantic_element:{plugin, ref, bbox}}", () => {
    const wire = serializeAnchor({
      target: {
        kind: "semantic_element",
        plugin: "slidey",
        ref: "scene-2.title",
        semantic_kind: "field",
        bbox: { x: 100, y: 50, width: 400, height: 80 },
        id: "scene-2.title",
        label: "scene-2 · title",
        selector: "[data-slidey-el='scene-2.title']",
        text: "Quarterly Results",
        data: { path: "scenes[1].title" },
        point: { x: 100, y: 50 },
      },
    });
    // UI-only fields (id/point) are dropped; semantic context is preserved.
    expect(wire).toEqual({
      kind: "semantic_element",
      semantic_element: {
        plugin: "slidey",
        ref: "scene-2.title",
        bbox: [100, 50, 400, 80],
        semantic_kind: "field",
        label: "scene-2 · title",
        selector: "[data-slidey-el='scene-2.title']",
        text: "Quarterly Results",
        data: { path: "scenes[1].title" },
      },
    });
  });

  it("projects a time_range and returns null for a target-less anchor", () => {
    expect(
      serializeAnchor({ target: { kind: "time_range", start_ms: 1200 } })
    ).toEqual({ kind: "time_range", time_range: { start_ms: 1200 } });
    expect(serializeAnchor({ media_handle: "m" })).toBeNull();
  });
});

describe("semanticSidecarName", () => {
  it("strips a content-address fragment and extension", () => {
    expect(semanticSidecarName("deck#6e2b0759")).toBe("deck.semantic.json");
    expect(semanticSidecarName("deck.mp4")).toBe("deck.semantic.json");
    expect(semanticSidecarName("path/to/slides.png")).toBe("slides.semantic.json");
    expect(semanticSidecarName("plain")).toBe("plain.semantic.json");
  });
});
