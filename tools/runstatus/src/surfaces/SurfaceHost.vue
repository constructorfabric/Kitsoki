<template>
  <ChatSurface v-if="surface === 'chat'" />
  <TraceSurface v-else-if="surface === 'trace'" />
  <GraphSurface v-else-if="surface === 'graph'" />
</template>

<script setup lang="ts">
// Single-surface root: switches on the resolved surface string and renders just
// that one surface. No router here — single-surface mode is intentionally a flat
// mount (Pinia only). See surfaces/select.ts for the selection contract.
import type { Surface } from "./select.js";
import ChatSurface from "./ChatSurface.vue";
import TraceSurface from "./TraceSurface.vue";
import GraphSurface from "./GraphSurface.vue";

defineProps<{ surface: Surface }>();
</script>
