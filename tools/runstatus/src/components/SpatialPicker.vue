<script setup lang="ts">
/**
 * SpatialPicker — a transparent overlay over a displayed frame that turns a
 * click into a point + a resolved DOM element, and a drag into a box
 * (docs/tui/spatial-capture.md).
 *
 * It sits absolutely over the rendered frame (the parent positions it; this
 * component is `position:absolute; inset:0`). On click it:
 *   1. maps the pointer from the OVERLAY's rendered rect into FRAME pixels
 *      (the rendered element is CSS-scaled / devicePixelRatio'd relative to the
 *      frame's natural size — proposal "Frame ↔ DOM scaling"), and
 *   2. resolves the element under that frame pixel against `root`
 *      (lib/resolveElement — "one resolver, two roots": the live `document`, or
 *      the rrweb Replayer iframe's `contentDocument`).
 * A drag emits an additional box (in frame pixels). It emits a single `pick`
 * carrying `{ point, box?, element }` — the spatial bundle the parent attaches
 * to the off-path question.
 *
 * The crosshair + box render as plain positioned DOM driven by reactive state
 * (the makeSpotlight getBoundingClientRect idea from
 * tests/playwright/_helpers/demo.ts) — no hand-rolled HTML strings, no
 * pointer-events leak (the markers are pointer-events:none so a second click
 * lands on the overlay, not the marker).
 */
import { ref } from "vue";
import { elementFromPoint, type ResolvedElement } from "../lib/resolveElement.js";

const props = defineProps<{
  /** The frame's natural (intrinsic) pixel width — for the rendered→frame map. */
  naturalWidth: number;
  /** The frame's natural (intrinsic) pixel height. */
  naturalHeight: number;
  /**
   * The DOM root to resolve the element against. The live `document` on the run
   * surface, or the rrweb Replayer iframe's `contentDocument` in review. When
   * absent the picker still emits point + box (a static-image frame has no DOM
   * to resolve — proposal open-question 4).
   */
  root?: Document | null;
}>();

const emit = defineEmits<{
  (
    e: "pick",
    bundle: {
      point: { x: number; y: number };
      box?: { x: number; y: number; width: number; height: number };
      element?: ResolvedElement;
    }
  ): void;
}>();

const overlay = ref<HTMLElement | null>(null);

// Crosshair (frame pixels) + box (frame pixels), null until set. Rendered as
// percentages of the natural size so they track the overlay at any CSS scale.
const point = ref<{ x: number; y: number } | null>(null);
const box = ref<{ x: number; y: number; width: number; height: number } | null>(
  null
);

// Drag state: the press origin in frame pixels, and whether the pointer has
// moved far enough to count as a drag (vs. a click).
let dragStart: { x: number; y: number } | null = null;
let dragged = false;
const DRAG_THRESHOLD = 4; // frame px

/**
 * Map a pointer event's client coords into FRAME pixels via the overlay's
 * rendered rect. The overlay covers exactly the rendered frame, so
 * (clientX-rectLeft)/rectWidth * naturalWidth is the frame-pixel X (and Y). The
 * frame's natural size + the live rect is the whole transform (devicePixelRatio
 * + CSS scale fold into rect.width vs. naturalWidth).
 */
function toFramePoint(ev: PointerEvent): { x: number; y: number } {
  const rect = overlay.value!.getBoundingClientRect();
  const sx = rect.width > 0 ? props.naturalWidth / rect.width : 1;
  const sy = rect.height > 0 ? props.naturalHeight / rect.height : 1;
  return {
    x: Math.round((ev.clientX - rect.left) * sx),
    y: Math.round((ev.clientY - rect.top) * sy),
  };
}

function onPointerDown(ev: PointerEvent): void {
  if (!overlay.value) return;
  overlay.value.setPointerCapture?.(ev.pointerId);
  dragStart = toFramePoint(ev);
  dragged = false;
  box.value = null;
}

function onPointerMove(ev: PointerEvent): void {
  if (!dragStart || !overlay.value) return;
  const cur = toFramePoint(ev);
  const dx = cur.x - dragStart.x;
  const dy = cur.y - dragStart.y;
  if (!dragged && Math.abs(dx) < DRAG_THRESHOLD && Math.abs(dy) < DRAG_THRESHOLD) {
    return;
  }
  dragged = true;
  box.value = {
    x: Math.min(dragStart.x, cur.x),
    y: Math.min(dragStart.y, cur.y),
    width: Math.abs(dx),
    height: Math.abs(dy),
  };
}

function onPointerUp(ev: PointerEvent): void {
  if (!dragStart || !overlay.value) return;
  overlay.value.releasePointerCapture?.(ev.pointerId);
  const cur = toFramePoint(ev);
  // A drag pins the point at the box's anchor (its start corner); a plain click
  // pins it where the pointer landed.
  const pt = dragged ? { x: box.value!.x, y: box.value!.y } : cur;
  point.value = pt;

  const element = resolveAt(ev, pt) ?? undefined;

  emit("pick", {
    point: pt,
    ...(dragged && box.value ? { box: box.value } : {}),
    ...(element ? { element } : {}),
  });
  dragStart = null;
}

/**
 * resolveAt resolves the element the operator pointed at, in whichever root was
 * supplied (one resolver, two roots — proposal open-question 2):
 *
 *  - LIVE PAGE: when `root` is the very document this overlay lives in, the
 *    element is BEHIND the transparent overlay. We hit-test at the raw CLIENT
 *    coords with the overlay momentarily pointer-events:none, so
 *    `elementFromPoint` returns the real page element, not the overlay itself.
 *  - REPLAY IFRAME: a separate `root` (the rrweb Replayer's contentDocument)
 *    whose viewport == the frame's natural size, so the FRAME pixels are that
 *    document's own coordinates — resolve there directly.
 *
 * Returns undefined when no root is given (a static-image frame has no DOM).
 */
function resolveAt(
  ev: PointerEvent,
  framePt: { x: number; y: number }
): ResolvedElement | null {
  const root = props.root;
  if (!root || !overlay.value) return null;
  if (root === overlay.value.ownerDocument) {
    const prev = overlay.value.style.pointerEvents;
    overlay.value.style.pointerEvents = "none";
    try {
      return elementFromPoint(root, ev.clientX, ev.clientY);
    } finally {
      overlay.value.style.pointerEvents = prev;
    }
  }
  return elementFromPoint(root, framePt.x, framePt.y);
}

// Percent-of-natural so the marker tracks the overlay regardless of CSS scale.
function pct(v: number, total: number): string {
  return total > 0 ? `${(v / total) * 100}%` : "0%";
}
</script>

<template>
  <div
    ref="overlay"
    class="spatial-picker"
    data-testid="spatial-picker"
    @pointerdown="onPointerDown"
    @pointermove="onPointerMove"
    @pointerup="onPointerUp"
  >
    <!-- Box annotation (frame pixels → % of natural). pointer-events:none so a
         follow-up click lands on the overlay, not the box. -->
    <div
      v-if="box"
      class="sp-box"
      data-testid="sp-box"
      :style="{
        left: pct(box.x, naturalWidth),
        top: pct(box.y, naturalHeight),
        width: pct(box.width, naturalWidth),
        height: pct(box.height, naturalHeight),
      }"
    />
    <!-- Crosshair at the pinned point. -->
    <div
      v-if="point"
      class="sp-point"
      data-testid="sp-point"
      :style="{ left: pct(point.x, naturalWidth), top: pct(point.y, naturalHeight) }"
    />
  </div>
</template>

<style scoped>
.spatial-picker {
  position: absolute;
  inset: 0;
  cursor: crosshair;
  /* The overlay itself captures clicks; its children do not. */
  touch-action: none;
}
.sp-box {
  position: absolute;
  border: 2px solid #fbbf24;
  background: rgba(251, 191, 36, 0.12);
  border-radius: 3px;
  pointer-events: none;
}
.sp-point {
  position: absolute;
  width: 12px;
  height: 12px;
  margin: -6px 0 0 -6px;
  border: 2px solid #fbbf24;
  border-radius: 50%;
  box-shadow: 0 0 0 2px rgba(2, 6, 23, 0.5);
  pointer-events: none;
}
</style>
