<template>
  <div class="tdiff" data-testid="transcript-diff">
    <div class="tdiff__control" data-testid="transcript-diff-control">
      <span class="tdiff__title">Cassette vs live</span>
      <button
        v-if="!live"
        class="tdiff__btn"
        data-testid="transcript-diff-rerun"
        :disabled="rerunning || !canRerun"
        :title="canRerun ? 'Re-run this call live and diff the tool-call path against the cassette' : 'Live re-run unavailable offline'"
        @click="$emit('rerun')"
      >{{ rerunning ? 'Re-running…' : 'Re-run live & diff' }}</button>
      <span v-else class="tdiff__status" :class="diff.identical ? 'tdiff__status--ok' : 'tdiff__status--drift'">
        {{ diff.identical ? 'No drift' : 'Drift detected' }}
      </span>
    </div>

    <!-- Honest degradation: deterministic replay produces NO live transcript to
         diff, so we say so explicitly rather than fabricate an empty diff. -->
    <div
      v-if="!live"
      class="tdiff__identical"
      data-testid="transcript-diff-identical"
    >
      No live run to compare — replay is byte-identical to the cassette.
    </div>

    <div v-else class="tdiff__rows">
      <div
        v-for="(r, i) in diff.rows"
        :key="i"
        class="tdiff__row"
        :class="`tdiff__row--${r.status}`"
        :data-testid="'transcript-diff-row'"
        :data-status="r.status"
      >
        <span class="tdiff__sign">{{ sign(r.status) }}</span>
        <span class="tdiff__row-title">{{ r.title }}</span>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from "vue";
import {
  diffToolPaths,
  type TranscriptData,
  type DiffRow,
} from "../../data/transcript.js";

const props = defineProps<{
  recorded: TranscriptData;
  /** The fresh live transcript to diff against; undefined → honest no-compare state. */
  live?: TranscriptData | null;
  rerunning?: boolean;
  /** Whether a live re-run is even possible (false offline / in a snapshot). */
  canRerun?: boolean;
}>();

defineEmits<{ (e: "rerun"): void }>();

const diff = computed(() =>
  props.live
    ? diffToolPaths(props.recorded, props.live)
    : { identical: true, rows: [] as DiffRow[] }
);

function sign(status: DiffRow["status"]): string {
  switch (status) {
    case "added":
      return "+";
    case "removed":
      return "−";
    default:
      return "=";
  }
}
</script>

<style scoped>
.tdiff {
  display: flex;
  flex-direction: column;
  gap: 0.3rem;
  border: 1px solid #1e293b;
  border-radius: 4px;
  padding: 0.4rem 0.5rem;
  background: #080f1a;
}

.tdiff__control {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}

.tdiff__title {
  color: #64748b;
  font-size: 0.7rem;
  text-transform: uppercase;
  letter-spacing: 0.04em;
}

.tdiff__btn {
  background: #1e293b;
  border: 1px solid #334155;
  color: #60a5fa;
  cursor: pointer;
  font-size: 0.7rem;
  padding: 0.15rem 0.5rem;
  border-radius: 3px;
}

.tdiff__btn:hover:not(:disabled) {
  background: #334155;
}

.tdiff__btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.tdiff__status {
  font-size: 0.7rem;
  font-weight: 600;
  padding: 0.1rem 0.4rem;
  border-radius: 3px;
}

.tdiff__status--ok    { background: #052e16; color: #86efac; }
.tdiff__status--drift { background: #2d0707; color: #fca5a5; }

.tdiff__identical {
  color: #64748b;
  font-size: 0.72rem;
  font-style: italic;
}

.tdiff__rows {
  display: flex;
  flex-direction: column;
  gap: 0.1rem;
}

.tdiff__row {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  font-family: ui-monospace, monospace;
  font-size: 0.7rem;
  padding: 0.1rem 0.3rem;
  border-radius: 2px;
}

.tdiff__row--same    { color: #94a3b8; }
.tdiff__row--added   { color: #86efac; background: #052e16; }
.tdiff__row--removed { color: #fca5a5; background: #2d0707; }

.tdiff__sign {
  min-width: 0.8rem;
  text-align: center;
}
</style>
