import { spawnSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { type BrowserContext, type Page, type Video } from "@playwright/test";

export const PACE = Number(process.env.WEB_CHAT_PACE ?? "1");
export const MIN_DEMO_SECONDS = Number(process.env.KITSOKI_MIN_DEMO_SECONDS ?? "25");

const patchedRrwebContexts = new WeakSet<BrowserContext>();

export function rrwebCaptureOutPath(): string | null {
  const out = process.env.KITSOKI_RRWEB_OUT?.trim();
  return out ? path.resolve(out) : null;
}

function rrwebCaptureSidecarPath(out: string): string {
  return out.endsWith(".rrweb.json")
    ? out.replace(/\.rrweb\.json$/, ".rrweb.capture.json")
    : `${out}.capture.json`;
}

function rrwebBundlePath(): string {
  return path.resolve(import.meta.dirname, "..", "..", "node_modules", "rrweb", "dist", "rrweb.umd.min.cjs");
}

export async function maybeInstallAutoRrwebCapture(page: Page): Promise<void> {
  const out = rrwebCaptureOutPath();
  if (!out) return;

  const events: unknown[] = [];
  await page.exposeFunction("__kitsokiTuiRrwebEmit", (event: unknown) => {
    events.push(event);
  });

  await page.addScriptTag({ path: rrwebBundlePath() });
  await page.evaluate(() => {
    if (!(window as any).rrweb) {
      throw new Error("rrweb global was not installed");
    }
    if ((window as any).__kitsokiTuiRrwebStop) return;
    (window as any).__kitsokiTuiRrwebStop = (window as any).rrweb.record({
      emit(event: unknown) {
        (window as any).__kitsokiTuiRrwebEmit(event);
      },
      maskAllInputs: false,
      maskAllText: false,
      recordCanvas: false,
    });
  });

  const context = page.context();
  if (patchedRrwebContexts.has(context)) return;
  patchedRrwebContexts.add(context);

  const originalClose = context.close.bind(context);
  context.close = async (...args: Parameters<BrowserContext["close"]>) => {
    try {
      await page.evaluate(() => {
        const stop = (window as any).__kitsokiTuiRrwebStop;
        if (typeof stop === "function") stop();
      });
    } catch {
      // The page may already be gone if the test failed during navigation.
    }

    fs.mkdirSync(path.dirname(out), { recursive: true });
    fs.writeFileSync(
      out,
      JSON.stringify(
        {
          schema: "kitsoki-tui-rrweb/v1",
          created_at: new Date().toISOString(),
          source: "tools/tui-bridge",
          events,
        },
        null,
        2,
      ) + "\n",
    );
    fs.writeFileSync(
      rrwebCaptureSidecarPath(out),
      JSON.stringify(
        {
          schema: "kitsoki-tui-rrweb-capture/v1",
          created_at: new Date().toISOString(),
          rrweb_path: out,
          event_count: events.length,
          viewport: page.viewportSize(),
        },
        null,
        2,
      ) + "\n",
    );

    return originalClose(...args);
  };
}

export function cameraContext(opts: { recordVideoDir?: string } = {}) {
  const size = { width: 1600, height: 900 };
  return {
    viewport: size,
    deviceScaleFactor: 2,
    ...(opts.recordVideoDir && !rrwebCaptureOutPath() ? { recordVideo: { dir: opts.recordVideoDir, size } } : {}),
  };
}

export function dwell(page: Page, ms: number): Promise<void> {
  return page.waitForTimeout(Math.round(ms * PACE));
}

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

export function makeShot(artifactDir: string): (page: Page, label: string) => Promise<string> {
  fs.mkdirSync(artifactDir, { recursive: true });
  let n = 0;
  return async (page: Page, label: string): Promise<string> => {
    const file = path.join(artifactDir, `${String(++n).padStart(2, "0")}-${label}.png`);
    await page.screenshot({ path: file });
    return file;
  };
}

export function videoDurationSeconds(file: string): number | null {
  const r = spawnSync(
    "ffprobe",
    ["-v", "error", "-show_entries", "format=duration", "-of", "default=nw=1:nk=1", file],
    { encoding: "utf8" },
  );
  if (r.status !== 0) return null;
  const s = Number.parseFloat((r.stdout ?? "").trim());
  return Number.isFinite(s) ? s : null;
}

export interface SaveVideoOptions {
  trimStartMs?: number;
}

function ffmpegSeconds(ms: number): string {
  return (Math.max(0, ms) / 1000).toFixed(3);
}

export async function saveVideoAsMp4(
  video: Video | null,
  artifactDir: string,
  name: string,
  opts: SaveVideoOptions = {},
): Promise<string | null> {
  const rrwebOut = rrwebCaptureOutPath();
  if (rrwebOut) return rrwebOut;
  if (!video) return null;
  const gate = PACE === 0;
  const outName = gate ? `${name}.fast` : name;
  const raw = path.join(artifactDir, `${outName}-raw.webm`);
  const mp4 = path.join(artifactDir, `${outName}.mp4`);
  await video.saveAs(raw);
  const trimArgs = opts.trimStartMs && opts.trimStartMs > 0 ? ["-ss", ffmpegSeconds(opts.trimStartMs)] : [];
  const r = spawnSync(
    "ffmpeg",
    [
      "-y",
      "-loglevel",
      "error",
      "-i",
      raw,
      ...trimArgs,
      "-vf",
      "fps=30,scale=trunc(iw/2)*2:trunc(ih/2)*2",
      "-c:v",
      "libx264",
      "-preset",
      "slow",
      "-crf",
      "20",
      "-pix_fmt",
      "yuv420p",
      "-movflags",
      "+faststart",
      "-an",
      mp4,
    ],
    { encoding: "utf8" },
  );
  if (r.status !== 0) {
    const fallback = path.join(artifactDir, `${outName}.webm`);
    fs.renameSync(raw, fallback);
    return fallback;
  }
  fs.rmSync(raw, { force: true });
  if (!gate) {
    const secs = videoDurationSeconds(mp4);
    if (secs != null && secs < MIN_DEMO_SECONDS) {
      const short = path.join(artifactDir, `${name}.SHORT-${Math.round(secs)}s.mp4`);
      fs.renameSync(mp4, short);
      return short;
    }
  }
  return mp4;
}

export interface Chapter {
  index: number;
  id: string;
  label: string;
  start_ms: number;
  end_ms: number;
  source_ref: { kind: "tour"; spec_path: string; step_id: string };
}

export class ChapterRecorder {
  private readonly t0 = Date.now();
  private readonly chapters: Chapter[] = [];
  private open_: { id: string; label: string; specPath: string; startMs: number } | null = null;

  elapsedMs(): number {
    return Date.now() - this.t0;
  }

  open(stepId: string, label: string, specPath: string): void {
    this.close();
    this.open_ = { id: stepId, label, specPath, startMs: this.elapsedMs() };
  }

  close(): void {
    if (!this.open_) return;
    const o = this.open_;
    this.chapters.push({
      index: this.chapters.length,
      id: o.id,
      label: o.label,
      start_ms: o.startMs,
      end_ms: this.elapsedMs(),
      source_ref: { kind: "tour", spec_path: o.specPath, step_id: o.id },
    });
    this.open_ = null;
  }

  list(): Chapter[] {
    this.close();
    return this.chapters;
  }
}

export function shiftChapters(chapters: Chapter[], offsetMs: number): Chapter[] {
  const offset = Math.max(0, Math.round(offsetMs));
  return chapters.map((chapter, index) => ({
    ...chapter,
    index,
    start_ms: Math.max(0, chapter.start_ms - offset),
    end_ms: Math.max(0, chapter.end_ms - offset),
  }));
}

export function writeChapters(videoPath: string | null, chapters: Chapter[]): string | null {
  if (!videoPath || chapters.length === 0) return null;
  const sidecar = `${videoPath}.chapters.json`;
  fs.writeFileSync(sidecar, JSON.stringify(chapters, null, 2) + "\n");
  return sidecar;
}
