/**
 * Component test for InteractiveView.vue's EMBED layout (the VS Code webview).
 *
 * When isEmbedded() is true the interactive view drops its browser two-column
 * layout and renders the chat alone; Trace and Graph live in their own dockable
 * VS Code surfaces. This guards that seam without a real webview. The
 * DataSource is mocked (no live server, no LLM) and heavy children are stubbed.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import { nextTick } from "vue";
import { setActivePinia, createPinia } from "pinia";
import type { TurnResult } from "../../src/types.js";

const dataSource = {
  getSession: vi.fn().mockResolvedValue({
    session_id: "s1",
    app_id: "demo",
    current_state: "lobby",
    turn: 0,
    started_at: "2026-06-04T00:00:00Z",
    terminal: false,
  }),
  getApp: vi.fn().mockResolvedValue({ id: "demo", name: "Demo", root: "lobby", states: {} }),
  getMermaid: vi.fn().mockResolvedValue({ source: "graph TD;", node_map: {} }),
  getTrace: vi.fn().mockResolvedValue({ events: [], last_turn: 0 }),
  artifactUrl: vi.fn((handle: string) => `/artifact/${handle}`),
  artifactPosterUrl: vi.fn((handle: string) => `/artifact/${handle}/poster`),
  subscribe: vi.fn().mockReturnValue(() => {}),
  view: vi.fn(
    (id: string): Promise<TurnResult> =>
      Promise.resolve({
        mode: "transitioned",
        state: "lobby",
        view: `Opening for ${id}`,
        typed_view: { Source: "", Elements: [] },
        allowed_intents: [],
        intents: [],
        turn_number: 0,
      }),
  ),
};

vi.mock("../../src/data/source.js", () => ({ createDataSource: () => dataSource }));

vi.mock("vue-router", () => ({
  useRoute: () => ({ path: "/s/s1/chat", query: {}, params: { sessionId: "s1" } }),
  useRouter: () => ({ replace: vi.fn() }),
  RouterLink: { props: ["to"], template: '<a :href="to"><slot /></a>' },
}));

import InteractiveView from "../../src/views/InteractiveView.vue";
import { setEmbeddedOverride } from "../../src/lib/embed.js";
import { useRunStore } from "../../src/stores/run.js";

const mountOpts = {
  props: { sessionId: "s1" },
  global: {
    stubs: {
      RouterLink: { props: ["to"], template: '<a :href="to"><slot /></a>' },
      StateDiagram: true,
      TraceTimeline: true,
      ChatTranscript: true,
      InputBar: true,
      StoryFreshness: {
        template: '<div data-testid="story-freshness-widget"></div>',
      },
      MetaButton: {
        props: ["placement"],
        template:
          '<div data-testid="meta-launcher" :data-placement="placement || \'floating\'"></div>',
      },
    },
  },
};

describe("InteractiveView — embed (VS Code) layout", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    setEmbeddedOverride(true);
    sessionStorage.clear();
    localStorage.clear();
  });
  afterEach(() => {
    setEmbeddedOverride(null);
  });

  it("renders chat-only embedded layout, not the browser trace panels", async () => {
    const wrapper = mount(InteractiveView, mountOpts);
    await flushPromises();

    expect(wrapper.find('[data-testid="chat-section"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="hint-rail"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="hint-trace"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="hint-graph"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="trace-timeline"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="trace-diagram"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="meta-launcher"]').attributes("data-placement")).toBe("topbar");

    wrapper.unmount();
  });

  it("keeps the normal web chat topbar free of the embedded Meta launcher", async () => {
    setEmbeddedOverride(false);
    const wrapper = mount(InteractiveView, mountOpts);
    await flushPromises();

    expect(wrapper.find('[data-testid="meta-launcher"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="trace-timeline"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="trace-diagram"]').exists()).toBe(true);

    wrapper.unmount();
  });

  it("does not show a redundant Observe link next to the trace toggle", async () => {
    setEmbeddedOverride(false);
    const wrapper = mount(InteractiveView, mountOpts);
    await flushPromises();

    expect(wrapper.find('[data-testid="trace-column-toggle"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="observe-link"]').exists()).toBe(false);

    wrapper.unmount();
  });

  it("collapses and expands the browser trace column", async () => {
    setEmbeddedOverride(false);
    const wrapper = mount(InteractiveView, mountOpts);
    await flushPromises();

    expect(wrapper.find('[data-testid="trace-timeline"]').exists()).toBe(true);

    await wrapper.find('[data-testid="trace-column-toggle"]').trigger("click");
    await flushPromises();

    expect(wrapper.find('[data-testid="trace-timeline"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="trace-diagram"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="chat-section"]').exists()).toBe(true);

    await wrapper.find('[data-testid="trace-column-toggle"]').trigger("click");
    await flushPromises();

    expect(wrapper.find('[data-testid="trace-timeline"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="trace-diagram"]').exists()).toBe(true);

    wrapper.unmount();
  });

  it("resizes the browser trace column and trace rows from keyboard splitters", async () => {
    setEmbeddedOverride(false);
    const wrapper = mount(InteractiveView, mountOpts);
    await flushPromises();

    await wrapper.find('[data-testid="trace-column-resizer"]').trigger("keydown", { key: "ArrowLeft" });
    await wrapper.find('[data-testid="trace-row-resizer"]').trigger("keydown", { key: "ArrowDown" });
    await flushPromises();

    expect(wrapper.find('[aria-label="Trace"]').attributes("style")).toContain("58%");
    expect(wrapper.find('[data-testid="trace-diagram"]').attributes("style")).toContain("49%");

    wrapper.unmount();
  });

  it("pins a media artifact into the browser workbench and can rearrange devtools", async () => {
    setEmbeddedOverride(false);
    const openSpy = vi.spyOn(window, "open").mockImplementation(() => null);
    const wrapper = mount(InteractiveView, mountOpts);
    await flushPromises();

    const store = useRunStore();
    store.transcript.push({
      role: "agent",
      text: "Rendered mockup",
      typedView: {
        Source: "",
        Elements: [
          {
            Kind: "media",
            MediaHandle: "mockup.html",
            MediaKind: "html",
            MediaCaption: "Checkout mockup",
          },
        ],
      },
    });
    await nextTick();

    await wrapper.find('[data-testid="media-workbench-toggle"]').trigger("click");
    await flushPromises();

    expect(wrapper.find('[data-testid="media-workbench-pane"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="media-workbench-stage"]').text()).toContain("Checkout mockup");
    expect(wrapper.find('[data-testid="media-workbench-stage"] [data-testid="media-pin-workbench"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="chat-pinned-context"]').text()).toContain("Checkout mockup");
    expect(wrapper.find('[data-testid="media-devtools-pane"]').exists()).toBe(true);
    expect(wrapper.find(".iv__main").attributes("style")).toContain("42%");

    await wrapper.find('[data-testid="media-workbench-resizer"]').trigger("keydown", { key: "ArrowRight" });
    await wrapper.find('[data-testid="devtools-workbench-resizer"]').trigger("keydown", { key: "ArrowLeft" });
    await flushPromises();

    expect(wrapper.find(".iv__main").attributes("style")).toContain("46%");
    expect(wrapper.find(".iv__main").attributes("style")).toContain("32%");

    await wrapper.find('[data-testid="devtools-popout"]').trigger("click");
    expect(openSpy).toHaveBeenCalledWith(
      expect.stringContaining("?surface=graph"),
      "kitsoki-graph-s1",
      "popup,width=760,height=760",
    );

    await wrapper.find('[data-testid="workbench-orient-horizontal"]').trigger("click");
    await flushPromises();
    expect(wrapper.find('[data-testid="devtools-workbench-resizer"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="devtools-workbench-row-resizer"]').exists()).toBe(true);

    await wrapper.find('[data-testid="devtools-dock-bottom"]').trigger("click");
    await flushPromises();
    await wrapper.find('[data-testid="devtools-workbench-row-resizer"]').trigger("keydown", { key: "ArrowUp" });
    await flushPromises();

    expect(wrapper.find(".iv__main--workbench-horizontal").exists()).toBe(true);
    expect(wrapper.find(".iv__main").attributes("style")).toContain("38%");
    expect(JSON.parse(localStorage.getItem("kitsoki:mediaWorkbench") || "{}")).toMatchObject({
      orientation: "horizontal",
      devtoolsDock: "bottom",
      mediaWidthPercent: 46,
      devtoolsWidthPercent: 32,
      devtoolsHeightPercent: 38,
    });

    await wrapper.find('[data-testid="devtools-dock-float"]').trigger("click");
    await flushPromises();

    expect(wrapper.find('[data-testid="floating-devtools-pane"]').exists()).toBe(true);

    wrapper.unmount();
    openSpy.mockRestore();
  });

  it("removes the devtools grid track when trace is hidden in the media workbench", async () => {
    setEmbeddedOverride(false);
    const wrapper = mount(InteractiveView, mountOpts);
    await flushPromises();

    const store = useRunStore();
    store.transcript.push({
      role: "agent",
      text: "Rendered deck",
      typedView: {
        Source: "",
        Elements: [
          {
            Kind: "media",
            MediaHandle: "deck.html",
            MediaKind: "slideshow",
            MediaCaption: "Pinned deck",
          },
        ],
      },
    });
    await nextTick();

    await wrapper.find('[data-testid="media-workbench-toggle"]').trigger("click");
    await wrapper.find('[data-testid="workbench-orient-horizontal"]').trigger("click");
    await wrapper.find('[data-testid="devtools-dock-bottom"]').trigger("click");
    await flushPromises();

    expect(wrapper.find('[data-testid="media-devtools-pane"]').exists()).toBe(true);
    expect(wrapper.find(".iv__main--workbench-horizontal").exists()).toBe(true);
    expect(wrapper.find(".iv__main--devtools-bottom").exists()).toBe(true);

    await wrapper.find('[data-testid="trace-column-toggle"]').trigger("click");
    await flushPromises();

    const main = wrapper.find(".iv__main");
    const style = main.attributes("style") ?? "";
    expect(wrapper.find('[data-testid="media-devtools-pane"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="devtools-workbench-row-resizer"]').exists()).toBe(false);
    expect(main.classes()).not.toContain("iv__main--workbench-horizontal");
    expect(main.classes()).not.toContain("iv__main--devtools-bottom");
    expect(style).toContain(
      "grid-template-columns: minmax(18rem, 42%) 0.55rem minmax(20rem, 1fr);",
    );
    expect(style).toContain("grid-template-rows: minmax(0, 1fr);");
    expect(style).not.toContain("minmax(12rem");

    wrapper.unmount();
  });
});
