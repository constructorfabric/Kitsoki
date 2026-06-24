/**
 * "Demo-video loop" feature-tour video demo.
 *
 * Drives the demo-video-loop tour against a real `kitsoki web` server in the
 * deterministic no-LLM posture (--host-cassette web_tour.cassette.yaml --mode
 * one-shot; the cassette stubs the maker / video gate / qa gate / artifact write
 * per iteration, matched by call id) and records a video + per-scene screenshots
 * to .artifacts/demo-video-loop/.
 *
 * Like the golden cherny-loop spec it copies, this runs ONLY the
 * DEMO_VIDEO_LOOP_TOUR_STEPS via window.__startTourWithSteps. The tour drives the
 * whole video: it opens on the home story library and a route-match action step
 * navigates home → new session → the drive view.
 *
 * KEY DIFFERENCE from cherny-loop: demo-video-loop's root `generating` CASCADES
 * on session entry (RunInitialOnEnter fires the whole loop). There is no
 * configure/launch — clicking "New session" runs the ENTIRE autonomous
 * fail-then-pass run (maker-1 → video gate PASS → qa-1 FAIL → loop_again →
 * maker-2 → video gate PASS → qa-2 PASS → @exit:achieved) in one cascade. So
 * there is NO per-step drive: the loop steps are PURE NARRATION over the
 * already-terminal run. The hard signal is the terminal state (__exit__achieved)
 * reached right after the new-session navigation.
 *
 * Validate fast (no dwells):
 *   WEB_CHAT_PACE=0 pnpm exec playwright test demo-video-loop-video --project=chromium
 * Record at watch-speed:
 *   pnpm exec playwright test demo-video-loop-video --project=chromium
 *
 * The harness suppresses Playwright stdout, so per-step progress and any failure
 * context is also written to .artifacts/demo-video-loop/ERROR.txt.
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
import { DEMO_VIDEO_LOOP_TOUR_STEPS } from "../../src/tour/demo-video-loop-manifest.js";

const CHAPTER_SOURCE = "tools/runstatus/src/tour/demo-video-loop-manifest.ts";

// 7793 — distinct from every other spec's port so parallel runs never race.
const ADDR = "127.0.0.1:7793";
const STORY_DIR = path.join(repoRoot, "stories", "demo-video-loop");
const CASSETTE = path.join(STORY_DIR, "flows", "cassettes", "web_tour.cassette.yaml");
const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "demo-video-loop");
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
  // Host cassette + one-shot, NO harness: the loop self-drives via emit_intent
  // (no free-text routing is needed — generating is the root and cascades on
  // session entry). One-shot lets the synthetic emit chain auto-advance the
  // whole fail-then-pass run when the session is created. Host.* calls come from
  // the cassette (deterministic, matched by call id; stamp-epoch / read-qa-report
  // carry replay: any since the rooms reuse one id across iterations).
  server = await startWebServer({
    addr: ADDR,
    storiesDir: STORY_DIR,
    hostCassette: CASSETTE,
    mode: "one-shot",
  });
});

test.afterAll(() => server?.stop());

/** Assert the drive view's current-state reaches `state`. */
async function expectState(page: Page, state: string): Promise<void> {
  await expect(page.getByTestId("current-state")).toHaveText(state, { timeout: 15000 });
}

test("demo-video loop feature-tour video", async () => {
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
    }, JSON.stringify(DEMO_VIDEO_LOOP_TOUR_STEPS));
    await expect(page.getByTestId("tour-overlay")).toBeVisible({ timeout: 8000 });

    let reachedTerminal = false;

    for (const step of DEMO_VIDEO_LOOP_TOUR_STEPS) {
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

      // Drive the trace to TELL this beat's story: expand the proving rows and
      // pulse the specific fields (window.__tourTrace, exposed by TraceTimeline),
      // or clear the focus on steps that don't narrate the trace. This is what
      // makes the trace panel communicate instead of showing collapsed rows.
      await page.evaluate((t) => {
        const api = (window as unknown as {
          __tourTrace?: { focus: (o: unknown) => number; reset: () => void };
        }).__tourTrace;
        if (!api) return;
        if (t) api.focus(t);
        else api.reset();
      }, (step.trace ?? null) as unknown);
      await page.waitForTimeout(350);

      chapters.open(step.id, step.title, CHAPTER_SOURCE);
      await dwell(page, step.dwellMs ?? 3000);
      await shot(page, step.id);

      if (step.kind === "action") {
        // Two intro action steps: "New session" (home → chat) then "observe-link"
        // (chat → observer). The loop cascades autonomously on session creation,
        // so once we land on the chat route the whole fail-then-pass run is
        // already terminal — assert it there (the InteractiveView ships
        // current-state), then navigate on to the trace-focused observer.
        const target = page.getByTestId(step.target!).first();
        await target.scrollIntoViewIfNeeded().catch(() => undefined);
        // DOM-level click so the dim:false tour popover (which can overlap the
        // observe-link) never intercepts the gesture — same trick as intent clicks.
        await target.evaluate((el) => (el as HTMLElement).click());
        await page.waitForTimeout(300);
        if (step.advanceRoute === "interactive") {
          await page.waitForURL(/#\/s\/[0-9a-f-]{36}\/chat$/, { timeout: 15000 });
          // The autonomous run completed on entry — assert the terminal state.
          await expectState(page, "__exit__achieved");
          reachedTerminal = true;
        } else if (step.advanceRoute === "any") {
          await page.waitForURL(/#\/s\/[0-9a-f-]{36}$/, { timeout: 15000 });
        }
        await dwell(page, 1200);
      } else {
        // Pure-narration loop step — the run is already terminal, just dwell + Next.
        await page.getByTestId("tour-next").click();
        await dwell(page, 700);
      }
    }

    expect(reachedTerminal).toBe(true);
    await expect(page.getByTestId("tour-overlay")).toHaveCount(0, { timeout: 5000 });
  } catch (e) {
    diag(`FAILED: ${e instanceof Error ? e.stack ?? e.message : String(e)}`);
    diag(`--- server log ---\n${server?.log?.() ?? ""}`);
    throw e;
  } finally {
    await context.close();
    const mp4 = await saveVideoAsMp4(video, ARTIFACT_DIR, "demo-video-loop-demo");
    writeChapters(mp4, chapters.list());
    await browser.close();
  }

  const pngs = fs.readdirSync(ARTIFACT_DIR).filter((f) => f.endsWith(".png"));
  console.log(`[demo-video-loop-video] screenshots (${pngs.length}) in ${ARTIFACT_DIR}`);
});
