import { fileURLToPath } from "node:url";
import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";

// Vite lib-mode build (D3: "a standard Vite lib-mode build") producing ONE
// self-contained ES module, graph.mjs, written directly next to this config
// (not into a dist/ subdir) since that IS the served path
// (internal/runstatus/server/kit_ui.go serves <kit-root>/ui/<entry>.mjs, and
// kit.yaml's provides.ui declares entry: graph). Bundles vue + cytoscape
// in — see src/main.ts's doc comment for why this isn't externalized against
// a shared host singleton yet.
const uiRoot = fileURLToPath(new URL(".", import.meta.url));

export default defineConfig({
  plugins: [vue()],
  build: {
    outDir: uiRoot,
    emptyOutDir: false,
    lib: {
      entry: fileURLToPath(new URL("./src/main.ts", import.meta.url)),
      name: "KitsokiObjectGraph",
      formats: ["es"],
      fileName: () => "graph.mjs",
    },
    target: "es2020",
  },
});
