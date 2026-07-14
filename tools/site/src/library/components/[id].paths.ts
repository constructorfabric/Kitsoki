import { loadExtensionLibrary } from "../../../.vitepress/data/extensions.js";

export default {
  paths() {
    return loadExtensionLibrary().components.map((component) => ({
      params: { id: component.slug, component },
    }));
  },
};
