/**
 * slidey-edit-rrweb-capture.spec.ts — rrweb capture spec (slidey-edit annotate→refine).
 *
 * The CAPTURE half of the rrweb capture→replay-render demo-video method. It
 * produces the rrweb DOM-mutation event stream of the slidey-edit
 * annotate→refine tour (the "feature-refine / spatial-oracle" phase) for
 * embedding as a NATIVE rrweb scene in a slidey hybrid deck (slidey inlines the
 * JSON as a data URI — the deck does NOT render this to MP4).
 *
 * This is a FORK of slidey-edit-video.spec.ts (the live screen-record drive),
 * with the rrweb hooks grafted on from slidey-bugfix-rrweb-capture.spec.ts:
 *   - installCapture(page) BEFORE the first navigation
 *   - dumpCapture(page) + writeEvents(events, out, viewport) at the end
 *
 * It carries over slidey-edit-video's deterministic no-LLM posture VERBATIM:
 *   - `kitsoki web --flow demo_web.yaml` (flow stubs every host.* call; no host
 *     cassette needed for the idle→drafting→reviewing path)
 *   - the network-edge ARTIFACT BRIDGE that serves the REAL baked deck files
 *     (poster + mp4 + semantic sidecar) from stories/slidey-edit/baked/ so the
 *     reviewing room's media poster + SemanticOverlay annotation markers ACTUALLY
 *     render under the rrweb capture (deck poster + overlay are HTML/CSS + <img>,
 *     safe for rrweb recordCanvas:false).
 *
 * Capture viewport is 1600x900 DSF1 (NOT slidey-edit-video's DSF2 camera
 * profile): the later rrweb replay-render forces transform:none on the player
 * wrapper, which is only clip-safe at DSF1 (see rrweb-replay.ts). UNIQUE port
 * 7755 so this can run alongside every other spec.
 *
 * Artifacts (under .artifacts/rrweb-eval/slidey-edit/):
 *   - slidey-edit.rrweb.json          ← the captured rrweb event stream
 *   - slidey-edit.rrweb.capture.json  ← viewport sidecar (width/height/dsf)
 *   - baseline-frames/NN-*.png        ← per-step baseline screenshots
 *
 * Run:
 *   pnpm exec playwright test slidey-edit-rrweb-capture --project=chromium
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
  dwell,
  cinematicGoto,
  SETTLE_MS,
  type WebServer,
} from "./_helpers/server.js";
import { installCapture, dumpCapture, writeEvents } from "./_helpers/rrweb-replay.js";
import { SLIDEY_EDIT_TOUR_STEPS } from "../../src/tour/slidey-edit-manifest.js";

const CHAPTER_SOURCE = "tools/runstatus/src/tour/slidey-edit-manifest.ts";

// Distinct port so this capture can run alongside other specs without racing.
const ADDR = "127.0.0.1:7755";
const STORY_DIR = path.join(repoRoot, "stories", "slidey-edit");
const FLOW = path.join(STORY_DIR, "flows", "demo_web.yaml");

const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "rrweb-eval", "slidey-edit");
const BASELINE_FRAMES_DIR = path.join(ARTIFACT_DIR, "baseline-frames");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "_baseline-video");
const EVENTS_JSON = path.join(ARTIFACT_DIR, "slidey-edit.rrweb.json");
const DIAG_LOG = path.join(ARTIFACT_DIR, "ERROR.txt");

// The REAL baked deck artifacts — served by the network-edge bridge below so the
// deck handle resolves to genuine bytes even though host.artifacts_dir transport
// is stubbed under --flow.
// The deck is now rendered as static HTML (commit 6a40445f) — there is no longer
// a deck.mp4. The reviewing room's media-element renders the POSTER still inline
// (media-slideshow-poster ← /artifact/<h>/poster) and the annotator floats its
// SemanticOverlay over that same poster; the deck HTML body is only fetched if
// the viewer opens the live deck. So the bridge serves the poster + semantic
// sidecar (the load-bearing pair) and the HTML body for the body route.
const BAKED_DIR = path.join(STORY_DIR, "baked");
const DECK_HTML = path.join(BAKED_DIR, "deck.html");
const DECK_POSTER = path.join(BAKED_DIR, "deck.poster.png");
const DECK_SEMANTIC = path.join(BAKED_DIR, "deck.semantic.json");

// Capture viewport — the later replay-render MUST use this same size + DSF.
const VIEWPORT = { width: 1600, height: 900 } as const;

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
  fs.mkdirSync(BASELINE_FRAMES_DIR, { recursive: true });
  fs.writeFileSync(DIAG_LOG, "");
  // The baked files MUST exist — the whole demo is the proof they render.
  for (const p of [DECK_HTML, DECK_POSTER, DECK_SEMANTIC]) {
    if (!fs.existsSync(p)) throw new Error(`missing baked artifact: ${p}`);
  }
  server = await startWebServer({ addr: ADDR, flow: FLOW, storiesDir: STORY_DIR });
});

test.afterAll(() => server?.stop());

// ── Pace + visible-typing (ported from slidey-edit-video.spec.ts) ───────────
const PACE = Number(process.env.WEB_CHAT_PACE ?? "1");
const TYPE_DELAY_MS = 42 * (Number.isFinite(PACE) ? PACE : 1);

async function expectState(page: Page, state: string): Promise<void> {
  await expect(page.getByTestId("current-state")).toHaveText(state, { timeout: 15000 });
}

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
  await dwell(page, SETTLE_MS);
  return form;
}

async function submitComposed(page: Page, form: Locator, expectStateName: string): Promise<void> {
  await form.evaluate((el) => (el as HTMLFormElement).requestSubmit());
  await expectState(page, expectStateName);
  await dwell(page, SETTLE_MS);
}

async function openAnnotator(page: Page): Promise<void> {
  diag("openAnnotator: click media-annotate");
  const btn = page.getByTestId("media-annotate").first();
  await expect(btn).toBeVisible({ timeout: 15000 });
  await btn.evaluate((el) => (el as HTMLElement).click());
  await expect(page.getByTestId("aa-slidey").first()).toBeVisible({ timeout: 15000 });
  await expect(page.getByTestId("aa-slidey-poster").first()).toBeVisible({ timeout: 15000 });
  await expect(page.getByTestId("semantic-overlay").first()).toBeVisible({ timeout: 15000 });
  await expect(page.getByTestId("so-marker-1/card_0").first()).toBeVisible({ timeout: 15000 });
  await dwell(page, SETTLE_MS);
}

test("slidey-edit annotate → refine rrweb capture (baseline + event stream)", async () => {
  test.setTimeout(360000);
  const browser: Browser = await chromium.launch({ headless: true });
  // DSF1 (NOT the DSF2 camera profile) — clip-safe under the rrweb replay-render.
  const context: BrowserContext = await browser.newContext({
    viewport: { ...VIEWPORT },
    deviceScaleFactor: 1,
    recordVideo: { dir: VIDEO_DIR, size: { ...VIEWPORT } },
  });

  // ── The artifact bridge (network-edge, transport-only) — ported verbatim ──
  await context.route("**/artifact/*/poster", async (route) => {
    await route.fulfill({ contentType: "image/png", body: fs.readFileSync(DECK_POSTER) });
  });
  await context.route("**/artifact/*", async (route) => {
    await route.fulfill({ contentType: "text/html", body: fs.readFileSync(DECK_HTML) });
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
  const shot = makeShot(BASELINE_FRAMES_DIR);
  const chapters = new ChapterRecorder();

  let startForm: Locator | null = null;
  let refineForm: Locator | null = null;

  try {
    // ── 1. Home story library, then start the tour ON it ──────────────────────
    diag("navigating home");
    await cinematicGoto(page, `${server.base}/#/`, { waitForTestId: "home-view" });

    // rrweb: start recording AFTER home paints, BEFORE the first navigation.
    await installCapture(page);
    diag("rrweb capture installed");

    await page.evaluate((stepsJson: string) => {
      (window as unknown as { __startTourWithSteps?: (s: string) => void })
        .__startTourWithSteps?.(stepsJson);
    }, JSON.stringify(SLIDEY_EDIT_TOUR_STEPS));
    await expect(page.getByTestId("tour-overlay")).toBeVisible({ timeout: 8000 });

    // ── 2. Walk SLIDEY_EDIT_TOUR_STEPS (port of slidey-edit-video's walk) ─────
    for (const step of SLIDEY_EDIT_TOUR_STEPS) {
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

      if (step.id === "se-author") {
        await expectState(page, "idle");
        startForm = await composeVisibly(page, "start", "author a tight 3-scene explainer deck");
      }
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
      if (step.id === "se-overlay") {
        await openAnnotator(page);
      }
      if (step.id === "se-loop-closed") {
        await expectState(page, "reviewing");
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
        const target = page.getByTestId(step.target!).first();
        await target.scrollIntoViewIfNeeded().catch(() => undefined);
        await target.click();
        await page.waitForTimeout(300);
        if (step.advanceRoute === "interactive") {
          await page.waitForURL(/#\/s\/[0-9a-f-]{36}\/chat$/, { timeout: 15000 });
          diag(`session ${page.url()}`);
        }
        await dwell(page, 1000);
      } else {
        if (step.id === "se-pick") {
          diag("se-pick: click so-marker-1/card_0");
          await page
            .getByTestId("so-marker-1/card_0")
            .first()
            .evaluate((el) => (el as HTMLElement).click());
          await expect(page.getByTestId("media-annotate-panel").first()).toBeVisible({ timeout: 15000 });
          await dwell(page, SETTLE_MS);
        }
        if (step.id === "se-author" && startForm) {
          // demo_web.yaml's `start` turn flows straight through drafting →
          // rendering → reviewing in ONE step (drafting auto-emits accept once the
          // deck spec is bound — see the flow's idle→reviewing turn), so there is
          // no pausable drafting state to drive an explicit accept through. Submit
          // the typed request and wait for the deck to land in reviewing.
          await submitComposed(page, startForm, "reviewing");
          await expect(page.getByTestId("media-element").first()).toBeVisible({ timeout: 15000 });
          startForm = null;
        }
        if (step.id === "se-drafting") {
          // The deck already rendered (start flowed straight to reviewing). This
          // beat narrates the authored/reviewing deck; just confirm the rendered
          // media is on-screen for the spotlight.
          await expectState(page, "reviewing");
          await expect(page.getByTestId("media-element").first()).toBeVisible({ timeout: 15000 });
          await dwell(page, SETTLE_MS);
        }
        if (step.id === "se-refine" && refineForm) {
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

    await expect(page.getByTestId("tour-overlay")).toHaveCount(0, { timeout: 5000 });

    // ── 3. rrweb: dump the FULL accumulated stream + capture viewport ─────────
    const { events, viewport } = await dumpCapture(page);
    diag(`rrweb captured ${events.length} events @ ${viewport.width}x${viewport.height} dsf=${viewport.deviceScaleFactor}`);
    writeEvents(events, EVENTS_JSON, viewport);
    expect(events.length, "rrweb should have emitted a healthy event stream").toBeGreaterThanOrEqual(50);
  } catch (e) {
    diag(`FAILED: ${e instanceof Error ? e.stack ?? e.message : String(e)}`);
    diag(`--- server log ---\n${server?.log?.() ?? ""}`);
    throw e;
  } finally {
    await context.close();
    const mp4 = await saveVideoAsMp4(video, ARTIFACT_DIR, "slidey-edit-baseline");
    writeChapters(mp4, chapters.list());
    await browser.close();
  }

  const pngs = fs.readdirSync(BASELINE_FRAMES_DIR).filter((f) => f.endsWith(".png"));
  console.log(`[slidey-edit-rrweb-capture] baseline frames (${pngs.length}) in ${BASELINE_FRAMES_DIR}`);
  console.log(`[slidey-edit-rrweb-capture] events → ${EVENTS_JSON}`);
});
