/**
 * ArtifactAnnotator generic HTML semantics: a sidecar can describe fields in an
 * arbitrary HTML mockup/object view by selector only. The annotator resolves the
 * live iframe DOM box/text, overlays a marker, and emits a semantic_element
 * anchor with field context preserved. No server, browser, or LLM.
 */
import { describe, it, expect, vi } from "vitest";
import { mount, flushPromises } from "@vue/test-utils";
import ArtifactAnnotator from "../../src/components/ArtifactAnnotator.vue";
import type { SemanticSidecar } from "../../src/lib/semanticPlugins.js";

const SIDECAR: SemanticSidecar = {
  plugin: "html-data",
  schema_version: 1,
  elements: [
    {
      ref: "issue.status",
      kind: "field",
      label: "Status",
      selector: "[data-field='status']",
      data: { path: "issue.status" },
    },
  ],
};

describe("ArtifactAnnotator html semantic sidecar", () => {
  it("overlays selector-only fields and emits a semantic_element anchor", async () => {
    const ds = {
      artifactUrl: (h: string) => `/artifact/${h}`,
      semanticMap: vi.fn().mockResolvedValue(SIDECAR),
    };

    const w = mount(ArtifactAnnotator, {
      attachTo: document.body,
      props: {
        ds: ds as never,
        sessionId: "s1",
        mediaHandle: "issue-card.html",
        mediaKind: "html",
      },
    });
    await flushPromises();

    const frame = w.get<HTMLIFrameElement>('[data-testid="aa-iframe"]');
    const doc = frame.element.contentDocument!;
    doc.body.innerHTML = `<article><span data-field="status">Blocked</span></article>`;
    const field = doc.querySelector("[data-field='status']") as HTMLElement;
    Object.defineProperty(field, "getBoundingClientRect", {
      value: () => ({ left: 20, top: 30, width: 120, height: 24 }),
    });
    Object.defineProperty(doc.documentElement, "clientWidth", {
      value: 800,
      configurable: true,
    });
    Object.defineProperty(doc.documentElement, "clientHeight", {
      value: 450,
      configurable: true,
    });

    await frame.trigger("load");
    await flushPromises();

    const marker = w.get('[data-testid="so-marker-issue.status"]');
    expect(marker.text()).toContain("Status");
    await marker.trigger("click");

    const emitted = w.emitted("anchor");
    expect(emitted).toBeTruthy();
    const anchor = emitted![0][0] as { media_kind: string; target: Record<string, unknown> };
    expect(anchor.media_kind).toBe("html");
    expect(anchor.target).toMatchObject({
      kind: "semantic_element",
      plugin: "html-data",
      ref: "issue.status",
      semantic_kind: "field",
      label: "Status",
      selector: "[data-field='status']",
      text: "Blocked",
      data: { path: "issue.status" },
      bbox: { x: 20, y: 30, width: 120, height: 24 },
    });
    w.unmount();
  });
});
