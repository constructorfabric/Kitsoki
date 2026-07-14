/**
 * Session Media Workbench feature tour.
 *
 * Drives the mockup-video story to a produced media artifact behind a curtain,
 * then records the workbench-specific tour:
 * media pinned beside chat, graph/trace devtools, horizontal layout, floating
 * devtools, and the popout affordance. The story flow is deterministic no-LLM.
 */
import { test, expect, chromium, type Browser, type BrowserContext, type Page, type Locator } from "@playwright/test";
import path from "path";
import fs from "fs";
import { execFileSync } from "child_process";
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
  type WebServer,
} from "./_helpers/server.js";
import { installCurtain, liftCurtain } from "./_helpers/demo.js";
import { cameraContext } from "./_helpers/camera.js";
import {
  SESSION_MEDIA_WORKBENCH_TOUR_STEPS,
  type TourStep,
} from "../../src/tour/generated/session-media-workbench.js";

const ADDR = demoAddr(7766);
const STORY_DIR = path.join(repoRoot, "stories", "mockup-video");
const FLOW = path.join(STORY_DIR, "flows", "demo_web.yaml");
const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "session-media-workbench");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "video");
const CHAPTER_SOURCE = "features/session-media-workbench.yaml";
const DIAG_LOG = path.join(ARTIFACT_DIR, "diagnostic.log");
const ERROR_TXT = path.join(ARTIFACT_DIR, "ERROR.txt");
const REAL_VIDEO = path.join(repoRoot, ".artifacts", "review-video", "render", "walkthrough.mp4");
const REAL_POSTER = path.join(repoRoot, ".artifacts", "review-video", "render", "walkthrough.poster.png");

let server: WebServer;

function diag(msg: string): void {
  try {
    fs.appendFileSync(DIAG_LOG, `[${new Date().toISOString()}] ${msg}\n`);
  } catch {
    /* best-effort */
  }
}

async function resolveTarget(page: Page, step: TourStep): Promise<Locator> {
  return page.getByTestId(step.target!).first();
}

async function dragSplitter(page: Page, target: Locator, deltaX: number, deltaY: number): Promise<void> {
  const box = await target.boundingBox();
  if (!box) throw new Error("splitter target has no bounding box");
  const startX = box.x + box.width / 2;
  const startY = box.y + box.height / 2;
  await page.mouse.move(startX, startY);
  await page.mouse.down();
  await page.mouse.move(startX + deltaX, startY + deltaY, { steps: 8 });
  await page.mouse.up();
}

async function workbenchGrid(page: Page): Promise<string> {
  return page.locator(".iv__main--workbench").evaluate((el) => {
    const style = getComputedStyle(el as HTMLElement);
    return `${style.gridTemplateColumns} / ${style.gridTemplateRows}`;
  });
}

async function settleMediaPreviews(page: Page): Promise<void> {
  await page.evaluate(async () => {
    const videos = Array.from(document.querySelectorAll<HTMLVideoElement>('[data-testid="media-video"]'));
    await Promise.all(videos.map(async (video) => {
      if (Number.isFinite(video.duration) && video.duration > 4 && video.currentTime < 2) {
        await new Promise<void>((resolve) => {
          const done = () => resolve();
          video.addEventListener("seeked", done, { once: true });
          window.setTimeout(done, 800);
          video.currentTime = Math.min(3, video.duration - 0.25);
        });
      }
    }));
  });
}

async function hideTourForProofFrame(page: Page, hidden: boolean): Promise<void> {
  await page.evaluate((hide) => {
    document.documentElement.toggleAttribute("data-qa-product-frame", hide);
  }, hidden);
}

async function productShot(
  page: Page,
  shot: (page: Page, label: string) => Promise<string>,
  label: string,
): Promise<void> {
  await hideTourForProofFrame(page, true);
  await dwell(page, 350);
  await shot(page, label);
  await hideTourForProofFrame(page, false);
}

function useProductShot(step: TourStep): boolean {
  return new Set([
    "smw-media-pane",
    "smw-chat-pane",
    "smw-media-receipt",
    "smw-devtools-graph",
    "smw-resize-media",
    "smw-trace-tab",
    "smw-resize-bottom",
    "smw-popout",
  ]).has(step.id);
}

async function revealPinnedReceipt(page: Page): Promise<void> {
  const receipt = page.getByTestId("chat-media-receipt").first();
  if ((await receipt.count()) === 0) return;
  await receipt.evaluate((el) => {
    (el.closest(".chat-row") ?? el).scrollIntoView({ block: "start", inline: "nearest" });
  }).catch(() => undefined);
}

async function ensureReviewVideo(): Promise<void> {
  const outDir = path.dirname(REAL_VIDEO);
  fs.mkdirSync(outDir, { recursive: true });
  const posterBrowser = await chromium.launch({ headless: true });
  const posterPage = await posterBrowser.newPage({ viewport: { width: 1280, height: 720 } });
  await posterPage.setContent(`
    <html>
      <body style="margin:0;background:#020617;color:#f8fafc;font-family:Inter,Arial,sans-serif;">
        <main style="box-sizing:border-box;width:1280px;height:720px;padding:40px;background:#020617;">
          <section style="height:640px;border-radius:22px;background:#0b1220;border:3px solid #38bdf8;padding:44px;box-sizing:border-box;">
            <div style="display:inline-block;background:#14532d;border-radius:10px;padding:18px 28px;font-size:46px;font-weight:800;">
              Kitsoki architecture walkthrough
            </div>
            <div style="margin-top:48px;font-size:38px;color:#dbeafe;">Story -> Room -> Host call -> Artifact</div>
            <div style="display:flex;gap:40px;margin-top:58px;">
              <div style="width:260px;height:150px;border-radius:14px;background:#1d4ed8;display:flex;align-items:center;justify-content:center;font-size:34px;font-weight:750;">Brief</div>
              <div style="width:260px;height:150px;border-radius:14px;background:#0f766e;display:flex;align-items:center;justify-content:center;font-size:34px;font-weight:750;">Render</div>
              <div style="width:260px;height:150px;border-radius:14px;background:#7c3aed;display:flex;align-items:center;justify-content:center;font-size:34px;font-weight:750;">Review</div>
            </div>
            <div style="margin-top:76px;font-size:30px;color:#f8fafc;">
              Trace and graph stay dockable while media remains pinned.
            </div>
          </section>
        </main>
      </body>
    </html>
  `);
  await posterPage.screenshot({ path: REAL_POSTER });
  await posterBrowser.close();
  execFileSync("ffmpeg", [
    "-hide_banner",
    "-loglevel",
    "error",
    "-y",
    "-loop",
    "1",
    "-framerate",
    "30",
    "-i",
    REAL_POSTER,
    "-t",
    "15",
    "-pix_fmt",
    "yuv420p",
    REAL_VIDEO,
  ]);
  fs.writeFileSync(
    `${REAL_VIDEO}.chapters.json`,
    JSON.stringify(
      [
        { index: 0, id: "scene-0", label: "Architecture overview", start_ms: 0, end_ms: 3000 },
        { index: 1, id: "scene-1", label: "Story anatomy", start_ms: 3000, end_ms: 6000 },
        { index: 2, id: "scene-2", label: "Render and review", start_ms: 6000, end_ms: 9000 },
        { index: 3, id: "scene-3", label: "Traceability", start_ms: 9000, end_ms: 12000 },
        { index: 4, id: "scene-4", label: "Pinned media workflow", start_ms: 12000, end_ms: 15000 },
      ],
      null,
      2,
    ),
  );
}

test.beforeAll(async () => {
  await ensureReviewVideo();
  fs.mkdirSync(ARTIFACT_DIR, { recursive: true });
  prepareVideoDir(VIDEO_DIR);
  fs.writeFileSync(DIAG_LOG, "");
  if (fs.existsSync(ERROR_TXT)) fs.rmSync(ERROR_TXT);
  server = await startWebServer({ addr: ADDR, flow: FLOW, storiesDir: STORY_DIR });
});

test.afterAll(() => server?.stop());

test("session media workbench tour video", async () => {
  test.setTimeout(300000);
  const browser: Browser = await chromium.launch({ headless: true });
  const context: BrowserContext = await browser.newContext(
    cameraContext({ recordVideoDir: VIDEO_DIR }),
  );
  const page: Page = await context.newPage();
  const video = page.video();
  const shot = makeShot(ARTIFACT_DIR);
  const chapters = new ChapterRecorder();

  try {
    await installCurtain(page, "Session Media Workbench");
    diag("navigating home behind curtain");
    await cinematicGoto(page, `${server.base}/#/`, { waitForTestId: "home-view" });
    await page.addStyleTag({
      content: `
        html[data-qa-product-frame] [data-testid="tour-overlay"] {
          visibility: hidden !important;
        }
      `,
    });
    await page.getByTestId("new-session-btn").first().click();
    await page.waitForURL(/#\/s\/[0-9a-f-]{36}\/chat$/, { timeout: 15000 });

    diag("driving mockup-video to review media");
    await expect(page.getByTestId("current-state")).toContainText("intake", { timeout: 15000 });
    await page.getByTestId("intent-btn-ready").click();
    await expect(page.getByTestId("intent-btn-accept")).toBeVisible({ timeout: 30000 });
    await page.getByTestId("intent-btn-accept").click();
    await expect(page.getByTestId("media-video").first()).toBeVisible({ timeout: 30000 });
    await expect(page.getByTestId("media-workbench-toggle")).toBeVisible({ timeout: 15000 });

    await page.evaluate((stepsJson: string) => {
      (window as unknown as { __startTourWithSteps?: (s: string) => void })
        .__startTourWithSteps?.(stepsJson);
    }, JSON.stringify(SESSION_MEDIA_WORKBENCH_TOUR_STEPS));
    await expect(page.getByTestId("tour-overlay")).toBeVisible({ timeout: 8000 });
    await liftCurtain(page);

    for (const step of SESSION_MEDIA_WORKBENCH_TOUR_STEPS) {
      diag(`step ${step.id}`);
      if (step.waitForTarget) {
        await expect(page.getByTestId(step.waitForTarget).first()).toBeVisible({ timeout: 15000 });
      }

      await expect(page.getByTestId("tour-title")).toHaveText(step.title, { timeout: 12000 });
      if (step.id === "smw-resize-media") {
        const target = await resolveTarget(page, step);
        const before = await workbenchGrid(page);
        await settleMediaPreviews(page);
        chapters.open(step.id, step.title, CHAPTER_SOURCE);
        await dwell(page, 800);
        await productShot(page, shot, `${step.id}-before`);
        await dragSplitter(page, target, 160, 0);
        await expect.poll(() => workbenchGrid(page)).not.toBe(before);
        await settleMediaPreviews(page);
        await dwell(page, (step.dwellMs ?? 3000) - 800);
        await productShot(page, shot, `${step.id}-after`);
      } else if (step.id === "smw-resize-bottom") {
        const target = await resolveTarget(page, step);
        const before = await workbenchGrid(page);
        await settleMediaPreviews(page);
        chapters.open(step.id, step.title, CHAPTER_SOURCE);
        await dwell(page, 800);
        await productShot(page, shot, `${step.id}-before`);
        await dragSplitter(page, target, 0, -80);
        await expect.poll(() => workbenchGrid(page)).not.toBe(before);
        await settleMediaPreviews(page);
        await dwell(page, (step.dwellMs ?? 3000) - 800);
        await productShot(page, shot, `${step.id}-after`);
      } else {
        await settleMediaPreviews(page);
        chapters.open(step.id, step.title, CHAPTER_SOURCE);
        if (step.id === "smw-media-receipt") await revealPinnedReceipt(page);
        await dwell(page, step.dwellMs ?? 3000);
        if (useProductShot(step)) await productShot(page, shot, step.id);
        else await shot(page, step.id);
      }

      if (step.kind === "explain") {
        await page.getByTestId("tour-next").click();
        await dwell(page, 700);
      } else {
        const target = await resolveTarget(page, step);
        await target.scrollIntoViewIfNeeded().catch(() => undefined);
        if (step.id !== "smw-resize-media" && step.id !== "smw-resize-bottom") {
          await target.evaluate((el) => (el as HTMLElement).click());
        }
        if (step.id === "smw-float") {
          await expect(page.getByTestId("floating-devtools-pane")).toBeVisible({ timeout: 5000 });
          await settleMediaPreviews(page);
          await productShot(page, shot, `${step.id}-after`);
          await page.getByTestId("floating-devtools-dock").evaluate((el) => (el as HTMLElement).click());
          await expect(page.getByTestId("media-devtools-pane")).toBeVisible({ timeout: 5000 });
        }
        await dwell(page, 1000);
      }
    }

    await expect(page.getByTestId("tour-overlay")).toHaveCount(0, { timeout: 5000 });
  } catch (e) {
    const msg = e instanceof Error ? e.stack ?? e.message : String(e);
    diag(`FAILED: ${msg}`);
    diag(`--- server log ---\n${server?.log?.() ?? ""}`);
    try {
      fs.writeFileSync(ERROR_TXT, `${msg}\n\n--- server log ---\n${server?.log?.() ?? ""}\n`);
    } catch {
      /* best-effort */
    }
    throw e;
  } finally {
    await context.close();
    const mp4 = await saveVideoAsMp4(video, ARTIFACT_DIR, "session-media-workbench-demo");
    writeChapters(mp4, chapters.list());
    await browser.close();
  }

  const pngs = fs.readdirSync(ARTIFACT_DIR).filter((f) => f.endsWith(".png"));
  console.log(`[session-media-workbench] screenshots (${pngs.length}) in ${ARTIFACT_DIR}`);
});
