/**
 * Operator-ask forwarding feature-spotlight video demo.
 *
 * Drives the dedicated operator-ask tour against a real `kitsoki web` server in
 * the deterministic no-LLM posture (--flow + the demo cassette) and records a
 * video + per-scene screenshots to .artifacts/operator-ask/.
 *
 * Like agent-actions-video.spec.ts, this spec runs ONLY the
 * OPERATOR_ASK_TOUR_STEPS from src/tour/generated/operator-ask.ts via
 * window.__startTourWithSteps. The tour drives the whole video: it opens on the
 * home story library and its route-match action step navigates home → new
 * session → the interactive session view, so even the intro is tour-narrated.
 *
 * The hard part — surfacing the modal with no LLM: the operator-question modal
 * only renders when the backend pushes a /rpc/questions SSE frame, which needs a
 * real dispatched agent. We can't do that no-LLM, so the spec uses the
 * window.__pushOperatorQuestion demo seam (registered in
 * OperatorQuestionModal.vue onMounted) to inject a realistic OperatorQuestionFrame
 * straight into the store's onFrame(). The frame's "demo-" question_id triggers
 * the store's answer() local-resolve short-circuit, so "Send answer" dismisses
 * the modal without a 404 backend round-trip. The REAL modal rendering + option
 * selection UX is unchanged — only the network round-trip is bypassed.
 *
 * Validate fast (no dwells):
 *   WEB_CHAT_PACE=0 pnpm exec playwright test operator-ask-video --project=chromium
 * Record at watch-speed:
 *   pnpm exec playwright test operator-ask-video --project=chromium
 *
 * NOTE: the harness suppresses Playwright stdout, so per-step progress and any
 * failure context is also written to .artifacts/operator-ask/diagnostic.log.
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
import { OPERATOR_ASK_TOUR_STEPS, type TourStep } from "../../src/tour/generated/operator-ask.js";

// The feature-catalog source of truth for this tour: each step becomes a chapter
// (source_ref kind=tour) whose [start,end] window is the recorded dwell.
const CHAPTER_SOURCE = "features/operator-ask.yaml";

// 7749 — distinct from agent-actions (7748), trace-features (7746) and
// tour-onboarding (7747) so parallel spec files never race on the same port.
const ADDR = demoAddr(7766);
const STORY_DIR = path.join(repoRoot, "stories", "bugfix");
const FLOW = path.join(STORY_DIR, "flows", "happy_llm.yaml");
const HOST_CASSETTE = path.join(STORY_DIR, "flows", "demo.cassette.yaml");
const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "operator-ask");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "video");
const DIAG_LOG = path.join(ARTIFACT_DIR, "diagnostic.log");

// Realistic demo frames the spec injects via window.__pushOperatorQuestion.
// Both carry a "demo-" question_id so the store's answer() short-circuit resolves
// them locally (no backend pending-registry entry exists for an injected frame).
const SINGLE_FRAME = {
  session_id: "demo-session",
  question_id: "demo-q-1",
  questions: [
    {
      question: "The fix touches the public API. How should I proceed?",
      header: "Ship",
      multiSelect: false,
      options: [
        { label: "Add a backward-compatible shim", description: "Keep the old signature working alongside the new one." },
        { label: "Break the API and bump the major version", description: "Cleaner, but downstream callers must update." },
        { label: "Stop and open a discussion first", description: "Defer the decision to a design review." },
      ],
    },
  ],
};

const MULTI_FRAME = {
  session_id: "demo-session",
  question_id: "demo-q-2",
  questions: [
    {
      question: "Which checks should I run before I ship this fix?",
      header: "Checks",
      multiSelect: true,
      options: [
        { label: "Unit tests", description: "Fast, run the package's own suite." },
        { label: "Integration tests", description: "Slower, exercises the full pipeline." },
        { label: "Lint + typecheck", description: "Static analysis only." },
        { label: "Security scan", description: "Dependency + secret audit." },
      ],
    },
  ],
};

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
  prepareVideoDir(VIDEO_DIR);
  fs.mkdirSync(ARTIFACT_DIR, { recursive: true });
  fs.writeFileSync(DIAG_LOG, "");
  server = await startWebServer({ addr: ADDR, flow: FLOW, hostCassette: HOST_CASSETTE, storiesDir: STORY_DIR });
});

test.afterAll(() => server?.stop());

/** Resolve an action step's real target element — first visible match. */
async function resolveTarget(page: Page, step: TourStep): Promise<Locator> {
  return page.getByTestId(step.target!).first();
}

/** Inject a demo operator-question frame via the deterministic demo seam. */
async function pushQuestion(page: Page, frame: unknown): Promise<void> {
  await page.evaluate((frameJson: string) => {
    (window as unknown as { __pushOperatorQuestion?: (s: string) => void })
      .__pushOperatorQuestion?.(frameJson);
  }, JSON.stringify(frame));
}

test("operator-ask forwarding feature-spotlight video", async () => {
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
    }, JSON.stringify(OPERATOR_ASK_TOUR_STEPS));
    await expect(page.getByTestId("tour-overlay")).toBeVisible({ timeout: 8000 });

    // ── 2. Walk the OPERATOR_ASK_TOUR_STEPS ──────────────────────────────────
    for (const step of OPERATOR_ASK_TOUR_STEPS) {
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

      // ── Pre-step setup ──────────────────────────────────────────────────
      // Inject the single-select question just before the first modal step so
      // the modal renders the agent's forwarded question on a composed frame.
      if (step.id === "oa-modal") {
        await pushQuestion(page, SINGLE_FRAME);
        await dwell(page, SETTLE_MS);
      }
      // After the single-select round-trip dismisses the modal, inject the
      // multi-select question just before its narration step.
      if (step.id === "oa-multi") {
        await pushQuestion(page, MULTI_FRAME);
        await dwell(page, SETTLE_MS);
      }
      // Before sending the multi-select answer, also check a second option so
      // the captured frame visibly shows the multi-select path (>1 checkbox lit)
      // rather than a single pick that could read like a radio.
      if (step.id === "oa-multi-send") {
        await page.getByTestId("oq-option-0-2").click().catch(() => undefined);
        await dwell(page, 600);
      }

      // Honor DOM-presence preconditions.
      if (step.waitForTarget) {
        await expect(page.getByTestId(step.waitForTarget).first()).toBeVisible({ timeout: 15000 });
      }

      // Anti-drift assertion: the popover must show THIS step's title.
      const titleEl = page.getByTestId("tour-title");
      const actualTitle = await titleEl.textContent({ timeout: 8000 }).catch(() => "");
      if (actualTitle !== step.title) {
        const remaining = OPERATOR_ASK_TOUR_STEPS.slice(OPERATOR_ASK_TOUR_STEPS.indexOf(step) + 1);
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
          // Intro navigation (New session). Click through the overlay's hole and
          // wait for the URL to change before the next iteration asserts.
          await target.click();
          await page.waitForTimeout(300);
          if (step.advanceRoute === "interactive") {
            await page.waitForURL(/#\/s\/[0-9a-f-]{36}\/chat$/, { timeout: 15000 });
            diag(`reached interactive ${page.url()}`);
          }
          await dwell(page, 1000);
        } else {
          // click-target modal control (an option button or Send answer).
          // Dispatch the DOM click directly: it fires the control's own @click
          // AND the overlay's capture-phase advance listener bound on the same
          // element, so the tour advances regardless of overlay paint order.
          await target.evaluate((el) => (el as HTMLElement).click());
          await dwell(page, 1000);
        }
      }
    }

    // The final oa-done step's "Done" closes the tour.
    await expect(page.getByTestId("tour-overlay")).toHaveCount(0, { timeout: 5000 });
  } catch (e) {
    diag(`FAILED: ${e instanceof Error ? e.stack ?? e.message : String(e)}`);
    diag(`--- server log ---\n${server?.log?.() ?? ""}`);
    throw e;
  } finally {
    await context.close();
    const mp4 = await saveVideoAsMp4(video, ARTIFACT_DIR, "operator-ask-demo");
    // Emit the producer-agnostic chapter sidecar beside the MP4: each tour
    // step → one chapter with source_ref kind=tour.
    writeChapters(mp4, chapters.list());
    await browser.close();
  }

  const pngs = fs.readdirSync(ARTIFACT_DIR).filter((f) => f.endsWith(".png"));
  console.log(`[operator-ask-video] screenshots (${pngs.length}) in ${ARTIFACT_DIR}`);
});
