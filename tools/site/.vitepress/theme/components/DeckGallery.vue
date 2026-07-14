<script setup lang="ts">
/**
 * Gallery of committed Slidey decks. The thumbnail intentionally renders the
 * first title scene's own text and theme colors instead of a generated image, so
 * the catalog is cheap to build and stays useful even when deck bundles are not
 * staged yet.
 */
import { computed } from "vue";
import { useData, withBase } from "vitepress";

interface GalleryDeck {
  id: string;
  title: string;
  eyebrow: string;
  subtitle: string;
  sceneCount: number;
  bundled: boolean;
  previewTheme: {
    background: string;
    surface: string;
    text: string;
    subtle: string;
    accent: string;
    accent2: string;
  };
}

const { theme } = useData();
const decks = computed(() => (theme.value.decks ?? []) as GalleryDeck[]);

function styleFor(deck: GalleryDeck): Record<string, string> {
  return {
    "--kdeck-bg": deck.previewTheme.background,
    "--kdeck-surface": deck.previewTheme.surface,
    "--kdeck-text": deck.previewTheme.text,
    "--kdeck-subtle": deck.previewTheme.subtle,
    "--kdeck-accent": deck.previewTheme.accent,
    "--kdeck-accent2": deck.previewTheme.accent2,
  };
}
</script>

<template>
  <div class="kdeck-grid">
    <a v-for="deck in decks" :key="deck.id" class="kdeck-card" :href="withBase(`/decks/${deck.id}.html`)">
      <div class="kdeck-card__slide" :style="styleFor(deck)">
        <div class="kdeck-card__eyebrow">{{ deck.eyebrow || "Slidey deck" }}</div>
        <div class="kdeck-card__rule"></div>
        <div class="kdeck-card__title">{{ deck.title }}</div>
        <div v-if="deck.subtitle" class="kdeck-card__subtitle">{{ deck.subtitle }}</div>
      </div>
      <div class="kdeck-card__meta">
        <span>{{ deck.sceneCount }} scenes</span>
        <span>{{ deck.bundled ? "viewer" : "source only" }}</span>
      </div>
      <h3>{{ deck.title }}</h3>
      <p>{{ deck.subtitle || "Open the present-only deck viewer in the Kitsoki site shell." }}</p>
    </a>
  </div>
</template>
