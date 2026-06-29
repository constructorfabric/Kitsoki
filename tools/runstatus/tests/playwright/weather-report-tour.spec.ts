/**
 * Weather & Climate — guided-tour video of the host.starlark.run example story.
 *
 * Tour-driven like the golden agent-actions spec: the WHOLE video is narrated by
 * the WEATHER_REPORT_TOUR_STEPS overlay. It opens on the home story library,
 * frames the weather-report story, drives home → new session → the interactive
 * /chat view via a route-match action step, then walks the feature with the
 * story being driven BEHIND the steps as pre-step hooks (free-text submits,
 * row expansion, back) so each spotlighted surface — the rendered report, the
 * host.starlark.run trace row, the climate report, the failure message, the
 * lobby — is present when its step runs.
 *
 * Server posture is unchanged: a real `kitsoki web` in the deterministic no-LLM
 * mode (`--flow tour.yaml`). The flow's starlark_http_cassette: makes the REAL
 * host.starlark.run handler run with its ctx.http GETs replayed from a cassette,
 * so the trace shows genuine host.starlark.run invocations and the
 * __http_exchanges summary, with no LLM and no socket.
 *
 * SINGLE SOURCE OF TRUTH: src/tour/generated/weather-report.ts drives both the
 * live overlay and this spec; the spec asserts each popover `title`, so the two
 * cannot drift.
 *
 * Validate fast (no dwells):
 *   WEB_CHAT_PACE=0 pnpm exec playwright test weather-report-tour --project=chromium
 * Record at watch-speed:
 *   pnpm exec playwright test weather-report-tour --project=chromium
 *
 * Requires a fresh binary: `make build-bin`.
 */
import { test, expect, chromium, type Browser, type BrowserContext, type Page, type Locator } from "@playwright/test";
import path from "path";
import fs from "fs";
import {
  startWebServer,
  repoRoot,
  makeShot,
  waitForState,
  saveVideoAsMp4,
  ChapterRecorder,
  writeChapters,
  dwell as dwellHelper,
  cinematicGoto,
  SETTLE_MS,
  demoAddr,
  type WebServer,
} from "./_helpers/server.js";
import { cameraContext } from "./_helpers/camera.js";
import { captureDiagnostics } from "./_helpers/demo.js";
import { WEATHER_REPORT_TOUR_STEPS, type TourStep } from "../../src/tour/generated/weather-report.js";

const ADDR = demoAddr(7767);
const STORY_DIR = path.join(repoRoot, "stories", "weather-report");
const FLOW = path.join(STORY_DIR, "flows", "tour.yaml");
const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "weather-report-tour");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "video");

// The feature-catalog source of truth for this spec's tour steps: each step
// becomes a chapter (source_ref kind=tour) whose [start,end] window is the
// recorded dwell.
const CHAPTER_SOURCE = "features/weather-report.yaml";

let server: WebServer;

test.beforeAll(async () => {
  fs.mkdirSync(VIDEO_DIR, { recursive: true });
  // Clear stale recordings so saveVideoAsMp4 can't pick an old run's webm.
  for (const f of fs.readdirSync(VIDEO_DIR)) {
    if (f.endsWith(".webm")) fs.rmSync(path.join(VIDEO_DIR, f));
  }
  server = await startWebServer({ addr: ADDR, flow: FLOW, storiesDir: STORY_DIR });
});

test.afterAll(() => server?.stop());

const dwell = (page: Page, ms: number) => dwellHelper(page, ms);

/** Fill a `choice:`-param free-text field for `intent` and submit it. */
async function submitParam(page: Page, intent: string, value: string): Promise<void> {
  const form = page.locator(`form[data-intent="${intent}"]`);
  await expect(form).toBeVisible({ timeout: 8000 });
  // The param composer is a wrapping <textarea> now (not a single-line input).
  await form.locator("textarea").fill(value);
  await dwell(page, 700);
  await form.locator('button[type="submit"]').click();
}

/** Resolve a step's real target element — honoring targetText when present. */
async function resolveTarget(page: Page, step: TourStep): Promise<Locator> {
  let loc = page.getByTestId(step.target!);
  if (step.targetText) loc = loc.filter({ hasText: new RegExp(step.targetText, "i") });
  return loc.first();
}

test("weather-report tour video (no-LLM, real host.starlark.run replay)", async () => {
  test.setTimeout(300000);
  const browser: Browser = await chromium.launch({ headless: true });
  const context: BrowserContext = await browser.newContext(
    cameraContext({ recordVideoDir: VIDEO_DIR }),
  );
  const page: Page = await context.newPage();
  const video = page.video();
  // Accumulate per-step time windows for the chapter sidecar. The clock starts
  // now so windows line up with the recorded MP4 timeline.
  const chapters = new ChapterRecorder();
  // Breadcrumb + crash trail to .artifacts/weather-report-tour/ERROR.txt — the
  // harness suppresses Playwright stdout, so a failed recording is otherwise
  // undiagnosable. Marking on every scene gives the throw a precise location.
  const { mark, onThrow } = captureDiagnostics(page, ARTIFACT_DIR);
  const rawShot = makeShot(ARTIFACT_DIR);
  const shot = async (p: Page, label: string): Promise<string> => {
    mark(label);
    return rawShot(p, label);
  };

  try {
    // ── 1. Open the home story library and start the tour ON it ──────────────
    // The whole video is tour-driven: rather than silently flashing home -> chat
    // before the overlay appears, we start the tour on home and let its
    // route-match action step perform the navigation, narrated by the popover.
    await cinematicGoto(page, `${server.base}/#/`, { waitForTestId: "home-view" });

    await page.evaluate((stepsJson: string) => {
      (window as unknown as { __startTourWithSteps?: (s: string) => void })
        .__startTourWithSteps?.(stepsJson);
    }, JSON.stringify(WEATHER_REPORT_TOUR_STEPS));
    await expect(page.getByTestId("tour-overlay")).toBeVisible({ timeout: 8000 });

    // ── 2. Walk the WEATHER_REPORT_TOUR_STEPS ────────────────────────────────
    for (const step of WEATHER_REPORT_TOUR_STEPS) {
      // Mirror the overlay's route guard so a step that isn't for the current
      // route is skipped here too (the intro steps are home; the rest are
      // interactive/any on /chat).
      const currentUrl = page.url();
      const currentRouteKind = currentUrl.includes("/chat")
        ? "interactive"
        : currentUrl.endsWith("/#/") || currentUrl.endsWith("/#")
          ? "home"
          : "any";
      if (step.route !== "any" && step.route !== currentRouteKind) {
        continue;
      }

      // ── Pre-step setup: drive the story BEHIND the step so its spotlighted
      //    surface is present, mirroring the golden's drawer pre-step hooks. ──
      if (step.id === "wr-forecast") {
        // From the lobby: free-text "forecast Tokyo" -> the 5-day report.
        await submitParam(page, "forecast", "Tokyo");
        await waitForState(page, "report");
        const transcript = page.getByTestId("chat-transcript");
        await expect(transcript.getByText("Tokyo, Japan")).toBeVisible({ timeout: 10000 });
        await dwell(page, SETTLE_MS);
      }
      if (step.id === "wr-trace-row") {
        // Expand the host.starlark.run row so its __http_exchanges payload is
        // visible under the spotlight.
        const timeline = page.getByTestId("trace-timeline");
        await timeline.scrollIntoViewIfNeeded().catch(() => undefined);
        const hostRow = timeline
          .locator('.trace-timeline__row:has([data-subsystem="host"])')
          .first();
        if (await hostRow.count()) {
          await hostRow.scrollIntoViewIfNeeded().catch(() => undefined);
          await hostRow.click().catch(() => undefined);
          await dwell(page, SETTLE_MS);
        }
      }
      if (step.id === "wr-climate") {
        // From the report room: "climate Oslo" -> the 2023 climate profile.
        await submitParam(page, "climate", "Oslo");
        const ct = page.getByTestId("chat-transcript");
        await expect(ct.getByText("2023 climate profile")).toBeVisible({ timeout: 10000 });
        await expect(ct.getByText("Oslo, Norway")).toBeVisible({ timeout: 10000 });
        await dwell(page, SETTLE_MS);
      }
      if (step.id === "wr-failure") {
        // From the report room: an unknown place -> the clean fail() path.
        await submitParam(page, "forecast", "Zzqxville");
        await waitForState(page, "failed");
        await expect(page.getByTestId("chat-transcript").getByText("no place found matching")).toBeVisible({ timeout: 10000 });
        await dwell(page, SETTLE_MS);
      }
      if (step.id === "wr-lobby-back") {
        // Step back to the lobby so the lobby intent picker is the spotlit
        // surface again.
        await page.getByTestId("intent-btn-back").click();
        await waitForState(page, "lobby");
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
        const remaining = WEATHER_REPORT_TOUR_STEPS.slice(WEATHER_REPORT_TOUR_STEPS.indexOf(step) + 1);
        const isOnNext = remaining.some((s) => s.title === actualTitle);
        if (isOnNext) {
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
          // Intro navigation (New session). The home view is static here, so a
          // hit-test click goes cleanly through the overlay's hole. Wait for the
          // URL to actually change before the next iteration asserts.
          await target.click();
          await page.waitForTimeout(300);
          if (step.advanceRoute === "interactive") {
            await page.waitForURL(/#\/s\/[0-9a-f-]{36}\/chat$/, { timeout: 15000 });
            await waitForState(page, "lobby");
          }
          await dwell(page, 1000);
        } else {
          // click-target: dispatch the DOM click directly so it fires both the
          // control's own @click AND the overlay's capture-phase advance.
          await target.evaluate((el) => (el as HTMLElement).click());
          await dwell(page, 1000);
        }
      }
    }

    // The final wr-done step's "Done" closes the tour.
    await expect(page.getByTestId("tour-overlay")).toHaveCount(0, { timeout: 5000 });
  } catch (err) {
    onThrow(err);
    fs.appendFileSync(path.join(ARTIFACT_DIR, "ERROR.txt"), `--- server log ---\n${server?.log?.() ?? ""}\n`);
    throw err;
  } finally {
    await context.close(); // finalises the recording
    // Transcode the raw webm to a universally-playable MP4 (VS Code / Keynote /
    // Slack); never ship the webm — it omits the container atoms those players
    // need. Mirrors the golden agent-actions spec.
    const mp4 = await saveVideoAsMp4(video, ARTIFACT_DIR, "weather-report-tour");
    // Emit the producer-agnostic chapter sidecar beside the MP4: each tour
    // step → one chapter with source_ref kind=tour.
    writeChapters(mp4, chapters.list());
    await browser.close();
  }

  const pngs = fs.readdirSync(ARTIFACT_DIR).filter((f) => f.endsWith(".png"));
  console.log(`[weather-tour] screenshots (${pngs.length}) in ${ARTIFACT_DIR}`);
});
