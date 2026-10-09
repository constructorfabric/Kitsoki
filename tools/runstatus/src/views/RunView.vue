<template>
  <div class="run-view">
    <div v-if="store.loading" class="run-view__loading">Loading session…</div>
    <template v-else>
      <!-- Top bar -->
      <div class="run-view__topbar">
        <span class="run-view__breadcrumb" data-testid="breadcrumb">
          <router-link to="/" class="run-view__back">Stories</router-link>
          <span class="run-view__crumb-sep">/</span>
          <span class="run-view__crumb-current">{{ storyTitle }}</span>
        </span>
        <router-link
          v-if="!store.terminal"
          :to="`/s/${sessionId}/chat`"
          class="run-view__drive"
          data-testid="drive-link"
          title="Drive this session — submit turns and choose intents"
        >Drive (chat) ↗</router-link>
        <span class="run-view__session-id">{{ sessionId }}</span>
        <span
          class="run-view__state-badge"
          :class="store.terminal ? 'run-view__state-badge--done' : 'run-view__state-badge--live'"
        >
          {{ store.terminal ? 'done' : 'live' }}
        </span>
        <code class="run-view__current-state">{{ store.currentStatePath }}</code>
        <span
          v-if="store.usageTotals.present"
          class="run-view__usage"
          :title="`${store.usageTotals.calls} agent calls · in ${fmtTokens(store.usageTotals.promptTokens)} / out ${fmtTokens(store.usageTotals.responseTokens)} tokens`"
        >
          Σ {{ fmtTokens(store.usageTotals.promptTokens + store.usageTotals.responseTokens) }} tok<template v-if="fmtCost(store.usageTotals.costUsd)"> · {{ fmtCost(store.usageTotals.costUsd) }}</template>
        </span>
        <span
          v-if="store.harnessProfiles.length"
          class="run-view__harness"
          data-testid="harness-picker"
        >
          <select
            class="run-view__harness-select"
            data-testid="provider-select"
            title="Harness profile (backend/provider) — takes effect next turn"
            :value="store.harnessActiveProfile"
            @change="onProviderChange"
          >
            <option v-for="p in store.harnessProfiles" :key="p.name" :value="p.name">
              {{ p.name }}
            </option>
          </select>
          <select
            v-if="activeModels.length"
            class="run-view__harness-select"
            data-testid="model-select"
            title="Model for the active profile — takes effect next turn"
            :value="activeModel"
            @change="onModelChange"
          >
            <option v-for="m in activeModels" :key="m" :value="m">
              {{ shortModel(m) }}
            </option>
          </select>
          <select
            v-if="activeEfforts.length"
            class="run-view__harness-select"
            data-testid="effort-select"
            title="Reasoning effort — where the model supports it; takes effect next turn"
            :value="activeEffort"
            @change="onEffortChange"
          >
            <option v-for="e in activeEfforts" :key="e" :value="e">effort: {{ e }}</option>
          </select>
        </span>
        <StoryFreshness
          :session-id="sessionId"
          :on-reloaded="onFreshnessReloaded"
          :on-reload-error="onFreshnessError"
          data-testid="story-freshness-widget"
        />
      </div>

      <!-- Reload warning: shown when the current state was removed by the edit,
           mirroring the TUI /reload's "re-render only" notice. -->
      <div
        v-if="reloadWarning"
        class="run-view__reload-warning"
        data-testid="reload-warning"
      >
        {{ reloadWarning }}
      </div>

      <ImprovePrompt
        v-if="store.terminal"
        :session-id="props.sessionId"
      />

      <!-- Main panel: ViewModeTabs (Tree / Timeline / Graph) -->
      <div class="run-view__panels" ref="panelsEl">
        <div class="run-view__panel run-view__panel--tabs">
          <ViewModeTabs
            :events="store.events"
            :selected-event-index="store.selectedEventIndex"
            :highlighted-state-paths="store.highlightedStatePaths"
            :highlight-tick="store.highlightTick"
            :mermaid-source="store.mermaid?.source ?? null"
            :node-map="store.mermaid?.node_map ?? null"
            :current-state-path="store.currentStatePath"
            :session-id="props.sessionId"
            @select-event="onEventSelect"
            @node-select="onNodeSelect"
            @phase-select="onPhaseSelect"
            @clear-highlight="onClearHighlight"
          />
        </div>
      </div>

    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from "vue";
import { useRunStore } from "../stores/run.js";
import { createDataSource } from "../data/source.js";
import { markAutoNavDone } from "../lib/auto-nav.js";
import StoryFreshness from "../components/StoryFreshness.vue";
import ViewModeTabs from "../components/ViewModeTabs.vue";
import ImprovePrompt from "../components/meta/ImprovePrompt.vue";
import { fmtTokens, fmtCost } from "../components/agent/lib.js";
import type { NodeRef } from "../types.js";

const props = defineProps<{ sessionId: string }>();
const store = useRunStore();

// One DataSource for this view, reused by hydrate and the harness picker so a
// profile switch drives the same live session that was hydrated.
const source = createDataSource();

// ── Harness picker ───────────────────────────────────────────────────────────
// Shown only when the session declares harness profiles. The provider dropdown
// switches the backend/profile; the dependent model dropdown lists the active
// profile's catalog. Both take effect next turn (substrate semantics).
const activeProfileObj = computed(() =>
  store.harnessProfiles.find((p) => p.active)
);
const activeModels = computed<string[]>(
  () => activeProfileObj.value?.models ?? []
);
const activeModel = computed<string>(
  () => store.harnessModel || activeProfileObj.value?.model || ""
);
const activeEfforts = computed<string[]>(
  () => activeProfileObj.value?.efforts ?? []
);
const activeEffort = computed<string>(
  () => store.harnessEffort || activeProfileObj.value?.effort || ""
);

async function onProviderChange(e: Event): Promise<void> {
  const profile = (e.target as HTMLSelectElement).value;
  await store.selectProfile(source, props.sessionId, profile);
}

async function onModelChange(e: Event): Promise<void> {
  const model = (e.target as HTMLSelectElement).value;
  await store.selectProfile(source, props.sessionId, store.harnessActiveProfile, model, store.harnessEffort);
}

async function onEffortChange(e: Event): Promise<void> {
  const effort = (e.target as HTMLSelectElement).value;
  await store.selectProfile(source, props.sessionId, store.harnessActiveProfile, store.harnessModel, effort);
}

// hf:Qwen/Qwen2.5-Coder-32B-Instruct → Qwen2.5-Coder-32B-Instruct
function shortModel(m: string): string {
  const slash = m.lastIndexOf("/");
  return slash >= 0 ? m.slice(slash + 1) : m;
}

// Breadcrumb label: the loaded story's title (falls back to its id, then to a
// generic label before the app definition has hydrated).
const storyTitle = computed<string>(
  () => store.appDef?.name || store.appDef?.id || "Session"
);

// ── Staleness / reload ───────────────────────────────────────────────────────
//
// StoryFreshness polls the server every 10 s and shows a diff modal when the
// app.yaml on disk has changed since the session was loaded. After a successful
// reload it calls onFreshnessReloaded so we can show the "state removed" notice
// the TUI /reload surfaces. The LiveSource used by StoryFreshness is constructed
// inside that component; we only need the DataSource for the rehydrate call.
const reloadWarning = ref<string | null>(null);

function onFreshnessReloaded(prevStateExists: boolean): void {
  if (!prevStateExists) {
    reloadWarning.value = "current state removed; staying put";
  } else {
    reloadWarning.value = null;
  }
}

function onFreshnessError(msg: string): void {
  reloadWarning.value = msg;
}

const panelsEl = ref<HTMLElement | null>(null);

onMounted(async () => {
  // Viewing a session spends the per-tab auto-nav convenience (see lib/auto-nav)
  // so a tab that opened straight into an observer view can still reach "/".
  markAutoNavDone();
  await store.hydrate(source, props.sessionId);
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
  background: var(--k-bg-deep, #0a1120);
  color: var(--k-fg, #e2e8f0);
  overflow: hidden;
}

/* ---- Loading ---- */
.run-view__harness {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}
.run-view__harness-select {
  background: #111c33;
  color: #cbd5e1;
  border: 1px solid #2b3a55;
  border-radius: 4px;
  font-size: 12px;
  padding: 2px 4px;
  max-width: 220px;
}
.run-view__harness-select:hover {
  border-color: #3b82f6;
}
.run-view__loading {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 100%;
  color: var(--k-fg-muted, #64748b);
  font-size: 1rem;
}

/* ---- Top bar ---- */
.run-view__topbar {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  padding: 0.5rem 1rem;
  background: var(--k-bg-widget, #0f172a);
  border-bottom: 1px solid var(--k-border, #1e293b);
  flex-shrink: 0;
  font-size: 0.8125rem;
}

.run-view__breadcrumb {
  display: inline-flex;
  align-items: center;
  gap: 0.4rem;
  font-size: 0.8125rem;
}

.run-view__back {
  color: var(--k-fg-accent, #60a5fa);
  text-decoration: none;
}

.run-view__back:hover {
  text-decoration: underline;
}

.run-view__crumb-sep {
  color: var(--k-fg-subtle, #475569);
}

.run-view__crumb-current {
  color: #cbd5e1;
  font-weight: 600;
}

/* The primary next-action from the read-only observer: jump to the chat surface
   to actually drive the live session. Styled as an accent pill so it reads as a
   call-to-action, not just another breadcrumb. */
.run-view__drive {
  color: var(--k-fg-accent, #93c5fd);
  background: rgba(59, 130, 246, 0.12);
  border: 1px solid rgba(59, 130, 246, 0.4);
  border-radius: 4px;
  padding: 0.1rem 0.5rem;
  font-size: 0.75rem;
  font-weight: 600;
  text-decoration: none;
}

.run-view__drive:hover {
  background: rgba(59, 130, 246, 0.22);
  border-color: var(--k-fg-accent, #60a5fa);
}


.run-view__reload-warning {
  flex-shrink: 0;
  padding: 0.35rem 1rem;
  background: #3a2d0e;
  border-bottom: 1px solid var(--k-warning, #fbbf24);
  color: #fde68a;
  font-size: 0.775rem;
}

.run-view__session-id {
  color: var(--k-fg-muted, #94a3b8);
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
  background: var(--k-success-bg, #14532d);
  color: var(--k-success, #86efac);
}

.run-view__state-badge--done {
  background: var(--k-bg-input, #1e293b);
  color: var(--k-fg-muted, #64748b);
}

.run-view__current-state {
  font-family: ui-monospace, monospace;
  font-size: 0.775rem;
  color: var(--k-fg-code, #7dd3fc);
}

.run-view__usage {
  margin-left: auto;
  font-family: ui-monospace, monospace;
  font-size: 0.75rem;
  color: #a3e635;
  background: #1a2e05;
  border: 1px solid #3f6212;
  border-radius: 4px;
  padding: 0.1rem 0.45rem;
  white-space: nowrap;
}

/* ---- Panels ---- */
.run-view__panels {
  display: flex;
  flex: 1;
  padding: 0.5rem;
  overflow: hidden;
  gap: 0;
}

.run-view__panel {
  display: flex;
  flex-direction: column;
  overflow: hidden;
  border-radius: 6px;
  flex-shrink: 0;
  flex-grow: 0;
  min-width: 0;
}

.run-view__panel--tabs {
  flex: 1;
  min-width: 0;
}

/* ViewModeTabs takes the full height */
.run-view__panel--tabs :deep(.view-mode-tabs) {
  flex: 1;
  height: 100%;
  min-height: 0;
}

.run-view__empty {
  color: var(--k-fg-subtle, #475569);
  font-size: 0.875rem;
  padding: 1rem;
}
</style>
