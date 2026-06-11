/**
 * Mockup Video Studio · /review feedback-mode feature-spotlight video.
 *
 * Records the /review video-feedback surface against a REAL rendered walkthrough
 * MP4 + chapter sidecar — the live end-to-end proof of the mockup-video frame
 * seam (render → chapters → artifact handle → /review → ffmpeg still on flag),
 * NOT a fixture stub for the artifact itself.
 *
 * Determinism / no-LLM posture (docs/skills/kitsoki-ui-demo/SKILL.md):
 *   - `kitsoki web --flow stories/mockup-video/flows/demo_review.yaml`: the
 *     intake/brief-gate/authoring oracle calls are stubbed, and
 *     host.slidey.render is stubbed to return the REAL pre-rendered files under
 *     .artifacts/review-video/render. host.artifacts_dir is NOT stubbed, so the
 *     REAL builtin runs and journals the artifact handle the resolver serves.
 *   - Setup (home → session → intake → review) is driven OFF-CAMERA via RPC
 *     behind a full-screen CURTAIN.
 *   - The video_handle is content-addressed (dynamic): the spec reads it from
 *     the review room's typed_view (the media element) rather than hardcoding.
 *   - On-camera, the demo navigates to /review and advances via REAL UI clicks
 *     (timeline marker → flag → instruction → send). The flag click triggers a
 *     real runstatus.video.frame ffmpeg grab.
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
  type WebServer,
} from "./_helpers/server.js";
import {
  DEMO_VIEWPORT,
  dwell,
  installCurtain,
  liftCurtain,
  makeCaption,
  captureDiagnostics,
} from "./_helpers/demo.js";
import { REVIEW_DEMO_STEPS, type ReviewStep } from "../../src/tour/review-manifest.js";

// 7754 — distinct from diagram-showcase (7753) / agent-actions (7748) so
// parallel spec files never race on the same port.
const ADDR = "127.0.0.1:7754";
const STORY_DIR = path.join(repoRoot, "stories", "mockup-video");
const FLOW = path.join(STORY_DIR, "flows", "demo_review.yaml");
const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "review-video");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "video");
const REAL_VIDEO = path.join(ARTIFACT_DIR, "render", "walkthrough.mp4");

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

/** Spotlight a target testid with an injected ring (presentation-only; never a
 *  trace/render hack). Cleared on the next call. */
async function spotlight(page: Page, testid?: string): Promise<void> {
  await page.evaluate((id) => {
    document.querySelectorAll("[data-demo-spot]").forEach((el) => {
      (el as HTMLElement).style.outline = "";
      (el as HTMLElement).style.outlineOffset = "";
      (el as HTMLElement).style.borderRadius = "";
      el.removeAttribute("data-demo-spot");
    });
    if (!id) return;
    const t = document.querySelector(`[data-testid="${id}"]`) as HTMLElement | null;
    if (!t) return;
    t.scrollIntoView({ block: "center", behavior: "instant" as ScrollBehavior });
    t.style.outline = "3px solid #fbbf24";
    t.style.outlineOffset = "3px";
    t.style.borderRadius = "6px";
    t.setAttribute("data-demo-spot", "1");
  }, testid);
}

test("mockup-video /review feedback-mode feature-spotlight (no-LLM, REAL render)", async () => {
  test.setTimeout(300000);
  const browser: Browser = await chromium.launch({ headless: true });
  const context: BrowserContext = await browser.newContext({
    viewport: { ...DEMO_VIEWPORT },
    recordVideo: { dir: VIDEO_DIR, size: { ...DEMO_VIEWPORT } },
  });
  const page: Page = await context.newPage();
  const video = page.video(); // capture BEFORE context.close()
  const shot = makeShot(ARTIFACT_DIR);
  const { mark, onThrow } = captureDiagnostics(page, ARTIFACT_DIR);

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
    await expect(page.getByTestId("home-view")).toBeVisible({ timeout: 15000 });
    const card = page
      .locator("[data-testid='story-card']")
      .filter({ hasText: /mockup.?video/i })
      .first();
    await card.getByTestId("new-session-btn").click();
    await page.waitForURL(/#\/s\/[0-9a-f-]{36}\/chat$/, { timeout: 15000 });
    sid = page.url().match(/\/s\/([0-9a-f-]{36})\/chat$/)?.[1] ?? "";
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
    // The review room's media() element carries the handle, interpolated at
    // render time (internal/render/elements/element.go media case), so read it
    // straight off that element — the same value the inline player and the
    // "Open in review" button resolve.
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

    // ── Show the REAL operator entry point: the review ROOM + its button ─────
    // An operator does not type a /review URL — they land in the review room
    // (the live chat view), where the rendered walkthrough plays inline and the
    // media element offers an "Open in review" button. The page already sits on
    // this session's /chat route (from the new-session click) showing an earlier
    // room, and the RPC advancement above happened out-of-band; bounce through
    // home so the chat view remounts and freshly hydrates to the review room.
    // All off-camera, behind the curtain.
    await page.goto(`${server.base}/#/`);
    await expect(page.getByTestId("home-view")).toBeVisible({ timeout: 15000 });
    await page.goto(`${server.base}/#/s/${sid}/chat`);
    const reviewLink = page.getByTestId("media-review-link").first();
    await expect(reviewLink).toBeVisible({ timeout: 15000 });
    // The button must carry THIS run's resolved handle — prove the entry point
    // is wired to the real artifact, not a literal template.
    const linkHref = (await reviewLink.getAttribute("href")) ?? "";
    if (!linkHref.includes(`video=${encodeURIComponent(handle)}`)) {
      throw new Error(`Open-in-review button href does not carry the resolved handle: ${linkHref}`);
    }

    const beat = await makeCaption(page, 6000);
    await dwell(page, 1400);
    await liftCurtain(page);

    // Step lookup so the walk reads off the manifest (single source of truth).
    const step = (id: string): ReviewStep => {
      const s = REVIEW_DEMO_STEPS.find((x) => x.id === id);
      if (!s) throw new Error(`unknown review step ${id}`);
      return s;
    };
    const narrate = async (id: string): Promise<ReviewStep> => {
      const s = step(id);
      mark(s.id);
      await spotlight(page, s.target);
      await beat(s.title, s.body, s.dwellMs ?? 6000);
      await shot(page, s.id);
      return s;
    };

    // 0. The room → click the REAL "Open in review" button to reach /review.
    await narrate("review-entry");
    await reviewLink.click();
    await expect(page.getByTestId("review-page")).toBeVisible({ timeout: 15000 });
    await expect(page.getByTestId("rp-player")).toBeVisible({ timeout: 15000 });
    await expect(page.getByTestId("chapter-timeline")).toBeVisible({ timeout: 15000 });

    // 1. Open + 2. player + 3. timeline.
    await narrate("review-open");
    await narrate("review-player");
    await narrate("review-timeline");

    // Pick a moment on the timeline (a real seek+select) then 4. flag it.
    // Click the 3rd chapter marker to seek, then click the track mid-point to
    // set a point selection so 'Flag this' is enabled.
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
    const flagStep = await narrate("review-flag");
    // The flag click triggers the REAL runstatus.video.frame ffmpeg grab.
    await page.getByTestId(flagStep.target!).click();
    // Wait for the captured still (the frame seam end-to-end) to render.
    await expect(page.getByTestId("fd-still").locator("img")).toBeVisible({ timeout: 20000 });
    await dwell(page, 800);

    // 5. still + 6. source_ref + 7. instruction.
    await narrate("review-still");
    await narrate("review-source");

    const instrStep = await narrate("review-instruction");
    const instr = page.getByTestId(instrStep.target!);
    await instr.click();
    await instr.fill("This 'Story anatomy' panel is too dense — split it into two beats and slow the narration.");
    await dwell(page, 900);

    // 8. send to refine (a real runstatus.feedback.add dispatch).
    const sendStep = await narrate("review-send");
    await page.getByTestId(sendStep.target!).click();
    await expect(page.getByTestId("fd-sent-badge")).toBeVisible({ timeout: 8000 });
    await dwell(page, 900);

    // 9. the flag list (dispatched dot) + Send all.
    await narrate("review-flaglist");

    // Hold a closing frame.
    await spotlight(page, undefined);
    await beat(
      "Capture, resolve, dispatch",
      "Every flag carries its still, its source_ref, and its instruction — captured in the browser, dispatched as a structured note, refined deterministically in the story.",
      6500,
    );
    await shot(page, "review-finale");
  } catch (err) {
    onThrow(err);
    throw err;
  } finally {
    await context.close();
    await saveVideoAsMp4(video, ARTIFACT_DIR, "review-video-demo");
    await browser.close();
  }

  const pngs = fs.readdirSync(ARTIFACT_DIR).filter((f) => f.endsWith(".png"));
  console.log(`[review-video] screenshots (${pngs.length}) in ${ARTIFACT_DIR}`);
});
