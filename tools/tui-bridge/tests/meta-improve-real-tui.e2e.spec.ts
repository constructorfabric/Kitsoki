import { test, expect, type Page } from "@playwright/test";
import { spawn, type ChildProcess } from "node:child_process";
import fs from "node:fs";
import net from "node:net";
import path from "node:path";
import {
  ChapterRecorder,
  PACE,
  cameraContext,
  dwell,
  makeShot,
  prepareVideoDir,
  saveVideoAsMp4,
  writeChapters,
} from "./_helpers/recording.js";

const REPO_ROOT = path.resolve(import.meta.dirname, "..", "..", "..");
const ARTIFACT_DIR = path.join(REPO_ROOT, ".artifacts", "tui-bridge", "meta-improve-real-tui");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "video");
const DB_PATH = path.join(ARTIFACT_DIR, "sessions.db");
const STORY = "testdata/apps/cloak/app.yaml";
const RECORDING = "testdata/apps/cloak/recording.yaml";
const CHAPTER_SOURCE = "tools/tui-bridge/tests/meta-improve-real-tui.e2e.spec.ts";
const TYPE_DELAY_MS = PACE === 0 ? 0 : 35;
const COMMAND_HOLD_MS = 700;
const STEP_HOLD_MS = 1_500;
const REPORT_HOLD_MS = 4_000;

async function freePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const srv = net.createServer();
    srv.once("error", reject);
    srv.listen(0, "127.0.0.1", () => {
      const addr = srv.address();
      const port = typeof addr === "object" && addr ? addr.port : 0;
      srv.close(() => resolve(port));
    });
  });
}

function startBridge(addr: string): ChildProcess {
  fs.mkdirSync(ARTIFACT_DIR, { recursive: true });
  fs.rmSync(DB_PATH, { force: true });
  const log = fs.createWriteStream(path.join(ARTIFACT_DIR, "bridge.log"), { flags: "w" });
  const metaDelay =
    process.env.KITSOKI_META_STREAM_DELAY_MS ?? (PACE === 0 ? "60" : "180");
  const proc = spawn(
    "go",
    [
      "run",
      "./cmd/kitsoki",
      "tui-serve",
      "--addr",
      addr,
      "--",
      "run",
      STORY,
      "--harness",
      "replay",
      "--recording",
      RECORDING,
      "--db",
      DB_PATH,
    ],
    {
      cwd: REPO_ROOT,
      stdio: ["ignore", "pipe", "pipe"],
      detached: true,
      env: { ...process.env, KITSOKI_META_STREAM_DELAY_MS: metaDelay },
    },
  );
  proc.stdout?.pipe(log, { end: false });
  proc.stderr?.pipe(log, { end: false });
  proc.once("exit", (code, signal) => {
    log.write(`\n[bridge exit] code=${code ?? ""} signal=${signal ?? ""}\n`);
    log.end();
  });
  return proc;
}

function stopBridge(proc: ChildProcess): void {
  if (!proc.pid) return;
  try {
    process.kill(-proc.pid, "SIGKILL");
  } catch {
    proc.kill("SIGKILL");
  }
}

async function bottom(page: Page): Promise<string> {
  return page.evaluate(() => (window as any).__scrollToBottom());
}

async function fullBuffer(page: Page): Promise<string> {
  return page.evaluate(() => (window as any).__dumpBuffer());
}

async function scrollLines(page: Page, lines: number): Promise<void> {
  await page.evaluate((n) => (window as any).__scrollLines(n), lines);
}

async function waitForBuffer(page: Page, text: string, timeout = 30_000): Promise<void> {
  await expect.poll(() => fullBuffer(page), { timeout }).toContain(text);
}

async function typeLine(
  page: Page,
  text: string,
  shot?: (page: Page, label: string) => Promise<string>,
  shotLabel?: string,
): Promise<void> {
  await bottom(page);
  await page.click("#term");
  await page.keyboard.type(text, { delay: TYPE_DELAY_MS });
  await dwell(page, COMMAND_HOLD_MS);
  if (shot && shotLabel) {
    await shot(page, shotLabel);
  }
  await page.keyboard.press("Enter");
}

test("records a real Kitsoki TUI completion reminder and /meta improve report", async ({ browser }) => {
  test.setTimeout(180_000);

  prepareVideoDir(VIDEO_DIR);
  const shot = makeShot(ARTIFACT_DIR);
  const port = await freePort();
  const addr = `127.0.0.1:${port}`;
  const bridge = startBridge(addr);
  const context = await browser.newContext(cameraContext({ recordVideoDir: VIDEO_DIR }));
  const page = await context.newPage();
  const chapters = new ChapterRecorder();

  try {
    await page.goto(`/player/?ws=ws://${addr}/pty`);
    await page.waitForFunction(() => (window as any).__ready === true);
    await expect
      .poll(() => page.evaluate(() => (window as any).__status()), { timeout: 60_000 })
      .toBe("connected");

    chapters.open("initial", "Cloak opens in the real Kitsoki TUI", CHAPTER_SOURCE);
    await waitForBuffer(page, "FOYER", 60_000);
    await dwell(page, STEP_HOLD_MS);
    await shot(page, "initial-foyer");

    chapters.open("brief-run", "Drive a short no-LLM story session", CHAPTER_SOURCE);
    await typeLine(page, "go west", shot, "typed-go-west");
    await waitForBuffer(page, "CLOAKROOM");
    await shot(page, "cloakroom");
    await typeLine(page, "hang the cloak", shot, "typed-hang-cloak");
    await waitForBuffer(page, "You hang the cloak on the hook.");
    await typeLine(page, "head east", shot, "typed-head-east");
    await waitForBuffer(page, "FOYER");
    await typeLine(page, "go south", shot, "typed-go-south");
    await waitForBuffer(page, "BAR (LIT)");
    await shot(page, "bar-lit");

    chapters.open("completion-reminder", "Terminal state recommends improve", CHAPTER_SOURCE);
    await typeLine(page, "read the message", shot, "typed-read-message");
    await waitForBuffer(page, "THE END");
    await waitForBuffer(page, "Improve this run");
    await waitForBuffer(page, "/meta improve");
    await bottom(page);
    await dwell(page, STEP_HOLD_MS);
    await shot(page, "terminal-improve-reminder");

    chapters.open("meta-command", "Open the actual /meta improve mode", CHAPTER_SOURCE);
    await typeLine(page, "/meta improve", shot, "typed-meta-improve");
    await waitForBuffer(page, "Reviewing this run for story prompt, tool, and flow-test improvements");
    await bottom(page);
    await dwell(page, STEP_HOLD_MS);
    await shot(page, "meta-improve-open");

    chapters.open("improvement-report", "Ask story.improve for a concrete report", CHAPTER_SOURCE);
    await typeLine(
      page,
      "Review the completed run and recommend one concrete improvement.",
      shot,
      "typed-improve-request",
    );
    await waitForBuffer(page, "Introspection report", 60_000);
    await waitForBuffer(page, "Evidence bundle", 60_000);
    await waitForBuffer(page, "Posting", 60_000);
    await waitForBuffer(page, "Tool and permission notes", 60_000);
    await waitForBuffer(page, "Regression coverage", 60_000);
    await scrollLines(page, -16);
    await dwell(page, STEP_HOLD_MS);
    await shot(page, "introspection-report-top");
    await bottom(page);
    await dwell(page, REPORT_HOLD_MS);
    await shot(page, "introspection-report-bottom");
  } finally {
    const video = page.video();
    await context.close();
    const videoPath = await saveVideoAsMp4(video, ARTIFACT_DIR, "meta-improve-real-tui");
    writeChapters(videoPath, chapters.list());
    stopBridge(bridge);
  }
});
