/**
 * Component test for ArtifactAnnotator's live-embed (slidey) path — the flagship
 * element-level picking. The deck owns spatial feedback: kitsoki turns on the
 * deck's annotation mode and, when the operator points at an element, the deck
 * posts an embed:pick that becomes a precise semantic_element AnnotationAnchor.
 * No server, no LLM — the embed protocol is driven via window messages.
 */
import { describe, it, expect, vi } from "vitest";
import { mount } from "@vue/test-utils";
import ArtifactAnnotator from "../../src/components/ArtifactAnnotator.vue";

const ds = { artifactUrl: (h: string) => `/artifact/${encodeURIComponent(h)}` };

function mountSlidey() {
  return mount(ArtifactAnnotator, {
    attachTo: document.body,
    props: {
      ds: ds as never,
      sessionId: "s1",
      mediaHandle: "slidey-edit#abc",
      mediaKind: "slidey",
      liveEmbed: true,
    },
  });
}

describe("ArtifactAnnotator live-embed (slidey)", () => {
  it("renders the live deck iframe and enables annotation mode on load", async () => {
    const w = mount(ArtifactAnnotator, {
      attachTo: document.body,
      props: {
        ds: ds as never,
        sessionId: "s1",
        mediaHandle: "slidey-edit#abc",
        mediaKind: "slidey",
        liveEmbed: true,
        embedScope: "9",
        embedStep: "2",
      },
    });
    const frame = w.find('[data-testid="aa-slidey-embed"]');
    expect(frame.exists()).toBe(true);

    // Stub the iframe's contentWindow so we can observe the enable message.
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

  it("turns an embed:pick into a precise semantic_element anchor", async () => {
    const w = mountSlidey();

    // The deck posts a pick when the operator points at the image on scene 9.
    window.dispatchEvent(
      new MessageEvent("message", {
        data: { type: "embed:pick", producer: "slidey", scope: "9", ref: "9/src", label: "image", bbox: [10, 70, 600, 400] },
      }),
    );
    await w.vm.$nextTick();

    const emitted = w.emitted("anchor");
    expect(emitted).toBeTruthy();
    const anchor = emitted![0][0] as { media_handle: string; target: Record<string, unknown> };
    expect(anchor.media_handle).toBe("slidey-edit#abc");
    expect(anchor.target).toMatchObject({
      kind: "semantic_element",
      plugin: "slidey",
      ref: "9/src", // the exact element the operator pointed at
      label: "image",
      bbox: [10, 70, 600, 400],
    });
  });
});
