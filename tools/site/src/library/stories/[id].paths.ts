import { loadExtensionLibrary } from "../../../.vitepress/data/extensions.js";

export default {
  paths() {
    return loadExtensionLibrary().stories.map((story) => ({
      params: { id: story.slug, story },
    }));
  },
};
