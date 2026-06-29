/**
 * "Report bug" demo for ONE specific bug: the redundant "Observe ↗" link
 * (data-testid `observe-link`) in the upper-right topbar, sitting right next to
 * the "Hide/show trace" toggle (data-testid `trace-column-toggle`). They are
 * easily confused; the (later) fix is to remove the Observe link. The
 * auto-captured rrweb replay in the report itself shows that topbar.
 *
 * This is an ADAPTED COPY of report-bug-video.spec.ts (which must NOT be
 * modified). Differences:
 *   - It copies the WHOLE stories/ tree into the throwaway tmp root so the
 *     bugfix story's `source: ../delivery-tail` (and that story's transitive
 *     `../conflict-resolve`) sibling imports resolve — the original spec copied
 *     only stories/bugfix, so 0 stories loaded and the tour died at story-card.
 *   - It sets the bug title + description to the Observe-link bug.
 *   - It honours DEMO_TICKET_REPO: unset/"" → LOCAL filing (issues/bugs/<id>.md
 *     + sibling <id>.artifacts/), "owner/repo" → a real GitHub issue via gh.
 *   - It writes the tour video + screenshots to .artifacts/report-bug-observe/.
 *
 * Run (LOCAL, deterministic, no GitHub):
 *   DEMO_TICKET_REPO="" pnpm exec playwright test report-bug-observe-video \
 *     --project=chromium --reporter=line
 * Run (GitHub, files a real issue via gh):
 *   DEMO_TICKET_REPO="bsacrobatix/Kitsoki" pnpm exec playwright test \
 *     report-bug-observe-video --project=chromium --reporter=line
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

// 7751 — distinct from report-bug-video (7750) and the other demo specs so
// parallel spec files never race on the same bind.
const ADDR = demoAddr(7751);

const SRC_STORIES_DIR = path.join(repoRoot, "stories");
let TMP_ROOT = "";
let STORY_DIR = "";
let FLOW = "";
let HOST_CASSETTE = "";

// Ticket target: "" (or unset) → local issues/bugs/<id>.md; "owner/repo" → gh.
const TICKET_REPO = process.env.DEMO_TICKET_REPO ?? "";

const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "report-bug-observe");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "video");
const ERROR_LOG = path.join(ARTIFACT_DIR, "ERROR.txt");

// The bug being reported (the Observe-link redundancy).
const DEMO_TITLE = "Remove redundant 'Observe' link next to Hide/show trace toggle";
const DEMO_DESCRIPTION =
  "The 'Observe ↗' link (observe-link) in the upper-right topbar is redundant " +
  "and easily confused with the adjacent 'Hide/show trace' toggle " +
  "(trace-column-toggle). It navigates to the read-only observer for the same " +
  "run. Remove the Observe link to declutter the topbar.";

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

  // Copy the WHOLE stories/ tree into a fresh tmp dir with NO .git ancestor, so
  // bugfix's `source: ../delivery-tail` (→ ../conflict-resolve) siblings
  // resolve. --stories-dir still points at the bugfix subdir; the local-filed
  // issues/bugs/<id>.md lands there (resolveWebBugRoot finds no .git above tmp).
  TMP_ROOT = fs.mkdtempSync(path.join(os.tmpdir(), "kitsoki-bugobs-"));
  fs.cpSync(SRC_STORIES_DIR, TMP_ROOT, { recursive: true });
  STORY_DIR = path.join(TMP_ROOT, "bugfix");
  FLOW = path.join(STORY_DIR, "flows", "happy_llm.yaml");
  HOST_CASSETTE = path.join(STORY_DIR, "flows", "demo.cassette.yaml");
  diag(`temp stories root: ${TMP_ROOT}`);
  diag(`story dir: ${STORY_DIR}`);
  diag(`ticket repo: ${TICKET_REPO === "" ? "(local)" : TICKET_REPO}`);

  server = await startWebServer({
    addr: ADDR,
    flow: FLOW,
    hostCassette: HOST_CASSETTE,
    storiesDir: STORY_DIR,
    ticketRepo: TICKET_REPO,
  });
});

test.afterAll(() => {
  server?.stop();
  // Keep the tmp root when filing locally so the caller can inspect the filed
  // issue + artifacts; otherwise tear it down.
  if (TMP_ROOT && TICKET_REPO !== "") fs.rmSync(TMP_ROOT, { recursive: true, force: true });
  else if (TMP_ROOT) diag(`KEEPING tmp root for inspection: ${TMP_ROOT}`);
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

/** Type `text` into a modal field at a watchable pace. */
async function pacedType(page: Page, locator: Locator, text: string): Promise<void> {
  await locator.click();
  await locator.fill("");
  const PACE = Number(process.env.WEB_CHAT_PACE ?? "1");
  await locator.pressSequentially(text, { delay: Math.round(28 * PACE) });
}

test("report-bug observe-link video", async () => {
  test.setTimeout(300000);
  const browser: Browser = await chromium.launch({ headless: true });
  const context: BrowserContext = await browser.newContext(
    cameraContext({ recordVideoDir: VIDEO_DIR }),
  );
  const page: Page = await context.newPage();
  const video = page.video();
  const shot = makeShot(ARTIFACT_DIR);

  let sessionId = "";
  let filedPath = "";

  try {
    // ── 1. Open the home story library and start the tour ON it ──────────────
    diag("navigating home");
    await cinematicGoto(page, `${server.base}/#/`, { waitForTestId: "home-view" });

    // Guard the sibling-copy fix: a story-card must exist before the tour walks.
    await expect(page.getByTestId("story-card").first()).toBeVisible({ timeout: 15000 });

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

      if (step.id === "bug-report-item") {
        await ensureMetaMenuOpen(page);
        await dwell(page, SETTLE_MS);
      }

      if (step.id === "bug-modal") {
        await expect(page.getByTestId("bug-modal")).toBeVisible({ timeout: 20000 });
        await dwell(page, SETTLE_MS);
      }

      if (step.id === "bug-replay") {
        const replay = page.getByTestId("bug-modal-replay");
        await expect(replay).toBeVisible({ timeout: 8000 });
        await page.waitForFunction(
          () => {
            const host = document.querySelector('[data-testid="bug-modal-replay"]');
            const ifr = host?.querySelector("iframe") as HTMLIFrameElement | null;
            if (!host?.querySelector(".replayer-wrapper") || !ifr) return false;
            const len = ifr.contentDocument?.body?.innerHTML.length ?? 0;
            return len > 200;
          },
          { timeout: 15000 },
        );
        await dwell(page, SETTLE_MS * 2);
      }

      if (step.id === "bug-har") {
        const toggle = page.getByTestId("bug-modal-har-raw-toggle");
        await expect(toggle).toBeVisible({ timeout: 8000 });
        if (!(await page.getByTestId("bug-modal-har-raw").isVisible().catch(() => false))) {
          await toggle.click();
        }
        await expect(page.getByTestId("bug-modal-har-raw")).toBeVisible({ timeout: 8000 });
        await dwell(page, SETTLE_MS);
      }

      // bug-describe: set the title to the Observe-link bug, then type the
      // description. The title input is prefilled "Bug report"; overwrite it.
      if (step.id === "bug-describe") {
        const titleInput = page.getByTestId("bug-modal-title");
        await expect(titleInput).toBeVisible({ timeout: 8000 });
        await pacedType(page, titleInput, DEMO_TITLE);
        const desc = page.getByTestId("bug-modal-description");
        await expect(desc).toBeVisible({ timeout: 8000 });
        await pacedType(page, desc, DEMO_DESCRIPTION);
        await dwell(page, SETTLE_MS);
      }

      if (step.waitForTarget) {
        await expect(page.getByTestId(step.waitForTarget).first()).toBeVisible({ timeout: 20000 });
      }

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
          await target.evaluate((el) => (el as HTMLElement).click());
          await dwell(page, 1000);
        }
      }
    }

    await expect(page.getByTestId("tour-overlay")).toHaveCount(0, { timeout: 5000 });

    // Capture the filed path/URL surfaced in the success toast.
    filedPath = (await page.getByTestId("bug-toast-path").textContent({ timeout: 5000 }).catch(() => "")) ?? "";
    diag(`FILED: ${filedPath}`);
  } catch (e) {
    diag(`FAILED: ${e instanceof Error ? e.stack ?? e.message : String(e)}`);
    diag(`--- server log ---\n${server?.log?.() ?? ""}`);
    throw e;
  } finally {
    await context.close();
    await saveVideoAsMp4(video, ARTIFACT_DIR, "report-bug-observe-demo");
    await browser.close();
  }

  const pngs = fs.readdirSync(ARTIFACT_DIR).filter((f) => f.endsWith(".png"));
  console.log(`[report-bug-observe-video] screenshots (${pngs.length}) in ${ARTIFACT_DIR}`);
  console.log(`[report-bug-observe-video] filed: ${filedPath}`);
  if (TICKET_REPO === "") console.log(`[report-bug-observe-video] tmp story root (kept): ${TMP_ROOT}`);
});
