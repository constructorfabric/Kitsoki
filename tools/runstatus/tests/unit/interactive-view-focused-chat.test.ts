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
  driveOperation: vi.fn(
    (): Promise<TurnResult> =>
      Promise.resolve({
        mode: "completed",
        state: "__exit__done",
        view: "Done",
        typed_view: { Source: "", Elements: [] },
        allowed_intents: [],
        intents: [],
        turn_number: 2,
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
  artifactUrl: vi.fn((handle: string): string => `/artifact/${encodeURIComponent(handle)}`),
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
    dataSource.driveOperation.mockClear();
    dataSource.artifactUrl.mockClear();
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
    dataSource.getTrace.mockReset();
    dataSource.getTrace.mockResolvedValue({ events: [], last_turn: 0 });
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

  it("renders an operation banner from the trace operation handle", async () => {
    route.query = {};
    dataSource.getTrace.mockResolvedValueOnce({
      last_turn: 1,
      events: [
        {
          time: "2026-01-01T00:00:01Z",
          level: "info",
          msg: "world.update",
          session_id: "s1",
          turn: 1,
          state_path: "idle",
          attrs: {
            set: {
              operation_run: {
                operation_id: "bf__capsule_demo",
                policy_id: "bf__capsule_demo",
                title: "Capsule bugfix",
                status: "running",
                mode: "autonomous",
                execution_mode: "one-shot",
                phase: "reproduce_bug",
                from: "idle",
                to: "bugfix.reproduce",
                run_in_background: true,
              },
            },
          },
        },
      ],
    });

    const wrapper = mount(InteractiveView, mountOpts);
    await flushPromises();

    const banner = wrapper.find('[data-testid="operation-run-banner"]');
    expect(banner.exists()).toBe(true);
    expect(wrapper.find('[data-testid="operation-run-title"]').text()).toBe("Capsule bugfix");
    expect(wrapper.find('[data-testid="operation-run-status"]').text()).toBe("running in background");
    expect(banner.text()).toContain("phase reproduce bug");
    const summary = wrapper.find('[data-testid="operation-run-summary"]');
    expect(summary.text()).toContain("mode autonomous");
    expect(summary.text()).toContain("execution one-shot");
    expect(summary.text()).toContain("phase reproduce bug");
    expect(summary.text()).toContain("route idle -> bugfix.reproduce");
    expect(wrapper.find('[data-testid="operation-run-drive"]').exists()).toBe(true);
    wrapper.unmount();
  });

  it("drives a running operation from the in-session banner", async () => {
    route.query = {};
    dataSource.getTrace
      .mockResolvedValueOnce({
        last_turn: 1,
        events: [
          {
            time: "2026-01-01T00:00:01Z",
            level: "info",
            msg: "world.update",
            session_id: "s1",
            turn: 1,
            state_path: "idle",
            attrs: {
              set: {
                operation_run: {
                  operation_id: "bf__capsule_demo",
                  policy_id: "bf__capsule_demo",
                  title: "Capsule bugfix",
                  status: "running",
                  mode: "supervised",
                  phase: "run_regression",
                  run_in_background: true,
                },
              },
            },
          },
        ],
      })
      .mockResolvedValueOnce({
        last_turn: 3,
        events: [
          {
            time: "2026-01-01T00:00:03Z",
            level: "info",
            msg: "operation.completed",
            session_id: "s1",
            turn: 3,
            state_path: "__exit__done",
            attrs: {
              operation_id: "bf__capsule_demo",
              policy_id: "bf__capsule_demo",
              title: "Capsule bugfix",
              status: "completed",
              terminal_state: "__exit__done",
              terminal_artifact: "artifacts/qa-report.md",
              terminal_artifact_handle: "qa-report#abc123",
            },
          },
        ],
      });
    dataSource.driveOperation.mockResolvedValueOnce({
      mode: "completed",
      state: "__exit__done",
      view: "Done",
      typed_view: { Source: "", Elements: [] },
      allowed_intents: [],
      intents: [],
      turn_number: 3,
    });

    const wrapper = mount(InteractiveView, mountOpts);
    await flushPromises();

    await wrapper.find('[data-testid="operation-run-drive"]').trigger("click");
    await flushPromises();

    expect(dataSource.driveOperation).toHaveBeenCalledWith("s1");
    expect(dataSource.getTrace).toHaveBeenLastCalledWith("s1", { since_turn: 2 });
    expect(wrapper.find('[data-testid="current-state"]').text()).toBe("__exit__done");
    expect(wrapper.find('[data-testid="operation-run-drive"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="operation-run-status"]').text()).toBe("completed");
    expect(wrapper.find('[data-testid="operation-run-artifact"]').text()).toContain("artifacts/qa-report.md");
    const openArtifact = wrapper.find('[data-testid="operation-run-artifact-open"]');
    expect(openArtifact.exists()).toBe(true);
    expect(openArtifact.attributes("href")).toBe("/artifact/qa-report%23abc123");
    expect(openArtifact.attributes("target")).toBe("_blank");
    wrapper.unmount();
  });

  it("does not render Drive for interactive running operations", async () => {
    route.query = {};
    dataSource.getTrace.mockResolvedValueOnce({
      last_turn: 1,
      events: [
        {
          time: "2026-01-01T00:00:01Z",
          level: "info",
          msg: "world.update",
          session_id: "s1",
          turn: 1,
          state_path: "review",
          attrs: {
            set: {
              operation_run: {
                operation_id: "guided_review",
                policy_id: "guided_review",
                title: "Guided review",
                status: "running",
                mode: "interactive",
                run_in_background: true,
              },
            },
          },
        },
      ],
    });

    const wrapper = mount(InteractiveView, mountOpts);
    await flushPromises();

    expect(wrapper.find('[data-testid="operation-run-status"]').text()).toBe("running in background");
    expect(wrapper.find('[data-testid="operation-run-drive"]').exists()).toBe(false);
    wrapper.unmount();
  });

  it("renders waiting operation details from the trace operation handle", async () => {
    route.query = {};
    dataSource.getTrace.mockResolvedValueOnce({
      last_turn: 2,
      events: [
        {
          time: "2026-01-01T00:00:01Z",
          level: "info",
          msg: "world.update",
          session_id: "s1",
          turn: 2,
          state_path: "__exit__needs-human",
          attrs: {
            set: {
              operation_run: {
                operation_id: "bf__capsule_demo",
                policy_id: "bf__capsule_demo",
                title: "Capsule bugfix",
                status: "waiting",
                terminal_state: "__exit__needs-human",
                stop_reason: "needs-human",
                stop_detail: "Regression gate was never RED.",
              },
            },
          },
        },
      ],
    });

    const wrapper = mount(InteractiveView, mountOpts);
    await flushPromises();

    const banner = wrapper.find('[data-testid="operation-run-banner"]');
    expect(banner.attributes("data-operation-status")).toBe("waiting");
    expect(wrapper.find('[data-testid="operation-run-status"]').text()).toBe("waiting for needs-human");
    expect(wrapper.find('[data-testid="operation-run-detail"]').text()).toContain("Regression gate was never RED.");
    expect(banner.text()).toContain("parked at __exit__needs-human");
    expect(wrapper.find('[data-testid="operation-run-drive"]').exists()).toBe(false);
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
