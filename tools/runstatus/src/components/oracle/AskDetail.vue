<template>
  <div class="ask-detail">
    <CollapsibleText label="System Prompt" :text="systemPrompt" />
    <CollapsibleText label="Prompt" :text="prompt" />

    <div v-if="responseText" class="ask-detail__block">
      <span class="od-label">Response</span>
      <pre class="od-pre od-pre--response">{{ responseText }}</pre>
    </div>

    <div v-if="intent" class="ask-detail__kv">
      <span class="od-label">Intent</span>
      <code class="ask-detail__code">{{ intent }}</code>
    </div>

    <div v-if="slots !== null" class="ask-detail__block">
      <span class="od-label">Slots</span>
      <pre class="od-pre">{{ prettyJson(slots) }}</pre>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from "vue";
import type { TraceEvent } from "../../types.js";
import { prettyJson } from "./lib.js";
import CollapsibleText from "./CollapsibleText.vue";

const props = defineProps<{ event: TraceEvent }>();

const attrs = computed(() => props.event.attrs);

const prompt = computed(() => String(attrs.value.prompt ?? ""));
const systemPrompt = computed(() => String(attrs.value.system_prompt ?? ""));

// Response may be the new shape (object with .text/.intent/.slots) OR the
// legacy lean shape — top-level attrs.response is a string, attrs.intent /
// attrs.slots live flat.  Support both so bugfix-recycle and real lean
// session logs render usefully.
const responseObj = computed(() =>
  typeof attrs.value.response === "object" && attrs.value.response !== null
    ? (attrs.value.response as Record<string, unknown>)
    : null
);

const responseText = computed(() => {
  const r = responseObj.value;
  if (typeof r?.text === "string") return r.text;
  if (typeof attrs.value.response === "string") return attrs.value.response;
  return "";
});

const intent = computed(() => {
  const r = responseObj.value;
  if (typeof r?.intent === "string") return r.intent;
  if (typeof attrs.value.intent === "string") return attrs.value.intent;
  return null;
});

const slots = computed(() => {
  const r = responseObj.value;
  if (r?.slots !== undefined && r.slots !== null) return r.slots;
  if (attrs.value.slots !== undefined && attrs.value.slots !== null) return attrs.value.slots;
  return null;
});
</script>

<style scoped>
.ask-detail {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}

.ask-detail__block {
  display: flex;
  flex-direction: column;
  gap: 0.2rem;
}

.ask-detail__kv {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}

.ask-detail__code {
  font-family: ui-monospace, monospace;
  font-size: 0.75rem;
  color: #fb923c;
  background: #431407;
  padding: 0.1rem 0.35rem;
  border-radius: 3px;
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
