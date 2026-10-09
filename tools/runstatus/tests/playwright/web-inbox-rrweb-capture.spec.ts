/**
 * web-inbox-rrweb-capture.spec.ts — rrweb capture spec (the async-inbox tour).
 *
 * The CAPTURE half of the rrweb capture→replay-render demo-video method, forked
 * from web-inbox-video.spec.ts. Instead of recording an MP4 it installs the
 * rrweb recorder BEFORE the first navigation, walks the SAME WEB_INBOX_TOUR_STEPS
 * against the same deterministic no-LLM posture (stories/inbox-demo +
 * flows/background_notifies.yaml, nil harness), then dumps the accumulated rrweb
 * event stream for embedding as a NATIVE rrweb scene in a slidey hybrid deck
 * (slidey inlines the JSON as a data URI — the deck does NOT render this to MP4).
 *
 * This is the "user reports a bug → it surfaces in the app inbox" beat that
 * precedes the GitHub front-door act in docs/decks/dev-story-hybrid.slidey.json.
 *
 * Artifacts (all under .artifacts/web-inbox/):
 *   - web-inbox.rrweb.json          ← the captured rrweb event stream
 *   - web-inbox.rrweb.capture.json  ← viewport sidecar (width/height/dsf)
 *
 * Run at watch-speed:
 *   pnpm exec playwright test web-inbox-rrweb-capture --project=chromium
 */
import { test, expect, chromium, type Browser, type BrowserContext, type Page, type Locator } from "@playwright/test";
import path from "path";
import fs from "fs";
import {
  startWebServer,
  repoRoot,
  makeShot,
  dwell,
  cinematicGoto,
  SETTLE_MS,
  type WebServer,
} from "./_helpers/server.js";
import { installCapture, dumpCapture, writeEvents } from "./_helpers/rrweb-replay.js";
import { WEB_INBOX_TOUR_STEPS, type TourStep } from "../../src/tour/generated/web-inbox.js";

const ADDR = "127.0.0.1:7793";
const STORY_DIR = path.join(repoRoot, "stories", "inbox-demo");
const FLOW = path.join(STORY_DIR, "flows", "background_notifies.yaml");

const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "web-inbox");
const BASELINE_FRAMES_DIR = ARTIFACT_DIR;
const EVENTS_JSON = path.join(ARTIFACT_DIR, "web-inbox.rrweb.json");
const DIAG_LOG = path.join(ARTIFACT_DIR, "diagnostic.log");

// Capture viewport — matches the other dev-story-hybrid deck clips (1600x900).
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

declare global {
  interface Window {
    __startTourWithSteps?: (s: string) => void;
  }
}

test.beforeAll(async () => {
  fs.mkdirSync(ARTIFACT_DIR, { recursive: true });
  fs.mkdirSync(BASELINE_FRAMES_DIR, { recursive: true });
  fs.writeFileSync(DIAG_LOG, "");
  server = await startWebServer({ addr: ADDR, flow: FLOW, storiesDir: STORY_DIR });
});

test.afterAll(() => server?.stop());

/** Resolve an action step's real target element — first visible match. */
async function resolveTarget(page: Page, step: TourStep): Promise<Locator> {
  return page.getByTestId(step.target!).first();
}

test("web async-inbox rrweb capture (event stream)", async () => {
  test.setTimeout(300000);
  const browser: Browser = await chromium.launch({ headless: true });
  const context: BrowserContext = await browser.newContext({
    viewport: { ...VIEWPORT },
    deviceScaleFactor: 1,
  });
  const page: Page = await context.newPage();
  const shot = makeShot(BASELINE_FRAMES_DIR);

  try {
    // ── 1. Home story library, then start the tour ON it ──────────────────────
    diag("navigating home");
    await cinematicGoto(page, `${server.base}/#/`, { waitForTestId: "home-view" });

    // rrweb: start recording AFTER home paints, BEFORE the first navigation.
    await installCapture(page);
    diag("rrweb capture installed");

    await page.evaluate((stepsJson: string) => {
      window.__startTourWithSteps?.(stepsJson);
    }, JSON.stringify(WEB_INBOX_TOUR_STEPS));
    await expect(page.getByTestId("tour-overlay")).toBeVisible({ timeout: 8000 });

    // ── 2. Walk the WEB_INBOX_TOUR_STEPS (port of web-inbox-video walk) ────────
    for (const step of WEB_INBOX_TOUR_STEPS) {
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

      // Before the panel step, open the inbox panel by clicking the badge so the
      // inbox-panel / inbox-item / inbox-jump testids are present.
      if (step.id === "wi-panel") {
        await page.getByTestId("inbox-badge").click({ timeout: 8000 });
        await expect(page.getByTestId("inbox-panel")).toBeVisible({ timeout: 8000 });
        await dwell(page, SETTLE_MS);
      }

      if (step.waitForTarget) {
        await expect(page.getByTestId(step.waitForTarget).first()).toBeVisible({ timeout: 20000 });
      }

      // Anti-drift assertion: the popover must show THIS step's title.
      const titleEl = page.getByTestId("tour-title");
      const actualTitle = await titleEl.textContent({ timeout: 8000 }).catch(() => "");
      if (actualTitle !== step.title) {
        const remaining = WEB_INBOX_TOUR_STEPS.slice(WEB_INBOX_TOUR_STEPS.indexOf(step) + 1);
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
            if (m) diag(`session ${m[1]}`);
          }
          await dwell(page, 1000);
        } else {
          await target.evaluate((el) => (el as HTMLElement).click());
          if (step.id === "wi-launch") {
            await expect(page.getByTestId("inbox-badge-count")).toBeVisible({ timeout: 20000 });
            diag("inbox-badge-count visible (background job terminal → SSE)");
          }
          if (step.id === "wi-jump") {
            await expect(page.getByTestId("current-state")).toHaveText("working", { timeout: 15000 });
            diag("teleport landed on origin state: working");
          }
          await dwell(page, 1000);
        }
      }
    }

    await expect(page.getByTestId("tour-overlay")).toHaveCount(0, { timeout: 5000 });

    // ── 3. rrweb: dump the FULL accumulated stream + capture viewport ─────────
    const { events, viewport } = await dumpCapture(page);
    diag(`rrweb captured ${events.length} events @ ${viewport.width}x${viewport.height} dsf=${viewport.deviceScaleFactor}`);
    writeEvents(events, EVENTS_JSON, viewport);
    expect(events.length, "rrweb should have emitted a healthy event stream").toBeGreaterThanOrEqual(50);
  } catch (e) {
    diag(`FAILED: ${e instanceof Error ? e.stack ?? e.message : String(e)}`);
    diag(`--- server log ---\n${server?.log?.() ?? ""}`);
    await page.screenshot({ path: path.join(ARTIFACT_DIR, "99-failure.png") }).catch(() => undefined);
    throw e;
  } finally {
    await context.close();
    await browser.close();
  }

  console.log(`[web-inbox-rrweb-capture] events → ${EVENTS_JSON}`);
});
