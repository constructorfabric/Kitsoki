<template>
  <div class="trace-waterfall">
    <div v-if="turnGroups.length === 0" class="trace-waterfall__empty">
      No timed events to display.
    </div>
    <div
      v-for="group in turnGroups"
      :key="group.turn"
      class="trace-waterfall__turn"
    >
      <div class="trace-waterfall__turn-header">Turn {{ group.turn }}</div>
      <div class="trace-waterfall__rows">
        <div
          v-for="row in group.rows"
          :key="row.index"
          class="trace-waterfall__row"
          :class="{ 'trace-waterfall__row--selected': row.index === selectedEventIndex }"
          @click="emit('select-event', row.index)"
        >
          <div class="trace-waterfall__label" :title="row.msg">{{ row.msg }}</div>
          <div class="trace-waterfall__bar-container">
            <div
              v-if="row.durationMs != null"
              class="trace-waterfall__bar"
              :data-testid="'waterfall-bar'"
              :data-duration-ms="row.durationMs"
              :style="{
                width: barWidth(row.durationMs) + '%',
                background: row.color,
              }"
            >
              <span class="trace-waterfall__bar-label">{{ fmtMs(row.durationMs) }}</span>
            </div>
            <div
              v-else
              class="trace-waterfall__tick"
              :style="{ borderColor: row.color }"
              :title="row.msg"
            />
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from "vue";
import type { TraceEvent } from "../types.js";
import { observationKind, COLOR_MAP } from "../lib/observation.js";
import { fmtMs } from "./agent/lib.js";

const props = defineProps<{
  events: TraceEvent[];
  selectedEventIndex: number | null;
}>();

const emit = defineEmits<{
  (e: "select-event", index: number): void;
}>();

interface WaterfallRow {
  index: number;
  msg: string;
  durationMs: number | null;
  color: string;
}

interface TurnGroup {
  turn: number;
  rows: WaterfallRow[];
}

function getDurationMs(event: TraceEvent): number | null {
  const a = event.attrs;
  if (!a) return null;
  // agent.call.complete: duration_ms on complete attrs
  if (typeof a.duration_ms === "number") return a.duration_ms;
  // harness calls: compute from dispatched→returned timestamps if present
  if (typeof a.dispatched_at === "string" && typeof a.returned_at === "string") {
    const d = new Date(a.dispatched_at as string).getTime();
    const r = new Date(a.returned_at as string).getTime();
    if (!isNaN(d) && !isNaN(r) && r >= d) return r - d;
  }
  return null;
}

// Compute rows grouped by turn, including ALL events (non-timed get tick marks)
const turnGroups = computed<TurnGroup[]>(() => {
  const groups = new Map<number, WaterfallRow[]>();

  props.events.forEach((event, index) => {
    const turn = event.turn ?? 0;
    if (!groups.has(turn)) groups.set(turn, []);
    const durationMs = getDurationMs(event);
    const kind = observationKind(event.msg);
    groups.get(turn)!.push({
      index,
      msg: event.msg,
      durationMs,
      color: COLOR_MAP[kind],
    });
  });

  // Sort by turn number
  const sorted = Array.from(groups.entries())
    .sort(([a], [b]) => a - b)
    .map(([turn, rows]) => ({ turn, rows }));

  return sorted;
});

// Max duration_ms across ALL rows, for bar width scaling
const maxDurationMs = computed<number>(() => {
  let max = 0;
  for (const group of turnGroups.value) {
    for (const row of group.rows) {
      if (row.durationMs != null && row.durationMs > max) {
        max = row.durationMs;
      }
    }
  }
  return max;
});

function barWidth(durationMs: number): number {
  const max = maxDurationMs.value;
  if (max === 0) return 1;
  return Math.max(1, (durationMs / max) * 100);
}
</script>

<style scoped>
.trace-waterfall {
  display: flex;
  flex-direction: column;
  overflow-y: auto;
  height: 100%;
  min-height: 0;
  font-size: 0.8125rem;
  color: var(--k-fg, #e2e8f0);
}

.trace-waterfall__empty {
  color: var(--k-fg-subtle, #475569);
  padding: 1rem;
  font-size: 0.875rem;
}

.trace-waterfall__turn {
  margin-bottom: 0.5rem;
}

.trace-waterfall__turn-header {
  padding: 0.25rem 0.75rem;
  font-size: 0.7rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: var(--k-fg-muted, #64748b);
  background: var(--k-bg-widget, #0f172a);
  border-bottom: 1px solid var(--k-border, #1e293b);
  position: sticky;
  top: 0;
  z-index: 1;
}

.trace-waterfall__rows {
  padding: 0.25rem 0;
}

.trace-waterfall__row {
  display: flex;
  align-items: center;
  padding: 0.2rem 0.75rem;
  gap: 0.75rem;
  cursor: pointer;
  border-radius: 3px;
  transition: background 0.1s;
}

.trace-waterfall__row:hover {
  background: var(--k-bg-hover, #1e293b);
}

.trace-waterfall__row--selected {
  background: var(--k-bg-selection, #1e3a5f);
}

.trace-waterfall__label {
  flex-shrink: 0;
  width: 200px;
  font-family: ui-monospace, monospace;
  font-size: 0.7rem;
  color: var(--k-fg-muted, #94a3b8);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.trace-waterfall__bar-container {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: center;
  height: 16px;
}

.trace-waterfall__bar {
  display: flex;
  align-items: center;
  height: 12px;
  border-radius: 2px;
  min-width: 4px;
  position: relative;
  transition: opacity 0.1s;
  opacity: 0.85;
}

.trace-waterfall__bar:hover {
  opacity: 1;
}

.trace-waterfall__bar-label {
  font-family: ui-monospace, monospace;
  font-size: 0.65rem;
  color: rgba(255, 255, 255, 0.85);
  padding: 0 4px;
  white-space: nowrap;
  overflow: hidden;
}

.trace-waterfall__tick {
  width: 2px;
  height: 12px;
  border-left: 2px solid;
  opacity: 0.6;
  flex-shrink: 0;
}
</style>
