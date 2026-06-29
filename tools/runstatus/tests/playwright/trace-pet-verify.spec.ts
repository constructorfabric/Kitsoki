/**
 * trace-pet-verify.spec.ts — verifies the new decorative trace-column pet
 * (src/components/TracePet.vue) renders, docks at the BOTTOM of the trace
 * column (.iv__trace), and animates (wanders) in the live `kitsoki web` UI.
 *
 * The pet is OPT-IN: off by default, enabled via the 🐾 toggle
 * ([data-testid="trace-pet-toggle"]) in the State Diagram panel header, which
 * persists localStorage["kitsoki:tracePet"]="1". When enabled,
 * [data-testid="trace-pet"] mounts containing an <svg> sprite.
 *
 * Runs a REAL `kitsoki web` server in the deterministic no-LLM posture (the PRD
 * happy-path flow), mints a session out-of-band via RPC, and opens its /chat
 * interactive drive view directly — the only surface that hosts .iv__trace +
 * the pet.
 *
 * Run:
 *   cd tools/runstatus && npx playwright test tests/playwright/trace-pet-verify.spec.ts --reporter=line
 */
import { test, expect, chromium, type Browser, type BrowserContext, type Page } from "@playwright/test";
import path from "path";
import fs from "fs";
import { startWebServer, repoRoot, STORIES_DIR, demoAddr, type WebServer } from "./_helpers/server.js";

const ADDR = demoAddr(7790);
const FLOW = path.join(STORIES_DIR, "prd", "flows", "happy_path.yaml");
const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "trace-pet-verify");

let server: WebServer;

test.beforeAll(async () => {
  fs.mkdirSync(ARTIFACT_DIR, { recursive: true });
  server = await startWebServer({ addr: ADDR, flow: FLOW, storiesDir: STORIES_DIR });
});

test.afterAll(() => server?.stop());

/** Read the .pet__sprite horizontal position (left % style) for the move check. */
async function spriteLeft(page: Page): Promise<string> {
  return page.evaluate(() => {
    const el = document.querySelector('[data-testid="trace-pet"] .pet__sprite') as HTMLElement | null;
    return el ? el.style.left : "";
  });
}

test("trace-column pet renders, docks at the bottom, and wanders", async () => {
  test.setTimeout(120000);

  // Mint a session out-of-band so we land DIRECTLY on the /chat drive view.
  const stories = await server.rpc<Array<{ path: string; app_id: string }>>(
    "runstatus.stories.list",
    {},
  );
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

  const consoleErrors: string[] = [];
  page.on("console", (m) => {
    if (m.type() === "error") consoleErrors.push(m.text());
  });
  page.on("pageerror", (e) => consoleErrors.push(`pageerror: ${e.message}`));

  let movedNote = "";
  try {
    // ── 1+2. Open the drive/chat view; confirm the trace column is present. ───
    await page.goto(`${server.base}/#/s/${sid}/chat`);
    await expect(page.getByTestId("chat-section")).toBeVisible({ timeout: 20000 });
    const traceCol = page.locator(".iv__trace");
    await expect(traceCol, "trace column is visible in the drive view").toBeVisible({ timeout: 15000 });

    // Pet is OFF by default.
    await expect(page.getByTestId("trace-pet")).toHaveCount(0);

    // ── 3. Enable the pet via the 🐾 toggle. ─────────────────────────────────
    const toggle = page.getByTestId("trace-pet-toggle");
    await expect(toggle, "pet toggle is in the State Diagram header").toBeVisible({ timeout: 10000 });
    await toggle.click();

    // Confirm the preference persisted to localStorage.
    const pref = await page.evaluate(() => localStorage.getItem("kitsoki:tracePet"));
    expect(pref, 'localStorage["kitsoki:tracePet"] persisted').toBe("1");

    // ── 4. Pet is visible and contains an <svg>. ─────────────────────────────
    const pet = page.getByTestId("trace-pet");
    await expect(pet, "pet mounts when enabled").toBeVisible({ timeout: 10000 });
    await expect(pet.locator("svg"), "pet contains an inline SVG sprite").toHaveCount(1);
    await expect(pet.locator("svg")).toBeVisible();

    // ── 5. Pet sits at the BOTTOM of the trace column. ───────────────────────
    const petBox = await pet.boundingBox();
    const colBox = await traceCol.boundingBox();
    expect(petBox, "pet has a bounding box").toBeTruthy();
    expect(colBox, "trace column has a bounding box").toBeTruthy();
    const petBottom = petBox!.y + petBox!.height;
    const colBottom = colBox!.y + colBox!.height;
    expect(
      Math.abs(petBottom - colBottom),
      `pet bottom (${petBottom.toFixed(1)}) ≈ trace column bottom (${colBottom.toFixed(1)})`,
    ).toBeLessThanOrEqual(4);

    // ── 6. Evidence + movement check. ────────────────────────────────────────
    await page.screenshot({ path: path.join(ARTIFACT_DIR, "pet.png") });

    const left1 = await spriteLeft(page);
    await page.screenshot({ path: path.join(ARTIFACT_DIR, "frame-1.png") });

    // The pet picks a new wander target ~1.5-3s after mount, then strolls. Poll
    // up to ~10s for the sprite's `left` to change (proof it wanders).
    let left2 = left1;
    const deadline = Date.now() + 10000;
    while (Date.now() < deadline) {
      await page.waitForTimeout(500);
      left2 = await spriteLeft(page);
      if (left2 !== left1) break;
    }
    await page.waitForTimeout(2500); // let it travel a bit further for the 2nd frame
    const left3 = await spriteLeft(page);
    await page.screenshot({ path: path.join(ARTIFACT_DIR, "frame-2.png") });

    const moved = left3 !== left1;
    movedNote = `sprite left: "${left1}" -> "${left3}" (moved=${moved})`;
    console.log(`[trace-pet-verify] ${movedNote}`);
    console.log(`[trace-pet-verify] screenshots in ${ARTIFACT_DIR}`);
    if (consoleErrors.length) console.log(`[trace-pet-verify] console errors:\n${consoleErrors.join("\n")}`);

    expect(moved, `pet should wander; ${movedNote}`).toBe(true);
  } catch (e) {
    console.log(`[trace-pet-verify] FAILED: ${e instanceof Error ? e.message : String(e)}`);
    console.log(`[trace-pet-verify] console errors:\n${consoleErrors.join("\n") || "(none)"}`);
    console.log(`[trace-pet-verify] ${movedNote}`);
    console.log(`--- server log ---\n${server?.log?.() ?? ""}`);
    throw e;
  } finally {
    await context.close();
    await browser.close();
  }
});
