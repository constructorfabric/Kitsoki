/**
 * Spatial-oracle feature-spotlight video demo.
 *
 * Records a deterministic, NO-LLM tour of the spatial capture feature — the
 * /review spatial picker (click a frame → resolve the DOM element under the
 * click → {selector, role, text} chip), the per-flag chat that rides the
 * {frame, point, element} bundle to a STUBBED read-only off-path oracle, the
 * inline thumbnail + chip render on the answer, and the chrome-less /point
 * handoff window — into .artifacts/spatial-oracle/.
 *
 * HOUSE STYLE: the /review portion is narrated by the REAL in-product tour
 * overlay (TourOverlay.vue: the "STEP N OF M" popover card with title / body /
 * Skip / Back / Next + a spotlight ring), exactly like the agent-actions /
 * trace-features demos. The SPATIAL_ORACLE_TOUR_STEPS array is injected into the
 * live overlay via window.__startTourWithSteps, and each step's real popover
 * title is asserted against the manifest (a drift guard). The spatial gestures
 * (flag the scene, pin a point, ask the question) are interleaved BETWEEN the
 * overlay advances — the picker/answer testids that later steps spotlight only
 * exist after those gestures, so each gesture runs just before the step that
 * narrates its result, mirroring how agent-actions interleaves real clicks with
 * its explain popovers.
 *
 * THE ONE EXCEPTION: the chrome-less /point window renders ONLY <PointPage>
 * (App.vue: `<PointPage v-if="chromeless">`) — the whole normal shell, INCLUDING
 * <TourOverlay>, is v-if'd out there. So the /point beat (so-point-window +
 * so-done) keeps the PORTABLE makeCaption + makeSpotlight helpers; the real
 * overlay simply does not exist on that route.
 *
 * POSTURE: this reuses spatial-capture.spec.ts's deterministic posture: the
 * built dist/index.html is served by a tiny static server WITHOUT an inlined
 * snapshot (so createDataSource() returns LiveSource and issues real JSON-RPC,
 * AND the tour store's snapshot guard is inert so __startTourWithSteps drives
 * the overlay), and every RPC — including the offpath oracle — is STUBBED via
 * page.route with canned, reproducible answers. No live kitsoki server, no LLM,
 * no cost.
 *
 * Validate fast (no dwells):
 *   WEB_CHAT_PACE=0 pnpm exec playwright test spatial-oracle-video --project=chromium
 * Record at watch-speed:
 *   pnpm exec playwright test spatial-oracle-video --project=chromium
 *
 * The harness suppresses Playwright stdout, so per-step progress + any failure
 * context is written to .artifacts/spatial-oracle/ERROR.txt and the NN-*.png
 * breadcrumbs.
 */
import { test, expect, chromium, type Browser, type BrowserContext, type Page } from "@playwright/test";
import { fileURLToPath } from "url";
import path from "path";
import fs from "fs";
import http from "http";
import type { AddressInfo } from "net";
import {
  makeShot,
  prepareVideoDir,
  saveVideoAsMp4,
  dwell,
  ChapterRecorder,
  writeChapters,
  SETTLE_MS,
} from "./_helpers/server.js";
import { makeCaption, makeSpotlight, captureDiagnostics, DEMO_VIEWPORT } from "./_helpers/demo.js";
import { SPATIAL_ORACLE_TOUR_STEPS, type TourStep } from "../../src/tour/spatial-oracle-manifest.js";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
// _helpers → playwright → tests → runstatus (project root for dist/)
const projectRoot = path.resolve(__dirname, "../..");
// _helpers → playwright → tests → runstatus → tools → kitsoki (repo root)
const repoRoot = path.resolve(__dirname, "../../../..");

const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "spatial-oracle");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "video");
const CHAPTER_SOURCE = "tools/runstatus/src/tour/spatial-oracle-manifest.ts";

const SID = "sess-spatial";
const VIDEO = "demo_video#ab12cd34";

// The /review steps are narrated by the REAL overlay; the /point beat keeps the
// portable caption (no overlay on the chromeless route). Split the manifest on
// that boundary so each half is driven by its own mechanism.
const REVIEW_STEP_IDS = [
  "so-intro",
  "so-review",
  "so-flag",
  "so-picker",
  "so-point",
  "so-element",
  "so-ask",
  "so-answer",
] as const;
const REVIEW_STEPS: TourStep[] = SPATIAL_ORACLE_TOUR_STEPS.filter((s) =>
  (REVIEW_STEP_IDS as readonly string[]).includes(s.id),
);

// A valid 1×1 red PNG (same bytes as spatial-capture.spec.ts / the Go fixture).
const ONE_PX_PNG = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGNgYGAAAAAEAAHzwAAAAABJRU5ErkJggg==",
  "base64",
);

// The checked-in deterministic rrweb fixture — recorded at a FIXED 1280×720
// viewport: a CONTENT-RICH real kitsoki room (the bugfix story's review surface —
// the AGENT panel, the ACTIONS intent buttons, the state diagram + trace panels).
// When videoEvents returns it, /review renders the rrweb Replayer (REAL
// reconstructed UI) under the picker, so a click resolves a real control
// (intent-btn-start) against the reconstructed DOM — the headline of the feature.
const REC_W = 1280;
const REC_H = 720;
// The Start intent button's center in those natural pixels (bbox {x:20,y:481,w:177,h:47}).
const START_CENTER = { x: 108, y: 504 };
const REPLAY_EVENTS = JSON.parse(
  fs.readFileSync(
    path.join(projectRoot, "tests", "fixtures", "spatial-replay.rrweb.json"),
    "utf-8",
  ),
) as unknown[];

const CHAPTERS = [
  {
    index: 0,
    id: "intro",
    label: "Intro",
    start_ms: 0,
    end_ms: 10000,
    source_ref: { kind: "slidey", spec_path: "deck.json", scene_id: "intro" },
  },
];

// The STUBBED oracle answer — deterministic + reproducible, no LLM.
const STUB_ANSWER =
  "That's the video player control. It's disabled here because no source clip resolved for this scene.";

/** A tiny static server: serves the built SPA on GET and stubs the /point/return
 *  POST so the chrome-less window's send() resolves. Mirrors spatial-capture's
 *  startStaticServer, extended for the /point return endpoint. */
function startStaticServer(html: string): Promise<{ origin: string; close: () => void }> {
  return new Promise((resolve) => {
    const server = http.createServer((req, res) => {
      if (req.method === "POST" && (req.url ?? "").startsWith("/point/return")) {
        res.setHeader("Content-Type", "application/json");
        res.end(JSON.stringify({ ok: true }));
        return;
      }
      // Any GET path (/, /point, …) serves the SPA shell; the SPA reads the
      // hash route or ?chromeless flag to decide what to render.
      res.setHeader("Content-Type", "text/html");
      res.end(html);
    });
    server.listen(0, "127.0.0.1", () => {
      const { port } = server.address() as AddressInfo;
      resolve({ origin: `http://127.0.0.1:${port}`, close: () => server.close() });
    });
  });
}

/** Install the page.route RPC + artifact stubs for the no-LLM posture.
 *
 * `getFrameStill` lets the test hand the artifact route a REAL screenshot of the
 * reconstructed kitsoki UI (captured off the /review rrweb replay frame) once it
 * has painted. Until then it returns null and the route falls back to ONE_PX_PNG,
 * so nothing breaks before the capture is taken. With the still set, the /point
 * window's `<img :src=artifactUrl(...)>` shows real UI behind the SpatialPicker
 * instead of a 1×1 pixel scaled into a black rectangle (and the /review fd-still
 * thumbnail becomes real too). */
async function installStubs(page: Page, getFrameStill: () => Buffer | null): Promise<void> {
  await page.route("**/rpc", async (route) => {
    const body = route.request().postDataJSON() as { method: string; params: Record<string, unknown> };
    let result: unknown = {};
    switch (body.method) {
      case "runstatus.video.chapters":
        result = { chapters: CHAPTERS };
        break;
      case "runstatus.video.events":
        result = { events: REPLAY_EVENTS, width: REC_W, height: REC_H };
        break;
      case "runstatus.video.frame":
        result = { handle: "frame#deadbeef", mime: "image/png", kind: "image" };
        break;
      case "runstatus.session.offpath":
        result = { answer: STUB_ANSWER };
        break;
      default:
        result = {};
    }
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({ jsonrpc: "2.0", id: 1, result }),
    });
  });
  // The captured still + frame media resolve to a REAL screenshot of the
  // reconstructed kitsoki UI once /review has painted it; before then (and as a
  // safe fallback) they resolve to the 1×1 PNG.
  await page.route("**/artifact/**", async (route) => {
    await route.fulfill({ contentType: "image/png", body: getFrameStill() ?? ONE_PX_PNG });
  });
}

/** Look up a manifest step by id (throws if a referenced step ever drifts). */
function step(id: string): TourStep {
  const s = SPATIAL_ORACLE_TOUR_STEPS.find((x) => x.id === id);
  if (!s) throw new Error(`spatial-oracle manifest has no step "${id}"`);
  return s;
}

test("spatial oracle feature-spotlight video", async () => {
  test.setTimeout(300000);

  const distIndex = path.join(projectRoot, "dist", "index.html");
  if (!fs.existsSync(distIndex)) {
    throw new Error(`dist/index.html not found — run the build (globalSetup) first`);
  }
  const html = fs.readFileSync(distIndex, "utf-8");
  const { origin, close } = await startStaticServer(html);

  prepareVideoDir(VIDEO_DIR);
  fs.mkdirSync(ARTIFACT_DIR, { recursive: true });

  const browser: Browser = await chromium.launch({ headless: true });
  const context: BrowserContext = await browser.newContext({
    viewport: DEMO_VIEWPORT,
    deviceScaleFactor: 2,
    recordVideo: { dir: VIDEO_DIR, size: DEMO_VIEWPORT },
  });
  const page: Page = await context.newPage();
  const video = page.video();
  const shot = makeShot(ARTIFACT_DIR);
  const chapters = new ChapterRecorder();
  const { mark, onThrow } = captureDiagnostics(page, ARTIFACT_DIR);

  // A real screenshot of the reconstructed kitsoki UI, captured off the /review
  // rrweb replay frame after it paints. The artifact route serves it for the
  // /point window's frame (and the /review fd-still) once it's set; until then
  // it's null and the route falls back to ONE_PX_PNG so nothing breaks early.
  let frameStill: Buffer | null = null;

  await installStubs(page, () => frameStill);

  try {
    // ── Stage /review behind a settle so the camera arrives on a composed view ─
    mark("goto /review");
    const reviewUrl = `${origin}/#/review/${SID}?video=${encodeURIComponent(VIDEO)}`;
    await page.goto(reviewUrl);
    await expect(page.getByTestId("review-page")).toBeVisible({ timeout: 15000 });
    await dwell(page, SETTLE_MS);

    // ── Start the REAL in-product tour overlay on /review ────────────────────
    // Inject only the /review steps (so-intro … so-answer) into the live
    // TourOverlay via the same driver the agent-actions spec uses. The overlay
    // is mounted globally at the App shell and its steps are route-agnostic
    // (route:"any"), so it renders the popover card on /review. The /point beat
    // (so-point-window + so-done) is excluded here — that chromeless route has
    // no overlay — and narrated with makeCaption further below.
    mark("start tour overlay");
    await page.evaluate((stepsJson: string) => {
      (window as unknown as { __startTourWithSteps?: (s: string) => void })
        .__startTourWithSteps?.(stepsJson);
    }, JSON.stringify(REVIEW_STEPS));
    await expect(page.getByTestId("tour-overlay")).toBeVisible({ timeout: 8000 });

    const titleEl = page.getByTestId("tour-title");

    /**
     * Walk ONE overlay step: assert the popover title matches the manifest (drift
     * guard), open the chapter, dwell, screenshot, then advance the overlay via
     * its own Next button — exactly the agent-actions loop. The spatial gestures
     * that conjure a later step's spotlight target run via `before`, fired AFTER
     * the popover for THIS step has surfaced but BEFORE we dwell+advance.
     */
    async function overlayStep(
      id: string,
      opts: { before?: () => Promise<void> } = {},
    ): Promise<TourStep> {
      const s = step(id);
      mark(s.id);
      // The popover must be showing THIS step's title before we narrate it.
      await expect(titleEl).toHaveText(s.title, { timeout: 12000 });
      // Conjure the next-step target now (the gesture's RESULT is what the
      // upcoming step spotlights), while this step's popover is on camera.
      if (opts.before) await opts.before();
      chapters.open(s.id, s.title, CHAPTER_SOURCE);
      await dwell(page, s.dwellMs ?? 4000);
      await shot(page, s.id);
      // Advance the REAL overlay — its Next button (or "Done" on the last step).
      await page.getByTestId("tour-next").click();
      // Let the spotlight animation move to the next target before the next
      // toHaveText assertion (mirrors agent-actions' post-Next beat).
      await dwell(page, 700);
      return s;
    }

    // ── 1. Intro ─────────────────────────────────────────────────────────────
    await overlayStep("so-intro");

    // ── 2. The review surface ────────────────────────────────────────────────
    await overlayStep("so-review");

    // ── 3. Flag a scene → selects a flag → mounts the picker ─────────────────
    // The so-flag popover spotlights the chapter timeline; its `before` performs
    // the real flag gesture so the NEXT step (so-picker) has a mounted picker to
    // spotlight. We flag AFTER so-flag's popover is asserted on camera.
    await overlayStep("so-flag", {
      before: async () => {
        mark("flag the scene");
        await page.getByTestId("ct-marker-intro").click();
        await page.getByTestId("ct-flag-btn").click();
        await expect(page.getByTestId("flag-detail")).toBeVisible();
        // The reconstructed-DOM replay frame renders the REAL UI under the picker.
        await expect(page.getByTestId("rp-replay-frame")).toBeVisible();
        await expect(page.getByTestId("spatial-picker")).toBeVisible({ timeout: 10000 });
        await dwell(page, SETTLE_MS);
      },
    });

    // ── 4. The picker overlay ────────────────────────────────────────────────
    // so-picker spotlights spatial-picker (now mounted). Its `before` pins the
    // point so the NEXT steps (so-point=sp-point crosshair, so-element=fd-element
    // chip) have their targets present.
    await overlayStep("so-picker", {
      before: async () => {
        mark("click the Start button");
        const picker = page.getByTestId("spatial-picker");
        const pbox = await picker.boundingBox();
        if (!pbox) throw new Error("picker has no bounding box");
        // position is relative to the picker box, which covers the rendered
        // (scaled) replay exactly — so the fraction of natural pixels equals the
        // box fraction.
        // The so-picker tour popover can sit over the (left-side) Start button
        // and intercept the picker click. Drop the popover's pointer-events for
        // the click (it stays VISIBLE on camera), then restore so its Next works.
        const popPE = (pe: string) =>
          page.evaluate((v) => {
            const p = document.querySelector('[data-testid="tour-popover"]');
            if (p) (p as HTMLElement).style.pointerEvents = v;
          }, pe);
        await popPE("none");
        await picker.click({
          position: {
            x: (START_CENTER.x / REC_W) * pbox.width,
            y: (START_CENTER.y / REC_H) * pbox.height,
          },
        });
        await popPE("");
        await expect(page.getByTestId("sp-point")).toBeVisible();
        await expect(page.getByTestId("fd-element")).toBeVisible();
        // Resolution against the reconstructed DOM: a REAL app control, not <video>.
        await expect(page.getByTestId("fd-element")).toContainText("intent-btn-start");
        await dwell(page, SETTLE_MS);
      },
    });

    // ── 5. Crosshair + resolved element chip ─────────────────────────────────
    await overlayStep("so-point");
    await overlayStep("so-element");

    // ── 6. Ask a question → stubbed oracle answer renders ────────────────────
    // so-ask spotlights the composer; its `before` types + sends so the NEXT
    // step (so-answer=fd-chat) has the stubbed answer to spotlight.
    await overlayStep("so-ask", {
      before: async () => {
        mark("ask the question");
        await page.getByTestId("fd-chat-box").fill("what is this control?");
        // The overlay spotlights fd-chat-box; its backdrop covers fd-chat-send
        // (outside the hole), so a hit-test click is intercepted. Dispatch the
        // DOM click directly — mirrors agent-actions' click-through technique.
        await page
          .getByTestId("fd-chat-send")
          .evaluate((el) => (el as HTMLElement).click());
        // The stubbed answer renders in the chat transcript.
        await expect(page.getByTestId("fd-chat")).toContainText(STUB_ANSWER, { timeout: 8000 });
        // The captured frame thumbnail + the element chip stay alongside it.
        await expect(page.getByTestId("fd-still")).toBeVisible();
        await expect(page.getByTestId("fd-element")).toBeVisible();
        await dwell(page, SETTLE_MS);
      },
    });

    // ── 7. The answer, with context ──────────────────────────────────────────
    // Last /review step: its "Next" reads "Done" and closes the overlay.
    await overlayStep("so-answer");
    // The /review tour has finished — the overlay is gone.
    await expect(page.getByTestId("tour-overlay")).toHaveCount(0, { timeout: 5000 });

    // ── Capture a REAL still of the reconstructed kitsoki UI ──────────────────
    // The /review rrweb replay frame has painted real reconstructed UI by now.
    // Screenshot it into a Buffer and hand it to the artifact route, so the
    // /point window's frame (and the /review fd-still thumbnail) show real UI
    // instead of a 1×1 PNG scaled into a black box. Assert the frame is visible
    // and let it settle so the captured region is not blank.
    mark("capture replay still");
    const replayFrame = page.getByTestId("rp-replay-frame");
    await expect(replayFrame).toBeVisible();
    await dwell(page, SETTLE_MS);
    frameStill = await replayFrame.screenshot();

    // ── 8. The chrome-less /point handoff window (PORTABLE caption) ───────────
    // The chromeless route renders ONLY <PointPage>; <TourOverlay> is v-if'd out,
    // so this beat MUST use the portable makeCaption + makeSpotlight helpers.
    mark("goto /point chromeless");
    const pointUrl =
      `${origin}/point?chromeless=1&token=tok-demo` +
      `&media_handle=${encodeURIComponent("frame#deadbeef")}&t_ms=0` +
      `&route=${encodeURIComponent(`/review/${SID}`)}` +
      `&prompt=${encodeURIComponent("Point at what you mean, then send.")}`;
    await page.goto(pointUrl);
    await expect(page.getByTestId("point-page")).toBeVisible({ timeout: 15000 });
    // Portable narration: caption banner + spotlight box (both pointer-events:none).
    const caption = await makeCaption(page);
    // The /point window fills most of the viewport, so the default top-centre
    // caption would cover the frame. Pin it to the bottom margin (below the
    // composer) for this beat so the reconstructed frame stays fully visible.
    await page.addStyleTag({
      content: "#demo-caption{top:auto !important;bottom:26px !important;max-width:80% !important}",
    });
    const spotlight = await makeSpotlight(page);
    await dwell(page, SETTLE_MS);

    // Pin a point in the handoff window's picker so its crosshair shows on camera.
    mark("point in handoff window");
    await page.getByTestId("spatial-picker").click({ position: { x: 120, y: 90 } });
    await expect(page.getByTestId("sp-point")).toBeVisible();
    await page.getByTestId("pp-input").fill("why is this disabled here?");
    await dwell(page, SETTLE_MS);

    {
      const s = step("so-point-window");
      mark(s.id);
      await spotlight(s.target ? `[data-testid="${s.target}"]` : null);
      chapters.open(s.id, s.title, CHAPTER_SOURCE);
      await caption(s.title, s.body, s.dwellMs ?? 4000);
      await shot(page, s.id);
    }

    // ── 9. Done (PORTABLE caption) ───────────────────────────────────────────
    {
      const s = step("so-done");
      mark(s.id);
      await spotlight(null);
      chapters.open(s.id, s.title, CHAPTER_SOURCE);
      await caption(s.title, s.body, s.dwellMs ?? 4000);
      await shot(page, s.id);
    }
  } catch (e) {
    onThrow(e);
    fs.appendFileSync(
      path.join(ARTIFACT_DIR, "ERROR.txt"),
      `\n${e instanceof Error ? e.stack ?? e.message : String(e)}\n`,
    );
    throw e;
  } finally {
    await context.close();
    const mp4 = await saveVideoAsMp4(video, ARTIFACT_DIR, "spatial-oracle-demo");
    writeChapters(mp4, chapters.list());
    await browser.close();
    close();
  }

  const pngs = fs.readdirSync(ARTIFACT_DIR).filter((f) => f.endsWith(".png"));
  console.log(`[spatial-oracle-video] screenshots (${pngs.length}) in ${ARTIFACT_DIR}`);
});
