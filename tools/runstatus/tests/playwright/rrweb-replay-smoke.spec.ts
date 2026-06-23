/**
 * rrweb-replay-smoke.spec.ts — fast end-to-end smoke of the rrweb capture+replay
 * harness (tests/playwright/_helpers/rrweb-replay.ts).
 *
 * Proves the harness round-trips on something trivial + fast (the heavy
 * tour captures are exercised by the *-rrweb-capture specs):
 *   1. load a real rich SPA surface deterministically with NO server — the
 *      bugfix snapshot artifact (dist/index.html with the snapshot inlined,
 *      served file://), which renders the run-view: topbar, state diagram, and
 *      trace timeline. (The brief suggested static-serving dist + home '#/';
 *      the snapshot artifact is the same built dist bundle but lands on a
 *      data-rich surface, so a non-blank reconstruction is unambiguous.)
 *   2. installCapture, interact for a few seconds (a couple of clicks: switch
 *      the diagram tab, expand a trace row).
 *   3. dumpCapture → writeEvents → .artifacts/rrweb-eval/_smoke/smoke.rrweb.json
 *      (+ the <events>.capture.json viewport sidecar).
 *   4. renderReplayToMp4 → .artifacts/rrweb-eval/_smoke/smoke-replay.mp4 (+ 1fps
 *      frames/), at the SAME viewport + deviceScaleFactor the capture used.
 *   5. assert the MP4 exists and is >0 bytes, and that frames were extracted.
 *
 * The vision check that the frames show REAL reconstructed UI (not blank) is
 * done out-of-band by the orchestrator reading the extracted PNGs.
 *
 * Run:
 *   pnpm exec playwright test rrweb-replay-smoke --project=chromium
 */
import { test, expect, chromium, type Browser, type BrowserContext, type Page } from "@playwright/test";
import path from "path";
import fs from "fs";
import { fileURLToPath } from "url";
import { buildArtifact } from "./_helpers/artifact.js";
import {
  installCapture,
  dumpCapture,
  writeEvents,
  renderReplayToMp4,
  RRWEB_BUNDLE,
} from "./_helpers/rrweb-replay.js";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const projectRoot = path.resolve(__dirname, "../..");
const repoRoot = path.resolve(projectRoot, "../..");

const SNAPSHOT = path.join(projectRoot, "fixtures", "bugfix.snapshot.json");
const OUT_DIR = path.join(repoRoot, ".artifacts", "rrweb-eval", "_smoke");
const EVENTS_JSON = path.join(OUT_DIR, "smoke.rrweb.json");

// Capture viewport — the replay MUST render at exactly this size + DSF.
const VIEWPORT = { width: 1280, height: 800 } as const;
const DSF = 1;

test("rrweb-replay harness smoke: capture a real surface and render the replay", async () => {
  test.setTimeout(120000);

  // Bundle sanity: the local UMD bundle exists and exports record + Replayer.
  expect(fs.existsSync(RRWEB_BUNDLE), `rrweb bundle missing: ${RRWEB_BUNDLE}`).toBe(true);

  fs.mkdirSync(OUT_DIR, { recursive: true });

  // ── 1. CAPTURE: drive a real rich surface, no server ──────────────────────
  const browser: Browser = await chromium.launch({ headless: true });
  const context: BrowserContext = await browser.newContext({
    viewport: VIEWPORT,
    deviceScaleFactor: DSF,
  });
  const page: Page = await context.newPage();

  try {
    const url = buildArtifact(SNAPSHOT);
    await page.goto(url);
    await page.waitForSelector(".run-view__topbar", { timeout: 15000 });

    // Start rrweb recording AFTER the surface has rendered so the first full
    // snapshot already carries the run-view DOM.
    await installCapture(page);
    await page.waitForTimeout(800);

    // A couple of clicks over a few seconds — enough mutation to prove the
    // replay reconstructs interaction, not just a static first snapshot.
    // Diagram tab modes are metro/ego/path/full (StateDiagram.vue).
    const metroTab = page.getByTestId("diagram-tab-metro");
    if (await metroTab.count()) {
      await metroTab.first().click().catch(() => undefined);
      await page.waitForTimeout(1200);
    }
    const fullTab = page.getByTestId("diagram-tab-full");
    if (await fullTab.count()) {
      await fullTab.first().click().catch(() => undefined);
      await page.waitForTimeout(1200);
    }
    // Expand a trace row to mutate the trace pane.
    const row = page.locator(".trace-timeline__row").first();
    if (await row.count()) {
      await row.click().catch(() => undefined);
      await page.waitForTimeout(1200);
    }
    await page.waitForTimeout(800);

    const { events, viewport } = await dumpCapture(page);
    expect(events.length, "rrweb should have emitted events").toBeGreaterThanOrEqual(2);
    // Persist the capture viewport sidecar so the render below exercises the
    // viewport-match invariant on its happy path (capture==render).
    writeEvents(events, EVENTS_JSON, viewport);
    console.log(`[rrweb-replay-smoke] captured ${events.length} events → ${EVENTS_JSON}`);
  } finally {
    await context.close();
    await browser.close();
  }

  // ── 2. RENDER: replay the captured stream through a Replayer, screen-record ─
  const result = await renderReplayToMp4({
    eventsJsonPath: EVENTS_JSON,
    viewport: { ...VIEWPORT },
    deviceScaleFactor: DSF,
    outDir: OUT_DIR,
    name: "smoke-replay",
  });

  expect(result.mp4Path, "renderReplayToMp4 should return an mp4 path").toBeTruthy();
  expect(fs.existsSync(result.mp4Path!), `mp4 missing: ${result.mp4Path}`).toBe(true);
  expect(fs.statSync(result.mp4Path!).size, "mp4 should be >0 bytes").toBeGreaterThan(0);

  const frames = fs.existsSync(result.framesDir)
    ? fs.readdirSync(result.framesDir).filter((f) => f.endsWith(".png"))
    : [];
  expect(frames.length, "should have extracted >=1 frame").toBeGreaterThanOrEqual(1);

  console.log(
    `[rrweb-replay-smoke] mp4=${result.mp4Path} totalTime=${result.totalTimeMs}ms ` +
      `events=${result.eventCount} frames=${frames.length}`,
  );
});
