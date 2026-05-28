<template>
  <div class="oracle-detail">
    <!-- Common header -->
    <div class="oracle-detail__header">
      <span class="oracle-detail__verb-badge" :class="verbBadgeClass">{{ verb }}</span>
      <span v-if="agent" class="oracle-detail__meta">{{ agent }}</span>
      <span v-if="model" class="oracle-detail__meta oracle-detail__meta--model">{{ model }}</span>
      <span class="oracle-detail__spacer" />
      <span v-if="durationMs !== undefined" class="oracle-detail__stat">{{ fmtMs(durationMs) }}</span>
      <span v-if="promptTokens !== undefined" class="oracle-detail__stat oracle-detail__stat--tokens">
        in:{{ fmtTokens(promptTokens) }}
      </span>
      <span v-if="responseTokens !== undefined" class="oracle-detail__stat oracle-detail__stat--tokens">
        out:{{ fmtTokens(responseTokens) }}
      </span>
      <span v-if="costStr" class="oracle-detail__stat oracle-detail__stat--cost">{{ costStr }}</span>
    </div>

    <!-- Error banner -->
    <div v-if="errorMsg" class="oracle-detail__error">{{ errorMsg }}</div>

    <!-- Per-verb body -->
    <DecideDetail  v-if="verb === 'decide'"  :event="event" />
    <ExtractDetail v-else-if="verb === 'extract'" :event="event" />
    <AskDetail     v-else-if="verb === 'ask'"     :event="event" />
    <TaskDetail    v-else-if="verb === 'task'"    :event="event" />
    <ConverseDetail v-else-if="verb === 'converse'" :event="event" />

    <!-- Fallback: raw attrs dump for unknown verbs -->
    <div v-else class="oracle-detail__fallback">
      <pre class="oracle-detail__pre">{{ prettyJson(event.attrs) }}</pre>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from "vue";
import type { TraceEvent } from "../../types.js";
import { fmtMs, fmtTokens, fmtCost, prettyJson } from "./lib.js";
import DecideDetail from "./DecideDetail.vue";
import ExtractDetail from "./ExtractDetail.vue";
import AskDetail from "./AskDetail.vue";
import TaskDetail from "./TaskDetail.vue";
import ConverseDetail from "./ConverseDetail.vue";

const props = defineProps<{ event: TraceEvent }>();

const attrs = computed(() => props.event.attrs);

const verb = computed(() => {
  const fromAttrs = typeof attrs.value.verb === "string" ? attrs.value.verb : "";
  if (fromAttrs) return fromAttrs;
  // Infer from msg: "oracle.<verb>.complete" → "<verb>".
  const m = props.event.msg.match(/^oracle\.([a-z]+)\.complete$/);
  return m ? m[1]! : "";
});
const agent    = computed(() => String(attrs.value.agent ?? ""));
const model    = computed(() => String(attrs.value.model ?? ""));
const durationMs     = computed(() => attrs.value.duration_ms as number | undefined);
const promptTokens   = computed(() => attrs.value.prompt_tokens as number | undefined);
const responseTokens = computed(() => attrs.value.response_tokens as number | undefined);
const costStr  = computed(() => fmtCost(attrs.value.cost_usd));
const errorMsg = computed(() => typeof attrs.value.error === "string" ? attrs.value.error : null);

const verbBadgeClass = computed(() => {
  switch (verb.value) {
    case "decide":  return "verb--decide";
    case "extract": return "verb--extract";
    case "ask":     return "verb--ask";
    case "task":    return "verb--task";
    case "converse": return "verb--converse";
    default:        return "verb--other";
  }
});
</script>

<style scoped>
.oracle-detail {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}

.oracle-detail__header {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  flex-wrap: wrap;
  padding-bottom: 0.35rem;
  border-bottom: 1px solid #1e293b;
}

.oracle-detail__verb-badge {
  padding: 0.1rem 0.5rem;
  border-radius: 3px;
  font-size: 0.75rem;
  font-weight: 700;
  font-family: ui-monospace, monospace;
  text-transform: uppercase;
}

/* Verb badge colours */
.verb--decide  { background: #1e1b4b; color: #a5b4fc; border: 1px solid #3730a3; }
.verb--extract { background: #042f2e; color: #5eead4; border: 1px solid #0d9488; }
.verb--ask     { background: #431407; color: #fdba74; border: 1px solid #c2410c; }
.verb--task    { background: #450a0a; color: #fca5a5; border: 1px solid #991b1b; }
.verb--converse { background: #083344; color: #67e8f9; border: 1px solid #0891b2; }
.verb--other   { background: #1e293b; color: #94a3b8; border: 1px solid #334155; }

.oracle-detail__meta {
  color: #94a3b8;
  font-size: 0.75rem;
  font-family: ui-monospace, monospace;
}

.oracle-detail__meta--model {
  color: #64748b;
  font-size: 0.7rem;
}

.oracle-detail__spacer {
  flex: 1;
}

.oracle-detail__stat {
  color: #64748b;
  font-size: 0.7rem;
  font-family: ui-monospace, monospace;
  white-space: nowrap;
}

.oracle-detail__stat--tokens {
  color: #475569;
}

.oracle-detail__stat--cost {
  color: #a3e635;
}

.oracle-detail__error {
  background: #2d0707;
  border: 1px solid #991b1b;
  border-radius: 4px;
  color: #fca5a5;
  padding: 0.3rem 0.5rem;
  font-size: 0.75rem;
  font-family: ui-monospace, monospace;
}

.oracle-detail__fallback {
  display: flex;
  flex-direction: column;
  gap: 0.2rem;
}

.oracle-detail__pre {
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
}
</style>
