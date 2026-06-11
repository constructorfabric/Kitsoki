<script setup lang="ts">
/**
 * FlagList — the ordered list of pinned flags in the left column. Flags are
 * shown by start_ms; selecting one pins it to the FlagDetail panel. A filled
 * dot marks a dispatched flag, a hollow dot an open one.
 */
import { computed } from "vue";
import type { Flag } from "../lib/flags.js";
import { formatMs } from "../lib/flags.js";

const props = defineProps<{
  flags: Flag[];
  selectedId: number | null;
}>();

const emit = defineEmits<{
  (e: "select", id: number): void;
  (e: "send-all"): void;
}>();

const ordered = computed(() =>
  [...props.flags].sort((a, b) => a.start_ms - b.start_ms)
);

function label(f: Flag): string {
  if (f.end_ms > f.start_ms) {
    return `${formatMs(f.start_ms)}–${formatMs(f.end_ms)}`;
  }
  return formatMs(f.start_ms);
}

const hasUnsent = computed(() => props.flags.some((f) => !f.sent));
</script>

<template>
  <div class="flag-list" data-testid="flag-list">
    <div class="fl-header">
      <h3 class="fl-title">Flags</h3>
      <button
        class="fl-send-all"
        data-testid="fl-send-all"
        :disabled="!hasUnsent"
        @click="emit('send-all')"
      >
        Send all
      </button>
    </div>
    <p v-if="flags.length === 0" class="fl-empty">
      No flags yet — select a moment on the timeline and flag it.
    </p>
    <ul v-else class="fl-items">
      <li
        v-for="f in ordered"
        :key="f.id"
        class="fl-item"
        :class="{ 'fl-item--selected': f.id === selectedId }"
        :data-testid="'fl-item-' + f.id"
        @click="emit('select', f.id)"
      >
        <span class="fl-dot" :class="{ 'fl-dot--sent': f.sent }" />
        <span class="fl-time">{{ label(f) }}</span>
        <span class="fl-text">{{
          f.instruction || f.chapter?.label || "(no note)"
        }}</span>
      </li>
    </ul>
  </div>
</template>

<style scoped>
.flag-list {
  display: flex;
  flex-direction: column;
  gap: 0.5em;
}
.fl-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.fl-title {
  margin: 0;
  font-size: 14px;
  font-weight: 600;
  color: #4a5160;
}
.fl-send-all {
  font-size: 12px;
  padding: 0.25em 0.7em;
  border: 1px solid #1d4ed8;
  border-radius: 6px;
  background: #fff;
  color: #1d4ed8;
  cursor: pointer;
}
.fl-send-all:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}
.fl-empty {
  font-size: 13px;
  color: #6b7280;
  margin: 0;
}
.fl-items {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 0.25em;
}
.fl-item {
  display: flex;
  align-items: center;
  gap: 0.6em;
  padding: 0.4em 0.5em;
  border-radius: 6px;
  cursor: pointer;
  font-size: 13px;
}
.fl-item:hover {
  background: #f1f3f7;
}
.fl-item--selected {
  background: #eef4ff;
}
.fl-dot {
  width: 9px;
  height: 9px;
  border-radius: 50%;
  border: 2px solid #1d4ed8;
  flex: none;
}
.fl-dot--sent {
  background: #1d4ed8;
}
.fl-time {
  font-variant-numeric: tabular-nums;
  color: #4a5160;
  flex: none;
}
.fl-text {
  color: #1f2430;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
