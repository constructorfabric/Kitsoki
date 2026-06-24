<template>
  <div class="surface" data-testid="surface-graph">
    <div v-if="loading" class="surface__loading" data-testid="surface-loading">
      Loading…
    </div>

    <div v-else-if="!sessionId" class="surface__empty" data-testid="surface-empty">
      <p class="surface__empty-msg">Start a chat to begin.</p>
    </div>

    <template v-else>
      <header class="surface__bar">
        <span class="surface__title">State Diagram</span>
        <code class="surface__state" data-testid="current-state">{{ store.currentStatePath || "—" }}</code>
        <span
          class="surface__badge"
          data-testid="state-badge"
          :data-terminal="store.terminal ? 'true' : 'false'"
          :class="store.terminal ? 'surface__badge--done' : 'surface__badge--live'"
        >{{ store.terminal ? 'done' : 'live' }}</span>
      </header>

      <div class="surface__body" data-testid="trace-diagram">
        <StateDiagram
          v-if="store.mermaid"
          :mermaid-source="store.mermaid.source"
          :node-map="store.mermaid.node_map"
          :current-state-path="store.currentStatePath"
          :highlighted-state-paths="store.highlightedStatePaths"
          :events="store.events"
          :selected-event-index="store.selectedEventIndex"
          :intents="store.currentView?.intents ?? []"
          @select="onNodeSelect"
          @select-phase="onPhaseSelect"
          @select-event="onEventSelect"
        />
        <div v-else class="surface__no-diagram">No diagram.</div>
      </div>

      <div v-if="error" class="surface__error" data-testid="surface-error">{{ error }}</div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, ref } from "vue";
import { useRunStore } from "../stores/run.js";
import { createDataSource } from "../data/source.js";
import type { DataSource } from "../data/source.js";
import StateDiagram from "../components/StateDiagram.vue";
import type { NodeRef } from "../types.js";

const store = useRunStore();

let source: DataSource | null = null;
let unsubscribe: (() => void) | null = null;

const sessionId = ref<string | null>(null);
const loading = ref(true);
const error = ref<string | null>(null);

async function adopt(id: string | null): Promise<void> {
  sessionId.value = id;
  if (id) {
    loading.value = true;
    try {
      if (source) {
        await store.hydrate(source, id);
        // Also read the current room's view (no advance) so the diagram knows the
        // active room's moves/intents — otherwise it renders "no moves available",
        // inconsistent with the in-chat graph that has the live view loaded.
        await store.loadInitialView(source, id);
      }
    } catch (e) {
      error.value = errMsg(e);
    } finally {
      loading.value = false;
    }
  } else {
    // No active session: clear the initial loading flag so the empty state renders.
    // Without this, `loading` (true at init) is never lowered when current-session
    // discovery returns null, leaving the surface stuck on "Loading…" indefinitely.
    store.teardown();
    loading.value = false;
  }
}

onMounted(async () => {
  source = createDataSource();
  try {
    await adopt(await source.getCurrentSession());
  } catch (e) {
    error.value = errMsg(e);
    loading.value = false;
  }
  unsubscribe = source.subscribeCurrentSession((id) => {
    void adopt(id);
  });
});

onUnmounted(() => {
  unsubscribe?.();
  store.teardown();
});

function onNodeSelect(_nodeId: string, nodeRef: NodeRef): void {
  if (nodeRef.kind === "state") {
    store.setHighlightedStatePaths([nodeRef.ref]);
  }
}
function onPhaseSelect(_phaseId: string, roomRefs: string[]): void {
  store.setHighlightedStatePaths(roomRefs);
}
function onEventSelect(index: number): void {
  store.selectEvent(index);
}

function errMsg(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}
</script>

<style scoped>
.surface {
  display: flex;
  flex-direction: column;
  height: 100vh;
  background: var(--k-bg-deep, #0a1120);
  color: var(--k-fg, #e2e8f0);
  overflow: hidden;
}
.surface__loading,
.surface__empty {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 100%;
  color: var(--k-fg-muted, #64748b);
  font-size: 0.95rem;
}
.surface__empty-msg {
  margin: 0;
}
.surface__bar {
  display: flex;
  align-items: center;
  gap: 0.6rem;
  padding: 0.5rem 1rem;
  background: var(--k-bg-widget, #0f172a);
  border-bottom: 1px solid var(--k-border, #1e293b);
  flex-shrink: 0;
  font-size: 0.8125rem;
}
.surface__title {
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  font-size: 0.72rem;
  color: var(--k-fg-muted, #94a3b8);
}
.surface__state {
  font-family: ui-monospace, monospace;
  font-size: 0.775rem;
  color: var(--k-fg-accent, #7dd3fc);
}
.surface__badge {
  display: inline-block;
  padding: 0.1rem 0.45rem;
  border-radius: 999px;
  font-size: 0.7rem;
  font-weight: 600;
}
.surface__badge--live {
  background: var(--k-success-bg, #14532d);
  color: var(--k-success, #86efac);
}
.surface__badge--done {
  background: var(--k-bg-input, #1e293b);
  color: var(--k-fg-muted, #64748b);
}
.surface__body {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
  padding: 0.5rem;
}
.surface__body :deep(.state-diagram) {
  flex: 1;
  height: 100%;
}
.surface__no-diagram {
  color: var(--k-fg-subtle, #475569);
  font-size: 0.875rem;
  padding: 1rem;
}
.surface__error {
  padding: 0.5rem 1.1rem;
  font-size: 0.78rem;
  color: var(--k-error, #fca5a5);
}
</style>
