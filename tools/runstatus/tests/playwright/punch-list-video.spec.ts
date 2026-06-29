/**
 * Punch-list story tour video.
 *
 * Records the catalog-backed punch-list tour against the story's deterministic
 * top-10 GPT-5.5 flow. The flow stubs live driver calls, so this recording
 * never calls a real LLM.
 */
import { test, expect, chromium, type Browser, type BrowserContext, type Page, type Locator } from "@playwright/test";
import path from "path";
import fs from "fs";
import {
  startWebServer,
  repoRoot,
  makeShot,
  prepareVideoDir,
  saveVideoAsMp4,
  dwell,
  cinematicGoto,
  ChapterRecorder,
  writeChapters,
  demoAddr,
  SETTLE_MS,
  type WebServer,
} from "./_helpers/server.js";
import { cameraContext } from "./_helpers/camera.js";
import { PUNCH_LIST_TOUR_STEPS, type TourStep } from "../../src/tour/generated/punch-list.js";

const CHAPTER_SOURCE = "features/punch-list.yaml";
const ADDR = demoAddr(7762);
const STORY_DIR = path.join(repoRoot, "stories", "punch-list");
const FLOW = path.join(STORY_DIR, "flows", "happy_top10_gpt55.yaml");
const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "punch-list");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "video");

let server: WebServer;

test.beforeAll(async () => {
  prepareVideoDir(VIDEO_DIR);
  fs.mkdirSync(ARTIFACT_DIR, { recursive: true });
  server = await startWebServer({ addr: ADDR, flow: FLOW, storiesDir: STORY_DIR });
});

test.afterAll(() => server?.stop());

function resolveTarget(page: Page, step: TourStep): Locator {
  return page.getByTestId(step.target!).first();
}

async function injectTour(page: Page, steps: readonly TourStep[]): Promise<void> {
  await page.evaluate((stepsJson: string) => {
    (window as unknown as { __startTourWithSteps?: (s: string) => void })
      .__startTourWithSteps?.(stepsJson);
  }, JSON.stringify(steps));
  await expect(page.getByTestId("tour-overlay")).toBeVisible({ timeout: 8000 });
}

async function clickIntent(page: Page, intent: string): Promise<void> {
  const btn = page.getByTestId(`intent-btn-${intent}`).first();
  await expect(btn).toBeEnabled({ timeout: 15000 });
  await btn.evaluate((el) => (el as HTMLElement).click());
  await dwell(page, SETTLE_MS);
}

async function waitForState(page: Page, state: string): Promise<void> {
  await expect(page.getByTestId("current-state")).toContainText(state, { timeout: 15000 });
}

test.describe("punch-list tour video", () => {
  test("tour-driven punch-list happy path", async () => {
    test.setTimeout(240000);

    const browser: Browser = await chromium.launch({ headless: true });
    const context: BrowserContext = await browser.newContext(
      cameraContext({ recordVideoDir: VIDEO_DIR }),
    );
    const page = await context.newPage();
    const video = page.video();
    const shot = makeShot(ARTIFACT_DIR);
    const chapters = new ChapterRecorder();

    try {
      await cinematicGoto(page, `${server.base}/#/`, { waitForTestId: "home-view" });
      await expect(page.getByTestId("story-card").first()).toBeVisible({ timeout: 15000 });
      await injectTour(page, PUNCH_LIST_TOUR_STEPS);

      for (const step of PUNCH_LIST_TOUR_STEPS) {
        const currentUrl = page.url();
        const routeKind = currentUrl.includes("/chat")
          ? "interactive"
          : currentUrl.match(/#\/s\/[0-9a-f-]{36}$/)
            ? "any"
            : "home";
        if (step.route !== "any" && step.route !== routeKind) continue;

        if (step.id === "pl-load") {
          await clickIntent(page, "start");
          await waitForState(page, "load");
        }
        if (step.id === "pl-board") {
          await clickIntent(page, "next_item");
          await waitForState(page, "board");
        }
        if (step.id === "pl-first-item") {
          await clickIntent(page, "next_item");
          await waitForState(page, "board");
          await expect(page.getByText(/Processed 1/)).toBeVisible({ timeout: 15000 });
        }
        if (step.id === "pl-midpoint") {
          for (let i = 0; i < 4; i++) {
            await clickIntent(page, "next_item");
            await waitForState(page, "board");
          }
          await expect(page.getByText(/Processed 5/)).toBeVisible({ timeout: 15000 });
        }
        if (step.id === "pl-final-pending") {
          for (let i = 0; i < 4; i++) {
            await clickIntent(page, "next_item");
            await waitForState(page, "board");
          }
          await waitForState(page, "board");
          await expect(page.getByText(/Processed 9/)).toBeVisible({ timeout: 15000 });
          await expect(page.getByText(/story-qa-workflow/)).toBeVisible({ timeout: 15000 });
        }
        if (step.id === "pl-report") {
          await clickIntent(page, "next_item");
          await waitForState(page, "board");
          await expect(page.getByText(/Processed 10/)).toBeVisible({ timeout: 15000 });
          await expect(page.getByText(/Pending 0/)).toBeVisible({ timeout: 15000 });
          await clickIntent(page, "next_item");
          await waitForState(page, "report");
          await expect(page.getByText(/10 passed/)).toBeVisible({ timeout: 15000 });
        }

        if (step.waitForTarget) {
          await expect(page.getByTestId(step.waitForTarget).first()).toBeVisible({ timeout: 15000 });
        }

        await expect(page.getByTestId("tour-title")).toHaveText(step.title, { timeout: 12000 });
        chapters.open(step.id, step.title, CHAPTER_SOURCE);
        await dwell(page, step.dwellMs ?? 3000);
        await shot(page, step.id);

        if (step.kind === "explain") {
          await page.getByTestId("tour-next").click();
          await dwell(page, 700);
        } else {
          const target = resolveTarget(page, step);
          await target.scrollIntoViewIfNeeded().catch(() => undefined);
          await target.evaluate((el) => (el as HTMLElement).click());
          if (step.advance === "route-match" && step.advanceRoute === "interactive") {
            await page.waitForURL(/#\/s\/[0-9a-f-]{36}\/chat$/, { timeout: 15000 });
            await waitForState(page, "idle");
          }
          await dwell(page, 1000);
        }
      }

      await expect(page.getByTestId("tour-overlay")).toHaveCount(0, { timeout: 5000 });
    } finally {
      await page.close();
      await context.close();
      const mp4 = await saveVideoAsMp4(video, ARTIFACT_DIR, "punch-list-demo");
      writeChapters(mp4, chapters.list());
      await browser.close();
    }
  });
});
