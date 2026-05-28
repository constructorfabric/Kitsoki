<template>
  <div class="state-diagram" ref="containerRef">
    <div v-if="!diagram || diagram.phases.length === 0" class="state-diagram__empty">
      No diagram available.
    </div>
    <template v-else>
      <div
        v-for="(phase, phaseIdx) in diagram.phases"
        :key="phase.id"
        class="state-diagram__phase"
        :class="{
          'state-diagram__phase--exit': isExitPhase(phase),
          'state-diagram__phase--current': phaseContainsCurrent(phase),
          'state-diagram__phase--highlight-target': highlightedPhaseIds.has(phase.id),
        }"
      >
        <header
          class="state-diagram__phase-header"
          :title="phase.desc"
          @click="onPhaseClick(phase)"
        >
          <span class="state-diagram__phase-index">{{ phaseIdx + 1 }}</span>
          <span class="state-diagram__phase-name">{{ phase.name }}</span>
          <span v-if="phase.desc" class="state-diagram__phase-desc">{{ phase.desc }}</span>
        </header>

        <div class="state-diagram__rooms">
          <button
            v-for="room in phase.rooms"
            :key="room.id"
            type="button"
            class="state-diagram__room"
            :class="{
              'state-diagram__room--current': room.id === currentRoomId,
              'state-diagram__room--highlight': highlightedRoomIds.has(room.id),
            }"
            :title="`${room.label} — ${outgoingFor(room.id).length} transition(s)`"
            @click.stop="onRoomClick(room)"
          >
            <span class="state-diagram__room-label">{{ room.label }}</span>
            <span
              v-if="outgoingFor(room.id).length > 0"
              class="state-diagram__room-out"
            >
              {{ outgoingFor(room.id).length }}
            </span>
          </button>
        </div>

        <!-- Event lane: trace events that occurred in this phase, left→right -->
        <div
          v-if="phaseEventBoxes(phase).length > 0"
          class="state-diagram__event-lane"
        >
          <div
            v-for="pe in phaseEventBoxes(phase)"
            :key="pe.index"
            class="state-diagram__event-box"
            :class="[
              `state-diagram__event-box--${pe.subsystem}`,
              { 'state-diagram__event-box--selected': pe.index === selectedEventIndex },
            ]"
            :title="`[${pe.subsystem}] ${pe.label}${pe.durationMs != null ? ` — ${fmtDur(pe.durationMs)}` : ''}`"
            @click.stop="emit('selectEvent', pe.index)"
          >
            <span class="state-diagram__event-box-label">{{ pe.label }}</span>
            <span v-if="pe.durationMs != null" class="state-diagram__event-box-dur">{{ fmtDur(pe.durationMs) }}</span>
          </div>
        </div>
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed } from "vue";
import { parseDiagram } from "../diagram/parse.js";
import type { Diagram, Phase, Room, Edge } from "../diagram/parse.js";
import type { NodeRef, TraceEvent } from "../types.js";

const props = defineProps<{
  mermaidSource: string;
  /** Kept for compatibility with the store/snapshot contract; unused now that
   *  we parse the mermaid source directly. */
  nodeMap: Record<string, NodeRef>;
  currentStatePath: string;
  /** Refs (state paths) that are currently highlighted via diagram click. */
  highlightedStatePaths?: string[];
  /** When provided, renders a horizontal event lane within each phase. */
  events?: TraceEvent[];
  /** Index of the selected event; the matching box gets a highlight ring. */
  selectedEventIndex?: number | null;
}>();

const emit = defineEmits<{
  (e: "select", nodeId: string, nodeRef: NodeRef): void;
  (e: "selectPhase", phaseId: string, roomRefs: string[]): void;
  (e: "clearHighlight"): void;
  (e: "selectEvent", index: number): void;
}>();

const diagram = computed<Diagram | null>(() => {
  if (!props.mermaidSource) return null;
  try {
    return parseDiagram(props.mermaidSource);
  } catch (err) {
    console.error("[StateDiagram] parse failed:", err);
    return null;
  }
});

// roomId for the current state path.  Matches either by exact equality
// (cloak: room.label === "bar.lit") or by prefix (bugfix: room "reproducing"
// matches state_path "reproducing._executing").  When multiple rooms match,
// the longest match wins.
const currentRoomId = computed<string | null>(() => {
  const d = diagram.value;
  if (!d) return null;
  let best: string | null = null;
  let bestLen = -1;
  for (const phase of d.phases) {
    for (const room of phase.rooms) {
      const lbl = room.label;
      if (
        props.currentStatePath === lbl ||
        props.currentStatePath.startsWith(lbl + ".")
      ) {
        if (lbl.length > bestLen) {
          best = room.id;
          bestLen = lbl.length;
        }
      }
    }
  }
  return best;
});

// Map from roomId → outgoing edges (forward direction only, excluding self-loops).
const outgoingByRoom = computed<Map<string, Edge[]>>(() => {
  const m = new Map<string, Edge[]>();
  const d = diagram.value;
  if (!d) return m;
  for (const e of d.edges) {
    if (!d.roomById.has(e.from)) continue;
    if (e.selfLoop) continue;
    const arr = m.get(e.from) ?? [];
    arr.push(e);
    m.set(e.from, arr);
  }
  return m;
});

function outgoingFor(roomId: string): Edge[] {
  return outgoingByRoom.value.get(roomId) ?? [];
}

// Resolve highlightedStatePaths → set of room ids using prefix matching, so a
// highlighted path "reproducing._executing" still flags the "reproducing"
// room (and vice versa).
const highlightedRoomIds = computed<Set<string>>(() => {
  const out = new Set<string>();
  const d = diagram.value;
  if (!d || !props.highlightedStatePaths || props.highlightedStatePaths.length === 0) return out;
  for (const path of props.highlightedStatePaths) {
    for (const phase of d.phases) {
      for (const room of phase.rooms) {
        const lbl = room.label;
        if (path === lbl || path.startsWith(lbl + ".") || lbl.startsWith(path + ".")) {
          out.add(room.id);
        }
      }
    }
  }
  return out;
});

const highlightedPhaseIds = computed<Set<string>>(() => {
  const out = new Set<string>();
  const d = diagram.value;
  if (!d) return out;
  for (const roomId of highlightedRoomIds.value) {
    const phaseId = d.phaseByRoom.get(roomId);
    if (phaseId) out.add(phaseId);
  }
  return out;
});

// ---- Event lane --------------------------------------------------------

type EventBoxSubsystem = "oracle" | "host" | "machine" | "world" | "user" | "other";

interface PhaseEventBox {
  index: number;
  subsystem: EventBoxSubsystem;
  label: string;
  durationMs?: number;
}

const ORACLE_START_RE_DIAG = /^oracle\.(decide|extract|ask|task|converse)\.start$/;
const ORACLE_COMPLETE_RE_DIAG = /^oracle\.(decide|extract|ask|task|converse)\.complete$/;

function fmtDur(ms: number): string {
  return ms < 1000 ? `${ms}ms` : `${(ms / 1000).toFixed(1)}s`;
}

// Rooms sorted longest-label-first for prefix matching (avoids shorter labels
// shadowing longer ones, e.g. "reproducing" vs "reproducing._executing").
const roomsSortedForMatch = computed(() => {
  const d = diagram.value;
  if (!d) return [];
  return [...d.roomById.values()].sort((a, b) => b.label.length - a.label.length);
});

function phaseIdForStatePath(sp: string): string | null {
  for (const r of roomsSortedForMatch.value) {
    if (sp === r.label || sp.startsWith(r.label + ".")) return r.phaseId;
  }
  return null;
}

const eventLaneByPhase = computed<Map<string, PhaseEventBox[]>>(() => {
  const events = props.events;
  if (!events || !diagram.value) return new Map();

  // oracle complete map: call_id → duration_ms
  const oracleCompleteDur = new Map<string, number | null>();
  const oracleStartCallIds = new Set<string>();
  for (const e of events) {
    if (ORACLE_COMPLETE_RE_DIAG.test(e.msg)) {
      const cid = e.attrs.call_id;
      if (typeof cid === "string") {
        const dur = typeof e.attrs.duration_ms === "number" ? (e.attrs.duration_ms as number) : null;
        oracleCompleteDur.set(cid, dur);
      }
    }
    if (ORACLE_START_RE_DIAG.test(e.msg)) {
      const cid = e.attrs.call_id;
      if (typeof cid === "string") oracleStartCallIds.add(cid);
    }
  }

  // harness: dispatched + returned are suppressed (absorbed into called)
  const harnessSuppress = new Set<number>();
  const harnessByKey = new Map<string, number[]>();
  for (let i = 0; i < events.length; i++) {
    const e = events[i]!;
    if (!e.msg.startsWith("harness.")) continue;
    const ns = typeof e.attrs.namespace === "string" ? e.attrs.namespace : "";
    if (ns.startsWith("host.oracle.")) continue;
    if (e.msg === "harness.dispatched" || e.msg === "harness.returned") {
      harnessSuppress.add(i);
    } else if (e.msg === "harness.called") {
      const key = `${e.turn}:${ns}`;
      const arr = harnessByKey.get(key) ?? [];
      arr.push(i);
      harnessByKey.set(key, arr);
    }
  }

  // world.update: one box per turn (first occurrence)
  const worldUpdateSeen = new Set<number>();

  const result = new Map<string, PhaseEventBox[]>();

  // turn.input events are deferred (same logic as TraceTimeline's groupedTurns):
  // they must appear last in the phase lane, after the machine work they triggered.
  const deferredTurnInputs: Array<{ i: number; phaseId: string; box: PhaseEventBox }> = [];

  for (let i = 0; i < events.length; i++) {
    const e = events[i]!;
    const phaseId = phaseIdForStatePath(e.state_path);
    if (!phaseId) continue;

    let box: PhaseEventBox | null = null;

    if (e.msg === "turn.input") {
      const text = String(e.attrs.input ?? "");
      const label = text.slice(0, 22) + (text.length > 22 ? "…" : "");
      deferredTurnInputs.push({ i, phaseId, box: { index: i, subsystem: "user", label } });
      continue;
    } else if (e.msg.startsWith("harness.")) {
      const ns = typeof e.attrs.namespace === "string" ? e.attrs.namespace : "";
      if (ns.startsWith("host.oracle.")) continue;
      if (harnessSuppress.has(i)) continue;
      if (e.msg !== "harness.called") continue;
      const parts = ns.split(".");
      const label = parts[parts.length - 1] ?? ns;
      box = { index: i, subsystem: "host", label };
    } else if (ORACLE_COMPLETE_RE_DIAG.test(e.msg)) {
      // Only show orphan completes (no paired start in this trace)
      const cid = typeof e.attrs.call_id === "string" ? e.attrs.call_id : null;
      if (cid && oracleStartCallIds.has(cid)) continue;
      const m = ORACLE_COMPLETE_RE_DIAG.exec(e.msg);
      box = { index: i, subsystem: "oracle", label: m ? m[1]! : "oracle" };
    } else if (ORACLE_START_RE_DIAG.test(e.msg)) {
      const m = ORACLE_START_RE_DIAG.exec(e.msg);
      const verb = m ? m[1]! : "oracle";
      const cid = typeof e.attrs.call_id === "string" ? e.attrs.call_id : null;
      const dur = cid ? oracleCompleteDur.get(cid) : null;
      box = { index: i, subsystem: "oracle", label: verb, durationMs: dur ?? undefined };
    } else if (e.msg === "world.update" && e.attrs.set && typeof e.attrs.set === "object" && !Array.isArray(e.attrs.set)) {
      if (worldUpdateSeen.has(e.turn)) continue;
      worldUpdateSeen.add(e.turn);
      box = { index: i, subsystem: "world", label: "update" };
    } else if (
      e.msg === "machine.state_entered" || e.msg === "machine.state_exited" ||
      e.msg === "machine.transition" || e.msg === "turn.start" || e.msg === "turn.end"
    ) {
      continue;
    } else {
      const label = e.msg;
      box = { index: i, subsystem: "other", label };
    }

    if (box) {
      const arr = result.get(phaseId) ?? [];
      arr.push(box);
      result.set(phaseId, arr);
    }
  }

  // Append deferred turn.input boxes last in each phase lane.
  for (const { phaseId, box } of deferredTurnInputs) {
    const arr = result.get(phaseId) ?? [];
    arr.push(box);
    result.set(phaseId, arr);
  }

  return result;
});

function phaseEventBoxes(phase: Phase): PhaseEventBox[] {
  return eventLaneByPhase.value.get(phase.id) ?? [];
}

// ---- End event lane -------------------------------------------------------

function isExitPhase(p: Phase): boolean {
  return p.name.startsWith("__exit__") || p.name === "ended";
}

function phaseContainsCurrent(p: Phase): boolean {
  const cur = currentRoomId.value;
  if (!cur) return false;
  return p.rooms.some((r) => r.id === cur);
}

function onRoomClick(room: Room): void {
  emit("select", room.id, { kind: "state", ref: room.label });
}

function onPhaseClick(phase: Phase): void {
  const refs = phase.rooms.map((r) => r.label);
  emit("selectPhase", phase.id, refs);
}
</script>

<style scoped>
.state-diagram {
  width: 100%;
  height: 100%;
  overflow-y: auto;
  overflow-x: hidden;
  background: #0f172a;
  border: 1px solid #1e293b;
  border-radius: 6px;
  padding: 0.5rem;
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}

.state-diagram__empty {
  color: #64748b;
  font-size: 0.875rem;
  padding: 1rem;
}

/* ---- Phase card --------------------------------------------------------- */
.state-diagram__phase {
  border: 1px solid #1e293b;
  border-radius: 6px;
  background: #0a1326;
  padding: 0.4rem 0.5rem 0.5rem;
  transition: border-color 0.15s, background 0.15s;
  position: relative;
}

.state-diagram__phase:not(:last-child)::after {
  content: "▼";
  position: absolute;
  bottom: -0.7rem;
  left: 1rem;
  font-size: 0.7rem;
  color: #334155;
  background: #0f172a;
  padding: 0 0.2rem;
  pointer-events: none;
  z-index: 1;
}

.state-diagram__phase--exit {
  border-style: dashed;
  opacity: 0.7;
}

.state-diagram__phase--current {
  border-color: #60a5fa;
  background: #0c1a36;
}

.state-diagram__phase--highlight-target {
  border-color: #fbbf24;
  box-shadow: 0 0 0 1px rgba(251, 191, 36, 0.3);
}

/* ---- Phase header ------------------------------------------------------- */
.state-diagram__phase-header {
  display: flex;
  align-items: baseline;
  gap: 0.45rem;
  cursor: pointer;
  padding: 0.1rem 0.2rem 0.3rem;
  user-select: none;
}

.state-diagram__phase-header:hover {
  background: rgba(96, 165, 250, 0.06);
}

.state-diagram__phase-index {
  font-size: 0.65rem;
  font-weight: 700;
  color: #475569;
  background: #1e293b;
  border-radius: 3px;
  padding: 0.05rem 0.3rem;
  flex-shrink: 0;
  letter-spacing: 0.04em;
}

.state-diagram__phase-name {
  font-weight: 600;
  color: #e2e8f0;
  font-size: 0.85rem;
  font-family: ui-monospace, monospace;
}

.state-diagram__phase-desc {
  flex: 1;
  color: #64748b;
  font-size: 0.7rem;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

/* ---- Rooms row ---------------------------------------------------------- */
.state-diagram__rooms {
  display: flex;
  flex-wrap: wrap;
  gap: 0.35rem;
  padding: 0.1rem 0;
}

.state-diagram__room {
  display: inline-flex;
  align-items: center;
  gap: 0.35rem;
  padding: 0.3rem 0.55rem;
  border: 1px solid #334155;
  border-radius: 4px;
  background: #1e293b;
  color: #cbd5e1;
  cursor: pointer;
  font-size: 0.775rem;
  font-family: ui-monospace, monospace;
  transition: border-color 0.1s, background 0.1s, color 0.1s, box-shadow 0.1s;
}

.state-diagram__room:hover {
  background: #2a3b54;
  border-color: #475569;
  color: #f1f5f9;
}

.state-diagram__room--current {
  border-color: #60a5fa;
  background: #1e3a5f;
  color: #dbeafe;
  box-shadow: 0 0 0 1px rgba(96, 165, 250, 0.5), 0 0 6px rgba(96, 165, 250, 0.4);
}

.state-diagram__room--highlight {
  border-color: #fbbf24;
  background: #3a2d0e;
  color: #fde68a;
}

.state-diagram__room-label {
  font-weight: 500;
}

.state-diagram__room-out {
  font-size: 0.65rem;
  color: #94a3b8;
  background: #0f172a;
  border-radius: 999px;
  padding: 0.05rem 0.35rem;
  font-family: ui-monospace, monospace;
}

.state-diagram__room--current .state-diagram__room-out {
  background: #0a1226;
  color: #93c5fd;
}

/* ---- Event lane (inside phase card, below rooms) ------------------------ */
.state-diagram__event-lane {
  display: flex;
  flex-wrap: nowrap;
  align-items: center;
  overflow-x: auto;
  gap: 0.25rem;
  padding: 0.3rem 0 0.1rem;
  scrollbar-width: thin;
  scrollbar-color: #334155 transparent;
}

.state-diagram__event-box {
  display: inline-flex;
  align-items: center;
  gap: 0.2rem;
  padding: 0.18rem 0.4rem;
  border-radius: 3px;
  font-size: 0.7rem;
  font-family: ui-monospace, monospace;
  white-space: nowrap;
  cursor: pointer;
  flex-shrink: 0;
  border: 1px solid transparent;
  transition: opacity 0.1s, box-shadow 0.1s;
}

.state-diagram__event-box:hover {
  opacity: 0.8;
}

.state-diagram__event-box--oracle  { background: #7c2d12; color: #fdba74; border-color: #9a3412; }
.state-diagram__event-box--host    { background: #4a1d96; color: #c4b5fd; border-color: #5b21b6; }
.state-diagram__event-box--machine { background: #14532d; color: #86efac; border-color: #166534; }
.state-diagram__event-box--world   { background: #134e4a; color: #5eead4; border-color: #0f766e; }
.state-diagram__event-box--user    { background: #312e81; color: #a5b4fc; border-color: #3730a3; }
.state-diagram__event-box--other   { background: #1e293b; color: #94a3b8; border-color: #334155; }

.state-diagram__event-box--selected {
  box-shadow: 0 0 0 2px #60a5fa;
}

.state-diagram__event-box-label {
  max-width: 10rem;
  overflow: hidden;
  text-overflow: ellipsis;
}

.state-diagram__event-box-dur {
  font-size: 0.65rem;
  opacity: 0.75;
  flex-shrink: 0;
}
</style>
