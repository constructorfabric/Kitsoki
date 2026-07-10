/**
 * recordFinding.ts — bundle a swarm finding into the existing bug-report
 * evidence machinery.
 *
 * When a per-user gate (isolation/console/audit) fails — or a driver just
 * wants to journal a finding explicitly — this pulls the SAME three legs of
 * evidence the interactive "Report a bug" flow captures, using the SAME
 * seams, and writes them to disk as one self-contained, replayable bundle:
 *
 *   1. rrweb session replay — `window.__kitsokiVisual.recording()`
 *      (tools/runstatus/src/lib/visualHelper.ts), the SPA's own always-on
 *      rolling ~30s capture. No test-side `installCapture` needed: the SPA
 *      installs this helper (and starts session-capture.ts's recorder) at
 *      bootstrap (`tools/runstatus/src/main.ts`), so it is already live on
 *      any page running the real kitsoki web UI.
 *   2. console log — the caller's own `page.on("console"/"pageerror")`
 *      collection (every swarm driver already gathers this per user; see
 *      swarm-replay-users.spec.ts's `consoleErrors` array). Passed in rather
 *      than re-derived, since Playwright console listeners are cumulative
 *      from the moment they're installed, not retroactively queryable.
 *   3. scrubbed HAR — `runstatus.bug.preview` (internal/runstatus/server/bug_report.go),
 *      the server's own HAR ring-buffer snapshot, already scrubbed
 *      server-side via internal/runstatus/harscrub before it ever reaches
 *      this process. We deliberately call `bug.preview` and NOT the
 *      confirming `bug.report` — `bug.report` writes a real
 *      issues/bugs/<id>.md (or files a GitHub issue) as a side effect, which
 *      is the right behavior for an operator explicitly filing a bug but the
 *      wrong one for automated per-user gate telemetry from a 24-way swarm
 *      run. `bug.preview`'s scrubbed HAR snapshot is the complete, correct
 *      evidence leg on its own.
 *
 * Bundle layout under `<findingsDir>/<capture_id>/`:
 *   manifest.json   — FindingManifest (persona, step, assertion, server sha, ...)
 *   rrweb.json       — CaptureRrwebEnvelope (schema-compatible with the
 *                      existing rrweb-replay.ts loader's CaptureDump.events)
 *   console.json     — string[] of console/pageerror messages
 *   har.json         — the scrubbed Har object bug.preview returned
 *
 * `.artifacts/swarm/findings/` is gitignored (per AGENTS.md — only the code
 * that produces bundles is committed, never the bundles themselves).
 */
import fs from "fs";
import path from "path";
import type { Page } from "@playwright/test";
import type {
  CaptureRpcFn,
  CaptureRrwebEnvelope,
  FindingBundle,
  FindingContext,
  FindingManifest,
} from "./types.js";
import { getServerSha } from "./serverSha.js";

/** Default bundles root: `.artifacts/swarm/findings/` under `repoRoot`. */
export function defaultFindingsDir(repoRoot: string): string {
  return path.join(repoRoot, ".artifacts", "swarm", "findings");
}

/** A short, filesystem-safe, sortable capture id: `<epoch-ms>-<persona>-<rand>`. */
function mintCaptureId(personaId: string): string {
  const safePersona = personaId.replace(/[^a-zA-Z0-9_-]+/g, "-").slice(0, 40) || "unknown";
  const rand = Math.random().toString(36).slice(2, 8);
  return `${Date.now()}-${safePersona}-${rand}`;
}

/** Pulls `window.__kitsokiVisual.recording()` off `page`. Returns null
 *  (rather than throwing) if the helper isn't installed — e.g. a page that
 *  never finished loading the SPA — so a capture always completes
 *  best-effort. */
async function captureRrweb(page: Page): Promise<CaptureRrwebEnvelope | null> {
  try {
    return await page.evaluate(() => {
      const w = window as unknown as {
        __kitsokiVisual?: { recording(): CaptureRrwebEnvelope };
      };
      return w.__kitsokiVisual ? w.__kitsokiVisual.recording() : null;
    });
  } catch {
    return null;
  }
}

/** Fetches + returns the server's scrubbed HAR snapshot via
 *  `runstatus.bug.preview`. Returns null (never throws) if the RPC itself
 *  fails, so a bad server round-trip doesn't stop the other evidence legs
 *  from being written. */
async function captureHar(
  rpc: CaptureRpcFn,
): Promise<{ har: unknown; depth: number } | null> {
  try {
    const result = await rpc<{ capture_id: string; har: unknown; depth: number; capacity: number }>(
      "runstatus.bug.preview",
      {},
    );
    return { har: result.har, depth: result.depth };
  } catch {
    return null;
  }
}

export interface RecordFindingOptions {
  /** The failing user's live page — rrweb + (indirectly) console evidence
   *  comes from here. Must still be open (not yet closed by the caller). */
  page: Page;
  /** Any RPC function reaching the shared swarm server (server.rpc /
   *  wrapRpcWithRetry's wrapped fn both satisfy this). */
  rpc: CaptureRpcFn;
  /** Console/pageerror messages already collected for this user (the swarm
   *  harness always gathers these via page.on(...) from context-open). */
  consoleMessages: string[];
  /** What failed, and for whom. */
  context: FindingContext;
  /** Repo root, used to resolve the default findings dir and the server sha. */
  repoRoot: string;
  /** Override the bundles root (defaults to `.artifacts/swarm/findings/` under
   *  repoRoot). Mainly for tests wanting an isolated scratch dir. */
  findingsDir?: string;
}

/**
 * Capture a swarm finding as a self-contained, replayable evidence bundle.
 * Best-effort per evidence leg (a missing rrweb helper or a failed HAR RPC
 * does not stop the other legs, or the manifest, from being written) — a
 * partial bundle with an honest manifest is more useful than none at all.
 */
export async function recordFinding(opts: RecordFindingOptions): Promise<FindingBundle> {
  const findingsDir = opts.findingsDir ?? defaultFindingsDir(opts.repoRoot);
  const captureId = mintCaptureId(opts.context.persona_id);
  const dir = path.join(findingsDir, captureId);
  fs.mkdirSync(dir, { recursive: true });

  const [rrweb, harResult] = await Promise.all([captureRrweb(opts.page), captureHar(opts.rpc)]);

  const files: FindingManifest["files"] = { rrweb: null, console: null, har: null };
  let rrwebEventCount = 0;

  if (rrweb) {
    fs.writeFileSync(path.join(dir, "rrweb.json"), JSON.stringify(rrweb, null, 2) + "\n");
    files.rrweb = "rrweb.json";
    rrwebEventCount = rrweb.events.length;
  }

  // console.json: always written, even if empty — an empty array is itself
  // evidence ("this user's console was clean at capture time").
  fs.writeFileSync(path.join(dir, "console.json"), JSON.stringify(opts.consoleMessages, null, 2) + "\n");
  files.console = "console.json";

  let harDepth: number | null = null;
  if (harResult) {
    fs.writeFileSync(path.join(dir, "har.json"), JSON.stringify(harResult.har, null, 2) + "\n");
    files.har = "har.json";
    harDepth = harResult.depth;
  }

  const manifest: FindingManifest = {
    capture_id: captureId,
    captured_at: new Date().toISOString(),
    persona_id: opts.context.persona_id,
    user_index: opts.context.user_index ?? null,
    marker: opts.context.marker ?? null,
    journey_step: opts.context.journey_step,
    assertion: opts.context.assertion,
    detail: opts.context.detail ?? {},
    server_sha: getServerSha(opts.repoRoot),
    files,
    counts: {
      rrweb_events: rrwebEventCount,
      console_entries: opts.consoleMessages.length,
      har_depth: harDepth,
    },
  };
  fs.writeFileSync(path.join(dir, "manifest.json"), JSON.stringify(manifest, null, 2) + "\n");

  return { manifest, dir };
}
