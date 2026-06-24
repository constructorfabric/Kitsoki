/**
 * slidey-bugfix-rrweb-capture.spec.ts — rrweb capture spec (the slidey bug-fix tour).
 *
 * The CAPTURE half of the rrweb capture→replay-render demo-video method (the
 * RENDER half is rrweb-replay-render.spec.ts). This produces the rrweb
 * DOM-mutation stream of the slidey-bugfix tour for embedding as a NATIVE rrweb
 * scene in a slidey hybrid demo deck (slidey inlines the JSON as a data URI —
 * the deck does NOT render this to MP4).
 *
 * Forked from agent-actions-rrweb-capture.spec.ts. Same shape — spawn
 * `kitsoki web --flow`, installCapture(page) BEFORE the first navigation,
 * window.__startTourWithSteps, walk the manifest at watch-speed, then
 * dumpCapture(page) + writeEvents(...) at the end. The ONLY substantive
 * differences vs the agent-actions fork:
 *
 *   - It drives the SLIDEY_BUGFIX_TOUR_STEPS manifest against the slidey-bugfix
 *     story instance (stories/slidey-bugfix) + its no-LLM flow
 *     (flows/tour.yaml + cassettes/tour.cassette.yaml).
 *   - The per-step `drive:` action arrays (click-intent / wait-state /
 *     reveal-turn / dwell-ms) are executed FAITHFULLY — this is the TypeScript
 *     port of the Go binary-tour drive executor (internal/tour/drive.go +
 *     runner.go), which is what `kitsoki tour --feature slidey-bugfix` runs. The
 *     manifest is self-driving: it advances the deterministic flow itself
 *     (bf__start → bf__accept × N), so NO patch_world is needed — the
 *     slidey-bugfix instance bakes ticket_id/workdir/etc. into its world DEFAULTS
 *     (and bf_autostart_attempted=true parks bf.idle for the explicit start).
 *
 * Artifacts (all under .artifacts/rrweb-eval/slidey-bugfix/):
 *   - slidey-bugfix.rrweb.json          ← the captured rrweb event stream
 *   - slidey-bugfix.rrweb.capture.json  ← viewport sidecar (width/height/dsf)
 *   - slidey-bugfix-baseline.mp4        ← the live screen-recording (the BASELINE)
 *   - baseline-frames/NN-*.png         ← per-step baseline screenshots
 *
 * Run at watch-speed:
 *   pnpm exec playwright test slidey-bugfix-rrweb-capture --project=chromium
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
  cinematicGoto,
  ChapterRecorder,
  writeChapters,
  type WebServer,
} from "./_helpers/server.js";
import { installCapture, dumpCapture, writeEvents } from "./_helpers/rrweb-replay.js";
import { SLIDEY_BUGFIX_TOUR_STEPS, type TourStep } from "../../src/tour/generated/slidey-bugfix.js";

const CHAPTER_SOURCE = "features/slidey-bugfix.yaml";

// Distinct port so this capture can run alongside other specs without racing.
const ADDR = "127.0.0.1:7753";
const STORY_DIR = path.join(repoRoot, "stories", "slidey-bugfix");
const FLOW = path.join(STORY_DIR, "flows", "tour.yaml");
const HOST_CASSETTE = path.join(STORY_DIR, "cassettes", "tour.cassette.yaml");

const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "rrweb-eval", "slidey-bugfix");
const BASELINE_FRAMES_DIR = path.join(ARTIFACT_DIR, "baseline-frames");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "_baseline-video");
const EVENTS_JSON = path.join(ARTIFACT_DIR, "slidey-bugfix.rrweb.json");
const DIAG_LOG = path.join(ARTIFACT_DIR, "diagnostic.log");

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
  server = await startWebServer({ addr: ADDR, flow: FLOW, hostCassette: HOST_CASSETTE, storiesDir: STORY_DIR });
});

test.afterAll(() => server?.stop());

// ── Drive-action executor — the TS port of internal/tour/drive.go ───────────
//
// Every gesture is an in-page evaluate() so it fires through the tour overlay's
// hit-test backdrop regardless of paint order (the same reason the Go executor
// and the video specs DOM-dispatch el.click() rather than a hit-test click).

const SCROLL_CONTROL = `(() => {
  const el = document.querySelector('[data-testid="chat-transcript"]');
  if (!el) return false;
  if (el.__nat) return true;
  el.__nat = true;
  const desc = Object.getOwnPropertyDescriptor(Element.prototype, 'scrollTop');
  const realGet = () => desc.get.call(el);
  const realSet = (v) => desc.set.call(el, v);
  Object.defineProperty(el, 'scrollTop', {
    configurable: true,
    get() { return realGet(); },
    set() { /* ignored — natural scroll driven via __ease */ },
  });
  window.__ease = (to, ms) => new Promise((res) => {
    const from = realGet();
    const max = el.scrollHeight - el.clientHeight;
    const target = Math.max(0, Math.min(to, max));
    if (ms <= 0 || Math.abs(target - from) < 2) { realSet(target); return res(); }
    const t0 = performance.now();
    const tick = (now) => {
      const p = Math.min(1, (now - t0) / ms);
      const eased = p < 0.5 ? 2 * p * p : 1 - Math.pow(-2 * p + 2, 2) / 2;
      realSet(from + (target - from) * eased);
      if (p < 1) requestAnimationFrame(tick); else res();
    };
    requestAnimationFrame(tick);
  });
  window.__lastUserTop = () => {
    const rows = el.querySelectorAll('[data-testid="chat-row-user"]');
    const last = rows[rows.length - 1];
    return last ? Math.max(0, last.offsetTop - 16) : el.scrollHeight;
  };
  window.__scrollMax = () => el.scrollHeight - el.clientHeight;
  return true;
})()`;

/** DriveAction list executor (port of executor.run / executor.one). */
async function runDrive(page: Page, actions: TourStep["drive"]): Promise<void> {
  for (const a of actions ?? []) {
    switch (a.type) {
      case "type-and-send":
        await typeAndSend(page, a.text);
        break;
      case "click-intent":
        await clickIntent(page, a.intent);
        break;
      case "wait-state":
        await waitForState(page, a.state, 20000);
        break;
      case "reveal-turn":
        await revealTurn(page);
        break;
      case "dwell-ms":
        await dwell(page, a.ms);
        break;
      default:
        throw new Error(`unknown drive type ${(a as { type: string }).type}`);
    }
  }
}

async function typeAndSend(page: Page, text: string): Promise<void> {
  const filled = await page.evaluate((t) => {
    const input =
      (document.querySelector('[data-testid="composer-input"]') as HTMLInputElement | null) ??
      (document.querySelector('[data-testid="text-floor-input"]') as HTMLInputElement | null);
    if (!input) return false;
    input.focus();
    input.value = t;
    input.dispatchEvent(new Event("input", { bubbles: true }));
    return true;
  }, text);
  if (!filled) throw new Error("composer-input not found (no composer or text floor)");
  await page.waitForTimeout(200);
  const sent = await page.evaluate(() => {
    const btn =
      (document.querySelector('[data-testid="composer-send"]') as HTMLElement | null) ??
      (document.querySelector('[data-testid="text-floor-send"]') as HTMLElement | null);
    if (!btn) return false;
    btn.click();
    return true;
  });
  if (!sent) throw new Error("composer-send not found");
}

async function clickIntent(page: Page, intent: string): Promise<void> {
  const ok = await page.evaluate((id) => {
    const btn = document.querySelector(`[data-testid="intent-btn-${id}"]`) as HTMLElement | null;
    if (!btn) return false;
    btn.scrollIntoView({ block: "center" });
    btn.click();
    return true;
  }, intent);
  if (!ok) throw new Error(`intent button ${intent} not found`);
}

async function waitForState(page: Page, state: string, timeoutMs: number): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  let cur = "";
  while (Date.now() < deadline) {
    cur = await page.evaluate(() => {
      const el = document.querySelector('[data-testid="current-state"]');
      return el ? (el.textContent || "").trim() : "";
    });
    if (cur === state) return;
    await page.waitForTimeout(300);
  }
  throw new Error(`wait-state ${state} timed out (last "${cur}")`);
}

async function revealTurn(page: Page): Promise<void> {
  await page.evaluate(SCROLL_CONTROL);
  await dwell(page, 1400);
  const top = await page.evaluate(() => (window.__lastUserTop ? window.__lastUserTop() : 0));
  await ease(page, top, paced(1200));
  await dwell(page, 1300);
  const max = await page.evaluate(() => (window.__scrollMax ? window.__scrollMax() : 0));
  let span = max - top;
  if (span < 0) span = 0;
  let downMs = Math.round(span * 3);
  if (downMs < 700) downMs = 700;
  if (downMs > 3000) downMs = 3000;
  await ease(page, max, paced(downMs));
  await dwell(page, 1500);
}

const PACE = process.env.WEB_CHAT_PACE !== undefined ? Number(process.env.WEB_CHAT_PACE) : 1;
function paced(ms: number): number {
  return Math.round(ms * (Number.isFinite(PACE) ? PACE : 1));
}

async function ease(page: Page, to: number, ms: number): Promise<void> {
  await page.evaluate(
    async ([t, d]) => {
      if (window.__ease) await window.__ease(t as number, d as number);
    },
    [to, ms]
  );
}

function routeKindFromUrl(url: string): "interactive" | "any" | "home" {
  if (url.includes("/chat")) return "interactive";
  if (/#\/s\/[0-9a-f-]{36}$/.test(url)) return "any";
  return "home";
}

declare global {
  interface Window {
    __ease?: (to: number, ms: number) => Promise<void>;
    __lastUserTop?: () => number;
    __scrollMax?: () => number;
    __startTourWithSteps?: (s: string) => void;
  }
}

test("slidey bug-fix rrweb capture (baseline + event stream)", async () => {
  test.setTimeout(360000);
  const browser: Browser = await chromium.launch({ headless: true });
  const context: BrowserContext = await browser.newContext({
    viewport: { ...VIEWPORT },
    recordVideo: { dir: VIDEO_DIR, size: { ...VIEWPORT } },
  });
  const page: Page = await context.newPage();
  const video = page.video();
  const shot = makeShot(BASELINE_FRAMES_DIR);

  const chapters = new ChapterRecorder();

  try {
    // ── 1. Home story library, then start the tour ON it ──────────────────────
    diag("navigating home");
    await cinematicGoto(page, `${server.base}/#/`, { waitForTestId: "home-view" });

    // rrweb: start recording AFTER home paints, BEFORE the first navigation.
    await installCapture(page);
    diag("rrweb capture installed");

    await page.evaluate((stepsJson: string) => {
      window.__startTourWithSteps?.(stepsJson);
    }, JSON.stringify(SLIDEY_BUGFIX_TOUR_STEPS));
    await expect(page.getByTestId("tour-overlay")).toBeVisible({ timeout: 8000 });

    // ── 2. Walk the SLIDEY_BUGFIX_TOUR_STEPS (port of walkSteps) ───────────────
    let pngIdx = 0;
    for (const step of SLIDEY_BUGFIX_TOUR_STEPS) {
      const routeKind = routeKindFromUrl(page.url());
      if (step.route !== "any" && step.route !== routeKind) {
        diag(`step ${step.id}: route-skip (on ${routeKind})`);
        continue;
      }
      diag(`step ${step.id}`);

      // Interactive beats span their whole conversation — open the chapter now.
      if (step.route === "interactive") chapters.open(step.id, step.title, CHAPTER_SOURCE);

      // (2) self-driving actions
      await runDrive(page, step.drive);

      // (3) DOM-presence precondition
      if (step.waitForTarget) {
        await expect(page.getByTestId(step.waitForTarget).first()).toBeVisible({ timeout: 15000 });
      }

      // (4) anti-drift title assert
      await expect(page.getByTestId("tour-title")).toHaveText(step.title, { timeout: 12000 });

      // (5) non-interactive steps open chapter + hold here
      if (step.route !== "interactive") {
        chapters.open(step.id, step.title, CHAPTER_SOURCE);
        await dwell(page, step.dwellMs ?? 3000);
      }
      pngIdx++;
      void pngIdx;
      await shot(page, step.id);

      // (6) advance
      if (step.kind === "explain") {
        await page.getByTestId("tour-next").click();
        await dwell(page, 700);
      } else if (step.target) {
        const ok = await page.evaluate((sel) => {
          const t = document.querySelector(sel) as HTMLElement | null;
          if (!t) return false;
          t.scrollIntoView({ block: "center" });
          t.click();
          return true;
        }, `[data-testid="${step.target}"]`);
        if (!ok) throw new Error(`advance target ${step.target} not present`);
        if (step.advance === "route-match") {
          const want = step.advanceRoute;
          const re = want === "interactive" ? /#\/s\/[0-9a-f-]{36}\/chat$/ : /#\/s\/[0-9a-f-]{36}$/;
          await page.waitForURL(re, { timeout: 15000 });
          diag(`  advanced to ${routeKindFromUrl(page.url())}`);
        }
        await dwell(page, 1000);
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
    const mp4 = await saveVideoAsMp4(video, ARTIFACT_DIR, "slidey-bugfix-baseline");
    writeChapters(mp4, chapters.list());
    await browser.close();
  }

  const pngs = fs.readdirSync(BASELINE_FRAMES_DIR).filter((f) => f.endsWith(".png"));
  console.log(`[slidey-bugfix-rrweb-capture] baseline frames (${pngs.length}) in ${BASELINE_FRAMES_DIR}`);
  console.log(`[slidey-bugfix-rrweb-capture] events → ${EVENTS_JSON}`);
});
