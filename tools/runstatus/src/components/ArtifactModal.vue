<script setup lang="ts">
// ArtifactModal — a global, fullscreen markdown artifact viewer.
//
// Mounted once in App.vue so any surface (or a demo capture) can full-screen a
// produced markdown artifact — the PRD, a design epic, a bugfix summary — and
// scroll through it. Reuses MarkdownModal (which reads the file via the
// runstatus.file.read RPC and renders it) in its `fullscreen` mode.
//
// Driven by window hooks so the no-LLM rrweb capture can open and scroll an
// artifact deterministically (mirrors __kitsokiSendText / __startTourWithSteps):
//   window.__openArtifact(path)  → full-screen the markdown at `path`
//   window.__closeArtifact()     → close it
// Inert unless something calls them.
import { ref, onMounted, onUnmounted } from "vue";
import MarkdownModal from "./MarkdownModal.vue";

const path = ref<string | null>(null);

function open(p: string) { path.value = p; }
function close() { path.value = null; }

onMounted(() => {
  (window as unknown as { __openArtifact?: (p: string) => void }).__openArtifact = open;
  (window as unknown as { __closeArtifact?: () => void }).__closeArtifact = close;
});
onUnmounted(() => {
  const w = window as unknown as { __openArtifact?: unknown; __closeArtifact?: unknown };
  delete w.__openArtifact;
  delete w.__closeArtifact;
});
</script>

<template>
  <MarkdownModal v-if="path" :path="path" :fullscreen="true" @close="close" />
</template>
