/**
 * Unit tests for the global inbox Pinia store. The LiveSource is a fake (no
 * live server, no SSE): the store's job is to fold the global notification
 * feed into an unread list + counts, and to mutate read/dismiss OPTIMISTICALLY
 * while reconciling with the RPC result.
 */

import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { setActivePinia, createPinia } from "pinia";
import { useInboxStore } from "../../src/stores/inbox.js";
import type {
  LiveSource,
  Notification,
  NotificationFrame,
  WorkItem,
  WorkListResult,
} from "../../src/data/live-source.js";

function notif(over: Partial<Notification> = {}): Notification {
  return {
    ID: "n1",
    SessionID: "s1",
    CreatedAt: new Date().toISOString(),
    Severity: "info",
    Title: "Turn ready",
    Body: "",
    TeleportState: "idle",
    TeleportSlots: null,
    TeleportProposalID: "",
    TeleportJobID: "",
    OriginKind: "",
    OriginRef: "",
    ReadAt: null,
    DismissedAt: null,
    SnoozedUntil: null,
    OriginURL: null,
    ...over,
  };
}

function frame(over: Partial<NotificationFrame> = {}): NotificationFrame {
  return {
    session_id: "s1",
    notification: notif(),
    unread: 1,
    needs_attention: 0,
    ...over,
  };
}

function fakeSource(overrides: Record<string, unknown> = {}): LiveSource {
  return {
    listWork: vi.fn().mockResolvedValue({
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
    } satisfies WorkListResult),
    subscribeNotifications: vi.fn().mockReturnValue(vi.fn()),
    syncGitHubInbox: vi.fn().mockResolvedValue({
      ok: true,
      session_id: "s1",
      fetched: 0,
      inserted: 0,
      skipped: 0,
      items: [],
    }),
    readNotification: vi.fn().mockResolvedValue({ ok: true }),
    dismissNotification: vi.fn().mockResolvedValue({ ok: true }),
    ...overrides,
  } as unknown as LiveSource;
}

function workResult(
  over: Partial<WorkListResult["summary"]> = {},
  items: WorkItem[] = []
): WorkListResult {
  return {
    summary: {
      items: items.length,
      needs_attention: 0,
      jobs_running: 0,
      jobs_awaiting_input: 0,
      jobs_terminal: 0,
      notifications_unread: 0,
      notifications_action_required: 0,
      pending_drives: 0,
      backgrounded_chats: 0,
      ...over,
    },
    sessions: [],
    items,
  };
}

describe("inbox store", () => {
  beforeEach(() => setActivePinia(createPinia()));
  afterEach(() => {
    useInboxStore().teardown();
    vi.useRealTimers();
  });

  it("a notification frame prepends the item and sets counts", () => {
    const inbox = useInboxStore();
    inbox.onFrame(frame({ unread: 1, needs_attention: 0 }));
    expect(inbox.notifications).toHaveLength(1);
    expect(inbox.notifications[0].ID).toBe("n1");
    expect(inbox.unread).toBe(1);
    expect(inbox.needsAttention).toBe(0);
  });

  it("init subscribes and refreshes active work", async () => {
    const inbox = useInboxStore();
    const src = fakeSource({
      listWork: vi.fn().mockResolvedValue({
        summary: {
          items: 2,
          needs_attention: 0,
          jobs_running: 0,
          jobs_awaiting_input: 0,
          jobs_terminal: 0,
          notifications_unread: 0,
          notifications_action_required: 0,
          pending_drives: 1,
          backgrounded_chats: 1,
        },
        sessions: [],
        items: [
          {
            kind: "pending_drive",
            priority: 65,
            session_id: "web-session-1",
            title: "continue the task",
            status: "pending",
            reacquire_tool: "chat.show",
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
            reacquire_tool: "chat.show",
            reacquire_session_id: "web-session-1",
            chat_id: "chat-2",
          },
        ],
      } satisfies WorkListResult),
    });

    inbox.init(src);
    await Promise.resolve();

    expect(src.subscribeNotifications).toHaveBeenCalledTimes(1);
    expect(src.listWork).toHaveBeenCalledTimes(1);
    expect(inbox.activeWorkCount).toBe(2);
    expect(inbox.chromeCount).toBe(2);
    expect(inbox.workItems.map((item) => item.kind)).toEqual([
      "pending_drive",
      "backgrounded_chat",
    ]);
  });

  it("chrome count and attention include active work", async () => {
    const inbox = useInboxStore();
    const src = fakeSource({
      listWork: vi.fn().mockResolvedValue({
        summary: {
          items: 3,
          needs_attention: 1,
          jobs_running: 1,
          jobs_awaiting_input: 1,
          jobs_terminal: 0,
          notifications_unread: 0,
          notifications_action_required: 0,
          pending_drives: 1,
          backgrounded_chats: 0,
        },
        sessions: [],
        items: [],
      } satisfies WorkListResult),
    });

    inbox.onFrame(frame({ unread: 1, needs_attention: 0 }));
    expect(inbox.chromeCount).toBe(1);
    expect(inbox.chromeNeedsAttention).toBe(false);

    await inbox.refreshWork(src);

    expect(inbox.activeWorkCount).toBe(3);
    expect(inbox.chromeCount).toBe(3);
    expect(inbox.chromeNeedsAttention).toBe(true);
  });

  it("opening the panel refreshes active work", async () => {
    const inbox = useInboxStore();
    const src = fakeSource();
    inbox.init(src);
    await Promise.resolve();

    inbox.toggle();
    await Promise.resolve();

    expect(inbox.open).toBe(true);
    expect(src.listWork).toHaveBeenCalledTimes(2);
  });

  it("polls active work while subscribed and stops on teardown", async () => {
    vi.useFakeTimers();
    const inbox = useInboxStore();
    const src = fakeSource();

    inbox.init(src);
    await Promise.resolve();
    expect(src.listWork).toHaveBeenCalledTimes(1);

    await vi.advanceTimersByTimeAsync(15_000);
    expect(src.listWork).toHaveBeenCalledTimes(2);

    inbox.teardown();
    await vi.advanceTimersByTimeAsync(15_000);
    expect(src.listWork).toHaveBeenCalledTimes(2);
  });

  it("keeps the newest active-work refresh when responses arrive out of order", async () => {
    const inbox = useInboxStore();
    let resolveFirst: (value: WorkListResult) => void = () => {};
    let resolveSecond: (value: WorkListResult) => void = () => {};
    const src = fakeSource({
      listWork: vi
        .fn()
        .mockImplementationOnce(
          () =>
            new Promise<WorkListResult>((resolve) => {
              resolveFirst = resolve;
            })
        )
        .mockImplementationOnce(
          () =>
            new Promise<WorkListResult>((resolve) => {
              resolveSecond = resolve;
            })
        ),
    });

    const first = inbox.refreshWork(src);
    const second = inbox.refreshWork(src);

    resolveSecond(
      workResult({ items: 2, pending_drives: 2 }, [
        {
          kind: "pending_drive",
          priority: 65,
          session_id: "web-session-1",
          title: "newer queued work",
          status: "pending",
          reacquire_tool: "chat.show",
          reacquire_session_id: "web-session-1",
          drive_id: "drive-new",
          chat_id: "chat-new",
        },
        {
          kind: "backgrounded_chat",
          priority: 60,
          session_id: "web-session-1",
          title: "newer background chat",
          status: "pty_background",
          reacquire_tool: "chat.show",
          reacquire_session_id: "web-session-1",
          chat_id: "chat-bg",
        },
      ])
    );
    await second;

    expect(inbox.activeWorkCount).toBe(2);
    expect(inbox.workItems.map((item) => item.title)).toEqual([
      "newer queued work",
      "newer background chat",
    ]);

    resolveFirst(
      workResult({ items: 1, pending_drives: 1 }, [
        {
          kind: "pending_drive",
          priority: 65,
          session_id: "web-session-1",
          title: "stale queued work",
          status: "pending",
          reacquire_tool: "chat.show",
          reacquire_session_id: "web-session-1",
          drive_id: "drive-old",
          chat_id: "chat-old",
        },
      ])
    );
    await first;

    expect(inbox.activeWorkCount).toBe(2);
    expect(inbox.workItems.map((item) => item.title)).toEqual([
      "newer queued work",
      "newer background chat",
    ]);
    expect(inbox.workLoading).toBe(false);
  });

  it("syncGitHub records counts and refreshes active work", async () => {
    const inbox = useInboxStore();
    const src = fakeSource({
      syncGitHubInbox: vi.fn().mockResolvedValue({
        ok: true,
        session_id: "s1",
        fetched: 2,
        inserted: 1,
        skipped: 1,
        items: [],
      }),
    });

    await inbox.syncGitHub(src, "s1", "acme/repo");

    expect(src.syncGitHubInbox).toHaveBeenCalledWith("s1", { repo: "acme/repo" });
    expect(src.listWork).toHaveBeenCalledTimes(1);
    expect(inbox.githubSyncError).toBe("");
    expect(inbox.githubSyncLast?.inserted).toBe(1);
    expect(inbox.githubSyncLast?.skipped).toBe(1);
  });

  it("syncGitHub records errors and clears stale success", async () => {
    const inbox = useInboxStore();
    const src = fakeSource({
      syncGitHubInbox: vi.fn().mockResolvedValue({
        ok: true,
        session_id: "s1",
        fetched: 1,
        inserted: 1,
        skipped: 0,
        items: [],
      }),
    });
    await inbox.syncGitHub(src, "s1");
    expect(inbox.githubSyncLast?.inserted).toBe(1);

    const failing = fakeSource({
      syncGitHubInbox: vi.fn().mockRejectedValue(new Error("gh auth required")),
    });
    await inbox.syncGitHub(failing, "s1");

    expect(inbox.githubSyncLast).toBeNull();
    expect(inbox.githubSyncError).toBe("gh auth required");
  });

  it("a second frame prepends (newest first) and takes the fresh counts", () => {
    const inbox = useInboxStore();
    inbox.onFrame(frame({ notification: notif({ ID: "n1" }), unread: 1 }));
    inbox.onFrame(
      frame({
        notification: notif({ ID: "n2", Severity: "action_required" }),
        unread: 2,
        needs_attention: 1,
      })
    );
    expect(inbox.notifications.map((n) => n.ID)).toEqual(["n2", "n1"]);
    expect(inbox.unread).toBe(2);
    expect(inbox.needsAttention).toBe(1);
    expect(inbox.hasNeedsAttention).toBe(true);
  });

  it("toasts only for success / action_required", () => {
    const inbox = useInboxStore();
    inbox.onFrame(frame({ notification: notif({ ID: "i", Severity: "info" }) }));
    expect(inbox.toast).toBeNull();
    inbox.onFrame(
      frame({ notification: notif({ ID: "s", Severity: "success" }) })
    );
    expect(inbox.toast?.ID).toBe("s");
    inbox.onFrame(
      frame({ notification: notif({ ID: "a", Severity: "action_required" }) })
    );
    expect(inbox.toast?.ID).toBe("a");
  });

  it("de-dupes a repeated push by id", () => {
    const inbox = useInboxStore();
    inbox.onFrame(frame({ notification: notif({ ID: "n1" }), unread: 1 }));
    inbox.onFrame(frame({ notification: notif({ ID: "n1" }), unread: 1 }));
    expect(inbox.notifications).toHaveLength(1);
  });

  it("markRead optimistically decrements unread and calls the RPC", async () => {
    const inbox = useInboxStore();
    const src = fakeSource();
    inbox.onFrame(
      frame({ notification: notif({ ID: "n1", Severity: "action_required" }), unread: 1, needs_attention: 1 })
    );
    await inbox.markRead(src, "s1", "n1");
    expect(inbox.unread).toBe(0);
    expect(inbox.needsAttention).toBe(0);
    expect(inbox.notifications[0].ReadAt).toBeTruthy();
    expect(src.readNotification).toHaveBeenCalledWith("s1", "n1");
  });

  it("markRead is a no-op on an already-read item", async () => {
    const inbox = useInboxStore();
    const src = fakeSource();
    inbox.onFrame(frame({ notification: notif({ ID: "n1" }), unread: 1 }));
    await inbox.markRead(src, "s1", "n1");
    expect(inbox.unread).toBe(0);
    await inbox.markRead(src, "s1", "n1");
    expect(inbox.unread).toBe(0); // not driven negative
  });

  it("dismiss optimistically removes the item, adjusts counts, calls RPC", async () => {
    const inbox = useInboxStore();
    const src = fakeSource();
    inbox.onFrame(
      frame({ notification: notif({ ID: "n1", Severity: "action_required" }), unread: 1, needs_attention: 1 })
    );
    await inbox.dismiss(src, "s1", "n1");
    expect(inbox.notifications).toHaveLength(0);
    expect(inbox.unread).toBe(0);
    expect(inbox.needsAttention).toBe(0);
    expect(src.dismissNotification).toHaveBeenCalledWith("s1", "n1");
  });

  it("dismiss restores the item when the RPC reports !ok", async () => {
    const inbox = useInboxStore();
    const src = fakeSource({
      dismissNotification: vi.fn().mockResolvedValue({ ok: false }),
    });
    inbox.onFrame(frame({ notification: notif({ ID: "n1" }), unread: 1 }));
    await inbox.dismiss(src, "s1", "n1");
    expect(inbox.notifications.map((n) => n.ID)).toEqual(["n1"]);
  });

  it("toggle / close drive the panel open state", () => {
    const inbox = useInboxStore();
    expect(inbox.open).toBe(false);
    inbox.toggle();
    expect(inbox.open).toBe(true);
    inbox.close();
    expect(inbox.open).toBe(false);
  });
});
