/**
 * pet-report-bug-capture.spec.ts — rrweb capture of the in-product "Report a
 * bug" flow for the trace-column pet scenario: a user opens Meta → Report a bug,
 * reviews the auto-captured session replay, describes the Observe-link bug, and
 * files it — all BEFORE the bug surfaces on GitHub (#54). The hybrid deck embeds
 * this clip just ahead of the GitHub bug-thread scene so the story shows where
 * the bug actually originates: a real operator filing it from the web UI.
 *
 * This is an rrweb-capture sibling of report-bug-observe-video.spec.ts (which
 * records an mp4 via recordVideo and must NOT be modified). It walks the SAME
 * proven REPORT_BUG_TOUR_STEPS manifest against the bugfix story, but:
 *   - installCapture(page) BEFORE the first navigation + dumpCapture/writeEvents
 *     at the end → a docs/decks/clips/*.rrweb.json clip (NOT an mp4).
 *   - a plain 1600x900 DSF1 context (matches the other kitsoki-web deck clips;
 *     no recordVideo).
 *   - LOCAL filing only (DEMO_TICKET_REPO defaults to "" → issues/bugs/<id>.md in
 *     the throwaway tmp stories root, never a real GitHub issue, no cost).
 *
 * The tour overlay narrates each step on-camera, so the clip is self-explanatory
 * (Meta launcher → Report a bug → review capture → describe → file).
 *
 * Output:
 *   docs/decks/clips/pet-report-bug.rrweb.json          ← rrweb event stream
 *   docs/decks/clips/pet-report-bug.rrweb.capture.json  ← viewport sidecar
 *
 * Run:
 *   cd tools/runstatus && npx playwright test tests/playwright/pet-report-bug-capture.spec.ts --reporter=line
 */
import { test, expect, chromium, type Browser, type BrowserContext, type Page, type Locator } from "@playwright/test";
import path from "path";
import fs from "fs";
import os from "os";
import {
  startWebServer,
  repoRoot,
  makeShot,
  dwell,
  cinematicGoto,
  SETTLE_MS,
  demoAddr,
  type WebServer,
} from "./_helpers/server.js";
import { installCapture, dumpCapture, writeEvents } from "./_helpers/rrweb-replay.js";
import { REPORT_BUG_TOUR_STEPS, type TourStep } from "../../src/tour/report-bug-manifest.js";

const ADDR = demoAddr(7752);
const OUT_DIR = path.join(repoRoot, "docs", "decks", "clips");
const EVENTS_JSON = path.join(OUT_DIR, "pet-report-bug.rrweb.json");
const VIEWPORT = { width: 1600, height: 900 } as const;

const SRC_STORIES_DIR = path.join(repoRoot, "stories");
let TMP_ROOT = "";
let STORY_DIR = "";
let FLOW = "";
let HOST_CASSETTE = "";

// LOCAL filing by default ("" → issues/bugs/<id>.md in the tmp root). A real
// repo can be passed to file a GitHub issue, but the deck clip never needs it.
const TICKET_REPO = process.env.DEMO_TICKET_REPO ?? "";

const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "pet-report-bug");
const ERROR_LOG = path.join(ARTIFACT_DIR, "ERROR.txt");

// The bug being reported — the redundant Observe link (matches the #54 thread).
const DEMO_TITLE = "Remove redundant 'Observe' link next to Hide/show trace toggle";
const DEMO_DESCRIPTION =
  "The 'Observe ↗' link (observe-link) in the upper-right topbar is redundant " +
  "and easily confused with the adjacent 'Hide/show trace' toggle " +
  "(trace-column-toggle). It navigates to the read-only observer for the same " +
  "run. Remove the Observe link to declutter the topbar.";

let server: WebServer;

function diag(msg: string): void {
  try {
    fs.appendFileSync(ERROR_LOG, `[${new Date().toISOString()}] ${msg}\n`);
  } catch {
    /* best-effort */
  }
}

test.beforeAll(async () => {
  fs.mkdirSync(OUT_DIR, { recursive: true });
  fs.mkdirSync(ARTIFACT_DIR, { recursive: true });
  fs.writeFileSync(ERROR_LOG, "");

  // Copy the WHOLE stories/ tree into a fresh tmp dir with NO .git ancestor, so
  // bugfix's `source: ../delivery-tail` (→ ../conflict-resolve) siblings resolve
  // AND the local-filed issues/bugs/<id>.md lands in tmp (not the real repo).
  TMP_ROOT = fs.mkdtempSync(path.join(os.tmpdir(), "kitsoki-petbug-"));
  fs.cpSync(SRC_STORIES_DIR, TMP_ROOT, { recursive: true });
  STORY_DIR = path.join(TMP_ROOT, "bugfix");
  FLOW = path.join(STORY_DIR, "flows", "happy_llm.yaml");
  HOST_CASSETTE = path.join(STORY_DIR, "flows", "demo.cassette.yaml");
  diag(`temp stories root: ${TMP_ROOT}`);
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
  if (TMP_ROOT && TICKET_REPO !== "") fs.rmSync(TMP_ROOT, { recursive: true, force: true });
  else if (TMP_ROOT) diag(`KEEPING tmp root for inspection: ${TMP_ROOT}`);
});

async function resolveTarget(page: Page, step: TourStep): Promise<Locator> {
  return page.getByTestId(step.target!).first();
}

async function ensureMetaMenuOpen(page: Page): Promise<void> {
  const menu = page.getByTestId("meta-menu");
  if (await menu.isVisible().catch(() => false)) return;
  await page.getByTestId("meta-button").click();
  await expect(menu).toBeVisible({ timeout: 6000 });
}

async function pacedType(page: Page, locator: Locator, text: string): Promise<void> {
  await locator.click();
  await locator.fill("");
  const PACE = Number(process.env.WEB_CHAT_PACE ?? "1");
  await locator.pressSequentially(text, { delay: Math.round(28 * PACE) });
}

test("pet report-bug rrweb capture (Meta → Report a bug → file)", async () => {
  test.setTimeout(300000);
  const browser: Browser = await chromium.launch({ headless: true });
  const context: BrowserContext = await browser.newContext({
    viewport: { ...VIEWPORT },
    deviceScaleFactor: 1,
  });
  const page: Page = await context.newPage();
  const shot = makeShot(ARTIFACT_DIR);

  let filedPath = "";

  try {
    // ── 1. Home story library, install rrweb capture, start the tour ─────────
    diag("navigating home");
    await cinematicGoto(page, `${server.base}/#/`, { waitForTestId: "home-view" });
    await expect(page.getByTestId("story-card").first()).toBeVisible({ timeout: 15000 });

    await installCapture(page);
    diag("rrweb capture installed");

    await page.evaluate((stepsJson: string) => {
      (window as unknown as { __startTourWithSteps?: (s: string) => void }).__startTourWithSteps?.(stepsJson);
    }, JSON.stringify(REPORT_BUG_TOUR_STEPS));
    await expect(page.getByTestId("tour-overlay")).toBeVisible({ timeout: 8000 });

    // ── 2. Walk REPORT_BUG_TOUR_STEPS (intro + capture/review/describe/file) ──
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
        await page
          .waitForFunction(
            () => {
              const host = document.querySelector('[data-testid="bug-modal-replay"]');
              const ifr = host?.querySelector("iframe") as HTMLIFrameElement | null;
              if (!host?.querySelector(".replayer-wrapper") || !ifr) return false;
              const len = ifr.contentDocument?.body?.innerHTML.length ?? 0;
              return len > 200;
            },
            { timeout: 15000 },
          )
          .catch(() => diag("  replay render wait timed out (non-fatal)"));
        await dwell(page, SETTLE_MS * 2);
      }

      if (step.id === "bug-har") {
        const toggle = page.getByTestId("bug-modal-har-raw-toggle");
        if (await toggle.isVisible().catch(() => false)) {
          if (!(await page.getByTestId("bug-modal-har-raw").isVisible().catch(() => false))) {
            await toggle.click().catch(() => undefined);
          }
        }
        await dwell(page, SETTLE_MS);
      }

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
        if (remaining.some((s) => s.title === actualTitle)) {
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

    filedPath = (await page.getByTestId("bug-toast-path").textContent({ timeout: 5000 }).catch(() => "")) ?? "";
    diag(`FILED: ${filedPath}`);
    await dwell(page, 1500); // rest on the success toast

    const { events, viewport } = await dumpCapture(page);
    writeEvents(events, EVENTS_JSON, viewport);
    console.log(
      `[pet-report-bug] events=${events.length} @ ${viewport.width}x${viewport.height} filed="${filedPath}" -> ${EVENTS_JSON}`,
    );
    expect(events.length, "recorded the full report-bug walk").toBeGreaterThanOrEqual(40);
  } catch (e) {
    diag(`FAILED: ${e instanceof Error ? e.stack ?? e.message : String(e)}`);
    diag(`--- server log ---\n${server?.log?.() ?? ""}`);
    throw e;
  } finally {
    await context.close();
    await browser.close();
  }
});
