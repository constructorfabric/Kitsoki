<template>
  <div class="hbd">
    <!-- host.append_to_file.post -->
    <template v-if="namespace === 'host.append_to_file.post'">
      <!-- Header row: thread chip + title -->
      <div class="hbd__kv">
        <span class="hbd__label">Thread</span>
        <span class="hbd__chip hbd__chip--blue">{{ appendArgs.thread }}</span>
        <span class="hbd__title">{{ appendArgs.title }}</span>
      </div>

      <!-- Phase ID -->
      <div v-if="appendPhaseId" class="hbd__kv">
        <span class="hbd__label">Phase</span>
        <span class="hbd__chip hbd__chip--mono">{{ appendPhaseId }}</span>
      </div>

      <!-- Body block with truncation toggle -->
      <div v-if="appendArgs.body" class="hbd__block">
        <span class="hbd__label hbd__label--block">Body</span>
        <pre class="hbd__pre">{{ bodyDisplay }}</pre>
        <button v-if="isBodyTruncatable" @click="toggleBodyFull" class="hbd__toggle-btn">
          {{ showBodyFull ? 'Show less' : 'Show full' }}
        </button>
      </div>

      <!-- Return row -->
      <div v-if="data !== undefined" class="hbd__kv">
        <span class="hbd__label">Return</span>
        <span class="hbd__badge" :class="appendOk ? 'hbd__badge--ok' : 'hbd__badge--err'">
          {{ appendOk ? '✓' : '✗' }}
        </span>
        <span v-if="appendMessageId" class="hbd__chip hbd__chip--mono">{{ appendMessageId }}</span>
      </div>
    </template>

    <!-- host.local_files.ticket.transition -->
    <template v-else-if="namespace === 'host.local_files.ticket.transition'">
      <!-- Transition row: id → state -->
      <div class="hbd__kv hbd__kv--center">
        <span class="hbd__label">Transition</span>
        <span class="hbd__chip hbd__chip--mono">{{ transitionArgs.id }}</span>
        <span class="hbd__arrow">→</span>
        <span class="hbd__chip hbd__chip--amber">{{ transitionArgs.to }}</span>
      </div>

      <!-- Error block -->
      <div v-if="error !== undefined" class="hbd__block">
        <pre class="hbd__pre hbd__pre--error">{{ typeof error === 'string' ? error : prettyJson(error) }}</pre>
      </div>
    </template>

    <!-- Fallback: generic JSON dump -->
    <template v-else>
      <div v-if="args !== undefined" class="hbd__block">
        <span class="hbd__label hbd__label--block">Args</span>
        <pre class="hbd__pre">{{ prettyJson(args) }}</pre>
      </div>
      <div v-if="data !== undefined" class="hbd__block">
        <span class="hbd__label hbd__label--block">Return</span>
        <pre class="hbd__pre">{{ prettyJson(data) }}</pre>
      </div>
      <div v-if="error !== undefined" class="hbd__block">
        <span class="hbd__label hbd__label--block">Error</span>
        <pre class="hbd__pre hbd__pre--error">{{ prettyJson(error) }}</pre>
      </div>
    </template>

    <!-- Raw toggle (shared) -->
    <div class="hbd__raw-row">
      <button class="hbd__toggle-btn" @click="showRaw = !showRaw">{{ showRaw ? 'Hide raw' : 'Show raw' }}</button>
    </div>
    <template v-if="showRaw">
      <div v-if="args !== undefined" class="hbd__block">
        <span class="hbd__label hbd__label--block">Args</span>
        <pre class="hbd__pre">{{ prettyJson(args) }}</pre>
      </div>
      <div v-if="data !== undefined" class="hbd__block">
        <span class="hbd__label hbd__label--block">Return</span>
        <pre class="hbd__pre">{{ prettyJson(data) }}</pre>
      </div>
      <div v-if="error !== undefined" class="hbd__block">
        <span class="hbd__label hbd__label--block">Error</span>
        <pre class="hbd__pre hbd__pre--error">{{ prettyJson(error) }}</pre>
      </div>
    </template>

    <!-- Duration footer (shared) -->
    <div v-if="durationMs !== null" class="hbd__kv hbd__kv--footer">
      <span class="hbd__label">Duration</span>
      <span class="hbd__muted">{{ durationMs }}ms</span>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from "vue";

const props = defineProps<{
  namespace: string;
  args: unknown;
  data: unknown;
  error: unknown;
  durationMs: number | null;
  incomplete: boolean;
}>();

const BODY_TRUNCATE_LIMIT = 400;
const showRaw = ref(false);

// ── helpers ────────────────────────────────────────────────────────────────

function prettyJson(val: unknown): string {
  return JSON.stringify(val, null, 2);
}

function asRecord(val: unknown): Record<string, unknown> {
  if (val !== null && typeof val === "object" && !Array.isArray(val)) {
    return val as Record<string, unknown>;
  }
  return {};
}

function strField(rec: Record<string, unknown>, key: string): string {
  const v = rec[key];
  return typeof v === "string" ? v : "";
}

// ── host.append_to_file.post ───────────────────────────────────────────────

const appendArgs = computed(() => asRecord(props.args));
const appendBody = computed(() => strField(appendArgs.value, "body"));

const showBodyFull = ref(false);
const isBodyTruncatable = computed(() => appendBody.value.length > BODY_TRUNCATE_LIMIT);

const bodyDisplay = computed(() => {
  if (!showBodyFull.value && isBodyTruncatable.value) {
    return appendBody.value.slice(0, BODY_TRUNCATE_LIMIT) + "…";
  }
  return appendBody.value;
});

function toggleBodyFull(): void {
  showBodyFull.value = !showBodyFull.value;
}

const appendData = computed(() => asRecord(props.data));
const appendOk = computed(() => appendData.value["ok"] === true);
const appendMessageId = computed(() => strField(appendData.value, "message_id"));
const appendPhaseId = computed(() => strField(appendArgs.value, "phase_id"));

// ── host.local_files.ticket.transition ────────────────────────────────────

const transitionArgs = computed(() => asRecord(props.args));
</script>

<style scoped>
.hbd {
  font-size: 0.8125rem;
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
}

/* ── key-value row ────────────────────────────────────────────────────────── */
.hbd__kv {
  display: flex;
  gap: 0.4rem;
  align-items: flex-start;
}

.hbd__kv--center {
  align-items: center;
}

.hbd__kv--footer {
  margin-top: 0.2rem;
  padding-top: 0.35rem;
  border-top: 1px solid #1e293b;
}

/* ── label ───────────────────────────────────────────────────────────────── */
.hbd__label {
  color: #94a3b8;
  font-size: 0.75rem;
  min-width: 5.5rem;
  flex-shrink: 0;
}

.hbd__label--block {
  display: block;
  margin-bottom: 0.2rem;
}

/* ── title (plain text alongside chips) ──────────────────────────────────── */
.hbd__title {
  color: #e2e8f0;
  word-break: break-word;
}

/* ── muted value ─────────────────────────────────────────────────────────── */
.hbd__muted {
  color: #64748b;
  font-size: 0.75rem;
}

/* ── arrow ───────────────────────────────────────────────────────────────── */
.hbd__arrow {
  color: #64748b;
  font-size: 0.8125rem;
}

/* ── chips ───────────────────────────────────────────────────────────────── */
.hbd__chip {
  display: inline-block;
  padding: 0.1rem 0.4rem;
  border-radius: 3px;
  font-size: 0.75rem;
  border: 1px solid transparent;
  white-space: nowrap;
}

.hbd__chip--mono {
  font-family: ui-monospace, monospace;
  font-size: 0.72rem;
  background: #080f1a;
  color: #7dd3fc;
  border-color: #1e293b;
}

.hbd__chip--blue {
  background: #1e3a5f;
  color: #93c5fd;
  border-color: #1e40af;
  font-family: ui-monospace, monospace;
  font-size: 0.72rem;
}

.hbd__chip--amber {
  background: #451a03;
  color: #fbbf24;
  border-color: #92400e;
  font-family: ui-monospace, monospace;
  font-size: 0.72rem;
}

/* ── ok / error badge ────────────────────────────────────────────────────── */
.hbd__badge {
  display: inline-block;
  padding: 0.1rem 0.4rem;
  border-radius: 3px;
  font-size: 0.75rem;
  font-weight: 700;
  border: 1px solid transparent;
}

.hbd__badge--ok {
  background: #14532d;
  color: #86efac;
  border-color: #166534;
}

.hbd__badge--err {
  background: #7f1d1d;
  color: #f87171;
  border-color: #991b1b;
}

/* ── block (label above, pre below) ──────────────────────────────────────── */
.hbd__block {
  margin-top: 0.1rem;
  margin-bottom: 0.25rem;
}

/* ── pre ─────────────────────────────────────────────────────────────────── */
.hbd__pre {
  background: #080f1a;
  border: 1px solid #1e293b;
  border-radius: 4px;
  padding: 0.4rem 0.6rem;
  font-family: ui-monospace, monospace;
  font-size: 0.75rem;
  color: #7dd3fc;
  white-space: pre-wrap;
  word-break: break-word;
  margin: 0;
}

.hbd__pre--error {
  background: #7f1d1d;
  color: #f87171;
  border-color: #991b1b;
}

/* ── raw toggle row ──────────────────────────────────────────────────────── */
.hbd__raw-row {
  margin-top: 0.2rem;
}

/* ── truncation toggle button ────────────────────────────────────────────── */
.hbd__toggle-btn {
  display: block;
  margin-top: 0.3rem;
  background: none;
  border: 1px solid #334155;
  color: #60a5fa;
  cursor: pointer;
  font-size: 0.75rem;
  padding: 0.15rem 0.5rem;
  border-radius: 3px;
}

.hbd__toggle-btn:hover {
  background: #1e293b;
}
</style>
