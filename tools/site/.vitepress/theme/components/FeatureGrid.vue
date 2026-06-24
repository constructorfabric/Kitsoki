<script setup lang="ts">
/**
 * The feature-card grid — the promo landing's centerpiece and the /features/
 * index. Cards render the SAME title/tagline/poster as the feature pages they
 * link to (one data source: themeConfig.features, from the feature catalog).
 *
 * Props: `kinds` filters by feature kind; `promoOnly` keeps only features with
 * a promo block (the landing page), sorted by promo.order.
 */
import { computed } from "vue";
import { withBase, useData } from "vitepress";

const props = withDefaults(
  defineProps<{
    kinds?: string[];
    promoOnly?: boolean;
  }>(),
  { kinds: () => ["feature", "product-tour", "story-demo"], promoOnly: false },
);

interface GridFeature {
  id: string;
  kind: string;
  title: string;
  tagline: string;
  promo: { order: number; highlight?: boolean } | null;
  media: { posterUrl: string | null };
}

const { theme } = useData();
const text = computed(() => theme.value.siteText?.labels ?? {});

const cards = computed(() => {
  let feats = (theme.value.features as GridFeature[]).filter((f) => props.kinds.includes(f.kind));
  if (props.promoOnly) feats = feats.filter((f) => f.promo);
  return feats
    .slice()
    .sort((a, b) => (a.promo?.order ?? 999) - (b.promo?.order ?? 999) || a.title.localeCompare(b.title));
});
</script>

<template>
  <div class="kgrid">
    <a
      v-for="f in cards"
      :key="f.id"
      class="kgrid__card"
      :class="{ 'kgrid__card--highlight': f.promo?.highlight }"
      :href="withBase(`/features/${f.id}.html`)"
    >
      <img
        v-if="f.media.posterUrl"
        class="kgrid__poster"
        :src="withBase(f.media.posterUrl)"
        :alt="`${f.title} — ${text.demoPosterAlt ?? 'demo poster'}`"
        loading="lazy"
      />
      <div v-else class="kgrid__poster kgrid__poster--empty" aria-hidden="true">▶</div>
      <h3 class="kgrid__title">{{ f.title }}</h3>
      <p class="kgrid__tagline">{{ f.tagline }}</p>
    </a>
  </div>
</template>
