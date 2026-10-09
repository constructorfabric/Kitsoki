import { test, expect, type Page } from "@playwright/test";
import { spawn, spawnSync, type ChildProcess } from "node:child_process";
import fs from "node:fs";
import net from "node:net";
import os from "node:os";
import path from "node:path";
import {
  ChapterRecorder,
  PACE,
  cameraContext,
  dwell,
  makeShot,
  maybeInstallAutoRrwebCapture,
  prepareVideoDir,
  saveVideoAsMp4,
  writeChapters,
} from "./_helpers/recording.js";

const REPO_ROOT = path.resolve(import.meta.dirname, "..", "..", "..");
const SOURCE_TARGET = process.env.KITSOKI_PRESENTATION_TARGET ?? "";
const ARTIFACT_DIR =
  process.env.KITSOKI_PRESENTATION_ARTIFACT_DIR ??
  path.join(SOURCE_TARGET, ".artifacts", "kitsoki-onboarding-demo");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "video");
const DB_PATH = path.join(ARTIFACT_DIR, "presentation-onboarding.sessions.db");
const TRACE_PATH = path.join(ARTIFACT_DIR, "presentation-onboarding.trace.jsonl");
const RUN_SUMMARY_PATH = path.join(ARTIFACT_DIR, "presentation-onboarding-run.json");
const INITIAL_DUMP_PATH = path.join(ARTIFACT_DIR, "presentation-onboarding.initial-dump.txt");
const CHAPTER_SOURCE = "tools/tui-bridge/tests/presentation-onboarding-real-tui.e2e.spec.ts";
const TYPE_DELAY_MS = PACE === 0 ? 0 : 32;
const COMMAND_HOLD_MS = 900;
const STEP_HOLD_MS = 1_500;
const FINAL_HOLD_MS = 3_500;

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

function copyFreshTarget(): { root: string; target: string } {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "kitsoki-presentation-onboarding-"));
  const target = path.join(root, "platform-presentation");
  fs.cpSync(SOURCE_TARGET, target, {
    recursive: true,
    filter(src) {
      const rel = path.relative(SOURCE_TARGET, src);
      if (!rel) return true;
      const top = rel.split(path.sep)[0];
      if ([".agents", ".artifacts", ".claude", ".kitsoki", ".worktrees"].includes(top)) return false;
      return ![".kitsoki.yaml", ".mcp.json"].includes(rel);
    },
  });
  return { root, target };
}

function startBridge(addr: string, workdir: string): ChildProcess {
  fs.mkdirSync(ARTIFACT_DIR, { recursive: true });
  fs.rmSync(DB_PATH, { force: true });
  fs.rmSync(TRACE_PATH, { force: true });
  const log = fs.createWriteStream(path.join(ARTIFACT_DIR, "presentation-onboarding.bridge.log"), { flags: "w" });
  const appPath = path.join(REPO_ROOT, "stories", "dev-story", "app.yaml");
  const proc = spawn(
    "go",
    [
      "run",
      "./cmd/kitsoki",
      "tui-serve",
      "--addr",
      addr,
      "--workdir",
      workdir,
      "--",
      "run",
      appPath,
      "--db",
      DB_PATH,
    ],
    {
      cwd: REPO_ROOT,
      stdio: ["ignore", "pipe", "pipe"],
      detached: true,
      env: { ...process.env, KITSOKI_TRACE_FILE: TRACE_PATH },
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

async function focusTerminal(page: Page): Promise<void> {
  await page.click("#term");
  await page.evaluate(() => (window as any).__focusTerm?.());
}

async function top(page: Page): Promise<string> {
  return page.evaluate(() => (window as any).__scrollToTop());
}

async function scrollToText(page: Page, text: string, contextLines = 2): Promise<string> {
  return page.evaluate((args) => (window as any).__scrollToText(args.text, args.contextLines), {
    text,
    contextLines,
  });
}

async function fullBuffer(page: Page): Promise<string> {
  return page.evaluate(() => (window as any).__dumpBuffer());
}

async function waitForBuffer(page: Page, text: string, timeout = 45_000): Promise<void> {
  await expect.poll(() => fullBuffer(page), { timeout }).toContain(text);
}

async function typeLine(
  page: Page,
  text: string,
  shot?: (page: Page, label: string) => Promise<string>,
  shotLabel?: string,
  opts: { chat?: boolean } = {},
): Promise<void> {
  await bottom(page);
  await focusTerminal(page);
  if (opts.chat) {
    await page.keyboard.press("Tab");
    await dwell(page, 300);
  }
  await page.keyboard.type(text, { delay: TYPE_DELAY_MS });
  await dwell(page, COMMAND_HOLD_MS);
  if (shot && shotLabel) {
    await shot(page, shotLabel);
  }
  await page.keyboard.press("Enter");
}

async function choiceCursorLine(page: Page): Promise<string> {
  const screen = await page.evaluate(() => (window as any).__dump());
  return String(screen)
    .split("\n")
    .map((line) => line.trim())
    .find((line) => line.includes("▸")) ?? "";
}

async function chooseDefaultAction(
  page: Page,
  shot: (page: Page, label: string) => Promise<string>,
  label: string,
  expectedCursorText: string,
): Promise<void> {
  await bottom(page);
  await focusTerminal(page);
  await expect.poll(() => choiceCursorLine(page), { timeout: 10_000 }).toContain(expectedCursorText);
  await dwell(page, STEP_HOLD_MS);
  await shot(page, label);
  await page.keyboard.press("Enter");
}

async function chooseActionByLabel(
  page: Page,
  shot: (page: Page, label: string) => Promise<string>,
  shotLabel: string,
  expectedCursorText: string,
): Promise<void> {
  await bottom(page);
  await focusTerminal(page);
  for (let i = 0; i < 24; i += 1) {
    const line = await choiceCursorLine(page);
    if (line.includes(expectedCursorText)) {
      await dwell(page, STEP_HOLD_MS);
      await shot(page, shotLabel);
      await page.keyboard.press("Enter");
      return;
    }
    await page.keyboard.press("ArrowDown");
    await expect.poll(() => choiceCursorLine(page), { timeout: 2_000 }).not.toBe(line);
    await dwell(page, 60);
  }
  throw new Error(`choice cursor did not reach ${expectedCursorText}`);
}

function validateSessionDb(): { turn_count: number; terminal_state: string; inputs: string[] } {
  if (!fs.existsSync(DB_PATH)) return { turn_count: 0, terminal_state: "", inputs: [] };
  const r = spawnSync(
    "sqlite3",
    [
      "-cmd",
      ".timeout 5000",
      "-json",
      DB_PATH,
      "select turn, seq, kind, payload_json from events where kind in ('turn.input','turn.end') order by turn, seq;",
    ],
    { encoding: "utf8" },
  );
  if (r.status !== 0) return { turn_count: 0, terminal_state: "", inputs: [] };
  const rows = JSON.parse(r.stdout || "[]") as Array<{ kind: string; payload_json: string }>;
  const inputs = rows
    .filter((row) => row.kind === "turn.input")
    .map((row) => JSON.parse(row.payload_json) as { input?: string; intent?: string })
    .map((payload) => payload.input || `[direct] intent=${payload.intent ?? ""}`);
  const ends = rows
    .filter((row) => row.kind === "turn.end")
    .map((row) => JSON.parse(row.payload_json) as { to?: string });
  const lastDone = ends.at(-1);
  return {
    turn_count: ends.length,
    terminal_state: String(lastDone?.to ?? ""),
    inputs,
  };
}

test("records project onboarding for platform-presentation through the real xterm TUI", async ({ browser }) => {
  test.setTimeout(240_000);
  test.skip(!SOURCE_TARGET, "set KITSOKI_PRESENTATION_TARGET to a platform-presentation checkout");
  test.skip(!fs.existsSync(SOURCE_TARGET), `KITSOKI_PRESENTATION_TARGET does not exist: ${SOURCE_TARGET}`);

  prepareVideoDir(VIDEO_DIR);
  if (process.env.KITSOKI_RRWEB_OUT) {
    fs.rmSync(process.env.KITSOKI_RRWEB_OUT, { force: true });
    fs.rmSync(`${process.env.KITSOKI_RRWEB_OUT}.chapters.json`, { force: true });
    fs.rmSync(
      process.env.KITSOKI_RRWEB_OUT.replace(/\.rrweb\.json$/, ".rrweb.capture.json"),
      { force: true },
    );
  }
  fs.rmSync(RUN_SUMMARY_PATH, { force: true });
  fs.rmSync(INITIAL_DUMP_PATH, { force: true });
  const demo = copyFreshTarget();
  const shot = makeShot(ARTIFACT_DIR);
  const port = await freePort();
  const addr = `127.0.0.1:${port}`;
  const bridge = startBridge(addr, demo.target);
  const context = await browser.newContext(cameraContext({ recordVideoDir: VIDEO_DIR }));
  const page = await context.newPage();
  const chapters = new ChapterRecorder();
  const screenshots: string[] = [];
  let mediaPath: string | null = null;

  try {
    await page.goto(`/player/?ws=ws://${addr}/pty`);
    await maybeInstallAutoRrwebCapture(page);
    await page.waitForFunction(() => (window as any).__ready === true);
    await expect
      .poll(() => page.evaluate(() => (window as any).__status()), { timeout: 60_000 })
      .toBe("connected");

    chapters.open("landing", "Kitsoki opens in the real xterm.js TUI", CHAPTER_SOURCE);
    await dwell(page, 2_000);
    fs.writeFileSync(INITIAL_DUMP_PATH, await fullBuffer(page));
    screenshots.push(await shot(page, "diagnostic-initial-screen"));
    await waitForBuffer(page, "WORKBENCH");
    await waitForBuffer(page, "Project onboarding");
    await dwell(page, STEP_HOLD_MS);
    screenshots.push(await shot(page, "landing-workbench"));

    chapters.open("request", "Select the onboarding quick action for platform-presentation", CHAPTER_SOURCE);
    await chooseActionByLabel(page, shot, "choose-onboard-quick-action", "onboard");

    chapters.open("discovery", "Discovery infers the presentation service profile", CHAPTER_SOURCE);
    await waitForBuffer(page, "PROJECT ONBOARDING", 90_000);
    await waitForBuffer(page, "Platform Presentation", 90_000);
    await waitForBuffer(page, "go project", 90_000);
    screenshots.push(await shot(page, "discovered-platform-profile"));
    await chooseDefaultAction(page, shot, "choose-continue-to-review", "continue");

    chapters.open("review", "Review the profile before writes", CHAPTER_SOURCE);
    await waitForBuffer(page, "review profile before writes");
    await waitForBuffer(page, "Commands");
    await waitForBuffer(page, "Test:");
    await waitForBuffer(page, "make build");
    await scrollToText(page, "review profile before writes", 1);
    await dwell(page, STEP_HOLD_MS);
    screenshots.push(await shot(page, "review-before-writes-top"));
    await bottom(page);
    await chooseDefaultAction(page, shot, "choose-accept-profile", "accept");

    chapters.open("apply", "Apply onboarding files and install local tools", CHAPTER_SOURCE);
    await waitForBuffer(page, "applying local setup", 90_000);
    await waitForBuffer(page, "studio MCP", 90_000);
    await waitForBuffer(page, "Profile validation", 90_000);
    screenshots.push(await shot(page, "applied-local-setup"));
    await chooseDefaultAction(page, shot, "choose-continue-to-result", "continue");

    chapters.open("done", "Applied result shows a valid project profile", CHAPTER_SOURCE);
    await waitForBuffer(page, "PROJECT ONBOARDING");
    await waitForBuffer(page, "applied");
    await waitForBuffer(page, "Profile valid:");
    await waitForBuffer(page, "platform-presentation-dev/app.yaml");
    await scrollToText(page, "PROJECT ONBOARDING  ·  applied", 1);
    await dwell(page, FINAL_HOLD_MS);
    screenshots.push(await shot(page, "final-applied-result-top"));
    await bottom(page);
    await dwell(page, STEP_HOLD_MS);
    screenshots.push(await shot(page, "final-applied-result-bottom"));
  } finally {
    const video = page.video();
    try {
      await context.close();
    } catch {
      // Interrupted and timed-out Playwright runs may have already closed it.
    }
    mediaPath = await saveVideoAsMp4(video, ARTIFACT_DIR, "presentation-onboarding-real-tui");
    const chaptersPath = writeChapters(mediaPath, chapters.list());
    stopBridge(bridge);
    fs.rmSync(demo.root, { recursive: true, force: true });

    const session = validateSessionDb();
    const rrwebStats =
      mediaPath && fs.existsSync(mediaPath)
        ? (() => {
            const parsed = JSON.parse(fs.readFileSync(mediaPath, "utf8")) as { events?: unknown[] };
            return { event_count: parsed.events?.length ?? 0 };
          })()
        : { event_count: 0 };
    fs.writeFileSync(
      RUN_SUMMARY_PATH,
      JSON.stringify(
        {
          schema: "kitsoki-presentation-onboarding-run/v1",
          source_target: SOURCE_TARGET,
          disposable_target: demo.target,
          media_path: mediaPath,
          chapters_path: chaptersPath,
          trace_path: fs.existsSync(TRACE_PATH) ? TRACE_PATH : null,
          db_path: DB_PATH,
          screenshots,
          rrweb: rrwebStats,
          session,
          visual_assertions: [
            "landing contains Project Onboarding",
            "discovery contains Platform Presentation and go project",
            "review contains inferred test command and make build",
            "apply contains studio MCP and profile validation",
            "done contains applied result and platform-presentation-dev/app.yaml",
          ],
        },
        null,
        2,
      ) + "\n",
    );
  }
});
