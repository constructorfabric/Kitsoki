/**
 * One-shot script to capture the Oregon Trail input area for before/after comparison.
 * Run: pnpm playwright test tests/playwright/input-screenshot.spec.ts --reporter=list
 */
import { test } from "@playwright/test";
import path from "path";
import { fileURLToPath } from "url";
import fs from "fs";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const repoRoot = path.resolve(__dirname, "../../..");

const outPath = process.env.SCREENSHOT_PATH || path.join(repoRoot, ".artifacts/input-style-qa/before.png");

test("capture input bar", async ({ page }) => {
  await page.setViewportSize({ width: 1600, height: 900 });
  // Kitsoki server at 7777 has the Oregon Trail session and serves the full UI
  await page.goto("http://localhost:7777", { waitUntil: "networkidle", timeout: 15000 });
  await page.waitForTimeout(1500);

  // Try to navigate to an Oregon Trail session
  const oregonLink = page.getByText(/oregon.trail/i).first();
  if (await oregonLink.count() > 0) {
    await oregonLink.click();
    await page.waitForTimeout(2000);
  } else {
    const firstSession = page.locator("a[href*='session'], .session-link, [data-testid*='session']").first();
    if (await firstSession.count() > 0) {
      await firstSession.click();
      await page.waitForTimeout(2000);
    }
  }

  fs.mkdirSync(path.dirname(outPath), { recursive: true });
  await page.screenshot({ path: outPath, fullPage: false });
  console.log(`Saved: ${outPath}`);
});
