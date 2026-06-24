/**
 * State-diagram SHOWCASE video — the four route views against the dev-story
 * design pipeline (the exact scenario the design mockup was drawn for:
 * `.artifacts/diagram-options/index.html`).
 *
 * A dedicated, feature-focused demo (distinct from the generic onboarding tour).
 * Like the golden agent-actions spec, this video is TOUR-DRIVEN: the
 * DIAGRAM_SHOWCASE_TOUR_STEPS in src/tour/generated/diagram-showcase.ts narrate
 * the whole run via window.__startTourWithSteps. The intro opens on the home
 * story library and its route-match action step navigates home → new session,
 * then route "any" steps spotlight the StateDiagram's four views (metro / ego /
 * path / full). The spec asserts each step's `title` against the live popover so
 * the manifest and video cannot silently drift.
 *
 * The diagram lives in the InteractiveView (/chat) panel, so once the intro
 * lands on the chat route the spec drives the design pipeline to
 * design_search OFF-CAMERA via RPC (the design_happy_path flow stubs every
 * host call — no LLM, no cost), and the diagram-view steps' pre-step hooks
 * switch the matching diagram tab so the spotlighted testid is on screen.
 *
 * Record:  pnpm exec playwright test diagram-showcase --project=chromium
 * Fast:    WEB_CHAT_PACE=0 pnpm exec playwright test diagram-showcase --project=chromium
 *
 * NOTE: the harness suppresses Playwright stdout, so per-step progress and any
 * failure context is also written to .artifacts/diagram-showcase/diagnostic.log.
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
  demoAddr,
  dwell,
  cinematicGoto,
  SETTLE_MS,
  type WebServer,
} from "./_helpers/server.js";
import { cameraContext } from "./_helpers/camera.js";
import { DIAGRAM_SHOWCASE_TOUR_STEPS, type TourStep } from "../../src/tour/generated/diagram-showcase.js";

// The feature-catalog source of truth for this spec's tour steps: each step
// becomes a chapter (source_ref kind=tour) in the MP4's sidecar.
const CHAPTER_SOURCE = "features/diagram-showcase.yaml";

// 7753 — distinct from the other spec files so parallel runs never race on the
// same port bind.
const ADDR = demoAddr(7753);
const STORY_DIR = path.join(repoRoot, "stories", "dev-story");
const FLOW = path.join(STORY_DIR, "flows", "design_happy_path.yaml");
const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "diagram-showcase");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "video");
const DIAG_LOG = path.join(ARTIFACT_DIR, "diagnostic.log");

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
  prepareVideoDir(VIDEO_DIR); // clear stale webm so saveVideoAsMp4 picks THIS run's
  fs.mkdirSync(ARTIFACT_DIR, { recursive: true });
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

test("state-diagram four-view showcase (dev-story, no-LLM)", async () => {
  test.setTimeout(300000);
  const browser: Browser = await chromium.launch({ headless: true });
  const context: BrowserContext = await browser.newContext(
    cameraContext({ recordVideoDir: VIDEO_DIR }),
  );
  const page: Page = await context.newPage();
  const video = page.video(); // capture BEFORE context.close()
  const shot = makeShot(ARTIFACT_DIR);

  // Accumulate per-step time windows for the chapter sidecar. The clock starts
  // now so windows line up with the recorded MP4 timeline.
  const chapters = new ChapterRecorder();

  // Carries the session id once the intro's "New session" step creates the run.
  let sid = "";

  try {
    // ── 1. Open the home story library and start the tour ON it ──────────────
    diag("navigating home");
    await cinematicGoto(page, `${server.base}/#/`, { waitForTestId: "home-view" });

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
        const cur = (await page.getByTestId("current-state").textContent())?.trim() ?? "";
        if (cur === "landing" || cur === "main") {
          await server.rpc("runstatus.session.submit", {
            session_id: sid,
            intent: "go_idea",
            slots: { message: "work on a proposal" },
          });
        }
        await waitForState(page, "design", 15000);
        await server.rpc("runstatus.session.patch_world", {
          session_id: sid,
          patch: { judge_mode: "human" },
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
  } catch (e) {
    diag(`FAILED: ${e instanceof Error ? e.stack ?? e.message : String(e)}`);
    diag(`--- server log ---\n${server?.log?.() ?? ""}`);
    throw e;
  } finally {
    await context.close(); // finalises the recording
    const mp4 = await saveVideoAsMp4(video, ARTIFACT_DIR, "diagram-showcase-demo");
    // Emit the producer-agnostic chapter sidecar beside the MP4: each tour
    // step → one chapter with source_ref kind=tour.
    writeChapters(mp4, chapters.list());
    await browser.close();
  }

  const pngs = fs.readdirSync(ARTIFACT_DIR).filter((f) => f.endsWith(".png"));
  console.log(`[diagram-showcase] screenshots (${pngs.length}) in ${ARTIFACT_DIR}`);
});
