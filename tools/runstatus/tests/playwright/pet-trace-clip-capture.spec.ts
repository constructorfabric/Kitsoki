/**
 * pet-trace-clip-capture.spec.ts — captures a short rrweb clip of the live
 * trace-column pet ("Kit") wandering in .iv__trace, for the phase-6 video scene
 * of docs/decks/pet-dev-story-hybrid.slidey.json.
 *
 * Models the drive on trace-pet-verify.spec.ts (mint a PRD session out-of-band,
 * open /chat, enable the pet via the 🐾 toggle), and the rrweb capture on the
 * slidey-*-rrweb-capture specs (installCapture/dumpCapture/writeEvents).
 *
 * Output:
 *   docs/decks/clips/pet-trace.rrweb.json          ← the rrweb event stream
 *   docs/decks/clips/pet-trace.rrweb.capture.json  ← viewport sidecar
 *
 * Run:
 *   cd tools/runstatus && npx playwright test tests/playwright/pet-trace-clip-capture.spec.ts --reporter=line
 */
import { test, expect, chromium, type Browser, type BrowserContext, type Page } from "@playwright/test";
import path from "path";
import fs from "fs";
import { startWebServer, repoRoot, STORIES_DIR, demoAddr, type WebServer } from "./_helpers/server.js";
import { installCapture, dumpCapture, writeEvents } from "./_helpers/rrweb-replay.js";

const ADDR = demoAddr(7791);
const FLOW = path.join(STORIES_DIR, "prd", "flows", "happy_path.yaml");
const OUT_DIR = path.join(repoRoot, "docs", "decks", "clips");
const EVENTS_JSON = path.join(OUT_DIR, "pet-trace.rrweb.json");

let server: WebServer;

test.beforeAll(async () => {
  fs.mkdirSync(OUT_DIR, { recursive: true });
  server = await startWebServer({ addr: ADDR, flow: FLOW, storiesDir: STORIES_DIR });
});

test.afterAll(() => server?.stop());

async function spriteLeft(page: Page): Promise<string> {
  return page.evaluate(() => {
    const el = document.querySelector('[data-testid="trace-pet"] .pet__sprite') as HTMLElement | null;
    return el ? el.style.left : "";
  });
}

test("capture rrweb clip of the trace-column pet wandering", async () => {
  test.setTimeout(180000);

  const stories = await server.rpc<Array<{ path: string; app_id: string }>>("runstatus.stories.list", {});
  const prd = stories.find((s) => s.app_id === "prd");
  expect(prd, "PRD story is in the catalogue").toBeTruthy();
  const { session_id: sid } = await server.rpc<{ session_id: string }>(
    "runstatus.session.new",
    { story_path: prd!.path },
  );
  expect(sid).toBeTruthy();

  const browser: Browser = await chromium.launch({ headless: true });
  const context: BrowserContext = await browser.newContext({ viewport: { width: 1600, height: 900 } });
  const page: Page = await context.newPage();

  try {
    await page.goto(`${server.base}/#/s/${sid}/chat`);
    await expect(page.getByTestId("chat-section")).toBeVisible({ timeout: 20000 });
    const traceCol = page.locator(".iv__trace");
    await expect(traceCol, "trace column visible").toBeVisible({ timeout: 15000 });

    // Install rrweb capture BEFORE enabling the pet so the mount + wander are recorded.
    await installCapture(page);

    const toggle = page.getByTestId("trace-pet-toggle");
    await expect(toggle, "pet toggle present").toBeVisible({ timeout: 10000 });
    await toggle.click();

    const pet = page.getByTestId("trace-pet");
    await expect(pet, "pet mounts").toBeVisible({ timeout: 10000 });
    await expect(pet.locator("svg")).toHaveCount(1);

    // Let the pet animate for ~12s, sampling its position so we can report motion.
    const left1 = await spriteLeft(page);
    const positions = new Set<string>([left1]);
    const deadline = Date.now() + 12000;
    while (Date.now() < deadline) {
      await page.waitForTimeout(500);
      positions.add(await spriteLeft(page));
    }
    const left2 = await spriteLeft(page);
    const moved = positions.size > 1;
    console.log(`[pet-trace-clip] sprite left "${left1}" -> "${left2}" distinct=${positions.size} moved=${moved}`);

    const { events, viewport } = await dumpCapture(page);
    writeEvents(events, EVENTS_JSON, viewport);
    console.log(`[pet-trace-clip] rrweb ${events.length} events @ ${viewport.width}x${viewport.height} -> ${EVENTS_JSON}`);

    expect(moved, "pet should wander during capture").toBe(true);
    expect(events.length, "healthy rrweb stream").toBeGreaterThanOrEqual(50);
  } catch (e) {
    console.log(`[pet-trace-clip] FAILED: ${e instanceof Error ? e.message : String(e)}`);
    console.log(`--- server log ---\n${server?.log?.() ?? ""}`);
    throw e;
  } finally {
    await context.close();
    await browser.close();
  }
});
