/**
 * "Cherny loop" feature-tour video demo.
 *
 * Drives the cherny-loop tour against a real `kitsoki web` server in the
 * deterministic no-LLM posture (--flow web_tour.yaml --mode one-shot; the flow's
 * host_handlers stub the maker / script gate / artifact write per iteration) and
 * records a video + per-scene screenshots to .artifacts/cherny-loop/.
 *
 * Like the golden agent-actions / dev-story-bugfix specs, this runs ONLY the
 * CHERNY_LOOP_TOUR_STEPS via window.__startTourWithSteps. The tour drives the
 * whole video: it opens on the home story library and a route-match action step
 * navigates home → new session → the drive view, then the operator types a
 * free-text goal and presses launch ONCE — the loop runs itself to completion.
 *
 * Driving mechanics:
 *   - The free-text first message is typed into the composer (set_goal sink) and
 *     sent — the goal in plain words.
 *   - `launch` is clicked once; one-shot mode runs the WHOLE autonomous loop in
 *     that turn, so the hard signal is the terminal state (__exit__exhausted),
 *     not a per-iteration counter.
 *
 * Validate fast (no dwells):
 *   WEB_CHAT_PACE=0 pnpm exec playwright test cherny-loop-video --project=chromium
 * Record at watch-speed:
 *   pnpm exec playwright test cherny-loop-video --project=chromium
 *
 * The harness suppresses Playwright stdout, so per-step progress and any failure
 * context is also written to .artifacts/cherny-loop/ERROR.txt.
 */
import { test, expect, chromium, type Browser, type BrowserContext, type Page } from "@playwright/test";
import path from "path";
import fs from "fs";
import {
  repoRoot,
  startWebServer,
  makeShot,
  prepareVideoDir,
  saveVideoAsMp4,
  ChapterRecorder,
  writeChapters,
  dwell,
  cinematicGoto,
  type WebServer,
} from "./_helpers/server.js";
import { CHERNY_LOOP_TOUR_STEPS } from "../../src/tour/cherny-loop-manifest.js";

const CHAPTER_SOURCE = "stories/cherny-loop/recording.yaml";

// 7771 — distinct from every other spec's port so parallel runs never race.
const ADDR = "127.0.0.1:7771";
const STORY_DIR = path.join(repoRoot, "stories", "cherny-loop");
const RECORDING = path.join(STORY_DIR, "recording.yaml");
const CASSETTE = path.join(STORY_DIR, "flows", "cassettes", "web_tour.cassette.yaml");
const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "cherny-loop");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "video");
const ERROR_TXT = path.join(ARTIFACT_DIR, "ERROR.txt");

let server: WebServer;

function diag(msg: string): void {
  const line = `[${new Date().toISOString()}] ${msg}\n`;
  try {
    fs.appendFileSync(ERROR_TXT, line);
  } catch {
    /* best-effort */
  }
}

test.beforeAll(async () => {
  prepareVideoDir(VIDEO_DIR);
  fs.mkdirSync(ARTIFACT_DIR, { recursive: true });
  fs.writeFileSync(ERROR_TXT, "");
  // Replay harness + host cassette + one-shot: the operator's free-text message
  // is routed to `configure` by the hand-authored recording (no LLM); host.* calls
  // come from the cassette (deterministic, distinct per iteration); one-shot lets
  // the synthetic emit chain auto-advance through the baseline gate so `launch`
  // runs the whole loop autonomously. (Replay + cassette coexist — runtime.go.)
  server = await startWebServer({
    addr: ADDR,
    storiesDir: STORY_DIR,
    harness: "replay",
    recording: RECORDING,
    hostCassette: CASSETTE,
    mode: "one-shot",
  });
});

test.afterAll(() => server?.stop());

/** Assert the drive view's current-state reaches `state`. */
async function expectState(page: Page, state: string): Promise<void> {
  await expect(page.getByTestId("current-state")).toHaveText(state, { timeout: 15000 });
}

/** Click a slotless intent button (DOM-level so the tour popover never intercepts). */
async function clickIntent(page: Page, intent: string): Promise<void> {
  const btn = page.getByTestId(`intent-btn-${intent}`).first();
  await expect(btn).toBeVisible({ timeout: 15000 });
  await dwell(page, 500);
  await btn.evaluate((el) => (el as HTMLElement).click());
  await dwell(page, 600);
}

/** Type a free-text message into the semantic composer and send it — the demo's
 *  "first message", routed via session.turn (→ the replay recording). Sets the
 *  value via the native setter so Vue's v-model fires, then clicks send
 *  (DOM-level, overlay-safe). */
async function sendText(page: Page, text: string): Promise<void> {
  // The free-text "floor" beneath the action widget — routes via session.turn.
  const input = page.getByTestId("text-floor-input").first();
  await expect(input).toBeVisible({ timeout: 15000 });
  await input.evaluate((el, value) => {
    const node = el as HTMLInputElement | HTMLTextAreaElement;
    const setter = Object.getOwnPropertyDescriptor(Object.getPrototypeOf(node), "value")?.set;
    setter?.call(node, value);
    node.dispatchEvent(new Event("input", { bubbles: true }));
  }, text);
  await dwell(page, 700);
  await page.getByTestId("text-floor-send").first().evaluate((el) => (el as HTMLElement).click());
  await dwell(page, 700);
}

/**
 * Drive the turn associated with a manifest step (AFTER its narration
 * screenshot). Each drive asserts a hard signal — the room transition or the
 * iteration counter — from the flow fixture's verified path.
 */
async function driveForStep(page: Page, stepId: string): Promise<void> {
  switch (stepId) {
    case "cl-goal":
      // The free-text first message — routed to `configure` by the replay
      // recording (no LLM). Submitted via the semantic composer (session.turn).
      diag("drive free-text goal via composer (replay-routed → configure)");
      await sendText(page, "the rate limiter tests are flaky — get `go test ./internal/ratelimit` green");
      await expectState(page, "configuring");
      // Rest on the just-sent first message before the next beat narrates it.
      await dwell(page, 2800);
      break;
    case "cl-launch":
      // One launch runs the WHOLE loop autonomously (one-shot mode) to the
      // iteration ceiling — no per-iteration prodding. The terminal state is
      // the hard signal the autonomous run completed.
      diag("drive launch → autonomous run → __exit__exhausted");
      await clickIntent(page, "launch");
      await expectState(page, "__exit__exhausted");
      await dwell(page, 800);
      break;
    default:
      // cl-converge / cl-budget / cl-done narrate the already-terminal run.
      break;
  }
}

test("cherny loop feature-tour video", async () => {
  test.setTimeout(300000);
  const browser: Browser = await chromium.launch({ headless: true });
  const context: BrowserContext = await browser.newContext({
    viewport: { width: 1600, height: 900 },
    deviceScaleFactor: 2,
    recordVideo: { dir: VIDEO_DIR, size: { width: 1600, height: 900 } },
  });
  const page: Page = await context.newPage();
  const video = page.video();
  const shot = makeShot(ARTIFACT_DIR);
  const chapters = new ChapterRecorder();

  try {
    diag("navigating home");
    await cinematicGoto(page, `${server.base}/#/`, { waitForTestId: "home-view" });

    await page.evaluate((stepsJson: string) => {
      (window as unknown as { __startTourWithSteps?: (s: string) => void })
        .__startTourWithSteps?.(stepsJson);
    }, JSON.stringify(CHERNY_LOOP_TOUR_STEPS));
    await expect(page.getByTestId("tour-overlay")).toBeVisible({ timeout: 8000 });

    for (const step of CHERNY_LOOP_TOUR_STEPS) {
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

      if (step.waitForTarget) {
        await expect(page.getByTestId(step.waitForTarget).first()).toBeVisible({ timeout: 15000 });
      }

      const titleEl = page.getByTestId("tour-title");
      const actualTitle = (await titleEl.textContent({ timeout: 8000 }).catch(() => "")) ?? "";
      if (actualTitle !== step.title) {
        diag(`  re-sync overlay from "${actualTitle}" → "${step.title}"`);
        await page.evaluate((id: string) => {
          (window as unknown as { __tourGoTo?: (s: string) => void }).__tourGoTo?.(id);
        }, step.id);
      }
      await expect(titleEl).toHaveText(step.title, { timeout: 12000 });

      chapters.open(step.id, step.title, CHAPTER_SOURCE);
      await dwell(page, step.dwellMs ?? 3000);
      await shot(page, step.id);

      if (step.kind === "action") {
        // The only action step is the intro "New session" navigation.
        const target = page.getByTestId(step.target!).first();
        await target.scrollIntoViewIfNeeded().catch(() => undefined);
        await target.click();
        await page.waitForTimeout(300);
        if (step.advanceRoute === "interactive") {
          await page.waitForURL(/#\/s\/[0-9a-f-]{36}\/chat$/, { timeout: 15000 });
        }
        await dwell(page, 1000);
      } else {
        await driveForStep(page, step.id);
        await page.getByTestId("tour-next").click();
        await dwell(page, 700);
      }
    }

    await expect(page.getByTestId("tour-overlay")).toHaveCount(0, { timeout: 5000 });
  } catch (e) {
    diag(`FAILED: ${e instanceof Error ? e.stack ?? e.message : String(e)}`);
    diag(`--- server log ---\n${server?.log?.() ?? ""}`);
    throw e;
  } finally {
    await context.close();
    const mp4 = await saveVideoAsMp4(video, ARTIFACT_DIR, "cherny-loop-demo");
    writeChapters(mp4, chapters.list());
    await browser.close();
  }

  const pngs = fs.readdirSync(ARTIFACT_DIR).filter((f) => f.endsWith(".png"));
  console.log(`[cherny-loop-video] screenshots (${pngs.length}) in ${ARTIFACT_DIR}`);
});
