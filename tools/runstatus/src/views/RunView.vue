<template>
  <div class="run-view">
    <div v-if="store.loading" class="run-view__loading">Loading session…</div>
    <template v-else>
      <!-- Top bar -->
      <div class="run-view__topbar">
        <router-link to="/" class="run-view__back">← Sessions</router-link>
        <span class="run-view__session-id">{{ sessionId }}</span>
        <span
          class="run-view__state-badge"
          :class="store.terminal ? 'run-view__state-badge--done' : 'run-view__state-badge--live'"
        >
          {{ store.terminal ? 'done' : 'live' }}
        </span>
        <code class="run-view__current-state">{{ store.currentStatePath }}</code>
      </div>

      <!-- Detail Drawer (teleports to body) -->
      <DetailDrawer
        v-if="store.appDef"
        :selected-node="store.selectedNode"
        :selected-event="store.selectedEvent"
        :app-def="store.appDef"
        @close="store.clearSelection()"
      />

      <!-- Main panels -->
      <div class="run-view__panels">
        <!-- Diagram panel -->
        <div class="run-view__panel run-view__panel--diagram">
          <div class="run-view__panel-header">State Diagram</div>
          <StateDiagram
            v-if="store.mermaid"
            :mermaid-source="store.mermaid.source"
            :node-map="store.mermaid.node_map"
            :current-state-path="store.currentStatePath"
            :highlighted-state-paths="store.highlightedStatePaths"
            @select="onNodeSelect"
            @select-phase="onPhaseSelect"
          />
          <div v-else class="run-view__empty">No diagram.</div>
        </div>

        <!-- Timeline panel -->
        <div class="run-view__panel run-view__panel--timeline">
          <div class="run-view__panel-header">
            <span>Trace</span>
            <button
              v-if="store.highlightedStatePaths.length > 0"
              class="run-view__clear-highlight"
              @click="onClearHighlight"
              :title="'Clear diagram highlight'"
            >clear highlight ({{ store.highlightedStatePaths.length }})</button>
          </div>
          <TraceTimeline
            :events="store.events"
            :selected-event-index="store.selectedEventIndex"
            :highlighted-state-paths="store.highlightedStatePaths"
            :highlight-tick="store.highlightTick"
            :mermaid-source="store.mermaid?.source ?? null"
            @select="onEventSelect"
          />
        </div>
      </div>

    </template>
  </div>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted } from "vue";
import { useRunStore } from "../stores/run.js";
import { createDataSource } from "../data/source.js";
import StateDiagram from "../components/StateDiagram.vue";
import TraceTimeline from "../components/TraceTimeline.vue";
import DetailDrawer from "../components/DetailDrawer.vue";
import type { NodeRef } from "../types.js";

const props = defineProps<{ sessionId: string }>();
const store = useRunStore();

onMounted(async () => {
  await store.hydrate(createDataSource(), props.sessionId);
});

onUnmounted(() => {
  store.teardown();
});

function onNodeSelect(_nodeId: string, nodeRef: NodeRef): void {
  // Diagram clicks drive the highlight set only — we intentionally do NOT
  // open the DetailDrawer here, because its backdrop would intercept the
  // next click in the diagram or timeline.  The drawer is still reachable
  // by clicking a trace row.
  if (nodeRef.kind === "state") {
    store.setHighlightedStatePaths([nodeRef.ref]);
  }
}

function onPhaseSelect(_phaseId: string, roomRefs: string[]): void {
  store.setHighlightedStatePaths(roomRefs);
}

function onClearHighlight(): void {
  store.setHighlightedStatePaths([]);
}

function onEventSelect(index: number): void {
  store.selectEvent(index);
}
</script>

<style scoped>
.run-view {
  display: flex;
  flex-direction: column;
  height: 100vh;
  background: #0a1120;
  color: #e2e8f0;
  overflow: hidden;
}

/* ---- Loading ---- */
.run-view__loading {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 100%;
  color: #64748b;
  font-size: 1rem;
}

/* ---- Top bar ---- */
.run-view__topbar {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  padding: 0.5rem 1rem;
  background: #0f172a;
  border-bottom: 1px solid #1e293b;
  flex-shrink: 0;
  font-size: 0.8125rem;
}

.run-view__back {
  color: #60a5fa;
  text-decoration: none;
}

.run-view__back:hover {
  text-decoration: underline;
}

.run-view__session-id {
  color: #94a3b8;
  font-family: ui-monospace, monospace;
  font-size: 0.775rem;
}

.run-view__state-badge {
  display: inline-block;
  padding: 0.1rem 0.4rem;
  border-radius: 999px;
  font-size: 0.7rem;
  font-weight: 600;
}

.run-view__state-badge--live {
  background: #14532d;
  color: #86efac;
}

.run-view__state-badge--done {
  background: #1e293b;
  color: #64748b;
}

.run-view__current-state {
  font-family: ui-monospace, monospace;
  font-size: 0.775rem;
  color: #7dd3fc;
}

/* ---- Panels ---- */
.run-view__panels {
  display: flex;
  flex: 1;
  gap: 0.5rem;
  padding: 0.5rem;
  overflow: hidden;
}

.run-view__panel {
  display: flex;
  flex-direction: column;
  overflow: hidden;
  border-radius: 6px;
}

.run-view__panel--diagram {
  flex: 1;
  min-width: 0;
}

.run-view__panel--timeline {
  flex: 1;
  min-width: 0;
}

.run-view__panel-header {
  font-size: 0.75rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: #64748b;
  padding: 0.25rem 0;
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 0.5rem;
}

.run-view__clear-highlight {
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

.run-view__clear-highlight:hover {
  background: #4a3a14;
}

/* StateDiagram takes the remaining height */
.run-view__panel--diagram :deep(.state-diagram) {
  flex: 1;
  height: 100%;
}

/* TraceTimeline takes the remaining height */
.run-view__panel--timeline :deep(.trace-timeline) {
  flex: 1;
  height: 100%;
  min-height: 0;
}

.run-view__empty {
  color: #475569;
  font-size: 0.875rem;
  padding: 1rem;
}
</style>
