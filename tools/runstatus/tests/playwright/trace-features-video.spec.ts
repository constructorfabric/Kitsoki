/**
 * Trace-introspection feature-spotlight video demo.
 *
 * Drives the dedicated trace-features tour against a real `kitsoki web` server
 * in the deterministic no-LLM posture (--flow winning_deterministic.yaml) and
 * records a video + per-scene screenshots to .artifacts/trace-features/.
 *
 * Unlike tour-video.spec.ts (which walks the full 13-step onboarding), this spec
 * runs ONLY the trace-introspection steps from src/tour/trace-manifest.ts via
 * window.__startTourWithSteps. The tour itself drives the whole video: it opens
 * on the home story library, then its route-match action steps navigate home →
 * new session → observer, so even the intro is tour-narrated rather than silent
 * spec orchestration.
 *
 * Validate fast (no dwells):
 *   WEB_CHAT_PACE=0 pnpm exec playwright test trace-features-video --project=chromium
 * Record at watch-speed:
 *   pnpm exec playwright test trace-features-video --project=chromium
 */
import { test, expect, chromium, type Browser, type BrowserContext, type Page, type Locator } from "@playwright/test";
import path from "path";
import fs from "fs";
import {
  startWebServer,
  repoRoot,
  makeShot,
  prepareVideoDir,
  saveAndRemuxVideo,
  dwell,
  cinematicGoto,
  SETTLE_MS,
  type WebServer,
} from "./_helpers/server.js";
import { TRACE_TOUR_STEPS, type TourStep } from "../../src/tour/trace-manifest.js";

const ADDR = "127.0.0.1:7746";
// Use the bugfix story with the happy_llm flow + the demo cassette so the
// trace has real oracle.call.complete events for the waterfall, decide-verdict,
// confidence-bar, annotation, and replay steps.
const STORY_DIR = path.join(repoRoot, "stories", "bugfix");
const FLOW = path.join(STORY_DIR, "flows", "happy_llm.yaml");
const HOST_CASSETTE = path.join(STORY_DIR, "flows", "demo.cassette.yaml");
const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "trace-features");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "video");

let server: WebServer;

test.beforeAll(async () => {
  prepareVideoDir(VIDEO_DIR);
  server = await startWebServer({ addr: ADDR, flow: FLOW, hostCassette: HOST_CASSETTE, storiesDir: STORY_DIR });
});

test.afterAll(() => server?.stop());

/**
 * Resolve an action step's real target element — first visible match.
 */
async function resolveTarget(page: Page, step: TourStep): Promise<Locator> {
  return page.getByTestId(step.target!).first();
}

test("trace introspection feature-spotlight video", async () => {
  test.setTimeout(300000);
  const browser: Browser = await chromium.launch({ headless: true });
  const context: BrowserContext = await browser.newContext({
    viewport: { width: 1600, height: 900 },
    recordVideo: { dir: VIDEO_DIR, size: { width: 1600, height: 900 } },
  });
  const page: Page = await context.newPage();
  const video = page.video();
  const shot = makeShot(ARTIFACT_DIR);

  // Carries the session id once the intro's "New session" step creates the run.
  let sessionId = "";

  try {
    // ── 1. Open the home story library and start the tour ON it ──────────────
    // The whole video is tour-driven: rather than silently flashing home -> chat
    // -> observer before the overlay appears, we start the tour on home and let
    // its route-match action steps perform the navigation, each narrated.
    await cinematicGoto(page, `${server.base}/#/`, { waitForTestId: "home-view" });

    await page.evaluate((stepsJson: string) => {
      (window as unknown as { __startTourWithSteps?: (s: string) => void })
        .__startTourWithSteps?.(stepsJson);
    }, JSON.stringify(TRACE_TOUR_STEPS));
    await expect(page.getByTestId("tour-overlay")).toBeVisible({ timeout: 8000 });

    // ── 2. Walk the TRACE_TOUR_STEPS (intro + introspection) ─────────────────
    for (const step of TRACE_TOUR_STEPS) {
      // Mirror the overlay's route-guard. The intro steps are home/interactive;
      // the introspection steps are route "any" on the observer.
      const currentUrl = page.url();
      const currentRouteKind = currentUrl.includes("/chat")
        ? "interactive"
        : currentUrl.match(/#\/s\/[0-9a-f-]{36}$/)
          ? "any"
          : "home";
      if (step.route !== "any" && step.route !== currentRouteKind) {
        continue;
      }

      // Before trace-decision-detail: click rows until the decide-verdict pane
      // opens. Must run before waitForTarget so the element is present.
      if (step.id === "trace-decision-detail") {
        const rows = page.getByTestId("trace-event-row");
        const count = await rows.count();
        for (let i = 0; i < Math.min(count, 20); i++) {
          await rows.nth(i).click();
          const verdict = page.getByTestId("decide-verdict");
          if (await verdict.isVisible({ timeout: 1500 }).catch(() => false)) break;
        }
        // Settle so the row-scan flicker resolves into a composed verdict pane
        // before the spotlight lands on it.
        await dwell(page, SETTLE_MS);
      }

      // Honor DOM-presence preconditions.
      if (step.waitForTarget) {
        await expect(page.getByTestId(step.waitForTarget).first()).toBeVisible({ timeout: 15000 });
      }

      // Anti-drift assertion: the popover must show THIS step's title.
      const titleEl = page.getByTestId("tour-title");
      const actualTitle = await titleEl.textContent({ timeout: 8000 }).catch(() => "");
      if (actualTitle !== step.title) {
        // The overlay may have skipped this step (e.g. target absent).
        const remaining = TRACE_TOUR_STEPS.slice(TRACE_TOUR_STEPS.indexOf(step) + 1);
        const isOnNext = remaining.some((s) => s.title === actualTitle);
        if (isOnNext) continue;
      }
      await expect(titleEl).toHaveText(step.title, { timeout: 12000 });

      await dwell(page, step.dwellMs ?? 3000);
      await shot(page, step.id);

      if (step.kind === "explain") {
        await page.getByTestId("tour-next").click();
        // Let the spotlight animation move to the next target before we assert on it.
        await dwell(page, 700);
      } else {
        // Action step: click the real control (the overlay leaves a click-through
        // hole for it). The intro's navigation steps advance by route-match, so
        // wait for the URL to actually change before the next iteration asserts.
        const target = await resolveTarget(page, step);
        await target.click();
        await page.waitForTimeout(300);
        if (step.advance === "route-match" && step.advanceRoute === "interactive") {
          // "New session" → the freshly-created run's chat view. Capture its id
          // for the submit we fire once we reach the observer.
          await page.waitForURL(/#\/s\/[0-9a-f-]{36}\/chat$/, { timeout: 15000 });
          const m = page.url().match(/\/s\/([0-9a-f-]{36})\/chat$/);
          if (m) sessionId = m[1];
        } else if (step.advance === "route-match" && step.advanceRoute === "any") {
          // "Observe" → the read-only observer. Now that the chat view is no
          // longer the active surface, patch the world and submit so the
          // cassette-backed oracle cascade streams its events into the observer's
          // live trace, ahead of the introspection steps that spotlight them.
          await page.waitForURL(/#\/s\/[0-9a-f-]{36}$/, { timeout: 15000 });
          if (sessionId) {
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
            await server.rpc("runstatus.session.submit", {
              session_id: sessionId,
              intent: "start",
              slots: {},
            });
            // Let the server process oracle events and push SSE updates before
            // the introspection steps start spotlighting trace rows.
            await page.waitForTimeout(3000);
          }
        }
        // Longer settle for action steps: tab switches / nav need the view to repaint.
        await dwell(page, 1000);
      }
    }

    // The final trace-done step's "Done" closes the tour.
    await expect(page.getByTestId("tour-overlay")).toHaveCount(0, { timeout: 5000 });
  } finally {
    await context.close();
    await saveAndRemuxVideo(video, ARTIFACT_DIR, "trace-features-demo");
    await browser.close();
  }

  const pngs = fs.readdirSync(ARTIFACT_DIR).filter((f) => f.endsWith(".png"));
  console.log(`[trace-features-video] screenshots (${pngs.length}) in ${ARTIFACT_DIR}`);
});
