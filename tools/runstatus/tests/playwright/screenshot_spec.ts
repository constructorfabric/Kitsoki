import { test } from "@playwright/test";
import path from "path";
import { buildArtifact } from "./tests/playwright/_helpers/artifact.js";
import { fileURLToPath } from "url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

const FIXTURES_DIR = path.resolve(__dirname, "fixtures");
const BUGFIX_SNAPSHOT = path.join(FIXTURES_DIR, "bugfix.snapshot.json");

test("screenshot", async ({ page }) => {
  const url = buildArtifact(BUGFIX_SNAPSHOT);
  await page.goto(url);
  await page.waitForSelector(".run-view__topbar", { timeout: 10000 });
  await page.setViewportSize({ width: 1600, height: 900 });
  await page.screenshot({ path: "/tmp/runstatus_full.png", fullPage: false });
});
