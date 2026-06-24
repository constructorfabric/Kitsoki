/**
 * surfaces-story-picker.spec.ts — deterministic, no-LLM regression guard for the
 * "chat surface silently starts the first story" bug.
 *
 * The chat surface STARTS sessions. With more than one discovered story it must
 * offer a PICKER (the operator chooses which story), defaulted to the kitsoki-dev
 * dogfood story — NOT silently bind the lexicographically-first story (which is
 * 'bugfix'). The VS Code sidebar embed hit exactly this: revealing Kitsoki landed
 * in 'bugfix' with no way to choose.
 *
 * This boots a story-less `kitsoki web` over the FULL stories dir (many stories,
 * no flow — no session is ever started, so no LLM), opens the chat surface, and
 * asserts the story <select> renders and defaults to kitsoki-dev.
 *
 * Run:  pnpm -C tools/runstatus exec playwright test surfaces-story-picker --project=chromium
 */
import { test, expect, chromium, type Browser } from "@playwright/test";
import path from "path";
import { startWebServer, repoRoot, type WebServer } from "./_helpers/server.js";

const ADDR = "127.0.0.1:7765";
const STORIES_DIR = path.join(repoRoot, "stories");

let server: WebServer;

test.beforeAll(async () => {
  server = await startWebServer({ addr: ADDR, storiesDir: STORIES_DIR });
});
test.afterAll(() => server?.stop());

test("the chat surface offers a multi-story picker defaulted to kitsoki-dev", async () => {
  test.setTimeout(120000);

  const browser: Browser = await chromium.launch({ headless: true });
  try {
    const context = await browser.newContext({ viewport: { width: 360, height: 850 } });
    const page = await context.newPage();
    try {
      await page.goto(`${server.base}/?surface=chat`);

      // Empty state renders (no session yet).
      await expect(
        page.locator('[data-testid="surface-empty"]').first(),
        "chat surface shows the empty/start state",
      ).toBeVisible({ timeout: 20000 });

      // With many stories the picker MUST appear (the bug bound the first story
      // with no picker at all).
      const select = page.locator('[data-testid="surface-story-select"]');
      await expect(select, "multi-story chat surface shows a story picker").toBeVisible({
        timeout: 10000,
      });

      // …and it defaults to the kitsoki-dev dogfood story, not lexicographic
      // 'bugfix'. The <option> value is the absolute app.yaml path.
      await expect(select, "picker defaults to kitsoki-dev").toHaveValue(/kitsoki-dev[/\\]app\.yaml$/);
    } finally {
      await context.close();
    }
  } finally {
    await browser.close();
  }
});
