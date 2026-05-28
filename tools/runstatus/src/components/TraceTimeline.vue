<template>
  <div class="trace-timeline">
    <!-- Filter bar -->
    <div class="trace-timeline__filters">
      <!-- Subsystem chips -->
      <div class="trace-timeline__filter-group">
        <span class="trace-timeline__filter-label">Subsystem:</span>
        <button
          v-for="sys in ALL_SUBSYSTEMS"
          :key="sys"
          class="trace-timeline__chip"
          :class="{ active: selectedSubsystems.has(sys) }"
          @click="toggleSubsystem(sys)"
        >{{ sys }}</button>
      </div>

      <!-- Level chips -->
      <div class="trace-timeline__filter-group">
        <span class="trace-timeline__filter-label">Level:</span>
        <button
          v-for="lvl in availableLevels"
          :key="lvl"
          class="trace-timeline__chip"
          :class="{ active: selectedLevels.has(lvl) }"
          @click="toggleLevel(lvl)"
        >{{ lvl }}</button>
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

            <!-- Normal turn sub-header -->
            <div
              v-else
              class="trace-timeline__turn-header trace-timeline__turn-header--sub"
              @click="toggleTurnCollapse(group.groupKey)"
            >
              <span class="trace-timeline__turn-caret">{{ collapsedTurns.has(group.groupKey) ? '▶' : '▼' }}</span>
              <span class="trace-timeline__turn-label">turn {{ group.turn }}</span>
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
                @click="onRowClick(row.index)"
              >
                <div class="trace-timeline__row-main">
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
                    <template v-else>{{
                      row.oracle ? `oracle.${row.oracle.verb}`
                      : row.harnessCall ? row.harnessCall.namespace
                      : row.event.msg
                    }}</template>
                  </span>
                  <span
                    v-if="(row.oracle?.durationMs ?? row.harnessCall?.durationMs) != null"
                    class="trace-timeline__duration"
                  >{{ fmtMs((row.oracle?.durationMs ?? row.harnessCall!.durationMs)!) }}</span>
                  <span
                    v-else-if="row.oracle?.incomplete || row.harnessCall?.incomplete"
                    class="trace-timeline__incomplete"
                    title="Call did not complete or its completion was not recorded"
                  >incomplete</span>
                  <span class="trace-timeline__level" :data-level="row.event.level">{{ row.event.level }}</span>
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
                    <div v-if="row.oracle?.incomplete" class="trace-timeline__incomplete-banner">
                      Oracle call started but no completion event was recorded.
                    </div>
                    <div v-if="row.harnessCall?.incomplete" class="trace-timeline__incomplete-banner">
                      Host call dispatched but no returned event was recorded.
                    </div>
                    <EventDetail :event="row.oracle?.complete ?? row.event" :harnessCall="row.harnessCall" />
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
import { ref, computed, reactive, watch, nextTick } from "vue";
import type { TraceEvent } from "../types.js";
import EventDetail from "./EventDetail.vue";
import WorldDiffViewer from "./WorldDiffViewer.vue";
import { parseDiagram } from "../diagram/parse.js";
import { fmtMs } from "./oracle/lib.js";

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
}>();

const emit = defineEmits<{
  (e: "select", index: number): void;
}>();

// ---- constants --------------------------------------------------------------

const ALL_SUBSYSTEMS = ["turn", "machine", "world", "host", "oracle", "other"] as const;
type Subsystem = (typeof ALL_SUBSYSTEMS)[number];

const ORACLE_START_RE = /^oracle\.(decide|extract|ask|task|converse)\.start$/;
const ORACLE_COMPLETE_RE = /^oracle\.(decide|extract|ask|task|converse)\.complete$/;

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
const selectedLevels = reactive(new Set<string>());
const selectedStatePath = ref<string | null>(null);
const collapsedTurns = reactive(new Set<string>());
const collapsedPhases = reactive(new Set<string>());
const expandedRows = reactive(new Set<number>());
const copiedRows = reactive(new Set<number>());

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
    case "oracle":  return "oracle";
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

// All available levels in the event stream.
const availableLevels = computed(() => {
  const s = new Set<string>();
  for (const e of props.events) s.add(e.level);
  return [...s].sort();
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
  const noLevelFilter = selectedLevels.size === 0;
  const noStateFilter = selectedStatePath.value === null;
  return !defaultSysSelected || !noLevelFilter || !noStateFilter;
});

// Filtered + annotated events (preserving original index).
interface OracleMerge {
  verb: string;
  complete: TraceEvent | null;
  durationMs: number | null;
  incomplete: boolean;
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
  oracle?: OracleMerge;
  harnessCall?: HarnessCallData;
  /** Present on the lead row of a grouped machine.effect[set] batch. */
  effectGroup?: EffectGroupData;
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

// Pair oracle.<verb>.start events with their matching .complete via call_id.
// The merged row sits at the start timestamp and shows elapsed time; the
// .complete row is suppressed.  A start with no complete is rendered with an
// "incomplete" badge.
const oracleStartCallIds = computed<Set<string>>(() => {
  const s = new Set<string>();
  for (const e of props.events) {
    if (!ORACLE_START_RE.test(e.msg)) continue;
    const cid = e.attrs.call_id;
    if (typeof cid === "string") s.add(cid);
  }
  return s;
});

const oracleCompleteByCallId = computed<Map<string, TraceEvent>>(() => {
  const m = new Map<string, TraceEvent>();
  for (const e of props.events) {
    if (!ORACLE_COMPLETE_RE.test(e.msg)) continue;
    const cid = e.attrs.call_id;
    if (typeof cid === "string") m.set(cid, e);
  }
  return m;
});

// Merge harness.called/dispatched/returned triplets into single host-call rows.
// Groups by (turn, namespace); nth called pairs with nth dispatched + nth returned.
// host.oracle.* namespaces are excluded — covered by oracle.*.start/complete rows.
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
    if (ns.startsWith("host.oracle.")) continue;
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
  const startCids = oracleStartCallIds.value;
  const completeMap = oracleCompleteByCallId.value;
  const worldStates = worldStateByTurn.value;
  const harnessData = harnessCallData.value;
  const out: AnnotatedEvent[] = [];

  // Tracks which turns have already emitted their grouped effect row.
  const seenEffectTurns = new Set<number>();

  for (let i = 0; i < props.events.length; i++) {
    const event = props.events[i]!;

    // turn.input is always visible regardless of the "turn" chip state —
    // it carries the user's raw message text that triggered the next turn.
    if (event.msg === "turn.input") {
      if (selectedLevels.size > 0 && !selectedLevels.has(event.level)) continue;
      if (selectedStatePath.value !== null && event.state_path !== selectedStatePath.value) continue;
      out.push({ index: i, event, subsystem: "turn" });
      continue;
    }

    // Harness events must be handled before the generic subsystem filter:
    // subsystemFromMsg returns "harness" (not in ALL_SUBSYSTEMS), so they
    // would be silently dropped. Merged host-call rows respect the "host" chip.
    if (event.msg.startsWith("harness.")) {
      const ns = event.attrs.namespace;
      // Oracle wrapper calls are already covered by oracle.*.start rows.
      if (typeof ns === "string" && ns.startsWith("host.oracle.")) continue;
      // dispatched/returned rows are absorbed into their paired called row.
      if (harnessData.suppressedIndices.has(i)) continue;
      // Apply normal level/state filters; gate on "host" chip.
      if (!selectedSubsystems.has("host")) continue;
      if (selectedLevels.size > 0 && !selectedLevels.has(event.level)) continue;
      if (selectedStatePath.value !== null && event.state_path !== selectedStatePath.value) continue;
      // called rows: attach merged data and surface as a host-subsystem row.
      const harnessCall = harnessData.mergeByCalledIndex.get(i);
      out.push({ index: i, event, subsystem: "host", harnessCall });
      continue;
    }

    const subsystem = subsystemFromMsg(event.msg);

    if (!selectedSubsystems.has(subsystem)) continue;
    if (selectedLevels.size > 0 && !selectedLevels.has(event.level)) continue;
    if (selectedStatePath.value !== null && event.state_path !== selectedStatePath.value) continue;

    // machine.transition / machine.state_exited / machine.state_entered are all
    // absorbed by the turn group structure: from/to are the adjacent turn headers,
    // intent is shown in the user badge (human turns) or oracle.decide (LLM turns).
    if (
      event.msg === "machine.transition" ||
      event.msg === "machine.state_exited" ||
      event.msg === "machine.state_entered"
    ) continue;

    // Suppress oracle.*.complete rows whose paired start exists; the start
    // row carries the merged duration.
    if (ORACLE_COMPLETE_RE.test(event.msg)) {
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

    let oracle: OracleMerge | undefined;
    const startMatch = ORACLE_START_RE.exec(event.msg);
    if (startMatch) {
      const verb = startMatch[1]!;
      const cid = typeof event.attrs.call_id === "string" ? event.attrs.call_id : null;
      const complete = cid ? completeMap.get(cid) ?? null : null;
      const dur = complete && typeof complete.attrs.duration_ms === "number"
        ? (complete.attrs.duration_ms as number)
        : null;
      oracle = { verb, complete, durationMs: dur, incomplete: complete === null };
    }

    out.push({ index: i, event, subsystem, oracle });
  }
  return out;
});

// Group by turn, descending.
interface TurnGroup {
  /** Unique key: "${statePath}:${turn}" — used for v-for and collapse state. */
  groupKey: string;
  turn: number;
  /** The state_path shared by all events in this group (may be empty). */
  statePath: string;
  events: AnnotatedEvent[];
  /** True when all events in this group carry a non-zero parent_turn —
   *  i.e. this is a pure off-path batch that interrupted the foreground. */
  isOffPath: boolean;
  /** The foreground turn this off-path group interrupted (0 when isOffPath is false). */
  parentTurn: number;
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
  // Group by (state_path, turn) so that turns spanning multiple states produce
  // separate groups per state. This keeps the RIGHT panel consistent with the
  // LEFT panel's per-state event lanes.
  const map = new Map<string, AnnotatedEvent[]>();

  // turn.input events are deferred: they carry turn=N-1 (fromhistory places them
  // at the end of the previous turn) but the user wants them LAST within the
  // phase — after the machine work in turn N that they triggered.  We move them
  // into the (state_path, N) group if it already exists; otherwise fall back to
  // (state_path, N-1) so they are never silently dropped.
  const deferredTurnInputs: AnnotatedEvent[] = [];

  for (const ae of filteredEvents.value) {
    if (ae.event.msg === "turn.input") {
      deferredTurnInputs.push(ae);
      continue;
    }
    const key = `${ae.event.state_path}:${ae.event.turn}`;
    const arr = map.get(key) ?? [];
    arr.push(ae);
    map.set(key, arr);
  }

  // Now resolve deferred turn.input events.
  for (const ae of deferredTurnInputs) {
    const nextKey = `${ae.event.state_path}:${ae.event.turn + 1}`;
    const origKey = `${ae.event.state_path}:${ae.event.turn}`;
    const target = map.has(nextKey) ? nextKey : origKey;
    const arr = map.get(target) ?? [];
    arr.push(ae);
    map.set(target, arr);
  }

  return [...map.entries()]
    .sort(([, a], [, b]) => {
      // Primary: ascending turn; secondary: earliest event index within turn.
      const ta = a[0]!.event.turn, tb = b[0]!.event.turn;
      if (ta !== tb) return ta - tb;
      return a[0]!.index - b[0]!.index;
    })
    .map(([groupKey, events]) => {
      events.sort((a, b) => {
        const ba = turnEventBucket(a), bb = turnEventBucket(b);
        return ba !== bb ? ba - bb : a.index - b.index;
      });
      const statePath = events[0]!.event.state_path;
      const turn = events[0]!.event.turn;
      const isOffPath = events.length > 0 && events.every((ae) => (ae.event.parent_turn ?? 0) !== 0);
      const parentTurn = isOffPath ? (events[0]!.event.parent_turn ?? 0) : 0;
      return { groupKey, turn, statePath, events, isOffPath, parentTurn };
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
  const sections: PhaseSection[] = [];
  for (const group of groupedTurns.value) {
    const phase = phaseForStatePath(group.statePath);
    const last = sections[sections.length - 1];
    if (last && last.phase === phase) {
      last.turnGroups.push(group);
      last.totalEvents += group.events.length;
    } else {
      sections.push({
        phaseKey: `phase:${phase ?? ""}:${sections.length}`,
        phase,
        turnGroups: [group],
        totalEvents: group.events.length,
      });
    }
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

const totalHeight = computed(() => flatItems.value.length * ROW_HEIGHT_ESTIMATE);

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

function toggleLevel(lvl: string): void {
  if (selectedLevels.has(lvl)) {
    selectedLevels.delete(lvl);
  } else {
    selectedLevels.add(lvl);
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
  selectedLevels.clear();
  selectedStatePath.value = null;
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
  } else if (row.oracle) {
    const e = row.oracle.complete ?? row.event;
    payload = { type: "oracle", verb: row.oracle.verb, durationMs: row.oracle.durationMs, ...e };
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
    collapsedTurns.delete(`${target.event.state_path}:${target.event.turn}`);
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
  background: #0f172a;
  border: 1px solid #1e293b;
  border-radius: 6px;
  overflow: hidden;
  font-size: 0.8125rem;
}

/* --- Filters --- */
.trace-timeline__filters {
  display: flex;
  flex-wrap: wrap;
  gap: 0.375rem 0.5rem;
  padding: 0.5rem;
  border-bottom: 1px solid #1e293b;
  background: #0f172a;
}

.trace-timeline__filter-group {
  display: flex;
  align-items: center;
  gap: 0.25rem;
  flex-wrap: wrap;
}

.trace-timeline__filter-label {
  color: #64748b;
  font-size: 0.75rem;
  white-space: nowrap;
}

.trace-timeline__chip {
  padding: 0.1rem 0.4rem;
  border: 1px solid #334155;
  border-radius: 999px;
  background: #1e293b;
  color: #94a3b8;
  cursor: pointer;
  font-size: 0.75rem;
  transition: background 0.1s, color 0.1s, border-color 0.1s;
}

.trace-timeline__chip.active {
  background: #1d4ed8;
  border-color: #3b82f6;
  color: #eff6ff;
}

.trace-timeline__chip--clear {
  background: #7f1d1d;
  border-color: #ef4444;
  color: #fee2e2;
}

.trace-timeline__select {
  background: #1e293b;
  border: 1px solid #334155;
  color: #e2e8f0;
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
  background: #1e293b;
  border-bottom: 1px solid #0f172a;
  cursor: pointer;
  user-select: none;
  position: sticky;
  top: 0;
  z-index: 2;
}

.trace-timeline__phase-header:hover {
  background: #293548;
}

/* --- Turn header (sub-level within a phase) --- */
.trace-timeline__turn-header {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  padding: 0.25rem 0.6rem 0.25rem 1.5rem;
  background: #0d1526;
  border-bottom: 1px solid #0f172a;
  cursor: pointer;
  user-select: none;
  position: sticky;
  top: 0;
  z-index: 1;
}

.trace-timeline__turn-header:hover {
  background: #1a2436;
}

/* Off-path sub-group header — visually nested under the parent turn */
.trace-timeline__turn-header--offpath {
  background: #0a1220;
  border-left: 2px solid #334155;
  padding-left: 1.4rem;
}

.trace-timeline__turn-header--offpath:hover {
  background: #111d30;
}

.trace-timeline__turn-offpath-indent {
  color: #475569;
  font-size: 0.75rem;
  margin-right: 0.1rem;
}

.trace-timeline__turn-offpath-label {
  font-family: ui-monospace, monospace;
  font-size: 0.7rem;
  color: #64748b;
  font-style: italic;
}

.trace-timeline__turn-caret {
  color: #64748b;
  font-size: 0.7rem;
}

.trace-timeline__turn-label {
  font-family: ui-monospace, monospace;
  font-size: 0.7rem;
  color: #64748b;
  font-weight: 400;
}

.trace-timeline__turn-phase {
  font-weight: 700;
  font-size: 0.875rem;
  color: #e2e8f0;
  letter-spacing: 0.01em;
}

.trace-timeline__turn-count {
  margin-left: auto;
  color: #475569;
  font-size: 0.7rem;
}

/* --- Row --- */
.trace-timeline__row {
  border-bottom: 1px solid #1a2337;
  cursor: pointer;
}

.trace-timeline__row:hover .trace-timeline__row-main {
  background: #162032;
}

.trace-timeline__row.selected .trace-timeline__row-main {
  background: #1e3a5f;
  border-left: 2px solid #60a5fa;
}

.trace-timeline__row.highlighted .trace-timeline__row-main {
  background: #2a2010;
  border-left: 2px solid #fbbf24;
}

.trace-timeline__row.highlighted.selected .trace-timeline__row-main {
  background: #2a2820;
  border-left: 2px solid #fbbf24;
  box-shadow: inset 4px 0 0 #60a5fa;
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
  background: #1e293b;
  color: #94a3b8;
}

.trace-timeline__subsystem-chip[data-subsystem="turn"]    { background: #1e3a5f; color: #93c5fd; }
.trace-timeline__subsystem-chip[data-subsystem="user"]    { background: #312e81; color: #a5b4fc; }
.trace-timeline__subsystem-chip[data-subsystem="machine"] { background: #14532d; color: #86efac; }
.trace-timeline__subsystem-chip[data-subsystem="world"]   { background: #134e4a; color: #5eead4; }
.trace-timeline__subsystem-chip[data-subsystem="host"]    { background: #4a1d96; color: #c4b5fd; }
.trace-timeline__subsystem-chip[data-subsystem="oracle"]  { background: #7c2d12; color: #fdba74; }
.trace-timeline__subsystem-chip[data-subsystem="harness"] { background: #1e3a5f; color: #7dd3fc; }

.trace-timeline__msg {
  flex: 1;
  color: #e2e8f0;
  font-family: ui-monospace, monospace;
  font-size: 0.775rem;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.trace-timeline__effect-count {
  display: inline-flex;
  align-items: center;
  background: #14532d;
  color: #86efac;
  border: 1px solid #166534;
  border-radius: 3px;
  font-size: 0.65rem;
  padding: 0.02rem 0.3rem;
  margin-left: 0.35rem;
  vertical-align: middle;
}

.trace-timeline__level {
  color: #64748b;
  font-size: 0.7rem;
  min-width: 2.5rem;
  text-align: right;
}
.trace-timeline__level[data-level="warn"]  { color: #fbbf24; }
.trace-timeline__level[data-level="error"] { color: #f87171; }
.trace-timeline__level[data-level="debug"] { color: #475569; }

.trace-timeline__time {
  color: #475569;
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

.trace-timeline__incomplete {
  color: #fecaca;
  background: #7f1d1d;
  border: 1px solid #ef4444;
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

.trace-timeline__expand-btn {
  background: none;
  border: 1px solid #334155;
  color: #64748b;
  cursor: pointer;
  padding: 0.05rem 0.3rem;
  border-radius: 3px;
  font-size: 0.75rem;
  line-height: 1;
}

.trace-timeline__expand-btn:hover {
  background: #1e293b;
  color: #e2e8f0;
}

/* --- Expanded row body --- */
.trace-timeline__row-body {
  padding: 0.4rem 0.6rem;
  background: #080f1a;
  border-top: 1px solid #1e293b;
}

.trace-timeline__attrs-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 0.3rem;
  color: #64748b;
  font-size: 0.75rem;
}

.trace-timeline__copy-btn {
  background: none;
  border: 1px solid #334155;
  color: #64748b;
  cursor: pointer;
  padding: 0.05rem 0.3rem;
  border-radius: 3px;
  font-size: 0.75rem;
  line-height: 1;
}

.trace-timeline__copy-btn:hover {
  background: #1e293b;
  color: #e2e8f0;
}

.trace-timeline__copy-btn--copied {
  border-color: #166534;
  color: #86efac;
}

.trace-timeline__attrs-pre {
  color: #7dd3fc;
  font-family: ui-monospace, monospace;
  font-size: 0.75rem;
  white-space: pre-wrap;
  word-break: break-word;
  margin: 0;
}

/* --- Empty state --- */
.trace-timeline__empty {
  padding: 1rem;
  color: #475569;
  font-size: 0.875rem;
  text-align: center;
}
</style>
