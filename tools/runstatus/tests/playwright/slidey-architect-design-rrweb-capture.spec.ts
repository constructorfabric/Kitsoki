/**
 * slidey-architect-design-rrweb-capture.spec.ts — rrweb capture spec (slidey architect/design tour).
 *
 * Phase 1 of the slidey dev-story hybrid: a product manager talks a real slidey
 * feature — a `slidey --notes` speaker-notes export — from a one-line idea into
 * a published PRD, on the slidey-dev instance (imports @kitsoki/dev-story as
 * core). No LLM: the pm_idea.yaml flow stubs every agent call with
 * slidey-themed content.
 *
 * Forked from slidey-bugfix-rrweb-capture.spec.ts. Same shape — spawn
 * `kitsoki web --flow`, installCapture(page) BEFORE the first navigation,
 * window.__startTourWithSteps, walk the manifest at watch-speed, then
 * dumpCapture + writeEvents at the end. Substantive differences:
 *
 *   - Drives SLIDEY_ARCHITECT_DESIGN_TOUR_STEPS against stories/slidey-dev +
 *     flows/pm_idea.yaml. No host cassette — the flow's host_handlers stub
 *     everything.
 *   - extraEnv KITSOKI_REPO points at the EMBEDDED clean basestories library so
 *     `@kitsoki/dev-story` resolves to the clean snapshot, sidestepping any
 *     in-progress working-tree bugfix WIP.
 *
 * Artifacts (all under .artifacts/rrweb-eval/slidey-architect-design/):
 *   - slidey-architect-design.rrweb.json          ← the captured rrweb event stream
 *   - slidey-architect-design.rrweb.capture.json  ← viewport sidecar (width/height/dsf)
 *
 * Run at watch-speed:
 *   pnpm exec playwright test slidey-architect-design-rrweb-capture --project=chromium
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
  showArtifact,
  type WebServer,
} from "./_helpers/server.js";
import { installCapture, dumpCapture, writeEvents } from "./_helpers/rrweb-replay.js";
import { SLIDEY_ARCHITECT_DESIGN_TOUR_STEPS, type TourStep } from "../../src/tour/generated/slidey-architect-design.js";

const CHAPTER_SOURCE = "features/slidey-architect-design.yaml";

// REPLAY harness (not nil --flow): the operator TYPES the design intent + refine
// note, routed to slot-bearing core__discuss by the recording (a nil-harness
// --flow cannot extract a slot from typed prose). The flow is still passed —
// but ONLY for its initial_state (core.prd_published) / initial_world seed (the
// design phase starts mid-graph, after the PRD is published); the live-harness
// SeedFixture path teleports there while the recording routes free text and the
// host cassette backs every host.* call. See .context/slidey-replay-clips.md.
const ADDR = "127.0.0.1:7754";
const STORY_DIR = path.join(repoRoot, "stories", "slidey-dev");
const FLOW = path.join(STORY_DIR, "flows", "architect_design.yaml");
const RECORDING = path.join(STORY_DIR, "assets", "architect_design-recording.yaml");
const HOST_CASSETTE = path.join(STORY_DIR, "assets", "architect_design-host.cassette.yaml");
const EMBED_REPO = repoRoot;

const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "rrweb-eval", "slidey-architect-design");
const BASELINE_FRAMES_DIR = path.join(ARTIFACT_DIR, "baseline-frames");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "_baseline-video");
const EVENTS_JSON = path.join(ARTIFACT_DIR, "slidey-architect-design.rrweb.json");
const DIAG_LOG = path.join(ARTIFACT_DIR, "diagnostic.log");

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
  server = await startWebServer({
    addr: ADDR,
    harness: "replay",
    flow: FLOW, // seed-only (initial_state/world) under the live-harness path
    recording: RECORDING,
    hostCassette: HOST_CASSETTE,
    storiesDir: STORY_DIR,
    extraEnv: { KITSOKI_REPO: EMBED_REPO },
  });
});

test.afterAll(() => server?.stop());

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

// The InputBar's composer send() emits the room's ACTIVE text-intent directly,
// NOT free-text routing — so a typed message only reaches the right room's
// discuss when the composer is bound to that room's intent. After a navigation
// the InputBar lags the state a render tick (still bound to the OUTGOING room's
// surface). We avoid the trap by driving through InteractiveView's own store
// hooks (`window.__kitsokiSendText` → session.turn → the replay recording routes
// it; `window.__kitsokiSubmitIntent` → the reactive submit path the buttons
// use), so the chat + InputBar re-render for the new room and the turn lands on
// the intended intent — never the stale dev-story landing `work`. A brief
// composer-settle still gives the camera a clean frame.
const LANDING_PLACEHOLDER_RE = /describe work/i;
async function waitForComposerSettled(page: Page, timeoutMs = 5000): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  let lastKey = "";
  let stableCount = 0;
  while (Date.now() < deadline) {
    const snap = await page.evaluate(() => {
      const composer = document.querySelector('[data-testid="composer-input"]') as HTMLElement | null;
      const floor = document.querySelector('[data-testid="text-floor-input"]') as HTMLElement | null;
      const pick = composer ?? floor;
      const vis = (el: HTMLElement | null) =>
        !!el && el.getBoundingClientRect().height > 0 && getComputedStyle(el).visibility !== "hidden";
      return {
        hasComposer: vis(composer),
        hasFloor: vis(floor),
        placeholder: pick ? (pick.getAttribute("placeholder") ?? "") : "",
        key: pick ? `${composer ? "c" : "f"}:${pick.getAttribute("placeholder") ?? ""}` : "",
      };
    });
    const single = (snap.hasComposer ? 1 : 0) + (snap.hasFloor ? 1 : 0) === 1;
    const notLanding = !LANDING_PLACEHOLDER_RE.test(snap.placeholder);
    if (single && notLanding && snap.key !== "" && snap.key === lastKey) {
      stableCount++;
      if (stableCount >= 2) return;
    } else {
      stableCount = 0;
    }
    lastKey = snap.key;
    await page.waitForTimeout(150);
  }
  diag(`waitForComposerSettled: timed out (last key="${lastKey}")`);
}

// waitForTurnSettled blocks until no turn is in flight. The InteractiveView
// renders `thinking-bubble` exactly while `pending` is true (its runTurn
// guard), and — critically — runTurn is a NO-OP if a prior turn is still
// pending: __kitsokiSendText / __kitsokiSubmitIntent silently drop the action
// and resolve instantly. So the previous turn (e.g. the refine `discuss` that
// re-runs the design_refiner via host.agent.task) must FULLY land before the
// next intent (`ready`) is issued, or `ready` is dropped and the session never
// leaves design_refine. A `wait-state` on the room we're already in is
// trivially-true and does NOT prove the turn settled — this does. We require
// the bubble to be absent for two consecutive polls so a between-turns flicker
// doesn't read as settled.
async function waitForTurnSettled(page: Page, timeoutMs = 20000): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  let clearStreak = 0;
  while (Date.now() < deadline) {
    const thinking = await page.evaluate(
      () => !!document.querySelector('[data-testid="thinking-bubble"]'),
    );
    if (!thinking) {
      clearStreak++;
      if (clearStreak >= 2) return;
    } else {
      clearStreak = 0;
    }
    await page.waitForTimeout(150);
  }
  diag("waitForTurnSettled: timed out (thinking-bubble never cleared)");
}

async function typeAndSend(page: Page, text: string): Promise<void> {
  await waitForTurnSettled(page);
  await waitForComposerSettled(page);
  const ok = await page.evaluate(async (t) => {
    const fn = (window as unknown as { __kitsokiSendText?: (s: string) => Promise<void> }).__kitsokiSendText;
    if (!fn) return false;
    await fn(t);
    return true;
  }, text);
  if (!ok) throw new Error("__kitsokiSendText hook not present (InteractiveView not mounted?)");
}

async function clickIntent(page: Page, intent: string): Promise<void> {
  await waitForTurnSettled(page);
  const ok = await page.evaluate(async (name) => {
    const fn = (window as unknown as {
      __kitsokiSubmitIntent?: (n: string, s?: Record<string, unknown>) => Promise<void>;
    }).__kitsokiSubmitIntent;
    if (!fn) return false;
    await fn(name, {});
    return true;
  }, intent);
  if (!ok) throw new Error(`__kitsokiSubmitIntent hook not present for ${intent}`);
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

test("slidey architect-design rrweb capture (baseline + event stream)", async () => {
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
    diag("navigating home");
    await cinematicGoto(page, `${server.base}/#/`, { waitForTestId: "home-view" });

    await installCapture(page);
    diag("rrweb capture installed");

    await page.evaluate((stepsJson: string) => {
      window.__startTourWithSteps?.(stepsJson);
    }, JSON.stringify(SLIDEY_ARCHITECT_DESIGN_TOUR_STEPS));
    await expect(page.getByTestId("tour-overlay")).toBeVisible({ timeout: 8000 });

    let pngIdx = 0;
    for (const step of SLIDEY_ARCHITECT_DESIGN_TOUR_STEPS) {
      const routeKind = routeKindFromUrl(page.url());
      if (step.route !== "any" && step.route !== routeKind) {
        diag(`step ${step.id}: route-skip (on ${routeKind})`);
        continue;
      }
      diag(`step ${step.id}`);

      if (step.route === "interactive") chapters.open(step.id, step.title, CHAPTER_SOURCE);

      await runDrive(page, step.drive);

      if (step.waitForTarget) {
        await expect(page.getByTestId(step.waitForTarget).first()).toBeVisible({ timeout: 15000 });
      }

      await expect(page.getByTestId("tour-title")).toHaveText(step.title, { timeout: 12000 });

      if (step.route !== "interactive") {
        chapters.open(step.id, step.title, CHAPTER_SOURCE);
        await dwell(page, step.dwellMs ?? 3000);
      }
      pngIdx++;
      void pngIdx;
      await shot(page, step.id);

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

    // ── Full-screen the published design and scroll through it ───────────────
    diag("opening published design artifact");
    chapters.open("sad-design-artifact", "Published design — full document", CHAPTER_SOURCE);
    await showArtifact(page, "stories/slidey-dev/assets/architect_design-doc.md");
    diag("design artifact shown + scrolled");

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
    const mp4 = await saveVideoAsMp4(video, ARTIFACT_DIR, "slidey-architect-design-baseline");
    writeChapters(mp4, chapters.list());
    await browser.close();
  }

  const pngs = fs.readdirSync(BASELINE_FRAMES_DIR).filter((f) => f.endsWith(".png"));
  console.log(`[slidey-architect-design-rrweb-capture] baseline frames (${pngs.length}) in ${BASELINE_FRAMES_DIR}`);
  console.log(`[slidey-architect-design-rrweb-capture] events → ${EVENTS_JSON}`);
});
