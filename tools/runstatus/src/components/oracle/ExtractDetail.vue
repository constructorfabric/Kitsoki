<template>
  <div class="extract-detail">
    <!-- Top row: schema + extracted side by side -->
    <div class="extract-detail__cols">
      <div v-if="schema !== null" class="extract-detail__col">
        <span class="od-label">Schema</span>
        <pre class="od-pre">{{ prettyJson(schema) }}</pre>
      </div>
      <div v-if="extracted !== null" class="extract-detail__col">
        <span class="od-label">Extracted</span>
        <pre class="od-pre" :class="{ 'od-pre--has-nulls': hasNulls }">{{ prettyJson(extracted) }}</pre>
        <div v-if="nullFields.length > 0" class="extract-detail__null-warn">
          Missing fields: {{ nullFields.join(", ") }}
        </div>
      </div>
    </div>

    <!-- Prompts below -->
    <CollapsibleText label="System Prompt" :text="systemPrompt" />
    <CollapsibleText label="Prompt" :text="prompt" />
  </div>
</template>

<script setup lang="ts">
import { computed } from "vue";
import type { TraceEvent } from "../../types.js";
import { prettyJson } from "./lib.js";
import CollapsibleText from "./CollapsibleText.vue";

const props = defineProps<{ event: TraceEvent }>();

const attrs = computed(() => props.event.attrs);

const schema = computed(() => {
  const inp = attrs.value.input as Record<string, unknown> | undefined;
  return inp?.schema ?? null;
});

const extracted = computed(() => {
  const r = attrs.value.response as Record<string, unknown> | undefined;
  return r?.extracted ?? r?.json ?? null;
});

const nullFields = computed<string[]>(() => {
  const e = extracted.value;
  if (!e || typeof e !== "object" || Array.isArray(e)) return [];
  return Object.entries(e as Record<string, unknown>)
    .filter(([, v]) => v === null || v === undefined)
    .map(([k]) => k);
});

const hasNulls = computed(() => nullFields.value.length > 0);

const prompt = computed(() => String(attrs.value.prompt ?? ""));
const systemPrompt = computed(() => String(attrs.value.system_prompt ?? ""));
</script>

<style scoped>
.extract-detail {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}

.extract-detail__cols {
  display: flex;
  gap: 0.75rem;
  flex-wrap: wrap;
}

.extract-detail__col {
  flex: 1;
  min-width: 12rem;
  display: flex;
  flex-direction: column;
  gap: 0.2rem;
}

.extract-detail__null-warn {
  color: #fca5a5;
  font-size: 0.72rem;
  margin-top: 0.2rem;
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
  max-height: 16rem;
  overflow-y: auto;
}

.od-pre--has-nulls {
  border-color: #7f1d1d;
}
</style>
