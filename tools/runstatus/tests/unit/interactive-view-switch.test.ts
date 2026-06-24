/**
 * Component test for InteractiveView.vue session switching.
 *
 * Switching directly between two /s/:sessionId/chat routes reuses the same
 * component instance (only the route param changes), so onMounted never
 * re-fires. The view must watch sessionId and re-load, or the chat terminal is
 * left showing the previous session's conversation. The DataSource RPC layer is
 * mocked (no live server, no LLM) and heavy children are stubbed.
 */

import { describe, it, expect, vi, beforeEach } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import { setActivePinia, createPinia } from "pinia";
import type { TurnResult } from "../../src/types.js";

function sessionFor(id: string) {
  return {
    session_id: id,
    app_id: "demo",
    current_state: "idle",
    turn: 0,
    started_at: "2026-06-04T00:00:00Z",
    terminal: false,
  };
}

function viewFor(id: string): TurnResult {
  return {
    mode: "transitioned",
    state: "idle",
    view: `Opening for ${id}`,
    typed_view: { Source: "", Elements: [] },
    allowed_intents: [],
    intents: [],
    turn_number: 0,
  };
}

const getSession = vi.fn((id: string) => Promise.resolve(sessionFor(id)));
const view = vi.fn((id: string) => Promise.resolve(viewFor(id)));

const dataSource = {
  getSession,
  getApp: vi.fn().mockResolvedValue({ id: "demo", name: "Demo", root: "idle", states: {} }),
  getMermaid: vi.fn().mockResolvedValue({ source: "", node_map: {} }),
  getTrace: vi.fn().mockResolvedValue({ events: [], last_turn: 0 }),
  subscribe: vi.fn().mockReturnValue(() => {}),
  view,
};

vi.mock("../../src/data/source.js", () => ({
  createDataSource: () => dataSource,
}));

import InteractiveView from "../../src/views/InteractiveView.vue";
import { useRunStore } from "../../src/stores/run.js";
import { autoNavDone } from "../../src/lib/auto-nav.js";

const mountOpts = {
  props: { sessionId: "s1" },
  global: {
    stubs: {
      RouterLink: { props: ["to"], template: '<a :href="to"><slot /></a>' },
      StateDiagram: true,
      TraceTimeline: true,
      ChatTranscript: true,
      InputBar: true,
    },
  },
};

describe("InteractiveView — session switching", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    getSession.mockClear();
    view.mockClear();
    sessionStorage.clear();
  });

  it("spends the per-tab auto-nav guard on mount (so '← Stories' can't bounce back)", async () => {
    // A tab that opens straight into a session view must mark the guard, or the
    // first HomeView mount (one live session) would redirect the user back in.
    expect(autoNavDone()).toBe(false);
    const wrapper = mount(InteractiveView, mountOpts);
    await flushPromises();
    expect(autoNavDone()).toBe(true);
    wrapper.unmount();
  });

  it("re-loads (and does not mix transcripts) when the sessionId prop changes", async () => {
    const wrapper = mount(InteractiveView, mountOpts);
    await flushPromises();

    const store = useRunStore();
    expect(store.transcript).toHaveLength(1);
    expect(store.transcript[0]!.text).toBe("Opening for s1");

    // Navigate directly to another session's chat (param-only change → the
    // component is reused, onMounted does not fire again).
    await wrapper.setProps({ sessionId: "s2" });
    await flushPromises();

    expect(getSession).toHaveBeenCalledWith("s2");
    expect(store.transcript).toHaveLength(1);
    expect(store.transcript[0]!.text).toBe("Opening for s2");
    expect(store.transcript.some((e) => e.text.includes("s1"))).toBe(false);

    wrapper.unmount();
  });
});
