/**
 * decision-detail.spec.ts — Slice #2 coverage: verdict-first layout in DecideDetail.
 *
 * Loads the bugfix snapshot (which contains an agent.call.complete with
 * verb=decide carrying attrs.response.intent.confidence = 0.95) and verifies:
 *  - The verdict block (chosen intent chip) renders above the evidence drawer.
 *  - The confidence bar (data-testid="confidence-bar") is visible in the verdict.
 *  - The evidence drawer is collapsed by default.
 *  - Clicking "Show evidence" expands the prompt/response section.
 */

import { test, expect, type Page } from "@playwright/test";
import { fileURLToPath } from "url";
import path from "path";
import fs from "fs";
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

/**
 * Find the agent.call.complete row with verb=decide.
 * The timeline merges start+complete into one row; the merged row's msg becomes
 * "agent.decide" or "agent.call.complete" depending on the verb.
 * We look for any row containing an agent verb badge text "decide".
 */
function decideRow(page: Page) {
  // Rows that carry a decide badge (rendered inside the body after click), or
  // whose msg label contains "decide".
  return page
    .locator(".trace-timeline__row", {
      has: page.locator(".trace-timeline__msg").filter({ hasText: /decide/ }),
    })
    .first();
}

test.describe("decision-detail: verdict-first layout (Slice #2)", () => {
  test("fixture has an agent.call.complete event with verb=decide and confidence", () => {
    // Pre-flight: assert shape in JSON before the UI test so failure is clear.
    const snap = JSON.parse(fs.readFileSync(BUGFIX_SNAPSHOT, "utf-8"));
    const events = snap.events as Array<{ msg: string; attrs: Record<string, unknown> }>;

    const decideCompletes = events.filter(
      (e) => e.msg === "agent.call.complete" && e.attrs.verb === "decide"
    );
    expect(
      decideCompletes.length,
      "Expected ≥1 agent.call.complete with verb=decide in bugfix snapshot"
    ).toBeGreaterThan(0);

    // Confidence lives at attrs.response.intent.confidence in the bugfix fixture.
    const withConf = decideCompletes.filter((e) => {
      const resp = e.attrs.response as Record<string, unknown> | undefined;
      const intent = resp?.intent as Record<string, unknown> | undefined;
      return typeof intent?.confidence === "number";
    });
    expect(
      withConf.length,
      "Expected at least one decide event to carry response.intent.confidence"
    ).toBeGreaterThan(0);
  });

  test("clicking a decide row shows verdict block above evidence drawer", async ({ page }) => {
    await load(page);

    const row = decideRow(page);
    await expect(row).toBeVisible({ timeout: 8000 });
    await row.click();

    const body = row.locator(".trace-timeline__row-body");
    await expect(body).toBeVisible({ timeout: 5000 });

    // AgentDetail verb badge confirms routing to DecideDetail.
    const verbBadge = body.locator(".agent-detail__verb-badge");
    await expect(verbBadge).toBeVisible({ timeout: 3000 });
    await expect(verbBadge).toContainText(/decide/i);

    // Verdict block must exist and be visible BEFORE the evidence section.
    const verdict = body.locator("[data-testid='decide-verdict']");
    await expect(verdict).toBeVisible({ timeout: 3000 });

    const evidence = body.locator("[data-testid='decide-evidence']");
    await expect(evidence).toBeVisible({ timeout: 3000 });

    // Confirm verdict appears before evidence in DOM order.
    const verdictBox = await verdict.boundingBox();
    const evidenceBox = await evidence.boundingBox();
    expect(verdictBox).not.toBeNull();
    expect(evidenceBox).not.toBeNull();
    // Verdict's top edge must be above evidence's top edge.
    expect(verdictBox!.y).toBeLessThan(evidenceBox!.y);
  });

  test("confidence bar is visible inside the verdict block", async ({ page }) => {
    await load(page);

    const row = decideRow(page);
    await expect(row).toBeVisible({ timeout: 8000 });
    await row.click();

    const body = row.locator(".trace-timeline__row-body");
    await expect(body).toBeVisible({ timeout: 5000 });

    const verdict = body.locator("[data-testid='decide-verdict']");
    await expect(verdict).toBeVisible({ timeout: 3000 });

    const confBar = verdict.locator("[data-testid='confidence-bar']");
    await expect(confBar).toBeVisible({ timeout: 3000 });

    // Verify the data attrs are set.
    const confValue = await confBar.getAttribute("data-confidence");
    expect(confValue).not.toBeNull();
    const conf = parseFloat(confValue!);
    expect(conf).toBeGreaterThan(0);
    expect(conf).toBeLessThanOrEqual(1);
  });

  test("evidence drawer is collapsed by default", async ({ page }) => {
    await load(page);

    const row = decideRow(page);
    await expect(row).toBeVisible({ timeout: 8000 });
    await row.click();

    const body = row.locator(".trace-timeline__row-body");
    await expect(body).toBeVisible({ timeout: 5000 });

    // The toggle button should be visible.
    const toggle = body.locator("[data-testid='decide-evidence-toggle']");
    await expect(toggle).toBeVisible({ timeout: 3000 });

    // The evidence body (containing prompt/response) must NOT be visible initially.
    const evidenceBody = body.locator(".decide-detail__evidence-body");
    await expect(evidenceBody).toHaveCount(0);
  });

  test("clicking show evidence expands the prompt/response section", async ({ page }) => {
    await load(page);

    const row = decideRow(page);
    await expect(row).toBeVisible({ timeout: 8000 });
    await row.click();

    const body = row.locator(".trace-timeline__row-body");
    await expect(body).toBeVisible({ timeout: 5000 });

    const toggle = body.locator("[data-testid='decide-evidence-toggle']");
    await expect(toggle).toBeVisible({ timeout: 3000 });

    // Click to expand.
    await toggle.click();

    // Evidence body should now be visible.
    const evidenceBody = body.locator(".decide-detail__evidence-body");
    await expect(evidenceBody).toBeVisible({ timeout: 3000 });

    // Should contain at least one CollapsibleText for prompts or the response panel.
    const hasContent = await Promise.race([
      body.locator(".collapsible-text").first().isVisible().then((v) => v),
      body.locator(".decide-detail__response").first().isVisible().then((v) => v),
    ]);
    expect(hasContent, "Expected prompt or response content after expanding evidence").toBe(true);
  });
});
