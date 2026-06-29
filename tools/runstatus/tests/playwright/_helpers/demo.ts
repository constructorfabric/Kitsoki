/**
 * Deterministic demo-recording helpers shared by the narrated UI-demo specs
 * (diagram-showcase, tour-video, …). These exist because recording a kitsoki
 * web demo has a handful of non-obvious traps that cost real time to rediscover;
 * each helper bakes the fix in so a new demo is deterministic by construction.
 *
 * The traps, and the helper that neutralises each:
 *
 *  - recordVideo captures from PAGE CREATION, so off-camera setup (home screen,
 *    the new-session click, live RPC room-flips) flashes by at the head of the
 *    video. → installCurtain / liftCurtain: a full-screen title card that hides
 *    everything until the scene is staged.
 *  - A caption/overlay that isn't pointer-events:none silently INTERCEPTS clicks
 *    on the UI beneath it (an opaque div over the tab bar = every tab click
 *    times out). → makeCaption and the curtain are both pointer-events:none.
 *  - The Claude Code harness suppresses Playwright's stdout, so a failing
 *    recording is otherwise undiagnosable. → captureDiagnostics writes the
 *    failure + a step breadcrumb to <artifactDir>/ERROR.txt; read it + the
 *    NN-*.png screenshots after the run.
 *  - Dwell timing must scale with WEB_CHAT_PACE so the same spec validates fast
 *    (PACE=0) and records at watch-speed (PACE=1). → dwell.
 *
 * These compose with the video lifecycle helpers in _helpers/server.ts —
 * `prepareVideoDir` (beforeAll) + `saveAndRemuxVideo` (after context.close) —
 * which are the canonical save path (a plain copy from the video dir picks a
 * stale file and skips the remux that fixes VP8 webm duration metadata). See
 * .agents/skills/kitsoki-ui-demo/SKILL.md → "Deterministic recording".
 */
import { type Page } from "@playwright/test";
import path from "path";
import fs from "fs";
import { PACE } from "./server.js";

/** Fixed MacBook-ish recording resolution — keep every demo identical. */
export const DEMO_VIEWPORT = { width: 1600, height: 900 } as const;

/** Dwell scaled by WEB_CHAT_PACE (0 = fast validation, 1 = watch-speed). */
export function dwell(page: Page, ms: number): Promise<void> {
  return page.waitForTimeout(Math.round(ms * PACE));
}

/**
 * Install the recording curtain: a full-screen title card injected on EVERY
 * load (it survives page.reload via sessionStorage) so the camera shows nothing
 * but the title through the entire off-camera setup. pointer-events:none, so
 * Playwright's clicks during setup still reach the elements beneath it.
 *
 * Call BEFORE the first page.goto. Lift it with liftCurtain once the scene is
 * fully staged. After lifting, the curtain never returns (the sessionStorage
 * flag persists across later reloads), so an on-camera reload stays visible.
 */
export async function installCurtain(page: Page, title: string): Promise<void> {
  await page.addInitScript((t: string) => {
    if (sessionStorage.getItem("kd-curtain-lifted")) return;
    const add = (): void => {
      if (document.getElementById("kd-curtain")) return;
      const c = document.createElement("div");
      c.id = "kd-curtain";
      c.style.cssText =
        "position:fixed;inset:0;z-index:2147483647;background:#070d1a;display:flex;" +
        "align-items:center;justify-content:center;pointer-events:none;color:#e2e8f0;" +
        "font:700 34px ui-sans-serif,system-ui,sans-serif;letter-spacing:.02em;transition:opacity .6s";
      c.textContent = t;
      (document.body ?? document.documentElement).appendChild(c);
    };
    if (document.body) add();
    else document.addEventListener("DOMContentLoaded", add);
  }, title);
}

/** Fade the curtain out and dwell briefly so the fade completes on camera. */
export async function liftCurtain(page: Page): Promise<void> {
  await page.evaluate(() => {
    sessionStorage.setItem("kd-curtain-lifted", "1");
    const c = document.getElementById("kd-curtain");
    if (c) {
      c.style.opacity = "0";
      setTimeout(() => c.remove(), 600);
    }
  });
  await dwell(page, 800);
}

/** A narration beat: set the caption (fade-in) then hold for `holdMs`. */
export type Beat = (title: string, sub?: string, holdMs?: number) => Promise<void>;

/**
 * Install the caption banner and return a `beat(title, sub, holdMs)` narrator.
 * The banner is fixed top-centre, pointer-events:none (so it never intercepts
 * clicks). Re-install after any page.reload — injected DOM does not survive it.
 */
export async function makeCaption(page: Page, defaultHoldMs = 5000): Promise<Beat> {
  await page.addStyleTag({
    content: `#demo-caption{position:fixed;top:18px;left:50%;transform:translateX(-50%);` +
      `z-index:99999;background:rgba(2,6,23,.94);color:#e2e8f0;border:1px solid #334155;` +
      `border-left:4px solid #fbbf24;border-radius:10px;padding:14px 22px;max-width:66%;` +
      `font:700 21px/1.35 ui-sans-serif,system-ui,sans-serif;box-shadow:0 10px 34px rgba(0,0,0,.55);` +
      `opacity:0;transition:opacity .4s;pointer-events:none}` +
      `#demo-caption.show{opacity:1}` +
      `#demo-caption .sub{display:block;margin-top:6px;font-weight:400;font-size:15px;color:#94a3b8}`,
  });
  await page.evaluate(() => {
    const el = document.createElement("div");
    el.id = "demo-caption";
    document.body.appendChild(el);
  });
  return async (title, sub = "", holdMs = defaultHoldMs) => {
    await page.evaluate(
      ([t, s]) => {
        const el = document.getElementById("demo-caption");
        if (!el) return;
        el.classList.remove("show");
        el.innerHTML = `${t}${s ? `<span class="sub">${s}</span>` : ""}`;
        requestAnimationFrame(() => el.classList.add("show"));
      },
      [title, sub],
    );
    await dwell(page, holdMs);
  };
}

/** Move/clear the spotlight box. Pass a selector to frame it, null to hide. */
export type Spotlight = (selector: string | null) => Promise<void>;

/**
 * Install a portable, non-obscuring spotlight outline and return a `spotlight(sel)`
 * mover. This is the narration primitive for driving a site OTHER than kitsoki:
 * the kitsoki tour overlay (window.__startTourWithSteps, [data-testid=tour-*])
 * only exists inside the kitsoki SPA, so an external page (e.g. a GitHub issue)
 * is narrated with makeCaption + this, both of which inject plain DOM and work
 * on ANY page. The outline frames the target element without tinting or blurring
 * the page beneath it; both helper nodes are pointer-events:none so they never
 * intercept clicks. Pass null to hide the box.
 */
export async function makeSpotlight(page: Page): Promise<Spotlight> {
  await page.addStyleTag({
    // Position is set INSTANTLY (only opacity fades) so a screenshot taken right
    // after a move can never catch the box mid-animation — the per-scene PNGs the
    // QA gate consumes must be correct at any WEB_CHAT_PACE, not just watch-speed.
    content:
      `#demo-spot-back{position:fixed;inset:0;z-index:99990;pointer-events:none;` +
      `background:transparent;opacity:0;transition:none}` +
      `#demo-spot-back.show{opacity:0}` +
      `#demo-spot{position:fixed;z-index:99992;pointer-events:none;border-radius:10px;` +
      `border:3px solid #fbbf24;box-shadow:0 0 0 3px rgba(251,191,36,.28),0 0 22px 4px rgba(251,191,36,.6);` +
      `opacity:0;transition:opacity .3s}` +
      `#demo-spot.show{opacity:1}`,
  });
  await page.evaluate(() => {
    for (const id of ["demo-spot-back", "demo-spot"]) {
      const el = document.createElement("div");
      el.id = id;
      document.body.appendChild(el);
    }
  });
  return async (selector) => {
    // Scroll the target into view, measure, and place the box in ONE evaluate so
    // the rect is always read post-scroll (no spec-side scroll race).
    await page.evaluate((sel) => {
      const back = document.getElementById("demo-spot-back");
      const box = document.getElementById("demo-spot");
      if (!back || !box) return;
      if (!sel) {
        back.classList.remove("show");
        box.classList.remove("show");
        return;
      }
      const t = document.querySelector(sel);
      if (!t) return;
      t.scrollIntoView({ block: "center", inline: "nearest" });
      const r = t.getBoundingClientRect();
      const pad = 8;
      box.style.top = `${r.top - pad}px`;
      box.style.left = `${r.left - pad}px`;
      box.style.width = `${r.width + pad * 2}px`;
      box.style.height = `${r.height + pad * 2}px`;
      back.classList.add("show");
      box.classList.add("show");
    }, selector);
    // Fixed settle (NOT PACE-scaled) so the opacity fade + scroll always land
    // before the next screenshot, in both validation and watch-speed runs.
    await page.waitForTimeout(300);
  };
}

export type TextBreathOptions = {
  pattern?: string;
  className?: string;
  eventTag?: string;
  context?: string;
};

export type TextBreathResult = {
  count: number;
  texts: string[];
};

export type TextBreath = (rootSelector?: string | null, options?: TextBreathOptions) => Promise<TextBreathResult>;

/**
 * Install a capture-only breathing emphasis helper and return `breath(root, opts)`.
 * It wraps matched text occurrences inside the selected root with animated spans
 * that briefly grow/bold/glow, then settle back down. This is for drawing the
 * viewer's eye to a real mention/keyword without replacing the source content.
 */
export async function makeTextBreath(page: Page): Promise<TextBreath> {
  await page.addStyleTag({
    content:
      `.kitsoki-text-breath{display:inline-block;position:relative;z-index:1;padding:.02em .18em;margin:0 .02em;` +
      `border-radius:.24em;border:1px solid rgba(251,191,36,.3);background-color:rgba(251,191,36,.08);` +
      `font-weight:normal!important;transform:scale(1);transform-origin:center;` +
      `transition:transform .38s cubic-bezier(.2,.78,.25,1),border-color .38s cubic-bezier(.2,.78,.25,1),` +
      `background-color .38s cubic-bezier(.2,.78,.25,1),font-weight .38s cubic-bezier(.2,.78,.25,1)}` +
      `.kitsoki-text-breath.kitsoki-text-breath--big{transform:scale(1.28);font-weight:900!important;` +
      `border-color:rgba(251,191,36,.9);background-color:rgba(251,191,36,.2)}` +
      `.kitsoki-text-breath.kitsoki-text-breath--small{transform:scale(.94);font-weight:normal!important;` +
      `border-color:rgba(251,191,36,.15);background-color:rgba(251,191,36,.04)}`,
  });
  return async (rootSelector = "body", options = {}) => {
    const result = await page.evaluate(
      ({ rootSel, opts }) => {
        const pattern = new RegExp(opts.pattern || "@kitsoki", "gi");
        const className = opts.className || "kitsoki-text-breath";
        const root = rootSel ? document.querySelector<HTMLElement>(rootSel) : document.body;
        if (!root) return { count: 0, texts: [] };
        const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT, {
          acceptNode(node) {
            const text = node.textContent || "";
            pattern.lastIndex = 0;
            if (!pattern.test(text)) return NodeFilter.FILTER_REJECT;
            const parent = node.parentElement;
            if (!parent) return NodeFilter.FILTER_REJECT;
            if (parent.closest("#demo-caption,#demo-spot,#demo-readable-zoom,.kitsoki-text-breath")) return NodeFilter.FILTER_REJECT;
            const style = window.getComputedStyle(parent);
            if (style.display === "none" || style.visibility === "hidden") return NodeFilter.FILTER_REJECT;
            return NodeFilter.FILTER_ACCEPT;
          },
        });
        const nodes: Text[] = [];
        while (walker.nextNode()) nodes.push(walker.currentNode as Text);
        for (const node of nodes) {
          const text = node.textContent || "";
          pattern.lastIndex = 0;
          const fragment = document.createDocumentFragment();
          let last = 0;
          let match: RegExpExecArray | null;
          while ((match = pattern.exec(text)) !== null) {
            if (match.index > last) fragment.appendChild(document.createTextNode(text.slice(last, match.index)));
            const span = document.createElement("span");
            span.className = className;
            span.dataset.kitsokiTextBreath = "true";
            span.textContent = match[0];
            fragment.appendChild(span);
            last = match.index + match[0].length;
          }
          if (last < text.length) fragment.appendChild(document.createTextNode(text.slice(last)));
          node.parentNode?.replaceChild(fragment, node);
        }
        const mentions = Array.from(root.querySelectorAll<HTMLElement>(`.${className}`));
        mentions[0]?.scrollIntoView({ block: "center", inline: "nearest" });
        const payload = {
          context: opts.context || "",
          pattern: opts.pattern || "@kitsoki",
          count: mentions.length,
          texts: mentions.map((el) => el.textContent || "").slice(0, 12),
        };
        const rrweb = (window as unknown as { rrweb?: { record?: { addCustomEvent?: (tag: string, payload: unknown) => void } } }).rrweb;
        const eventTag = opts.eventTag || "kitsoki.text_breath";
        const emit = (phase: "start" | "peak" | "small" | "settle") => {
          rrweb?.record?.addCustomEvent?.(eventTag, { ...payload, phase });
        };
        emit("start");
        if (mentions.length > 0) {
          requestAnimationFrame(() => {
            for (const mention of mentions) {
              mention.classList.add(`${className}--big`);
              mention.classList.remove(`${className}--small`);
            }
            emit("peak");
          });
          window.setTimeout(() => {
            for (const mention of mentions) {
              mention.classList.remove(`${className}--big`);
              mention.classList.add(`${className}--small`);
            }
            emit("small");
          }, 650);
          window.setTimeout(() => {
            for (const mention of mentions) mention.classList.remove(`${className}--small`);
            emit("settle");
          }, 1180);
        }
        return { count: payload.count, texts: payload.texts };
      },
      { rootSel: rootSelector, opts: options },
    );
    await page.waitForTimeout(350);
    return result;
  };
}

export type ReadableZoomOptions = {
  title?: string;
  maxWidth?: string;
  maxHeight?: string;
  fontSize?: number;
  minScale?: number;
  selectHoldMs?: number;
};

export type ReadableZoomResult = {
  phase?: "open" | "return" | "hidden";
  shown: boolean;
  animatedFromSource: boolean;
  sourceMatched?: boolean;
  selectedBeforeExpand?: boolean;
  returnedToSource?: boolean;
  scale?: number;
  sourceSelector?: string;
  sourceText?: string;
  resolvedSourceKind?: string;
  sourceRect?: { top: number; left: number; width: number; height: number };
  finalRect?: { top: number; left: number; width: number; height: number };
  styleSignature?: {
    tag: string;
    resolvedSourceKind: string;
    display: string;
    fontFamily: string;
    fontSize: string;
    color: string;
    targetBackgroundColor: string;
    rawBackgroundColor: string;
    backgroundColor: string;
    pageBackgroundColor: string;
    themeAdjusted: boolean;
  };
};

export type ReadableZoom = (selector: string | null, options?: ReadableZoomOptions) => Promise<ReadableZoomResult>;

/**
 * Install a capture-only readable zoom overlay and return a `zoom(selector)`
 * mover. The helper first frames the real target with a glowing selected-state
 * border, then clones the target with computed visual styles into a pop-out
 * that starts at the target's exact viewport bounds, expands into focus, and
 * returns to the source rectangle when cleared. rrweb captures each step as
 * ordinary DOM.
 *
 * Use this when the real evidence is a dense comment, code block, JSON payload,
 * or metadata panel that is authentic but too small for a narrated demo. The
 * source page is not destructively modified: the original target remains on the
 * page and can still be spotlighted separately.
 */
export async function makeReadableZoom(page: Page): Promise<ReadableZoom> {
  await page.addStyleTag({
    content:
      `#demo-readable-back{position:fixed;inset:0;z-index:99996;pointer-events:none;` +
      `background:transparent;backdrop-filter:none;-webkit-backdrop-filter:none;opacity:0;transition:none}` +
      `#demo-readable-back.show{opacity:0}` +
      `#demo-readable-select{position:fixed;z-index:99998;pointer-events:none;box-sizing:border-box;` +
      `border:3px solid #fbbf24;border-radius:var(--rz-source-radius,10px);opacity:0;` +
      `box-shadow:0 0 0 3px rgba(251,191,36,.25),0 0 30px 8px rgba(251,191,36,.72);` +
      `transition:top .18s ease,left .18s ease,width .18s ease,height .18s ease,opacity .16s,box-shadow .5s}` +
      `#demo-readable-select.show{opacity:1;box-shadow:0 0 0 4px rgba(251,191,36,.32),0 0 44px 12px rgba(251,191,36,.9)}` +
      `#demo-readable-zoom{position:fixed;z-index:99999;box-sizing:border-box;overflow:hidden;` +
      `background:var(--rz-bg,#fff);color:var(--rz-color,#0f172a);border:3px solid #fbbf24;` +
      `border-radius:var(--rz-source-radius,10px);box-shadow:0 0 0 3px rgba(251,191,36,.22),` +
      `0 32px 96px rgba(2,6,23,.54),0 0 50px rgba(251,191,36,.62);` +
      `padding:0;opacity:0;pointer-events:none;transform-origin:top left;` +
      `transition:top .68s cubic-bezier(.18,.92,.18,1),left .68s cubic-bezier(.18,.92,.18,1),` +
      `width .68s cubic-bezier(.18,.92,.18,1),height .68s cubic-bezier(.18,.92,.18,1),` +
      `border-radius .68s cubic-bezier(.18,.92,.18,1),opacity .2s,box-shadow .68s}` +
      `#demo-readable-zoom.show{opacity:1}` +
      `#demo-readable-zoom.returning{transition-duration:.46s}` +
      `#demo-readable-zoom .rz-stage{width:100%;height:100%;box-sizing:border-box;overflow:auto;` +
      `overscroll-behavior:contain;padding:0;transition:padding .68s cubic-bezier(.18,.92,.18,1)}` +
      `#demo-readable-zoom .rz-source-copy{box-sizing:border-box!important;margin:0!important;` +
      `max-width:none!important;min-width:100%;width:100%;min-height:100%;transform-origin:top left;` +
      `transition:font-size .68s cubic-bezier(.18,.92,.18,1),line-height .68s cubic-bezier(.18,.92,.18,1),` +
      `padding .68s cubic-bezier(.18,.92,.18,1)}` +
      `#demo-readable-zoom *{box-sizing:border-box}`,
  });
  await page.evaluate(() => {
    if (!document.getElementById("demo-readable-back")) {
      const back = document.createElement("div");
      back.id = "demo-readable-back";
      back.setAttribute("aria-hidden", "true");
      document.body.appendChild(back);
    }
    if (!document.getElementById("demo-readable-select")) {
      const select = document.createElement("div");
      select.id = "demo-readable-select";
      select.setAttribute("aria-hidden", "true");
      document.body.appendChild(select);
    }
    if (!document.getElementById("demo-readable-zoom")) {
      const el = document.createElement("div");
      el.id = "demo-readable-zoom";
      el.setAttribute("aria-hidden", "true");
      el.innerHTML = `<div class="rz-stage"></div>`;
      document.body.appendChild(el);
    }
  });
  return async (selector, options = {}) => {
    const result = await page.evaluate(
      ({ sel, opts }) => {
        type ZoomState = {
          sourceRect: { top: number; left: number; width: number; height: number };
          finalRect: { top: number; left: number; width: number; height: number };
          sourceRadius: string;
          sourceMatched: boolean;
          selectedBeforeExpand: boolean;
          scale: number;
          sourceSelector?: string;
          sourceText?: string;
          resolvedSourceKind?: string;
        };
        const w = window as unknown as { __demoReadableZoomState?: ZoomState };
        const back = document.getElementById("demo-readable-back");
        const panel = document.getElementById("demo-readable-zoom");
        const select = document.getElementById("demo-readable-select");
        const stage = panel?.querySelector<HTMLElement>(".rz-stage");
        const hidden = {
          phase: "hidden" as const,
          shown: false,
          animatedFromSource: false,
          sourceMatched: false,
          selectedBeforeExpand: false,
          returnedToSource: false,
        };
        if (!back || !panel || !select || !stage) return hidden;
        if (!sel) {
          const state = w.__demoReadableZoomState;
          if (!state) {
            panel.classList.remove("show", "returning");
            select.classList.remove("show");
            back.classList.remove("show");
            stage.replaceChildren();
            return hidden;
          }
          applyFontScale(stage, 1);
          panel.classList.add("returning");
          panel.style.top = `${state.sourceRect.top}px`;
          panel.style.left = `${state.sourceRect.left}px`;
          panel.style.width = `${state.sourceRect.width}px`;
          panel.style.height = `${state.sourceRect.height}px`;
          panel.style.borderRadius = state.sourceRadius;
          stage.style.padding = "0px";
          window.setTimeout(() => {
            panel.classList.remove("show", "returning");
            select.classList.remove("show");
            back.classList.remove("show");
            stage.replaceChildren();
            delete w.__demoReadableZoomState;
          }, 470);
          return {
            phase: "return" as const,
            shown: false,
            animatedFromSource: false,
            sourceMatched: state.sourceMatched,
            selectedBeforeExpand: state.selectedBeforeExpand,
            returnedToSource: true,
            scale: state.scale,
            sourceSelector: state.sourceSelector,
            sourceText: state.sourceText,
            resolvedSourceKind: state.resolvedSourceKind,
            sourceRect: state.sourceRect,
            finalRect: state.finalRect,
          };
        }
        const target = document.querySelector<HTMLElement>(sel);
        if (!target) {
          panel.classList.remove("show", "returning");
          select.classList.remove("show");
          back.classList.remove("show");
          stage.replaceChildren();
          return hidden;
        }
        target.scrollIntoView({ block: "center", inline: "nearest" });
        const source = resolveZoomSource(target, sel);
        const rect = source.rect;
        if (rect.width <= 0 || rect.height <= 0) return hidden;
        const style = window.getComputedStyle(source.styleElement);
        const targetStyle = window.getComputedStyle(target);
        const clone = source.clone;
        clone.classList.add("rz-source-copy");
        clone.removeAttribute("id");
        clone.querySelectorAll("[id]").forEach((el) => el.removeAttribute("id"));
        stage.replaceChildren(clone);
        const vw = window.innerWidth;
        const vh = window.innerHeight;
        const maxWidth = Math.min(parseCSSSize(opts.maxWidth, vw, vw - 72), vw - 72);
        const maxHeight = Math.min(parseCSSSize(opts.maxHeight, vh, vh - 72), vh - 72);
        const pageBackground = pageBackgroundColor();
        const sourceRadius = normalizeRadius(style.borderRadius);
        const rawSourceColor = source.color || targetStyle.color || style.color || "#0f172a";
        const rawSourceBackground = source.background || readableBackground(source.backgroundElement, rawSourceColor, pageBackground);
        // Faithful reproduction: the zoom is a magnified copy of the real
        // element, so it must keep the element's own rendered colors. The clone
        // already carries every descendant's computed background/color/border
        // (copyTreeStyles). We must NOT coerce the panel to a flat #fff or a
        // forced-dark #0d1117 surface — that flattens GitHub's comment chrome
        // (gray header, white body, syntax/badge colors) and the kitsoki page's
        // real theme. The panel background only shows through scroll/padding
        // gaps, so it tracks the element's true resolved background.
        const themeAdjusted = false;
        const sourceBackground = rawSourceBackground;
        const sourceColor = rawSourceColor;
        const baseFont = parseFloat(targetStyle.fontSize || style.fontSize) || 16;
        const requestedFont = opts.fontSize || Math.min(23, Math.max(18, baseFont * 1.28));
        const minScale = Math.max(1, opts.minScale ?? 1.18);
        const requestedScale = Math.max(minScale, Math.min(1.8, requestedFont / baseFont));
        const viewportScale = Math.max(1, Math.min(maxWidth / rect.width, maxHeight / rect.height));
        const scale = Math.round(Math.max(1.05, Math.min(requestedScale, viewportScale)) * 100) / 100;
        const finalWidth = Math.round(rect.width * scale);
        const finalHeight = Math.round(rect.height * scale);
        const finalLeft = Math.round(clamp(rect.left + rect.width / 2 - finalWidth / 2, 36, vw - finalWidth - 36));
        const finalTop = Math.round(clamp(rect.top + rect.height / 2 - finalHeight / 2, 36, vh - finalHeight - 36));
        const pad = 6;
        select.style.setProperty("--rz-source-radius", sourceRadius);
        select.style.top = `${rect.top - pad}px`;
        select.style.left = `${rect.left - pad}px`;
        select.style.width = `${rect.width + pad * 2}px`;
        select.style.height = `${rect.height + pad * 2}px`;
        panel.style.setProperty("--rz-bg", sourceBackground);
        panel.style.setProperty("--rz-color", sourceColor);
        panel.style.setProperty("--rz-source-radius", sourceRadius);
        panel.classList.remove("returning");
        panel.style.borderRadius = sourceRadius;
        panel.style.top = `${rect.top}px`;
        panel.style.left = `${rect.left}px`;
        panel.style.width = `${rect.width}px`;
        panel.style.height = `${rect.height}px`;
        stage.style.padding = "0px";
        applyFontScale(stage, 1);
        back.classList.add("show");
        select.classList.add("show");
        panel.classList.add("show");
        panel.getBoundingClientRect();
        const selectHoldMs = Math.max(140, opts.selectHoldMs ?? 280);
        window.setTimeout(() => {
          requestAnimationFrame(() => {
            panel.style.top = `${finalTop}px`;
            panel.style.left = `${finalLeft}px`;
            panel.style.width = `${finalWidth}px`;
            panel.style.height = `${finalHeight}px`;
            panel.style.borderRadius = "14px";
            applyFontScale(stage, scale);
          });
        }, selectHoldMs);
        const sourceRect = compactRect(rect);
        const finalRect = { top: finalTop, left: finalLeft, width: finalWidth, height: finalHeight };
        w.__demoReadableZoomState = {
          sourceRect,
          finalRect,
          sourceRadius,
          sourceMatched: true,
          selectedBeforeExpand: true,
          scale,
          sourceSelector: source.selector,
          sourceText: source.text,
          resolvedSourceKind: source.kind,
        };
        return {
          phase: "open" as const,
          shown: true,
          animatedFromSource: true,
          sourceMatched: true,
          selectedBeforeExpand: true,
          returnedToSource: false,
          scale,
          sourceSelector: source.selector,
          sourceText: source.text,
          resolvedSourceKind: source.kind,
          sourceRect,
          finalRect,
          styleSignature: {
            tag: source.tag,
            resolvedSourceKind: source.kind,
            display: style.display,
            fontFamily: style.fontFamily,
            fontSize: targetStyle.fontSize || style.fontSize,
            color: sourceColor,
            targetBackgroundColor: targetStyle.backgroundColor,
            rawBackgroundColor: rawSourceBackground,
            backgroundColor: sourceBackground,
            pageBackgroundColor: pageBackground,
            themeAdjusted,
          },
        };

        function resolveZoomSource(targetEl: HTMLElement, selector: string): {
          clone: HTMLElement;
          rect: { top: number; left: number; width: number; height: number };
          styleElement: HTMLElement;
          backgroundElement: HTMLElement;
          color: string;
          background: string;
          kind: string;
          tag: string;
          text: string;
          selector: string;
        } {
          const pair = definitionPair(targetEl);
          if (pair) {
            const container = document.createElement("div");
            container.setAttribute("data-readable-zoom-kind", "definition-pair");
            const label = pair.dt.cloneNode(true) as HTMLElement;
            const value = pair.dd.cloneNode(true) as HTMLElement;
            copyTreeStyles(pair.dt, label);
            copyTreeStyles(pair.dd, value);
            label.style.marginTop = "0px";
            value.style.marginBottom = "0px";

            const parent = pair.dt.parentElement instanceof HTMLElement ? pair.dt.parentElement : targetEl;
            const parentStyle = window.getComputedStyle(parent);
            const targetPairStyle = window.getComputedStyle(targetEl);
            const color = targetPairStyle.color || parentStyle.color || "#0f172a";
            const pageBg = pageBackgroundColor();
            const background = readableBackground(parent, color, pageBg);
            container.style.display = "block";
            container.style.position = "static";
            container.style.transform = "none";
            container.style.margin = "0";
            container.style.width = "100%";
            container.style.minHeight = "100%";
            container.style.maxWidth = "none";
            container.style.maxHeight = "none";
            container.style.overflow = "visible";
            container.style.fontFamily = parentStyle.fontFamily;
            container.style.fontWeight = parentStyle.fontWeight;
            container.style.fontStyle = parentStyle.fontStyle;
            container.style.color = color;
            container.style.backgroundColor = background;
            container.dataset.rzFontSize = String(parseFloat(parentStyle.fontSize) || 16);
            if (parentStyle.lineHeight.endsWith("px")) container.dataset.rzLineHeight = String(parseFloat(parentStyle.lineHeight));
            container.append(label, value);

            return {
              clone: container,
              rect: unionRects([pair.dt, pair.dd]),
              styleElement: parent,
              backgroundElement: parent,
              color,
              background,
              kind: "definition-pair",
              tag: "definition-pair",
              text: `${textOf(pair.dt)} ${textOf(pair.dd)}`.trim(),
              selector,
            };
          }

          const clone = targetEl.cloneNode(true) as HTMLElement;
          copyTreeStyles(targetEl, clone, true);
          const targetElementStyle = window.getComputedStyle(targetEl);
          return {
            clone,
            rect: compactRect(targetEl.getBoundingClientRect()),
            styleElement: targetEl,
            backgroundElement: targetEl,
            color: targetElementStyle.color,
            background: "",
            kind: "element",
            tag: targetEl.tagName.toLowerCase(),
            text: textOf(targetEl),
            selector,
          };
        }

        function definitionPair(targetEl: HTMLElement): { dt: HTMLElement; dd: HTMLElement } | null {
          const direct = targetEl;
          const owningDd = direct.closest("dd");
          const owningDt = direct.closest("dt");
          let dt: Element | null = null;
          let dd: Element | null = null;
          if (owningDt) {
            dt = owningDt;
            dd = nextElement(dt, "dd");
          } else if (owningDd) {
            dd = owningDd;
            dt = previousElement(dd, "dt");
          }
          if (
            dt instanceof HTMLElement &&
            dd instanceof HTMLElement &&
            dt.parentElement &&
            dt.parentElement === dd.parentElement &&
            dt.parentElement.tagName.toLowerCase() === "dl"
          ) {
            return { dt, dd };
          }
          return null;
        }

        function nextElement(el: Element, tag: string): Element | null {
          let current = el.nextElementSibling;
          while (current) {
            if (current.tagName.toLowerCase() === tag) return current;
            current = current.nextElementSibling;
          }
          return null;
        }

        function previousElement(el: Element, tag: string): Element | null {
          let current = el.previousElementSibling;
          while (current) {
            if (current.tagName.toLowerCase() === tag) return current;
            current = current.previousElementSibling;
          }
          return null;
        }

        function unionRects(elements: HTMLElement[]): { top: number; left: number; width: number; height: number } {
          const rects = elements
            .map((el) => el.getBoundingClientRect())
            .filter((r) => r.width > 0 && r.height > 0);
          if (rects.length === 0) return compactRect(elements[0].getBoundingClientRect());
          const top = Math.min(...rects.map((r) => r.top));
          const left = Math.min(...rects.map((r) => r.left));
          const right = Math.max(...rects.map((r) => r.right));
          const bottom = Math.max(...rects.map((r) => r.bottom));
          return {
            top: Math.round(top),
            left: Math.round(left),
            width: Math.round(right - left),
            height: Math.round(bottom - top),
          };
        }

        function textOf(el: HTMLElement): string {
          return (el.innerText || el.textContent || "").replace(/\s+/g, " ").trim();
        }

        function copyTreeStyles(source: Element, dest: Element, isRoot = false): void {
          if (!(source instanceof HTMLElement) || !(dest instanceof HTMLElement)) return;
          const computed = window.getComputedStyle(source);
          const props = [
            "display",
            "align-items",
            "justify-content",
            "gap",
            "grid-template-columns",
            "grid-template-rows",
            "flex-direction",
            "flex-wrap",
            "font-family",
            "font-weight",
            "font-style",
            "font-variant",
            "letter-spacing",
            "text-transform",
            "text-align",
            "text-decoration-line",
            "text-decoration-color",
            "text-decoration-style",
            "text-decoration-thickness",
            "color",
            "background-color",
            "background-image",
            "background-size",
            "background-position",
            "background-repeat",
            "border-top-color",
            "border-right-color",
            "border-bottom-color",
            "border-left-color",
            "border-top-style",
            "border-right-style",
            "border-bottom-style",
            "border-left-style",
            "border-top-width",
            "border-right-width",
            "border-bottom-width",
            "border-left-width",
            "border-top-left-radius",
            "border-top-right-radius",
            "border-bottom-right-radius",
            "border-bottom-left-radius",
            "box-shadow",
            "margin-top",
            "margin-right",
            "margin-bottom",
            "margin-left",
            "width",
            "height",
            "min-width",
            "min-height",
            "max-width",
            "max-height",
            "padding-top",
            "padding-right",
            "padding-bottom",
            "padding-left",
            "white-space",
            "word-break",
            "overflow-wrap",
            "object-fit",
            "object-position",
            "vertical-align",
            "list-style-type",
            "list-style-position",
          ];
          for (const prop of props) {
            const value = computed.getPropertyValue(prop);
            if (value) dest.style.setProperty(prop, value);
          }
          const fontSize = parseFloat(computed.fontSize) || 16;
          dest.dataset.rzFontSize = String(fontSize);
          dest.style.fontSize = `${fontSize}px`;
          if (computed.lineHeight.endsWith("px")) {
            dest.dataset.rzLineHeight = String(parseFloat(computed.lineHeight));
            dest.style.lineHeight = computed.lineHeight;
          }
          for (const [side, value] of [
            ["Top", computed.paddingTop],
            ["Right", computed.paddingRight],
            ["Bottom", computed.paddingBottom],
            ["Left", computed.paddingLeft],
          ] as const) {
            if (value.endsWith("px")) dest.dataset[`rzPadding${side}`] = String(parseFloat(value));
          }
          if (!isRoot) {
            for (const [name, value] of [
              ["Width", computed.width],
              ["Height", computed.height],
              ["MinWidth", computed.minWidth],
              ["MinHeight", computed.minHeight],
              ["MaxWidth", computed.maxWidth],
              ["MaxHeight", computed.maxHeight],
            ] as const) {
              if (value.endsWith("px")) dest.dataset[`rz${name}`] = String(parseFloat(value));
            }
          }
          dest.style.animation = "none";
          dest.style.transition =
            "font-size .68s cubic-bezier(.18,.92,.18,1),line-height .68s cubic-bezier(.18,.92,.18,1)," +
            "padding .68s cubic-bezier(.18,.92,.18,1),width .68s cubic-bezier(.18,.92,.18,1)," +
            "height .68s cubic-bezier(.18,.92,.18,1),min-width .68s cubic-bezier(.18,.92,.18,1)," +
            "min-height .68s cubic-bezier(.18,.92,.18,1),max-width .68s cubic-bezier(.18,.92,.18,1)," +
            "max-height .68s cubic-bezier(.18,.92,.18,1)";
          if (isRoot) {
            dest.style.position = "static";
            dest.style.transform = "none";
            dest.style.margin = "0";
            dest.style.width = "100%";
            dest.style.minHeight = "100%";
            dest.style.maxWidth = "none";
            dest.style.maxHeight = "none";
            dest.style.overflow = "visible";
            if (computed.display === "inline") dest.style.display = "inline-block";
          }
          const sourceChildren = Array.from(source.children);
          const destChildren = Array.from(dest.children);
          for (let i = 0; i < sourceChildren.length; i += 1) {
            if (destChildren[i]) copyTreeStyles(sourceChildren[i], destChildren[i]);
          }
        }
        function applyFontScale(root: HTMLElement, scale: number): void {
          const els = [root, ...Array.from(root.querySelectorAll<HTMLElement>("[data-rz-font-size]"))];
          for (const el of els) {
            const font = Number(el.dataset.rzFontSize);
            if (Number.isFinite(font) && font > 0) el.style.fontSize = `${Math.round(font * scale * 10) / 10}px`;
            const line = Number(el.dataset.rzLineHeight);
            if (Number.isFinite(line) && line > 0) el.style.lineHeight = `${Math.round(line * scale * 10) / 10}px`;
            for (const side of ["Top", "Right", "Bottom", "Left"] as const) {
              const pad = Number(el.dataset[`rzPadding${side}`]);
              if (Number.isFinite(pad) && pad > 0) {
                el.style[`padding${side}` as "paddingTop"] = `${Math.round(pad * scale * 10) / 10}px`;
              }
            }
            for (const [name, prop] of [
              ["Width", "width"],
              ["Height", "height"],
              ["MinWidth", "minWidth"],
              ["MinHeight", "minHeight"],
              ["MaxWidth", "maxWidth"],
              ["MaxHeight", "maxHeight"],
            ] as const) {
              const base = Number(el.dataset[`rz${name}`]);
              if (Number.isFinite(base) && base > 0) {
                el.style[prop] = `${Math.round(base * scale * 10) / 10}px`;
              }
            }
          }
        }
        function readableBackground(el: HTMLElement, foreground: string, pageBg: string): string {
          let current: HTMLElement | null = el;
          while (current) {
            const bg = window.getComputedStyle(current).backgroundColor;
            if (isOpaqueColor(bg)) {
              return bg;
            }
            current = current.parentElement;
          }
          if (isOpaqueColor(pageBg)) return pageBg;
          return colorLuminance(foreground) > 0.55 ? "#0d1117" : "#ffffff";
        }
        function pageBackgroundColor(): string {
          const bodyBg = window.getComputedStyle(document.body).backgroundColor;
          if (isOpaqueColor(bodyBg)) return bodyBg;
          const htmlBg = window.getComputedStyle(document.documentElement).backgroundColor;
          if (isOpaqueColor(htmlBg)) return htmlBg;
          return "rgb(255, 255, 255)";
        }
        function isOpaqueColor(value: string): boolean {
          if (!value || value === "transparent") return false;
          const match = value.match(/rgba?\(([^)]+)\)/);
          if (!match) return true;
          const parts = match[1].split(",").map((part) => part.trim());
          if (parts.length < 4) return true;
          return Number(parts[3]) > 0.01;
        }
        function colorLuminance(value: string): number {
          const match = value.match(/rgba?\(([^)]+)\)/);
          if (!match) {
            if (value.startsWith("#")) {
              const hex = value.length === 4
                ? value.slice(1).split("").map((ch) => ch + ch).join("")
                : value.slice(1);
              const r = parseInt(hex.slice(0, 2), 16) / 255;
              const g = parseInt(hex.slice(2, 4), 16) / 255;
              const b = parseInt(hex.slice(4, 6), 16) / 255;
              const linear = [r, g, b].map((c) => (c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4));
              return 0.2126 * linear[0] + 0.7152 * linear[1] + 0.0722 * linear[2];
            }
            return 0;
          }
          const [r, g, b] = match[1].split(",").slice(0, 3).map((part) => Number(part.trim()) / 255);
          const linear = [r, g, b].map((c) => (c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4));
          return 0.2126 * linear[0] + 0.7152 * linear[1] + 0.0722 * linear[2];
        }
        function normalizeRadius(value: string): string {
          if (!value || value === "0px") return "10px";
          return value;
        }
        function clamp(value: number, min: number, max: number): number {
          if (max < min) return min;
          return Math.max(min, Math.min(value, max));
        }
        function compactRect(value: { top: number; left: number; width: number; height: number }): { top: number; left: number; width: number; height: number } {
          return {
            top: Math.round(value.top),
            left: Math.round(value.left),
            width: Math.round(value.width),
            height: Math.round(value.height),
          };
        }
        function parseCSSSize(value: string | undefined, basis: number, fallback: number): number {
          if (!value) return fallback;
          const px = value.match(/^(\d+(?:\.\d+)?)px$/);
          if (px) return Number(px[1]);
          const vwMatch = value.match(/(\d+(?:\.\d+)?)vw/);
          if (vwMatch) return Math.round((Number(vwMatch[1]) / 100) * window.innerWidth);
          const vhMatch = value.match(/(\d+(?:\.\d+)?)vh/);
          if (vhMatch) return Math.round((Number(vhMatch[1]) / 100) * window.innerHeight);
          if (value.startsWith("min(")) {
            const numbers = [...value.matchAll(/(\d+(?:\.\d+)?)(px|vw|vh)/g)].map((match) => {
              const n = Number(match[1]);
              if (match[2] === "vw") return (n / 100) * window.innerWidth;
              if (match[2] === "vh") return (n / 100) * window.innerHeight;
              return n;
            });
            if (numbers.length > 0) return Math.round(Math.min(...numbers));
          }
          return Math.round(Math.min(fallback, basis - 48));
        }
      },
      { sel: selector, opts: options },
    );
    await page.waitForTimeout(selector ? Math.max(1150, (options.selectHoldMs ?? 280) + 760) : 620);
    return result;
  };
}

/**
 * Wire crash + pageerror to <artifactDir>/ERROR.txt and return a `mark(step)`
 * breadcrumb. Because the harness eats Playwright's stdout, this file (plus the
 * per-scene screenshots) is how you learn WHY a recording failed. Clears any
 * stale ERROR.txt up front so its presence after a run means a real failure.
 */
export function captureDiagnostics(page: Page, artifactDir: string): { mark: (step: string) => void; onThrow: (err: unknown) => void } {
  fs.mkdirSync(artifactDir, { recursive: true });
  const errFile = path.join(artifactDir, "ERROR.txt");
  if (fs.existsSync(errFile)) fs.rmSync(errFile);
  let step = "init";
  const mark = (s: string): void => { step = s; };
  page.on("crash", () => fs.appendFileSync(errFile, `PAGE CRASH @ ${step}\n`));
  page.on("pageerror", (e) => fs.appendFileSync(errFile, `pageerror @ ${step}: ${e.message}\n`));
  const onThrow = (err: unknown): void =>
    fs.appendFileSync(errFile, `THROW @ ${step}: ${String((err as Error)?.message ?? err)}\n`);
  return { mark, onThrow };
}
