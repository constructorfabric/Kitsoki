/**
 * trace-introspection-demo.spec.ts
 *
 * TOUR-DRIVEN walkthrough video of the trace-introspection surfaces, recorded
 * against the bugfix trace SNAPSHOT fixture (static/offline — no live server).
 *
 * Like the golden agent-actions / diagram-showcase specs, the whole run is
 * narrated by TRACE_INTROSPECTION_TOUR_STEPS via window.__startTourWithSteps:
 * we stage the frozen trace with loadBugfix(), inject the steps, assert the
 * tour overlay, then walk each step — asserting the popover title against the
 * manifest (anti-drift) so the manifest and video cannot silently diverge.
 *
 * Because this is a snapshot artifact (route kind "any" everywhere), there is
 * NO home intro and NO route-match navigation. Surfaces that need opening
 * before their spotlight (the decide row's detail body, the waterfall / graph
 * view-mode tabs) are opened by pre-step hooks. We never page.reload() after
 * injecting — that would tear down the in-memory tour overlay.
 *
 * OMITTED vs the old scripted walk (testids absent in the static snapshot):
 *   - Home triage table (no live home view in the snapshot artifact).
 *   - Annotation (AnnotateButton is `v-if="isLive"` — off a live server).
 *
 * Features demonstrated:
 *  1. Observation kinds — colored category filter chips + obs-dots.
 *  2. Decision-first detail — verdict, confidence bar, evidence drawer, replay.
 *  3. View modes — tree (default) / waterfall / state graph.
 *
 * Output:
 *   .artifacts/trace-introspection-demo/  (screenshots + video)
 *
 * Run:
 *   WEB_CHAT_PACE=0 npx playwright test trace-introspection-demo --project=chromium
 */

import { test, expect, chromium, type Browser, type BrowserContext, type Page } from "@playwright/test";
import path from "path";
import fs from "fs";
import { fileURLToPath } from "url";
import { buildArtifact } from "./_helpers/artifact.js";
import { saveVideoAsMp4, ChapterRecorder, writeChapters, maybeInstallAutoRrwebCapture } from "./_helpers/server.js";
import { cameraContext } from "./_helpers/camera.js";
import { captureDiagnostics } from "./_helpers/demo.js";
import { execSync } from "child_process";
import { TRACE_INTROSPECTION_TOUR_STEPS, type TourStep } from "../../src/tour/generated/trace-introspection.js";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

const FIXTURES_DIR = path.resolve(__dirname, "../../fixtures");
const BUGFIX_SNAPSHOT = path.join(FIXTURES_DIR, "bugfix.snapshot.json");

// Derive repo root (same logic as artifact.ts)
const projectRoot = path.resolve(__dirname, "../../..");
const repoRoot = execSync("git rev-parse --show-toplevel", { cwd: projectRoot, encoding: "utf-8" }).trim();
const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "trace-introspection-demo");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "video");

// The feature-catalog source of truth for this spec's tour steps: each step
// becomes a chapter (source_ref kind=tour) whose [start,end] window is the
// recorded dwell.
const CHAPTER_SOURCE = "features/trace-introspection.yaml";

// Diagnostics breadcrumb sink — set by the test from captureDiagnostics so each
// shot() records where the run was if it later throws (the harness suppresses
// Playwright stdout, so ERROR.txt is the only failure trail).
let markScene: (label: string) => void = () => {};

/** Take a labeled screenshot into ARTIFACT_DIR. */
async function shot(page: Page, label: string): Promise<void> {
  markScene(label);
  const file = path.join(ARTIFACT_DIR, `${label}.png`);
  await page.screenshot({ path: file, fullPage: false });
  console.log(`[demo] screenshot: ${file}`);
}

/** Load the bugfix snapshot artifact and wait for the trace view to be ready. */
async function loadBugfix(page: Page): Promise<void> {
  const url = buildArtifact(BUGFIX_SNAPSHOT);
  await page.goto(url);
  await page.waitForSelector(".run-view__topbar", { timeout: 15000 });
  await page.waitForSelector(".trace-timeline__row", { timeout: 10000 });
  await maybeInstallAutoRrwebCapture(page);
}

/** Expand the first decide agent call's detail body so DecideDetail
 *  (verdict / confidence / evidence / replay) is on screen for its spotlights. */
async function openDecideDetail(page: Page): Promise<void> {
  const decideRow = page
    .locator(".trace-timeline__row", {
      has: page.locator(".trace-timeline__msg").filter({ hasText: /decide/ }),
    })
    .first();
  await expect(decideRow).toBeVisible({ timeout: 8000 });
  await decideRow.scrollIntoViewIfNeeded();
  // The expand (+) button renders the row-body containing EventDetail →
  // AgentDetail → DecideDetail.
  await decideRow.locator(".trace-timeline__expand-btn").click();
  await expect(decideRow.locator(".trace-timeline__row-body")).toBeVisible({ timeout: 5000 });
  await expect(page.getByTestId("decide-verdict").first()).toBeVisible({ timeout: 5000 });
}

/** Click a view-mode tab and wait for its surface to mount. */
async function viewModeTab(page: Page, tab: string, waitFor: string): Promise<void> {
  await page.getByTestId(tab).click();
  await page.waitForSelector(`[data-testid="${waitFor}"]`, { timeout: 8000 });
  await page.waitForTimeout(700);
}

// Pre-step hooks: open the surface a step's spotlighted testid lives on before
// the overlay tries to anchor it.
async function preStep(page: Page, step: TourStep): Promise<void> {
  switch (step.id) {
    case "ti-decide-verdict":
      await openDecideDetail(page);
      break;
    case "ti-waterfall":
      await viewModeTab(page, "tab-timeline", "waterfall-bar");
      break;
    case "ti-graph":
      await viewModeTab(page, "tab-graph", "diagram-tabs");
      break;
    default:
      break;
  }
}

test("trace-introspection tour walkthrough (bugfix snapshot, no-LLM)", async () => {
  test.setTimeout(300_000);

  fs.mkdirSync(VIDEO_DIR, { recursive: true });
  // Clear stale PNGs from prior runs so the labeled-frame dir only holds THIS
  // run's `ti-*` scene shots — otherwise QA (--frames) reviews old captures too.
  fs.mkdirSync(ARTIFACT_DIR, { recursive: true });
  for (const f of fs.readdirSync(ARTIFACT_DIR)) {
    if (f.endsWith(".png")) fs.rmSync(path.join(ARTIFACT_DIR, f));
  }

  const browser: Browser = await chromium.launch({ headless: true, slowMo: 100 });
  const context: BrowserContext = await browser.newContext(
    cameraContext({ recordVideoDir: VIDEO_DIR }),
  );
  const page: Page = await context.newPage();
  const video = page.video(); // capture BEFORE context.close()
  // Accumulate per-step time windows for the chapter sidecar. The clock starts
  // now so windows line up with the recorded MP4 timeline.
  const chapters = new ChapterRecorder();
  const { mark, onThrow } = captureDiagnostics(page, ARTIFACT_DIR);
  markScene = mark;

  try {
    // ── Stage the frozen trace, then start the tour ON it ────────────────────
    await loadBugfix(page);
    await page.waitForTimeout(1000);

    await page.evaluate((stepsJson: string) => {
      (window as unknown as { __startTourWithSteps?: (s: string) => void })
        .__startTourWithSteps?.(stepsJson);
    }, JSON.stringify(TRACE_INTROSPECTION_TOUR_STEPS));
    await expect(page.getByTestId("tour-overlay")).toBeVisible({ timeout: 8000 });

    // ── Walk the steps (all route "any" on the snapshot artifact) ────────────
    for (const step of TRACE_INTROSPECTION_TOUR_STEPS) {
      mark(`step:${step.id}`);

      // Open the surface this step spotlights, if it needs opening.
      await preStep(page, step);

      // Honor DOM-presence preconditions.
      if (step.waitForTarget) {
        await expect(page.getByTestId(step.waitForTarget).first()).toBeVisible({ timeout: 15000 });
      }

      // Anti-drift: the popover must show THIS step's title.
      await expect(page.getByTestId("tour-title")).toHaveText(step.title, { timeout: 12000 });

      // This step's spotlight is settled and on-screen — open its chapter
      // (auto-closes the prior one) so the dwell below becomes its window.
      chapters.open(step.id, step.title, CHAPTER_SOURCE);

      await page.waitForTimeout(step.dwellMs ?? 3000);
      await shot(page, step.id);

      if (step.kind === "explain") {
        await page.getByTestId("tour-next").click();
        await page.waitForTimeout(700);
      } else {
        // click-target control: dispatch the click on the real element; the
        // click is itself the advance signal.
        const target = page.getByTestId(step.target!).first();
        await target.scrollIntoViewIfNeeded().catch(() => undefined);
        await target.evaluate((el) => (el as HTMLElement).click());
        await page.waitForTimeout(1000);
      }
    }

    // The final ti-done step's "Done" closes the tour.
    await expect(page.getByTestId("tour-overlay")).toHaveCount(0, { timeout: 5000 });
  } catch (err) {
    onThrow(err);
    throw err;
  } finally {
    await context.close(); // finalises the recording
    // Transcode to a universally-playable MP4 — never ship the raw webm.
    const mp4 = await saveVideoAsMp4(video, ARTIFACT_DIR, "trace-introspection-demo");
    // Emit the producer-agnostic chapter sidecar beside the MP4: each tour
    // step → one chapter with source_ref kind=tour.
    writeChapters(mp4, chapters.list());
    await browser.close();
  }

  const pngs = fs.readdirSync(ARTIFACT_DIR).filter((f) => f.endsWith(".png"));
  console.log(`[demo] screenshots (${pngs.length}) in ${ARTIFACT_DIR}`);
});
