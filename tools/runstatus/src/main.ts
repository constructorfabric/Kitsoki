// Global semantic token layer — imported before mounting so BOTH the full SPA
// and the single-surface SurfaceHost inherit the theme tokens. See theme.css for
// the var(--vscode-*, fallback) dual-context strategy.
import "./theme.css";
import { createApp } from "vue";
import { createPinia } from "pinia";
import App from "./App.vue";
import router from "./router.js";
import SurfaceHost from "./surfaces/SurfaceHost.vue";
import { resolveSurface } from "./surfaces/select.js";
import { installConsoleCapture } from "./data/console-capture.js";
import { installErrorCapture, vueErrorHandler } from "./data/error-capture.js";
import { startSessionCapture } from "./data/session-capture.js";
import { isEmbedded } from "./lib/embed.js";

// Compact scaling: inside the VS Code webview the SPA reads one step larger than
// the rest of the UI. Mark the root element so theme.css can zoom the embed down
// one step (uniformly across rem AND px sizing). Mirrors InteractiveView's embed
// detection (isEmbedded() or the ?embed=1 demo fallback); the browser is untouched.
const embedded =
  isEmbedded() || new URLSearchParams(window.location.search).get("embed") === "1";
if (embedded) {
  document.documentElement.setAttribute("data-embedded", "true");
}

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

// Surface decomposition (VS Code): each surface (chat / trace / graph) can mount
// standalone, selected by an injected global `window.__KITSOKI_SURFACE` (a plain
// string). A `?surface=` query param is honoured as a browser dev fallback. When
// neither selects a valid surface we keep today's full SPA (App + router) intact.
// Single-surface mode still needs Pinia (the run store) but not the router.
const surface = resolveSurface();

if (surface) {
  const app = createApp(SurfaceHost, { surface });
  app.config.errorHandler = vueErrorHandler;
  app.use(createPinia());
  app.mount("#app");
} else {
  const app = createApp(App);
  app.config.errorHandler = vueErrorHandler;
  app.use(createPinia());
  app.use(router);
  app.mount("#app");
}
