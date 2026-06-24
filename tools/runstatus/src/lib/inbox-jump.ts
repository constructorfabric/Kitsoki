import type { Router } from "vue-router";
import type { LiveSource, Notification } from "../data/live-source.js";
import { useInboxStore } from "../stores/inbox.js";
import { useRunStore } from "../stores/run.js";

/**
 * Jump to the room a notification points at. Two cases:
 *
 *  - Already on that session's chat view: teleport directly (the InteractiveView
 *    is already mounted; an on-mount handler would not re-fire), then apply the
 *    TurnResult to the run store and mark the notification read.
 *  - Otherwise: navigate to /s/<sid>/chat?notif=<id> — InteractiveView's
 *    on-mount handler performs the teleport + mark-read + param clear.
 *
 * Either way the notification is marked read. A non-teleportable / unknown id
 * rejects with JSON-RPC -32000; we swallow it (the item simply doesn't jump).
 */
export async function jumpToNotification(
  router: Router,
  source: LiveSource,
  n: Notification
): Promise<void> {
  const inbox = useInboxStore();
  inbox.close();
  inbox.clearToast();

  const current = router.currentRoute.value;
  const onThisSession =
    current.params.sessionId === n.SessionID &&
    typeof current.params.sessionId === "string";

  if (onThisSession) {
    try {
      const result = await source.teleport(n.SessionID, n.ID);
      useRunStore().applyTurnResult(result);
    } catch {
      // Non-teleportable / session no longer live — degrade silently.
    }
    void inbox.markRead(source, n.SessionID, n.ID);
    return;
  }

  // Mark read eagerly (optimistic); the destination handler does the teleport.
  void inbox.markRead(source, n.SessionID, n.ID);
  await router.push(`/s/${n.SessionID}/chat?notif=${encodeURIComponent(n.ID)}`);
}
