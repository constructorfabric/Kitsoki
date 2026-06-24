import { defineStore } from "pinia";
import { computed, ref } from "vue";
import type {
  LiveSource,
  Notification,
  NotificationFrame,
  WorkItem,
  WorkListResult,
  WorkSummary,
  GitHubInboxSyncResult,
} from "../data/live-source.js";

const WORK_REFRESH_INTERVAL_MS = 15_000;

/**
 * The global inbox store. App-global (mounted once in App.vue via the chrome
 * badge + panel + toast), so its state survives router navigation — the inbox
 * is a mailbox that belongs to the operator, not to the room being viewed.
 *
 * Feed model: the list RPC is per-session, but the live feed
 * (subscribeNotifications) is GLOBAL/cross-session, and is the primary source
 * of truth here. On a fresh load with no active session the list may be empty
 * until a push arrives — accepted per the epic (reconnect re-fetches). When a
 * frame arrives the store prepends the notification and takes the FRESH
 * unread/needs_attention counts the backend folds in.
 *
 * read/dismiss do NOT push a refreshed-count frame, so the store mutates the
 * counts OPTIMISTICALLY client-side and reconciles with the RPC result.
 */
export const useInboxStore = defineStore("inbox", () => {
  // ---- state ----
  const notifications = ref<Notification[]>([]);
  const unread = ref(0);
  const needsAttention = ref(0);
  const workItems = ref<WorkItem[]>([]);
  const workSummary = ref<WorkSummary | null>(null);
  const workLoading = ref(false);
  const workError = ref("");
  const githubSyncing = ref(false);
  const githubSyncError = ref("");
  const githubSyncLast = ref<GitHubInboxSyncResult | null>(null);
  const open = ref(false);
  // The most recent push, surfaced as a transient toast (success /
  // action_required only). Cleared when the toast auto-dismisses or is acted on.
  const toast = ref<Notification | null>(null);

  let unsubscribe: (() => void) | null = null;
  let liveSource: LiveSource | null = null;
  let workRefreshTimer: ReturnType<typeof setInterval> | null = null;
  let workRefreshSeq = 0;

  // ---- getters ----
  const hasNeedsAttention = computed(() => needsAttention.value > 0);
  const activeWorkCount = computed(() => workSummary.value?.items ?? 0);
  const chromeCount = computed(() => Math.max(unread.value, activeWorkCount.value));
  const chromeNeedsAttention = computed(
    () => needsAttention.value > 0 || (workSummary.value?.needs_attention ?? 0) > 0
  );

  // ---- actions ----

  /**
   * Start the global notification feed. Idempotent: a second init() is a no-op
   * so App.vue can call it once on mount without double-subscribing.
   */
  function init(source: LiveSource): void {
    if (unsubscribe) return;
    liveSource = source;
    unsubscribe = source.subscribeNotifications((frame) => onFrame(frame));
    void refreshWork();
    workRefreshTimer = setInterval(() => {
      if (!workLoading.value) void refreshWork();
    }, WORK_REFRESH_INTERVAL_MS);
  }

  /** Tear down the feed (e.g. on app unmount / hot reload). */
  function teardown(): void {
    if (unsubscribe) {
      unsubscribe();
      unsubscribe = null;
    }
    if (workRefreshTimer) {
      clearInterval(workRefreshTimer);
      workRefreshTimer = null;
    }
    liveSource = null;
  }

  /** Handle one SSE push: prepend the item, take the fresh counts. */
  function onFrame(frame: NotificationFrame): void {
    const n = frame.notification;
    if (n && n.ID) {
      // De-dupe by id (a reconnect backfill could repeat a push).
      const existing = notifications.value.findIndex((x) => x.ID === n.ID);
      if (existing >= 0) {
        notifications.value.splice(existing, 1);
      }
      notifications.value.unshift(n);
      // Toast only for success + action_required (epic open-question 1 lean).
      if (n.Severity === "success" || n.Severity === "action_required") {
        toast.value = n;
      }
    }
    // The frame's counts are fresh (folded from $inbox by the backend).
    unread.value = frame.unread;
    needsAttention.value = frame.needs_attention;
    void refreshWork();
  }

  /** Toggle the inbox panel open/closed. */
  function toggle(): void {
    open.value = !open.value;
    if (open.value) void refreshWork();
  }

  function openPanel(): void {
    open.value = true;
    void refreshWork();
  }

  function close(): void {
    open.value = false;
  }

  /** Dismiss the active toast (auto-dismiss timer or click handled elsewhere). */
  function clearToast(): void {
    toast.value = null;
  }

  async function refreshWork(
    source: Pick<LiveSource, "listWork"> | null = liveSource
  ): Promise<void> {
    if (!source) return;
    const seq = ++workRefreshSeq;
    workLoading.value = true;
    workError.value = "";
    try {
      const result: WorkListResult = await source.listWork();
      if (seq !== workRefreshSeq) return;
      workItems.value = result.items ?? [];
      workSummary.value = result.summary ?? null;
    } catch (err) {
      if (seq !== workRefreshSeq) return;
      workError.value = err instanceof Error ? err.message : String(err);
    } finally {
      if (seq === workRefreshSeq) {
        workLoading.value = false;
      }
    }
  }

  async function syncGitHub(
    source: Pick<LiveSource, "syncGitHubInbox" | "listWork">,
    sessionId: string,
    repo?: string,
    options: { silent?: boolean } = {}
  ): Promise<void> {
    if (!options.silent) {
      githubSyncing.value = true;
      githubSyncError.value = "";
      githubSyncLast.value = null;
    }
    try {
      const result = await source.syncGitHubInbox(sessionId, repo ? { repo } : {});
      if (!options.silent) {
        githubSyncLast.value = result;
      }
      await refreshWork(source);
    } catch (err) {
      if (!options.silent) {
        githubSyncError.value = err instanceof Error ? err.message : String(err);
      }
    } finally {
      if (!options.silent) {
        githubSyncing.value = false;
      }
    }
  }

  /**
   * Mark one notification read. Optimistically decrements unread (and clears a
   * pending action_required) before the RPC; reconciles on the response (the
   * RPC returns {ok:true}, so the optimistic state stands — we just re-assert
   * the local item's ReadAt and recompute needs_attention from the list).
   */
  async function markRead(
    source: LiveSource,
    sessionId: string,
    id: string
  ): Promise<void> {
    const item = notifications.value.find((n) => n.ID === id);
    if (item && !item.ReadAt) {
      item.ReadAt = new Date().toISOString();
      if (unread.value > 0) unread.value -= 1;
      if (item.Severity === "action_required" && needsAttention.value > 0) {
        needsAttention.value -= 1;
      }
    }
    const res = await source.readNotification(sessionId, id);
    // Reconcile: {ok:true} confirms the optimistic mutation; nothing to undo.
    if (!res.ok && item) {
      item.ReadAt = null;
    }
  }

  /**
   * Dismiss one notification. Optimistically removes it from the list (and
   * adjusts counts if it was unread) before the RPC; reconciles on the result.
   */
  async function dismiss(
    source: LiveSource,
    sessionId: string,
    id: string
  ): Promise<void> {
    const idx = notifications.value.findIndex((n) => n.ID === id);
    const removed = idx >= 0 ? notifications.value[idx] : null;
    if (idx >= 0) notifications.value.splice(idx, 1);
    if (removed && !removed.ReadAt && unread.value > 0) unread.value -= 1;
    if (
      removed &&
      removed.Severity === "action_required" &&
      needsAttention.value > 0
    ) {
      needsAttention.value -= 1;
    }
    if (toast.value?.ID === id) toast.value = null;
    const res = await source.dismissNotification(sessionId, id);
    // Reconcile: on failure, restore the item at its old position.
    if (!res.ok && removed) {
      notifications.value.splice(Math.max(idx, 0), 0, removed);
    }
  }

  return {
    // state
    notifications,
    unread,
    needsAttention,
    open,
    toast,
    workItems,
    workSummary,
    workLoading,
    workError,
    githubSyncing,
    githubSyncError,
    githubSyncLast,
    // getters
    hasNeedsAttention,
    activeWorkCount,
    chromeCount,
    chromeNeedsAttention,
    // actions
    init,
    teardown,
    onFrame,
    toggle,
    openPanel,
    close,
    clearToast,
    refreshWork,
    syncGitHub,
    markRead,
    dismiss,
  };
});
