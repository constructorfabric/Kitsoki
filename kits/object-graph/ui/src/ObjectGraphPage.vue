<template>
  <div class="objectgraph-page" data-testid="objectgraph-page">
    <header class="objectgraph-page__bar">
      <span class="objectgraph-page__title">Project object graph</span>
      <span
        v-if="displayGraph"
        class="objectgraph-page__count"
        data-testid="objectgraph-count"
      >{{ displayGraph.nodes.length }} nodes / {{ displayGraph.edges.length }} edges</span>
      <div v-if="overlayPath" class="objectgraph-page__mode-toggle" data-testid="objectgraph-mode-toggle">
        <button
          v-for="m in (['asis', 'proposed', 'diff'] as const)"
          :key="m"
          type="button"
          :class="{ active: mode === m }"
          :data-testid="`objectgraph-mode-${m}`"
          @click="mode = m"
        >{{ modeLabel(m) }}</button>
      </div>
      <button
        type="button"
        class="objectgraph-page__full-graph-link"
        data-testid="objectgraph-open-full-graph"
        @click="fullGraphOpen = true"
      >View full graph ⤢</button>
    </header>

    <div v-if="loading" class="objectgraph-page__loading" data-testid="objectgraph-loading">
      Loading…
    </div>
    <div v-else-if="error" class="objectgraph-page__error" data-testid="objectgraph-error">
      {{ error }}
    </div>
    <CatalogPanel
      v-else-if="displayGraph"
      :graph="displayGraph"
      :selected-id="selectedId"
      data-testid="objectgraph-catalog"
      @update:selected-id="selectedId = $event"
    />

    <div
      v-if="fullGraphOpen && displayGraph"
      class="objectgraph-page__modal"
      data-testid="objectgraph-graph-modal"
      @keydown.esc="fullGraphOpen = false"
    >
      <div class="objectgraph-page__modal-bar">
        <span>Full project object graph</span>
        <div class="objectgraph-page__modal-actions">
          <label v-if="catalogHasAreas" class="objectgraph-page__group-toggle">
            Group by
            <select v-model="groupMode" data-testid="objectgraph-group-mode">
              <option value="type">Type layers</option>
              <option value="area">Areas</option>
            </select>
          </label>
          <button type="button" data-testid="objectgraph-close-full-graph" @click="fullGraphOpen = false">Close</button>
        </div>
      </div>
      <GraphView
        :graph="displayGraph"
        :focus-id="selectedId"
        :group-by-layer="groupByLayerFn"
        :group-label="groupLabelFn"
        class="objectgraph-page__modal-view"
        @update:focus-id="selectedId = $event"
      />
    </div>
  </div>
</template>

<script setup lang="ts">
/**
 * ObjectGraphPage — @kitsoki/object-graph's Vue viewer (S5 — moved out of
 * the engine SPA nearly as-is; see .context/kits-implementation-plan.md D3/
 * D4). CatalogPanel is the primary surface (layer map, object picker, focus
 * detail), and every object's "points to" / "points here" relationships
 * render inline as a Cytoscape neighborhood graph (see CatalogPanel's
 * relationship-graph section) rather than behind a separate mode switch. A
 * full-graph overlay (GraphView.vue over the whole catalog) is available but
 * deliberately de-emphasized — a small link, not a primary toggle.
 *
 * Catalog/overlay path are passed in as props (rather than read via
 * vue-router's useRoute()) because this module runs its own bundled Vue
 * instance, not the host SPA's — see kit-rpc.ts's doc comment for why.
 * KitPage.vue (the engine's generic kit-mount wrapper) reads
 * `?catalog=`/`?overlay=` off window.location and passes them down.
 * Data loads through kit.object-graph.graph.project (JSON-RPC), the S5
 * replacement for the deleted runstatus.objectgraph.load/diff.
 */
import { computed, onMounted, ref, watch } from "vue";
import { graphOp } from "./kit-rpc.js";
import type { ObjectGraph } from "./objectgraph-types.js";
import CatalogPanel from "./components/objectgraph/CatalogPanel.vue";
import GraphView from "./components/objectgraph/GraphView.vue";
import {
  areaGroupLabel,
  buildAreaGroupResolver,
  hasAreaNodes,
  nodeLayerId,
  setLayers,
  type Layer,
} from "./components/objectgraph/catalog-model.js";

const props = defineProps<{
  catalogPath?: string;
  overlayPath?: string;
}>();

const graph = ref<ObjectGraph | null>(null);
const diffGraph = ref<ObjectGraph | null>(null);
const loading = ref(false);
const error = ref("");
const selectedId = ref("");
const fullGraphOpen = ref(false);

// Diff mode (`?overlay=`): current-vs-proposed, the first real exercise of
// internal/graph.LoadCatalogWithOverlay + runstatus.objectgraph.diff
// (docs/proposals/project-object-graph/ui-declutter-and-diff-mode.md).
// "As-is" renders the plain catalog load (100% correct, no overlay
// involved); "Proposed" and "Diff" both render the diff RPC's graph
// (every node carries a diff_kind), filtered client-side — Proposed drops
// removed nodes, Diff keeps only the gapped ones.
type Mode = "asis" | "proposed" | "diff";
const mode = ref<Mode>("asis");
function modeLabel(m: Mode): string {
  return { asis: "As-is", proposed: "Proposed", diff: "Diff" }[m];
}

const overlayPath = computed<string>(() => props.overlayPath ?? "");

// Filters diffGraph's nodes for a mode, then drops any edge that would
// dangle (source/target no longer present) — Cytoscape (GraphView) errors
// on an edge referencing a missing node id, so this must hold for every
// mode, not just look right in the catalog list.
function filterGraph(g: ObjectGraph, keepNode: (n: ObjectGraph["nodes"][number]) => boolean): ObjectGraph {
  const nodes = g.nodes.filter(keepNode);
  const ids = new Set(nodes.map((n) => n.id));
  const edges = g.edges.filter((e) => ids.has(e.source) && ids.has(e.target));
  return { ...g, nodes, edges };
}

const displayGraph = computed<ObjectGraph | null>(() => {
  if (!overlayPath.value || mode.value === "asis") return graph.value;
  if (!diffGraph.value) return null;
  if (mode.value === "proposed") {
    return filterGraph(diffGraph.value, (n) => n.attrs?.diff_kind !== "removed");
  }
  // mode === "diff": only the gapped nodes.
  return filterGraph(diffGraph.value, (n) => n.attrs?.diff_kind !== "unchanged");
});

// Full-graph "group by" mode: the default 'type' mode is the existing
// hardcoded presentation layers (nodeLayerId); 'area' is the data-driven
// grouping over whatever area nodes/in_area edges the catalog actually has
// (design doc §4.4). Falls back to 'type' when a catalog has no area nodes.
type GroupMode = "type" | "area";
const groupMode = ref<GroupMode>("type");
const catalogHasAreas = computed(() => (graph.value ? hasAreaNodes(graph.value) : false));
const areaResolver = computed(() => (graph.value ? buildAreaGroupResolver(graph.value) : null));
const groupByLayerFn = computed(() =>
  groupMode.value === "area" && areaResolver.value ? areaResolver.value : nodeLayerId,
);
const groupLabelFn = computed(() =>
  groupMode.value === "area" && graph.value ? areaGroupLabel(graph.value) : undefined,
);
watch(catalogHasAreas, (hasAreas) => {
  if (!hasAreas) groupMode.value = "type";
});

const catalogPath = computed<string>(
  () => props.catalogPath || "docs/proposals/project-object-graph/seed-objects.yaml",
);

interface ProjectResult {
  graph: ObjectGraph;
}

async function load(): Promise<void> {
  loading.value = true;
  error.value = "";
  diffGraph.value = null;
  try {
    const loaded = await graphOp<ProjectResult>("project", {
      catalog_path: catalogPath.value,
      graph_id: "objectgraph:" + catalogPath.value,
    });
    graph.value = loaded.graph;
    selectedId.value = graph.value.nodes[0]?.id ?? "";
    if (overlayPath.value) {
      const diffed = await graphOp<ProjectResult>("project", {
        catalog_path: catalogPath.value,
        overlay_path: overlayPath.value,
        graph_id: "objectgraph-diff:" + catalogPath.value + "+" + overlayPath.value,
      });
      diffGraph.value = diffed.graph;
    } else {
      mode.value = "asis";
    }
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e);
    graph.value = null;
  } finally {
    loading.value = false;
  }
}

async function loadPresentation(): Promise<void> {
  try {
    const pres = await graphOp<{ layers: Layer[] }>("presentation", {});
    setLayers(pres.layers);
  } catch {
    // Non-fatal: the bundled default taxonomy (catalog-model.ts's initial
    // `layers` value) stays in effect — a presentation-endpoint hiccup
    // shouldn't block the viewer from rendering the graph itself.
  }
}

onMounted(() => {
  void loadPresentation();
  void load();
});
watch([catalogPath, overlayPath], load);
</script>

<style scoped>
.objectgraph-page {
  display: flex;
  flex-direction: column;
  height: 100vh;
  overflow: hidden;
}
.objectgraph-page__bar {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  padding: 0.5rem 1rem;
  border-bottom: 1px solid var(--border-color, #d6dde8);
}
.objectgraph-page__title {
  font-weight: 600;
}
.objectgraph-page__count {
  /* #667 (--muted-color's unset fallback) is 3.17:1 on the page's dark
     background — an axe color-contrast error every object-graph-review
     persona pass flagged (WCAG needs 4.5:1). #94a3b8 clears it. */
  color: var(--muted-color, #94a3b8);
  font-size: 0.85rem;
}
.objectgraph-page__full-graph-link {
  background: none;
  border: none;
  /* #46534d (here and on .objectgraph-page__mode-toggle button below) was a
     light-background-only choice: 2.21:1 against this bar's dark shell —
     the same axe color-contrast error as .objectgraph-page__count above.
     #94a3b8 fixes both sites. */
  color: #94a3b8;
  cursor: pointer;
  font-size: 0.8rem;
  margin-left: auto;
  text-decoration: underline;
}
.objectgraph-page__mode-toggle {
  display: flex;
  gap: 2px;
  margin-left: 0.5rem;
  border: 1px solid var(--border-color, #d6dde8);
  border-radius: 6px;
  padding: 2px;
}
.objectgraph-page__mode-toggle button {
  background: none;
  border: none;
  border-radius: 4px;
  color: #94a3b8;
  cursor: pointer;
  font-size: 0.75rem;
  padding: 0.2rem 0.6rem;
}
.objectgraph-page__mode-toggle button.active {
  background: #1d2a24;
  color: #f4f7f3;
  font-weight: 700;
}
:deep(.catalog-panel) {
  flex: 1;
  min-height: 0;
  overflow: auto;
}
.objectgraph-page__loading,
.objectgraph-page__error {
  padding: 1rem;
}
.objectgraph-page__error {
  color: var(--error-color, #b00020);
}
.objectgraph-page__modal {
  position: fixed;
  inset: 0;
  background: #fff;
  display: flex;
  flex-direction: column;
  z-index: 20;
}
.objectgraph-page__modal-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0.5rem 1rem;
  border-bottom: 1px solid var(--border-color, #d6dde8);
  font-weight: 600;
}
.objectgraph-page__modal-actions {
  display: flex;
  align-items: center;
  gap: 0.75rem;
}
.objectgraph-page__group-toggle {
  display: flex;
  align-items: center;
  gap: 0.35rem;
  font-size: 0.8rem;
  font-weight: 400;
}
.objectgraph-page__modal-bar button {
  background: #1d2a24;
  border: none;
  border-radius: 6px;
  color: #f4f7f3;
  cursor: pointer;
  font-weight: 700;
  padding: 0.35rem 0.9rem;
}
.objectgraph-page__modal-view {
  flex: 1;
  min-height: 0;
}
</style>
