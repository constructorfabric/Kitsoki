/**
 * pet-prd-mockup-capture.spec.ts — rrweb capture of the PRD-phase MOCKUP.
 *
 * The PM phase produces TWO artifacts: the markdown PRD (shown by pet-prd-doc) and
 * a self-contained HTML mockup of the proposed pet in the real drive layout
 * (stories/pets-dev/assets/pm_idea-mockup.html — embedded inline in prd_published
 * as the PRD's mockup). The overall video showed the PRD as markdown + a summary
 * deck but never the actual mockup, so "PRD + mockup" read as an empty claim. This
 * captures the real mockup so it can be embedded as the PRD's mockup artifact.
 *
 * The mockup is a single static page (pure inline SVG + CSS — the pet bobs/blinks
 * via CSS keyframes, which replay live from the snapshot). It has no slides to step
 * through, so we give the clip a short timeline by nudging a 1px marker each tick
 * (real DOM mutations → a real duration); the CSS pet animation plays on replay.
 *
 * Output:
 *   docs/decks/clips/pet-prd-mockup.rrweb.json          ← rrweb event stream
 *   docs/decks/clips/pet-prd-mockup.rrweb.capture.json  ← viewport sidecar
 *
 * Run:
 *   cd tools/runstatus && npx playwright test tests/playwright/pet-prd-mockup-capture.spec.ts --reporter=line
 */
import { test, expect, chromium, type Browser, type BrowserContext, type Page } from "@playwright/test";
import path from "path";
import fs from "fs";
import { repoRoot, dwell } from "./_helpers/server.js";
import { installCapture, dumpCapture, writeEvents } from "./_helpers/rrweb-replay.js";

const OUT_DIR = path.join(repoRoot, "docs", "decks", "clips");
const MOCKUP = path.join(repoRoot, "stories", "pets-dev", "assets", "pm_idea-mockup.html");
// Match the other kitsoki-web clips (pet-prd-doc / pet-prd-dev): 1600x900 DSF1.
const VIEWPORT = { width: 1600, height: 900 } as const;

test.beforeAll(() => {
  fs.mkdirSync(OUT_DIR, { recursive: true });
});

test("capture pet PRD mockup", async () => {
  test.setTimeout(120000);
  expect(fs.existsSync(MOCKUP), `mockup exists: ${MOCKUP}`).toBe(true);

  const browser: Browser = await chromium.launch({ headless: true });
  const context: BrowserContext = await browser.newContext({
    viewport: { ...VIEWPORT },
    deviceScaleFactor: 1,
    colorScheme: "dark",
  });
  const page: Page = await context.newPage();

  try {
    await page.goto(`file://${MOCKUP}`, { waitUntil: "load", timeout: 30000 });
    await page.waitForTimeout(2000); // mount + first paint of the SVG pet

    const firstText = (await page.evaluate(() => (document.body.innerText || "").trim())) ?? "";
    expect(firstText.length, "mockup rendered").toBeGreaterThan(40);

    await installCapture(page);

    // Give the static mockup a timeline so the embedded video has a real duration.
    // The pet's CSS-keyframe bob/blink animates live on replay regardless; this just
    // emits a steady trickle of real mutations so the clip isn't a 0-length snapshot.
    await page.evaluate(() => {
      const m = document.createElement("div");
      m.id = "__cap_marker";
      m.style.cssText = "position:fixed;left:0;top:0;width:1px;height:1px;opacity:0;pointer-events:none";
      document.body.appendChild(m);
    });
    const TICKS = 14;
    for (let i = 0; i < TICKS; i++) {
      await page.evaluate((n) => {
        const m = document.getElementById("__cap_marker");
        if (m) m.style.transform = `translateX(${n % 2}px)`;
      }, i);
      await dwell(page, 500);
    }

    const { events, viewport } = await dumpCapture(page);
    const outPath = path.join(OUT_DIR, "pet-prd-mockup.rrweb.json");
    writeEvents(events, outPath, viewport);

    console.log(
      `[pet-prd-mockup] events=${events.length} @ ${viewport.width}x${viewport.height} -> ${outPath}`,
    );
    expect(events.length, "recorded a real timeline, not just the opening snapshot").toBeGreaterThanOrEqual(10);
  } finally {
    await context.close();
    await browser.close();
  }
});
