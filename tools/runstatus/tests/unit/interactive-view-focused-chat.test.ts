import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import { setActivePinia, createPinia } from "pinia";
import type { TurnResult } from "../../src/types.js";
import { useProposalsStore } from "../../src/stores/proposals.js";

const route = vi.hoisted(() => ({
  path: "/s/s1/chat",
  query: { chat: "chat-1" } as Record<string, string>,
  params: { sessionId: "s1" },
}));
const replace = vi.hoisted(() => vi.fn());
const showChat = vi.hoisted(() => vi.fn());

const dataSource = {
  getSession: vi.fn().mockResolvedValue({
    session_id: "s1",
    app_id: "demo",
    current_state: "idle",
    turn: 0,
    started_at: "2026-06-04T00:00:00Z",
    terminal: false,
  }),
  getApp: vi.fn().mockResolvedValue({ id: "demo", name: "Demo", root: "idle", states: {} }),
  getMermaid: vi.fn().mockResolvedValue({ source: "", node_map: {} }),
  getTrace: vi.fn().mockResolvedValue({ events: [], last_turn: 0 }),
  subscribe: vi.fn().mockReturnValue(() => {}),
  view: vi.fn(
    (): Promise<TurnResult> =>
      Promise.resolve({
        mode: "transitioned",
        state: "idle",
        view: "Opening",
        typed_view: { Source: "", Elements: [] },
        allowed_intents: [],
        intents: [],
        turn_number: 0,
      }),
  ),
  submit: vi.fn(
    (): Promise<TurnResult> =>
      Promise.resolve({
        mode: "transitioned",
        state: "queued",
        view: "Queued",
        typed_view: { Source: "", Elements: [] },
        allowed_intents: [],
        intents: [],
        turn_number: 1,
      }),
  ),
  listWork: vi.fn().mockResolvedValue({
    summary: {
      items: 1,
      needs_attention: 0,
      jobs_running: 0,
      jobs_awaiting_input: 0,
      jobs_terminal: 0,
      notifications_unread: 0,
      notifications_action_required: 0,
      pending_drives: 1,
      backgrounded_chats: 0,
    },
    sessions: [],
    items: [
      {
        kind: "pending_drive",
        priority: 65,
        session_id: "s1",
        title: "Queued subagent",
        status: "pending",
        reacquire_tool: "chat.show",
        reacquire_session_id: "s1",
        chat_id: "chat-queued",
      },
    ],
  }),
  syncGitHubInbox: vi.fn().mockResolvedValue({
    ok: true,
    session_id: "s1",
    fetched: 0,
    inserted: 0,
    skipped: 0,
    items: [],
  }),
};

vi.mock("../../src/data/source.js", () => ({
  createDataSource: () => dataSource,
}));

vi.mock("../../src/data/live-source.js", () => ({
  TurnCancelledError: class TurnCancelledError extends Error {},
  LiveSource: vi.fn().mockImplementation(() => ({
    showChat,
  })),
}));

vi.mock("vue-router", () => ({
  useRoute: () => route,
  useRouter: () => ({ replace }),
  RouterLink: { props: ["to"], template: '<a :href="to"><slot /></a>' },
}));

import InteractiveView from "../../src/views/InteractiveView.vue";

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
        template: '<div data-testid="meta-launcher" :data-placement="placement || \'floating\'"></div>',
      },
    },
  },
};

describe("InteractiveView focused chat context", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    showChat.mockReset();
    dataSource.submit.mockClear();
    dataSource.listWork.mockClear();
    dataSource.syncGitHubInbox.mockClear();
    showChat.mockResolvedValue({
      ok: true,
      context: {
        session_id: "s1",
      },
      chat: {
        id: "chat-1",
        app_id: "demo",
        room: "agent",
        scope_key: "\u0000session=s1\u0000scope",
        display_scope_key: "scope",
        title: "Background Claude",
        status: "active",
        created_at_unix_micro: 1,
        updated_at_unix_micro: 2,
        last_active_at_unix_micro: 3,
      },
      pty: {
        chat_id: "chat-1",
        tmux_session: "kit-bg",
        tmux_host: "devbox",
        mode: "pty_background",
        created_at_unix_micro: 4,
        updated_at_unix_micro: 5,
      },
      messages: [
        { chat_id: "chat-1", seq: 0, role: "user", content: "check the flaky test", created_at_unix_micro: 6 },
        { chat_id: "chat-1", seq: 1, role: "assistant", content: "the failure is in setup", created_at_unix_micro: 7 },
      ],
    });
    replace.mockReset();
    route.query = { chat: "chat-1" };
    sessionStorage.clear();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("loads and renders focused context from the chat query", async () => {
    const wrapper = mount(InteractiveView, mountOpts);
    await flushPromises();

    expect(showChat).toHaveBeenCalledWith("s1", "chat-1");
    expect(wrapper.find('[data-testid="focused-chat"]').text()).toContain("Background Claude");
    expect(wrapper.find('[data-testid="focused-chat"]').text()).toContain("session s1");
    expect(wrapper.find('[data-testid="focused-chat"]').text()).toContain("scope scope");
    expect(wrapper.find('[data-testid="focused-chat"]').text()).not.toContain("\u0000session=s1");
    expect(wrapper.find('[data-testid="focused-chat"]').text()).toContain("tmux kit-bg");
    expect(wrapper.find('[data-testid="focused-chat"]').text()).toContain("check the flaky test");
    expect(wrapper.find('[data-testid="focused-chat"]').text()).toContain("the failure is in setup");

    await wrapper.find('[data-testid="focused-chat-close"]').trigger("click");
    expect(replace).toHaveBeenCalledWith({ path: "/s/s1/chat", query: {} });
    wrapper.unmount();
  });

  it("seeds proposal review rows from the proposal query and clears only that key", async () => {
    route.query = {
      inbox: "1",
      proposal: JSON.stringify({
        id: "demo-query-proposal",
        kind: "write_mode",
        title: "May I edit README.md?",
        detail: "Proposed doc cleanup",
      }),
    };

    const wrapper = mount(InteractiveView, mountOpts);
    await flushPromises();

    const proposals = useProposalsStore();
    expect(proposals.queue).toHaveLength(1);
    expect(proposals.queue[0]?.id).toBe("demo-query-proposal");
    expect(proposals.queue[0]?.kind).toBe("write_mode");
    expect(replace).toHaveBeenCalledWith({
      path: "/s/s1/chat",
      query: { inbox: "1" },
    });
    wrapper.unmount();
  });

  it("keeps the newest focused chat response when session switches race", async () => {
    let resolveFirst: (value: unknown) => void = () => {};
    let resolveSecond: (value: unknown) => void = () => {};
    showChat
      .mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            resolveFirst = resolve;
          }),
      )
      .mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            resolveSecond = resolve;
          }),
      );

    const wrapper = mount(InteractiveView, mountOpts);
    await flushPromises();
    expect(showChat).toHaveBeenCalledWith("s1", "chat-1");

    await wrapper.setProps({ sessionId: "s2" });
    await flushPromises();
    expect(showChat).toHaveBeenCalledWith("s2", "chat-1");

    resolveSecond({
      ok: true,
      context: { session_id: "s2" },
      chat: {
        id: "chat-1",
        app_id: "demo",
        room: "agent",
        scope_key: "scope",
        display_scope_key: "new-scope",
        title: "Newer focused chat",
        status: "active",
        created_at_unix_micro: 1,
        updated_at_unix_micro: 2,
        last_active_at_unix_micro: 3,
      },
      messages: [
        { chat_id: "chat-1", seq: 0, role: "assistant", content: "newer context", created_at_unix_micro: 4 },
      ],
    });
    await flushPromises();

    expect(wrapper.find('[data-testid="focused-chat"]').text()).toContain("Newer focused chat");
    expect(wrapper.find('[data-testid="focused-chat"]').text()).toContain("session s2");
    expect(wrapper.find('[data-testid="focused-chat"]').text()).toContain("scope new-scope");

    resolveFirst({
      ok: true,
      context: { session_id: "s1" },
      chat: {
        id: "chat-1",
        app_id: "demo",
        room: "agent",
        scope_key: "scope",
        title: "Stale focused chat",
        status: "active",
        created_at_unix_micro: 1,
        updated_at_unix_micro: 2,
        last_active_at_unix_micro: 3,
      },
      messages: [
        { chat_id: "chat-1", seq: 0, role: "assistant", content: "stale context", created_at_unix_micro: 4 },
      ],
    });
    await flushPromises();

    expect(wrapper.find('[data-testid="focused-chat"]').text()).toContain("Newer focused chat");
    expect(wrapper.find('[data-testid="focused-chat"]').text()).not.toContain("Stale focused chat");
    expect(wrapper.find('[data-testid="focused-chat"]').text()).not.toContain("stale context");
    wrapper.unmount();
  });

  it("refreshes active work after a submitted turn", async () => {
    route.query = {};
    const wrapper = mount(InteractiveView, {
      ...mountOpts,
      global: {
        ...mountOpts.global,
        stubs: {
          ...mountOpts.global.stubs,
          InputBar: {
            template:
              '<button data-testid="submit-queue" @click="$emit(\'intent\', \'queue\', {}, \'Queue\')">queue</button>',
          },
        },
      },
    });
    await flushPromises();
    dataSource.listWork.mockClear();

    await wrapper.find('[data-testid="submit-queue"]').trigger("click");
    await flushPromises();

    expect(dataSource.submit).toHaveBeenCalledWith("s1", "queue", {});
    expect(dataSource.listWork).toHaveBeenCalledTimes(1);
    wrapper.unmount();
  });

  it("polls GitHub inbox work while viewing a session and stops on unmount", async () => {
    vi.useFakeTimers();
    route.query = {};
    const wrapper = mount(InteractiveView, mountOpts);
    await flushPromises();

    expect(dataSource.syncGitHubInbox).toHaveBeenCalledWith("s1", {});

    await vi.advanceTimersByTimeAsync(5 * 60 * 1000);
    expect(dataSource.syncGitHubInbox).toHaveBeenCalledTimes(2);

    wrapper.unmount();
    await vi.advanceTimersByTimeAsync(5 * 60 * 1000);
    expect(dataSource.syncGitHubInbox).toHaveBeenCalledTimes(2);
  });
});
