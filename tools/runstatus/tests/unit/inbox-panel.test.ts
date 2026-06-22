import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import { setActivePinia, createPinia } from "pinia";
import InboxPanel from "../../src/components/InboxPanel.vue";
import { useInboxStore } from "../../src/stores/inbox.js";

const push = vi.fn();
vi.mock("vue-router", () => ({
  useRouter: () => ({ push }),
}));

vi.mock("../../src/data/live-source.js", () => ({
  LiveSource: vi.fn().mockImplementation(() => ({})),
}));

describe("InboxPanel", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    push.mockReset();
    document.body.innerHTML = "";
  });

  afterEach(() => {
    document.body.innerHTML = "";
  });

  it("renders active chat work and routes to the public session chat", async () => {
    const inbox = useInboxStore();
    inbox.open = true;
    inbox.workSummary = {
      items: 2,
      needs_attention: 0,
      jobs_running: 0,
      jobs_awaiting_input: 0,
      jobs_terminal: 0,
      notifications_unread: 0,
      notifications_action_required: 0,
      pending_drives: 1,
      backgrounded_chats: 1,
    };
    inbox.workItems = [
      {
        kind: "pending_drive",
        priority: 65,
        session_id: "web-session-1",
        title: "continue the agent task",
        status: "pending",
        reacquire_tool: "session",
        reacquire_session_id: "web-session-1",
        drive_id: "drive-1",
        chat_id: "chat-1",
      },
      {
        kind: "backgrounded_chat",
        priority: 60,
        session_id: "web-session-1",
        title: "Background Claude",
        status: "pty_background",
        reacquire_tool: "session",
        reacquire_session_id: "web-session-1",
        chat_id: "chat-2",
      },
    ];

    const wrapper = mount(InboxPanel, { attachTo: document.body });
    await flushPromises();

    const rows = document.body.querySelectorAll('[data-testid="work-item"]');
    expect(rows).toHaveLength(2);
    expect(document.body.textContent).toContain("queued");
    expect(document.body.textContent).toContain("chat");
    expect(document.body.textContent).toContain("continue the agent task");
    expect(document.body.textContent).toContain("Background Claude");

    (rows[1] as HTMLButtonElement).click();
    await flushPromises();

    expect(inbox.open).toBe(false);
    expect(push).toHaveBeenCalledWith("/s/web-session-1/chat");
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

    wrapper.unmount();
  });
});
