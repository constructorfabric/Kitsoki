/**
 * github-demo-webviewer.spec.ts — Act 2 of the @kitsoki GitHub-loop demo.
 *
 * The "kitsoki side" of the loop, captured against a REAL `kitsoki web` server
 * in no-LLM replay posture. It shows ONLY surfaces that ship today:
 *
 *   - the tour-narrated home → observer intro (home-view → story-card →
 *     new-session-btn → chat),
 *   - live trace streaming in the observer (chat-transcript / trace surface),
 *   - the operator typing prose into the composer (composer-input/send) which
 *     the recording routes to the slot-bearing PRD intents, and
 *   - the state-badge (current-state) advancing as each turn lands.
 *
 * slidey IS the case study: this drives the slidey-dev PRD tour
 * (SLIDEY_PM_IDEA_TOUR_STEPS) against stories/slidey-dev + the pm_idea replay
 * recording + host cassette — the same spawn shape as
 * slidey-pm-idea-rrweb-capture.spec.ts, verbatim. REPLAY (not --flow) is
 * required: the operator types prose the recording routes to slot-bearing
 * core__prd intents (a nil-harness --flow cannot extract a slot from prose).
 *
 * What this spec deliberately does NOT assert (gate.act2CannotShow — those
 * seams belong to unbuilt slices #4/#5, not this demo slice):
 *   - a browsable artifact gallery (no gallery surface exists today),
 *   - an ack-comment posted back to the GitHub thread (no host path exists).
 * The composite deck narrates those as cross-cut fixtures (Act 1 / closer),
 * never as a real-surface assertion here.
 *
 * Artifacts (under .artifacts/github-demo-act2/):
 *   - github-demo-webviewer.mp4            ← the recorded Act-2 clip
 *   - github-demo-webviewer.mp4.chapters.json ← chapter sidecar (one per scene)
 *   - frames/NN-<scene>.png                ← labeled ground-truth frames (QA)
 *
 * Validate fast:
 *   WEB_CHAT_PACE=0 pnpm exec playwright test github-demo-webviewer --project=chromium
 * Record at watch pace (the shippable clip — do NOT ship the WEB_CHAT_PACE=0 cut):
 *   pnpm exec playwright test github-demo-webviewer --project=chromium
 * (pnpm cwd = tools/runstatus.)
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
import { SLIDEY_PM_IDEA_TOUR_STEPS, type TourStep } from "../../src/tour/generated/slidey-dev-prd-design.js";

const CHAPTER_SOURCE = "features/slidey-dev-prd-design.yaml";

// Reuse the slidey pm_idea drive config verbatim (slideyDriveConfig).
const ADDR = "127.0.0.1:7762";
const STORY_DIR = path.join(repoRoot, "stories", "slidey-dev");
const RECORDING = path.join(STORY_DIR, "assets", "pm_idea-recording.yaml");
const HOST_CASSETTE = path.join(STORY_DIR, "assets", "pm_idea-host.cassette.yaml");
const EMBED_REPO = repoRoot;

const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "github-demo-act2");
const FRAMES_DIR = path.join(ARTIFACT_DIR, "frames");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "_video");
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
  fs.mkdirSync(FRAMES_DIR, { recursive: true });
  fs.writeFileSync(DIAG_LOG, "");
  server = await startWebServer({
    addr: ADDR,
    harness: "replay",
    recording: RECORDING,
    hostCassette: HOST_CASSETTE,
    storiesDir: STORY_DIR,
    extraEnv: { KITSOKI_REPO: EMBED_REPO },
  });
});

test.afterAll(() => server?.stop());

// Eased natural-scroll over the chat transcript (composeVisibly reveal helper).
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

// The slidey pm_idea recording routes a handful of bare navigation VERBS — typed
// as free text at a conversational room — to the slot-less PRD pipeline intents
// (idle `ready` → start, search `confirm` → confirm, clarifying `submit` →
// submit_answers). Under the live SPA those rooms render the STRUCTURED composer
// (their text-slot intents pin the composer to the discuss/answer sink), so a
// typed "ready" would fire core__prd__discuss, never reaching the semantic
// router that maps it to start. These rooms ship no button for the verb either.
// So we submit the verb's mapped intent through the live submit path
// (window.__driveIntent) — trace-honest (the same turn a real router-routed verb
// produces) and the transcript + state-badge advance identically. The
// SLOT-BEARING prose (the idea, the clarifying answer) is still TYPED visibly
// into the composer (typeAndSend), exactly as a PM would.
const VERB_INTENTS: Record<string, string> = {
  ready: "core__prd__start",
  confirm: "core__prd__confirm",
  submit: "core__prd__submit_answers",
};

async function runDrive(page: Page, actions: TourStep["drive"]): Promise<void> {
  for (const a of actions ?? []) {
    switch (a.type) {
      case "type-and-send": {
        const verbIntent = VERB_INTENTS[a.text.trim().toLowerCase()];
        if (verbIntent) {
          await driveIntent(page, verbIntent);
        } else {
          await typeAndSend(page, a.text);
        }
        break;
      }
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

// composeVisibly: type the prose into the composer with a visible cadence, hold,
// then submit via composer-send (text-floor fallback).
async function typeAndSend(page: Page, text: string): Promise<void> {
  const focused = await page.evaluate(() => {
    const input =
      (document.querySelector('[data-testid="composer-input"]') as HTMLInputElement | null) ??
      (document.querySelector('[data-testid="text-floor-input"]') as HTMLInputElement | null);
    if (!input) return false;
    input.focus();
    input.value = "";
    input.dispatchEvent(new Event("input", { bubbles: true }));
    return true;
  });
  if (!focused) throw new Error("composer-input not found (no composer or text floor)");
  // Visible keystroke cadence (scaled by pace; collapses to an instant fill at PACE=0).
  if (paced(1) > 0) {
    await page.keyboard.type(text, { delay: 14 });
  } else {
    await page.evaluate((t) => {
      const input =
        (document.querySelector('[data-testid="composer-input"]') as HTMLInputElement | null) ??
        (document.querySelector('[data-testid="text-floor-input"]') as HTMLInputElement | null);
      if (input) {
        input.value = t;
        input.dispatchEvent(new Event("input", { bubbles: true }));
      }
    }, text);
  }
  await dwell(page, paced(600)); // hold so the typed prose is legible
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

// Submit a navigation intent through the SPA's live submit path (the
// automation-only window.__driveIntent seam registered by ChatSurface). Used for
// the recording's bare verbs that a conversational room exposes only as a
// semantic verb in prose (no button, and its structured composer would pin typed
// prose to the discuss/answer sink).
async function driveIntent(page: Page, intent: string): Promise<void> {
  const ok = await page.evaluate(async (name) => {
    if (typeof window.__kitsokiSubmitIntent !== "function") return false;
    await window.__kitsokiSubmitIntent(name);
    return true;
  }, intent);
  if (!ok) throw new Error(`__kitsokiSubmitIntent unavailable (cannot submit ${intent})`);
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

// Poll current-state until it reads `state` — proves the state-badge advances on
// a landed turn (gate: real surface).
//
// The state-BADGE (current-state) updates from the turn result a beat BEFORE the
// SPA re-renders `currentView` (the room's composer + its active-text-intent /
// default_intent). If we typed the next prose the instant the badge flipped, the
// composer could still carry the PRIOR room's text-intent — at core.landing that
// is `core__work` (the workbench sink), so the slidey idea would fire as
// core__work and bounce prd.idle → landing instead of routing to
// core__prd__discuss. We therefore also wait for the VIEW to settle: the
// landing-only quick-action `core__go_prd` button must be gone before we accept
// a non-landing state. (Belt-and-braces: also require no pending turn.)
async function waitForState(page: Page, state: string, timeoutMs: number): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  let cur = "";
  while (Date.now() < deadline) {
    const probe = await page.evaluate(() => {
      const badge = document.querySelector('[data-testid="current-state"]');
      const cur = badge ? (badge.textContent || "").trim() : "";
      // The PRD/bugfix-pipeline quick-action lives ONLY on the core.landing
      // workbench view; its presence proves currentView still renders landing.
      const onLanding = !!document.querySelector('[data-testid="intent-btn-core__go_prd"]');
      const pending = !!document.querySelector('[data-testid="composer-input"][disabled], [data-testid="text-floor-input"][disabled]');
      return { cur, onLanding, pending };
    });
    cur = probe.cur;
    const viewSettled = state === "core.landing" ? probe.onLanding : !probe.onLanding;
    if (cur === state && viewSettled && !probe.pending) return;
    await page.waitForTimeout(300);
  }
  throw new Error(`wait-state ${state} timed out (last "${cur}")`);
}

async function revealTurn(page: Page): Promise<void> {
  await page.evaluate(SCROLL_CONTROL);
  await dwell(page, paced(1400));
  const top = await page.evaluate(() => (window.__lastUserTop ? window.__lastUserTop() : 0));
  await ease(page, top, paced(1200));
  await dwell(page, paced(1300));
  const max = await page.evaluate(() => (window.__scrollMax ? window.__scrollMax() : 0));
  let span = max - top;
  if (span < 0) span = 0;
  let downMs = Math.round(span * 3);
  if (downMs < 700) downMs = 700;
  if (downMs > 3000) downMs = 3000;
  await ease(page, max, paced(downMs));
  await dwell(page, paced(1500));
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
    __kitsokiSubmitIntent?: (
      name: string,
      slots?: Record<string, unknown>,
      displayLabel?: string,
    ) => Promise<void>;
  }
}

test("github-demo act2 kitsoki-side webviewer video", async () => {
  test.setTimeout(360000);
  const browser: Browser = await chromium.launch({ headless: true });
  const context: BrowserContext = await browser.newContext({
    viewport: { ...VIEWPORT },
    recordVideo: { dir: VIDEO_DIR, size: { ...VIEWPORT } },
  });
  const page: Page = await context.newPage();
  const video = page.video();
  const shot = makeShot(FRAMES_DIR);
  const chapters = new ChapterRecorder();

  try {
    diag("navigating home");
    await cinematicGoto(page, `${server.base}/#/`, { waitForTestId: "home-view" });

    // Drive the SPA's real tour overlay (home → observer → chat narration).
    await page.evaluate((stepsJson: string) => {
      window.__startTourWithSteps?.(stepsJson);
    }, JSON.stringify(SLIDEY_PM_IDEA_TOUR_STEPS));
    await expect(page.getByTestId("tour-overlay")).toBeVisible({ timeout: 8000 });

    let pngIdx = 0;
    for (const step of SLIDEY_PM_IDEA_TOUR_STEPS) {
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
        await dwell(page, paced(step.dwellMs ?? 3000));
      }
      pngIdx++;
      void pngIdx;
      await shot(page, step.id);

      if (step.kind === "explain") {
        await page.getByTestId("tour-next").click();
        await dwell(page, paced(700));
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
        await dwell(page, paced(1000));
      }
    }

    await expect(page.getByTestId("tour-overlay")).toHaveCount(0, { timeout: 5000 });
  } catch (e) {
    diag(`FAILED: ${e instanceof Error ? e.stack ?? e.message : String(e)}`);
    diag(`--- server log ---\n${server?.log?.() ?? ""}`);
    throw e;
  } finally {
    await context.close();
    const mp4 = await saveVideoAsMp4(video, ARTIFACT_DIR, "github-demo-webviewer");
    if (mp4) writeChapters(mp4, chapters.list());
    await browser.close();
  }

  const pngs = fs.readdirSync(FRAMES_DIR).filter((f) => f.endsWith(".png"));
  console.log(`[github-demo-webviewer] frames (${pngs.length}) in ${FRAMES_DIR}`);
});
