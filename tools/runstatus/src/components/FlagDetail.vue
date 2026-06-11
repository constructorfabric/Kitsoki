<script setup lang="ts">
/**
 * FlagDetail — the right column: the selected flag's captured still, the
 * source_ref it resolves to (with a VS Code deep-link), a per-flag chat for
 * discussion (the read-only off-path oracle), and the dispatch buttons.
 *
 * Per epic shared decision 3 this panel CAPTURES and DISPATCHES — it never
 * edits a spec or calls a code-writing LLM. The chat is the read-only off-path
 * oracle for discussion; "Send to refine" emits a structured feedback note the
 * parent hands to runstatus.feedback.add. The interpretive edit is the slice-3
 * story's recorded refine, not a web side-effect.
 */
import { ref, computed, watch } from "vue";
import type { Flag } from "../lib/flags.js";
import type { FeedbackNote } from "../data/source.js";
import { sourceRefFor, vscodeLink, formatMs } from "../lib/flags.js";
import ViewElement from "./ViewElement.vue";
import type { ViewElement as ViewElementT } from "../types.js";

const props = defineProps<{
  flag: Flag | null;
  video: string;
  /** Per-flag chat transcript: [{ role, text }]. Owned by the parent. */
  chat: { role: string; text: string }[];
  /** True while an off-path chat turn is in flight. */
  chatBusy?: boolean;
}>();

const emit = defineEmits<{
  (e: "update-instruction", value: string): void;
  (e: "send-chat", input: string): void;
  (e: "send-refine", note: FeedbackNote): void;
}>();

const sourceRef = computed(() => (props.flag ? sourceRefFor(props.flag) : undefined));
const deepLink = computed(() => vscodeLink(sourceRef.value));

// The captured still as a media element, reusing ViewElement's image branch.
const stillElement = computed<ViewElementT | null>(() => {
  if (!props.flag?.frame_handle) return null;
  return {
    Kind: "media",
    Handle: props.flag.frame_handle,
    Mime: "image/png",
    Caption: props.flag ? `frame @ ${formatMs(props.flag.start_ms)}` : undefined,
  };
});

const chatInput = ref("");
function onSendChat() {
  const text = chatInput.value.trim();
  if (!text) return;
  emit("send-chat", text);
  chatInput.value = "";
}

const instruction = ref("");
watch(
  () => props.flag,
  (f) => {
    instruction.value = f?.instruction ?? "";
  },
  { immediate: true }
);
function onInstructionInput() {
  emit("update-instruction", instruction.value);
}

const canSend = computed(
  () => !!props.flag && instruction.value.trim().length > 0
);

function buildNote(): FeedbackNote | null {
  if (!props.flag) return null;
  const f = props.flag;
  return {
    video: props.video,
    source_ref: sourceRef.value,
    time_range:
      f.end_ms > f.start_ms
        ? { start_ms: f.start_ms, end_ms: f.end_ms }
        : { start_ms: f.start_ms },
    frame_handle: f.frame_handle ?? undefined,
    instruction: instruction.value.trim(),
  };
}

function onSendRefine() {
  const note = buildNote();
  if (note) emit("send-refine", note);
}
</script>

<template>
  <div class="flag-detail" data-testid="flag-detail">
    <p v-if="!flag" class="fd-empty" data-testid="fd-empty">
      Select a flag to review it.
    </p>

    <template v-else>
      <header class="fd-head">
        <span class="fd-flag-id">Flag #{{ flag.id }}</span>
        <span v-if="flag.chapter" class="fd-chapter">
          · scene {{ flag.chapter.index + 1 }} "{{ flag.chapter.label }}"
        </span>
      </header>

      <!-- Captured still: a media image via the shared ViewElement renderer. -->
      <div class="fd-still" data-testid="fd-still">
        <ViewElement v-if="stillElement" :element="stillElement" />
        <p v-else class="fd-still-pending">Capturing still…</p>
      </div>

      <!-- Resolved source_ref + IDE deep-link. -->
      <div v-if="sourceRef" class="fd-source" data-testid="fd-source">
        <span class="fd-source-label">source:</span>
        <code class="fd-source-ref">
          {{ sourceRef.spec_path
          }}{{ sourceRef.scene_id ? "#scene:" + sourceRef.scene_id : "" }}{{
            sourceRef.step_id ? "#step:" + sourceRef.step_id : ""
          }}
        </code>
        <a
          v-if="deepLink"
          class="fd-open"
          data-testid="fd-open"
          :href="deepLink"
          >↗ open</a
        >
      </div>

      <!-- Per-flag chat (read-only off-path discussion). -->
      <div class="fd-chat" data-testid="fd-chat">
        <div
          v-for="(m, i) in chat"
          :key="i"
          class="fd-msg"
          :class="'fd-msg--' + m.role"
        >
          <span class="fd-msg-role">{{ m.role }}</span>
          <span class="fd-msg-text">{{ m.text }}</span>
        </div>
        <div class="fd-chat-input">
          <input
            v-model="chatInput"
            class="fd-chat-box"
            data-testid="fd-chat-box"
            type="text"
            placeholder="Ask about this moment…"
            :disabled="chatBusy"
            @keyup.enter="onSendChat"
          />
          <button
            class="fd-chat-send"
            data-testid="fd-chat-send"
            :disabled="chatBusy"
            @click="onSendChat"
          >
            Ask
          </button>
        </div>
      </div>

      <!-- The instruction the feedback note carries. -->
      <textarea
        v-model="instruction"
        class="fd-instruction"
        data-testid="fd-instruction"
        rows="2"
        placeholder="What should change? (this becomes the refine instruction)"
        @input="onInstructionInput"
      />

      <div class="fd-actions">
        <button
          class="fd-send-refine"
          data-testid="fd-send-refine"
          :disabled="!canSend"
          @click="onSendRefine"
        >
          Send to refine
        </button>
        <span v-if="flag.sent" class="fd-sent-badge" data-testid="fd-sent-badge"
          >sent</span
        >
      </div>
    </template>
  </div>
</template>

<style scoped>
.flag-detail {
  display: flex;
  flex-direction: column;
  gap: 0.75em;
}
.fd-empty {
  color: #6b7280;
  font-size: 14px;
}
.fd-head {
  display: flex;
  gap: 0.4em;
  align-items: baseline;
}
.fd-flag-id {
  font-weight: 700;
  color: #11151c;
}
.fd-chapter {
  color: #4a5160;
  font-size: 14px;
}
.fd-still {
  border: 1px solid #d8dbe2;
  border-radius: 8px;
  padding: 0.5em;
  background: #fafbfc;
}
.fd-still-pending {
  color: #6b7280;
  font-size: 13px;
  margin: 0;
}
.fd-source {
  display: flex;
  align-items: center;
  gap: 0.5em;
  font-size: 13px;
}
.fd-source-label {
  color: #6b7280;
}
.fd-source-ref {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  color: #b3306b;
  background: #f0f1f4;
  padding: 0.1em 0.4em;
  border-radius: 4px;
}
.fd-open {
  color: #1d4ed8;
  text-decoration: none;
  font-weight: 600;
}
.fd-chat {
  border: 1px solid #e2e5ea;
  border-radius: 8px;
  padding: 0.5em;
  display: flex;
  flex-direction: column;
  gap: 0.4em;
  max-height: 220px;
  overflow-y: auto;
}
.fd-msg {
  font-size: 13px;
  display: flex;
  gap: 0.4em;
}
.fd-msg-role {
  font-weight: 600;
  color: #4a5160;
  flex: none;
}
.fd-msg--assistant .fd-msg-text {
  color: #1f2430;
}
.fd-chat-input {
  display: flex;
  gap: 0.4em;
}
.fd-chat-box {
  flex: 1;
  padding: 0.35em 0.6em;
  border: 1px solid #d8dbe2;
  border-radius: 6px;
  font-size: 13px;
}
.fd-chat-send {
  padding: 0.35em 0.8em;
  border: 1px solid #d8dbe2;
  border-radius: 6px;
  background: #fff;
  cursor: pointer;
}
.fd-instruction {
  width: 100%;
  padding: 0.5em 0.6em;
  border: 1px solid #d8dbe2;
  border-radius: 6px;
  font-size: 14px;
  font-family: inherit;
  resize: vertical;
  box-sizing: border-box;
}
.fd-actions {
  display: flex;
  align-items: center;
  gap: 0.6em;
}
.fd-send-refine {
  padding: 0.45em 1em;
  border: 1px solid #1d4ed8;
  border-radius: 6px;
  background: #1d4ed8;
  color: #fff;
  font-size: 14px;
  cursor: pointer;
}
.fd-send-refine:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}
.fd-sent-badge {
  font-size: 12px;
  color: #1b7a3e;
  font-weight: 600;
}
</style>
