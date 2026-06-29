/**
 * Alt-click placed bug report rrweb proof.
 *
 * This is a focused browser-backed regression/demo for the point-specific bug
 * reporter. Unit tests mock the store and cannot prove the real DOM event +
 * runstatus.bug.preview path; this spec drives a real `kitsoki web` server,
 * Alt-right-clicks a story title, asserts the placed bug-report context menu
 * appears, chooses "Report bug here", asserts the review modal opens with
 * placement context, writes an rrweb event stream, then renders that stream to
 * MP4.
 *
 * Artifacts:
 *   .artifacts/alt-click-bug-report/
 *     01-home-before-alt-click.png
 *     02-context-menu-after-right-click.png
 *     03-modal-after-menu-action.png
 *     04-modal-replay.png
 *     05-modal-description-placement.png
 *     alt-click-bug-report.rrweb.json
 *     alt-click-bug-report-demo.mp4
 *     frames/frame-*.png
 *
 * Run:
 *   KITSOKI_WEB_GO_RUN=1 pnpm exec playwright test alt-click-bug-report-rrweb --project=chromium
 */
import { test, expect, chromium, type Browser, type BrowserContext, type Page } from "@playwright/test";
import path from "path";
import fs from "fs";
import {
  startWebServer,
  repoRoot,
  makeShot,
  dwell,
  cinematicGoto,
  demoAddr,
  type WebServer,
} from "./_helpers/server.js";
import {
  installCapture,
  dumpCapture,
  writeEvents,
  renderReplayToMp4,
} from "./_helpers/rrweb-replay.js";

const ADDR = demoAddr(7761);
const STORY_DIR = path.join(repoRoot, "stories", "weather-report");
const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "alt-click-bug-report");
const EVENTS_JSON = path.join(ARTIFACT_DIR, "alt-click-bug-report.rrweb.json");
const DIAG_LOG = path.join(ARTIFACT_DIR, "diagnostic.log");
const VIEWPORT = { width: 1600, height: 900 } as const;

let server: WebServer;

function diag(msg: string): void {
  fs.mkdirSync(ARTIFACT_DIR, { recursive: true });
  fs.appendFileSync(DIAG_LOG, `[${new Date().toISOString()}] ${msg}\n`);
}

async function expectReplayCursorNear(framesDir: string, point: { x: number; y: number }): Promise<void> {
  const frames = fs.existsSync(framesDir)
    ? fs.readdirSync(framesDir).filter((f) => f.endsWith(".png")).sort()
    : [];
  expect(frames.length, "replay render should extract PNG frames").toBeGreaterThan(0);

  const browser = await chromium.launch({ headless: true });
  const page = await browser.newPage();
  try {
    for (const frame of frames) {
      const framePath = path.join(framesDir, frame);
      const frameURL = `data:image/png;base64,${fs.readFileSync(framePath).toString("base64")}`;
      const counts = await page.evaluate(
        async ({ url, x, y }) => {
          const img = new Image();
          img.src = url;
          await img.decode();
          const canvas = document.createElement("canvas");
          canvas.width = img.naturalWidth;
          canvas.height = img.naturalHeight;
          const ctx = canvas.getContext("2d");
          if (!ctx) throw new Error("2d canvas unavailable");
          ctx.drawImage(img, 0, 0);
          const sx = Math.max(0, x - 2);
          const sy = Math.max(0, y - 2);
          const data = ctx.getImageData(sx, sy, 40, 44).data;
          let white = 0;
          let black = 0;
          let blue = 0;
          for (let i = 0; i < data.length; i += 4) {
            const r = data[i];
            const g = data[i + 1];
            const b = data[i + 2];
            const a = data[i + 3];
            if (a > 200 && r > 245 && g > 245 && b > 245) white += 1;
            if (a > 200 && r < 20 && g < 20 && b < 20) black += 1;
            if (a > 120 && b > 150 && r < 120 && g < 170) blue += 1;
          }
          return { white, black, blue };
        },
        {
          url: frameURL,
          x: point.x,
          y: point.y,
        },
      );
      if (counts.white > 60 && counts.black > 30 && counts.blue > 50) return;
    }
  } finally {
    await browser.close();
  }

  throw new Error(`rendered replay frames did not show a visible cursor near ${point.x},${point.y}`);
}

test.beforeAll(async () => {
  test.setTimeout(120000);
  fs.rmSync(ARTIFACT_DIR, { recursive: true, force: true });
  fs.mkdirSync(ARTIFACT_DIR, { recursive: true });
  fs.writeFileSync(DIAG_LOG, "");
  server = await startWebServer({ addr: ADDR, storiesDir: STORY_DIR });
});

test.afterAll(() => server?.stop());

test("Alt-right-click menu opens placed bug report modal and rrweb video evidence", async () => {
  test.setTimeout(180000);

  const browser: Browser = await chromium.launch({ headless: true });
  const context: BrowserContext = await browser.newContext({
    viewport: { ...VIEWPORT },
    deviceScaleFactor: 1,
  });
  const page: Page = await context.newPage();
  const shot = makeShot(ARTIFACT_DIR);

  try {
    diag("navigating home");
    await cinematicGoto(page, `${server.base}/#/`, {
      waitForTestId: "home-view",
      settleMs: 2500,
    });
    await expect(page.getByTestId("story-title").first()).toBeVisible({ timeout: 15000 });

    await installCapture(page);
    diag("rrweb capture installed");
    await shot(page, "home-before-alt-click");
    await dwell(page, 5000);

    diag("alt-right-clicking story title");
    await page.getByTestId("story-title").first().click({
      button: "right",
      modifiers: ["Alt"],
    });
    const pointMenu = page.getByTestId("bug-point-menu");
    await expect(pointMenu).toBeVisible({ timeout: 8000 });
    await expect(page.getByTestId("bug-point-menu-report")).toContainText("Report bug here");
    await expect(page.getByTestId("bug-point-menu-report")).toContainText(
      '[data-testid="story-title"]',
    );
    await dwell(page, 7000);
    await shot(page, "context-menu-after-right-click");

    await page.getByTestId("bug-point-menu-report").click();

    const modal = page.getByTestId("bug-modal");
    await expect(modal).toBeVisible({ timeout: 20000 });
    await expect(page.getByTestId("bug-modal-placement")).toBeVisible({ timeout: 8000 });
    await expect(page.getByTestId("bug-modal-placement-target")).toContainText(
      '[data-testid="story-title"]',
    );
    await expect(page.getByTestId("bug-modal-description")).toHaveValue(/Clicked location:/, {
      timeout: 8000,
    });
    await dwell(page, 7000);
    await shot(page, "modal-after-menu-action");

    await expect(page.getByTestId("bug-modal-replay")).toBeVisible({ timeout: 8000 });
    await page.waitForFunction(
      () => {
        const host = document.querySelector('[data-testid="bug-modal-replay"]');
        const ifr = host?.querySelector("iframe") as HTMLIFrameElement | null;
        const len = ifr?.contentDocument?.body?.innerHTML.length ?? 0;
        return len > 200;
      },
      { timeout: 15000 },
    );
    await dwell(page, 7000);
    await shot(page, "modal-replay");

    const description = page.getByTestId("bug-modal-description");
    await description.scrollIntoViewIfNeeded();
    await expect(description).toBeVisible({ timeout: 8000 });
    await dwell(page, 7000);
    await shot(page, "modal-description-placement");

    await dwell(page, 10000);
    await page.evaluate(() => {
      document.body.setAttribute("data-alt-click-bug-report-proof", String(Date.now()));
    });
    await page.waitForTimeout(500);

    const { events, viewport } = await dumpCapture(page);
    diag(`rrweb captured ${events.length} events @ ${viewport.width}x${viewport.height} dsf=${viewport.deviceScaleFactor}`);
    expect(events.length, "rrweb should include a valid home view and modal transition stream").toBeGreaterThan(10);
    writeEvents(events, EVENTS_JSON, viewport);
  } catch (e) {
    diag(`FAILED: ${e instanceof Error ? e.stack ?? e.message : String(e)}`);
    diag(`--- server log ---\n${server?.log?.() ?? ""}`);
    throw e;
  } finally {
    await context.close();
    await browser.close();
  }

  const render = await renderReplayToMp4({
    eventsJsonPath: EVENTS_JSON,
    viewport: { ...VIEWPORT },
    deviceScaleFactor: 1,
    outDir: ARTIFACT_DIR,
    name: "alt-click-bug-report-demo",
  });
  expect(render.mp4Path).toBeTruthy();
  expect(render.eventCount).toBeGreaterThan(10);
  await expectReplayCursorNear(render.framesDir, { x: 461, y: 95 });
});
