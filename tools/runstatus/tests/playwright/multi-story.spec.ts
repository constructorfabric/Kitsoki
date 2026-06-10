/**
 * multi-story.spec.ts — the end-to-end product demo for the multi-story web UI,
 * driven against a REAL `kitsoki web` server in the deterministic no-LLM posture
 * (--flow stories/prd/flows/happy_path.yaml: host responses stubbed, harness
 * nil, intents submitted explicitly — no LLM is ever called).
 *
 * This spec supersedes the old single-session web-chat.spec.ts (whose
 * positional-<app.yaml> invocation no longer exists). It validates the FULL
 * product experience the web-multi-story epic shipped, recording a
 * MacBook-resolution video + per-scene screenshots for visual verification:
 *
 *   1. Discovery / home   — `/` lists the discovered PRD story (card: title,
 *                           path, active-count badge, New-session, Rescan).
 *   2. Start a session    — clicking "New session" mints a session and lands on
 *                           its run view, with a Stories breadcrumb + Reload.
 *   3. Reload parity      — the Reload button mirrors the TUI /reload; with the
 *                           current state intact it shows no warning.
 *   4. Drive the workflow — the PRD happy path idle → clarifying → brief →
 *                           references → drafting → @exit:done, scene by scene
 *                           in the chat view, asserting the state badge.
 *   5. Active sessions    — a second session created via the live session.new
 *                           RPC appears in the home active-sessions table; the
 *                           Open link routes to the run view.
 *
 * Acceptance bar: the product drives cleanly end-to-end with no LLM, every new
 * RPC (stories.list / session.new / session.reload / sessions.list) exercised
 * through the real UI, and the recorded video is the reviewable deliverable.
 */
import { test, expect, chromium, type Browser, type BrowserContext, type Page } from "@playwright/test";
import { fileURLToPath } from "url";
import path from "path";
import fs from "fs";
import os from "os";
import { spawn, type ChildProcess } from "child_process";
import { prepareVideoDir, saveVideoAsMp4 } from "./_helpers/server.js";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

// repoRoot = .../kitsoki (tools/runstatus/tests/playwright -> 4 up)
const repoRoot = path.resolve(__dirname, "../../../..");
const BIN = path.join(repoRoot, "bin", "kitsoki");
// Point at the whole stories/ tree so the home screen shows the real catalogue
// (every app.yaml under here is discovered). The deterministic no-LLM --flow
// posture is prd-specific (it encodes prd's intents + host stubs and is applied
// to every session runtimeBase creates), so the demo only *drives* the PRD card;
// the other discovered stories are browsed, not instantiated, here.
const STORIES_DIR = path.join(repoRoot, "stories");
const FLOW = path.join(repoRoot, "stories", "prd", "flows", "happy_path.yaml");

const ADDR = "127.0.0.1:7740";
const BASE = `http://${ADDR}`;
const RPC = `${BASE}/rpc`;

const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "multi-story");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "video");

// ── server lifecycle ────────────────────────────────────────────────────────

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
  prepareVideoDir(VIDEO_DIR); // clears stale .webm files; must run before context creation
  // Clean stale screenshots from a prior run so the artifact set is exact.
  if (fs.existsSync(ARTIFACT_DIR)) {
    for (const f of fs.readdirSync(ARTIFACT_DIR)) {
      if (f.endsWith(".png")) fs.rmSync(path.join(ARTIFACT_DIR, f));
    }
  }

  tmpDbDir = fs.mkdtempSync(path.join(os.tmpdir(), "kitsoki-multi-story-"));
  const dbPath = path.join(tmpDbDir, "s.db");

  // The NEW entrypoint: no positional app.yaml — discover stories under
  // --stories-dir and apply the deterministic --flow posture to EVERY session
  // created from the home screen.
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

// ── the chat scenario (PRD happy path) ──────────────────────────────────────
// Each scene maps a happy_path turn to a UI action + the state it should land
// in. noAgentView marks a turn whose landed state renders no room view (a
// terminal exit), so the UI correctly pushes no agent bubble.
type Scene =
  | { kind: "text"; intent: string; slot: string; text: string; expectState: string; label: string; noAgentView?: boolean }
  | { kind: "action"; intent: string; expectState: string; label: string; noAgentView?: boolean };

const SCENES: Scene[] = [
  { kind: "text", intent: "discuss", slot: "message", text: "I want a CLI for X", expectState: "idle", label: "discuss" },
  { kind: "action", intent: "start", expectState: "clarifying", label: "start" },
  { kind: "text", intent: "submit_answers", slot: "answers", text: "1) developers 2) time-to-first-success", expectState: "brief", label: "submit_answers" },
  { kind: "action", intent: "confirm", expectState: "references", label: "confirm-brief" },
  { kind: "action", intent: "confirm", expectState: "drafting", label: "confirm-references" },
  { kind: "action", intent: "accept", expectState: "__exit__done", label: "accept", noAgentView: true },
];

// Pacing: the recorded video is the deliverable, so by default we drive at a
// human-watchable speed. Set WEB_CHAT_PACE=0 to collapse all delays for a fast
// assertion-only CI run.
const PACE = process.env.WEB_CHAT_PACE === "0" ? 0 : 1;
const TYPE_DELAY = 55 * PACE; // per-keystroke, so typing is visible
const BEFORE_ACT = 900 * PACE; // beat before send/click so the control is seen
const DWELL = 2400 * PACE; // read the landed room before the next turn
const OPEN_DWELL = 3000 * PACE; // first frame of a screen
const FINAL_DWELL = 4500 * PACE; // linger on the completed run

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

test.describe("multi-story web UI (live, no-LLM)", () => {
  test("home → new session → reload → drive PRD happy path → active sessions", async () => {
    test.setTimeout(180000);

    // Startup discovers the whole stories/ catalogue but creates no sessions.
    const stories = await rpc<Array<{ path: string; app_id: string; title: string; active_sessions: string[] }>>(
      "runstatus.stories.list",
      {},
    );
    expect(stories.length, "expected the full stories/ catalogue").toBeGreaterThan(5);
    const prd = stories.find((s) => s.app_id === "prd");
    expect(prd, "PRD story is in the catalogue").toBeTruthy();
    const storyPath = prd!.path;
    const noSessions = await rpc<Array<unknown>>("runstatus.sessions.list", {});
    expect(noSessions.length, "no sessions exist until one is created").toBe(0);

    const browser: Browser = await chromium.launch();
    const context: BrowserContext = await browser.newContext({
      viewport: { width: 1440, height: 900 }, // MacBook (13") logical resolution
      deviceScaleFactor: 2, // retina
      recordVideo: { dir: VIDEO_DIR, size: { width: 1440, height: 900 } },
    });
    const page = await context.newPage();
    // Capture before context closes — saveVideoAsMp4 needs this reference.
    const video = page.video();

    try {
      // ── Scene 1: discovery / home ──────────────────────────────────────────
      await page.goto(`${BASE}/#/`);
      await expect(page.getByTestId("home-view")).toBeVisible({ timeout: 15000 });
      // The full catalogue renders one card per discovered story…
      await expect(page.getByTestId("story-card").nth(5)).toBeVisible({ timeout: 15000 });
      expect(await page.getByTestId("story-card").count()).toBeGreaterThan(5);
      // …and we drive the PRD card specifically.
      const card = page
        .getByTestId("story-card")
        .filter({ has: page.getByTestId("story-title").filter({ hasText: "PRD authoring" }) });
      await expect(card).toBeVisible();
      await expect(card.getByTestId("story-title")).toHaveText("PRD authoring");
      // No live-session badge yet (it only renders once a session exists).
      await expect(card.getByTestId("story-active-count")).toHaveCount(0);
      // The Rescan control re-walks the story dirs (explicit, no fsnotify).
      await expect(page.getByTestId("rescan-btn")).toBeVisible();
      await shot(page, "home-discovery");
      await page.waitForTimeout(OPEN_DWELL);

      // ── Scene 2: start a session → lands on the DRIVE (chat) surface ───────
      await page.waitForTimeout(BEFORE_ACT);
      await card.getByTestId("new-session-btn").click();
      // A fresh session is live and opens directly on its chat surface — the
      // operator can act immediately (the opening prompt + composer are right
      // there), instead of dead-ending on the read-only observer.
      await page.waitForURL(/#\/s\/[0-9a-f-]{36}\/chat$/, { timeout: 15000 });
      const sid1 = page.url().match(/#\/s\/([0-9a-f-]{36})\/chat$/)?.[1];
      expect(sid1, "new session id captured from the route").toBeTruthy();
      await expect(page.getByTestId("chat-section")).toBeVisible({ timeout: 15000 });
      await expect(page.getByTestId("current-state")).toHaveText("idle");
      await expect(
        page.getByTestId("chat-transcript").getByTestId("chat-row-agent").first(),
      ).toBeVisible();
      await shot(page, "session-chat-landing");
      await page.waitForTimeout(OPEN_DWELL);

      // ── Scene 3: Drive ⇄ Observe cross-link + reload parity ────────────────
      // Hop to the read-only observer via its link, exercise the in-place Reload
      // (state intact → no "current state removed" warning), then hop straight
      // back to driving via the observer's "Drive (chat)" call-to-action — so
      // neither surface is ever a dead-end.
      await page.waitForTimeout(BEFORE_ACT);
      await page.getByTestId("observe-link").click();
      await page.waitForURL(/#\/s\/[0-9a-f-]{36}$/, { timeout: 15000 });
      await expect(page.getByTestId("breadcrumb")).toBeVisible();
      await expect(page.getByTestId("reload-button")).toBeVisible();
      await expect(page.getByTestId("drive-link"), "observer offers a way back to driving").toBeVisible();
      await shot(page, "observer");
      await page.waitForTimeout(BEFORE_ACT);
      await page.getByTestId("reload-button").click();
      await page.waitForTimeout(1200);
      await expect(page.getByTestId("reload-warning")).toHaveCount(0);
      await shot(page, "reload-ok");
      await page.waitForTimeout(DWELL);
      await page.getByTestId("drive-link").click();
      await page.waitForURL(/#\/s\/[0-9a-f-]{36}\/chat$/, { timeout: 15000 });

      // ── Scene 4: drive the PRD happy path in the chat view (no LLM) ─────────
      await expect(page.getByTestId("chat-section")).toBeVisible({ timeout: 15000 });
      await expect(page.getByTestId("current-state")).toHaveText("idle");
      await expect(
        page.getByTestId("chat-transcript").getByTestId("chat-row-agent").first(),
      ).toBeVisible();
      await shot(page, "chat-idle-open");
      await page.waitForTimeout(OPEN_DWELL);

      for (const scene of SCENES) {
        const prevAgentCount = await page
          .getByTestId("chat-transcript")
          .getByTestId("chat-row-agent")
          .count();

        if (scene.kind === "text") {
          const select = page.getByTestId("composer-select");
          if ((await select.count()) > 0) {
            await select.selectOption(scene.intent);
          }
          const input = page.getByTestId("composer-input");
          await input.click();
          await input.fill("");
          await input.pressSequentially(scene.text, { delay: TYPE_DELAY });
          await page.waitForTimeout(BEFORE_ACT);
          await page.getByTestId("composer-send").click();
        } else {
          await page.waitForTimeout(BEFORE_ACT);
          await page.getByTestId(`intent-btn-${scene.intent}`).click();
        }

        // The state badge is the hard signal the turn applied.
        await expect(page.getByTestId("current-state")).toHaveText(scene.expectState, {
          timeout: 15000,
        });

        if (scene.noAgentView) {
          await expect(
            page.getByTestId("chat-transcript").getByTestId("chat-row-agent"),
          ).toHaveCount(prevAgentCount);
        } else {
          await expect(
            page.getByTestId("chat-transcript").getByTestId("chat-row-agent"),
          ).toHaveCount(prevAgentCount + 1, { timeout: 15000 });
        }

        await shot(page, `chat-${scene.expectState}`);
        await page.waitForTimeout(DWELL);
      }

      // Terminal: badge flips to done, input replaced by the done note.
      await expect(page.getByTestId("state-badge")).toHaveAttribute("data-terminal", "true");
      await expect(page.locator(".iv__done-note")).toBeVisible();
      // The trace timeline accumulated events across the run.
      const traceRows = page.locator(".iv__panel--timeline .trace-timeline__row");
      expect(await traceRows.count(), "expected accumulated trace rows").toBeGreaterThan(0);
      await page.waitForTimeout(FINAL_DWELL);

      // ── Scene 5: active-sessions list + Open ───────────────────────────────
      // Create a second session via the live RPC so the home table is populated
      // (with exactly one session, the home auto-navigates straight into it — a
      // deliberate single-session convenience; two sessions exercise the list).
      const sid2 = await rpc<{ session_id: string }>("runstatus.session.new", {
        story_path: storyPath,
      });
      expect(sid2.session_id).toBeTruthy();

      await page.goto(`${BASE}/#/`);
      await expect(page.getByTestId("home-view")).toBeVisible({ timeout: 15000 });
      const rows = page.getByTestId("session-row");
      await expect(rows, "both live sessions are listed").toHaveCount(2);
      // The driven session reached the terminal exit state; the fresh one is
      // idle. The table reports the raw current_state from the trace (the exit
      // node is "__exit__done"), faithfully — no display rewriting.
      const states = await page.getByTestId("session-state").allTextContents();
      expect(states.map((s) => s.trim()).sort()).toEqual(["__exit__done", "idle"]);
      // The PRD card's active-session badge now reflects its two live sessions.
      const prdCard = page
        .getByTestId("story-card")
        .filter({ has: page.getByTestId("story-title").filter({ hasText: "PRD authoring" }) });
      await expect(prdCard.getByTestId("story-active-count")).toContainText("2 live");
      await shot(page, "home-active-sessions");
      await page.waitForTimeout(DWELL);

      // Open routes back into a run view.
      await page.getByTestId("session-open").first().click();
      await page.waitForURL(/#\/s\/[0-9a-f-]{36}$/, { timeout: 15000 });
      await expect(page.getByTestId("breadcrumb")).toBeVisible();
      await shot(page, "open-session");
      await page.waitForTimeout(FINAL_DWELL);
    } finally {
      await page.close();
      await context.close(); // finalises the video
      await saveVideoAsMp4(video, ARTIFACT_DIR, "multi-story-demo");
      await browser.close();
    }

    console.log("[multi-story] screenshots:\n" + screenshotPaths.join("\n"));
  });
});
