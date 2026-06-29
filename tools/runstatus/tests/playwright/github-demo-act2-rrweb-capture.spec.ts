/**
 * github-demo-act2-rrweb-capture.spec.ts — Act 2 (the kitsoki side) rrweb clip.
 *
 * Produces docs/proposals/demo-assets/kitsoki-github/deck/clips/act2-webviewer.rrweb.json
 * — the rrweb DOM-session log embedded by the @kitsoki GitHub-loop deck's Act-2
 * `video` scene. It is a fork of github-demo-webviewer.spec.ts (the proven Act-2
 * drive: live trace + composer prose→intent + state-badge), swapping the live
 * screen-record for the kitsoki rrweb capture harness (installCapture / dumpCapture
 * / writeEvents from _helpers/rrweb-replay.ts) — the same harness slidey-pm-idea-
 * rrweb-capture.spec.ts uses.
 *
 * WHY THE KITSOKI HARNESS, NOT SLIDEY'S TOUR ENGINE: Act 1 is a static file://
 * fixture, so slidey's `capture --format rrweb` drives it directly. Act 2 needs
 * the kitsoki SPA's tour overlay (__startTourWithSteps) AND the composer
 * prose→intent routing (typeAndSend into the slot-bearing PRD intents, plus the
 * window.__kitsokiSubmitIntent seam for the recording's bare navigation verbs).
 * slidey's app-agnostic tour engine has no vocabulary for either, so Act 2 is
 * captured here and the clip is embedded by path.
 *
 * CHAPTERS: like slidey's tour engine, this stamps in-log `slidey.chapter` custom
 * events (window.rrweb.record.addCustomEvent) at each interactive step boundary,
 * so the clip is self-describing and the deck's `chapters:"auto"` derives
 * lower-thirds with no sidecar.
 *
 * Run at watch-speed (the shippable clip — do NOT ship the WEB_CHAT_PACE=0 cut):
 *   pnpm exec playwright test github-demo-act2-rrweb-capture --project=chromium
 * Fast-validate (no dwells):
 *   WEB_CHAT_PACE=0 pnpm exec playwright test github-demo-act2-rrweb-capture --project=chromium
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
  type WebServer,
} from "./_helpers/server.js";
import { installCapture, dumpCapture, writeEvents } from "./_helpers/rrweb-replay.js";
import { SLIDEY_PM_IDEA_TOUR_STEPS, type TourStep } from "../../src/tour/generated/slidey-dev-prd-design.js";

const CHAPTER_SOURCE = "features/slidey-dev-prd-design.yaml";

// Reuse the slidey pm_idea drive config verbatim (slideyDriveConfig), same as
// github-demo-webviewer.spec.ts. REPLAY (not --flow): the operator types prose
// the recording routes to slot-bearing core__prd intents.
const ADDR = "127.0.0.1:7764";
const STORY_DIR = path.join(repoRoot, "stories", "slidey-dev");
const RECORDING = path.join(STORY_DIR, "assets", "pm_idea-recording.yaml");
const HOST_CASSETTE = path.join(STORY_DIR, "assets", "pm_idea-host.cassette.yaml");
const EMBED_REPO = repoRoot;

// The clip lands directly in the deck's clips dir (committed) — not .artifacts.
const CLIP_OUT = path.join(
  repoRoot,
  "docs",
  "proposals",
  "demo-assets",
  "kitsoki-github",
  "deck",
  "clips",
  "act2-webviewer.rrweb.json",
);
const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "github-demo-act2-rrweb");
const FRAMES_DIR = path.join(ARTIFACT_DIR, "frames");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "_video");
const DIAG_LOG = path.join(ARTIFACT_DIR, "diagnostic.log");

const VIEWPORT = { width: 1600, height: 900 } as const;

let server: WebServer;

function diag(msg: string): void {
  try {
    fs.appendFileSync(DIAG_LOG, `[${new Date().toISOString()}] ${msg}\n`);
  } catch {
    /* best-effort */
  }
}

test.beforeAll(async () => {
  prepareVideoDir(VIDEO_DIR);
  fs.mkdirSync(ARTIFACT_DIR, { recursive: true });
  fs.mkdirSync(FRAMES_DIR, { recursive: true });
  fs.mkdirSync(path.dirname(CLIP_OUT), { recursive: true });
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
    [to, ms],
  );
}

// See github-demo-webviewer.spec.ts: the recording routes a handful of bare
// navigation verbs — typed as free text at a conversational room — to the
// slot-less PRD pipeline intents. Those rooms render the STRUCTURED composer, so
// we submit the verb's mapped intent through the live submit seam instead.
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
  await dwell(page, paced(600));
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

async function waitForState(page: Page, state: string, timeoutMs: number): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  let cur = "";
  while (Date.now() < deadline) {
    const probe = await page.evaluate(() => {
      const badge = document.querySelector('[data-testid="current-state"]');
      const cur = badge ? (badge.textContent || "").trim() : "";
      const onLanding = !!document.querySelector('[data-testid="intent-btn-core__go_prd"]');
      const pending = !!document.querySelector(
        '[data-testid="composer-input"][disabled], [data-testid="text-floor-input"][disabled]',
      );
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

// Stamp an in-log slidey.chapter custom event so the clip is self-describing
// (same mechanism slidey's tour engine uses). Best-effort: never fail the drive
// over a chapter marker.
async function markChapter(page: Page, id: string, label: string): Promise<void> {
  await page
    .evaluate(
      ({ id, label, specPath }) => {
        const w = window as unknown as { rrweb?: { record?: { addCustomEvent?: (t: string, p: unknown) => void } } };
        w.rrweb?.record?.addCustomEvent?.("slidey.chapter", { id, label, specPath });
      },
      { id, label, specPath: CHAPTER_SOURCE },
    )
    .catch(() => {});
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

test("github-demo act2 kitsoki-side rrweb capture", async () => {
  test.setTimeout(360000);
  const browser: Browser = await chromium.launch({ headless: true });
  const context: BrowserContext = await browser.newContext({
    viewport: { ...VIEWPORT },
    recordVideo: { dir: VIDEO_DIR, size: { ...VIEWPORT } },
  });
  const page: Page = await context.newPage();
  const video = page.video();
  const shot = makeShot(FRAMES_DIR);

  try {
    diag("navigating home");
    await cinematicGoto(page, `${server.base}/#/`, { waitForTestId: "home-view" });

    await installCapture(page);
    diag("rrweb capture installed");

    await page.evaluate((stepsJson: string) => {
      window.__startTourWithSteps?.(stepsJson);
    }, JSON.stringify(SLIDEY_PM_IDEA_TOUR_STEPS));
    await expect(page.getByTestId("tour-overlay")).toBeVisible({ timeout: 8000 });

    for (const step of SLIDEY_PM_IDEA_TOUR_STEPS) {
      const routeKind = routeKindFromUrl(page.url());
      if (step.route !== "any" && step.route !== routeKind) {
        diag(`step ${step.id}: route-skip (on ${routeKind})`);
        continue;
      }
      diag(`step ${step.id}`);

      // Mark the chapter boundary BEFORE the step's motion, so the window opens
      // on the step's first frame (mirrors slidey's tour engine).
      await markChapter(page, step.id, step.title);

      await runDrive(page, step.drive);

      if (step.waitForTarget) {
        await expect(page.getByTestId(step.waitForTarget).first()).toBeVisible({ timeout: 15000 });
      }

      await expect(page.getByTestId("tour-title")).toHaveText(step.title, { timeout: 12000 });

      if (step.route !== "interactive") {
        await dwell(page, paced(step.dwellMs ?? 3000));
      }
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

    // Tail dwell so the final chapter window spans its dwell (not a zero-width
    // collapse — same reason slidey stamps a trailing sentinel).
    await dwell(page, paced(1500));

    const { events, viewport } = await dumpCapture(page);
    diag(`rrweb captured ${events.length} events @ ${viewport.width}x${viewport.height} dsf=${viewport.deviceScaleFactor}`);
    const chapterCount = events.filter(
      (e) =>
        (e as { type?: number }).type === 5 &&
        ((e as { data?: { tag?: string } }).data?.tag === "slidey.chapter"),
    ).length;
    diag(`in-log slidey.chapter markers: ${chapterCount}`);
    writeEvents(events, CLIP_OUT, viewport);
    expect(events.length, "rrweb should have emitted a healthy event stream").toBeGreaterThanOrEqual(50);
    expect(chapterCount, "clip must carry in-log chapters").toBeGreaterThanOrEqual(1);
  } catch (e) {
    diag(`FAILED: ${e instanceof Error ? e.stack ?? e.message : String(e)}`);
    diag(`--- server log ---\n${server?.log?.() ?? ""}`);
    throw e;
  } finally {
    await context.close();
    await saveVideoAsMp4(video, ARTIFACT_DIR, "github-demo-act2-rrweb");
    await browser.close();
  }

  const pngs = fs.readdirSync(FRAMES_DIR).filter((f) => f.endsWith(".png"));
  console.log(`[github-demo-act2-rrweb-capture] frames (${pngs.length}) in ${FRAMES_DIR}`);
  console.log(`[github-demo-act2-rrweb-capture] clip → ${CLIP_OUT}`);
});
