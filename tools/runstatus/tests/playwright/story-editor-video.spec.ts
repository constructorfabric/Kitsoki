/**
 * Story Editor View feature-spotlight video demo.
 *
 * Drives the dedicated story-editor tour against a real `kitsoki web` server in
 * the deterministic no-LLM posture (--flow stories/prd/flows/happy_path.yaml) and
 * records a video + per-scene screenshots to .artifacts/story-editor/.
 *
 * Like agent-actions-video.spec.ts, this spec runs ONLY the
 * STORY_EDITOR_TOUR_STEPS from src/tour/generated/story-editor.ts via
 * window.__startTourWithSteps. The tour drives the whole video: it opens on the
 * home story library and its route-match action step navigates home → editor, so
 * even the intro is tour-narrated rather than silent spec orchestration.
 *
 * The editor is a STATIC read surface — it needs no session and no LLM. The
 * runstatus.editor.* RPCs read the PRD story's room graph directly, so there is
 * no patch_world/submit orchestration here (unlike the agent-actions spec). The
 * only on-camera setup is selecting the 'clarifying' room (a pre-step hook)
 * before the room-detail steps spotlight its panes.
 *
 * Validate fast (no dwells):
 *   WEB_CHAT_PACE=0 pnpm exec playwright test story-editor-video --project=chromium
 * Record at watch-speed:
 *   pnpm exec playwright test story-editor-video --project=chromium
 *
 * NOTE: the harness suppresses Playwright stdout, so per-step progress and any
 * failure context is also written to .artifacts/story-editor/diagnostic.log.
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
  SETTLE_MS,
  demoAddr,
  type WebServer,
} from "./_helpers/server.js";
import { cameraContext } from "./_helpers/camera.js";
import { STORY_EDITOR_TOUR_STEPS, type TourStep } from "../../src/tour/generated/story-editor.js";

// The feature-catalog source of truth for this tour: each step becomes a chapter
// (source_ref kind=tour) whose [start,end] window is the recorded dwell.
const CHAPTER_SOURCE = "features/story-editor.yaml";

// 7749 — distinct from agent-actions (7748), tour-onboarding (7747),
// trace-features (7746), editor (7798) so parallel spec files never race.
const ADDR = demoAddr(7749);
const STORY_DIR = path.join(repoRoot, "stories", "prd");
const FLOW = path.join(STORY_DIR, "flows", "happy_path.yaml");
const PRD_APP = path.join(STORY_DIR, "app.yaml");
const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "story-editor");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "video");
const DIAG_LOG = path.join(ARTIFACT_DIR, "diagnostic.log");

let server: WebServer;

function diag(msg: string): void {
  try {
    fs.appendFileSync(DIAG_LOG, `[${new Date().toISOString()}] ${msg}\n`);
  } catch {
    /* best-effort */
  }
}

test.beforeAll(async () => {
  prepareVideoDir(VIDEO_DIR);
  fs.mkdirSync(ARTIFACT_DIR, { recursive: true });
  fs.writeFileSync(DIAG_LOG, "");
  // Single-story dir so the PRD card is the ONLY story card — its 'edit-story-btn'
  // is then unique, so the tour overlay anchors its spotlight + click-through hole
  // to the right button (the full catalogue has one edit-story-btn per card, which
  // the overlay's .first() resolution can't disambiguate).
  server = await startWebServer({ addr: ADDR, flow: FLOW, storiesDir: STORY_DIR });
});

test.afterAll(() => server?.stop());

/** Map a hash URL to the overlay's route-kind. The editor is neither "/" nor a
 * chat route, so it falls through to "any" — exactly as TourOverlay computes it. */
function routeKind(url: string): TourRouteKind {
  const hash = url.split("#")[1] ?? "/";
  const p = hash.split("?")[0];
  if (p === "/" || p === "") return "home";
  if (p.endsWith("/chat")) return "interactive";
  return "any";
}
type TourRouteKind = "home" | "interactive" | "any";

/** Resolve an action step's real target. The only action step is se-intro-open,
 * whose 'edit-story-btn' must be the PRD card's button — scope to it. */
async function resolveTarget(page: Page, step: TourStep): Promise<Locator> {
  if (step.id === "se-intro-open" || step.id === "se-intro-card") {
    return page
      .locator(`[data-testid="story-card"][data-story-path="${PRD_APP}"] [data-testid="edit-story-btn"]`)
      .first();
  }
  return page.getByTestId(step.target!).first();
}

test("story editor view feature-spotlight video", async () => {
  test.setTimeout(240000);
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
    // ── 1. Open the home story library and start the tour ON it ──────────────
    diag("navigating home");
    await cinematicGoto(page, `${server.base}/#/`, { waitForTestId: "home-view" });
    // The PRD card must be present before we spotlight its edit affordance.
    await expect(
      page.locator(`[data-testid="story-card"][data-story-path="${PRD_APP}"]`)
    ).toBeVisible({ timeout: 10000 });

    await page.evaluate((stepsJson: string) => {
      (window as unknown as { __startTourWithSteps?: (s: string) => void }).__startTourWithSteps?.(stepsJson);
    }, JSON.stringify(STORY_EDITOR_TOUR_STEPS));
    await expect(page.getByTestId("tour-overlay")).toBeVisible({ timeout: 8000 });

    // ── 2. Walk the STORY_EDITOR_TOUR_STEPS ──────────────────────────────────
    for (const step of STORY_EDITOR_TOUR_STEPS) {
      diag(`step ${step.id}`);
      const currentRouteKind = routeKind(page.url());
      if (step.route !== "any" && step.route !== currentRouteKind) {
        diag(`  route-skip (${currentRouteKind})`);
        continue;
      }

      // Pre-step hook: select 'clarifying' before the first room-detail step so
      // the hook / domain-model / view / workbench panes are present + composed.
      if (step.id === "se-detail") {
        const clarifying = page.locator('[data-testid="editor-room-item"][data-room-id="clarifying"]');
        await expect(clarifying).toBeVisible({ timeout: 8000 });
        // DOM-dispatch the click so the tour backdrop can't intercept it (the
        // room list is not this step's spotlight target, so it sits under the
        // backdrop). A hit-test click would be swallowed and leave idle selected.
        await clarifying.evaluate((el) => (el as HTMLElement).click());
        // Hard signal the right room loaded: the detail header must show the
        // clarifying room's label, not idle's.
        await expect(page.getByTestId("editor-room-detail")).toContainText("Surface the gaps", {
          timeout: 10000,
        });
        await dwell(page, SETTLE_MS);
      }

      if (step.waitForTarget) {
        await expect(page.getByTestId(step.waitForTarget).first()).toBeVisible({ timeout: 15000 });
      }

      // Anti-drift: the popover must show THIS step's title (skip if the overlay
      // already advanced past it).
      const titleEl = page.getByTestId("tour-title");
      const actualTitle = (await titleEl.textContent({ timeout: 8000 }).catch(() => "")) ?? "";
      if (actualTitle !== step.title) {
        const remaining = STORY_EDITOR_TOUR_STEPS.slice(STORY_EDITOR_TOUR_STEPS.indexOf(step) + 1);
        if (remaining.some((s) => s.title === actualTitle)) {
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
        // The only action step: se-intro-open (home → editor via route-match).
        const target = await resolveTarget(page, step);
        await target.scrollIntoViewIfNeeded().catch(() => undefined);
        // Dispatch the DOM click directly so popover placement cannot affect the
        // action; this fires the anchor's navigation AND the overlay's
        // capture-phase advance listener regardless of paint timing.
        await target.evaluate((el) => (el as HTMLElement).click());
        await page.waitForTimeout(300);
        await page.waitForURL(/#\/editor/, { timeout: 15000 });
        // Editor surface must hydrate before the next step spotlights it.
        await expect(page.getByTestId("editor-room-list")).toBeVisible({ timeout: 15000 });
        await dwell(page, 1000);
      }
    }

    await expect(page.getByTestId("tour-overlay")).toHaveCount(0, { timeout: 5000 });
  } catch (e) {
    diag(`FAILED: ${e instanceof Error ? e.stack ?? e.message : String(e)}`);
    diag(`--- server log ---\n${server?.log?.() ?? ""}`);
    throw e;
  } finally {
    await context.close();
    const mp4 = await saveVideoAsMp4(video, ARTIFACT_DIR, "story-editor-demo");
    // Emit the producer-agnostic chapter sidecar beside the MP4: each tour
    // step → one chapter with source_ref kind=tour.
    writeChapters(mp4, chapters.list());
    await browser.close();
  }

  const pngs = fs.readdirSync(ARTIFACT_DIR).filter((f) => f.endsWith(".png"));
  console.log(`[story-editor-video] screenshots (${pngs.length}) in ${ARTIFACT_DIR}`);
});
