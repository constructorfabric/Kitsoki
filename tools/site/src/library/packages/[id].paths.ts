import { loadExtensionLibrary } from "../../../.vitepress/data/extensions.js";

export default {
  paths() {
    return loadExtensionLibrary().packages.map((pkg) => ({
      params: { id: pkg.slug, pkg },
    }));
  },
};
