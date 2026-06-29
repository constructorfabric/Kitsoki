/**
 * Component test: the annotate composer can be DISMISSED without losing work.
 * Clicking the click-outside backdrop (or pressing Esc) unstages the input box
 * but parks the half-typed instruction keyed by the picked spot — re-picking the
 * SAME element restores the text, while picking a DIFFERENT spot starts blank.
 * This is the "no way to close the input box" fix. No server, no LLM.
 */
import { describe, it, expect, vi, beforeEach } from "vitest";
import { mount } from "@vue/test-utils";
import { setActivePinia, createPinia } from "pinia";

vi.mock("../../src/data/source.js", () => ({
  createDataSource: () => ({
    artifactUrl: (h: string) => `/artifact/${encodeURIComponent(h)}`,
    semanticMap: vi.fn().mockResolvedValue(null),
  }),
}));
vi.mock("vue-router", () => ({
  useRoute: () => ({ path: "/s/s1", query: {}, params: { sessionId: "s1" } }),
}));

import ViewElement from "../../src/components/ViewElement.vue";

beforeEach(() => setActivePinia(createPinia()));

function mountSlideshow() {
  return mount(ViewElement, {
    attachTo: document.body,
    props: {
      element: {
        Kind: "media",
        MediaKind: "slideshow",
        MediaHandle: "slidey-edit#abc",
        Mime: "text/html",
        AnnotateIntent: "refine",
        AnnotateFeedbackSlot: "feedback",
      } as never,
    },
  });
}

type VM = {
  openAnnotate: () => Promise<void>;
  onAnchor: (a: unknown) => void;
  dismissComposer: () => void;
  instruction: string;
  pendingAnchor: unknown;
};

function anchor(ref: string) {
  return {
    media_handle: "slidey-edit#abc",
    media_kind: "html",
    target: { kind: "semantic_element", ref, label: ref },
  };
}

describe("ViewElement annotate composer dismiss/persist", () => {
  it("dismiss parks the draft; re-picking the same spot restores it", async () => {
    const w = mountSlideshow();
    const vm = w.vm as unknown as VM;
    await vm.openAnnotate();

    vm.onAnchor(anchor("9/title"));
    vm.instruction = "make the title bolder";
    await w.vm.$nextTick();
    expect(w.find('[data-testid="media-annotate-composer"]').exists()).toBe(true);

    // Click outside (the backdrop) → composer closes, no work lost.
    await w.find('[data-testid="media-annotate-backdrop"]').trigger("click");
    expect(vm.pendingAnchor).toBe(null);
    expect(w.find('[data-testid="media-annotate-composer"]').exists()).toBe(false);

    // Re-pick the SAME spot → the parked text comes back.
    vm.onAnchor(anchor("9/title"));
    await w.vm.$nextTick();
    expect(vm.instruction).toBe("make the title bolder");

    // Pick a DIFFERENT spot → blank, not bleeding the other spot's text.
    vm.onAnchor(anchor("9/body"));
    await w.vm.$nextTick();
    expect(vm.instruction).toBe("");
  });

  it("Esc dismisses the composer too", async () => {
    const w = mountSlideshow();
    const vm = w.vm as unknown as VM;
    await vm.openAnnotate();
    vm.onAnchor(anchor("9/title"));
    vm.instruction = "tweak it";
    await w.vm.$nextTick();

    window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
    await w.vm.$nextTick();
    expect(vm.pendingAnchor).toBe(null);

    // Draft survived: re-pick restores.
    vm.onAnchor(anchor("9/title"));
    await w.vm.$nextTick();
    expect(vm.instruction).toBe("tweak it");
  });
});
