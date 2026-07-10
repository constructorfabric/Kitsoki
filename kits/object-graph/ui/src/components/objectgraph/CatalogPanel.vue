<script setup lang="ts">
/**
 * CatalogPanel — the layered list/detail catalog projection of the project
 * object graph, restored from the deleted W5.0-prototype App.vue (the
 * "catalog" half of docs/proposals/project-object-graph/viewer/'s two
 * projections; the "graph" half is GraphCanvas.vue, already ported).
 * Reads the same ObjectGraph the canvas renders (runstatus.objectgraph.load)
 * so both projections share one fetch and one selection.
 */
import { computed, nextTick, ref, watch } from "vue";
import type { ObjectGraph, ObjectGraphNode } from "../../objectgraph-types.js";
import {
  diffKind,
  diffKindLabel,
  edgeTargetsByKind,
  incomingGroups,
  layers,
  lifecycleBucket,
  lifecycleLabel,
  nodeFields,
  nodeLayerId,
  nodeSources,
  nodeText,
  nodeTypeChain,
  nodeVisibility,
  outgoingGroups,
  typeLabel,
  typeOrder,
} from "./catalog-model.js";
import GraphView from "./GraphView.vue";
import { neighborhood } from "./graph-elements.js";

const props = defineProps<{ graph: ObjectGraph; selectedId: string }>();
const emit = defineEmits<{ "update:selectedId": [id: string] }>();

const selectedLayerId = ref(nodeLayerId(currentNode()));
const selectedTypeId = ref("all");
const query = ref("");
const draftFields = ref({ title: "", tagline: "", summary: "" });
const requestStarted = ref(false);
const relationshipGraphExpanded = ref(false);
const relationshipGraphModalEl = ref<HTMLDivElement>();

watch(relationshipGraphExpanded, (expanded) => {
  if (!expanded) return;
  nextTick(() => relationshipGraphModalEl.value?.focus());
});

function currentNode(): ObjectGraphNode {
  return props.graph.nodes.find((n) => n.id === props.selectedId) ?? props.graph.nodes[0];
}

const nodeById = computed(() => new Map(props.graph.nodes.map((n) => [n.id, n])));
const selectedNode = computed(() => nodeById.value.get(props.selectedId) ?? props.graph.nodes[0]);
const selectedLayer = computed(() => layers.find((layer) => layer.id === selectedLayerId.value) ?? layers[0]);

const typeCounts = computed(() => {
  const counts = new Map<string, number>();
  for (const node of props.graph.nodes) counts.set(node.kind, (counts.get(node.kind) ?? 0) + 1);
  return counts;
});

const lifecycleSummaries = computed(() =>
  ["available", "active", "proof", "roadmap", "candidate"].map((bucket) => ({
    bucket,
    label: lifecycleLabel(bucket),
    count: props.graph.nodes.filter((node) => lifecycleBucket(node) === bucket).length,
  })),
);

const layerSummaries = computed(() =>
  layers.map((layer) => {
    const nodes = props.graph.nodes.filter((node) => layer.types.includes(node.kind));
    const selected = nodes.some((node) => node.id === selectedNode.value?.id);
    return {
      ...layer,
      count: nodes.length,
      selected,
      lifecycleCounts: ["available", "active", "proof", "roadmap", "candidate"]
        .map((bucket) => ({
          bucket,
          label: lifecycleLabel(bucket),
          count: nodes.filter((node) => lifecycleBucket(node) === bucket).length,
        }))
        .filter((entry) => entry.count > 0),
    };
  }),
);

const availableTypes = computed(() =>
  selectedLayer.value.types
    .filter((type) => typeCounts.value.has(type))
    .map((type) => ({ type, label: typeLabel(type), count: typeCounts.value.get(type) ?? 0 })),
);

const filteredNodes = computed(() => {
  const needle = query.value.trim().toLowerCase();
  return props.graph.nodes
    .filter((node) => selectedLayer.value.types.includes(node.kind))
    .filter((node) => selectedTypeId.value === "all" || node.kind === selectedTypeId.value)
    .filter((node) => {
      if (!needle) return true;
      const fields = nodeFields(node);
      return [node.label, node.id, fields.summary, fields.statement, fields.goal, fields.desired_outcome]
        .filter(Boolean)
        .some((value) => String(value).toLowerCase().includes(needle));
    })
    .sort((a, b) => typeOrder(a.kind) - typeOrder(b.kind) || a.label.localeCompare(b.label));
});

// Some nodes nest under a same-or-different-kind parent rather than
// standing as their own peer entry: a persona nests under the actor it
// profiles (persona_of), and a proposal slice nests under the epic it
// decomposes (child_of, W6.2). Which edges mean "nest me under my target"
// is read straight off the wire graph's edge.attrs.nests_under — set by
// ObjectCatalogGraph from the type registry's `nests_under: true` edge-field
// marker (internal/graph/registry.go's EdgeFieldDecl.NestsUnder) — rather
// than a hand-maintained kind->edge table here that silently drifts from
// the type registry whenever a new nesting relationship is added (the bug
// this replaces: W6.2 shipped 21 new child proposals before this table
// existed, and they all rendered as flat peers until it was added by hand).
// Nesting only applies when the parent is present in the current filtered
// list (e.g. the type chip narrows to just "Personas") — otherwise the
// node falls back to a flat peer entry so it's never silently hidden.
const groupedNodes = computed(() => {
  const nodes = filteredNodes.value;
  const listedIds = new Set(nodes.map((node) => node.id));
  const nodeById = new Map(props.graph.nodes.map((node) => [node.id, node]));
  const childrenByParent = new Map<string, ObjectGraphNode[]>();
  const nestedIds = new Set<string>();
  for (const edge of props.graph.edges) {
    if (!edge.attrs?.nests_under) continue;
    if (!listedIds.has(edge.source) || !listedIds.has(edge.target)) continue;
    const child = nodeById.get(edge.source);
    if (!child) continue;
    if (!childrenByParent.has(edge.target)) childrenByParent.set(edge.target, []);
    childrenByParent.get(edge.target)?.push(child);
    nestedIds.add(edge.source);
  }
  const grouped = new Map<string, Array<{ node: ObjectGraphNode; children: ObjectGraphNode[] }>>();
  for (const node of nodes) {
    if (nestedIds.has(node.id)) continue;
    if (!grouped.has(node.kind)) grouped.set(node.kind, []);
    grouped.get(node.kind)?.push({ node, children: childrenByParent.get(node.id) ?? [] });
  }
  return [...grouped.entries()].map(([type, entries]) => ({ type, label: typeLabel(type), entries }));
});

const outgoing = computed(() => (selectedNode.value ? outgoingGroups(props.graph, selectedNode.value.id) : []));
const incoming = computed(() => (selectedNode.value ? incomingGroups(props.graph, selectedNode.value.id) : []));
// The relationship graph is the selected object's own 1-hop neighborhood —
// the same "points to" / "points here" edges as the text groups below,
// drawn as a graph rather than restated as another data source.
const relationshipGraph = computed(() =>
  selectedNode.value ? neighborhood(props.graph, selectedNode.value.id) : { ...props.graph, nodes: [], edges: [] },
);
const typeChain = computed(() => (selectedNode.value ? nodeTypeChain(selectedNode.value) : []));
const sourceIds = computed(() => (selectedNode.value ? nodeSources(selectedNode.value) : []));

const sitePageForSelection = computed(() => {
  if (!selectedNode.value) return undefined;
  if (selectedNode.value.kind === "site-page") return selectedNode.value;
  return incoming.value.find((group) => group.name === "presents")?.nodes[0];
});

const demoEvidenceForSelection = computed(() => {
  const page = sitePageForSelection.value;
  if (page) {
    const media = edgeTargetsByKind(props.graph, page.id, "has_media")[0];
    if (media) return media;
  }
  if (!selectedNode.value) return undefined;
  return edgeTargetsByKind(props.graph, selectedNode.value.id, "evidence")[0];
});

const originalSiteFields = computed(() => {
  const page = sitePageForSelection.value;
  const pageFields = page ? nodeFields(page) : {};
  const contentFields = (pageFields.content_fields as Record<string, string>) ?? {};
  return {
    title: contentFields.title ?? page?.label ?? selectedNode.value?.label ?? "",
    tagline: contentFields.tagline ?? (pageFields.tagline as string) ?? "",
    summary: contentFields.summary ?? (selectedNode.value ? nodeText(selectedNode.value) : ""),
  };
});

const changedFields = computed(() =>
  (["title", "tagline", "summary"] as const).filter(
    (field) => draftFields.value[field].trim() !== originalSiteFields.value[field].trim(),
  ),
);

const changeEvaluation = computed(() => {
  const combined = Object.values(draftFields.value).join(" ").toLowerCase();
  const changed = changedFields.value;
  const checks = [
    {
      id: "content",
      label: "Product-site content",
      state: changed.length ? "changed" : "unchanged",
      detail: changed.length ? `${changed.length} structured field changes` : "No page field changed yet",
    },
    {
      id: "feature",
      label: "Feature impact",
      state: /\b(new|add|adds|support|supports|integrat|automate|launch|workflow)\b/.test(combined)
        ? "review"
        : "clear",
      detail: "Flags copy that appears to promise a new or expanded capability.",
    },
    {
      id: "consistency",
      label: "Graph consistency",
      state: sitePageForSelection.value && demoEvidenceForSelection.value ? "clear" : "review",
      detail: sitePageForSelection.value
        ? "Page is linked to a feature and demo evidence object."
        : "No site-page object is linked to the selected record.",
    },
    {
      id: "policy",
      label: "Non-negotiable review",
      state: /\b(guarantee|compliance|regulation|regulated|privacy|security|legal|hipaa|gdpr|soc2|soc 2)\b/.test(
        combined,
      )
        ? "review"
        : "clear",
      detail: "Flags legal, privacy, security, compliance, or absolute claims.",
    },
  ];
  const route = checks.some((check) => check.state === "review")
    ? "Evaluate before delivery"
    : changed.length
      ? "Content-only site update"
      : "No change request";
  return { route, checks };
});

const generatedChangeRequest = computed(() => ({
  schema: "graph/change-request/v0",
  target: sitePageForSelection.value?.id ?? selectedNode.value?.id,
  changed_fields: changedFields.value,
  route: changeEvaluation.value.route,
  checks: changeEvaluation.value.checks.map((check) => ({ id: check.id, state: check.state })),
}));

watch(
  originalSiteFields,
  (fields) => {
    draftFields.value = { ...fields };
    requestStarted.value = false;
  },
  { immediate: true },
);

watch(changedFields, () => {
  requestStarted.value = false;
});

function selectLayer(layerId: string) {
  selectedLayerId.value = layerId;
  selectedTypeId.value = "all";
}

function selectNode(id: string) {
  emit("update:selectedId", id);
  const node = nodeById.value.get(id);
  if (node) selectedLayerId.value = nodeLayerId(node);
  selectedTypeId.value = "all";
}
</script>

<template>
  <div class="catalog-panel" data-testid="catalog-panel">
    <section class="status-legend" aria-label="Lifecycle status summary">
      <span v-for="entry in lifecycleSummaries" :key="entry.bucket" :class="['status-badge', `life-${entry.bucket}`]">
        {{ entry.label }} {{ entry.count }}
      </span>
    </section>

    <section class="layer-map" aria-label="Project object graph layers">
      <button
        v-for="layer in layerSummaries"
        :key="layer.id"
        type="button"
        class="layer-card"
        :class="{ active: selectedLayerId === layer.id, contains: layer.selected }"
        :title="layer.description"
        @click="selectLayer(layer.id)"
      >
        <span class="step">{{ layer.short }}</span>
        <strong>{{ layer.title }}</strong>
        <span class="layer-count">{{ layer.count }}</span>
        <span class="layer-statuses">
          <span
            v-for="entry in layer.lifecycleCounts"
            :key="entry.bucket"
            :class="['status-dot', `life-${entry.bucket}`]"
            :title="`${entry.label}: ${entry.count}`"
          >{{ entry.count }}</span>
        </span>
      </button>
    </section>

    <section class="workspace">
      <aside class="object-picker">
        <div class="picker-head">
          <div>
            <p class="kicker">{{ selectedLayer.short }}</p>
            <h2>{{ selectedLayer.title }}</h2>
          </div>
          <button v-if="selectedTypeId !== 'all'" @click="selectedTypeId = 'all'">All types</button>
        </div>

        <p class="picker-context">{{ selectedLayer.description }}</p>

        <div class="type-filter" aria-label="Type filter">
          <button :class="{ active: selectedTypeId === 'all' }" @click="selectedTypeId = 'all'">
            All {{ selectedLayer.short.toLowerCase() }}
          </button>
          <button
            v-for="entry in availableTypes"
            :key="entry.type"
            :class="{ active: selectedTypeId === entry.type }"
            @click="selectedTypeId = entry.type"
          >{{ entry.label }} {{ entry.count }}</button>
        </div>

        <input v-model="query" type="search" placeholder="Search this layer" aria-label="Search objects" />

        <div class="object-list">
          <section v-for="group in groupedNodes" :key="group.type" class="object-group">
            <h3>{{ group.label }}</h3>
            <template v-for="entry in group.entries" :key="entry.node.id">
              <button :class="{ selected: selectedNode?.id === entry.node.id }" @click="selectNode(entry.node.id)">
                <strong>{{ entry.node.label }}</strong>
                <small>{{ entry.node.id }}</small>
                <span class="node-badges">
                  <span v-if="diffKind(entry.node) && diffKind(entry.node) !== 'unchanged'" :class="['diff-badge', `diff-${diffKind(entry.node)}`]">{{ diffKindLabel(diffKind(entry.node)) }}</span>
                  <span :class="['status-badge', `life-${lifecycleBucket(entry.node)}`]">{{ lifecycleLabel(lifecycleBucket(entry.node)) }}</span>
                  <span :class="['visibility-badge', `visibility-${nodeVisibility(entry.node)}`]">{{ nodeVisibility(entry.node) }}</span>
                </span>
              </button>
              <button
                v-for="child in entry.children"
                :key="child.id"
                class="object-child"
                :class="{ selected: selectedNode?.id === child.id }"
                @click="selectNode(child.id)"
              >
                <strong>{{ child.label }}</strong>
                <small>{{ child.id }}</small>
                <span class="node-badges">
                  <span class="type-badge">{{ child.kind }}</span>
                  <span v-if="diffKind(child) && diffKind(child) !== 'unchanged'" :class="['diff-badge', `diff-${diffKind(child)}`]">{{ diffKindLabel(diffKind(child)) }}</span>
                  <span :class="['status-badge', `life-${lifecycleBucket(child)}`]">{{ lifecycleLabel(lifecycleBucket(child)) }}</span>
                  <span :class="['visibility-badge', `visibility-${nodeVisibility(child)}`]">{{ nodeVisibility(child) }}</span>
                </span>
              </button>
            </template>
          </section>
        </div>
      </aside>

      <section v-if="selectedNode" class="focus">
        <article class="focus-card">
          <div class="focus-top">
            <div>
              <p class="kicker">{{ typeLabel(selectedNode.kind) }}</p>
              <h2>{{ selectedNode.label }}</h2>
            </div>
            <div class="chips">
              <span
                v-if="diffKind(selectedNode) && diffKind(selectedNode) !== 'unchanged'"
                :class="['diff-badge', `diff-${diffKind(selectedNode)}`]"
              >{{ diffKindLabel(diffKind(selectedNode)) }}</span>
              <span :class="['status-badge', `life-${lifecycleBucket(selectedNode)}`]">
                {{ lifecycleLabel(lifecycleBucket(selectedNode)) }} / {{ selectedNode.status }}
              </span>
              <span :class="['visibility-badge', `visibility-${nodeVisibility(selectedNode)}`]">
                {{ nodeVisibility(selectedNode) }}
              </span>
            </div>
          </div>

          <p class="body-text">{{ nodeText(selectedNode) }}</p>

          <div class="type-chain">
            <span>Type inheritance</span>
            <button
              v-for="type in typeChain"
              :key="type"
              @click="
                selectedTypeId = type;
                selectedLayerId = layers.find((layer) => layer.types.includes(type))?.id ?? selectedLayerId;
              "
            >{{ type }}</button>
          </div>
        </article>

        <section v-if="sitePageForSelection" class="site-workbench">
          <article class="site-preview">
            <div>
              <p class="kicker">Product site projection</p>
              <h3>{{ originalSiteFields.title }}</h3>
              <strong>{{ originalSiteFields.tagline }}</strong>
              <p>{{ originalSiteFields.summary }}</p>
            </div>
            <div class="media-box">
              <span>Demo evidence</span>
              <strong>{{ demoEvidenceForSelection?.label ?? "No linked evidence object" }}</strong>
              <code v-if="demoEvidenceForSelection">{{ demoEvidenceForSelection.id }}</code>
            </div>
          </article>

          <article class="change-editor">
            <div class="editor-head">
              <div>
                <p class="kicker">Edit structured fields</p>
                <h3>Change request preview</h3>
              </div>
              <span :class="['request-route', changedFields.length ? 'route-active' : 'route-idle']">
                {{ changeEvaluation.route }}
              </span>
            </div>

            <label>
              <span>Title</span>
              <input v-model="draftFields.title" />
            </label>
            <label>
              <span>Tagline</span>
              <input v-model="draftFields.tagline" />
            </label>
            <label>
              <span>Summary</span>
              <textarea v-model="draftFields.summary" rows="4" />
            </label>

            <div class="evaluation-grid">
              <div v-for="check in changeEvaluation.checks" :key="check.id" :class="['evaluation-check', `check-${check.state}`]">
                <span>{{ check.label }}</span>
                <strong>{{ check.state }}</strong>
                <p>{{ check.detail }}</p>
              </div>
            </div>

            <div class="request-actions">
              <button type="button" :disabled="!changedFields.length" @click="requestStarted = true">
                Start change request
              </button>
              <code v-if="requestStarted">{{ JSON.stringify(generatedChangeRequest) }}</code>
              <small v-else>Edits stay as structured draft fields until a change request is started.</small>
            </div>
          </article>
        </section>

        <section class="relationship-graph" data-testid="relationship-graph">
          <div class="relationship-graph__head">
            <h3>Relationships, visualized</h3>
            <button
              type="button"
              class="relationship-graph__expand"
              data-testid="relationship-graph-expand"
              @click="relationshipGraphExpanded = true"
            >Expand ⤢</button>
          </div>
          <GraphView
            :graph="relationshipGraph"
            :focus-id="selectedNode.id"
            class="relationship-graph__view"
            @update:focus-id="selectNode"
          />
        </section>

        <div class="relationship-board">
          <section>
            <h3>What this object points to</h3>
            <div v-if="!outgoing.length" class="empty">No outgoing relationships.</div>
            <article v-for="group in outgoing" :key="group.name" class="relationship-group">
              <p>{{ group.label }}</p>
              <button v-for="node in group.nodes" :key="node.id" @click="selectNode(node.id)">
                <span>{{ typeLabel(node.kind) }}</span>
                <strong>{{ node.label }}</strong>
                <em :class="['relationship-state', `life-${lifecycleBucket(node)}`]">{{ lifecycleLabel(lifecycleBucket(node)) }}</em>
              </button>
            </article>
          </section>

          <section>
            <h3>What points here</h3>
            <div v-if="!incoming.length" class="empty">No incoming relationships.</div>
            <article v-for="group in incoming" :key="group.name" class="relationship-group incoming">
              <p>{{ group.label }}</p>
              <button v-for="node in group.nodes" :key="node.id" @click="selectNode(node.id)">
                <span>{{ typeLabel(node.kind) }}</span>
                <strong>{{ node.label }}</strong>
                <em :class="['relationship-state', `life-${lifecycleBucket(node)}`]">{{ lifecycleLabel(lifecycleBucket(node)) }}</em>
              </button>
            </article>
          </section>
        </div>

        <section class="sources">
          <h3>Source records</h3>
          <div v-if="!sourceIds.length" class="empty">No source refs.</div>
          <div v-for="source in sourceIds" :key="source" class="source-row">
            <code>{{ source }}</code>
          </div>
        </section>
      </section>
    </section>

    <div
      v-if="relationshipGraphExpanded && selectedNode"
      ref="relationshipGraphModalEl"
      class="relationship-graph-modal__backdrop"
      data-testid="relationship-graph-modal"
      tabindex="-1"
      @click.self="relationshipGraphExpanded = false"
      @keydown.esc="relationshipGraphExpanded = false"
    >
      <div class="relationship-graph-modal">
        <div class="relationship-graph-modal__bar">
          <span>Relationships, visualized — {{ selectedNode.label }}</span>
          <button
            type="button"
            data-testid="relationship-graph-modal-close"
            @click="relationshipGraphExpanded = false"
          >Close</button>
        </div>
        <GraphView
          :graph="relationshipGraph"
          :focus-id="selectedNode.id"
          class="relationship-graph-modal__view"
          @update:focus-id="selectNode"
        />
      </div>
    </div>
  </div>
</template>

<style>
@import "./catalog.css";
</style>
