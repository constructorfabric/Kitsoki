/**
 * new-session.spec.ts — regression for "once one story session is started,
 * that's it; there's no way to start a new one" in the web UI.
 *
 * Root cause: the home screen ("/") auto-redirects into the lone live session
 * every time it mounts, so whenever exactly one session was live the stories
 * list — and its "New session" buttons — was unreachable. The fix spends a
 * per-tab auto-nav guard (sessionStorage) that the session views also mark on
 * mount, so a tab that opened straight into a session can still reach "/".
 *
 * This runs a REAL `kitsoki web` server in the deterministic no-LLM posture on
 * its OWN port + db (isolated from multi-story.spec.ts), so its session count is
 * controlled: it starts at zero, the test mints exactly one, and the bounce
 * condition (single live session) is reproduced deterministically. The worst
 * case is exercised: a brand-new browser tab whose FIRST view is a session (no
 * prior home mount to set the guard).
 */
import { test, expect, chromium, type Browser, type BrowserContext } from "@playwright/test";
import { fileURLToPath } from "url";
import path from "path";
import fs from "fs";
import os from "os";
import { spawn, type ChildProcess } from "child_process";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

// repoRoot = .../kitsoki (tools/runstatus/tests/playwright -> 4 up)
const repoRoot = path.resolve(__dirname, "../../../..");
const BIN = path.join(repoRoot, "bin", "kitsoki");
const STORIES_DIR = path.join(repoRoot, "stories");
const FLOW = path.join(repoRoot, "stories", "prd", "flows", "happy_path.yaml");

// Distinct port from multi-story.spec.ts (7740) so the two specs' servers can
// run in parallel under the default worker pool without colliding.
const ADDR = "127.0.0.1:7742";
const BASE = `http://${ADDR}`;
const RPC = `${BASE}/rpc`;

let server: ChildProcess | null = null;
let serverLog = "";
let tmpDbDir = "";

async function rpc<T>(method: string, params: Record<string, unknown>): Promise<T> {
  const res = await fetch(RPC, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ jsonrpc: "2.0", id: 1, method, params }),
  });
  const body = (await res.json()) as { result?: T; error?: { message: string } };
  if (body.error) throw new Error(`${method} failed: ${body.error.message}`);
  return body.result as T;
}

async function waitForHealthy(timeoutMs: number): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  let lastErr = "";
  while (Date.now() < deadline) {
    try {
      const res = await fetch(`${BASE}/`, { method: "GET" });
      if (res.status === 200) return;
      lastErr = `status ${res.status}`;
    } catch (e) {
      lastErr = e instanceof Error ? e.message : String(e);
    }
    await new Promise((r) => setTimeout(r, 200));
  }
  throw new Error(`server not healthy after ${timeoutMs}ms (last: ${lastErr})\n--- server log ---\n${serverLog}`);
}

test.beforeAll(async () => {
  for (const p of [STORIES_DIR, FLOW, BIN]) {
    if (!fs.existsSync(p)) throw new Error(`missing required path: ${p} (run 'make build' first)`);
  }
  tmpDbDir = fs.mkdtempSync(path.join(os.tmpdir(), "kitsoki-new-session-"));
  const dbPath = path.join(tmpDbDir, "s.db");

  server = spawn(
    BIN,
    ["web", "--stories-dir", STORIES_DIR, "--flow", FLOW, "--addr", ADDR, "--db", dbPath],
    { cwd: repoRoot, stdio: ["ignore", "pipe", "pipe"] },
  );
  server.stdout?.on("data", (d) => (serverLog += d.toString()));
  server.stderr?.on("data", (d) => (serverLog += d.toString()));
  server.on("exit", (code, sig) => {
    serverLog += `\n[server exited code=${code} sig=${sig}]\n`;
  });

  await waitForHealthy(20000);
});

test.afterAll(async () => {
  if (server && !server.killed) {
    server.kill("SIGTERM");
    await new Promise((r) => setTimeout(r, 500));
    if (!server.killed) server.kill("SIGKILL");
  }
  if (tmpDbDir) fs.rmSync(tmpDbDir, { recursive: true, force: true });
});

test.describe("a live session never traps the stories list", () => {
  test("session view → '← Stories' stays home → can start another session", async () => {
    test.setTimeout(60000);

    // Fresh server: no sessions exist yet, so the single-session bounce
    // condition is reproduced deterministically.
    const noSessions = await rpc<Array<unknown>>("runstatus.sessions.list", {});
    expect(noSessions.length, "fresh server has no sessions").toBe(0);

    const stories = await rpc<Array<{ path: string; app_id: string }>>(
      "runstatus.stories.list",
      {},
    );
    const prd = stories.find((s) => s.app_id === "prd");
    expect(prd, "PRD story is in the catalogue").toBeTruthy();
    const storyPath = prd!.path;

    // Mint exactly ONE session out-of-band and open its chat surface DIRECTLY —
    // the tab never mounted the home screen first, so the old in-memory guard
    // would be unset and the first "← Stories" click (with one live session)
    // would bounce the user straight back into it.
    const { session_id: sid1 } = await rpc<{ session_id: string }>(
      "runstatus.session.new",
      { story_path: storyPath },
    );
    expect(sid1).toBeTruthy();

    const browser: Browser = await chromium.launch();
    // Fresh context = fresh sessionStorage, i.e. a brand-new browser tab.
    const context: BrowserContext = await browser.newContext({
      viewport: { width: 1280, height: 800 },
    });
    const page = await context.newPage();
    try {
      await page.goto(`${BASE}/#/s/${sid1}/chat`);
      await expect(page.getByTestId("chat-section")).toBeVisible({ timeout: 15000 });

      // Back to the catalogue must STAY there — not redirect back into sid1.
      await page.getByTestId("back-stories").click();
      await expect(page.getByTestId("home-view")).toBeVisible({ timeout: 15000 });
      await expect(page, "stayed on the stories list, not bounced into the session")
        .toHaveURL(/#\/$/);

      // And from the catalogue a brand-new session can be started through the UI.
      const card = page
        .getByTestId("story-card")
        .filter({ has: page.getByTestId("story-title").filter({ hasText: "PRD authoring" }) });
      await expect(card).toBeVisible();
      await card.getByTestId("new-session-btn").click();
      await page.waitForURL(/#\/s\/[0-9a-f-]{36}\/chat$/, { timeout: 15000 });
      const sid2 = page.url().match(/#\/s\/([0-9a-f-]{36})\/chat$/)?.[1];
      expect(sid2, "a second, distinct session was started via the UI").toBeTruthy();
      expect(sid2).not.toBe(sid1);
      await expect(page.getByTestId("chat-section")).toBeVisible({ timeout: 15000 });
    } finally {
      await page.close();
      await context.close();
      await browser.close();
    }
  });
});
