<script setup lang="ts">
/**
 * The whole demo block of a /features/<id> page: the chaptered video wired to
 * the step-by-step cards (card click → video seeks to that step's chapter),
 * plus the deeper-docs / related-features links. Receives the page's $params
 * (from src/features/[id].paths.ts) verbatim.
 */
import { computed, ref } from "vue";
import { useData, withBase } from "vitepress";
import ChapteredVideo from "./ChapteredVideo.vue";
import TourStepCards from "./TourStepCards.vue";

interface Link {
  text: string;
  href: string;
}

const props = defineProps<{
  feature: {
    id: string;
    title: string;
    summary: string;
    media: { videoUrl: string | null; posterUrl: string | null; chaptersUrl: string | null; videoAvailable: boolean };
    steps: Array<{ id: string; title: string; body: string; shotUrl: string | null }>;
    docLinks: Link[];
    related: Link[];
  };
}>();

const player = ref<InstanceType<typeof ChapteredVideo> | null>(null);
const { theme } = useData();
const text = computed(() => theme.value.siteText?.labels ?? {});

function href(l: Link): string {
  return l.href.startsWith("/") ? withBase(l.href) : l.href;
}
</script>

<template>
  <div class="kdemo">
    <p class="kdemo__summary">{{ feature.summary }}</p>

    <ChapteredVideo ref="player" :media="feature.media" :title="feature.title" :feature-id="feature.id" />

    <template v-if="feature.steps.length">
      <h2 id="step-by-step">{{ text.stepByStep ?? "Step by step" }}</h2>
      <TourStepCards :steps="feature.steps" @seek="(id) => player?.seekToStep(id)" />
    </template>

    <aside v-if="feature.docLinks.length || feature.related.length" class="kdemo__links">
      <div v-if="feature.docLinks.length">
        <h3>{{ text.deeperDocs ?? "Deeper docs" }}</h3>
        <ul>
          <li v-for="l in feature.docLinks" :key="l.href">
            <a :href="href(l)">{{ l.text }}</a>
          </li>
        </ul>
      </div>
      <div v-if="feature.related.length">
        <h3>{{ text.relatedFeatures ?? "Related features" }}</h3>
        <ul>
          <li v-for="l in feature.related" :key="l.href">
            <a :href="href(l)">{{ l.text }}</a>
          </li>
        </ul>
      </div>
    </aside>
  </div>
</template>
