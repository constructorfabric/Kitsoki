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
import { dwell } from "./recorder.js";

/** Fixed MacBook-ish recording resolution — keep every demo identical. */
export const DEMO_VIEWPORT = { width: 1600, height: 900 } as const;

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
 * Install a portable, non-obscuring spotlight outline and return a
 * `spotlight(sel)` mover. The outline frames the target element without
 * tinting, blurring, or covering the page beneath it; helper nodes are
 * pointer-events:none so they never intercept clicks. Pass null to hide it.
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
