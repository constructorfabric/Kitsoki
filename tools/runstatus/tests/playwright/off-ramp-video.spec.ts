/**
 * off-ramp-video.spec.ts — the AGENT OFF-RAMP feature-spotlight video, driven
 * against a REAL `kitsoki web` server in the deterministic no-LLM REPLAY +
 * HOST-CASSETTE posture (NOT the nil-harness --flow posture the golden
 * agent-actions spec uses): the off-ramp needs FREE-TEXT routing, so the server
 * runs `--harness replay --recording … --host-cassette …`. The recording maps
 * the off-menu question to a CLARIFY (no-match) that fires the off-ramp; the
 * cassette stubs the voiced converse answer so the frames are byte-stable.
 *
 * Like the golden agent-actions / multi-story specs, the WHOLE video is
 * TOUR-DRIVEN: it runs OFF_RAMP_TOUR_STEPS from src/tour/off-ramp-manifest.ts
 * via window.__startTourWithSteps and asserts each popover `title` against the
 * manifest so the recording can't drift. The real interactions (type the
 * off-menu question + Send, click the menu item) run as PRE-STEP hooks so each
 * spotlighted surface and state exists before the spotlight lands.
 *
 * THE FEATURE, end to end:
 *   - desk room: a menu (browse/status/about) AND a free-text composer.
 *   - off-menu QUESTION → answered in place: an `offramp-bubble` appears, state
 *     stays `desk`, the menu (`intent-btn-browse`) is still present. No bounce.
 *   - menu PICK (browse) → transitions normally to `catalogue`.
 * The contrast IS the feature.
 *
 * NB — testids that actually ship for this MENU room: because the desk view has
 * choice items, the free-text box is the TEXT FLOOR (`text-floor-input` /
 * `text-floor-send`), not `composer-input`/`composer-send` (those render only on
 * a pure semantic/text-slot room with no menu). The off-ramp answer is
 * `offramp-bubble` (data-mode="offpath") carrying an `offramp-chip`.
 *
 * Record:  pnpm exec playwright test off-ramp-video --project=chromium
 * Fast:    WEB_CHAT_PACE=0 pnpm exec playwright test off-ramp-video --project=chromium
 */
import { test, expect, chromium, type Browser, type BrowserContext, type Page } from "@playwright/test";
import { fileURLToPath } from "url";
import path from "path";
import fs from "fs";
import os from "os";
import { spawn, type ChildProcess } from "child_process";
import {
  makeShot,
  prepareVideoDir,
  saveVideoAsMp4,
  dwell,
  cinematicGoto,
  SETTLE_MS,
  ChapterRecorder,
  writeChapters,
  demoAddr,
} from "./_helpers/server.js";
import { cameraContext } from "./_helpers/camera.js";
import { captureDiagnostics } from "./_helpers/demo.js";
import { OFF_RAMP_TOUR_STEPS, type TourStep } from "../../src/tour/off-ramp-manifest.js";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

// repoRoot = .../kitsoki (tools/runstatus/tests/playwright -> 4 up)
const repoRoot = path.resolve(__dirname, "../../../..");
const BIN = path.join(repoRoot, "bin", "kitsoki");
// Point at the whole stories/ tree so the home screen shows the real catalogue.
const STORIES_DIR = path.join(repoRoot, "stories");
const STORY_DIR = path.join(repoRoot, "stories", "off-ramp-demo");
const RECORDING = path.join(STORY_DIR, "assets", "recording.yaml");
const HOST_CASSETTE = path.join(STORY_DIR, "assets", "converse-cassette.yaml");

// Unique port — distinct from every other spec (7740 multi-story, 7746-7748
// trace/onboarding/agent-actions) so parallel runs never race on the bind.
const ADDR = demoAddr(7751);
const BASE = `http://${ADDR}`;
const RPC = `${BASE}/rpc`;

const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "off-ramp");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "video");
const CHAPTER_SOURCE = "tools/runstatus/src/tour/off-ramp-manifest.ts";

const OFF_MENU_QUESTION = "why should I trust an AI with my project?";

// ── server lifecycle (tmp DB; spawned directly so we own the args/DB path) ────

let server: ChildProcess | null = null;
let serverLog = "";
let tmpDbDir = "";

async function rpc<T>(method: string, params: Record<string, unknown>): Promise<T> {
  const res = await fetch(RPC, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ jsonrpc: "2.0", id: 1, method, params }),
  });
  const body = (await res.json()) as { result?: T; error?: { message: string } };
  if (body.error) throw new Error(`${method} failed: ${body.error.message}`);
  return body.result as T;
}

async function waitForHealthy(timeoutMs: number): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  let lastErr = "";
  while (Date.now() < deadline) {
    try {
      const res = await fetch(`${BASE}/`, { method: "GET" });
      if (res.status === 200) return;
      lastErr = `status ${res.status}`;
    } catch (e) {
      lastErr = e instanceof Error ? e.message : String(e);
    }
    await new Promise((r) => setTimeout(r, 200));
  }
  throw new Error(`server not healthy after ${timeoutMs}ms (last: ${lastErr})\n--- server log ---\n${serverLog}`);
}

test.beforeAll(async () => {
  for (const p of [STORIES_DIR, RECORDING, HOST_CASSETTE, BIN]) {
    if (!fs.existsSync(p)) {
      throw new Error(`missing required path: ${p} (run 'make build-bin' first)`);
    }
  }
  prepareVideoDir(VIDEO_DIR); // clears stale .webm; must run before context creation

  tmpDbDir = fs.mkdtempSync(path.join(os.tmpdir(), "kitsoki-off-ramp-"));
  const dbPath = path.join(tmpDbDir, "s.db");

  // The off-ramp posture: replay harness supplies the no-match that fires the
  // off-ramp; the host cassette supplies the voiced converse answer.
  server = spawn(
    BIN,
    [
      "web",
      "--harness", "replay",
      "--recording", RECORDING,
      "--host-cassette", HOST_CASSETTE,
      "--stories-dir", STORIES_DIR,
      "--addr", ADDR,
      "--db", dbPath,
    ],
    { cwd: repoRoot, stdio: ["ignore", "pipe", "pipe"] },
  );
  server.stdout?.on("data", (d) => (serverLog += d.toString()));
  server.stderr?.on("data", (d) => (serverLog += d.toString()));
  server.on("exit", (code, sig) => {
    serverLog += `\n[server exited code=${code} sig=${sig}]\n`;
  });

  await waitForHealthy(20000);
});

test.afterAll(async () => {
  // Stop ONLY this spec's server, by its own handle (never pkill -f kitsoki).
  if (server && !server.killed) {
    server.kill("SIGTERM");
    await new Promise((r) => setTimeout(r, 500));
    if (!server.killed) server.kill("SIGKILL");
  }
  if (tmpDbDir) fs.rmSync(tmpDbDir, { recursive: true, force: true });
});

/** The off-ramp-demo story card (matched by its title). */
function offRampCard(page: Page) {
  return page
    .getByTestId("story-card")
    .filter({ has: page.getByTestId("story-title").filter({ hasText: "Off-Ramp Demo" }) });
}

/** Inject (or re-inject) the tour and confirm the overlay is up. */
async function injectTour(page: Page, steps: readonly TourStep[]): Promise<void> {
  await page.evaluate((stepsJson: string) => {
    (window as unknown as { __startTourWithSteps?: (s: string) => void })
      .__startTourWithSteps?.(stepsJson);
  }, JSON.stringify(steps));
  await expect(page.getByTestId("tour-overlay")).toBeVisible({ timeout: 8000 });
}

/**
 * Type into the free-text floor (the off-menu door for a MENU room) and send.
 * The tour overlay's backdrop is up during a pre-step hook, so set the value via
 * the native setter (firing a real `input` event for Vue's v-model) and dispatch
 * the click on the send button directly, bypassing the backdrop's hit-test.
 */
async function sendFreeText(page: Page, text: string): Promise<void> {
  const input = page.getByTestId("text-floor-input").first();
  await expect(input).toBeVisible({ timeout: 15000 });
  await input.evaluate((el, value) => {
    const node = el as HTMLTextAreaElement;
    const proto = Object.getPrototypeOf(node);
    const setter = Object.getOwnPropertyDescriptor(proto, "value")?.set;
    setter?.call(node, value);
    node.dispatchEvent(new Event("input", { bubbles: true }));
  }, text);
  await dwell(page, SETTLE_MS);
  await page.getByTestId("text-floor-send").first().evaluate((el) => (el as HTMLElement).click());
}

test.describe("agent off-ramp feature-spotlight (live, no-LLM replay+cassette)", () => {
  test("home → desk → off-menu question answered in place → menu pick transitions", async () => {
    test.setTimeout(180000);

    // Startup discovers the whole catalogue but creates no sessions.
    const stories = await rpc<Array<{ path: string; app_id: string; title: string }>>(
      "runstatus.stories.list",
      {},
    );
    const offRamp = stories.find((s) => s.app_id === "off-ramp-demo");
    expect(offRamp, "off-ramp-demo story is in the catalogue").toBeTruthy();

    const browser: Browser = await chromium.launch({ headless: true });
    const context: BrowserContext = await browser.newContext(
      cameraContext({ recordVideoDir: VIDEO_DIR }),
    );
    const page = await context.newPage();
    const video = page.video(); // capture BEFORE context.close()
    const shot = makeShot(ARTIFACT_DIR);
    const { mark, onThrow } = captureDiagnostics(page, ARTIFACT_DIR);

    // Per-step chapter windows for the MP4 sidecar (clock starts now).
    const chapters = new ChapterRecorder();

    try {
      // ── 1. Open the home story library and start the tour ON it ──────────────
      mark("navigating home");
      await cinematicGoto(page, `${BASE}/#/`, { waitForTestId: "home-view" });
      await expect(page.getByTestId("story-card").first()).toBeVisible({ timeout: 15000 });
      await injectTour(page, OFF_RAMP_TOUR_STEPS);

      // ── 2. Walk OFF_RAMP_TOUR_STEPS ──────────────────────────────────────────
      for (const step of OFF_RAMP_TOUR_STEPS) {
        mark(`step ${step.id}`);

        // Mirror the overlay's route-guard.
        const currentUrl = page.url();
        const currentRouteKind = currentUrl.includes("/chat")
          ? "interactive"
          : currentUrl.match(/#\/s\/[0-9a-f-]{36}$/)
            ? "any"
            : "home";
        if (step.route !== "any" && step.route !== currentRouteKind) {
          mark(`  route-skip (${currentRouteKind})`);
          continue;
        }

        // ── Pre-step setup ────────────────────────────────────────────────────
        if (step.id === "or-desk") {
          // Fresh run lands in the desk room. Anchor the menu + free-text floor.
          await expect(page.getByTestId("current-state")).toContainText("desk", { timeout: 15000 });
          await expect(page.getByTestId("intent-btn-browse")).toBeVisible({ timeout: 15000 });
          await expect(page.getByTestId("text-floor-input")).toBeVisible({ timeout: 15000 });
          await dwell(page, SETTLE_MS);
        }
        if (step.id === "or-offramp") {
          // THE OFF-RAMP BEAT: type the off-menu question and send it. The
          // recording maps it to a no-match → off-ramp → voiced converse answer.
          await sendFreeText(page, OFF_MENU_QUESTION);
          // Wait for the off-ramp answer bubble (data-mode="offpath" + chip).
          await expect(page.getByTestId("offramp-bubble")).toBeVisible({ timeout: 15000 });
          await expect(page.getByTestId("offramp-bubble")).toHaveAttribute("data-mode", "offpath", {
            timeout: 5000,
          });
          await expect(page.getByTestId("offramp-chip")).toBeVisible({ timeout: 5000 });
          await dwell(page, SETTLE_MS);
        }
        if (step.id === "or-no-advance") {
          // The hard signal: the off-ramp did NOT advance state and the menu is
          // intact. State badge still desk; intent-btn-browse still present.
          await expect(page.getByTestId("current-state")).toContainText("desk", { timeout: 5000 });
          await expect(page.getByTestId("intent-btn-browse")).toBeVisible({ timeout: 5000 });
          await dwell(page, SETTLE_MS);
        }
        if (step.id === "or-pick") {
          // THE CONTRAST: a menu pick transitions normally. Dispatch the DOM
          // click so the pre-step hook does not depend on browser hit-testing.
          const browseBtn = page.getByTestId("intent-btn-browse").first();
          await expect(browseBtn).toBeVisible({ timeout: 10000 });
          await browseBtn.evaluate((el) => (el as HTMLElement).click());
          await expect(page.getByTestId("current-state")).toContainText("catalogue", { timeout: 15000 });
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
          const remaining = OFF_RAMP_TOUR_STEPS.slice(OFF_RAMP_TOUR_STEPS.indexOf(step) + 1);
          const isOnNext = remaining.some((s) => s.title === actualTitle);
          if (isOnNext) {
            mark(`  drift-skip: overlay on "${actualTitle}"`);
            continue;
          }
        }
        await expect(titleEl).toHaveText(step.title, { timeout: 12000 });

        // Spotlight settled → open this step's chapter window.
        chapters.open(step.id, step.title, CHAPTER_SOURCE);

        await dwell(page, step.dwellMs ?? 3000);
        await shot(page, step.id);

        if (step.kind === "explain") {
          await page.getByTestId("tour-next").click();
          await dwell(page, 700);
        } else {
          // The only action step is or-intro-start (route-match → interactive):
          // click New session on the off-ramp card specifically.
          const target = offRampCard(page).getByTestId("new-session-btn");
          await target.scrollIntoViewIfNeeded().catch(() => undefined);
          await target.evaluate((el) => (el as HTMLElement).click());
          await page.waitForTimeout(300);
          await page.waitForURL(/#\/s\/[0-9a-f-]{36}\/chat$/, { timeout: 15000 });
          await dwell(page, 1000);
        }
      }

      // The final step's "Done" closes the tour.
      await expect(page.getByTestId("tour-overlay")).toHaveCount(0, { timeout: 5000 });
    } catch (err) {
      onThrow(err);
      serverLog += "";
      fs.writeFileSync(path.join(ARTIFACT_DIR, "server.log"), serverLog);
      throw err;
    } finally {
      await page.close();
      await context.close(); // finalises the video
      const mp4 = await saveVideoAsMp4(video, ARTIFACT_DIR, "off-ramp-demo");
      writeChapters(mp4, chapters.list());
      await browser.close();
    }

    const pngs = fs.readdirSync(ARTIFACT_DIR).filter((f) => f.endsWith(".png"));
    console.log(`[off-ramp-video] screenshots (${pngs.length}) in ${ARTIFACT_DIR}`);
  });
});
