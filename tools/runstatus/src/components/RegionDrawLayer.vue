<script setup lang="ts">
/**
 * RegionDrawLayer — a reusable <canvas> overlay for drawing a REGION annotation
 * over a still (a png <img>, a grabbed mp4 frame, or an rrweb frame). It is the
 * canvas counterpart to SpatialPicker's DOM crosshair: where the picker resolves
 * a DOM element behind the frame, this layer lets the operator MARK an area when
 * there is no DOM to resolve (a flat image) or when the area, not an element, is
 * the point.
 *
 * Three tools, all drawn in NATURAL pixels (the canvas backing store is sized to
 * the media's intrinsic size, CSS-scaled to fit, so a region is media-resolution
 * independent — same contract as SpatialPicker.toFramePoint):
 *
 *   - box       — drag a rectangle (a crisp outlined box)
 *   - highlight — drag a rectangle drawn as a translucent fill (a marker swipe)
 *   - freeform  — draw a freehand path of points (its bbox is the path's extent)
 *
 * On pointer-up it emits a single `region` with {shape, bbox, path?} in natural
 * pixels — the parent maps it into an AnnotationAnchor via regionToTarget. It
 * does NOT own the anchor or the artifact metadata; it is a pure draw surface.
 */
import { ref, watch, onMounted } from "vue";
import type { Box, Point, RegionShape } from "../lib/annotationAnchor.js";

const props = defineProps<{
  /** The still's natural (intrinsic) pixel width — the canvas backing size. */
  naturalWidth: number;
  /** The still's natural (intrinsic) pixel height. */
  naturalHeight: number;
  /** Which tool is active. The parent owns the toolbar; default `box`. */
  shape?: RegionShape;
}>();

const emit = defineEmits<{
  (e: "region", region: { shape: RegionShape; bbox: Box; path?: Point[] }): void;
}>();

const canvas = ref<HTMLCanvasElement | null>(null);

// Drag/draw state in NATURAL pixels.
let drawing = false;
let start: Point | null = null;
let path: Point[] = [];

const STROKE = "#fbbf24";
const FILL = "rgba(251, 191, 36, 0.18)";

function activeShape(): RegionShape {
  return props.shape ?? "box";
}

/** Map a pointer event into the canvas's NATURAL pixel space via its rendered
 *  rect (the backing store is naturalWidth×naturalHeight, CSS-scaled to fit). */
function toNatural(ev: PointerEvent): Point {
  const el = canvas.value!;
  const rect = el.getBoundingClientRect();
  const sx = rect.width > 0 ? props.naturalWidth / rect.width : 1;
  const sy = rect.height > 0 ? props.naturalHeight / rect.height : 1;
  return {
    x: Math.round((ev.clientX - rect.left) * sx),
    y: Math.round((ev.clientY - rect.top) * sy),
  };
}

function ctx(): CanvasRenderingContext2D | null {
  return canvas.value?.getContext("2d") ?? null;
}

function clear(): void {
  const c = ctx();
  if (c) c.clearRect(0, 0, props.naturalWidth, props.naturalHeight);
}

/** bbox of the drag (box/highlight) or of the freeform path. */
function bboxFromPath(pts: Point[]): Box {
  const xs = pts.map((p) => p.x);
  const ys = pts.map((p) => p.y);
  const minX = Math.min(...xs);
  const minY = Math.min(...ys);
  return {
    x: minX,
    y: minY,
    width: Math.max(...xs) - minX,
    height: Math.max(...ys) - minY,
  };
}

function bboxFromDrag(a: Point, b: Point): Box {
  return {
    x: Math.min(a.x, b.x),
    y: Math.min(a.y, b.y),
    width: Math.abs(b.x - a.x),
    height: Math.abs(b.y - a.y),
  };
}

function drawBox(box: Box, highlight: boolean): void {
  const c = ctx();
  if (!c) return;
  clear();
  c.lineWidth = 2;
  c.strokeStyle = STROKE;
  if (highlight) {
    c.fillStyle = FILL;
    c.fillRect(box.x, box.y, box.width, box.height);
  }
  c.strokeRect(box.x, box.y, box.width, box.height);
}

function drawPath(pts: Point[]): void {
  const c = ctx();
  if (!c || pts.length === 0) return;
  clear();
  c.lineWidth = 2;
  c.strokeStyle = STROKE;
  c.lineJoin = "round";
  c.lineCap = "round";
  c.beginPath();
  c.moveTo(pts[0].x, pts[0].y);
  for (const p of pts.slice(1)) c.lineTo(p.x, p.y);
  c.stroke();
}

function onPointerDown(ev: PointerEvent): void {
  if (!canvas.value) return;
  canvas.value.setPointerCapture?.(ev.pointerId);
  drawing = true;
  start = toNatural(ev);
  path = [start];
}

function onPointerMove(ev: PointerEvent): void {
  if (!drawing || !start) return;
  const cur = toNatural(ev);
  if (activeShape() === "freeform") {
    path.push(cur);
    drawPath(path);
  } else {
    drawBox(bboxFromDrag(start, cur), activeShape() === "highlight");
  }
}

function onPointerUp(ev: PointerEvent): void {
  if (!drawing || !start) return;
  canvas.value?.releasePointerCapture?.(ev.pointerId);
  drawing = false;
  const cur = toNatural(ev);
  const shape = activeShape();
  if (shape === "freeform") {
    path.push(cur);
    const bbox = bboxFromPath(path);
    drawPath(path);
    emit("region", { shape, bbox, path: [...path] });
  } else {
    const bbox = bboxFromDrag(start, cur);
    // A zero-area drag (a click) collapses to a 1×1 box so it is still a region.
    const safe: Box = {
      x: bbox.x,
      y: bbox.y,
      width: Math.max(bbox.width, 1),
      height: Math.max(bbox.height, 1),
    };
    drawBox(safe, shape === "highlight");
    emit("region", { shape, bbox: safe });
  }
  start = null;
}

/** Size the canvas backing store to the natural pixels whenever it changes, so
 *  every draw is in intrinsic-resolution coordinates. */
function sizeCanvas(): void {
  const el = canvas.value;
  if (!el) return;
  el.width = props.naturalWidth;
  el.height = props.naturalHeight;
}

onMounted(sizeCanvas);
watch(() => [props.naturalWidth, props.naturalHeight], sizeCanvas);
// Switching tools clears the in-progress draw so shapes never overlap.
watch(() => props.shape, clear);
</script>

<template>
  <canvas
    ref="canvas"
    class="region-draw-layer"
    data-testid="region-draw-layer"
    @pointerdown="onPointerDown"
    @pointermove="onPointerMove"
    @pointerup="onPointerUp"
  />
</template>

<style scoped>
.region-draw-layer {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  cursor: crosshair;
  touch-action: none;
}
</style>
