import { defineConfig } from "@playwright/test";
import { fileURLToPath } from "url";
import path from "path";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

export default defineConfig({
  testDir: "./tests/playwright",
  workers: 4,
  // No webServer — artifact tests load from file:// URLs directly.
  use: {
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    // Playwright's default actionTimeout is 0 (INFINITE): a click on a missing /
    // covered element hangs the whole run with no error. Cap it so actions
    // fail fast and diagnosably instead. Generous vs expect.timeout (5s) for
    // slower CI, still far below the per-test timeout.
    actionTimeout: 15000,
    navigationTimeout: 15000,
  },
  expect: {
    timeout: 5000,
  },
  projects: [
    {
      name: "chromium",
      use: { browserName: "chromium" },
    },
  ],
  // Run globalSetup once to build dist/index.html before any test.
  globalSetup: path.resolve(__dirname, "./tests/playwright/_helpers/globalSetup.ts"),
  outputDir: "./test-results",
  reporter: [["list"], ["html", { open: "never" }]],
});
