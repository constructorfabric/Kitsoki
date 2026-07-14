/**
 * Default VitePress theme extended with the feature-catalog components. The
 * promo landing and every /features/<id> page are assembled from these — the
 * "thin layer" principle: one component set, one data source (the feature
 * catalog), rendered on both surfaces.
 */
import DefaultTheme from "vitepress/theme";
import type { Theme } from "vitepress";
import { defineComponent, h, onMounted, onUnmounted, watch } from "vue";
import { useRoute } from "vitepress";
import ChapteredVideo from "./components/ChapteredVideo.vue";
import TourStepCards from "./components/TourStepCards.vue";
import FeatureDemo from "./components/FeatureDemo.vue";
import FeatureGrid from "./components/FeatureGrid.vue";
import HeroDemo from "./components/HeroDemo.vue";
import DeckGallery from "./components/DeckGallery.vue";
import DeckViewer from "./components/DeckViewer.vue";
import ExtensionLibrary from "./components/ExtensionLibrary.vue";
import ExtensionPackage from "./components/ExtensionPackage.vue";
import ExtensionStory from "./components/ExtensionStory.vue";
import ExtensionComponent from "./components/ExtensionComponent.vue";
import { renderMermaidDiagrams, watchMermaidTheme } from "./mermaid";
import "./custom.css";

const Layout = defineComponent({
  name: "KitsokiSiteLayout",
  setup() {
    const route = useRoute();
    let stopThemeWatcher: (() => void) | null = null;

    onMounted(() => {
      stopThemeWatcher = watchMermaidTheme();
      void renderMermaidDiagrams();
    });
    onUnmounted(() => stopThemeWatcher?.());
    watch(
      () => route.path,
      () => {
        void renderMermaidDiagrams();
      },
      { flush: "post" },
    );

    return () => h(DefaultTheme.Layout);
  },
});

export default {
  extends: DefaultTheme,
  Layout,
  enhanceApp({ app }) {
    app.component("ChapteredVideo", ChapteredVideo);
    app.component("TourStepCards", TourStepCards);
    app.component("FeatureDemo", FeatureDemo);
    app.component("FeatureGrid", FeatureGrid);
    app.component("HeroDemo", HeroDemo);
    app.component("DeckGallery", DeckGallery);
    app.component("DeckViewer", DeckViewer);
    app.component("ExtensionLibrary", ExtensionLibrary);
    app.component("ExtensionPackage", ExtensionPackage);
    app.component("ExtensionStory", ExtensionStory);
    app.component("ExtensionComponent", ExtensionComponent);
  },
} satisfies Theme;
