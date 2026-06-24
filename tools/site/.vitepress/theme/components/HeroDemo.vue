<script setup lang="ts">
/**
 * The landing page's headline demo: the lowest-promo-order highlighted feature
 * (the onboarding tour) rendered with the same ChapteredVideo player the
 * feature pages use — the promo layer reuses, never duplicates.
 */
import { computed } from "vue";
import { useData } from "vitepress";
import ChapteredVideo from "./ChapteredVideo.vue";

interface HeroFeature {
  id: string;
  title: string;
  promo: { order: number; highlight?: boolean } | null;
  media: { videoUrl: string | null; posterUrl: string | null; chaptersUrl: string | null; videoAvailable: boolean };
}

const { theme } = useData();

const hero = computed<HeroFeature | null>(() => {
  const feats = (theme.value.features as HeroFeature[]).filter((f) => f.promo?.highlight);
  if (feats.length === 0) return null;
  return feats.slice().sort((a, b) => (a.promo!.order ?? 999) - (b.promo!.order ?? 999))[0];
});
</script>

<template>
  <div v-if="hero" class="khero">
    <ChapteredVideo :media="hero.media" :title="hero.title" :feature-id="hero.id" />
  </div>
</template>
