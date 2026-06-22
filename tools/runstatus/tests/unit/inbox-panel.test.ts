import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import { setActivePinia, createPinia } from "pinia";
import InboxPanel from "../../src/components/InboxPanel.vue";
import { useInboxStore } from "../../src/stores/inbox.js";

const push = vi.fn();
const route = { params: { sessionId: "web-session-1" } };
vi.mock("vue-router", () => ({
  useRouter: () => ({ push }),
  useRoute: () => route,
}));

const syncGitHubInbox = vi.fn();
const listWork = vi.fn();
vi.mock("../../src/data/live-source.js", () => ({
  LiveSource: vi.fn().mockImplementation(() => ({
    syncGitHubInbox,
    listWork,
  })),
}));

describe("InboxPanel", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    push.mockReset();
    syncGitHubInbox.mockReset();
    syncGitHubInbox.mockResolvedValue({ ok: true, fetched: 2, inserted: 1, skipped: 1, items: [] });
    listWork.mockReset();
    listWork.mockResolvedValue({
      summary: {
        items: 0,
        needs_attention: 0,
        jobs_running: 0,
        jobs_awaiting_input: 0,
        jobs_terminal: 0,
        notifications_unread: 0,
        notifications_action_required: 0,
        pending_drives: 0,
        backgrounded_chats: 0,
      },
      sessions: [],
      items: [],
    });
    route.params.sessionId = "web-session-1";
    document.body.innerHTML = "";
  });

  afterEach(() => {
    document.body.innerHTML = "";
  });

  it("renders active chat work and routes to the public session chat", async () => {
    const inbox = useInboxStore();
    inbox.open = true;
    inbox.workSummary = {
      items: 3,
      needs_attention: 0,
      jobs_running: 0,
      jobs_awaiting_input: 0,
      jobs_terminal: 0,
      notifications_unread: 0,
      notifications_action_required: 0,
      pending_drives: 1,
      dispatching_drives: 1,
      backgrounded_chats: 1,
    };
    inbox.workItems = [
      {
        kind: "pending_drive",
        priority: 65,
        session_id: "web-session-1",
        title: "continue the agent task",
        status: "pending",
        reacquire_tool: "chat.show",
        reacquire_session_id: "web-session-1",
        drive_id: "drive-1",
        chat_id: "chat-1",
        actor: "claude",
        thread: "thread-1",
      },
      {
        kind: "pending_drive",
        priority: 68,
        session_id: "web-session-1",
        title: "dispatching the agent task",
        status: "dispatching",
        reacquire_tool: "chat.show",
        reacquire_session_id: "web-session-1",
        drive_id: "drive-2",
        chat_id: "chat-dispatching",
        actor: "claude",
      },
      {
        kind: "backgrounded_chat",
        priority: 60,
        session_id: "web-session-1",
        title: "Background Claude",
        status: "pty_background",
        reacquire_tool: "chat.show",
        reacquire_session_id: "web-session-1",
        chat_id: "chat-2",
        tmux_session: "kit-bg",
        tmux_host: "devbox",
      },
    ];

    const wrapper = mount(InboxPanel, { attachTo: document.body });
    await flushPromises();

    const rows = document.body.querySelectorAll('[data-testid="work-item"]');
    expect(rows).toHaveLength(3);
    expect(document.body.textContent).toContain("queued");
    expect(document.body.textContent).toContain("dispatching");
    expect(document.body.textContent).toContain("chat");
    expect(document.body.textContent).toContain("continue the agent task");
    expect(document.body.textContent).toContain("dispatching the agent task");
    expect(document.body.textContent).toContain("Background Claude");
    expect(document.body.textContent).toContain("chat chat-1");
    expect(document.body.textContent).toContain("drive drive-1");
    expect(document.body.textContent).toContain("chat chat-dispatching");
    expect(document.body.textContent).toContain("drive drive-2");
    expect(document.body.textContent).toContain("claude");
    expect(document.body.textContent).toContain("thread-1");
    expect(document.body.textContent).toContain("chat chat-2");
    expect(document.body.textContent).toContain("tmux kit-bg");
    expect(document.body.textContent).toContain("devbox");
    expect(document.body.textContent).toContain("open context");
    expect(inbox.workItems[2]?.reacquire_tool).toBe("chat.show");

    (rows[2] as HTMLButtonElement).click();
    await flushPromises();

    expect(inbox.open).toBe(false);
    expect(push).toHaveBeenCalledWith("/s/web-session-1/chat?chat=chat-2");
    wrapper.unmount();
  });

  it("renders focused context for active GitHub notification work", async () => {
    const inbox = useInboxStore();
    inbox.open = true;
    inbox.workSummary = {
      items: 1,
      needs_attention: 1,
      jobs_running: 0,
      jobs_awaiting_input: 0,
      jobs_terminal: 0,
      notifications_unread: 1,
      notifications_action_required: 1,
      pending_drives: 0,
      backgrounded_chats: 0,
    };
    inbox.workItems = [
      {
        kind: "notification",
        priority: 100,
        session_id: "web-session-1",
        title: "PR #42 needs review: Review this",
        body: "Review this\n\nhttps://github.com/acme/repo/pull/42",
        status: "unread",
        notification_id: "notif-pr-42",
        severity: "action_required",
        teleport_state: "foyer",
        teleport_slots: { pr_id: "42", pr_title: "Review this", pr_author: "alice" },
        origin_kind: "external",
        origin_ref: "github:acme/repo/pr/42",
        origin_url: "https://github.com/acme/repo/pull/42",
        reacquire_tool: "notification",
        reacquire_session_id: "web-session-1",
      },
    ];

    const wrapper = mount(InboxPanel, { attachTo: document.body });
    await flushPromises();

    const rows = document.body.querySelectorAll('[data-testid="work-item"]');
    expect(rows).toHaveLength(1);
    expect(document.body.textContent).toContain("PR #42 needs review: Review this");
    expect(document.body.textContent).toContain("Review this");
    expect(document.body.textContent).toContain("https://github.com/acme/repo/pull/42");
    expect(document.body.textContent).toContain("action_required");
    expect(document.body.textContent).toContain("jump");

    wrapper.unmount();
  });

  it("syncs GitHub inbox work for the current session", async () => {
    const inbox = useInboxStore();
    inbox.open = true;

    const wrapper = mount(InboxPanel, { attachTo: document.body });
    await flushPromises();

    const button = document.body.querySelector('[data-testid="inbox-sync-github"]') as HTMLButtonElement;
    expect(button.disabled).toBe(false);
    button.click();
    await flushPromises();

    expect(syncGitHubInbox).toHaveBeenCalledWith("web-session-1", {});
    expect(listWork).toHaveBeenCalled();
    expect(document.body.textContent).toContain("GitHub sync: 1 new, 1 existing");
    wrapper.unmount();
  });
});
