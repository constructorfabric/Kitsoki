<script setup lang="ts">
/**
 * The step-by-step walkthrough: one card per tour step (the same steps that
 * drive the live web-UI tour overlay and the recorded demo — straight from the
 * feature catalog). Clicking a card emits `seek` so the page can jump the demo
 * video to that step's chapter.
 */
import { computed } from "vue";
import { withBase, useData } from "vitepress";

interface Step {
  id: string;
  title: string;
  body: string;
  shotUrl: string | null;
}

defineProps<{ steps: Step[] }>();
const emit = defineEmits<{ seek: [stepId: string] }>();

const { theme } = useData();
const embedded = theme.value.siteVariant === "embedded";
const text = computed(() => theme.value.siteText?.labels ?? {});
</script>

<template>
  <ol class="ksteps">
    <li v-for="(s, i) in steps" :key="s.id" class="ksteps__card">
      <button class="ksteps__head" type="button" :title="text.jumpToStep ?? 'Jump the video to this step'" @click="emit('seek', s.id)">
        <span class="ksteps__num">{{ i + 1 }}</span>
        <span class="ksteps__title">{{ s.title }}</span>
      </button>
      <p class="ksteps__body">{{ s.body }}</p>
      <img
        v-if="s.shotUrl && !embedded"
        class="ksteps__shot"
        :src="withBase(s.shotUrl)"
        :alt="`${s.title} — screenshot`"
        loading="lazy"
        @click="emit('seek', s.id)"
      />
    </li>
  </ol>
</template>
