import { loadFeatures } from "../../../.vitepress/data/features.js";

export default {
  paths() {
    return loadFeatures("ja").map((f) => ({
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
