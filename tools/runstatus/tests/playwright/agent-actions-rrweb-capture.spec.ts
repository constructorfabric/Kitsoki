/**
 * agent-actions-rrweb-capture.spec.ts — rrweb capture spec (simple all-DOM tour).
 *
 * The CAPTURE half of the rrweb capture→replay-render demo-video method (the
 * RENDER half is rrweb-replay-render.spec.ts). This does NOT replace the golden
 * live-record agent-actions-video.spec.ts — that remains the fallback for any
 * canvas/video surface (recordCanvas:false; see _helpers/rrweb-replay.ts).
 *
 * Forked from agent-actions-video.spec.ts. Keeps the SAME full live drive
 * (spawn `kitsoki web --flow`, window.__startTourWithSteps, the
 * home → session → observer agent-actions tour, watch-speed pacing) and the
 * SAME recordVideo baseline. The ONLY additions vs the golden spec are the
 * rrweb capture hooks:
 *
 *   - installCapture(page) is called right after the page is created and BEFORE
 *     the first navigation, so the rrweb full-snapshot + mutation stream covers
 *     the ENTIRE tour from the home view onward. (The kitsoki SPA is hash-routed
 *     — no full document reload between routes — so a single installCapture
 *     accumulates across #/ → #/s/... and the FULL stream, NOT a rolling
 *     buffer, is retained.)
 *   - At the very end (after the tour, before context.close) dumpEvents(page) +
 *     writeEvents → .artifacts/rrweb-eval/agent-actions/agent-actions.rrweb.json.
 *
 * Artifacts (all under .artifacts/rrweb-eval/agent-actions/):
 *   - agent-actions-baseline.mp4   ← the live screen-recording (the BASELINE)
 *   - baseline-frames/NN-*.png     ← the per-scene baseline screenshots
 *   - agent-actions.rrweb.json     ← the captured rrweb event stream
 *
 * Because the events and the baseline come from the SAME drive, they correspond
 * exactly: a later render-from-replay can be compared 1:1 against this baseline.
 *
 * Run at watch-speed:
 *   pnpm exec playwright test agent-actions-rrweb-capture --project=chromium
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
  type WebServer,
} from "./_helpers/server.js";
import { installCapture, dumpCapture, writeEvents } from "./_helpers/rrweb-replay.js";
import { AGENT_ACTIONS_TOUR_STEPS, type TourStep } from "../../src/tour/generated/agent-actions.js";

const CHAPTER_SOURCE = "features/agent-actions.yaml";

// Distinct port from the golden spec (7748) so this eval fork can run alongside
// it without racing on the same bind.
const ADDR = "127.0.0.1:7749";
const STORY_DIR = path.join(repoRoot, "stories", "bugfix");
const FLOW = path.join(STORY_DIR, "flows", "happy_llm.yaml");
const HOST_CASSETTE = path.join(STORY_DIR, "flows", "demo.cassette.yaml");

// Eval artifacts live under .artifacts/rrweb-eval/agent-actions/ (NOT the
// golden .artifacts/agent-actions/).
const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "rrweb-eval", "agent-actions");
const BASELINE_FRAMES_DIR = path.join(ARTIFACT_DIR, "baseline-frames");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "_baseline-video");
const EVENTS_JSON = path.join(ARTIFACT_DIR, "agent-actions.rrweb.json");
const DIAG_LOG = path.join(ARTIFACT_DIR, "diagnostic.log");

// Recorded transcript event counts the affordance badge shows.
const TASK_EVENTS = 18;
const DECIDE_EVENTS = 8;

// Capture viewport — the later replay-render MUST use this same size + DSF.
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
  server = await startWebServer({ addr: ADDR, flow: FLOW, hostCassette: HOST_CASSETTE, storiesDir: STORY_DIR });
});

test.afterAll(() => server?.stop());

/** Resolve an action step's real target element — first visible match. */
async function resolveTarget(page: Page, step: TourStep): Promise<Locator> {
  return page.getByTestId(step.target!).first();
}

async function waitForOracleTranscripts(sessionId: string, timeoutMs: number): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  let withRef = 0;
  while (Date.now() < deadline) {
    const tr = await server
      .rpc<{ events?: Array<{ msg: string; attrs?: Record<string, unknown> }> }>(
        "runstatus.session.trace",
        { session_id: sessionId }
      )
      .catch(() => ({ events: [] as Array<{ msg: string; attrs?: Record<string, unknown> }> }));
    withRef = (tr.events ?? []).filter(
      (e) => e.msg === "oracle.call.complete" && !!(e.attrs && e.attrs["transcript_ref"])
    ).length;
    diag(`waitForOracleTranscripts: ${withRef} call(s) with transcript_ref`);
    if (withRef >= 2) return;
    await new Promise((r) => setTimeout(r, 500));
  }
  throw new Error(`oracle transcripts never settled: only ${withRef} call(s) with transcript_ref after ${timeoutMs}ms`);
}

async function openDrawerForCall(page: Page, wantEvents: number): Promise<boolean> {
  const rows = page.getByTestId("trace-event-row");
  const count = await rows.count();
  diag(`openDrawerForCall(${wantEvents}): ${count} trace-event-rows`);
  for (let i = 0; i < Math.min(count, 40); i++) {
    const row = rows.nth(i);
    const header = row.locator(".trace-timeline__row-main");
    await header.click({ timeout: 4000 }).catch(() => undefined);
    const aff = page.getByTestId("agent-actions-affordance");
    const affCount = await aff.count();
    for (let a = 0; a < affCount; a++) {
      const txt = (await aff.nth(a).textContent({ timeout: 1500 }).catch(() => "")) ?? "";
      if (txt.includes(`(${wantEvents})`)) {
        diag(`  row ${i}: matched affordance "${txt.trim()}"`);
        await aff.nth(a).click({ timeout: 4000 });
        const drawer = page.getByTestId("agent-actions-drawer");
        const ok = await drawer
          .first()
          .isVisible({ timeout: 6000 })
          .catch(() => false);
        diag(`  drawer visible: ${ok}`);
        return ok;
      }
    }
    await header.click({ timeout: 4000 }).catch(() => undefined);
  }
  diag(`openDrawerForCall(${wantEvents}): no matching call found`);
  return false;
}

test("agent action transcripts rrweb capture (baseline + event stream)", async () => {
  test.setTimeout(300000);
  const browser: Browser = await chromium.launch({ headless: true });
  const context: BrowserContext = await browser.newContext({
    viewport: { ...VIEWPORT },
    recordVideo: { dir: VIDEO_DIR, size: { ...VIEWPORT } },
  });
  const page: Page = await context.newPage();
  const video = page.video();
  const shot = makeShot(BASELINE_FRAMES_DIR);

  const chapters = new ChapterRecorder();
  let sessionId = "";

  try {
    // ── 1. Open the home story library and start the tour ON it ──────────────
    diag("navigating home");
    await cinematicGoto(page, `${server.base}/#/`, { waitForTestId: "home-view" });

    // ── rrweb: start recording AFTER the home view has painted (so the first
    // full snapshot already carries the home-view DOM) and BEFORE the first
    // tour-driven navigation. A single install accumulates the FULL stream
    // across the hash-routed tour. ──────────────────────────────────────────
    await installCapture(page);
    diag("rrweb capture installed");

    await page.evaluate((stepsJson: string) => {
      (window as unknown as { __startTourWithSteps?: (s: string) => void })
        .__startTourWithSteps?.(stepsJson);
    }, JSON.stringify(AGENT_ACTIONS_TOUR_STEPS));
    await expect(page.getByTestId("tour-overlay")).toBeVisible({ timeout: 8000 });

    // ── 2. Walk the AGENT_ACTIONS_TOUR_STEPS (intro + drawer) ────────────────
    for (const step of AGENT_ACTIONS_TOUR_STEPS) {
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

      if (step.id === "aa-affordance") {
        const opened = await openTaskDetail(page);
        diag(`  aa-affordance: task detail opened=${opened}`);
        await dwell(page, SETTLE_MS);
      }
      if (step.id === "aa-row") {
        const toolHeaders = page.locator(
          '[data-testid="agent-action-row"][data-kind="tool"] [data-testid="agent-action-row-header"]'
        );
        const n = await toolHeaders.count();
        diag(`  aa-row: expanding ${n} tool rows`);
        for (let i = 0; i < n; i++) {
          await toolHeaders.nth(i).evaluate((el) => (el as HTMLElement).click()).catch(() => undefined);
          await dwell(page, 300);
        }
      }
      if (step.id === "aa-decide-guardrail") {
        const ok = await openDrawerForCall(page, DECIDE_EVENTS);
        diag(`  aa-decide-guardrail: decide drawer opened=${ok}`);
        await dwell(page, SETTLE_MS);
      }

      if (step.waitForTarget) {
        await expect(page.getByTestId(step.waitForTarget).first()).toBeVisible({ timeout: 15000 });
      }

      const titleEl = page.getByTestId("tour-title");
      const actualTitle = await titleEl.textContent({ timeout: 8000 }).catch(() => "");
      if (actualTitle !== step.title) {
        const remaining = AGENT_ACTIONS_TOUR_STEPS.slice(AGENT_ACTIONS_TOUR_STEPS.indexOf(step) + 1);
        const isOnNext = remaining.some((s) => s.title === actualTitle);
        if (isOnNext) {
          diag(`  drift-skip: overlay on "${actualTitle}"`);
          continue;
        }
      }
      await expect(titleEl).toHaveText(step.title, { timeout: 12000 });

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
            if (sessionId) {
              await server.rpc("runstatus.session.patch_world", {
                session_id: sessionId,
                patch: {
                  judge_mode: "llm",
                  ticket_id: "TKT-demo",
                  ticket_title: "Demo agent-actions run",
                  workdir: ".worktrees/tkt-demo",
                  workspace_id: "ws-demo",
                  thread: "TKT-demo",
                  base_branch: "main",
                  feature_branch: "fix/tkt-demo",
                  judge_confidence_threshold: 0.8,
                },
              });
              await server.rpc("runstatus.session.submit", {
                session_id: sessionId,
                intent: "start",
                slots: {},
              });
              await waitForOracleTranscripts(sessionId, 40000);
              await expect(
                page
                  .getByTestId("trace-event-row")
                  .filter({ hasText: "oracle.decide" })
                  .first()
              ).toBeVisible({ timeout: 15000 });
            }
          }
          await dwell(page, 1000);
        } else {
          await target.evaluate((el) => (el as HTMLElement).click());
          await dwell(page, 1000);
        }
      }
    }

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
    await context.close();
    const mp4 = await saveVideoAsMp4(video, ARTIFACT_DIR, "agent-actions-baseline");
    writeChapters(mp4, chapters.list());
    await browser.close();
  }

  const pngs = fs.readdirSync(BASELINE_FRAMES_DIR).filter((f) => f.endsWith(".png"));
  console.log(`[agent-actions-rrweb-capture] baseline frames (${pngs.length}) in ${BASELINE_FRAMES_DIR}`);
  console.log(`[agent-actions-rrweb-capture] events → ${EVENTS_JSON}`);
});

async function openTaskDetail(page: Page): Promise<boolean> {
  const rows = page.getByTestId("trace-event-row");
  const count = await rows.count();
  diag(`openTaskDetail: ${count} trace-event-rows`);
  for (let i = 0; i < Math.min(count, 40); i++) {
    const row = rows.nth(i);
    const header = row.locator(".trace-timeline__row-main");
    await header.click({ timeout: 4000 }).catch(() => undefined);
    const aff = page.getByTestId("agent-actions-affordance");
    const affCount = await aff.count();
    for (let a = 0; a < affCount; a++) {
      const txt = (await aff.nth(a).textContent({ timeout: 1500 }).catch(() => "")) ?? "";
      if (txt.includes(`(${TASK_EVENTS})`)) {
        diag(`  openTaskDetail row ${i}: matched "${txt.trim()}"`);
        return true;
      }
    }
    await header.click({ timeout: 4000 }).catch(() => undefined);
  }
  diag(`openTaskDetail: no task call found`);
  return false;
}
