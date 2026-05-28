<template>
  <!-- Scalars -->
  <span v-if="value === null" class="jv-null">null</span>
  <span v-else-if="typeof value === 'boolean'" class="jv-bool">{{ value }}</span>
  <span v-else-if="typeof value === 'number'" class="jv-number">{{ value }}</span>
  <span v-else-if="typeof value === 'string'" class="jv-string">"{{ value }}"</span>

  <!-- Arrays -->
  <span v-else-if="Array.isArray(value)" class="jv-container">
    <template v-if="isOpen">
      <button class="jv-toggle" @click="isOpen = false">▾</button>
      <span class="jv-brace">[</span>
      <div class="jv-indent">
        <div v-for="(item, i) in (value as unknown[])" :key="i" class="jv-row">
          <JsonViewer :value="item" /><span v-if="i < (value as unknown[]).length - 1" class="jv-comma">,</span>
        </div>
      </div>
      <span class="jv-brace">]</span>
    </template>
    <span v-else class="jv-collapsed" @click="isOpen = true">
      <button class="jv-toggle">▸</button><span class="jv-brace">[</span><span class="jv-ellipsis">… {{ (value as unknown[]).length }}</span><span class="jv-brace">]</span>
    </span>
  </span>

  <!-- Objects -->
  <span v-else-if="typeof value === 'object'" class="jv-container">
    <template v-if="isOpen">
      <button class="jv-toggle" @click="isOpen = false">▾</button>
      <span class="jv-brace">{</span>
      <div class="jv-indent">
        <div v-for="(k, ki) in Object.keys(value as Record<string, unknown>)" :key="k" class="jv-row">
          <span class="jv-key">{{ k }}</span><span class="jv-colon">: </span><JsonViewer :value="(value as Record<string, unknown>)[k]" /><span v-if="ki < Object.keys(value as Record<string, unknown>).length - 1" class="jv-comma">,</span>
        </div>
      </div>
      <span class="jv-brace">}</span>
    </template>
    <span v-else class="jv-collapsed" @click="isOpen = true">
      <button class="jv-toggle">▸</button><span class="jv-brace">{</span><span class="jv-ellipsis">… {{ Object.keys(value as Record<string, unknown>).length }}</span><span class="jv-brace">}</span>
    </span>
  </span>

  <!-- Fallback -->
  <span v-else class="jv-unknown">{{ value }}</span>
</template>

<script setup lang="ts">
import { ref } from "vue";

const props = defineProps<{ value: unknown; defaultOpen?: boolean }>();

// Arrays/objects start open unless explicitly closed. Scalars don't use this.
const isOpen = ref(props.defaultOpen !== false);
</script>

<!-- Allow JsonViewer to recurse into itself -->
<script lang="ts">
export default { name: "JsonViewer" };
</script>

<style scoped>
.jv-null    { color: #94a3b8; font-style: italic; }
.jv-bool    { color: #a78bfa; }
.jv-number  { color: #fb923c; }
.jv-string  { color: #4ade80; }
.jv-key     { color: #7dd3fc; }
.jv-colon   { color: #475569; }
.jv-brace   { color: #64748b; }
.jv-comma   { color: #475569; }
.jv-ellipsis { color: #475569; font-size: 0.7em; margin: 0 0.2em; }

.jv-container {
  display: inline;
  font-family: ui-monospace, monospace;
  font-size: 0.72rem;
}

.jv-toggle {
  background: none;
  border: none;
  color: #475569;
  cursor: pointer;
  font-size: 0.65rem;
  padding: 0 0.15rem;
  line-height: 1;
  vertical-align: middle;
}
.jv-toggle:hover { color: #94a3b8; }

.jv-indent {
  padding-left: 1.2rem;
  display: flex;
  flex-direction: column;
  gap: 0.1rem;
}

.jv-row {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 0;
  min-height: 1.2em;
}

.jv-collapsed {
  cursor: pointer;
  display: inline;
}
.jv-collapsed:hover .jv-ellipsis { color: #94a3b8; }

.jv-unknown { color: #f87171; }
</style>
