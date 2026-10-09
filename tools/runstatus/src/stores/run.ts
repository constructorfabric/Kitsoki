import { defineStore, acceptHMRUpdate } from "pinia";
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
  OperationDriveSummary,
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
  /**
   * The state path the turn routed FROM (the turn.start event's own
   * state_path). Carried so the WS-C C4 thumbs-up/down feedback control can
   * journal a verdict without a second server round-trip — see
   * runstatus.session.routing_feedback / Orchestrator.RecordRoutingFeedback.
   */
  statePath?: string;
}

export interface OperationRunSummary {
  status: string;
  policyId: string;
  operationId: string;
  title: string;
  phase?: string;
  mode?: string;
  executionMode?: string;
  runInBackground?: boolean;
  from?: string;
  to?: string;
  entryIntent?: string;
  terminalState?: string;
  terminalArtifact?: string;
  terminalArtifactHandle?: string;
  stopReason?: string;
  stopDetail?: string;
  phaseSummaryFrom: string[];
  stopOn: string[];
  pauseOn: string[];
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
   * Set once the operator has given a routing-feedback verdict on this turn
   * (WS-C C4's thumbs-up/down control) — the chip disables further clicks and
   * shows which way it went. Persists only for the session's lifetime (not
   * replayed from the trace); a page reload re-enables the control.
   */
  feedbackGiven?: "up" | "down";
  /**
   * Set on a USER bubble dispatched from the media-annotation composer: the
   * artifact the operator pointed at + the picked anchor, so the bubble renders
   * the annotation (a thumbnail of the deck frame with the region/marker) above
   * the typed instruction — exactly like attaching a marked-up screenshot.
   */
  annotation?: {
    mediaHandle: string;
    anchor: import("../lib/annotationAnchor.js").AnnotationAnchor;
  };
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

const OPERATION_LIFECYCLE_STATUS: Record<string, string> = {
  "operation.run_started": "running",
  "operation.waiting": "waiting",
  "operation.completed": "completed",
  "operation.failed": "failed",
};

export function deriveOperationRun(
  traceEvents: TraceEvent[]
): OperationRunSummary | null {
  let current: OperationRunSummary | null = null;
  for (const event of traceEvents) {
    const handle =
      operationRunFromWorldUpdate(event) ?? operationRunFromLifecycleEvent(event);
    if (!handle) continue;
    const next = normalizeOperationRun(handle, current);
    if (next) current = next;
  }
  return current;
}

function operationRunFromWorldUpdate(
  event: TraceEvent
): Record<string, unknown> | null {
  if (event.msg !== "world.update") return null;
  const set = asRecord(event.attrs.set);
  return asRecord(set?.operation_run);
}

function operationRunFromLifecycleEvent(
  event: TraceEvent
): Record<string, unknown> | null {
  const status = OPERATION_LIFECYCLE_STATUS[event.msg];
  if (!status) return null;
  const attrs = asRecord(event.attrs);
  if (!attrs) return null;
  return {
    ...attrs,
    status: readString(attrs, "status") || status,
  };
}

function normalizeOperationRun(
  raw: Record<string, unknown>,
  previous: OperationRunSummary | null
): OperationRunSummary | null {
  const operationId =
    readString(raw, "operation_id") || previous?.operationId || "";
  const policyId =
    readString(raw, "policy_id") || previous?.policyId || operationId;
  const status = readString(raw, "status") || previous?.status || "";
  if (!operationId && !policyId && !status) return null;

  return {
    status,
    policyId,
    operationId: operationId || policyId,
    title:
      readString(raw, "title") ||
      previous?.title ||
      policyId ||
      operationId ||
      "Operation",
    phase: readString(raw, "phase") || previous?.phase,
    mode: readString(raw, "mode") || previous?.mode,
    executionMode:
      readString(raw, "execution_mode") || previous?.executionMode,
    runInBackground:
      readBool(raw, "run_in_background") ?? previous?.runInBackground,
    from: readString(raw, "from") || previous?.from,
    to: readString(raw, "to") || previous?.to,
    entryIntent: readString(raw, "entry_intent") || previous?.entryIntent,
    terminalState:
      readString(raw, "terminal_state") || previous?.terminalState,
    terminalArtifact:
      readString(raw, "terminal_artifact") || previous?.terminalArtifact,
    terminalArtifactHandle:
      readString(raw, "terminal_artifact_handle") ||
      previous?.terminalArtifactHandle,
    stopReason:
      readString(raw, "stop_reason") ||
      readString(raw, "reason") ||
      previous?.stopReason,
    stopDetail:
      readString(raw, "stop_detail") ||
      readString(raw, "detail") ||
      readString(raw, "message") ||
      previous?.stopDetail,
    phaseSummaryFrom:
      readStringArray(raw, "phase_summary_from") ??
      previous?.phaseSummaryFrom ??
      [],
    stopOn: readStringArray(raw, "stop_on") ?? previous?.stopOn ?? [],
    pauseOn: readStringArray(raw, "pause_on") ?? previous?.pauseOn ?? [],
  };
}

function asRecord(value: unknown): Record<string, unknown> | null {
  return value != null && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : null;
}

function readString(source: Record<string, unknown>, key: string): string {
  const value = source[key];
  return typeof value === "string" ? value : "";
}

function readBool(source: Record<string, unknown>, key: string): boolean | undefined {
  const value = source[key];
  return typeof value === "boolean" ? value : undefined;
}

function readStringArray(
  source: Record<string, unknown>,
  key: string
): string[] | undefined {
  const value = source[key];
  if (!Array.isArray(value)) return undefined;
  return value.filter((item): item is string => typeof item === "string");
}

export const useRunStore = defineStore("run", () => {
  // ---- state ----
  const appDef = ref<AppDef | null>(null);
  const mermaid = ref<MermaidSnapshot | null>(null);
  const events = ref<TraceEvent[]>([]);
  const eventKeys = new Set<string>();
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
  // True while a store-driven turn is in flight (submit/turn/continue). A
  // surface watches this to show the "agent is working" thinking bubble even for
  // dispatches it didn't originate — e.g. an annotation sent from a media
  // ViewElement, which calls submitIntent directly rather than the chat input.
  const busy = ref(false);
  // The latest place an embedded artifact reports it is showing (the generic
  // `embed:view` protocol — see lib/embedView.ts). `embedScope` is the opaque
  // token a refine carries as the `current_scene` slot so the edit targets the
  // slide/view the operator is actually looking at; `embedLabel` is for display.
  // `embedStep` is the producer-native reveal/transition within that scope,
  // used only to reopen annotation embeds at the exact same visual position.
  // Producer-neutral: kitsoki never interprets the scope.
  const embedScope = ref<string>("");
  const embedStep = ref<string>("");
  const embedLabel = ref<string>("");
  function setEmbedView(view: { scope: string; step?: string; label?: string }): void {
    embedScope.value = view.scope;
    embedStep.value = view.step ?? "";
    embedLabel.value = view.label ?? "";
  }
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
  const operationRun = computed<OperationRunSummary | null>(() =>
    deriveOperationRun(events.value)
  );

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
          statePath: e.state_path || undefined,
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
      replaceTraceEvents(traceResult.events);
      await loadHarness(source, sessionId);
    } finally {
      loading.value = false;
    }

    // Subscribe for live updates; track stream liveness for the banner.
    _unsubscribe = source.subscribe(
      sessionId,
      (e: TraceEvent) => {
        if (!appendTraceEvent(e)) return;
        applyStatePath(e);
        applyOperationTerminal(e);
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

  function applyOperationTerminal(e: TraceEvent): void {
    const run = operationRunFromWorldUpdate(e) ?? operationRunFromLifecycleEvent(e);
    if (!run) return;
    const status = readString(run, "status");
    if (!["waiting", "completed", "failed"].includes(status)) return;
    const terminalState = readString(run, "terminal_state");
    if (terminalState) currentStatePath.value = terminalState;
    terminal.value = true;
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
    return JSON.stringify([
      e.time,
      e.level,
      e.msg,
      e.session_id,
      e.turn,
      e.state_path ?? "",
      e.parent_turn ?? 0,
      e.attrs ?? {},
    ]);
  }

  function replaceTraceEvents(next: TraceEvent[]): void {
    events.value = next.slice();
    eventKeys.clear();
    for (const e of events.value) eventKeys.add(traceEventKey(e));
  }

  function appendTraceEvent(e: TraceEvent): boolean {
    const key = traceEventKey(e);
    if (eventKeys.has(key)) return false;
    eventKeys.add(key);
    events.value.push(e);
    return true;
  }

  function maxKnownTurn(): number {
    let max = currentView.value?.turn_number ?? 0;
    for (const e of events.value) {
      if (typeof e.turn === "number") max = Math.max(max, e.turn);
    }
    return max;
  }

  async function backfillTraceSince(
    source: DataSource,
    sessionId: string,
    sinceTurn: number
  ): Promise<void> {
    try {
      const { events: fresh } = await source.getTrace(sessionId, {
        since_turn: sinceTurn,
      });
      if (!fresh.length) return;

      for (const e of fresh) {
        if (!appendTraceEvent(e)) continue;
        applyStatePath(e);
        applyOperationTerminal(e);
      }
    } catch {
      // The transcript and final view are still usable if trace reconciliation
      // fails; the live subscription/reconnect path can fill in later.
    }
  }

  async function backfillTurnTrace(
    source: DataSource,
    sessionId: string,
    turn: number
  ): Promise<void> {
    await backfillTraceSince(source, sessionId, turn);
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
    eventKeys.clear();
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
    params: {
      input?: string;
      intent?: string;
      slots?: Record<string, unknown>;
      anchor?: import("../lib/annotationAnchor.js").AnnotationAnchor;
    },
    onRouting?: (routing: RoutingInfo, turn?: number) => void
  ): Promise<{ result: TurnResult; streamedText: string; stream: StreamItem[] }> {
    pendingStream.value = [];
    let pendingNarration = "";
    const flushNarration = (): void => {
      if (!pendingNarration.trim()) {
        pendingNarration = "";
        return;
      }
      const next = pendingStream.value.slice();
      appendThought(next, pendingNarration.trimEnd());
      pendingStream.value = next;
      pendingNarration = "";
    };
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
        // Extended thinking is never the reply, so it renders immediately.
        // Plain narration is ambiguous: it may be an intermediate thought, or
        // it may be the final answer that the done frame/result will present.
        // Hold each narration delta until later activity proves it
        // intermediate, mirroring the TUI and meta overlay deferral.
        if (ev.type === "think" && ev.text) {
          flushNarration();
          const next = pendingStream.value.slice();
          appendThought(next, ev.text);
          pendingStream.value = next;
        } else if (ev.type === "delta" && ev.text) {
          if (pendingNarration && !/\s$/.test(pendingNarration)) {
            flushNarration();
            pendingNarration = ev.text;
          } else {
            pendingNarration += ev.text;
          }
        } else if (ev.type === "tool" && ev.tool) {
          flushNarration();
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
      const fallbackText = pendingNarration.trim() || streamedText;
      await backfillTurnTrace(live, sessionId, result.turn_number ?? 0);
      return { result, streamedText: fallbackText, stream };
    } finally {
      globalThis.clearInterval(traceRefreshTimer);
      pendingNarration = "";
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
    displayLabel?: string,
    opts?: {
      anchor?: import("../lib/annotationAnchor.js").AnnotationAnchor;
      annotation?: TranscriptEntry["annotation"];
    }
  ): Promise<TurnResult> {
    transcript.value.push({
      role: "user",
      text: userText(intent, slots, displayLabel),
      ...(opts?.annotation ? { annotation: opts.annotation } : {}),
    });
    busy.value = true;
    const sinceTurn = maxKnownTurn() + 1;
    try {
      let result: TurnResult;
      let capturedStream = "";
      let capturedItems: StreamItem[] | undefined;
      if ("turnStream" in source) {
        const out = await runTurnStream(source as LiveSource, sessionId, "submit", {
          intent,
          slots,
          ...(opts?.anchor ? { anchor: opts.anchor } : {}),
        });
        result = out.result;
        capturedStream = out.streamedText;
        capturedItems = out.stream;
      } else {
        result = opts?.anchor
          ? await source.submit(sessionId, intent, slots, opts.anchor)
          : await source.submit(sessionId, intent, slots);
      }
      if (result.operation_drive) {
        await backfillTraceSince(source, sessionId, sinceTurn);
      } else if (typeof result.turn_number === "number") {
        await backfillTurnTrace(source, sessionId, result.turn_number);
      }
      applyTurnResult(result, capturedStream, capturedItems);
      appendOperationDriveSummary(result.operation_drive);
      return result;
    } finally {
      busy.value = false;
    }
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
    const sinceTurn = maxKnownTurn() + 1;
    let result: TurnResult;
    let capturedStream = "";
    let capturedItems: StreamItem[] | undefined;
    // When the operator is viewing an embedded deck, ride the slide they're
    // looking at as a `current_scene` supplement slot so a free-text refine
    // targets THAT slide with no annotation needed (gap-fill only — the router's
    // own classification wins). Producer-neutral: `embedScope` is an opaque token.
    const viewSlots: Record<string, unknown> = embedScope.value
      ? { current_scene: embedScope.value }
      : {};
    if ("turnStream" in source) {
      const out = await runTurnStream(source as LiveSource, sessionId, "turn", {
        input: text,
        ...(embedScope.value ? { slots: viewSlots } : {}),
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
      userEntry.turn = result.operation_drive ? sinceTurn : result.turn_number;
      if (result.operation_drive) {
        await backfillTraceSince(source, sessionId, sinceTurn);
      } else {
        await backfillTurnTrace(source, sessionId, result.turn_number);
      }
    }
    applyTurnResult(result, capturedStream, capturedItems);
    appendOperationDriveSummary(result.operation_drive);
    return result;
  }

  /**
   * Drive a running autonomous/supervised operation until the orchestrator reaches its
   * next safe checkpoint. Unlike a normal operator turn, one drive can cascade
   * through several internal turns, so backfill from the highest turn we knew
   * before the RPC rather than only the returned terminal turn.
   */
  async function driveOperation(
    source: DataSource,
    sessionId: string
  ): Promise<TurnResult> {
    transcript.value.push({ role: "user", text: "Drive operation" });
    busy.value = true;
    const sinceTurn = maxKnownTurn() + 1;
    try {
      const result = await source.driveOperation(sessionId);
      await backfillTraceSince(source, sessionId, sinceTurn);
      applyTurnResult(result);
      appendOperationDriveSummary(result.operation_drive);
      return result;
    } finally {
      busy.value = false;
    }
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

  /**
   * Record an operator up/down verdict on a routed turn — the web chat's
   * thumbs-up/down control (WS-C C4), the browser twin of the TUI's `/route
   * up|down` command. entry carries the turn number + the routing provenance
   * (state/intent/tier) readTurnRouting already recovered from the trace, and
   * entry.text is the original phrase — nothing is looked up server-side.
   * Journals through the SAME event the TUI writes
   * (Orchestrator.RecordRoutingFeedback); does not advance the turn, so there
   * is no TurnResult to apply. A source without routingFeedback (artifact/
   * snapshot sources) makes this a no-op — the control hides there.
   */
  async function sendRoutingFeedback(
    source: DataSource,
    sessionId: string,
    entry: TranscriptEntry,
    verdict: "up" | "down"
  ): Promise<void> {
    if (!source.routingFeedback || entry.turn == null) return;
    await source.routingFeedback(sessionId, {
      state: entry.routing?.statePath ?? "",
      intent: entry.routing?.intent ?? "",
      phrase: entry.text,
      tier: entry.routing?.routedBy ?? "",
      verdict,
    });
    // Mutate the master transcript entry (not the derived chatEntries copy) so
    // the disabled/given state survives the next reactive recompute.
    const master = transcript.value.find((e) => e.turn === entry.turn && e.role === "user");
    if (master) master.feedbackGiven = verdict;
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

  function appendOperationDriveSummary(summary?: OperationDriveSummary): void {
    const text = operationDriveSummaryText(summary);
    if (!text) return;
    transcript.value.push({ role: "narration", text });
  }

  function operationDriveSummaryText(summary?: OperationDriveSummary): string {
    if (!summary) return "";
    const turns =
      typeof summary.turns === "number" && Number.isFinite(summary.turns)
        ? summary.turns
        : 0;
    const base = turns === 1 ? "Drove 1 turn" : `Drove ${turns} turns`;
    const intent = summary.last_intent?.trim()
      ? ` via ${humanizeIntent(summary.last_intent)}`
      : "";
    const stop = operationDriveStopLabel(summary.stop_reason);
    return stop ? `${base}${intent}; stopped ${stop}.` : `${base}${intent}.`;
  }

  function operationDriveStopLabel(reason?: string): string {
    const normalized = (reason || "").trim();
    switch (normalized) {
      case "":
        return "";
      case "no-driver-intent":
        return "at a checkpoint";
      case "clarify":
        return "for missing input";
      case "rejected":
        return "after a rejected turn";
      case "offpath":
        return "after an off-path answer";
      case "cancelled":
        return "after cancellation";
      case "max-turns":
        return "at the safety turn limit";
      case "terminal":
        return "at a terminal state";
      case "no-operation":
        return "because there is no active operation";
      case "operation-not-autonomous":
        return "because this operation needs manual input";
      default:
        if (normalized.startsWith("operation-")) {
          return `because the operation is ${normalized.slice("operation-".length).replace(/_/g, " ")}`;
        }
        return `because ${normalized.replace(/_/g, " ")}`;
    }
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
    operationRun,
    transcript,
    chatEntries,
    currentView,
    allowedIntents,
    pendingStream,
    busy,
    embedScope,
    embedStep,
    embedLabel,
    setEmbedView,
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
    driveOperation,
    rewindRoute,
    sendRoutingFeedback,
    applyTurnResult,
  };
});

// Preserve the run store (transcript, current view, …) across Vite HMR. Without
// this, editing this module hot-reloads the store and RESETS its state — the
// conversation appears to "lose" all but the most recent messages. The accept
// hook hands the updated store definition to the existing instance instead.
if (import.meta.hot) {
  import.meta.hot.accept(acceptHMRUpdate(useRunStore, import.meta.hot));
}
