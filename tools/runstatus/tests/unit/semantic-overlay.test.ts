/**
 * Component tests for src/components/SemanticOverlay.vue + the
 * lib/semanticPlugins registry — the slidey (sidecar-driven) annotation surface.
 * A click on an element marker emits a `semantic_element` anchor target carrying
 * the sidecar `ref` VERBATIM + plugin + resolved label + bbox; the label routes
 * through the client plugin registry (a registered plugin customizes it, an
 * absent one falls back to the element's own label, then its `ref`). The wire
 * shape mirrors host.SemanticSidecar (envelope plugin, element `ref`, bbox
 * [x,y,w,h]). No server, no LLM.
 */
import { describe, it, expect } from "vitest";
import { mount } from "@vue/test-utils";
import SemanticOverlay from "../../src/components/SemanticOverlay.vue";
import {
  enrichSemanticMapFromDOM,
  formatLabel,
  toSemanticMap,
} from "../../src/lib/semanticPlugins.js";
import type { SemanticSidecar, SemanticMap } from "../../src/lib/semanticPlugins.js";

const SIDECAR: SemanticSidecar = {
  plugin: "slidey",
  elements: [
    {
      ref: "scene-2.title",
      kind: "field",
      description: "The title field in the rendered scene",
      selector: "[data-slidey-el='scene-2.title']",
      text: "Quarterly Results",
      data: { path: "scenes[1].title" },
      bbox: [100, 50, 400, 80],
    },
    { ref: "mystery", label: "Mystery box", bbox: [10, 10, 20, 20] },
    { ref: "no-box" }, // no bbox → not drawable as a positioned marker
  ],
};
const MAP: SemanticMap = toSemanticMap(SIDECAR, "deck#abc", {
  width: 1280,
  height: 720,
});

describe("semanticPlugins.formatLabel", () => {
  it("humanizes a slidey ref when no explicit label is given", () => {
    expect(formatLabel(SIDECAR.elements[0], "slidey")).toBe("scene-2 · title");
  });

  it("prefers an explicit label over the plugin formatter", () => {
    expect(formatLabel(SIDECAR.elements[1], "slidey")).toBe("Mystery box");
  });

  it("falls back to the ref for an unregistered plugin", () => {
    expect(formatLabel({ ref: "bare" }, "unknown-future-plugin")).toBe("bare");
  });
});

describe("toSemanticMap", () => {
  it("lifts the envelope plugin + natural size onto the overlay map", () => {
    expect(MAP.plugin).toBe("slidey");
    expect(MAP.natural).toEqual({ width: 1280, height: 720 });
    expect(MAP.elements).toHaveLength(3);
  });

  it("uses a sidecar-declared viewport when one exists", () => {
    const map = toSemanticMap(
      { plugin: "html-data", viewport: { width: 900, height: 500 }, elements: [] },
      "mockup.html",
      { width: 1280, height: 720 }
    );
    expect(map.natural).toEqual({ width: 900, height: 500 });
  });
});

describe("enrichSemanticMapFromDOM", () => {
  it("resolves selector-only HTML fields to bbox and text", () => {
    const root = document.implementation.createHTMLDocument("fixture");
    root.body.innerHTML = `<section><span data-field="status">Blocked</span></section>`;
    const field = root.querySelector("[data-field='status']") as HTMLElement;
    Object.defineProperty(field, "getBoundingClientRect", {
      value: () => ({ left: 12, top: 34, width: 56, height: 20 }),
    });
    const map = toSemanticMap(
      {
        plugin: "html-data",
        elements: [{ ref: "issue.status", selector: "[data-field='status']" }],
      },
      "issue.html",
      { width: 800, height: 600 }
    );

    const enriched = enrichSemanticMapFromDOM(map, root);
    expect(enriched.elements[0]).toMatchObject({
      ref: "issue.status",
      selector: "[data-field='status']",
      text: "Blocked",
      bbox: [12, 34, 56, 20],
    });
  });
});

describe("SemanticOverlay", () => {
  it("renders one marker per DRAWABLE element (with a bbox)", () => {
    const w = mount(SemanticOverlay, { props: { map: MAP } });
    const markers = w.findAll("[data-testid^='so-marker-']");
    expect(markers).toHaveLength(2); // the no-box element is skipped
    expect(w.get("[data-testid='so-marker-scene-2.title']").text()).toContain(
      "scene-2 · title"
    );
    w.unmount();
  });

  it("emits a semantic_element anchor target (ref verbatim) on a click", async () => {
    const w = mount(SemanticOverlay, { props: { map: MAP } });
    await w.get("[data-testid='so-marker-scene-2.title']").trigger("click");
    const target = w.emitted("pick")![0][0] as {
      kind: string;
      plugin: string;
      ref: string;
      semantic_kind: string;
      label: string;
      description: string;
      selector: string;
      text: string;
      data: Record<string, unknown>;
      bbox: { x: number; y: number; width: number; height: number };
      point: { x: number; y: number };
    };
    expect(target).toEqual({
      kind: "semantic_element",
      plugin: "slidey",
      ref: "scene-2.title",
      semantic_kind: "field",
      description: "The title field in the rendered scene",
      selector: "[data-slidey-el='scene-2.title']",
      text: "Quarterly Results",
      data: { path: "scenes[1].title" },
      bbox: { x: 100, y: 50, width: 400, height: 80 },
      id: "scene-2.title",
      label: "scene-2 · title",
      point: { x: 100, y: 50 },
    });
    w.unmount();
  });

  it("positions a marker as a percent of the natural size", () => {
    const w = mount(SemanticOverlay, { props: { map: MAP } });
    const style = (
      w.get("[data-testid='so-marker-scene-2.title']").element as HTMLElement
    ).style;
    // x=100 / 1280 = 7.8125%, width=400/1280 = 31.25%.
    expect(style.left).toBe("7.8125%");
    expect(style.width).toBe("31.25%");
    w.unmount();
  });
});
