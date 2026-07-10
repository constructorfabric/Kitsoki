<script setup lang="ts">
// OperatorQuestionModal — surfaces a forwarded agent question to the operator.
//
// When a dispatched agent forwards an AskUserQuestion into kitsoki, the agent
// turn is parked and BLOCKING until we answer (see operatorQuestions store +
// internal/host/operator_ask_bridge.go). This modal renders the active question
// at the head of the queue and submits the operator's selection, which unblocks
// the agent. It is intentionally a hard modal (no dismiss / no backdrop close):
// an agent is waiting, and the only way forward is to answer. Escape and
// backdrop clicks are inert by design.
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import { useOperatorQuestionStore } from "../stores/operatorQuestions.js";
import type { OperatorQuestionFrame } from "../data/live-source.js";

const store = useOperatorQuestionStore();

// Deterministic demo/test driver (mirrors TourOverlay's __startTourWithSteps).
// The operator-question modal only renders when the backend pushes a
// /rpc/questions SSE frame, which needs a real dispatched agent — impossible in
// the no-LLM demo posture. This seam lets the demo spec inject a frame directly
// into the store's onFrame() so the REAL modal renders. Guarded the same way as
// the tour seam: it only ever ENQUEUES a frame the caller already constructed,
// so production behaviour is untouched (nothing calls this in prod). The local
// answer() short-circuit (operatorQuestions store) pairs with this for frames
// whose question_id starts with "demo-".
type OperatorQuestionWindow = typeof window & {
  __pushOperatorQuestion?: (frameJson: string) => void;
};

onMounted(() => {
  const win = window as OperatorQuestionWindow;
  win.__pushOperatorQuestion = (frameJson: string) => {
    try {
      const frame = JSON.parse(frameJson) as OperatorQuestionFrame;
      store.onFrame(frame);
    } catch {
      /* malformed JSON — ignore (deterministic test/demo driver only) */
    }
  };
});

onUnmounted(() => {
  const win = window as OperatorQuestionWindow;
  delete win.__pushOperatorQuestion;
});

const active = computed(() => store.active);
// pending count beyond the active one, surfaced as a "N more queued" hint.
const morePending = computed(() => Math.max(0, store.pending - 1));

// selections[questionText] = chosen label/custom text (single) or labels (multi).
// Reset whenever the active question_id changes so a fresh frame starts clean.
const selections = ref<Record<string, string | string[]>>({});
const customSelected = ref<Record<string, boolean>>({});
const customDrafts = ref<Record<string, string>>({});

watch(
  () => active.value?.question_id,
  () => {
    selections.value = {};
    customSelected.value = {};
    customDrafts.value = {};
  }
);

/** Single-select: record the chosen label. */
function selectOne(questionText: string, label: string): void {
  customSelected.value[questionText] = false;
  selections.value[questionText] = label;
}

/** True when this single-select option is the current pick. */
function isPicked(questionText: string, label: string): boolean {
  return !customSelected.value[questionText] && selections.value[questionText] === label;
}

/** Single-select: allow an answer outside the model-provided choices. */
function selectCustom(questionText: string): void {
  customSelected.value[questionText] = true;
  selections.value[questionText] = (customDrafts.value[questionText] || "").trim();
}

function updateCustom(questionText: string, event: Event): void {
  const value = (event.target as HTMLInputElement).value;
  customDrafts.value[questionText] = value;
  if (customSelected.value[questionText]) {
    selections.value[questionText] = value.trim();
  }
}

function isCustomPicked(questionText: string): boolean {
  return customSelected.value[questionText] === true;
}

/** Multi-select: toggle the label in/out of the list. */
function toggleMany(questionText: string, label: string): void {
  const cur = selections.value[questionText];
  const list = Array.isArray(cur) ? [...cur] : [];
  const idx = list.indexOf(label);
  if (idx >= 0) list.splice(idx, 1);
  else list.push(label);
  selections.value[questionText] = list;
}

/** True when this multi-select option is checked. */
function isChecked(questionText: string, label: string): boolean {
  const cur = selections.value[questionText];
  return Array.isArray(cur) && cur.includes(label);
}

// Every question must have a non-empty selection before we can submit.
const canSubmit = computed(() => {
  const frame = active.value;
  if (!frame) return false;
  return frame.questions.every((q) => {
    const sel = selections.value[q.question];
    if (q.multiSelect) return Array.isArray(sel) && sel.length > 0;
    return typeof sel === "string" && sel.trim().length > 0;
  });
});

async function submit(): Promise<void> {
  if (!canSubmit.value || store.submitting) return;
  // Build the answer map: question text → label (single) or labels (multi).
  const answers: Record<string, string | string[]> = {};
  for (const q of active.value!.questions) {
    answers[q.question] = selections.value[q.question]!;
  }
  await store.answer(answers);
}
</script>

<template>
  <Teleport to="body">
    <div
      v-if="active"
      class="oq-backdrop"
      role="dialog"
      aria-modal="true"
      data-testid="operator-question-modal"
    >
      <div class="oq-panel">
        <header class="oq-header">
          <span class="oq-glyph">🤖</span>
          <span class="oq-title">The agent has a question</span>
          <span v-if="morePending > 0" class="oq-more">
            +{{ morePending }} more queued
          </span>
        </header>
        <div class="oq-body">
          <fieldset
            v-for="(q, qi) in active.questions"
            :key="qi"
            class="oq-question"
          >
            <legend class="oq-q-text">
              {{ q.question }}
              <span v-if="q.multiSelect" class="oq-multi-hint">(choose any)</span>
            </legend>
            <div class="oq-options">
              <button
                v-for="(opt, oi) in q.options"
                :key="oi"
                type="button"
                class="oq-option"
                :class="{
                  'oq-option--picked': q.multiSelect
                    ? isChecked(q.question, opt.label)
                    : isPicked(q.question, opt.label),
                }"
                :data-testid="`oq-option-${qi}-${oi}`"
                @click="
                  q.multiSelect
                    ? toggleMany(q.question, opt.label)
                    : selectOne(q.question, opt.label)
                "
              >
                <span class="oq-option__mark">
                  {{
                    (q.multiSelect ? isChecked(q.question, opt.label) : isPicked(q.question, opt.label))
                      ? (q.multiSelect ? "☑" : "◉")
                      : (q.multiSelect ? "☐" : "○")
                  }}
                </span>
                <span class="oq-option__text">
                  <span class="oq-option__label">{{ opt.label }}</span>
                  <span v-if="opt.description" class="oq-option__desc">{{ opt.description }}</span>
                </span>
              </button>
              <button
                v-if="!q.multiSelect"
                type="button"
                class="oq-option oq-option--custom"
                :class="{ 'oq-option--picked': isCustomPicked(q.question) }"
                :data-testid="`oq-option-${qi}-custom`"
                @click="selectCustom(q.question)"
              >
                <span class="oq-option__mark">{{ isCustomPicked(q.question) ? "◉" : "○" }}</span>
                <span class="oq-option__text">
                  <span class="oq-option__label">Custom answer</span>
                  <span class="oq-option__desc">Send a short answer that is not listed above.</span>
                </span>
              </button>
              <label
                v-if="!q.multiSelect && isCustomPicked(q.question)"
                class="oq-custom-answer"
              >
                <span class="oq-custom-answer__label">Answer</span>
                <input
                  class="oq-custom-answer__input"
                  :data-testid="`oq-custom-answer-${qi}`"
                  type="text"
                  :value="customDrafts[q.question] || ''"
                  @input="updateCustom(q.question, $event)"
                />
              </label>
            </div>
          </fieldset>
        </div>
        <footer class="oq-footer">
          <span class="oq-wait-note">An agent is waiting on your answer.</span>
          <button
            type="button"
            class="oq-submit"
            data-testid="oq-submit"
            :disabled="!canSubmit || store.submitting"
            @click="submit"
          >
            {{ store.submitting ? "Sending…" : "Send answer" }}
          </button>
        </footer>
      </div>
    </div>
  </Teleport>
</template>

<style scoped>
.oq-backdrop {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.55);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 1200; /* above toasts (1100) — an agent is blocked on this */
}

.oq-panel {
  background: var(--k-bg-widget, #0d1b2a);
  border: 1px solid var(--k-border, #1e3a5f);
  border-radius: 10px;
  box-shadow: 0 8px 40px rgba(0, 0, 0, 0.5);
  display: flex;
  flex-direction: column;
  width: min(540px, 92vw);
  max-height: 85vh;
  overflow: hidden;
  color: var(--k-fg, #e2e8f0);
}

.oq-header {
  display: flex;
  align-items: center;
  gap: 0.55rem;
  padding: 0.85rem 1.1rem;
  border-bottom: 1px solid var(--k-border, #1e3a5f);
  background: var(--k-bg-deep, #0a1521);
  flex-shrink: 0;
}
.oq-glyph {
  font-size: 1.05rem;
}
.oq-title {
  font-size: 0.9rem;
  font-weight: 600;
}
.oq-more {
  margin-left: auto;
  font-size: 0.72rem;
  color: var(--k-fg-muted, #94a3b8);
}

.oq-body {
  overflow-y: auto;
  padding: 1rem 1.1rem;
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 1.1rem;
}

.oq-question {
  border: none;
  margin: 0;
  padding: 0;
}
.oq-q-text {
  font-size: 0.85rem;
  font-weight: 600;
  color: var(--k-fg, #e2e8f0);
  padding: 0;
  margin-bottom: 0.5rem;
}
.oq-multi-hint {
  font-weight: 400;
  color: var(--k-fg-muted, #94a3b8);
  font-size: 0.72rem;
  margin-left: 0.35rem;
}

.oq-options {
  display: flex;
  flex-direction: column;
  gap: 0.4rem;
}
.oq-option {
  display: flex;
  align-items: flex-start;
  gap: 0.55rem;
  text-align: left;
  background: var(--k-bg-input, #11243a);
  border: 1px solid var(--k-border, #1e3a5f);
  border-radius: 7px;
  padding: 0.55rem 0.7rem;
  cursor: pointer;
  color: inherit;
  font: inherit;
}
.oq-option:hover {
  border-color: var(--k-border-focus, #2563eb);
}
.oq-option--picked {
  border-color: var(--k-border-focus, #2563eb);
  background: var(--k-bg-selection, #15314f);
}
.oq-option--custom {
  border-style: dashed;
}
.oq-option__mark {
  font-size: 0.95rem;
  line-height: 1.3;
  color: var(--k-fg-accent, #60a5fa);
  flex-shrink: 0;
}
.oq-option__text {
  display: flex;
  flex-direction: column;
  min-width: 0;
}
.oq-option__label {
  font-size: 0.82rem;
  font-weight: 500;
}
.oq-option__desc {
  font-size: 0.72rem;
  color: var(--k-fg-muted, #94a3b8);
  margin-top: 0.1rem;
}
.oq-custom-answer {
  display: flex;
  align-items: center;
  gap: 0.45rem;
  margin-top: 0.15rem;
}
.oq-custom-answer__label {
  color: var(--k-fg-muted, #94a3b8);
  font-size: 0.72rem;
  font-weight: 600;
}
.oq-custom-answer__input {
  flex: 1;
  min-width: 0;
  background: var(--k-bg-input, #11243a);
  border: 1px solid var(--k-border-focus, #2563eb);
  border-radius: 6px;
  color: var(--k-fg, #e2e8f0);
  font: inherit;
  font-size: 0.78rem;
  padding: 0.42rem 0.55rem;
}
.oq-custom-answer__input:focus {
  outline: 2px solid color-mix(in srgb, var(--k-border-focus, #2563eb) 35%, transparent);
  outline-offset: 1px;
}

.oq-footer {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  padding: 0.75rem 1.1rem;
  border-top: 1px solid var(--k-border, #1e3a5f);
  background: var(--k-bg-deep, #0a1521);
  flex-shrink: 0;
}
.oq-wait-note {
  font-size: 0.72rem;
  color: var(--k-fg-muted, #94a3b8);
}
.oq-submit {
  margin-left: auto;
  background: var(--k-button-bg, #2563eb);
  border: none;
  border-radius: 6px;
  color: var(--k-button-fg, #fff);
  font: inherit;
  font-size: 0.8rem;
  font-weight: 600;
  padding: 0.45rem 0.95rem;
  cursor: pointer;
}
.oq-submit:hover:not(:disabled) {
  background: var(--k-button-hover-bg, #1d4ed8);
}
.oq-submit:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
</style>
