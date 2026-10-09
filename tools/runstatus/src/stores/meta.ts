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
 * Crucially, the in-flight streaming runtime (busy / pendingStream /
 * pendingAssistantText / error / reloadNote) is ALSO kept per scope, in
 * `runtimes`. A send() turn is a long-lived async over an SSE that outlives the
 * overlay — closing the modal does not abort it (LiveSource.metaStream's fetch
 * is not cancelled). Holding that runtime per scope is what lets a turn keep
 * streaming into its own scope while the user closes the overlay, switches
 * modes, or reopens: the display is consistent on reopen as if it had stayed
 * open, and a turn that finishes while you're looking elsewhere flags `waiting`
 * for the launcher badge instead of silently completing. The busy/pending/…
 * getters below project the active scope's runtime so the overlay component and
 * the unit tests read them exactly as before.
 *
 * Modes the UI exposes (resolved against the server's available set):
 *   - story.edit  — edit this story's YAML (writes + commits + reloads content)
 *   - story.ask   — read-only Q&A about the current story
 *   - story.improve — read-only introspection report for the latest run
 *   - kitsoki.ask — read-only help about kitsoki itself (cross-app)
 *   - kitsoki.improve — read-only engine improvement report (cross-app)
 */

/** Composite key so the same mode in different sessions keeps separate chats. */
function scopeKey(sessionId: string, mode: string): string {
  return `${sessionId}::${mode}`;
}

/**
 * The per-scope streaming runtime. One of these exists per (session, mode) that
 * has been opened; a send() turn mutates only its own scope's runtime, so it is
 * unaffected by the user closing/reopening the overlay or switching modes.
 */
interface ScopeRuntime {
  // A send() turn is streaming for this scope.
  busy: boolean;
  // In-progress deferred assistant narration (see send()'s callback). Empty
  // when idle.
  pendingAssistantText: string;
  // The ordered thinking/tool feed of the in-flight turn (cleared on done).
  pendingStream: StreamItem[];
  // Last turn's error / story-edit reload note, scoped so they don't leak
  // across modes.
  error: string;
  reloadNote: string;
  // A turn finished (reply OR error) for this scope while the user was NOT
  // looking at it — overlay closed, or a different scope active. Cleared when
  // the scope is next viewed (markSeen). Drives the launcher "ready" badge.
  waiting: boolean;
}

function freshRuntime(): ScopeRuntime {
  return {
    busy: false,
    pendingAssistantText: "",
    pendingStream: [],
    error: "",
    reloadNote: "",
    waiting: false,
  };
}

export const useMetaStore = defineStore("meta", () => {
  // ---- state ----
  const open = ref(false);
  const activeMode = ref<string>("");
  const activeSessionId = ref<string>("");
  // A momentary seed round-trip (metaEnter / metaNew) for the active scope.
  // Distinct from a scope's `busy` so opening/resetting never disturbs an
  // in-flight turn streaming in another scope.
  const entering = ref(false);

  // Modes available in the current scope (from runstatus.meta.modes).
  const modes = ref<MetaModeInfo[]>([]);

  // Per-(session,mode) transcript, chat id, and streaming runtime — all kept
  // across close/reopen/nav so the overlay is genuinely persistent.
  const transcripts = ref<Record<string, MetaMessage[]>>({});
  const chatIds = ref<Record<string, string>>({});
  const runtimes = ref<Record<string, ScopeRuntime>>({});

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

  const activeRuntime = computed<ScopeRuntime | undefined>(
    () => runtimes.value[activeKey.value]
  );
  // Project the active scope's runtime so the overlay/tests read these exactly
  // as they did when the state was global. `entering` folds in so the composer
  // stays disabled during the seed round-trip, matching the prior behaviour.
  const busy = computed(
    () => (activeRuntime.value?.busy ?? false) || entering.value
  );
  const pendingAssistantText = computed(
    () => activeRuntime.value?.pendingAssistantText ?? ""
  );
  const pendingStream = computed<StreamItem[]>(
    () => activeRuntime.value?.pendingStream ?? []
  );
  const error = computed(() => activeRuntime.value?.error ?? "");
  const reloadNote = computed(() => activeRuntime.value?.reloadNote ?? "");

  // Launcher aggregates: a meta chat anywhere is working / has a reply waiting.
  // Both can be true at once (one mode streaming while another finished) — the
  // launcher renders a distinct badge for each.
  const anyBusy = computed(() =>
    Object.values(runtimes.value).some((r) => r.busy)
  );
  const anyWaiting = computed(() =>
    Object.values(runtimes.value).some((r) => r.waiting)
  );

  /** Working/waiting status for one (session, mode) — for the dropdown items. */
  function statusFor(
    sessionId: string,
    mode: string
  ): { busy: boolean; waiting: boolean } {
    const r = runtimes.value[scopeKey(sessionId, mode)];
    return { busy: !!r?.busy, waiting: !!r?.waiting };
  }

  // ---- actions ----

  /** Get (creating if needed) the streaming runtime for a scope. */
  function runtime(k: string): ScopeRuntime {
    let rt = runtimes.value[k];
    if (!rt) {
      rt = freshRuntime();
      runtimes.value[k] = rt;
    }
    return rt;
  }

  /** Clear a scope's "reply waiting" flag — it is now being viewed. */
  function markSeen(k: string): void {
    const r = runtimes.value[k];
    if (r?.waiting) r.waiting = false;
  }

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
    } catch {
      // Best-effort: an unavailable mode set just leaves the dropdown empty.
      modes.value = [];
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
    const k = scopeKey(sessionId, mode);
    const rt = runtime(k);
    rt.error = "";
    rt.reloadNote = "";
    // The user is now looking at this scope — clear its pending-reply flag.
    markSeen(k);
    // Already entered: resume instantly. Do NOT touch the scope's runtime — a
    // turn may be streaming into it right now and must keep its busy/feed.
    if (chatIds.value[k]) return;
    entering.value = true;
    try {
      await ensureEntered(source, sessionId, mode);
    } catch (e) {
      rt.error = errMsg(e);
    } finally {
      entering.value = false;
    }
  }

  /** Close the overlay (transcripts + runtimes are kept for the next open). */
  function close(): void {
    open.value = false;
  }

  /** Send one turn; streams the assistant reply via SSE, finalises on done. */
  async function send(source: LiveSource, text: string): Promise<void> {
    const trimmed = text.trim();
    // Capture the scope NOW: the user may close/switch while this turn streams,
    // but every mutation below targets this captured scope's runtime.
    const k = activeKey.value;
    const mode = activeMode.value;
    const sessionId = activeSessionId.value;
    const rt = runtime(k);
    if (!trimmed || rt.busy) return;
    rt.error = "";
    rt.reloadNote = "";
    rt.pendingAssistantText = "";
    rt.pendingStream = [];
    rt.waiting = false;

    // In-progress assistant narration is DEFERRED: the model's final reply also
    // arrives as plain narration, so each narration delta is held in
    // rt.pendingAssistantText until later activity (a think/tool frame, or a
    // fresh complete narration) proves it intermediate — then it flushes into
    // the feed as a thought. Whatever is still held when "done" arrives IS the
    // reply and is dropped (the done frame carries it authoritatively). This
    // mirrors the TUI's metaStreamPending deferral (tui.go).
    const flushNarration = (): void => {
      const held = rt.pendingAssistantText;
      if (held.trim()) {
        const next = rt.pendingStream.slice();
        appendThought(next, held.trimEnd());
        rt.pendingStream = next;
      }
      rt.pendingAssistantText = "";
    };

    // Optimistically show the user's turn.
    pushMessage(k, { role: "user", text: trimmed });
    rt.busy = true;
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
            const next = rt.pendingStream.slice();
            appendThought(next, ev.text);
            rt.pendingStream = next;
          } else if (ev.type === "delta" && ev.text) {
            // Narration: ambiguous until the next event — intermediate
            // thought (flushed by whatever follows) or the final reply
            // (dropped on done). Chunked senders (the no-LLM stub) split one
            // narration across many fragment deltas with trailing spaces; a
            // fragment continues the held text, while a COMPLETE prior
            // narration (no trailing whitespace) is proven intermediate by
            // this fresh one and flushes into the feed.
            const held = rt.pendingAssistantText;
            if (held && !/\s$/.test(held)) {
              flushNarration();
              rt.pendingAssistantText = ev.text;
            } else {
              rt.pendingAssistantText = held + ev.text;
            }
          } else if (ev.type === "tool" && ev.tool) {
            // A tool round-trip still follows, so any held narration was
            // unambiguously intermediate.
            flushNarration();
            const next = rt.pendingStream.slice();
            appendTool(next, ev.tool, ev.preview ?? "");
            rt.pendingStream = next;
          }
        }
      );
      // The narration still held is the reply (rendered from res.assistant
      // below) — dropping it from the feed is the point, or every reply
      // would duplicate as a trailing thought.
      const stream = rt.pendingStream;
      rt.pendingAssistantText = "";
      rt.pendingStream = [];
      if (res.chat_id) chatIds.value = { ...chatIds.value, [k]: res.chat_id };
      pushMessage(k, {
        role: "assistant",
        text: res.assistant,
        stream: stream.length ? stream : undefined,
      });

      if (res.reload_requested) {
        const changed = res.changed_files ?? [];
        rt.reloadNote =
          changed.length > 0
            ? `Story reloaded — changed: ${changed.join(", ")}`
            : "Story reloaded.";
        try {
          await source.reloadSession(sessionId);
          const runStore = useRunStore();
          await runStore.rehydrate(source, sessionId);
        } catch (e) {
          rt.reloadNote = `Reload failed: ${errMsg(e)}`;
        }
      }
    } catch (e) {
      rt.pendingAssistantText = "";
      rt.pendingStream = [];
      rt.error = errMsg(e);
    } finally {
      rt.busy = false;
      // If the user isn't currently looking at this scope, flag the finished
      // turn so the launcher can surface a "reply waiting" badge.
      const viewed = open.value && activeKey.value === k;
      if (!viewed) rt.waiting = true;
    }
  }

  /** Archive the active chat and start a fresh one in the same scope. */
  async function newChat(source: LiveSource): Promise<void> {
    const k = activeKey.value;
    const mode = activeMode.value;
    const sessionId = activeSessionId.value;
    entering.value = true;
    try {
      const sess = await source.metaNew(
        sessionId,
        mode,
        chatIds.value[k] ?? ""
      );
      chatIds.value = { ...chatIds.value, [k]: sess.chat_id };
      transcripts.value = { ...transcripts.value, [k]: [] };
      // Reset the streaming runtime too — fresh chat, fresh feed/badges.
      runtimes.value[k] = freshRuntime();
    } catch (e) {
      runtime(k).error = errMsg(e);
    } finally {
      entering.value = false;
    }
  }

  // ---- internal ----
  function pushMessage(k: string, msg: MetaMessage): void {
    const prev = transcripts.value[k] ?? [];
    transcripts.value = { ...transcripts.value, [k]: [...prev, msg] };
  }

  /** Demo-only: seed the overlay as if a story.edit refine turn completed. */
  function seedForDemo(payload: SeedMetaRefinePayload): void {
    // Ensure story.edit mode is in the modes list.
    if (!modes.value.some((m) => m.key === "story.edit")) {
      modes.value = [
        {
          key: "story.edit",
          label: "Edit story",
          read_only: false,
          banner: "Refine the mined draft — say what should change.",
          agent: "",
          group: "story",
        },
        ...(payload.modes ?? []),
        ...modes.value,
      ];
    }
    const k = scopeKey(payload.sessionId, "story.edit");
    transcripts.value = { ...transcripts.value, [k]: payload.transcript };
    const rt = runtime(k);
    rt.reloadNote = payload.reloadNote;
    // Open the overlay on story.edit.
    activeSessionId.value = payload.sessionId;
    activeMode.value = "story.edit";
    open.value = true;
  }

  return {
    // state
    open,
    activeMode,
    activeSessionId,
    entering,
    modes,
    // getters
    busy,
    error,
    reloadNote,
    pendingAssistantText,
    pendingStream,
    activeTranscript,
    activeModeInfo,
    anyBusy,
    anyWaiting,
    statusFor,
    // actions
    setSession,
    loadModes,
    ensureEntered,
    openMode,
    close,
    send,
    newChat,
    seedForDemo,
  };
});

function errMsg(e: unknown): string {
  if (e instanceof Error) return e.message;
  return String(e);
}

// ── Demo seam (dev/demo only — mirrors window.__pushProposal in proposals.ts) ──
// window.__seedMetaRefine(payload) opens the meta overlay pre-loaded as if a
// story.edit refine turn had completed — without invoking a real LLM. Used by
// ad-hoc-workbench-video.spec.ts to demo the refine flow deterministically.
interface SeedMetaRefinePayload {
  sessionId: string;
  transcript: MetaMessage[];
  reloadNote: string;
  modes?: MetaModeInfo[];
}

if (typeof window !== "undefined") {
  (
    window as unknown as {
      __seedMetaRefine?: (payload: SeedMetaRefinePayload) => void;
    }
  ).__seedMetaRefine = (payload: SeedMetaRefinePayload) => {
    useMetaStore().seedForDemo(payload);
  };
}
