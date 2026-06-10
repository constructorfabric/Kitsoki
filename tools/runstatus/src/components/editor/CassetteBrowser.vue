<script setup lang="ts">
/**
 * CassetteBrowser — lists the cassette episodes matching one oracle contract's
 * CassetteKey (runstatus.editor.cassettes). Each row shows the input digest +
 * output preview; clicking a row selects-and-expands it and emits `select` with
 * the episode so the parent can drive a cassette-override replay (which feeds
 * the StoryViewer's world snapshot).
 */
import { ref, watch } from "vue";
import type {
  CassetteKey,
  CassetteEpisodeSummary,
} from "../../data/editor.js";
import type { LiveSource } from "../../data/live-source.js";

const props = defineProps<{
  source: LiveSource;
  storyPath: string;
  cassetteKey: CassetteKey;
}>();

const emit = defineEmits<{
  (e: "select", ep: CassetteEpisodeSummary): void;
}>();

const episodes = ref<CassetteEpisodeSummary[]>([]);
const loading = ref(false);
const error = ref("");
const expanded = ref<string>(""); // episode_id of the expanded row

function rowKey(ep: CassetteEpisodeSummary): string {
  return `${ep.cassette_file}::${ep.episode_id}`;
}

async function load(): Promise<void> {
  loading.value = true;
  error.value = "";
  try {
    episodes.value = await props.source.editorCassettes(
      props.storyPath,
      props.cassetteKey
    );
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e);
    episodes.value = [];
  } finally {
    loading.value = false;
  }
}

function onSelect(ep: CassetteEpisodeSummary): void {
  const k = rowKey(ep);
  expanded.value = expanded.value === k ? "" : k;
  emit("select", ep);
}

watch(
  () => [props.storyPath, props.cassetteKey] as const,
  () => load(),
  { immediate: true, deep: true }
);
</script>

<template>
  <div class="cassettes" data-testid="editor-cassette-list">
    <div v-if="loading" class="cassettes__status">Loading cassettes…</div>
    <div v-else-if="error" class="cassettes__status cassettes__status--error">{{ error }}</div>
    <p v-else-if="episodes.length === 0" class="cassettes__status">
      No matching cassette episodes.
    </p>
    <ul v-else class="cassettes__rows">
      <li
        v-for="ep in episodes"
        :key="rowKey(ep)"
        class="cassettes__row"
        :class="{ 'cassettes__row--active': expanded === rowKey(ep) }"
        data-testid="editor-cassette-item"
        @click="onSelect(ep)"
      >
        <div class="cassettes__row-head">
          <span class="cassettes__id">{{ ep.episode_id || "(episode)" }}</span>
          <span class="cassettes__digest">{{ ep.input_digest }}</span>
        </div>
        <div v-if="expanded === rowKey(ep)" class="cassettes__expand" data-testid="editor-cassette-expand">
          <div class="cassettes__file">{{ ep.cassette_file }}</div>
          <pre class="cassettes__preview">{{ ep.output_preview }}</pre>
        </div>
      </li>
    </ul>
  </div>
</template>

<style scoped>
.cassettes__status {
  opacity: 0.7;
  font-size: 0.85rem;
  padding: 0.3rem 0;
}
.cassettes__status--error { color: #f28b82; }
.cassettes__rows {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 0.3rem;
}
.cassettes__row {
  border: 1px solid var(--border, #2a2d35);
  border-radius: 6px;
  padding: 0.4rem 0.5rem;
  cursor: pointer;
}
.cassettes__row--active {
  border-color: #6db3f2;
}
.cassettes__row-head {
  display: flex;
  justify-content: space-between;
  gap: 0.5rem;
  font-size: 0.85rem;
}
.cassettes__id {
  font-family: monospace;
  font-weight: 600;
}
.cassettes__digest {
  opacity: 0.6;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 60%;
}
.cassettes__expand {
  margin-top: 0.4rem;
  border-top: 1px dashed var(--border, #2a2d35);
  padding-top: 0.4rem;
}
.cassettes__file {
  font-family: monospace;
  font-size: 0.75rem;
  opacity: 0.6;
  margin-bottom: 0.3rem;
  word-break: break-all;
}
.cassettes__preview {
  margin: 0;
  white-space: pre-wrap;
  word-break: break-word;
  font-size: 0.8rem;
}
</style>
