/**
 * Regression test for ChatSurface.vue's transcript binding.
 *
 * ChatSurface (the VS Code surface-decomposition chat) must feed
 * store.chatEntries — NOT the raw store.transcript — to ChatTranscript, so each
 * user turn carries its routing provenance and the inline routing chip renders.
 * A prior refactor bound the bare transcript here, silently dropping the chip on
 * this surface while InteractiveView kept it. This test mounts the surface, then
 * seeds a routed user turn and asserts the chip surfaces — it fails the moment
 * anyone rebinds to store.transcript again.
 *
 * The DataSource RPC layer is mocked (no live server, no LLM); only the heavy
 * input child is stubbed so ChatTranscript renders for real.
 */

import { describe, it, expect, vi, beforeEach } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import { nextTick } from "vue";
import { setActivePinia, createPinia } from "pinia";
import type { TraceEvent, TurnResult } from "../../src/types.js";

const SESSION = {
  session_id: "sess-1",
  app_id: "git-ops",
  current_state: "root/menu",
  turn: 0,
  started_at: "2026-06-19T00:00:00Z",
  terminal: false,
};

const OPENING_VIEW: TurnResult = {
  mode: "transitioned",
  state: "root/menu",
  view: "What would you like to do?",
  typed_view: { Source: "", Elements: [] },
  allowed_intents: [],
  intents: [],
  turn_number: 0,
};

// A fake DataSource: getCurrentSession hands the surface a live session so it
// adopts it and renders the chat column (the chip only lives there).
const dataSource = {
  getCurrentSession: vi.fn().mockResolvedValue("sess-1"),
  subscribeCurrentSession: vi.fn().mockReturnValue(() => {}),
  getSession: vi.fn().mockResolvedValue(SESSION),
  getApp: vi.fn().mockResolvedValue({ id: "git-ops", name: "Git Ops", root: "root", states: {} }),
  getMermaid: vi.fn().mockResolvedValue({ source: "", node_map: {} }),
  getTrace: vi.fn().mockResolvedValue({ events: [], last_turn: 0 }),
  subscribe: vi.fn().mockReturnValue(() => {}),
  view: vi.fn().mockResolvedValue(OPENING_VIEW),
};

vi.mock("../../src/data/source.js", () => ({
  createDataSource: () => dataSource,
}));

import ChatSurface from "../../src/surfaces/ChatSurface.vue";
import { useRunStore } from "../../src/stores/run.js";

function traceEvent(over: Partial<TraceEvent>): TraceEvent {
  return {
    time: "2026-06-19T00:00:00Z",
    level: "info",
    msg: "",
    session_id: "sess-1",
    turn: 0,
    state_path: "root/menu",
    attrs: {},
    ...over,
  };
}

const mountOpts = {
  global: {
    stubs: {
      // ChatTranscript renders for real (it owns the chip); the input bar and
      // activity feed are irrelevant to the binding under test.
      InputBar: true,
      ActivityFeed: true,
    },
  },
};

describe("ChatSurface — routing chip binding", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
  });

  it("feeds chatEntries so a routed user turn renders the inline routing chip", async () => {
    const wrapper = mount(ChatSurface, mountOpts);
    await flushPromises(); // onMounted: adopt → hydrate → loadInitialView

    // The surface adopted the session and rendered the chat column.
    expect(wrapper.find("[data-testid='chat-section']").exists()).toBe(true);

    // A free-text user turn lands with its provenance in the event log — exactly
    // what sendText + the SSE trace produce.
    const store = useRunStore();
    store.transcript.push({ role: "user", text: "commit my work", turn: 1 });
    store.events.push(
      traceEvent({
        turn: 1,
        msg: "turn.start",
        attrs: { routed_by: "semantic", match_type: "leading-verb:commit", confidence: 0.95 },
      }),
      traceEvent({ turn: 1, msg: "machine.transition", attrs: { intent: "git.commit" } })
    );
    await nextTick();

    // The chip surfaces — proving ChatSurface bound chatEntries, not the bare
    // transcript (which would carry no routing and render no chip).
    const chip = wrapper.find("[data-testid='routing-chip']");
    expect(chip.exists()).toBe(true);
    expect(chip.find(".chat-routing__intent").text()).toBe("git.commit");
    expect(chip.find(".chat-routing__tier").text()).toBe("semantic");
  });
});
