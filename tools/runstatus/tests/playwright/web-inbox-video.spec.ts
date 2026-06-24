/**
 * Web async-inbox feature-spotlight video demo.
 *
 * Drives the dedicated async-inbox tour against a real `kitsoki web` server in
 * the deterministic no-LLM posture (--flow inbox-demo/background_notifies.yaml,
 * nil harness) and records a video + per-scene screenshots to
 * .artifacts/web-inbox/.
 *
 * The demo story (stories/inbox-demo) launches a `background: true` host.run
 * whose flow-stubbed handler completes after a short real-clock delay. On the
 * terminal transition the runtime auto-posts a success notification whose
 * TeleportState is the originating room (working); the server fans it out over
 * the global notifications SSE (GET /rpc/notifications), the SPA's global inbox
 * badge increments, a toast appears, and the panel item teleports back to the
 * origin room. No LLM, no cassette — the background job terminates on the real
 * scheduler clock (the stub's `delay` sleeps real time under `kitsoki web`).
 *
 * Like agent-actions-video.spec.ts, this spec runs ONLY the WEB_INBOX_TOUR_STEPS
 * from src/tour/generated/web-inbox.ts via window.__startTourWithSteps. The tour
 * drives the whole video: it opens on the home story library and its route-match
 * action step navigates home → new session → chat, so even the intro is
 * tour-narrated. The inbox surfaces (badge/toast/panel) are the cross-client SSE
 * path under test — the spec waits on inbox-badge-count rather than racing.
 *
 * Validate fast (no dwells):
 *   WEB_CHAT_PACE=0 pnpm exec playwright test web-inbox-video --project=chromium
 * Record at watch-speed:
 *   pnpm exec playwright test web-inbox-video --project=chromium
 *
 * NOTE: the harness suppresses Playwright stdout, so per-step progress and any
 * failure context is also written to .artifacts/web-inbox/ERROR.txt.
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
import { WEB_INBOX_TOUR_STEPS, type TourStep } from "../../src/tour/generated/web-inbox.js";

// 7791 — distinct from agent-actions (7748), tour-onboarding (7747), and
// trace-features (7746) so parallel spec files never race on the same port.
const ADDR = demoAddr(7791);
const STORY_DIR = path.join(repoRoot, "stories", "inbox-demo");
const FLOW = path.join(STORY_DIR, "flows", "background_notifies.yaml");
const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "web-inbox");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "video");
const ERROR_LOG = path.join(ARTIFACT_DIR, "ERROR.txt");

// The feature-catalog source of truth for this spec's tour steps: each step
// becomes a chapter (source_ref kind=tour) whose [start,end] window is the
// recorded dwell.
const CHAPTER_SOURCE = "features/web-inbox.yaml";

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

/** Resolve an action step's real target element — first visible match. */
async function resolveTarget(page: Page, step: TourStep): Promise<Locator> {
  return page.getByTestId(step.target!).first();
}

test("web async-inbox feature-spotlight video", async () => {
  test.setTimeout(300000);
  const browser: Browser = await chromium.launch({ headless: true });
  const context: BrowserContext = await browser.newContext(
    cameraContext({ recordVideoDir: VIDEO_DIR }),
  );
  const page: Page = await context.newPage();
  const video = page.video();
  // Accumulate per-step time windows for the chapter sidecar. The clock starts
  // now so windows line up with the recorded MP4 timeline.
  const chapters = new ChapterRecorder();
  const shot = makeShot(ARTIFACT_DIR);

  try {
    // ── 1. Open the home story library and start the tour ON it ──────────────
    mark("navigating home");
    await cinematicGoto(page, `${server.base}/#/`, { waitForTestId: "home-view" });

    await page.evaluate((stepsJson: string) => {
      (window as unknown as { __startTourWithSteps?: (s: string) => void })
        .__startTourWithSteps?.(stepsJson);
    }, JSON.stringify(WEB_INBOX_TOUR_STEPS));
    await expect(page.getByTestId("tour-overlay")).toBeVisible({ timeout: 8000 });

    // ── 2. Walk the WEB_INBOX_TOUR_STEPS ─────────────────────────────────────
    for (const step of WEB_INBOX_TOUR_STEPS) {
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

      // ── Pre-step setup ──────────────────────────────────────────────────
      // Before the panel step, open the inbox panel by clicking the badge so
      // the inbox-panel / inbox-item / inbox-jump testids are present and the
      // spotlight lands on a composed surface (the toast may have auto-dismissed
      // by now — the panel is the durable surface).
      if (step.id === "wi-panel") {
        await page.getByTestId("inbox-badge").click({ timeout: 8000 });
        await expect(page.getByTestId("inbox-panel")).toBeVisible({ timeout: 8000 });
        await dwell(page, SETTLE_MS);
      }

      // Honor DOM-presence preconditions. The badge/toast are the cross-client
      // SSE surfaces under test — a generous timeout, not a race.
      if (step.waitForTarget) {
        await expect(page.getByTestId(step.waitForTarget).first()).toBeVisible({ timeout: 20000 });
      }

      // Anti-drift assertion: the popover must show THIS step's title.
      const titleEl = page.getByTestId("tour-title");
      const actualTitle = await titleEl.textContent({ timeout: 8000 }).catch(() => "");
      if (actualTitle !== step.title) {
        const remaining = WEB_INBOX_TOUR_STEPS.slice(WEB_INBOX_TOUR_STEPS.indexOf(step) + 1);
        const isOnNext = remaining.some((s) => s.title === actualTitle);
        if (isOnNext) {
          mark(`  drift-skip: overlay on "${actualTitle}"`);
          continue;
        }
      }
      await expect(titleEl).toHaveText(step.title, { timeout: 12000 });

      // This step's spotlight is settled and on-screen — open its chapter
      // (auto-closes the prior one) so the dwell below becomes its window.
      chapters.open(step.id, step.title, CHAPTER_SOURCE);

      // The toast is transient (auto-dismisses 6s after the SSE push), so grab
      // its screenshot BEFORE the dwell consumes the remaining window; every
      // other (durable) surface screenshots after the dwell as usual.
      if (step.id === "wi-toast") {
        await shot(page, step.id);
        await dwell(page, step.dwellMs ?? 3000);
      } else {
        await dwell(page, step.dwellMs ?? 3000);
        await shot(page, step.id);
      }

      if (step.kind === "explain") {
        await page.getByTestId("tour-next").click();
        await dwell(page, 700);
      } else {
        const target = await resolveTarget(page, step);
        await target.scrollIntoViewIfNeeded().catch(() => undefined);
        if (step.advance === "route-match") {
          // Intro navigation (New session). The chat view is static at this
          // point so a hit-test click goes cleanly through the overlay hole.
          await target.click();
          await page.waitForTimeout(300);
          if (step.advanceRoute === "interactive") {
            await page.waitForURL(/#\/s\/[0-9a-f-]{36}\/chat$/, { timeout: 15000 });
            const m = page.url().match(/\/s\/([0-9a-f-]{36})\/chat$/);
            if (m) mark(`session ${m[1]}`);
          }
          await dwell(page, 1000);
        } else {
          // click-target. Dispatch the DOM click directly so it fires the
          // control's own @click AND the overlay's capture-phase advance
          // listener regardless of paint order.
          await target.evaluate((el) => (el as HTMLElement).click());
          // After the launch click, the background job runs ~0.5s on the real
          // scheduler clock then fans the notification out over SSE.
          if (step.id === "wi-launch") {
            await expect(page.getByTestId("inbox-badge-count")).toBeVisible({ timeout: 20000 });
            mark("inbox-badge-count visible (background job terminal → SSE)");
          }
          // After the jump click, the session teleports back to the origin room.
          if (step.id === "wi-jump") {
            await expect(page.getByTestId("current-state")).toHaveText("working", { timeout: 15000 });
            mark("teleport landed on origin state: working");
          }
          await dwell(page, 1000);
        }
      }
    }

    // The final wi-done step's "Done" closes the tour.
    await expect(page.getByTestId("tour-overlay")).toHaveCount(0, { timeout: 5000 });
  } catch (e) {
    mark(`FAILED: ${e instanceof Error ? e.stack ?? e.message : String(e)}`);
    mark(`--- server log ---\n${server?.log?.() ?? ""}`);
    await page.screenshot({ path: path.join(ARTIFACT_DIR, "99-failure.png") }).catch(() => undefined);
    throw e;
  } finally {
    await context.close();
    const mp4 = await saveVideoAsMp4(video, ARTIFACT_DIR, "web-inbox-demo");
    // Emit the producer-agnostic chapter sidecar beside the MP4: each tour
    // step → one chapter with source_ref kind=tour.
    writeChapters(mp4, chapters.list());
    await browser.close();
  }

  const pngs = fs.readdirSync(ARTIFACT_DIR).filter((f) => f.endsWith(".png"));
  console.log(`[web-inbox-video] screenshots (${pngs.length}) in ${ARTIFACT_DIR}`);
});
