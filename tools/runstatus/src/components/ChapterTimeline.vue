<script setup lang="ts">
/**
 * ChapterTimeline — the seek/select strip under the video player.
 *
 * Renders one marker per chapter, positioned along a [0, totalMs] track.
 * Clicking a marker emits `seek` (jump the player to the chapter start);
 * clicking or dragging across the track emits `select` with a [start,end]
 * range in ms; "flag this" emits `flag` with the current selection (a point
 * when start === end). The component holds no video — the parent owns the
 * player and reacts to these events.
 *
 * Positions are derived from each chapter's start_ms over totalMs. When the
 * sidecar's time windows are zero-width (slice-1 deviation 2: slidey chapters
 * may carry zero ranges), markers fall back to even spacing so the strip stays
 * legible — the parent passes evenFallback=true in that case.
 */
import { computed, ref } from "vue";
import type { Chapter } from "../data/source.js";

const props = defineProps<{
  chapters: Chapter[];
  /** Total video duration in ms; positions markers along the track. */
  totalMs: number;
  /** Currently selected range (for highlighting), or null. */
  selection?: { start_ms: number; end_ms: number } | null;
}>();

const emit = defineEmits<{
  (e: "seek", tMs: number): void;
  (e: "select", range: { start_ms: number; end_ms: number }): void;
  (e: "flag", range: { start_ms: number; end_ms: number }): void;
}>();

// When the sidecar carries no usable duration (all zero-width), space markers
// evenly so they remain clickable.
const evenFallback = computed(
  () => props.totalMs <= 0 || props.chapters.every((c) => c.end_ms <= c.start_ms)
);

function markerPct(c: Chapter, i: number): number {
  if (evenFallback.value) {
    const n = props.chapters.length;
    return n <= 1 ? 0 : (i / (n - 1)) * 100;
  }
  return Math.max(0, Math.min(100, (c.start_ms / props.totalMs) * 100));
}

function onMarkerClick(c: Chapter) {
  emit("seek", c.start_ms);
}

// ── Drag selection ──────────────────────────────────────────────────────────
const track = ref<HTMLElement | null>(null);
const dragStart = ref<number | null>(null);

function msAtClientX(clientX: number): number {
  const el = track.value;
  if (!el || props.totalMs <= 0) return 0;
  const rect = el.getBoundingClientRect();
  const frac = Math.max(0, Math.min(1, (clientX - rect.left) / rect.width));
  return Math.round(frac * props.totalMs);
}

function onTrackPointerDown(ev: PointerEvent) {
  dragStart.value = msAtClientX(ev.clientX);
}

function onTrackPointerUp(ev: PointerEvent) {
  if (dragStart.value === null) return;
  const a = dragStart.value;
  const b = msAtClientX(ev.clientX);
  dragStart.value = null;
  const start = Math.min(a, b);
  const end = Math.max(a, b);
  emit("select", { start_ms: start, end_ms: end });
}

const selPct = computed(() => {
  if (!props.selection || props.totalMs <= 0) return null;
  const left = (props.selection.start_ms / props.totalMs) * 100;
  const width =
    ((props.selection.end_ms - props.selection.start_ms) / props.totalMs) * 100;
  return { left, width: Math.max(0.5, width) };
});

function onFlag() {
  const sel = props.selection;
  if (sel) {
    emit("flag", sel);
  }
}
</script>

<template>
  <div class="chapter-timeline" data-testid="chapter-timeline">
    <div
      ref="track"
      class="ct-track"
      data-testid="ct-track"
      @pointerdown="onTrackPointerDown"
      @pointerup="onTrackPointerUp"
    >
      <div
        v-if="selPct"
        class="ct-selection"
        :style="{ left: selPct.left + '%', width: selPct.width + '%' }"
      />
      <button
        v-for="(c, i) in chapters"
        :key="c.id"
        class="ct-marker"
        :data-testid="'ct-marker-' + c.id"
        :style="{ left: markerPct(c, i) + '%' }"
        :title="c.label"
        @click.stop="onMarkerClick(c)"
      >
        <span class="ct-marker-index">{{ c.index + 1 }}</span>
      </button>
    </div>
    <div class="ct-actions">
      <button
        class="ct-flag-btn"
        data-testid="ct-flag-btn"
        :disabled="!selection"
        @click="onFlag"
      >
        Flag this
      </button>
    </div>
  </div>
</template>

<style scoped>
.chapter-timeline {
  display: flex;
  flex-direction: column;
  gap: 0.5em;
}
.ct-track {
  position: relative;
  height: 28px;
  background: #eef0f4;
  border-radius: 6px;
  cursor: crosshair;
  touch-action: none;
}
.ct-selection {
  position: absolute;
  top: 0;
  bottom: 0;
  background: rgba(29, 78, 216, 0.18);
  border-left: 2px solid #1d4ed8;
  border-right: 2px solid #1d4ed8;
  pointer-events: none;
}
.ct-marker {
  position: absolute;
  top: 50%;
  transform: translate(-50%, -50%);
  width: 18px;
  height: 18px;
  border-radius: 50%;
  border: 2px solid #1d4ed8;
  background: #fff;
  color: #1d4ed8;
  font-size: 10px;
  font-weight: 700;
  cursor: pointer;
  padding: 0;
  display: flex;
  align-items: center;
  justify-content: center;
}
.ct-marker:hover {
  background: #1d4ed8;
  color: #fff;
}
.ct-actions {
  display: flex;
  justify-content: flex-end;
}
.ct-flag-btn {
  font-size: 13px;
  padding: 0.3em 0.8em;
  border: 1px solid #1d4ed8;
  border-radius: 6px;
  background: #1d4ed8;
  color: #fff;
  cursor: pointer;
}
.ct-flag-btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}
</style>
