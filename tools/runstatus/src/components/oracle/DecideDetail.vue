<template>
  <div class="decide-detail">
    <div class="decide-detail__cols">
      <!-- Left: choices -->
      <div class="decide-detail__choices">
        <span class="od-label">Choices</span>
        <div class="decide-detail__choice-list">
          <div
            v-for="c in choices"
            :key="c.id"
            class="decide-detail__choice"
            :class="{ 'decide-detail__choice--selected': c.id === decision }"
          >
            <span class="decide-detail__choice-id">{{ c.id }}</span>
            <span v-if="c.description" class="decide-detail__choice-desc">{{ c.description }}</span>
          </div>
        </div>
        <div v-if="decision" class="decide-detail__decision">
          <span class="od-label">Decision</span>
          <code class="decide-detail__decision-val">{{ decision }}</code>
        </div>
      </div>

      <!-- Right: prompt + response -->
      <div class="decide-detail__right">
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

const props = defineProps<{ event: TraceEvent }>();

const attrs = computed(() => props.event.attrs);

interface Choice {
  id: string;
  description?: string;
}

const choices = computed<Choice[]>(() => {
  const c = attrs.value.input as Record<string, unknown> | undefined;
  const arr = c?.choices;
  if (!Array.isArray(arr)) return [];
  return arr.map((item) => {
    if (typeof item === "string") return { id: item };
    const obj = item as Record<string, unknown>;
    return { id: String(obj.id ?? obj.name ?? JSON.stringify(obj)), description: obj.description as string | undefined };
  });
});

const decision = computed<string | null>(() => {
  const r = attrs.value.response as Record<string, unknown> | undefined;
  return typeof r?.decision === "string" ? r.decision : null;
});

const prompt = computed(() => String(attrs.value.prompt ?? ""));
const systemPrompt = computed(() => String(attrs.value.system_prompt ?? ""));

const responseJson = computed(() => {
  const r = attrs.value.response as Record<string, unknown> | undefined;
  if (!r) return null;
  return r.json ?? r ?? null;
});

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
.decide-detail__cols {
  display: flex;
  gap: 0.75rem;
  flex-wrap: wrap;
}

.decide-detail__choices {
  min-width: 12rem;
  flex: 0 0 auto;
  max-width: 22rem;
}

.decide-detail__choice-list {
  display: flex;
  flex-direction: column;
  gap: 0.2rem;
  margin: 0.3rem 0;
}

.decide-detail__choice {
  padding: 0.2rem 0.45rem;
  border-radius: 3px;
  font-size: 0.75rem;
  background: #1e293b;
  border: 1px solid transparent;
  display: flex;
  flex-direction: column;
  gap: 0.1rem;
}

.decide-detail__choice-id {
  font-family: ui-monospace, monospace;
  color: #94a3b8;
}

.decide-detail__choice-desc {
  color: #475569;
  font-size: 0.7rem;
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

.decide-detail__decision {
  margin-top: 0.5rem;
  display: flex;
  flex-direction: column;
  gap: 0.2rem;
}

.decide-detail__decision-val {
  font-family: ui-monospace, monospace;
  font-size: 0.75rem;
  color: #c4b5fd;
  background: #2e1065;
  padding: 0.1rem 0.35rem;
  border-radius: 3px;
}

.decide-detail__right {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
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
  border: 1px solid #1e293b;
  border-radius: 4px;
  overflow: hidden;
}

.decide-detail__tab {
  background: none;
  border: none;
  color: #475569;
  cursor: pointer;
  font-size: 0.7rem;
  padding: 0.1rem 0.45rem;
  font-family: ui-monospace, monospace;
  transition: background 0.1s;
}
.decide-detail__tab:hover { background: #1e293b; color: #94a3b8; }
.decide-detail__tab--active { background: #1e293b; color: #e2e8f0; }

.decide-detail__response-body {
  background: #080f1a;
  border: 1px solid #1e293b;
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
  color: #475569;
  cursor: pointer;
  font-size: 0.85rem;
  padding: 0 0.2rem;
  line-height: 1;
  transition: color 0.1s;
}
.decide-detail__popout:hover { color: #94a3b8; }

/* Shared label/pre styles */
.od-label {
  color: #64748b;
  font-size: 0.75rem;
  display: block;
  margin-bottom: 0.15rem;
}

.od-pre {
  background: transparent;
  border: none;
  padding: 0;
  font-family: ui-monospace, monospace;
  font-size: 0.72rem;
  color: #7dd3fc;
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
