<template>
  <div class="state-diagram" ref="containerRef">
    <div v-if="!diagram || diagram.phases.length === 0" class="state-diagram__empty">
      No diagram available.
    </div>

    <template v-else>
      <!-- View switcher. metro / ego / path are route-centric (they answer
           "where am I in the machine"); full is the whole static graph. Shown
           only when a current room resolves (see the mode watcher). -->
      <div v-if="currentRoomId" class="state-diagram__tabs" data-testid="diagram-tabs" role="tablist">
        <button
          v-for="t in viewTabs"
          :key="t.mode"
          type="button"
          class="state-diagram__tab"
          :class="{ 'state-diagram__tab--active': mode === t.mode }"
          :data-testid="`diagram-tab-${t.mode}`"
          role="tab"
          :aria-selected="mode === t.mode"
          :title="t.title"
          @click="setMode(t.mode)"
        >
          {{ t.label }}
        </button>
      </div>

      <!-- ░░ METRO STEPPER (vertical interchange line) ░░ -->
      <!-- Three truth tiers, visually distinct per tools/runstatus/CLAUDE.md:
           traveled (TRACE, solid green), current (LIVE, amber) + horizon pills,
           road ahead (PROJECTION, muted/dashed). Stations are ROOMS — the
           design pipeline lives in one phase, so a phase spine would collapse
           it. Banners are declared metadata (parse.ts Room.banner). -->
      <div v-if="mode === 'metro'" class="state-diagram__metro" data-testid="diagram-metro">
        <!-- Traveled leg (TRACE) -->
        <button
          v-for="r in traveledRooms"
          :key="r.id"
          type="button"
          class="state-diagram__ms-step state-diagram__ms-step--done"
          data-testid="diagram-metro-station"
          :title="`Visited: ${r.label}`"
          @click="onRoomClickById(r.id)"
        >
          <span class="state-diagram__ms-rail"><span class="state-diagram__ms-dot" /></span>
          <span class="state-diagram__ms-body">
            <span class="state-diagram__ms-headline">
              <span class="state-diagram__ms-name">{{ r.label }}</span>
              <span v-if="r.banner" class="state-diagram__ms-banner state-diagram__ms-banner--done">{{ r.banner }}</span>
            </span>
            <span v-if="enteringIntent(r.id)" class="state-diagram__ms-via" :title="enteringIntent(r.id)">via {{ humanizeIntent(enteringIntent(r.id)) }}</span>
          </span>
        </button>

        <!-- Current station (LIVE) + horizon pills -->
        <div
          v-if="currentRoom"
          class="state-diagram__ms-step state-diagram__ms-step--current"
          data-testid="diagram-current-station"
        >
          <span class="state-diagram__ms-rail"><span class="state-diagram__ms-dot state-diagram__ms-dot--current" /></span>
          <span class="state-diagram__ms-body">
            <span class="state-diagram__ms-headline">
              <span class="state-diagram__ms-name state-diagram__ms-name--current">{{ currentRoom.label }}</span>
              <span v-if="currentRoom.banner" class="state-diagram__ms-banner state-diagram__ms-banner--current">{{ currentRoom.banner }}</span>
            </span>
            <span class="state-diagram__ms-here">▸ you are here</span>
            <span class="state-diagram__ms-pills">
              <button
                v-for="(arc, i) in horizonArcs"
                :key="arc.intent + ':' + i"
                type="button"
                class="state-diagram__pill"
                :class="[`state-diagram__pill--${arc.kind}`, { 'state-diagram__pill--dead': arc.targetRoomId === null }]"
                data-testid="diagram-horizon-pill"
                :disabled="arc.targetRoomId === null"
                :title="pillTitle(arc)"
                @click="onPillClick(arc)"
              >
                <span class="state-diagram__pill-intent">{{ arc.label }}</span>
                <span v-if="arc.targetRoomId" class="state-diagram__pill-arrow">→</span>
                <span v-if="arc.targetRoomId" class="state-diagram__pill-target">{{ roomLabel(arc.targetRoomId) }}</span>
              </button>
              <span v-if="horizonArcs.length === 0" class="state-diagram__pills-empty">no moves available</span>
            </span>
          </span>
        </div>

        <!-- Road ahead (PROJECTION) -->
        <button
          v-for="(r, idx) in roomAhead.rooms"
          :key="r.id"
          type="button"
          class="state-diagram__ms-step state-diagram__ms-step--ahead"
          :class="{ 'state-diagram__ms-step--terminal': idx === roomAhead.rooms.length - 1 && isTerminalRoom(r) }"
          data-testid="diagram-road-ahead"
          :title="`Declared ahead: ${r.label}`"
          @click="onRoomClickById(r.id)"
        >
          <span class="state-diagram__ms-rail"><span class="state-diagram__ms-dot state-diagram__ms-dot--ahead" /></span>
          <span class="state-diagram__ms-body">
            <span class="state-diagram__ms-headline">
              <span class="state-diagram__ms-name">{{ r.label }}</span>
              <span v-if="r.banner" class="state-diagram__ms-banner state-diagram__ms-banner--ahead">{{ r.banner }}</span>
            </span>
          </span>
        </button>

        <div v-if="roomAhead.rooms.length === 0 && currentRoom" class="state-diagram__ms-end">⚑ journey's end</div>

        <button
          v-if="elsewhereCount > 0"
          type="button"
          class="state-diagram__elsewhere"
          data-testid="diagram-elsewhere"
          title="Show the full static graph"
          @click="setMode('full')"
        >
          +{{ elsewhereCount }} elsewhere
        </button>
      </div>

      <!-- ░░ EGO-GRAPH (1-hop SVG node-link) ░░ -->
      <!-- came-from (TRACE) → current (LIVE) → exits (LIVE), elbow connectors
           with arrowheads. Node label text is compressed to its rect via
           textLength so long room ids can't overflow (SVG-containment rule). -->
      <div v-else-if="mode === 'ego'" class="state-diagram__ego" data-testid="diagram-ego">
        <svg v-if="ego" :viewBox="`0 0 ${ego.W} ${ego.H}`" role="img" aria-label="1-hop neighbourhood graph">
          <defs>
            <marker id="kd-ar-blue" markerWidth="7" markerHeight="7" refX="6" refY="3" orient="auto">
              <path d="M0,0 L6,3 L0,6 Z" fill="#60a5fa" />
            </marker>
            <marker id="kd-ar-grey" markerWidth="7" markerHeight="7" refX="6" refY="3" orient="auto">
              <path d="M0,0 L6,3 L0,6 Z" fill="#64748b" />
            </marker>
          </defs>

          <!-- came-from edge + node (TRACE) -->
          <template v-if="ego.cameNode">
            <line
              :x1="ego.cameNode.x + ego.cameNode.w"
              :y1="ego.CUR.y + ego.CUR.h / 2"
              :x2="ego.CUR.x"
              :y2="ego.CUR.y + ego.CUR.h / 2"
              stroke="#3b82f6"
              stroke-width="2"
              marker-end="url(#kd-ar-blue)"
            />
            <text
              v-if="ego.cameIntent"
              class="state-diagram__ego-elabel"
              :x="(ego.cameNode.x + ego.cameNode.w + ego.CUR.x) / 2"
              :y="ego.CUR.y + ego.CUR.h / 2 - 5"
              text-anchor="middle"
              :textLength="ego.CUR.x - (ego.cameNode.x + ego.cameNode.w) - 10"
              lengthAdjust="spacingAndGlyphs"
            >{{ ego.cameIntent }}</text>
            <g class="state-diagram__ego-g" @click="onRoomClickById(traveledRooms[traveledRooms.length - 1]!.id)">
              <rect class="state-diagram__ego-node-from" :x="ego.cameNode.x" :y="ego.cameNode.y" :width="ego.cameNode.w" :height="ego.cameNode.h" rx="7" />
              <text class="state-diagram__ego-nlabel" :x="ego.cameNode.x + ego.cameNode.w / 2" :y="ego.cameNode.y + ego.cameNode.h / 2 + 4" text-anchor="middle" :textLength="ego.cameNode.w - 12" lengthAdjust="spacingAndGlyphs">{{ ego.cameNode.label }}</text>
            </g>
          </template>

          <!-- exit edges + nodes (LIVE) -->
          <template v-for="(ex, i) in ego.exits" :key="i">
            <polyline
              :points="egoEdge(ex)"
              fill="none"
              :stroke="ex.ghost ? '#64748b' : '#3b82f6'"
              stroke-width="2"
              stroke-linejoin="round"
              :marker-end="ex.ghost ? 'url(#kd-ar-grey)' : 'url(#kd-ar-blue)'"
            />
            <text
              class="state-diagram__ego-elabel"
              :x="ex.x - 7"
              :y="ex.cy - 5"
              text-anchor="end"
            >{{ ex.intent }}</text>
            <g class="state-diagram__ego-g" data-testid="diagram-ego-exit" @click="onEgoExitClick(ex)">
              <rect :class="ex.ghost ? 'state-diagram__ego-node-ghost' : 'state-diagram__ego-node-to'" :x="ex.x" :y="ex.y" :width="ex.w" :height="ex.h" rx="7" />
              <text class="state-diagram__ego-nlabel" :x="ex.x + ex.w / 2" :y="ex.cy + 4" text-anchor="middle" :textLength="ex.w - 12" lengthAdjust="spacingAndGlyphs">{{ ex.label }}</text>
            </g>
          </template>

          <!-- current node (LIVE) -->
          <g data-testid="diagram-ego-current">
            <rect :x="ego.CUR.x - 3" :y="ego.CUR.y - 3" :width="ego.CUR.w + 6" :height="ego.CUR.h + 6" rx="10" fill="none" stroke="#f59e0b" stroke-width="6" opacity="0.18" />
            <rect class="state-diagram__ego-node-cur" :x="ego.CUR.x" :y="ego.CUR.y" :width="ego.CUR.w" :height="ego.CUR.h" rx="9" />
            <text class="state-diagram__ego-nlabel-cur" :x="ego.CUR.x + ego.CUR.w / 2" :y="ego.CUR.y + 21" text-anchor="middle" :textLength="ego.CUR.w - 14" lengthAdjust="spacingAndGlyphs">{{ ego.CUR.label }}</text>
            <text v-if="ego.CUR.banner" class="state-diagram__ego-banner" :x="ego.CUR.x + ego.CUR.w / 2" :y="ego.CUR.y + 35" text-anchor="middle">{{ ego.CUR.banner }}</text>
            <text class="state-diagram__ego-here" :x="ego.CUR.x + ego.CUR.w / 2" :y="ego.CUR.y + 48" text-anchor="middle">▸ you are here</text>
          </g>
        </svg>
        <div v-else class="state-diagram__empty">No current room.</div>
      </div>

      <!-- ░░ PATH & HORIZON (breadcrumb + chips) ░░ -->
      <!-- Provenance (traveled breadcrumb with "via <intent>") on top, the
           current room as a hero card, live exits as chips below. Lightest
           weight, no SVG. Same three tiers as the metro. -->
      <div v-else-if="mode === 'path'" class="state-diagram__path" data-testid="diagram-path-horizon">

      <!-- Traveled breadcrumb (TRACE): each room + the intent that entered it. -->
      <div class="state-diagram__crumbs" data-testid="diagram-traveled">
        <template v-if="traveledRooms.length > 0">
          <template v-for="r in traveledRooms" :key="r.id">
            <button
              type="button"
              class="state-diagram__crumb"
              :title="`Visited: ${r.label}`"
              @click="onRoomClickById(r.id)"
            >
              <span class="state-diagram__crumb-room">{{ r.label }}</span>
              <span v-if="enteringIntent(r.id)" class="state-diagram__crumb-via" :title="enteringIntent(r.id)">via {{ humanizeIntent(enteringIntent(r.id)) }}</span>
            </button>
            <span class="state-diagram__crumb-arrow">→</span>
          </template>
        </template>
        <span v-else class="state-diagram__journey-start">▶ start of journey</span>
      </div>

      <!-- Current station (LIVE — amber/solid) + horizon exit pills. -->
      <div
        class="state-diagram__station state-diagram__station--current"
        data-testid="diagram-current-station"
      >
        <header class="state-diagram__station-head">
          <span class="state-diagram__station-dot state-diagram__station-dot--current" />
          <span class="state-diagram__station-room">{{ currentRoomLabel }}</span>
          <span v-if="currentRoom?.banner" class="state-diagram__ms-banner state-diagram__ms-banner--current">{{ currentRoom.banner }}</span>
          <span v-else class="state-diagram__station-phase">{{ currentPhaseName }}</span>
        </header>
        <div class="state-diagram__pills">
          <template v-if="horizonArcs.length > 0">
            <button
              v-for="(arc, i) in horizonArcs"
              :key="arc.intent + ':' + i"
              type="button"
              class="state-diagram__pill"
              :class="[
                `state-diagram__pill--${arc.kind}`,
                { 'state-diagram__pill--dead': arc.targetRoomId === null },
              ]"
              data-testid="diagram-horizon-pill"
              :disabled="arc.targetRoomId === null"
              :title="pillTitle(arc)"
              @click="onPillClick(arc)"
            >
              <span class="state-diagram__pill-intent">{{ arc.label }}</span>
              <span v-if="arc.targetRoomId" class="state-diagram__pill-arrow">→</span>
              <span v-if="arc.targetRoomId" class="state-diagram__pill-target">{{ roomLabel(arc.targetRoomId) }}</span>
            </button>
          </template>
          <span v-else class="state-diagram__pills-empty">no moves available</span>
        </div>
      </div>

      <!-- Road ahead (PROJECTION — muted/dashed): ROOMS not yet reached, room
           granularity so a one-phase pipeline still projects. NEVER traveled. -->
      <div class="state-diagram__ahead" data-testid="diagram-road-ahead">
        <template v-if="roomAhead.rooms.length > 0">
          <span class="state-diagram__ahead-cap">road ahead (declared)</span>
          <button
            v-for="r in roomAhead.rooms"
            :key="r.id"
            type="button"
            class="state-diagram__station state-diagram__station--ahead"
            :title="`Declared ahead: ${r.label}`"
            @click="onRoomClickById(r.id)"
          >
            <span class="state-diagram__station-dot state-diagram__station-dot--ahead" />
            <span class="state-diagram__station-label">{{ r.label }}</span>
            <span v-if="r.banner" class="state-diagram__ms-banner state-diagram__ms-banner--ahead">{{ r.banner }}</span>
          </button>
        </template>
        <span v-else class="state-diagram__journey-end">⚑ journey's end</span>
      </div>

      <!-- Collapsed remainder: rooms not on the path and not in the horizon. -->
      <button
        type="button"
        class="state-diagram__elsewhere"
        data-testid="diagram-elsewhere"
        :title="elsewhereCount > 0 ? 'Show the full static graph' : 'Nothing else to show'"
        @click="setMode('full')"
      >
        +{{ elsewhereCount }} elsewhere
      </button>
    </div>

      <!-- ░░ FULL static graph (the whole machine) ░░ -->
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
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { parseDiagram } from "../diagram/parse.js";
import type { Diagram, Phase, Room, Edge } from "../diagram/parse.js";
import {
  matchRoomId,
  traveledPath,
  horizon,
  roomSpineAhead,
  enteringIntents,
  type HorizonArc,
} from "../diagram/horizon.js";
import type { NodeRef, TraceEvent, IntentInfo } from "../types.js";
import { humanizeIntent } from "../lib/intent.js";

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
  /** Live allowed intents of the current room — the HORIZON source (run.ts
   *  currentView.intents). Absent for static-snapshot consumers; the path mode
   *  still renders (current station with no pills). */
  intents?: IntentInfo[];
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

// roomId for the current state path. Longest-prefix match (cloak's exact
// "bar.lit", bugfix's "reproducing._executing" → "reproducing", oregon's
// "leg_a_executing.traveling" → "leg_a_executing"). Shared with the tests via
// the pure matcher in horizon.ts so there is ONE matcher.
const currentRoomId = computed<string | null>(() =>
  matchRoomId(props.currentStatePath, diagram.value),
);

// ---- Path / horizon (metro stepper) mode --------------------------------
// Default to the path view whenever a current room resolves; fall back to the
// full static graph otherwise (a mid-stream trace with no landed state) so
// nothing regresses. The operator can flip with the view toggle; once they do,
// we respect their choice for the session and stop auto-switching.
// Four co-equal views. The three route-centric ones (metro / ego / path) all
// answer "where am I in the machine"; `full` is the whole static graph. Default
// to the metro stepper when a current room resolves (the proposal's lean — it
// balances provenance + horizon + the multi-stage pipeline shape), else full so
// nothing regresses for a mid-stream trace with no landed state. Once the
// operator picks a view we respect it for the session.
type DiagramMode = "metro" | "ego" | "path" | "full";
const mode = ref<DiagramMode>("full");
const userPinnedMode = ref(false);
watch(
  currentRoomId,
  (cur) => {
    if (userPinnedMode.value) return;
    mode.value = cur ? "metro" : "full";
  },
  { immediate: true },
);
function setMode(m: DiagramMode): void {
  userPinnedMode.value = true;
  mode.value = m;
}

const viewTabs: ReadonlyArray<{ mode: DiagramMode; label: string; title: string }> = [
  { mode: "metro", label: "Metro", title: "Vertical route: traveled leg, current stop + live exits, road ahead" },
  { mode: "ego", label: "Graph", title: "1-hop node-link: where you came from, where you are, where you can go" },
  { mode: "path", label: "Path", title: "Breadcrumb of the traveled path + the current room's live exit chips" },
  { mode: "full", label: "Full", title: "The whole static machine (every phase / room)" },
];

// Horizon (LIVE tier): allowed intents × current room outgoing edges.
const horizonArcs = computed<HorizonArc[]>(() =>
  horizon(props.intents ?? [], currentRoomId.value, diagram.value),
);

// ---- Room-level path + provenance (metro / ego / path views) -------------
// The design pipeline (dev-story) lives in ONE phase, so these walk ROOMS,
// not phases — the phase-keyed `ahead` would show nothing for it.
const traveledRoomIds = computed<string[]>(() =>
  traveledPath(props.events ?? [], diagram.value),
);
// Traveled rooms as full objects, dropping the trailing current room (it is
// rendered as its own current station). TRACE tier.
const traveledRooms = computed<Room[]>(() => {
  const d = diagram.value;
  if (!d) return [];
  const path = traveledRoomIds.value;
  const trimmed =
    path.length > 0 && path[path.length - 1] === currentRoomId.value
      ? path.slice(0, -1)
      : path;
  return trimmed.map((id) => d.roomById.get(id)).filter((r): r is Room => r != null);
});
const currentRoom = computed<Room | null>(() =>
  currentRoomId.value ? diagram.value?.roomById.get(currentRoomId.value) ?? null : null,
);
// roomId → the intent that drove entry (TRACE provenance, machine.transition).
const enteringIntentByRoom = computed(() =>
  enteringIntents(props.events ?? [], diagram.value),
);
function enteringIntent(roomId: string): string | undefined {
  return enteringIntentByRoom.value.get(roomId);
}

// humanizeIntent (display label for the "via …" breadcrumb) is shared with
// InputBar — see ../lib/intent.ts. The trace keeps the raw name (title on hover).
// Road ahead at ROOM granularity (PROJECTION). Seeds the cycle guard with the
// traveled path so the projection never loops back onto rooms already left.
const roomAhead = computed(() =>
  roomSpineAhead(currentRoomId.value, diagram.value, traveledRoomIds.value),
);
// A room is the terminus when no forward edge leaves it (only self-loops or
// edges back to the hub). Caps the metro line with a "journey's end" flag.
function isTerminalRoom(room: Room): boolean {
  const d = diagram.value;
  if (!d) return false;
  for (const e of d.edges) {
    if (e.from !== room.id || e.selfLoop) continue;
    if (e.to === d.startRoomId) continue;
    if (d.roomById.has(e.to)) return false;
  }
  return true;
}
function onRoomClickById(roomId: string): void {
  const r = diagram.value?.roomById.get(roomId);
  if (r) emit("select", r.id, { kind: "state", ref: r.label });
}

// ---- Ego-graph (1-hop SVG node-link) layout ------------------------------
// Coordinates only — the template composes <rect>/<polyline>/<text> from these
// (data-driven, never string-built markup). Node label text is compressed to
// its rect via textLength so long room ids can't overflow (the SVG-containment
// rule the proposal calls for).
interface EgoNode {
  x: number;
  y: number;
  w: number;
  h: number;
  label: string;
  banner?: string;
}
interface EgoExit extends EgoNode {
  cy: number;
  intent: string;
  targetRoomId: string;
  ghost: boolean;
}
const ego = computed(() => {
  const cur = currentRoom.value;
  if (!cur) return null;
  const W = 460;
  // Exit targets: live arcs that LEAVE the current room (drop self-loops and
  // null-target intents), deduped by target. ghost = exit-classed (hub/quit).
  const seen = new Set<string>();
  const exitsRaw: HorizonArc[] = [];
  for (const a of horizonArcs.value) {
    if (!a.targetRoomId || a.targetRoomId === currentRoomId.value) continue;
    if (seen.has(a.targetRoomId)) continue;
    seen.add(a.targetRoomId);
    exitsRaw.push(a);
  }
  const EXIT_W = 100;
  const EXIT_H = 40;
  const EXIT_GAP = 14;
  const TOP = 18;
  const EXIT_X = W - EXIT_W - 6;
  const n = Math.max(exitsRaw.length, 1);
  const blockH = n * EXIT_H + (n - 1) * EXIT_GAP;
  const H = Math.max(blockH + TOP * 2, 140);
  const midY = H / 2;
  const CUR: EgoNode = { x: 160, y: midY - 29, w: 116, h: 58, label: cur.label, banner: cur.banner };
  const came = traveledRooms.value.length
    ? traveledRooms.value[traveledRooms.value.length - 1]!
    : null;
  const cameNode: EgoNode | null = came
    ? { x: 6, y: midY - 21, w: 92, h: 42, label: came.label }
    : null;
  const cameIntent = currentRoomId.value
    ? enteringIntentByRoom.value.get(currentRoomId.value) ?? null
    : null;
  const exits: EgoExit[] = exitsRaw.map((a, i) => {
    const y = exitsRaw.length === 1 ? midY - EXIT_H / 2 : TOP + i * (EXIT_H + EXIT_GAP);
    return {
      x: EXIT_X,
      y,
      w: EXIT_W,
      h: EXIT_H,
      cy: y + EXIT_H / 2,
      label: roomLabel(a.targetRoomId!),
      intent: a.label,
      targetRoomId: a.targetRoomId!,
      ghost: a.kind === "exit",
    };
  });
  return { W, H, CUR, cameNode, cameIntent, exits };
});
// Elbow connector (right, down/up, right) from the current node to an exit.
function egoEdge(ex: { x: number; cy: number }): string {
  const e = ego.value;
  if (!e) return "";
  const sx = e.CUR.x + e.CUR.w;
  const sy = e.CUR.y + e.CUR.h / 2;
  const bx = sx + 14;
  return `${sx},${sy} ${bx},${sy} ${bx},${ex.cy} ${ex.x},${ex.cy}`;
}
function onEgoExitClick(ex: { targetRoomId: string }): void {
  onRoomClickById(ex.targetRoomId);
}

const currentRoomLabel = computed<string>(() => {
  const d = diagram.value;
  const cur = currentRoomId.value;
  if (!d || !cur) return props.currentStatePath || "—";
  return d.roomById.get(cur)?.label ?? props.currentStatePath;
});

const currentPhaseName = computed<string>(() => {
  const d = diagram.value;
  const cur = currentRoomId.value;
  if (!d || !cur) return "";
  const pid = d.phaseByRoom.get(cur);
  return d.phases.find((p) => p.id === pid)?.name ?? "";
});

function roomLabel(roomId: string): string {
  return diagram.value?.roomById.get(roomId)?.label ?? roomId;
}

// Rooms neither on the traveled path, nor the current room, nor a horizon
// target — collapsed behind the "+N elsewhere" chip (routes to full graph).
const elsewhereCount = computed<number>(() => {
  const d = diagram.value;
  if (!d) return 0;
  const shown = new Set<string>();
  if (currentRoomId.value) shown.add(currentRoomId.value);
  for (const e of props.events ?? []) {
    if (e.msg !== "machine.state_entered") continue;
    const rid = matchRoomId(e.state_path, d);
    if (rid) shown.add(rid);
  }
  for (const a of horizonArcs.value) if (a.targetRoomId) shown.add(a.targetRoomId);
  let n = 0;
  for (const r of d.roomById.values()) if (!shown.has(r.id)) n += 1;
  return n;
});

function pillTitle(arc: HorizonArc): string {
  if (arc.targetRoomId === null) return `${arc.label} (no declared transition)`;
  const tier = arc.kind === "self" ? "self-loop" : arc.kind === "exit" ? "exit" : "forward";
  return `${arc.label} → ${roomLabel(arc.targetRoomId)} (${tier})`;
}

// Clicking a horizon pill highlights its target room (existing select emit;
// reuses store.highlightedStatePaths). Dead pills (no target) don't navigate.
function onPillClick(arc: HorizonArc): void {
  if (arc.targetRoomId === null) return;
  emit("select", arc.targetRoomId, { kind: "state", ref: roomLabel(arc.targetRoomId) });
}

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

type EventBoxSubsystem = "agent" | "host" | "machine" | "world" | "user" | "other";

interface PhaseEventBox {
  index: number;
  subsystem: EventBoxSubsystem;
  label: string;
  durationMs?: number;
}

// Canonical agent events: verb lives in attrs.verb, not the msg.
const AGENT_START_MSG_DIAG = "agent.call.start";
const AGENT_COMPLETE_MSG_DIAG = "agent.call.complete";
function agentVerbDiag(e: TraceEvent): string {
  return typeof e.attrs.verb === "string" ? e.attrs.verb : "agent";
}

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

  // agent complete map: call_id → duration_ms
  const agentCompleteDur = new Map<string, number | null>();
  const agentStartCallIds = new Set<string>();
  for (const e of events) {
    if (e.msg === AGENT_COMPLETE_MSG_DIAG) {
      const cid = e.attrs.call_id;
      if (typeof cid === "string") {
        const dur = typeof e.attrs.duration_ms === "number" ? (e.attrs.duration_ms as number) : null;
        agentCompleteDur.set(cid, dur);
      }
    }
    if (e.msg === AGENT_START_MSG_DIAG) {
      const cid = e.attrs.call_id;
      if (typeof cid === "string") agentStartCallIds.add(cid);
    }
  }

  // harness: dispatched + returned are suppressed (absorbed into called)
  const harnessSuppress = new Set<number>();
  const harnessByKey = new Map<string, number[]>();
  for (let i = 0; i < events.length; i++) {
    const e = events[i]!;
    if (!e.msg.startsWith("harness.")) continue;
    const ns = typeof e.attrs.namespace === "string" ? e.attrs.namespace : "";
    if (ns.startsWith("host.agent.")) continue;
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

  // turn.input (UserInputReceived) events: a real event written by the orchestrator
  // at input-receive time, not a synthesized row. Deferred here so user-input boxes
  // appear last in the phase lane (presentation ordering; the event's turn is correct).
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
      if (ns.startsWith("host.agent.")) continue;
      if (harnessSuppress.has(i)) continue;
      if (e.msg !== "harness.called") continue;
      const parts = ns.split(".");
      const label = parts[parts.length - 1] ?? ns;
      box = { index: i, subsystem: "host", label };
    } else if (e.msg === AGENT_COMPLETE_MSG_DIAG) {
      // Only show orphan completes (no paired start in this trace)
      const cid = typeof e.attrs.call_id === "string" ? e.attrs.call_id : null;
      if (cid && agentStartCallIds.has(cid)) continue;
      box = { index: i, subsystem: "agent", label: agentVerbDiag(e) };
    } else if (e.msg === AGENT_START_MSG_DIAG) {
      const verb = agentVerbDiag(e);
      const cid = typeof e.attrs.call_id === "string" ? e.attrs.call_id : null;
      const dur = cid ? agentCompleteDur.get(cid) : null;
      box = { index: i, subsystem: "agent", label: verb, durationMs: dur ?? undefined };
    } else if (e.msg === "machine.say") {
      const text = typeof e.attrs.text === "string" ? e.attrs.text : "";
      const label = "say: " + (text.slice(0, 18) + (text.length > 18 ? "…" : ""));
      box = { index: i, subsystem: "machine", label };
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

  // Append deferred turn.input boxes last in each phase lane (display ordering).
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
  background: var(--k-bg, #0f172a);
  border: 1px solid var(--k-border, #1e293b);
  border-radius: 6px;
  padding: 0.5rem;
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}

/* ========================================================================= */
/* PATH & HORIZON (metro stepper) mode                                       */
/* DOM/CSS only (no SVG text) so labels can't overflow a fixed-size <rect>.  */
/* The three tiers are deliberately distinct: traveled bright/solid, current */
/* amber/solid, road-ahead muted/DASHED (projection must never read as       */
/* traveled — tools/runstatus/CLAUDE.md).                                    */
/* ========================================================================= */
.state-diagram__path {
  display: flex;
  flex-direction: column;
  gap: 0.45rem;
  min-width: 0;
}

/* ---- View switcher tabs ------------------------------------------------- */
.state-diagram__tabs {
  display: flex;
  gap: 0.25rem;
  border-bottom: 1px solid var(--k-border, #1e293b);
  padding-bottom: 0.35rem;
  margin-bottom: 0.1rem;
}
.state-diagram__tab {
  background: transparent;
  border: 1px solid transparent;
  color: var(--k-fg-muted, #64748b);
  font: 600 0.7rem ui-sans-serif, system-ui, sans-serif;
  padding: 0.2rem 0.6rem;
  border-radius: 5px;
  cursor: pointer;
}
.state-diagram__tab:hover {
  color: var(--k-fg, #cbd5e1);
  background: var(--k-bg-hover, #1e293b);
}
.state-diagram__tab--active {
  color: var(--k-fg-accent, #93c5fd);
  background: var(--k-bg-selection, #0c1a36);
  border-color: var(--k-border-focus, #1e3a5f);
}

/* ---- Metro stepper (vertical interchange line) -------------------------- */
.state-diagram__metro {
  display: flex;
  flex-direction: column;
  min-width: 0;
}
.state-diagram__ms-step {
  display: flex;
  gap: 0.6rem;
  align-items: stretch;
  position: relative;
  width: 100%;
  background: none;
  border: none;
  text-align: left;
  font: inherit;
  color: inherit;
  cursor: pointer;
  padding: 0;
}
.state-diagram__ms-rail {
  position: relative;
  flex: 0 0 1.1rem;
  display: flex;
  justify-content: center;
}
/* The connecting line runs from this station's dot down to the next. */
.state-diagram__ms-rail::before {
  content: "";
  position: absolute;
  top: 0.35rem;
  bottom: -0.35rem;
  left: 50%;
  transform: translateX(-50%);
  width: 3px;
  background: var(--k-border, #1e293b);
}
.state-diagram__ms-step:last-child .state-diagram__ms-rail::before {
  display: none;
}
.state-diagram__ms-dot {
  position: relative;
  z-index: 1;
  width: 0.85rem;
  height: 0.85rem;
  border-radius: 50%;
  margin-top: 0.2rem;
  background: var(--k-bg, #0f172a);
  border: 3px solid var(--k-fg-subtle, #334155);
  flex-shrink: 0;
}
/* traveled (TRACE): bright/solid line + green dots */
.state-diagram__ms-step--done .state-diagram__ms-dot {
  background: var(--k-success, #22c55e);
  border-color: var(--k-success, #166534);
}
.state-diagram__ms-step--done .state-diagram__ms-rail::before {
  background: var(--k-success, #166534);
}
/* current (LIVE): amber, larger, glowing */
.state-diagram__ms-dot--current {
  width: 1.2rem;
  height: 1.2rem;
  margin-top: 0;
  background: var(--k-bg-selection, #3a2d0e);
  border-color: var(--k-warning, #f59e0b);
  box-shadow: 0 0 0 5px rgba(245, 158, 11, 0.15);
}
/* road ahead (PROJECTION): dashed/muted — must never read as traveled */
.state-diagram__ms-step--ahead .state-diagram__ms-rail::before {
  background: transparent;
  border-left: 2px dashed var(--k-fg-subtle, #334155);
  width: 0;
}
.state-diagram__ms-dot--ahead {
  background: transparent;
  border-style: dashed;
  border-color: var(--k-fg-subtle, #475569);
}
/* Terminus hint: green but still DASHED/hollow — it's projection (not yet
   reached), so it must not read as a traveled (solid) station. */
.state-diagram__ms-step--terminal .state-diagram__ms-dot--ahead {
  border-color: var(--k-success, #166534);
}
.state-diagram__ms-body {
  display: flex;
  flex-direction: column;
  gap: 0.05rem;
  min-width: 0;
  padding-bottom: 0.65rem;
}
.state-diagram__ms-headline {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  flex-wrap: wrap;
  min-width: 0;
}
.state-diagram__ms-name {
  font-family: ui-monospace, monospace;
  font-size: 0.82rem;
  font-weight: 600;
  color: var(--k-fg, #cbd5e1);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
}
.state-diagram__ms-name--current {
  color: var(--k-warning, #fde68a);
  font-size: 0.9rem;
}
.state-diagram__ms-step--ahead .state-diagram__ms-name {
  color: var(--k-fg-subtle, #64748b);
}
.state-diagram__ms-via {
  font-size: 0.66rem;
  color: var(--k-fg-muted, #64748b);
  font-family: ui-monospace, monospace;
}
.state-diagram__ms-here {
  font-size: 0.66rem;
  color: var(--k-warning, #f59e0b);
  letter-spacing: 0.03em;
}
.state-diagram__ms-pills {
  display: flex;
  flex-wrap: wrap;
  gap: 0.3rem;
  margin-top: 0.35rem;
}
.state-diagram__ms-end {
  color: var(--k-fg-muted, #64748b);
  font-family: ui-monospace, monospace;
  font-size: 0.74rem;
  padding-left: 1.7rem;
}

/* ---- Phase banner pill (declared projection metadata) ------------------- */
.state-diagram__ms-banner {
  font-size: 0.6rem;
  font-weight: 700;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  padding: 0.05rem 0.4rem;
  border-radius: 3px;
  flex-shrink: 0;
}
.state-diagram__ms-banner--done {
  background: var(--k-success-bg, #0c2a1a);
  color: var(--k-success, #86efac);
  border: 1px solid var(--k-success, #166534);
}
.state-diagram__ms-banner--current {
  background: var(--k-bg-selection, #3a2d0e);
  color: var(--k-warning, #fde68a);
  border: 1px solid var(--k-warning, #b45309);
}
.state-diagram__ms-banner--ahead {
  background: transparent;
  color: var(--k-fg-subtle, #64748b);
  border: 1px dashed var(--k-fg-subtle, #334155);
}

/* ---- Path-view breadcrumb ---------------------------------------------- */
.state-diagram__crumbs {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.3rem;
}
.state-diagram__crumb {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  padding: 0.2rem 0.5rem;
  border: 1px solid var(--k-border-subtle, #334155);
  background: var(--k-bg-selection, #0c1a36);
  border-radius: 6px;
  cursor: pointer;
  font: inherit;
  min-width: 0;
}
.state-diagram__crumb-room {
  font-family: ui-monospace, monospace;
  font-size: 0.74rem;
  font-weight: 600;
  color: var(--k-fg, #cbd5e1);
}
.state-diagram__crumb-via {
  font-size: 0.62rem;
  color: var(--k-fg-muted, #64748b);
}
.state-diagram__crumb-arrow {
  color: var(--k-fg-accent, #3b82f6);
  font-size: 0.85rem;
}

/* ---- Ego-graph (SVG node-link) ----------------------------------------- */
.state-diagram__ego {
  padding: 0.5rem 0;
  min-width: 0;
}
/* Cap the width so the 360-unit viewBox isn't stretched across a wide panel
   (which blows the node text up and crowds the edges). Centered. */
.state-diagram__ego svg {
  width: 100%;
  max-width: 560px;
  height: auto;
  display: block;
  margin: 0 auto;
}
.state-diagram__ego-g {
  cursor: pointer;
}
.state-diagram__ego-node-from {
  fill: var(--k-bg-selection, #0c1a36);
  stroke: var(--k-border-subtle, #334155);
}
.state-diagram__ego-node-to {
  fill: var(--k-bg-selection, #0c1a36);
  stroke: var(--k-fg-accent, #3b82f6);
  stroke-width: 1.5;
}
.state-diagram__ego-node-ghost {
  fill: var(--k-bg-deep, #0a1326);
  stroke: var(--k-border-subtle, #334155);
  stroke-dasharray: 4 3;
}
.state-diagram__ego-node-cur {
  fill: var(--k-bg-selection, #2a1e05);
  stroke: var(--k-warning, #f59e0b);
  stroke-width: 2;
}
.state-diagram__ego-nlabel {
  font: 600 11px ui-monospace, monospace;
  fill: var(--k-fg, #cbd5e1);
}
.state-diagram__ego-nlabel-cur {
  font: 700 12px ui-monospace, monospace;
  fill: var(--k-warning, #fde68a);
}
.state-diagram__ego-banner {
  font: 700 8px ui-sans-serif, system-ui, sans-serif;
  letter-spacing: 0.1em;
  fill: var(--k-warning, #f59e0b);
}
.state-diagram__ego-here {
  font: 600 8px ui-sans-serif, system-ui, sans-serif;
  fill: var(--k-warning, #fbbf24);
}
.state-diagram__ego-elabel {
  font: 11px ui-monospace, monospace;
  fill: var(--k-fg-muted, #94a3b8);
}

.state-diagram__toolbar {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}
.state-diagram__path-title {
  font-size: 0.7rem;
  font-weight: 700;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: var(--k-fg-muted, #64748b);
}
.state-diagram__toggle {
  margin-left: auto;
  background: var(--k-bg-widget, #1e293b);
  border: 1px solid var(--k-border-subtle, #334155);
  color: var(--k-fg, #cbd5e1);
  font-size: 0.68rem;
  padding: 0.12rem 0.5rem;
  border-radius: 999px;
  cursor: pointer;
  font-family: inherit;
  white-space: nowrap;
}
.state-diagram__toggle:hover {
  background: var(--k-bg-hover, #2a3b54);
  color: var(--k-fg, #f1f5f9);
}

/* ---- Stations (shared) -------------------------------------------------- */
.state-diagram__station {
  display: inline-flex;
  align-items: center;
  gap: 0.4rem;
  border-radius: 5px;
  font-family: ui-monospace, monospace;
  font-size: 0.78rem;
  cursor: pointer;
  border: 1px solid transparent;
  max-width: 100%;
  min-width: 0;
  text-align: left;
}
.state-diagram__station-label,
.state-diagram__station-room {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
}
.state-diagram__station-dot {
  width: 0.55rem;
  height: 0.55rem;
  border-radius: 999px;
  flex-shrink: 0;
}

/* ---- Traveled leg (TRACE — bright/solid) -------------------------------- */
.state-diagram__traveled {
  display: flex;
  flex-wrap: wrap;
  gap: 0.3rem;
  align-items: center;
}
.state-diagram__station--traveled {
  background: var(--k-success-bg, #0c2a1a);
  border-color: var(--k-success, #166534);
  color: var(--k-success, #86efac);
  padding: 0.2rem 0.5rem;
}
.state-diagram__station--traveled .state-diagram__station-dot {
  background: var(--k-success, #22c55e);
}
.state-diagram__station--traveled:hover {
  background: var(--k-success-bg, #103a23);
}
.state-diagram__journey-start {
  font-size: 0.72rem;
  color: var(--k-fg-subtle, #475569);
  font-family: ui-monospace, monospace;
}

/* ---- Current station (LIVE — amber/solid) ------------------------------- */
.state-diagram__station--current {
  flex-direction: column;
  align-items: stretch;
  background: var(--k-bg-selection, #2a1e05);
  border: 1px solid var(--k-warning, #f59e0b);
  box-shadow: 0 0 0 1px rgba(245, 158, 11, 0.35), 0 0 8px rgba(245, 158, 11, 0.25);
  border-radius: 6px;
  padding: 0.4rem 0.5rem 0.5rem;
  cursor: default;
}
.state-diagram__station-head {
  display: flex;
  align-items: baseline;
  gap: 0.4rem;
  min-width: 0;
}
.state-diagram__station-dot--current {
  background: var(--k-warning, #fbbf24);
  align-self: center;
}
.state-diagram__station-room {
  font-weight: 700;
  color: var(--k-warning, #fde68a);
  font-size: 0.85rem;
}
.state-diagram__station-phase {
  font-size: 0.68rem;
  color: var(--k-warning, #b45309);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
}

/* ---- Horizon pills ------------------------------------------------------ */
.state-diagram__pills {
  display: flex;
  flex-wrap: wrap;
  gap: 0.3rem;
  margin-top: 0.4rem;
}
.state-diagram__pill {
  display: inline-flex;
  align-items: center;
  gap: 0.25rem;
  padding: 0.18rem 0.45rem;
  border-radius: 999px;
  font-size: 0.72rem;
  font-family: ui-monospace, monospace;
  cursor: pointer;
  max-width: 100%;
  min-width: 0;
}
.state-diagram__pill-intent,
.state-diagram__pill-target {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
}
.state-diagram__pill-arrow {
  flex-shrink: 0;
  opacity: 0.7;
}
/* forward = solid bright */
.state-diagram__pill--forward {
  background: var(--k-bg-selection, #1e3a5f);
  border: 1px solid var(--k-fg-accent, #3b82f6);
  color: var(--k-fg, #dbeafe);
}
.state-diagram__pill--forward:hover {
  background: var(--k-bg-hover, #274b78);
}
/* self & exit = outlined / distinct */
.state-diagram__pill--self {
  background: transparent;
  border: 1px dashed var(--k-fg-muted, #64748b);
  color: var(--k-fg, #cbd5e1);
}
.state-diagram__pill--exit {
  background: transparent;
  border: 1px solid var(--k-error, #7f1d1d);
  color: var(--k-error, #fca5a5);
}
.state-diagram__pill--dead {
  opacity: 0.5;
  cursor: not-allowed;
}
.state-diagram__pills-empty {
  font-size: 0.7rem;
  color: var(--k-warning, #92400e);
  font-style: italic;
}

/* ---- Road ahead (PROJECTION — muted/DASHED) ----------------------------- */
.state-diagram__ahead {
  display: flex;
  flex-wrap: wrap;
  gap: 0.3rem;
  align-items: center;
}
.state-diagram__ahead-cap {
  font-size: 0.65rem;
  color: var(--k-fg-subtle, #475569);
  text-transform: uppercase;
  letter-spacing: 0.05em;
  width: 100%;
}
.state-diagram__station--ahead {
  background: transparent;
  border: 1px dashed var(--k-fg-subtle, #334155);
  color: var(--k-fg-subtle, #64748b);
  padding: 0.18rem 0.5rem;
}
.state-diagram__station--ahead .state-diagram__station-dot--ahead {
  background: transparent;
  border: 1px dashed var(--k-fg-subtle, #475569);
}
.state-diagram__station--ahead:hover {
  border-color: var(--k-fg-subtle, #475569);
  color: var(--k-fg-muted, #94a3b8);
}
.state-diagram__branches,
.state-diagram__journey-end {
  font-size: 0.72rem;
  color: var(--k-fg-muted, #64748b);
  font-family: ui-monospace, monospace;
}
.state-diagram__branches {
  cursor: pointer;
  border: 1px dashed var(--k-fg-subtle, #334155);
  border-radius: 5px;
  padding: 0.18rem 0.5rem;
}
.state-diagram__branches:hover {
  color: var(--k-fg-muted, #94a3b8);
  border-color: var(--k-fg-subtle, #475569);
}

/* ---- +N elsewhere chip -------------------------------------------------- */
.state-diagram__elsewhere {
  align-self: flex-start;
  background: var(--k-bg, #0f172a);
  border: 1px solid var(--k-border, #1e293b);
  color: var(--k-fg-muted, #64748b);
  font-size: 0.68rem;
  font-family: ui-monospace, monospace;
  padding: 0.12rem 0.5rem;
  border-radius: 999px;
  cursor: pointer;
}
.state-diagram__elsewhere:hover {
  color: var(--k-fg-muted, #94a3b8);
  border-color: var(--k-border-subtle, #334155);
}

.state-diagram__empty {
  color: var(--k-fg-subtle, #64748b);
  font-size: 0.875rem;
  padding: 1rem;
}

/* ---- Phase card --------------------------------------------------------- */
.state-diagram__phase {
  border: 1px solid var(--k-border, #1e293b);
  border-radius: 6px;
  background: var(--k-bg-deep, #0a1326);
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
  color: var(--k-border-subtle, #334155);
  background: inherit;
  padding: 0 0.2rem;
  pointer-events: none;
  z-index: 1;
}

.state-diagram__phase--exit {
  border-style: dashed;
  opacity: 0.7;
}

.state-diagram__phase--current {
  border-color: var(--k-border-focus, #60a5fa);
  background: #2a1e05;
}

.state-diagram__phase--highlight-target {
  border-color: var(--k-warning, #fbbf24);
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
  color: var(--k-fg-subtle, #475569);
  background: var(--k-bg-widget, #1e293b);
  border-radius: 3px;
  padding: 0.05rem 0.3rem;
  flex-shrink: 0;
  letter-spacing: 0.04em;
}

.state-diagram__phase-name {
  font-weight: 600;
  color: var(--k-fg, #e2e8f0);
  font-size: 0.85rem;
  font-family: ui-monospace, monospace;
}

.state-diagram__phase-desc {
  flex: 1;
  color: var(--k-fg-muted, #64748b);
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
  border: 1px solid var(--k-border-subtle, #334155);
  border-radius: 4px;
  background: var(--k-bg-widget, #1e293b);
  color: var(--k-fg, #cbd5e1);
  cursor: pointer;
  font-size: 0.775rem;
  font-family: ui-monospace, monospace;
  transition: border-color 0.1s, background 0.1s, color 0.1s, box-shadow 0.1s;
}

.state-diagram__room:hover {
  background: var(--k-bg-hover, #2a3b54);
  border-color: var(--k-fg-subtle, #475569);
  color: var(--k-fg, #f1f5f9);
}

.state-diagram__room--current {
  border-color: var(--k-border-focus, #60a5fa);
  background: var(--k-bg-selection, #1e3a5f);
  color: var(--k-fg, #dbeafe);
  box-shadow: 0 0 0 1px rgba(96, 165, 250, 0.5), 0 0 6px rgba(96, 165, 250, 0.4);
}

.state-diagram__room--highlight {
  border-color: var(--k-warning, #fbbf24);
  background: var(--k-bg-selection, #3a2d0e);
  color: var(--k-warning, #fde68a);
}

.state-diagram__room-label {
  font-weight: 500;
}

.state-diagram__room-out {
  font-size: 0.65rem;
  color: var(--k-fg-muted, #94a3b8);
  background: var(--k-bg, #0f172a);
  border-radius: 999px;
  padding: 0.05rem 0.35rem;
  font-family: ui-monospace, monospace;
}

.state-diagram__room--current .state-diagram__room-out {
  background: var(--k-bg-deep, #0a1226);
  color: var(--k-fg-accent, #93c5fd);
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
  scrollbar-color: var(--k-border-subtle, #334155) transparent;
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

.state-diagram__event-box--agent  { background: #7c2d12; color: #fdba74; border-color: #9a3412; }
.state-diagram__event-box--host    { background: #4a1d96; color: #c4b5fd; border-color: #5b21b6; }
.state-diagram__event-box--machine { background: var(--k-success-bg, #14532d); color: var(--k-success, #86efac); border-color: var(--k-success, #166534); }
.state-diagram__event-box--world   { background: #134e4a; color: #5eead4; border-color: #0f766e; }
.state-diagram__event-box--user    { background: #312e81; color: #a5b4fc; border-color: #3730a3; }
.state-diagram__event-box--other   { background: var(--k-bg-widget, #1e293b); color: var(--k-fg-muted, #94a3b8); border-color: var(--k-border-subtle, #334155); }

.state-diagram__event-box--selected {
  box-shadow: 0 0 0 2px var(--k-border-focus, #60a5fa);
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
