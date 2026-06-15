<template>
  <!-- Only render when the event has a call_id and a replayable verb. -->
  <div v-if="isReplayable" class="replay-button">
    <button
      v-if="!result && !loading"
      class="replay-button__trigger"
      data-testid="replay-button"
      @click="replay"
    >Replay</button>

    <span v-if="loading" class="replay-button__loading" data-testid="replay-loading">
      Replaying…
    </span>

    <div v-if="result" class="replay-button__result" data-testid="replay-result">
      <span class="replay-button__verb">{{ result.original_verb }}</span>
      <span
        class="replay-button__badge"
        :class="result.replayable ? 'replay-button__badge--ok' : 'replay-button__badge--err'"
      >{{ result.replayable ? 'replayable' : 'not replayable' }}</span>
      <span v-if="result.note" class="replay-button__note">{{ result.note }}</span>
      <button
        class="replay-button__reset"
        @click="reset"
        title="Clear"
      >✕</button>
    </div>

    <p v-if="error" class="replay-button__error" data-testid="replay-error">{{ error }}</p>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from "vue";
import type { TraceEvent, ReplayResult } from "../../types.js";
import { LiveSource } from "../../data/live-source.js";

const props = defineProps<{
  event: TraceEvent;
  sessionId?: string;
}>();

// Only oracle.call.complete events with a call_id and a supported verb get the
// button. task and converse are excluded in v1 (side effects).
const UNSUPPORTED_VERBS = new Set(["task", "converse"]);

const isReplayable = computed(() => {
  if (props.event.msg !== "oracle.call.complete") return false;
  const callId = props.event.attrs.call_id;
  if (!callId || typeof callId !== "string") return false;
  const verb = props.event.attrs.verb;
  if (typeof verb === "string" && UNSUPPORTED_VERBS.has(verb)) return false;
  return true;
});

const loading = ref(false);
const result = ref<ReplayResult | null>(null);
const error = ref("");

function reset(): void {
  result.value = null;
  error.value = "";
}

async function replay(): Promise<void> {
  error.value = "";
  loading.value = true;
  try {
    const sessionId =
      props.sessionId || props.event.session_id || "";
    const callId = props.event.attrs.call_id as string;
    const source = new LiveSource("/");
    const res = await source.replayCall(sessionId, callId, "claude");
    result.value = res;
  } catch (e: unknown) {
    error.value = e instanceof Error ? e.message : String(e);
  } finally {
    loading.value = false;
  }
}
</script>

<style scoped>
.replay-button {
  margin-top: 0.75rem;
  border-top: 1px solid var(--k-border, #1e293b);
  padding-top: 0.6rem;
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
}

.replay-button__trigger {
  background: var(--k-button-bg, #1a1a3e);
  border: 1px solid var(--k-border-focus, #4c4cf0);
  color: var(--k-button-fg, #a5b4fc);
  border-radius: 4px;
  padding: 0.3rem 0.75rem;
  font-size: 0.75rem;
  cursor: pointer;
  align-self: flex-start;
}
.replay-button__trigger:hover {
  background: var(--k-button-hover-bg, #2d2db5);
  border-color: var(--k-border-focus, #818cf8);
  color: var(--k-button-fg, #e0e7ff);
}

.replay-button__loading {
  font-size: 0.72rem;
  color: var(--k-fg-muted, #64748b);
}

.replay-button__result {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  flex-wrap: wrap;
}

.replay-button__verb {
  font-family: ui-monospace, monospace;
  font-size: 0.72rem;
  color: #94a3b8;
  background: var(--k-bg-input, #1e293b);
  padding: 0.1rem 0.35rem;
  border-radius: 3px;
}

.replay-button__badge {
  font-size: 0.7rem;
  font-weight: 600;
  padding: 0.1rem 0.4rem;
  border-radius: 3px;
}

.replay-button__badge--ok {
  background: var(--k-success-bg, #14532d);
  border: 1px solid #16a34a;
  color: var(--k-success, #4ade80);
}

.replay-button__badge--err {
  background: #450a0a;
  border: 1px solid #dc2626;
  color: var(--k-error, #f87171);
}

.replay-button__note {
  font-size: 0.7rem;
  color: var(--k-fg-muted, #64748b);
  font-style: italic;
}

.replay-button__reset {
  background: none;
  border: none;
  color: var(--k-fg-subtle, #475569);
  cursor: pointer;
  font-size: 0.75rem;
  padding: 0.1rem 0.2rem;
  border-radius: 3px;
  line-height: 1;
  margin-left: auto;
}
.replay-button__reset:hover {
  color: #94a3b8;
}

.replay-button__error {
  color: var(--k-error, #f87171);
  font-size: 0.72rem;
  margin: 0;
}
</style>
