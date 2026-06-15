<template>
  <div class="agent-actions" data-testid="agent-actions-drawer">
    <!-- Header: row count, accrued totals, view-mode toggle -->
    <div class="agent-actions__header">
      <span class="agent-actions__count">{{ rows.length }} action{{ rows.length === 1 ? '' : 's' }}</span>
      <span class="agent-actions__format">{{ data.format }}</span>
      <span class="agent-actions__spacer" />
      <span v-if="accrued.tokens" class="agent-actions__accrual" data-testid="agent-actions-accrual">
        Σ in:{{ fmtTokens(accrued.input) }} out:{{ fmtTokens(accrued.output) }}
      </span>
      <span v-if="accrued.costStr" class="agent-actions__accrual agent-actions__accrual--cost">
        {{ accrued.costStr }}
      </span>
      <div class="agent-actions__modes">
        <button
          v-for="m in MODES"
          :key="m"
          class="agent-actions__mode"
          :class="{ 'agent-actions__mode--active': mode === m }"
          :data-testid="'agent-actions-mode-' + m"
          @click="mode = m"
        >{{ m }}</button>
      </div>
    </div>

    <!-- Waterfall -->
    <AgentActionWaterfall
      v-if="mode === 'waterfall'"
      :rows="rows"
      :selected-index="selectedIndex"
      @select="selectRow"
    />

    <!-- Typed rows -->
    <div v-else class="agent-actions__rows">
      <div v-if="rows.length === 0" class="agent-actions__empty">
        No agent actions recorded for this call.
      </div>
      <AgentActionRow
        v-for="(row, i) in rows"
        :key="i"
        :ref="(el: unknown) => registerRow(i, el)"
        :row="row"
      />
    </div>

    <!-- Cassette-vs-live drift diff (degrades honestly under replay). -->
    <TranscriptDiff
      :recorded="data"
      :live="live ?? null"
      :rerunning="rerunning"
      :can-rerun="canRerun"
      @rerun="$emit('rerun')"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from "vue";
import { fmtTokens, fmtCost } from "./lib.js";
import {
  normalizeTranscript,
  type TranscriptData,
} from "../../data/transcript.js";
import AgentActionRow from "./AgentActionRow.vue";
import AgentActionWaterfall from "./AgentActionWaterfall.vue";
import TranscriptDiff from "./TranscriptDiff.vue";

const props = defineProps<{
  /** The fetched (live RPC or snapshot-inlined) transcript for this call. */
  data: TranscriptData;
  /** A fresh live transcript to diff the recorded `data` against, if any. */
  live?: TranscriptData | null;
  rerunning?: boolean;
  canRerun?: boolean;
}>();

defineEmits<{ (e: "rerun"): void }>();

const MODES = ["list", "waterfall"] as const;
type Mode = (typeof MODES)[number];
const mode = ref<Mode>("list");

const rows = computed(() => normalizeTranscript(props.data));

// Running cost/token accrual across the whole arc — sums every row that carries
// usage (each assistant turn's result envelope), so a decide that retried twice
// shows the accumulated spend, not just the terminal total.
const accrued = computed(() => {
  let input = 0;
  let output = 0;
  let cost = 0;
  let tokens = false;
  let hasCost = false;
  for (const r of rows.value) {
    if (r.tokens) {
      tokens = true;
      input += r.tokens.input ?? 0;
      output += r.tokens.output ?? 0;
    }
    if (typeof r.cost === "number") {
      hasCost = true;
      cost += r.cost;
    }
  }
  return {
    tokens,
    input,
    output,
    costStr: hasCost ? fmtCost(cost) : "",
  };
});

// Cross-highlight between the waterfall and the row list.
const selectedIndex = ref<number | null>(null);
const rowEls = new Map<number, HTMLElement>();
function registerRow(i: number, el: unknown): void {
  const node = (el as { $el?: HTMLElement } | null)?.$el;
  if (node) rowEls.set(i, node);
}
function selectRow(i: number): void {
  selectedIndex.value = i;
  mode.value = "list";
}
</script>

<style scoped>
.agent-actions {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}

.agent-actions__header {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  flex-wrap: wrap;
  padding-bottom: 0.3rem;
  border-bottom: 1px solid var(--k-border, #1e293b);
}

.agent-actions__count {
  color: var(--k-fg, #e2e8f0);
  font-size: 0.78rem;
  font-weight: 600;
}

.agent-actions__format {
  color: var(--k-fg-subtle, #475569);
  font-size: 0.68rem;
  font-family: ui-monospace, monospace;
}

.agent-actions__spacer {
  flex: 1;
}

.agent-actions__accrual {
  color: var(--k-fg-muted, #64748b);
  font-size: 0.68rem;
  font-family: ui-monospace, monospace;
  white-space: nowrap;
}

.agent-actions__accrual--cost {
  color: #a3e635;
}

.agent-actions__modes {
  display: flex;
  gap: 0.2rem;
}

.agent-actions__mode {
  background: none;
  border: 1px solid var(--k-border-subtle, #334155);
  color: var(--k-fg-muted, #64748b);
  cursor: pointer;
  font-size: 0.68rem;
  padding: 0.1rem 0.45rem;
  border-radius: 3px;
}

.agent-actions__mode:hover {
  background: var(--k-bg-hover, #1e293b);
  color: var(--k-fg, #e2e8f0);
}

.agent-actions__mode--active {
  background: var(--k-bg-hover, #1e293b);
  border-color: var(--k-border-focus, #3b82f6);
  color: #93c5fd;
}

.agent-actions__rows {
  display: flex;
  flex-direction: column;
  gap: 0.2rem;
}

.agent-actions__empty {
  color: var(--k-fg-subtle, #475569);
  font-size: 0.78rem;
  padding: 0.4rem 0;
}
</style>
