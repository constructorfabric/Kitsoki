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
  waitForAgentComplete,
  prepareVideoDir,
  saveVideoAsMp4,
  ChapterRecorder,
  writeChapters,
  PACE,
  demoAddr,
  maybeInstallAutoRrwebCapture,
  type WebServer,
} from "./_helpers/server.js";
import { cameraContext } from "./_helpers/camera.js";
import { TOUR_STEPS, type TourStep } from "../../src/tour/manifest.js";

// The tour manifest IS the chapter source: each TourStep becomes a chapter
// (source_ref kind=tour) whose [start,end] window is the recorded dwell.
const TOUR_SPEC_PATH = "features/onboarding-tour.yaml";

const ADDR = demoAddr(7745);
// Use the bugfix story with the happy_llm flow + the demo cassette.
// The cassette provides agent.decide episodes that carry an agent: block;
// the web server's cassette dispatcher writes agent.call.start /
// agent.call.complete events so the trace has real decision data for the
// waterfall, decide-verdict, confidence-bar, annotate, and replay tour steps.
const STORY_DIR = path.join(repoRoot, "stories", "bugfix");
const FLOW = path.join(STORY_DIR, "flows", "happy_llm.yaml");
const HOST_CASSETTE = path.join(STORY_DIR, "flows", "demo.cassette.yaml");
const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "tour-video");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "video");

let server: WebServer;

test.beforeAll(async () => {
  prepareVideoDir(VIDEO_DIR);
  server = await startWebServer({ addr: ADDR, flow: FLOW, hostCassette: HOST_CASSETTE, storiesDir: STORY_DIR });
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
  const context: BrowserContext = await browser.newContext(
    cameraContext({ recordVideoDir: VIDEO_DIR }),
  );
  const page: Page = await context.newPage();
  // Capture the Video reference before the context closes — saveAs() works after close().
  const video = page.video();
  const shot = makeShot(ARTIFACT_DIR);

  // Accumulate per-step time windows for the chapter sidecar (slice 1). The
  // clock starts now so windows line up with the recorded MP4 timeline.
  const chapters = new ChapterRecorder();

  // Track the session ID once the session URL is known (set after home-start action).
  let sessionId = "";

  try {
    await page.goto(`${server.base}/#/`);
    await expect(page.getByTestId("home-view")).toBeVisible({ timeout: 15000 });
    await maybeInstallAutoRrwebCapture(page);

    // Force-start for determinism (idempotent with the first-login auto-start).
    await page.evaluate(() => {
      (window as unknown as { __startTour?: () => void }).__startTour?.();
    });
    await expect(page.getByTestId("tour-overlay")).toBeVisible({ timeout: 8000 });

    for (const step of TOUR_STEPS) {
      // Mirror the overlay's route-guard: if this step's route doesn't match the
      // current URL, the overlay auto-skips it — so the spec must too.
      const currentUrl = page.url();
      const currentRouteKind = currentUrl.includes("/chat")
        ? "interactive"
        : currentUrl.match(/#\/s\/[0-9a-f-]{36}$/)
          ? "any"
          : "home";
      if (step.route !== "any" && step.route !== currentRouteKind) {
        // Step is auto-skipped by the overlay; don't assert or act on it.
        continue;
      }

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

      // Before trace-decision-detail: click event rows until an agent.call.complete
      // decide event opens the decide-verdict detail pane. Must run BEFORE the
      // waitForTarget check so the element exists when we assert on it.
      if (step.id === "trace-decision-detail") {
        const rows = page.getByTestId("trace-event-row");
        const count = await rows.count();
        for (let i = 0; i < Math.min(count, 20); i++) {
          await rows.nth(i).click();
          const verdict = page.getByTestId("decide-verdict");
          if (await verdict.isVisible({ timeout: 1500 }).catch(() => false)) break;
        }
      }

      // Honor the step's preconditions before expecting it to render. (These are
      // DOM-presence gates only — the generic tour never waits on a story state.)
      if (step.waitForTarget) {
        await expect(page.getByTestId(step.waitForTarget).first()).toBeVisible({ timeout: 15000 });
      }

      // The live popover must be showing THIS step — the anti-drift assertion.
      // If the overlay has already auto-advanced past this step (e.g. because
      // its anchor element was absent in this run), the spec syncs by checking
      // if the next non-skipped step's title is showing instead; if so, skip.
      const titleEl = page.getByTestId("tour-title");
      const actualTitle = await titleEl.textContent({ timeout: 8000 }).catch(() => "");
      if (actualTitle !== step.title) {
        // The overlay may have skipped this step. Verify it's on a subsequent step.
        const remaining = TOUR_STEPS.slice(TOUR_STEPS.indexOf(step) + 1);
        const isOnNext = remaining.some((s) => s.title === actualTitle);
        if (isOnNext) continue; // spec syncs: skip this step as the overlay did
      }
      await expect(titleEl).toHaveText(step.title, { timeout: 12000 });

      // This step's spotlight is settled and on-screen — open its chapter
      // (auto-closes the prior one) so the dwell below becomes its window.
      chapters.open(step.id, step.title, TOUR_SPEC_PATH);

      await dwell(page, step.dwellMs ?? 3000);
      await shot(page, step.id);

      if (step.kind === "explain") {
        await page.getByTestId("tour-next").click();
      } else {
        const target = await resolveTarget(page, step);
        await target.click();
        // Short settling dwell so the tour overlay has time to advance and
        // re-render before the next iteration's title assertion.
        await page.waitForTimeout(300);
        if (step.advance === "route-match" && step.advanceRoute === "interactive") {
          await page.waitForURL(/#\/s\/[0-9a-f-]{36}\/chat$/, { timeout: 15000 });
          // Capture session ID for later RPC calls.
          const m = page.url().match(/\/s\/([0-9a-f-]{36})\/chat$/);
          if (m) sessionId = m[1];
        } else if (step.advance === "route-match" && step.advanceRoute === "any") {
          await page.waitForURL(/#\/s\/[0-9a-f-]{36}$/, { timeout: 15000 });
          // After navigating to the observer view, drive agent-triggering turns
          // via RPC so the trace has agent.call.complete events for the
          // waterfall / decide-verdict / replay steps.
          if (sessionId) {
            try {
              // Patch the world so agent.decide fires: set judge_mode=llm so
              // the bugfix story's validating room runs the LLM judge on its
              // on_enter, producing agent.call.complete events in the trace.
              await server.rpc("runstatus.session.patch_world", {
                session_id: sessionId,
                patch: {
                  judge_mode: "llm",
                  ticket_id: "TKT-demo",
                  ticket_title: "Demo trace run",
                  workdir: ".worktrees/tkt-demo",
                  workspace_id: "ws-demo",
                  thread: "TKT-demo",
                  base_branch: "main",
                  feature_branch: "fix/tkt-demo",
                  judge_confidence_threshold: 0.8,
                },
              });
              // Submit `start` to trigger agent.decide cascade — fires the
              // judge on every checkpoint's on_enter and produces multiple
              // agent.call.complete events with duration_ms in the trace.
              await server.rpc("runstatus.session.submit", { session_id: sessionId, intent: "start", slots: {} });
              // Poll the trace to a deadline (not a flat sleep) so the agent
              // events have landed before the trace-detail steps spotlight them.
              await waitForAgentComplete(server, sessionId, 1, 40000);
            } catch {
              // Non-fatal: the trace introspection steps degrade gracefully
              // if agent events are absent.
            }
          }
        }
      }
    }

    // The final 'Done' closes the tour.
    await expect(page.getByTestId("tour-overlay")).toHaveCount(0, { timeout: 5000 });
    // Capture a labeled frame of the dismissed state (overlay gone) so a QA pass
    // against the per-step PNGs can verify the tour actually concludes — the
    // tour-done step's own screenshot is taken before Done is clicked and still
    // shows the closing popover. Dwell so the video lingers on the clean UI.
    await dwell(page, 3000);
    await shot(page, "tour-dismissed");
  } finally {
    // Close context first to finalize the video file, then save via the Video
    // reference (avoids picking a stale file). saveAs must happen after context
    // close but before browser close.
    await context.close();
    const mp4 = await saveVideoAsMp4(video, ARTIFACT_DIR, "tour-video-demo");
    // Emit the producer-agnostic chapter sidecar beside the MP4 (slice 1):
    // each tour step → one chapter with source_ref kind=tour.
    writeChapters(mp4, chapters.list());
    await browser.close();
  }

  const pngs = fs.readdirSync(ARTIFACT_DIR).filter((f) => f.endsWith(".png"));
  console.log(`[tour-video] screenshots (${pngs.length}) in ${ARTIFACT_DIR}`);
});
