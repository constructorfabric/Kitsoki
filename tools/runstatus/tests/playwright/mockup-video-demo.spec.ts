/**
 * Mockup Video Studio — full story-walkthrough feature-spotlight video.
 *
 * Drives the dedicated mockup-video tour (src/tour/generated/mockup-video.ts)
 * against a real `kitsoki web` server in the deterministic no-LLM posture
 * (--flow stories/mockup-video/flows/demo_web.yaml) and records a video +
 * per-step screenshots to .artifacts/mockup-video-demo/.
 *
 * Like the golden agent-actions spec, this runs ONLY the MOCKUP_VIDEO_TOUR_STEPS
 * via window.__startTourWithSteps. The tour drives the WHOLE video: it opens on
 * the home story library, its route-match action step navigates home → new
 * session (interactive), and each advancing story turn is a real on-camera click
 * of an `intent-btn-<intent>` button (a real click renders the turn result
 * directly — no RPC-then-reload race). The narration steps Next through the room
 * views.
 *
 * The produced video PLAYS INLINE: demo_web.yaml stubs host.slidey.render to the
 * REAL pre-rendered walkthrough under .artifacts/review-video/render and leaves
 * host.artifacts_dir REAL, so the bound (content-addressed) handle resolves
 * through the JournalArtifactResolver and the room's <video> (data-testid=
 * "media-video") serves the real mp4 bytes.
 *
 * Validate fast (no dwells):
 *   WEB_CHAT_PACE=0 pnpm exec playwright test mockup-video-demo --project=chromium
 * Record at watch-speed:
 *   pnpm exec playwright test mockup-video-demo --project=chromium
 *
 * NOTE: the harness suppresses Playwright stdout, so per-step progress and any
 * failure context is also written to .artifacts/mockup-video-demo/ERROR.txt and
 * diagnostic.log.
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
  SETTLE_MS,
  ChapterRecorder,
  writeChapters,
  demoAddr,
  type WebServer,
} from "./_helpers/server.js";
import { cameraContext } from "./_helpers/camera.js";
import { MOCKUP_VIDEO_TOUR_STEPS, type TourStep } from "../../src/tour/generated/mockup-video.js";

// 7755 — distinct from review-video (7754) / agent-actions (7748) so parallel
// spec files never race on the same port bind.
const ADDR = demoAddr(7755);
const STORY_DIR = path.join(repoRoot, "stories", "mockup-video");
const FLOW = path.join(STORY_DIR, "flows", "demo_web.yaml");
const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "mockup-video-demo");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "video");
// Feature-catalog source of truth for this spec's tour steps — each step
// becomes a chapter in the SPEC's own recorded MP4 sidecar (NOT the story's
// inline walkthrough.mp4, which carries its own Go-side chapters).
const CHAPTER_SOURCE = "features/mockup-video.yaml";
const DIAG_LOG = path.join(ARTIFACT_DIR, "diagnostic.log");
const ERROR_TXT = path.join(ARTIFACT_DIR, "ERROR.txt");
const REAL_VIDEO = path.join(repoRoot, ".artifacts", "review-video", "render", "walkthrough.mp4");

let server: WebServer;

function diag(msg: string): void {
  const line = `[${new Date().toISOString()}] ${msg}\n`;
  try {
    fs.appendFileSync(DIAG_LOG, line);
  } catch {
    /* best-effort */
  }
}

test.beforeAll(async () => {
  // The real pre-rendered walkthrough must already exist (demo_web stubs the
  // render to it; the artifacts_dir builtin copies + journals it).
  if (!fs.existsSync(REAL_VIDEO) || !fs.existsSync(REAL_VIDEO + ".chapters.json")) {
    throw new Error(
      `missing real render at ${REAL_VIDEO}(.chapters.json) — render the deck + generate chapters first (see review-video.spec.ts header).`,
    );
  }
  fs.mkdirSync(ARTIFACT_DIR, { recursive: true });
  prepareVideoDir(VIDEO_DIR);
  fs.writeFileSync(DIAG_LOG, "");
  if (fs.existsSync(ERROR_TXT)) fs.rmSync(ERROR_TXT);
  server = await startWebServer({ addr: ADDR, flow: FLOW, storiesDir: STORY_DIR });
});

test.afterAll(() => server?.stop());

/** Resolve an action step's real target element — first visible match. */
async function resolveTarget(page: Page, step: TourStep): Promise<Locator> {
  return page.getByTestId(step.target!).first();
}

test("mockup video studio full-walkthrough feature-spotlight video", async () => {
  test.setTimeout(300000);
  const browser: Browser = await chromium.launch({ headless: true });
  const context: BrowserContext = await browser.newContext(
    cameraContext({ recordVideoDir: VIDEO_DIR }),
  );
  const page: Page = await context.newPage();
  const video = page.video();
  const shot = makeShot(ARTIFACT_DIR);

  // Accumulate per-step time windows for the chapter sidecar. The clock starts
  // now so windows line up with the recorded MP4 timeline.
  const chapters = new ChapterRecorder();

  try {
    // ── Open the home story library and start the tour ON it ─────────────────
    diag("navigating home");
    await cinematicGoto(page, `${server.base}/#/`, { waitForTestId: "home-view" });

    await page.evaluate((stepsJson: string) => {
      (window as unknown as { __startTourWithSteps?: (s: string) => void })
        .__startTourWithSteps?.(stepsJson);
    }, JSON.stringify(MOCKUP_VIDEO_TOUR_STEPS));
    await expect(page.getByTestId("tour-overlay")).toBeVisible({ timeout: 8000 });

    // ── Walk the MOCKUP_VIDEO_TOUR_STEPS ─────────────────────────────────────
    for (const step of MOCKUP_VIDEO_TOUR_STEPS) {
      diag(`step ${step.id}`);
      // Mirror the overlay's route-guard. The intro steps are home; the rest are
      // interactive (the /chat drive route — the whole story runs there).
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

      // Pre-step settle on the video steps so the player composes (poster +
      // metadata) before the spotlight lands on it.
      if (step.id === "mv-review-video" || step.id === "mv-done-video") {
        await dwell(page, SETTLE_MS);
      }
      // The closing done step is centered/anchorless, so it keeps whatever
      // scroll position the prior video spotlight left. Scroll the done room's
      // "Scenarios covered" list into view so the final frame shows the gallery
      // contents (the scenarios the walkthrough covers), not just the player.
      if (step.id === "mv-done") {
        await page
          .getByRole("heading", { name: /scenarios covered/i })
          .first()
          .scrollIntoViewIfNeeded()
          .catch(() => undefined);
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
        const remaining = MOCKUP_VIDEO_TOUR_STEPS.slice(MOCKUP_VIDEO_TOUR_STEPS.indexOf(step) + 1);
        const isOnNext = remaining.some((s) => s.title === actualTitle);
        if (isOnNext) {
          diag(`  drift-skip: overlay on "${actualTitle}"`);
          continue;
        }
      }
      await expect(titleEl).toHaveText(step.title, { timeout: 12000 });

      // This step's spotlight is settled and on-screen — open its chapter
      // (auto-closes the prior one) so the dwell below becomes its window.
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
          // home → new session (interactive). The home view is static here, so
          // the click goes cleanly through the overlay's hole. Wait for the URL
          // to change before the next iteration asserts.
          await target.click();
          await page.waitForTimeout(300);
          if (step.advanceRoute === "interactive") {
            await page.waitForURL(/#\/s\/[0-9a-f-]{36}\/chat$/, { timeout: 15000 });
            const m = page.url().match(/\/s\/([0-9a-f-]{36})\/chat$/);
            if (m) diag(`session ${m[1]}`);
          }
          await dwell(page, 1200);
        } else {
          // click-target: a story-turn intent button. Dispatch the DOM click
          // directly so it fires both the button's @click (submits the turn,
          // re-renders the room) AND the overlay's capture-phase advance
          // listener, regardless of paint order (mirrors the agent-actions spec).
          await target.evaluate((el) => (el as HTMLElement).click());
          // The turn submits and the room view re-renders; give it room to settle.
          await dwell(page, 1400);
        }
      }
    }

    // The final mv-done step's "Done" closes the tour.
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
    const mp4 = await saveVideoAsMp4(video, ARTIFACT_DIR, "mockup-video-demo");
    // Emit the producer-agnostic chapter sidecar beside the MP4: each tour
    // step → one chapter with source_ref kind=tour.
    writeChapters(mp4, chapters.list());
    await browser.close();
  }

  const pngs = fs.readdirSync(ARTIFACT_DIR).filter((f) => f.endsWith(".png"));
  console.log(`[mockup-video-demo] screenshots (${pngs.length}) in ${ARTIFACT_DIR}`);
});
