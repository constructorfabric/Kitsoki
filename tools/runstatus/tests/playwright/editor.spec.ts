/**
 * editor.spec.ts — end-to-end test for the story-editor surface (/editor),
 * driven against a REAL `kitsoki web` server in the deterministic no-LLM posture
 * (--flow stories/prd/flows/happy_path.yaml). The editor reads the PRD story's
 * room graph statically via the runstatus.editor.* RPCs (no session, no LLM).
 *
 * Scenario:
 *   1. Navigate to /editor for the discovered PRD story.
 *   2. Assert the room list renders in BFS order (idle first).
 *   3. Click "clarifying"; assert the hook, domain model, and oracle workbench
 *      panes appear, plus the read-only story viewer.
 *   4. Expand a cassette in the workbench's cassette browser.
 */
import { test, expect, chromium, type Browser, type Page } from "@playwright/test";
import { fileURLToPath } from "url";
import path from "path";
import fs from "fs";
import os from "os";
import { spawn, type ChildProcess } from "child_process";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const repoRoot = path.resolve(__dirname, "../../../..");
const BIN = path.join(repoRoot, "bin", "kitsoki");
const STORIES_DIR = path.join(repoRoot, "stories");
const FLOW = path.join(repoRoot, "stories", "prd", "flows", "happy_path.yaml");
const PRD_APP = path.join(repoRoot, "stories", "prd", "app.yaml");

const ADDR = "127.0.0.1:7798";
const BASE = `http://${ADDR}`;
const RPC = `${BASE}/rpc`;

let server: ChildProcess | null = null;
let serverLog = "";
let tmpDbDir = "";
let browser: Browser;

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
  throw new Error(`server not healthy after ${timeoutMs}ms (last: ${lastErr})\n${serverLog}`);
}

test.beforeAll(async () => {
  for (const p of [STORIES_DIR, FLOW, BIN]) {
    if (!fs.existsSync(p)) throw new Error(`missing required path: ${p} (run 'make build' first)`);
  }
  tmpDbDir = fs.mkdtempSync(path.join(os.tmpdir(), "kitsoki-editor-"));
  server = spawn(
    BIN,
    ["web", "--stories-dir", STORIES_DIR, "--flow", FLOW, "--addr", ADDR, "--db", path.join(tmpDbDir, "s.db")],
    { cwd: repoRoot, stdio: ["ignore", "pipe", "pipe"] }
  );
  server.stdout?.on("data", (d) => (serverLog += d.toString()));
  server.stderr?.on("data", (d) => (serverLog += d.toString()));
  await waitForHealthy(20000);
  browser = await chromium.launch();
});

test.afterAll(async () => {
  await browser?.close();
  if (server && !server.killed) {
    server.kill("SIGTERM");
    await new Promise((r) => setTimeout(r, 300));
    if (!server.killed) server.kill("SIGKILL");
  }
  if (tmpDbDir) fs.rmSync(tmpDbDir, { recursive: true, force: true });
});

test("story editor: room list, room detail, oracle workbench, cassette", async () => {
  // Resolve the PRD story's canonical absolute app.yaml path from the catalogue.
  const stories = await rpc<{ path: string; app_id: string }[]>("runstatus.stories.list", {});
  const prd = stories.find((s) => s.path === PRD_APP || s.app_id === "prd");
  expect(prd, `prd story not in catalogue: ${JSON.stringify(stories)}`).toBeTruthy();

  const page: Page = await browser.newPage();
  await page.goto(`${BASE}/#/editor?story=${encodeURIComponent(prd!.path)}`);

  // Editor page + room list present.
  await page.waitForSelector('[data-testid="editor-page"]', { timeout: 10000 });
  await page.waitForSelector('[data-testid="editor-room-item"]', { timeout: 10000 });

  // Room list in BFS order — idle is the initial room (distance 0) so it is first.
  const roomIds = await page.$$eval('[data-testid="editor-room-item"]', (els) =>
    els.map((e) => e.getAttribute("data-room-id"))
  );
  expect(roomIds.length).toBeGreaterThan(1);
  expect(roomIds[0]).toBe("idle");

  // Select "clarifying".
  await page.click('[data-testid="editor-room-item"][data-room-id="clarifying"]');

  // Detail panes appear.
  await page.waitForSelector('[data-testid="editor-hook"]');
  await expect(page.locator('[data-testid="editor-domain-model"]')).toBeVisible();
  await expect(page.locator('[data-testid="editor-oracle-workbench"]')).toBeVisible();
  await expect(page.locator('[data-testid="editor-story-viewer"]')).toBeVisible();

  // The clarifying room makes an oracle call — its workbench shows a card and
  // embeds a cassette browser. The PRD story ships no oracle cassette files, so
  // the browser renders its (empty) list container; when a story DOES carry
  // matching episodes the same surface lists them and a click expands one.
  await page.waitForSelector('[data-testid="editor-oracle-card"]');
  await expect(page.locator('[data-testid="editor-cassette-list"]').first()).toBeVisible();

  const cassetteItems = page.locator('[data-testid="editor-cassette-item"]');
  if ((await cassetteItems.count()) > 0) {
    await cassetteItems.first().click();
    await expect(page.locator('[data-testid="editor-cassette-expand"]').first()).toBeVisible();
  } else {
    await expect(
      page.locator('[data-testid="editor-cassette-list"]').first()
    ).toContainText("No matching cassette episodes");
  }

  await page.close();
});
