/**
 * Standalone Playwright snapshot script.
 */
import { chromium } from "@playwright/test";
import path from "path";

const outPath = process.argv[2] ?? "/tmp/snap.png";

async function main() {
  const browser = await chromium.launch({ headless: true, args: ["--no-sandbox"] });
  const page = await browser.newPage();
  await page.setViewportSize({ width: 1400, height: 900 });

  await page.goto("http://localhost:7777/", { timeout: 10000, waitUntil: "domcontentloaded" });
  await page.waitForTimeout(2000);

  const oregonLink = page.getByText(/oregon.trail/i).first();
  if (await oregonLink.count() > 0) {
    console.log("clicking Oregon Trail link");
    await oregonLink.click();
    await page.waitForTimeout(2500);
  } else {
    console.log("no Oregon Trail home-screen link; trying active session link");
    const sessionLink = page.locator(".session-item a, a[href*='session'], [data-testid*='session']").first();
    if (await sessionLink.count() > 0) {
      await sessionLink.click();
      await page.waitForTimeout(2500);
    }
  }

  // Full screenshot
  await page.screenshot({ path: outPath, fullPage: false });
  console.log(`saved: ${outPath}`);

  // Also crop the bottom input area (right panel, full width)
  const cropPath = outPath.replace(/\.png$/, "-input-crop.png");
  await page.screenshot({ path: cropPath, clip: { x: 0, y: 800, width: 1400, height: 100 } });
  console.log(`saved crop: ${cropPath}`);

  await browser.close();
}

main().catch((e) => { console.error(e); process.exit(1); });
