import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import { setActivePinia, createPinia } from "pinia";
import InboxPanel from "../../src/components/InboxPanel.vue";
import { useInboxStore } from "../../src/stores/inbox.js";
import { useOperatorQuestionStore } from "../../src/stores/operatorQuestions.js";
import { useProposalsStore } from "../../src/stores/proposals.js";

const push = vi.fn();
const route = { params: { sessionId: "web-session-1" }, query: {} as Record<string, string> };
vi.mock("vue-router", () => ({
  useRouter: () => ({ push }),
  useRoute: () => route,
}));

const syncGitHubInbox = vi.fn();
const listWork = vi.fn();
const jumpMock = vi.hoisted(() => ({
  jumpToNotification: vi.fn(),
}));
vi.mock("../../src/data/live-source.js", () => ({
  LiveSource: vi.fn().mockImplementation(() => ({
    syncGitHubInbox,
    listWork,
  })),
}));
vi.mock("../../src/lib/inbox-jump.js", () => ({
  jumpToNotification: jumpMock.jumpToNotification,
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
    jumpMock.jumpToNotification.mockReset();
    jumpMock.jumpToNotification.mockResolvedValue(undefined);
    route.params.sessionId = "web-session-1";
    route.query = {};
    document.body.innerHTML = "";
  });

  afterEach(() => {
    document.body.innerHTML = "";
  });

  it("renders active chat work and routes to the public session chat", async () => {
    const inbox = useInboxStore();
    inbox.open = true;
    inbox.workSummary = {
      items: 4,
      needs_attention: 1,
      jobs_running: 0,
      jobs_awaiting_input: 0,
      jobs_terminal: 0,
      notifications_unread: 0,
      notifications_action_required: 0,
      pending_drives: 1,
      dispatching_drives: 1,
      failed_drives: 1,
      backgrounded_chats: 1,
    };
    inbox.workItems = [
      {
        kind: "failed_drive",
        priority: 94,
        session_id: "web-session-1",
        title: "failed agent task",
        body: "claude exited 1",
        status: "failed",
        reacquire_tool: "chat.show",
        reacquire_session_id: "web-session-1",
        drive_id: "drive-failed",
        chat_id: "chat-failed",
        actor: "claude",
      },
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
    expect(rows).toHaveLength(4);
    expect(document.body.textContent).toContain("failed");
    expect(document.body.textContent).toContain("failed agent task");
    expect(document.body.textContent).toContain("claude exited 1");
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

    (rows[0] as HTMLButtonElement).click();
    await flushPromises();

    expect(inbox.open).toBe(false);
    expect(push).toHaveBeenCalledWith("/s/web-session-1/chat?chat=chat-failed");
    wrapper.unmount();
  });

  it("opens and refreshes from the inbox query param", async () => {
    route.query = { inbox: "1" };
    listWork.mockResolvedValue({
      summary: {
        items: 1,
        needs_attention: 1,
        jobs_running: 0,
        jobs_awaiting_input: 1,
        jobs_terminal: 0,
        notifications_unread: 1,
        notifications_action_required: 1,
        pending_drives: 0,
        backgrounded_chats: 0,
      },
      sessions: [],
      items: [
        {
          kind: "job",
          priority: 96,
          session_id: "web-session-1",
          title: "host.run",
          body: "Which environment?",
          status: "awaiting_input",
          job_id: "job-awaiting",
          notification_id: "notif-awaiting",
          severity: "action_required",
          reacquire_tool: "notification",
          reacquire_session_id: "web-session-1",
        },
      ],
    });

    const inbox = useInboxStore();
    expect(inbox.open).toBe(false);

    const wrapper = mount(InboxPanel, { attachTo: document.body });
    await flushPromises();

    expect(inbox.open).toBe(true);
    expect(listWork).toHaveBeenCalled();
    expect(document.body.querySelector('[data-testid="inbox-panel"]')).not.toBeNull();
    expect(document.body.textContent).toContain("Active work");
    expect(document.body.textContent).toContain("Which environment?");
    wrapper.unmount();
  });

  it("opens a pending operator question from active work", async () => {
    const inbox = useInboxStore();
    const questions = useOperatorQuestionStore();
    inbox.open = true;
    inbox.workSummary = {
      items: 1,
      needs_attention: 1,
      jobs_running: 0,
      jobs_awaiting_input: 0,
      jobs_terminal: 0,
      notifications_unread: 0,
      notifications_action_required: 0,
      pending_drives: 0,
      backgrounded_chats: 0,
      operator_questions: 1,
    };
    inbox.workItems = [
      {
        kind: "operator_question",
        priority: 98,
        session_id: "web-session-1",
        title: "Env",
        body: "Which environment?",
        status: "awaiting_answer",
        question_id: "q-7",
        questions: [
          {
            question: "Which environment?",
            header: "Env",
            options: [{ label: "staging" }, { label: "prod" }],
          },
        ],
        reacquire_tool: "operator_question",
        reacquire_session_id: "web-session-1",
      },
    ];

    const wrapper = mount(InboxPanel, { attachTo: document.body });
    await flushPromises();

    const row = document.body.querySelector('[data-testid="work-item"]') as HTMLButtonElement;
    expect(row).not.toBeNull();
    expect(document.body.textContent).toContain("question");
    expect(document.body.textContent).toContain("Which environment?");
    expect(document.body.textContent).toContain("answer");

    row.click();
    await flushPromises();

    expect(inbox.open).toBe(false);
    expect(questions.active?.question_id).toBe("q-7");
    expect(questions.active?.questions[0]?.question).toBe("Which environment?");
    expect(push).not.toHaveBeenCalled();
    wrapper.unmount();
  });

  it("surfaces queued proposals in the global active work panel", async () => {
    const inbox = useInboxStore();
    const proposals = useProposalsStore();
    const questions = useOperatorQuestionStore();
    inbox.open = true;
    proposals.push({
      id: "demo-proposal-1",
      kind: "write_mode",
      title: "Edit docs proposal",
      detail: "Allow the agent to patch docs/proposals/example.md",
    });

    const wrapper = mount(InboxPanel, { attachTo: document.body });
    await flushPromises();

    const row = document.body.querySelector('[data-testid="work-item"]') as HTMLButtonElement;
    expect(row).not.toBeNull();
    expect(document.body.textContent).toContain("Active work");
    expect(document.body.textContent).toContain("1");
    expect(document.body.textContent).toContain("approval");
    expect(document.body.textContent).toContain("Edit docs proposal");
    expect(document.body.textContent).toContain("Allow the agent to patch docs/proposals/example.md");
    expect(document.body.textContent).toContain("review");

    row.click();
    await flushPromises();

    expect(inbox.open).toBe(false);
    expect(proposals.count).toBe(0);
    expect(questions.active?.question_id).toBe("demo-proposal-1");
    expect(questions.active?.questions[0]?.header).toBe("May I edit?");
    expect(questions.active?.questions[0]?.question).toContain("Edit docs proposal");
    expect(push).not.toHaveBeenCalled();
    wrapper.unmount();
  });

  it("keeps low-stakes structure proposals below backend active work", async () => {
    const inbox = useInboxStore();
    const proposals = useProposalsStore();
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
        title: "PR #42 needs review",
        status: "unread",
        notification_id: "notif-pr-42",
        severity: "action_required",
        reacquire_tool: "notification",
        reacquire_session_id: "web-session-1",
      },
    ];
    proposals.push({
      id: "demo-structure-1",
      kind: "structure",
      title: "Capture route idea",
    });

    const wrapper = mount(InboxPanel, { attachTo: document.body });
    await flushPromises();

    const rows = Array.from(document.body.querySelectorAll('[data-testid="work-item"]'));
    expect(rows).toHaveLength(2);
    expect(rows[0]?.textContent).toContain("PR #42 needs review");
    expect(rows[1]?.textContent).toContain("Capture route idea");
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

  it("routes notification-backed active job work through inbox jump", async () => {
    const inbox = useInboxStore();
    inbox.open = true;
    inbox.workSummary = {
      items: 1,
      needs_attention: 1,
      jobs_running: 0,
      jobs_awaiting_input: 0,
      jobs_terminal: 1,
      notifications_unread: 0,
      notifications_action_required: 0,
      pending_drives: 0,
      backgrounded_chats: 0,
    };
    inbox.workItems = [
      {
        kind: "job",
        priority: 90,
        session_id: "web-session-1",
        title: "host.agent.task",
        status: "failed",
        job_id: "job-1",
        notification_id: "notif-job-1",
        severity: "error",
        teleport_state: "foyer",
        teleport_job_id: "job-1",
        origin_kind: "job",
        origin_ref: "job:job-1",
        reacquire_tool: "notification",
        reacquire_session_id: "web-session-1",
      },
    ];

    const wrapper = mount(InboxPanel, { attachTo: document.body });
    await flushPromises();

    const rows = document.body.querySelectorAll('[data-testid="work-item"]');
    expect(rows).toHaveLength(1);
    expect(document.body.textContent).toContain("jump");

    (rows[0] as HTMLButtonElement).click();
    await flushPromises();

    expect(jumpMock.jumpToNotification).toHaveBeenCalledOnce();
    expect(jumpMock.jumpToNotification.mock.calls[0]?.[2]).toMatchObject({
      ID: "notif-job-1",
      SessionID: "web-session-1",
      TeleportJobID: "job-1",
      OriginRef: "job:job-1",
    });
    expect(push).not.toHaveBeenCalled();
    wrapper.unmount();
  });

  it("renders trace-backed mining proposals from backend active work", async () => {
    const inbox = useInboxStore();
    inbox.open = true;
    inbox.workSummary = {
      items: 1,
      needs_attention: 0,
      jobs_running: 0,
      jobs_awaiting_input: 0,
      jobs_terminal: 0,
      notifications_unread: 0,
      notifications_action_required: 0,
      pending_drives: 0,
      backgrounded_chats: 0,
      mining_proposals: 1,
    };
    inbox.workItems = [
      {
        kind: "mining_proposal",
        priority: 58,
        session_id: "web-session-1",
        title: "intent proposal",
        body: "target=dev-story; rung=2; draft=.artifacts/mining/recipe-pending",
        status: "awaiting_review",
        proposal_id: "recipe-pending",
        proposal_kind: "intent",
        proposal_target: "dev-story",
        draft_path: ".artifacts/mining/recipe-pending",
        rung: 2,
        reacquire_tool: "session",
        reacquire_session_id: "web-session-1",
      },
    ];

    const wrapper = mount(InboxPanel, { attachTo: document.body });
    await flushPromises();

    const rows = document.body.querySelectorAll('[data-testid="work-item"]');
    expect(rows).toHaveLength(1);
    expect(document.body.textContent).toContain("proposal");
    expect(document.body.textContent).toContain("intent proposal");
    expect(document.body.textContent).toContain("target=dev-story");
    expect(document.body.textContent).toContain("intent | dev-story | rung 2 | .artifacts/mining/recipe-pending");
    expect(document.body.textContent).toContain("review");

    (rows[0] as HTMLButtonElement).click();
    await flushPromises();

    expect(inbox.open).toBe(false);
    expect(push).toHaveBeenCalledWith("/s/web-session-1");
    wrapper.unmount();
  });

  it("routes active job work without notification context to the session view", async () => {
    const inbox = useInboxStore();
    inbox.open = true;
    inbox.workSummary = {
      items: 1,
      needs_attention: 0,
      jobs_running: 1,
      jobs_awaiting_input: 0,
      jobs_terminal: 0,
      notifications_unread: 0,
      notifications_action_required: 0,
      pending_drives: 0,
      backgrounded_chats: 0,
    };
    inbox.workItems = [
      {
        kind: "job",
        priority: 70,
        session_id: "web-session-1",
        title: "host.agent.task",
        status: "running",
        job_id: "job-1",
        reacquire_tool: "session",
        reacquire_session_id: "web-session-1",
      },
    ];

    const wrapper = mount(InboxPanel, { attachTo: document.body });
    await flushPromises();

    const rows = document.body.querySelectorAll('[data-testid="work-item"]');
    expect(rows).toHaveLength(1);
    expect(document.body.textContent).toContain("open session");

    (rows[0] as HTMLButtonElement).click();
    await flushPromises();

    expect(inbox.open).toBe(false);
    expect(push).toHaveBeenCalledWith("/s/web-session-1");
    expect(jumpMock.jumpToNotification).not.toHaveBeenCalled();
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
