import { defineStore } from "pinia";
import { computed, ref } from "vue";
import type {
  AppDef,
  MermaidSnapshot,
  TraceEvent,
  TurnResult,
  IntentInfo,
  View,
  HarnessProfileInfo,
  ContextRouteInfo,
} from "../types.js";
import type { DataSource, ConnectionState } from "../data/source.js";
import type { LiveSource } from "../data/live-source.js";
import { appendThought, appendTool, type StreamItem } from "../lib/activity.js";
import { readAgentUsage } from "../components/agent/lib.js";
import { humanizeIntent } from "../lib/intent.js";

/**
 * How a free-text user turn was resolved to an intent — the provenance the
 * routing tiers stamp on the turn.start event (routed_by / match_type /
 * confidence; see internal/orchestrator RouteProvenance) plus the resolved
 * intent from the turn's first machine.transition. Surfaced as the inline
 * routing chip under the user bubble so the web chat shows the semantic-routing
 * layer the TUI already does (ideas.md "the web ui doesn't show the semantic
 * routing layer aspect from the TUI").
 */
export interface RoutingInfo {
  /** Resolving tier: "semantic" | "deterministic" | "turncache" | "llm" | … */
  routedBy: string;
  /** Tier-specific reason, e.g. "leading-verb:commit", "example:back". */
  matchType?: string;
  /** Routing confidence band (0.90 synonym, etc.); omitted when not applicable. */
  confidence?: number;
  /** The intent the turn resolved to (turn's first transition). */
  intent?: string;
}

/** One entry of the conversational transcript shown beside the trace. */
export interface TranscriptEntry {
  /**
   * "user"/"agent" are operator-driven turn bubbles. "narration" is a machine
   * `say:` breadcrumb surfaced from the event log so a SELF-DRIVING run (one
   * that cascades to terminal on entry with no operator turn) still shows
   * meaningful, followable progress in the conversation — not only in the trace.
   */
  role: "user" | "agent" | "narration";
  text: string;
  /** The agent's typed view for this turn (when the result carried one). */
  typedView?: View;
  /**
   * The turn's live thinking/tool feed, preserved when the turn streamed.
   * Rendered collapsed inside the agent bubble so the activity that produced
   * the reply stays reviewable after the final view replaces the live bubble.
   */
  stream?: StreamItem[];
  /**
   * True when this agent bubble came from an off-ramp turn (TurnResult mode
   * "offpath"): a free-form `host.agent.converse` answer that did NOT advance
   * state. The transcript marks it so the bubble can be rendered distinctly
   * ("off path") — the menu still persists because state is unchanged.
   */
  isOffRamp?: boolean;
  /**
   * The turn number this user message produced (set after the turn resolves).
   * Used to recover routing provenance from the event log reactively, so the
   * chip fills in even when events arrive a tick later over SSE.
   */
  turn?: number;
  /** Routing provenance, resolved reactively from events (see chatEntries). */
  routing?: RoutingInfo;
  /**
   * The contextual-routing receipt, set on an AGENT bubble when the CRR tier
   * resolved this turn. Renders the "routed to … · contextual" receipt chip in
   * the agent bubble; absent for deterministic/semantic/LLM turns.
   */
  contextRoute?: ContextRouteInfo;
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
  // Liveness of the live trace stream. "reconnecting" while the SSE stream is
  // dropped (the transport backs off + reopens invisibly); the view binds this
  // to a "Reconnecting to session…" banner so a stalled stream isn't mistaken
  // for a slow agent. Stays "connected" for static (snapshot) sources.
  const connectionState = ref<ConnectionState>("connected");

  // ---- harness profiles ----
  // Declared profiles + live selection, loaded from the (optional)
  // source.getHarness. Empty when the source has no orchestrator (artifact
  // mode) or no profiles declared — the header picker stays hidden.
  const harnessProfiles = ref<HarnessProfileInfo[]>([]);
  const harnessModel = ref<string>("");
  const harnessEffort = ref<string>("");
  // The active profile's name, derived from the profiles' active flag.
  const harnessActiveProfile = computed<string>(
    () => harnessProfiles.value.find((p) => p.active)?.name ?? ""
  );

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

  // Aggregate token usage + cost across every agent.call.complete event in the
  // run. Reads the canonical transport meta via readAgentUsage. `present` is
  // false when no call carried any usage (so the UI can hide the chip).
  const usageTotals = computed(() => {
    let promptTokens = 0;
    let responseTokens = 0;
    let costUsd = 0;
    let calls = 0;
    let present = false;
    for (const e of events.value) {
      if (e.msg !== "agent.call.complete") continue;
      const u = readAgentUsage(e.attrs);
      if (u.promptTokens || u.responseTokens || u.costUsd) present = true;
      promptTokens += u.promptTokens ?? 0;
      responseTokens += u.responseTokens ?? 0;
      costUsd += u.costUsd ?? 0;
      calls += 1;
    }
    return { promptTokens, responseTokens, costUsd, calls, present };
  });

  // readTurnRouting recovers the routing provenance for a turn from the event
  // log: routed_by / match_type / confidence off the turn.start event, and the
  // resolved intent off the turn's FIRST machine.transition (the hub→room arc;
  // later transitions are internal auto-routing). Returns undefined until the
  // turn.start has landed (so the chip simply doesn't render yet).
  function readTurnRouting(turn: number): RoutingInfo | undefined {
    let info: RoutingInfo | undefined;
    let intent: string | undefined;
    for (const e of events.value) {
      if (e.turn !== turn) continue;
      if (e.msg === "turn.start" && typeof e.attrs.routed_by === "string") {
        info = {
          routedBy: e.attrs.routed_by,
          matchType: typeof e.attrs.match_type === "string" ? e.attrs.match_type : undefined,
          confidence: typeof e.attrs.confidence === "number" ? e.attrs.confidence : undefined,
        };
      }
      if (!intent && e.msg === "machine.transition" && typeof e.attrs.intent === "string") {
        intent = e.attrs.intent;
      }
    }
    if (!info) return undefined;
    if (intent) info.intent = intent;
    return info;
  }

  // narrationByTurn buckets every `machine.say` breadcrumb by the turn it fired
  // in, preserving event order within a turn. A self-driving room (one that
  // cascades through several states on a single engine step via emit_intent)
  // emits all its narration under one turn (the initial RunInitialOnEnter is
  // turn 0), so this is what lets that whole autonomous run read as a
  // conversation instead of vanishing into the trace timeline.
  const narrationByTurn = computed<Map<number, TranscriptEntry[]>>(() => {
    const byTurn = new Map<number, TranscriptEntry[]>();
    for (const e of events.value) {
      if (e.msg !== "machine.say") continue;
      const text = typeof e.attrs.text === "string" ? e.attrs.text : "";
      if (!text) continue;
      const bucket = byTurn.get(e.turn) ?? [];
      bucket.push({ role: "narration", text });
      byTurn.set(e.turn, bucket);
    }
    return byTurn;
  });

  // chatEntries is the transcript enriched with each user turn's routing
  // provenance AND interleaved with machine `say:` narration, resolved
  // reactively from the event log (recomputes as events stream in over SSE).
  //
  // Why narration is merged here: a story may advance with NO operator input —
  // the demo-video-loop self-drives maker→QA→loop to terminal the moment the
  // session is created. The operator transcript then holds only the opening
  // view, so the conversation column would otherwise be empty and the run's
  // progress would live only in the developer trace. We surface each `say:`
  // breadcrumb as a distinct "narration" bubble so EVERY conversation provides
  // meaningful, followable feedback as it progresses, even when no input is
  // required. Placement: a turn's narration is flushed right AFTER that turn's
  // agent bubble (so an operator turn reads "you → agent → what it did"); a
  // self-driving run's turn-0 narration is flushed BEFORE the opening view so
  // the journey reads top-to-bottom with the landed (often terminal) view last.
  const chatEntries = computed<TranscriptEntry[]>(() => {
    const byTurn = narrationByTurn.value;
    const out: TranscriptEntry[] = [];
    const flushed = new Set<number>();
    const flush = (turn: number): void => {
      if (flushed.has(turn)) return;
      flushed.add(turn);
      const bucket = byTurn.get(turn);
      if (bucket) out.push(...bucket);
    };

    // Turn-0 narration (a self-driving cascade, or a root on_enter greeting)
    // leads the conversation, ahead of the opening room-view bubble.
    flush(0);

    let curTurn = 0;
    for (const e of transcript.value) {
      if (e.role === "user") {
        if (e.turn != null) curTurn = e.turn;
        const routing = e.turn != null ? readTurnRouting(e.turn) : undefined;
        out.push(routing ? { ...e, routing } : e);
      } else {
        // An agent bubble pairs with the turn opened by the preceding user
        // message; emit it, then flush that turn's narration after it.
        out.push(e);
        flush(curTurn);
      }
    }
    // Any narration in turns past the last rendered bubble (rare) trails at the
    // end so nothing the machine said is dropped from the conversation.
    for (const turn of [...byTurn.keys()].sort((a, b) => a - b)) flush(turn);
    return out;
  });

  // ---- internal ----
  let _unsubscribe: (() => void) | null = null;
  // Guards maybeRefreshViewOnBackgroundCompletion against duplicate/late
  // turn.end events: the SSE callback can see the same completion event more
  // than once (reconnect replay, backfill). We refresh+push at most once per
  // background-completion turn, and never overlap two refreshes in flight.
  let _bgCompletionRefreshInFlight = false;
  const _bgCompletionRefreshedTurns = new Set<number>();
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
      await loadHarness(source, sessionId);
    } finally {
      loading.value = false;
    }

    // Subscribe for live updates; track stream liveness for the banner.
    _unsubscribe = source.subscribe(
      sessionId,
      (e: TraceEvent) => {
        events.value.push(e);
        applyStatePath(e);
        maybeRefreshViewOnBackgroundCompletion(source, sessionId, e);
      },
      (state) => {
        connectionState.value = state;
      }
    );
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

  /**
   * Surface a scheduler-driven background_completion turn over the live SSE
   * stream. Such a turn is NOT driven by any inbound write RPC — it arrives
   * entirely over the subscription, so none of the RPC write paths
   * (applyTurnResult / loadInitialView / rehydrate) ever run and currentView
   * stays frozen at the pre-completion "…executing" view while the failure
   * `say` / `world.last_error` sit unread in the event log (the session "looks
   * hung").
   *
   * The completion turn ends with a terminal `turn.end` stamped
   * outcome=background_completion (after machine.state_entered /
   * world.update last_error / machine.say have already been pushed). On that
   * event we pull the freshly-landed room view and mirror it into currentView /
   * currentStatePath / terminal, then push a single agent transcript entry so
   * the destination state's failure narration reaches the operator — exactly
   * mirroring how the TUI re-renders on completion (AttachOrchestratorObserver).
   *
   * Guarded against the callback seeing repeated/late events (de-dupe by turn
   * number + an in-flight flag), and a transient view RPC failure is swallowed
   * so it can't break the stream.
   */
  function maybeRefreshViewOnBackgroundCompletion(
    source: DataSource,
    sessionId: string,
    e: TraceEvent
  ): void {
    if (e.msg !== "turn.end" || e.attrs?.outcome !== "background_completion") {
      return;
    }
    if (_bgCompletionRefreshInFlight) return;
    if (typeof e.turn === "number" && _bgCompletionRefreshedTurns.has(e.turn)) {
      return;
    }
    if (typeof e.turn === "number") _bgCompletionRefreshedTurns.add(e.turn);
    _bgCompletionRefreshInFlight = true;
    void source
      .view(sessionId)
      .then((result) => {
        currentView.value = result;
        if (result.state) currentStatePath.value = result.state;
        terminal.value = result.mode === "completed";
        const text = agentText(result);
        const hasElements = (result.typed_view?.Elements?.length ?? 0) > 0;
        if (text || hasElements) {
          transcript.value.push({
            role: "agent",
            text,
            typedView: result.typed_view,
          });
        }
      })
      .catch(() => {})
      .finally(() => {
        _bgCompletionRefreshInFlight = false;
      });
  }

  function traceEventKey(e: TraceEvent): string {
    return JSON.stringify([e.turn, e.msg, e.state_path ?? "", e.attrs ?? {}]);
  }

  async function backfillTurnTrace(
    source: DataSource,
    sessionId: string,
    turn: number
  ): Promise<void> {
    try {
      const { events: fresh } = await source.getTrace(sessionId, {
        since_turn: turn,
      });
      if (!fresh.length) return;

      const seen = new Set(events.value.map(traceEventKey));
      for (const e of fresh) {
        const key = traceEventKey(e);
        if (seen.has(key)) continue;
        seen.add(key);
        events.value.push(e);
        applyStatePath(e);
      }
    } catch {
      // The transcript and final view are still usable if trace reconciliation
      // fails; the live subscription/reconnect path can fill in later.
    }
  }

  /** Stop the live subscription. */
  function teardown(): void {
    _unsubscribe?.();
    _unsubscribe = null;
    _seenStateEntered = false;
    _bgCompletionRefreshInFlight = false;
    _bgCompletionRefreshedTurns.clear();
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
    connectionState.value = "connected";
    selectedEventIndex.value = null;
    highlightedStatePaths.value = [];
    harnessProfiles.value = [];
    harnessModel.value = "";
    harnessEffort.value = "";
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
        // Mark an off-ramp answer so the bubble renders distinctly. The state
        // is unchanged, so the menu / allowed-intents UI persists alongside it.
        ...(result.mode === "offpath" ? { isOffRamp: true } : {}),
        // Carry the CRR receipt so the bubble shows a "routed to … · contextual"
        // chip when the contextual-routing tier resolved this turn.
        ...(result.context_route ? { contextRoute: result.context_route } : {}),
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
    params: { input?: string; intent?: string; slots?: Record<string, unknown> },
    onRouting?: (routing: RoutingInfo, turn?: number) => void
  ): Promise<{ result: TurnResult; streamedText: string; stream: StreamItem[] }> {
    pendingStream.value = [];
    let traceRefreshInFlight = false;
    const refreshTrace = () => {
      if (traceRefreshInFlight) return;
      traceRefreshInFlight = true;
      void backfillTurnTrace(live, sessionId, 0).finally(() => {
        traceRefreshInFlight = false;
      });
    };
    refreshTrace();
    const traceRefreshTimer = globalThis.setInterval(refreshTrace, 750);
    try {
      const result = await live.turnStream(sessionId, method, params, (ev) => {
        refreshTrace();
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
        } else if (ev.type === "routing" && ev.routed_by) {
          onRouting?.(
            {
              routedBy: ev.routed_by,
              matchType: ev.match_type,
              confidence: ev.confidence,
              intent: ev.intent,
            },
            ev.turn
          );
        }
      });
      // Capture the feed before the finally clears the ref (clearing
      // reassigns the array, so this reference stays intact).
      const stream = pendingStream.value;
      const streamedText = stream
        .flatMap((it) => (it.kind === "thinking" ? [it.text] : []))
        .join("\n\n");
      await backfillTurnTrace(live, sessionId, result.turn_number ?? 0);
      return { result, streamedText, stream };
    } finally {
      globalThis.clearInterval(traceRefreshTimer);
      pendingStream.value = [];
    }
  }

  /**
   * Submit an explicit intent (+ slots): push a user transcript entry, advance
   * the session, and apply the resulting view. Streams agent progress via SSE
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
    if (typeof result.turn_number === "number") {
      await backfillTurnTrace(source, sessionId, result.turn_number);
    }
    applyTurnResult(result, capturedStream, capturedItems);
    return result;
  }

  /**
   * Send free text as a turn. Streams agent progress via SSE when the source
   * supports it (LiveSource).
   */
  async function sendText(
    source: DataSource,
    sessionId: string,
    text: string,
    _intentName?: string
  ): Promise<TurnResult> {
    const userEntry: TranscriptEntry = { role: "user", text };
    transcript.value.push(userEntry);
    let result: TurnResult;
    let capturedStream = "";
    let capturedItems: StreamItem[] | undefined;
    if ("turnStream" in source) {
      const out = await runTurnStream(source as LiveSource, sessionId, "turn", {
        input: text,
      }, (routing, turn) => {
        userEntry.routing = routing;
        if (typeof turn === "number") userEntry.turn = turn;
      });
      result = out.result;
      capturedStream = out.streamedText;
      capturedItems = out.stream;
    } else {
      result = await source.sendTurn(sessionId, text);
    }
    // Tag the user entry with its turn number so the routing chip can recover
    // provenance from the event log (chatEntries) once the turn.start +
    // transition events land — reactively, surviving the SSE settle lag.
    if (typeof result.turn_number === "number") {
      userEntry.turn = result.turn_number;
      await backfillTurnTrace(source, sessionId, result.turn_number);
    }
    applyTurnResult(result, capturedStream, capturedItems);
    return result;
  }

  /**
   * Rewind one contextual-routing (CRR) decision: reverse the route identified
   * by decisionId and re-dispatch the original utterance (optionally under a new
   * class). Pushes a small "rewound …" user marker, then applies the
   * re-dispatched turn so the transcript reflects the new route. Requires a
   * source that exposes rewindRoute (the live session); a source without it is a
   * no-op (the chip hides the control there). Rejects (propagated to the caller)
   * when the engine can't rewind that route — e.g. an intent-class decision.
   */
  async function rewindRoute(
    source: DataSource,
    sessionId: string,
    decisionId: string,
    newClass?: string,
    reason?: string
  ): Promise<TurnResult | undefined> {
    if (!source.rewindRoute) return undefined;
    transcript.value.push({
      role: "user",
      text: `↺ rewound route ${decisionId}${newClass ? ` → ${newClass}` : ""}`,
    });
    const result = await source.rewindRoute(sessionId, decisionId, newClass, reason);
    applyTurnResult(result);
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
    // No authored label and no slot text: a bare intent fire (e.g. an action
    // button). NEVER echo the raw intent slug (`core__prd__start`) into the
    // operator's chat bubble — humanise it the same way the button label is.
    return humanizeIntent(intent);
  }

  /**
   * Load the harness profiles + selection from the source, when it exposes the
   * optional getHarness. A failure (or an unsupported source) leaves the picker
   * hidden rather than blocking hydrate.
   */
  async function loadHarness(
    source: DataSource,
    sessionId: string
  ): Promise<void> {
    if (!source.getHarness) {
      harnessProfiles.value = [];
      harnessModel.value = "";
      harnessEffort.value = "";
      return;
    }
    try {
      const state = await source.getHarness(sessionId);
      applyHarnessState(state);
    } catch {
      harnessProfiles.value = [];
      harnessModel.value = "";
      harnessEffort.value = "";
    }
  }

  function applyHarnessState(state: {
    profiles: HarnessProfileInfo[];
    selection: { profile: string; model?: string; effort?: string };
  }): void {
    harnessProfiles.value = state.profiles ?? [];
    harnessModel.value = state.selection?.model ?? "";
    harnessEffort.value = state.selection?.effort ?? "";
  }

  /**
   * Switch the active harness profile (and optional model / effort), effective
   * next turn. Re-applies the echoed state so the picker reflects the new
   * selection.
   */
  async function selectProfile(
    source: DataSource,
    sessionId: string,
    profile: string,
    model?: string,
    effort?: string
  ): Promise<void> {
    if (!source.setSelection) return;
    const state = await source.setSelection(sessionId, profile, model, effort);
    applyHarnessState(state);
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
    connectionState,
    highlightedStatePaths,
    highlightTick,
    usageTotals,
    transcript,
    chatEntries,
    currentView,
    allowedIntents,
    pendingStream,
    harnessProfiles,
    harnessModel,
    harnessEffort,
    harnessActiveProfile,
    // actions
    hydrate,
    selectProfile,
    rehydrate,
    teardown,
    selectEvent,
    clearSelection,
    setHighlightedStatePaths,
    loadInitialView,
    submitIntent,
    sendText,
    rewindRoute,
    applyTurnResult,
  };
});
