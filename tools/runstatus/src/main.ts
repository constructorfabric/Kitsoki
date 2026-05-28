import { createApp } from "vue";
import { createPinia } from "pinia";
import App from "./App.vue";
import router from "./router.js";

// Bootstrap: parse inlined snapshot JSON (artifact mode).
// The export-status command injects a <script type="application/json"
// id="kitsoki-snapshot"> tag before the SPA boot script. We read it
// here and assign to window.__KITSOKI_SNAPSHOT__ so that
// createDataSource() in source.ts picks up SnapshotSource.
const snapshotEl = document.getElementById("kitsoki-snapshot");
if (snapshotEl) {
  try {
    (window as Window & { __KITSOKI_SNAPSHOT__?: unknown }).__KITSOKI_SNAPSHOT__ =
      JSON.parse(snapshotEl.textContent ?? "");
  } catch {
    console.error("[kitsoki] Failed to parse inlined snapshot JSON");
  }
}

const app = createApp(App);
app.use(createPinia());
app.use(router);
app.mount("#app");
