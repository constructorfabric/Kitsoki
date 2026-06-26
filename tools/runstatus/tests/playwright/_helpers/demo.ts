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
 * Install a portable spotlight + dimming backdrop and return a `spotlight(sel)`
 * mover. This is the narration primitive for driving a site OTHER than kitsoki:
 * the kitsoki tour overlay (window.__startTourWithSteps, [data-testid=tour-*])
 * only exists inside the kitsoki SPA, so an external page (e.g. a GitHub issue)
 * is narrated with makeCaption + this, both of which inject plain DOM and work
 * on ANY page. The backdrop dims the page and the box cuts a bright outlined
 * hole over the target element's bounding rect; both are pointer-events:none so
 * they never intercept clicks. Pass null to lift the dim and hide the box.
 */
export async function makeSpotlight(page: Page): Promise<Spotlight> {
  await page.addStyleTag({
    // Position is set INSTANTLY (only opacity fades) so a screenshot taken right
    // after a move can never catch the box mid-animation — the per-scene PNGs the
    // QA gate consumes must be correct at any WEB_CHAT_PACE, not just watch-speed.
    content:
      `#demo-spot-back{position:fixed;inset:0;z-index:99990;pointer-events:none;` +
      `box-shadow:inset 0 0 0 4000px rgba(2,6,23,.62);opacity:0;transition:opacity .35s}` +
      `#demo-spot-back.show{opacity:1}` +
      `#demo-spot{position:fixed;z-index:99992;pointer-events:none;border-radius:10px;` +
      `border:3px solid #fbbf24;box-shadow:0 0 0 4000px rgba(2,6,23,.62),0 0 22px 4px rgba(251,191,36,.6);` +
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

export type ReadableZoomOptions = {
  title?: string;
  maxWidth?: string;
  maxHeight?: string;
  fontSize?: number;
};

export type ReadableZoom = (selector: string | null, options?: ReadableZoomOptions) => Promise<boolean>;

/**
 * Install a capture-only readable zoom overlay and return a `zoom(selector)`
 * mover. The helper clones the target's rendered text/HTML into a fixed panel,
 * keeps the original page visible behind the dimmed backdrop, and leaves a
 * pointer-events:none overlay that rrweb captures as ordinary DOM.
 *
 * Use this when the real evidence is a dense comment, code block, JSON payload,
 * or metadata panel that is authentic but too small for a narrated demo. The
 * source page is not destructively modified: the original target remains on the
 * page and can still be spotlighted separately.
 */
export async function makeReadableZoom(page: Page): Promise<ReadableZoom> {
  await page.addStyleTag({
    content:
      `#demo-readable-zoom{position:fixed;right:34px;bottom:32px;z-index:100000;` +
      `box-sizing:border-box;width:min(760px,52vw);max-height:62vh;overflow:auto;` +
      `background:#f8fafc;color:#0f172a;border:2px solid #fbbf24;border-radius:14px;` +
      `box-shadow:0 24px 80px rgba(2,6,23,.42);padding:18px 22px;opacity:0;` +
      `transform:translateY(14px) scale(.98);transition:opacity .25s,transform .25s;` +
      `pointer-events:none;font:500 20px/1.45 ui-sans-serif,system-ui,sans-serif}` +
      `#demo-readable-zoom.show{opacity:1;transform:translateY(0) scale(1)}` +
      `#demo-readable-zoom .rz-title{font:800 17px/1.2 ui-sans-serif,system-ui,sans-serif;` +
      `text-transform:uppercase;letter-spacing:.06em;color:#475569;margin-bottom:10px}` +
      `#demo-readable-zoom .rz-body{white-space:pre-wrap;overflow-wrap:anywhere}` +
      `#demo-readable-zoom pre,#demo-readable-zoom code{font:600 18px/1.45 ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;` +
      `white-space:pre-wrap;overflow-wrap:anywhere;background:#e2e8f0;border-radius:8px;padding:2px 5px}` +
      `#demo-readable-zoom a{color:#075985;text-decoration:underline}` +
      `#demo-readable-zoom mark{background:#fde68a;color:#111827;border-radius:4px;padding:0 3px}`,
  });
  await page.evaluate(() => {
    if (document.getElementById("demo-readable-zoom")) return;
    const el = document.createElement("div");
    el.id = "demo-readable-zoom";
    el.setAttribute("aria-hidden", "true");
    el.innerHTML = `<div class="rz-title"></div><div class="rz-body"></div>`;
    document.body.appendChild(el);
  });
  return async (selector, options = {}) => {
    const shown = await page.evaluate(
      ({ sel, opts }) => {
        const panel = document.getElementById("demo-readable-zoom");
        if (!panel) return false;
        if (!sel) {
          panel.classList.remove("show");
          return true;
        }
        const target = document.querySelector<HTMLElement>(sel);
        if (!target) {
          panel.classList.remove("show");
          return false;
        }
        const title = panel.querySelector<HTMLElement>(".rz-title");
        const body = panel.querySelector<HTMLElement>(".rz-body");
        if (!title || !body) return false;
        const text = (target.innerText || target.textContent || "").replace(/\n{3,}/g, "\n\n").trim();
        const html = target.matches("pre, code") ? `<pre>${escapeHTML(text)}</pre>` : target.innerHTML;
        title.textContent = opts.title || "Readable zoom";
        body.innerHTML = html || escapeHTML(text);
        panel.style.width = opts.maxWidth || "min(760px,52vw)";
        panel.style.maxHeight = opts.maxHeight || "62vh";
        panel.style.fontSize = `${opts.fontSize || 20}px`;
        requestAnimationFrame(() => panel.classList.add("show"));
        return true;

        function escapeHTML(value: string): string {
          return value
            .replace(/&/g, "&amp;")
            .replace(/</g, "&lt;")
            .replace(/>/g, "&gt;");
        }
      },
      { sel: selector, opts: options },
    );
    await page.waitForTimeout(300);
    return shown;
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
