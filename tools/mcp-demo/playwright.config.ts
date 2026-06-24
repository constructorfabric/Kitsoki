import { defineConfig } from "@playwright/test";

// Single-origin static server for the player page + xterm dist. The spec creates
// its own recording context via cameraContext(), so the project `use` block stays
// minimal — only the webServer matters here.
const PORT = Number(process.env.MCP_DEMO_PORT ?? "4319");

export default defineConfig({
  testDir: "./tests",
  fullyParallel: false,
  workers: 1,
  reporter: [["list"]],
  timeout: 180_000,
  webServer: {
    command: "node player/serve.mjs",
    url: `http://localhost:${PORT}/player/`,
    reuseExistingServer: !process.env.CI,
    env: { MCP_DEMO_PORT: String(PORT) },
    timeout: 30_000,
  },
  projects: [{ name: "chromium", use: { browserName: "chromium" } }],
});
