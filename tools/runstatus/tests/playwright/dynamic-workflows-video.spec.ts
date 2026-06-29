/**
 * Dynamic workflows feature-spotlight video demo.
 *
 * Drives the dynamic-workflows tour against a real `kitsoki web` server in the
 * deterministic no-LLM posture and records a video + per-scene screenshots to
 * .artifacts/dynamic-workflows/.
 *
 * The demo uses an existing proposal in docs/proposals as the example payload,
 * then launches the generated workflow, drives the launched session once so the
 * trace is non-empty, reopens the receipt, and exports the starter artifacts.
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
import { captureDiagnostics } from "./_helpers/demo.js";
import { DYNAMIC_WORKFLOWS_TOUR_STEPS, type TourStep } from "../../src/tour/generated/dynamic-workflows.js";

const CHAPTER_SOURCE = "features/dynamic-workflows.yaml";
const ADDR = demoAddr(7768);
const STORY_DIR = path.join(repoRoot, "stories", "punch-list");
const FLOW = path.join(STORY_DIR, "flows", "happy_top10_gpt55.yaml");
const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "dynamic-workflows");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "video");
const GOAL = "Promote docs/proposals/process-design.md into a reusable workflow draft";
const SLUG = "process-design-workflow";
const TARGET = "stories/process-design-workflow";

let server: WebServer;

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

async function waitForState(page: Page, state: string): Promise<void> {
  await expect(page.getByTestId("current-state")).toHaveText(state, { timeout: 15000 });
}

test.describe("dynamic workflows feature-spotlight video", () => {
  test.beforeAll(async () => {
    prepareVideoDir(VIDEO_DIR);
    fs.mkdirSync(ARTIFACT_DIR, { recursive: true });
    server = await startWebServer({ addr: ADDR, flow: FLOW, storiesDir: STORY_DIR });
  });

  test.afterAll(() => server?.stop());

  test("tour-driven dynamic workflows demo", async () => {
    test.setTimeout(300000);

    const browser: Browser = await chromium.launch({ headless: true });
    const context: BrowserContext = await browser.newContext(
      cameraContext({ recordVideoDir: VIDEO_DIR }),
    );
    const page: Page = await context.newPage();
    const video = page.video();
    const shot = makeShot(ARTIFACT_DIR);
    const { mark, onThrow } = captureDiagnostics(page, ARTIFACT_DIR);
    const chapters = new ChapterRecorder();

    try {
      mark("navigating home");
      await cinematicGoto(page, `${server.base}/#/`, { waitForTestId: "home-view" });
      await injectTour(page, DYNAMIC_WORKFLOWS_TOUR_STEPS);

      for (const step of DYNAMIC_WORKFLOWS_TOUR_STEPS) {
        mark(`step ${step.id}`);

        const currentUrl = page.url();
        const routeKind = currentUrl.includes("/chat")
          ? "interactive"
          : currentUrl.match(/#\/s\/[0-9a-f-]{36}$/)
            ? "any"
            : "home";
        if (step.route !== "any" && step.route !== routeKind) continue;

        if (step.id === "dwf-goal") {
          const goal = page.getByTestId("workflow-goal");
          const slug = page.getByTestId("workflow-slug");
          const target = page.getByTestId("workflow-target");
          await goal.pressSequentially(GOAL, { delay: 24 });
          await slug.pressSequentially(SLUG, { delay: 18 });
          await target.pressSequentially(TARGET, { delay: 18 });
          await dwell(page, SETTLE_MS);
        }

        if (step.id === "dwf-launcher" || step.id === "dwf-reopen") {
          await expect(page.getByTestId("meta-button")).toBeVisible({ timeout: 15000 });
          await page.getByTestId("meta-button").click();
          await expect(page.getByTestId("meta-menu")).toBeVisible({ timeout: 15000 });
        }

        if (step.id === "dwf-start") {
          await expect(page.getByTestId("intent-btn-start").first()).toBeVisible({ timeout: 15000 });
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
          await dwell(page, 600);
          continue;
        }

        const target = resolveTarget(page, step);
        await target.evaluate((el) => (el as HTMLElement).click());
        await dwell(page, SETTLE_MS);

        if (step.id === "dwf-launch") {
          await page.waitForURL(/#\/s\/[0-9a-f-]{36}$/, { timeout: 15000 });
          const driveLink = page.getByTestId("drive-link");
          if ((await driveLink.count()) > 0) {
            await driveLink.first().click();
            await page.waitForURL(/#\/s\/[0-9a-f-]{36}\/chat$/, { timeout: 15000 });
            await waitForState(page, "idle");
          }
        }

        if (step.id === "dwf-reopen") {
          await expect(page.getByTestId("workflow-receipt")).toBeVisible({ timeout: 15000 });
        }

        if (step.id === "dwf-export") {
          await expect(page.getByTestId("workflow-export-path")).toContainText(TARGET, { timeout: 15000 });
          await expect(page.getByTestId("workflow-export-report")).toBeVisible({ timeout: 15000 });
        }
      }

      await expect(page.getByTestId("tour-overlay")).toHaveCount(0, { timeout: 5000 });
      await dwell(page, 1500);
      await shot(page, "tour-dismissed");
    } catch (e) {
      onThrow(e);
      throw e;
    } finally {
      await context.close();
      const mp4 = await saveVideoAsMp4(video, ARTIFACT_DIR, "dynamic-workflows-demo");
      writeChapters(mp4, chapters.list());
      await browser.close();
    }
  });
});
