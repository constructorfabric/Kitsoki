import path from "node:path";
import { fileURLToPath } from "node:url";
import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";
import { viteSingleFile } from "vite-plugin-singlefile";

// In dev mode, proxy /rpc and /rpc/events to the kitsoki Go backend so the
// Vite HMR dev server can serve the Vue app while the real JSON-RPC surface
// runs in the Go process. Set KITSOKI_API to override the default address.
const apiBase = process.env.KITSOKI_API ?? "http://127.0.0.1:7777";
const runstatusRoot = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.resolve(runstatusRoot, "../..");
const tempRoot = process.env.KITSOKI_TEMP_ROOT ?? path.join(repoRoot, ".temp");

export default defineConfig({
  root: runstatusRoot,
  // Webview-relative asset resolution: under a VS Code webview the SPA is loaded
  // via asWebviewUri (not from a server root), so emitted asset URLs must be
  // relative ("./") rather than absolute ("/"). The single-file build inlines
  // everything, so this is harmless for the browser/Go-embed build too.
  base: "./",
  cacheDir: path.join(tempRoot, "runstatus", "vite-cache"),
  plugins: [vue(), viteSingleFile()],
  server: {
    middlewareMode: false,
    fs: {
      allow: [runstatusRoot],
    },
    host: "127.0.0.1",
    port: parseInt(process.env.VITE_PORT ?? "5173"),
    // Vite 6 rejects any request whose Host header isn't localhost/127.0.0.1
    // ("Blocked request. This host is not allowed."), which breaks access
    // through SSH/cloudflared/ngrok tunnels that present a different Host.
    // This is a localhost-bound dev server, so accept any Host header.
    allowedHosts: true,
    proxy: {
      // timeout: 0 / proxyTimeout: 0 disables http-proxy's built-in timeout so
      // LLM-backed turn/submit/continue/offpath calls (which can take 30-120s)
      // never produce a 504 Gateway Timeout from the Vite dev proxy. The Go
      // backend uses ReadHeaderTimeout only (no WriteTimeout) for the same reason.
      "/rpc": { target: apiBase, changeOrigin: true, timeout: 0, proxyTimeout: 0 },
      // /artifact/<handle> (+ /artifact/<handle>/poster) serves the rendered
      // media — slidey decks, posters, video grabs — straight off the Go
      // backend's artifact substrate. Without this proxy the dev server's SPA
      // catch-all answers every artifact URL with index.html, so a deck iframe
      // renders the app (empty), "Open the interactive deck" opens the app in a
      // new tab, and the annotator loads the app instead of the deck.
      "/artifact": { target: apiBase, changeOrigin: true, timeout: 0, proxyTimeout: 0 },
    },
  },
  build: {
    target: "es2020",
    outDir: path.join(tempRoot, "runstatus", "dist"),
    emptyOutDir: true,
  },
});
