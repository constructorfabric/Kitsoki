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
