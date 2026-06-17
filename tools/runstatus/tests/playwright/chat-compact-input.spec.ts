/**
 * chat-compact-input.spec.ts — deterministic, no-LLM guard for the height-responsive
 * InputBar collapse.
 *
 * In a SHORT space (the narrow sidebar Chat surface, where Chat shares height with
 * Trace + Graph) the structured action widgets won't fit, so the InputBar collapses
 * to a SINGLE-LINE text input plus a disclosure icon that reveals the hidden actions;
 * making the panel TALLER un-collapses it. This drives a real session over the
 * cassette flow and asserts both states so the behavior can't silently regress.
 *
 * Run:  pnpm -C tools/runstatus exec playwright test chat-compact-input --project=chromium
 */
import { test, expect, chromium, type Browser } from "@playwright/test";
import path from "path";
import { startWebServer, repoRoot, type WebServer } from "./_helpers/server.js";

const ADDR = "127.0.0.1:7765";
const STORY_DIR = path.join(repoRoot, "stories", "weather-report");
const FLOW = path.join(STORY_DIR, "flows", "tour.yaml");

let server: WebServer;

test.beforeAll(async () => {
  server = await startWebServer({ addr: ADDR, flow: FLOW, storiesDir: STORY_DIR });
});
test.afterAll(() => server?.stop());

test("the chat InputBar collapses to a single line when short and reveals actions when tall", async () => {
  test.setTimeout(120000);

  // Start a session; the lobby room exposes structured actions (forecast/climate
  // forms + a quit button), so there's something for the collapse to hide.
  const stories = await server.rpc<Array<{ path: string }>>("runstatus.stories.list", {});
  const storyPath = stories[0]?.path;
  if (!storyPath) throw new Error("no story to drive");
  await server.rpc("runstatus.session.new", { story_path: storyPath });

  const browser: Browser = await chromium.launch({ headless: true });
  try {
    // ── Short pane → collapsed: single-line input, NO action buttons, a disclosure
    //    icon hinting they exist. ───────────────────────────────────────────────
    {
      const context = await browser.newContext({ viewport: { width: 360, height: 360 } });
      const page = await context.newPage();
      try {
        await page.goto(`${server.base}/?surface=chat`);
        await expect(page.locator('[data-testid="chat-section"]')).toBeVisible({ timeout: 20000 });
        // The single-line composer is present; the structured forecast form is NOT.
        await expect(
          page.locator('[data-testid="composer-input"]'),
          "single-line composer present in the short pane",
        ).toBeVisible({ timeout: 10000 });
        await expect(
          page.locator('form[data-intent="forecast"]'),
          "structured action form is hidden while collapsed",
        ).toHaveCount(0);
        const disclose = page.locator('[data-testid="input-disclose"]');
        await expect(disclose, "a disclosure icon hints the hidden actions").toBeVisible({
          timeout: 10000,
        });

        // Clicking the disclosure reveals the actions in place.
        await disclose.click();
        await expect(
          page.locator('form[data-intent="forecast"]'),
          "disclosure reveals the structured action form",
        ).toBeVisible({ timeout: 10000 });
      } finally {
        await context.close();
      }
    }

    // ── Tall pane → expanded by default: the action form shows, no disclosure. ────
    {
      const context = await browser.newContext({ viewport: { width: 360, height: 850 } });
      const page = await context.newPage();
      try {
        await page.goto(`${server.base}/?surface=chat`);
        await expect(page.locator('[data-testid="chat-section"]')).toBeVisible({ timeout: 20000 });
        await expect(
          page.locator('form[data-intent="forecast"]'),
          "structured action form shows without disclosure when there's room",
        ).toBeVisible({ timeout: 10000 });
        await expect(
          page.locator('[data-testid="input-disclose"]'),
          "no disclosure icon when the actions already fit",
        ).toHaveCount(0);
      } finally {
        await context.close();
      }
    }
  } finally {
    await browser.close();
  }
});
