import { createApp } from "vue";
import { createPinia } from "pinia";
import App from "./App.vue";
import router from "./router.js";
import { installConsoleCapture } from "./data/console-capture.js";
import { installErrorCapture, vueErrorHandler } from "./data/error-capture.js";
import { startSessionCapture } from "./data/session-capture.js";

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

// Bug-report capture layer: console/error/session capture run for the whole
// app lifetime so a bug report can attach recent context. Each install is
// guarded internally and never throws into the app.
installConsoleCapture();
installErrorCapture();
startSessionCapture();

const app = createApp(App);
app.config.errorHandler = vueErrorHandler;
app.use(createPinia());
app.use(router);
app.mount("#app");
