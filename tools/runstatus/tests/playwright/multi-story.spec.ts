/**
 * multi-story.spec.ts — the end-to-end full-product walkthrough video, driven
 * against a REAL `kitsoki web` server in the deterministic no-LLM posture
 * (--flow stories/prd/flows/happy_path.yaml: host responses stubbed, harness
 * nil, intents submitted explicitly — no LLM is ever called).
 *
 * Like the golden agent-actions spec, the WHOLE video is TOUR-DRIVEN: it runs
 * the MULTI_STORY_TOUR_STEPS from src/tour/generated/multi-story.ts via
 * window.__startTourWithSteps. The tour opens on the home story library, frames
 * the catalogue, drives home → new session → the interactive /chat view via a
 * route-match action step, narrates the PRD happy path turn-by-turn on the chat
 * surface, crosses a full page reload to show active sessions survive it, and
 * lands on the home active-sessions table. The spec asserts each step's `title`
 * against the live popover so the manifest and video cannot silently drift.
 *
 * The pipeline-advancing interactions (type the idea, send each PRD intent) are
 * NOT tour steps — the spec performs them as PRE-STEP HOOKS (exactly as the
 * design-walkthrough spec advances the pipeline before each spotlight) so
 * each spotlighted surface and state exists before the spotlight lands.
 *
 * THE RELOAD SEAM. A real `page.goto` reload tears down the in-memory Pinia
 * tour overlay, so the tour is injected ONCE for the interactive phase, then the
 * `ms-reload` step's pre-step hook performs the reload back to home and
 * RE-INJECTS the remaining steps (ms-reload onward). The reload is itself a
 * narrated step — "active sessions survive reload" is part of this demo's story.
 *
 * Uses a tmp DB (created in beforeAll, cleaned in afterAll). ADDR 7740.
 *
 * Record:  pnpm exec playwright test multi-story --project=chromium
 * Fast:    WEB_CHAT_PACE=0 pnpm exec playwright test multi-story --project=chromium
 */
import { test, expect, chromium, type Browser, type BrowserContext, type Page, type Locator } from "@playwright/test";
import { fileURLToPath } from "url";
import path from "path";
import fs from "fs";
import os from "os";
import { spawn, type ChildProcess } from "child_process";
import {
  makeShot,
  waitForState,
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
import { MULTI_STORY_TOUR_STEPS, type TourStep } from "../../src/tour/generated/multi-story.js";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

// repoRoot = .../kitsoki (tools/runstatus/tests/playwright -> 4 up)
const repoRoot = path.resolve(__dirname, "../../../..");
const BIN = path.join(repoRoot, "bin", "kitsoki");
// Point at the whole stories/ tree so the home screen shows the real catalogue.
const STORIES_DIR = path.join(repoRoot, "stories");
const FLOW = path.join(repoRoot, "stories", "prd", "flows", "happy_path.yaml");

const ADDR = demoAddr(7740);
const BASE = `http://${ADDR}`;
const RPC = `${BASE}/rpc`;

const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "multi-story");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "video");
// Feature-catalog source of truth for this spec's tour steps — each step
// becomes a chapter in the MP4's sidecar.
const CHAPTER_SOURCE = "features/multi-story.yaml";

// ── server lifecycle (tmp DB; spawned directly so we own the DB path) ─────────

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
  for (const p of [STORIES_DIR, FLOW, BIN]) {
    if (!fs.existsSync(p)) throw new Error(`missing required path: ${p} (run 'make build' first)`);
  }
  prepareVideoDir(VIDEO_DIR); // clears stale .webm files; must run before context creation

  tmpDbDir = fs.mkdtempSync(path.join(os.tmpdir(), "kitsoki-multi-story-"));
  const dbPath = path.join(tmpDbDir, "s.db");

  server = spawn(
    BIN,
    ["web", "--stories-dir", STORIES_DIR, "--flow", FLOW, "--addr", ADDR, "--db", dbPath],
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
  if (server && !server.killed) {
    server.kill("SIGTERM");
    await new Promise((r) => setTimeout(r, 500));
    if (!server.killed) server.kill("SIGKILL");
  }
  if (tmpDbDir) fs.rmSync(tmpDbDir, { recursive: true, force: true });
});

/** Resolve an action step's real target element. The new-session click must
 *  land on the PRD card specifically (the deterministic --flow posture is
 *  PRD-specific), so it's resolved to that card's button, not the first card. */
function resolveTarget(page: Page, step: TourStep): Locator {
  if (step.id === "ms-intro-start") {
    return prdCard(page).getByTestId("new-session-btn");
  }
  return page.getByTestId(step.target!).first();
}

/** The PRD story card (matched by its title). */
function prdCard(page: Page): Locator {
  return page
    .getByTestId("story-card")
    .filter({ has: page.getByTestId("story-title").filter({ hasText: "PRD authoring" }) });
}

/** Inject (or re-inject) a slice of the tour and confirm the overlay is up. */
async function injectTour(page: Page, steps: readonly TourStep[]): Promise<void> {
  await page.evaluate((stepsJson: string) => {
    (window as unknown as { __startTourWithSteps?: (s: string) => void })
      .__startTourWithSteps?.(stepsJson);
  }, JSON.stringify(steps));
  await expect(page.getByTestId("tour-overlay")).toBeVisible({ timeout: 8000 });
}

test.describe("multi-story full-product walkthrough (live, no-LLM)", () => {
  test("home → new session → drive PRD happy path → reload → active sessions", async () => {
    test.setTimeout(300000);

    // Startup discovers the whole stories/ catalogue but creates no sessions.
    const stories = await rpc<Array<{ path: string; app_id: string; title: string }>>(
      "runstatus.stories.list",
      {},
    );
    const prd = stories.find((s) => s.app_id === "prd");
    expect(prd, "PRD story is in the catalogue").toBeTruthy();
    const storyPath = prd!.path;

    const browser: Browser = await chromium.launch({ headless: true });
    const context: BrowserContext = await browser.newContext(
      cameraContext({ recordVideoDir: VIDEO_DIR }),
    );
    const page = await context.newPage();
    const video = page.video(); // capture BEFORE context.close()
    const shot = makeShot(ARTIFACT_DIR);
    const { mark, onThrow } = captureDiagnostics(page, ARTIFACT_DIR);

    // Accumulate per-step time windows for the chapter sidecar. The clock
    // starts now so windows line up with the recorded MP4 timeline. The
    // recorder lives in the spec's Node process, so it survives the ms-reload
    // page reload — chapters keep opening as steps settle after the seam.
    const chapters = new ChapterRecorder();

    // Captured once the intro's "New session" step creates the run.
    let sid = "";
    let reloaded = false; // guards the one-shot reload + re-inject in ms-reload.

    // Drive a text-slot intent: select the intent, type, send. These run as
    // pre-step hooks WHILE the tour overlay sits on the prior (centered) step,
    // whose backdrop intercepts hit-test clicks — so set the value through the
    // input's native setter (firing a real `input` event for Vue's v-model) and
    // dispatch the click on the send button directly, bypassing the backdrop.
    async function sendText(intent: string, text: string): Promise<void> {
      const select = page.getByTestId("composer-select");
      if ((await select.count()) > 0) await select.selectOption(intent).catch(() => undefined);
      const input = page.getByTestId("composer-input").first();
      await expect(input).toBeVisible({ timeout: 15000 });
      await input.evaluate((el, value) => {
        const node = el as HTMLInputElement | HTMLTextAreaElement;
        const proto = Object.getPrototypeOf(node);
        const setter = Object.getOwnPropertyDescriptor(proto, "value")?.set;
        setter?.call(node, value);
        node.dispatchEvent(new Event("input", { bubbles: true }));
      }, text);
      await dwell(page, SETTLE_MS);
      await page.getByTestId("composer-send").first().evaluate((el) => (el as HTMLElement).click());
    }
    // Drive a slot-less intent by dispatching the button's DOM click (the
    // overlay backdrop is up during the pre-step hook, so a hit-test click on
    // the button beneath it would be intercepted).
    async function clickIntent(intent: string): Promise<void> {
      const btn = page.getByTestId(`intent-btn-${intent}`).first();
      // toBeEnabled (not just toBeVisible): action buttons are
      // `:disabled="pending"` while the prior turn is in flight, and a DOM
      // .click() on a disabled button is a silent no-op. Wait out any pending
      // turn so the intent actually fires.
      await expect(btn).toBeEnabled({ timeout: 15000 });
      await btn.evaluate((el) => (el as HTMLElement).click());
    }

    try {
      // ── 1. Open the home story library and start the tour ON it ──────────────
      mark("navigating home");
      await cinematicGoto(page, `${BASE}/#/`, { waitForTestId: "home-view" });
      // Anchor the catalogue actually rendered before the tour frames it.
      await expect(page.getByTestId("story-card").first()).toBeVisible({ timeout: 15000 });
      await injectTour(page, MULTI_STORY_TOUR_STEPS);

      // ── 2. Walk the MULTI_STORY_TOUR_STEPS ───────────────────────────────────
      for (const step of MULTI_STORY_TOUR_STEPS) {
        mark(`step ${step.id}`);

        // ── The reload seam — runs BEFORE the route guard (we're still on
        // /chat here; the reload itself takes us home). ───────────────────────
        if (step.id === "ms-reload" && !reloaded) {
          reloaded = true;
          // Create a SECOND session via the live RPC so the home table is
          // populated (a lone session auto-navigates straight into its run).
          const sid2 = await rpc<{ session_id: string }>("runstatus.session.new", {
            story_path: storyPath,
          });
          expect(sid2.session_id, "second session minted").toBeTruthy();
          // The reload seam: hard-navigate back to home. This tears down the
          // Pinia tour overlay, so re-inject the REMAINING steps afterwards.
          await cinematicGoto(page, `${BASE}/#/`, { waitForTestId: "home-view" });
          await expect(page.getByTestId("session-row").first()).toBeVisible({ timeout: 15000 });
          const remaining = MULTI_STORY_TOUR_STEPS.slice(
            MULTI_STORY_TOUR_STEPS.indexOf(step),
          );
          await injectTour(page, remaining);
        }

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

        // ── Pre-step setup: advance the PRD pipeline so the surface exists ─────
        if (step.id === "ms-chat-idle") {
          // Fresh run lands in idle on the chat surface.
          await waitForState(page, "idle", 15000);
          await dwell(page, SETTLE_MS);
        }
        if (step.id === "ms-chat-composer") {
          // Send the idea (discuss self-transition: stays idle).
          await sendText("discuss", "I want a CLI for X");
          await waitForState(page, "idle", 15000);
          await dwell(page, SETTLE_MS);
        }
        if (step.id === "ms-chat-intents") {
          // Press start → the prior-art search gate; confirm (no overlap) →
          // clarifying. (The PRD happy path routes idle → search → clarifying.)
          await clickIntent("start");
          await waitForState(page, "search", 15000);
          await dwell(page, SETTLE_MS);
          await clickIntent("confirm");
          await waitForState(page, "clarifying", 15000);
          await dwell(page, SETTLE_MS);
        }
        if (step.id === "ms-chat-clarifying") {
          // The PRD clarifying gate is a TWO-turn dance (per
          // stories/prd/flows/happy_path.yaml + rooms/clarifying.yaml):
          //   (1) `answer` the questions in free text — the stubbed matcher
          //       resolves both q1+q2 (matched_count:2), so the "Answered so far"
          //       readout flips to 2/2, but the room STAYS in clarifying (a
          //       self-transition; there is no auto-advance here).
          //   (2) the slot-less `submit_answers` verb appends the round and
          //       advances clarifying → brief.
          //
          // The historical break sent `submit_answers` carrying the answer text
          // in ONE turn. `submit_answers` is a pure verb with no composer option
          // (the composer-select only renders with >1 text intent, and clarifying
          // has exactly one — `answer`), so selectOption silently fell back to the
          // default `answer` intent and the room never left clarifying (34×
          // "Expected brief, Received clarifying" in the old ERROR.txt).
          //
          // Wait on the 2/2 readout — NOT on the state, which never changes across
          // the answer self-transition — so the answer turn has fully settled and
          // its `pending` flag (which disables the action buttons) has cleared
          // before we click submit_answers.
          await sendText("answer", "developers, and the metric is time-to-first-success");
          await expect(page.getByText(/Answered so far \(2\/2\)/)).toBeVisible({ timeout: 15000 });
          await dwell(page, SETTLE_MS);
          await clickIntent("submit_answers");
          await waitForState(page, "brief", 15000);
          await dwell(page, SETTLE_MS);
        }
        if (step.id === "ms-chat-references") {
          // Confirm brief → references, confirm references → drafting.
          await clickIntent("confirm");
          await waitForState(page, "references", 15000);
          await dwell(page, SETTLE_MS);
          await clickIntent("confirm");
          await waitForState(page, "drafting", 15000);
          await dwell(page, SETTLE_MS);
        }
        if (step.id === "ms-chat-done") {
          // Accept → terminal exit (__exit__done).
          await clickIntent("accept");
          await waitForState(page, "__exit__done", 15000);
          await expect(page.getByTestId("state-badge")).toHaveAttribute("data-terminal", "true", {
            timeout: 15000,
          });
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
          const remaining = MULTI_STORY_TOUR_STEPS.slice(MULTI_STORY_TOUR_STEPS.indexOf(step) + 1);
          const isOnNext = remaining.some((s) => s.title === actualTitle);
          if (isOnNext) {
            mark(`  drift-skip: overlay on "${actualTitle}"`);
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
          await dwell(page, 700);
        } else {
          const target = resolveTarget(page, step);
          await target.scrollIntoViewIfNeeded().catch(() => undefined);
          if (step.advance === "route-match") {
            // Intro navigation (New session). The overlay's click-through hole
            // is aligned on the FIRST story-card, but the deterministic PRD card
            // may be elsewhere — so dispatch the DOM click directly, which fires
            // the control's @click regardless of the backdrop's hole geometry.
            await target.evaluate((el) => (el as HTMLElement).click());
            await page.waitForTimeout(300);
            if (step.advanceRoute === "interactive") {
              await page.waitForURL(/#\/s\/[0-9a-f-]{36}\/chat$/, { timeout: 15000 });
              const m = page.url().match(/\/s\/([0-9a-f-]{36})\/chat$/);
              if (m) {
                sid = m[1];
                mark(`session ${sid}`);
              }
            }
            await dwell(page, 1000);
          } else {
            // click-target: dispatch the DOM click directly so it fires both the
            // control's @click AND the overlay's capture-phase advance listener.
            await target.evaluate((el) => (el as HTMLElement).click());
            await dwell(page, 1000);
          }
        }
      }

      // The final step's "Done" closes the tour.
      await expect(page.getByTestId("tour-overlay")).toHaveCount(0, { timeout: 5000 });
    } catch (err) {
      onThrow(err);
      throw err;
    } finally {
      await page.close();
      await context.close(); // finalises the video
      const mp4 = await saveVideoAsMp4(video, ARTIFACT_DIR, "multi-story-demo");
      // Emit the producer-agnostic chapter sidecar beside the MP4: each tour
      // step → one chapter with source_ref kind=tour.
      writeChapters(mp4, chapters.list());
      await browser.close();
    }

    const pngs = fs.readdirSync(ARTIFACT_DIR).filter((f) => f.endsWith(".png"));
    console.log(`[multi-story] screenshots (${pngs.length}) in ${ARTIFACT_DIR}`);
  });
});
