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
import {
  captureDiagnostics,
  installCurtain,
  liftCurtain,
  makeCaption,
  makeReadableZoom,
  makeSpotlight,
  makeTextBreath,
  type Beat,
  type ReadableZoom,
  type Spotlight,
  type TextBreath,
} from "./_helpers/demo.js";
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
const ANNOTATION_TAG = "kitsoki.annotation";
const ZOOM_TAG = "kitsoki.readable_zoom";
const ZOOM_RETURN_TAG = "kitsoki.readable_zoom_return";
const MENTION_BREATH_TAG = "kitsoki.mention_breath";

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

async function tryMakeReadableZoom(page: Page): Promise<ReadableZoom> {
  try {
    return await makeReadableZoom(page);
  } catch (e) {
    console.warn(`[live-capture] readable zoom disabled: ${String(e).slice(0, 240)}`);
    return async () => ({ phase: "hidden", shown: false, animatedFromSource: false });
  }
}

async function tryMakeTextBreath(page: Page): Promise<TextBreath> {
  try {
    return await makeTextBreath(page);
  } catch (e) {
    console.warn(`[live-capture] text breath disabled: ${String(e).slice(0, 240)}`);
    return async () => ({ count: 0, texts: [] });
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
  return await page.evaluate((sel) => Boolean(document.querySelector(sel)), selector).catch(() => false);
}

async function showTextTarget(page: Page, name: string, needle: string): Promise<{ selector: string | null; fallback: boolean }> {
  return await page
    .evaluate(
      ({ attr, targetName, value }) => {
        const position = (el: HTMLElement, fallback: boolean): { selector: string; fallback: boolean } => {
          el.setAttribute(attr, targetName);
          el.scrollIntoView({ block: "center", inline: "nearest" });
          const back = document.getElementById("demo-spot-back");
          const box = document.getElementById("demo-spot");
          if (back && box) {
            const r = el.getBoundingClientRect();
            const pad = fallback ? 0 : 8;
            box.style.top = `${Math.max(0, r.top - pad)}px`;
            box.style.left = `${Math.max(0, r.left - pad)}px`;
            box.style.width = `${Math.max(1, r.width + pad * 2)}px`;
            box.style.height = `${Math.max(1, r.height + pad * 2)}px`;
            back.classList.add("show");
            box.classList.add("show");
          }
          return { selector: `[${attr}="${targetName}"]`, fallback };
        };

        const exact = value.trim().toLowerCase();
        const candidates = Array.from(
          document.querySelectorAll<HTMLElement>(
            "a, button, h1, h2, h3, dt, dd, pre, code, p, li, td, th, span, div.timeline-comment, div.js-comment-body, div.markdown-body, div.comment-body",
          ),
        );
        const scored = candidates
          .map((el) => {
            const text = (el.innerText || el.textContent || "").replace(/\s+/g, " ").trim();
            const href = el instanceof HTMLAnchorElement ? el.href : "";
            const lower = `${text} ${href}`.toLowerCase();
            if (!lower.trim()) return { el, score: 0 };
            if (lower === exact) return { el, score: 1000 - text.length };
            if (lower.includes(exact)) return { el, score: 500 - Math.min(text.length || href.length, 480) };
            return { el, score: 0 };
          })
          .filter((entry) => entry.score > 0)
          .sort((a, b) => b.score - a.score);
        const target = scored[0]?.el;
        if (target) return position(target, false);

        const fallback = document.querySelector<HTMLElement>("main, article, body");
        if (fallback) return position(fallback, true);
        return { selector: null, fallback: true };
      },
      { attr: "data-kitsoki-demo-target", targetName: name, value: needle },
    )
    .catch(() => ({ selector: null, fallback: true }));
}

async function showTarget(page: Page, spotlight: Spotlight, selector: string | null): Promise<{ selector: string | null; fallback: boolean }> {
  if (await targetExists(page, selector)) {
    await spotlight(selector);
    return { selector, fallback: false };
  }
  for (const fallback of ["main", "article", "body"]) {
    if (await targetExists(page, fallback)) {
      await spotlight(fallback);
      return { selector: fallback, fallback: true };
    }
  }
  await spotlight(null);
  return { selector: null, fallback: true };
}

async function annotatedBeat(
  page: Page,
  caption: Beat,
  spotlight: Spotlight,
  selector: string | null,
  title: string,
  sub: string,
  holdMs: number,
): Promise<void> {
  const shown = await showTarget(page, spotlight, selector);
  await page.evaluate(
    ({ tag, title: eventTitle, requestedSelector, shownSelector, fallback }) => {
      const rrweb = (window as unknown as { rrweb?: { record?: { addCustomEvent?: (tag: string, payload: unknown) => void } } }).rrweb;
      rrweb?.record?.addCustomEvent?.(tag, {
        title: eventTitle,
        requestedSelector,
        shownSelector,
        fallback,
      });
    },
    {
      tag: ANNOTATION_TAG,
      title,
      requestedSelector: selector,
      shownSelector: shown.selector,
      fallback: shown.fallback,
    },
  );
  await caption(title, sub, holdMs);
}

async function annotatedTextBeat(
  page: Page,
  caption: Beat,
  needle: string,
  targetName: string,
  title: string,
  sub: string,
  holdMs: number,
): Promise<void> {
  const shown = await showTextTarget(page, targetName, needle);
  await page.evaluate(
    ({ tag, title: eventTitle, needle: eventNeedle, shownSelector, fallback }) => {
      const rrweb = (window as unknown as { rrweb?: { record?: { addCustomEvent?: (tag: string, payload: unknown) => void } } }).rrweb;
      rrweb?.record?.addCustomEvent?.(tag, {
        title: eventTitle,
        needle: eventNeedle,
        shownSelector,
        fallback,
      });
    },
    {
      tag: ANNOTATION_TAG,
      title,
      needle,
      shownSelector: shown.selector,
      fallback: shown.fallback,
    },
  );
  await caption(title, sub, holdMs);
}

async function markGithubCommentByText(
  page: Page,
  name: string,
  needles: string[],
): Promise<{ selector: string | null; fallback: boolean; textSample: string }> {
  return await page
    .evaluate(
      ({ attr, targetName, values }) => {
        const wanted = values.map((value: string) => value.trim().toLowerCase()).filter(Boolean);
        const commentSelector = [
          "[data-testid^='comment-viewer-outer-box-']",
          "[data-testid^='timeline-row-border-']",
          "[id^='discussion_r']",
          ".js-timeline-item",
          ".TimelineItem",
          ".timeline-comment-group",
          ".timeline-comment",
          ".js-comment-container",
          ".js-comment",
          ".comment",
        ].join(",");

        const textOf = (el: HTMLElement): string => (el.innerText || el.textContent || "").replace(/\s+/g, " ").trim();
        const containsAll = (text: string): boolean => wanted.every((value: string) => text.toLowerCase().includes(value));
        const candidates = Array.from(document.querySelectorAll<HTMLElement>(commentSelector))
          .map((el) => {
            const text = textOf(el);
            if (!containsAll(text)) return { el, score: 0, text };
            const hasAvatar = Boolean(el.querySelector("img.avatar, img[alt*='@'], .avatar"));
            const hasHeader = Boolean(el.querySelector(".TimelineItem-header, .timeline-comment-header, .comment-header, h3, h4"));
            const hasBody = Boolean(el.querySelector(".comment-body, .js-comment-body, .markdown-body, [data-test-selector='issue-body']"));
            const hasAnchor = /^issuecomment-|^discussion_r/.test(el.id);
            const area = Math.max(1, Math.round(el.getBoundingClientRect().width * el.getBoundingClientRect().height));
            const chromeScore = (hasAvatar ? 150 : 0) + (hasHeader ? 100 : 0) + (hasBody ? 100 : 0) + (hasAnchor ? 60 : 0);
            return { el, score: chromeScore + Math.min(area / 1000, 220), text };
          })
          .filter((entry) => entry.score > 0)
          .sort((a, b) => b.score - a.score);

        let target = candidates[0]?.el || null;
        if (!target) {
          const leaves = Array.from(
            document.querySelectorAll<HTMLElement>(
              "a, button, h2, h3, dt, dd, pre, code, p, li, td, th, span, div.js-comment-body, div.markdown-body, div.comment-body",
            ),
          );
          const leaf = leaves.find((el) => containsAll(textOf(el)));
          target = leaf?.closest<HTMLElement>(commentSelector) || null;
        }
        if (!target) return { selector: null, fallback: true, textSample: "" };

        target.setAttribute(attr, targetName);
        target.scrollIntoView({ block: "center", inline: "nearest" });
        const back = document.getElementById("demo-spot-back");
        const box = document.getElementById("demo-spot");
        if (back && box) {
          const r = target.getBoundingClientRect();
          const pad = 8;
          box.style.top = `${Math.max(0, r.top - pad)}px`;
          box.style.left = `${Math.max(0, r.left - pad)}px`;
          box.style.width = `${Math.max(1, r.width + pad * 2)}px`;
          box.style.height = `${Math.max(1, r.height + pad * 2)}px`;
          back.classList.add("show");
          box.classList.add("show");
        }
        return {
          selector: `[${attr}="${targetName}"]`,
          fallback: !target.matches(commentSelector),
          textSample: textOf(target).slice(0, 400),
        };
      },
      { attr: "data-kitsoki-demo-target", targetName: name, values: needles },
    )
    .catch(() => ({ selector: null, fallback: true, textSample: "" }));
}

async function markFirstGithubComment(page: Page, name: string): Promise<{ selector: string | null; fallback: boolean; textSample: string }> {
  return await page
    .evaluate(
      ({ attr, targetName }) => {
        const selectors = [
          "[data-testid='issue-body']",
          "#issue-body-viewer",
          "[data-testid='issue-body-viewer']",
          "[data-test-selector='issue-body']",
          ".js-issue-body",
        ];
        const textOf = (el: HTMLElement): string => (el.innerText || el.textContent || "").replace(/\s+/g, " ").trim();
        let target: HTMLElement | null = null;
        for (const selector of selectors) {
          const candidate = document.querySelector<HTMLElement>(selector);
          if (!candidate) continue;
          target = candidate.closest<HTMLElement>(
            "[data-testid='issue-body'],[data-testid='issue-body-viewer'],#issue-body-viewer,.js-issue-body",
          ) || candidate;
          break;
        }
        if (!target) return { selector: null, fallback: true, textSample: "" };
        target.setAttribute(attr, targetName);
        target.scrollIntoView({ block: "center", inline: "nearest" });
        const back = document.getElementById("demo-spot-back");
        const box = document.getElementById("demo-spot");
        if (back && box) {
          const r = target.getBoundingClientRect();
          const pad = 8;
          box.style.top = `${Math.max(0, r.top - pad)}px`;
          box.style.left = `${Math.max(0, r.left - pad)}px`;
          box.style.width = `${Math.max(1, r.width + pad * 2)}px`;
          box.style.height = `${Math.max(1, r.height + pad * 2)}px`;
          back.classList.add("show");
          box.classList.add("show");
        }
        return {
          selector: `[${attr}="${targetName}"]`,
          fallback: false,
          textSample: textOf(target).slice(0, 400),
        };
      },
      { attr: "data-kitsoki-demo-target", targetName: name },
    )
    .catch(() => ({ selector: null, fallback: true, textSample: "" }));
}

async function annotatedGithubCommentBeat(
  page: Page,
  caption: Beat,
  needles: string[],
  targetName: string,
  title: string,
  sub: string,
  holdMs: number,
): Promise<{ selector: string | null; fallback: boolean; textSample: string }> {
  const shown = await markGithubCommentByText(page, targetName, needles);
  await page.evaluate(
    ({ tag, title: eventTitle, needles: eventNeedles, shownSelector, fallback, textSample }) => {
      const rrweb = (window as unknown as { rrweb?: { record?: { addCustomEvent?: (tag: string, payload: unknown) => void } } }).rrweb;
      rrweb?.record?.addCustomEvent?.(tag, {
        title: eventTitle,
        needles: eventNeedles,
        shownSelector,
        fallback,
        textSample,
      });
    },
    {
      tag: ANNOTATION_TAG,
      title,
      needles,
      shownSelector: shown.selector,
      fallback: shown.fallback,
      textSample: shown.textSample,
    },
  );
  await caption(title, sub, holdMs);
  return shown;
}

async function mentionBreathBeat(
  textBreath: TextBreath,
  selector: string | null,
  context: string,
): Promise<void> {
  if (!selector) return;
  await textBreath(selector, {
    pattern: "@kitsoki",
    eventTag: MENTION_BREATH_TAG,
    context,
  });
}

async function zoomBeat(
  page: Page,
  caption: Beat,
  zoom: ReadableZoom,
  selector: string | null,
  title: string,
  sub: string,
  holdMs: number,
): Promise<void> {
  const fullGithubComment = /bug report|requester comment|App response|App comment/i.test(title);
  const zoomResult = selector
    ? await zoom(selector, {
        title,
        fontSize: selector.includes("api-json") ? 17 : fullGithubComment ? 17 : 20,
        minScale: fullGithubComment ? 1.05 : undefined,
      })
    : { shown: false, animatedFromSource: false };
  await page.evaluate(
    ({ tag, title: eventTitle, selector: eventSelector, result }) => {
      const rrweb = (window as unknown as { rrweb?: { record?: { addCustomEvent?: (tag: string, payload: unknown) => void } } }).rrweb;
      rrweb?.record?.addCustomEvent?.(tag, {
        title: eventTitle,
        selector: eventSelector,
        shown: result.shown,
        animatedFromSource: result.animatedFromSource,
        sourceMatched: result.sourceMatched,
        selectedBeforeExpand: result.selectedBeforeExpand,
        scale: result.scale,
        sourceSelector: result.sourceSelector,
        sourceText: result.sourceText,
        resolvedSourceKind: result.resolvedSourceKind,
        sourceRect: result.sourceRect,
        finalRect: result.finalRect,
        styleSignature: result.styleSignature,
      });
    },
    { tag: ZOOM_TAG, title, selector, result: zoomResult },
  );
  await caption(title, sub, holdMs);
  const returnResult = selector ? await zoom(null) : { shown: false, animatedFromSource: false };
  await page.evaluate(
    ({ tag, title: eventTitle, selector: eventSelector, result }) => {
      const rrweb = (window as unknown as { rrweb?: { record?: { addCustomEvent?: (tag: string, payload: unknown) => void } } }).rrweb;
      rrweb?.record?.addCustomEvent?.(tag, {
        title: eventTitle,
        selector: eventSelector,
        returnedToSource: result.returnedToSource,
        sourceMatched: result.sourceMatched,
        selectedBeforeExpand: result.selectedBeforeExpand,
        scale: result.scale,
        sourceSelector: result.sourceSelector,
        sourceText: result.sourceText,
        resolvedSourceKind: result.resolvedSourceKind,
        sourceRect: result.sourceRect,
        finalRect: result.finalRect,
      });
    },
    { tag: ZOOM_RETURN_TAG, title, selector, result: returnResult },
  );
}

async function tourGithubThread(page: Page, step: CaptureStep, caption: Beat, spotlight: Spotlight, zoom: ReadableZoom, textBreath: TextBreath): Promise<void> {
  const title = await markFirstSelector(page, "issue-title", [
    "[data-testid='issue-title']",
    "bdi.js-issue-title",
    ".js-issue-title",
    ".gh-header-title",
    "h1",
  ]);
  await mentionBreathBeat(textBreath, title, `${step.id}:title`);
  await annotatedBeat(page, caption, spotlight, title, step.title, "Start where the requester worked: the live GitHub thread.", 2300);
  await dwell(page, 1500);

  const issueComment = await markFirstGithubComment(page, "issue-opening-comment");
  if (issueComment.selector) {
    await mentionBreathBeat(textBreath, issueComment.selector, `${step.id}:opening-comment`);
    await zoomBeat(
      page,
      caption,
      zoom,
      issueComment.selector,
      "Read the bug report",
      "The opening GitHub comment stays intact: avatar, username, metadata, and full issue body travel together.",
      3000,
    );
    await dwell(page, 1300);
  }

  const mentionComment = await annotatedGithubCommentBeat(
    page,
    caption,
    ["@kitsoki"],
    "request-mention",
    "Requester mentions @kitsoki",
    "The whole requester comment is selected, not only the mention token.",
    2600,
  );
  await mentionBreathBeat(textBreath, mentionComment.selector, `${step.id}:requester-comment`);
  await zoomBeat(
    page,
    caption,
    zoom,
    mentionComment.selector,
    "Read the requester comment",
    "The expanded box preserves the GitHub comment theme, author, avatar, timestamp context, and the complete text.",
    3200,
  );
  await dwell(page, 1500);

  const appComment = await annotatedGithubCommentBeat(
    page,
    caption,
    ["kitsoki-test.slothattax.me/run/"],
    "app-comment",
    "kitsoki answers on the thread",
    "The App-authenticated response is selected as a full GitHub comment box.",
    2800,
  );
  await zoomBeat(
    page,
    caption,
    zoom,
    appComment.selector,
    "Read the App response",
    "The zoomed copy makes the story, state, job id, and run URL readable while keeping GitHub chrome accurate.",
    3200,
  );
  await dwell(page, 1800);

  await annotatedTextBeat(
    page,
    caption,
    "https://kitsoki-test.slothattax.me/run/",
    "run-link",
    "User follows the run link",
    "The visible link is the proof surface the requester can open.",
    2300,
  );
  await dwell(page, 1500);
}

async function tourAppComment(page: Page, step: CaptureStep, caption: Beat, spotlight: Spotlight, zoom: ReadableZoom): Promise<void> {
  const appComment = await annotatedGithubCommentBeat(
    page,
    caption,
    ["kitsoki-test.slothattax.me/run/"],
    "app-comment-anchor",
    step.title,
    "The URL opens directly on the full App response comment box.",
    2600,
  );
  await zoomBeat(
    page,
    caption,
    zoom,
    appComment.selector,
    "Read the App response",
    "The zoomed copy makes the story, state, job id, and run URL readable while keeping GitHub chrome accurate.",
    3200,
  );
  await dwell(page, 1800);

  await annotatedTextBeat(
    page,
    caption,
    "https://kitsoki-test.slothattax.me/run/",
    "app-run-link",
    "Run link is the user's next click",
    "The visible link is the proof surface the requester can open.",
    2300,
  );
  await dwell(page, 1500);
}



async function tourRunPage(page: Page, step: CaptureStep, caption: Beat, spotlight: Spotlight, zoom: ReadableZoom): Promise<void> {
  const heading = await markFirstSelector(page, "run-heading", ["h1", "main h2", "body"]);
  await annotatedBeat(
    page,
    caption,
    spotlight,
    heading,
    step.title,
    "The hosted page proves the VM-backed job exists and is readable.",
    2600,
  );
  await dwell(page, 1600);

  await annotatedTextBeat(
    page,
    caption,
    "state",
    "run-state",
    "Read the job state",
    "The page exposes the route, state, source, and run URL a reviewer needs.",
    2600,
  );
  await zoomBeat(
    page,
    caption,
    zoom,
    "[data-kitsoki-demo-target=\"run-state\"]",
    "Readable job state",
    "The run page state is enlarged so the proof status is legible in the deck.",
    2600,
  );
  await page.mouse.wheel(0, 360).catch(() => undefined);
  await dwell(page, 1800);

  await annotatedTextBeat(
    page,
    caption,
    "github.com/bsacrobatix/Kitsoki",
    "run-source",
    "Trace it back to GitHub",
    "The hosted view ties the job back to the original issue or PR.",
    2300,
  );
  await zoomBeat(
    page,
    caption,
    zoom,
    "[data-kitsoki-demo-target=\"run-source\"]",
    "Readable source link",
    "The source link ties the hosted run back to the exact GitHub issue or PR.",
    2600,
  );
  await dwell(page, 1600);
}

async function tourRunAPI(page: Page, step: CaptureStep, caption: Beat, spotlight: Spotlight, zoom: ReadableZoom): Promise<void> {
  const pre = await markFirstSelector(page, "api-json", ["pre", "body"]);
  await annotatedBeat(
    page,
    caption,
    spotlight,
    pre,
    step.title,
    "The same proof is machine-readable for verification and automation.",
    2600,
  );
  await zoomBeat(
    page,
    caption,
    zoom,
    pre,
    "Readable API payload",
    "The JSON proof is enlarged so the job id, state, source URL, and run URL are reviewable.",
    3600,
  );
  await dwell(page, 1600);

  await annotatedTextBeat(
    page,
    caption,
    "\"state\"",
    "api-state",
    "Verifier reads the same state",
    "This prevents the deck from being only a pretty screenshot.",
    2600,
  );
  await page.mouse.wheel(0, 320).catch(() => undefined);
  await dwell(page, 1800);
}

async function runStepTour(page: Page, step: CaptureStep, caption: Beat, spotlight: Spotlight, zoom: ReadableZoom, textBreath: TextBreath): Promise<void> {
  switch (step.id) {
    case "github-thread":
      await tourGithubThread(page, step, caption, spotlight, zoom, textBreath);
      break;
    case "app-comment":
      await tourAppComment(page, step, caption, spotlight, zoom);
      break;
    case "run-page":
      await tourRunPage(page, step, caption, spotlight, zoom);
      break;
    case "run-api":
      await tourRunAPI(page, step, caption, spotlight, zoom);
      break;
    default:
      await caption(step.title, step.caption || step.url, step.dwellMs ?? 5000);
      break;
  }
  await spotlight(null);
  await zoom(null);
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
  zoom: ReadableZoom,
  textBreath: TextBreath,
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
  await runStepTour(page, step, caption, spotlight, zoom, textBreath);
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
  // Capture GitHub in its dark theme so the evidence matches the dark deck.
  // GitHub honors prefers-color-scheme for anonymous viewing, so emulating a
  // dark color scheme renders real dark-theme comment chrome; the readable-zoom
  // then reproduces those true colors faithfully (no white pop-outs on a dark
  // deck, and no synthetic recolor).
  const context: BrowserContext = await browser.newContext({
    ...cameraContext(),
    bypassCSP: true,
    colorScheme: "dark",
  });
  const page: Page = await context.newPage();
  // Pin GitHub to its explicit dark theme via the data-color-mode attribute.
  // GitHub's anonymous dark theme is otherwise driven by @media
  // (prefers-color-scheme), which does NOT survive rrweb replay — the page would
  // revert to light at replay while a dark-captured pop-out stayed dark. The
  // attribute-keyed dark theme ([data-color-mode="dark"]) is baked into the DOM,
  // so the page and the readable-zoom clone stay dark and consistent in the deck.
  await page.addInitScript(() => {
    const pin = () => {
      const html = document.documentElement;
      if (!html) return;
      html.setAttribute("data-color-mode", "dark");
      html.setAttribute("data-dark-theme", "dark");
      html.setAttribute("data-light-theme", "dark");
    };
    pin();
    document.addEventListener("DOMContentLoaded", pin);
  });
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
        await page.getByText(step.waitForText, { exact: false }).first().waitFor({ state: "attached", timeout: 30000 });
      }
      if (/github\.com/.test(step.url)) {
        // Re-assert the dark theme after GitHub's boot JS settles, in case it
        // restored the SSR auto attribute.
        diag.mark(`step ${step.id}: pin-dark-theme`);
        await page.evaluate(() => {
          const html = document.documentElement;
          html.setAttribute("data-color-mode", "dark");
          html.setAttribute("data-dark-theme", "dark");
          html.setAttribute("data-light-theme", "dark");
        });
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
      const zoom = await tryMakeReadableZoom(page);
      const textBreath = await tryMakeTextBreath(page);
      diag.mark(`step ${step.id}: rrweb-tour`);
      await captureRrwebStep(page, artifactDir, idx, step, caption, spotlight, zoom, textBreath);
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
