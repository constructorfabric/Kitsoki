/**
 * view-modes.spec.ts — exercises the ViewModeTabs tri-view switcher
 * (Tree / Timeline / Graph) using the bugfix snapshot fixture.
 *
 * Assertions:
 *  - All three tab buttons are present.
 *  - Switching to "Timeline" renders TraceWaterfall bars.
 *  - Switching to "Graph" renders the StateDiagram.
 *  - Switching back to "Tree" renders TraceTimeline rows.
 *  - Tab switching does not trigger a new XHR/fetch request.
 *  - URL hash reflects the active tab mode.
 */

import { test, expect, type Page } from "@playwright/test";
import { fileURLToPath } from "url";
import path from "path";
import { buildArtifact } from "./_helpers/artifact.js";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

const FIXTURES_DIR = path.resolve(__dirname, "../../fixtures");
const BUGFIX_SNAPSHOT = path.join(FIXTURES_DIR, "bugfix.snapshot.json");

async function load(page: Page): Promise<void> {
  const url = buildArtifact(BUGFIX_SNAPSHOT);
  await page.goto(url);
  await page.waitForSelector(".run-view__topbar", { timeout: 10000 });
  // Wait for ViewModeTabs to mount
  await page.waitForSelector('[data-testid="view-mode-tabs"]', { timeout: 8000 });
}

test.describe("ViewModeTabs — multi-view switcher", () => {
  test("all three tab buttons are present", async ({ page }) => {
    await load(page);
    await expect(page.locator('[data-testid="tab-tree"]')).toBeVisible();
    await expect(page.locator('[data-testid="tab-timeline"]')).toBeVisible();
    await expect(page.locator('[data-testid="tab-graph"]')).toBeVisible();
  });

  test("default view is Tree — TraceTimeline rows are visible", async ({ page }) => {
    await load(page);
    // Tree is the default; trace-timeline rows should be present
    await page.waitForSelector(".trace-timeline__row", { timeout: 8000 });
    const rows = page.locator(".trace-timeline__row");
    await expect(rows.first()).toBeVisible();
  });

  test("switching to Timeline tab renders waterfall bars", async ({ page }) => {
    await load(page);

    // Click the Timeline tab
    await page.locator('[data-testid="tab-timeline"]').click();

    // Wait for waterfall bars to appear
    await page.waitForSelector('[data-testid="waterfall-bar"]', { timeout: 8000 });
    const bars = page.locator('[data-testid="waterfall-bar"]');
    await expect(bars.first()).toBeVisible();

    // Bars should have a numeric data-duration-ms attribute
    const firstBar = bars.first();
    const durationMs = await firstBar.getAttribute("data-duration-ms");
    expect(durationMs).not.toBeNull();
    expect(Number(durationMs)).toBeGreaterThanOrEqual(0);
  });

  test("switching to Graph tab renders StateDiagram", async ({ page }) => {
    await load(page);

    // Click the Graph tab
    await page.locator('[data-testid="tab-graph"]').click();

    // StateDiagram should render phases
    await page.waitForSelector(".state-diagram__phase", { timeout: 8000 });
    await expect(page.locator(".state-diagram__phase").first()).toBeVisible();
  });

  test("switching back to Tree tab shows timeline rows again", async ({ page }) => {
    await load(page);

    // Go to Graph
    await page.locator('[data-testid="tab-graph"]').click();
    await page.waitForSelector(".state-diagram__phase", { timeout: 8000 });

    // Go back to Tree
    await page.locator('[data-testid="tab-tree"]').click();
    await page.waitForSelector(".trace-timeline__row", { timeout: 8000 });
    await expect(page.locator(".trace-timeline__row").first()).toBeVisible();
  });

  test("tab switching does not trigger a network refetch", async ({ page }) => {
    const xhrRequests: string[] = [];

    // Track all fetch/XHR requests AFTER initial page load
    await load(page);

    page.on("request", (req) => {
      // Only track requests that are not the initial page/asset load
      if (req.resourceType() === "fetch" || req.resourceType() === "xhr") {
        xhrRequests.push(req.url());
      }
    });

    // Switch through all tabs
    await page.locator('[data-testid="tab-timeline"]').click();
    await page.locator('[data-testid="tab-graph"]').click();
    await page.locator('[data-testid="tab-tree"]').click();

    // Give a moment for any async requests to fire
    await page.waitForTimeout(200);

    expect(xhrRequests).toHaveLength(0);
  });

  test("URL hash changes to reflect active tab mode", async ({ page }) => {
    await load(page);

    // Switch to timeline
    await page.locator('[data-testid="tab-timeline"]').click();
    const hashAfterTimeline = await page.evaluate(() => window.location.hash);
    expect(hashAfterTimeline).toContain("timeline");

    // Switch to graph
    await page.locator('[data-testid="tab-graph"]').click();
    const hashAfterGraph = await page.evaluate(() => window.location.hash);
    expect(hashAfterGraph).toContain("graph");

    // Switch back to tree
    await page.locator('[data-testid="tab-tree"]').click();
    const hashAfterTree = await page.evaluate(() => window.location.hash);
    expect(hashAfterTree).toContain("tree");
  });
});
