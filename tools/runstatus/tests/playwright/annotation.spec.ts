/**
 * annotation.spec.ts — verifies the annotation slice behaviour in static mode.
 *
 * The static artifact (window.__KITSOKI_SNAPSHOT__ set) is a read-only export
 * with no live server. AnnotateButton must NOT render in this mode because
 * there is no RPC endpoint to POST to.
 *
 * Live-mode annotation (with a real server) requires an integration test
 * environment and is out of scope for this spec.
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
}

/** Expand the first agent.call.complete event to open EventDetail. */
async function expandFirstAgentEvent(page: Page): Promise<void> {
  // Expand any event row with the "+" button (first one available).
  const expandBtns = page.locator(".trace-timeline__expand-btn");
  await expandBtns.first().click();
}

test.describe("annotation slice (static mode)", () => {
  test("AnnotateButton is NOT rendered in static artifact", async ({ page }) => {
    await load(page);

    // Expand the first event to open EventDetail.
    await expandFirstAgentEvent(page);

    // The annotate-button trigger should be absent because we are in static
    // mode (window.__KITSOKI_SNAPSHOT__ is defined).
    const annotateBtn = page.locator('[data-testid="annotate-button"]');
    await expect(annotateBtn).toHaveCount(0);
  });

  test("annotate form fields are absent in static mode", async ({ page }) => {
    await load(page);
    await expandFirstAgentEvent(page);

    // None of the form data-testids should appear.
    for (const testid of ["annotate-score", "annotate-label", "annotate-comment", "annotate-submit"]) {
      await expect(page.locator(`[data-testid="${testid}"]`)).toHaveCount(0);
    }
  });
});
