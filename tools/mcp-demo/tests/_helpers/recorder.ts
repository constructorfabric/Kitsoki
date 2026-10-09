/**
 * Recording primitives for the MCP terminal-demo harness — a self-contained trim
 * of the runstatus demo recorder (tools/runstatus/tests/playwright/_helpers/
 * server.ts), carrying ONLY the surface-agnostic video lifecycle so the artifact
 * is byte-compatible with the shared kitsoki-ui-qa gates:
 *
 *   • saveVideoAsMp4   — webm→H.264 mp4, the WEB_CHAT_PACE=0 `.fast.mp4` gate, and
 *                        the 25s duration floor (a short run-through is down-named,
 *                        never the canonical `<name>.mp4`).
 *   • ChapterRecorder  — the `<video>.chapters.json` sidecar pacing-scan.sh reads.
 *   • makeShot         — numbered per-beat PNGs the vision QA can consume directly.
 *
 * The kitsoki-web launcher bits of server.ts (BIN / startWebServer / demoAddr /
 * waitForState …) are deliberately absent: the terminal demo REPLAYS a committed
 * termcast cassette in xterm.js and never boots a kitsoki server, which is exactly
 * what makes the replay path no-LLM by construction. Keep these definitions in
 * lockstep with server.ts so a recording records identically on either surface.
 */
import { spawnSync } from "child_process";
import path from "path";
import fs from "fs";
import { type Page, type Video } from "@playwright/test";
import { profileSuffix } from "./camera.js";

/** Recording pace: 0 = fast assert-only validation, 1 = watch-speed (default). */
export const PACE = Number(process.env.WEB_CHAT_PACE ?? "1");

/** A fixed settle used after writes/navigations so a screenshot can't race them. */
export const SETTLE_MS = 1400;

/** Dwell scaled by WEB_CHAT_PACE so one spec validates fast and records slow. */
export function dwell(page: Page, ms: number): Promise<void> {
  return page.waitForTimeout(Math.round(ms * PACE));
}

/** Wipe + recreate the per-spec video dir so a stale `.webm` can't be picked up. */
export function prepareVideoDir(videoDir: string): void {
  const artifactDir = path.dirname(videoDir);
  if (fs.existsSync(artifactDir)) {
    for (const name of fs.readdirSync(artifactDir)) {
      if (/^\d{2}-.+\.png$/.test(name) || name === "ERROR.txt") {
        fs.rmSync(path.join(artifactDir, name), { force: true });
      }
    }
  }
  fs.rmSync(videoDir, { recursive: true, force: true });
  fs.mkdirSync(videoDir, { recursive: true });
}

/**
 * Save the Playwright-recorded video as a universally-playable H.264 MP4.
 * Mirrors server.ts/saveVideoAsMp4 exactly (so demos render identically on the
 * terminal surface): call AFTER context.close(), BEFORE browser.close().
 *
 *  - WEB_CHAT_PACE=0 fast runs write `<base>.fast.mp4`, never the canonical name.
 *  - A real-pace recording shorter than MIN_DEMO_SECONDS is down-named
 *    `<base>.SHORT-<n>s.mp4`; the canonical `<base>.mp4` stays absent.
 *  - ffmpeg failure falls back to `<base>.webm` so a recording is never lost.
 */
export async function saveVideoAsMp4(
  video: Video | null,
  artifactDir: string,
  name: string,
): Promise<string | null> {
  if (!video) return null;
  const suffix = profileSuffix();
  const base = `${name}${suffix}`;
  const gate = PACE === 0;
  const outName = gate ? `${base}.fast` : base;
  if (gate) {
    console.warn(
      `[video] WEB_CHAT_PACE=0 (fast run): saving collapsed-timing video to ${outName}.mp4 — ` +
        `this is NOT the watch-speed cut. Re-run without WEB_CHAT_PACE=0 to produce ${base}.mp4.`,
    );
  }
  const raw = path.join(artifactDir, `${outName}-raw.webm`);
  const mp4 = path.join(artifactDir, `${outName}.mp4`);
  try {
    await video.saveAs(raw);
  } catch (e) {
    console.warn(`[video] saveAs failed: ${e}`);
    return null;
  }
  const vf = "fps=30,scale=trunc(iw/2)*2:trunc(ih/2)*2";
  const r = spawnSync(
    "ffmpeg",
    ["-y", "-loglevel", "error", "-i", raw, "-vf", vf,
      "-c:v", "libx264", "-preset", "slow", "-crf", "20",
      "-pix_fmt", "yuv420p", "-movflags", "+faststart", "-an", mp4],
    { encoding: "utf8" },
  );
  if (r.status !== 0) {
    const fallback = path.join(artifactDir, `${outName}.webm`);
    fs.renameSync(raw, fallback);
    console.warn(`[video] ffmpeg mp4 transcode failed; using raw webm\n${r.stderr?.slice(0, 400)}`);
    return fallback;
  }
  fs.unlinkSync(raw);
  if (!gate) {
    const secs = videoDurationSeconds(mp4);
    if (secs != null && secs < MIN_DEMO_SECONDS) {
      const short = path.join(artifactDir, `${base}.SHORT-${Math.round(secs)}s.mp4`);
      fs.renameSync(mp4, short);
      console.warn(
        `[video] ⚠ ${path.basename(short)} is only ${secs.toFixed(0)}s ` +
        `(< ${MIN_DEMO_SECONDS}s) — looks like a fast run-through, NOT a user-facing ` +
        `demo. Increase per-beat dwell (and/or WEB_CHAT_PACE) and re-record. ` +
        `The canonical ${base}.mp4 was NOT written.`,
      );
      return short;
    }
  }
  console.log(`[video] ${mp4}`);
  return mp4;
}

/** A real demo should be substantial; shorter record-mode runs are down-named. */
export const MIN_DEMO_SECONDS = Number(process.env.KITSOKI_MIN_DEMO_SECONDS ?? "25");

/** Probe a video's duration (seconds) via ffprobe, or null if unavailable. */
export function videoDurationSeconds(file: string): number | null {
  const r = spawnSync(
    "ffprobe",
    ["-v", "error", "-show_entries", "format=duration", "-of", "default=nw=1:nk=1", file],
    { encoding: "utf8" },
  );
  if (r.status !== 0) return null;
  const s = parseFloat((r.stdout ?? "").trim());
  return Number.isFinite(s) ? s : null;
}

// ── Chapter sidecar — identical shape to server.ts / internal/video.Chapter ──

/** Names the producing unit a chapter came from. kind is always "tour". */
export interface ChapterSourceRef {
  kind: "tour";
  spec_path: string;
  step_id: string;
  line?: number;
}

/** One [start_ms, end_ms) window mapped back to its beat. */
export interface Chapter {
  index: number;
  id: string;
  label: string;
  start_ms: number;
  end_ms: number;
  source_ref: ChapterSourceRef;
}

/**
 * Accumulates per-beat time windows during the recording. Construct it the moment
 * recording starts; `open(id,…)` as each beat begins and `close()` when it ends.
 * Elapsed wall-clock since construction is the video timeline, so windows line up
 * with the recorded MP4 (this is the ground truth pacing-scan.sh reads).
 */
export class ChapterRecorder {
  private readonly t0 = Date.now();
  private readonly chapters: Chapter[] = [];
  private open_: { id: string; label: string; specPath: string; line?: number; startMs: number } | null = null;

  open(stepId: string, label: string, specPath: string, line?: number): void {
    this.close();
    this.open_ = { id: stepId, label, specPath, line, startMs: Date.now() - this.t0 };
  }

  close(): void {
    if (!this.open_) return;
    const o = this.open_;
    this.chapters.push({
      index: this.chapters.length,
      id: o.id,
      label: o.label,
      start_ms: o.startMs,
      end_ms: Date.now() - this.t0,
      source_ref: { kind: "tour", spec_path: o.specPath, step_id: o.id, ...(o.line ? { line: o.line } : {}) },
    });
    this.open_ = null;
  }

  list(): Chapter[] {
    this.close();
    return this.chapters;
  }
}

/** Write `<video>.chapters.json` beside the mp4; null when no video / no chapters. */
export function writeChapters(videoPath: string | null, chapters: Chapter[]): string | null {
  if (!videoPath || chapters.length === 0) return null;
  const sidecar = `${videoPath}.chapters.json`;
  fs.writeFileSync(sidecar, JSON.stringify(chapters, null, 2) + "\n");
  console.log(`[chapters] ${sidecar} (${chapters.length})`);
  return sidecar;
}

/** Numbered per-beat screenshots (`NN-label.png`) for the vision QA `--frames`. */
export function makeShot(artifactDir: string): (page: Page, label: string) => Promise<string> {
  fs.mkdirSync(artifactDir, { recursive: true });
  let n = 0;
  return async (page: Page, label: string): Promise<string> => {
    const file = path.join(artifactDir, `${String(++n).padStart(2, "0")}-${label}.png`);
    await page.screenshot({ path: file });
    return file;
  };
}
