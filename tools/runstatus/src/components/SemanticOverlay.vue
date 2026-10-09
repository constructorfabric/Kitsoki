<script setup lang="ts">
/**
 * SemanticOverlay — renders the clickable element markers declared by an
 * artifact's `<name>.semantic.json` sidecar over the rendered media, and emits a
 * `semantic_element` anchor target on click (mirrors the Go semantic-sidecar
 * contract in internal/host/semantic_sidecar.go).
 *
 * This is the producer-declared counterpart to SpatialPicker's DOM resolution:
 * a mockup, object view, image, replay, or slideshow can declare named fields
 * and boxes in the sidecar instead of relying only on `elementFromPoint`. Each
 * element's box (in the media's natural pixel space) is positioned as a percent
 * of natural so it tracks the media at any CSS scale — the same
 * scale-independent trick SpatialPicker uses.
 *
 * Labels go through the client plugin registry (lib/semanticPlugins): a
 * registered plugin customizes the marker label; an absent one falls back to the
 * element's own label, then its `ref`. The emitted anchor carries `ref` VERBATIM
 * (kitsoki round-trips it; the picker never interprets it).
 */
import { computed } from "vue";
import type { SemanticMap, SemanticElement } from "../lib/semanticPlugins.js";
import { formatLabel, semanticPlugins } from "../lib/semanticPlugins.js";
import type { SemanticElementTarget, Box } from "../lib/annotationAnchor.js";

const props = defineProps<{
  /** The parsed sidecar (plugin + elements + their natural boxes). */
  map: SemanticMap;
}>();

const emit = defineEmits<{
  (e: "pick", target: SemanticElementTarget): void;
}>();

const natural = computed(() => props.map.natural);

/** Elements with a usable bbox — only these can be drawn as positioned markers. */
const drawable = computed(() =>
  props.map.elements.filter((el) => Array.isArray(el.bbox) && el.bbox.length === 4)
);

function boxOf(el: SemanticElement): Box {
  const [x, y, width, height] = el.bbox as [number, number, number, number];
  return { x, y, width, height };
}

function pct(v: number, total: number): string {
  return total > 0 ? `${(v / total) * 100}%` : "0%";
}

function label(el: SemanticElement): string {
  return formatLabel(el, props.map.plugin, semanticPlugins);
}

/** A click on a marker emits a `semantic_element` anchor target: the sidecar
 *  `ref` (verbatim), its plugin, the formatted label, its bbox, and the anchor
 *  point. `id` mirrors `ref` (the UI marker key). */
function onPick(el: SemanticElement): void {
  const bbox = boxOf(el);
  emit("pick", {
    kind: "semantic_element",
    plugin: props.map.plugin,
    ref: el.ref,
    bbox,
    id: el.ref,
    ...(el.kind ? { semantic_kind: el.kind } : {}),
    label: label(el),
    ...(el.description ? { description: el.description } : {}),
    ...(el.selector ? { selector: el.selector } : {}),
    ...(el.text ? { text: el.text } : {}),
    ...(el.value ? { value: el.value } : {}),
    ...(el.data ? { data: el.data } : {}),
    point: { x: bbox.x, y: bbox.y },
  });
}
</script>

<template>
  <div class="semantic-overlay" data-testid="semantic-overlay">
    <button
      v-for="el in drawable"
      :key="el.ref"
      class="so-marker"
      type="button"
      :data-testid="`so-marker-${el.ref}`"
      :title="label(el)"
      :style="{
        left: pct(boxOf(el).x, natural.width),
        top: pct(boxOf(el).y, natural.height),
        width: pct(boxOf(el).width, natural.width),
        height: pct(boxOf(el).height, natural.height),
      }"
      @click="onPick(el)"
    >
      <span class="so-label">{{ label(el) }}</span>
    </button>
  </div>
</template>

<style scoped>
.semantic-overlay {
  position: absolute;
  inset: 0;
  pointer-events: none;
}
.so-marker {
  position: absolute;
  border: 2px solid rgba(96, 165, 250, 0.7);
  background: rgba(96, 165, 250, 0.1);
  border-radius: 4px;
  padding: 0;
  cursor: pointer;
  pointer-events: auto;
  transition: background 0.1s ease;
}
.so-marker:hover {
  background: rgba(96, 165, 250, 0.25);
}
.so-label {
  position: absolute;
  left: 0;
  bottom: 100%;
  font-size: 10px;
  line-height: 1.3;
  padding: 1px 4px;
  background: #1e3a5f;
  color: #dbeafe;
  border-radius: 3px 3px 3px 0;
  white-space: nowrap;
  opacity: 0;
  transition: opacity 0.1s ease;
}
.so-marker:hover .so-label {
  opacity: 1;
}
</style>
