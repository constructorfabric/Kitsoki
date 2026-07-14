/**
 * Agent-action-transcripts feature-spotlight video demo.
 *
 * Drives the dedicated agent-actions tour against a real `kitsoki web` server in
 * the deterministic no-LLM posture (--flow happy_llm.yaml + the demo cassette)
 * and records a video + per-scene screenshots to .artifacts/agent-actions/.
 *
 * Like trace-features-video.spec.ts, this spec runs ONLY the
 * AGENT_ACTIONS_TOUR_STEPS from src/tour/generated/agent-actions.ts via
 * window.__startTourWithSteps. The tour drives the whole video: it opens on the
 * home story library and its route-match action steps navigate home → new
 * session → observer, so even the intro is tour-narrated rather than silent
 * spec orchestration.
 *
 * The demo cassette (stories/bugfix/flows/demo.cassette.yaml) carries the rich
 * recorded transcripts: an 18-event task arc (call_id 4e96533378e89461) and an
 * 8-event decide arc (call_id e5129592efb9250c) with the _kitsoki reject / nudge
 * / accept rows. session.new -> patch_world{judge_mode:"llm"} -> submit{start}
 * produces both calls with their transcript_ref pointers.
 *
 * Validate fast (no dwells):
 *   WEB_CHAT_PACE=0 pnpm exec playwright test agent-actions-video --project=chromium
 * Record at watch-speed:
 *   pnpm exec playwright test agent-actions-video --project=chromium
 *
 * NOTE: the harness suppresses Playwright stdout, so per-step progress and any
 * failure context is also written to .artifacts/agent-actions/diagnostic.log.
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
  demoAddr,
  SETTLE_MS,
  type WebServer,
} from "./_helpers/server.js";
import { cameraContext } from "./_helpers/camera.js";
import { AGENT_ACTIONS_TOUR_STEPS, type TourStep } from "../../src/tour/generated/agent-actions.js";

// The feature-catalog source of truth for this tour: each step becomes a chapter
// (source_ref kind=tour) whose [start,end] window is the recorded dwell.
const CHAPTER_SOURCE = "features/agent-actions.yaml";

// 7748 — distinct from tour-onboarding.spec.ts (7747) and trace-features (7746)
// so parallel spec files never race on the same port bind.
const ADDR = demoAddr(7748);
// Same server posture as the trace-features spec: the bugfix story under the
// happy_llm flow + the demo cassette, so the trace carries real
// agent.call.complete events whose transcript_ref pointers back the drawer.
const STORY_DIR = path.join(repoRoot, "stories", "bugfix");
const FLOW = path.join(STORY_DIR, "flows", "happy_llm.yaml");
const HOST_CASSETTE = path.join(STORY_DIR, "flows", "demo.cassette.yaml");
const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "agent-actions");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "video");
const DIAG_LOG = path.join(ARTIFACT_DIR, "diagnostic.log");

// The recorded transcript event counts the affordance badge shows. The task
// call has 18 events; the decide call has 8. Selecting a call's row by its
// badge count is robust to trace ordering.
const TASK_EVENTS = 18;
const DECIDE_EVENTS = 8;

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

/**
 * Poll the trace RPC until at least two agent.call.complete events carry a
 * transcript_ref (the task + decide calls), so the tour never starts before the
 * drawer-backing data has settled. Deterministic replacement for a fixed sleep.
 */
async function waitForAgentTranscripts(sessionId: string, timeoutMs: number): Promise<void> {
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
      (e) => e.msg === "agent.call.complete" && !!(e.attrs && e.attrs["transcript_ref"])
    ).length;
    diag(`waitForAgentTranscripts: ${withRef} call(s) with transcript_ref`);
    if (withRef >= 2) return;
    await new Promise((r) => setTimeout(r, 500));
  }
  throw new Error(`agent transcripts never settled: only ${withRef} call(s) with transcript_ref after ${timeoutMs}ms`);
}

/**
 * Open the AgentDetail drawer for the agent.call.complete whose captured
 * transcript has `wantEvents` events. Strategy mirrors the trace spec's
 * row-clicking loop: expand trace-event-rows in turn, and for each one check
 * whether the now-visible agent-actions affordance reads "Agent actions
 * (wantEvents)". When it matches, click the affordance to open the drawer.
 *
 * Returns true once the drawer for that call is open. Collapses any non-match
 * it expanded so only the target call's detail stays open.
 */
async function openDrawerForCall(page: Page, wantEvents: number): Promise<boolean> {
  const rows = page.getByTestId("trace-event-row");
  const count = await rows.count();
  diag(`openDrawerForCall(${wantEvents}): ${count} trace-event-rows`);
  for (let i = 0; i < Math.min(count, 40); i++) {
    const row = rows.nth(i);
    // Click the row HEADER, not the row: an expanded row's body fills the
    // element's center and swallows clicks (@click.stop), so a center-click
    // on an expanded row never collapses it — leaving e.g. the session.story
    // base64 wall open across the spotlight frames.
    const header = row.locator(".trace-timeline__row-main");
    // Dispatch the DOM click directly (not a hit-test .click()): a pre-step
    // hook can run while the tour popover is still settling, which can make
    // hit-test clicks flaky. el.click() fires the row header's @click directly.
    await header.evaluate((el) => (el as HTMLElement).click()).catch(() => undefined);
    // The affordance only renders for agent.call.complete rows that carry a
    // transcript_ref. Look for the one whose badge count matches.
    const aff = page.getByTestId("agent-actions-affordance");
    const affCount = await aff.count();
    for (let a = 0; a < affCount; a++) {
      const txt = (await aff.nth(a).textContent({ timeout: 1500 }).catch(() => "")) ?? "";
      if (txt.includes(`(${wantEvents})`)) {
        diag(`  row ${i}: matched affordance "${txt.trim()}"`);
        // DOM click (hit-test-proof — see header note above): open this call's
        // agent-actions drawer regardless of overlay placement.
        await aff.nth(a).evaluate((el) => (el as HTMLElement).click());
        const drawer = page.getByTestId("agent-actions-drawer");
        const ok = await drawer
          .first()
          .isVisible({ timeout: 6000 })
          .catch(() => false);
        diag(`  drawer visible: ${ok}`);
        return ok;
      }
    }
    // Not a match — collapse this row again so the next expand is clean.
    // Dispatch the DOM click directly (not a hit-test .click()): a pre-step
    // hook can run while the tour popover is still settling, which can make
    // hit-test clicks flaky. el.click() fires the row header's @click directly.
    await header.evaluate((el) => (el as HTMLElement).click()).catch(() => undefined);
  }
  diag(`openDrawerForCall(${wantEvents}): no matching call found`);
  return false;
}

test("agent action transcripts feature-spotlight video", async () => {
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

  // Carries the session id once the intro's "New session" step creates the run.
  let sessionId = "";

  try {
    // ── 1. Open the home story library and start the tour ON it ──────────────
    // The whole video is tour-driven: rather than silently flashing home -> chat
    // -> observer before the overlay appears, we start the tour on home and let
    // its route-match action steps perform the navigation, each narrated.
    diag("navigating home");
    await cinematicGoto(page, `${server.base}/#/`, { waitForTestId: "home-view" });

    await page.evaluate((stepsJson: string) => {
      (window as unknown as { __startTourWithSteps?: (s: string) => void })
        .__startTourWithSteps?.(stepsJson);
    }, JSON.stringify(AGENT_ACTIONS_TOUR_STEPS));
    await expect(page.getByTestId("tour-overlay")).toBeVisible({ timeout: 8000 });

    // ── 2. Walk the AGENT_ACTIONS_TOUR_STEPS (intro + drawer) ────────────────
    for (const step of AGENT_ACTIONS_TOUR_STEPS) {
      diag(`step ${step.id}`);
      // Mirror the overlay's route-guard. The intro steps are home/interactive;
      // the drawer steps are route "any" on the observer.
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
      // Before the TASK-call steps, open the 18-event task call's drawer so the
      // affordance / drawer / row / waterfall / accrual testids are present.
      if (step.id === "aa-affordance") {
        // Open the task call's detail pane (affordance is the step target, but
        // the row must be expanded first so the affordance renders).
        const opened = await openTaskDetail(page);
        diag(`  aa-affordance: task detail opened=${opened}`);
        // Settle so the row-scan flicker resolves into a composed detail pane
        // before the spotlight lands on it.
        await dwell(page, SETTLE_MS);
      }
      // Before the "Full tool I/O" step, expand the tool rows so the frame shows
      // real tool input/output (the Read file_path, the Edit old/new diff, the
      // Bash command + its stdout) rather than collapsed name-only headers — the
      // headline of the feature, and what the aa-row narration describes.
      if (step.id === "aa-row") {
        const toolHeaders = page.locator(
          '[data-testid="agent-action-row"][data-kind="tool"] [data-testid="agent-action-row-header"]'
        );
        const n = await toolHeaders.count();
        diag(`  aa-row: expanding ${n} tool rows`);
        for (let i = 0; i < n; i++) {
          await toolHeaders.nth(i).evaluate((el) => (el as HTMLElement).click()).catch(() => undefined);
          // Cascade the expansions with a short beat instead of popping all four
          // open in a single frame — reads as the rows unfolding, not a flash.
          await dwell(page, 300);
        }
      }
      // Before the DECIDE-call steps, switch to the 8-event decide call's drawer
      // so guardrail-row / nudge-row are present.
      if (step.id === "aa-decide-guardrail") {
        const ok = await openDrawerForCall(page, DECIDE_EVENTS);
        diag(`  aa-decide-guardrail: decide drawer opened=${ok}`);
        // Settle so the decide drawer composes before the guardrail spotlight.
        await dwell(page, SETTLE_MS);
      }

      // Honor DOM-presence preconditions.
      if (step.waitForTarget) {
        await expect(page.getByTestId(step.waitForTarget).first()).toBeVisible({ timeout: 15000 });
      }

      // Anti-drift assertion: the popover must show THIS step's title.
      const titleEl = page.getByTestId("tour-title");
      const actualTitle = await titleEl.textContent({ timeout: 8000 }).catch(() => "");
      if (actualTitle !== step.title) {
        // The overlay may have skipped this step (e.g. target absent).
        const remaining = AGENT_ACTIONS_TOUR_STEPS.slice(AGENT_ACTIONS_TOUR_STEPS.indexOf(step) + 1);
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
        // Let the spotlight animation move to the next target before asserting.
        await dwell(page, 700);
      } else {
        const target = await resolveTarget(page, step);
        await target.scrollIntoViewIfNeeded().catch(() => undefined);
        if (step.advance === "route-match") {
          // Intro navigation (New session / Observe). The chat view is static at
          // this point — the submit happens only after we reach the observer —
          // so a hit-test click goes cleanly through the overlay's hole. Wait for
          // the URL to actually change before the next iteration asserts.
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
              // On the observer now: patch the world + submit so BOTH agent
              // calls (task + decide) complete AND carry a transcript_ref before
              // the drawer steps spotlight them. Poll the trace RPC to a deadline
              // rather than a flat sleep — the affordance never renders until the
              // trace has settled.
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
              await waitForAgentTranscripts(sessionId, 40000);
              // The RPC settle above proves the SERVER trace carries both
              // calls; the page renders them only after the next SSE poll
              // tick (500ms) plus a Vue frame. The drawer steps scan the
              // timeline rows in a single un-retried pass, so also wait for
              // the OBSERVER to catch up. The decide row is the LAST of the
              // two calls, so its presence implies the task row too.
              await expect(
                page
                  .getByTestId("trace-event-row")
                  .filter({ hasText: "agent.decide" })
                  .first()
              ).toBeVisible({ timeout: 15000 });
            }
          }
          await dwell(page, 1000);
        } else {
          // click-target drawer control. The overlay paints a backdrop + popover
          // (leaving a click-through hole for the target), but that hole's
          // geometry can lag for a control inside a freshly-rendered detail pane,
          // so a hit-test click gets intercepted. Dispatch the DOM click directly:
          // it fires the control's own @click AND the overlay's capture-phase
          // advance listener (bound on the same element), so the tour advances
          // regardless of paint order.
          await target.evaluate((el) => (el as HTMLElement).click());
          // Longer settle for action steps: drawer toggles + tab switches repaint.
          await dwell(page, 1000);
        }
      }
    }

    // The final aa-done step's "Done" closes the tour.
    await expect(page.getByTestId("tour-overlay")).toHaveCount(0, { timeout: 5000 });
  } catch (e) {
    diag(`FAILED: ${e instanceof Error ? e.stack ?? e.message : String(e)}`);
    diag(`--- server log ---\n${server?.log?.() ?? ""}`);
    throw e;
  } finally {
    await context.close();
    const mp4 = await saveVideoAsMp4(video, ARTIFACT_DIR, "agent-actions-demo");
    // Emit the producer-agnostic chapter sidecar beside the MP4: each tour
    // step → one chapter with source_ref kind=tour.
    writeChapters(mp4, chapters.list());
    await browser.close();
  }

  const pngs = fs.readdirSync(ARTIFACT_DIR).filter((f) => f.endsWith(".png"));
  console.log(`[agent-actions-video] screenshots (${pngs.length}) in ${ARTIFACT_DIR}`);
});

/**
 * Open the TASK call's AgentDetail by expanding its trace-event-row, so the
 * agent-actions-affordance (the aa-affordance step's target) is present for the
 * tour to click. Reuses openDrawerForCall's matching logic but stops BEFORE
 * clicking the affordance — the tour step itself performs that click.
 */
async function openTaskDetail(page: Page): Promise<boolean> {
  const rows = page.getByTestId("trace-event-row");
  const count = await rows.count();
  diag(`openTaskDetail: ${count} trace-event-rows`);
  for (let i = 0; i < Math.min(count, 40); i++) {
    const row = rows.nth(i);
    // Header click, not row click — see openDrawerForCall: an expanded row's
    // body swallows center-clicks, so collapsing a non-match needs the header.
    const header = row.locator(".trace-timeline__row-main");
    // Dispatch the DOM click directly (not a hit-test .click()): a pre-step
    // hook can run while the tour popover is still settling, which can make
    // hit-test clicks flaky. el.click() fires the row header's @click directly.
    await header.evaluate((el) => (el as HTMLElement).click()).catch(() => undefined);
    const aff = page.getByTestId("agent-actions-affordance");
    const affCount = await aff.count();
    for (let a = 0; a < affCount; a++) {
      const txt = (await aff.nth(a).textContent({ timeout: 1500 }).catch(() => "")) ?? "";
      if (txt.includes(`(${TASK_EVENTS})`)) {
        diag(`  openTaskDetail row ${i}: matched "${txt.trim()}"`);
        return true; // leave the affordance present; the tour step clicks it.
      }
    }
    // Dispatch the DOM click directly (not a hit-test .click()): a pre-step
    // hook can run while the tour popover is still settling, which can make
    // hit-test clicks flaky. el.click() fires the row header's @click directly.
    await header.evaluate((el) => (el as HTMLElement).click()).catch(() => undefined);
  }
  diag(`openTaskDetail: no task call found`);
  return false;
}
