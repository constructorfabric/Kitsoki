<script setup lang="ts">
/**
 * OracleWorkbench — for the selected room, renders one card per host.oracle.*
 * contract (runstatus.editor.oracles). Each card shows the verb / prompt /
 * schema, embeds a CassetteBrowser keyed by the contract's CassetteKey, and
 * offers replay buttons:
 *   - "Replay (cassette)": cassette-override replay against the selected
 *     episode (or the contract default) → renders output + updates the
 *     StoryViewer world snapshot via `replay`.
 *   - "Replay (live)": disabled placeholder — live replay requires a session,
 *     which the per-story editor surface does not have.
 *
 * The recorded output is shown in a lightweight pre block (the editor surface
 * mirrors OracleDetail's intent: show the recorded oracle output for review).
 */
import { ref, watch } from "vue";
import type {
  OracleContract,
  CassetteEpisodeSummary,
  ReplayResult,
} from "../../data/editor.js";
import type { LiveSource } from "../../data/live-source.js";
import CassetteBrowser from "./CassetteBrowser.vue";

const props = defineProps<{
  source: LiveSource;
  storyPath: string;
  roomId: string;
}>();

const emit = defineEmits<{
  /** A replay produced a world snapshot the StoryViewer should show. */
  (e: "replay", payload: { world: Record<string, unknown>; output: unknown }): void;
}>();

const contracts = ref<OracleContract[]>([]);
const loading = ref(false);
const error = ref("");

// Per-contract (by index) selected cassette episode + replay output.
const selectedEpisode = ref<Record<number, CassetteEpisodeSummary>>({});
const replayOutput = ref<Record<number, ReplayResult>>({});
const replayError = ref<Record<number, string>>({});

async function load(): Promise<void> {
  loading.value = true;
  error.value = "";
  selectedEpisode.value = {};
  replayOutput.value = {};
  replayError.value = {};
  try {
    const res = await props.source.editorOracles(props.storyPath, props.roomId);
    contracts.value = res.contracts ?? [];
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e);
    contracts.value = [];
  } finally {
    loading.value = false;
  }
}

function onCassetteSelect(idx: number, ep: CassetteEpisodeSummary): void {
  selectedEpisode.value = { ...selectedEpisode.value, [idx]: ep };
}

async function replayCassette(idx: number): Promise<void> {
  replayError.value = { ...replayError.value, [idx]: "" };
  const ep = selectedEpisode.value[idx];
  try {
    const res = await props.source.editorReplay(
      props.storyPath,
      props.roomId,
      idx,
      ep?.cassette_file
    );
    replayOutput.value = { ...replayOutput.value, [idx]: res };
    emit("replay", { world: res.world_snapshot ?? {}, output: res.output });
  } catch (e) {
    replayError.value = {
      ...replayError.value,
      [idx]: e instanceof Error ? e.message : String(e),
    };
  }
}

function fmtOutput(v: unknown): string {
  if (typeof v === "string") return v;
  return JSON.stringify(v, null, 2);
}

watch(
  () => [props.storyPath, props.roomId] as const,
  () => load(),
  { immediate: true }
);
</script>

<template>
  <section class="workbench" data-testid="editor-oracle-workbench">
    <h3 class="workbench__title">Oracle workbench</h3>
    <div v-if="loading" class="workbench__status">Loading oracle contracts…</div>
    <div v-else-if="error" class="workbench__status workbench__status--error">{{ error }}</div>
    <p v-else-if="contracts.length === 0" class="workbench__empty">
      This room makes no oracle calls.
    </p>
    <div v-else class="workbench__cards">
      <div
        v-for="(c, idx) in contracts"
        :key="idx"
        class="workbench__card"
        data-testid="editor-oracle-card"
      >
        <div class="workbench__card-head">
          <span class="workbench__kind">{{ c.kind }}</span>
          <span v-if="c.cassette_key.call" class="workbench__call">#{{ c.cassette_key.call }}</span>
        </div>
        <dl class="workbench__fields">
          <template v-if="c.prompt_path">
            <dt>prompt</dt>
            <dd>{{ c.prompt_path }}</dd>
          </template>
          <template v-if="c.output_schema">
            <dt>schema</dt>
            <dd>{{ c.output_schema }}</dd>
          </template>
        </dl>

        <div class="workbench__actions">
          <button
            class="workbench__btn"
            data-testid="editor-oracle-replay-cassette"
            @click="replayCassette(idx)"
          >Replay (cassette)</button>
          <button
            class="workbench__btn workbench__btn--disabled"
            data-testid="editor-oracle-replay-live"
            disabled
            title="Live replay requires an active session"
          >Replay (live)</button>
        </div>

        <div v-if="replayError[idx]" class="workbench__status workbench__status--error">
          {{ replayError[idx] }}
        </div>
        <div
          v-if="replayOutput[idx]"
          class="workbench__output"
          data-testid="editor-oracle-output"
        >
          <pre>{{ fmtOutput(replayOutput[idx].output) }}</pre>
        </div>

        <CassetteBrowser
          :source="source"
          :story-path="storyPath"
          :cassette-key="c.cassette_key"
          @select="(ep) => onCassetteSelect(idx, ep)"
        />
      </div>
    </div>
  </section>
</template>

<style scoped>
.workbench__title {
  margin: 0 0 0.5rem;
  font-size: 0.8rem;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  opacity: 0.7;
}
.workbench__status {
  opacity: 0.7;
  font-size: 0.85rem;
}
.workbench__status--error { color: #f28b82; }
.workbench__empty {
  opacity: 0.6;
  font-style: italic;
}
.workbench__cards {
  display: flex;
  flex-direction: column;
  gap: 0.6rem;
}
.workbench__card {
  border: 1px solid var(--border, #2a2d35);
  border-radius: 6px;
  padding: 0.55rem 0.6rem;
}
.workbench__card-head {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  margin-bottom: 0.3rem;
}
.workbench__kind {
  font-family: monospace;
  font-weight: 600;
  color: #b39ddb;
}
.workbench__call {
  font-family: monospace;
  opacity: 0.7;
  font-size: 0.8rem;
}
.workbench__fields {
  margin: 0 0 0.4rem;
  display: grid;
  grid-template-columns: max-content 1fr;
  gap: 0.1rem 0.6rem;
  font-size: 0.82rem;
}
.workbench__fields dt { opacity: 0.65; }
.workbench__fields dd { margin: 0; font-family: monospace; word-break: break-all; }
.workbench__actions {
  display: flex;
  gap: 0.4rem;
  margin-bottom: 0.4rem;
}
.workbench__btn {
  background: #2d4a63;
  color: inherit;
  border: none;
  border-radius: 4px;
  padding: 0.25rem 0.6rem;
  cursor: pointer;
  font-size: 0.82rem;
}
.workbench__btn--disabled {
  background: #2a2d35;
  opacity: 0.5;
  cursor: not-allowed;
}
.workbench__output {
  border: 1px solid var(--border, #2a2d35);
  border-radius: 4px;
  padding: 0.4rem;
  margin-bottom: 0.4rem;
}
.workbench__output pre {
  margin: 0;
  white-space: pre-wrap;
  word-break: break-word;
  font-size: 0.8rem;
}
</style>
