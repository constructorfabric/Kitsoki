import { describe, it, expect, vi } from "vitest";
import { mount } from "@vue/test-utils";

import ViewElement from "../../src/components/ViewElement.vue";
import type { ViewElement as VE } from "../../src/types.js";

// Mock createDataSource so ViewElement.vue's module-init call returns a stub
// that resolves artifact handles to predictable fake URLs without a real server.
vi.mock("../../src/data/source.js", () => ({
  createDataSource: () => ({
    artifactUrl: (handle: string) => `/fake-artifact/${handle}`,
  }),
}));

function render(element: VE) {
  return mount(ViewElement, { props: { element } });
}

describe("ViewElement", () => {
  it("renders prose as <p> with inline code", () => {
    const w = render({
      Kind: "prose",
      Source: "Hello `world` of traces.\n\nSecond paragraph.",
    });
    const ps = w.findAll("p.ve-prose");
    expect(ps.length).toBe(2);
    expect(ps[0].text()).toContain("Hello");
    expect(ps[0].find("code.ve-inline-code").text()).toBe("world");
    expect(ps[1].text()).toBe("Second paragraph.");
    w.unmount();
  });

  it("renders heading as <h3>", () => {
    const w = render({ Kind: "heading", Source: "Section Title" });
    const h = w.find("h3.ve-heading");
    expect(h.exists()).toBe(true);
    expect(h.text()).toBe("Section Title");
    w.unmount();
  });

  it("renders code as <pre><code>", () => {
    const w = render({ Kind: "code", Source: "let x = 1;" });
    const pre = w.find("pre.ve-code");
    expect(pre.exists()).toBe(true);
    expect(pre.find("code").text()).toBe("let x = 1;");
    w.unmount();
  });

  it("renders list as <ul><li> with labels and hints", () => {
    const w = render({
      Kind: "list",
      Items: [
        { Label: "first", Hint: "a hint" },
        { Label: "second" },
      ],
    });
    const lis = w.findAll("ul.ve-list li");
    expect(lis.length).toBe(2);
    expect(lis[0].find(".ve-list-label").text()).toBe("first");
    expect(lis[0].find(".ve-list-hint").text()).toBe("a hint");
    expect(lis[1].find(".ve-list-hint").exists()).toBe(false);
    w.unmount();
  });

  it("renders kv as a definition list of key/value", () => {
    const w = render({
      Kind: "kv",
      Pairs: [
        { Key: "state", Value: "proposal_draft" },
        { Key: "turn", Value: "7" },
      ],
    });
    const dl = w.find("dl.ve-kv");
    expect(dl.exists()).toBe(true);
    const dts = w.findAll("dt.ve-kv-key");
    const dds = w.findAll("dd.ve-kv-value");
    expect(dts.map((d) => d.text())).toEqual(["state", "turn"]);
    expect(dds.map((d) => d.text())).toEqual(["proposal_draft", "7"]);
    w.unmount();
  });

  it("renders banner as a styled callout with marker and subtitle", () => {
    const w = render({
      Kind: "banner",
      Source: "Heads up",
      Subtitle: "details here",
      Marker: "!",
      Color: "warn",
    });
    const b = w.find(".ve-banner");
    expect(b.exists()).toBe(true);
    expect(b.classes()).toContain("banner--warn");
    expect(b.find(".ve-banner-marker").text()).toBe("!");
    expect(b.find(".ve-banner-text").text()).toBe("Heads up");
    expect(b.find(".ve-banner-subtitle").text()).toBe("details here");
    w.unmount();
  });

  it("omits choice elements (InputBar renders them as interactive buttons)", () => {
    const w = render({
      Kind: "choice",
      ChoicePrompt: "Pick one",
      ChoiceIntent: "select_option",
    });
    // Choice items are surfaced as clickable buttons by InputBar; ViewElement
    // deliberately renders nothing for them to avoid duplication.
    expect(w.find(".ve-choice").exists()).toBe(false);
    w.unmount();
  });

  it("renders template kind as prose paragraphs", () => {
    const w = render({ Kind: "template", Source: "resolved text" });
    const ps = w.findAll("p.ve-prose");
    expect(ps.length).toBe(1);
    expect(ps[0].text()).toBe("resolved text");
    w.unmount();
  });

  it("tolerates null Items / Pairs from Go zero-value marshalling", () => {
    const list = render({ Kind: "list", Items: null });
    expect(list.findAll("li").length).toBe(0);
    list.unmount();
    const kv = render({ Kind: "kv", Pairs: null });
    expect(kv.findAll("dt").length).toBe(0);
    kv.unmount();
  });

  // ── media element tests ────────────────────────────────────────────────────
  // artifactUrl is supplied by the vi.mock stub above; no server involved.

  it("media video/mp4 renders <video> with src from artifactUrl", () => {
    const w = render({
      Kind: "media",
      Handle: "clip.mp4",
      Mime: "video/mp4",
    });
    const wrapper = w.find(".ve-media");
    expect(wrapper.exists()).toBe(true);
    const video = wrapper.find("video.ve-media-video");
    expect(video.exists()).toBe(true);
    expect(video.attributes("src")).toBe("/fake-artifact/clip.mp4");
    expect(video.attributes("controls")).toBeDefined();
    w.unmount();
  });

  it("media image/png renders <img> with lazy loading and resolved src", () => {
    const w = render({
      Kind: "media",
      Handle: "screenshot.png",
      Mime: "image/png",
    });
    const img = w.find("img.ve-media-image");
    expect(img.exists()).toBe(true);
    expect(img.attributes("src")).toBe("/fake-artifact/screenshot.png");
    expect(img.attributes("loading")).toBe("lazy");
    w.unmount();
  });

  it("media application/pdf renders <iframe> with resolved src", () => {
    const w = render({
      Kind: "media",
      Handle: "report.pdf",
      Mime: "application/pdf",
    });
    const frame = w.find("iframe.ve-media-iframe");
    expect(frame.exists()).toBe(true);
    expect(frame.attributes("src")).toBe("/fake-artifact/report.pdf");
    // PDF iframe must NOT have a sandbox attribute (needs plugin access)
    expect(frame.attributes("sandbox")).toBeUndefined();
    w.unmount();
  });

  it("media text/html renders a sandboxed <iframe>", () => {
    const w = render({
      Kind: "media",
      Handle: "preview.html",
      Mime: "text/html",
    });
    const frame = w.find("iframe.ve-media-iframe");
    expect(frame.exists()).toBe(true);
    expect(frame.attributes("src")).toBe("/fake-artifact/preview.html");
    // sandbox attribute must be present (value may be "" or "true" in happy-dom)
    expect(frame.attributes("sandbox")).toBeDefined();
    w.unmount();
  });

  it("media unknown MIME renders a labeled <a> download link", () => {
    const w = render({
      Kind: "media",
      Handle: "data.bin",
      Mime: "application/octet-stream",
      Caption: "Binary blob",
    });
    const link = w.find("a.ve-media-link");
    expect(link.exists()).toBe(true);
    expect(link.attributes("href")).toBe("/fake-artifact/data.bin");
    expect(link.attributes("download")).toBe("data.bin");
    expect(link.text()).toBe("Binary blob");
    w.unmount();
  });

  it("media element renders caption below the media when Caption is set", () => {
    const w = render({
      Kind: "media",
      Handle: "photo.jpg",
      Mime: "image/jpeg",
      Caption: "A test photo",
    });
    const caption = w.find("p.ve-media-caption");
    expect(caption.exists()).toBe(true);
    expect(caption.text()).toBe("A test photo");
    w.unmount();
  });

  it("media element with no Handle renders the outer wrapper but no media tag", () => {
    // A media element missing its Handle (zero-value from Go) must not crash;
    // the template guard skips the inner <template v-if="el.Handle">.
    const w = render({ Kind: "media", Mime: "image/png" });
    expect(w.find(".ve-media").exists()).toBe(true);
    expect(w.find("img").exists()).toBe(false);
    expect(w.find("video").exists()).toBe(false);
    w.unmount();
  });
});
