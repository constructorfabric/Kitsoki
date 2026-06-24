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
});
