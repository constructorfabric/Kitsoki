/**
 * Dynamic-route loader: one SSG page per feature in the catalog. Params carry
 * everything FeatureDemo renders; the feature's optional `narrative` markdown
 * becomes the page content (rendered at <!-- @content --> in [id].md).
 */
import { loadFeatures } from "../../.vitepress/data/features.js";

export default {
  paths() {
    return loadFeatures().map((f) => ({
      params: {
        id: f.id,
        kind: f.kind,
        title: f.title,
        tagline: f.tagline,
        summary: f.summary,
        media: f.media,
        steps: f.steps,
        docLinks: f.docLinks,
        related: f.related,
        demoSpec: f.demoSpec,
      },
      content: f.narrative ?? "",
    }));
  },
};
