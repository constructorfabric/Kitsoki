/**
 * Meta-chat persistence + launcher-status feature-spotlight video demo.
 *
 * Proves the fix behind branch fix/meta-chat-persistence-and-status:
 *   1. A meta-mode (Story Q&A) turn streams a thinking + tool feed in the
 *      overlay (meta-row-streaming).
 *   2. CLOSING the overlay while the turn streams leaves it running — the
 *      launcher grows a working badge (meta-status-busy, ⟳).
 *   3. REOPENING resumes the SAME conversation, still streaming, as if it was
 *      never closed (persistence).
 *   4. When a turn FINISHES while the overlay is closed, the launcher shows a
 *      ready badge (meta-status-ready, ●); reopening clears it.
 *   5. (stretch, required:false) two modes at once — one ● waiting, one ⟳
 *      working — visible as both launcher badges.
 *
 * Posture: deterministic no-LLM. `kitsoki web --flow happy_llm.yaml` runs the
 * meta-mode StubOracleCaller; KITSOKI_META_STREAM_DELAY_MS paces its stream so a
 * close-mid-stream is reliably filmable. NEVER a real LLM.
 *
 * ONE annotation style throughout — every narrated moment is a TOUR POPOVER, no
 * banner captions. The four-step intro is walked through the tour overlay
 * (home → story → new session → chat). From mc-open-mode onward the spec PERFORMS
 * each open/close/send action, then jumps the overlay to the matching step
 * (window.__tourGoTo) and asserts its title against META_CHAT_TOUR_STEPS. The
 * tour overlay (z 1500, popover 1600) renders ABOVE the meta overlay (z 1000), so
 * the popover can spotlight meta-overlay / the streaming row / the launcher
 * badges — there is no z-index blocker.
 *
 * Validate fast (no dwells, tiny but non-zero stream delay so the bubble exists
 * long enough to assert close-mid-stream):
 *   WEB_CHAT_PACE=0 KITSOKI_META_STREAM_DELAY_MS=120 \
 *     pnpm exec playwright test meta-chat-video --project=chromium
 * Record at watch-speed:
 *   pnpm exec playwright test meta-chat-video --project=chromium
 *
 * NOTE: the harness suppresses Playwright stdout, so per-step progress + any
 * failure context is also written to .artifacts/meta-chat/diagnostic.log.
 */
import { test, expect, chromium, type Browser, type BrowserContext, type Page } from "@playwright/test";
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
import {
  META_CHAT_TOUR_STEPS,
  MC_FIRST_CHOREO_STEP,
  type TourStep,
} from "../../src/tour/meta-chat-manifest.js";
import { cameraContext } from "./_helpers/camera.js";

const CHAPTER_SOURCE = "tools/runstatus/src/tour/meta-chat-manifest.ts";

// 7765 — the brief reserves this port (7740–7762 are taken).
const ADDR = "127.0.0.1:7765";
const STORY_DIR = path.join(repoRoot, "stories", "bugfix");
const FLOW = path.join(STORY_DIR, "flows", "happy_llm.yaml");
const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "meta-chat");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "video");
const DIAG_LOG = path.join(ARTIFACT_DIR, "diagnostic.log");

const ASK_QUESTION = "What does this story do, and where is the run right now?";
const ASK2_QUESTION = "Which rooms can the autofix agent reach from idle?";

// The intro steps are walked through the overlay's Next button; from this id on
// the spec drives the choreography and syncs the overlay manually.
const FIRST_CHOREO_IDX = META_CHAT_TOUR_STEPS.findIndex((s) => s.id === MC_FIRST_CHOREO_STEP);
const INTRO_STEPS = META_CHAT_TOUR_STEPS.slice(0, FIRST_CHOREO_IDX);
const STEP_BY_ID: Record<string, TourStep> = Object.fromEntries(
  META_CHAT_TOUR_STEPS.map((s) => [s.id, s])
);

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
  // Pace the stub meta stream so a close-mid-stream is deterministic and
  // filmable. The stub emits think -> 2x -> Read -> 2x -> 4x -> reply words at
  // 1x each, so the turn is in-flight for well over 8x the delay before the
  // reply even starts — a wide window to close + reopen on camera. Keep a small
  // floor even in fast-validation mode so the streaming bubble reliably EXISTS
  // when we assert close-mid-stream (at 0 the turn finishes before the click).
  if (process.env.KITSOKI_META_STREAM_DELAY_MS === undefined) {
    process.env.KITSOKI_META_STREAM_DELAY_MS =
      process.env.WEB_CHAT_PACE === "0" ? "120" : "380";
  }
  diag(`KITSOKI_META_STREAM_DELAY_MS=${process.env.KITSOKI_META_STREAM_DELAY_MS}`);
  server = await startWebServer({ addr: ADDR, flow: FLOW, storiesDir: STORY_DIR });
});

test.afterAll(() => server?.stop());

/** Wait for the launcher working badge (⟳) to appear, with a timeout. */
async function expectBusyBadge(page: Page, timeout = 8000): Promise<void> {
  await expect(page.getByTestId("meta-status-busy")).toBeVisible({ timeout });
}

/** Wait for the launcher ready badge (●) to appear, with a timeout. */
async function expectReadyBadge(page: Page, timeout = 30000): Promise<void> {
  await expect(page.getByTestId("meta-status-ready")).toBeVisible({ timeout });
}

/**
 * Click a meta control by testid via a DOM-dispatched click. The tour popover
 * (z 1600) renders ABOVE the meta overlay / launcher menu, so a hit-test click
 * is intercepted by the popover; dispatching the click directly fires the
 * control's own handler regardless of paint order (the golden agent-actions
 * spec's technique for controls under the tour backdrop).
 */
async function metaClick(page: Page, testid: string, timeout = 10000): Promise<void> {
  const el = page.getByTestId(testid).first();
  await expect(el).toBeAttached({ timeout });
  await el.evaluate((node) => (node as HTMLElement).click());
}

/** Fill a meta composer input via the DOM (the tour popover covers it). */
async function metaFill(page: Page, testid: string, value: string): Promise<void> {
  const el = page.getByTestId(testid).first();
  await expect(el).toBeAttached({ timeout: 8000 });
  await el.evaluate((node, v) => {
    const input = node as HTMLInputElement | HTMLTextAreaElement;
    input.focus();
    input.value = v as string;
    input.dispatchEvent(new Event("input", { bubbles: true }));
    input.dispatchEvent(new Event("change", { bubbles: true }));
  }, value);
}

/**
 * Sync the tour overlay to a choreography step and narrate it. The spec has
 * already performed the action that makes the step's target real; this jumps the
 * overlay's popover to that step (window.__tourGoTo), asserts the popover title
 * matches the manifest (drift guard, like the golden agent-actions spec), opens
 * the chapter, dwells, and screenshots. The step stays `kind:"explain"` so the
 * overlay never advances it on its own.
 */
async function narrate(
  page: Page,
  chapters: ChapterRecorder,
  shot: (p: Page, name: string) => Promise<void>,
  stepId: string,
  opts: { skipShot?: boolean } = {}
): Promise<void> {
  const step = STEP_BY_ID[stepId];
  if (!step) throw new Error(`unknown choreography step: ${stepId}`);
  diag(`narrate ${stepId}`);
  await page.evaluate((id: string) => {
    (window as unknown as { __tourGoTo?: (s: string) => void }).__tourGoTo?.(id);
  }, stepId);
  // The overlay must still be active for the popover to render.
  await expect(page.getByTestId("tour-overlay")).toBeVisible({ timeout: 8000 });
  // Drift guard: the popover shows THIS step's title.
  await expect(page.getByTestId("tour-title")).toHaveText(step.title, { timeout: 12000 });
  chapters.open(step.id, step.title, CHAPTER_SOURCE);
  await dwell(page, step.dwellMs ?? 3000);
  // A caller that captured the labeled frame at a precise moment (e.g. the
  // timing-sensitive both-badges window) passes skipShot so the dwell above
  // doesn't overwrite that frame with a later state.
  if (!opts.skipShot) await shot(page, step.id);
}

test("meta-chat persistence + launcher status feature-spotlight video", async () => {
  test.setTimeout(300000);
  const browser: Browser = await chromium.launch({ headless: true });
  const context: BrowserContext = await browser.newContext(
    cameraContext({ recordVideoDir: VIDEO_DIR }),
  );
  const page: Page = await context.newPage();
  const video = page.video();
  const shot = makeShot(ARTIFACT_DIR);
  const chapters = new ChapterRecorder();

  // Verdicts collected during the walk, asserted AFTER it, so a failing
  // behaviour still yields a complete recording that SHOWS the problem.
  let sawStreaming = false;
  let sawBusyOnClose = false;
  let resumedStreaming = false;
  let resumedTranscriptIntact = false;
  let sawReadyWhenDone = false;
  let readyClearedOnReopen = false;
  let sawBothBadges = false;

  let sessionId = "";

  try {
    // ── 1. Tour-narrated intro: home -> story -> new session -> chat ─────────
    diag("navigating home");
    await cinematicGoto(page, `${server.base}/#/`, { waitForTestId: "home-view" });

    await page.evaluate((stepsJson: string) => {
      (window as unknown as { __startTourWithSteps?: (s: string) => void })
        .__startTourWithSteps?.(stepsJson);
    }, JSON.stringify(META_CHAT_TOUR_STEPS));
    await expect(page.getByTestId("tour-overlay")).toBeVisible({ timeout: 8000 });

    for (const step of INTRO_STEPS) {
      diag(`intro step ${step.id}`);
      const url = page.url();
      const routeKind = url.includes("/chat")
        ? "interactive"
        : url.match(/#\/s\/[0-9a-f-]{36}$/)
          ? "any"
          : "home";
      if (step.route !== "any" && step.route !== routeKind) {
        diag(`  route-skip (${routeKind})`);
        continue;
      }
      if (step.waitForTarget) {
        await expect(page.getByTestId(step.waitForTarget).first()).toBeVisible({ timeout: 20000 });
      }
      const titleEl = page.getByTestId("tour-title");
      await expect(titleEl).toHaveText(step.title, { timeout: 12000 });
      chapters.open(step.id, step.title, CHAPTER_SOURCE);
      await dwell(page, step.dwellMs ?? 3000);
      await shot(page, step.id);

      if (step.kind === "explain") {
        await page.getByTestId("tour-next").click();
        await dwell(page, 700);
      } else {
        const target = page.getByTestId(step.target!).first();
        await target.scrollIntoViewIfNeeded().catch(() => undefined);
        await target.click();
        await page.waitForTimeout(300);
        if (step.advanceRoute === "interactive") {
          await page.waitForURL(/#\/s\/[0-9a-f-]{36}\/chat$/, { timeout: 15000 });
          const m = page.url().match(/\/s\/([0-9a-f-]{36})\/chat$/);
          if (m) {
            sessionId = m[1];
            diag(`session ${sessionId}`);
          }
        }
        await dwell(page, 1000);
      }
    }

    // The tour overlay STAYS active through the choreography — the intro's last
    // step (mc-launcher) is advanced via Next above, leaving the overlay on the
    // first choreography step. From here the spec performs each action and syncs
    // the overlay to the matching step via narrate().
    await expect(page.getByTestId("tour-overlay")).toBeVisible({ timeout: 8000 });

    // ── 2. Open the ✦ Meta launcher dropdown and pick Story Q&A ──────────────
    // Use DOM-dispatched clicks for every meta control: the tour overlay stays
    // active through the choreography, and its popover (z 1600) sits above the
    // launcher menu / overlay, so a hit-test click is intercepted.
    diag("open meta launcher");
    if (!(await page.getByTestId("meta-mode-story-ask").isVisible().catch(() => false))) {
      await metaClick(page, "meta-button");
    }
    await expect(page.getByTestId("meta-mode-story-ask")).toBeEnabled({ timeout: 10000 });
    await metaClick(page, "meta-mode-story-ask");
    await expect(page.getByTestId("meta-overlay")).toBeVisible({ timeout: 8000 });
    await dwell(page, SETTLE_MS);
    await narrate(page, chapters, shot, "mc-open-mode");

    // Scenario 1 — type a question, send, watch the streaming bubble appear.
    diag("send meta question 1");
    await metaFill(page, "meta-composer-input", ASK_QUESTION);
    await metaClick(page, "meta-composer-send");
    const streamingBubble = page.getByTestId("meta-row-streaming");
    try {
      await expect(streamingBubble).toBeVisible({ timeout: 8000 });
      sawStreaming = true;
    } catch {
      sawStreaming = false;
    }
    diag(`scenario1 sawStreaming=${sawStreaming}`);
    await narrate(page, chapters, shot, "mc-stream");

    // Scenario 2 — CLOSE the overlay mid-stream; launcher shows the ⟳ badge.
    // The ⟳ badge (meta.anyBusy) renders on the always-present launcher as soon
    // as the turn is in flight — even with the overlay open — so wait for it
    // BEFORE closing. That both proves the turn is mid-stream and removes the
    // race where a fast stub stream finishes before a post-close poll fires.
    diag("close overlay mid-stream");
    try {
      await expectBusyBadge(page, 8000);
      await metaClick(page, "meta-close");
      await expect(page.getByTestId("meta-overlay")).toHaveCount(0, { timeout: 5000 });
      // The turn keeps streaming after close, so the badge persists.
      sawBusyOnClose = await page.getByTestId("meta-status-busy").isVisible().catch(() => false);
    } catch {
      sawBusyOnClose = false;
      // Still close the overlay so the rest of the walk proceeds.
      await metaClick(page, "meta-close").catch(() => undefined);
      await expect(page.getByTestId("meta-overlay")).toHaveCount(0, { timeout: 5000 }).catch(() => undefined);
    }
    diag(`scenario2 sawBusyOnClose=${sawBusyOnClose}`);
    await narrate(page, chapters, shot, "mc-close-busy");

    // Scenario 3 — REOPEN; the same conversation is intact and still streaming.
    diag("reopen overlay mid-stream");
    await metaClick(page, "meta-button");
    await metaClick(page, "meta-mode-story-ask");
    await expect(page.getByTestId("meta-overlay")).toBeVisible({ timeout: 8000 });
    // The user turn must still be present (transcript persisted).
    const userRows = page.getByTestId("meta-row-user");
    resumedTranscriptIntact =
      (await userRows.count().catch(() => 0)) >= 1 &&
      ((await userRows.first().textContent().catch(() => "")) ?? "").includes("What does this story do");
    resumedStreaming = await streamingBubble.isVisible().catch(() => false);
    diag(`scenario3 transcriptIntact=${resumedTranscriptIntact} stillStreaming=${resumedStreaming}`);
    await narrate(page, chapters, shot, "mc-reopen");

    // Let this turn finish while we watch, so the overlay shows the resolved
    // reply (consistent resolution) before we stage the "ready" scenario.
    await expect(streamingBubble).toBeHidden({ timeout: 40000 }).catch(() => undefined);
    await expect(page.getByTestId("meta-row-agent").last()).toBeVisible({ timeout: 5000 }).catch(() => undefined);
    await narrate(page, chapters, shot, "mc-resolved");

    // Scenario 4 — start ANOTHER turn, close immediately, let it FINISH while
    // closed → the launcher shows the ready ● badge; reopening clears it.
    diag("send meta question 2, then close to finish-while-closed");
    await metaFill(page, "meta-composer-input", ASK2_QUESTION);
    await metaClick(page, "meta-composer-send");
    await expect(streamingBubble).toBeVisible({ timeout: 8000 }).catch(() => undefined);
    await dwell(page, 800);
    await metaClick(page, "meta-close");
    await expect(page.getByTestId("meta-overlay")).toHaveCount(0, { timeout: 5000 });
    // While closed, the badge is first ⟳ (working) and then flips to ● (ready)
    // when the turn finishes.
    try {
      await expectReadyBadge(page, 40000);
      sawReadyWhenDone = true;
    } catch {
      sawReadyWhenDone = false;
    }
    diag(`scenario4 sawReadyWhenDone=${sawReadyWhenDone}`);
    await narrate(page, chapters, shot, "mc-ready");

    // Reopening clears the ready badge.
    diag("reopen to clear ready badge");
    await metaClick(page, "meta-button");
    await metaClick(page, "meta-mode-story-ask");
    await expect(page.getByTestId("meta-overlay")).toBeVisible({ timeout: 8000 });
    await dwell(page, 600);
    readyClearedOnReopen = !(await page.getByTestId("meta-status-ready").isVisible().catch(() => false));
    diag(`scenario4 readyClearedOnReopen=${readyClearedOnReopen}`);
    await narrate(page, chapters, shot, "mc-ready-cleared");

    // Scenario 5 (stretch) — two modes at once: story.ask holds an unseen
    // finishing reply (●) while a kitsoki.ask turn streams (⟳). Timing-sensitive,
    // hence required:false — only narrate mc-both-badges if both badges land.
    diag("scenario5: attempt both badges at once");
    try {
      await expect(page.getByTestId("meta-overlay")).toBeVisible();
      await metaFill(page, "meta-composer-input", "Summarise the story in one line.");
      await metaClick(page, "meta-composer-send");
      await expect(streamingBubble).toBeVisible({ timeout: 8000 }).catch(() => undefined);
      await dwell(page, 400);
      const kitsokiTab = page.getByTestId("meta-tab-kitsoki-ask");
      if (await kitsokiTab.isVisible().catch(() => false)) {
        await metaClick(page, "meta-tab-kitsoki-ask");
        await dwell(page, 400);
        await metaFill(page, "meta-composer-input", "What is kitsoki?");
        await metaClick(page, "meta-composer-send");
        await expect(streamingBubble).toBeVisible({ timeout: 8000 }).catch(() => undefined);
        await dwell(page, 600);
        await metaClick(page, "meta-close");
        await expect(page.getByTestId("meta-overlay")).toHaveCount(0, { timeout: 5000 });
        const deadline = Date.now() + 12000;
        while (Date.now() < deadline) {
          const busy = await page.getByTestId("meta-status-busy").isVisible().catch(() => false);
          const ready = await page.getByTestId("meta-status-ready").isVisible().catch(() => false);
          if (busy && ready) {
            sawBothBadges = true;
            break;
          }
          await page.waitForTimeout(300);
        }
        diag(`scenario5 sawBothBadges=${sawBothBadges}`);
        if (sawBothBadges) {
          // Capture the labeled frame NOW, while BOTH badges are on screen — the
          // busy turn finishes shortly, so a shot taken after narrate()'s dwell
          // would catch only the ● ready badge. Sync the popover first so the
          // captured frame already carries the tour narration, then narrate with
          // skipShot so the dwell doesn't overwrite this frame.
          await page.evaluate((id: string) => {
            (window as unknown as { __tourGoTo?: (s: string) => void }).__tourGoTo?.(id);
          }, "mc-both-badges");
          await expect(page.getByTestId("tour-title"))
            .toHaveText(STEP_BY_ID["mc-both-badges"].title, { timeout: 8000 })
            .catch(() => undefined);
          await shot(page, "mc-both-badges");
          await narrate(page, chapters, shot, "mc-both-badges", { skipShot: true });
        }
      } else {
        diag("scenario5: kitsoki-ask tab not available; skipping both-badges stage");
      }
    } catch (e) {
      diag(`scenario5 error (non-fatal): ${e instanceof Error ? e.message : String(e)}`);
    }

    // Final step — narrate the close-out, then advance Next to dismiss the tour.
    await narrate(page, chapters, shot, "mc-done");
    await page.getByTestId("tour-next").click().catch(() => undefined);
    await expect(page.getByTestId("tour-overlay")).toHaveCount(0, { timeout: 8000 }).catch(() => undefined);
  } catch (e) {
    diag(`FAILED: ${e instanceof Error ? e.stack ?? e.message : String(e)}`);
    diag(`--- server log ---\n${server?.log?.() ?? ""}`);
    throw e;
  } finally {
    await context.close();
    const mp4 = await saveVideoAsMp4(video, ARTIFACT_DIR, "meta-chat-demo");
    writeChapters(mp4, chapters.list());
    await browser.close();
  }

  // ── Verdicts (after the walk so the recording is always complete) ──────────
  diag(
    `VERDICTS sawStreaming=${sawStreaming} sawBusyOnClose=${sawBusyOnClose} ` +
      `resumedStreaming=${resumedStreaming} resumedTranscriptIntact=${resumedTranscriptIntact} ` +
      `sawReadyWhenDone=${sawReadyWhenDone} readyClearedOnReopen=${readyClearedOnReopen} ` +
      `sawBothBadges=${sawBothBadges}`
  );
  expect(sawStreaming, "scenario 1: streaming bubble appears").toBe(true);
  expect(sawBusyOnClose, "scenario 2: ⟳ working badge after close-mid-stream").toBe(true);
  expect(resumedTranscriptIntact, "scenario 3: conversation intact on reopen").toBe(true);
  expect(sawReadyWhenDone, "scenario 4: ● ready badge when turn finishes closed").toBe(true);
  expect(readyClearedOnReopen, "scenario 4: ● clears on reopen").toBe(true);
  // scenario 5 is a stretch — non-blocking.

  const pngs = fs.readdirSync(ARTIFACT_DIR).filter((f) => f.endsWith(".png"));
  console.log(`[meta-chat-video] screenshots (${pngs.length}) in ${ARTIFACT_DIR}`);
});
