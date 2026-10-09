/**
 * Component test: a slideshow ViewElement carries the VIEWED scene onto its
 * refine dispatch. The live deck reports its on-screen scene via the generic
 * `embed:view` postMessage; ViewElement tracks it (store.embedScope) and the
 * annotate "Send & refine" must include it as the `current_scene` slot so the
 * edit lands on the slide the operator is looking at — the wrong-slide fix's
 * front-end half. No server, no LLM.
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
import { useRunStore } from "../../src/stores/run.js";

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

describe("ViewElement scene-aware refine dispatch", () => {
  it("carries the viewed scene (embed:view scope) as current_scene on refine", async () => {
    const w = mountSlideshow();
    const store = useRunStore();
    const submit = vi
      .spyOn(store, "submitIntent")
      .mockResolvedValue({} as never);

    // The live deck reports the operator navigated to scene 9 (Cat Wrangling).
    window.dispatchEvent(
      new MessageEvent("message", {
        data: { type: "embed:view", producer: "slidey", scope: "9", step: 2, label: "Cat Wrangling" },
      }),
    );
    await w.vm.$nextTick();
    expect(store.embedScope).toBe("9");
    expect(store.embedStep).toBe("2");

    // Stage an anchor + instruction and send (drive the component's send path).
    (w.vm as unknown as { onAnchor: (a: unknown) => void }).onAnchor({
      media_handle: "slidey-edit#abc",
      media_kind: "html",
      target: { kind: "semantic_element", ref: "9/image", label: "Scene 9" },
    });
    (w.vm as unknown as { instruction: string }).instruction =
      "swap the cat for a cowboy herding cats";
    await (w.vm as unknown as { sendAnnotation: () => Promise<void> }).sendAnnotation();

    expect(submit).toHaveBeenCalled();
    const slots = submit.mock.calls[0][3] as Record<string, unknown>;
    expect(slots.feedback).toBe("swap the cat for a cowboy herding cats");
    expect(slots.current_scene).toBe("9"); // the viewed slide rode the refine
    // WHAT the operator clicked rides as a slot too, so the story can fold the
    // LIVE anchor into world.annotation (else the refine reads only the seed).
    expect(slots.annotation_anchor).toEqual({
      kind: "semantic_element",
      semantic_element: { plugin: undefined, ref: "9/image", label: "Scene 9" },
      label: "Scene 9",
    });
  });

  it("opens annotate on the same viewed scene transition", async () => {
    const w = mountSlideshow();
    const store = useRunStore();

    window.dispatchEvent(
      new MessageEvent("message", {
        data: { type: "embed:view", producer: "slidey", scope: "9", step: 2, label: "Cat Wrangling" },
      }),
    );
    await w.vm.$nextTick();
    expect(store.embedScope).toBe("9");
    expect(store.embedStep).toBe("2");

    await w.find('[data-testid="media-annotate"]').trigger("click");
    await w.vm.$nextTick();

    const frame = w.find('[data-testid="aa-slidey-embed"]');
    expect(frame.exists()).toBe(true);
    const post = vi.fn();
    Object.defineProperty(frame.element, "contentWindow", {
      value: { postMessage: post },
      configurable: true,
    });
    await frame.trigger("load");

    expect(post).toHaveBeenCalledWith(
      { type: "embed:annotate", enabled: true, scope: "9", step: "2" },
      "*",
    );
  });

  it("reloads a re-rendered deck at the last viewed scene transition", async () => {
    const w = mountSlideshow();
    const store = useRunStore();

    window.dispatchEvent(
      new MessageEvent("message", {
        data: { type: "embed:view", producer: "slidey", scope: "9", step: 2, label: "Cat Wrangling" },
      }),
    );
    await w.vm.$nextTick();
    expect(store.embedScope).toBe("9");
    expect(store.embedStep).toBe("2");

    await w.setProps({
      element: {
        Kind: "media",
        MediaKind: "slideshow",
        MediaHandle: "slidey-edit#def",
        Mime: "text/html",
        AnnotateIntent: "refine",
        AnnotateFeedbackSlot: "feedback",
      } as never,
    });

    const frame = w.find('[data-testid="media-slideshow-frame"]');
    expect(frame.attributes("src")).toBe("/artifact/slidey-edit%23def?scene=9&step=2");
  });

  it("keeps the iframe src stable across reveals on the same handle (no flicker)", async () => {
    const w = mountSlideshow();

    const frame = w.find('[data-testid="media-slideshow-frame"]');
    const initialSrc = frame.attributes("src");

    // Operator navigates the LOADED deck — the deck publishes new embed:view
    // scope/step on each reveal. The main iframe src must NOT change, or the
    // iframe reloads (a visible flicker) and the deck resets to slide 1.
    for (const [scope, step] of [["3", 1], ["7", 2], ["9", 0]] as const) {
      window.dispatchEvent(
        new MessageEvent("message", {
          data: { type: "embed:view", producer: "slidey", scope, step },
        }),
      );
      await w.vm.$nextTick();
      expect(w.find('[data-testid="media-slideshow-frame"]').attributes("src")).toBe(initialSrc);
    }
  });
});
