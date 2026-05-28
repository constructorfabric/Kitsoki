<template>
  <div class="wdv">
    <div class="wdv__tabs">
      <button class="wdv__tab" :class="{ active: tab === 'before' }" @click="tab = 'before'">Before</button>
      <button class="wdv__tab" :class="{ active: tab === 'diff' }" @click="tab = 'diff'">
        Diff <span class="wdv__tab-count">{{ diffKeys.length }}</span>
      </button>
      <button class="wdv__tab" :class="{ active: tab === 'after' }" @click="tab = 'after'">After</button>
    </div>

    <div class="wdv__body">
      <!-- Before: full world state at this point in time -->
      <template v-if="tab === 'before'">
        <div v-if="Object.keys(props.before).length === 0" class="wdv__empty">World was empty before this update</div>
        <div v-else class="wdv__kv-list">
          <div
            v-for="k in Object.keys(props.before).sort()"
            :key="k"
            class="wdv__kv"
            :class="diffKeys.includes(k) ? 'wdv__kv--changed' : 'wdv__kv--neutral'"
          >
            <span class="wdv__kv-gutter">{{ diffKeys.includes(k) ? '~' : ' ' }}</span>
            <span class="wdv__kv-key">{{ k }}</span>
            <span class="wdv__kv-sep">:</span>
            <div class="wdv__kv-val"><JsonViewer :value="props.before[k]" :defaultOpen="true" /></div>
          </div>
        </div>
      </template>

      <!-- Diff: every changed key, colour-coded -->
      <template v-else-if="tab === 'diff'">
        <div v-if="diffKeys.length === 0" class="wdv__empty">No changes</div>
        <div v-else class="wdv__kv-list">
          <div
            v-for="k in diffKeys"
            :key="k"
            class="wdv__kv"
            :class="keyClass(k)"
          >
            <span class="wdv__kv-gutter">{{ keyGutter(k) }}</span>
            <span class="wdv__kv-key">{{ k }}</span>
            <span class="wdv__kv-sep">:</span>
            <div class="wdv__kv-val">
              <template v-if="keyStatus(k) === 'added'">
                <JsonViewer :value="props.after[k]" :defaultOpen="true" />
              </template>
              <template v-else-if="keyStatus(k) === 'removed'">
                <JsonViewer :value="props.before[k]" :defaultOpen="true" />
              </template>
              <template v-else>
                <div class="wdv__changed">
                  <div class="wdv__changed-row wdv__changed-row--old">
                    <span class="wdv__changed-label">was</span>
                    <JsonViewer :value="props.before[k]" :defaultOpen="true" />
                  </div>
                  <div class="wdv__changed-row wdv__changed-row--new">
                    <span class="wdv__changed-label">now</span>
                    <JsonViewer :value="props.after[k]" :defaultOpen="true" />
                  </div>
                </div>
              </template>
            </div>
          </div>
        </div>
      </template>

      <!-- After: full world state as a plain object view -->
      <template v-else>
        <div v-if="Object.keys(props.after).length === 0" class="wdv__empty">World is empty after this update</div>
        <JsonViewer v-else :value="props.after" :defaultOpen="true" />
      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from "vue";
import JsonViewer from "./oracle/JsonViewer.vue";

const props = defineProps<{
  before: Record<string, unknown>;
  after: Record<string, unknown>;
}>();

const tab = ref<"before" | "diff" | "after">("diff");

function deepEqual(a: unknown, b: unknown): boolean {
  return JSON.stringify(a) === JSON.stringify(b);
}

const diffKeys = computed<string[]>(() => {
  const all = new Set([...Object.keys(props.before), ...Object.keys(props.after)]);
  return [...all].filter((k) => !deepEqual(props.before[k], props.after[k]));
});

type KeyStatus = "added" | "removed" | "changed";

function keyStatus(k: string): KeyStatus {
  if (!(k in props.before)) return "added";
  if (!(k in props.after))  return "removed";
  return "changed";
}

function keyClass(k: string): string {
  switch (keyStatus(k)) {
    case "added":   return "wdv__kv--added";
    case "removed": return "wdv__kv--removed";
    case "changed": return "wdv__kv--changed";
  }
}

function keyGutter(k: string): string {
  switch (keyStatus(k)) {
    case "added":   return "+";
    case "removed": return "−";
    case "changed": return "~";
  }
}
</script>

<style scoped>
.wdv {
  font-family: ui-monospace, monospace;
  font-size: 0.72rem;
}

/* --- Tabs --- */
.wdv__tabs {
  display: flex;
  gap: 0;
  border-bottom: 1px solid #1e293b;
  margin-bottom: 0.4rem;
}

.wdv__tab {
  padding: 0.2rem 0.7rem;
  background: none;
  border: none;
  border-bottom: 2px solid transparent;
  color: #64748b;
  cursor: pointer;
  font-size: 0.72rem;
  font-family: inherit;
  transition: color 0.1s, border-color 0.1s;
}

.wdv__tab:hover { color: #94a3b8; }
.wdv__tab.active {
  color: #e2e8f0;
  border-bottom-color: #3b82f6;
}

.wdv__tab-count {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  background: #1e3a5f;
  color: #93c5fd;
  border-radius: 999px;
  font-size: 0.65rem;
  min-width: 1.2em;
  padding: 0 0.25em;
  margin-left: 0.25em;
}

/* --- Body --- */
.wdv__body {
  padding: 0.1rem 0;
}

.wdv__empty {
  color: #475569;
  font-style: italic;
  padding: 0.3rem 0;
}

/* --- KV list --- */
.wdv__kv-list {
  display: flex;
  flex-direction: column;
  gap: 0.15rem;
}

.wdv__kv {
  display: flex;
  align-items: baseline;
  gap: 0.3rem;
  padding: 0.15rem 0.4rem;
  border-radius: 3px;
  border-left: 2px solid transparent;
}

.wdv__kv--added {
  background: #052e16;
  border-left-color: #22c55e;
}

.wdv__kv--removed {
  background: #2d0707;
  border-left-color: #ef4444;
}

.wdv__kv--changed {
  background: #1a1200;
  border-left-color: #f59e0b;
}

.wdv__kv--neutral {
  background: transparent;
  border-left-color: #1e293b;
}

.wdv__kv-gutter {
  width: 0.8rem;
  flex-shrink: 0;
  text-align: center;
  user-select: none;
  font-weight: 700;
}

.wdv__kv--added   .wdv__kv-gutter { color: #4ade80; }
.wdv__kv--removed .wdv__kv-gutter { color: #f87171; }
.wdv__kv--changed .wdv__kv-gutter { color: #fbbf24; }

.wdv__kv-key {
  color: #7dd3fc;
  flex-shrink: 0;
}

.wdv__kv-sep {
  color: #475569;
  flex-shrink: 0;
}

.wdv__kv-val {
  flex: 1;
  min-width: 0;
}

/* --- Changed (before+after rows) --- */
.wdv__changed {
  display: flex;
  flex-direction: column;
  gap: 0.1rem;
}

.wdv__changed-row {
  display: flex;
  align-items: baseline;
  gap: 0.4rem;
}

.wdv__changed-label {
  font-size: 0.65rem;
  font-weight: 600;
  min-width: 1.8rem;
  text-align: right;
  flex-shrink: 0;
}

.wdv__changed-row--old .wdv__changed-label { color: #f87171; }
.wdv__changed-row--new .wdv__changed-label { color: #4ade80; }

.wdv__changed-row--old { opacity: 0.8; }
</style>
