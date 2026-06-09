<template>
  <Teleport to="body">
    <div
      v-if="meta.open"
      class="meta-overlay__backdrop"
      data-testid="meta-overlay"
      @click.self="meta.close()"
    >
      <div class="meta-overlay">
        <!-- Header: mode tabs + new-chat + close -->
        <div class="meta-overlay__header">
          <div class="meta-overlay__tabs">
            <button
              v-for="m in tabModes"
              :key="m.key"
              class="meta-overlay__tab"
              :class="{ 'meta-overlay__tab--active': m.key === meta.activeMode }"
              :data-testid="`meta-tab-${testidFor(m.key)}`"
              @click="switchMode(m.key)"
            >
              {{ m.label }}
              <span v-if="m.read_only" class="meta-overlay__ro" title="read-only">RO</span>
            </button>
          </div>
          <div class="meta-overlay__header-actions">
            <button
              class="meta-overlay__action"
              data-testid="meta-new"
              title="Start a fresh chat in this mode"
              @click="meta.newChat(source)"
            >＋ New chat</button>
            <button
              class="meta-overlay__close"
              data-testid="meta-close"
              title="Close (Esc)"
              @click="meta.close()"
            >✕</button>
          </div>
        </div>

        <!-- Banner for the active mode -->
        <div v-if="meta.activeModeInfo?.banner" class="meta-overlay__banner">
          {{ meta.activeModeInfo.banner }}
        </div>

        <!-- Transcript -->
        <div class="meta-overlay__body" data-testid="meta-transcript">
          <p v-if="meta.activeTranscript.length === 0" class="meta-overlay__empty">
            No messages yet — ask a question or request a change below.
          </p>
          <div
            v-for="(msg, i) in meta.activeTranscript"
            :key="i"
            class="meta-row"
            :class="`meta-row--${msg.role === 'user' ? 'user' : 'agent'}`"
            :data-testid="`meta-row-${msg.role === 'user' ? 'user' : 'agent'}`"
          >
            <span class="meta-row__who">{{ msg.role === "user" ? "you" : "agent" }}</span>
            <span class="meta-row__text">{{ msg.text }}</span>
          </div>
        </div>

        <!-- Reload note + error -->
        <div v-if="meta.reloadNote" class="meta-overlay__note" data-testid="meta-reload-note">
          ↻ {{ meta.reloadNote }}
        </div>
        <div v-if="meta.error" class="meta-overlay__error" data-testid="meta-error">
          {{ meta.error }}
        </div>

        <!-- Composer -->
        <form class="meta-overlay__composer" @submit.prevent="onSend">
          <textarea
            ref="inputEl"
            v-model="draft"
            class="meta-overlay__input"
            data-testid="meta-composer-input"
            :placeholder="placeholder"
            rows="2"
            :disabled="meta.busy"
            @keydown.enter.exact.prevent="onSend"
          ></textarea>
          <button
            type="submit"
            class="meta-overlay__send"
            data-testid="meta-composer-send"
            :disabled="meta.busy || draft.trim() === ''"
          >{{ meta.busy ? "…" : "Send" }}</button>
        </form>
      </div>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from "vue";
import { LiveSource } from "../../data/live-source.js";
import { useMetaStore } from "../../stores/meta.js";

const meta = useMetaStore();
const source = new LiveSource("/");

// The three modes the web surface curates (same set the launcher dropdown
// shows); the server may advertise more (story.bug, kitsoki.edit, …) that we
// don't surface as tabs here.
const CURATED = new Set(["story.edit", "story.ask", "kitsoki.ask"]);
const tabModes = computed(() => meta.modes.filter((m) => CURATED.has(m.key)));

const draft = ref("");
const inputEl = ref<HTMLTextAreaElement | null>(null);

const placeholder = computed(() =>
  meta.activeModeInfo?.read_only
    ? "Ask a question…"
    : "Describe the change you want…"
);

function testidFor(key: string): string {
  return key.replace(/\./g, "-");
}

async function switchMode(key: string): Promise<void> {
  if (key === meta.activeMode) return;
  await meta.openMode(source, meta.activeSessionId, key);
}

async function onSend(): Promise<void> {
  const text = draft.value;
  if (text.trim() === "" || meta.busy) return;
  draft.value = "";
  await meta.send(source, text);
}

function onKeydown(e: KeyboardEvent): void {
  if (e.key === "Escape" && meta.open) meta.close();
}
onMounted(() => window.addEventListener("keydown", onKeydown));
onUnmounted(() => window.removeEventListener("keydown", onKeydown));
</script>

<style scoped>
.meta-overlay__backdrop {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.65);
  z-index: 1000;
  display: flex;
  align-items: center;
  justify-content: center;
}

.meta-overlay {
  background: #0d1b2a;
  border: 1px solid #1e293b;
  border-radius: 8px;
  width: 86vw;
  height: 84vh;
  max-width: 1100px;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  color: #e2e8f0;
}

.meta-overlay__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0.45rem 0.7rem;
  border-bottom: 1px solid #1e293b;
  flex-shrink: 0;
  gap: 0.5rem;
}

.meta-overlay__tabs {
  display: flex;
  gap: 0.25rem;
  flex-wrap: wrap;
}

.meta-overlay__tab {
  display: inline-flex;
  align-items: center;
  gap: 0.3rem;
  background: #15233a;
  border: 1px solid #1e293b;
  color: #94a3b8;
  border-radius: 5px;
  padding: 0.28rem 0.6rem;
  font-size: 0.76rem;
  cursor: pointer;
}
.meta-overlay__tab--active {
  background: #1d4ed8;
  border-color: #2563eb;
  color: #eef2ff;
}
.meta-overlay__ro {
  font-size: 0.58rem;
  background: #0d1b2a;
  border-radius: 3px;
  padding: 0 0.2rem;
  opacity: 0.8;
}

.meta-overlay__header-actions {
  display: flex;
  align-items: center;
  gap: 0.4rem;
}
.meta-overlay__action {
  background: none;
  border: 1px solid #1e293b;
  color: #cbd5e1;
  border-radius: 5px;
  padding: 0.25rem 0.55rem;
  font-size: 0.72rem;
  cursor: pointer;
}
.meta-overlay__action:hover {
  background: #15233a;
}
.meta-overlay__close {
  background: none;
  border: none;
  color: #64748b;
  cursor: pointer;
  font-size: 0.9rem;
  padding: 0.1rem 0.3rem;
}
.meta-overlay__close:hover {
  color: #e2e8f0;
}

.meta-overlay__banner {
  padding: 0.4rem 0.8rem;
  background: #112033;
  border-bottom: 1px solid #1e293b;
  font-size: 0.74rem;
  color: #93c5fd;
  flex-shrink: 0;
}

.meta-overlay__body {
  flex: 1;
  overflow: auto;
  padding: 0.8rem 1rem;
  display: flex;
  flex-direction: column;
  gap: 0.6rem;
}
.meta-overlay__empty {
  color: #475569;
  font-size: 0.8rem;
  font-style: italic;
}

.meta-row {
  display: flex;
  flex-direction: column;
  gap: 0.15rem;
  max-width: 80%;
}
.meta-row--user {
  align-self: flex-end;
  align-items: flex-end;
}
.meta-row__who {
  font-size: 0.62rem;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  color: #64748b;
}
.meta-row__text {
  white-space: pre-wrap;
  font-size: 0.82rem;
  line-height: 1.5;
  padding: 0.45rem 0.65rem;
  border-radius: 8px;
  background: #15233a;
}
.meta-row--user .meta-row__text {
  background: #1d4ed8;
  color: #eef2ff;
}

.meta-overlay__note {
  padding: 0.4rem 0.8rem;
  background: #0f2a1c;
  border-top: 1px solid #14532d;
  color: #86efac;
  font-size: 0.74rem;
  flex-shrink: 0;
}
.meta-overlay__error {
  padding: 0.4rem 0.8rem;
  background: #2a0f12;
  border-top: 1px solid #7f1d1d;
  color: #fca5a5;
  font-size: 0.74rem;
  flex-shrink: 0;
}

.meta-overlay__composer {
  display: flex;
  gap: 0.5rem;
  padding: 0.6rem 0.8rem;
  border-top: 1px solid #1e293b;
  flex-shrink: 0;
}
.meta-overlay__input {
  flex: 1;
  resize: none;
  background: #0a1422;
  border: 1px solid #1e293b;
  border-radius: 6px;
  color: #e2e8f0;
  padding: 0.45rem 0.6rem;
  font-size: 0.82rem;
  font-family: inherit;
}
.meta-overlay__send {
  align-self: stretch;
  background: #1d4ed8;
  border: 1px solid #2563eb;
  color: #eef2ff;
  border-radius: 6px;
  padding: 0 1rem;
  font-size: 0.8rem;
  font-weight: 600;
  cursor: pointer;
}
.meta-overlay__send:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
</style>
