/**
 * Operation-handles feature-spotlight video demo.
 *
 * Drives the dedicated operation-handles tour against a real `kitsoki web`
 * server in the deterministic no-LLM posture
 * (--flow operation-demo/background_operation.yaml) and records a video plus
 * per-scene screenshots to .artifacts/operation-handles/.
 *
 * Validate fast (no dwells):
 *   WEB_CHAT_PACE=0 pnpm exec playwright test operation-handles-video --project=chromium
 * Record at watch-speed:
 *   pnpm exec playwright test operation-handles-video --project=chromium
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
  ChapterRecorder,
  writeChapters,
  dwell,
  cinematicGoto,
  SETTLE_MS,
  demoAddr,
  type WebServer,
} from "./_helpers/server.js";
import { cameraContext } from "./_helpers/camera.js";
import { OPERATION_HANDLES_TOUR_STEPS, type TourStep } from "../../src/tour/generated/operation-handles.js";

const ADDR = demoAddr(7798);
const STORY_DIR = path.join(repoRoot, "stories", "operation-demo");
const FLOW = path.join(STORY_DIR, "flows", "background_operation.yaml");
const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "operation-handles");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "video");
const ERROR_LOG = path.join(ARTIFACT_DIR, "ERROR.txt");
const CHAPTER_SOURCE = "features/operation-handles.yaml";

let server: WebServer;

function mark(msg: string): void {
  const line = `[${new Date().toISOString()}] ${msg}\n`;
  try {
    fs.appendFileSync(ERROR_LOG, line);
  } catch {
    /* best-effort */
  }
}

test.beforeAll(async () => {
  prepareVideoDir(VIDEO_DIR);
  fs.mkdirSync(ARTIFACT_DIR, { recursive: true });
  fs.writeFileSync(ERROR_LOG, "");
  server = await startWebServer({ addr: ADDR, flow: FLOW, storiesDir: STORY_DIR });
});

test.afterAll(() => server?.stop());

async function resolveTarget(page: Page, step: TourStep): Promise<Locator> {
  return page.getByTestId(step.target!).first();
}

test("operation-handles feature-spotlight video", async () => {
  test.setTimeout(300000);
  const browser: Browser = await chromium.launch({ headless: true });
  const context: BrowserContext = await browser.newContext(
    cameraContext({ recordVideoDir: VIDEO_DIR }),
  );
  const page: Page = await context.newPage();
  const video = page.video();
  const chapters = new ChapterRecorder();
  const shot = makeShot(ARTIFACT_DIR);

  try {
    mark("navigating home");
    await cinematicGoto(page, `${server.base}/#/`, { waitForTestId: "home-view" });

    await page.evaluate((stepsJson: string) => {
      (window as unknown as { __startTourWithSteps?: (s: string) => void })
        .__startTourWithSteps?.(stepsJson);
    }, JSON.stringify(OPERATION_HANDLES_TOUR_STEPS));
    await expect(page.getByTestId("tour-overlay")).toBeVisible({ timeout: 8000 });

    for (const step of OPERATION_HANDLES_TOUR_STEPS) {
      mark(`step ${step.id}`);
      const currentUrl = page.url();
      const currentRouteKind = currentUrl.includes("/chat")
        ? "interactive"
        : currentUrl.match(/#\/s\/[0-9a-f-]{36}$/)
          ? "any"
          : "home";
      if (step.route !== "any" && step.route !== currentRouteKind) {
        mark(`  route-skip (${currentRouteKind})`);
        continue;
      }

      if (step.id === "oh-completed") {
        await expect(page.getByTestId("operation-run-artifact-open")).toBeVisible({ timeout: 20000 });
      }

      if (step.waitForTarget) {
        await expect(page.getByTestId(step.waitForTarget).first()).toBeVisible({ timeout: 20000 });
      }

      const titleEl = page.getByTestId("tour-title");
      const actualTitle = await titleEl.textContent({ timeout: 8000 }).catch(() => "");
      if (actualTitle !== step.title) {
        const remaining = OPERATION_HANDLES_TOUR_STEPS.slice(OPERATION_HANDLES_TOUR_STEPS.indexOf(step) + 1);
        const isOnNext = remaining.some((s) => s.title === actualTitle);
        if (isOnNext) {
          mark(`  drift-skip: overlay on "${actualTitle}"`);
          continue;
        }
      }
      await expect(titleEl).toHaveText(step.title, { timeout: 12000 });

      chapters.open(step.id, step.title, CHAPTER_SOURCE);
      await dwell(page, step.dwellMs ?? 3000);
      await shot(page, step.id);

      if (step.kind === "explain") {
        await page.getByTestId("tour-next").click();
        await dwell(page, 700);
      } else {
        const target = await resolveTarget(page, step);
        await target.scrollIntoViewIfNeeded().catch(() => undefined);
        if (step.advance === "route-match") {
          await target.click();
          if (step.advanceRoute === "interactive") {
            await page.waitForURL(/#\/s\/[0-9a-f-]{36}\/chat$/, { timeout: 15000 });
          }
          await dwell(page, SETTLE_MS);
        } else {
          await target.evaluate((el) => (el as HTMLElement).click());
          if (step.id === "oh-start") {
            await expect(page.getByTestId("operation-run-drive")).toBeVisible({ timeout: 8000 });
            mark("operation drive action visible");
          }
          await dwell(page, 1000);
        }
      }
    }

    await expect(page.getByTestId("tour-overlay")).toHaveCount(0, { timeout: 5000 });
  } catch (e) {
    mark(`FAILED: ${e instanceof Error ? e.stack ?? e.message : String(e)}`);
    mark(`--- server log ---\n${server?.log?.() ?? ""}`);
    await page.screenshot({ path: path.join(ARTIFACT_DIR, "99-failure.png") }).catch(() => undefined);
    throw e;
  } finally {
    await context.close();
    const mp4 = await saveVideoAsMp4(video, ARTIFACT_DIR, "operation-handles-demo");
    writeChapters(mp4, chapters.list());
    await browser.close();
  }

  const pngs = fs.readdirSync(ARTIFACT_DIR).filter((f) => f.endsWith(".png"));
  console.log(`[operation-handles-video] screenshots (${pngs.length}) in ${ARTIFACT_DIR}`);
});
