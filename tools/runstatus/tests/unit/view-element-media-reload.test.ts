/**
 * Component test for src/components/ViewElement.vue's media-reload behaviour.
 *
 * Regression for the slidey-edit "the deck never updates after I refine it" bug.
 * The engine re-renders the deck to a NEW content-addressed handle each refine
 * (proved end-to-end on the Go side by TestSlideyRefineChangesHandleAndBytes).
 * The handle flows into the slideshow <iframe>'s src. Without a :key bound to the
 * handle, Vue patches `src` on the SAME iframe DOM node and the browser keeps the
 * stale cached render — the user sees no change. With :key="mediaHandle" the
 * iframe element is REPLACED when the handle changes, forcing a fresh load.
 *
 * We assert the contract that guarantees a reload: the iframe DOM node identity
 * changes when the handle changes (and is stable when it doesn't). No server, no
 * LLM — the data source is mocked to a pure URL builder.
 */
import { describe, it, expect, vi, beforeEach } from "vitest";
import { mount } from "@vue/test-utils";
import { setActivePinia, createPinia } from "pinia";

beforeEach(() => {
  setActivePinia(createPinia());
});

// artifactUrl just needs to be a deterministic function of the handle.
vi.mock("../../src/data/source.js", () => ({
  createDataSource: () => ({
    artifactUrl: (h: string) => `/artifact/${encodeURIComponent(h)}`,
  }),
}));

// ViewElement reads the route only to recover a sessionId for the /review link;
// a stub with no sessionId is fine (the slideshow frame does not need it).
vi.mock("vue-router", () => ({
  useRoute: () => ({ path: "/", query: {}, params: {} }),
}));

import ViewElement from "../../src/components/ViewElement.vue";

function mountSlideshow(handle: string) {
  return mount(ViewElement, {
    props: {
      element: {
        Kind: "media",
        MediaKind: "slideshow",
        MediaHandle: handle,
        Mime: "text/html",
        MediaCaption: "deck",
      } as never,
    },
  });
}

describe("ViewElement slideshow media reload", () => {
  it("replaces the iframe DOM node when the deck handle changes", async () => {
    const w = mountSlideshow("slidey-edit#01584636");
    const frame = w.find('[data-testid="media-slideshow-frame"]');
    expect(frame.exists()).toBe(true);
    expect(frame.attributes("src")).toBe(
      "/artifact/slidey-edit%2301584636",
    );
    const firstEl = frame.element;

    // Re-render the SAME handle — the iframe element must be stable (caching
    // still works when the deck didn't change).
    await w.setProps({
      element: {
        Kind: "media",
        MediaKind: "slideshow",
        MediaHandle: "slidey-edit#01584636",
        Mime: "text/html",
        MediaCaption: "deck",
      } as never,
    });
    expect(
      w.find('[data-testid="media-slideshow-frame"]').element,
    ).toBe(firstEl);

    // A refine produces a NEW content-addressed handle. The :key must force a
    // brand-new iframe element (not a patched src on the old one) so the browser
    // drops the stale render and loads the updated deck.
    await w.setProps({
      element: {
        Kind: "media",
        MediaKind: "slideshow",
        MediaHandle: "slidey-edit#79a7b871",
        Mime: "text/html",
        MediaCaption: "deck",
      } as never,
    });
    const frame2 = w.find('[data-testid="media-slideshow-frame"]');
    expect(frame2.attributes("src")).toBe(
      "/artifact/slidey-edit%2379a7b871",
    );
    expect(frame2.element).not.toBe(firstEl);
  });
});
