/**
 * Persona QA required-input evidence report.
 *
 * Produces one scenario-qa Slidey report with REAL rrweb user-session replays
 * from the web UI and the Kitsoki TUI bridge. It also opens the deck in the
 * live Slidey viewer, clicks through several slides, and opens an rrweb artifact
 * from an evidence row in the popout replay modal. No MP4 render and no bundled
 * HTML are produced.
 *
 * Validate/produce:
 *   KITSOKI_WEB_GO_RUN=1 pnpm exec playwright test persona-qa-affordance-report --project=chromium
 */
import { test, expect, chromium, type BrowserContext, type Page } from "@playwright/test";
import { spawn, spawnSync, type ChildProcess } from "child_process";
import fs from "fs";
import net from "net";
import path from "path";
import {
  startWebServer,
  repoRoot,
  cinematicGoto,
  dwell,
  demoAddr,
  makeShot,
  type WebServer,
} from "./_helpers/server.js";
import { cameraContext } from "./_helpers/camera.js";
import { captureSidecarPath, dumpCapture, installCapture, writeEvents } from "./_helpers/rrweb-replay.js";

const ADDR = demoAddr(7794);
const STORY_DIR = path.join(repoRoot, "stories", "scenario-qa");
const FLOW = path.join(STORY_DIR, "flows", "persona_qa_demo.yaml");
const RECORDING = path.join(STORY_DIR, "recording.yaml");
const HOST_CASSETTE = path.join(STORY_DIR, "flows", "persona_qa_demo.cassette.yaml");
const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "persona-qa-affordance-report");
const RUN_ID = "persona-qa-affordance-required-input";
const RUN_DIR = path.join(ARTIFACT_DIR, RUN_ID);
const CLIPS_DIR = path.join(RUN_DIR, "clips");
const RAW_CLIPS_DIR = path.join(ARTIFACT_DIR, "_raw-clips", RUN_ID);
const OPEN_DIR = path.join(ARTIFACT_DIR, "opened");
const DIAG_LOG = path.join(ARTIFACT_DIR, "diagnostic.log");
const TUI_DB_PATH = path.join(ARTIFACT_DIR, "tui-session.db");

const SCENARIO = "required user input affordance as a QA engineer and developer";
const SCENARIO_REQUEST = `${SCENARIO} transport=web,tui target=kitsoki seed=affordance-demo`;
const PREVIEW_REQUEST = `preview ${SCENARIO_REQUEST}`;
const WEB_RRWEB = path.join(CLIPS_DIR, "web-required-input.rrweb.json");
const TUI_RRWEB = path.join(CLIPS_DIR, "tui-required-input.rrweb.json");
const RAW_WEB_RRWEB = path.join(RAW_CLIPS_DIR, "web-required-input.raw.rrweb.json");
const RAW_TUI_RRWEB = path.join(RAW_CLIPS_DIR, "tui-required-input.raw.rrweb.json");

// This spec is an artifact producer. Keep its generated rrweb clips readable
// even when the Playwright invocation uses WEB_CHAT_PACE=0 for fast assertions.
// Set PERSONA_QA_REPLAY_PACE=0 only when intentionally producing throwaway
// speed-run evidence.
const REPLAY_PACE = Number(process.env.PERSONA_QA_REPLAY_PACE ?? "1");
const TYPE_DELAY_MS = REPLAY_PACE <= 0 ? 0 : Math.round(38 * REPLAY_PACE);

let server: WebServer;

function diag(msg: string): void {
  try {
    fs.appendFileSync(DIAG_LOG, `[${new Date().toISOString()}] ${msg}\n`);
  } catch {
    /* best effort */
  }
}

function localSlideyIndex(): string {
  return path.resolve(repoRoot, "..", "studio-slidey", "src", "index.js");
}

function slideyCommand(): { cmd: string; argsPrefix: string[]; cwd: string } {
  const local = localSlideyIndex();
  if (fs.existsSync(local)) {
    return { cmd: process.execPath, argsPrefix: [local], cwd: path.dirname(path.dirname(local)) };
  }
  if (process.env.SLIDEY_BIN) return { cmd: process.env.SLIDEY_BIN, argsPrefix: [], cwd: repoRoot };
  const homeBin = path.join(process.env.HOME ?? "", ".local", "bin", "slidey");
  return { cmd: fs.existsSync(homeBin) ? homeBin : "slidey", argsPrefix: [], cwd: repoRoot };
}

function replayMs(ms: number): number {
  return Math.round(ms * (Number.isFinite(REPLAY_PACE) ? REPLAY_PACE : 1));
}

async function replayDwell(page: Page, ms: number): Promise<void> {
  const delay = replayMs(ms);
  if (delay > 0) await page.waitForTimeout(delay);
}

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

async function waitForHttp(url: string, timeoutMs = 90_000): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  let last = "";
  while (Date.now() < deadline) {
    try {
      const res = await fetch(url);
      if (res.ok) return;
      last = `status ${res.status}`;
    } catch (err) {
      last = err instanceof Error ? err.message : String(err);
    }
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
  throw new Error(`timed out waiting for ${url}: ${last}`);
}

function pipeProcessLog(proc: ChildProcess, logPath: string): void {
  fs.mkdirSync(path.dirname(logPath), { recursive: true });
  const log = fs.createWriteStream(logPath, { flags: "w" });
  proc.stdout?.pipe(log, { end: false });
  proc.stderr?.pipe(log, { end: false });
  proc.once("exit", (code, signal) => {
    log.write(`\n[exit] code=${code ?? ""} signal=${signal ?? ""}\n`);
    log.end();
  });
}

function stopProcess(proc: ChildProcess | undefined): void {
  if (!proc?.pid) return;
  try {
    process.kill(-proc.pid, "SIGKILL");
  } catch {
    proc.kill("SIGKILL");
  }
}

async function stamp(page: Page, id: string, label: string): Promise<void> {
  await page.evaluate(([eventId, eventLabel]) => {
    const rrweb = (window as unknown as { rrweb?: { record?: { addCustomEvent?: (tag: string, payload: unknown) => void } } }).rrweb;
    rrweb?.record?.addCustomEvent?.("slidey.chapter", {
      id: eventId,
      label: eventLabel,
      specPath: "persona-qa-affordance-report",
    });
  }, [id, label]);
}

const CHAT_SCROLL_CONTROL = `(() => {
  const el = document.querySelector('[data-testid="chat-transcript"]');
  if (!el) return false;
  if (el.__personaQaReveal) return true;
  el.__personaQaReveal = true;
  const desc = Object.getOwnPropertyDescriptor(Element.prototype, 'scrollTop');
  const realGet = () => desc.get.call(el);
  const realSet = (v) => desc.set.call(el, v);
  Object.defineProperty(el, 'scrollTop', {
    configurable: true,
    get() { return realGet(); },
    set() { /* native snap-to-bottom ignored; replay camera owns scroll */ },
  });
  window.__personaQaEase = (to, ms) => new Promise((res) => {
    const from = realGet();
    const max = el.scrollHeight - el.clientHeight;
    const target = Math.max(0, Math.min(to, max));
    if (ms <= 0 || Math.abs(target - from) < 2) { realSet(target); return res(); }
    const t0 = performance.now();
    const tick = (now) => {
      const p = Math.min(1, (now - t0) / ms);
      const eased = p < 0.5 ? 2 * p * p : 1 - Math.pow(-2 * p + 2, 2) / 2;
      realSet(from + (target - from) * eased);
      if (p < 1) requestAnimationFrame(tick); else res();
    };
    requestAnimationFrame(tick);
  });
  window.__personaQaLastUserTop = () => {
    const rows = el.querySelectorAll('[data-testid="chat-row-user"]');
    const last = rows[rows.length - 1];
    return last ? Math.max(0, last.offsetTop - 16) : el.scrollHeight;
  };
  window.__personaQaScrollMax = () => el.scrollHeight - el.clientHeight;
  return true;
})()`;

async function installWebConversationReveal(page: Page): Promise<void> {
  await expect.poll(() => page.evaluate(CHAT_SCROLL_CONTROL), { timeout: 10_000 }).toBeTruthy();
}

async function easeWebChat(page: Page, to: number, ms: number): Promise<void> {
  await page.evaluate(
    async ([target, duration]) => {
      await (window as unknown as { __personaQaEase?: (to: number, ms: number) => Promise<void> }).__personaQaEase?.(
        target as number,
        duration as number,
      );
    },
    [to, ms],
  );
}

async function revealWebTurn(page: Page): Promise<void> {
  if (REPLAY_PACE <= 0) return;
  await installWebConversationReveal(page);
  await replayDwell(page, 700);
  const top = await page.evaluate(() => {
    return (window as unknown as { __personaQaLastUserTop?: () => number }).__personaQaLastUserTop?.() ?? 0;
  });
  await easeWebChat(page, top, replayMs(1200));
  await replayDwell(page, 1200);
  const max = await page.evaluate(() => {
    return (window as unknown as { __personaQaScrollMax?: () => number }).__personaQaScrollMax?.() ?? 0;
  });
  const span = Math.max(0, max - top);
  const downMs = Math.min(3600, Math.max(900, Math.round(span * 4)));
  await easeWebChat(page, max, replayMs(downMs));
  await replayDwell(page, 1100);
}

async function typeAndSend(page: Page, text: string): Promise<void> {
  const input = page.getByTestId("text-floor-input").or(page.getByTestId("composer-input")).first();
  await expect(input).toBeVisible({ timeout: 15_000 });
  await input.scrollIntoViewIfNeeded().catch(() => undefined);
  await input.click();
  await input.fill("");
  await input.pressSequentially(text, { delay: TYPE_DELAY_MS });
  await replayDwell(page, 1300);
  const send = page.getByTestId("text-floor-send").or(page.getByTestId("composer-send")).first();
  await send.evaluate((el) => (el as HTMLButtonElement).click());
}

function runSlideyCli(args: string[], label: string): void {
  const slidey = slideyCommand();
  const result = spawnSync(slidey.cmd, [...slidey.argsPrefix, ...args], {
    cwd: slidey.cwd,
    encoding: "utf8",
  });
  expect(result.status, `${label}\n${result.stdout}\n${result.stderr}`).toBe(0);
  diag(`${label}: ${(result.stderr || result.stdout || "").trim()}`);
}

function rrwebDurationMs(eventsPath: string): number {
  const raw = JSON.parse(fs.readFileSync(eventsPath, "utf8"));
  const events = Array.isArray(raw) ? raw : raw.events;
  expect(Array.isArray(events), `${eventsPath} should contain an rrweb event array`).toBeTruthy();
  if (events.length < 2) return 0;
  return Number(events[events.length - 1].timestamp) - Number(events[0].timestamp);
}

function polishRrwebClip(
  rawPath: string,
  outPath: string,
  opts: { repace?: boolean; minDurationMs?: number; maxDurationMs?: number } = {},
): void {
  const repace = opts.repace ?? true;
  const revealedPath = outPath.replace(/\.rrweb\.json$/i, ".revealed.rrweb.json");
  runSlideyCli(
    [
      "rrweb-reveal",
      rawPath,
      revealedPath,
      "--hold",
      "850",
      "--ease-min",
      "950",
      "--ease-max",
      "3400",
      "--per-px",
      "4",
      "--tail-hold",
      "900",
    ],
    `rrweb reveal ${path.basename(outPath)}`,
  );
  if (repace) {
    runSlideyCli(
      [
        "rrweb-repace",
        revealedPath,
        outPath,
        "--min-dwell",
        "1200",
        "--max-dwell",
        "2400",
        "--per-char",
        "10",
        "--coalesce",
        "180",
        "--hold",
        "1400",
      ],
      `rrweb repace ${path.basename(outPath)}`,
    );
  } else {
    fs.copyFileSync(revealedPath, outPath);
    diag(`rrweb repace skipped for ${path.basename(outPath)}; reveal track is the readable terminal pacing`);
  }
  fs.rmSync(revealedPath, { force: true });
  const rawSidecar = captureSidecarPath(rawPath);
  if (fs.existsSync(rawSidecar)) {
    fs.copyFileSync(rawSidecar, captureSidecarPath(outPath));
  }
  const duration = rrwebDurationMs(outPath);
  expect(duration, `${path.basename(outPath)} should be long enough to watch`).toBeGreaterThanOrEqual(
    opts.minDurationMs ?? 24_000,
  );
  expect(duration, `${path.basename(outPath)} should not be padded into dead air`).toBeLessThanOrEqual(
    opts.maxDurationMs ?? 150_000,
  );
}

async function captureWebSession(context: BrowserContext): Promise<void> {
  const page = await context.newPage();
  await cinematicGoto(page, `${server.base}/#/`, { waitForTestId: "home-view" });
  await page.getByTestId("new-session-btn").first().click();
  await page.waitForURL(/#\/s\/[0-9a-f-]{36}\/chat$/, { timeout: 15_000 });
  await expect(page.getByTestId("chat-section")).toBeVisible({ timeout: 15_000 });
  await installCapture(page);
  await installWebConversationReveal(page);

  await stamp(page, "web-request", "Web UI accepts a plain-language QA request");
  await typeAndSend(page, PREVIEW_REQUEST);
  await expect(page.getByTestId("chat-section")).toContainText("Preview ready: 2 transport checks", { timeout: 20_000 });
  await expect(page.getByTestId("chat-section")).toContainText("Transports to check: web, tui", { timeout: 20_000 });
  await stamp(page, "web-preview-plan", "Web UI explains the test plan before capture");
  await revealWebTurn(page);

  await typeAndSend(page, SCENARIO_REQUEST);
  await expect(page.getByTestId("chat-section")).toContainText(/Transport check:?\s*1 of 2/, { timeout: 20_000 });
  await expect(page.getByTestId("chat-section")).toContainText(/Driver status:?\s*captured/, { timeout: 20_000 });
  await stamp(page, "web-first-result", "Web UI shows the first transport result");
  await revealWebTurn(page);

  await page.getByTestId("intent-btn-next_leg").first().evaluate((el) => (el as HTMLElement).click());
  await expect(page.getByTestId("chat-section")).toContainText("2 / 2 transport checks passed", { timeout: 20_000 });
  await expect(page.getByTestId("chat-section")).toContainText("Review deck", { timeout: 20_000 });
  await expect(page.getByTestId("chat-section")).toContainText("clips/web-required-input.rrweb.json", { timeout: 20_000 });
  await expect(page.getByTestId("chat-section")).toContainText("clips/tui-required-input.rrweb.json", { timeout: 20_000 });
  await stamp(page, "web-summary-report", "Web UI shows final summary and clickable artifacts");
  await revealWebTurn(page);

  await page.getByTestId("intent-btn-main_room").first().evaluate((el) => (el as HTMLElement).click());
  await expect(page.getByTestId("chat-section")).toContainText(/Last run\s*scenario-qa-affordance-demo-run/, { timeout: 20_000 });
  await stamp(page, "web-main-room", "Web UI returns to the main room after the report");
  await revealWebTurn(page);

  const capture = await dumpCapture(page);
  expect(capture.events.length).toBeGreaterThan(40);
  writeEvents(capture.events, RAW_WEB_RRWEB, capture.viewport);
  polishRrwebClip(RAW_WEB_RRWEB, WEB_RRWEB);
  await page.close();
}

function startTuiBridge(addr: string): ChildProcess {
  fs.rmSync(TUI_DB_PATH, { force: true });
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
      "stories/scenario-qa/app.yaml",
      "--harness",
      "replay",
      "--recording",
      "stories/scenario-qa/recording.yaml",
      "--host-cassette",
      "stories/scenario-qa/flows/persona_qa_demo.cassette.yaml",
      "--db",
      TUI_DB_PATH,
    ],
    {
      cwd: repoRoot,
      stdio: ["ignore", "pipe", "pipe"],
      detached: true,
      env: { ...process.env, KITSOKI_META_STREAM_DELAY_MS: REPLAY_PACE <= 0 ? "0" : "120" },
    },
  );
  pipeProcessLog(proc, path.join(ARTIFACT_DIR, "tui-bridge.log"));
  return proc;
}

function ensureTuiBridgeDeps(): void {
  const bridgeRoot = path.join(repoRoot, "tools", "tui-bridge");
  const required = path.join(bridgeRoot, "node_modules", "@xterm", "xterm", "lib", "xterm.js");
  if (fs.existsSync(required)) return;
  const result = spawnSync("pnpm", ["install", "--frozen-lockfile"], {
    cwd: bridgeRoot,
    encoding: "utf8",
  });
  expect(result.status, result.stderr || result.stdout).toBe(0);
}

function startTuiPlayer(port: number): ChildProcess {
  ensureTuiBridgeDeps();
  const proc = spawn(process.execPath, ["tools/tui-bridge/player/serve.mjs"], {
    cwd: repoRoot,
    stdio: ["ignore", "pipe", "pipe"],
    detached: true,
    env: { ...process.env, TUI_BRIDGE_PLAYER_PORT: String(port) },
  });
  pipeProcessLog(proc, path.join(ARTIFACT_DIR, "tui-player.log"));
  return proc;
}

async function tuiBuffer(page: Page): Promise<string> {
  return page.evaluate(() => {
    const w = window as unknown as { __dumpBuffer?: () => string; __dump?: () => string };
    return w.__dumpBuffer?.() ?? w.__dump?.() ?? "";
  });
}

async function waitForTui(page: Page, text: string | RegExp, timeout = 60_000): Promise<void> {
  if (typeof text === "string") {
    await expect.poll(() => tuiBuffer(page), { timeout }).toContain(text);
  } else {
    await expect.poll(() => tuiBuffer(page), { timeout }).toMatch(text);
  }
}

async function typeTuiLine(page: Page, text: string): Promise<void> {
  await page.bringToFront();
  await page.evaluate(() => (window as unknown as { __scrollToBottom?: () => string }).__scrollToBottom?.());
  await page.click("#term");
  await page.keyboard.press("Tab");
  await replayDwell(page, 450);
  await page.keyboard.type(text, { delay: TYPE_DELAY_MS });
  await replayDwell(page, 1300);
  await page.keyboard.press("Enter");
}

async function captureTuiSession(context: BrowserContext): Promise<void> {
  const bridgePort = await freePort();
  const playerPort = await freePort();
  const bridgeAddr = `127.0.0.1:${bridgePort}`;
  const bridge = startTuiBridge(bridgeAddr);
  const player = startTuiPlayer(playerPort);
  const page = await context.newPage();
  try {
    await waitForHttp(`http://127.0.0.1:${playerPort}/player/`);
    await page.goto(`http://127.0.0.1:${playerPort}/player/?ws=ws://${bridgeAddr}/pty`);
    await page.waitForFunction(() => (window as unknown as { __ready?: boolean }).__ready === true);
    await expect
      .poll(() => page.evaluate(() => (window as unknown as { __status?: () => string }).__status?.()), { timeout: 60_000 })
      .toBe("connected");
    await waitForTui(page, "SCENARIO QA", 90_000);
    await installCapture(page);

    await stamp(page, "tui-request", "TUI accepts a plain-language QA request");
    await typeTuiLine(page, PREVIEW_REQUEST);
    await waitForTui(page, "Preview ready: 2 transport checks", 60_000);
    await waitForTui(page, "Transports to check: web, tui", 60_000);
    await stamp(page, "tui-preview-plan", "TUI explains the test plan before capture");
    await replayDwell(page, 1800);

    await typeTuiLine(page, SCENARIO_REQUEST);
    await waitForTui(page, /Transport check:?\s*1 of 2/, 60_000);
    await waitForTui(page, /Driver status:?\s*captured/, 60_000);
    await stamp(page, "tui-first-result", "TUI shows the first transport result");
    await replayDwell(page, 1800);

    await typeTuiLine(page, "next transport");
    await waitForTui(page, "2 / 2 transport checks passed", 60_000);
    await waitForTui(page, "Review deck", 60_000);
    await waitForTui(page, "clips/tui-required-input.rrweb.json", 60_000);
    await stamp(page, "tui-summary-report", "TUI shows final summary and report artifacts");
    await replayDwell(page, 1800);

    await typeTuiLine(page, "main room");
    await waitForTui(page, "Last run", 60_000);
    await waitForTui(page, "scenario-qa-affordance-demo-run", 60_000);
    await stamp(page, "tui-main-room", "TUI returns to the main room after the report");
    await replayDwell(page, 1400);

    const capture = await dumpCapture(page);
    expect(capture.events.length).toBeGreaterThan(40);
    writeEvents(capture.events, RAW_TUI_RRWEB, capture.viewport);
    polishRrwebClip(RAW_TUI_RRWEB, TUI_RRWEB, { repace: false, minDurationMs: 35_000, maxDurationMs: 90_000 });
  } finally {
    await page.close().catch(() => undefined);
    stopProcess(bridge);
    stopProcess(player);
  }
}

function writeLegResultsAndReport(): string {
  fs.mkdirSync(RUN_DIR, { recursive: true });
  const legResults = {
    items: [
      {
        leg_id: "required-input-affordance::web",
        scenario: "required-input-affordance",
        scenario_label: "Required input affordance",
        scenario_task: SCENARIO,
        transport: "web",
        evidence_level: "frame-level",
        driver_status: "captured",
        verdict: "pass",
        checked: [
          "A QA engineer can enter a natural-language scenario and see the previewed test plan before capture.",
          "The web report makes the per-transport verdicts, summary report, and session replay artifacts visible.",
          "The report screen offers a main-room action after completion.",
        ],
        verdict_summary: "Web UI made the required input path visible: request entry, plan preview, per-transport progress, final summary, replay artifact links, and return-to-main-room action.",
        playback_path: "clips/web-required-input.rrweb.json",
        playback_caption: "Web session replay shows scenario entry, plan preview, two transport checks, summary report, and main-room return.",
      },
      {
        leg_id: "required-input-affordance::tui",
        scenario: "required-input-affordance",
        scenario_label: "Required input affordance",
        scenario_task: SCENARIO,
        transport: "tui",
        evidence_level: "frame-level",
        driver_status: "captured",
        verdict: "pass",
        checked: [
          "A developer can enter the same natural-language scenario in the terminal and see the previewed test plan.",
          "The TUI report makes the per-transport verdicts, summary report, and session replay artifacts visible.",
          "The report screen offers a main-room action after completion.",
        ],
        verdict_summary: "TUI made the required input path visible: request entry, plan preview, per-transport progress, final summary, replay artifact links, and return-to-main-room action.",
        playback_path: "clips/tui-required-input.rrweb.json",
        playback_caption: "TUI session replay shows scenario entry, plan preview, two transport checks, summary report, and main-room return through the real TUI bridge.",
      },
    ],
  };
  const legPath = path.join(RUN_DIR, "leg-results.json");
  fs.writeFileSync(legPath, JSON.stringify(legResults, null, 2) + "\n", "utf8");
  fs.writeFileSync(path.join(RUN_DIR, "report.md"), `# Scenario QA report

- Scenario: \`${SCENARIO}\`
- Run: \`${RUN_ID}\`

| Transport | Level | Verdict | Playback | What was checked |
|---|---|---|---|---|
| web | frame-level | pass | clips/web-required-input.rrweb.json | Natural request entry, plan preview, progress, final summary, artifact links, and main-room return. |
| tui | frame-level | pass | clips/tui-required-input.rrweb.json | Same flow through the real TUI bridge: request entry, plan preview, progress, final summary, artifact links, and main-room return. |

2 / 2 transport checks passed.
`, "utf8");
  return legPath;
}

function buildDeck(legResultsPath: string): string {
  const run = spawnSync("python3", [
    "tools/product-journey/run.py",
    "--scenario-qa-report",
    "--json-output",
    "--run-dir",
    RUN_DIR,
    "--scenario-description",
    SCENARIO,
    "--leg-results-json",
    `@${legResultsPath}`,
  ], { cwd: repoRoot, encoding: "utf8" });
  expect(run.status, run.stderr || run.stdout).toBe(0);
  const payload = JSON.parse(run.stdout);
  expect(payload.summary).toContain("2 / 2 transport checks passed");
  const deckPath = path.join(RUN_DIR, "deck.slidey.json");
  const deck = JSON.parse(fs.readFileSync(deckPath, "utf8"));
  const deckText = JSON.stringify(deck);
  expect(deckText).toContain("Transport checks");
  expect(deckText).toContain("input path");
  expect(deckText).toContain("plan preview");
  expect(deckText).toContain("User session replay");
  expect(deckText).toContain("clips/web-required-input.rrweb.json");
  expect(deckText).toContain("clips/tui-required-input.rrweb.json");
  expect(deckText).toContain('"refType":"rrweb"');
  return deckPath;
}

function ensureLocalSlideyWebBuild(): void {
  const local = localSlideyIndex();
  if (!fs.existsSync(local)) return;
  const result = spawnSync("npm", ["run", "build:web"], {
    cwd: path.dirname(path.dirname(local)),
    encoding: "utf8",
  });
  expect(result.status, result.stderr || result.stdout).toBe(0);
}

async function settleSlidey(page: Page): Promise<void> {
  await page.evaluate(() => (window as unknown as { __slideySettle?: () => Promise<void> }).__slideySettle?.()).catch(() => undefined);
}

function contentMatches(content: string, text: string | RegExp): boolean {
  return typeof text === "string" ? content.includes(text) : text.test(content);
}

async function advanceUntilText(page: Page, text: string | RegExp, maxSteps = 18): Promise<void> {
  const body = page.locator("body");
  for (let i = 0; i <= maxSteps; i++) {
    const content = await body.textContent({ timeout: 2_000 }).catch(() => "");
    if (contentMatches(content, text)) return;
    await page.keyboard.press("ArrowRight");
    await settleSlidey(page);
    await dwell(page, 180);
  }
  await expect(body).toContainText(text, { timeout: 1_000 });
}

async function advanceSteps(page: Page, count: number): Promise<void> {
  for (let i = 0; i < count; i++) {
    await page.keyboard.press("ArrowRight");
    await settleSlidey(page);
    await dwell(page, 180);
  }
}

async function gotoSlide(page: Page, baseUrl: string, scene: number, step: number, expectedText: string | RegExp): Promise<void> {
  const url = `${baseUrl}?scene=${scene}&step=${step}`;
  for (let attempt = 0; attempt < 3; attempt++) {
    await page.goto(url, { waitUntil: "domcontentloaded" });
    await settleSlidey(page);
    await dwell(page, 300);
    const content = await page.locator("body").textContent({ timeout: 2_000 }).catch(() => "");
    if (content.trim() !== "Not found" && contentMatches(content, expectedText)) return;
  }
  await expect(page.locator("body")).toContainText(expectedText, { timeout: 15_000 });
}

async function openAndClickThrough(deckPath: string): Promise<void> {
  ensureLocalSlideyWebBuild();
  const port = await freePort();
  const slidey = slideyCommand();
  const proc = spawn(
    slidey.cmd,
    [...slidey.argsPrefix, deckPath, "--no-open", "--port", String(port)],
    { cwd: slidey.cwd, stdio: ["ignore", "pipe", "pipe"], detached: true },
  );
  pipeProcessLog(proc, path.join(ARTIFACT_DIR, "slidey-viewer.log"));

  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext(cameraContext());
  const page = await context.newPage();
  const shot = makeShot(OPEN_DIR);
  try {
    await waitForHttp(`http://127.0.0.1:${port}/api/config`);
    const baseUrl = `http://127.0.0.1:${port}/`;
    await page.goto(baseUrl);
    await expect(page.getByText("Scenario QA").first()).toBeVisible({ timeout: 15_000 });
    await dwell(page, 900);
    await shot(page, "01-title");

    await advanceUntilText(page, "Transport checks: 2 / 2 passed");
    await advanceSteps(page, 3);
    await expect(page.locator("body")).toContainText("Transport checks: 2 / 2 passed", { timeout: 15_000 });
    await expect(page.getByText("Web UI").first()).toBeVisible({ timeout: 15_000 });
    await expect(page.getByText("input path, plan preview").first()).toBeVisible({ timeout: 15_000 });
    await dwell(page, 900);
    await shot(page, "02-transport-checks");

    await advanceUntilText(page, "Session evidence: 2 user session replay(s) embedded");
    await advanceSteps(page, 2);
    await expect(page.getByTestId("evidence-open-rrweb").first()).toBeVisible({ timeout: 15_000 });
    await dwell(page, 900);
    await shot(page, "03-session-evidence");

    await page.getByTestId("evidence-open-rrweb").first().click();
    await expect(page.getByTestId("rrweb-popout-modal")).toBeVisible({ timeout: 15_000 });
    await expect(page.getByTestId("rrweb-popout-player")).toBeVisible({ timeout: 15_000 });
    await dwell(page, 900);
    await shot(page, "04-rrweb-popout");
    await page.getByTestId("rrweb-popout-close").click();
    await expect(page.getByTestId("rrweb-popout-modal")).toHaveCount(0, { timeout: 15_000 });

    await gotoSlide(page, baseUrl, 2, 2, "Session evidence: 2 user session replay(s) embedded");
    await expect(page.getByTestId("evidence-open-rrweb")).toHaveCount(2, { timeout: 15_000 });
    await page.getByTestId("evidence-open-rrweb").nth(1).evaluate((el) => (el as HTMLElement).click());
    await expect(page.getByTestId("rrweb-popout-modal")).toBeVisible({ timeout: 15_000 });
    await expect(page.getByTestId("rrweb-popout-player")).toBeVisible({ timeout: 15_000 });
    await dwell(page, 900);
    await shot(page, "04b-tui-rrweb-popout");
    await page.getByTestId("rrweb-popout-close").click();

    await gotoSlide(page, baseUrl, 3, 0, "User session replay");
    await expect(page.getByText("User session replay").first()).toBeVisible({ timeout: 15_000 });
    await shot(page, "05-web-replay-slide");

    await gotoSlide(page, baseUrl, 5, 2, /2 transport check\(s\); 2 pass/);
    await expect(page.locator("body")).toContainText(RUN_ID, { timeout: 15_000 });
    await expect(page.getByText(/2 transport check\(s\); 2 pass/).first()).toBeVisible({ timeout: 15_000 });
    await shot(page, "06-run-summary");
  } finally {
    await context.close();
    await browser.close();
    stopProcess(proc);
  }
}

test.beforeAll(async () => {
  fs.rmSync(ARTIFACT_DIR, { recursive: true, force: true });
  fs.mkdirSync(CLIPS_DIR, { recursive: true });
  fs.mkdirSync(RAW_CLIPS_DIR, { recursive: true });
  fs.mkdirSync(OPEN_DIR, { recursive: true });
  fs.writeFileSync(DIAG_LOG, "");
  server = await startWebServer({ addr: ADDR, flow: FLOW, storiesDir: STORY_DIR });
});

test.afterAll(() => server?.stop());

test("required-input Persona QA report embeds real web and TUI rrweb sessions", async () => {
  test.setTimeout(600_000);
  expect(fs.existsSync(RECORDING)).toBeTruthy();
  expect(fs.existsSync(HOST_CASSETTE)).toBeTruthy();

  const browser = await chromium.launch({ headless: true });
  const captureContext = await browser.newContext(cameraContext());
  try {
    diag("capture web rrweb");
    await captureWebSession(captureContext);
    diag("capture real tui rrweb");
    await captureTuiSession(captureContext);
  } finally {
    await captureContext.close();
    await browser.close();
  }

  const legResultsPath = writeLegResultsAndReport();
  const deckPath = buildDeck(legResultsPath);
  await openAndClickThrough(deckPath);

  expect(fs.existsSync(WEB_RRWEB)).toBeTruthy();
  expect(fs.existsSync(TUI_RRWEB)).toBeTruthy();
  expect(fs.existsSync(path.join(RUN_DIR, "deck.slidey.json"))).toBeTruthy();
  expect(fs.existsSync(path.join(RUN_DIR, "report.md"))).toBeTruthy();
});
