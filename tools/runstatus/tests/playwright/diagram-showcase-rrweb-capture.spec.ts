/**
 * diagram-showcase-rrweb-capture.spec.ts — rrweb capture spec (complex view-dwell tour).
 *
 * The CAPTURE half of the rrweb capture→replay-render method for the COMPLEX
 * view-dwell class (rendered with renderReplayWithHolds so each diagram view
 * holds its real manifest dwell). Does NOT replace the golden live-record
 * diagram-showcase.spec.ts — that remains the fallback for any canvas/video
 * surface (recordCanvas:false; see _helpers/rrweb-replay.ts).
 *
 * Forked from diagram-showcase.spec.ts. Keeps the SAME full live drive (spawn
 * `kitsoki web --flow design_happy_path`, window.__startTourWithSteps, the
 * home → new-session → four StateDiagram views tour, off-camera RPC drive to
 * design_search, watch-speed pacing) and the SAME recordVideo baseline. The
 * ONLY additions vs the golden spec are the rrweb capture hooks:
 *
 *   - installCapture(page) is called right after the home view paints and BEFORE
 *     the first tour-driven navigation, so the rrweb full-snapshot + mutation
 *     stream covers the ENTIRE tour from the home view onward. The kitsoki SPA is
 *     hash-routed (no full document reload between routes) so a single
 *     installCapture accumulates the FULL stream across #/ → #/s/.../chat.
 *   - At the very end (after the tour, before context.close) dumpCapture(page) +
 *     writeEvents → .artifacts/rrweb-eval/diagram-showcase/diagram-showcase.rrweb.json
 *     (+ the <events>.capture.json viewport sidecar for the render's
 *     viewport-match assertion).
 *
 * The StateDiagram is ALL SVG + HTML/CSS (no canvas, no <video>) so it
 * reconstructs faithfully through an rrweb Replayer — that is precisely why
 * diagram-showcase is the COMPLEX case in this method eval.
 *
 * Artifacts (all under .artifacts/rrweb-eval/diagram-showcase/):
 *   - diagram-showcase-baseline.mp4   ← the live screen-recording (the BASELINE)
 *   - baseline-frames/NN-*.png        ← the per-step baseline screenshots
 *   - diagram-showcase.rrweb.json     ← the captured rrweb event stream
 *
 * Because the events and the baseline come from the SAME drive they correspond
 * exactly; rrweb-replay-render.spec.ts (RRWEB_TARGET=diagram-showcase) renders
 * the events to an MP4 to compare 1:1 against this baseline.
 *
 * Run at watch-speed (LONG, ~minutes; the live drive pays the full wall-clock
 * once — launch in the background and poll):
 *   pnpm exec playwright test diagram-showcase-rrweb-capture --project=chromium
 */
import { test, expect, chromium, type Browser, type BrowserContext, type Page, type Locator } from "@playwright/test";
import path from "path";
import fs from "fs";
import {
  startWebServer,
  repoRoot,
  makeShot,
  waitForState,
  prepareVideoDir,
  saveVideoAsMp4,
  ChapterRecorder,
  writeChapters,
  dwell,
  cinematicGoto,
  SETTLE_MS,
  type WebServer,
} from "./_helpers/server.js";
import { installCapture, dumpCapture, writeEvents } from "./_helpers/rrweb-replay.js";
import { DIAGRAM_SHOWCASE_TOUR_STEPS, type TourStep } from "../../src/tour/generated/diagram-showcase.js";

const CHAPTER_SOURCE = "features/diagram-showcase.yaml";

// 7754 — distinct from the golden diagram-showcase (7753) and the other specs so
// this eval fork can run alongside them without racing on the same bind.
const ADDR = "127.0.0.1:7754";
const STORY_DIR = path.join(repoRoot, "stories", "dev-story");
const FLOW = path.join(STORY_DIR, "flows", "design_happy_path.yaml");

// Eval artifacts live under .artifacts/rrweb-eval/diagram-showcase/ (NOT the
// golden .artifacts/diagram-showcase/).
const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "rrweb-eval", "diagram-showcase");
const BASELINE_FRAMES_DIR = path.join(ARTIFACT_DIR, "baseline-frames");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "_baseline-video");
const EVENTS_JSON = path.join(ARTIFACT_DIR, "diagram-showcase.rrweb.json");
const DIAG_LOG = path.join(ARTIFACT_DIR, "diagnostic.log");

// Capture viewport — the later replay-render MUST use this same size + DSF
// (rrweb-replay-render.spec.ts TARGETS["diagram-showcase"] = 1600x900, DSF 1).
const VIEWPORT = { width: 1600, height: 900 } as const;

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
  fs.mkdirSync(BASELINE_FRAMES_DIR, { recursive: true });
  fs.writeFileSync(DIAG_LOG, "");
  server = await startWebServer({ addr: ADDR, flow: FLOW, storiesDir: STORY_DIR });
});
test.afterAll(() => server?.stop());

/** Resolve an action step's real target element — first visible match. */
function resolveTarget(page: Page, step: TourStep): Locator {
  return page.getByTestId(step.target!).first();
}

/** Give the diagram the stage: shrink chat, widen the diagram panel. Injected
 *  presentation CSS only — not a render hack on the trace. */
async function stageDiagram(page: Page): Promise<void> {
  await page.addStyleTag({
    content: `
      .iv__chat { flex: 0 0 22% !important; }
      .iv__trace { flex: 1 1 78% !important; }
      .iv__panel--diagram { flex: 1 1 82% !important; }
      .iv__panel--timeline { flex: 1 1 18% !important; }
    `,
  });
}

/** Switch the StateDiagram view tab so the next step's spotlight testid is present. */
async function diagramTab(page: Page, mode: string): Promise<void> {
  diag(`tab:${mode}`);
  await page.getByTestId(`diagram-tab-${mode}`).click().catch(() => undefined);
  await dwell(page, 700);
}

// The diagram tab each route "any" step needs on screen before its spotlight
// lands (the InteractiveView renders metro by default).
const TAB_FOR_STEP: Record<string, string> = {
  "dsg-metro-overview": "metro",
  "dsg-metro-traveled": "metro",
  "dsg-metro-current": "metro",
  "dsg-metro-horizon": "metro",
  "dsg-metro-road-ahead": "metro",
  "dsg-ego": "ego",
  "dsg-path": "path",
  "dsg-full": "full",
  "dsg-done": "metro",
};

test("state-diagram four-view showcase rrweb capture (baseline + event stream)", async () => {
  test.setTimeout(600000);
  const browser: Browser = await chromium.launch({ headless: true });
  const context: BrowserContext = await browser.newContext({
    viewport: { ...VIEWPORT },
    recordVideo: { dir: VIDEO_DIR, size: { ...VIEWPORT } },
  });
  const page: Page = await context.newPage();
  const video = page.video(); // capture BEFORE context.close()
  const shot = makeShot(BASELINE_FRAMES_DIR);

  const chapters = new ChapterRecorder();
  let sid = "";

  try {
    // ── 1. Open the home story library and start the tour ON it ──────────────
    diag("navigating home");
    await cinematicGoto(page, `${server.base}/#/`, { waitForTestId: "home-view" });

    // ── rrweb: start recording AFTER the home view has painted (so the first
    // full snapshot already carries the home-view DOM) and BEFORE the first
    // tour-driven navigation. A single install accumulates the FULL stream
    // across the hash-routed tour (home → /chat). ────────────────────────────
    await installCapture(page);
    diag("rrweb capture installed");

    await page.evaluate((stepsJson: string) => {
      (window as unknown as { __startTourWithSteps?: (s: string) => void })
        .__startTourWithSteps?.(stepsJson);
    }, JSON.stringify(DIAGRAM_SHOWCASE_TOUR_STEPS));
    await expect(page.getByTestId("tour-overlay")).toBeVisible({ timeout: 8000 });

    // ── 2. Walk the DIAGRAM_SHOWCASE_TOUR_STEPS (intro + four views) ─────────
    for (const step of DIAGRAM_SHOWCASE_TOUR_STEPS) {
      diag(`step ${step.id}`);
      const currentUrl = page.url();
      const currentRouteKind = currentUrl.includes("/chat")
        ? "interactive"
        : currentUrl.endsWith("/#/") || currentUrl.endsWith("/#") || currentUrl.endsWith("/")
          ? "home"
          : "any";
      if (step.route !== "any" && step.route !== currentRouteKind) {
        diag(`  route-skip (${currentRouteKind})`);
        continue;
      }

      // ── Pre-step setup ──────────────────────────────────────────────────
      // After the intro lands on /chat, drive the design pipeline to
      // design_search off-camera so the diagram populates with a real
      // traveled leg + current station + road ahead, then stage the panel.
      if (step.id === "dsg-metro-overview") {
        await waitForState(page, "main", 15000);
        await server.rpc("runstatus.session.patch_world", {
          session_id: sid,
          patch: { judge_mode: "human" },
        });
        await server.rpc("runstatus.session.submit", {
          session_id: sid,
          intent: "go_idea",
          slots: { message: "work on a proposal" },
        });
        await server.rpc("runstatus.session.submit", {
          session_id: sid,
          intent: "discuss",
          slots: { message: "I want a per-session working folder primitive" },
        });
        // Do NOT reload — the tour overlay lives in in-memory Pinia state and a
        // reload would tear it down. The driving page is already on /chat
        // watching THIS session, so SSE pushes the state updates live.
        await waitForState(page, "design_search", 15000);
        await stageDiagram(page);
        await dwell(page, SETTLE_MS);
      }

      // Switch the diagram tab so the spotlighted testid is on screen.
      const tab = TAB_FOR_STEP[step.id];
      if (tab) await diagramTab(page, tab);

      // Honor DOM-presence preconditions.
      if (step.waitForTarget) {
        await expect(page.getByTestId(step.waitForTarget).first()).toBeVisible({ timeout: 15000 });
      }

      // Anti-drift assertion: the popover must show THIS step's title.
      const titleEl = page.getByTestId("tour-title");
      const actualTitle = await titleEl.textContent({ timeout: 8000 }).catch(() => "");
      if (actualTitle !== step.title) {
        const remaining = DIAGRAM_SHOWCASE_TOUR_STEPS.slice(
          DIAGRAM_SHOWCASE_TOUR_STEPS.indexOf(step) + 1
        );
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
        const target = resolveTarget(page, step);
        await target.scrollIntoViewIfNeeded().catch(() => undefined);
        if (step.advance === "route-match") {
          await target.click();
          await page.waitForTimeout(300);
          if (step.advanceRoute === "interactive") {
            await page.waitForURL(/#\/s\/[0-9a-f-]{36}\/chat$/, { timeout: 15000 });
            const m = page.url().match(/\/s\/([0-9a-f-]{36})\/chat$/);
            if (m) {
              sid = m[1];
              diag(`session ${sid}`);
            }
          }
          await dwell(page, 1000);
        } else {
          // click-target control.
          await target.evaluate((el) => (el as HTMLElement).click());
          await dwell(page, 1000);
        }
      }
    }

    // The final dsg-done step's "Done" closes the tour.
    await expect(page.getByTestId("tour-overlay")).toHaveCount(0, { timeout: 5000 });

    // ── 3. rrweb: dump the FULL accumulated event stream + capture viewport ───
    // dumpCapture returns the observed viewport/DSF; writeEvents persists it in
    // the <events>.capture.json sidecar so the render asserts the viewport-match
    // invariant (transform:none is only clip-safe at 1:1).
    const { events, viewport } = await dumpCapture(page);
    diag(`rrweb captured ${events.length} events @ ${viewport.width}x${viewport.height} dsf=${viewport.deviceScaleFactor}`);
    writeEvents(events, EVENTS_JSON, viewport);
    expect(events.length, "rrweb should have emitted a healthy event stream").toBeGreaterThanOrEqual(50);
  } catch (e) {
    diag(`FAILED: ${e instanceof Error ? e.stack ?? e.message : String(e)}`);
    diag(`--- server log ---\n${server?.log?.() ?? ""}`);
    throw e;
  } finally {
    await context.close(); // finalises the recording
    const mp4 = await saveVideoAsMp4(video, ARTIFACT_DIR, "diagram-showcase-baseline");
    writeChapters(mp4, chapters.list());
    await browser.close();
  }

  const pngs = fs.readdirSync(BASELINE_FRAMES_DIR).filter((f) => f.endsWith(".png"));
  console.log(`[diagram-showcase-rrweb-capture] baseline frames (${pngs.length}) in ${BASELINE_FRAMES_DIR}`);
  console.log(`[diagram-showcase-rrweb-capture] events → ${EVENTS_JSON}`);
});
