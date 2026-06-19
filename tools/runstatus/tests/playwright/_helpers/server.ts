/**
 * Shared lifecycle + recording harness for the LIVE Playwright specs that drive
 * a real `kitsoki web` server in the deterministic no-LLM posture
 * (`--flow <fixture>`, nil harness). Distinct from artifact.ts, which loads
 * static file:// snapshots with no server.
 *
 * Used by oregon-trail-e2e.spec.ts and tour-video.spec.ts.
 *
 * IMPORTANT: the binary serves the SPA via go:embed, so a fresh UI requires
 * `make build && cp ./kitsoki bin/kitsoki` before recording — an un-rebuilt
 * bin/kitsoki serves a stale bundle.
 */
import { spawn, spawnSync, type ChildProcess } from "child_process";
import { fileURLToPath } from "url";
import path from "path";
import fs from "fs";
import os from "os";
import { expect, type Page, type Video, type Locator } from "@playwright/test";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
// _helpers → playwright → tests → runstatus → tools → kitsoki (repo root)
export const repoRoot = path.resolve(__dirname, "../../../../..");
export const BIN = path.join(repoRoot, "bin", "kitsoki");
export const STORIES_DIR = path.join(repoRoot, "stories");

/**
 * `go run` vs. a built binary. The rule: `go run ./cmd/kitsoki` for LOCAL DEV /
 * TESTING (these recordings, iterating on a spec) — it always tracks the working
 * tree, so there's no stale-binary trap and nothing to copy into bin/; a REAL
 * BINARY for an actual client/CI case (faster, what ships). Resolution:
 *   - KITSOKI_WEB_GO_RUN=1 / =0 forces go run / binary explicitly;
 *   - otherwise prefer bin/kitsoki when it exists (built flows / CI stay fast),
 *     and fall back to go run when it doesn't (local dev just works).
 * Either way the go:embed'd SPA must be staged first (`make web`); go run reads
 * internal/runstatus/web/assets/index.html at compile time.
 */
export const GO_RUN =
  process.env.KITSOKI_WEB_GO_RUN !== undefined
    ? process.env.KITSOKI_WEB_GO_RUN !== "0"
    : !fs.existsSync(BIN);

/** Global pacing knob: 0 for fast assertion runs, 1 (default) for the camera. */
export const PACE = Number(process.env.WEB_CHAT_PACE ?? "1");

/**
 * Default "settle" beat (ms, before PACE-scaling) after a surface change.
 *
 * The eye needs roughly a second to register a freshly-rendered view. Tour
 * steps already dwell on each spotlight, but the OPENING orchestration (home ->
 * new session -> observer) and any pre-step setup must settle too, or the
 * camera lurches between surfaces in under a second — the "rushed navigation
 * outside the tour" defect. Keep this on the same `PACE` knob so fast assertion
 * runs (WEB_CHAT_PACE=0) collapse it to zero.
 */
export const SETTLE_MS = 1400;

/** Pause for `ms` (PACE-scaled). The single pacing primitive every recording
 * spec shares — previously each spec redefined its own copy, so the opening
 * navigation kept getting written without one. Import this instead. */
export function dwell(page: Page, ms: number): Promise<void> {
  return page.waitForTimeout(Math.round(ms * PACE));
}

/**
 * Navigate to `url`, confirm the surface has actually rendered (optional URL
 * regex and/or testid anchor), then SETTLE so the frame is watchable.
 *
 * This is the camera-move primitive: replaces a bare `page.goto` in the
 * recording specs so non-tour navigation is paced exactly like the tour itself.
 * The settle is the whole point — a `goto` that immediately `goto`s away (the
 * old home -> chat -> observer flash) never gives the viewer a frame to read.
 */
export async function cinematicGoto(
  page: Page,
  url: string,
  opts: { waitForUrl?: RegExp; waitForTestId?: string; settleMs?: number } = {},
): Promise<void> {
  await page.goto(url);
  if (opts.waitForUrl) await page.waitForURL(opts.waitForUrl, { timeout: 15000 });
  if (opts.waitForTestId) {
    await expect(page.getByTestId(opts.waitForTestId).first()).toBeVisible({ timeout: 15000 });
  }
  await dwell(page, opts.settleMs ?? SETTLE_MS);
}

/**
 * Click `target` with a cinematic beat BEFORE (so the cursor's intent reads) and
 * a SETTLE after (so the resulting surface is held on screen). Use for the
 * opening orchestration clicks that sit outside the paced tour loop — e.g. the
 * "New session" button — which otherwise fire instantly and flash past.
 */
export async function pacedClick(
  page: Page,
  target: Locator,
  opts: { beforeMs?: number; afterMs?: number } = {},
): Promise<void> {
  await dwell(page, opts.beforeMs ?? 600);
  await target.click();
  await dwell(page, opts.afterMs ?? SETTLE_MS);
}

export interface WebServer {
  base: string;
  rpc<T>(method: string, params: Record<string, unknown>): Promise<T>;
  log(): string;
  stop(): void;
}

async function waitForHealthy(
  base: string,
  timeoutMs: number,
  log: () => string,
): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  let lastErr = "";
  while (Date.now() < deadline) {
    try {
      const res = await fetch(`${base}/`, { method: "GET" });
      if (res.status === 200) return;
      lastErr = `status ${res.status}`;
    } catch (e) {
      lastErr = e instanceof Error ? e.message : String(e);
    }
    await new Promise((r) => setTimeout(r, 200));
  }
  throw new Error(
    `server not healthy after ${timeoutMs}ms (last: ${lastErr})\n--- server log ---\n${log()}`,
  );
}

/** Spawn `kitsoki web` with a story flow fixture and wait until it's healthy. */
export async function startWebServer(opts: {
  addr: string;
  /** Nil-harness flow fixture (explicit intents). Omit when using `harness`. */
  flow?: string;
  storiesDir?: string;
  /** Optional host cassette path. Combinable with `flow` (nil-harness) OR with
   *  `harness: "replay"` (free-text routed by the recording, host calls from the
   *  cassette — the deterministic no-LLM free-text posture). */
  hostCassette?: string;
  /** Optional .kitsoki.yaml path (--config), e.g. to declare harness_profiles. */
  config?: string;
  /** Harness for free-text routing, e.g. "replay" (with `recording`). */
  harness?: string;
  /** Recording YAML for --harness replay (deterministic, hand-authorable). */
  recording?: string;
  /** Execution mode: "one-shot" (auto-advance synthetic emit chains through
   *  decision gates — needed for an autonomous in-story loop) or "staged"
   *  (default). */
  mode?: string;
  /** Optional extra env merged into the spawned server (e.g. a dummy
   *  SYNTHETIC_API_KEY so a harness_profiles fixture's ${VAR} resolves). */
  extraEnv?: Record<string, string>;
}): Promise<WebServer> {
  const storiesDir = opts.storiesDir ?? STORIES_DIR;
  const checkPaths = [storiesDir];
  if (opts.flow) checkPaths.push(opts.flow);
  if (opts.recording) checkPaths.push(opts.recording);
  if (!GO_RUN) checkPaths.push(BIN);
  if (opts.hostCassette) checkPaths.push(opts.hostCassette);
  if (opts.config) checkPaths.push(opts.config);
  for (const p of checkPaths) {
    if (!fs.existsSync(p)) {
      const hint = p === BIN ? " (run 'make build && cp ./kitsoki bin/kitsoki', or unset KITSOKI_WEB_GO_RUN to use go run)" : "";
      throw new Error(`missing required path: ${p}${hint}`);
    }
  }

  const tmpDbDir = fs.mkdtempSync(path.join(os.tmpdir(), "kitsoki-pw-"));
  const dbPath = path.join(tmpDbDir, "s.db");
  let serverLog = "";

  const args = ["web", "--stories-dir", storiesDir, "--addr", opts.addr, "--db", dbPath];
  if (opts.flow) args.push("--flow", opts.flow);
  if (opts.harness) args.push("--harness", opts.harness);
  if (opts.recording) args.push("--recording", opts.recording);
  if (opts.mode) args.push("--mode", opts.mode);
  if (opts.hostCassette) args.push("--host-cassette", opts.hostCassette);
  if (opts.config) args.push("--config", opts.config);

  // Slow-play passthrough (opt-in): when the RECORDING process has
  // KITSOKI_CASSETTE_SLOWPLAY set, forward it to the spawned server so a
  // `KITSOKI_CASSETTE_SLOWPLAY=1 pnpm exec playwright test <spec>` run records
  // the cassette REPLAY streaming its agent-action transcript live (paced by
  // recorded timings) into the web turn-stream. An UNSET run inherits nothing
  // here, so the default `playwright test` posture stays instant + deterministic
  // (CLAUDE.md: tests must not slow down or become non-deterministic by default).
  const childEnv: Record<string, string | undefined> = { ...process.env };
  if (process.env.KITSOKI_CASSETTE_SLOWPLAY !== undefined) {
    childEnv.KITSOKI_CASSETTE_SLOWPLAY = process.env.KITSOKI_CASSETTE_SLOWPLAY;
  }
  if (opts.extraEnv) Object.assign(childEnv, opts.extraEnv);

  // go run ./cmd/kitsoki <args>  vs.  bin/kitsoki <args>. In go-run mode the
  // first request may have to wait on a compile, so allow a longer health
  // window; the build cache makes it a few seconds in practice.
  const cmd = GO_RUN ? "go" : BIN;
  const cmdArgs = GO_RUN ? ["run", "./cmd/kitsoki", ...args] : args;
  const proc: ChildProcess = spawn(
    cmd,
    cmdArgs,
    // detached so go run's compiled child shares a killable process group (a
    // bare proc.kill() would orphan it). stop() kills the whole group.
    { cwd: repoRoot, stdio: ["ignore", "pipe", "pipe"], env: childEnv, detached: GO_RUN },
  );
  proc.stdout?.on("data", (d: Buffer) => (serverLog += d.toString()));
  proc.stderr?.on("data", (d: Buffer) => (serverLog += d.toString()));
  proc.on("exit", (code, sig) => (serverLog += `\n[server exited code=${code} sig=${sig}]\n`));

  const base = `http://${opts.addr}`;
  await waitForHealthy(base, GO_RUN ? 90000 : 20000, () => serverLog);

  return {
    base,
    async rpc<T>(method: string, params: Record<string, unknown>): Promise<T> {
      const res = await fetch(`${base}/rpc`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ jsonrpc: "2.0", id: 1, method, params }),
      });
      const body = (await res.json()) as { result?: T; error?: { message: string } };
      if (body.error) throw new Error(`${method} failed: ${body.error.message}`);
      return body.result as T;
    },
    log: () => serverLog,
    stop(): void {
      // go run mode: kill the whole process group so the compiled child dies
      // with the `go run` parent (else it lingers holding the port).
      if (GO_RUN && proc.pid) {
        try {
          process.kill(-proc.pid, "SIGKILL");
        } catch {
          proc.kill("SIGKILL");
        }
      } else {
        proc.kill();
      }
      fs.rmSync(tmpDbDir, { recursive: true, force: true });
    },
  };
}

/**
 * Returns a screenshot helper that writes numbered `NN-<label>.png` into
 * artifactDir (cleared of stale PNGs first). The contact-sheet / render scripts
 * consume this numbering.
 */
export function makeShot(artifactDir: string): (page: Page, label: string) => Promise<string> {
  fs.mkdirSync(artifactDir, { recursive: true });
  for (const f of fs.readdirSync(artifactDir)) {
    if (f.endsWith(".png")) fs.rmSync(path.join(artifactDir, f));
  }
  let idx = 0;
  return async (page: Page, label: string): Promise<string> => {
    const n = String(++idx).padStart(2, "0");
    const p = path.join(artifactDir, `${n}-${label}.png`);
    await page.screenshot({ path: p });
    return p;
  };
}

/** Assert the interactive view's current-state reaches `state`. */
export async function waitForState(
  page: Page,
  state: string,
  timeoutMs = 12000,
): Promise<void> {
  await expect(page.getByTestId("current-state")).toHaveText(state, { timeout: timeoutMs });
}

/**
 * Poll the trace RPC until at least `minCount` `oracle.call.complete` events are
 * present, so a tour never starts spotlighting trace rows before the SSE stream
 * has actually pushed them. A deterministic replacement for a flat
 * `page.waitForTimeout` — the events arrive on wall-clock-variable SSE timing,
 * so a fixed sleep is a flicker/rushed-frame risk under load. Mirrors the golden
 * agent-actions spec's `waitForOracleTranscripts`.
 *
 * Resolves once the count is met; throws on timeout with the last seen count so
 * a failed recording is diagnosable. Pass `requireTranscriptRef` to additionally
 * gate on a `transcript_ref` attr (needed before the agent-actions drawer steps,
 * where the affordance only renders for transcript-bearing calls).
 */
export async function waitForOracleComplete(
  server: WebServer,
  sessionId: string,
  minCount: number,
  timeoutMs: number,
  requireTranscriptRef = false,
): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  let seen = 0;
  while (Date.now() < deadline) {
    const tr = await server
      .rpc<{ events?: Array<{ msg: string; attrs?: Record<string, unknown> }> }>(
        "runstatus.session.trace",
        { session_id: sessionId },
      )
      .catch(() => ({ events: [] as Array<{ msg: string; attrs?: Record<string, unknown> }> }));
    seen = (tr.events ?? []).filter(
      (e) =>
        e.msg === "oracle.call.complete" &&
        (!requireTranscriptRef || !!(e.attrs && e.attrs["transcript_ref"])),
    ).length;
    if (seen >= minCount) return;
    await new Promise((r) => setTimeout(r, 400));
  }
  throw new Error(
    `oracle events never settled: only ${seen}/${minCount} oracle.call.complete` +
      `${requireTranscriptRef ? " with transcript_ref" : ""} after ${timeoutMs}ms`,
  );
}

/**
 * Prepare a fresh VIDEO_DIR for a recording run.
 *
 * Must be called in beforeAll (or at the top of the test) BEFORE the Playwright
 * context is created with `recordVideo: { dir: videoDir }`. Clears any stale
 * .webm files from previous runs so `saveVideoAsMp4` always picks the right
 * file and so VIDEO_DIR never silently fills up across CI runs.
 */
export function prepareVideoDir(videoDir: string): void {
  fs.rmSync(videoDir, { recursive: true, force: true });
  fs.mkdirSync(videoDir, { recursive: true });
}

/**
 * Save the Playwright-recorded video as a universally-playable H.264 MP4.
 *
 * ALWAYS emit MP4, never `.webm`. Playwright records VP8 `.webm`, which (a)
 * omits the DURATION/CUES container atoms so most players show only the first
 * frame, and (b) does not play inline in VS Code, Keynote, Slack, or iMessage.
 * An H.264 + `yuv420p` + `+faststart` MP4 plays everywhere — including the VS
 * Code editor preview. So the canonical recording artifact is the `.mp4`; the
 * intermediate `.webm` is transcoded away.
 *
 * Call this AFTER `context.close()` (which finalises the video) but BEFORE
 * `browser.close()`. Steps:
 *
 *   1. `video.saveAs(raw)` — copies THIS page's `.webm` from VIDEO_DIR to a known
 *      path, avoiding the "alphabetically first stale file" trap.
 *   2. ffmpeg transcode → `<name>.mp4` with the same settings as
 *      `scripts/webm-to-mp4.sh` (libx264 / preset slow / crf 20 / yuv420p /
 *      +faststart / 30fps / even dims, audio dropped). The transcode also fixes
 *      the missing-atoms problem inherently.
 *   3. Removes the raw `.webm` on success. On ffmpeg failure, falls back to the
 *      `.webm` (renamed in place) so a recording is never silently lost.
 *
 * Returns the final stable path (`.mp4`, or `.webm` only on ffmpeg failure).
 */
export async function saveVideoAsMp4(
  video: Video | null,
  artifactDir: string,
  name: string,
): Promise<string | null> {
  if (!video) return null;
  const raw = path.join(artifactDir, `${name}-raw.webm`);
  const mp4 = path.join(artifactDir, `${name}.mp4`);
  try {
    await video.saveAs(raw);
  } catch (e) {
    console.warn(`[video] saveAs failed: ${e}`);
    return null;
  }
  // Mirror scripts/webm-to-mp4.sh so in-spec and manual conversions match.
  const vf = "fps=30,scale=trunc(iw/2)*2:trunc(ih/2)*2";
  const r = spawnSync(
    "ffmpeg",
    ["-y", "-loglevel", "error", "-i", raw, "-vf", vf,
      "-c:v", "libx264", "-preset", "slow", "-crf", "20",
      "-pix_fmt", "yuv420p", "-movflags", "+faststart", "-an", mp4],
    { encoding: "utf8" },
  );
  if (r.status === 0) {
    fs.unlinkSync(raw);
    console.log(`[video] ${mp4}`);
    return mp4;
  }
  // ffmpeg failed — promote the raw webm as the fallback so we never lose it.
  const fallback = path.join(artifactDir, `${name}.webm`);
  fs.renameSync(raw, fallback);
  console.warn(`[video] ffmpeg mp4 transcode failed; using raw webm\n${r.stderr?.slice(0, 400)}`);
  return fallback;
}

// ── Chapter sidecar (mockup-video-studio epic, Slice 1) ─────────────────────
//
// The recorder emits the SAME producer-agnostic chapter sidecar shape that
// host.slidey.render writes from the Go side (internal/video.Chapter), so the
// slice-2 feedback panel reads one uniform chapter list regardless of whether
// a video came from slidey or a tour walkthrough (epic shared decision 1).
//
// The schema is intentionally duplicated here in JS rather than shared — the
// recorder already owns its dwell windows, and a checked-in JSON shape keeps
// the two producers honest (proposal open question 2). Keep these fields in
// lockstep with internal/video.Chapter / SourceRef.

/** Names the producing unit a tour chapter came from. kind is always "tour". */
export interface ChapterSourceRef {
  kind: "tour";
  spec_path: string;
  step_id: string;
  line?: number;
}

/** One [start_ms, end_ms) window mapped back to its tour step. */
export interface Chapter {
  index: number;
  id: string;
  label: string;
  start_ms: number;
  end_ms: number;
  source_ref: ChapterSourceRef;
}

/**
 * Accumulates per-step time windows during a tour walkthrough recording.
 *
 * Construct it at the moment recording starts (right after the context is
 * created), then call `open(stepId, ...)` as each step's spotlight settles and
 * `close()` when the walk moves on. The elapsed wall-clock since construction
 * is the video timeline, so the windows line up with the recorded MP4.
 */
export class ChapterRecorder {
  private readonly t0 = Date.now();
  private readonly chapters: Chapter[] = [];
  private open_: { id: string; label: string; specPath: string; line?: number; startMs: number } | null = null;

  /** Begin a chapter for `stepId`. Closes any currently-open chapter first. */
  open(stepId: string, label: string, specPath: string, line?: number): void {
    this.close();
    this.open_ = { id: stepId, label, specPath, line, startMs: Date.now() - this.t0 };
  }

  /** Close the current chapter, sealing its end at the current elapsed time. */
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

  /** The collected chapters (closes any open one first). */
  list(): Chapter[] {
    this.close();
    return this.chapters;
  }
}

/**
 * Write a chapter sidecar beside a rendered video as `<video>.chapters.json`
 * (epic cross-cutting Q1 — sibling file), matching internal/video.SidecarPath.
 * Returns the sidecar path, or null when there is no video / no chapters.
 */
export function writeChapters(videoPath: string | null, chapters: Chapter[]): string | null {
  if (!videoPath || chapters.length === 0) return null;
  const sidecar = `${videoPath}.chapters.json`;
  fs.writeFileSync(sidecar, JSON.stringify(chapters, null, 2) + "\n");
  console.log(`[chapters] ${sidecar} (${chapters.length})`);
  return sidecar;
}
