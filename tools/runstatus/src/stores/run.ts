import { defineStore } from "pinia";
import { computed, ref } from "vue";
import type {
  AppDef,
  MermaidSnapshot,
  TraceEvent,
  TurnResult,
  IntentInfo,
  View,
} from "../types.js";
import type { DataSource } from "../data/source.js";
import type { LiveSource } from "../data/live-source.js";
import { appendThought, appendTool, type StreamItem } from "../lib/activity.js";
import { readOracleUsage } from "../components/oracle/lib.js";

/** One entry of the conversational transcript shown beside the trace. */
export interface TranscriptEntry {
  role: "user" | "agent";
  text: string;
  /** The agent's typed view for this turn (when the result carried one). */
  typedView?: View;
  /**
   * The turn's live thinking/tool feed, preserved when the turn streamed.
   * Rendered collapsed inside the agent bubble so the activity that produced
   * the reply stays reviewable after the final view replaces the live bubble.
   */
  stream?: StreamItem[];
}

// StreamItem (the ordered feed shape) moved to lib/activity.ts so the meta
// store shares it; re-exported here for existing importers.
export type { StreamItem } from "../lib/activity.js";

export const useRunStore = defineStore("run", () => {
  // ---- state ----
  const appDef = ref<AppDef | null>(null);
  const mermaid = ref<MermaidSnapshot | null>(null);
  const events = ref<TraceEvent[]>([]);
  const currentStatePath = ref<string>("");
  const selectedEventIndex = ref<number | null>(null);
  const terminal = ref<boolean>(false);
  const loading = ref<boolean>(false);

  // ---- conversational / write-side state ----
  // transcript is the ordered user↔agent exchange driven by the write RPCs.
  const transcript = ref<TranscriptEntry[]>([]);
  // Streaming state: the ordered thinking/tool feed of the in-flight turn.
  const pendingStream = ref<StreamItem[]>([]);
  // currentView is the latest TurnResult (the current room's view + menu).
  const currentView = ref<TurnResult | null>(null);
  // allowedIntents is the enriched per-intent menu of the current room.
  const allowedIntents = computed<IntentInfo[]>(
    () => currentView.value?.intents ?? []
  );
  // Set of state_path values that should be highlighted in the timeline.
  // Driven by clicks on diagram rooms/phases.  Empty = no highlight.
  const highlightedStatePaths = ref<string[]>([]);
  // Bumped each time the highlight set changes; TraceTimeline watches it to
  // scroll the first matching row into view (so re-clicking the same room
  // scrolls again).
  const highlightTick = ref<number>(0);

  // Aggregate token usage + cost across every oracle.call.complete event in the
  // run. Reads the canonical transport meta via readOracleUsage. `present` is
  // false when no call carried any usage (so the UI can hide the chip).
  const usageTotals = computed(() => {
    let promptTokens = 0;
    let responseTokens = 0;
    let costUsd = 0;
    let calls = 0;
    let present = false;
    for (const e of events.value) {
      if (e.msg !== "oracle.call.complete") continue;
      const u = readOracleUsage(e.attrs);
      if (u.promptTokens || u.responseTokens || u.costUsd) present = true;
      promptTokens += u.promptTokens ?? 0;
      responseTokens += u.responseTokens ?? 0;
      costUsd += u.costUsd ?? 0;
      calls += 1;
    }
    return { promptTokens, responseTokens, costUsd, calls, present };
  });

  // ---- internal ----
  let _unsubscribe: (() => void) | null = null;
  // True once any machine.state_entered event has been observed. Until then we
  // fall back to a raw state_path; after, only state_entered events move the
  // current state (turn.end is stamped with the turn's STARTING state, so it
  // must never overwrite the landed state).
  let _seenStateEntered = false;

  // ---- actions ----

  /**
   * Hydrate from a DataSource: load session + app + mermaid + initial trace,
   * then subscribe to keep events/currentStatePath updated.
   */
  async function hydrate(source: DataSource, sessionId: string): Promise<void> {
    // The store is a singleton; switching sessions reuses it. Drop every
    // session-scoped bit of state up front so the incoming session can't
    // inherit the previous one's transcript bubbles, current view, selection,
    // or diagram highlight. (hydrate already replaces events/state below, but
    // the conversational + interaction state must be cleared explicitly.)
    resetSessionState();
    loading.value = true;
    try {
      const [session, app, mer, traceResult] = await Promise.all([
        source.getSession(sessionId),
        source.getApp(sessionId),
        source.getMermaid(sessionId),
        source.getTrace(sessionId),
      ]);

      appDef.value = app;
      mermaid.value = mer;
      currentStatePath.value = session.current_state;
      terminal.value = session.terminal;
      events.value = traceResult.events.slice();
    } finally {
      loading.value = false;
    }

    // Subscribe for live updates.
    _unsubscribe = source.subscribe(sessionId, (e: TraceEvent) => {
      events.value.push(e);
      applyStatePath(e);
    });
  }

  /**
   * Derive currentStatePath from a trace event, preferring the LANDED state.
   *
   * machine.state_entered carries the TO state (the state the turn landed in),
   * so it is authoritative. turn.end is stamped with the turn's STARTING state,
   * so blindly taking e.state_path off every event would rewind the current
   * state to where the turn began. Once we've seen any state_entered we only
   * trust state_entered; before that (e.g. a trace that opens mid-stream) we
   * fall back to any non-empty state_path so the UI isn't left blank.
   */
  function applyStatePath(e: TraceEvent): void {
    if (e.msg === "machine.state_entered") {
      _seenStateEntered = true;
      if (e.state_path) currentStatePath.value = e.state_path;
      return;
    }
    if (!_seenStateEntered && e.state_path) {
      currentStatePath.value = e.state_path;
    }
  }

  /** Stop the live subscription. */
  function teardown(): void {
    _unsubscribe?.();
    _unsubscribe = null;
    _seenStateEntered = false;
  }

  /**
   * Clear all session-scoped state and stop any in-flight subscription. Called
   * at the head of hydrate so switching sessions (the store is a singleton)
   * starts from a clean slate instead of inheriting the prior session's
   * transcript, current view, selection, or diagram highlight.
   */
  function resetSessionState(): void {
    teardown();
    transcript.value = [];
    currentView.value = null;
    events.value = [];
    currentStatePath.value = "";
    terminal.value = false;
    selectedEventIndex.value = null;
    highlightedStatePaths.value = [];
  }

  /**
   * Re-pull the session after an out-of-band content change (a meta-mode
   * story edit triggered a server-side reload). Refreshes app/mermaid/trace +
   * the current room view IN PLACE — no browser reload, and the conversational
   * transcript is preserved (we don't push a fresh opening view). Used by the
   * meta store when a turn returns reload_requested.
   */
  async function rehydrate(
    source: DataSource,
    sessionId: string
  ): Promise<void> {
    // hydrate() clears all session-scoped state (so plain session switches
    // start clean); a reload of the SAME session must keep the conversation,
    // so snapshot the transcript and restore it after the reload.
    const preserved = transcript.value.slice();
    await hydrate(source, sessionId);
    transcript.value = preserved;
    // Refresh the current room view without appending a transcript entry
    // (loadInitialView would duplicate the opening bubble).
    const result = await source.view(sessionId);
    currentView.value = result;
    if (result.state) currentStatePath.value = result.state;
    terminal.value = result.mode === "completed";
  }

  // ---- write-side actions ----

  /**
   * Apply a TurnResult to the store: record it as currentView, sync the landed
   * state / terminal flags, and push an agent transcript entry built from the
   * result's pre-rendered view (carrying typed_view for richer rendering).
   *
   * On mode "rejected" / "clarify" the engine reports the SAME (un-advanced)
   * state; we still mirror it. We do NOT touch currentStatePath off a rejected
   * turn's state if it would rewind — the result.state IS the current state in
   * every mode, so it is always safe to mirror.
   */
  function applyTurnResult(
    result: TurnResult,
    streamedText?: string,
    stream?: StreamItem[]
  ): void {
    currentView.value = result;
    if (result.state) currentStatePath.value = result.state;
    terminal.value = result.mode === "completed";
    // The bubble text is the final room view; the streamed narration is only
    // the FALLBACK for view-less turns. (It used to be preferred — the only
    // way to keep it at all — but now the full feed survives on `stream`, so
    // preferring it would render the thinking twice and the view never.)
    const text = agentText(result) || streamedText?.trim() || "";
    const hasElements = (result.typed_view?.Elements?.length ?? 0) > 0;
    // Skip a content-less agent turn (e.g. a terminal transition whose target
    // renders no view) so the transcript doesn't trail an empty bubble.
    if (text || hasElements) {
      transcript.value.push({
        role: "agent",
        text,
        typedView: result.typed_view,
        // Keep the turn's live feed so the bubble can offer it collapsed —
        // without this the activity vanishes the moment the view renders.
        ...(stream && stream.length > 0 ? { stream } : {}),
      });
    }
  }

  /**
   * Load the current room without advancing the session, seed currentView, and
   * push the opening agent transcript entry. Call once after hydrate for a live
   * session so the conversation pane shows the room the session is sitting in.
   */
  async function loadInitialView(
    source: DataSource,
    sessionId: string
  ): Promise<void> {
    const result = await source.view(sessionId);
    currentView.value = result;
    if (result.state) currentStatePath.value = result.state;
    terminal.value = result.mode === "completed";
    transcript.value.push({
      role: "agent",
      text: agentText(result),
      typedView: result.typed_view,
    });
  }

  /**
   * Run one streamed turn against a LiveSource: reset the pending feed, append
   * each delta/tool frame in arrival order, and return the final TurnResult
   * plus the turn's concatenated thinking prose (preferred over the static
   * view text as the agent's transcript bubble) and the feed itself (kept on
   * the transcript entry for collapsed display). The pending feed is cleared
   * on the way out — the live bubble only exists while the turn is in flight.
   */
  async function runTurnStream(
    live: LiveSource,
    sessionId: string,
    method: "turn" | "submit",
    params: { input?: string; intent?: string; slots?: Record<string, unknown> }
  ): Promise<{ result: TurnResult; streamedText: string; stream: StreamItem[] }> {
    pendingStream.value = [];
    try {
      const result = await live.turnStream(sessionId, method, params, (ev) => {
        // In the MAIN chat the reply is the room view carried by the final
        // result, so extended-thinking ("think") and narration ("delta")
        // frames are the same thing to this feed: intermediate reasoning.
        // (The meta overlay treats them differently — its reply IS the
        // narration — see stores/meta.ts.) appendThought merges consecutive
        // thoughts; reassigning the array keeps the ref reactive.
        if ((ev.type === "delta" || ev.type === "think") && ev.text) {
          const next = pendingStream.value.slice();
          appendThought(next, ev.text);
          pendingStream.value = next;
        } else if (ev.type === "tool" && ev.tool) {
          const next = pendingStream.value.slice();
          appendTool(next, ev.tool, ev.preview ?? "");
          pendingStream.value = next;
        }
      });
      // Capture the feed before the finally clears the ref (clearing
      // reassigns the array, so this reference stays intact).
      const stream = pendingStream.value;
      const streamedText = stream
        .flatMap((it) => (it.kind === "thinking" ? [it.text] : []))
        .join("\n\n");
      return { result, streamedText, stream };
    } finally {
      pendingStream.value = [];
    }
  }

  /**
   * Submit an explicit intent (+ slots): push a user transcript entry, advance
   * the session, and apply the resulting view. Streams oracle progress via SSE
   * when the source supports it (LiveSource).
   */
  async function submitIntent(
    source: DataSource,
    sessionId: string,
    intent: string,
    slots: Record<string, unknown> = {},
    displayLabel?: string
  ): Promise<TurnResult> {
    transcript.value.push({ role: "user", text: userText(intent, slots, displayLabel) });
    let result: TurnResult;
    let capturedStream = "";
    let capturedItems: StreamItem[] | undefined;
    if ("turnStream" in source) {
      const out = await runTurnStream(source as LiveSource, sessionId, "submit", {
        intent,
        slots,
      });
      result = out.result;
      capturedStream = out.streamedText;
      capturedItems = out.stream;
    } else {
      result = await source.submit(sessionId, intent, slots);
    }
    applyTurnResult(result, capturedStream, capturedItems);
    return result;
  }

  /**
   * Send free text as a turn. Streams oracle progress via SSE when the source
   * supports it (LiveSource).
   */
  async function sendText(
    source: DataSource,
    sessionId: string,
    text: string,
    _intentName?: string
  ): Promise<TurnResult> {
    transcript.value.push({ role: "user", text });
    let result: TurnResult;
    let capturedStream = "";
    let capturedItems: StreamItem[] | undefined;
    if ("turnStream" in source) {
      const out = await runTurnStream(source as LiveSource, sessionId, "turn", {
        input: text,
      });
      result = out.result;
      capturedStream = out.streamedText;
      capturedItems = out.stream;
    } else {
      result = await source.sendTurn(sessionId, text);
    }
    applyTurnResult(result, capturedStream, capturedItems);
    return result;
  }

  /** Set the selected event by index (drives inline row highlight). */
  function selectEvent(index: number): void {
    selectedEventIndex.value = index;
  }

  /** Clear the selected event. */
  function clearSelection(): void {
    selectedEventIndex.value = null;
  }

  /** Set the highlighted state paths (driven by diagram clicks). */
  function setHighlightedStatePaths(paths: string[]): void {
    highlightedStatePaths.value = paths.slice();
    highlightTick.value += 1;
  }

  // ---- transcript text derivation ----

  /**
   * Build the agent transcript text for a TurnResult. Prefers the pre-rendered
   * `view`; on a rejection / clarification with no view, falls back to the
   * structured reason so the operator sees why the turn didn't advance.
   */
  function agentText(result: TurnResult): string {
    if (result.view) return result.view;
    if (result.mode === "rejected") {
      return result.error_message || result.guard_hint || "(rejected)";
    }
    if (result.mode === "clarify") {
      const prompts = (result.slots_needed ?? [])
        .map((s) => s.Prompt || s.Name)
        .filter(Boolean);
      return prompts.length > 0 ? prompts.join("\n") : "(more input needed)";
    }
    return "";
  }

  /** Build the user transcript text for a submitted intent. */
  function userText(intent: string, slots: Record<string, unknown>, displayLabel?: string): string {
    if (displayLabel?.trim()) {
      const values = Object.values(slots).filter(
        (v) => typeof v === "string" && v.trim() !== ""
      );
      if (values.length > 0) return `${displayLabel}: ${values.join(" ")}`;
      return displayLabel;
    }
    const values = Object.values(slots).filter(
      (v) => typeof v === "string" && v.trim() !== ""
    );
    if (values.length > 0) return values.join(" ");
    return intent;
  }

  return {
    // state
    appDef,
    mermaid,
    events,
    currentStatePath,
    selectedEventIndex,
    terminal,
    loading,
    highlightedStatePaths,
    highlightTick,
    usageTotals,
    transcript,
    currentView,
    allowedIntents,
    pendingStream,
    // actions
    hydrate,
    rehydrate,
    teardown,
    selectEvent,
    clearSelection,
    setHighlightedStatePaths,
    loadInitialView,
    submitIntent,
    sendText,
  };
});
