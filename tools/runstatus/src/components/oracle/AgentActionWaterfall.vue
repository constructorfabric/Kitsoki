<template>
  <div class="aaw" data-testid="agent-action-waterfall">
    <div v-if="bars.length === 0" class="aaw__empty">No capture-time offsets recorded.</div>
    <div
      v-for="bar in bars"
      :key="bar.index"
      class="aaw__row"
      :class="{ 'aaw__row--selected': bar.index === selectedIndex }"
      :data-testid="'agent-action-waterfall-row'"
      :title="bar.title"
      @click="$emit('select', bar.index)"
    >
      <div class="aaw__label" :title="bar.title">{{ bar.title }}</div>
      <div class="aaw__track">
        <div
          class="aaw__bar"
          data-testid="agent-action-waterfall-bar"
          :data-duration-ms="bar.durationMs"
          :data-offset-ms="bar.offsetMs"
          :class="{ 'aaw__bar--error': bar.isError }"
          :style="{
            marginLeft: bar.offsetPct + '%',
            width: bar.widthPct + '%',
            background: bar.color,
          }"
        >
          <span class="aaw__bar-label">{{ fmtMs(bar.durationMs) }}</span>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from "vue";
import { fmtMs } from "./lib.js";
import type { NormalizedEvent, NormalizedKind } from "../../data/transcript.js";

const props = defineProps<{
  rows: NormalizedEvent[];
  selectedIndex?: number | null;
}>();

defineEmits<{ (e: "select", index: number): void }>();

// Per-kind colour, harmonised with the trace observation palette
// (lib/observation.ts COLOR_MAP) so the agent drawer reads consistently with the
// run-level waterfall: tool/mcp ~ host-call amber, guardrail ~ decision purple,
// reasoning ~ oracle blue, host-nudge/banner ~ pink boundary, result ~ emerald.
const KIND_COLOR: Record<NormalizedKind, string> = {
  system: "#475569",
  reasoning: "#0ea5e9",
  tool: "#f59e0b",
  mcp: "#f59e0b",
  guardrail: "#7c3aed",
  "host-nudge": "#ec4899",
  banner: "#ec4899",
  result: "#10b981",
};

interface Bar {
  index: number;
  title: string;
  offsetMs: number;
  durationMs: number;
  offsetPct: number;
  widthPct: number;
  color: string;
  isError: boolean;
}

// Build duration-proportional bars from the capture-time offsets. Each row's
// duration is the gap to the NEXT row's offset (the terminal row gets the gap to
// the max offset, or a nominal sliver). offsetPct positions the bar start on the
// shared wall-clock track so "where the time went" reads at a glance.
const bars = computed<Bar[]>(() => {
  const rows = props.rows ?? [];
  if (rows.length === 0) return [];
  const offsets = rows.map((r) => (typeof r.offsetMs === "number" ? r.offsetMs : 0));
  const total = Math.max(...offsets, 1);
  // Degrade honestly: if every offset is 0 (no .timings sidecar) we still render
  // ordered bars, but they carry no proportional width — equal slivers.
  const span = total > 0 ? total : 1;
  return rows.map((r, i) => {
    const offsetMs = offsets[i]!;
    const next = i + 1 < offsets.length ? offsets[i + 1]! : total;
    const durationMs = Math.max(0, next - offsetMs);
    return {
      index: i,
      title: r.title,
      offsetMs,
      durationMs,
      offsetPct: (offsetMs / span) * 100,
      widthPct: Math.max(1, (durationMs / span) * 100),
      color: KIND_COLOR[r.kind] ?? "#475569",
      isError: r.isError === true,
    };
  });
});
</script>

<style scoped>
.aaw {
  display: flex;
  flex-direction: column;
  gap: 0.15rem;
  font-size: 0.75rem;
}

.aaw__empty {
  color: var(--k-fg-subtle, #475569);
  padding: 0.4rem 0;
}

.aaw__row {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.1rem 0.25rem;
  border-radius: 3px;
  cursor: pointer;
}

.aaw__row:hover {
  background: var(--k-bg-hover, #1e293b);
}

.aaw__row--selected {
  background: var(--k-bg-selection, #1e3a5f);
}

.aaw__label {
  flex-shrink: 0;
  width: 160px;
  font-family: ui-monospace, monospace;
  font-size: 0.68rem;
  color: #94a3b8;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.aaw__track {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: center;
  height: 14px;
  background: var(--k-bg-inset, #0a1728);
  border-radius: 2px;
}

.aaw__bar {
  display: flex;
  align-items: center;
  height: 11px;
  border-radius: 2px;
  min-width: 3px;
  opacity: 0.85;
}

.aaw__bar--error {
  outline: 1px solid var(--k-error, #ef4444);
}

.aaw__bar:hover {
  opacity: 1;
}

.aaw__bar-label {
  font-family: ui-monospace, monospace;
  font-size: 0.6rem;
  color: rgba(255, 255, 255, 0.85);
  padding: 0 3px;
  white-space: nowrap;
  overflow: hidden;
}
</style>
