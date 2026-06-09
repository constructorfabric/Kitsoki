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
import { spawn, type ChildProcess } from "child_process";
import { fileURLToPath } from "url";
import path from "path";
import fs from "fs";
import os from "os";
import { expect, type Page } from "@playwright/test";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
// _helpers → playwright → tests → runstatus → tools → kitsoki (repo root)
export const repoRoot = path.resolve(__dirname, "../../../../..");
export const BIN = path.join(repoRoot, "bin", "kitsoki");
export const STORIES_DIR = path.join(repoRoot, "stories");

/** Global pacing knob: 0 for fast assertion runs, 1 (default) for the camera. */
export const PACE = Number(process.env.WEB_CHAT_PACE ?? "1");

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
  flow: string;
  storiesDir?: string;
}): Promise<WebServer> {
  const storiesDir = opts.storiesDir ?? STORIES_DIR;
  for (const p of [storiesDir, opts.flow, BIN]) {
    if (!fs.existsSync(p)) {
      throw new Error(
        `missing required path: ${p} (run 'make build && cp ./kitsoki bin/kitsoki' first)`,
      );
    }
  }

  const tmpDbDir = fs.mkdtempSync(path.join(os.tmpdir(), "kitsoki-pw-"));
  const dbPath = path.join(tmpDbDir, "s.db");
  let serverLog = "";

  const proc: ChildProcess = spawn(
    BIN,
    ["web", "--stories-dir", storiesDir, "--flow", opts.flow, "--addr", opts.addr, "--db", dbPath],
    { cwd: repoRoot, stdio: ["ignore", "pipe", "pipe"] },
  );
  proc.stdout?.on("data", (d: Buffer) => (serverLog += d.toString()));
  proc.stderr?.on("data", (d: Buffer) => (serverLog += d.toString()));
  proc.on("exit", (code, sig) => (serverLog += `\n[server exited code=${code} sig=${sig}]\n`));

  const base = `http://${opts.addr}`;
  await waitForHealthy(base, 20000, () => serverLog);

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
      proc.kill();
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
