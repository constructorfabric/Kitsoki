import { defineStore } from "pinia";
import { computed, ref } from "vue";
import type { MetaModeInfo, MetaMessage } from "../data/source.js";
import type { LiveSource } from "../data/live-source.js";
import { appendThought, appendTool, type StreamItem } from "../lib/activity.js";
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
  // In-progress assistant narration while the SSE stream is live. Empty when
  // idle. Unlike the feed below, this text is DEFERRED: the model's final
  // reply also arrives as plain narration, so each narration delta is held
  // here until later activity (a think/tool frame, or a fresh complete
  // narration) proves it intermediate — then it flushes into the feed as a
  // thought. Whatever is still held when "done" arrives IS the reply and is
  // dropped (the done frame carries it authoritatively). This mirrors the
  // TUI's metaStreamPending deferral (tui.go handleMetaStreamEvent).
  const pendingAssistantText = ref<string>("");
  // The ordered thinking/tool feed of the in-flight turn (cleared on done) —
  // the same shape the main chat streams (see stores/run.ts pendingStream).
  const pendingStream = ref<StreamItem[]>([]);

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

  /**
   * Flush the deferred narration into the feed as a thought. Called when
   * later stream activity proves the held narration was intermediate; the
   * narration still held when the turn ends is the reply and is dropped.
   */
  function flushNarration(): void {
    const held = pendingAssistantText.value;
    if (held.trim()) {
      const next = pendingStream.value.slice();
      appendThought(next, held.trimEnd());
      pendingStream.value = next;
    }
    pendingAssistantText.value = "";
  }

  /** Send one turn; streams the assistant reply via SSE, finalises on done. */
  async function send(source: LiveSource, text: string): Promise<void> {
    const trimmed = text.trim();
    if (!trimmed || busy.value) return;
    const k = activeKey.value;
    const mode = activeMode.value;
    const sessionId = activeSessionId.value;
    error.value = "";
    reloadNote.value = "";
    pendingAssistantText.value = "";
    pendingStream.value = [];

    // Optimistically show the user's turn.
    pushMessage(k, { role: "user", text: trimmed });
    busy.value = true;
    try {
      const res = await source.metaStream(
        sessionId,
        mode,
        chatIds.value[k] ?? "",
        trimmed,
        (ev) => {
          if (ev.type === "think" && ev.text) {
            // Extended-thinking prose is never the reply — it goes straight
            // into the feed. Any held narration is proven intermediate by
            // this fresh model activity, so flush it first.
            flushNarration();
            const next = pendingStream.value.slice();
            appendThought(next, ev.text);
            pendingStream.value = next;
          } else if (ev.type === "delta" && ev.text) {
            // Narration: ambiguous until the next event — intermediate
            // thought (flushed by whatever follows) or the final reply
            // (dropped on done). Chunked senders (the no-LLM stub) split one
            // narration across many fragment deltas with trailing spaces; a
            // fragment continues the held text, while a COMPLETE prior
            // narration (no trailing whitespace) is proven intermediate by
            // this fresh one and flushes into the feed.
            const held = pendingAssistantText.value;
            if (held && !/\s$/.test(held)) {
              flushNarration();
              pendingAssistantText.value = ev.text;
            } else {
              pendingAssistantText.value = held + ev.text;
            }
          } else if (ev.type === "tool" && ev.tool) {
            // A tool round-trip still follows, so any held narration was
            // unambiguously intermediate.
            flushNarration();
            const next = pendingStream.value.slice();
            appendTool(next, ev.tool, ev.preview ?? "");
            pendingStream.value = next;
          }
        }
      );
      // The narration still held is the reply (rendered from res.assistant
      // below) — dropping it from the feed is the point, or every reply
      // would duplicate as a trailing thought.
      const stream = pendingStream.value;
      pendingAssistantText.value = "";
      pendingStream.value = [];
      if (res.chat_id) chatIds.value = { ...chatIds.value, [k]: res.chat_id };
      pushMessage(k, {
        role: "assistant",
        text: res.assistant,
        stream: stream.length ? stream : undefined,
      });

      if (res.reload_requested) {
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
      pendingAssistantText.value = "";
      pendingStream.value = [];
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
    pendingAssistantText,
    pendingStream,
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
