/**
 * Live GitHub-agent POC capture harness.
 *
 * This spec is intentionally gated and skipped by default. It records REAL
 * GitHub and kitsoki-test pages after the live POC cases have been created:
 *
 *   KITSOKI_GH_AGENT_LIVE_CAPTURE=1 \
 *   KITSOKI_GH_AGENT_LIVE_CAPTURE_PLAN=.artifacts/github-agent-live/capture-plan.json \
 *   pnpm -C tools/runstatus exec playwright test github-agent-live-capture --project=chromium
 *
 * The capture plan lives under .artifacts because it names real throwaway
 * issue/PR/run URLs. Generated media also stays under .artifacts.
 */
import { test, chromium, type Browser, type BrowserContext, type Page } from "@playwright/test";
import fs from "fs";
import path from "path";
import {
  repoRoot,
  makeShot,
  dwell,
  SETTLE_MS,
} from "./_helpers/server.js";
import { cameraContext } from "./_helpers/camera.js";
import { captureDiagnostics, installCurtain, liftCurtain, makeCaption, makeSpotlight, type Beat, type Spotlight } from "./_helpers/demo.js";
import { dumpCapture, installCapture, type CaptureViewport, type RrwebEvent } from "./_helpers/rrweb-replay.js";

type CaptureStep = {
  id: string;
  title: string;
  url: string;
  caption?: string;
  waitForText?: string;
  dwellMs?: number;
};

type CapturePlan = {
  artifactDir?: string;
  curtainTitle?: string;
  steps: CaptureStep[];
};

const DEFAULT_PLAN = path.join(repoRoot, ".artifacts", "github-agent-live", "capture-plan.json");
const SPEC_REF = "tools/runstatus/tests/playwright/github-agent-live-capture.spec.ts";
const CHAPTER_TAG = "slidey.chapter";

function loadPlan(): CapturePlan {
  const planPath = process.env.KITSOKI_GH_AGENT_LIVE_CAPTURE_PLAN || DEFAULT_PLAN;
  const raw = fs.readFileSync(planPath, "utf8");
  const plan = JSON.parse(raw) as CapturePlan;
  if (!Array.isArray(plan.steps) || plan.steps.length === 0) {
    throw new Error(`capture plan ${planPath} must contain a non-empty steps array`);
  }
  for (const [idx, step] of plan.steps.entries()) {
    if (!step.id || !step.title || !step.url) {
      throw new Error(`capture plan step ${idx + 1} must include id, title, and url`);
    }
    if (!/^https?:\/\//.test(step.url)) {
      throw new Error(`capture plan step ${step.id} must use an http(s) URL, got ${step.url}`);
    }
  }
  return plan;
}

async function tryInstallCurtain(page: Page, title: string): Promise<void> {
  try {
    await installCurtain(page, title);
  } catch (e) {
    console.warn(`[live-capture] curtain disabled: ${String(e).slice(0, 240)}`);
  }
}

async function tryLiftCurtain(page: Page): Promise<void> {
  try {
    await liftCurtain(page);
  } catch (e) {
    console.warn(`[live-capture] curtain lift skipped: ${String(e).slice(0, 240)}`);
  }
}

async function tryRearmCurtain(page: Page, title: string): Promise<void> {
  try {
    await page.evaluate((t: string) => {
      sessionStorage.removeItem("kd-curtain-lifted");
      document.documentElement.style.background = "#070d1a";
      if (document.body) document.body.style.background = "#070d1a";
      let c = document.getElementById("kd-curtain");
      if (!c) {
        c = document.createElement("div");
        c.id = "kd-curtain";
        (document.body ?? document.documentElement).appendChild(c);
      }
      c.style.cssText =
        "position:fixed;inset:0;z-index:2147483647;background:#070d1a;display:flex;" +
        "align-items:center;justify-content:center;pointer-events:none;color:#e2e8f0;" +
        "font:700 34px ui-sans-serif,system-ui,sans-serif;letter-spacing:.02em;transition:opacity .6s";
      c.textContent = t;
    }, title);
  } catch (e) {
    console.warn(`[live-capture] curtain re-arm skipped: ${String(e).slice(0, 240)}`);
  }
}

async function tryMakeCaption(page: Page): Promise<(title: string, sub?: string, holdMs?: number) => Promise<void>> {
  try {
    return await makeCaption(page);
  } catch (e) {
    console.warn(`[live-capture] captions disabled: ${String(e).slice(0, 240)}`);
    return async (_title, _sub, holdMs = 5000) => {
      await dwell(page, holdMs);
    };
  }
}

async function tryMakeSpotlight(page: Page): Promise<Spotlight> {
  try {
    return await makeSpotlight(page);
  } catch (e) {
    console.warn(`[live-capture] spotlight disabled: ${String(e).slice(0, 240)}`);
    return async () => undefined;
  }
}

async function tryStyleAPIProof(page: Page): Promise<void> {
  try {
    await page.addStyleTag({
      content:
        `html,body{margin:0;min-height:100%;background:#0b1220!important;color:#dbeafe!important;` +
        `font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace!important}` +
        `body{display:flex;align-items:center;justify-content:center;padding:56px!important}` +
        `body::before{content:"Live /api/run job state";position:fixed;top:24px;left:50%;` +
        `transform:translateX(-50%);font:700 22px ui-sans-serif,system-ui,sans-serif;` +
        `color:#f8fafc;background:#111827;border:1px solid #334155;border-left:4px solid #38bdf8;` +
        `border-radius:10px;padding:12px 18px;box-shadow:0 12px 32px rgba(0,0,0,.35)}` +
        `pre{box-sizing:border-box;width:min(1180px,88vw);max-height:70vh;overflow:auto;` +
        `white-space:pre-wrap;overflow-wrap:anywhere;background:#111827!important;color:#dbeafe!important;` +
        `border:1px solid #334155;border-radius:14px;padding:28px 32px!important;` +
        `font-size:18px!important;line-height:1.55!important;box-shadow:0 24px 70px rgba(0,0,0,.45)}`,
    });
  } catch (e) {
    console.warn(`[live-capture] API proof styling skipped: ${String(e).slice(0, 240)}`);
  }
}

async function markTextTarget(page: Page, name: string, needle: string): Promise<string | null> {
  const marked = await page
    .evaluate(
      ({ attr, value }) => {
        const exact = value.trim().toLowerCase();
        const candidates = Array.from(
          document.querySelectorAll<HTMLElement>(
            "a, button, h1, h2, h3, pre, code, p, li, td, th, span, div.timeline-comment, div.js-comment-body, div.markdown-body",
          ),
        );
        const scored = candidates
          .map((el) => {
            const text = (el.innerText || el.textContent || "").replace(/\s+/g, " ").trim();
            if (!text) return { el, score: 0 };
            const lower = text.toLowerCase();
            if (lower === exact) return { el, score: 1000 - text.length };
            if (lower.includes(exact)) return { el, score: 500 - Math.min(text.length, 480) };
            return { el, score: 0 };
          })
          .filter((entry) => entry.score > 0)
          .sort((a, b) => b.score - a.score);
        const target = scored[0]?.el;
        if (!target) return false;
        target.setAttribute(attr, name);
        return true;
      },
      { attr: "data-kitsoki-demo-target", value: needle },
    )
    .catch(() => false);
  return marked ? `[data-kitsoki-demo-target="${name}"]` : null;
}

async function markFirstSelector(page: Page, name: string, selectors: string[]): Promise<string | null> {
  const marked = await page
    .evaluate(
      ({ attr, name: targetName, selectors: choices }) => {
        for (const sel of choices) {
          const el = document.querySelector<HTMLElement>(sel);
          if (el) {
            el.setAttribute(attr, targetName);
            return true;
          }
        }
        return false;
      },
      { attr: "data-kitsoki-demo-target", name, selectors },
    )
    .catch(() => false);
  return marked ? `[data-kitsoki-demo-target="${name}"]` : null;
}

async function targetExists(page: Page, selector: string | null): Promise<boolean> {
  if (!selector) return false;
  return await page.locator(selector).first().count().then((n) => n > 0).catch(() => false);
}

async function showTarget(page: Page, spotlight: Spotlight, selector: string | null): Promise<void> {
  if (await targetExists(page, selector)) {
    await spotlight(selector);
  } else {
    await spotlight(null);
  }
}

async function tourGithubThread(page: Page, step: CaptureStep, caption: Beat, spotlight: Spotlight): Promise<void> {
  const title = await markFirstSelector(page, "issue-title", [
    "[data-testid='issue-title']",
    "bdi.js-issue-title",
    ".js-issue-title",
    ".gh-header-title",
    "h1",
  ]);
  await caption(step.title, "Start where the requester worked: the live GitHub thread.", 2300);
  await showTarget(page, spotlight, title);
  await dwell(page, 1500);

  const mention = await markTextTarget(page, "request-mention", "@kitsoki");
  await caption("Requester mentions @kitsoki", "This is the user action that should create exactly one kitsoki job.", 2600);
  await showTarget(page, spotlight, mention);
  await dwell(page, 1500);

  const appComment = await markTextTarget(page, "app-comment", "kitsoki-test.slothattax.me/run/");
  await caption("kitsoki answers on the thread", "The App-authenticated response is the handoff from GitHub into the hosted run.", 2800);
  await showTarget(page, spotlight, appComment);
  await dwell(page, 1800);

  const runLink = await markTextTarget(page, "run-link", "https://kitsoki-test.slothattax.me/run/");
  await caption("User follows the run link", "The visible link is the proof surface the requester can open.", 2300);
  await showTarget(page, spotlight, runLink);
  await dwell(page, 1500);
}

async function tourAppComment(page: Page, step: CaptureStep, caption: Beat, spotlight: Spotlight): Promise<void> {
  const comment = await markTextTarget(page, "app-comment-anchor", "kitsoki-test.slothattax.me/run/");
  await caption(step.title, "The URL opens directly on the App response, not just the original mention.", 2600);
  await showTarget(page, spotlight, comment);
  await dwell(page, 1700);

  const link = await markTextTarget(page, "app-run-link", "https://kitsoki-test.slothattax.me/run/");
  await caption("Run link is the user's next click", "This is the durable bridge from the GitHub thread to kitsoki's hosted evidence.", 2600);
  await showTarget(page, spotlight, link);
  await dwell(page, 1700);
}

async function tourRunPage(page: Page, step: CaptureStep, caption: Beat, spotlight: Spotlight): Promise<void> {
  const heading = await markFirstSelector(page, "run-heading", ["h1", "main h2", "body"]);
  await caption(step.title, "The hosted page proves the VM-backed job exists and is readable.", 2600);
  await showTarget(page, spotlight, heading);
  await dwell(page, 1600);

  const state = await markTextTarget(page, "run-state", "state");
  await caption("Read the job state", "The page exposes the route, state, source, and run URL a reviewer needs.", 2600);
  await showTarget(page, spotlight, state);
  await page.mouse.wheel(0, 360).catch(() => undefined);
  await dwell(page, 1800);

  const source = await markTextTarget(page, "run-source", "github.com/bsacrobatix/Kitsoki");
  await caption("Trace it back to GitHub", "The hosted view ties the job back to the original issue or PR.", 2300);
  await showTarget(page, spotlight, source);
  await dwell(page, 1600);
}

async function tourRunAPI(page: Page, step: CaptureStep, caption: Beat, spotlight: Spotlight): Promise<void> {
  const pre = await markFirstSelector(page, "api-json", ["pre", "body"]);
  await caption(step.title, "The same proof is machine-readable for verification and automation.", 2600);
  await showTarget(page, spotlight, pre);
  await dwell(page, 1600);

  const state = await markTextTarget(page, "api-state", "\"state\"");
  await caption("Verifier reads the same state", "This prevents the deck from being only a pretty screenshot.", 2600);
  await showTarget(page, spotlight, state || pre);
  await page.mouse.wheel(0, 320).catch(() => undefined);
  await dwell(page, 1800);
}

async function runStepTour(page: Page, step: CaptureStep, caption: Beat, spotlight: Spotlight): Promise<void> {
  switch (step.id) {
    case "github-thread":
      await tourGithubThread(page, step, caption, spotlight);
      break;
    case "app-comment":
      await tourAppComment(page, step, caption, spotlight);
      break;
    case "run-page":
      await tourRunPage(page, step, caption, spotlight);
      break;
    case "run-api":
      await tourRunAPI(page, step, caption, spotlight);
      break;
    default:
      await caption(step.title, step.caption || step.url, step.dwellMs ?? 5000);
      break;
  }
  await spotlight(null);
}

async function resetRrwebCapture(page: Page): Promise<void> {
  await page
    .evaluate(() => {
      const w = window as unknown as {
        __rrwebEvents?: unknown[];
        __rrwebRecording?: boolean;
        __rrwebStop?: () => void;
      };
      try {
        w.__rrwebStop?.();
      } catch {
        // best effort; the next install starts a new segment.
      }
      delete w.__rrwebEvents;
      delete w.__rrwebRecording;
      delete w.__rrwebStop;
    })
    .catch(() => undefined);
}

function writeRrwebEnvelope(outPath: string, events: RrwebEvent[], viewport: CaptureViewport): void {
  if (events.length < 2) {
    throw new Error(`rrweb capture ${outPath} has only ${events.length} event(s)`);
  }
  const startTime = Number(events[0]?.timestamp ?? 0);
  const endTime = Number(events[events.length - 1]?.timestamp ?? startTime);
  fs.writeFileSync(
    outPath,
    `${JSON.stringify({
      schemaVersion: 1,
      source: "kitsoki-live-github-capture",
      viewport,
      startTime,
      endTime,
      durationMs: Math.max(0, endTime - startTime),
      events,
    })}\n`,
  );
}

async function captureRrwebStep(
  page: Page,
  artifactDir: string,
  idx: number,
  step: CaptureStep,
  caption: Beat,
  spotlight: Spotlight,
): Promise<void> {
  await resetRrwebCapture(page);
  await installCapture(page);
  await page.evaluate(
    ({ id, label, specPath, tag }) => {
      const rrweb = (window as unknown as { rrweb?: { record?: { addCustomEvent?: (tag: string, payload: unknown) => void } } }).rrweb;
      rrweb?.record?.addCustomEvent?.(tag, { id, label, specPath });
    },
    { id: step.id, label: step.title, specPath: SPEC_REF, tag: CHAPTER_TAG },
  );
  await runStepTour(page, step, caption, spotlight);
  await page.evaluate(() => {
    const rrweb = (window as unknown as { rrweb?: { record?: { addCustomEvent?: (tag: string, payload: unknown) => void } } }).rrweb;
    rrweb?.record?.addCustomEvent?.("slidey.end", {});
  });
  const capture = await dumpCapture(page);
  const rrwebPath = path.join(artifactDir, `${String(idx + 1).padStart(2, "0")}-${step.id}.rrweb.json`);
  writeRrwebEnvelope(rrwebPath, capture.events, capture.viewport);
}

test("capture live GitHub-agent evidence", async () => {
  test.skip(
    process.env.KITSOKI_GH_AGENT_LIVE_CAPTURE !== "1",
    "live capture is gated; set KITSOKI_GH_AGENT_LIVE_CAPTURE=1 with a capture plan",
  );

  test.setTimeout(420000);

  const plan = loadPlan();
  const artifactDir = path.resolve(repoRoot, plan.artifactDir || ".artifacts/github-agent-live/capture");
  const curtainTitle = plan.curtainTitle || "Live @kitsoki GitHub App POC";

  fs.mkdirSync(artifactDir, { recursive: true });

  const browser: Browser = await chromium.launch({ headless: true });
  const context: BrowserContext = await browser.newContext({ ...cameraContext(), bypassCSP: true });
  const page: Page = await context.newPage();
  const shot = makeShot(artifactDir);
  const diag = captureDiagnostics(page, artifactDir);

  try {
    diag.mark("install-curtain");
    await tryInstallCurtain(page, curtainTitle);

    for (const [idx, step] of plan.steps.entries()) {
      diag.mark(`step ${step.id}: goto`);
      if (idx > 0) {
        diag.mark(`step ${step.id}: re-arm curtain`);
        await tryRearmCurtain(page, curtainTitle);
      }
      await page.goto(step.url, { waitUntil: "domcontentloaded", timeout: 45000 });
      if (step.waitForText) {
        diag.mark(`step ${step.id}: wait ${step.waitForText}`);
        await page.getByText(step.waitForText, { exact: false }).first().waitFor({ timeout: 30000 });
      }
      if (step.id === "run-api") {
        diag.mark(`step ${step.id}: style-api-proof`);
        await tryStyleAPIProof(page);
      }
      await dwell(page, SETTLE_MS);
      diag.mark(`step ${step.id}: lift-curtain`);
      await tryLiftCurtain(page);
      const caption = await tryMakeCaption(page);
      const spotlight = await tryMakeSpotlight(page);
      diag.mark(`step ${step.id}: rrweb-tour`);
      await captureRrwebStep(page, artifactDir, idx, step, caption, spotlight);
      diag.mark(`step ${step.id}: screenshot`);
      await shot(page, step.id);
    }
  } catch (e) {
    diag.onThrow(e);
    throw e;
  } finally {
    await context.close();
    await browser.close();
  }
});
