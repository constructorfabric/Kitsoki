/**
 * Default VitePress theme extended with the feature-catalog components. The
 * promo landing and every /features/<id> page are assembled from these — the
 * "thin layer" principle: one component set, one data source (the feature
 * catalog), rendered on both surfaces.
 */
import DefaultTheme from "vitepress/theme";
import type { Theme } from "vitepress";
import ChapteredVideo from "./components/ChapteredVideo.vue";
import TourStepCards from "./components/TourStepCards.vue";
import FeatureDemo from "./components/FeatureDemo.vue";
import FeatureGrid from "./components/FeatureGrid.vue";
import HeroDemo from "./components/HeroDemo.vue";
import "./custom.css";

export default {
  extends: DefaultTheme,
  enhanceApp({ app }) {
    app.component("ChapteredVideo", ChapteredVideo);
    app.component("TourStepCards", TourStepCards);
    app.component("FeatureDemo", FeatureDemo);
    app.component("FeatureGrid", FeatureGrid);
    app.component("HeroDemo", HeroDemo);
  },
} satisfies Theme;
