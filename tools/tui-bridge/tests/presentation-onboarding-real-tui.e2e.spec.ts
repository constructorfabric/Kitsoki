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
const BASE_HOST_CASSETTE_PATH = path.join(ARTIFACT_DIR, "presentation-onboarding.host-cassette.json");
const EXTENDED_HOST_CASSETTE_PATH = path.join(ARTIFACT_DIR, "presentation-onboarding.extended-host-cassette.json");
const WARP_PATH = path.join(ARTIFACT_DIR, "presentation-onboarding.warp.yaml");
const CHAPTER_SOURCE = "tools/tui-bridge/tests/presentation-onboarding-real-tui.e2e.spec.ts";
const TYPE_DELAY_MS = PACE === 0 ? 0 : 60;
const COMMAND_HOLD_MS = 1_800;
const STEP_HOLD_MS = 3_000;
const SCREEN_HOLD_MS = 2_200;
const MENU_MOVE_HOLD_MS = 220;
const NARRATION_HOLD_MS = 4_800;
const NARRATION_EXIT_HOLD_MS = 500;
const FINAL_HOLD_MS = 6_000;

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

function seedLocalTickets(target: string): void {
  const dir = path.join(target, "issues", "bugs");
  fs.mkdirSync(dir, { recursive: true });
  const tickets = [
    {
      id: "2026-07-09T101500Z-presentation-queue-filter-stale",
      severity: "P1",
      title: "Presentation queue keeps stale JQL results after filter refresh",
      body:
        "When the operator changes the presentation-service bug JQL, the ticket queue keeps rendering the previous result set until the TUI is restarted.\n\nExpected: changing the JQL refreshes the visible queue immediately.\n",
    },
    {
      id: "2026-07-09T093000Z-presentation-slide-preview-overflow",
      severity: "P2",
      title: "Slide preview overflows narrow terminal columns",
      body: "The presentation preview panel overflows when the terminal is narrowed below 120 columns.\n",
    },
    {
      id: "2026-07-08T164000Z-presentation-export-status-copy",
      severity: "P3",
      title: "Export status copy still says pending after successful render",
      body: "A completed export leaves the status line on the old pending message, which confuses reviewers.\n",
    },
  ];
  for (const ticket of tickets) {
    fs.writeFileSync(
      path.join(dir, `${ticket.id}.md`),
      `---\ntitle: ${JSON.stringify(ticket.title)}\nstatus: open\nseverity: ${ticket.severity}\nassignee: demo.developer\n---\n# ${ticket.title}\n\n${ticket.body}`,
    );
  }
}

function writeWarp(): string {
  fs.mkdirSync(ARTIFACT_DIR, { recursive: true });
  fs.writeFileSync(
    WARP_PATH,
    [
      'name: "Presentation onboarding demo controls"',
      'description: "Keep the captured bugfix handoff deterministic."',
      "state: landing",
      "world:",
      "  auto_triage: false",
      "  judge_mode: human",
      "  base_branch: master",
      "",
    ].join("\n"),
  );
  return WARP_PATH;
}

function writeExtendedHostCassette(): string | null {
  if (!fs.existsSync(BASE_HOST_CASSETTE_PATH)) return null;
  const cassette = JSON.parse(fs.readFileSync(BASE_HOST_CASSETTE_PATH, "utf8")) as {
    episodes?: unknown[];
  };
  const reproductionArtifact = {
    summary_title: "Stale JQL queue reproduced in presentation service",
    summary_markdown:
      "The selected bug is now in the bugfix pipeline. The reproducer verified that changing the configured JQL can leave the visible queue on the previous result set; the next phase will propose the smallest refresh-state fix.",
    bug_verified: true,
    steps: ["open the tickets view", "change the JQL filter", "observe that the previous rows remain visible"],
    involved_components: [
      { name: "ticket queue refresh", reason: "provider-backed result cache needs invalidation after JQL changes" },
    ],
    confidence: 0.9,
    reasoning: "The seeded local ticket carries the same workflow shape as the Jira-backed JQL queue, but avoids live Jira during capture.",
  };
  cassette.episodes ??= [];
  cassette.episodes.push(
    {
      id: "presentation-bugfix-workspace",
      match: { handler: "host.git_worktree" },
      response: {
        data: {
          ok: true,
          path: ".capsules/workspaces/bf-presentation-queue-filter-stale-demo",
          log: "prepared deterministic demo workspace",
        },
      },
      replay: "any",
    },
    {
      id: "presentation-bugfix-reproducer",
      match: { handler: "host.agent.task" },
      response: {
        data: {
          ok: true,
          submitted: reproductionArtifact,
        },
      },
      replay: "any",
      agent: {
        verb: "task",
        agent: "reproducer",
        model: "cassette/no-live-llm",
        duration_ms: 1200,
        prompt_tokens: 0,
        response_tokens: 0,
        cost_usd: 0,
        response: JSON.stringify(reproductionArtifact, null, 2),
        transcript: {
          format: "cassette.synthetic.v1",
          events: [
            JSON.stringify({
              type: "message",
              role: "assistant",
              content: "Replayed no-LLM reproduction artifact for presentation onboarding demo.",
            }),
          ],
        },
      },
    },
  );
  fs.writeFileSync(EXTENDED_HOST_CASSETTE_PATH, JSON.stringify(cassette, null, 2) + "\n");
  return EXTENDED_HOST_CASSETTE_PATH;
}

function startBridge(addr: string, workdir: string): ChildProcess {
  fs.mkdirSync(ARTIFACT_DIR, { recursive: true });
  fs.rmSync(DB_PATH, { force: true });
  fs.rmSync(TRACE_PATH, { force: true });
  const log = fs.createWriteStream(path.join(ARTIFACT_DIR, "presentation-onboarding.bridge.log"), { flags: "w" });
  const appPath = path.join(REPO_ROOT, "stories", "dev-story", "app.yaml");
  const hostCassette = writeExtendedHostCassette();
  const warpPath = writeWarp();
  const runArgs = [
    "run",
    appPath,
    "--db",
    DB_PATH,
    "--warp",
    warpPath,
    ...(hostCassette ? ["--host-cassette", hostCassette] : []),
  ];
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
      ...runArgs,
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
  if (PACE === 0) {
    return page.evaluate(() => (window as any).__scrollToBottom());
  }
  return page.evaluate(() => (window as any).__scrollToBottomSmooth?.(18) ?? (window as any).__scrollToBottom());
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

async function waitForScreen(page: Page, text: string, timeout = 45_000): Promise<void> {
  await expect.poll(() => page.evaluate(() => (window as any).__dump()), { timeout }).toContain(text);
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
    await page.keyboard.press(process.platform === "darwin" ? "Meta+A" : "Control+A");
    await page.keyboard.press("Backspace");
    await dwell(page, 100);
  }
  if (TYPE_DELAY_MS === 0) {
    await page.keyboard.insertText(text);
  } else {
    await page.keyboard.type(text, { delay: TYPE_DELAY_MS });
  }
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
    .filter((line) => line.includes("▸"))
    .at(-1) ?? "";
}

async function ensureChoiceCursor(page: Page): Promise<void> {
  await expect.poll(() => choiceCursorLine(page), { timeout: 10_000 }).not.toBe("");
}

async function chooseDefaultAction(
  page: Page,
  shot: (page: Page, label: string) => Promise<string>,
  label: string,
  expectedCursorText: string,
): Promise<void> {
  await bottom(page);
  await focusTerminal(page);
  await ensureChoiceCursor(page);
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
  await ensureChoiceCursor(page);
  for (let i = 0; i < 64; i += 1) {
    const line = await choiceCursorLine(page);
    if (line.includes(expectedCursorText)) {
      await dwell(page, STEP_HOLD_MS);
      await shot(page, shotLabel);
      await page.keyboard.press("Enter");
      return;
    }
    await page.keyboard.press("ArrowDown");
    await expect.poll(() => choiceCursorLine(page), { timeout: 2_000 }).not.toBe(line);
    await dwell(page, MENU_MOVE_HOLD_MS);
  }
  throw new Error(`choice cursor did not reach ${expectedCursorText}`);
}

async function chooseActionByOffset(
  page: Page,
  shot: (page: Page, label: string) => Promise<string>,
  shotLabel: string,
  steps: number,
  expectedCursorText: string,
): Promise<void> {
  await bottom(page);
  await focusTerminal(page);
  await ensureChoiceCursor(page);
  for (let i = 0; i < steps; i += 1) {
    await page.keyboard.press("ArrowDown");
    await dwell(page, MENU_MOVE_HOLD_MS);
  }
  await expect.poll(() => choiceCursorLine(page), { timeout: 5_000 }).toContain(expectedCursorText);
  await dwell(page, STEP_HOLD_MS);
  await shot(page, shotLabel);
  await page.keyboard.press("Enter");
}

async function fillSelectedParam(
  page: Page,
  value: string,
  shot: (page: Page, label: string) => Promise<string>,
  shotLabel: string,
): Promise<void> {
  await bottom(page);
  await focusTerminal(page);
  await ensureChoiceCursor(page);
  await page.keyboard.press("Enter");
  await dwell(page, 300);
  if (TYPE_DELAY_MS === 0) {
    await page.keyboard.insertText(value);
  } else {
    await page.keyboard.type(value, { delay: TYPE_DELAY_MS });
  }
  await dwell(page, COMMAND_HOLD_MS);
  await shot(page, shotLabel);
  await page.keyboard.press("Enter");
}

async function installNarration(page: Page): Promise<(title: string, sub: string, holdMs?: number) => Promise<void>> {
  await page.addStyleTag({
    content:
      "#demo-narration{position:fixed;left:28px;right:28px;top:22px;z-index:9999;pointer-events:none;" +
      "font:600 18px/1.35 ui-sans-serif,system-ui,-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif;" +
      "color:#e5e7eb;background:rgba(2,6,23,.92);border:1px solid rgba(148,163,184,.55);" +
      "box-shadow:0 18px 40px rgba(0,0,0,.3);border-radius:8px;padding:14px 18px;opacity:0;" +
      "transform:translateY(-8px);transition:opacity .18s ease,transform .18s ease}" +
      "#demo-narration.show{opacity:1;transform:translateY(0)}" +
      "#demo-narration .sub{display:block;margin-top:6px;color:#cbd5e1;font-weight:500;font-size:15px}" +
      "#demo-narration code{font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;color:#93c5fd}",
  });
  await page.evaluate(() => {
    const el = document.createElement("div");
    el.id = "demo-narration";
    document.body.appendChild(el);
  });
  return async (title: string, sub: string, holdMs = NARRATION_HOLD_MS) => {
    await page.evaluate(
      ({ title: t, sub: s }) => {
        const el = document.getElementById("demo-narration");
        if (!el) return;
        el.innerHTML = `${t}<span class="sub">${s}</span>`;
        el.classList.add("show");
      },
      { title, sub },
    );
    await dwell(page, holdMs);
    await page.evaluate(() => document.getElementById("demo-narration")?.classList.remove("show"));
    await dwell(page, NARRATION_EXIT_HOLD_MS);
  };
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
  test.setTimeout(480_000);
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
  seedLocalTickets(demo.target);
  const rawShot = makeShot(ARTIFACT_DIR);
  const shot = async (page: Page, label: string): Promise<string> => {
    const file = await rawShot(page, label);
    await dwell(page, SCREEN_HOLD_MS);
    return file;
  };
  const port = await freePort();
  const addr = `127.0.0.1:${port}`;
  const bridge = startBridge(addr, demo.target);
  const context = await browser.newContext(cameraContext({ recordVideoDir: VIDEO_DIR }));
  const page = await context.newPage();
  const chapters = new ChapterRecorder();
  const screenshots: string[] = [];
  let mediaPath: string | null = null;

  try {
    await page.goto(`/player/?ws=ws://${addr}/pty&scrollback=0&fontSize=15&lineHeight=1.18`);
    await maybeInstallAutoRrwebCapture(page);
    await page.waitForFunction(() => (window as any).__ready === true);
    await expect
      .poll(() => page.evaluate(() => (window as any).__status()), { timeout: 60_000 })
      .toBe("connected");
    const narrate = await installNarration(page);

    chapters.open("launch", "Launch Kitsoki from the terminal", CHAPTER_SOURCE);
    await narrate(
      "Terminal launch",
      "<code>cd src/cyberstack/platform-presentation && kitsoki run .kitsoki/stories/platform-presentation-dev/app.yaml</code>",
    );
    await narrate(
      "First run",
      "If this repo already has <code>.kitsoki/</code>, setup can reuse it; otherwise provider setup and onboarding create the local profile and project story.",
    );

    chapters.open("landing", "Kitsoki opens in the real xterm.js TUI", CHAPTER_SOURCE);
    await dwell(page, STEP_HOLD_MS);
    fs.writeFileSync(INITIAL_DUMP_PATH, await fullBuffer(page));
    screenshots.push(await shot(page, "diagnostic-initial-screen"));
    await waitForBuffer(page, "onboard");
    await waitForBuffer(page, "discover a project profile");
    await dwell(page, STEP_HOLD_MS);
    screenshots.push(await shot(page, "landing-workbench"));

    chapters.open("provider-setup", "Review local provider setup before project onboarding", CHAPTER_SOURCE);
    await narrate(
      "Provider setup",
      "The developer first checks which harness providers are installed and chooses the local profile used for model calls.",
    );
    await typeLine(page, "setup local harness profile", shot, "choose-provider-quick-action");
    await waitForBuffer(page, "LOCAL HARNESS PROFILE", 90_000);
    await waitForBuffer(page, "discovering local setup", 90_000);
    screenshots.push(await shot(page, "provider-discovery"));
    await chooseDefaultAction(page, shot, "choose-continue-to-provider-review", "continue");
    await waitForBuffer(page, "Patch Preview", 90_000);
    await waitForBuffer(page, "profile_setup_review", 90_000);
    await narrate(
      "Local override",
      "Only <code>.kitsoki.local.yaml</code> is written here, so checked-in project defaults stay shared while personal provider choices stay local.",
    );
    screenshots.push(await shot(page, "provider-review"));
    await waitForBuffer(page, "Patch Preview", 30_000);
    await waitForBuffer(page, "default_profile:", 30_000);
    screenshots.push(await shot(page, "provider-patch-preview"));
    await chooseDefaultAction(page, shot, "choose-apply-provider-profile", "apply");
    await waitForBuffer(page, "applying local override", 90_000);
    await waitForBuffer(page, ".kitsoki.local.yaml", 90_000);
    screenshots.push(await shot(page, "provider-apply"));
    await chooseDefaultAction(page, shot, "choose-continue-after-provider-apply", "continue");
    await waitForBuffer(page, "LOCAL HARNESS PROFILE", 90_000);
    await waitForBuffer(page, "configured", 90_000);
    screenshots.push(await shot(page, "provider-configured"));
    await chooseDefaultAction(page, shot, "choose-workbench-after-provider", "workbench");
    await waitForBuffer(page, "discover a project profile", 90_000);
    screenshots.push(await shot(page, "workbench-after-provider"));

    chapters.open("request", "Select the onboarding quick action for platform-presentation", CHAPTER_SOURCE);
    await narrate(
      "Project onboarding",
      "Now the developer onboards the repository itself: commands, story pack, ticket intake, generated instance, and local setup files.",
    );
    await typeLine(page, "onboard this project", shot, "choose-onboard-quick-action");

    chapters.open("discovery", "Discovery infers the presentation service profile", CHAPTER_SOURCE);
    await waitForBuffer(page, "Platform Presentation", 90_000);
    await waitForBuffer(page, "go project", 90_000);
    screenshots.push(await shot(page, "discovered-platform-profile"));
    await chooseDefaultAction(page, shot, "choose-continue-to-review", "continue");

    chapters.open("review", "Review the profile before writes and cover normal pre-apply choices", CHAPTER_SOURCE);
    await waitForBuffer(page, "Writes:");
    await waitForBuffer(page, "Starter Stories");
    await waitForBuffer(page, "Mode:");
    await scrollToText(page, "Writes:", 1);
    await dwell(page, STEP_HOLD_MS);
    screenshots.push(await shot(page, "review-before-writes-top"));
    await bottom(page);

    chapters.open("story-pack", "Choose the planning-and-delivery starter pack", CHAPTER_SOURCE);
    await chooseActionByLabel(page, shot, "choose-story-packs", "story packs");
    await waitForBuffer(page, "choose story pack", 30_000);
    screenshots.push(await shot(page, "story-pack-menu"));
    await chooseActionByLabel(page, shot, "choose-planning-delivery-pack", "planning delivery");
    await waitForBuffer(page, "Planning and delivery", 30_000);
    screenshots.push(await shot(page, "review-after-story-pack"));

    chapters.open("ticket-provider", "Confirm local ticket intake and explain JQL handoff", CHAPTER_SOURCE);
    await chooseActionByLabel(page, shot, "choose-ticket-provider", "ticket provider");
    await waitForBuffer(page, "choose ticket provider", 30_000);
    screenshots.push(await shot(page, "ticket-provider-menu"));
    await narrate(
      "Ticket provider",
      "For this Acronis remote the demo uses local markdown and pasted reports. In a Jira-backed project, this is where the team records the JQL that defines the queue.",
    );
    await chooseDefaultAction(page, shot, "choose-local-ticket-provider", "local reports");
    await waitForBuffer(page, "local markdown / pasted reports", 30_000);
    screenshots.push(await shot(page, "review-after-ticket-provider"));

    chapters.open("llm-draft", "Draft, validate, and select a richer no-LLM cassette-backed profile", CHAPTER_SOURCE);
    await chooseActionByLabel(page, shot, "choose-llm-draft", "LLM draft");
    await waitForBuffer(page, "LLM profile draft", 90_000);
    await waitForBuffer(page, "platform-presentation-dev/app.yaml", 90_000);
    screenshots.push(await shot(page, "llm-profile-draft"));
    await chooseDefaultAction(page, shot, "choose-validate-llm-draft", "validate");
    await waitForBuffer(page, "schema + semantic validation", 90_000);
    await waitForBuffer(page, "OK:", 90_000);
    screenshots.push(await shot(page, "llm-profile-validation"));
    await chooseDefaultAction(page, shot, "choose-use-validated-draft", "use draft");
    await waitForBuffer(page, "llm-validated", 30_000);
    screenshots.push(await shot(page, "review-after-llm-profile"));
    await chooseDefaultAction(page, shot, "choose-accept-validated-profile", "accept");

    chapters.open("apply", "Apply onboarding files and install local tools", CHAPTER_SOURCE);
    await waitForBuffer(page, "applying local setup", 90_000);
    await waitForBuffer(page, "studio MCP", 90_000);
    await waitForBuffer(page, "Profile validation", 90_000);
    screenshots.push(await shot(page, "applied-local-setup"));
    await chooseDefaultAction(page, shot, "choose-continue-to-result", "continue");

    chapters.open("done", "Applied result shows a valid project profile", CHAPTER_SOURCE);
    await waitForBuffer(page, "applied");
    await waitForBuffer(page, "Readiness");
    await waitForBuffer(page, "workbench");
    await waitForBuffer(page, "platform-presentation-dev/app.yaml");
    await scrollToText(page, "PROJECT ONBOARDING  ·  applied", 1);
    await dwell(page, FINAL_HOLD_MS);
    screenshots.push(await shot(page, "final-applied-result-top"));
    await bottom(page);
    await dwell(page, STEP_HOLD_MS);
    screenshots.push(await shot(page, "final-applied-result-bottom-before-postapply"));

    chapters.open("customizations", "Review mined customization affordances after apply", CHAPTER_SOURCE);
    await chooseActionByLabel(page, shot, "choose-review-customizations", "customizations");
    await waitForBuffer(page, "init_customizations", 90_000);
    await waitForBuffer(page, "pending customizations", 90_000);
    screenshots.push(await shot(page, "customization-review"));
    await chooseActionByLabel(page, shot, "choose-continue-after-customizations", "continue");
    await waitForBuffer(page, "applied", 90_000);
    screenshots.push(await shot(page, "done-after-customizations"));

    chapters.open("tickets", "Open the configured ticket queue and select a bug", CHAPTER_SOURCE);
    await chooseActionByOffset(page, shot, "choose-workbench-after-onboarding", 2, "workbench");
    await waitForScreen(page, "landing ·", 90_000);
    await narrate(
      "Ticket queue",
      "With onboarding complete, the developer opens Tickets. A Jira-backed project would resolve the configured JQL here; this deterministic recording uses equivalent local bug files.",
    );
    await chooseActionByLabel(page, shot, "choose-tickets", "tickets");
    await waitForBuffer(page, "Tickets", 90_000);
    await waitForBuffer(page, "stale JQL results", 90_000);
    screenshots.push(await shot(page, "tickets-list"));
    await fillSelectedParam(page, "1", shot, "pick-first-bug");
    await waitForBuffer(page, "Ready", 90_000);
    await waitForBuffer(page, "Presentation queue keeps stale JQL results", 90_000);
    screenshots.push(await shot(page, "ticket-picked"));

    chapters.open("bugfix", "Drive the selected bug into the bugfix pipeline", CHAPTER_SOURCE);
    await chooseActionByLabel(page, shot, "choose-drive-selected-bug", "drive");
    await waitForBuffer(page, "REPRODUCING", 90_000);
    await waitForBuffer(page, "Stale JQL queue reproduced", 90_000);
    await narrate(
      "Working the bug",
      "The selected bug is now in the bugfix story. The first phase produces a reproduction artifact before the developer accepts it into proposal and implementation.",
    );
    screenshots.push(await shot(page, "bugfix-reproducing"));
    await dwell(page, FINAL_HOLD_MS);
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
          host_cassette_path: fs.existsSync(EXTENDED_HOST_CASSETTE_PATH)
            ? EXTENDED_HOST_CASSETTE_PATH
            : fs.existsSync(BASE_HOST_CASSETTE_PATH)
              ? BASE_HOST_CASSETTE_PATH
              : null,
          warp_path: fs.existsSync(WARP_PATH) ? WARP_PATH : null,
          trace_path: fs.existsSync(TRACE_PATH) ? TRACE_PATH : null,
          db_path: DB_PATH,
          screenshots,
          rrweb: rrwebStats,
          session,
          visual_assertions: [
            "landing contains Project Onboarding",
            "provider setup discovers local harness state and writes only .kitsoki.local.yaml",
            "discovery contains Platform Presentation and go project",
            "review contains visible project writes, starter stories, and deterministic mode",
            "story pack menu includes planning delivery and returns to review",
            "ticket provider menu explains local intake and the Jira/JQL handoff",
            "LLM draft path shows draft, validation, and llm-validated review state",
            "apply contains studio MCP and profile validation",
            "done contains applied result and platform-presentation-dev/app.yaml",
            "customizations path returns to the applied result",
            "tickets path shows the configured queue, picks a bug, and enters the reproducing phase",
          ],
          limitations: [
            "The LLM profile draft and bugfix reproduction artifact are replayed from a host cassette to avoid live-model spend and keep the demo deterministic.",
            "The ticket queue is seeded as local markdown to avoid live Jira during capture; the narration identifies where a real Jira-backed project would configure and use JQL.",
          ],
        },
        null,
        2,
      ) + "\n",
    );
  }
});
