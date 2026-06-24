/**
 * surfaces-empty-state.spec.ts — deterministic, no-LLM regression guard for the
 * "surface stuck on Loading…" bug.
 *
 * Each decomposed surface (chat / trace / graph) boots with `loading = true` and
 * discovers the current session on mount. When there is NO active session,
 * discovery returns null and the surface MUST lower `loading` and render its empty
 * / start state — not sit on the "Loading…" placeholder forever. A regression
 * (the no-session branch forgetting to clear `loading`) left all three sidebar
 * panels showing "Loading…" indefinitely in the VS Code embed.
 *
 * This drives NO session (a fresh `kitsoki web` over the cassette flow), opens each
 * `?surface=`, and asserts the empty state appears and the loading placeholder is
 * gone within a tight budget — so the bug can never silently come back.
 *
 * Run:  pnpm -C tools/runstatus exec playwright test surfaces-empty-state --project=chromium
 */
import { test, expect, chromium, type Browser } from "@playwright/test";
import path from "path";
import { startWebServer, repoRoot, type WebServer } from "./_helpers/server.js";

const ADDR = "127.0.0.1:7764";
const STORY_DIR = path.join(repoRoot, "stories", "weather-report");
const FLOW = path.join(STORY_DIR, "flows", "tour.yaml");

const SURFACES = ["chat", "trace", "graph"] as const;

let server: WebServer;

test.beforeAll(async () => {
  server = await startWebServer({ addr: ADDR, flow: FLOW, storiesDir: STORY_DIR });
});
test.afterAll(() => server?.stop());

test("each surface clears Loading… and shows the empty state when no session is active", async () => {
  test.setTimeout(120000);

  const browser: Browser = await chromium.launch({ headless: true });
  try {
    for (const surface of SURFACES) {
      const context = await browser.newContext({ viewport: { width: 360, height: 850 } });
      const page = await context.newPage();
      try {
        await page.goto(`${server.base}/?surface=${surface}`);
        // The surface root mounts immediately.
        await expect(
          page.locator(`[data-testid="surface-${surface}"]`).first(),
          `${surface} surface mounts`,
        ).toBeVisible({ timeout: 20000 });
        // With no active session, discovery resolves null fast → the empty/start
        // state must render and the loading placeholder must be gone. A short
        // budget is the whole point: a stuck-loading regression blows past it.
        await expect(
          page.locator(`[data-testid="surface-empty"]`).first(),
          `${surface} surface shows the empty/start state (not stuck on Loading…)`,
        ).toBeVisible({ timeout: 8000 });
        await expect(
          page.locator(`[data-testid="surface-loading"]`),
          `${surface} surface is NOT stuck on the Loading… placeholder`,
        ).toHaveCount(0, { timeout: 8000 });
      } finally {
        await context.close();
      }
    }
  } finally {
    await browser.close();
  }
});
