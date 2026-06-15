<template>
  <div class="decide-detail">

    <!-- ── TOP: Verdict block (always visible) ─────────────────────────────── -->
    <div class="decide-detail__verdict" data-testid="decide-verdict">

      <!-- Chosen intent chip -->
      <div v-if="decision" class="decide-detail__winner-row">
        <span class="od-label">Decision</span>
        <span class="decide-detail__winner-chip">{{ decision }}</span>
        <span v-if="bailedToHuman" class="decide-detail__bail-badge">bailed to human</span>
      </div>

      <!-- Confidence bar -->
      <div v-if="confidence !== null" class="decide-detail__conf-row">
        <span class="od-label">Confidence</span>
        <ConfidenceBar
          :confidence="confidence"
          :threshold="threshold"
        />
      </div>

      <!-- Available choices: chosen highlighted, others dimmed -->
      <div v-if="choices.length" class="decide-detail__choices-row">
        <span class="od-label">Choices</span>
        <div class="decide-detail__choice-chips">
          <div
            v-for="c in choices"
            :key="c.id"
            class="decide-detail__choice"
            :class="{ 'decide-detail__choice--selected': c.id === decision }"
            :title="c.description"
          >
            <span class="decide-detail__choice-id">{{ c.id }}</span>
            <span v-if="c.description" class="decide-detail__choice-desc">{{ c.description }}</span>
          </div>
        </div>
      </div>

      <!-- Alternatives (if present — Slice #4 populates this) -->
      <div v-if="alternatives.length" class="decide-detail__alternatives">
        <span class="od-label">Alternatives</span>
        <div class="decide-detail__alt-list">
          <div v-for="alt in alternatives" :key="alt.id" class="decide-detail__alt">
            <span class="decide-detail__alt-id">{{ alt.id }}</span>
            <ConfidenceBar
              v-if="alt.score !== undefined"
              :confidence="alt.score"
              :threshold="threshold"
              class="decide-detail__alt-bar"
            />
          </div>
        </div>
      </div>

      <!-- Reason text -->
      <div v-if="reason" class="decide-detail__reason-row">
        <span class="od-label">Reason</span>
        <pre class="decide-detail__reason">{{ reason }}</pre>
      </div>
    </div>

    <!-- ── REPLAY: Re-run this decision against a different operator ──────── -->
    <ReplayButton :event="event" :session-id="sessionId" />

    <!-- ── BOTTOM: Collapsed evidence drawer ───────────────────────────────── -->
    <div class="decide-detail__evidence" data-testid="decide-evidence">
      <button
        class="decide-detail__evidence-toggle"
        data-testid="decide-evidence-toggle"
        @click="evidenceOpen = !evidenceOpen"
      >
        <span class="decide-detail__evidence-arrow" :class="{ 'decide-detail__evidence-arrow--open': evidenceOpen }">▶</span>
        Show evidence (prompt · response)
      </button>

      <div v-if="evidenceOpen" class="decide-detail__evidence-body">
        <CollapsibleText label="System Prompt" :text="systemPrompt" />
        <CollapsibleText label="Prompt" :text="prompt" />

        <div v-if="responseJson !== null" class="decide-detail__response">
          <div class="decide-detail__tabs">
            <span class="od-label">Response</span>
            <div class="decide-detail__tab-row">
              <button
                class="decide-detail__tab"
                :class="{ 'decide-detail__tab--active': responseTab === 'object' }"
                @click="responseTab = 'object'"
              >Object</button>
              <button
                class="decide-detail__tab"
                :class="{ 'decide-detail__tab--active': responseTab === 'raw' }"
                @click="responseTab = 'raw'"
              >Raw JSON</button>
            </div>
            <button class="decide-detail__popout" title="Pop out" @click="openModal">⤢</button>
          </div>
          <div class="decide-detail__response-body">
            <div v-if="responseTab === 'object'" class="decide-detail__viewer">
              <JsonViewer :value="responseJson" :default-open="true" />
            </div>
            <pre v-else class="od-pre">{{ prettyJson(responseJson) }}</pre>
          </div>
        </div>
      </div>
    </div>
  </div>

  <Teleport to="body">
    <div v-if="modalOpen" class="jv-modal-backdrop" @click.self="closeModal">
      <div class="jv-modal">
        <div class="jv-modal__header">
          <div class="jv-modal__tab-row">
            <button
              class="decide-detail__tab"
              :class="{ 'decide-detail__tab--active': modalTab === 'object' }"
              @click="modalTab = 'object'"
            >Object</button>
            <button
              class="decide-detail__tab"
              :class="{ 'decide-detail__tab--active': modalTab === 'raw' }"
              @click="modalTab = 'raw'"
            >Raw JSON</button>
          </div>
          <button class="jv-modal__close" title="Close (Esc)" @click="closeModal">✕</button>
        </div>
        <div class="jv-modal__body">
          <div v-if="modalTab === 'object'" class="decide-detail__viewer">
            <JsonViewer :value="responseJson" :default-open="true" />
          </div>
          <pre v-else class="od-pre">{{ prettyJson(responseJson) }}</pre>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from "vue";
import type { TraceEvent } from "../../types.js";
import { prettyJson } from "./lib.js";
import CollapsibleText from "./CollapsibleText.vue";
import JsonViewer from "./JsonViewer.vue";
import ConfidenceBar from "./ConfidenceBar.vue";
import { usePromptLoader } from "./usePromptLoader.js";
import ReplayButton from "./ReplayButton.vue";

const props = defineProps<{
  event: TraceEvent;
  /** Optional session id for the replay RPC. Falls back to event.session_id. */
  sessionId?: string;
}>();

const attrs = computed(() => props.event.attrs);

interface Choice {
  id: string;
  description?: string;
}

interface Alternative {
  id: string;
  score?: number;
}

const choices = computed<Choice[]>(() => {
  const c = attrs.value.input as Record<string, unknown> | undefined;
  const arr = c?.choices ?? (c as Record<string, unknown> | undefined)?.available_intents;
  if (!Array.isArray(arr)) return [];
  return arr.map((item) => {
    if (typeof item === "string") return { id: item };
    const obj = item as Record<string, unknown>;
    return {
      id: String(obj.id ?? obj.name ?? JSON.stringify(obj)),
      description: obj.description as string | undefined,
    };
  });
});

// Decision: try response.decision (simple string), then response.intent.name/id,
// then response.intent (if it's a string).
const decision = computed<string | null>(() => {
  const r = attrs.value.response as Record<string, unknown> | undefined;
  if (!r) return null;
  if (typeof r.decision === "string") return r.decision;
  const intent = r.intent;
  if (typeof intent === "string") return intent;
  if (intent && typeof intent === "object") {
    const io = intent as Record<string, unknown>;
    // The bugfix fixture's intent object has a summary_title but not a simple
    // name/id — use summary_title as the winner label when no id is present.
    const id = io.id ?? io.name ?? io.summary_title;
    if (typeof id === "string") return id;
  }
  return null;
});

// Confidence: try response.confidence, then response.intent.confidence.
const confidence = computed<number | null>(() => {
  const r = attrs.value.response as Record<string, unknown> | undefined;
  if (!r) return null;
  if (typeof r.confidence === "number") return r.confidence;
  const intent = r.intent as Record<string, unknown> | undefined;
  if (intent && typeof intent.confidence === "number") return intent.confidence;
  return null;
});

// Threshold: from response (optional), default 0.8.
const threshold = computed<number>(() => {
  const r = attrs.value.response as Record<string, unknown> | undefined;
  if (r && typeof r.threshold === "number") return r.threshold;
  return 0.8;
});

// Reason: response.reason, or response.intent.reasoning.
const reason = computed<string | null>(() => {
  const r = attrs.value.response as Record<string, unknown> | undefined;
  if (!r) return null;
  if (typeof r.reason === "string") return r.reason;
  const intent = r.intent as Record<string, unknown> | undefined;
  if (intent && typeof intent.reasoning === "string") return intent.reasoning;
  return null;
});

// Bailed to human: from attrs or response.
const bailedToHuman = computed<boolean>(() => {
  if (attrs.value.bailed_to_human) return true;
  const r = attrs.value.response as Record<string, unknown> | undefined;
  return !!(r?.bailed_to_human);
});

// Alternatives (Slice #4 placeholder — reads response.alternatives array).
const alternatives = computed<Alternative[]>(() => {
  const r = attrs.value.response as Record<string, unknown> | undefined;
  if (!r || !Array.isArray(r.alternatives)) return [];
  return r.alternatives.map((a) => {
    const obj = a as Record<string, unknown>;
    return {
      id: String(obj.id ?? obj.name ?? JSON.stringify(obj)),
      score: typeof obj.score === "number" ? obj.score : undefined,
    };
  });
});

const { prompt, systemPrompt } = usePromptLoader(attrs);

const responseJson = computed(() => {
  const r = attrs.value.response as Record<string, unknown> | undefined;
  if (!r) return null;
  return r.json ?? r ?? null;
});

// Evidence drawer state — collapsed by default.
const evidenceOpen = ref(false);

const responseTab = ref<"object" | "raw">("object");
const modalOpen = ref(false);
const modalTab = ref<"object" | "raw">("object");

function openModal() {
  modalTab.value = responseTab.value;
  modalOpen.value = true;
}
function closeModal() { modalOpen.value = false; }

function onKeydown(e: KeyboardEvent) {
  if (e.key === "Escape" && modalOpen.value) closeModal();
}
onMounted(() => window.addEventListener("keydown", onKeydown));
onUnmounted(() => window.removeEventListener("keydown", onKeydown));
</script>

<style scoped>
/* Verdict block */
.decide-detail__verdict {
  display: flex;
  flex-direction: column;
  gap: 0.45rem;
  padding-bottom: 0.6rem;
  border-bottom: 1px solid var(--k-border, #1e293b);
}

.decide-detail__winner-row,
.decide-detail__conf-row,
.decide-detail__choices-row,
.decide-detail__reason-row {
  display: flex;
  align-items: flex-start;
  gap: 0.5rem;
}

.decide-detail__winner-chip {
  font-family: ui-monospace, monospace;
  font-size: 0.75rem;
  font-weight: 700;
  color: #c4b5fd;
  background: #2e1065;
  border: 1px solid #7c3aed;
  padding: 0.1rem 0.45rem;
  border-radius: 4px;
}

.decide-detail__bail-badge {
  font-family: ui-monospace, monospace;
  font-size: 0.7rem;
  font-weight: 700;
  color: var(--k-warning, #fbbf24);
  background: #451a03;
  border: 1px solid #b45309;
  padding: 0.1rem 0.4rem;
  border-radius: 4px;
}

.decide-detail__conf-row .confidence-bar {
  flex: 1;
  min-width: 8rem;
}

.decide-detail__choice-chips {
  display: flex;
  flex-wrap: wrap;
  gap: 0.2rem;
  flex: 1;
}

.decide-detail__choice {
  padding: 0.15rem 0.4rem;
  border-radius: 3px;
  font-size: 0.72rem;
  background: var(--k-bg-input, #1e293b);
  border: 1px solid transparent;
  display: flex;
  flex-direction: column;
  gap: 0.05rem;
}

.decide-detail__choice-id {
  font-family: ui-monospace, monospace;
  color: var(--k-fg-muted, #94a3b8);
}

.decide-detail__choice-desc {
  color: var(--k-fg-subtle, #475569);
  font-size: 0.68rem;
  line-height: 1.3;
}

.decide-detail__choice--selected {
  background: #2e1065;
  border-color: #7c3aed;
}
.decide-detail__choice--selected .decide-detail__choice-id {
  color: #c4b5fd;
  font-weight: 700;
}
.decide-detail__choice--selected .decide-detail__choice-desc {
  color: #a78bfa;
}

/* Alternatives */
.decide-detail__alternatives {
  display: flex;
  align-items: flex-start;
  gap: 0.5rem;
}

.decide-detail__alt-list {
  display: flex;
  flex-direction: column;
  gap: 0.2rem;
  flex: 1;
}

.decide-detail__alt {
  display: flex;
  align-items: center;
  gap: 0.4rem;
}

.decide-detail__alt-id {
  font-family: ui-monospace, monospace;
  font-size: 0.72rem;
  color: var(--k-fg-muted, #64748b);
  min-width: 5rem;
  flex-shrink: 0;
}

.decide-detail__alt-bar {
  flex: 1;
}

/* Reason */
.decide-detail__reason {
  background: var(--k-bg-inset, #080f1a);
  border: 1px solid var(--k-border, #1e293b);
  border-radius: 4px;
  padding: 0.4rem 0.6rem;
  font-family: ui-monospace, monospace;
  font-size: 0.72rem;
  color: var(--k-fg-code, #7dd3fc);
  white-space: pre-wrap;
  word-break: break-word;
  margin: 0;
  max-height: 10rem;
  overflow-y: auto;
  flex: 1;
}

/* Evidence drawer */
.decide-detail__evidence {
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
}

.decide-detail__evidence-toggle {
  background: none;
  border: 1px solid #334155;
  color: var(--k-fg-accent, #60a5fa);
  cursor: pointer;
  font-size: 0.72rem;
  padding: 0.2rem 0.5rem;
  border-radius: 3px;
  text-align: left;
  display: flex;
  align-items: center;
  gap: 0.4rem;
  align-self: flex-start;
}
.decide-detail__evidence-toggle:hover {
  background: var(--k-bg-hover, #1e293b);
}

.decide-detail__evidence-arrow {
  font-size: 0.6rem;
  transition: transform 0.15s ease;
  display: inline-block;
}
.decide-detail__evidence-arrow--open {
  transform: rotate(90deg);
}

.decide-detail__evidence-body {
  display: flex;
  flex-direction: column;
  gap: 0.45rem;
  padding-left: 0.25rem;
}

/* Response panel */
.decide-detail__response {
  display: flex;
  flex-direction: column;
  gap: 0.2rem;
}

.decide-detail__tabs {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}

.decide-detail__tab-row {
  display: flex;
  gap: 0;
  border: 1px solid var(--k-border, #1e293b);
  border-radius: 4px;
  overflow: hidden;
}

.decide-detail__tab {
  background: none;
  border: none;
  color: var(--k-fg-subtle, #475569);
  cursor: pointer;
  font-size: 0.7rem;
  padding: 0.1rem 0.45rem;
  font-family: ui-monospace, monospace;
  transition: background 0.1s;
}
.decide-detail__tab:hover { background: var(--k-bg-hover, #1e293b); color: var(--k-fg-muted, #94a3b8); }
.decide-detail__tab--active { background: var(--k-bg-hover, #1e293b); color: var(--k-fg, #e2e8f0); }

.decide-detail__response-body {
  background: var(--k-bg-inset, #080f1a);
  border: 1px solid var(--k-border, #1e293b);
  border-radius: 4px;
  padding: 0.5rem 0.65rem;
  font-family: ui-monospace, monospace;
  font-size: 0.72rem;
  min-height: 3rem;
  max-height: 18rem;
  overflow-y: auto;
}

.decide-detail__viewer {
  line-height: 1.5;
}

.decide-detail__popout {
  background: none;
  border: none;
  color: var(--k-fg-subtle, #475569);
  cursor: pointer;
  font-size: 0.85rem;
  padding: 0 0.2rem;
  line-height: 1;
  transition: color 0.1s;
}
.decide-detail__popout:hover { color: var(--k-fg-muted, #94a3b8); }

/* Shared label/pre styles */
.od-label {
  color: var(--k-fg-muted, #64748b);
  font-size: 0.75rem;
  display: block;
  min-width: 5rem;
  flex-shrink: 0;
}

.od-pre {
  background: transparent;
  border: none;
  padding: 0;
  font-family: ui-monospace, monospace;
  font-size: 0.72rem;
  color: var(--k-fg-code, #7dd3fc);
  white-space: pre-wrap;
  word-break: break-word;
  margin: 0;
}
</style>

<style>
/* Modal styles must be unscoped — rendered outside component root via Teleport */
.jv-modal-backdrop {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.65);
  z-index: 1000;
  display: flex;
  align-items: center;
  justify-content: center;
}

.jv-modal {
  background: #0d1b2a;
  border: 1px solid #1e293b;
  border-radius: 6px;
  width: 90vw;
  height: 85vh;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.jv-modal__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0.4rem 0.65rem;
  border-bottom: 1px solid #1e293b;
  flex-shrink: 0;
}

.jv-modal__tab-row {
  display: flex;
  gap: 0;
  border: 1px solid #1e293b;
  border-radius: 4px;
  overflow: hidden;
}

.jv-modal__close {
  background: none;
  border: none;
  color: #475569;
  cursor: pointer;
  font-size: 0.85rem;
  padding: 0.1rem 0.3rem;
  border-radius: 3px;
  transition: color 0.1s, background 0.1s;
}
.jv-modal__close:hover { color: #e2e8f0; background: #1e293b; }

.jv-modal__body {
  flex: 1;
  overflow: auto;
  padding: 0.75rem 1rem;
  font-family: ui-monospace, monospace;
  font-size: 0.78rem;
  line-height: 1.6;
}
</style>
