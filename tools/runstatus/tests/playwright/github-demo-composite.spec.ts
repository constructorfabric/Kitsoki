/**
 * github-demo composite — Act 3 of the @kitsoki GitHub-loop demo (epic
 * kitsoki-github-agent, demo slice #6).
 *
 * Renders ONE slidey pitch deck that interleaves title / personas (cast +
 * use-cases) / section slides ("The GitHub side" / "The kitsoki side" /
 * "One loop") around TWO rrweb-EMBEDDED `video` scenes — NOT MP4. The deck is
 *   docs/proposals/demo-assets/kitsoki-github/deck/kitsoki-github.deck.json
 * and the two `video` scenes embed rrweb DOM-session logs staged under
 *   docs/proposals/demo-assets/kitsoki-github/deck/clips/{act1-github,act2-webviewer}.rrweb.json
 * Each clip carries IN-LOG `slidey.chapter` custom events, so `chapters:"auto"`
 * derives deck-styled lower-thirds with no sidecar (Act 1 from slidey's rrweb
 * tour engine; Act 2 stamped by github-demo-act2-rrweb-capture.spec.ts).
 *
 * The render is driven through the SAME slidey pipeline host.slidey.render
 * discovers ($SLIDEY_HOME/src/index.js, else the `slidey` binary on PATH). We
 * shell out directly so the composite can be produced + validated without a
 * kitsoki server — the bytes are identical to the host-call path. The baked
 * render seek-rasterizes each rrweb log via Replayer.goto(t): real motion, no
 * MP4 input. No LLM, no GitHub, no network: deterministic render only.
 *
 * Output (gitignored): .artifacts/github-demo/composite/kitsoki-github.mp4 plus
 * the slidey-emitted kitsoki-github.mp4.chapters.json sidecar + per-second QA
 * frames so the embedded video scenes can be proven non-blank.
 *
 * BLOCKED-SAFE: if either rrweb clip is missing, the deck is still valid but
 * un-renderable; the render test SKIPS with a precise message (record Act 1 via
 * the slidey tour engine, Act 2 via github-demo-act2-rrweb-capture.spec.ts).
 *
 * Validate (no render, deck shape only): the always-on `deck validates` test.
 * Render (full MP4, ~minutes — rrweb rasterize is slow):
 *   pnpm exec playwright test github-demo-composite --project=chromium
 */
import { test, expect } from "@playwright/test";
import { execFileSync, spawnSync } from "node:child_process";
import * as fs from "node:fs";
import * as path from "node:path";

import { repoRoot } from "./_helpers/server.js";

const DECK = path.join(
  repoRoot,
  "docs",
  "proposals",
  "demo-assets",
  "kitsoki-github",
  "deck",
  "kitsoki-github.deck.json",
);
const CLIPS_DIR = path.join(
  repoRoot,
  "docs",
  "proposals",
  "demo-assets",
  "kitsoki-github",
  "deck",
  "clips",
);
const ACT1 = path.join(CLIPS_DIR, "act1-github.rrweb.json");
const ACT2 = path.join(CLIPS_DIR, "act2-webviewer.rrweb.json");

const OUT_DIR = path.join(repoRoot, ".artifacts", "github-demo", "composite");
const OUT_MP4 = path.join(OUT_DIR, "kitsoki-github.mp4");
const OUT_CHAPTERS = `${OUT_MP4}.chapters.json`;
const FRAMES_DIR = path.join(OUT_DIR, "frames");

// Resolve the slidey entrypoint exactly as host.slidey.render does:
// $SLIDEY_HOME/src/index.js first, else the `slidey` binary on PATH. Default to
// the sibling slidey checkout so the spec runs without SLIDEY_HOME exported.
function slideyArgv(rest: string[]): { cmd: string; args: string[] } {
  const home = process.env.SLIDEY_HOME || path.resolve(repoRoot, "..", "slidey");
  if (fs.existsSync(path.join(home, "src", "index.js"))) {
    return { cmd: process.execPath, args: [path.join(home, "src", "index.js"), ...rest] };
  }
  return { cmd: "slidey", args: rest };
}

function runSlidey(rest: string[], timeoutMs: number): string {
  const { cmd, args } = slideyArgv(rest);
  return execFileSync(cmd, args, {
    cwd: repoRoot,
    encoding: "utf8",
    timeout: timeoutMs,
    stdio: ["ignore", "pipe", "pipe"],
  });
}

type RrwebEvent = { type?: number; timestamp?: number; data?: { tag?: string } };

/** Load an rrweb log (bare array or {events:[...]}). */
function loadEvents(p: string): RrwebEvent[] {
  const raw = JSON.parse(fs.readFileSync(p, "utf8"));
  return Array.isArray(raw) ? raw : Array.isArray(raw?.events) ? raw.events : [];
}

/** Wall-clock span of an rrweb log, ms. */
function clipDurationMs(p: string): number {
  const ev = loadEvents(p);
  const ts = ev.map((e) => e.timestamp).filter((t): t is number => typeof t === "number");
  return ts.length < 2 ? 0 : Math.max(...ts) - Math.min(...ts);
}

/** Count in-log slidey.chapter custom events (type 5, tag slidey.chapter). */
function clipChapterCount(p: string): number {
  return loadEvents(p).filter((e) => e.type === 5 && e.data?.tag === "slidey.chapter").length;
}

/** MP4 duration via ffprobe (seconds → ms). */
function mp4DurationMs(p: string): number {
  const r = spawnSync(
    "ffprobe",
    ["-v", "error", "-show_entries", "format=duration", "-of", "default=nw=1:nk=1", p],
    { encoding: "utf8" },
  );
  const sec = parseFloat((r.stdout || "").trim());
  return Number.isFinite(sec) ? Math.round(sec * 1000) : 0;
}

test.describe("github-demo composite (rrweb-embedded slidey deck)", () => {
  // Always-on: proves the deck is structurally valid + rrweb-embedded — runs
  // even when the clips are absent. `--list` validates the spec without render.
  test("github-demo composite deck validates", () => {
    expect(fs.existsSync(DECK), `deck missing at ${DECK}`).toBeTruthy();

    const spec = JSON.parse(fs.readFileSync(DECK, "utf8"));
    expect(spec?.meta?.mode, "meta.mode MUST be 'pitch' or every frame renders blank").toBe(
      "pitch",
    );
    // Capture==render viewport invariant (1600x900) so video scenes letterbox-clean.
    expect(spec.meta.resolution).toEqual({ width: 1600, height: 900 });

    const scenes = spec.scenes as Array<{ type: string; src?: string; rrweb?: string }>;
    const videos = scenes.filter((s) => s.type === "video");
    // Exactly two video scenes, BOTH rrweb-embedded, NEITHER MP4.
    expect(videos.length).toBe(2);
    for (const v of videos) {
      expect(v.rrweb, "video scene must embed an rrweb log").toBeTruthy();
      expect(v.src, "video scene must NOT reference a baked MP4 (rrweb only)").toBeFalsy();
    }
    expect(videos.map((v) => v.rrweb)).toEqual([
      "clips/act1-github.rrweb.json",
      "clips/act2-webviewer.rrweb.json",
    ]);
    // Section slides per the storyboard.
    const titles = scenes
      .filter((s) => s.type === "title")
      .map((s) => (s as { title?: string }).title);
    for (const want of ["The GitHub side", "The kitsoki side", "Mention to merge"]) {
      expect(titles, `missing section slide "${want}"`).toContain(want);
    }

    // slidey --list parses + validates the spec without rasterizing the clips.
    let out = "";
    try {
      out = runSlidey([DECK, "--list"], 180_000);
    } catch (err) {
      const e = err as { stdout?: string; stderr?: string; message?: string };
      out = `${e.stdout ?? ""}\n${e.stderr ?? ""}\n${e.message ?? ""}`;
    }
    expect(/SyntaxError|unexpected token|invalid spec/i.test(out), `slidey rejected the deck:\n${out}`).toBeFalsy();
  });

  // Full render — only when both rrweb clips are staged under clips/.
  test("github-demo composite renders to MP4 + chapter sidecar (non-blank)", () => {
    test.setTimeout(1_200_000); // rrweb seek-rasterize is slow

    const haveClips = fs.existsSync(ACT1) && fs.existsSync(ACT2);
    test.skip(
      !haveClips,
      `rrweb clips not staged — expected ${ACT1} and ${ACT2}. Record Act 1 via ` +
        `slidey's rrweb tour engine (.artifacts/github-rrweb-tours/act1-github.tour.json) ` +
        `and Act 2 via github-demo-act2-rrweb-capture.spec.ts, both into deck/clips/.`,
    );

    // Each clip must be non-empty and carry in-log chapters (chapters:"auto").
    const dur1 = clipDurationMs(ACT1);
    const dur2 = clipDurationMs(ACT2);
    expect(dur1, "Act 1 clip is empty/zero-length").toBeGreaterThan(1000);
    expect(dur2, "Act 2 clip is empty/zero-length").toBeGreaterThan(1000);
    expect(clipChapterCount(ACT1), "Act 1 clip carries no in-log chapters").toBeGreaterThanOrEqual(1);
    expect(clipChapterCount(ACT2), "Act 2 clip carries no in-log chapters").toBeGreaterThanOrEqual(1);
    const clipSumMs = dur1 + dur2;

    fs.mkdirSync(OUT_DIR, { recursive: true });
    for (const f of [OUT_MP4, OUT_CHAPTERS]) {
      if (fs.existsSync(f)) fs.rmSync(f);
    }
    fs.rmSync(FRAMES_DIR, { recursive: true, force: true });
    fs.mkdirSync(FRAMES_DIR, { recursive: true });

    // Render the rrweb-embedded deck → MP4 + slidey's own chapter sidecar.
    runSlidey([DECK, OUT_MP4], 1_200_000);

    expect(fs.existsSync(OUT_MP4), `render produced no MP4 at ${OUT_MP4}`).toBeTruthy();
    expect(fs.statSync(OUT_MP4).size, "rendered MP4 is empty").toBeGreaterThan(10_000);

    // Duration > the SUM of the two clip durations: the composite carries both
    // embedded clips IN FULL plus the bracketing title/persona/cta scenes.
    const compMs = mp4DurationMs(OUT_MP4);
    expect(
      compMs,
      `composite (${compMs}ms) must exceed the clip-duration sum (${clipSumMs}ms)`,
    ).toBeGreaterThan(clipSumMs);

    // Chapter sidecar: slidey emits <out>.mp4.chapters.json for a deck render.
    expect(fs.existsSync(OUT_CHAPTERS), `no chapter sidecar at ${OUT_CHAPTERS}`).toBeTruthy();
    const raw = JSON.parse(fs.readFileSync(OUT_CHAPTERS, "utf8"));
    const list = Array.isArray(raw) ? raw : raw?.chapters;
    expect(Array.isArray(list) && list.length > 0, "chapter sidecar carries no chapters").toBeTruthy();

    // Non-blank proof: extract 1fps frames and assert the frames inside each
    // embedded video scene's composite time window are visually busy. A blank
    // rasterize (the classic rrweb "missing player CSS" failure) yields
    // near-uniform, tiny PNGs; a real reconstructed UI frame is tens of KB+.
    const r = spawnSync(
      "ffmpeg",
      ["-y", "-loglevel", "error", "-i", OUT_MP4, "-vf", "fps=1", path.join(FRAMES_DIR, "f-%04d.png")],
      { encoding: "utf8" },
    );
    expect(r.status, `frame extraction failed: ${r.stderr?.slice(0, 300)}`).toBe(0);

    const frames = fs
      .readdirSync(FRAMES_DIR)
      .filter((f) => f.endsWith(".png"))
      .map((f) => ({
        name: f,
        sec: parseInt(f.match(/f-(\d+)\.png/)?.[1] ?? "0", 10),
        size: fs.statSync(path.join(FRAMES_DIR, f)).size,
      }));
    expect(frames.length, "no frames extracted from composite").toBeGreaterThan(10);

    // Video scene start offsets (sec) from slidey's --list table, in order.
    const listOut = runSlidey([DECK, "--list"], 180_000);
    const videoStarts: number[] = [];
    for (const line of listOut.split("\n")) {
      const m = line.match(/^\s*\d+\s+video\s+([\d.]+)s\s/);
      if (m) videoStarts.push(parseFloat(m[1]));
    }
    expect(videoStarts.length, "expected 2 video scenes in --list").toBe(2);

    const BLANK_FLOOR = 15_000;
    const windows = [
      { start: videoStarts[0], dur: dur1 / 1000, label: "Act 1 (GitHub)" },
      { start: videoStarts[1], dur: dur2 / 1000, label: "Act 2 (kitsoki)" },
    ];
    for (const w of windows) {
      const inWin = frames.filter(
        (f) => f.sec >= Math.floor(w.start) + 1 && f.sec <= Math.ceil(w.start + w.dur),
      );
      expect(inWin.length, `no frames inside the ${w.label} video window`).toBeGreaterThan(0);
      const max = Math.max(...inWin.map((f) => f.size));
      expect(
        max,
        `${w.label} embedded video appears BLANK (max frame ${max}B < ${BLANK_FLOOR}B floor)`,
      ).toBeGreaterThan(BLANK_FLOOR);
    }
  });
});
