<template>
  <div class="converse-detail">
    <div v-if="messages.length > 0" class="converse-detail__chat">
      <span class="od-label">Messages</span>
      <div class="converse-detail__log">
        <div
          v-for="(msg, i) in messages"
          :key="i"
          class="converse-detail__bubble"
          :class="bubbleClass(msg.role)"
        >
          <span class="converse-detail__role">{{ msg.role }}</span>
          <pre class="converse-detail__text">{{ msgText(msg) }}</pre>
        </div>
      </div>
    </div>

    <CollapsibleText label="System Prompt" :text="systemPrompt" />
    <CollapsibleText label="Prompt" :text="prompt" />

    <div v-if="responseText" class="converse-detail__block">
      <span class="od-label">Final Response</span>
      <pre class="od-pre od-pre--response">{{ responseText }}</pre>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from "vue";
import type { TraceEvent } from "../../types.js";
import CollapsibleText from "./CollapsibleText.vue";

const props = defineProps<{ event: TraceEvent }>();

interface ChatMessage {
  role: string;
  content?: unknown;
  [key: string]: unknown;
}

const attrs = computed(() => props.event.attrs);

const messages = computed<ChatMessage[]>(() => {
  const inp = attrs.value.input as Record<string, unknown> | undefined;
  const msgs = inp?.messages;
  if (Array.isArray(msgs)) return msgs as ChatMessage[];
  return [];
});

const prompt = computed(() => String(attrs.value.prompt ?? ""));
const systemPrompt = computed(() => String(attrs.value.system_prompt ?? ""));

const response = computed(() => attrs.value.response as Record<string, unknown> | undefined);
const responseText = computed(() => {
  const r = response.value;
  return typeof r?.text === "string" ? r.text : "";
});

function bubbleClass(role: string): string {
  switch (role) {
    case "user": return "converse-detail__bubble--user";
    case "assistant": return "converse-detail__bubble--assistant";
    case "system": return "converse-detail__bubble--system";
    default: return "converse-detail__bubble--other";
  }
}

function msgText(msg: ChatMessage): string {
  if (typeof msg.content === "string") return msg.content;
  if (Array.isArray(msg.content)) {
    return msg.content
      .map((c) => (typeof c === "object" && c !== null && "text" in c ? String((c as Record<string, unknown>).text) : JSON.stringify(c)))
      .join("\n");
  }
  return JSON.stringify(msg, null, 2);
}
</script>

<style scoped>
.converse-detail {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}

.converse-detail__chat {
  display: flex;
  flex-direction: column;
  gap: 0.2rem;
}

.converse-detail__log {
  display: flex;
  flex-direction: column;
  gap: 0.3rem;
  max-height: 20rem;
  overflow-y: auto;
  padding: 0.3rem;
  background: #080f1a;
  border: 1px solid #1e293b;
  border-radius: 4px;
}

.converse-detail__bubble {
  padding: 0.3rem 0.5rem;
  border-radius: 4px;
  border-left: 3px solid transparent;
}

.converse-detail__bubble--user      { background: #0f1e38; border-left-color: #60a5fa; }
.converse-detail__bubble--assistant { background: #0a1a14; border-left-color: #34d399; }
.converse-detail__bubble--system    { background: #1a1020; border-left-color: #a78bfa; }
.converse-detail__bubble--other     { background: #1e293b; border-left-color: #475569; }

.converse-detail__role {
  font-size: 0.65rem;
  font-weight: 700;
  text-transform: uppercase;
  color: #64748b;
  display: block;
  margin-bottom: 0.15rem;
}

.converse-detail__text {
  font-family: ui-monospace, monospace;
  font-size: 0.72rem;
  color: #e2e8f0;
  white-space: pre-wrap;
  word-break: break-word;
  margin: 0;
}

.converse-detail__block {
  display: flex;
  flex-direction: column;
  gap: 0.2rem;
}

.od-label {
  color: #64748b;
  font-size: 0.75rem;
}

.od-pre {
  background: #080f1a;
  border: 1px solid #1e293b;
  border-radius: 4px;
  padding: 0.4rem 0.6rem;
  font-family: ui-monospace, monospace;
  font-size: 0.72rem;
  color: #7dd3fc;
  white-space: pre-wrap;
  word-break: break-word;
  margin: 0;
  max-height: 14rem;
  overflow-y: auto;
}

.od-pre--response {
  color: #e2e8f0;
}
</style>
