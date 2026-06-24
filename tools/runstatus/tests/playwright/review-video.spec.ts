/**
 * Mockup Video Studio · /review feedback-mode feature-spotlight video.
 *
 * Records the /review video-feedback surface against a REAL rendered walkthrough
 * MP4 + chapter sidecar — the live end-to-end proof of the mockup-video frame
 * seam (render → chapters → artifact handle → /review → ffmpeg still on flag),
 * NOT a fixture stub for the artifact itself.
 *
 * Like the golden agent-actions / diagram-showcase specs, this video is
 * TOUR-DRIVEN: the REVIEW_TOUR_STEPS in src/tour/generated/review.ts narrate the
 * whole /review walk via window.__startTourWithSteps. The render→chapters→
 * artifact→/review setup runs OFF-CAMERA via RPC (behind a curtain); once the
 * page is ON /review the spec injects the tour, asserts the overlay is visible,
 * and walks the steps with an anti-drift title assertion (tour-title === step
 * title) so the manifest and video cannot silently drift. Detail-pane steps have
 * a pre-step hook that selects + flags a moment so their spotlight testid is on
 * screen. The tour lives in in-memory Pinia state, so the spec NEVER reloads
 * after injecting it (a reload would tear the overlay down).
 *
 * Determinism / no-LLM posture (.agents/skills/kitsoki-ui-demo/SKILL.md):
 *   - `kitsoki web --flow stories/mockup-video/flows/demo_review.yaml`: the
 *     intake/brief-gate/authoring agent calls are stubbed, and
 *     host.slidey.render is stubbed to return the REAL pre-rendered files under
 *     .artifacts/review-video/render. host.artifacts_dir is NOT stubbed, so the
 *     REAL builtin runs and journals the artifact handle the resolver serves.
 *   - The video_handle is content-addressed (dynamic): the spec reads it from
 *     the review room's typed_view (the media element) rather than hardcoding.
 *
 * PRECONDITION: the real video + chapters must exist on disk before this runs:
 *   node /home/cloud-user/code/slidey/src/index.js docs/decks/arch-and-usage.json \
 *     .artifacts/review-video/render/walkthrough.mp4 --fps 30 --scenes 0-5
 *   SCENES=0-5 go run .artifacts/review-video/genchapters \
 *     docs/decks/arch-and-usage.json .artifacts/review-video/render/walkthrough.mp4
 *
 * Validate fast:  WEB_CHAT_PACE=0 pnpm exec playwright test review-video --project=chromium
 * Record:         pnpm exec playwright test review-video --project=chromium
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
  SETTLE_MS,
  ChapterRecorder,
  writeChapters,
  demoAddr,
  type WebServer,
} from "./_helpers/server.js";
import { cameraContext } from "./_helpers/camera.js";
import { installCurtain, liftCurtain, captureDiagnostics } from "./_helpers/demo.js";
import { REVIEW_TOUR_STEPS, type TourStep } from "../../src/tour/generated/review.js";

// 7754 — distinct from diagram-showcase (7753) / agent-actions (7748) so
// parallel spec files never race on the same port.
const ADDR = demoAddr(7754);
const STORY_DIR = path.join(repoRoot, "stories", "mockup-video");
const FLOW = path.join(STORY_DIR, "flows", "demo_review.yaml");
const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "review-video");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "video");
const REAL_VIDEO = path.join(ARTIFACT_DIR, "render", "walkthrough.mp4");
// Feature-catalog source of truth for this spec's tour steps — each step
// becomes a chapter in the SPEC's own recorded MP4 sidecar (distinct from the
// REAL render's walkthrough.mp4.chapters.json, which the demo plays against).
const CHAPTER_SOURCE = "features/review.yaml";

let server: WebServer;

test.beforeAll(async () => {
  // The real rendered walkthrough must already exist (see header).
  if (!fs.existsSync(REAL_VIDEO) || !fs.existsSync(REAL_VIDEO + ".chapters.json")) {
    throw new Error(
      `missing real render at ${REAL_VIDEO}(.chapters.json) — render the deck + generate chapters first (see spec header).`,
    );
  }
  prepareVideoDir(VIDEO_DIR);
  server = await startWebServer({ addr: ADDR, flow: FLOW, storiesDir: STORY_DIR });
});
test.afterAll(() => server?.stop());

test("mockup-video /review feedback-mode feature-spotlight (no-LLM, REAL render, tour-driven)", async () => {
  test.setTimeout(300000);
  const browser: Browser = await chromium.launch({ headless: true });
  const context: BrowserContext = await browser.newContext(
    cameraContext({ recordVideoDir: VIDEO_DIR }),
  );
  const page: Page = await context.newPage();
  const video = page.video(); // capture BEFORE context.close()
  const shot = makeShot(ARTIFACT_DIR);
  const { mark, onThrow } = captureDiagnostics(page, ARTIFACT_DIR);

  // Accumulate per-step time windows for the chapter sidecar. The clock starts
  // now so windows line up with the recorded MP4 timeline.
  const chapters = new ChapterRecorder();

  let sid = "";
  const submit = (intent: string, slots: Record<string, unknown> = {}) =>
    server.rpc<{
      state: string;
      typed_view?: { Elements?: Array<{ Kind: string; Handle?: string; MediaHandle?: string; Source?: string }> };
    }>("runstatus.session.submit", { session_id: sid, intent, slots });

  await installCurtain(page, "Mockup Video Studio — /review feedback");

  try {
    // ── Off-camera setup (behind the curtain): reach the review room ─────────
    mark("home");
    await page.goto(`${server.base}/#/`);
    await page
      .waitForURL(/#\/s\/[0-9a-f-]{36}\/chat$/, { timeout: 3000 })
      .catch(async () => {
        await expect(page.getByTestId("home-view")).toBeVisible({ timeout: 15000 });
        const card = page
          .locator("[data-testid='story-card']")
          .filter({ hasText: /mockup.?video/i })
          .first();
        await card.getByTestId("new-session-btn").click();
        await page.waitForURL(/#\/s\/[0-9a-f-]{36}\/chat$/, { timeout: 15000 });
      });
    sid = page.url().match(/\/s\/([0-9a-f-]{36})\/chat$/)?.[1] ?? "";
    if (!sid) throw new Error(`could not resolve session id from ${page.url()}`);
    mark(`session ${sid}`);

    // intake → ready (distil stub) → brief-gate auto-ok → authoring.
    await submit("ready");
    // authoring → accept → rendering (slidey stub + REAL artifacts_dir emit) →
    // auto accept → review. The review view carries media(video_handle).
    const reviewTurn = await submit("accept");
    if (reviewTurn.state !== "review") {
      throw new Error(`expected to land on 'review', got '${reviewTurn.state}' (server log:\n${server.log()})`);
    }

    // Pull the REAL, content-addressed video handle the artifacts_dir builtin
    // journalled (this is what /review resolves through the ArtifactResolver).
    const els = reviewTurn.typed_view?.Elements ?? [];
    const media = els.find((e) => e.Kind === "media" && (e.Handle || e.MediaHandle));
    const handle = media?.Handle ?? media?.MediaHandle ?? "";
    if (!handle || handle.includes("{{")) {
      throw new Error(`no resolved video handle in review view (typed_view=${JSON.stringify(reviewTurn.typed_view)})`);
    }
    mark(`video_handle ${handle}`);

    // Sanity: the RPCs the panel uses resolve this handle for real.
    const chRes = await server.rpc<{ chapters: unknown[] }>("runstatus.video.chapters", {
      session_id: sid,
      video: handle,
    });
    mark(`chapters resolved: ${chRes.chapters?.length ?? 0}`);
    if (!chRes.chapters || chRes.chapters.length === 0) {
      throw new Error("runstatus.video.chapters returned no chapters for the real handle");
    }

    // Reach the review room (chat view), then click the REAL "Open in review"
    // button to land on /review — all off-camera, behind the curtain. The page
    // sits on /chat from the new-session click and the RPC advancement happened
    // out-of-band, so bounce through home to remount + hydrate the review room.
    await page.goto(`${server.base}/#/`);
    await expect(page.getByTestId("home-view")).toBeVisible({ timeout: 15000 });
    await page.goto(`${server.base}/#/s/${sid}/chat`);
    const reviewLink = page.getByTestId("media-review-link").first();
    await expect(reviewLink).toBeVisible({ timeout: 15000 });
    const linkHref = (await reviewLink.getAttribute("href")) ?? "";
    if (!linkHref.includes(`video=${encodeURIComponent(handle)}`)) {
      throw new Error(`Open-in-review button href does not carry the resolved handle: ${linkHref}`);
    }
    await reviewLink.click();
    await expect(page.getByTestId("review-page")).toBeVisible({ timeout: 15000 });
    await expect(page.getByTestId("rp-player")).toBeVisible({ timeout: 15000 });
    await expect(page.getByTestId("chapter-timeline")).toBeVisible({ timeout: 15000 });
    mark("on /review");

    // ── Inject the tour ON /review and lift the curtain ──────────────────────
    // The /review route maps to "any" (TourOverlay.currentRouteKind), and the
    // overlay is mounted globally in App.vue, so __startTourWithSteps drives the
    // live overlay against the on-screen /review testids. Do NOT reload after
    // this — the overlay lives in in-memory Pinia state.
    await page.evaluate((stepsJson: string) => {
      (window as unknown as { __startTourWithSteps?: (s: string) => void })
        .__startTourWithSteps?.(stepsJson);
    }, JSON.stringify(REVIEW_TOUR_STEPS));
    await expect(page.getByTestId("tour-overlay")).toBeVisible({ timeout: 8000 });
    await liftCurtain(page);

    // Has a flag been captured + selected yet? The detail-pane steps need it.
    let flagged = false;
    const ensureFlagSelected = async (): Promise<void> => {
      if (flagged) return;
      // Pick a moment on the timeline (a real seek+select) so 'Flag this' enables.
      const track = page.getByTestId("ct-track");
      const box = await track.boundingBox();
      if (box) {
        const x = box.x + box.width * 0.45;
        const y = box.y + box.height / 2;
        await page.mouse.move(x, y);
        await page.mouse.down();
        await page.mouse.up();
        await dwell(page, 600);
      }
      // The flag click triggers the REAL runstatus.video.frame ffmpeg grab.
      await page.getByTestId("ct-flag-btn").evaluate((el) => (el as HTMLElement).click());
      await expect(page.getByTestId("fd-still").locator("img")).toBeVisible({ timeout: 20000 });
      await dwell(page, 800);
      flagged = true;
    };

    // ── Walk the REVIEW_TOUR_STEPS ───────────────────────────────────────────
    for (const step of REVIEW_TOUR_STEPS) {
      mark(`step ${step.id}`);

      // ── Pre-step setup ─────────────────────────────────────────────────────
      // The detail-pane steps only exist once a flag is captured + selected. Do
      // the real seek → flag → ffmpeg-still capture before the first such step.
      if (step.id === "review-still") {
        await ensureFlagSelected();
      }
      // Type the refine instruction before the "Send to refine" step so the send
      // is a real dispatch of a non-empty note.
      if (step.id === "review-send") {
        const instr = page.getByTestId("fd-instruction");
        await instr.evaluate((el) => (el as HTMLElement).click());
        await instr.fill(
          "This 'Story anatomy' panel is too dense — split it into two beats and slow the narration.",
        );
        await dwell(page, 700);
      }

      // Honor DOM-presence preconditions.
      if (step.waitForTarget) {
        await expect(page.getByTestId(step.waitForTarget).first()).toBeVisible({ timeout: 15000 });
      }

      // Anti-drift assertion: the popover must show THIS step's title.
      const titleEl = page.getByTestId("tour-title");
      const actualTitle = await titleEl.textContent({ timeout: 8000 }).catch(() => "");
      if (actualTitle !== step.title) {
        const remaining = REVIEW_TOUR_STEPS.slice(REVIEW_TOUR_STEPS.indexOf(step) + 1);
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

      // Every step here is an "explain" step (advance on Next). The
      // "review-send" step still fires the REAL dispatch first.
      if (step.id === "review-send") {
        await page.getByTestId("fd-send-refine").evaluate((el) => (el as HTMLElement).click());
        await expect(page.getByTestId("fd-sent-badge")).toBeVisible({ timeout: 8000 });
        await dwell(page, SETTLE_MS);
      }
      await page.getByTestId("tour-next").click();
      await dwell(page, 700);
    }

    // The final step's "Next" closes the tour.
    await expect(page.getByTestId("tour-overlay")).toHaveCount(0, { timeout: 5000 });
    await shot(page, "review-finale");
  } catch (err) {
    onThrow(err);
    throw err;
  } finally {
    await context.close();
    const mp4 = await saveVideoAsMp4(video, ARTIFACT_DIR, "review-video-demo");
    // Emit the producer-agnostic chapter sidecar beside the MP4: each tour
    // step → one chapter with source_ref kind=tour.
    writeChapters(mp4, chapters.list());
    await browser.close();
  }

  const pngs = fs.readdirSync(ARTIFACT_DIR).filter((f) => f.endsWith(".png"));
  console.log(`[review-video] screenshots (${pngs.length}) in ${ARTIFACT_DIR}`);
});
