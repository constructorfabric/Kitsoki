import { defineStore } from "pinia";
import { computed, ref } from "vue";
import type { MetaModeInfo, MetaMessage } from "../data/source.js";
import type { LiveSource } from "../data/live-source.js";
import { useRunStore } from "./run.js";

// Meta mode is live-only (the global button hides itself in snapshot/artifact
// mode), so the actions take a concrete LiveSource: it carries both the meta.*
// RPCs and the lifecycle reloadSession the DataSource interface deliberately
// omits (see RunView.vue's note).

/**
 * The meta-mode overlay store. App-global (mounted once in App.vue), so its
 * state survives Vue Router navigation between views — that is what makes the
 * overlay "persistent": close it, navigate, reopen, and the same conversation
 * is still there. The durable backing is the server-side chat row (keyed by
 * mode + session scope); this store keeps the loaded transcript and the chat
 * id per (session, mode) so a reopen resumes without a round-trip, and a full
 * page reload rehydrates from the row via metaEnter.
 *
 * Modes the UI exposes (resolved against the server's available set):
 *   - story.edit  — edit this story's YAML (writes + commits + reloads content)
 *   - story.ask   — read-only Q&A about the current story
 *   - kitsoki.ask — read-only help about kitsoki itself (cross-app)
 */

/** Composite key so the same mode in different sessions keeps separate chats. */
function scopeKey(sessionId: string, mode: string): string {
  return `${sessionId}::${mode}`;
}

export const useMetaStore = defineStore("meta", () => {
  // ---- state ----
  const open = ref(false);
  const activeMode = ref<string>("");
  const activeSessionId = ref<string>("");
  const busy = ref(false);
  const error = ref<string>("");
  // A transient note shown after a story-edit reload, e.g. the changed files.
  const reloadNote = ref<string>("");

  // Modes available in the current scope (from runstatus.meta.modes).
  const modes = ref<MetaModeInfo[]>([]);

  // Per-(session,mode) transcript + chat id, kept across close/reopen/nav.
  const transcripts = ref<Record<string, MetaMessage[]>>({});
  const chatIds = ref<Record<string, string>>({});

  // ---- getters ----
  const activeKey = computed(() =>
    scopeKey(activeSessionId.value, activeMode.value)
  );
  const activeTranscript = computed<MetaMessage[]>(
    () => transcripts.value[activeKey.value] ?? []
  );
  const activeModeInfo = computed<MetaModeInfo | undefined>(() =>
    modes.value.find((m) => m.key === activeMode.value)
  );

  // ---- actions ----

  /** Track which session the overlay targets (set from the current route). */
  function setSession(sessionId: string): void {
    activeSessionId.value = sessionId;
  }

  /** Fetch the modes available for the current scope (best-effort). */
  async function loadModes(
    source: LiveSource,
    sessionId: string
  ): Promise<void> {
    try {
      modes.value = await source.metaModes(sessionId);
    } catch (e) {
      modes.value = [];
      error.value = errMsg(e);
    }
  }

  /** Resolve/resume a mode's chat, seeding the transcript from the server. */
  async function ensureEntered(
    source: LiveSource,
    sessionId: string,
    mode: string
  ): Promise<void> {
    const k = scopeKey(sessionId, mode);
    if (chatIds.value[k]) return; // already entered this scope
    const sess = await source.metaEnter(sessionId, mode, "");
    chatIds.value = { ...chatIds.value, [k]: sess.chat_id };
    transcripts.value = { ...transcripts.value, [k]: sess.messages ?? [] };
  }

  /** Open the overlay on a specific mode. */
  async function openMode(
    source: LiveSource,
    sessionId: string,
    mode: string
  ): Promise<void> {
    activeSessionId.value = sessionId;
    activeMode.value = mode;
    open.value = true;
    error.value = "";
    reloadNote.value = "";
    busy.value = true;
    try {
      await ensureEntered(source, sessionId, mode);
    } catch (e) {
      error.value = errMsg(e);
    } finally {
      busy.value = false;
    }
  }

  /** Close the overlay (transcripts are kept for the next open). */
  function close(): void {
    open.value = false;
  }

  /** Send one turn; on a story-edit reload, refresh the run store in place. */
  async function send(source: LiveSource, text: string): Promise<void> {
    const trimmed = text.trim();
    if (!trimmed || busy.value) return;
    const k = activeKey.value;
    const mode = activeMode.value;
    const sessionId = activeSessionId.value;
    error.value = "";
    reloadNote.value = "";

    // Optimistically show the user's turn.
    pushMessage(k, { role: "user", text: trimmed });
    busy.value = true;
    try {
      const res = await source.metaSend(
        sessionId,
        mode,
        chatIds.value[k] ?? "",
        trimmed
      );
      if (res.chat_id) chatIds.value = { ...chatIds.value, [k]: res.chat_id };
      pushMessage(k, { role: "assistant", text: res.assistant });

      if (res.reload_requested) {
        // Story edit landed: reload the session's content and re-hydrate the
        // run store IN PLACE (no browser reload). Best-effort — the chat reply
        // already shows what happened.
        const changed = res.changed_files ?? [];
        reloadNote.value =
          changed.length > 0
            ? `Story reloaded — changed: ${changed.join(", ")}`
            : "Story reloaded.";
        try {
          await source.reloadSession(sessionId);
          const runStore = useRunStore();
          await runStore.rehydrate(source, sessionId);
        } catch (e) {
          reloadNote.value = `Reload failed: ${errMsg(e)}`;
        }
      }
    } catch (e) {
      error.value = errMsg(e);
    } finally {
      busy.value = false;
    }
  }

  /** Archive the active chat and start a fresh one in the same scope. */
  async function newChat(source: LiveSource): Promise<void> {
    const k = activeKey.value;
    const mode = activeMode.value;
    const sessionId = activeSessionId.value;
    error.value = "";
    reloadNote.value = "";
    busy.value = true;
    try {
      const sess = await source.metaNew(
        sessionId,
        mode,
        chatIds.value[k] ?? ""
      );
      chatIds.value = { ...chatIds.value, [k]: sess.chat_id };
      transcripts.value = { ...transcripts.value, [k]: [] };
    } catch (e) {
      error.value = errMsg(e);
    } finally {
      busy.value = false;
    }
  }

  // ---- internal ----
  function pushMessage(k: string, msg: MetaMessage): void {
    const prev = transcripts.value[k] ?? [];
    transcripts.value = { ...transcripts.value, [k]: [...prev, msg] };
  }

  return {
    // state
    open,
    activeMode,
    activeSessionId,
    busy,
    error,
    reloadNote,
    modes,
    // getters
    activeTranscript,
    activeModeInfo,
    // actions
    setSession,
    loadModes,
    ensureEntered,
    openMode,
    close,
    send,
    newChat,
  };
});

function errMsg(e: unknown): string {
  if (e instanceof Error) return e.message;
  return String(e);
}
