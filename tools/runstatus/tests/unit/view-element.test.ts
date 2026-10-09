import { describe, it, expect, vi, beforeEach } from "vitest";
import { mount, flushPromises } from "@vue/test-utils";

import ViewElement from "../../src/components/ViewElement.vue";
import type { ViewElement as VE } from "../../src/types.js";
import type { SemanticSidecar } from "../../src/lib/semanticPlugins.js";
import type { AnnotationAnchor } from "../../src/lib/annotationAnchor.js";

// A shared mutable stub the createDataSource mock returns. Tests override
// semanticMap / capture offpath calls per-case. artifactUrl / artifactPosterUrl
// resolve to predictable fake URLs without a real server.
const dsStub = {
  artifactUrl: (handle: string) => `/fake-artifact/${handle}`,
  artifactPosterUrl: (handle: string) => `/fake-artifact/${handle}/poster`,
  semanticMap: vi.fn<
    (sessionId: string, handle: string) => Promise<SemanticSidecar | null>
  >(),
  offpath: vi.fn<
    (
      sessionId: string,
      input: string,
      visual?: unknown,
      anchor?: AnnotationAnchor
    ) => Promise<{ answer: string }>
  >(),
};

vi.mock("../../src/data/source.js", () => ({
  createDataSource: () => dsStub,
}));

// A mutable route the useRoute() mock returns. Default to no sessionId (the
// off-session / snapshot posture); renderWithSession sets one for the
// annotate-affordance cases. ViewElement only reads route.params.sessionId.
const route: { params: Record<string, string> } = { params: {} };
vi.mock("vue-router", () => ({
  useRoute: () => route,
}));

const runStoreStub = {
  embedScope: "",
  setEmbedView: vi.fn(),
  submitIntent: vi.fn<() => Promise<void>>(),
};
vi.mock("../../src/stores/run.js", () => ({
  useRunStore: () => runStoreStub,
}));

beforeEach(() => {
  route.params = {};
  window.location.hash = "";
  runStoreStub.embedScope = "";
  runStoreStub.setEmbedView.mockReset();
  runStoreStub.submitIntent.mockReset();
  runStoreStub.submitIntent.mockResolvedValue(undefined);
  dsStub.semanticMap.mockReset();
  dsStub.semanticMap.mockResolvedValue(null);
  dsStub.offpath.mockReset();
  dsStub.offpath.mockResolvedValue({ answer: "ok" });
});

function render(element: VE) {
  return mount(ViewElement, { props: { element } });
}

// A sidecar fixture mirroring the slidey deck (refs + bboxes in natural pixels).
const SIDECAR: SemanticSidecar = {
  plugin: "slidey",
  schema_version: 1,
  elements: [
    { ref: "1/card_0", label: "Scene 1 · card 0", bbox: [140, 518, 535, 114] },
    { ref: "1/title", label: "Scene 1 · title", bbox: [200, 100, 400, 60] },
  ],
};

// Mount ViewElement under a live session id (so useRoute() yields a sessionId —
// the Annotate affordance is gated on one).
function renderWithSession(element: VE, sessionId = "sess-1") {
  route.params = { sessionId };
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

  it("renders **bold** spans as <strong> in prose", () => {
    const w = render({
      Kind: "prose",
      Source: "→ Use **change_existing** with the path, not `override_new`.",
    });
    const p = w.find("p.ve-prose");
    const strong = p.find("strong.ve-bold");
    expect(strong.exists()).toBe(true);
    expect(strong.text()).toBe("change_existing");
    // Inside a code span, ** is literal (code is not re-scanned for bold).
    expect(p.find("code.ve-inline-code").text()).toBe("override_new");
    // No literal asterisks leak into the rendered text.
    expect(p.text()).not.toContain("**");
    w.unmount();
  });

  it("keeps ** literal inside an inline-code span", () => {
    const w = render({ Kind: "prose", Source: "run `a ** b` now" });
    expect(w.find("code.ve-inline-code").text()).toBe("a ** b");
    expect(w.find("strong.ve-bold").exists()).toBe(false);
    w.unmount();
  });

  it("renders template elements as markdown documents", () => {
    const w = render({
      Kind: "template",
      Source:
        "## Fix summary\n\n- Updated **config** handling for `APP_FLAG`.\n\n| Check | Result |\n|---|---|\n| unit | pass |",
    });
    const md = w.find('[data-testid="view-template-markdown"]');
    expect(md.exists()).toBe(true);
    expect(md.find("h2.md-h2").text()).toBe("Fix summary");
    expect(md.find("ul.md-ul li").text()).toContain("Updated config handling");
    expect(md.find("strong").text()).toBe("config");
    expect(md.find("code").text()).toBe("APP_FLAG");
    const table = md.find("table.md-table");
    expect(table.exists()).toBe(true);
    expect(table.findAll("th").map((th) => th.text())).toEqual(["Check", "Result"]);
    expect(table.findAll("td").map((td) => td.text())).toEqual(["unit", "pass"]);
    expect(md.html()).not.toContain("## Fix summary");
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
        { Key: "state", Value: "design_draft" },
        { Key: "turn", Value: "7" },
      ],
    });
    const dl = w.find("dl.ve-kv");
    expect(dl.exists()).toBe(true);
    const dts = w.findAll("dt.ve-kv-key");
    const dds = w.findAll("dd.ve-kv-value");
    expect(dts.map((d) => d.text())).toEqual(["state", "turn"]);
    expect(dds.map((d) => d.text())).toEqual(["design_draft", "7"]);
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

  it("renders semantic warnings as orange warning callouts even when authored blue", () => {
    const w = render({
      Kind: "banner",
      Source: "Session warning\nReload the story to apply changes",
      Subtitle: "The current session may be stale",
      Color: "blue",
    });
    const b = w.find(".ve-banner");
    expect(b.exists()).toBe(true);
    expect(b.classes()).toContain("banner--warn");
    expect(b.classes()).not.toContain("banner--info");
    expect(b.find(".ve-banner-marker").text()).toBe("⚠");
    expect(b.findAll(".ve-banner-line").map((line) => line.text())).toEqual([
      "Session warning",
      "Reload the story to apply changes",
    ]);
    w.unmount();
  });

  it("honours a literal hex banner colour as an inline accent (TUI parity)", () => {
    // The bugfix / design pipeline banners carry per-phase hex accents
    // (#06B6D4 cyan, #3B82F6 blue, #8B5CF6 violet, …). The web must convey the
    // same colour the TUI's coloured rule does instead of falling back to grey.
    const w = render({
      Kind: "banner",
      Source: "REPRODUCING",
      Subtitle: "Phase 1 / 7",
      Color: "#06B6D4",
    });
    const b = w.find(".ve-banner");
    expect(b.classes()).toContain("banner--accent");
    // Inline style carries the authored hex on border + text.
    const style = (b.attributes("style") ?? "").toLowerCase();
    expect(style).toContain("#06b6d4");
    expect(style).toContain("border-color");
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

  it("renders simple template kind content through the markdown path", () => {
    const w = render({ Kind: "template", Source: "resolved text" });
    const ps = w.findAll(".ve-markdown p.md-p");
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
    expect(video.attributes("poster")).toBe("/fake-artifact/clip.mp4/poster");
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
    expect(frame.attributes("sandbox")).toBe("allow-scripts");
    w.unmount();
  });

  it("media slideshow embeds the static HTML deck + links to the interactive deck", () => {
    const w = render({
      Kind: "media",
      MediaHandle: "slidey-edit#1",
      MediaKind: "slideshow",
    });
    const frame = w.find('[data-testid="media-slideshow-frame"]');
    expect(frame.exists()).toBe(true);
    expect(frame.attributes("src")).toBe("/fake-artifact/slidey-edit#1");
    expect(frame.attributes("sandbox")).toBe("allow-scripts");
    // A link opens the live interactive deck (the bundle can run scripts there).
    const open = w.find('[data-testid="media-slideshow-open"]');
    expect(open.exists()).toBe(true);
    expect(open.attributes("href")).toBe("/fake-artifact/slidey-edit#1");
    expect(open.attributes("target")).toBe("_blank");
    w.unmount();
  });

  it("opens the annotator on a slideshow deck with the slidey path (poster + overlay)", async () => {
    dsStub.semanticMap.mockResolvedValue(SIDECAR);
    const w = renderWithSession({
      Kind: "media",
      MediaHandle: "slidey-edit#1",
      MediaKind: "slideshow",
    });
    await w.find('[data-testid="media-annotate"]').trigger("click");
    await flushPromises();
    expect(w.find('[data-testid="media-annotate-panel"]').text()).toContain(
      "slidey"
    );
    w.unmount();
  });

  it("auto-opens the annotator when visual_annotate is requested in the hash query", async () => {
    window.location.hash = "#/s/sess-1/chat?visual_annotate=1";
    dsStub.semanticMap.mockResolvedValue(SIDECAR);
    const w = renderWithSession({
      Kind: "media",
      MediaHandle: "slidey-edit#1",
      MediaKind: "slideshow",
    });
    await flushPromises();
    expect(w.find('[data-testid="media-annotate-panel"]').exists()).toBe(true);
    expect(w.find('[data-testid="artifact-annotator"]').exists()).toBe(true);
    w.unmount();
  });

  it("auto-opens only the matching media handle when visual_annotate names a handle", async () => {
    window.location.hash = "#/s/sess-1/chat?visual_annotate=other%231";
    const w = renderWithSession({
      Kind: "media",
      MediaHandle: "slidey-edit#1",
      MediaKind: "slideshow",
    });
    await flushPromises();
    expect(w.find('[data-testid="media-annotate-panel"]').exists()).toBe(false);
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

  // ── Annotate affordance (unified ArtifactAnnotator) ─────────────────────────

  it("offers no Annotate affordance off-session (no sessionId)", () => {
    const w = render({ Kind: "media", Handle: "clip.mp4", Mime: "video/mp4" });
    expect(w.find('[data-testid="media-annotate"]').exists()).toBe(false);
    w.unmount();
  });

  it("offers Annotate on a live video but NOT on a pdf", () => {
    const v = renderWithSession({
      Kind: "media",
      Handle: "clip.mp4",
      Mime: "video/mp4",
    });
    expect(v.find('[data-testid="media-annotate"]').exists()).toBe(true);
    v.unmount();

    const pdf = renderWithSession({
      Kind: "media",
      Handle: "report.pdf",
      Mime: "application/pdf",
    });
    expect(pdf.find('[data-testid="media-annotate"]').exists()).toBe(false);
    pdf.unmount();
  });

  it("opens the annotator with the MIME-mapped kind when no sidecar exists", async () => {
    dsStub.semanticMap.mockResolvedValue(null);
    const w = renderWithSession({
      Kind: "media",
      Handle: "clip.mp4",
      Mime: "video/mp4",
    });
    await w.find('[data-testid="media-annotate"]').trigger("click");
    await flushPromises();
    const panel = w.find('[data-testid="media-annotate-panel"]');
    expect(panel.exists()).toBe(true);
    // Probed the sidecar once; no sidecar ⇒ mp4 path (video <video>, not slidey).
    expect(dsStub.semanticMap).toHaveBeenCalledWith("sess-1", "clip.mp4");
    expect(w.find('[data-testid="aa-mp4"]').exists()).toBe(true);
    expect(w.find('[data-testid="aa-slidey"]').exists()).toBe(false);
    w.unmount();
  });

  it("promotes a sidecar-bearing mp4 deck to the slidey path with a poster backdrop + overlay markers", async () => {
    dsStub.semanticMap.mockResolvedValue(SIDECAR);
    const w = renderWithSession({
      Kind: "media",
      MediaHandle: "slidey-edit#1",
      MediaKind: "video",
      MediaCaption: "deck",
    });
    await w.find('[data-testid="media-annotate"]').trigger("click");
    await flushPromises();
    // Sidecar present ⇒ slidey path even though the base artifact is an mp4.
    expect(w.find('[data-testid="aa-slidey"]').exists()).toBe(true);
    // The backdrop is the sibling POSTER still (not the mp4), at the poster URL.
    const poster = w.find('[data-testid="aa-slidey-poster"]');
    expect(poster.exists()).toBe(true);
    expect(poster.attributes("src")).toBe("/fake-artifact/slidey-edit#1/poster");
    // The SemanticOverlay renders a positioned marker per sidecar element.
    expect(w.find('[data-testid="semantic-overlay"]').exists()).toBe(true);
    expect(w.find('[data-testid="so-marker-1/card_0"]').exists()).toBe(true);
    expect(w.find('[data-testid="so-marker-1/title"]').exists()).toBe(true);
    w.unmount();
  });

  it("keeps a sidecar-bearing html artifact on the html substrate with semantic markers", async () => {
    dsStub.semanticMap.mockResolvedValue(SIDECAR);
    const w = renderWithSession({
      Kind: "media",
      Handle: "mockup.html",
      Mime: "text/html",
    });
    await w.find('[data-testid="media-annotate"]').trigger("click");
    await flushPromises();
    expect(w.find('[data-testid="aa-html"]').exists()).toBe(true);
    expect(w.find('[data-testid="aa-slidey"]').exists()).toBe(false);
    expect(w.find('[data-testid="semantic-overlay"]').exists()).toBe(true);
    expect(w.find('[data-testid="so-marker-1/card_0"]').exists()).toBe(true);
    w.unmount();
  });

  it("dispatches an emitted anchor as an anchored off-path note", async () => {
    dsStub.semanticMap.mockResolvedValue(SIDECAR);
    const w = renderWithSession({
      Kind: "media",
      MediaHandle: "slidey-edit#1",
      MediaKind: "video",
    });
    await w.find('[data-testid="media-annotate"]').trigger("click");
    await flushPromises();
    // Click a marker → SemanticOverlay emits → ArtifactAnnotator emits anchor →
    // ViewElement stages it in the composer; Send dispatches it via ds.offpath
    // with the serialized anchor.
    await w.find('[data-testid="so-marker-1/card_0"]').trigger("click");
    await flushPromises();
    expect(w.find('[data-testid="media-annotate-composer"]').exists()).toBe(true);
    await w.find('[data-testid="media-annotate-send"]').trigger("click");
    await flushPromises();
    expect(dsStub.offpath).toHaveBeenCalledTimes(1);
    const [sid, , , anchor] = dsStub.offpath.mock.calls[0];
    expect(sid).toBe("sess-1");
    expect(anchor?.target?.kind).toBe("semantic_element");
    expect((anchor?.target as { ref: string }).ref).toBe("1/card_0");
    expect(anchor?.media_handle).toBe("slidey-edit#1");
    // Confirmation surfaces in the panel.
    expect(w.find(".ve-media-annotate-ok").exists()).toBe(true);
    w.unmount();
  });

  it("closes the annotator panel via Close", async () => {
    const w = renderWithSession({
      Kind: "media",
      Handle: "shot.png",
      Mime: "image/png",
    });
    await w.find('[data-testid="media-annotate"]').trigger("click");
    await flushPromises();
    expect(w.find('[data-testid="media-annotate-panel"]').exists()).toBe(true);
    await w.find('[data-testid="media-annotate-close"]').trigger("click");
    expect(w.find('[data-testid="media-annotate-panel"]').exists()).toBe(false);
    expect(w.find('[data-testid="media-annotate"]').exists()).toBe(true);
    w.unmount();
  });
});
