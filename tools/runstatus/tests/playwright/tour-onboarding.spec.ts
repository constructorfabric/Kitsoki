/**
 * Onboarding-tour robustness guard (the inverse of tour-video.spec.ts).
 *
 * tour-video forces the tour and walks every step. This spec instead simulates a
 * REAL first-time user — auto-start is normally suppressed under automation
 * (navigator.webdriver), so we spoof webdriver=false via an init script — and
 * asserts the tour:
 *   - auto-starts on the home screen and shows the welcome popover (not a stuck
 *     "Setting up…" holding state),
 *   - is always dismissible and never blocks the UI afterward,
 *   - does NOT auto-start when the user deep-links straight into a session view.
 *
 * Regression cover for: a tour that froze the whole UI with a non-escapable
 * "Setting up…" overlay.
 */
import { test, expect, chromium } from "@playwright/test";
import path from "path";
import { startWebServer, repoRoot, type WebServer } from "./_helpers/server.js";

const ADDR = "127.0.0.1:7747";
const FLOW = path.join(repoRoot, "stories", "oregon-trail", "flows", "winning_deterministic.yaml");

// Make the headless browser look like a real user so first-login auto-start runs.
const SPOOF_REAL_USER =
  "Object.defineProperty(navigator, 'webdriver', { get: () => false, configurable: true });";

let server: WebServer;

test.beforeAll(async () => {
  server = await startWebServer({ addr: ADDR, flow: FLOW });
});
test.afterAll(() => server?.stop());

test("first login: tour auto-starts on home and is dismissible", async () => {
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  await context.addInitScript(SPOOF_REAL_USER);
  const page = await context.newPage();
  try {
    await page.goto(`${server.base}/#/`);
    await expect(page.getByTestId("home-view")).toBeVisible({ timeout: 15000 });

    // The welcome popover — NOT the holding pill — proves the first step anchored.
    await expect(page.getByTestId("tour-title")).toHaveText("Welcome to kitsoki", { timeout: 8000 });
    await expect(page.getByTestId("tour-loading")).toHaveCount(0);
    await expect(page.locator(".tour__backdrop")).toHaveCount(0);

    // The tour should guide without dimming the page: anchorless intro steps
    // have no backdrop, and targeted steps keep only the highlight ring.
    await page.getByTestId("tour-next").click();
    await expect(page.getByTestId("tour-title")).toHaveText("Your stories", { timeout: 8000 });
    await expect(page.locator(".tour__backdrop")).toHaveCount(0);
    await expect(page.locator(".tour__ring")).toBeVisible();

    // Always escapable, and the UI is usable again immediately after.
    await page.getByTestId("tour-skip").click();
    await expect(page.getByTestId("tour-overlay")).toHaveCount(0, { timeout: 5000 });
    await expect(page.getByTestId("rescan-btn")).toBeEnabled();
  } finally {
    await context.close();
    await browser.close();
  }
});

test("deep-linked session view does NOT auto-start the tour", async () => {
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  await context.addInitScript(SPOOF_REAL_USER);
  const page = await context.newPage();
  try {
    // A non-home route at mount: auto-start is gated on route.path === "/".
    await page.goto(`${server.base}/#/s/00000000-0000-0000-0000-000000000000/chat`);
    await page.waitForTimeout(1500);
    await expect(page.getByTestId("tour-overlay")).toHaveCount(0);
  } finally {
    await context.close();
    await browser.close();
  }
});
