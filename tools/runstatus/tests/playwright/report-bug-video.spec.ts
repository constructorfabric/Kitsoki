/**
 * "Report bug" Meta-menu feature-spotlight video demo.
 *
 * Drives the dedicated report-bug tour against a real `kitsoki web` server in
 * the deterministic no-LLM posture (--flow happy_llm.yaml + the demo cassette)
 * and records a video + per-scene screenshots to .artifacts/report-bug/.
 *
 * Like agent-actions-video.spec.ts, this spec runs ONLY the
 * REPORT_BUG_TOUR_STEPS from src/tour/report-bug-manifest.ts via
 * window.__startTourWithSteps. The tour drives the whole video: it opens on the
 * home story library and its route-match action steps navigate home → new
 * session → observer, so even the intro is tour-narrated rather than silent
 * spec orchestration.
 *
 * THE FLOW (capture → review modal → file): clicking "Report bug" no longer
 * files silently. It CAPTURES (rrweb session replay + console + a server-scrubbed
 * HAR via runstatus.bug.preview) and opens the review modal (BugReportModal.vue,
 * a Teleport-to-body overlay; store states idle|capturing|reviewing|submitting|
 * filed|error). The operator reviews replay/HAR/console, edits the title +
 * optional description, then clicks "File bug". Only THEN does the server write
 * issues/bugs/<id>.md + a sibling <id>.artifacts/ folder. The spec mirrors this:
 * a pre-step hook for bug-har clicks bug-modal-har-raw-toggle to reveal the raw
 * JSON; a pre-step hook for bug-describe types a description; the bug-submit
 * click-target files the bug and surfaces the result toast (bug-toast-path).
 *
 * REPO-SAFETY: filing WRITES a real ticket. The server resolves the bug root to
 * the git toplevel of its --stories-dir (cmd/kitsoki/web.go resolveWebBugRoot →
 * internal/runstatus/server/bug_report.go resolveBugRoot). So this spec copies
 * stories/bugfix into a fresh os.tmpdir() path that is NOT inside this git repo
 * (no .git ancestor), and points --stories-dir there. The filed
 * issues/bugs/<id>.md + sibling <id>.artifacts/ land in that throwaway dir and
 * are torn down in afterAll — never in the worktree.
 *
 * Validate fast (no dwells):
 *   WEB_CHAT_PACE=0 pnpm exec playwright test report-bug-video --project=chromium
 * Record at watch-speed:
 *   pnpm exec playwright test report-bug-video --project=chromium
 *
 * NOTE: the harness suppresses Playwright stdout, so per-step progress and any
 * failure context is also written to .artifacts/report-bug/ERROR.txt.
 */
import { test, expect, chromium, type Browser, type BrowserContext, type Page, type Locator } from "@playwright/test";
import path from "path";
import fs from "fs";
import os from "os";
import {
  startWebServer,
  repoRoot,
  makeShot,
  prepareVideoDir,
  saveVideoAsMp4,
  dwell,
  cinematicGoto,
  SETTLE_MS,
  demoAddr,
  type WebServer,
} from "./_helpers/server.js";
import { cameraContext } from "./_helpers/camera.js";
import { REPORT_BUG_TOUR_STEPS, type TourStep } from "../../src/tour/report-bug-manifest.js";

// 7750 — distinct from agent-actions (7748), tour-onboarding (7747) and
// trace-features (7746) so parallel spec files never race on the same bind.
const ADDR = demoAddr(7750);

// REPO-SAFETY: copy the bugfix story into a throwaway dir OUTSIDE this git
// repo, so the filed ticket lands there (resolveWebBugRoot walks up to the
// nearest .git; with none above the tmp dir, the bug root is the story dir
// itself). The tmp dir is removed in afterAll.
const SRC_STORY_DIR = path.join(repoRoot, "stories", "bugfix");
let TMP_ROOT = "";
let STORY_DIR = "";
let FLOW = "";
let HOST_CASSETTE = "";

const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "report-bug");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "video");
const ERROR_LOG = path.join(ARTIFACT_DIR, "ERROR.txt");

// A description the operator types into the review modal (paced typing, like
// the golden composer typing). Short enough to type at watch-speed within dwell.
const DEMO_DESCRIPTION =
  "The judge gate landed somewhere I didn't expect — capturing the run for a repro.";

let server: WebServer;

function diag(msg: string): void {
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

  // Copy stories/bugfix into a fresh tmp dir with NO .git ancestor.
  TMP_ROOT = fs.mkdtempSync(path.join(os.tmpdir(), "kitsoki-bugdemo-"));
  STORY_DIR = path.join(TMP_ROOT, "bugfix");
  fs.cpSync(SRC_STORY_DIR, STORY_DIR, { recursive: true });
  FLOW = path.join(STORY_DIR, "flows", "happy_llm.yaml");
  HOST_CASSETTE = path.join(STORY_DIR, "flows", "demo.cassette.yaml");
  diag(`temp story dir: ${STORY_DIR}`);

  server = await startWebServer({ addr: ADDR, flow: FLOW, hostCassette: HOST_CASSETTE, storiesDir: STORY_DIR });
});

test.afterAll(() => {
  server?.stop();
  if (TMP_ROOT) fs.rmSync(TMP_ROOT, { recursive: true, force: true });
});

/** Resolve an action step's real target element — first visible match. */
async function resolveTarget(page: Page, step: TourStep): Promise<Locator> {
  return page.getByTestId(step.target!).first();
}

/** Open the Meta dropdown if it isn't already open. */
async function ensureMetaMenuOpen(page: Page): Promise<void> {
  const menu = page.getByTestId("meta-menu");
  if (await menu.isVisible().catch(() => false)) return;
  await page.getByTestId("meta-button").click();
  await expect(menu).toBeVisible({ timeout: 6000 });
}

/**
 * Type `text` into a modal field at a watchable pace, the way the golden
 * composer typing does — set value through the native setter so Vue's v-model
 * sees it, but here we drive it char-by-char so the camera shows it landing.
 * The review modal is a Teleport-to-body overlay above the tour backdrop, so a
 * real focus + key sequence works (no backdrop interception).
 */
async function pacedType(page: Page, locator: Locator, text: string): Promise<void> {
  await locator.click();
  await locator.fill("");
  // pressSequentially at a PACE-scaled per-char delay (0 when WEB_CHAT_PACE=0).
  const PACE = Number(process.env.WEB_CHAT_PACE ?? "1");
  await locator.pressSequentially(text, { delay: Math.round(28 * PACE) });
}

test("report-bug feature-spotlight video", async () => {
  test.setTimeout(300000);
  const browser: Browser = await chromium.launch({ headless: true });
  const context: BrowserContext = await browser.newContext(
    cameraContext({ recordVideoDir: VIDEO_DIR }),
  );
  const page: Page = await context.newPage();
  const video = page.video();
  const shot = makeShot(ARTIFACT_DIR);

  // Carries the session id once the intro's "New session" step creates the run.
  let sessionId = "";

  try {
    // ── 1. Open the home story library and start the tour ON it ──────────────
    diag("navigating home");
    await cinematicGoto(page, `${server.base}/#/`, { waitForTestId: "home-view" });

    await page.evaluate((stepsJson: string) => {
      (window as unknown as { __startTourWithSteps?: (s: string) => void })
        .__startTourWithSteps?.(stepsJson);
    }, JSON.stringify(REPORT_BUG_TOUR_STEPS));
    await expect(page.getByTestId("tour-overlay")).toBeVisible({ timeout: 8000 });

    // ── 2. Walk the REPORT_BUG_TOUR_STEPS (intro + capture/review/file) ──────
    for (const step of REPORT_BUG_TOUR_STEPS) {
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
      // bug-report-item spotlights the menu item — re-open the dropdown if the
      // prior explain step's Next click (outside .meta-launcher) re-closed it.
      if (step.id === "bug-report-item") {
        await ensureMetaMenuOpen(page);
        await dwell(page, SETTLE_MS);
      }

      // bug-modal: the prior step (bug-report-item) clicked Report bug, kicking
      // the store idle→capturing→reviewing. Wait for the review modal to mount
      // (Teleport-to-body), then DWELL so the lazy rrweb Replayer has a beat to
      // build its iframe (replacing the "Loading replay…" placeholder) before the
      // spotlight lands.
      if (step.id === "bug-modal") {
        await expect(page.getByTestId("bug-modal")).toBeVisible({ timeout: 20000 });
        await dwell(page, SETTLE_MS);
      }

      // bug-replay: the single visual is the rrweb session replay (rendered by
      // rrweb's own Replayer, NOT the broken rrweb-player wrapper). Wait until
      // the Replayer has actually built its iframe AND reconstructed a populated
      // DOM inside it, so the spotlight (and the captured screenshot) shows the
      // reconstructed UI — a faithful render of the app, never a blank/white box
      // or the "Loading replay…" placeholder. This assertion is the recording's
      // hard guard against a silently-empty replay.
      if (step.id === "bug-replay") {
        const replay = page.getByTestId("bug-modal-replay");
        await expect(replay).toBeVisible({ timeout: 8000 });
        await page.waitForFunction(
          () => {
            const host = document.querySelector(
              '[data-testid="bug-modal-replay"]',
            );
            const ifr = host?.querySelector("iframe") as
              | HTMLIFrameElement
              | null;
            if (!host?.querySelector(".replayer-wrapper") || !ifr) return false;
            // The reconstructed DOM must be non-trivially populated — a blank
            // iframe (the old failure mode) has an essentially empty body.
            const len =
              ifr.contentDocument?.body?.innerHTML.length ?? 0;
            return len > 200;
          },
          { timeout: 15000 },
        );
        await dwell(page, SETTLE_MS * 2);
      }

      // bug-har: reveal the raw scrubbed HAR JSON (the spotlight target) by
      // clicking the toggle inside the modal, then settle so the <pre> renders.
      if (step.id === "bug-har") {
        const toggle = page.getByTestId("bug-modal-har-raw-toggle");
        await expect(toggle).toBeVisible({ timeout: 8000 });
        if (!(await page.getByTestId("bug-modal-har-raw").isVisible().catch(() => false))) {
          await toggle.click();
        }
        await expect(page.getByTestId("bug-modal-har-raw")).toBeVisible({ timeout: 8000 });
        await dwell(page, SETTLE_MS);
      }

      // bug-describe: type an optional description into the modal textarea at a
      // watchable pace (mirrors the golden composer typing).
      if (step.id === "bug-describe") {
        const desc = page.getByTestId("bug-modal-description");
        await expect(desc).toBeVisible({ timeout: 8000 });
        await pacedType(page, desc, DEMO_DESCRIPTION);
        await dwell(page, SETTLE_MS);
      }

      // Honor DOM-presence preconditions.
      if (step.waitForTarget) {
        await expect(page.getByTestId(step.waitForTarget).first()).toBeVisible({ timeout: 20000 });
      }

      // Anti-drift assertion: the popover must show THIS step's title.
      const titleEl = page.getByTestId("tour-title");
      const actualTitle = await titleEl.textContent({ timeout: 8000 }).catch(() => "");
      if (actualTitle !== step.title) {
        const remaining = REPORT_BUG_TOUR_STEPS.slice(REPORT_BUG_TOUR_STEPS.indexOf(step) + 1);
        const isOnNext = remaining.some((s) => s.title === actualTitle);
        if (isOnNext) {
          diag(`  drift-skip: overlay on "${actualTitle}"`);
          continue;
        }
      }
      await expect(titleEl).toHaveText(step.title, { timeout: 12000 });

      await dwell(page, step.dwellMs ?? 3000);
      await shot(page, step.id);

      if (step.kind === "explain") {
        await page.getByTestId("tour-next").click();
        await dwell(page, 700);
      } else {
        const target = await resolveTarget(page, step);
        await target.scrollIntoViewIfNeeded().catch(() => undefined);
        if (step.advance === "route-match") {
          await target.click();
          await page.waitForTimeout(300);
          if (step.advanceRoute === "interactive") {
            await page.waitForURL(/#\/s\/[0-9a-f-]{36}\/chat$/, { timeout: 15000 });
            const m = page.url().match(/\/s\/([0-9a-f-]{36})\/chat$/);
            if (m) {
              sessionId = m[1];
              diag(`session ${sessionId}`);
            }
          } else if (step.advanceRoute === "any") {
            await page.waitForURL(/#\/s\/[0-9a-f-]{36}$/, { timeout: 15000 });
          }
          await dwell(page, 1000);
        } else {
          // click-target: meta-button opens the menu; meta-report-bug triggers
          // capture (→ review modal); bug-modal-submit files the bug. Dispatch
          // the DOM click directly so it fires the control's own @click AND the
          // overlay's capture-phase advance listener (bound on the same
          // element), regardless of the backdrop hole's paint geometry.
          await target.evaluate((el) => (el as HTMLElement).click());
          await dwell(page, 1000);
        }
      }
    }

    // The final bug-done step's "Done" closes the tour.
    await expect(page.getByTestId("tour-overlay")).toHaveCount(0, { timeout: 5000 });
  } catch (e) {
    diag(`FAILED: ${e instanceof Error ? e.stack ?? e.message : String(e)}`);
    diag(`--- server log ---\n${server?.log?.() ?? ""}`);
    throw e;
  } finally {
    await context.close();
    await saveVideoAsMp4(video, ARTIFACT_DIR, "report-bug-demo");
    await browser.close();
  }

  const pngs = fs.readdirSync(ARTIFACT_DIR).filter((f) => f.endsWith(".png"));
  console.log(`[report-bug-video] screenshots (${pngs.length}) in ${ARTIFACT_DIR}`);
});
