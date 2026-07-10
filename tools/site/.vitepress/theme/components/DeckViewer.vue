<script setup lang="ts">
import { withBase } from "vitepress";

defineProps<{
  deck: {
    id: string;
    title: string;
    subtitle: string;
    sceneCount: number;
    sourcePath: string;
    bundlePath: string;
    bundled: boolean;
    viewerUrl: string | null;
  };
}>();
</script>

<template>
  <section class="kdeck-viewer">
    <div class="kdeck-viewer__bar">
      <a :href="withBase('/decks/index.html')">Deck gallery</a>
      <span>{{ deck.sceneCount }} scenes</span>
      <span>{{ deck.sourcePath }}</span>
    </div>

    <iframe
      v-if="deck.viewerUrl"
      class="kdeck-viewer__frame"
      :src="withBase(deck.viewerUrl)"
      sandbox="allow-scripts allow-same-origin"
      :title="`${deck.title} Slidey deck`"
    />
    <div v-else class="kdeck-viewer__missing">
      <strong>Viewer bundle missing</strong>
      <p>Bundle this deck to {{ deck.bundlePath }} and rebuild the site.</p>
    </div>
  </section>
</template>
