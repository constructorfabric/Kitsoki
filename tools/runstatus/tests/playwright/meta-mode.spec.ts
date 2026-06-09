/**
 * meta-mode.spec.ts — end-to-end product demo for the web UI's META MODE
 * overlay, driven against a REAL `kitsoki web` server in the deterministic
 * no-LLM posture (--flow: host stubbed, harness nil, the meta oracle replaced
 * by the deterministic stub — no LLM is ever called).
 *
 * Meta mode is a global, persistent overlay chat with kitsoki's named agents:
 *   - Story edit  (story-author)    — edits this story's YAML, commits, reloads
 *   - Story Q&A   (story-explainer) — read-only questions about the story
 *   - Kitsoki help(kitsoki-explainer)— read-only questions about kitsoki itself
 *
 * The demo proves the full experience, recording a MacBook-resolution video +
 * per-scene screenshots:
 *
 *   1. Home meta        — the global Meta button works with NO session open:
 *                         story modes are disabled, Kitsoki help is usable.
 *   2. Drive a story    — start a PRD session (lands on the chat surface).
 *   3. Story Q&A        — read-only meta turn; reply echoes the current state.
 *   4. Story edit       — an edit turn reloads the story content IN PLACE
 *                         (no browser reload) and surfaces the changed files.
 *   5. Persistence      — close, navigate to Observe, reopen → same chat.
 *   6. New chat         — the new-chat control resets the conversation.
 *
 * Repo-safety: story-edit COMMITS its changes into the story's git repo, so the
 * demo runs against a THROWAWAY COPY of stories/ in a tmp dir (not a git repo),
 * leaving the real repo untouched.
 *
 * KITSOKI_REPO is exported to the server so the cross-app kitsoki.* meta modes
 * (Kitsoki help) are injected — they are gated on that env var.
 */
import { test, expect, chromium, type Browser, type BrowserContext, type Page } from "@playwright/test";
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
const STORIES_SRC = path.join(repoRoot, "stories");

const ADDR = "127.0.0.1:7741";
const BASE = `http://${ADDR}`;
const RPC = `${BASE}/rpc`;

const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "meta-mode");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "video");

// ── server lifecycle ────────────────────────────────────────────────────────

let server: ChildProcess | null = null;
let serverLog = "";
let tmpRoot = ""; // holds the throwaway stories copy + db

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
  for (const p of [STORIES_SRC, BIN]) {
    if (!fs.existsSync(p)) throw new Error(`missing required path: ${p} (run 'make build' first)`);
  }
  fs.mkdirSync(VIDEO_DIR, { recursive: true });
  if (fs.existsSync(ARTIFACT_DIR)) {
    for (const f of fs.readdirSync(ARTIFACT_DIR)) {
      if (f.endsWith(".png")) fs.rmSync(path.join(ARTIFACT_DIR, f));
    }
  }

  // Throwaway copy of stories/ so a story-edit commit lands in a tmp dir (not a
  // git repo → the commit step fails harmlessly, the reload still fires), and
  // the real repo stays clean.
  tmpRoot = fs.mkdtempSync(path.join(os.tmpdir(), "kitsoki-meta-mode-"));
  const tmpStories = path.join(tmpRoot, "stories");
  fs.cpSync(STORIES_SRC, tmpStories, { recursive: true });
  const flow = path.join(tmpStories, "prd", "flows", "happy_path.yaml");
  const dbPath = path.join(tmpRoot, "s.db");

  server = spawn(
    BIN,
    ["web", "--stories-dir", tmpStories, "--flow", flow, "--addr", ADDR, "--db", dbPath],
    {
      cwd: repoRoot,
      stdio: ["ignore", "pipe", "pipe"],
      // KITSOKI_REPO gates the cross-app kitsoki.* meta modes (Kitsoki help).
      env: { ...process.env, KITSOKI_REPO: repoRoot },
    },
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
  if (tmpRoot) fs.rmSync(tmpRoot, { recursive: true, force: true });
});

// Pacing: WEB_CHAT_PACE=0 collapses delays for fast assertion-only CI.
const PACE = process.env.WEB_CHAT_PACE === "0" ? 0 : 1;
const TYPE_DELAY = 55 * PACE;
const BEFORE_ACT = 900 * PACE;
const DWELL = 2400 * PACE;
const OPEN_DWELL = 3000 * PACE;
const FINAL_DWELL = 4500 * PACE;

const screenshotPaths: string[] = [];
let shotIdx = 0;
async function shot(page: Page, label: string): Promise<void> {
  const safe = label.replace(/[^a-zA-Z0-9_]+/g, "_");
  const name = `${String(shotIdx).padStart(2, "0")}-${safe}.png`;
  shotIdx += 1;
  const p = path.join(ARTIFACT_DIR, name);
  await page.screenshot({ path: p, fullPage: true });
  screenshotPaths.push(p);
}

// Type into the meta composer and send, waiting for a new agent bubble.
async function metaSend(page: Page, text: string): Promise<void> {
  const before = await page.getByTestId("meta-row-agent").count();
  const input = page.getByTestId("meta-composer-input");
  await input.click();
  await input.fill("");
  await input.pressSequentially(text, { delay: TYPE_DELAY });
  await page.waitForTimeout(BEFORE_ACT);
  await page.getByTestId("meta-composer-send").click();
  await expect(page.getByTestId("meta-row-agent")).toHaveCount(before + 1, { timeout: 15000 });
}

test.describe("meta mode (live, no-LLM)", () => {
  test("home help → drive → story Q&A → story edit reload → persistence → new chat", async () => {
    test.setTimeout(process.env.WEB_CHAT_PACE === "0" ? 60000 : 180000);

    const browser: Browser = await chromium.launch();
    const context: BrowserContext = await browser.newContext({
      viewport: { width: 1440, height: 900 },
      deviceScaleFactor: 2,
      recordVideo: { dir: VIDEO_DIR, size: { width: 1440, height: 900 } },
    });
    const page = await context.newPage();
    page.on("pageerror", (e) => console.log("PAGEERROR:", e.message));
    page.on("console", (m) => {
      if (m.type() === "error") console.log("CONSOLE.ERR:", m.text());
    });

    try {
      // ── Scene 1: home meta (no session) ────────────────────────────────────
      await page.goto(`${BASE}/#/`);
      await expect(page.getByTestId("home-view")).toBeVisible({ timeout: 15000 });
      await expect(page.getByTestId("meta-button")).toBeVisible();
      await page.waitForTimeout(OPEN_DWELL);

      await page.getByTestId("meta-button").click();
      await expect(page.getByTestId("meta-menu")).toBeVisible();
      // Story modes need a running story → disabled on home.
      await expect(page.getByTestId("meta-mode-story-edit")).toBeDisabled();
      // Kitsoki help is cross-app → usable with no session.
      await expect(page.getByTestId("meta-mode-kitsoki-ask")).toBeEnabled({ timeout: 10000 });
      await shot(page, "home-meta-menu");
      await page.waitForTimeout(BEFORE_ACT);
      await page.getByTestId("meta-mode-kitsoki-ask").click();
      await expect(page.getByTestId("meta-overlay")).toBeVisible();
      await page.waitForTimeout(BEFORE_ACT);
      await metaSend(page, "What is kitsoki in one sentence?");
      await shot(page, "home-kitsoki-help");
      await page.waitForTimeout(DWELL);
      await page.getByTestId("meta-close").click();
      await expect(page.getByTestId("meta-overlay")).toHaveCount(0);

      // ── Scene 2: start a PRD session → chat surface ────────────────────────
      const card = page
        .getByTestId("story-card")
        .filter({ has: page.getByTestId("story-title").filter({ hasText: "PRD authoring" }) });
      await expect(card).toBeVisible();
      await page.waitForTimeout(BEFORE_ACT);
      await card.getByTestId("new-session-btn").click();
      await page.waitForURL(/#\/s\/[0-9a-f-]{36}\/chat$/, { timeout: 15000 });
      const sid = page.url().match(/#\/s\/([0-9a-f-]{36})\/chat$/)?.[1];
      expect(sid, "session id captured").toBeTruthy();
      await expect(page.getByTestId("chat-section")).toBeVisible({ timeout: 15000 });
      await expect(page.getByTestId("current-state")).toHaveText("idle");
      await shot(page, "session-chat");
      await page.waitForTimeout(OPEN_DWELL);

      // ── Scene 3: Story Q&A (read-only) ─────────────────────────────────────
      await page.getByTestId("meta-button").click();
      await expect(page.getByTestId("meta-menu")).toBeVisible();
      await expect(page.getByTestId("meta-mode-story-ask")).toBeEnabled({ timeout: 10000 });
      await page.getByTestId("meta-mode-story-ask").click();
      await expect(page.getByTestId("meta-overlay")).toBeVisible();
      await page.waitForTimeout(BEFORE_ACT);
      await metaSend(page, "What state am I in, and what should I do next?");
      // The read-only reply echoes the live state (idle) — meta sees the session.
      await expect(page.getByTestId("meta-row-agent").last()).toContainText("idle");
      await shot(page, "story-qa");
      await page.waitForTimeout(DWELL);

      // ── Scene 4: Story edit → in-place content reload ──────────────────────
      await page.getByTestId("meta-tab-story-edit").click();
      await page.waitForTimeout(BEFORE_ACT);
      await metaSend(page, "Make the opening prompt a little warmer.");
      // The edit landed: the overlay surfaces the reload note + changed files,
      // and the run view re-hydrated WITHOUT a browser reload.
      await expect(page.getByTestId("meta-reload-note")).toBeVisible({ timeout: 15000 });
      await expect(page.getByTestId("meta-reload-note")).toContainText("meta-edits.log");
      await shot(page, "story-edit-reload");
      await page.waitForTimeout(DWELL);
      // Count the edit-mode messages so we can prove persistence next.
      const editMsgCount = await page.getByTestId("meta-transcript").locator(".meta-row").count();
      expect(editMsgCount).toBeGreaterThan(0);

      // ── Scene 5: persistence across navigation ─────────────────────────────
      await page.getByTestId("meta-close").click();
      await expect(page.getByTestId("meta-overlay")).toHaveCount(0);
      // Hop to the read-only observer (a different view, same session).
      await page.getByTestId("observe-link").click();
      await page.waitForURL(/#\/s\/[0-9a-f-]{36}$/, { timeout: 15000 });
      await expect(page.getByTestId("breadcrumb")).toBeVisible();
      await page.waitForTimeout(BEFORE_ACT);
      // Reopen Story edit → the SAME conversation is still there.
      await page.getByTestId("meta-button").click();
      await page.getByTestId("meta-mode-story-edit").click();
      await expect(page.getByTestId("meta-overlay")).toBeVisible();
      await expect(page.getByTestId("meta-transcript").locator(".meta-row")).toHaveCount(editMsgCount);
      await shot(page, "persistence");
      await page.waitForTimeout(DWELL);

      // ── Scene 6: new chat resets the conversation ──────────────────────────
      await page.getByTestId("meta-new").click();
      await expect(page.getByTestId("meta-transcript").locator(".meta-row")).toHaveCount(0, { timeout: 10000 });
      await shot(page, "new-chat");
      await page.waitForTimeout(FINAL_DWELL);
    } finally {
      await page.close();
      await context.close(); // flush the video
      await browser.close();
    }

    const webms = fs
      .readdirSync(VIDEO_DIR)
      .filter((f) => f.endsWith(".webm"))
      .map((f) => ({ f, t: fs.statSync(path.join(VIDEO_DIR, f)).mtimeMs }))
      .sort((a, b) => b.t - a.t);
    expect(webms.length, "expected a recorded video webm").toBeGreaterThan(0);
    const latest = path.join(VIDEO_DIR, webms[0].f);
    fs.copyFileSync(latest, path.join(ARTIFACT_DIR, "meta-mode-demo.webm"));

    console.log("[meta-mode] screenshots:\n" + screenshotPaths.join("\n"));
    console.log("[meta-mode] video (stable): " + path.join(ARTIFACT_DIR, "meta-mode-demo.webm"));
  });
});
