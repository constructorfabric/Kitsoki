<template>
  <div class="surface" data-testid="surface-trace">
    <div v-if="loading" class="surface__loading" data-testid="surface-loading">
      Loading…
    </div>

    <div v-else-if="!sessionId" class="surface__empty" data-testid="surface-empty">
      <p class="surface__empty-msg">Start a chat to begin.</p>
    </div>

    <template v-else>
      <header class="surface__bar">
        <span class="surface__title">Trace</span>
        <code class="surface__state" data-testid="current-state">{{ store.currentStatePath || "—" }}</code>
        <span
          class="surface__badge"
          data-testid="state-badge"
          :data-terminal="store.terminal ? 'true' : 'false'"
          :class="store.terminal ? 'surface__badge--done' : 'surface__badge--live'"
        >{{ store.terminal ? 'done' : 'live' }}</span>
        <span class="surface__count">{{ store.events.length }} events</span>
      </header>

      <div class="surface__body" data-testid="trace-timeline">
        <TraceTimeline
          compact
          :events="store.events"
          :selected-event-index="store.selectedEventIndex"
          :highlighted-state-paths="store.highlightedStatePaths"
          :highlight-tick="store.highlightTick"
          :mermaid-source="store.mermaid?.source ?? null"
          @select="onEventSelect"
        />
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
import TraceTimeline from "../components/TraceTimeline.vue";

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
      if (source) await store.hydrate(source, id);
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
.surface__count {
  margin-left: auto;
  color: var(--k-fg-muted, #64748b);
  font-size: 0.75rem;
}
.surface__body {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
  /* No gutter: the timeline sits flush in the VS Code panel. A padding ring here
     plus the timeline's own border read as a second frame duplicating the native
     panel chrome. */
  padding: 0;
}
.surface__body :deep(.trace-timeline) {
  flex: 1;
  height: 100%;
  min-height: 0;
}
.surface__error {
  padding: 0.5rem 1.1rem;
  font-size: 0.78rem;
  color: var(--k-error, #fca5a5);
}
</style>
