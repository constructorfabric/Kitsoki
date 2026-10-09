/**
 * devstory "bug triage → handoff into the autonomous bugfix pipeline"
 * feature-tour video demo — STAGE 1.
 *
 * Drives the devstory tour against a real `kitsoki web` server in the
 * deterministic no-LLM posture (--flow tour_triage_to_bugfix.yaml supplies the
 * initial_world; --host-cassette triage-handoff.yaml backs every host.run along
 * the path) and records a video + per-scene screenshots to
 * .artifacts/devstory-triage/.
 *
 * Runs ONLY the DEVSTORY_TRIAGE_TOUR_STEPS from
 * _fixtures/devstory-triage-tour-steps.ts via window.__startTourWithSteps. The
 * tour drives the whole video: it opens on the home story library, its
 * route-match action step navigates home → new session → the drive view, then
 * the explain beats narrate the triage + handoff while the spec drives the
 * matching intents between beats.
 *
 * NOTE: devstory-triage was de-listed from features/*.yaml (an external stub,
 * never recordable in this repo — see features/AGENTS.md's completeness
 * rules), so this manifest is no longer code-generated; it's a frozen copy
 * kept alongside this spec (see _fixtures/devstory-triage-tour-steps.ts).
 *
 * Driving mechanics:
 *   - Slotless intents (go_triage, triage__scan, triage__accept,
 *     bf__start_pipeline) are clicked on-camera via intent-btn-<name>; the
 *     resulting current-state is the hard signal the turn landed.
 *   - Slotted intents (triage__configure_jql, triage__select_ticket) are driven
 *     via the composer (composer-select → composer-input → composer form submit).
 *
 * STAGE 1 STOPS once the pipeline is kicked off (bf.phase_minus_1_executing).
 *
 * Validate fast (no dwells):
 *   WEB_CHAT_PACE=0 pnpm exec playwright test devstory-triage-video --project=chromium
 * Record at watch-speed:
 *   pnpm exec playwright test devstory-triage-video --project=chromium
 *
 * The harness suppresses Playwright stdout, so per-step progress and any failure
 * context is also written to .artifacts/devstory-triage/ERROR.txt.
 */
import { test, expect, chromium, type Browser, type BrowserContext, type Page } from "@playwright/test";
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
  demoAddr,
  dwell,
  cinematicGoto,
  type WebServer,
} from "./_helpers/server.js";
import { cameraContext } from "./_helpers/camera.js";
import { DEVSTORY_TRIAGE_TOUR_STEPS } from "./_fixtures/devstory-triage-tour-steps.js";

// The feature-catalog source of truth for this spec's tour steps: each step
// becomes a chapter (source_ref kind=tour) in the MP4's sidecar.
// Was "features/devstory-triage.yaml" before the feature was de-listed from
// the catalog; the frozen fixture is now the source of truth for these steps.
const CHAPTER_SOURCE = "tools/runstatus/tests/playwright/_fixtures/devstory-triage-tour-steps.ts";

// 7762 — the stage-1 port (matches the cassette-validation server). Distinct
// from every other spec's port so parallel runs never race on the same bind.
const ADDR = demoAddr(7762);
// The devstory app lives in an external worktree, not this kitsoki repo.
const EXTERNAL_STORIES = process.env.DEVSTORY_TRIAGE_STORIES ?? "/path/to/external-devstory/stories";
const STORY_DIR = path.join(EXTERNAL_STORIES, "devstory");
const FLOW = path.join(STORY_DIR, "flows", "tour_triage_to_bugfix.yaml");
const HOST_CASSETTE = path.join(STORY_DIR, "cassettes", "triage-handoff.yaml");
const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "devstory-triage");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "video");
const ERROR_TXT = path.join(ARTIFACT_DIR, "ERROR.txt");

const JQL = "project = ABR AND issuetype = Bug AND resolution = Unresolved";

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
  server = await startWebServer({
    addr: ADDR,
    flow: FLOW,
    storiesDir: STORY_DIR,
    hostCassette: HOST_CASSETTE,
  });
});

test.afterAll(() => server?.stop());

async function expectState(page: Page, state: string): Promise<void> {
  await expect(page.getByTestId("current-state")).toHaveText(state, { timeout: 15000 });
}

/**
 * Drive a slotless intent by clicking its on-camera button, then assert the
 * resulting state. Dispatch the DOM click directly: the tour overlay paints a
 * backdrop while its popover narrates the control, so a hit-test click can be
 * intercepted — the element's own @click still fires the turn.
 */
async function driveButton(page: Page, intent: string, expectStateName: string): Promise<void> {
  diag(`driveButton ${intent} → ${expectStateName}`);
  const btn = page.getByTestId(`intent-btn-${intent}`).first();
  await expect(btn).toBeVisible({ timeout: 15000 });
  await dwell(page, 500);
  await btn.evaluate((el) => (el as HTMLElement).click());
  await expectState(page, expectStateName);
  await dwell(page, 600);
}

/**
 * Drive a slotted (text-slot) intent through the legacy composer: an optional
 * composer-select (present only when >1 text intent), a composer-input textarea,
 * and the composer form. DOM-level so the tour popover never intercepts.
 */
async function driveComposer(
  page: Page,
  intent: string,
  value: string,
  expectStateName: string,
): Promise<void> {
  diag(`driveComposer ${intent}="${value}" → ${expectStateName}`);
  const select = page.getByTestId("composer-select");
  if ((await select.count()) > 0) {
    await select
      .evaluate((el, v) => {
        const sel = el as HTMLSelectElement;
        sel.value = v;
        sel.dispatchEvent(new Event("change", { bubbles: true }));
      }, intent)
      .catch(() => undefined);
    await dwell(page, 300);
  }
  const input = page.getByTestId("composer-input").first();
  await expect(input).toBeVisible({ timeout: 15000 });
  await input.evaluate((el, v) => {
    const ta = el as HTMLTextAreaElement;
    ta.value = v;
    ta.dispatchEvent(new Event("input", { bubbles: true }));
  }, value);
  await dwell(page, 600);
  const form = page.getByTestId("composer").first();
  await form.evaluate((el) => (el as HTMLFormElement).requestSubmit());
  await expectState(page, expectStateName);
  await dwell(page, 600);
}

/**
 * Drive the turn associated with a given manifest step (performed AFTER its
 * narration screenshot, BEFORE advancing to the next step). Steps with no entry
 * are pure narration. Each entry asserts the resulting current-state.
 */
async function driveForStep(page: Page, stepId: string): Promise<void> {
  switch (stepId) {
    case "ds-triage-open":
      await driveButton(page, "go_triage", "triage.queue");
      break;
    case "ds-triage-jql":
      await driveComposer(page, "triage__configure_jql", JQL, "triage.queue");
      break;
    case "ds-triage-survey":
      await driveButton(page, "triage__scan", "triage.results");
      break;
    case "ds-triage-card":
      await driveComposer(page, "triage__select_ticket", "ABR-429271", "triage.ticket");
      break;
    case "ds-handoff":
      await driveButton(page, "triage__accept", "bf.ticket_setup");
      break;
    case "ds-pipeline-kickoff":
      await driveButton(page, "bf__start_pipeline", "bf.phase_minus_1_executing");
      break;
    default:
      break;
  }
}

test("devstory triage → bugfix handoff feature-tour video", async () => {
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
    // ── 1. Open the home story library and start the tour ON it ──────────────
    diag("navigating home");
    await cinematicGoto(page, `${server.base}/#/`, { waitForTestId: "home-view" });

    await page.evaluate((stepsJson: string) => {
      (window as unknown as { __startTourWithSteps?: (s: string) => void })
        .__startTourWithSteps?.(stepsJson);
    }, JSON.stringify(DEVSTORY_TRIAGE_TOUR_STEPS));
    await expect(page.getByTestId("tour-overlay")).toBeVisible({ timeout: 8000 });

    // ── 2. Walk DEVSTORY_TRIAGE_TOUR_STEPS ───────────────────────────────────
    for (const step of DEVSTORY_TRIAGE_TOUR_STEPS) {
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

      // The terminal narration beat (ds-done) lands AFTER the pipeline has been
      // kicked off; the background context-extraction job then churns the run
      // forward (phase_minus_1 → phase_0), remounting the InteractiveView and
      // closing the overlay. Stage-1's hard stop is "pipeline kicked off", so
      // this beat is tolerant: it screenshots whatever the final pipeline view
      // shows and does not require the overlay to have survived.
      const isTerminal = step.id === "ds-done";

      if (step.waitForTarget && !isTerminal) {
        await expect(page.getByTestId(step.waitForTarget).first()).toBeVisible({ timeout: 15000 });
      }

      if (!isTerminal) {
        // Re-sync the overlay to THIS step if its internal anchoring drifted ahead.
        const titleEl = page.getByTestId("tour-title");
        const actualTitle = (await titleEl.textContent({ timeout: 8000 }).catch(() => "")) ?? "";
        if (actualTitle !== step.title) {
          diag(`  re-sync overlay from "${actualTitle}" → "${step.title}"`);
          await page.evaluate((id: string) => {
            (window as unknown as { __tourGoTo?: (s: string) => void }).__tourGoTo?.(id);
          }, step.id);
        }
        await expect(titleEl).toHaveText(step.title, { timeout: 12000 });
      }

      // This step is settled and on-screen (the terminal beat tolerates the
      // overlay being gone) — open its chapter (auto-closes the prior one) so
      // the dwell below becomes its window.
      chapters.open(step.id, step.title, CHAPTER_SOURCE);

      await dwell(page, step.dwellMs ?? 3000);
      await shot(page, step.id);

      if (isTerminal) {
        diag("terminal beat: stage-1 stop");
        break;
      }

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
        // explain step: drive the associated turn (if any) on-camera AFTER its
        // narration screenshot, then advance the overlay.
        await driveForStep(page, step.id);
        // Advance the overlay. After the pipeline-kickoff drive the background
        // job can remount the view and drop the overlay, so this click is
        // best-effort — the loop's terminal beat handles the post-kickoff frame.
        await page
          .getByTestId("tour-next")
          .click({ timeout: 5000 })
          .catch(() => diag("  tour-next gone (post-kickoff churn); continuing"));
        await dwell(page, 700);
      }
    }
  } catch (e) {
    diag(`FAILED: ${e instanceof Error ? e.stack ?? e.message : String(e)}`);
    diag(`--- server log ---\n${server?.log?.() ?? ""}`);
    throw e;
  } finally {
    await context.close();
    const mp4 = await saveVideoAsMp4(video, ARTIFACT_DIR, "devstory-triage-demo");
    // Emit the producer-agnostic chapter sidecar beside the MP4: each tour
    // step → one chapter with source_ref kind=tour.
    writeChapters(mp4, chapters.list());
    await browser.close();
  }

  const pngs = fs.readdirSync(ARTIFACT_DIR).filter((f) => f.endsWith(".png"));
  console.log(`[devstory-triage-video] screenshots (${pngs.length}) in ${ARTIFACT_DIR}`);
});
