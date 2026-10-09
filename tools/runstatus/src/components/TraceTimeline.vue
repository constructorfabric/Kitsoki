<template>
  <div class="trace-timeline" :class="{ 'trace-timeline--compact': compact }">
    <!-- Compact (docked panel): a one-line toggle so the filter bar doesn't crowd
         out the rows; collapsed by default. -->
    <button
      v-if="compact"
      type="button"
      class="trace-timeline__filters-toggle"
      :aria-expanded="filtersOpen"
      @click="filtersOpen = !filtersOpen"
    >
      <span>{{ filtersOpen ? '▾' : '▸' }} Filters</span>
      <span v-if="hasActiveFilters" class="trace-timeline__filters-active" title="filters active">●</span>
    </button>
    <!-- Filter bar -->
    <div v-show="filtersOpen" class="trace-timeline__filters">
      <!-- Subsystem chips -->
      <div class="trace-timeline__filter-group">
        <span class="trace-timeline__filter-label">Subsystem:</span>
        <button
          v-for="sys in ALL_SUBSYSTEMS"
          :key="sys"
          class="trace-timeline__chip"
          :class="{ active: selectedSubsystems.has(sys) }"
          :data-testid="`subsystem-chip-${sys}`"
          @click="toggleSubsystem(sys)"
        >{{ sys }}</button>
      </div>

      <!-- Category chips -->
      <div class="trace-timeline__filter-group" data-testid="category-filter-chips">
        <span class="trace-timeline__filter-label">Category:</span>
        <button
          v-for="cat in allCategories"
          :key="cat"
          class="trace-timeline__chip trace-timeline__chip--category"
          :class="{ active: selectedCategories.has(cat) }"
          :style="selectedCategories.has(cat) ? { borderColor: COLOR_MAP[cat], color: COLOR_MAP[cat] } : {}"
          @click="toggleCategory(cat)"
        >
          <span class="trace-timeline__cat-dot" :style="{ background: COLOR_MAP[cat] }"></span>
          {{ cat }}
        </button>
      </div>

      <!-- State path single-select -->
      <div class="trace-timeline__filter-group">
        <span class="trace-timeline__filter-label">State:</span>
        <select
          class="trace-timeline__select"
          :value="selectedStatePath ?? ''"
          @change="onStatePathChange"
        >
          <option value="">All</option>
          <option v-for="sp in availableStatePaths" :key="sp" :value="sp">{{ sp }}</option>
        </select>
      </div>

      <!-- Clear -->
      <button
        v-if="hasActiveFilters"
        class="trace-timeline__chip trace-timeline__chip--clear"
        @click="clearFilters"
      >Clear</button>
    </div>

    <!-- Timeline body -->
    <div
      class="trace-timeline__body"
      ref="bodyRef"
      @scroll="onScroll"
    >
      <!-- Virtual spacer (top) -->
      <div v-if="useVirtualisation" :style="{ height: topSpacerHeight + 'px' }" />

      <template v-for="section in visiblePhases" :key="section.phaseKey">
        <!-- Phase header -->
        <div
          class="trace-timeline__phase-header"
          @click="togglePhaseCollapse(section.phaseKey)"
        >
          <span class="trace-timeline__turn-caret">{{ collapsedPhases.has(section.phaseKey) ? '▶' : '▼' }}</span>
          <span class="trace-timeline__turn-phase">{{ section.phase ?? '—' }}</span>
          <span class="trace-timeline__turn-count">{{ section.totalEvents }} event{{ section.totalEvents !== 1 ? 's' : '' }}</span>
        </div>

        <template v-if="!collapsedPhases.has(section.phaseKey)">
          <template v-for="group in section.turnGroups" :key="group.groupKey">
            <!-- Off-path turn sub-header -->
            <div
              v-if="group.isOffPath"
              class="trace-timeline__turn-header trace-timeline__turn-header--offpath trace-timeline__turn-header--sub"
              @click="toggleTurnCollapse(group.groupKey)"
            >
              <span class="trace-timeline__turn-offpath-indent">↳</span>
              <span class="trace-timeline__turn-caret">{{ collapsedTurns.has(group.groupKey) ? '▶' : '▼' }}</span>
              <span class="trace-timeline__turn-offpath-label">off-path (turn {{ group.parentTurn }})</span>
              <span class="trace-timeline__turn-count">{{ group.events.length }} event{{ group.events.length !== 1 ? 's' : '' }}</span>
            </div>

            <!-- Normal visit sub-header: one room visit = one accepted intent -->
            <div
              v-else
              class="trace-timeline__turn-header trace-timeline__turn-header--sub"
              @click="toggleTurnCollapse(group.groupKey)"
            >
              <span class="trace-timeline__turn-caret">{{ collapsedTurns.has(group.groupKey) ? '▶' : '▼' }}</span>
              <span v-if="group.intent" class="trace-timeline__intent-label">
                <span class="trace-timeline__intent-kw">intent</span>
                <span class="trace-timeline__intent-value">{{ group.intent }}</span>
              </span>
              <span class="trace-timeline__turn-label">{{ fmtTurnSpan(group.turnSpan) }}</span>
              <span class="trace-timeline__turn-count">{{ group.events.length }} event{{ group.events.length !== 1 ? 's' : '' }}</span>
            </div>

            <!-- Rows within turn (hidden if collapsed) -->
            <template v-if="!collapsedTurns.has(group.groupKey)">
              <div
                v-for="row in group.events"
                :key="row.index"
                class="trace-timeline__row"
                :class="{
                  selected: row.index === selectedEventIndex,
                  expanded: expandedRows.has(row.index),
                  highlighted: isHighlighted(row.event.state_path),
                }"
                :data-event-index="row.index"
                data-testid="trace-event-row"
                @click="onRowClick(row.index)"
              >
                <div class="trace-timeline__row-main">
                  <span
                    class="trace-timeline__obs-dot"
                    :style="{ background: COLOR_MAP[observationKind(row.event.msg)] }"
                    :title="observationKind(row.event.msg)"
                  ></span>
                  <span
                    class="trace-timeline__subsystem-chip"
                    :data-subsystem="row.event.msg === 'turn.input' ? 'user' : row.subsystem"
                  >{{ row.event.msg === "turn.input" ? "user" : row.subsystem }}</span>
                  <span class="trace-timeline__msg">
                    <template v-if="row.effectGroup">
                      world.update
                      <span class="trace-timeline__effect-count">{{ row.effectGroup.count }} keys</span>
                    </template>
                    <template v-else-if="row.event.msg === 'turn.input'">{{ String(row.event.attrs.input ?? "") }}</template>
                    <template v-else-if="row.narration != null">
                      <span class="trace-timeline__say-label">say</span>
                      <span class="trace-timeline__say-text">{{ row.narration }}</span>
                    </template>
                    <template v-else>{{
                      row.agent ? `agent.${row.agent.verb}`
                      : row.event.msg === "agent.stream" ? agentStreamLabel(row.event)
                      : row.harnessCall ? row.harnessCall.namespace
                      : row.event.msg
                    }}</template>
                  </span>
                  <!-- Harness provenance: which profile/model answered this call.
                       Matches the operator's live picker selection. -->
                  <span
                    v-if="row.agent && (row.agent.profile || row.agent.model || row.agent.effort)"
                    class="trace-timeline__harness"
                    data-testid="trace-harness-label"
                    :title="`harness profile / model / effort for this agent call`"
                  >
                    <span v-if="row.agent.profile" class="trace-timeline__harness-profile">{{ row.agent.profile }}</span>
                    <span v-if="row.agent.model" class="trace-timeline__harness-model">{{ row.agent.model }}</span>
                    <span v-if="row.agent.effort" class="trace-timeline__harness-effort">effort:{{ row.agent.effort }}</span>
                  </span>
                  <span
                    v-if="(row.agent?.durationMs ?? row.harnessCall?.durationMs) != null"
                    class="trace-timeline__duration"
                  >{{ fmtMs((row.agent?.durationMs ?? row.harnessCall!.durationMs)!) }}</span>
                  <span
                    v-else-if="row.agent?.incomplete || row.harnessCall?.incomplete"
                    class="trace-timeline__incomplete"
                    title="Call did not complete or its completion was not recorded"
                  >incomplete</span>
                  <span
                    v-if="row.agent && agentCostStr(row.agent.merged)"
                    class="trace-timeline__cost"
                    title="Estimated cost for this agent call (meta.cost_usd)"
                  >{{ agentCostStr(row.agent.merged) }}</span>
                  <!-- Annotation badge: shown when at least one annotation targets this event's call_id. -->
                  <template v-if="rowAnnotations(row.event).length > 0">
                    <span
                      v-for="(ann, ai) in rowAnnotations(row.event)"
                      :key="ai"
                      class="trace-timeline__annotation-badge"
                      :title="[ann.label, ann.score != null ? `score: ${ann.score}` : '', ann.comment].filter(Boolean).join(' · ')"
                    >
                      <template v-if="ann.label">{{ ann.label }}</template>
                      <template v-else-if="ann.score != null">{{ ann.score.toFixed(2) }}</template>
                      <template v-else>ann</template>
                    </span>
                  </template>
                  <span class="trace-timeline__time">{{ formatTime(row.event.time) }}</span>
                  <button
                    class="trace-timeline__expand-btn"
                    @click.stop="toggleRowExpand(row.index)"
                    :title="expandedRows.has(row.index) ? 'Collapse' : 'Expand'"
                  >{{ expandedRows.has(row.index) ? '−' : '+' }}</button>
                  <button
                    class="trace-timeline__copy-btn"
                    :class="{ 'trace-timeline__copy-btn--copied': copiedRows.has(row.index) }"
                    @click.stop="copyRow(row)"
                    title="Copy as JSON"
                  >{{ copiedRows.has(row.index) ? '✓' : '⎘' }}</button>
                </div>

                <!-- Expanded body: effect groups get the world diff viewer; everything else uses EventDetail. -->
                <div v-if="expandedRows.has(row.index)" class="trace-timeline__row-body" @click.stop>
                  <template v-if="row.effectGroup">
                    <WorldDiffViewer :before="row.effectGroup.before" :after="row.effectGroup.after" />
                  </template>
                  <template v-else>
                    <div v-if="row.agent?.incomplete" class="trace-timeline__incomplete-banner">
                      Agent call started but no completion event was recorded.
                    </div>
                    <div
                      v-if="row.agent && row.agent.streamEvents.length > 0"
                      class="trace-timeline__agent-stream"
                      data-testid="trace-agent-stream"
                    >
                      <div class="trace-timeline__agent-stream-title">
                        Agent stream
                        <span class="trace-timeline__agent-stream-count">{{ row.agent.streamEvents.length }}</span>
                      </div>
                      <div
                        v-for="stream in row.agent.streamEvents"
                        :key="`${stream.time}:${stream.attrs.call_id}:${stream.attrs.type}:${stream.attrs.preview ?? ''}`"
                        class="trace-timeline__agent-stream-row"
                        :class="{ 'trace-timeline__agent-stream-row--warning': isWarningStream(stream) }"
                        data-testid="trace-agent-stream-row"
                      >
                        <span class="trace-timeline__agent-stream-kind">{{ agentStreamLabel(stream) }}</span>
                        <span class="trace-timeline__agent-stream-text">{{ agentStreamText(stream) }}</span>
                      </div>
                    </div>
                    <div v-if="row.harnessCall?.incomplete" class="trace-timeline__incomplete-banner">
                      Host call dispatched but no returned event was recorded.
                    </div>
                    <EventDetail
                      :event="row.agent?.merged ?? row.event"
                      :harnessCall="row.harnessCall"
                      :sessionId="props.sessionId || (row.agent?.merged ?? row.event).session_id"
                    />
                  </template>
                </div>
              </div>
            </template>
          </template>
        </template>
      </template>

      <!-- Virtual spacer (bottom) -->
      <div v-if="useVirtualisation" :style="{ height: bottomSpacerHeight + 'px' }" />
    </div>

    <!-- Empty state -->
    <div v-if="filteredEvents.length === 0" class="trace-timeline__empty">
      No events{{ hasActiveFilters ? ' match the current filters' : '' }}.
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, reactive, watch, nextTick, onMounted, onUnmounted } from "vue";
import type { TraceEvent, AnnotationEntry } from "../types.js";
import EventDetail from "./EventDetail.vue";
import WorldDiffViewer from "./WorldDiffViewer.vue";
import { parseDiagram } from "../diagram/parse.js";
import { fmtMs, fmtCost, readAgentUsage } from "./agent/lib.js";
import { observationKind, COLOR_MAP } from "../lib/observation.js";
import type { ObservationKind } from "../lib/observation.js";

// Cost string for a merged agent row, shown inline next to the duration in the
// collapsed timeline row. Reads the canonical attrs.meta.cost_usd (with the
// legacy flat fallback). Returns "" when no cost was recorded, so the span hides.
function agentCostStr(merged: TraceEvent): string {
  return fmtCost(readAgentUsage(merged.attrs).costUsd);
}

// ---- props & emits ----------------------------------------------------------

const props = defineProps<{
  events: TraceEvent[];
  selectedEventIndex: number | null;
  /** State paths whose rows should be visually marked. */
  highlightedStatePaths?: string[];
  /** Bumped each time highlight changes — triggers scroll-into-view. */
  highlightTick?: number;
  /** Mermaid source — used to derive phase names for turn headers. */
  mermaidSource?: string | null;
  /** Operator annotations from the session sidecar. Used to render score/label badges. */
  annotations?: AnnotationEntry[];
  /** Session ID passed down so EventDetail can show the annotate/replay buttons. */
  sessionId?: string;
  /** Compact mode (a docked surface in a narrow/short VS Code panel): collapse the
   * filter bar behind a toggle (default collapsed) so the timeline rows — the
   * point of the panel — fill the height instead of being crowded out. */
  compact?: boolean;
}>();

const emit = defineEmits<{
  (e: "select", index: number): void;
}>();

// In compact mode the filter bar starts collapsed (rows get the height); in the
// full browser layout it is always shown (no toggle).
const filtersOpen = ref(!props.compact);

// rowAnnotations returns the annotations that target a given event, matched by
// call_id (preferred) or turn.  Returns [] when no annotations exist.
function rowAnnotations(event: TraceEvent): AnnotationEntry[] {
  const anns = props.annotations;
  if (!anns || anns.length === 0) return [];
  const callId = event.attrs?.call_id as string | undefined;
  return anns.filter((a) => {
    if (callId && a.target_call_id && a.target_call_id === callId) return true;
    if (!callId && a.target_turn && a.target_turn === event.turn) return true;
    return false;
  });
}

// ---- constants --------------------------------------------------------------

const ALL_SUBSYSTEMS = ["turn", "machine", "world", "host", "agent", "harness", "other"] as const;
type Subsystem = (typeof ALL_SUBSYSTEMS)[number];

// Canonical agent events: the verb lives in attrs.verb, not the msg.  The
// engine only ever emits agent.call.start / agent.call.complete; the per-verb
// msg shape (agent.decide.start, …) was a fiction the consumer used to assume.
const AGENT_START_MSG = "agent.call.start";
const AGENT_COMPLETE_MSG = "agent.call.complete";
const AGENT_ERROR_MSG = "agent.call.error";
const AGENT_STREAM_MSG = "agent.stream";
function agentVerb(e: TraceEvent): string {
  return typeof e.attrs.verb === "string" ? e.attrs.verb : "";
}

function agentStreamLabel(e: TraceEvent): string {
  if (isWarningStream(e)) return "agent.warning";
  if (typeof e.attrs.tool === "string" && e.attrs.tool) return `agent.tool ${e.attrs.tool}`;
  if (typeof e.attrs.thinking === "string" && e.attrs.thinking) return "agent.thinking";
  if (typeof e.attrs.text === "string" && e.attrs.text) return "agent.delta";
  return "agent.stream";
}

function agentStreamText(e: TraceEvent): string {
  if (typeof e.attrs.preview === "string" && e.attrs.preview) return e.attrs.preview;
  if (typeof e.attrs.thinking === "string" && e.attrs.thinking) return e.attrs.thinking;
  if (typeof e.attrs.text === "string" && e.attrs.text) return e.attrs.text;
  return "";
}

function isWarningStream(e: TraceEvent): boolean {
  const severity = typeof e.attrs.severity === "string" ? e.attrs.severity : "";
  if (severity === "warn" || severity === "warning") return true;
  const type = typeof e.attrs.type === "string" ? e.attrs.type : "";
  return type === "ladder_fallback" || type === "ladder_session_pinned" || type === "ladder_stop" || type === "ladder_exhausted";
}

// Virtualisation is only worth the complexity for very large traces.  The
// windowing math assumes a uniform row height, but rows (~27px) and turn
// headers (~32px) actually differ — and expanded rows differ a lot more.
// At the previous threshold (200) a mid-sized fixture (~230 events)
// engaged virtualisation and scrolled with a ~9px lurch per row because the
// spacer math walked faster than the real content.  Keep it off until traces
// are large enough that flat rendering would actually hurt.
const VIRTUALISATION_THRESHOLD = 5000;
const ROW_HEIGHT_ESTIMATE = 27; // px — used for windowing math
const WINDOW_OVERSCAN = 20; // extra rows above/below visible window

// ---- filter state -----------------------------------------------------------

// "turn" (turn.start / turn.end) is off by default — the group headers already
// convey turn boundaries; these rows add noise without new information.
const selectedSubsystems = reactive(new Set<Subsystem>(ALL_SUBSYSTEMS.filter((s) => s !== "turn")));
const selectedStatePath = ref<string | null>(null);
const collapsedTurns = reactive(new Set<string>());
const collapsedPhases = reactive(new Set<string>());
const expandedRows = reactive(new Set<number>());
const copiedRows = reactive(new Set<number>());

// Category filter — derived from ObservationKind. All categories selected by default.
// selectedCategories starts as all-selected; we track a Set of active categories.
const selectedCategories = reactive(new Set<ObservationKind>([
  "decision", "agent-call", "host-call", "narration", "world-mutation", "routing", "lifecycle",
]));

// ---- virtualisation state ---------------------------------------------------

const bodyRef = ref<HTMLElement | null>(null);
const scrollTop = ref(0);
const clientHeight = ref(600); // sensible default; updated on scroll

// ---- helpers ----------------------------------------------------------------

function subsystemFromMsg(msg: string): Subsystem {
  const prefix = msg.split(".")[0] ?? "";
  switch (prefix) {
    case "turn":    return "turn";
    case "harness": return "harness";
    case "machine": return "machine";
    case "world":   return "world";
    case "host":    return "host";
    case "agent":  return "agent";
    default:        return "other";
  }
}

function formatTime(iso: string): string {
  try {
    const d = new Date(iso);
    return d.toISOString().replace("T", " ").replace("Z", "").slice(11); // HH:MM:SS.mmm
  } catch {
    return iso;
  }
}

// ---- derived ----------------------------------------------------------------

// All observation categories present in the current event list, in canonical order.
const CATEGORY_ORDER: ObservationKind[] = [
  "decision", "agent-call", "host-call", "narration", "world-mutation", "routing", "lifecycle",
];
const allCategories = computed<ObservationKind[]>(() => {
  const present = new Set<ObservationKind>();
  for (const e of props.events) present.add(observationKind(e.msg));
  return CATEGORY_ORDER.filter((c) => present.has(c));
});

// All available state paths.
const availableStatePaths = computed(() => {
  const s = new Set<string>();
  for (const e of props.events) if (e.state_path) s.add(e.state_path);
  return [...s].sort();
});

// Default-off subsystems: these are not selected by default, so their absence
// does not count as an "active filter" that warrants a Clear button.
const DEFAULT_OFF_SUBSYSTEMS: ReadonlySet<Subsystem> = new Set(["turn"]);

const hasActiveFilters = computed(() => {
  const defaultSysSelected = ALL_SUBSYSTEMS.every(
    (s) => selectedSubsystems.has(s) || DEFAULT_OFF_SUBSYSTEMS.has(s)
  );
  const noStateFilter = selectedStatePath.value === null;
  const allCatSelected = allCategories.value.every((c) => selectedCategories.has(c));
  return !defaultSysSelected || !noStateFilter || !allCatSelected;
});

// Filtered + annotated events (preserving original index).
interface AgentMerge {
  verb: string;
  complete: TraceEvent | null;
  streamEvents: TraceEvent[];
  durationMs: number | null;
  incomplete: boolean;
  /** The harness profile name selected for this call (from agent.call.start);
   *  "" when the session declared no profiles. Surfaced as a row chip so the
   *  trace shows which backend/provider answered. */
  profile: string;
  /** The model the call ran on (start's attrs.model). "" when unset. */
  model: string;
  /** The reasoning effort the call ran on (start's attrs.effort). "" when unset. */
  effort: string;
  /**
   * The single logical agent call, presented to EventDetail/AgentDetail as
   * one event. The engine records the *prompt* on agent.call.start and the
   * *response* on agent.call.complete; this merge stitches them back together
   * so the detail pane shows both. Shaped as the complete event (msg =
   * agent.call.complete) with the start's prompt/agent/model attrs folded in;
   * complete attrs win on conflict. Faithful to the trace — it surfaces what
   * the two paired events together recorded, inventing nothing.
   */
  merged: TraceEvent;
}

interface EffectGroupData {
  /** Number of machine.effect[set] events merged into this single row. */
  count: number;
  /** World state before any set-effects in this turn were applied. */
  before: Record<string, unknown>;
  /** World state after all set-effects in this turn were applied. */
  after: Record<string, unknown>;
}

// Merged view of a harness.called/dispatched/returned triplet.
interface HarnessCallData {
  namespace: string;
  args: unknown;
  data: unknown;
  error: unknown;
  durationMs: number | null;
  incomplete: boolean;
}

interface AnnotatedEvent {
  index: number;
  event: TraceEvent;
  subsystem: Subsystem;
  agent?: AgentMerge;
  harnessCall?: HarnessCallData;
  /** Present on the lead row of a grouped machine.effect[set] batch. */
  effectGroup?: EffectGroupData;
  /** Operator narration text for a machine.say event. */
  narration?: string;
}

// Compute world state before/after each turn's machine.effect[set] batch.
// Keyed by turn number.  Derived from the raw (unfiltered) events so it stays
// correct even when the "machine" subsystem chip is deselected.
interface TurnWorldState {
  before: Record<string, unknown>;
  after: Record<string, unknown>;
  count: number; // number of set-effect events in this turn
}

const worldStateByTurn = computed<Map<number, TurnWorldState>>(() => {
  // Collect set-effects per turn (in event order) and count them.
  const setsByTurn = new Map<number, Array<Record<string, unknown>>>();
  for (const e of props.events) {
    if (e.msg !== "world.update") continue;
    const s = e.attrs.set;
    if (!s || typeof s !== "object" || Array.isArray(s)) continue;
    const arr = setsByTurn.get(e.turn) ?? [];
    arr.push(s as Record<string, unknown>);
    setsByTurn.set(e.turn, arr);
  }

  // Walk turns in ascending order, maintaining a rolling world state.
  const turns = [...new Set(props.events.map((e) => e.turn))].sort((a, b) => a - b);
  const result = new Map<number, TurnWorldState>();
  const running: Record<string, unknown> = {};

  for (const turn of turns) {
    const before = { ...running };
    const sets = setsByTurn.get(turn) ?? [];
    for (const s of sets) Object.assign(running, s);
    if (sets.length > 0) {
      result.set(turn, { before, after: { ...running }, count: sets.length });
    }
  }
  return result;
});

// Pair agent.<verb>.start events with their matching .complete via call_id.
// The merged row sits at the start timestamp and shows elapsed time; the
// .complete row is suppressed.  A start with no complete is rendered with an
// "incomplete" badge.
const agentStartCallIds = computed<Set<string>>(() => {
  const s = new Set<string>();
  for (const e of props.events) {
    if (e.msg !== AGENT_START_MSG) continue;
    const cid = e.attrs.call_id;
    if (typeof cid === "string") s.add(cid);
  }
  return s;
});

const agentTerminalByCallId = computed<Map<string, TraceEvent>>(() => {
  const m = new Map<string, TraceEvent>();
  for (const e of props.events) {
    if (e.msg !== AGENT_COMPLETE_MSG && e.msg !== AGENT_ERROR_MSG) continue;
    const cid = e.attrs.call_id;
    if (typeof cid === "string") m.set(cid, e);
  }
  return m;
});

const agentStreamsByCallId = computed<Map<string, TraceEvent[]>>(() => {
  const m = new Map<string, TraceEvent[]>();
  for (const e of props.events) {
    if (e.msg !== AGENT_STREAM_MSG) continue;
    const cid = e.attrs.call_id;
    if (typeof cid !== "string" || cid === "") continue;
    const events = m.get(cid) ?? [];
    events.push(e);
    m.set(cid, events);
  }
  return m;
});

// Merge harness.called/dispatched/returned triplets into single host-call rows.
// Groups by (turn, namespace); nth called pairs with nth dispatched + nth returned.
// host.agent.* namespaces are excluded — covered by agent.*.start/complete rows.
const harnessCallData = computed<{
  mergeByCalledIndex: Map<number, HarnessCallData>;
  suppressedIndices: Set<number>;
}>(() => {
  type Bucket = { calledIdx: number[]; dispatchedEvt: TraceEvent[]; returnedEvt: TraceEvent[] };
  const byKey = new Map<string, Bucket>();
  const suppressedIndices = new Set<number>();

  for (let i = 0; i < props.events.length; i++) {
    const e = props.events[i]!;
    if (!e.msg.startsWith("harness.")) continue;
    const ns = typeof e.attrs.namespace === "string" ? e.attrs.namespace : "";
    if (ns.startsWith("host.agent.")) continue;
    const key = `${e.turn}:${ns}`;
    if (!byKey.has(key)) byKey.set(key, { calledIdx: [], dispatchedEvt: [], returnedEvt: [] });
    const b = byKey.get(key)!;
    if (e.msg === "harness.called") {
      b.calledIdx.push(i);
    } else if (e.msg === "harness.dispatched") {
      b.dispatchedEvt.push(e);
      suppressedIndices.add(i);
    } else if (e.msg === "harness.returned") {
      b.returnedEvt.push(e);
      suppressedIndices.add(i);
    }
  }

  const mergeByCalledIndex = new Map<number, HarnessCallData>();
  for (const [, b] of byKey) {
    for (let n = 0; n < b.calledIdx.length; n++) {
      const calledIdx = b.calledIdx[n]!;
      const calledEvent = props.events[calledIdx]!;
      const dispatched = b.dispatchedEvt[n] ?? null;
      const returned = b.returnedEvt[n] ?? null;
      let durationMs: number | null = null;
      if (dispatched && returned) {
        const t1 = new Date(dispatched.time).getTime();
        const t2 = new Date(returned.time).getTime();
        if (!isNaN(t1) && !isNaN(t2)) durationMs = t2 - t1;
      }
      mergeByCalledIndex.set(calledIdx, {
        namespace: typeof calledEvent.attrs.namespace === "string" ? calledEvent.attrs.namespace : "",
        args: calledEvent.attrs.args,
        data: returned?.attrs.data,
        error: returned?.attrs.error,
        durationMs,
        incomplete: returned === null,
      });
    }
  }
  return { mergeByCalledIndex, suppressedIndices };
});

const filteredEvents = computed<AnnotatedEvent[]>(() => {
  const startCids = agentStartCallIds.value;
  const terminalMap = agentTerminalByCallId.value;
  const streamMap = agentStreamsByCallId.value;
  const worldStates = worldStateByTurn.value;
  const harnessData = harnessCallData.value;
  const out: AnnotatedEvent[] = [];

  // Tracks which turns have already emitted their grouped effect row.
  const seenEffectTurns = new Set<number>();

  for (let i = 0; i < props.events.length; i++) {
    const event = props.events[i]!;

    // Category filter — applied before subsystem checks so it is orthogonal.
    // turn.input is treated as "routing" (same as turn.start) for the purpose
    // of category filtering.
    {
      const cat = observationKind(event.msg);
      if (!selectedCategories.has(cat)) continue;
    }

    // turn.input (UserInputReceived) is always visible regardless of the "turn"
    // chip state — it carries the user's raw message text for the turn.
    // This is a real event written by the orchestrator, not a synthesized row.
    if (event.msg === "turn.input") {
      if (selectedStatePath.value !== null && event.state_path !== selectedStatePath.value) continue;
      out.push({ index: i, event, subsystem: "turn" });
      continue;
    }

    // Harness events must be handled before the generic subsystem filter:
    // subsystemFromMsg returns "harness" (not in ALL_SUBSYSTEMS), so they
    // would be silently dropped. Merged host-call rows respect the "host" chip.
    if (event.msg.startsWith("harness.")) {
      const ns = event.attrs.namespace;
      // Agent wrapper calls are already covered by agent.*.start rows.
      if (typeof ns === "string" && ns.startsWith("host.agent.")) continue;
      // dispatched/returned rows are absorbed into their paired called row.
      if (harnessData.suppressedIndices.has(i)) continue;
      // Apply normal level/state filters; gate on "host" chip.
      if (!selectedSubsystems.has("host")) continue;
      if (selectedStatePath.value !== null && event.state_path !== selectedStatePath.value) continue;
      // called rows: attach merged data and surface as a host-subsystem row.
      const harnessCall = harnessData.mergeByCalledIndex.get(i);
      out.push({ index: i, event, subsystem: "host", harnessCall });
      continue;
    }

    const subsystem = subsystemFromMsg(event.msg);

    if (!selectedSubsystems.has(subsystem)) continue;
    if (selectedStatePath.value !== null && event.state_path !== selectedStatePath.value) continue;

    // machine.transition / machine.state_exited / machine.state_entered are all
    // absorbed by the turn group structure: from/to are the adjacent turn headers,
    // intent is shown in the user badge (human turns) or agent.decide (LLM turns).
    if (
      event.msg === "machine.transition" ||
      event.msg === "machine.state_exited" ||
      event.msg === "machine.state_entered"
    ) continue;

    // Suppress agent.call.complete/error rows whose paired start exists; the
    // start row carries the merged terminal event and duration.
    if (event.msg === AGENT_COMPLETE_MSG || event.msg === AGENT_ERROR_MSG) {
      const cid = event.attrs.call_id;
      if (typeof cid === "string" && startCids.has(cid)) continue;
    }

    // Stream frames are children of their agent call. They are rendered under
    // the merged agent row instead of as top-level timeline rows.
    if (event.msg === AGENT_STREAM_MSG) {
      const cid = event.attrs.call_id;
      if (typeof cid === "string" && startCids.has(cid)) continue;
    }

    // Group machine.effect[set] events within a turn into one row.
    if (event.msg === "world.update" && event.attrs.set && typeof event.attrs.set === "object" && !Array.isArray(event.attrs.set)) {
      if (seenEffectTurns.has(event.turn)) continue; // absorbed into the lead row
      seenEffectTurns.add(event.turn);
      const ws = worldStates.get(event.turn);
      const effectGroup: EffectGroupData | undefined = ws
        ? { count: ws.count, before: ws.before, after: ws.after }
        : undefined;
      out.push({ index: i, event, subsystem, effectGroup });
      continue;
    }

    // machine.say carries operator narration; render it as a distinct row that
    // shows its text (not a world mutation — world.update is set-only now).
    if (event.msg === "machine.say") {
      const text = typeof event.attrs.text === "string" ? event.attrs.text : "";
      out.push({ index: i, event, subsystem, narration: text });
      continue;
    }

    let agent: AgentMerge | undefined;
    if (event.msg === AGENT_START_MSG) {
      // Verb comes from attrs.verb; fall back to the complete event's verb.
      const cid = typeof event.attrs.call_id === "string" ? event.attrs.call_id : null;
      const complete = cid ? terminalMap.get(cid) ?? null : null;
      const streamEvents = cid ? streamMap.get(cid) ?? [] : [];
      const verb = agentVerb(event) || (complete ? agentVerb(complete) : "");
      const dur = complete && typeof complete.attrs.duration_ms === "number"
        ? (complete.attrs.duration_ms as number)
        : null;
      // Stitch the paired events into one logical call for the detail pane.
      // Base on the complete event (so msg === agent.call.complete routes to
      // AgentDetail) and fold in the start's prompt/agent/model attrs, which
      // the complete event does not carry. Complete attrs win on conflict.
      const merged: TraceEvent = complete
        ? { ...complete, attrs: { ...event.attrs, ...complete.attrs } }
        : event;
      // Harness provenance is recorded on the START event (AgentCalledPayload):
      // profile = the selected harness profile, model = the resolved model.
      const profile = typeof event.attrs.profile === "string" ? event.attrs.profile : "";
      const model = typeof merged.attrs.model === "string" ? (merged.attrs.model as string) : "";
      const effort = typeof merged.attrs.effort === "string" ? (merged.attrs.effort as string) : "";
      agent = { verb, complete, streamEvents, durationMs: dur, incomplete: complete === null, merged, profile, model, effort };
    }

    out.push({ index: i, event, subsystem, agent });
  }
  return out;
});

// A turn group is one *room visit* — a single occupancy of a room, which carries
// exactly one accepted intent (the decider break that ends the visit).
interface TurnGroup {
  /** Unique key: "${statePath}:visit:${decisionTurn}" (on-path) or
   *  "offpath:${statePath}:${turn}" — used for v-for and collapse state. */
  groupKey: string;
  /** Decision turn: the turn whose intent_accepted closes this visit. For
   *  off-path / intent-less groups this is just the raw turn number. */
  turn: number;
  /** The state_path shared by all events in this group (may be empty). */
  statePath: string;
  events: AnnotatedEvent[];
  /** The intent accepted for this visit ("" when none — e.g. terminal rooms,
   *  off-path batches, or traces with no machine.intent_accepted events). */
  intent: string;
  /** Distinct raw turn numbers this visit spans, ascending. A visit normally
   *  spans two turns: the room is entered at the tail of the previous decision's
   *  turn (state_entered), then its own intent fires in the next turn. */
  turnSpan: number[];
  /** True when all events in this group carry a non-zero parent_turn —
   *  i.e. this is a pure off-path batch that interrupted the foreground. */
  isOffPath: boolean;
  /** The foreground turn this off-path group interrupted (0 when isOffPath is false). */
  parentTurn: number;
}

// ---- visit resolution -------------------------------------------------------

// A turn is one decider break and carries exactly one machine.intent_accepted.
// But a turn straddles the transition it triggers: when room X's intent fires,
// the *entered* room Y's state_entered (and its on-enter work) lands in that
// SAME turn. So an event's own turn number is not the unit a reader reasons
// about — a room visit is. We key each visit by the turn whose intent_accepted
// closes it, and route every event for that room to the upcoming decision.
//
// decisionsByState maps a state_path → the (turn, intent) of each intent
// accepted while occupying it, ascending by turn. A room visited more than once
// (re-entry) has multiple entries; an event routes to the earliest decision at
// or after its own turn, so a room's entry events (recorded in the prior
// decision's turn) join the visit they actually belong to.
const decisionsByState = computed<Map<string, { turn: number; intent: string }[]>>(() => {
  const m = new Map<string, { turn: number; intent: string }[]>();
  for (const e of props.events) {
    if (e.msg !== "machine.intent_accepted") continue;
    const intent = typeof e.attrs.intent === "string" ? e.attrs.intent : "";
    const arr = m.get(e.state_path) ?? [];
    arr.push({ turn: e.turn, intent });
    m.set(e.state_path, arr);
  }
  for (const arr of m.values()) arr.sort((a, b) => a.turn - b.turn);
  return m;
});

interface VisitInfo {
  groupKey: string;
  decisionTurn: number;
  intent: string;
  offPath: boolean;
}

function visitInfo(statePath: string, turn: number, parentTurn: number): VisitInfo {
  const offPath = (parentTurn ?? 0) !== 0;
  if (offPath) {
    return { groupKey: `offpath:${statePath}:${turn}`, decisionTurn: turn, intent: "", offPath };
  }
  const decisions = decisionsByState.value.get(statePath);
  let decisionTurn = turn;
  let intent = "";
  if (decisions && decisions.length > 0) {
    // Earliest decision at or after this event's turn (the upcoming break);
    // fall back to the last decision for trailing events past the final intent.
    const d = decisions.find((x) => x.turn >= turn) ?? decisions[decisions.length - 1]!;
    decisionTurn = d.turn;
    intent = d.intent;
  }
  return { groupKey: `${statePath}:visit:${decisionTurn}`, decisionTurn, intent, offPath };
}

function fmtTurnSpan(turns: number[]): string {
  if (turns.length === 0) return "";
  const lo = turns[0]!;
  const hi = turns[turns.length - 1]!;
  return lo === hi ? `turn ${lo}` : `turn ${lo}–${hi}`;
}

// ---- phase resolution -------------------------------------------------------

interface RoomEntry {
  label: string;
  phaseName: string;
}

const diagramRooms = computed<RoomEntry[]>(() => {
  if (!props.mermaidSource) return [];
  try {
    const d = parseDiagram(props.mermaidSource);
    const out: RoomEntry[] = [];
    for (const phase of d.phases) {
      for (const room of phase.rooms) {
        out.push({ label: room.label, phaseName: phase.name });
      }
    }
    // Longest label first so prefix matching prefers the most specific room.
    out.sort((a, b) => b.label.length - a.label.length);
    return out;
  } catch {
    return [];
  }
});

function phaseForStatePath(statePath: string): string | null {
  if (!statePath) return null;
  for (const r of diagramRooms.value) {
    if (statePath === r.label || statePath.startsWith(r.label + ".")) {
      return r.phaseName;
    }
  }
  return null;
}

// Bucket order within a turn group:
//   0 = turn.start (hidden, opens the group)
//   1 = work events (default)
//   2 = turn.input (user message that triggers the next turn — end of this turn)
//   3 = turn.end   (hidden, closes the group)
function turnEventBucket(ae: AnnotatedEvent): number {
  if (ae.event.msg === "turn.start") return 0;
  if (ae.event.msg === "turn.input") return 2;
  if (ae.event.msg === "turn.end")   return 3;
  return 1;
}

const groupedTurns = computed<TurnGroup[]>(() => {
  // Group by ROOM VISIT (state_path + the decision turn that closes the visit),
  // not by raw turn. Because a turn straddles the transition, a room's entry
  // events are recorded under the previous decision's turn; visitInfo routes
  // them to the visit they belong to, so one phase visit = one group carrying
  // its single intent — even though its events span two raw turns.
  const map = new Map<string, AnnotatedEvent[]>();

  // turn.input (UserInputReceived) events are real events written by the
  // orchestrator at the moment user input arrives, with the SAME turn number as
  // the TurnStarted that follows. The UI defers them to appear after the machine
  // work in the same group (so the input chip renders at the logical "top" of
  // the response thread, where the user's message appears before the engine's
  // reaction — a presentation convention, not a data correction).
  const deferredTurnInputs: AnnotatedEvent[] = [];

  const keyFor = (ae: AnnotatedEvent): string =>
    visitInfo(ae.event.state_path, ae.event.turn, ae.event.parent_turn ?? 0).groupKey;

  for (const ae of filteredEvents.value) {
    if (ae.event.msg === "turn.input") {
      deferredTurnInputs.push(ae);
      continue;
    }
    const key = keyFor(ae);
    const arr = map.get(key) ?? [];
    arr.push(ae);
    map.set(key, arr);
  }

  // Resolve deferred turn.input events: place them in the same visit group as
  // their peers, appended last so they render after the machine work.
  for (const ae of deferredTurnInputs) {
    const key = keyFor(ae);
    const arr = map.get(key) ?? [];
    arr.push(ae);
    map.set(key, arr);
  }

  return [...map.entries()]
    .map(([groupKey, events]) => {
      events.sort((a, b) => {
        const ba = turnEventBucket(a), bb = turnEventBucket(b);
        return ba !== bb ? ba - bb : a.index - b.index;
      });
      const statePath = events[0]!.event.state_path;
      const isOffPath = events.length > 0 && events.every((ae) => (ae.event.parent_turn ?? 0) !== 0);
      const parentTurn = isOffPath ? (events[0]!.event.parent_turn ?? 0) : 0;
      const vi = visitInfo(statePath, events[0]!.event.turn, parentTurn);
      const turnSpan = [...new Set(events.map((e) => e.event.turn))].sort((a, b) => a - b);
      return {
        groupKey,
        turn: vi.decisionTurn,
        statePath,
        events,
        intent: vi.intent,
        turnSpan,
        isOffPath,
        parentTurn,
      };
    })
    .sort((a, b) => {
      // Primary: ascending decision turn; secondary: earliest event index.
      if (a.turn !== b.turn) return a.turn - b.turn;
      return a.events[0]!.index - b.events[0]!.index;
    });
});

// ---- phase grouping ---------------------------------------------------------

interface PhaseSection {
  phaseKey: string;
  phase: string | null;
  turnGroups: TurnGroup[];
  totalEvents: number;
}

const groupedPhases = computed<PhaseSection[]>(() => {
  // Coalesce only ADJACENT groups with the same phase. A phase can be revisited
  // later in a run (proposing -> testing -> proposing); grouping every same
  // phase into one section makes that revisit render before intervening phases.
  // The trace pane is an audit surface, so section order must follow event
  // order even when that means the same phase label appears again later.
  const sections: PhaseSection[] = [];
  for (const group of groupedTurns.value) {
    const phase = phaseForStatePath(group.statePath);
    const key = phase ?? "—";
    let section = sections[sections.length - 1];
    if (section && section.phase === phase) {
      section.turnGroups.push(group);
      section.totalEvents += group.events.length;
      continue;
    }

    section = {
      phaseKey: `phase:${key}:${sections.length}`,
      phase,
      turnGroups: [group],
      totalEvents: group.events.length,
    };
    sections.push(section);
  }
  return sections;
});

// ---- virtualisation ---------------------------------------------------------

const useVirtualisation = computed(() => filteredEvents.value.length > VIRTUALISATION_THRESHOLD);

/**
 * For virtualisation we need to know how many "rows" each group takes:
 * 1 (phase-header) + 1 (turn header) + N (events, if not collapsed).
 */
interface FlatItem {
  type: "phase-header" | "header" | "row";
  phaseKey?: string;
  turn: number;
  groupIndex: number;
  phaseIndex?: number;
  rowIndex?: number;
  annotatedEvent?: AnnotatedEvent;
}

const flatItems = computed<FlatItem[]>(() => {
  const items: FlatItem[] = [];
  for (let pi = 0; pi < groupedPhases.value.length; pi++) {
    const section = groupedPhases.value[pi]!;
    items.push({ type: "phase-header", phaseKey: section.phaseKey, turn: -1, groupIndex: -1, phaseIndex: pi });
    if (collapsedPhases.has(section.phaseKey)) continue;
    for (let gi = 0; gi < section.turnGroups.length; gi++) {
      const g = section.turnGroups[gi]!;
      items.push({ type: "header", turn: g.turn, groupIndex: gi, phaseIndex: pi });
      if (collapsedTurns.has(g.groupKey)) continue;
      for (let ri = 0; ri < g.events.length; ri++) {
        items.push({ type: "row", turn: g.turn, groupIndex: gi, phaseIndex: pi, rowIndex: ri, annotatedEvent: g.events[ri] });
      }
    }
  }
  return items;
});

const visibleStart = computed(() => {
  if (!useVirtualisation.value) return 0;
  return Math.max(0, Math.floor(scrollTop.value / ROW_HEIGHT_ESTIMATE) - WINDOW_OVERSCAN);
});

const visibleEnd = computed(() => {
  if (!useVirtualisation.value) return flatItems.value.length;
  const end = Math.ceil((scrollTop.value + clientHeight.value) / ROW_HEIGHT_ESTIMATE) + WINDOW_OVERSCAN;
  return Math.min(flatItems.value.length, end);
});

const topSpacerHeight = computed(() => visibleStart.value * ROW_HEIGHT_ESTIMATE);
const bottomSpacerHeight = computed(() => (flatItems.value.length - visibleEnd.value) * ROW_HEIGHT_ESTIMATE);

// Re-collapse flat items back into phase sections for the template, preserving only the visible window.
const visiblePhases = computed<PhaseSection[]>(() => {
  if (!useVirtualisation.value) return groupedPhases.value;

  const slice = flatItems.value.slice(visibleStart.value, visibleEnd.value);
  const result: PhaseSection[] = [];
  let currentSection: PhaseSection | null = null;
  let currentGroup: TurnGroup | null = null;

  for (const item of slice) {
    if (item.type === "phase-header") {
      const ps = groupedPhases.value[item.phaseIndex!]!;
      currentSection = { phaseKey: ps.phaseKey, phase: ps.phase, turnGroups: [], totalEvents: 0 };
      result.push(currentSection);
      currentGroup = null;
    } else if (item.type === "header") {
      const ps = groupedPhases.value[item.phaseIndex!]!;
      const tg = ps.turnGroups[item.groupIndex]!;
      currentGroup = { ...tg, events: [] };
      if (currentSection) {
        currentSection.turnGroups.push(currentGroup);
      }
    } else if (item.annotatedEvent && currentGroup) {
      currentGroup.events.push(item.annotatedEvent);
      if (currentSection) currentSection.totalEvents++;
    }
  }
  return result;
});

// ---- event handlers ---------------------------------------------------------

function toggleSubsystem(sys: Subsystem): void {
  if (selectedSubsystems.has(sys)) {
    selectedSubsystems.delete(sys);
  } else {
    selectedSubsystems.add(sys);
  }
}

function onStatePathChange(e: Event): void {
  const val = (e.target as HTMLSelectElement).value;
  selectedStatePath.value = val === "" ? null : val;
}

function clearFilters(): void {
  ALL_SUBSYSTEMS.forEach((s) => {
    if (DEFAULT_OFF_SUBSYSTEMS.has(s)) {
      selectedSubsystems.delete(s);
    } else {
      selectedSubsystems.add(s);
    }
  });
  selectedStatePath.value = null;
  // Re-select all categories.
  allCategories.value.forEach((c) => selectedCategories.add(c));
}

function toggleCategory(cat: ObservationKind): void {
  if (selectedCategories.has(cat)) {
    selectedCategories.delete(cat);
  } else {
    selectedCategories.add(cat);
  }
}

function toggleTurnCollapse(groupKey: string): void {
  if (collapsedTurns.has(groupKey)) {
    collapsedTurns.delete(groupKey);
  } else {
    collapsedTurns.add(groupKey);
  }
}

function togglePhaseCollapse(phaseKey: string): void {
  if (collapsedPhases.has(phaseKey)) {
    collapsedPhases.delete(phaseKey);
  } else {
    collapsedPhases.add(phaseKey);
  }
}

function onRowClick(index: number): void {
  emit("select", index);
  toggleRowExpand(index);
}

function toggleRowExpand(index: number): void {
  if (expandedRows.has(index)) {
    expandedRows.delete(index);
  } else {
    expandedRows.add(index);
  }
}

// ── Tour-driven trace focus (window.__tourTrace) ─────────────────────────────
// A self-driving run's progress lives in the trace as terse, collapsed rows that
// "don't communicate anything" until expanded. So a tour (the demo-video-loop
// video) can drive the timeline to TELL THE STORY: expand the specific rows for
// a beat (the maker task, the video gate, the QA gate, the verdict) and pulse the
// specific fields that matter (an exit_code, a PASS/FAIL stdout, the verdict).
// This is render-time driving data only — the live operator UI is untouched.
//
// The match is over a row's FULL searchable text (msg + attrs + the merged host
// call's args/return + narration), so a focus can target a row by what it
// actually did ("video gate PASS", "QA FAIL", "qa.sh") rather than a brittle
// index. Highlighted fields are matched the same way within the expanded bodies.
function rowSearchText(row: AnnotatedEvent): string {
  const parts: string[] = [row.event.msg];
  try { parts.push(JSON.stringify(row.event.attrs)); } catch { /* unserialisable */ }
  if (row.harnessCall) {
    parts.push(row.harnessCall.namespace);
    try {
      parts.push(JSON.stringify(row.harnessCall.args));
      parts.push(JSON.stringify(row.harnessCall.data));
    } catch { /* unserialisable */ }
  }
  if (row.narration != null) parts.push(row.narration);
  if (row.agent) {
    parts.push("agent." + row.agent.verb);
    // The maker is an agent row; its submitted summary / output lives in the
    // merged complete event's attrs, so include it (a beat targets the maker by
    // what it returned, e.g. "recorded the demo-video-loop tour").
    const merged = row.agent.merged ?? row.agent.complete;
    if (merged?.attrs) {
      try { parts.push(JSON.stringify(merged.attrs)); } catch { /* unserialisable */ }
    }
  }
  return parts.join(" ").toLowerCase();
}

function clearFieldHighlight(): void {
  const root = bodyRef.value;
  if (!root) return;
  root
    .querySelectorAll(".trace-timeline__field-hl")
    .forEach((el) => el.classList.remove("trace-timeline__field-hl"));
}

function applyFieldHighlight(terms: string[]): void {
  clearFieldHighlight();
  const root = bodyRef.value;
  if (!root || terms.length === 0) return;
  const lowered = terms.map((t) => t.toLowerCase());
  // Leaf text elements across the detail renderers (EventDetail, HostBuiltinDetail,
  // HostCliDetail, AgentDetail) — `pre`/`code` catch the JSON/stdout blocks, the
  // explicit classes catch chips/badges/values, so a highlight term lands on the
  // smallest element that carries it regardless of which renderer drew the row.
  const candidates = root.querySelectorAll<HTMLElement>(
    ".trace-timeline__row.expanded pre," +
      ".trace-timeline__row.expanded code," +
      ".trace-timeline__row.expanded .event-detail__val," +
      ".trace-timeline__row.expanded .hbd__pre," +
      ".trace-timeline__row.expanded .hbd__chip," +
      ".trace-timeline__row.expanded .hbd__badge," +
      ".trace-timeline__row.expanded .trace-timeline__say-text",
  );
  let firstHit: HTMLElement | null = null;
  candidates.forEach((el) => {
    const txt = (el.textContent ?? "").toLowerCase();
    if (lowered.some((t) => txt.includes(t))) {
      el.classList.add("trace-timeline__field-hl");
      if (!firstHit) firstHit = el;
    }
  });
  (firstHit as HTMLElement | null)?.scrollIntoView?.({ block: "center", behavior: "smooth" });
}

interface TraceFocusOpts {
  /** Expand every row whose searchable text contains ANY of these substrings. */
  match?: string | string[];
  /** Skip rows whose searchable text contains this substring (disambiguation). */
  exclude?: string;
  /** Pulse fields inside the expanded bodies whose text contains these terms. */
  highlight?: string[];
}

function applyTraceFocus(opts: TraceFocusOpts): number {
  const matches = (Array.isArray(opts.match) ? opts.match : opts.match ? [opts.match] : [])
    .map((s) => s.toLowerCase())
    .filter(Boolean);
  const exclude = opts.exclude?.toLowerCase();
  // Show all groups so a matched row is never hidden inside a collapsed
  // phase/turn (this trace is small; for the demo that is exactly right).
  collapsedPhases.clear();
  collapsedTurns.clear();
  expandedRows.clear();
  const hits: number[] = [];
  for (const row of filteredEvents.value) {
    const text = rowSearchText(row);
    if (exclude && text.includes(exclude)) continue;
    if (matches.length && matches.some((m) => text.includes(m))) {
      expandedRows.add(row.index);
      hits.push(row.index);
    }
  }
  void nextTick().then(() => {
    const root = bodyRef.value;
    if (root && hits.length) {
      const el = root.querySelector<HTMLElement>(`[data-event-index="${hits[0]}"]`);
      el?.scrollIntoView?.({ block: "center", behavior: "smooth" });
    }
    applyFieldHighlight(opts.highlight ?? []);
  });
  return hits.length;
}

onMounted(() => {
  (window as unknown as { __tourTrace?: unknown }).__tourTrace = {
    focus: (opts: TraceFocusOpts) => applyTraceFocus(opts ?? {}),
    reset: () => {
      expandedRows.clear();
      clearFieldHighlight();
    },
  };
});
onUnmounted(() => {
  delete (window as unknown as { __tourTrace?: unknown }).__tourTrace;
});

async function copyRow(row: AnnotatedEvent): Promise<void> {
  let payload: unknown;
  if (row.harnessCall) {
    payload = {
      type: "host",
      namespace: row.harnessCall.namespace,
      args: row.harnessCall.args,
      data: row.harnessCall.data,
      error: row.harnessCall.error,
      durationMs: row.harnessCall.durationMs,
      turn: row.event.turn,
      state_path: row.event.state_path,
      time: row.event.time,
    };
  } else if (row.agent) {
    const e = row.agent.complete ?? row.event;
    payload = { type: "agent", verb: row.agent.verb, durationMs: row.agent.durationMs, ...e };
  } else if (row.narration != null) {
    payload = {
      type: "machine.say",
      text: row.narration,
      turn: row.event.turn,
      state_path: row.event.state_path,
      time: row.event.time,
    };
  } else if (row.effectGroup) {
    payload = {
      type: "world.update",
      before: row.effectGroup.before,
      after: row.effectGroup.after,
      turn: row.event.turn,
      state_path: row.event.state_path,
      time: row.event.time,
    };
  } else {
    payload = row.event;
  }
  try {
    await navigator.clipboard.writeText(JSON.stringify(payload, null, 2));
    copiedRows.add(row.index);
    setTimeout(() => copiedRows.delete(row.index), 1200);
  } catch {
    // ignore — clipboard may be unavailable in some browsers
  }
}

function onScroll(e: Event): void {
  const el = e.target as HTMLElement;
  scrollTop.value = el.scrollTop;
  clientHeight.value = el.clientHeight;
}

// ---- highlight ----------------------------------------------------------

const highlightedSet = computed<Set<string>>(
  () => new Set(props.highlightedStatePaths ?? [])
);

/**
 * Prefix-aware match: a highlight path "reproducing" matches events with
 * state_path === "reproducing" OR starting with "reproducing.".  This lets
 * one click on a compound room (bugfix) light up every substate's events.
 */
function isHighlighted(statePath: string): boolean {
  if (highlightedSet.value.size === 0) return false;
  for (const path of highlightedSet.value) {
    if (statePath === path) return true;
    if (statePath.startsWith(path + ".")) return true;
    if (path.startsWith(statePath + ".")) return true;
  }
  return false;
}

/**
 * Scroll the first matching row into view when the highlight set changes.
 * We watch `highlightTick` (bumped by the store on every highlight set
 * change) so re-clicking the same room scrolls again.
 */
watch(
  () => props.highlightTick,
  async () => {
    if (highlightedSet.value.size === 0) return;
    const root = bodyRef.value;
    if (!root) return;

    // Find the first matching event in the filtered list.
    const target = filteredEvents.value.find((ae) =>
      isHighlighted(ae.event.state_path)
    );
    if (!target) return;

    // Ensure the phase section and turn group aren't collapsed.
    const targetPhase = phaseForStatePath(target.event.state_path);
    for (const section of groupedPhases.value) {
      if (section.phase === targetPhase) {
        collapsedPhases.delete(section.phaseKey);
        break;
      }
    }
    collapsedTurns.delete(
      visitInfo(target.event.state_path, target.event.turn, target.event.parent_turn ?? 0).groupKey,
    );
    await nextTick();

    // When virtualised, the target row may be outside the rendered window —
    // its DOM node won't exist.  Locate the target's index in `flatItems`
    // and pre-scroll so the row materialises before we ask the browser to
    // centre it.
    if (useVirtualisation.value) {
      const flat = flatItems.value;
      const idx = flat.findIndex(
        (item) => item.type === "row" && item.annotatedEvent?.index === target.index,
      );
      if (idx >= 0) {
        const desired = idx * ROW_HEIGHT_ESTIMATE - root.clientHeight / 2;
        root.scrollTop = Math.max(0, desired);
        // The scroll handler updates `scrollTop.value`, which re-computes
        // visibleStart/End on the next tick.
        await nextTick();
        await nextTick();
      }
    }

    const el = root.querySelector<HTMLElement>(
      `[data-event-index="${target.index}"]`,
    );
    if (el && typeof el.scrollIntoView === "function") {
      el.scrollIntoView({ block: "center", behavior: "smooth" });
    }
  },
);

</script>

<style scoped>
.trace-timeline {
  display: flex;
  flex-direction: column;
  height: 100%;
  background: var(--k-bg-widget, #0f172a);
  border: 1px solid var(--k-border, #1e293b);
  border-radius: 6px;
  overflow: hidden;
  font-size: 0.8125rem;
}

/* --- Compact filters toggle (docked panel) --- */
.trace-timeline__filters-toggle {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  width: 100%;
  padding: 0.3rem 0.55rem;
  font: inherit;
  font-size: 0.72rem;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  text-align: left;
  color: var(--k-fg-muted, #94a3b8);
  background: var(--k-bg-widget, #0f172a);
  border: none;
  border-bottom: 1px solid var(--k-border, #1e293b);
  cursor: pointer;
}
.trace-timeline__filters-toggle:hover {
  color: var(--k-fg, #e2e8f0);
}
.trace-timeline__filters-active {
  color: var(--k-fg-accent, #7dd3fc);
  font-size: 0.6rem;
}
/* In compact mode (the docked VS Code surface) the timeline IS the panel: drop
   the bordered, rounded box so it sits flush against the native VS Code panel
   chrome instead of drawing a second, redundant frame inside it. (The surrounding
   gutter is removed in TraceSurface.) The browser's full layout keeps the framed
   card via the base .trace-timeline rule. */
.trace-timeline--compact {
  position: relative;
  border: none;
  border-radius: 0;
}
.trace-timeline--compact .trace-timeline__filters {
  position: absolute;
  top: 1.6rem;
  left: 0;
  right: 0;
  z-index: 5;
  box-shadow: 0 6px 16px rgba(0, 0, 0, 0.35);
}

/* --- Filters --- */
.trace-timeline__filters {
  display: flex;
  flex-wrap: wrap;
  gap: 0.375rem 0.5rem;
  padding: 0.5rem;
  border-bottom: 1px solid var(--k-border, #1e293b);
  background: var(--k-bg-widget, #0f172a);
}

.trace-timeline__filter-group {
  display: flex;
  align-items: center;
  gap: 0.25rem;
  flex-wrap: wrap;
}

.trace-timeline__filter-label {
  color: var(--k-fg-muted, #64748b);
  font-size: 0.75rem;
  white-space: nowrap;
}

.trace-timeline__chip {
  padding: 0.1rem 0.4rem;
  border: 1px solid var(--k-border, #334155);
  border-radius: 999px;
  background: var(--k-bg-input, #1e293b);
  color: var(--k-fg-muted, #94a3b8);
  cursor: pointer;
  font-size: 0.75rem;
  transition: background 0.1s, color 0.1s, border-color 0.1s;
}

.trace-timeline__chip.active {
  background: var(--k-bg-selection, #1d4ed8);
  border-color: var(--k-border-focus, #3b82f6);
  color: var(--k-fg-accent, #eff6ff);
}

.trace-timeline__chip--clear {
  background: #7f1d1d;
  border-color: var(--k-error, #ef4444);
  color: #fee2e2;
}

.trace-timeline__chip--category {
  display: inline-flex;
  align-items: center;
  gap: 0.25rem;
}

.trace-timeline__cat-dot {
  display: inline-block;
  width: 7px;
  height: 7px;
  border-radius: 50%;
  flex-shrink: 0;
}

/* Observation kind dot — small colored circle at the start of each event row */
.trace-timeline__obs-dot {
  display: inline-block;
  width: 6px;
  height: 6px;
  border-radius: 50%;
  flex-shrink: 0;
}

.trace-timeline__select {
  background: var(--k-bg-input, #1e293b);
  border: 1px solid var(--k-border, #334155);
  color: var(--k-fg, #e2e8f0);
  font-size: 0.75rem;
  padding: 0.1rem 0.3rem;
  border-radius: 4px;
  max-width: 140px;
}

/* --- Body --- */
.trace-timeline__body {
  flex: 1;
  overflow-y: auto;
  overflow-x: hidden;
  overflow-anchor: none;
}

/* --- Phase header (top-level) --- */
.trace-timeline__phase-header {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  padding: 0.3rem 0.6rem;
  background: var(--k-bg-input, #1e293b);
  border-bottom: 1px solid var(--k-bg-widget, #0f172a);
  cursor: pointer;
  user-select: none;
  position: sticky;
  top: 0;
  z-index: 2;
}

.trace-timeline__phase-header:hover {
  background: var(--k-bg-hover, #293548);
}

/* --- Turn header (sub-level within a phase) --- */
.trace-timeline__turn-header {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  padding: 0.25rem 0.6rem 0.25rem 1.5rem;
  background: var(--k-bg-deep, #0d1526);
  border-bottom: 1px solid var(--k-bg-widget, #0f172a);
  cursor: pointer;
  user-select: none;
  position: sticky;
  top: 0;
  z-index: 1;
}

.trace-timeline__turn-header:hover {
  background: var(--k-bg-hover, #1a2436);
}

/* Off-path sub-group header — visually nested under the parent turn */
.trace-timeline__turn-header--offpath {
  background: var(--k-bg-deep, #0a1220);
  border-left: 2px solid var(--k-border, #334155);
  padding-left: 1.4rem;
}

.trace-timeline__turn-header--offpath:hover {
  background: var(--k-bg-hover, #111d30);
}

.trace-timeline__turn-offpath-indent {
  color: var(--k-fg-subtle, #475569);
  font-size: 0.75rem;
  margin-right: 0.1rem;
}

.trace-timeline__turn-offpath-label {
  font-family: ui-monospace, monospace;
  font-size: 0.7rem;
  color: var(--k-fg-muted, #64748b);
  font-style: italic;
}

.trace-timeline__turn-caret {
  color: var(--k-fg-muted, #64748b);
  font-size: 0.7rem;
}

.trace-timeline__turn-label {
  font-family: ui-monospace, monospace;
  font-size: 0.7rem;
  color: var(--k-fg-muted, #64748b);
  font-weight: 400;
}

/* Intent badge — the single decider break that closes a room visit. */
.trace-timeline__intent-label {
  display: inline-flex;
  align-items: stretch;
  border-radius: 3px;
  overflow: hidden;
  font-size: 0.65rem;
  font-family: ui-monospace, monospace;
  line-height: 1;
}

.trace-timeline__intent-kw {
  background: var(--k-bg-input, #1e293b);
  color: var(--k-fg-muted, #64748b);
  padding: 0.15rem 0.3rem;
  text-transform: uppercase;
  letter-spacing: 0.04em;
}

.trace-timeline__intent-value {
  background: var(--k-bg-selection, #1e3a5f);
  color: var(--k-fg-accent, #93c5fd);
  padding: 0.15rem 0.4rem;
  font-weight: 600;
}

.trace-timeline__turn-phase {
  font-weight: 700;
  font-size: 0.875rem;
  color: var(--k-fg, #e2e8f0);
  letter-spacing: 0.01em;
}

.trace-timeline__turn-count {
  margin-left: auto;
  color: var(--k-fg-subtle, #475569);
  font-size: 0.7rem;
}

/* --- Row --- */
.trace-timeline__row {
  border-bottom: 1px solid var(--k-border, #1a2337);
  cursor: pointer;
}

.trace-timeline__row:hover .trace-timeline__row-main {
  background: var(--k-bg-hover, #162032);
}

.trace-timeline__row.selected .trace-timeline__row-main {
  background: var(--k-bg-selection, #1e3a5f);
  border-left: 2px solid var(--k-border-focus, #60a5fa);
}

.trace-timeline__row.highlighted .trace-timeline__row-main {
  background: #2a2010;
  border-left: 2px solid var(--k-warning, #fbbf24);
}

.trace-timeline__row.highlighted.selected .trace-timeline__row-main {
  background: #2a2820;
  border-left: 2px solid var(--k-warning, #fbbf24);
  box-shadow: inset 4px 0 0 var(--k-border-focus, #60a5fa);
}

.trace-timeline__row-main {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.25rem 0.6rem;
  transition: background 0.1s;
}

/* Subsystem chip */
.trace-timeline__subsystem-chip {
  display: inline-block;
  min-width: 4.5rem;
  text-align: center;
  padding: 0.05rem 0.3rem;
  border-radius: 3px;
  font-size: 0.7rem;
  font-weight: 600;
  background: var(--k-bg-input, #1e293b);
  color: var(--k-fg-muted, #94a3b8);
}

.trace-timeline__subsystem-chip[data-subsystem="turn"]    { background: #1e3a5f; color: #93c5fd; }
.trace-timeline__subsystem-chip[data-subsystem="user"]    { background: #312e81; color: #a5b4fc; }
.trace-timeline__subsystem-chip[data-subsystem="machine"] { background: #14532d; color: #86efac; }
.trace-timeline__subsystem-chip[data-subsystem="world"]   { background: #134e4a; color: #5eead4; }
.trace-timeline__subsystem-chip[data-subsystem="host"]    { background: #4a1d96; color: #c4b5fd; }
.trace-timeline__subsystem-chip[data-subsystem="agent"]  { background: #7c2d12; color: #fdba74; }
.trace-timeline__subsystem-chip[data-subsystem="harness"] { background: #1e3a5f; color: #7dd3fc; }

.trace-timeline__msg {
  flex: 1;
  color: var(--k-fg, #e2e8f0);
  font-family: ui-monospace, monospace;
  font-size: 0.775rem;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.trace-timeline__say-label {
  display: inline-block;
  background: var(--k-success-bg, #14532d);
  color: var(--k-success, #86efac);
  border: 1px solid #166534;
  border-radius: 3px;
  font-size: 0.65rem;
  padding: 0.02rem 0.3rem;
  margin-right: 0.4rem;
  vertical-align: middle;
  text-transform: uppercase;
  font-weight: 600;
}

.trace-timeline__say-text {
  color: #d1fae5;
  font-style: italic;
}

.trace-timeline__effect-count {
  display: inline-flex;
  align-items: center;
  background: var(--k-success-bg, #14532d);
  color: var(--k-success, #86efac);
  border: 1px solid #166534;
  border-radius: 3px;
  font-size: 0.65rem;
  padding: 0.02rem 0.3rem;
  margin-left: 0.35rem;
  vertical-align: middle;
}

.trace-timeline__time {
  color: var(--k-fg-subtle, #475569);
  font-size: 0.7rem;
  font-family: ui-monospace, monospace;
  white-space: nowrap;
}

.trace-timeline__duration {
  color: #fdba74;
  font-size: 0.7rem;
  font-family: ui-monospace, monospace;
  white-space: nowrap;
  padding: 0.05rem 0.35rem;
  border: 1px solid #7c2d12;
  border-radius: 3px;
  background: #1a0f08;
}

.trace-timeline__cost {
  color: #a3e635;
  font-size: 0.7rem;
  font-family: ui-monospace, monospace;
  white-space: nowrap;
  padding: 0.05rem 0.35rem;
  border: 1px solid #3f6212;
  border-radius: 3px;
  background: #1a2e05;
}

.trace-timeline__harness {
  display: inline-flex;
  align-items: center;
  gap: 0.3rem;
  white-space: nowrap;
}
.trace-timeline__harness-profile {
  color: #c4b5fd;
  font-size: 0.7rem;
  font-family: ui-monospace, monospace;
  padding: 0.05rem 0.35rem;
  border: 1px solid #5b21b6;
  border-radius: 3px;
  background: #2e1065;
}
.trace-timeline__harness-model {
  color: #93c5fd;
  font-size: 0.68rem;
  font-family: ui-monospace, monospace;
}
.trace-timeline__harness-effort {
  color: #fcd34d;
  font-size: 0.66rem;
  font-family: ui-monospace, monospace;
}

.trace-timeline__incomplete {
  color: #fecaca;
  background: #7f1d1d;
  border: 1px solid var(--k-error, #ef4444);
  border-radius: 3px;
  font-size: 0.7rem;
  font-weight: 600;
  padding: 0.05rem 0.35rem;
  white-space: nowrap;
}

.trace-timeline__incomplete-banner {
  background: #2a1010;
  border: 1px solid #7f1d1d;
  color: #fecaca;
  padding: 0.4rem 0.6rem;
  border-radius: 4px;
  font-size: 0.75rem;
  margin-bottom: 0.5rem;
}

.trace-timeline__agent-stream {
  border-left: 2px solid #475569;
  margin: 0 0 0.55rem 0.15rem;
  padding: 0.1rem 0 0.1rem 0.65rem;
}

.trace-timeline__agent-stream-title {
  color: #cbd5e1;
  font-size: 0.72rem;
  font-weight: 700;
  margin-bottom: 0.35rem;
  text-transform: uppercase;
}

.trace-timeline__agent-stream-count {
  color: #94a3b8;
  font-family: ui-monospace, monospace;
  font-weight: 500;
  margin-left: 0.35rem;
}

.trace-timeline__agent-stream-row {
  display: grid;
  grid-template-columns: minmax(8rem, max-content) minmax(0, 1fr);
  gap: 0.5rem;
  align-items: baseline;
  color: #cbd5e1;
  font-size: 0.76rem;
  padding: 0.14rem 0;
}

.trace-timeline__agent-stream-row--warning {
  border-left: 2px solid var(--k-warning, #fbbf24);
  border-radius: 4px;
  background: color-mix(in srgb, var(--k-warning, #fbbf24) 10%, transparent);
  padding: 0.18rem 0.35rem;
}

.trace-timeline__agent-stream-kind {
  color: #93c5fd;
  font-family: ui-monospace, monospace;
  white-space: nowrap;
}

.trace-timeline__agent-stream-row--warning .trace-timeline__agent-stream-kind {
  color: var(--k-warning, #fbbf24);
}

.trace-timeline__agent-stream-text {
  color: #d1d5db;
  min-width: 0;
  overflow-wrap: anywhere;
}

.trace-timeline__expand-btn {
  background: none;
  border: 1px solid var(--k-border, #334155);
  color: var(--k-fg-muted, #64748b);
  cursor: pointer;
  padding: 0.05rem 0.3rem;
  border-radius: 3px;
  font-size: 0.75rem;
  line-height: 1;
}

.trace-timeline__expand-btn:hover {
  background: var(--k-bg-hover, #1e293b);
  color: var(--k-fg, #e2e8f0);
}

/* --- Expanded row body --- */
.trace-timeline__row-body {
  padding: 0.4rem 0.6rem;
  background: var(--k-bg-deep, #080f1a);
  border-top: 1px solid var(--k-border, #1e293b);
}

/* Tour field highlight: a steady amber spotlight on the one field a beat is
   narrating (an exit_code, a PASS/FAIL stdout, the verdict). Steady (not a quick
   pulse) so it reads in any captured frame; a brief grow-in adds life on screen
   without risking a frame that misses it. Applied/cleared by window.__tourTrace. */
/* Tour field highlight lives in a GLOBAL style block below — the highlighted
   leaf elements belong to child detail components (EventDetail / HostBuiltinDetail),
   which this component's scoped CSS cannot reach. */

.trace-timeline__attrs-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 0.3rem;
  color: var(--k-fg-muted, #64748b);
  font-size: 0.75rem;
}

.trace-timeline__copy-btn {
  background: none;
  border: 1px solid var(--k-border, #334155);
  color: var(--k-fg-muted, #64748b);
  cursor: pointer;
  padding: 0.05rem 0.3rem;
  border-radius: 3px;
  font-size: 0.75rem;
  line-height: 1;
}

.trace-timeline__copy-btn:hover {
  background: var(--k-bg-hover, #1e293b);
  color: var(--k-fg, #e2e8f0);
}

.trace-timeline__copy-btn--copied {
  border-color: #166534;
  color: var(--k-success, #86efac);
}

.trace-timeline__attrs-pre {
  color: var(--k-fg-code, #7dd3fc);
  font-family: ui-monospace, monospace;
  font-size: 0.75rem;
  white-space: pre-wrap;
  word-break: break-word;
  margin: 0;
}

/* --- Empty state --- */
.trace-timeline__empty {
  padding: 1rem;
  color: var(--k-fg-subtle, #475569);
  font-size: 0.875rem;
  text-align: center;
}

/* --- Annotation badges --- */
.trace-timeline__annotation-badge {
  display: inline-block;
  background: var(--k-bg-selection, #1e3a5f);
  border: 1px solid var(--k-border-focus, #3b82f6);
  color: var(--k-fg-accent, #93c5fd);
  border-radius: 3px;
  padding: 0.05rem 0.35rem;
  font-size: 0.65rem;
  font-weight: 600;
  cursor: default;
  white-space: nowrap;
}
</style>

<!-- Tour field highlight (GLOBAL, unscoped): window.__tourTrace adds
     .trace-timeline__field-hl to a leaf field inside an expanded row's detail
     body. Those bodies are rendered by child components (EventDetail /
     HostBuiltinDetail / AgentDetail), so this component's scoped CSS can't style
     them — the rule must be global. A steady amber spotlight (not a quick pulse)
     so it reads in any captured demo frame; a brief grow-in adds life on screen. -->
<style>
.trace-timeline__field-hl {
  background: rgba(251, 191, 36, 0.22) !important;
  outline: 3px solid #f59e0b !important;
  outline-offset: 2px;
  border-radius: 5px;
  box-shadow: 0 0 0 5px rgba(251, 191, 36, 0.18), 0 0 26px rgba(251, 191, 36, 0.55) !important;
  color: #fde68a !important;
  animation: trace-field-hl-in 0.5s ease-out;
}
.trace-timeline__field-hl * {
  color: #fde68a !important;
}
@keyframes trace-field-hl-in {
  from { box-shadow: 0 0 0 14px rgba(251, 191, 36, 0); }
  to { box-shadow: 0 0 0 5px rgba(251, 191, 36, 0.18), 0 0 26px rgba(251, 191, 36, 0.55); }
}
</style>
