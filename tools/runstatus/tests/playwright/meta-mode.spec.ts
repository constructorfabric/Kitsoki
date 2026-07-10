/**
 * meta-mode.spec.ts — end-to-end product demo for the web UI's META MODE
 * overlay, driven against a REAL `kitsoki web` server in the deterministic
 * no-LLM posture (--flow: host stubbed, harness nil, the meta agent replaced
 * by the deterministic stub — no LLM is ever called).
 *
 * Meta mode is a global, persistent overlay chat with kitsoki's named agents:
 *   - Story edit  (story-author)    — edits this story's YAML, commits, reloads
 *   - Story Q&A   (story-explainer) — read-only questions about the story
 *   - Kitsoki help(kitsoki-explainer)— read-only questions about kitsoki itself
 *
 * The demo proves the full experience, recording a MacBook-resolution video +
 * per-scene screenshots. Streaming is validated by setting
 * KITSOKI_META_STREAM_DELAY_MS so the stub agent emits tool/delta events with
 * a visible pause — the streaming bubble, 🧠 brain icon, and tool breadcrumbs
 * all render for at least the duration of the agent call before the committed
 * message lands.
 *
 * Scenes:
 *   1. Home meta        — Meta button works with NO session; story modes disabled,
 *                         Kitsoki help usable.
 *   2. Drive a story    — Start a PRD session (lands on the chat surface).
 *   3. Story Q&A        — Read-only meta turn; streaming bubble with 🧠 visible;
 *                         reply echoes the current state.
 *   4. Story Q&A round 2 — Second turn in the same chat; proves multi-round.
 *   5. Story edit       — Edit turn triggers reload; reload note + changed files.
 *   6. Persistence      — Close, navigate, reopen → same chat.
 *   7. New chat         — New-chat control resets the conversation.
 *
 * Repo-safety: story-edit COMMITS its changes; the demo runs against a THROWAWAY
 * COPY of stories/ in a tmp dir (not a git repo), leaving the real repo untouched.
 */
import { test, expect, chromium, type Browser, type BrowserContext, type Page } from "@playwright/test";
import { fileURLToPath } from "url";
import path from "path";
import fs from "fs";
import os from "os";
import { spawn, type ChildProcess } from "child_process";
import { saveVideoAsMp4, ChapterRecorder, writeChapters, demoAddr, maybeInstallAutoRrwebCapture } from "./_helpers/server.js";
import { cameraContext } from "./_helpers/camera.js";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

// repoRoot = .../kitsoki (tools/runstatus/tests/playwright -> 4 up)
const repoRoot = path.resolve(__dirname, "../../../..");
const BIN = path.join(repoRoot, "bin", "kitsoki");
const STORIES_SRC = path.join(repoRoot, "stories");

const ADDR = demoAddr(7741);
const BASE = `http://${ADDR}`;
const RPC = `${BASE}/rpc`;

const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "meta-mode");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "video");
const CHAPTER_SOURCE = "features/meta-mode.yaml";

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

// Pacing: WEB_CHAT_PACE=0 collapses delays for fast CI.
const PACE = process.env.WEB_CHAT_PACE === "0" ? 0 : 1;
const TYPE_DELAY = 55 * PACE;
const BEFORE_ACT = 900 * PACE;
const DWELL = 2400 * PACE;
const OPEN_DWELL = 3000 * PACE;
const FINAL_DWELL = 4500 * PACE;

// Stream delay: per-event pause the stub injects between streaming events.
// The stub also holds 4× this value after the tool event before text starts,
// giving the viewer a clear look at 🧠 + ▸ ToolName + "…" before text streams.
// Zero in fast-CI mode.
const STREAM_DELAY_MS = PACE === 0 ? "0" : "150";

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
      env: {
        ...process.env,
        KITSOKI_REPO: repoRoot,
        KITSOKI_META_STREAM_DELAY_MS: STREAM_DELAY_MS,
      },
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

/**
 * Send a message and wait for the streaming bubble to appear (asserts 🧠 label
 * + optional tool breadcrumbs), captures a screenshot of the streaming state,
 * then waits for the committed agent row.
 *
 * With STREAM_DELAY_MS=150, the stub holds 4×150ms=600ms after the tool event
 * before text starts (viewer sees 🧠 + tool + "…"), then streams each word at
 * 150ms. Total bubble duration ≈ 600ms + N×150ms. Plenty of time to screenshot.
 *
 * In fast-CI mode (PACE=0) streaming completes instantly; skip the assertions.
 */
async function metaSendWithStreamCheck(
  page: Page,
  text: string,
  opts: { checkStream?: boolean; checkTool?: boolean; streamShot?: string } = {}
): Promise<void> {
  const beforeCount = await page.getByTestId("meta-row-agent").count();

  const input = page.getByTestId("meta-composer-input");
  await input.click();
  await input.fill("");
  await input.pressSequentially(text, { delay: TYPE_DELAY });
  await page.waitForTimeout(BEFORE_ACT);
  await page.getByTestId("meta-composer-send").click();

  // In non-CI mode, the stub emits events with a delay → streaming bubble stays
  // visible for the duration of the agent call. Assert and screenshot it.
  if (PACE > 0 && opts.checkStream !== false) {
    const streamBubble = page.getByTestId("meta-row-streaming");
    await expect(streamBubble).toBeVisible({ timeout: 5000 });
    // Brain icon must be in the "who" label
    await expect(streamBubble.locator(".meta-row__who")).toContainText("🧠");
    // At least one streaming text element
    await expect(streamBubble.locator(".meta-row__text")).toBeVisible();
    // Tool breadcrumb — appears after the stub emits its tool event. The
    // bubble renders the shared ActivityFeed (same rows as the main chat).
    if (opts.checkTool !== false) {
      await expect(streamBubble.locator(".chat-activity__tool").first()).toBeVisible({ timeout: 5000 });
    }
    // Screenshot the streaming state: shows 🧠 + tool breadcrumb + "…" or partial text
    if (opts.streamShot) {
      await shot(page, opts.streamShot);
    }
    // Let the streaming animate for a moment so the video shows progression
    await page.waitForTimeout(800 * PACE);
  }

  await expect(page.getByTestId("meta-row-agent")).toHaveCount(beforeCount + 1, { timeout: 25000 });
}

/** Legacy helper for scenes that don't need stream assertions. */
async function metaSend(page: Page, text: string): Promise<void> {
  await metaSendWithStreamCheck(page, text, { checkStream: false });
}

test.describe("meta mode (live, no-LLM)", () => {
  test("home help → drive → story Q&A multi-round → story edit reload → persistence → new chat", async () => {
    test.setTimeout(PACE === 0 ? 60000 : 240000);

    const browser: Browser = await chromium.launch();
    const context: BrowserContext = await browser.newContext(
      cameraContext({ recordVideoDir: VIDEO_DIR }),
    );
    const page = await context.newPage();
    const video = page.video();
    const chapters = new ChapterRecorder();
    chapters.open("meta-home", "Meta mode with no session", CHAPTER_SOURCE);
    page.on("pageerror", (e) => console.log("PAGEERROR:", e.message));
    page.on("console", (m) => {
      if (m.type() === "error") console.log("CONSOLE.ERR:", m.text());
    });

    try {
      // ── Scene 1: home meta (no session) ────────────────────────────────────
      await page.goto(`${BASE}/#/`);
      await expect(page.getByTestId("home-view")).toBeVisible({ timeout: 15000 });
      await maybeInstallAutoRrwebCapture(page);
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

      await metaSendWithStreamCheck(page, "What is kitsoki in one sentence?", {
        checkTool: false,
        streamShot: "home-kitsoki-streaming",
      });
      const kitsokiReply = page.getByTestId("meta-row-agent").last();
      await expect(kitsokiReply).toBeVisible();
      await shot(page, "home-kitsoki-help");
      await page.waitForTimeout(DWELL);
      await page.getByTestId("meta-close").click();
      await expect(page.getByTestId("meta-overlay")).toHaveCount(0);

      // ── Scene 2: start a PRD session → chat surface ────────────────────────
      chapters.open("meta-drive", "Start a PRD session", CHAPTER_SOURCE);
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

      // ── Scene 3: Story Q&A round 1 — streaming visible ──────────────────────
      await page.getByTestId("meta-button").click();
      await expect(page.getByTestId("meta-menu")).toBeVisible();
      await expect(page.getByTestId("meta-mode-story-ask")).toBeEnabled({ timeout: 10000 });
      await page.getByTestId("meta-mode-story-ask").click();
      await expect(page.getByTestId("meta-overlay")).toBeVisible();
      await page.waitForTimeout(BEFORE_ACT);

      chapters.open("meta-story-qa", "Story Q&A — streaming reply", CHAPTER_SOURCE);
      await metaSendWithStreamCheck(page, "What state am I in, and what should I do next?", {
        streamShot: "story-qa-round1-brain-streaming",
      });

      // After streaming, the committed reply references the current state.
      const qaReply1 = page.getByTestId("meta-row-agent").last();
      await expect(qaReply1).toContainText("idle");
      await shot(page, "story-qa-round1-committed");
      // Hold on round 1 so the video viewer can read the exchange
      await page.waitForTimeout(DWELL);

      // ── Scene 4: Story Q&A round 2 — multi-round in the same chat ──────────
      chapters.open("meta-story-qa-2", "Story Q&A — second round", CHAPTER_SOURCE);
      await metaSendWithStreamCheck(page, "What options do I have from this state?", {
        streamShot: "story-qa-round2-brain-streaming",
      });
      const qaReply2 = page.getByTestId("meta-row-agent").last();
      await expect(qaReply2).toBeVisible();
      // Two committed agent rows now — proves multi-round
      await expect(page.getByTestId("meta-row-agent")).toHaveCount(2);
      await shot(page, "story-qa-round2-both-turns");
      // Hold long enough that the video clearly shows both exchanges together
      await page.waitForTimeout(DWELL * 2);

      // ── Scene 5: Story edit → in-place content reload ──────────────────────
      chapters.open("meta-story-edit", "Story edit → live reload", CHAPTER_SOURCE);
      await page.getByTestId("meta-tab-story-edit").click();
      await page.waitForTimeout(BEFORE_ACT);

      await metaSendWithStreamCheck(page, "Make the opening prompt a little warmer.", {
        streamShot: "story-edit-brain-streaming",
      });

      // The edit landed: reload note appears and contains the changed marker file.
      await expect(page.getByTestId("meta-reload-note")).toBeVisible({ timeout: 15000 });
      await expect(page.getByTestId("meta-reload-note")).toContainText("meta-edits.log");
      await shot(page, "story-edit-reload");
      await page.waitForTimeout(DWELL);

      // Count edit-mode rows so we can prove persistence next.
      const editMsgCount = await page.getByTestId("meta-transcript").locator(".meta-row").count();
      expect(editMsgCount).toBeGreaterThan(0);

      // ── Scene 6: persistence across navigation ─────────────────────────────
      await page.getByTestId("meta-close").click();
      await expect(page.getByTestId("meta-overlay")).toHaveCount(0);
      // Hop to the read-only observer.
      chapters.open("meta-persistence", "Conversation persists", CHAPTER_SOURCE);
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

      // ── Scene 7: new chat resets the conversation ──────────────────────────
      chapters.open("meta-new-chat", "New chat resets", CHAPTER_SOURCE);
      await page.getByTestId("meta-new").click();
      await expect(page.getByTestId("meta-transcript").locator(".meta-row")).toHaveCount(0, { timeout: 10000 });
      await shot(page, "new-chat");
      await page.waitForTimeout(FINAL_DWELL);
    } finally {
      chapters.close();
      await page.close();
      await context.close(); // flush the video
      // One shared encoder for every section's MP4 (identical libx264 settings),
      // plus the chapter sidecar beside it — so meta-mode stitches into the
      // master tour exactly like the tour-popover sections do.
      const mp4 = await saveVideoAsMp4(video, ARTIFACT_DIR, "meta-mode-demo");
      writeChapters(mp4, chapters.list());
      await browser.close();
      console.log("[meta-mode] screenshots:\n" + screenshotPaths.join("\n"));
      if (mp4) console.log("[meta-mode] video: " + mp4);
    }
  });
});
