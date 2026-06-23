/**
 * slidey-edit "annotate then refine" feature-tour video demo.
 *
 * Drives the slidey-edit tour against a real `kitsoki web` server in the
 * deterministic no-LLM posture (--flow demo_web.yaml; the flow's host_handlers
 * stub every host.* call, so NO host cassette is needed) and records a video +
 * per-scene screenshots to .artifacts/slidey-edit/.
 *
 * Like the golden agent-actions / dev-story-bugfix video specs, this spec runs
 * ONLY the SLIDEY_EDIT_TOUR_STEPS from src/tour/slidey-edit-manifest.ts via
 * window.__startTourWithSteps. The tour drives the whole video: it opens on the
 * home story library and its one route-match action step navigates home → the
 * drive (chat) view; the explain beats then narrate the reviewing → Annotate →
 * SemanticOverlay → pick → refine walk while the spec drives the matching
 * intents between beats.
 *
 * ── THE ARTIFACT BRIDGE (the one deterministic seam) ────────────────────────
 * Under `kitsoki web --flow` the host.artifacts_dir transport is STUBBED, so the
 * deck handle never resolves to real bytes: `/artifact/<handle>` 404s, its
 * `/poster` 404s, and `runstatus.artifact.semantic` returns no result. The
 * ANNOTATOR/OVERLAY/ANCHOR path itself runs fully unmocked in the SPA — only the
 * artifact TRANSPORT is bridged at the network edge, serving the REAL baked deck
 * files from stories/slidey-edit/baked/:
 *   - GET  **<base>/artifact/<h>/poster        → deck.poster.png bytes
 *   - GET  **<base>/artifact/<h>               → deck.mp4 bytes
 *   - POST **<base>/rpc {runstatus.artifact.semantic} → deck.semantic.json result
 * Every OTHER /rpc call falls through to the real kitsoki server (route.continue),
 * so the story machine, the view render, the choice intents, and the
 * annotate→refine dispatch are all genuine. This replicates the network-edge
 * bridge a prior agent used for the screenshot proof of this same surface.
 *
 * Validate fast (no dwells):
 *   WEB_CHAT_PACE=0 pnpm exec playwright test slidey-edit-video --project=chromium
 * Record at watch-speed:
 *   pnpm exec playwright test slidey-edit-video --project=chromium
 *
 * NOTE: the harness suppresses Playwright stdout, so per-step progress and any
 * failure context is also written to .artifacts/slidey-edit/ERROR.txt.
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
  ChapterRecorder,
  writeChapters,
  demoAddr,
  dwell,
  cinematicGoto,
  SETTLE_MS,
  type WebServer,
} from "./_helpers/server.js";
import { cameraContext } from "./_helpers/camera.js";
import { SLIDEY_EDIT_TOUR_STEPS, type TourStep } from "../../src/tour/slidey-edit-manifest.js";

// The manifest source of truth for this spec's tour steps: each step becomes a
// chapter (source_ref kind=tour) in the MP4's sidecar.
const CHAPTER_SOURCE = "tools/runstatus/src/tour/slidey-edit-manifest.ts";

// 7762 — confirmed free; distinct from every other spec's port so parallel runs
// never race on the same bind.
const ADDR = demoAddr(7762);
const STORY_DIR = path.join(repoRoot, "stories", "slidey-edit");
const FLOW = path.join(STORY_DIR, "flows", "demo_web.yaml");
// No host cassette: demo_web.yaml's host_handlers stub every host.* call along
// the idle → drafting → reviewing path.
const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "slidey-edit");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "video");
const ERROR_TXT = path.join(ARTIFACT_DIR, "ERROR.txt");

// The REAL baked deck artifacts — served by the network-edge bridge below so the
// deck handle resolves to genuine bytes even though host.artifacts_dir transport
// is stubbed under --flow.
const BAKED_DIR = path.join(STORY_DIR, "baked");
const DECK_MP4 = path.join(BAKED_DIR, "deck.mp4");
const DECK_POSTER = path.join(BAKED_DIR, "deck.poster.png");
const DECK_SEMANTIC = path.join(BAKED_DIR, "deck.semantic.json");

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
  // The baked files MUST exist — the whole demo is the proof they render.
  for (const p of [DECK_MP4, DECK_POSTER, DECK_SEMANTIC]) {
    if (!fs.existsSync(p)) throw new Error(`missing baked artifact: ${p}`);
  }
  server = await startWebServer({ addr: ADDR, flow: FLOW, storiesDir: STORY_DIR });
});

test.afterAll(() => server?.stop());

/** Assert the drive view's current-state reaches `state`. */
async function expectState(page: Page, state: string): Promise<void> {
  await expect(page.getByTestId("current-state")).toHaveText(state, { timeout: 15000 });
}

// Per-character typing delay, pace-scaled: 0 under WEB_CHAT_PACE=0 (fast
// validate, no dwells), ~42ms/char at the default watch pace so the viewer reads
// each input being COMPOSED rather than appearing atomically. The #1 demo
// legibility bug — and the one the operator hit here — is setting the textarea
// via el.value / fill(): the input then flashes and you never see what was asked.
// Type it for real instead.
const PACE = Number(process.env.WEB_CHAT_PACE ?? "1");
const TYPE_DELAY_MS = 42 * (Number.isFinite(PACE) ? PACE : 1);

/**
 * Type `value` into a room composer textarea VISIBLY and leave it
 * composed-but-UNSENT, so the step's narration screenshot captures the operator's
 * input on screen. Scrolls the input into view, focuses it, types
 * character-by-character (real keystrokes → real input events, so Send enables),
 * then HOLDS so the line is readable. Returns the form for a later submit.
 * `intent` selects the room's legacy composer (`form[data-active-intent=…]`).
 */
async function composeVisibly(page: Page, intent: string, value: string): Promise<Locator> {
  diag(`composeVisibly ${intent}="${value}"`);
  const form = page
    .locator(`form[data-testid="composer"][data-active-intent="${intent}"]`)
    .first();
  await expect(form).toBeVisible({ timeout: 15000 });
  const input = form.getByTestId("composer-input").first();
  await input.scrollIntoViewIfNeeded().catch(() => undefined);
  await input.click().catch(() => undefined);
  await input.fill("");
  await input.pressSequentially(value, { delay: TYPE_DELAY_MS });
  await dwell(page, SETTLE_MS); // hold on the composed input so it reads
  return form;
}

/** Submit a previously-composed form and assert the resulting state. */
async function submitComposed(page: Page, form: Locator, expectStateName: string): Promise<void> {
  await form.evaluate((el) => (el as HTMLFormElement).requestSubmit());
  await expectState(page, expectStateName);
  await dwell(page, SETTLE_MS);
}

/**
 * Open the unified ArtifactAnnotator on the deck media: click `media-annotate`,
 * which probes the semantic sidecar (bridged) and — because the deck HAS one —
 * opens the slidey substrate (`aa-slidey` → `aa-slidey-poster` + the
 * SemanticOverlay markers). Returns once the poster + overlay are on-screen.
 */
async function openAnnotator(page: Page): Promise<void> {
  diag("openAnnotator: click media-annotate");
  const btn = page.getByTestId("media-annotate").first();
  await expect(btn).toBeVisible({ timeout: 15000 });
  await btn.evaluate((el) => (el as HTMLElement).click());
  // The slidey stage + poster + overlay must compose before any spotlight.
  await expect(page.getByTestId("aa-slidey").first()).toBeVisible({ timeout: 15000 });
  await expect(page.getByTestId("aa-slidey-poster").first()).toBeVisible({ timeout: 15000 });
  await expect(page.getByTestId("semantic-overlay").first()).toBeVisible({ timeout: 15000 });
  // The card_0 marker is the one se-pick spotlights — wait for the sidecar boxes
  // to have drawn before we narrate them.
  await expect(page.getByTestId("so-marker-1/card_0").first()).toBeVisible({ timeout: 15000 });
  await dwell(page, SETTLE_MS);
}

test("slidey-edit annotate → refine feature-tour video", async () => {
  test.setTimeout(300000);
  const browser: Browser = await chromium.launch({ headless: true });
  const context: BrowserContext = await browser.newContext(
    cameraContext({ recordVideoDir: VIDEO_DIR }),
  );

  // ── The artifact bridge (network-edge, transport-only) ────────────────────
  // Order matters: poster is the more specific path, registered first. Each
  // route serves the REAL baked bytes; everything else (including all other /rpc
  // methods) falls through to the live kitsoki server via route.continue().
  await context.route("**/artifact/*/poster", async (route) => {
    await route.fulfill({
      contentType: "image/png",
      body: fs.readFileSync(DECK_POSTER),
    });
  });
  await context.route("**/artifact/*", async (route) => {
    await route.fulfill({
      contentType: "video/mp4",
      body: fs.readFileSync(DECK_MP4),
    });
  });
  await context.route("**/rpc", async (route) => {
    const req = route.request();
    let method = "";
    try {
      method = (route.request().postDataJSON() as { method?: string })?.method ?? "";
    } catch {
      /* not JSON — let the server handle it */
    }
    if (req.method() === "POST" && method === "runstatus.artifact.semantic") {
      const sidecar = JSON.parse(fs.readFileSync(DECK_SEMANTIC, "utf-8"));
      await route.fulfill({
        contentType: "application/json",
        body: JSON.stringify({ jsonrpc: "2.0", id: 1, result: sidecar }),
      });
      return;
    }
    await route.continue();
  });

  const page: Page = await context.newPage();
  const video = page.video();
  const shot = makeShot(ARTIFACT_DIR);

  // Accumulate per-step time windows for the chapter sidecar. The clock starts
  // now so windows line up with the recorded MP4 timeline.
  const chapters = new ChapterRecorder();

  let sessionId = "";
  // Composers typed (visibly) in a step's pre-step block and submitted in its
  // post-step block, AFTER the narration screenshot has captured the input.
  let startForm: Locator | null = null;
  let refineForm: Locator | null = null;

  try {
    // ── 1. Open the home story library and start the tour ON it ──────────────
    diag("navigating home");
    await cinematicGoto(page, `${server.base}/#/`, { waitForTestId: "home-view" });

    await page.evaluate((stepsJson: string) => {
      (window as unknown as { __startTourWithSteps?: (s: string) => void })
        .__startTourWithSteps?.(stepsJson);
    }, JSON.stringify(SLIDEY_EDIT_TOUR_STEPS));
    await expect(page.getByTestId("tour-overlay")).toBeVisible({ timeout: 8000 });

    // ── 2. Walk SLIDEY_EDIT_TOUR_STEPS ───────────────────────────────────────
    for (const step of SLIDEY_EDIT_TOUR_STEPS) {
      diag(`step ${step.id}`);
      // Mirror the overlay's route-guard. The intro home steps are "home"; once
      // we click New session we're on /chat ("interactive"); the walk steps are
      // route "any" so they show on the drive view.
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

      // ── Pre-step setup: stage the surface a step spotlights BEFORE it shows ──
      // se-author is the first interactive beat: TYPE the deck request into the
      // idle `start` composer and leave it composed-but-unsent, so this step's
      // narration screenshot shows the operator's input on screen (the submit +
      // accept happen in the post-step block below, after the shot).
      if (step.id === "se-author") {
        await expectState(page, "idle");
        startForm = await composeVisibly(
          page,
          "start",
          "author a tight 3-scene explainer deck",
        );
      }
      // se-refine: back at reviewing, CLOSE the annotator panel so the refine
      // composer is the clear focus, then TYPE the refinement instruction and
      // leave it composed-but-unsent for this step's screenshot (submit + close
      // the loop in the post-step block).
      if (step.id === "se-refine") {
        await page
          .getByTestId("media-annotate-close")
          .first()
          .evaluate((el) => (el as HTMLElement).click())
          .catch(() => undefined);
        await dwell(page, SETTLE_MS);
        refineForm = await composeVisibly(
          page,
          "refine",
          "tighten the callout I pointed at and add a one-line example beneath it",
        );
      }
      // se-overlay/se-markers/se-pick spotlight the annotator substrate — open it
      // (and let the poster + sidecar markers draw) just before se-overlay.
      if (step.id === "se-overlay") {
        await openAnnotator(page);
      }
      // se-loop-closed proves the refine loop closed: assert the badge reads
      // `reviewing` BEFORE its narration screenshot, so the captured frame is the
      // hard evidence the QA gate cites (state=reviewing after refine).
      if (step.id === "se-loop-closed") {
        // current-state is the state-NAME badge (state-badge is the live/terminal
        // indicator). Assert it reads `reviewing` before the narration screenshot
        // so the captured frame is the hard evidence the QA gate cites.
        await expectState(page, "reviewing");
      }

      // Honor DOM-presence preconditions.
      if (step.waitForTarget) {
        await expect(page.getByTestId(step.waitForTarget).first()).toBeVisible({ timeout: 15000 });
      }

      // If the overlay drifted ahead of this loop step (its anchoring can
      // auto-advance when a target/route settles), re-sync it to THIS step so the
      // popover narrates the surface we're about to show.
      const titleEl = page.getByTestId("tour-title");
      const actualTitle = (await titleEl.textContent({ timeout: 8000 }).catch(() => "")) ?? "";
      if (actualTitle !== step.title) {
        diag(`  re-sync overlay from "${actualTitle}" → "${step.title}"`);
        await page.evaluate((id: string) => {
          (window as unknown as { __tourGoTo?: (s: string) => void }).__tourGoTo?.(id);
        }, step.id);
      }
      await expect(titleEl).toHaveText(step.title, { timeout: 12000 });

      // This step's spotlight is settled and on-screen — open its chapter
      // (auto-closes the prior one) so the dwell below becomes its window.
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
          const m = page.url().match(/\/s\/([0-9a-f-]{36})\/chat$/);
          if (m) {
            sessionId = m[1];
            diag(`session ${sessionId}`);
          }
        }
        await dwell(page, 1000);
      } else {
        // explain step: drive the associated gesture (if any) on-camera AFTER its
        // narration screenshot, then advance the overlay.
        if (step.id === "se-pick") {
          // Click the spotlit marker — the location-tied gesture. The overlay
          // backdrop leaves a click-through hole, but dispatch the DOM click
          // directly so it fires regardless of paint order.
          diag("se-pick: click so-marker-1/card_0");
          await page
            .getByTestId("so-marker-1/card_0")
            .first()
            .evaluate((el) => (el as HTMLElement).click());
          // The anchor dispatch confirmation renders in the annotate panel.
          await expect(
            page.getByTestId("media-annotate-panel").first(),
          ).toBeVisible({ timeout: 15000 });
          await dwell(page, SETTLE_MS);
        }
        if (step.id === "se-author" && startForm) {
          // The operator's request was typed (visibly) in the pre-step and shown
          // in this step's screenshot. Submit it → drafting, where the next beat
          // (se-drafting) lets the viewer READ the authored plan before anything
          // is approved. No second typed message here: accept is a single button
          // click on the drafting plan, not a "looks good" message about a deck
          // the viewer hasn't seen yet.
          await submitComposed(page, startForm, "drafting");
          startForm = null;
        }
        if (step.id === "se-drafting") {
          // The operator typed ONE request and has now READ the authored plan
          // (this beat's screenshot shows the drafting Summary). Accept it to
          // render — drafting is interpretive (the state-diagram pill is a viz,
          // not a submit, and the semantic composer is non-deterministic without
          // an LLM), so the demo drives the verified explicit `accept` intent
          // through the store (drafting:accept → rendering → reviewing), exactly
          // as a flow fixture's `intent: accept` would. NO second typed message:
          // acceptance is a single advance AFTER the plan was reviewed, so the
          // viewer never sees two prompts in a row before the deck appears.
          diag("se-drafting: accept the reviewed plan via __kitsokiSubmitIntent");
          await page.evaluate(async () => {
            await (window as unknown as {
              __kitsokiSubmitIntent?: (n: string, s?: Record<string, unknown>) => Promise<void>;
            }).__kitsokiSubmitIntent?.("accept", {});
          });
          await expectState(page, "reviewing");
          await expect(page.getByTestId("media-element").first()).toBeVisible({ timeout: 15000 });
          await dwell(page, SETTLE_MS);
        }
        if (step.id === "se-refine" && refineForm) {
          // The refinement instruction was typed (visibly) in the pre-step and
          // shown in this step's screenshot. Submit it, then WAIT for the whole
          // loop to close back to reviewing with the cycle advanced — the proof
          // the rerender ran (not a refining→reviewing error bounce). The
          // reviewing room renders `Cycle: {{ world.cycle }}` as a kv pair.
          await refineForm.evaluate((el) => (el as HTMLFormElement).requestSubmit());
          refineForm = null;
          await expectState(page, "reviewing");
          await expect(
            page.locator(".ve-kv").filter({ hasText: "Cycle" }).first(),
          ).toContainText("1", { timeout: 15000 });
          await expect(page.getByTestId("media-element").first()).toBeVisible({ timeout: 15000 });
          await dwell(page, SETTLE_MS);
        }
        await page.getByTestId("tour-next").click();
        await dwell(page, 700);
      }
    }

    // The final se-done step's "Done" closes the tour.
    await expect(page.getByTestId("tour-overlay")).toHaveCount(0, { timeout: 5000 });
  } catch (e) {
    diag(`FAILED: ${e instanceof Error ? e.stack ?? e.message : String(e)}`);
    diag(`--- server log ---\n${server?.log?.() ?? ""}`);
    throw e;
  } finally {
    await context.close();
    const mp4 = await saveVideoAsMp4(video, ARTIFACT_DIR, "slidey-edit-demo");
    // Emit the producer-agnostic chapter sidecar beside the MP4: each tour step →
    // one chapter with source_ref kind=tour.
    writeChapters(mp4, chapters.list());
    await browser.close();
  }

  const pngs = fs.readdirSync(ARTIFACT_DIR).filter((f) => f.endsWith(".png"));
  console.log(`[slidey-edit-video] screenshots (${pngs.length}) in ${ARTIFACT_DIR}`);
});
