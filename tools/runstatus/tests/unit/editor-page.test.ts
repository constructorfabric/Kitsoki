/**
 * Component tests for src/views/EditorPage.vue. The live-source RPCs are mocked
 * (no server, no LLM); vue-router's useRoute/useRouter are stubbed with a
 * mutable query object so room selection (a query-param replace) can be driven.
 */
import { describe, it, expect, vi, beforeEach } from "vitest";
import { reactive } from "vue";
import { flushPromises, mount } from "@vue/test-utils";
import type { RoomSummary, RoomDetail } from "../../src/data/editor.js";

vi.mock("../../src/data/source.js", () => ({
  createDataSource: () => ({ artifactUrl: (h: string) => `/fake/${h}` }),
}));

const editorRooms = vi.fn<[string], Promise<RoomSummary[]>>();
const editorRoom = vi.fn<[string, string], Promise<RoomDetail>>();
const editorOracles = vi.fn().mockResolvedValue({ contracts: [], cassette_globs: [] });
const editorCassettes = vi.fn().mockResolvedValue([]);
const listStories = vi.fn().mockResolvedValue([
  { path: "/repo/stories/prd/app.yaml", app_id: "prd", title: "PRD", active_sessions: [] },
]);
const metaModes = vi.fn().mockResolvedValue([]);

vi.mock("../../src/data/live-source.js", () => ({
  LiveSource: vi.fn().mockImplementation(() => ({
    editorRooms,
    editorRoom,
    editorOracles,
    editorCassettes,
    listStories,
    metaModes,
  })),
}));

const route = reactive({ query: {} as Record<string, string> });
const replace = vi.fn((loc: { query: Record<string, string> }) => {
  route.query = { ...loc.query };
});
vi.mock("vue-router", () => ({
  useRoute: () => route,
  useRouter: () => ({ replace }),
  RouterLink: { props: ["to"], template: "<a><slot /></a>" },
}));

import { createPinia, setActivePinia } from "pinia";
import EditorPage from "../../src/views/EditorPage.vue";

const ROOMS: RoomSummary[] = [
  { id: "idle", label: "Idle", distance: 0, has_oracle: false },
  { id: "clarifying", label: "Clarifying", distance: 1, has_oracle: true },
  { id: "brief", label: "Brief", distance: 2, has_oracle: false },
];

function detail(over: Partial<RoomDetail> = {}): RoomDetail {
  return {
    id: "clarifying",
    label: "Clarifying",
    distance: 1,
    on_enter: [{ kind: "invoke", invoke: "host.oracle.decide" }],
    world_keys: [{ name: "idea", type: "string", direction: "read" }],
    intents: [{ name: "submit" }],
    transitions: [{ intent: "submit", target: "brief" }],
    view: [{ Kind: "prose", Source: "Clarify the idea." }],
    source_ref: { path: "/repo/stories/prd/app.yaml", line: 1 },
    ...over,
  };
}

beforeEach(() => {
  setActivePinia(createPinia());
  route.query = { story: "/repo/stories/prd/app.yaml" };
  editorRooms.mockResolvedValue(ROOMS);
  editorRoom.mockResolvedValue(detail());
  replace.mockClear();
});

describe("EditorPage", () => {
  it("renders the room list in BFS order and auto-selects the first room", async () => {
    const w = mount(EditorPage);
    await flushPromises();
    const items = w.findAll('[data-testid="editor-room-item"]');
    expect(items.map((i) => i.attributes("data-room-id"))).toEqual([
      "idle",
      "clarifying",
      "brief",
    ]);
    // auto-select first room => replace called with room=idle
    expect(replace).toHaveBeenCalledWith(
      expect.objectContaining({ query: expect.objectContaining({ room: "idle" }) })
    );
    w.unmount();
  });

  it("loads room detail (hook + domain + workbench) for the selected room", async () => {
    route.query = { story: "/repo/stories/prd/app.yaml", room: "clarifying" };
    const w = mount(EditorPage);
    await flushPromises();
    expect(w.find('[data-testid="editor-hook"]').exists()).toBe(true);
    expect(w.find('[data-testid="editor-domain-model"]').exists()).toBe(true);
    expect(w.find('[data-testid="editor-oracle-workbench"]').exists()).toBe(true);
    const viewer = w.find('[data-testid="editor-story-viewer"]');
    expect(viewer.exists()).toBe(true);
    // Pin the wire contract: the room detail's view elements are real
    // app.ViewElement (PascalCase Kind/Source) that ViewElement.vue renders.
    // Assert actual body content makes it onto the page, not just the container.
    expect(viewer.text()).toContain("Clarify the idea.");
    // IDE deep-link points at vscode://file/<path>:<line>.
    expect(w.find('[data-testid="editor-ide-link"]').attributes("href")).toBe(
      "vscode://file//repo/stories/prd/app.yaml:1"
    );
    w.unmount();
  });

  it("selecting a transition target updates the room query param", async () => {
    route.query = { story: "/repo/stories/prd/app.yaml", room: "clarifying" };
    const w = mount(EditorPage);
    await flushPromises();
    await w.find('[data-testid="editor-domain-transition-target"]').trigger("click");
    expect(replace).toHaveBeenCalledWith(
      expect.objectContaining({ query: expect.objectContaining({ room: "brief" }) })
    );
    w.unmount();
  });

  it("shows a meta-chat placeholder when no session is active", async () => {
    const w = mount(EditorPage);
    await flushPromises();
    expect(w.find('[data-testid="editor-meta-placeholder"]').exists()).toBe(true);
    w.unmount();
  });

  it("reload button re-fetches the room list", async () => {
    route.query = { story: "/repo/stories/prd/app.yaml", room: "idle" };
    const w = mount(EditorPage);
    await flushPromises();
    editorRooms.mockClear();
    await w.find('[data-testid="editor-reload"]').trigger("click");
    await flushPromises();
    expect(editorRooms).toHaveBeenCalled();
    w.unmount();
  });
});
