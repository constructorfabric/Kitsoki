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
import { test, expect, chromium, type Browser, type BrowserContext, type Page } from "@playwright/test";
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

/**
 * Drive a single text-slot intent through the legacy composer the room renders
 * when it has exactly one text intent (`form[data-testid="composer"]` carrying
 * `data-active-intent="<intent>"`, with a `composer-input` textarea + Send).
 * Both `start` at idle (optional `feedback`) and `refine` at reviewing render
 * this way. The Send button is disabled until the textarea has a value, so we
 * fire the input event first (enabling it), then submit the form. DOM-level so
 * the overlay backdrop never intercepts. Asserts the resulting current-state.
 */
async function driveComposer(
  page: Page,
  intent: string,
  value: string,
  expectStateName: string,
): Promise<void> {
  diag(`driveComposer ${intent}="${value}" → ${expectStateName}`);
  const form = page
    .locator(`form[data-testid="composer"][data-active-intent="${intent}"]`)
    .first();
  await expect(form).toBeVisible({ timeout: 15000 });
  const input = form.getByTestId("composer-input").first();
  await input.evaluate((el, v) => {
    const t = el as HTMLTextAreaElement;
    t.value = v;
    t.dispatchEvent(new Event("input", { bubbles: true }));
  }, value);
  await dwell(page, 600);
  await form.evaluate((el) => (el as HTMLFormElement).requestSubmit());
  await expectState(page, expectStateName);
  await dwell(page, 600);
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
      // se-reviewing is the first interactive beat: drive the run idle → drafting
      // → reviewing so the deck media is present for the whole annotate walk.
      if (step.id === "se-reviewing") {
        await expectState(page, "idle");
        // idle is a normal room: `start` is its single text intent (optional
        // `feedback`) → the legacy composer (textarea + Send). Drive it on-camera.
        await driveComposer(page, "start", "author a tight 3-scene explainer deck", "drafting");
        // drafting is INTERPRETIVE (a host.agent.task authors the deck) and
        // renders the free-text SEMANTIC composer — no intent buttons, and the
        // semantic router is non-deterministic without an LLM. Drive the verified
        // explicit `accept` through THIS view's own store path via the
        // __kitsokiSubmitIntent test hook (the same technique dev-story-bugfix
        // uses for its semantic triage room), so the chat re-renders reactively.
        // accept → rendering → (auto emit_intent accept) → reviewing.
        diag("se-reviewing: submit accept via __kitsokiSubmitIntent (drafting is semantic)");
        await page.evaluate(async () => {
          await (window as unknown as {
            __kitsokiSubmitIntent?: (n: string, s?: Record<string, unknown>) => Promise<void>;
          }).__kitsokiSubmitIntent?.("accept", {});
        });
        await expectState(page, "reviewing");
        // The reviewing room renders the deck media element (handle slidey-edit#1).
        await expect(page.getByTestId("media-element").first()).toBeVisible({ timeout: 15000 });
        await dwell(page, SETTLE_MS);
      }
      // se-overlay/se-markers/se-pick spotlight the annotator substrate — open it
      // (and let the poster + sidecar markers draw) just before se-overlay.
      if (step.id === "se-overlay") {
        await openAnnotator(page);
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
        if (step.id === "se-refine") {
          // Close the annotator panel so the refine param-form composer is the
          // clear focus, then drive the location-tied refine.
          await page
            .getByTestId("media-annotate-close")
            .first()
            .evaluate((el) => (el as HTMLElement).click())
            .catch(() => undefined);
          await dwell(page, SETTLE_MS);
          // refine → refining → (auto) reviewing: the deterministic settle point
          // is back at reviewing with the deck re-rendered. The location-tied
          // anchor (the marker we picked) is what the reviser edits.
          await driveComposer(
            page,
            "refine",
            "tighten the callout I pointed at and add a one-line example beneath it",
            "reviewing",
          );
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
