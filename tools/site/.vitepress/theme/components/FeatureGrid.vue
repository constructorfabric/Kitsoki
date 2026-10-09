<script setup lang="ts">
/**
 * The feature-card grid — the promo landing's centerpiece and the /features/
 * index. Cards render the SAME title/tagline/poster as the feature pages they
 * link to (one data source: themeConfig.features, from the feature catalog).
 *
 * Props: `kinds` filters by feature kind; `promoOnly` keeps only features with
 * a promo block (the landing page), sorted by promo.order. `ids` renders an
 * explicit curated set in the order provided.
 */
import { computed } from "vue";
import { withBase, useData } from "vitepress";

const props = withDefaults(
  defineProps<{
    ids?: string[];
    kinds?: string[];
    promoOnly?: boolean;
  }>(),
  { ids: undefined, kinds: () => ["feature", "product-tour", "story-demo"], promoOnly: false },
);

interface GridFeature {
  id: string;
  kind: string;
  title: string;
  tagline: string;
  promo: { order: number; highlight?: boolean } | null;
  media: { posterUrl: string | null; videoAvailable?: boolean; embedUrl?: string | null; embedKind?: "deck" | "rrweb" | null };
}

const { theme } = useData();
const text = computed(() => theme.value.siteText?.labels ?? {});

const cards = computed(() => {
  const all = theme.value.features as GridFeature[];
  if (props.ids?.length) {
    const byId = new Map(all.map((f) => [f.id, f]));
    return props.ids.map((id) => byId.get(id)).filter((f): f is GridFeature => !!f);
  }

  let feats = all.filter((f) => props.kinds.includes(f.kind));
  if (props.promoOnly) feats = feats.filter((f) => f.promo);
  return feats
    .slice()
    .sort((a, b) => (a.promo?.order ?? 999) - (b.promo?.order ?? 999) || a.title.localeCompare(b.title));
});

function kindLabel(kind: string): string {
  if (kind === "product-tour") return "tour";
  if (kind === "story-demo") return "case study";
  return "feature";
}

function mediaLabel(f: GridFeature): string {
  if (f.media.videoAvailable) return "video";
  if (f.media.embedKind === "rrweb") return "replay";
  if (f.media.embedUrl) return "deck";
  return "text";
}
</script>

<template>
  <div class="kgrid">
    <a
      v-for="f in cards"
      :key="f.id"
      class="kgrid__card"
      :class="{ 'kgrid__card--highlight': f.promo?.highlight, 'kgrid__card--text': !f.media.posterUrl }"
      :href="withBase(`/features/${f.id}.html`)"
    >
      <div v-if="f.media.posterUrl" class="kgrid__media">
        <img
          class="kgrid__poster"
          :src="withBase(f.media.posterUrl)"
          :alt="`${f.title} — ${text.demoPosterAlt ?? 'demo poster'}`"
          loading="lazy"
        />
      </div>
      <div class="kgrid__badges">
        <span>{{ kindLabel(f.kind) }}</span>
        <span>{{ mediaLabel(f) }}</span>
      </div>
      <h3 class="kgrid__title">{{ f.title }}</h3>
      <p class="kgrid__tagline">{{ f.tagline }}</p>
    </a>
  </div>
</template>
