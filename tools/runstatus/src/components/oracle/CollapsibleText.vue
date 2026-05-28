<template>
  <div v-if="text" class="collapsible-text">
    <span class="ct-label">{{ label }}</span>
    <pre class="ct-pre">{{ displayed }}</pre>
    <button v-if="isTruncated(text)" class="ct-toggle" @click="expanded = !expanded">
      {{ expanded ? 'Show less' : 'Show full' }}
    </button>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from "vue";
import { isTruncated, maybeShow } from "./lib.js";

const props = defineProps<{ label: string; text: string }>();

const expanded = ref(true);

const displayed = computed(() => maybeShow(props.text, expanded.value));
</script>

<style scoped>
.collapsible-text {
  display: flex;
  flex-direction: column;
  gap: 0.15rem;
}

.ct-label {
  color: #64748b;
  font-size: 0.75rem;
}

.ct-pre {
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

.ct-toggle {
  align-self: flex-start;
  background: none;
  border: 1px solid #334155;
  color: #60a5fa;
  cursor: pointer;
  font-size: 0.72rem;
  padding: 0.15rem 0.5rem;
  border-radius: 3px;
}

.ct-toggle:hover {
  background: #1e293b;
}
</style>
