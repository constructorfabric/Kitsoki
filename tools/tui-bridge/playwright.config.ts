import { defineConfig } from "@playwright/test";
import path from "node:path";

// Two servers: the Go pty-bridge (tui-serve, spawning a deterministic no-LLM
// /bin/cat per connection for the test) and the static player page. Both are
// started fresh per run so the spec never depends on a developer's own
// `kitsoki tui-serve` still being up.
const PLAYER_PORT = Number(process.env.TUI_BRIDGE_PLAYER_PORT ?? "4320");
const BRIDGE_ADDR = process.env.TUI_BRIDGE_ADDR ?? "127.0.0.1:4700";
const REPO_ROOT = path.resolve(import.meta.dirname, "..", "..");

export default defineConfig({
  testDir: "./tests",
  fullyParallel: false,
  workers: 1,
  reporter: [["list"]],
  timeout: 60_000,
  webServer: [
    {
      command: `go run ./cmd/kitsoki tui-serve --addr ${BRIDGE_ADDR} --exec /bin/cat`,
      cwd: REPO_ROOT,
      port: Number(BRIDGE_ADDR.split(":")[1]),
      reuseExistingServer: !process.env.CI,
      timeout: 60_000,
    },
    {
      command: "node player/serve.mjs",
      url: `http://localhost:${PLAYER_PORT}/player/`,
      reuseExistingServer: !process.env.CI,
      env: { TUI_BRIDGE_PLAYER_PORT: String(PLAYER_PORT) },
      timeout: 30_000,
    },
  ],
  use: {
    baseURL: `http://localhost:${PLAYER_PORT}`,
  },
  projects: [{ name: "chromium", use: { browserName: "chromium" } }],
});
