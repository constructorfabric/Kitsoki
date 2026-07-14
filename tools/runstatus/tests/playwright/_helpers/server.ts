/**
 * Shared lifecycle + recording harness for the LIVE Playwright specs that drive
 * a real `kitsoki web` server in the deterministic no-LLM posture
 * (`--flow <fixture>`, nil harness). Distinct from artifact.ts, which loads
 * static file:// snapshots with no server.
 *
 * Used by oregon-trail-e2e.spec.ts and tour-video.spec.ts.
 *
 * IMPORTANT: the binary serves the SPA via go:embed, so a fresh UI requires
 * `make build-bin` before recording — an un-rebuilt
 * bin/kitsoki serves a stale bundle.
 */
import { spawn, spawnSync, type ChildProcess } from "child_process";
import { fileURLToPath } from "url";
import path from "path";
import fs from "fs";
import os from "os";
import net from "net";
import { expect, type BrowserContext, type Page, type Video, type Locator } from "@playwright/test";
import { profileSuffix, activeProfile } from "./camera.js";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
// _helpers → playwright → tests → runstatus → tools → kitsoki (repo root)
export const repoRoot = path.resolve(__dirname, "../../../../..");
export const BIN = path.join(repoRoot, "bin", "kitsoki");
export const STORIES_DIR = path.join(repoRoot, "stories");

/**
 * The loopback address a demo spec's server binds to. `basePort` is the spec's
 * stable port; this is the ONE place a port is computed — no spec hardcodes a
 * raw `127.0.0.1:NNNN`.
 *
 * Two shifts keep concurrent recordings from colliding:
 *   - `KITSOKI_DEMO_PORT_BASE` (default 0) shifts EVERY demo's port, so another
 *     session / worktree can claim a free range and stop squatting yours (the
 *     bug that left trace-features a 3s cut for a whole session);
 *   - the active camera profile's `portShift`, so parallel profile passes of one
 *     spec (desktop + mobile) never land on the same port.
 */
export function demoAddr(basePort: number): string {
  const shift = Number(process.env.KITSOKI_DEMO_PORT_BASE ?? "0");
  return `127.0.0.1:${basePort + shift + activeProfile().portShift}`;
}

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

interface AutoRrwebState {
  events: unknown[];
  viewport: { width: number; height: number; deviceScaleFactor: number } | null;
  exposed: boolean;
  patched: boolean;
}

const autoRrwebByContext = new WeakMap<BrowserContext, AutoRrwebState>();

export function rrwebCaptureOutPath(): string | null {
  const out = process.env.KITSOKI_RRWEB_OUT;
  if (!out) return null;
  return path.resolve(repoRoot, out);
}

/**
 * When KITSOKI_RRWEB_OUT is set, install a Node-backed rrweb recorder on the
 * page and patch context.close() so existing tour specs emit rrweb without
 * adding a parallel capture spec. The Node accumulator survives hard reloads:
 * a later maybeInstallAutoRrwebCapture() call starts a fresh rrweb recorder in
 * the new document and appends into the same event stream.
 */
export async function maybeInstallAutoRrwebCapture(page: Page): Promise<void> {
  const out = rrwebCaptureOutPath();
  if (!out || page.isClosed()) return;

  const context = page.context();
  let state = autoRrwebByContext.get(context);
  if (!state) {
    state = { events: [], viewport: null, exposed: false, patched: false };
    autoRrwebByContext.set(context, state);
  }

  if (!state.exposed) {
    await context.exposeFunction("__kitsokiAutoRrwebEmit", (event: unknown) => {
      state!.events.push(event);
    });
    state.exposed = true;
  }

  if (!state.patched) {
    const originalClose = context.close.bind(context);
    context.close = (async (...args: Parameters<BrowserContext["close"]>) => {
      await writeAutoRrwebCapture(context).catch((err) => {
        console.warn(`[rrweb] failed to write ${out}: ${err instanceof Error ? err.message : String(err)}`);
      });
      return originalClose(...args);
    }) as BrowserContext["close"];
    state.patched = true;
  }

  const alreadyRecording = await page
    .evaluate(() => Boolean((window as unknown as { __kitsokiAutoRrwebRecording?: boolean }).__kitsokiAutoRrwebRecording))
    .catch(() => false);
  if (alreadyRecording) return;

  const { RRWEB_BUNDLE } = await import("./rrweb-replay.js");
  await page.addScriptTag({ path: RRWEB_BUNDLE });
  const viewport = await page.evaluate(() => ({
    width: window.innerWidth,
    height: window.innerHeight,
    deviceScaleFactor: window.devicePixelRatio || 1,
  }));
  if (!state.viewport) state.viewport = viewport;
  await page.evaluate(() => {
    const w = window as unknown as {
      rrweb?: { record: (o: unknown) => (() => void) | undefined };
      __kitsokiAutoRrwebRecording?: boolean;
      __kitsokiAutoRrwebStop?: () => void;
      __kitsokiAutoRrwebEmit?: (event: unknown) => void;
    };
    if (w.__kitsokiAutoRrwebRecording) return;
    if (!w.rrweb || typeof w.rrweb.record !== "function") {
      throw new Error("rrweb global missing record()");
    }
    const stop = w.rrweb.record({
      emit: (event: unknown) => w.__kitsokiAutoRrwebEmit?.(event),
      maskAllText: false,
      maskAllInputs: false,
      recordCanvas: false,
    });
    w.__kitsokiAutoRrwebStop = stop ?? (() => undefined);
    w.__kitsokiAutoRrwebRecording = true;
  });
}

async function writeAutoRrwebCapture(context: BrowserContext): Promise<void> {
  const out = rrwebCaptureOutPath();
  if (!out) return;
  const state = autoRrwebByContext.get(context);
  if (!state || state.events.length === 0) return;
  const { writeEvents } = await import("./rrweb-replay.js");
  writeEvents(state.events as Record<string, unknown>[], out, state.viewport ?? undefined);
  console.log(`[rrweb] ${out} (${state.events.length} events)`);
}

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
 * Full-screen a produced markdown artifact and ease-scroll it top→bottom, then
 * close — the demo-target "every artifact is full-screened via the modal to show
 * the full content" beat, reused by every phase capture (PRD, design,
 * decomposition, bugfix summary, PR summary).
 *
 * Driven through the global `__openArtifact` hook (ArtifactModal, mounted in
 * App.vue) which full-screens MarkdownModal on the given `.md` path (read via
 * runstatus.file.read). The dwells and the scroll are FIXED (not PACE-scaled):
 * the conversation is captured lean (WEB_CHAT_PACE=0) and the readable dwells are
 * added deterministically by `slidey rrweb-repace`, but the document read-through
 * must stay smooth/legible regardless of capture pace.
 */
export async function showArtifact(
  page: Page,
  artifactPath: string,
  opts: { scrollMs?: number; topDwellMs?: number; endDwellMs?: number } = {},
): Promise<void> {
  const scrollMs = opts.scrollMs ?? 5200;
  const topDwellMs = opts.topDwellMs ?? 1600;
  const endDwellMs = opts.endDwellMs ?? 1600;
  await page.evaluate((p) => {
    (window as unknown as { __openArtifact?: (s: string) => void }).__openArtifact?.(p);
  }, artifactPath);
  await expect(page.getByTestId("markdown-modal")).toBeVisible({ timeout: 8000 });
  // Wait for the markdown to actually render (the modal fetches the file via RPC)
  // and to be tall enough to scroll.
  await page.waitForFunction(() => {
    const el = document.querySelector('[data-testid="markdown-modal-body"] .mm-md') as HTMLElement | null;
    return !!el && el.scrollHeight > el.clientHeight - 1;
  }, undefined, { timeout: 8000 });
  await page.waitForTimeout(topDwellMs); // read the top of the document
  await page.evaluate(async (ms) => {
    const el = document.querySelector('[data-testid="markdown-modal-body"]') as HTMLElement | null;
    if (!el) return;
    const max = el.scrollHeight - el.clientHeight;
    if (max <= 2) return;
    const from = el.scrollTop;
    const t0 = performance.now();
    await new Promise<void>((res) => {
      const tick = (now: number) => {
        const p = Math.min(1, (now - t0) / ms);
        const eased = p < 0.5 ? 2 * p * p : 1 - Math.pow(-2 * p + 2, 2) / 2;
        el.scrollTop = from + (max - from) * eased;
        if (p < 1) requestAnimationFrame(tick);
        else res();
      };
      requestAnimationFrame(tick);
    });
  }, scrollMs);
  await page.waitForTimeout(endDwellMs); // rest on the end of the document
  await page.evaluate(() => {
    (window as unknown as { __closeArtifact?: () => void }).__closeArtifact?.();
  });
  await expect(page.getByTestId("markdown-modal")).toHaveCount(0, { timeout: 5000 });
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
  await maybeInstallAutoRrwebCapture(page);
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

/**
 * Fast "is this TCP port free" probe (no shelling out) — tries to bind it and
 * immediately releases it. Resolves `true` iff the bind+listen succeeded.
 * Any bind error (EADDRINUSE or otherwise) is treated as "not free"; callers
 * that need to know WHY fall back to `lsof` (see `reapStaleKitsokiOnPort`).
 */
function isPortFree(host: string, port: number): Promise<boolean> {
  return new Promise((resolve) => {
    const srv = net.createServer();
    srv.once("error", () => resolve(false));
    srv.once("listening", () => srv.close(() => resolve(true)));
    srv.listen(port, host);
  });
}

/** Split a `host:port` addr (this repo always uses bare loopback IPv4, e.g.
 *  `127.0.0.1:7799` — no IPv6/hostname support needed here). */
function parseAddr(addr: string): { host: string; port: number } {
  const idx = addr.lastIndexOf(":");
  return { host: addr.slice(0, idx), port: Number(addr.slice(idx + 1)) };
}

/** Poll until `pid` no longer exists (via the zero-signal `kill` probe), or
 *  `timeoutMs` elapses. Returns whether it died in time. */
async function waitForPidExit(pid: number, timeoutMs: number): Promise<boolean> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      process.kill(pid, 0);
    } catch {
      return true; // ESRCH: no such process — it's gone.
    }
    await new Promise((r) => setTimeout(r, 100));
  }
  try {
    process.kill(pid, 0);
    return false;
  } catch {
    return true;
  }
}

/**
 * Before spawning, check whether `addr`'s port is already held by a leftover
 * `kitsoki web` process from a prior (usually failed/killed) test run.
 *
 * Rationale: Playwright's own process-kill on a failed run doesn't always reap
 * the full `go run` → compiled-binary process tree, so the NEXT run against
 * the same fixed port sees either a bind failure or a hung/non-matching
 * server — both of which read exactly like a bug in the feature under test.
 * This makes that specific failure mode self-healing (best-effort) and, when
 * it can't be safely healed, gives an unambiguous error instead of a
 * misleading one.
 *
 * Safety: this will ONLY ever signal a process whose command line matches
 * `kitsoki web` as an executable+subcommand pair — i.e. the token "kitsoki"
 * immediately followed by the "web" subcommand (covers `go run ./cmd/kitsoki
 * web ...`, the built `bin/kitsoki web`, and go's `go-build` temp
 * "exe/kitsoki web" compiled-child path). This is deliberately NARROWER than a
 * bare substring match on "kitsoki" — this repo's own checkout directory is
 * named "Kitsoki", so anything spawned from inside it (including the test
 * runner's own process) has that word in its command line; matching just the
 * word would risk mistaking an unrelated process (or the test runner itself)
 * for a stale server. Any occupant that doesn't match this pair, or a PID we
 * can't confidently identify, is left alone.
 *
 * Returns a human-readable note describing the recovery action taken (to be
 * prepended to a later health-check error for context), or `null` when the
 * port was already free and nothing happened.
 *
 * Throws when the port is occupied and cannot be (or should not be) reaped,
 * so the caller never proceeds to spawn against a still-busy port.
 */
async function reapStaleKitsokiOnPort(addr: string): Promise<string | null> {
  const { host, port } = parseAddr(addr);
  if (await isPortFree(host, port)) return null;

  const lsof = spawnSync("lsof", ["-i", `:${port}`, "-sTCP:LISTEN", "-t"], { encoding: "utf8" });
  const pids = (lsof.stdout ?? "")
    .split("\n")
    .map((s) => s.trim())
    .filter(Boolean);

  if (pids.length === 0) {
    throw new Error(
      `startWebServer: port ${port} (${addr}) is already in use, but the occupying ` +
        `process could not be identified via 'lsof -i :${port}'. This is likely a leftover ` +
        `process from a previous test run (or another process squatting the port) — ` +
        `NOT a bug in the code under test. Find and stop it manually (e.g. ` +
        `'lsof -i :${port}') and re-run.`,
    );
  }

  const notes: string[] = [];
  let killedAny = false;
  for (const pidStr of pids) {
    const pid = Number(pidStr);
    if (!Number.isFinite(pid)) continue;
    const ps = spawnSync("ps", ["-p", pidStr, "-o", "command="], { encoding: "utf8" });
    const cmd = (ps.stdout ?? "").trim();
    // Require "kitsoki" immediately followed by the "web" subcommand, not a
    // bare substring — this repo's checkout directory is itself named
    // "Kitsoki", so any process spawned from inside it (including the test
    // runner) would otherwise false-positive-match.
    const looksLikeKitsokiWeb = /(^|[/\s])kitsoki(\.exe)?\s+web(\s|$)/.test(cmd);
    if (!cmd || !looksLikeKitsokiWeb) {
      // Doesn't look kitsoki-related (or we couldn't read its command line at
      // all) — never touch it. Surface exactly what we found so a human
      // immediately knows to look elsewhere rather than at their own code.
      throw new Error(
        `startWebServer: port ${port} (${addr}) is already in use by pid ${pidStr}` +
          `${cmd ? ` (${cmd})` : " (command line unreadable)"}, which does not look like a ` +
          `kitsoki process — refusing to kill it. This is a port conflict with an unrelated ` +
          `process, NOT a bug in the code under test. Free the port manually and re-run.`,
      );
    }
    // Looks like our own leftover — reap it.
    try {
      process.kill(pid, "SIGTERM");
    } catch {
      // already gone
    }
    let died = await waitForPidExit(pid, 2000);
    if (!died) {
      try {
        process.kill(pid, "SIGKILL");
      } catch {
        // already gone
      }
      died = await waitForPidExit(pid, 1000);
    }
    const note = `startWebServer: killed stale kitsoki process ${pid} holding port ${port} from a previous run`;
    console.warn(note);
    notes.push(note);
    killedAny = true;
  }

  if (!killedAny) {
    // Shouldn't be reachable (the loop above throws before falling through
    // for any non-kitsoki pid), but keep the invariant explicit.
    throw new Error(
      `startWebServer: port ${port} (${addr}) is already in use and no occupant could be reaped.`,
    );
  }

  if (!(await isPortFree(host, port))) {
    throw new Error(
      `startWebServer: port ${port} (${addr}) is STILL in use after attempting to kill ` +
        `stale kitsoki process(es) (${notes.join("; ")}). It may not have died in time, or ` +
        `another process has since claimed the port.`,
    );
  }

  return notes.join("; ");
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
  /** Report-bug ticket target (--ticket-repo). Pass "" for LOCAL filing
   *  (issues/bugs/<id>.md), or "owner/repo" to file a real GitHub issue via gh.
   *  When undefined the flag is omitted and `kitsoki web`'s own default applies. */
  ticketRepo?: string;
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
  const tmpDbDir = fs.mkdtempSync(path.join(os.tmpdir(), "kitsoki-pw-"));
  const dbPath = path.join(tmpDbDir, "s.db");
  let configPath = opts.config;
  if (!configPath && opts.flow) {
    configPath = path.join(tmpDbDir, "demo.kitsoki.yaml");
    fs.writeFileSync(
      configPath,
      "# Auto-generated by the Playwright demo helper.\n" +
        "# Flow-mode demos are deterministic/no-LLM and must not inherit local harness profiles.\n" +
        "story_dirs: []\n",
    );
  }
  const checkPaths = [storiesDir];
  if (opts.flow) checkPaths.push(opts.flow);
  if (opts.recording) checkPaths.push(opts.recording);
  if (!GO_RUN) checkPaths.push(BIN);
  if (opts.hostCassette) checkPaths.push(opts.hostCassette);
  if (configPath) checkPaths.push(configPath);
  for (const p of checkPaths) {
    if (!fs.existsSync(p)) {
      const hint = p === BIN ? " (run 'make build-bin', or unset KITSOKI_WEB_GO_RUN to use go run)" : "";
      throw new Error(`missing required path: ${p}${hint}`);
    }
  }

  // Self-heal the "previous run's server still holds this port" failure mode
  // (see reapStaleKitsokiOnPort doc) before spawning. Fast no-op when the
  // port is already free (the common/success case).
  const staleNote = await reapStaleKitsokiOnPort(opts.addr);

  let serverLog = "";

  const args = ["web", "--stories-dir", storiesDir, "--addr", opts.addr, "--db", dbPath];
  if (opts.flow) args.push("--flow", opts.flow);
  if (opts.harness) args.push("--harness", opts.harness);
  if (opts.recording) args.push("--recording", opts.recording);
  if (opts.mode) args.push("--mode", opts.mode);
  if (opts.hostCassette) args.push("--host-cassette", opts.hostCassette);
  if (configPath) args.push("--config", configPath);
  if (opts.ticketRepo !== undefined) args.push("--ticket-repo", opts.ticketRepo);

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
  try {
    await waitForHealthy(base, GO_RUN ? 90000 : 20000, () => serverLog);
  } catch (e) {
    if (staleNote) {
      const msg = e instanceof Error ? e.message : String(e);
      throw new Error(`${staleNote}\n${msg}`);
    }
    throw e;
  }

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
 * Poll the trace RPC until at least `minCount` `agent.call.complete` events are
 * present, so a tour never starts spotlighting trace rows before the SSE stream
 * has actually pushed them. A deterministic replacement for a flat
 * `page.waitForTimeout` — the events arrive on wall-clock-variable SSE timing,
 * so a fixed sleep is a flicker/rushed-frame risk under load. Mirrors the golden
 * agent-actions spec's `waitForAgentTranscripts`.
 *
 * Resolves once the count is met; throws on timeout with the last seen count so
 * a failed recording is diagnosable. Pass `requireTranscriptRef` to additionally
 * gate on a `transcript_ref` attr (needed before the agent-actions drawer steps,
 * where the affordance only renders for transcript-bearing calls).
 */
export async function waitForAgentComplete(
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
        e.msg === "agent.call.complete" &&
        (!requireTranscriptRef || !!(e.attrs && e.attrs["transcript_ref"])),
    ).length;
    if (seen >= minCount) return;
    await new Promise((r) => setTimeout(r, 400));
  }
  throw new Error(
    `agent events never settled: only ${seen}/${minCount} agent.call.complete` +
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
 * FAST RUNS DON'T CLOBBER THE REAL VIDEO: when WEB_CHAT_PACE=0 (PACE===0) every
 * dwell collapses to zero, so the recording is a useless ~5s blur meant only for
 * assertion runs. Saving it to `<name>.mp4` would overwrite the watch-speed cut a
 * human is meant to see — and the overwrite is silent, so you think you shipped a
 * demo when you shipped a blur. Fast runs therefore write to `<name>.fast.mp4`
 * instead; only a real-pace run (the default) produces `<name>.mp4`.
 *
 * Returns the final stable path (`.mp4`, or `.webm` only on ffmpeg failure).
 */
export async function saveVideoAsMp4(
  video: Video | null,
  artifactDir: string,
  name: string,
): Promise<string | null> {
  const rrwebOut = rrwebCaptureOutPath();
  if (rrwebOut) {
    console.log(`[rrweb] video export disabled; using ${rrwebOut} for chapter sidecar`);
    return rrwebOut;
  }
  if (!video) return null;
  // The active camera profile suffixes the base filename so multi-profile passes
  // sit side by side; desktop's suffix is empty, so its artifact is unchanged.
  const suffix = profileSuffix();
  const base = `${name}${suffix}`;
  // The CANONICAL user-facing filename (`<base>.mp4`) is reserved for a REAL
  // recording. A fast/assert run (WEB_CHAT_PACE=0) records a throwaway used only
  // to validate beats — it must NEVER take the canonical name, or a quick
  // run-through would masquerade as (or clobber) the real demo. Bake the pace
  // into the filename so the two can never collide.
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
  // Mirror scripts/webm-to-mp4.sh so in-spec and manual conversions match.
  const vf = "fps=30,scale=trunc(iw/2)*2:trunc(ih/2)*2";
  const r = spawnSync(
    "ffmpeg",
    ["-y", "-loglevel", "error", "-i", raw, "-vf", vf,
      "-c:v", "libx264", "-preset", "slow", "-crf", "20",
      "-pix_fmt", "yuv420p", "-movflags", "+faststart", "-an", mp4],
    { encoding: "utf8" },
  );
  if (r.status !== 0) {
    // ffmpeg failed — promote the raw webm as the fallback so we never lose it.
    const fallback = path.join(artifactDir, `${outName}.webm`);
    fs.renameSync(raw, fallback);
    console.warn(`[video] ffmpeg mp4 transcode failed; using raw webm\n${r.stderr?.slice(0, 400)}`);
    return fallback;
  }
  fs.unlinkSync(raw);
  // A RECORD-mode video that came out suspiciously short is an under-dwelled
  // run-through, not a demo — down-name it (with its length) so it can't be
  // mistaken for the polished artifact, and the canonical <base>.mp4 stays
  // ABSENT until a proper recording is made. Tunable via KITSOKI_MIN_DEMO_SECONDS.
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

/**
 * A real user-facing demo should be substantial. A RECORD-mode video shorter
 * than this many seconds is treated as a fast run-through and down-named (never
 * the canonical `<name>.mp4`). Override with KITSOKI_MIN_DEMO_SECONDS.
 */
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
