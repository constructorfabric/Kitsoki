/**
 * Component tests for the RunView.vue reload control + breadcrumb.
 *
 * The live-source RPC layer is mocked (no live server, no LLM): vi.mock
 * replaces both the snapshot-capable DataSource factory (so hydrate() never
 * opens a real EventSource) and the LiveSource that StoryFreshness uses for
 * checkStaleness / reloadSession calls. StoryFreshness is stubbed with a
 * thin harness component so we can fire the reload callbacks without driving
 * the full polling cycle.
 */

import { describe, it, expect, vi, beforeEach } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import { setActivePinia, createPinia } from "pinia";
import { defineComponent, h } from "vue";
import type { ReloadResult } from "../../src/data/live-source.js";

// ── Mocks ───────────────────────────────────────────────────────────────────

const reloadSession = vi.fn<[string], Promise<ReloadResult>>();
const checkStaleness = vi.fn<[string], Promise<{ stale: boolean; diff: string }>>();

vi.mock("../../src/data/live-source.js", () => ({
  LiveSource: vi.fn().mockImplementation(() => ({ reloadSession, checkStaleness })),
}));

// createDataSource() backs the store's hydrate(); a no-op stub keeps the view
// out of a real server / EventSource. subscribe returns a no-op unsubscribe.
const dataSource = {
  getSession: vi
    .fn()
    .mockResolvedValue({
      session_id: "s1",
      app_id: "demo",
      current_state: "idle",
      turn: 0,
      started_at: "2026-06-04T00:00:00Z",
      terminal: false,
    }),
  getApp: vi.fn().mockResolvedValue({ id: "demo", name: "Demo Story", root: "idle", states: {} }),
  getMermaid: vi.fn().mockResolvedValue({ source: "", node_map: {} }),
  getTrace: vi.fn().mockResolvedValue({ events: [], last_turn: 0 }),
  subscribe: vi.fn().mockReturnValue(() => {}),
};

vi.mock("../../src/data/source.js", () => ({
  createDataSource: () => dataSource,
}));

// StoryFreshness stub: renders two test-only buttons that simulate the reload
// callbacks so RunView's warning logic can be exercised without a polling cycle.
const StoryFreshnessStub = defineComponent({
  props: {
    sessionId: String,
    onReloaded: Function,
    onReloadError: Function,
  },
  setup(props) {
    return () => h("div", { "data-testid": "freshness-stub" }, [
      h("button", {
        "data-testid": "stub-reload-ok",
        onClick: () => props.onReloaded?.(true),
      }, "reload-ok"),
      h("button", {
        "data-testid": "stub-reload-removed",
        onClick: () => props.onReloaded?.(false),
      }, "reload-removed"),
    ]);
  },
});

// Imported after the mocks are registered.
import RunView from "../../src/views/RunView.vue";

const mountOpts = {
  props: { sessionId: "s1" },
  global: {
    stubs: {
      RouterLink: { props: ["to"], template: "<a :href=\"to\"><slot /></a>" },
      StateDiagram: true,
      TraceTimeline: true,
      StoryFreshness: StoryFreshnessStub,
    },
  },
};

describe("RunView — reload control + breadcrumb", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    reloadSession.mockReset();
    checkStaleness.mockReset();
    reloadSession.mockResolvedValue({ ok: true, prev_state_exists: true });
    checkStaleness.mockResolvedValue({ stale: false, diff: "" });
  });

  it("renders a breadcrumb with the story title linking back to /", async () => {
    const wrapper = mount(RunView, mountOpts);
    await flushPromises();

    const crumb = wrapper.find("[data-testid='breadcrumb']");
    expect(crumb.exists()).toBe(true);
    expect(crumb.text()).toContain("Demo Story");
    expect(crumb.find("a").attributes("href")).toBe("/");
    wrapper.unmount();
  });

  it("offers a 'Drive (chat)' link to the chat surface while the session is live", async () => {
    const wrapper = mount(RunView, mountOpts);
    await flushPromises();

    const drive = wrapper.find("[data-testid='drive-link']");
    expect(drive.exists()).toBe(true);
    expect(drive.attributes("href")).toBe("/s/s1/chat");
    wrapper.unmount();
  });

  it("hides the 'Drive (chat)' link once the session is terminal", async () => {
    dataSource.getSession.mockResolvedValueOnce({
      session_id: "s1",
      app_id: "demo",
      current_state: "__exit__done",
      turn: 3,
      started_at: "2026-06-04T00:00:00Z",
      terminal: true,
    });
    const wrapper = mount(RunView, mountOpts);
    await flushPromises();

    expect(wrapper.find("[data-testid='drive-link']").exists()).toBe(false);
    wrapper.unmount();
  });

  it("mounts the StoryFreshness widget in the toolbar", async () => {
    const wrapper = mount(RunView, mountOpts);
    await flushPromises();

    // The parent template passes data-testid="story-freshness-widget" which
    // overwrites the stub's own testid via Vue's attribute fallthrough.
    expect(wrapper.find("[data-testid='story-freshness-widget']").exists()).toBe(true);
    wrapper.unmount();
  });

  it("shows no warning when freshness callback reports prev_state_exists true", async () => {
    const wrapper = mount(RunView, mountOpts);
    await flushPromises();

    await wrapper.find("[data-testid='stub-reload-ok']").trigger("click");
    await flushPromises();

    expect(wrapper.find("[data-testid='reload-warning']").exists()).toBe(false);
    wrapper.unmount();
  });

  it("surfaces the staying-put warning when prev_state_exists is false", async () => {
    const wrapper = mount(RunView, mountOpts);
    await flushPromises();

    await wrapper.find("[data-testid='stub-reload-removed']").trigger("click");
    await flushPromises();

    const warn = wrapper.find("[data-testid='reload-warning']");
    expect(warn.exists()).toBe(true);
    expect(warn.text()).toContain("current state removed; staying put");
    wrapper.unmount();
  });
});
