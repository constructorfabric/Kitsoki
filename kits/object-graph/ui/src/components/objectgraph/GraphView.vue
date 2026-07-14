<script setup lang="ts">
// GraphView — the one Cytoscape.js renderer shared by the inline
// relationship graph (CatalogPanel, a node's local neighborhood) and the
// full-graph overlay (ObjectGraphPage). Layouts are pluggable — see
// ./layouts.ts; picking a different one is just a dropdown, no code change.
import cytoscape, { type Core } from "cytoscape";
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
import type { ObjectGraph } from "../../objectgraph-types.js";
import { lifecycleLabel } from "./catalog-model.js";
import { cytoscapeStyle, DIFF_COLORS, LIFECYCLE_COLORS, toElements } from "./graph-elements.js";
import { defaultLayoutId, findLayout, layouts } from "./layouts.js";

// Same buckets as lifecycleBucket's possible outputs (catalog-model.ts) —
// the legend documents exactly what each node color means, nothing more.
const legend = (["available", "active", "proof", "roadmap", "candidate"] as const).map((bucket) => ({
  bucket,
  color: LIFECYCLE_COLORS[bucket],
  label: lifecycleLabel(bucket),
}));

const props = withDefaults(
  defineProps<{
    graph: ObjectGraph;
    focusId?: string;
    groupByLayer?: (node: ObjectGraph["nodes"][number]) => string;
    groupLabel?: (groupId: string) => string;
  }>(),
  { focusId: "" },
);
const emit = defineEmits<{ "update:focusId": [id: string] }>();

const diffLegendLabels: Record<string, string> = { added: "Added", modified: "Modified", removed: "Removed" };
const diffLegend = (["added", "modified", "removed"] as const).map((kind) => ({
  kind,
  color: DIFF_COLORS[kind],
  label: diffLegendLabels[kind],
}));
// Only shown when the graph actually carries diff_kind attrs (diff mode) —
// a plain (non-diff) graph load has no such attr and the border colors above
// never match, so the legend would be noise.
const showDiffLegend = computed(() => props.graph.nodes.some((n) => typeof n.attrs?.diff_kind === "string"));

const host = ref<HTMLDivElement>();
const layoutId = ref(defaultLayoutId);
let cy: Core | null = null;

function render() {
  if (!host.value) return;
  cy?.destroy();
  cy = cytoscape({
    container: host.value,
    elements: toElements(props.graph, { groupByLayer: props.groupByLayer, groupLabel: props.groupLabel }),
    style: cytoscapeStyle,
    wheelSensitivity: 0.25,
  });
  cy.on("tap", "node", (evt) => {
    const id = evt.target.id();
    if (!evt.target.data("isLayer")) emit("update:focusId", id);
  });
  runLayout();
  markFocus();
}

function runLayout() {
  if (!cy) return;
  const layout = findLayout(layoutId.value);
  cy.layout(layout.options as unknown as cytoscape.LayoutOptions).run();
}

function markFocus() {
  if (!cy) return;
  cy.nodes().removeClass("focused");
  if (props.focusId) cy.getElementById(props.focusId).addClass("focused");
}

onMounted(render);
onBeforeUnmount(() => cy?.destroy());

watch(() => props.graph, render);
watch(() => props.groupByLayer, render);
watch(() => props.groupLabel, render);
watch(layoutId, runLayout);
watch(() => props.focusId, markFocus);

defineExpose({ layoutId, layouts });
</script>

<template>
  <div class="graph-view">
    <div class="graph-view__toolbar">
      <label>
        Layout
        <select v-model="layoutId" data-testid="graph-view-layout">
          <option v-for="layout in layouts" :key="layout.id" :value="layout.id">{{ layout.label }}</option>
        </select>
      </label>
      <ul class="graph-view__legend" data-testid="graph-view-legend">
        <li v-for="entry in legend" :key="entry.bucket">
          <span class="graph-view__swatch" :style="{ background: entry.color }"></span>
          {{ entry.label }}
        </li>
      </ul>
      <ul v-if="showDiffLegend" class="graph-view__legend graph-view__legend--diff" data-testid="graph-view-diff-legend">
        <li v-for="entry in diffLegend" :key="entry.kind">
          <span class="graph-view__swatch graph-view__swatch--ring" :style="{ borderColor: entry.color }"></span>
          {{ entry.label }}
        </li>
      </ul>
      <span class="graph-view__count" data-testid="graph-view-count">
        {{ graph.nodes.length }} nodes / {{ graph.edges.length }} edges
      </span>
    </div>
    <div ref="host" class="graph-view__host" data-testid="graph-view-host"></div>
  </div>
</template>

<style scoped>
.graph-view {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
}
.graph-view__toolbar {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 0.4rem 0.75rem;
  padding: 0.4rem 0.6rem;
  border-bottom: 1px solid #d8ddd6;
  font-size: 0.8rem;
}
.graph-view__toolbar label {
  display: flex;
  align-items: center;
  gap: 0.35rem;
}
.graph-view__legend {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.15rem 0.6rem;
  margin: 0;
  padding: 0;
  list-style: none;
}
.graph-view__legend li {
  display: flex;
  align-items: center;
  gap: 0.3rem;
  color: #46534d;
}
.graph-view__swatch {
  display: inline-block;
  width: 10px;
  height: 10px;
  border-radius: 999px;
  border: 1px solid rgb(0 0 0 / 15%);
}
.graph-view__legend--diff {
  padding-left: 0.6rem;
  border-left: 1px solid #d8ddd6;
}
.graph-view__swatch--ring {
  background: none;
  border-width: 2px;
  border-style: solid;
}
.graph-view__count {
  color: #667;
  margin-left: auto;
}
.graph-view__host {
  flex: 1;
  min-height: 0;
}
</style>
