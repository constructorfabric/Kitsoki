/**
 * Live-chat-streaming feature-spotlight video demo.
 *
 * Drives the chat-stream tour (src/tour/generated/chat-stream.ts) against a real
 * `kitsoki web` server in the deterministic no-LLM posture (--flow
 * happy_llm.yaml + the bugfix demo cassette) and records a video + per-scene
 * screenshots to .artifacts/chat-stream/.
 *
 * The whole video is tour-driven and stays in the MAIN CHAT: home → new
 * session → the chat, where the `start` turn is driven through the choice
 * widget (the /rpc/turn-stream SSE path that injects a host StreamSink).
 * KITSOKI_CASSETTE_SLOWPLAY makes the cassette replay stream the recorded
 * task + decide transcripts into that sink, paced by the recorded timings —
 * so the thinking bubble fills with 🧠 thoughts and tool rows in real-ish
 * time, "live" from the cassette, no LLM. The spec defaults the knob to 1.5
 * (slightly slower than recorded) so a plain run records a watchable stream;
 * override it for fast validation.
 *
 * After the turn lands the tour walks the preserved feed: the collapsed
 * "🧠 N thoughts · M tool calls" activity section inside the final agent
 * bubble, expanded back to the same interleaved feed.
 *
 * Then the tour opens the META OVERLAY and repeats the loop there: a Story
 * Q&A question streams through /rpc/meta-stream (the deterministic stub
 * agent emits a thinking event, a Read tool call, then the reply chunks —
 * KITSOKI_META_STREAM_DELAY_MS paces it for the camera), rendered by the SAME
 * shared ActivityFeed/ActivityDisclosure components the main chat uses.
 *
 * REGRESSION CONTRACT (asserted after the tour completes):
 *   a) the feed interleaves thinking and tool rows in ARRIVAL order, including
 *      thoughts carried as `thinking` content blocks (the real claude
 *      stream-json shape with extended thinking) — not just `text` blocks;
 *   b) the activity collapses into the final agent bubble and expands to the
 *      same presentation;
 *   c) the meta overlay streams + preserves the same presentation, with the
 *      reply NOT duplicated into the feed (the feed holds the reasoning, the
 *      bubble holds the answer).
 *
 * Validate fast (no dwells, 10x replay):
 *   WEB_CHAT_PACE=0 KITSOKI_CASSETTE_SLOWPLAY=0.1 \
 *     pnpm exec playwright test chat-stream-video --project=chromium
 * Record at watch-speed:
 *   pnpm exec playwright test chat-stream-video --project=chromium
 *
 * NOTE: the harness suppresses Playwright stdout, so per-step progress and any
 * failure context is also written to .artifacts/chat-stream/diagnostic.log.
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
  demoAddr,
  SETTLE_MS,
  type WebServer,
} from "./_helpers/server.js";
import { cameraContext } from "./_helpers/camera.js";
import { CHAT_STREAM_TOUR_STEPS, type TourStep } from "../../src/tour/generated/chat-stream.js";

// The feature-catalog source of truth for this tour: each step becomes a chapter
// (source_ref kind=tour) whose [start,end] window is the recorded dwell.
const CHAPTER_SOURCE = "features/chat-stream.yaml";

// 7758 — distinct from every other spec's port (7740–7757 are taken) so
// parallel specs never race on a bind.
const ADDR = demoAddr(7758);
const STORY_DIR = path.join(repoRoot, "stories", "bugfix");
const FLOW = path.join(STORY_DIR, "flows", "happy_llm.yaml");
const HOST_CASSETTE = path.join(STORY_DIR, "flows", "demo.cassette.yaml");
const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "chat-stream");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "video");
const DIAG_LOG = path.join(ARTIFACT_DIR, "diagnostic.log");

// The feed the cassette's two streamed arcs must produce, in arrival order.
// Task arc: a `text` thought, Read, Grep, a `thinking`-block thought, Edit,
// Bash. Decide arc: a `thinking`-block thought, then the bad + good validator
// submits (the _kitsoki reject/nudge rows never reach the stream). "user"
// tool_result rows and system/result rows are not assistant events, so they
// never become feed rows.
const EXPECTED_FEED: Array<{ kind: "think" | "tool"; match: string }> = [
  { kind: "think", match: "I'll read the failing test first." },
  { kind: "tool", match: "Read" },
  { kind: "tool", match: "Grep" },
  { kind: "think", match: "The off-by-one is in the loop bound." },
  { kind: "tool", match: "Edit" },
  { kind: "tool", match: "Bash" },
  { kind: "think", match: "The reproduction is clear; I'll accept and submit the verdict." },
  { kind: "tool", match: "mcp__validator__submit" },
  { kind: "tool", match: "mcp__validator__submit" },
];
const EXPECTED_SUMMARY = "🧠 3 thoughts · 6 tool calls";

// The meta turn's feed: the stub agent (story.ask, read-only) emits one
// thinking event and one Read tool call before streaming the reply; the
// reply narration itself is deferred and dropped on done, so it must NOT
// appear as a trailing thought.
const EXPECTED_META_FEED: Array<{ kind: "think" | "tool"; match: string }> = [
  { kind: "think", match: "Let me look at the story definition first." },
  { kind: "tool", match: "Read" },
];
const EXPECTED_META_SUMMARY = "🧠 1 thought · 1 tool call";
const META_QUESTION = "Where is this run right now, and what does this story do?";

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
  // Slow-play is the POINT of this demo: without it the cassette replay is
  // instant and there is nothing to film. Default to 1.5x-slower-than-recorded
  // for a watchable stream; an explicit env (e.g. 0.1 for fast validation)
  // wins. startWebServer forwards the var to the spawned server.
  if (process.env.KITSOKI_CASSETTE_SLOWPLAY === undefined) {
    process.env.KITSOKI_CASSETTE_SLOWPLAY = "1.5";
  }
  // The meta turn streams from the stub agent; pace its events so the
  // overlay's bubble is filmable. Keep a small floor even in fast-validation
  // mode (WEB_CHAT_PACE=0) so the streaming bubble reliably EXISTS long
  // enough for the tour step gated on it — at 0 the turn can finish before
  // the step renders. startWebServer forwards process.env to the server.
  if (process.env.KITSOKI_META_STREAM_DELAY_MS === undefined) {
    process.env.KITSOKI_META_STREAM_DELAY_MS =
      process.env.WEB_CHAT_PACE === "0" ? "60" : "350";
  }
  diag(`KITSOKI_CASSETTE_SLOWPLAY=${process.env.KITSOKI_CASSETTE_SLOWPLAY}`);
  diag(`KITSOKI_META_STREAM_DELAY_MS=${process.env.KITSOKI_META_STREAM_DELAY_MS}`);
  server = await startWebServer({ addr: ADDR, flow: FLOW, hostCassette: HOST_CASSETTE, storiesDir: STORY_DIR });
});

test.afterAll(() => server?.stop());

/** One row of the live bubble / preserved feed, read from the DOM. */
interface FeedRow {
  kind: "think" | "tool";
  text: string;
}

/**
 * Read activity-feed rows (kind + text) inside a scope selector, in DOM
 * order. Every surface — the live thinking bubble, the preserved disclosure,
 * the meta overlay's bubble and disclosure — renders the same shared
 * ActivityFeed rows, so one reader covers them all; the scope picks which.
 */
async function readFeedRows(page: Page, scope: string): Promise<FeedRow[]> {
  return page.$$eval(
    `${scope} .chat-activity__thought, ${scope} .chat-activity__tool`,
    (els) =>
      els.map((el) => ({
        kind: el.classList.contains("chat-activity__thought") ? ("think" as const) : ("tool" as const),
        text: (el.textContent ?? "").trim(),
      }))
  );
}

/** The main chat's live thinking-bubble feed. */
const readBubbleFeed = (page: Page) => readFeedRows(page, '[data-testid="thinking-bubble"]');
/** The main chat's preserved (expanded) activity feed. */
const readActivityFeed = (page: Page) => readFeedRows(page, '[data-testid="chat-activity-feed"]');
/** The meta overlay's live streaming-bubble feed. */
const readMetaBubbleFeed = (page: Page) => readFeedRows(page, '[data-testid="meta-row-streaming"]');
/** The meta overlay's preserved (expanded) activity feed. */
const readMetaActivityFeed = (page: Page) => readFeedRows(page, '[data-testid="meta-activity-feed"]');

/**
 * Dwell across the in-flight streaming turn, saving progressive frames
 * (early / mid / late) and logging the bubble's row growth, then wait for the
 * bubble to dissolve into the final reply. Best-effort on the frames (a very
 * fast replay can outrun the camera) — the post-turn feed assertions are the
 * hard gate.
 */
async function captureLiveStreaming(
  page: Page,
  shot: (p: Page, name: string) => Promise<void>
): Promise<void> {
  const bubble = page.getByTestId("thinking-bubble");
  let sawBubble = false;
  try {
    await expect(bubble).toBeVisible({ timeout: 6000 });
    sawBubble = true;
  } catch {
    sawBubble = false;
  }
  diag(`captureLiveStreaming: thinking-bubble visible=${sawBubble}`);
  if (!sawBubble) {
    await shot(page, "cs-stream-watch-instant");
    return;
  }
  await dwell(page, 500);
  const early = await readBubbleFeed(page);
  await shot(page, "cs-stream-watch-early");
  await dwell(page, 1200);
  const mid = await readBubbleFeed(page);
  await shot(page, "cs-stream-watch-mid");
  await dwell(page, 1400);
  const late = await readBubbleFeed(page);
  await shot(page, "cs-stream-watch-late");
  diag(`captureLiveStreaming rows early=${early.length} mid=${mid.length} late=${late.length}`);
  diag(`captureLiveStreaming late feed:\n${late.map((r) => `  ${r.kind}: ${r.text}`).join("\n")}`);
  await expect(bubble).toBeHidden({ timeout: 60000 }).catch(() => undefined);
  diag("captureLiveStreaming: bubble hidden (turn landed)");
}

/**
 * Same progressive capture for the META overlay's streaming bubble: frames
 * while the stub-paced turn streams the feed + reply, then wait for the
 * bubble to dissolve into the committed assistant message. Best-effort on
 * the frames; the post-turn meta-activity assertions are the hard gate.
 */
async function captureMetaStreaming(
  page: Page,
  shot: (p: Page, name: string) => Promise<void>
): Promise<void> {
  const bubble = page.getByTestId("meta-row-streaming");
  let sawBubble = false;
  try {
    await expect(bubble).toBeVisible({ timeout: 6000 });
    sawBubble = true;
  } catch {
    sawBubble = false;
  }
  diag(`captureMetaStreaming: meta-row-streaming visible=${sawBubble}`);
  if (!sawBubble) {
    await shot(page, "cs-meta-stream-instant");
    return;
  }
  await dwell(page, 600);
  const early = await readMetaBubbleFeed(page);
  await shot(page, "cs-meta-stream-early");
  await dwell(page, 1400);
  const late = await readMetaBubbleFeed(page);
  await shot(page, "cs-meta-stream-late");
  diag(`captureMetaStreaming rows early=${early.length} late=${late.length}`);
  diag(`captureMetaStreaming late feed:\n${late.map((r) => `  ${r.kind}: ${r.text}`).join("\n")}`);
  await expect(bubble).toBeHidden({ timeout: 60000 }).catch(() => undefined);
  diag("captureMetaStreaming: bubble hidden (meta turn landed)");
}

test("live chat streaming feature-spotlight video", async () => {
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

  let sessionId = "";
  // Filled while walking the tour; asserted AFTER the walk so a content bug
  // still yields a complete recording that SHOWS the problem.
  let expandedFeed: FeedRow[] = [];
  let summaryText = "";
  let expandedMetaFeed: FeedRow[] = [];
  let metaSummaryText = "";
  let metaReplyText = "";

  try {
    // ── 1. Open the home story library and start the tour ON it ──────────────
    diag("navigating home");
    await cinematicGoto(page, `${server.base}/#/`, { waitForTestId: "home-view" });

    await page.evaluate((stepsJson: string) => {
      (window as unknown as { __startTourWithSteps?: (s: string) => void })
        .__startTourWithSteps?.(stepsJson);
    }, JSON.stringify(CHAT_STREAM_TOUR_STEPS));
    await expect(page.getByTestId("tour-overlay")).toBeVisible({ timeout: 8000 });

    // ── 2. Walk the CHAT_STREAM_TOUR_STEPS ───────────────────────────────────
    for (const step of CHAT_STREAM_TOUR_STEPS) {
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

      // The streaming steps do their own progressive capture + advance.
      if (step.id === "cs-stream-watch" || step.id === "cs-meta-stream") {
        // The popover is already on this step (the prior step's Next advanced
        // it) — open its chapter now so the streaming capture is its window.
        chapters.open(step.id, step.title, CHAPTER_SOURCE);
        if (step.id === "cs-stream-watch") {
          await captureLiveStreaming(page, shot);
        } else {
          await captureMetaStreaming(page, shot);
        }
        const watchTitle = await page
          .getByTestId("tour-title")
          .textContent({ timeout: 8000 })
          .catch(() => "");
        await shot(page, step.id);
        if (watchTitle === step.title) {
          await page.getByTestId("tour-next").click();
          await dwell(page, 700);
        } else {
          diag(`  ${step.id}: overlay drifted to "${watchTitle}" — accepting`);
        }
        continue;
      }

      if (step.waitForTarget) {
        await expect(page.getByTestId(step.waitForTarget).first()).toBeVisible({ timeout: 30000 });
      }

      // Anti-drift: the popover must show THIS step's title (or have skipped
      // ahead to a later one).
      const titleEl = page.getByTestId("tour-title");
      const actualTitle = await titleEl.textContent({ timeout: 8000 }).catch(() => "");
      if (actualTitle !== step.title) {
        const remaining = CHAT_STREAM_TOUR_STEPS.slice(CHAT_STREAM_TOUR_STEPS.indexOf(step) + 1);
        if (remaining.some((s) => s.title === actualTitle)) {
          diag(`  drift-skip: overlay on "${actualTitle}"`);
          continue;
        }
      }
      await expect(titleEl).toHaveText(step.title, { timeout: 12000 });

      // This step's spotlight is settled and on-screen — open its chapter
      // (auto-closes the prior one) so the dwell below becomes its window.
      chapters.open(step.id, step.title, CHAPTER_SOURCE);

      // Capture the collapsed summary line while the cs-collapsed spotlight is
      // on it (before the expand step opens the feed).
      if (step.id === "cs-collapsed") {
        summaryText =
          (await page
            .getByTestId("chat-activity-summary")
            .last()
            .textContent({ timeout: 5000 })
            .catch(() => "")) ?? "";
        diag(`  collapsed summary: "${summaryText.trim()}"`);
        // Collapsed by default: the feed body must NOT be visible yet.
        await expect(page.getByTestId("chat-activity-feed").last()).toBeHidden();
      }

      // The Story Q&A item only enables once the server's mode list loads;
      // clicking a disabled button fires nothing and the click-target advance
      // would stall the tour.
      if (step.id === "cs-meta-mode") {
        await expect(page.getByTestId("meta-mode-story-ask")).toBeEnabled({ timeout: 10000 });
      }

      // Type the question before the send-button spotlight dwells, so the
      // camera sees what is about to be asked.
      if (step.id === "cs-meta-ask") {
        await page.getByTestId("meta-composer-input").fill(META_QUESTION);
      }

      // Mirror cs-collapsed for the meta overlay's preserved activity.
      if (step.id === "cs-meta-collapsed") {
        metaSummaryText =
          (await page
            .getByTestId("meta-activity-summary")
            .last()
            .textContent({ timeout: 5000 })
            .catch(() => "")) ?? "";
        diag(`  meta collapsed summary: "${metaSummaryText.trim()}"`);
        await expect(page.getByTestId("meta-activity-feed").last()).toBeHidden();
        metaReplyText =
          (await page
            .getByTestId("meta-row-agent")
            .last()
            .textContent({ timeout: 5000 })
            .catch(() => "")) ?? "";
      }

      await dwell(page, step.dwellMs ?? 3000);
      await shot(page, step.id);

      if (step.kind === "explain") {
        // Read the expanded feeds on their spotlight steps, after the dwell.
        if (step.id === "cs-expanded") {
          expandedFeed = await readActivityFeed(page);
          diag(`  expanded feed (${expandedFeed.length} rows):\n${expandedFeed.map((r) => `  ${r.kind}: ${r.text}`).join("\n")}`);
        }
        if (step.id === "cs-meta-expanded") {
          expandedMetaFeed = await readMetaActivityFeed(page);
          diag(`  expanded META feed (${expandedMetaFeed.length} rows):\n${expandedMetaFeed.map((r) => `  ${r.kind}: ${r.text}`).join("\n")}`);
        }
        await page.getByTestId("tour-next").click();
        await dwell(page, 700);
      } else {
        const target = page.getByTestId(step.target!).first();
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
            // A UI-created session starts with a FRESH story world, so
            // judge_mode=human and `start` would not fire the transcript-
            // bearing agent calls. Patch the world off-camera to the llm
            // posture the demo cassette's arcs need (mirrors
            // agent-actions-video.spec.ts; setting BOTH ticket_id and
            // workspace_id keeps the idle auto-start arc from firing, so the
            // turn waits for the on-camera intent-btn-start click).
            if (sessionId) {
              await server.rpc("runstatus.session.patch_world", {
                session_id: sessionId,
                patch: {
                  judge_mode: "llm",
                  ticket_id: "TKT-demo",
                  ticket_title: "Demo live-stream run",
                  workdir: ".worktrees/tkt-demo",
                  workspace_id: "ws-demo",
                  thread: "TKT-demo",
                  base_branch: "main",
                  feature_branch: "fix/tkt-demo",
                  judge_confidence_threshold: 0.8,
                },
              });
            }
          }
          await dwell(page, 1000);
        } else {
          // click-target: dispatch the DOM click directly so it fires both the
          // control's behaviour (e.g. the <summary> toggle) and the overlay's
          // capture-phase advance listener regardless of paint order.
          await target.evaluate((el) => (el as HTMLElement).click());
          await dwell(page, SETTLE_MS);
        }
      }
    }

    // The final cs-done step's Next closes the tour.
    await expect(page.getByTestId("tour-overlay")).toHaveCount(0, { timeout: 5000 });

    // ── 3. The regression contract (after the walk, so the video is complete
    // even when these fail — the recording then SHOWS the bug). ──────────────
    expect(summaryText.trim()).toBe(EXPECTED_SUMMARY);
    expect(expandedFeed.map((r) => r.kind)).toEqual(EXPECTED_FEED.map((r) => r.kind));
    for (let i = 0; i < EXPECTED_FEED.length; i++) {
      expect(expandedFeed[i]!.text).toContain(EXPECTED_FEED[i]!.match);
    }
    // Every thought row carries the 🧠 marker.
    for (const row of expandedFeed.filter((r) => r.kind === "think")) {
      expect(row.text).toContain("🧠");
    }

    // ── 4. The meta-overlay contract: same presentation, shared components.
    expect(metaSummaryText.trim()).toBe(EXPECTED_META_SUMMARY);
    expect(expandedMetaFeed.map((r) => r.kind)).toEqual(EXPECTED_META_FEED.map((r) => r.kind));
    for (let i = 0; i < EXPECTED_META_FEED.length; i++) {
      expect(expandedMetaFeed[i]!.text).toContain(EXPECTED_META_FEED[i]!.match);
    }
    for (const row of expandedMetaFeed.filter((r) => r.kind === "think")) {
      expect(row.text).toContain("🧠");
    }
    // The reply must land in the bubble — and must NOT duplicate into the
    // feed as a trailing thought (the deferred-narration drop).
    expect(metaReplyText).toContain("deterministic no-LLM reply");
    for (const row of expandedMetaFeed) {
      expect(row.text).not.toContain("deterministic no-LLM reply");
    }
  } catch (e) {
    diag(`FAILED: ${e instanceof Error ? e.stack ?? e.message : String(e)}`);
    diag(`--- server log ---\n${server?.log?.() ?? ""}`);
    throw e;
  } finally {
    await context.close();
    const mp4 = await saveVideoAsMp4(video, ARTIFACT_DIR, "chat-stream-demo");
    // Emit the producer-agnostic chapter sidecar beside the MP4: each tour
    // step → one chapter with source_ref kind=tour.
    writeChapters(mp4, chapters.list());
    await browser.close();
  }

  const pngs = fs.readdirSync(ARTIFACT_DIR).filter((f) => f.endsWith(".png"));
  console.log(`[chat-stream-video] screenshots (${pngs.length}) in ${ARTIFACT_DIR}`);
});
