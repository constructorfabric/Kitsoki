// main.ts — @kitsoki/object-graph's kit UI module entry point (S5, D3). The
// built output (graph.mjs, checked in next to this file — see package.json)
// is what internal/runstatus/server/kit_ui.go serves at
// /kit/kitsoki/object-graph/ui/graph.mjs, and what the SPA's KitPage.vue
// (tools/runstatus/src/kits/KitPage.vue) dynamic-imports and calls
// `.default.mount(el, params)` on — see that file's doc comment for the
// mount contract this default export satisfies.
//
// This module bundles its own Vue + Cytoscape (not shared with the host
// SPA's instance) — a deliberate scope-down from D3's most ambitious "one
// shared Vue singleton via an import map" vision; see kit-rpc.ts's doc
// comment for the full rationale and what a real @kitsoki/ui-sdk extraction
// would change here later.
import { createApp } from "vue";
import ObjectGraphPage from "./ObjectGraphPage.vue";

// Vite's lib-mode build extracts every SFC's <style> into a sibling CSS file
// (object-graph-ui.css, next to graph.mjs) rather than inlining it into the
// JS — inject a <link> for it, resolved relative to THIS module's own URL
// (import.meta.url) so it works regardless of which namespace/kit path this
// module was served from.
let styleInjected = false;
function ensureStyleInjected(): void {
  if (styleInjected) return;
  styleInjected = true;
  const href = new URL("./object-graph-ui.css", import.meta.url).href;
  if (document.querySelector(`link[href="${href}"]`)) return;
  const link = document.createElement("link");
  link.rel = "stylesheet";
  link.href = href;
  document.head.appendChild(link);
}

export default {
  mount(el: HTMLElement, params: Record<string, string>): () => void {
    ensureStyleInjected();
    const app = createApp(ObjectGraphPage, {
      catalogPath: params.catalog,
      overlayPath: params.overlay,
    });
    app.mount(el);
    return () => app.unmount();
  },
};
