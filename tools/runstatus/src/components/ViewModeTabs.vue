<template>
  <div class="view-mode-tabs" data-testid="view-mode-tabs">
    <!-- Tab bar -->
    <div class="view-mode-tabs__bar">
      <button
        class="view-mode-tabs__tab"
        :class="{ 'view-mode-tabs__tab--active': activeMode === 'tree' }"
        data-testid="tab-tree"
        @click="setMode('tree')"
      >Tree</button>
      <button
        class="view-mode-tabs__tab"
        :class="{ 'view-mode-tabs__tab--active': activeMode === 'timeline' }"
        data-testid="tab-timeline"
        @click="setMode('timeline')"
      >Timeline</button>
      <button
        class="view-mode-tabs__tab"
        :class="{ 'view-mode-tabs__tab--active': activeMode === 'graph' }"
        data-testid="tab-graph"
        @click="setMode('graph')"
      >Graph</button>
      <button
        class="view-mode-tabs__tab"
        :class="{ 'view-mode-tabs__tab--active': activeMode === 'actions' }"
        data-testid="tab-actions"
        @click="setMode('actions')"
      >Actions</button>

      <!-- Clear highlight button shown in trace panel header area -->
      <button
        v-if="highlightedStatePaths.length > 0"
        class="view-mode-tabs__clear-highlight run-view__clear-highlight"
        data-testid="clear-highlight"
        @click="emit('clear-highlight')"
        :title="'Clear diagram highlight'"
      >clear highlight ({{ highlightedStatePaths.length }})</button>
    </div>

    <!-- Tab content -->
    <div class="view-mode-tabs__content">
      <!-- Tree: TraceTimeline -->
      <div v-show="activeMode === 'tree'" class="view-mode-tabs__pane">
        <TraceTimeline
          :events="events"
          :selected-event-index="selectedEventIndex"
          :highlighted-state-paths="highlightedStatePaths"
          :highlight-tick="highlightTick"
          :mermaid-source="mermaidSource"
          :session-id="sessionId"
          @select="(idx: number) => emit('select-event', idx)"
        />
      </div>

      <!-- Timeline: TraceWaterfall -->
      <div v-show="activeMode === 'timeline'" class="view-mode-tabs__pane">
        <TraceWaterfall
          :events="events"
          :selected-event-index="selectedEventIndex"
          @select-event="(idx: number) => emit('select-event', idx)"
        />
      </div>

      <!-- Graph: StateDiagram -->
      <div v-show="activeMode === 'graph'" class="view-mode-tabs__pane">
        <StateDiagram
          v-if="mermaidSource && nodeMap"
          :mermaid-source="mermaidSource"
          :node-map="nodeMap"
          :current-state-path="currentStatePath"
          :highlighted-state-paths="highlightedStatePaths"
          :events="events"
          :selected-event-index="selectedEventIndex"
          @select="(nodeId: string, nodeRef: NodeRef) => emit('node-select', nodeId, nodeRef)"
          @select-phase="(phaseId: string, roomRefs: string[]) => emit('phase-select', phaseId, roomRefs)"
          @select-event="(idx: number) => emit('select-event', idx)"
        />
        <div v-else class="view-mode-tabs__empty">No diagram available.</div>
      </div>

      <!-- Actions: SessionRollup — all agent transcripts across the run -->
      <div v-show="activeMode === 'actions'" class="view-mode-tabs__pane">
        <SessionRollup :events="events" :session-id="sessionId" />
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted } from "vue";
import TraceTimeline from "./TraceTimeline.vue";
import TraceWaterfall from "./TraceWaterfall.vue";
import StateDiagram from "./StateDiagram.vue";
import SessionRollup from "./oracle/SessionRollup.vue";
import type { TraceEvent, NodeRef } from "../types.js";

type ViewMode = "tree" | "timeline" | "graph" | "actions";

const props = defineProps<{
  events: TraceEvent[];
  selectedEventIndex: number | null;
  highlightedStatePaths: string[];
  highlightTick: number;
  mermaidSource: string | null;
  nodeMap: Record<string, NodeRef> | null;
  currentStatePath: string;
  /** Session ID threaded to EventDetail so the annotate/replay buttons appear. */
  sessionId?: string;
}>();

const emit = defineEmits<{
  (e: "select-event", index: number): void;
  (e: "node-select", nodeId: string, nodeRef: NodeRef): void;
  (e: "phase-select", phaseId: string, roomRefs: string[]): void;
  (e: "clear-highlight"): void;
}>();

function readHashMode(): ViewMode {
  const hash = window.location.hash;
  const m = hash.match(/#(?:.*#)?(tree|timeline|graph|actions)/);
  if (m) return m[1] as ViewMode;
  return "tree";
}

const activeMode = ref<ViewMode>(readHashMode());

function setMode(mode: ViewMode): void {
  activeMode.value = mode;
  // Preserve the existing hash path prefix (router uses hash history like #/s/xxx)
  // Append the view mode as a secondary fragment after the route hash.
  // We store mode in the URL hash as a query-like suffix: #<route>#<mode>
  const currentHash = window.location.hash;
  // Strip any existing mode suffix
  const baseHash = currentHash.replace(/#(tree|timeline|graph|actions)$/, "");
  window.location.hash = baseHash + "#" + mode;
}

// Sync if browser back/forward changes hash
function onHashChange(): void {
  activeMode.value = readHashMode();
}

onMounted(() => {
  window.addEventListener("hashchange", onHashChange);
});

onUnmounted(() => {
  window.removeEventListener("hashchange", onHashChange);
});
</script>

<style scoped>
.view-mode-tabs {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
  overflow: hidden;
}

.view-mode-tabs__bar {
  display: flex;
  align-items: center;
  gap: 0.25rem;
  padding: 0.25rem 0;
  border-bottom: 1px solid #1e293b;
  flex-shrink: 0;
}

.view-mode-tabs__tab {
  background: transparent;
  border: 1px solid transparent;
  color: #64748b;
  border-radius: 4px;
  padding: 0.2rem 0.7rem;
  font-size: 0.75rem;
  font-weight: 600;
  font-family: inherit;
  cursor: pointer;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  transition: color 0.1s, border-color 0.1s, background 0.1s;
}

.view-mode-tabs__tab:hover {
  color: #94a3b8;
  background: #1e293b;
}

.view-mode-tabs__tab--active {
  color: #60a5fa;
  border-color: #1d4ed8;
  background: rgba(29, 78, 216, 0.12);
}

.view-mode-tabs__clear-highlight {
  margin-left: auto;
  background: #3a2d0e;
  border: 1px solid #fbbf24;
  color: #fde68a;
  font-size: 0.65rem;
  text-transform: none;
  letter-spacing: normal;
  padding: 0.1rem 0.4rem;
  border-radius: 999px;
  cursor: pointer;
  font-family: inherit;
}

.view-mode-tabs__clear-highlight:hover {
  background: #4a3a14;
}

.view-mode-tabs__content {
  flex: 1;
  min-height: 0;
  overflow: hidden;
  position: relative;
}

.view-mode-tabs__pane {
  height: 100%;
  overflow: hidden;
  display: flex;
  flex-direction: column;
}

/* TraceTimeline + TraceWaterfall need full height */
.view-mode-tabs__pane :deep(.trace-timeline),
.view-mode-tabs__pane :deep(.trace-waterfall),
.view-mode-tabs__pane :deep(.rollup),
.view-mode-tabs__pane :deep(.state-diagram) {
  flex: 1;
  height: 100%;
  min-height: 0;
}

.view-mode-tabs__empty {
  color: #475569;
  font-size: 0.875rem;
  padding: 1rem;
}
</style>
