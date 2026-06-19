/**
 * "Cherny loop" feature-tour video demo.
 *
 * Drives the cherny-loop tour against a real `kitsoki web` server in the
 * deterministic no-LLM posture (--flow web_tour.yaml; the flow's host_handlers
 * stub the maker / script gate / artifact write, so NO host cassette is needed)
 * and records a video + per-scene screenshots to .artifacts/cherny-loop/.
 *
 * Like the golden agent-actions / dev-story-bugfix specs, this runs ONLY the
 * CHERNY_LOOP_TOUR_STEPS via window.__startTourWithSteps. The tour drives the
 * whole video: it opens on the home story library and a route-match action step
 * navigates home → new session → the drive view, then the explain beats narrate
 * the loop while the spec drives the matching intents between beats.
 *
 * Driving mechanics:
 *   - Slotless intents (begin, launch, evaluate) are driven on-camera by
 *     clicking intent-btn-<name>; the resulting current-state / iteration
 *     counter is the hard signal the turn landed.
 *   - The slotted `configure` intent is driven via the __kitsokiSubmitIntent
 *     page hook (goal + gate + budget in one shot).
 *
 * Validate fast (no dwells):
 *   WEB_CHAT_PACE=0 pnpm exec playwright test cherny-loop-video --project=chromium
 * Record at watch-speed:
 *   pnpm exec playwright test cherny-loop-video --project=chromium
 *
 * The harness suppresses Playwright stdout, so per-step progress and any failure
 * context is also written to .artifacts/cherny-loop/ERROR.txt.
 */
import { test, expect, chromium, type Browser, type BrowserContext, type Page } from "@playwright/test";
import path from "path";
import fs from "fs";
import {
  repoRoot,
  startWebServer,
  makeShot,
  prepareVideoDir,
  saveVideoAsMp4,
  ChapterRecorder,
  writeChapters,
  dwell,
  cinematicGoto,
  type WebServer,
} from "./_helpers/server.js";
import { CHERNY_LOOP_TOUR_STEPS } from "../../src/tour/cherny-loop-manifest.js";

const CHAPTER_SOURCE = "stories/cherny-loop/flows/web_tour.yaml";

// 7771 — distinct from every other spec's port so parallel runs never race.
const ADDR = "127.0.0.1:7771";
const STORY_DIR = path.join(repoRoot, "stories", "cherny-loop");
const FLOW = path.join(STORY_DIR, "flows", "web_tour.yaml");
const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "cherny-loop");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "video");
const ERROR_TXT = path.join(ARTIFACT_DIR, "ERROR.txt");

let server: WebServer;

function diag(msg: string): void {
  const line = `[${new Date().toISOString()}] ${msg}\n`;
  try {
    fs.appendFileSync(ERROR_TXT, line);
  } catch {
    /* best-effort */
  }
}

test.beforeAll(async () => {
  prepareVideoDir(VIDEO_DIR);
  fs.mkdirSync(ARTIFACT_DIR, { recursive: true });
  fs.writeFileSync(ERROR_TXT, "");
  server = await startWebServer({ addr: ADDR, flow: FLOW, storiesDir: STORY_DIR });
});

test.afterAll(() => server?.stop());

/** Assert the drive view's current-state reaches `state`. */
async function expectState(page: Page, state: string): Promise<void> {
  await expect(page.getByTestId("current-state")).toHaveText(state, { timeout: 15000 });
}

/** Assert the loop's status bar shows iteration `n` of 4. */
async function expectIter(page: Page, n: number): Promise<void> {
  await expect(page.getByText(new RegExp(`iter ${n}/4`)).first()).toBeVisible({ timeout: 15000 });
}

/** Click a slotless intent button (DOM-level so the overlay backdrop never intercepts). */
async function clickIntent(page: Page, intent: string): Promise<void> {
  const btn = page.getByTestId(`intent-btn-${intent}`).first();
  await expect(btn).toBeVisible({ timeout: 15000 });
  await dwell(page, 500);
  await btn.evaluate((el) => (el as HTMLElement).click());
  await dwell(page, 600);
}

/**
 * Drive the turn associated with a manifest step (AFTER its narration
 * screenshot). Each drive asserts a hard signal — the room transition or the
 * iteration counter — from the flow fixture's verified path.
 */
async function driveForStep(page: Page, stepId: string): Promise<void> {
  switch (stepId) {
    case "cl-configure":
      diag("drive configure (goal + artifact + gate + budget) via hook");
      await page.evaluate(async () => {
        await (window as unknown as {
          __kitsokiSubmitIntent?: (n: string, s?: Record<string, unknown>) => Promise<void>;
        }).__kitsokiSubmitIntent?.("configure", {
          goal: "Get `go test ./internal/ratelimit` green",
          artifact: "internal/ratelimit/limiter.go",
          gate_command: "go test ./internal/ratelimit/",
          gate_mode: "script",
          iteration_budget: 4,
        });
      });
      await expectState(page, "configuring");
      await dwell(page, 600);
      break;
    case "cl-launch":
      diag("drive launch → baseline (gate proven RED, no maker spend)");
      await clickIntent(page, "launch");
      await expectState(page, "baseline");
      break;
    case "cl-baseline":
      diag("drive proceed → iterating (iteration 1)");
      await clickIntent(page, "proceed");
      await expectState(page, "iterating");
      await expectIter(page, 1);
      break;
    case "cl-evaluate-1":
      diag("drive evaluate → iteration 2");
      await clickIntent(page, "evaluate");
      await expectIter(page, 2);
      break;
    case "cl-evaluate-2":
      diag("drive evaluate → iteration 3");
      await clickIntent(page, "evaluate");
      await expectIter(page, 3);
      break;
    case "cl-evaluate-3":
      diag("drive evaluate → iteration 4");
      await clickIntent(page, "evaluate");
      await expectIter(page, 4);
      break;
    case "cl-budget":
      diag("drive evaluate → __exit__exhausted (budget)");
      await clickIntent(page, "evaluate");
      await expectState(page, "__exit__exhausted");
      break;
    default:
      break;
  }
}

test("cherny loop feature-tour video", async () => {
  test.setTimeout(300000);
  const browser: Browser = await chromium.launch({ headless: true });
  const context: BrowserContext = await browser.newContext({
    viewport: { width: 1600, height: 900 },
    deviceScaleFactor: 2,
    recordVideo: { dir: VIDEO_DIR, size: { width: 1600, height: 900 } },
  });
  const page: Page = await context.newPage();
  const video = page.video();
  const shot = makeShot(ARTIFACT_DIR);
  const chapters = new ChapterRecorder();

  try {
    diag("navigating home");
    await cinematicGoto(page, `${server.base}/#/`, { waitForTestId: "home-view" });

    await page.evaluate((stepsJson: string) => {
      (window as unknown as { __startTourWithSteps?: (s: string) => void })
        .__startTourWithSteps?.(stepsJson);
    }, JSON.stringify(CHERNY_LOOP_TOUR_STEPS));
    await expect(page.getByTestId("tour-overlay")).toBeVisible({ timeout: 8000 });

    for (const step of CHERNY_LOOP_TOUR_STEPS) {
      diag(`step ${step.id}`);
      const currentUrl = page.url();
      const currentRouteKind = currentUrl.includes("/chat")
        ? "interactive"
        : currentUrl.match(/#\/s\/[0-9a-f-]{36}$/)
          ? "any"
          : "home";
      if (step.route !== "any" && step.route !== currentRouteKind) {
        diag(`  route-skip (${currentRouteKind})`);
        continue;
      }

      if (step.waitForTarget) {
        await expect(page.getByTestId(step.waitForTarget).first()).toBeVisible({ timeout: 15000 });
      }

      const titleEl = page.getByTestId("tour-title");
      const actualTitle = (await titleEl.textContent({ timeout: 8000 }).catch(() => "")) ?? "";
      if (actualTitle !== step.title) {
        diag(`  re-sync overlay from "${actualTitle}" → "${step.title}"`);
        await page.evaluate((id: string) => {
          (window as unknown as { __tourGoTo?: (s: string) => void }).__tourGoTo?.(id);
        }, step.id);
      }
      await expect(titleEl).toHaveText(step.title, { timeout: 12000 });

      chapters.open(step.id, step.title, CHAPTER_SOURCE);
      await dwell(page, step.dwellMs ?? 3000);
      await shot(page, step.id);

      if (step.kind === "action") {
        // The only action step is the intro "New session" navigation.
        const target = page.getByTestId(step.target!).first();
        await target.scrollIntoViewIfNeeded().catch(() => undefined);
        await target.click();
        await page.waitForTimeout(300);
        if (step.advanceRoute === "interactive") {
          await page.waitForURL(/#\/s\/[0-9a-f-]{36}\/chat$/, { timeout: 15000 });
        }
        await dwell(page, 1000);
      } else {
        await driveForStep(page, step.id);
        await page.getByTestId("tour-next").click();
        await dwell(page, 700);
      }
    }

    await expect(page.getByTestId("tour-overlay")).toHaveCount(0, { timeout: 5000 });
  } catch (e) {
    diag(`FAILED: ${e instanceof Error ? e.stack ?? e.message : String(e)}`);
    diag(`--- server log ---\n${server?.log?.() ?? ""}`);
    throw e;
  } finally {
    await context.close();
    const mp4 = await saveVideoAsMp4(video, ARTIFACT_DIR, "cherny-loop-demo");
    writeChapters(mp4, chapters.list());
    await browser.close();
  }

  const pngs = fs.readdirSync(ARTIFACT_DIR).filter((f) => f.endsWith(".png"));
  console.log(`[cherny-loop-video] screenshots (${pngs.length}) in ${ARTIFACT_DIR}`);
});
