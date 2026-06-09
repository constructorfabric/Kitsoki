/**
 * Guided-tour video demo + walkthrough QA.
 *
 * Drives the live onboarding tour against a real `kitsoki web` server in the
 * deterministic no-LLM posture (--flow winning_deterministic.yaml) and records
 * a video + per-scene screenshots to .artifacts/tour-video/.
 *
 * The spec imports the SAME step manifest the live overlay renders
 * (src/tour/manifest.ts) and walks it: for 'explain' steps it dwells and clicks
 * the tour's own Next; for 'action' steps it clicks the REAL highlighted control
 * (which drives both the Oregon Trail story and the tour). It asserts the live
 * popover's title against each step, so the recording can never drift from the
 * shipped tour.
 *
 * Validate fast (no dwells):
 *   WEB_CHAT_PACE=0 pnpm exec playwright test tour-video --project=chromium
 * Record at watch-speed:
 *   pnpm exec playwright test tour-video --project=chromium
 */
import { test, expect, chromium, type Browser, type BrowserContext, type Page, type Locator } from "@playwright/test";
import path from "path";
import fs from "fs";
import {
  startWebServer,
  repoRoot,
  makeShot,
  waitForState,
  PACE,
  type WebServer,
} from "./_helpers/server.js";
import { TOUR_STEPS, type TourStep } from "../../src/tour/manifest.js";

const ADDR = "127.0.0.1:7745";
const STORY_DIR = path.join(repoRoot, "stories", "oregon-trail");
const FLOW = path.join(STORY_DIR, "flows", "winning_deterministic.yaml");
const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "tour-video");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "video");

let server: WebServer;

test.beforeAll(async () => {
  fs.mkdirSync(VIDEO_DIR, { recursive: true });
  // Single-story catalogue: the one New-session button the generic tour
  // highlights IS Oregon Trail's, so the spotlight hole and the spec's click
  // line up (no ambiguity, no story name baked into the manifest).
  server = await startWebServer({ addr: ADDR, flow: FLOW, storiesDir: STORY_DIR });
});

test.afterAll(() => server?.stop());

function dwell(page: Page, ms: number): Promise<void> {
  return page.waitForTimeout(Math.round(ms * PACE));
}

/**
 * Resolve an action step's real target. The single-story catalogue means the
 * generic anchor (first match) is unambiguous, so this is just `.first()` — the
 * same element the overlay spotlights.
 */
async function resolveTarget(page: Page, step: TourStep): Promise<Locator> {
  return page.getByTestId(step.target!).first();
}

test("onboarding tour video (no-LLM)", async () => {
  test.setTimeout(300000);
  const browser: Browser = await chromium.launch({ headless: true });
  // The tour never auto-starts under automation (navigator.webdriver) so it
  // can't sabotage the other live UI specs; here we opt in explicitly below
  // via window.__startTour.
  const context: BrowserContext = await browser.newContext({
    viewport: { width: 1600, height: 900 },
    recordVideo: { dir: VIDEO_DIR, size: { width: 1600, height: 900 } },
  });
  const page: Page = await context.newPage();
  const shot = makeShot(ARTIFACT_DIR);

  try {
    await page.goto(`${server.base}/#/`);
    await expect(page.getByTestId("home-view")).toBeVisible({ timeout: 15000 });

    // Force-start for determinism (idempotent with the first-login auto-start).
    await page.evaluate(() => {
      (window as unknown as { __startTour?: () => void }).__startTour?.();
    });
    await expect(page.getByTestId("tour-overlay")).toBeVisible({ timeout: 8000 });

    for (const step of TOUR_STEPS) {
      // Honor the step's preconditions before expecting it to render. (These are
      // DOM-presence gates only — the generic tour never waits on a story state.)
      if (step.waitForTarget) {
        await expect(page.getByTestId(step.waitForTarget).first()).toBeVisible({ timeout: 15000 });
      }

      // The live popover must be showing THIS step — the anti-drift assertion.
      await expect(page.getByTestId("tour-title")).toHaveText(step.title, { timeout: 12000 });

      await dwell(page, step.dwellMs ?? 3000);
      await shot(page, step.id);

      // The manifest's input step is a generic "try clicking an option" — the
      // VIDEO actually drives one Oregon turn here so the recording shows the
      // trace light up, then advances the (explain) tour with Next.
      if (step.id === "iv-input") {
        const begin = page.getByTestId("intent-btn-begin_setup");
        if ((await begin.count()) > 0) {
          await begin.first().click();
          await waitForState(page, "intro_profession", 15000);
          await dwell(page, 1500);
        }
      }

      if (step.kind === "explain") {
        await page.getByTestId("tour-next").click();
      } else {
        const target = await resolveTarget(page, step);
        await target.click();
        if (step.advance === "route-match" && step.advanceRoute === "interactive") {
          await page.waitForURL(/#\/s\/[0-9a-f-]{36}\/chat$/, { timeout: 15000 });
        }
      }
    }

    // The final 'Done' closes the tour.
    await expect(page.getByTestId("tour-overlay")).toHaveCount(0, { timeout: 5000 });
  } finally {
    await context.close();
    await browser.close();
  }

  // Stabilize the recorded video name for the render scripts.
  const vids = fs.readdirSync(VIDEO_DIR).filter((f) => f.endsWith(".webm"));
  if (vids.length > 0) {
    const stable = path.join(ARTIFACT_DIR, "tour-video-demo.webm");
    fs.copyFileSync(path.join(VIDEO_DIR, vids[0]), stable);
    console.log(`[tour-video] demo: ${stable}`);
  }
  const pngs = fs.readdirSync(ARTIFACT_DIR).filter((f) => f.endsWith(".png"));
  console.log(`[tour-video] screenshots (${pngs.length}) in ${ARTIFACT_DIR}`);
});
