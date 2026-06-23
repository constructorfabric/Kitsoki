/**
 * Design pipeline walkthrough video — tamagotchi-pets edition.
 *
 * Records the full design user flow from idea entry through to a published
 * design doc, using the tamagotchi-pets idea as realistic content. Designed for
 * UI review: every input, button, and room view is shown on-camera with
 * narration so a reviewer can judge whether the UI is clear and not confusing.
 *
 * Like agent-actions-video.spec.ts, this spec is TOUR-DRIVEN: it runs ONLY the
 * DESIGN_WALKTHROUGH_TOUR_STEPS from
 * src/tour/generated/design-walkthrough.ts via window.__startTourWithSteps.
 * The tour opens on the home story library and its route-match action step
 * navigates home → new session → the interactive /chat view, so even the intro
 * is tour-narrated rather than silent spec orchestration.
 *
 * The pipeline-advancing interactions (submit the idea, click confirm / ready /
 * advance_brief / accept) are NOT tour steps — they run as PRE-STEP HOOKS
 * (exactly as the agent-actions spec opens drawers in pre-step hooks) so each
 * spotlighted surface exists before the spotlight lands on it.
 *
 * No LLM: uses  stubs.
 *
 * Record:  pnpm exec playwright test design-walkthrough --project=chromium
 * Fast:    WEB_CHAT_PACE=0 pnpm exec playwright test design-walkthrough --project=chromium
 */
import { test, expect, chromium, type Browser, type BrowserContext, type Page, type Locator } from "@playwright/test";
import path from "path";
import {
  startWebServer,
  repoRoot,
  makeShot,
  waitForState,
  prepareVideoDir,
  saveVideoAsMp4,
  ChapterRecorder,
  writeChapters,
  demoAddr,
  dwell,
  cinematicGoto,
  SETTLE_MS,
  type WebServer,
} from "./_helpers/server.js";
import { cameraContext } from "./_helpers/camera.js";
import { captureDiagnostics } from "./_helpers/demo.js";
import { DESIGN_WALKTHROUGH_TOUR_STEPS, type TourStep } from "../../src/tour/generated/design-walkthrough.js";

// The feature-catalog source of truth for this spec's tour steps: each step
// becomes a chapter (source_ref kind=tour) in the MP4's sidecar.
const CHAPTER_SOURCE = "features/design-walkthrough.yaml";

// Port distinct from all other specs.
const ADDR = demoAddr(7757);
const STORY_DIR = path.join(repoRoot, "stories", "dev-story");
const FLOW = path.join(STORY_DIR, "flows", "design_tamagotchi_demo.yaml");
const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "design-walkthrough");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "video");

let server: WebServer;

async function textInput(page: Page) {
  return page.getByTestId("composer-input").or(page.getByTestId("text-floor-input")).first();
}

async function sendButton(page: Page) {
  return page.getByTestId("composer-send").or(page.getByTestId("text-floor-send")).first();
}
test.beforeAll(async () => {
  prepareVideoDir(VIDEO_DIR);
  server = await startWebServer({ addr: ADDR, flow: FLOW, storiesDir: STORY_DIR });
});
test.afterAll(() => server?.stop());

/** Resolve an action step's real target element — first visible match. */
async function resolveTarget(page: Page, step: TourStep): Promise<Locator> {
  return page.getByTestId(step.target!).first();
}

test("design pipeline walkthrough — tamagotchi pets", async () => {
  test.setTimeout(300000);
  const browser: Browser = await chromium.launch({ headless: true });
  const context: BrowserContext = await browser.newContext(
    cameraContext({ recordVideoDir: VIDEO_DIR }),
  );
  const page: Page = await context.newPage();
  const video = page.video();
  const shot = makeShot(ARTIFACT_DIR);
  const { mark, onThrow } = captureDiagnostics(page, ARTIFACT_DIR);

  // Accumulate per-step time windows for the chapter sidecar. The clock starts
  // now so windows line up with the recorded MP4 timeline.
  const chapters = new ChapterRecorder();

  // Captured once the intro's "New session" step creates the run.
  let sid = "";
  const submit = (intent: string, slots: Record<string, unknown> = {}) =>
    server.rpc("runstatus.session.submit", { session_id: sid, intent, slots });

  try {
    // ── 1. Open the home story library and start the tour ON it ──────────────
    // The whole video is tour-driven: rather than silently flashing home -> chat
    // before the overlay appears, we start the tour on home and let its
    // route-match action step perform the navigation, narrated.
    mark("navigating home");
    await cinematicGoto(page, `${server.base}/#/`, { waitForTestId: "home-view" });

    await page.evaluate((stepsJson: string) => {
      (window as unknown as { __startTourWithSteps?: (s: string) => void })
        .__startTourWithSteps?.(stepsJson);
    }, JSON.stringify(DESIGN_WALKTHROUGH_TOUR_STEPS));
    await expect(page.getByTestId("tour-overlay")).toBeVisible({ timeout: 8000 });

    // ── 2. Walk the DESIGN_WALKTHROUGH_TOUR_STEPS ────────────────────────────
    for (const step of DESIGN_WALKTHROUGH_TOUR_STEPS) {
      mark(`step ${step.id}`);
      // Mirror the overlay's route-guard.
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

      // ── Pre-step setup: advance the pipeline so this step's surface exists ──
      // Each hook submits an intent (or types the idea) and waits for the next
      // room, mirroring how the golden opens drawers before drawer steps.
      if (step.id === "pw-intake") {
        // The flow starts directly in design intake. Older versions booted via
        // main/landing and needed go_idea; keep this tolerant while the catalog
        // remains reusable against both shapes.
        const cur = (await page.getByTestId("current-state").textContent())?.trim() ?? "";
        if (cur === "landing" || cur === "main") {
          await submit("go_idea", { message: "" });
        }
        await waitForState(page, "design", 12000);
        await dwell(page, SETTLE_MS);
      }
      if (step.id === "pw-idea-input") {
        // Type the idea on-camera into the composer (the spotlighted surface).
        const composer = await textInput(page);
        await expect(composer).toBeVisible({ timeout: 15000 });
        await composer.click();
        await composer.fill("I want to add tamagotchi-style virtual pets to the session UI");
        await dwell(page, SETTLE_MS);
      }
      if (step.id === "pw-search") {
        // Submit the idea → scout search completes → design_search room.
        const send = await sendButton(page);
        await expect(send).toBeVisible({ timeout: 15000 });
        await send.click();
        await waitForState(page, "design_search", 20000);
        await dwell(page, SETTLE_MS);
      }
      if (step.id === "pw-refine") {
        // Confirm (no overlap) → mint workspace + scaffold brief.
        await page.getByTestId("intent-btn-confirm").first().click();
        await waitForState(page, "design_refine", 20000);
        await dwell(page, SETTLE_MS);
      }
      if (step.id === "pw-judge") {
        // Press ready → brief judge fires (verdict: continue) → advance_brief
        // appears as a choice item for the operator.
        await page.getByTestId("intent-btn-ready").first().click();
        await waitForState(page, "design_refine", 15000);
        await expect(page.getByTestId("intent-btn-advance_brief").first()).toBeVisible({ timeout: 15000 });
        await dwell(page, SETTLE_MS);
      }
      if (step.id === "pw-draft") {
        // Advance to draft → draft author writes the design document.
        await page.getByTestId("intent-btn-advance_brief").first().click();
        await waitForState(page, "design_draft", 20000);
        await dwell(page, SETTLE_MS);
      }
      if (step.id === "pw-done") {
        // Accept → publish script moves the draft into docs/proposals/. Reload
        // for the same reliability reason the original spec used.
        await submit("accept", {});
        await waitForState(page, "design_done", 20000);
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
        const remaining = DESIGN_WALKTHROUGH_TOUR_STEPS.slice(
          DESIGN_WALKTHROUGH_TOUR_STEPS.indexOf(step) + 1
        );
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

      await dwell(page, step.dwellMs ?? 3000);
      await shot(page, step.id);

      if (step.kind === "explain") {
        await page.getByTestId("tour-next").click();
        await dwell(page, 700);
      } else {
        const target = await resolveTarget(page, step);
        await target.scrollIntoViewIfNeeded().catch(() => undefined);
        if (step.advance === "route-match") {
          // Intro navigation (New session). The chat view is static at this
          // point, so a hit-test click goes cleanly through the overlay hole.
          await target.click();
          await page.waitForTimeout(300);
          if (step.advanceRoute === "interactive") {
            await page.waitForURL(/#\/s\/[0-9a-f-]{36}\/chat$/, { timeout: 15000 });
            const m = page.url().match(/\/s\/([0-9a-f-]{36})\/chat$/);
            if (m) {
              sid = m[1];
              mark(`session ${sid}`);
            }
          } else if (step.advanceRoute === "any") {
            await page.waitForURL(/#\/s\/[0-9a-f-]{36}$/, { timeout: 15000 });
          }
          await dwell(page, 1000);
        } else {
          // click-target: dispatch the DOM click directly so it fires both the
          // control's @click AND the overlay's capture-phase advance listener.
          await target.evaluate((el) => (el as HTMLElement).click());
          await dwell(page, 1000);
        }
      }
    }

    // The final step's "Done" closes the tour.
    await expect(page.getByTestId("tour-overlay")).toHaveCount(0, { timeout: 5000 });
  } catch (err) {
    onThrow(err);
    throw err;
  } finally {
    await context.close();
    const mp4 = await saveVideoAsMp4(video, ARTIFACT_DIR, "design-walkthrough");
    // Emit the producer-agnostic chapter sidecar beside the MP4: each tour
    // step → one chapter with source_ref kind=tour.
    writeChapters(mp4, chapters.list());
    await browser.close();
  }
});
