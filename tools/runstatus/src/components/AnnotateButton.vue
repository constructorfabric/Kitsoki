<template>
  <!-- Only render in live mode (not static artifact). -->
  <div v-if="isLive" class="annotate-button">
    <button
      v-if="!open"
      class="annotate-button__trigger"
      data-testid="annotate-button"
      @click="open = true"
    >Annotate</button>

    <form
      v-else
      class="annotate-button__form"
      @submit.prevent="submit"
    >
      <label class="annotate-button__field">
        <span class="annotate-button__label">Score (0–1)</span>
        <input
          v-model="scoreRaw"
          type="number"
          min="0"
          max="1"
          step="0.01"
          placeholder="e.g. 0.9"
          class="annotate-button__input"
          data-testid="annotate-score"
        />
      </label>
      <label class="annotate-button__field">
        <span class="annotate-button__label">Label</span>
        <input
          v-model="label"
          type="text"
          placeholder="e.g. good / bad / off-topic"
          class="annotate-button__input"
          data-testid="annotate-label"
        />
      </label>
      <label class="annotate-button__field">
        <span class="annotate-button__label">Comment</span>
        <textarea
          v-model="comment"
          rows="2"
          placeholder="Free-form commentary…"
          class="annotate-button__textarea"
          data-testid="annotate-comment"
        />
      </label>

      <div class="annotate-button__actions">
        <button
          type="submit"
          class="annotate-button__submit"
          :disabled="submitting"
          data-testid="annotate-submit"
        >{{ submitting ? 'Saving…' : 'Save' }}</button>
        <button
          type="button"
          class="annotate-button__cancel"
          @click="cancel"
        >Cancel</button>
      </div>

      <p v-if="error" class="annotate-button__error">{{ error }}</p>
      <p v-if="saved" class="annotate-button__saved">Annotation saved.</p>
    </form>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from "vue";
import { LiveSource } from "../data/live-source.js";

const props = defineProps<{
  sessionId: string;
  targetCallId?: string;
  targetTurn?: number;
}>();

// Detect live mode: in the static artifact window.__KITSOKI_SNAPSHOT__ is set.
const isLive = computed(() => {
  const win = globalThis as typeof globalThis & { __KITSOKI_SNAPSHOT__?: unknown };
  return win.__KITSOKI_SNAPSHOT__ === undefined;
});

const open = ref(false);
const scoreRaw = ref("");
const label = ref("");
const comment = ref("");
const submitting = ref(false);
const error = ref("");
const saved = ref(false);

function cancel(): void {
  open.value = false;
  scoreRaw.value = "";
  label.value = "";
  comment.value = "";
  error.value = "";
  saved.value = false;
}

async function submit(): Promise<void> {
  error.value = "";
  saved.value = false;
  submitting.value = true;

  try {
    const source = new LiveSource("/");
    const params: {
      targetCallId?: string;
      targetTurn?: number;
      score?: number;
      label?: string;
      comment?: string;
    } = {};
    if (props.targetCallId) params.targetCallId = props.targetCallId;
    if (props.targetTurn !== undefined && props.targetTurn > 0) params.targetTurn = props.targetTurn;
    const scoreNum = parseFloat(scoreRaw.value);
    if (!isNaN(scoreNum)) params.score = Math.min(1, Math.max(0, scoreNum));
    if (label.value.trim()) params.label = label.value.trim();
    if (comment.value.trim()) params.comment = comment.value.trim();

    await source.addAnnotation(props.sessionId, params);
    saved.value = true;
    // Auto-collapse after a short delay so the user sees the confirmation.
    setTimeout(() => {
      cancel();
    }, 1500);
  } catch (e: unknown) {
    error.value = e instanceof Error ? e.message : String(e);
  } finally {
    submitting.value = false;
  }
}
</script>

<style scoped>
.annotate-button {
  margin-top: 0.75rem;
  border-top: 1px solid var(--k-border, #1e293b);
  padding-top: 0.6rem;
}

.annotate-button__trigger {
  background: var(--k-bg-selection, #1e3a5f);
  border: 1px solid var(--k-border, #334155);
  color: var(--k-fg-accent, #93c5fd);
  border-radius: 4px;
  padding: 0.3rem 0.75rem;
  font-size: 0.75rem;
  cursor: pointer;
}
.annotate-button__trigger:hover {
  background: var(--k-button-bg, #1d4ed8);
  border-color: var(--k-border-focus, #3b82f6);
}

.annotate-button__form {
  display: flex;
  flex-direction: column;
  gap: 0.45rem;
}

.annotate-button__field {
  display: flex;
  flex-direction: column;
  gap: 0.15rem;
}

.annotate-button__label {
  font-size: 0.7rem;
  color: var(--k-fg-muted, #64748b);
}

.annotate-button__input,
.annotate-button__textarea {
  background: var(--k-bg-input, #0f172a);
  border: 1px solid var(--k-border, #334155);
  color: var(--k-fg, #e2e8f0);
  border-radius: 4px;
  padding: 0.25rem 0.5rem;
  font-size: 0.75rem;
  width: 100%;
  box-sizing: border-box;
}

.annotate-button__textarea {
  resize: vertical;
}

.annotate-button__actions {
  display: flex;
  gap: 0.5rem;
  margin-top: 0.25rem;
}

.annotate-button__submit {
  background: #16a34a;
  border: 1px solid #15803d;
  color: var(--k-button-fg, #fff);
  border-radius: 4px;
  padding: 0.25rem 0.75rem;
  font-size: 0.75rem;
  cursor: pointer;
}
.annotate-button__submit:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.annotate-button__cancel {
  background: transparent;
  border: 1px solid var(--k-border, #334155);
  color: var(--k-fg-muted, #94a3b8);
  border-radius: 4px;
  padding: 0.25rem 0.75rem;
  font-size: 0.75rem;
  cursor: pointer;
}

.annotate-button__error {
  color: var(--k-error, #f87171);
  font-size: 0.72rem;
  margin: 0;
}

.annotate-button__saved {
  color: var(--k-success, #4ade80);
  font-size: 0.72rem;
  margin: 0;
}
</style>
