/**
 * pet-deck-artifacts-capture.spec.ts — rrweb capture of a PRODUCED slidey deck
 * for the trace-column pet scenario, played as a clean standalone deck.
 *
 * The overall video embeds ONE deck artifact: the PRD's slidey deck, shown right
 * after the PRD markdown (the PM phase produces both a markdown PRD and an HTML
 * mockup deck). The other phases' decks are shown elsewhere, so only "prd" is
 * captured here. (Add a slug to PHASES to capture more.)
 *
 * The deck-review clips (pet-<phase>-review) capture a deck through kitsoki's
 * review surface — the deck sits in a sandboxed iframe inside the chat/trace
 * chrome, so the clip is really a screenshot of kitsoki, not the deck. This spec
 * instead opens the RENDERED deck HTML at top level (no iframe, no sandbox) and
 * steps through its slides, so rrweb records the deck's own slide/reveal DOM
 * transitions — a clean "here is the produced deck" artifact video.
 *
 * Decks render as pure DOM (no <canvas>), so rrweb captures them faithfully; the
 * capture viewport is the deck's native 1920x1080 (capture == embed resolution).
 *
 * Output (one per deck):
 *   docs/decks/clips/pet-<phase>-deck.rrweb.json          ← rrweb event stream
 *   docs/decks/clips/pet-<phase>-deck.rrweb.capture.json  ← viewport sidecar
 *
 * Run:
 *   cd tools/runstatus && npx playwright test tests/playwright/pet-deck-artifacts-capture.spec.ts --reporter=line
 */
import { test, expect, chromium, type Browser, type BrowserContext, type Page } from "@playwright/test";
import path from "path";
import fs from "fs";
import { repoRoot, dwell } from "./_helpers/server.js";
import { installCapture, dumpCapture, writeEvents } from "./_helpers/rrweb-replay.js";

const DECKS_DIR = path.join(repoRoot, "docs", "decks");
const OUT_DIR = path.join(DECKS_DIR, "clips");
const VIEWPORT = { width: 1920, height: 1080 } as const;

// phase slug → rendered deck html (relative to docs/decks). Clip = pet-<phase>-deck.
// Only the PRD deck is embedded in the overall video (see header); the rest are
// shown elsewhere. Add slugs here to capture additional decks.
const PHASES = ["prd"] as const;

test.beforeAll(() => {
  fs.mkdirSync(OUT_DIR, { recursive: true });
});

// Step through the deck with ArrowRight (reveal-by-reveal, scene-by-scene),
// dwelling so the camera reads each step, until the visible content stabilises
// (deck end) or a generous cap is hit. Returns the number of advances taken.
async function playDeck(page: Page): Promise<number> {
  const sig = () =>
    page.evaluate(() => (document.body.innerText || "").replace(/\s+/g, " ").trim().slice(0, 600));
  let last = await sig();
  let stable = 0;
  let advances = 0;
  for (let i = 0; i < 28 && stable < 2; i++) {
    await page.keyboard.press("ArrowRight");
    await dwell(page, 1100);
    advances++;
    const cur = await sig();
    if (cur === last) stable++;
    else {
      stable = 0;
      last = cur;
    }
  }
  return advances;
}

for (const phase of PHASES) {
  test(`capture pet deck artifact · ${phase}`, async () => {
    test.setTimeout(180000);
    const htmlPath = path.join(DECKS_DIR, `pet-${phase}.slidey.html`);
    expect(fs.existsSync(htmlPath), `rendered deck exists: ${htmlPath}`).toBe(true);

    const browser: Browser = await chromium.launch({ headless: true });
    const context: BrowserContext = await browser.newContext({
      viewport: { ...VIEWPORT },
      deviceScaleFactor: 1,
    });
    const page: Page = await context.newPage();

    try {
      await page.goto(`file://${htmlPath}`, { waitUntil: "load", timeout: 30000 });
      // Let the deck mount + paint its first slide.
      await page.waitForTimeout(2500);
      // Sanity: the deck rendered real text (not a blank/error page).
      const firstText = (await page.evaluate(() => (document.body.innerText || "").trim())) ?? "";
      expect(firstText.length, `deck ${phase} rendered first slide`).toBeGreaterThan(20);

      await installCapture(page);
      await page.waitForTimeout(1200); // hold on the opening slide
      const advances = await playDeck(page);
      await page.waitForTimeout(1200); // rest on the final slide

      const { events, viewport } = await dumpCapture(page);
      const outPath = path.join(OUT_DIR, `pet-${phase}-deck.rrweb.json`);
      writeEvents(events, outPath, viewport);

      const clipBytes = JSON.stringify(events).length;
      console.log(
        `[pet-deck-artifact] ${phase}: advances=${advances} events=${events.length} ` +
          `clipBytes=${clipBytes} @ ${viewport.width}x${viewport.height} -> ${outPath}`,
      );
      // slidey swaps each slide/reveal as a single efficient DOM mutation, so the
      // count tracks the step count (~1 per advance) plus meta + opening snapshot.
      // A floor of 8 still rejects a blank/snapshot-only capture (~3 events).
      expect(events.length, "recorded slide transitions, not just the opening snapshot").toBeGreaterThanOrEqual(8);
    } catch (e) {
      console.log(`[pet-deck-artifact] ${phase} FAILED: ${e instanceof Error ? e.message : String(e)}`);
      throw e;
    } finally {
      await context.close();
      await browser.close();
    }
  });
}
