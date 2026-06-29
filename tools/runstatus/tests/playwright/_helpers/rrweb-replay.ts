/**
 * rrweb-replay.ts — production rrweb capture + replay-render harness (test-side).
 *
 * Demo-video production split into two deterministic halves:
 *
 *   1. CAPTURE (one live drive). While a Playwright spec drives the real
 *      `kitsoki web` server through a tour ONCE, record the session's full rrweb
 *      DOM-mutation event stream (installCapture → dumpEvents → writeEvents).
 *   2. RENDER (server-free, re-runnable). Replay that event stream through an
 *      rrweb Replayer while Playwright screen-records the replay surface, then
 *      transcode to H.264 MP4 + extract frames (renderReplayWithHolds, or the
 *      straight-through renderReplayToMp4).
 *
 * Once captured, the video is a pure function of (events, holds, viewport, DSF):
 * re-renderable offline, with no server, no story runtime, and no live-timing
 * variance. That is the determinism win over screen-recording a live server.
 *
 * PUBLIC SURFACE (the four entry points the demo skill drives):
 *   - installCapture(page)            — inject local rrweb + start full-stream recording
 *   - dumpEvents(page)                — pull the accumulated event stream
 *   - writeEvents(events, outPath)    — persist the stream (+ viewport/DSF sidecar)
 *   - renderReplayWithHolds(opts)     — chapter-keyed render (each view holds its
 *                                       real manifest dwell — the G2 fix)
 *   - renderReplayToMp4(opts)         — straight-through render (no per-view holds)
 *
 * DESIGN CONSTRAINTS baked in here (validated by the rrweb-replay eval):
 *  - rrweb MUST be the LOCAL bundled copy from node_modules/rrweb/dist/*,
 *    injected via page.addScriptTag({ path }) — NEVER a CDN/network import, so
 *    the render is deterministic and offline.
 *  - The UMD/IIFE bundle (rrweb.umd.min.cjs) exposes a single global `rrweb`
 *    carrying BOTH record and Replayer.
 *  - Capture with maskAllText:false + maskAllInputs:false (demos want full text)
 *    and recordCanvas:false.
 *  - CANVAS/VIDEO BOUNDARY: recordCanvas:false means <canvas>/<video>/WebGL
 *    surfaces do NOT reconstruct under this config — those tours MUST stay on the
 *    live screen-record path (the *-video.spec.ts specs). Validated only on
 *    SVG+HTML/CSS.
 *  - Accumulate the FULL event stream — do NOT reuse session-capture.ts's 30s
 *    rolling buffer, which truncates long tours.
 *  - VIEWPORT-MATCH INVARIANT: the render viewport/DSF MUST equal the capture's.
 *    The render forces transform:none on the player wrapper to defeat rrweb's
 *    fit-scale; that is only safe at 1:1, otherwise it silently clips to the
 *    top-left. writeEvents persists the capture viewport/DSF in a sidecar and the
 *    render helpers assert equality, throwing loudly on mismatch.
 *
 * No SPA changes: everything here is test-side. Reuses saveVideoAsMp4 /
 * prepareVideoDir from server.ts so the recording artifact is the same
 * universally-playable H.264 MP4 the live specs emit.
 */
import { chromium, type Page, type Browser, type BrowserContext } from "@playwright/test";
import { fileURLToPath } from "url";
import path from "path";
import fs from "fs";
import { spawnSync } from "child_process";
import { prepareVideoDir, saveVideoAsMp4 } from "./server.js";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

// _helpers → playwright → tests → runstatus (tools/runstatus project root)
const projectRoot = path.resolve(__dirname, "../../..");

/**
 * Absolute path to the LOCAL rrweb UMD/IIFE bundle that exposes a single global
 * `rrweb` with BOTH `record` and `Replayer`. We pick the minified UMD build
 * (rrweb.umd.min.cjs) over rrweb.umd.cjs purely for size; both expose the same
 * global.
 *
 * NEVER swap this for a CDN URL — the determinism guarantee depends on the
 * pinned local bundle (rrweb@2.0.1 in this workspace).
 */
export const RRWEB_BUNDLE = path.join(
  projectRoot,
  "node_modules",
  "rrweb",
  "dist",
  "rrweb.umd.min.cjs",
);

/** The global the UMD bundle installs on window. */
const RRWEB_GLOBAL = "rrweb";

/** Absolute path to the rrweb Replayer stylesheet (player chrome / iframe sizing). */
export const RRWEB_STYLE = path.join(
  projectRoot,
  "node_modules",
  "rrweb",
  "dist",
  "style.css",
);

const REPLAY_CURSOR_STYLE = `
  #replay-host .replayer-wrapper {
    overflow: visible;
  }
  #replay-host .replayer-mouse {
    z-index: 2147483647;
    pointer-events: none;
    width: 24px;
    height: 24px;
    background-image: url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='28' height='32' viewBox='0 0 28 32'%3E%3Cpath d='M3 3l18 18-8 1.5 4.5 8-4.5 2.5-4.5-8-5.5 6z' fill='%23fff' stroke='%23000' stroke-width='2' stroke-linejoin='round'/%3E%3C/svg%3E");
    filter: drop-shadow(0 1px 1px rgba(0, 0, 0, 0.75));
  }
  #replay-host .replayer-mouse::after {
    background: rgba(37, 99, 235, 0.35);
    box-shadow: 0 0 0 2px rgba(255, 255, 255, 0.9);
  }
  #replay-host .replayer-mouse-tail {
    z-index: 2147483646;
    pointer-events: none;
  }
`;

/** One rrweb event. Opaque to us — we only ferry the array to the Replayer. */
export type RrwebEvent = Record<string, unknown>;

/**
 * The capture's viewport + deviceScaleFactor, as observed INSIDE the recorded
 * page (window.innerWidth/innerHeight/devicePixelRatio) at install time. This is
 * the source of truth for the viewport-match invariant: the render must replay at
 * exactly these dimensions or rrweb's fit-scale defeat (transform:none) silently
 * clips to the top-left.
 */
export interface CaptureViewport {
  width: number;
  height: number;
  deviceScaleFactor: number;
}

/** Sidecar path for a given events JSON path: `<events>.capture.json`. */
export function captureSidecarPath(eventsJsonPath: string): string {
  return eventsJsonPath.replace(/\.json$/i, "") + ".capture.json";
}

/**
 * Inject the local rrweb bundle into `page` and start recording into
 * `window.__rrwebEvents`. maskAllText / maskAllInputs are false (demos want the
 * real text), recordCanvas is false (validated only on SVG+HTML/CSS — see the
 * canvas/video boundary in the file header). Accumulates the FULL stream — no
 * rolling buffer.
 *
 * Also stashes the page's observed viewport + DSF on `window.__rrwebViewport` so
 * dumpEvents/writeEvents can persist it for the render-time viewport-match
 * assertion.
 *
 * Persistence across hash-route changes: the kitsoki SPA uses hash routing, so
 * there is NO full document reload between routes — `record` keeps emitting
 * across `#/` → `#/s/...` transitions and the single `window.__rrwebEvents`
 * array keeps growing. (We also confirm `__rrwebRecording` so a double-install
 * is a no-op.) If a spec ever does a hard reload it must call installCapture
 * again, but the tour flows do not.
 */
export async function installCapture(page: Page): Promise<void> {
  await page.addScriptTag({ path: RRWEB_BUNDLE });
  await page.evaluate((globalName: string) => {
    const w = window as unknown as {
      __rrwebEvents?: unknown[];
      __rrwebRecording?: boolean;
      __rrwebStop?: () => void;
      __rrwebViewport?: { width: number; height: number; deviceScaleFactor: number };
    };
    if (w.__rrwebRecording) return;
    w.__rrwebEvents = [];
    // Snapshot the capture viewport/DSF as the page itself sees it — this is the
    // authoritative size the Replayer iframe is reconstructed at.
    w.__rrwebViewport = {
      width: window.innerWidth,
      height: window.innerHeight,
      deviceScaleFactor: window.devicePixelRatio || 1,
    };
    const rrweb = (window as unknown as Record<string, { record: (o: unknown) => (() => void) | undefined }>)[
      globalName
    ];
    if (!rrweb || typeof rrweb.record !== "function") {
      throw new Error(`rrweb global "${globalName}" missing record()`);
    }
    const stop = rrweb.record({
      emit: (e: unknown) => {
        w.__rrwebEvents!.push(e);
      },
      maskAllText: false,
      maskAllInputs: false,
      recordCanvas: false,
    });
    w.__rrwebStop = stop ?? (() => undefined);
    w.__rrwebRecording = true;
  }, RRWEB_GLOBAL);
}

/** The accumulated event stream + the capture viewport/DSF, pulled off the page. */
export interface CaptureDump {
  events: RrwebEvent[];
  viewport: CaptureViewport;
}

/**
 * Pull the accumulated event stream + the observed capture viewport/DSF off the
 * page. Returns a plain RrwebEvent[] (back-compat) — callers wanting the viewport
 * for the sidecar use dumpCapture instead.
 */
export async function dumpEvents(page: Page): Promise<RrwebEvent[]> {
  return page.evaluate(() => {
    const w = window as unknown as { __rrwebEvents?: RrwebEvent[] };
    return w.__rrwebEvents ?? [];
  });
}

/** Pull BOTH the event stream and the capture viewport/DSF off the page. */
export async function dumpCapture(page: Page): Promise<CaptureDump> {
  return page.evaluate(() => {
    const w = window as unknown as {
      __rrwebEvents?: RrwebEvent[];
      __rrwebViewport?: { width: number; height: number; deviceScaleFactor: number };
    };
    return {
      events: w.__rrwebEvents ?? [],
      viewport: w.__rrwebViewport ?? {
        width: window.innerWidth,
        height: window.innerHeight,
        deviceScaleFactor: window.devicePixelRatio || 1,
      },
    };
  });
}

/**
 * Write an event array to `outPath` as JSON (dir created as needed). If a
 * `viewport` is supplied (from dumpCapture), ALSO writes the capture sidecar
 * `<outPath>.capture.json` recording the capture viewport/DSF — the source of
 * truth for the render-time viewport-match assertion.
 */
export function writeEvents(events: RrwebEvent[], outPath: string, viewport?: CaptureViewport): void {
  fs.mkdirSync(path.dirname(outPath), { recursive: true });
  fs.writeFileSync(outPath, JSON.stringify(events, null, 0) + "\n");
  if (viewport) {
    fs.writeFileSync(captureSidecarPath(outPath), JSON.stringify(viewport, null, 2) + "\n");
  }
}

/**
 * Assert the render viewport/DSF equals the capture's (recorded in the sidecar by
 * writeEvents). The render forces transform:none on the player wrapper to defeat
 * rrweb's fit-scale — that is ONLY safe at 1:1, otherwise it silently clips to
 * the top-left. We FAIL LOUDLY here rather than ship a clipped video.
 *
 * If no sidecar exists (older capture written without viewport, or hand-authored
 * events), this is a no-op — there is nothing to compare against. To enforce the
 * invariant, always capture via dumpCapture + writeEvents(...viewport).
 */
export function assertViewportMatchesCapture(
  eventsJsonPath: string,
  render: { width: number; height: number; deviceScaleFactor: number },
): void {
  const sidecar = captureSidecarPath(eventsJsonPath);
  if (!fs.existsSync(sidecar)) return;
  const cap = JSON.parse(fs.readFileSync(sidecar, "utf-8")) as CaptureViewport;
  const mismatches: string[] = [];
  if (cap.width !== render.width) mismatches.push(`width capture=${cap.width} render=${render.width}`);
  if (cap.height !== render.height) mismatches.push(`height capture=${cap.height} render=${render.height}`);
  if (cap.deviceScaleFactor !== render.deviceScaleFactor) {
    mismatches.push(`deviceScaleFactor capture=${cap.deviceScaleFactor} render=${render.deviceScaleFactor}`);
  }
  if (mismatches.length) {
    throw new Error(
      `rrweb render viewport/DSF must equal the capture's (the render forces ` +
        `transform:none on the player wrapper, which silently clips to the top-left ` +
        `at any other size). Mismatch: ${mismatches.join("; ")}. ` +
        `Capture sidecar: ${sidecar}`,
    );
  }
}

export interface RenderOpts {
  /** Path to the JSON file written by writeEvents. */
  eventsJsonPath: string;
  /** MUST match the viewport the capture ran at (asserted against the sidecar —
   *  rrweb sizes the iframe to the captured viewport; matching avoids clipping). */
  viewport: { width: number; height: number };
  /** MUST match the capture's deviceScaleFactor (crispness parity + clip safety). */
  deviceScaleFactor: number;
  /** Directory for the MP4 + frames/. */
  outDir: string;
  /** Base name for the MP4 (no extension). */
  name: string;
}

export interface RenderResult {
  mp4Path: string | null;
  framesDir: string;
  totalTimeMs: number;
  eventCount: number;
}

/**
 * One held chapter for renderReplayWithHolds. `seekMs` is the player-relative
 * timestamp (ms from the first event) at which this step's view is FULLY
 * rendered — we `pause(seekMs)` to freeze that exact reconstructed frame, then
 * hold it on-screen for `holdMs` wall-clock. `id` is for logging only.
 */
export interface HoldChapter {
  id: string;
  /** player.pause() target in ms from first event — the settled view for this step. */
  seekMs: number;
  /** how long to keep this view on-screen (ms) — the tour step's manifest dwellMs. */
  holdMs: number;
}

export interface RenderHoldsOpts extends RenderOpts {
  /** Ordered, settled per-step seek points + manifest dwell durations. */
  chapters: HoldChapter[];
  /** Extract frames at this fps (>=2 recommended so each multi-second hold lands
   *  on several frames and the QA sampler can't miss a legible window). Default 2. */
  fps?: number;
}

/**
 * WHY THIS EXISTS (the G2 fix). renderReplayToMp4 below plays the stream once at
 * speed:1 with skipInactive:false. The PLAYER honours that faithfully — its
 * getCurrentTime() tracks wall-clock across the long inter-step inactive gaps.
 * The collapse is NOT in the player: it is in the RECORDER. rrweb defaults
 * pauseAnimation:true, so during a multi-second dwell the reconstructed DOM is
 * perfectly static — zero compositor paints — and headless Chromium's
 * recordVideo emits ~no frames for that span. After the fps CFR transcode the
 * static hold survives as a single frame while the next mutation "jumps" in, so
 * a view that held ~7s live shows for ~1s in the extracted frames (and the QA
 * sampler lands on a transition frame).
 *
 * The robust, deterministic fix: stop trusting the recorder to capture passive
 * holds. Drive the Replayer CHAPTER BY CHAPTER — pause(seekMs) to freeze each
 * step's settled view, then hold for that step's manifest dwellMs while nudging
 * a 1px marker every frame so the compositor keeps producing frames the recorder
 * actually captures. The render stays a pure function of (events, chapters,
 * viewport, DSF): same inputs → same video. seekMs/holdMs come from the tour
 * manifest dwellMs + the capture's step timeline, never from live timing.
 */
export async function renderReplayWithHolds(opts: RenderHoldsOpts): Promise<RenderResult> {
  // Viewport-match invariant: fail loudly before any rendering if the render
  // size/DSF differs from the capture's (would silently clip to top-left).
  assertViewportMatchesCapture(opts.eventsJsonPath, {
    width: opts.viewport.width,
    height: opts.viewport.height,
    deviceScaleFactor: opts.deviceScaleFactor,
  });

  const events = JSON.parse(fs.readFileSync(opts.eventsJsonPath, "utf-8")) as RrwebEvent[];
  if (!Array.isArray(events) || events.length < 2) {
    throw new Error(
      `renderReplayWithHolds: need >=2 events, got ${Array.isArray(events) ? events.length : "non-array"} from ${opts.eventsJsonPath}`,
    );
  }
  if (!opts.chapters.length) throw new Error("renderReplayWithHolds: need >=1 chapter");
  const fps = opts.fps ?? 2;

  fs.mkdirSync(opts.outDir, { recursive: true });
  const videoDir = path.join(opts.outDir, "_replay-video");
  prepareVideoDir(videoDir);

  const browser: Browser = await chromium.launch({ headless: true });
  const context: BrowserContext = await browser.newContext({
    viewport: opts.viewport,
    deviceScaleFactor: opts.deviceScaleFactor,
    recordVideo: { dir: videoDir, size: opts.viewport },
  });
  const page: Page = await context.newPage();
  const video = page.video();

  let totalTimeMs = 0;
  let mp4Path: string | null = null;
  const framesDir = path.join(opts.outDir, "frames");

  try {
    // Same host as renderReplayToMp4 (viewport-match invariant: transform:none
    // is only safe because render viewport/DSF == capture viewport/DSF). A 1px
    // #repaint-nudge marker in the corner is toggled during holds to keep the
    // compositor painting so the recorder captures the static dwell.
    await page.setContent(
      `<!doctype html><html><head><meta charset="utf-8">
       <style>
         html,body{margin:0;padding:0;background:#070d1a;width:100%;height:100%;overflow:hidden}
         #replay-host{position:fixed;inset:0;background:#070d1a}
         #replay-host .replayer-wrapper{position:absolute;top:0;left:0;transform:none!important}
         #replay-host iframe{border:none;background:#fff}
         #repaint-nudge{position:fixed;top:0;left:0;width:1px;height:1px;background:#070d1a;z-index:9}
       </style></head>
       <body><div id="replay-host"></div><div id="repaint-nudge"></div></body></html>`,
      { waitUntil: "load" },
    );
    // setContent replaces the document head, so inject the rrweb runtime/styles
    // after creating the replay host. The player CSS is load-bearing for cursor
    // size/background and iframe layout.
    await page.addScriptTag({ path: RRWEB_BUNDLE });
    await page.addStyleTag({ path: RRWEB_STYLE });
    await page.addStyleTag({ content: REPLAY_CURSOR_STYLE });

    totalTimeMs = await page.evaluate(
      ({ evts, globalName }) => {
        const host = document.getElementById("replay-host");
        if (!host) throw new Error("no #replay-host");
        const rrweb = (window as unknown as Record<string, { Replayer: new (e: unknown[], c: Record<string, unknown>) => { getMetaData(): { totalTime: number }; pause(t?: number): void; play(t?: number): void } }>)[
          globalName
        ];
        if (!rrweb || typeof rrweb.Replayer !== "function") {
          throw new Error(`rrweb global "${globalName}" missing Replayer`);
        }
        const player = new rrweb.Replayer(evts as unknown[], {
          root: host,
          speed: 1,
          skipInactive: false,
          showWarning: false,
          mouseTail: false,
        });
        (window as unknown as { __player: unknown }).__player = player;
        // pause(0) builds the iframe + paints the first full snapshot without
        // starting playback, so subsequent pause(seekMs) is a pure seek.
        player.pause(0);
        return Math.max(0, player.getMetaData().totalTime || 0);
      },
      { evts: events, globalName: RRWEB_GLOBAL },
    );

    await page.waitForSelector("#replay-host .replayer-wrapper iframe", { timeout: 10000 });
    await page.waitForTimeout(500);

    // Drive chapter by chapter: seek to the settled view, then hold while nudging
    // a repaint every animation frame so the recorder captures the dwell.
    for (const ch of opts.chapters) {
      await page.evaluate((seekMs) => {
        const player = (window as unknown as { __player: { pause(t?: number): void } }).__player;
        player.pause(seekMs);
      }, ch.seekMs);
      // Let the seek settle / paint before the hold begins.
      await page.waitForTimeout(250);
      const holdUntil = Date.now() + ch.holdMs;
      let flip = 0;
      while (Date.now() < holdUntil) {
        // Toggle the 1px marker so each tick is a real compositor frame.
        flip ^= 1;
        await page.evaluate((f) => {
          const n = document.getElementById("repaint-nudge");
          if (n) n.style.background = f ? "#070d1b" : "#070d1a";
        }, flip);
        await page.waitForTimeout(80);
      }
    }
    // Tail so the final held view is captured cleanly.
    await page.waitForTimeout(800);
  } finally {
    await context.close();
    mp4Path = await saveVideoAsMp4(video, opts.outDir, opts.name);
    await browser.close();
  }

  fs.rmSync(framesDir, { recursive: true, force: true });
  fs.mkdirSync(framesDir, { recursive: true });
  if (mp4Path && fs.existsSync(mp4Path)) {
    const r = spawnSync(
      "ffmpeg",
      ["-y", "-loglevel", "error", "-i", mp4Path, "-vf", `fps=${fps}`, path.join(framesDir, "frame-%03d.png")],
      { encoding: "utf8" },
    );
    if (r.status !== 0) {
      console.warn(`[rrweb-replay] frame extraction failed: ${r.stderr?.slice(0, 300)}`);
    }
  }

  return { mp4Path, framesDir, totalTimeMs, eventCount: events.length };
}

/**
 * Render an rrweb event stream to an MP4 by replaying it through a Replayer
 * while Playwright screen-records, then extract per-second PNG frames via
 * ffmpeg (so QA / viewability review have stills). NO per-view holds — use this
 * for short, mutation-dense tours where every view is naturally on-screen; for
 * view-dwell tours that linger on a tab, use renderReplayWithHolds (the G2 fix).
 *
 * The replay page is a full-viewport dark `#replay-host` into which the
 * Replayer mounts its own iframe. We:
 *   1. assert the render viewport/DSF matches the capture's (clip safety);
 *   2. launch a chromium context with recordVideo{dir,size:viewport} +
 *      deviceScaleFactor matching the capture;
 *   3. addScriptTag(local rrweb bundle) + addStyleTag(rrweb style.css) — the
 *      style is load-bearing: without the player CSS the iframe is unstyled /
 *      collapsed and frames look blank;
 *   4. setContent a host div, then page.evaluate `new Replayer(events, {...})`
 *      — passing the EVENTS ARRAY (not a JSON string) — and `player.play(0)`;
 *   5. read getMetaData().totalTime and wait totalTime + 1500ms so the last
 *      frame is held;
 *   6. context.close() finalises the webm, saveVideoAsMp4 transcodes to H.264;
 *   7. ffmpeg extracts 1fps PNG frames into outDir/frames.
 *
 * The Replayer needs the iframe's first full snapshot to paint before we trust
 * a frame, so we wait for `.replayer-wrapper iframe` to exist and give it a
 * tick before play.
 */
export async function renderReplayToMp4(opts: RenderOpts): Promise<RenderResult> {
  // Viewport-match invariant (see renderReplayWithHolds).
  assertViewportMatchesCapture(opts.eventsJsonPath, {
    width: opts.viewport.width,
    height: opts.viewport.height,
    deviceScaleFactor: opts.deviceScaleFactor,
  });

  const events = JSON.parse(fs.readFileSync(opts.eventsJsonPath, "utf-8")) as RrwebEvent[];
  if (!Array.isArray(events) || events.length < 2) {
    throw new Error(
      `renderReplayToMp4: need >=2 events, got ${Array.isArray(events) ? events.length : "non-array"} from ${opts.eventsJsonPath}`,
    );
  }

  fs.mkdirSync(opts.outDir, { recursive: true });
  const videoDir = path.join(opts.outDir, "_replay-video");
  prepareVideoDir(videoDir);

  const browser: Browser = await chromium.launch({ headless: true });
  const context: BrowserContext = await browser.newContext({
    viewport: opts.viewport,
    deviceScaleFactor: opts.deviceScaleFactor,
    recordVideo: { dir: videoDir, size: opts.viewport },
  });
  const page: Page = await context.newPage();
  const video = page.video();

  let totalTimeMs = 0;
  let mp4Path: string | null = null;
  const framesDir = path.join(opts.outDir, "frames");

  try {
    // Full-viewport dark host. The Replayer mounts a .replayer-wrapper > iframe
    // inside it; we pin top-left so the recorded frame is the reconstructed UI
    // at 1:1, not a centered/scaled thumbnail.
    await page.setContent(
      `<!doctype html><html><head><meta charset="utf-8">
       <style>
         html,body{margin:0;padding:0;background:#070d1a;width:100%;height:100%;overflow:hidden}
         #replay-host{position:fixed;inset:0;background:#070d1a}
         #replay-host .replayer-wrapper{position:absolute;top:0;left:0;transform:none!important}
         #replay-host iframe{border:none;background:#fff}
       </style></head>
       <body><div id="replay-host"></div></body></html>`,
      { waitUntil: "load" },
    );
    // Inject the LOCAL bundle + player CSS after setContent. style.css is what
    // makes the iframe and replay cursor paint correctly; injecting it before
    // setContent silently discards it.
    await page.addScriptTag({ path: RRWEB_BUNDLE });
    await page.addStyleTag({ path: RRWEB_STYLE });
    await page.addStyleTag({ content: REPLAY_CURSOR_STYLE });

    // Construct the Replayer with the EVENTS ARRAY (not a JSON string) and the
    // exact options the eval mandates, then play from 0.
    totalTimeMs = await page.evaluate(
      ({ evts, globalName }) => {
        const host = document.getElementById("replay-host");
        if (!host) throw new Error("no #replay-host");
        const rrweb = (window as unknown as Record<string, { Replayer: new (e: unknown[], c: Record<string, unknown>) => { getMetaData(): { totalTime: number }; play(t?: number): void } }>)[
          globalName
        ];
        if (!rrweb || typeof rrweb.Replayer !== "function") {
          throw new Error(`rrweb global "${globalName}" missing Replayer`);
        }
        const player = new rrweb.Replayer(evts as unknown[], {
          root: host,
          speed: 1,
          skipInactive: false,
          showWarning: false,
          mouseTail: false,
        });
        const meta = player.getMetaData();
        player.play(0);
        return Math.max(0, meta.totalTime || 0);
      },
      { evts: events, globalName: RRWEB_GLOBAL },
    );

    // The iframe needs a tick to paint its first full snapshot before the video
    // frames are trustworthy.
    await page.waitForSelector("#replay-host .replayer-wrapper iframe", { timeout: 10000 });
    await page.waitForTimeout(500);

    // Hold for the whole replay + a tail buffer so the final frame is captured.
    await page.waitForTimeout(totalTimeMs + 1500);
  } finally {
    await context.close();
    mp4Path = await saveVideoAsMp4(video, opts.outDir, opts.name);
    await browser.close();
  }

  // Extract 1fps PNG frames for QA / viewability review.
  fs.rmSync(framesDir, { recursive: true, force: true });
  fs.mkdirSync(framesDir, { recursive: true });
  if (mp4Path && fs.existsSync(mp4Path)) {
    const r = spawnSync(
      "ffmpeg",
      ["-y", "-loglevel", "error", "-i", mp4Path, "-vf", "fps=1", path.join(framesDir, "frame-%03d.png")],
      { encoding: "utf8" },
    );
    if (r.status !== 0) {
      console.warn(`[rrweb-replay] frame extraction failed: ${r.stderr?.slice(0, 300)}`);
    }
  }

  return { mp4Path, framesDir, totalTimeMs, eventCount: events.length };
}
