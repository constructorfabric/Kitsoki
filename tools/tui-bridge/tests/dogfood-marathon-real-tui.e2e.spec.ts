import { test, expect, type Page } from "@playwright/test";
import { spawn, spawnSync, type ChildProcess } from "node:child_process";
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
  shiftChapters,
  writeChapters,
} from "./_helpers/recording.js";

const REPO_ROOT = path.resolve(import.meta.dirname, "..", "..", "..");
const LATEST_DIR = path.join(REPO_ROOT, ".artifacts", "tui-bridge", "dogfood-marathon-real-tui");
const SCENARIO_ID = "dogfood-marathon-tui";
const SCENARIO_PROJECT = "gears-rust";
const SCENARIO_PERSONA = "core-maintainer";
const SCENARIO_SEED = "dogfood-real-tui";
const RECORDING = "tools/tui-bridge/fixtures/dogfood-marathon-recording.yaml";
const HOST_CASSETTE = "tools/tui-bridge/fixtures/dogfood-marathon.host.cassette.yaml";
const TYPE_DELAY_MS = PACE === 0 ? 0 : 45;
const COMMAND_HOLD_MS = 900;
const START_HOLD_MS = 2_500;
const CASE_HOLD_MS = 3_200;
const EXCEPTION_HOLD_MS = 4_000;
const REPORT_HOLD_MS = 5_000;
const READABLE_CHAPTER_MIN_MS = 3_000;
const START_FRAME_SETTLE_MS = 500;
const INTRO_HOLD_MS = 2_000;
const CHOICE_HOLD_MS = 1_300;
const MAX_USEFUL_PREROLL_MS = 3_500;

interface ScenarioRun {
  run_id: string;
  run_dir: string;
  driver_plan_path: string;
}

interface CaseVariant {
  id: string;
  title?: string;
  utterance?: string;
}

interface CaptureRoute {
  evidence_kind: string;
  artifact_path_template: string;
}

interface DriverScenario {
  scenario: string;
  transport?: string;
  case_variants?: CaseVariant[];
  capture_routes?: CaptureRoute[];
}

interface CapturePlan {
  run: ScenarioRun;
  runDir: string;
  artifactDir: string;
  videoDir: string;
  dbPath: string;
  videoName: string;
  cases: CaseVariant[];
  routes: Record<string, CaptureRoute>;
  renderedFramePath: string;
  pngSequencePath: string;
}

function runProductJourney(args: string[]): string {
  const result = spawnSync("python3", ["tools/product-journey/run.py", ...args], {
    cwd: REPO_ROOT,
    encoding: "utf8",
  });
  if (result.status !== 0) {
    throw new Error(
      [
        `product-journey failed: python3 tools/product-journey/run.py ${args.join(" ")}`,
        result.stdout,
        result.stderr,
      ]
        .filter(Boolean)
        .join("\n"),
    );
  }
  return (result.stdout ?? "").trim();
}

function relToRun(plan: CapturePlan, file: string): string {
  return path.relative(plan.runDir, file).split(path.sep).join("/");
}

function routeFor(scenario: DriverScenario, kind: string): CaptureRoute {
  const route = (scenario.capture_routes ?? []).find((item) => item.evidence_kind === kind);
  if (!route) {
    throw new Error(`driver-plan missing ${kind} capture route for ${SCENARIO_ID}`);
  }
  return route;
}

function createScenarioRun(): CapturePlan {
  const stdout = runProductJourney([
    "--emit-run",
    "--project",
    SCENARIO_PROJECT,
    "--persona",
    SCENARIO_PERSONA,
    "--seed",
    SCENARIO_SEED,
    "--scenarios",
    SCENARIO_ID,
    "--transport",
    "tui",
    "--live-budget-minutes",
    "0",
    "--json-output",
  ]);
  const run = JSON.parse(stdout) as ScenarioRun;
  const runDir = run.run_dir;
  const driverPlan = JSON.parse(fs.readFileSync(path.join(runDir, "driver-plan.json"), "utf8")) as {
    scenarios: DriverScenario[];
  };
  const scenario = driverPlan.scenarios.find(
    (item) => item.scenario === SCENARIO_ID && item.transport === "tui",
  );
  if (!scenario) {
    throw new Error(`driver-plan did not include ${SCENARIO_ID}::tui`);
  }
  const cases = scenario.case_variants ?? [];
  if (cases.length !== 15) {
    throw new Error(`expected 15 catalog cases for ${SCENARIO_ID}, got ${cases.length}`);
  }
  const videoRoute = routeFor(scenario, "key_interaction_video");
  const frameRoute = routeFor(scenario, "rendered_tui_frame");
  const sequenceRoute = routeFor(scenario, "png-sequence");
  const artifactDir = path.join(runDir, path.dirname(videoRoute.artifact_path_template));
  fs.mkdirSync(artifactDir, { recursive: true });
  return {
    run,
    runDir,
    artifactDir,
    videoDir: path.join(artifactDir, "video"),
    dbPath: path.join(runDir, "sessions.db"),
    videoName: path.basename(videoRoute.artifact_path_template, ".mp4"),
    cases,
    routes: {
      key_interaction_video: videoRoute,
      rendered_tui_frame: frameRoute,
      "png-sequence": sequenceRoute,
    },
    renderedFramePath: path.join(runDir, frameRoute.artifact_path_template),
    pngSequencePath: path.join(runDir, sequenceRoute.artifact_path_template),
  };
}

function attachEvidence(plan: CapturePlan, kind: string, file: string, notes: string): void {
  runProductJourney([
    "--attach-evidence",
    "--run-dir",
    plan.runDir,
    "--scenario",
    SCENARIO_ID,
    "--evidence-kind",
    kind,
    "--evidence-path",
    relToRun(plan, file),
    "--evidence-source",
    "local",
    "--notes",
    notes,
    "--json-output",
  ]);
}

function recordDriverEvent(plan: CapturePlan, evidenceRefs: string[]): void {
  runProductJourney([
    "--record-driver-event",
    "--run-dir",
    plan.runDir,
    "--scenario",
    SCENARIO_ID,
    "--dispatch-mode",
    "replay",
    "--driver-status",
    "captured",
    "--summary",
    "Recorded one continuous xterm.js TUI session for the scenario-backed dogfood marathon.",
    "--mcp-tools",
    "session.open,session.submit,render.tui,session.trace",
    "--evidence-refs",
    evidenceRefs.join(","),
    "--json-output",
  ]);
}

function writeFrameManifest(
  plan: CapturePlan,
  shotPaths: string[],
  videoPath: string | null,
  chaptersPath: string | null,
): string {
  fs.mkdirSync(path.dirname(plan.pngSequencePath), { recursive: true });
  const frames = shotPaths.map((file, index) => ({
    index,
    path: relToRun(plan, file),
    label: path.basename(file).replace(/^\d{2}-/, "").replace(/\.png$/, ""),
  }));
  const manifest = {
    schema: "kitsoki/tui-bridge/png-sequence/v1",
    run_id: plan.run.run_id,
    scenario: SCENARIO_ID,
    transport: "tui",
    video: videoPath ? relToRun(plan, videoPath) : "",
    chapters: chaptersPath ? relToRun(plan, chaptersPath) : "",
    frames,
  };
  fs.writeFileSync(plan.pngSequencePath, `${JSON.stringify(manifest, null, 2)}\n`);
  return plan.pngSequencePath;
}

function writeLatestRun(
  plan: CapturePlan,
  videoPath: string | null,
  chaptersPath: string | null,
  frameManifestPath: string,
  representativeFramePath: string,
): void {
  fs.mkdirSync(LATEST_DIR, { recursive: true });
  const latest = {
    schema: "kitsoki/tui-bridge/latest-run/v1",
    scenario: SCENARIO_ID,
    run_id: plan.run.run_id,
    run_dir: plan.runDir,
    driver_plan_path: path.join(plan.runDir, "driver-plan.json"),
    video_path: videoPath,
    chapters_path: chaptersPath,
    frame_manifest_path: frameManifestPath,
    representative_frame_path: representativeFramePath,
  };
  fs.writeFileSync(path.join(LATEST_DIR, "latest-run.json"), `${JSON.stringify(latest, null, 2)}\n`);
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

function startBridge(addr: string, plan: CapturePlan): ChildProcess {
  fs.mkdirSync(plan.artifactDir, { recursive: true });
  fs.rmSync(plan.dbPath, { force: true });
  const log = fs.createWriteStream(path.join(plan.artifactDir, "bridge.log"), { flags: "w" });
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
      ".kitsoki/stories/kitsoki-dev/app.yaml",
      "--harness",
      "replay",
      "--recording",
      RECORDING,
      "--host-cassette",
      HOST_CASSETTE,
      "--db",
      plan.dbPath,
    ],
    { cwd: REPO_ROOT, stdio: ["ignore", "pipe", "pipe"], detached: true },
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

async function visibleScreen(page: Page): Promise<string> {
  return page.evaluate(() => (window as any).__dump());
}

async function scrollLines(page: Page, lines: number): Promise<void> {
  await page.evaluate((n) => (window as any).__scrollLines(n), lines);
}

async function scrollUpUntilVisible(page: Page, text: string): Promise<void> {
  for (let i = 0; i < 8; i += 1) {
    if ((await visibleScreen(page)).includes(text)) {
      return;
    }
    await scrollLines(page, -8);
    await dwell(page, 100);
  }
  await expect.poll(() => visibleScreen(page), { timeout: 5_000 }).toContain(text);
}

async function focusTerminal(page: Page): Promise<void> {
  await page.click("#term");
}

async function focusChat(page: Page): Promise<void> {
  await focusTerminal(page);
  await page.keyboard.press("Tab");
  await dwell(page, 300);
}

async function waitForScreen(page: Page, text: string, timeout = 30_000): Promise<void> {
  await expect.poll(() => bottom(page), { timeout }).toContain(text);
}

async function waitForScreenPattern(page: Page, pattern: RegExp, timeout = 30_000): Promise<void> {
  await expect.poll(() => bottom(page), { timeout }).toMatch(pattern);
}

async function setWarpOverlay(page: Page, visible: boolean): Promise<void> {
  await page.evaluate((state) => {
    return (window as any).__setWarpOverlay(state);
  }, {
    visible,
    label: "FAST FORWARD",
    detail: "cassette replay · autonomous 15-case loop",
  });
  await expect.poll(() => page.evaluate(() => (window as any).__warpOverlayState())).toMatchObject({
    visible,
    label: "FAST FORWARD",
  });
}

async function waitForBuffer(page: Page, text: string, timeout = 30_000): Promise<void> {
  await expect.poll(() => fullBuffer(page), { timeout }).toContain(text);
}

async function typeLine(
  page: Page,
  text: string,
  shot?: (page: Page, label: string) => Promise<string>,
  shotLabel?: string,
  beforeEnter?: () => Promise<void>,
): Promise<void> {
  await page.click("#term");
  await page.keyboard.type(text, { delay: TYPE_DELAY_MS });
  await dwell(page, COMMAND_HOLD_MS);
  if (shot && shotLabel) {
    await shot(page, shotLabel);
  }
  if (beforeEnter) {
    await beforeEnter();
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

async function waitForChoiceCursor(page: Page): Promise<string> {
  await expect.poll(() => choiceCursorLine(page), { timeout: 10_000 }).not.toBe("");
  return choiceCursorLine(page);
}

async function pressChoiceKeyForCursorChange(
  page: Page,
  key: "ArrowDown" | "ArrowUp",
  before: string,
): Promise<string> {
  await page.keyboard.press(key);
  await expect.poll(() => choiceCursorLine(page), { timeout: 10_000 }).not.toBe(before);
  return choiceCursorLine(page);
}

async function exerciseChoiceWidget(
  page: Page,
  shot: (page: Page, label: string) => Promise<string>,
  labelPrefix: string,
  expected?: { initialIncludes?: string; downIncludes?: string },
): Promise<void> {
  await focusTerminal(page);
  await waitForScreen(page, "[↑/↓ move");
  const initial = await waitForChoiceCursor(page);
  if (expected?.initialIncludes) {
    expect(initial).toContain(expected.initialIncludes);
  }
  await shot(page, `${labelPrefix}-choice-initial`);
  await dwell(page, CHOICE_HOLD_MS);

  const down = await pressChoiceKeyForCursorChange(page, "ArrowDown", initial);
  expect(down).not.toEqual(initial);
  if (expected?.downIncludes) {
    expect(down).toContain(expected.downIncludes);
  }
  await shot(page, `${labelPrefix}-choice-arrow-down`);
  await dwell(page, CHOICE_HOLD_MS);

  await page.keyboard.press("ArrowUp");
  await expect.poll(() => choiceCursorLine(page), { timeout: 10_000 }).toBe(initial);
  await shot(page, `${labelPrefix}-choice-arrow-up`);
  await dwell(page, CHOICE_HOLD_MS);
}

async function parkExceptionWithChoice(
  page: Page,
  shot: (page: Page, label: string) => Promise<string>,
): Promise<void> {
  await focusTerminal(page);
  await waitForBuffer(page, "! SERIOUS EXCEPTION");
  await waitForScreen(page, "[↑/↓ move");
  const initial = await waitForChoiceCursor(page);
  expect(initial).toContain("park case and continue");
  await shot(page, "exception-choice-initial");
  await dwell(page, CHOICE_HOLD_MS);

  const answer = await pressChoiceKeyForCursorChange(page, "ArrowDown", initial);
  expect(answer).toContain("answer with guidance");
  await shot(page, "exception-choice-answer");
  await dwell(page, CHOICE_HOLD_MS);

  await page.keyboard.press("ArrowUp");
  await expect.poll(() => choiceCursorLine(page), { timeout: 10_000 }).toBe(initial);
  await shot(page, "exception-choice-park");
  await dwell(page, CHOICE_HOLD_MS);
  await page.keyboard.press("Enter");
}

test("records one continuous real Kitsoki TUI dogfood marathon session", async ({ browser }) => {
  test.setTimeout(240_000);

  const plan = createScenarioRun();
  const cases = plan.cases;
  prepareVideoDir(plan.videoDir);
  const rawShot = makeShot(plan.artifactDir);
  const shotPaths: string[] = [];
  const shot = async (page: Page, label: string): Promise<string> => {
    const file = await rawShot(page, label);
    shotPaths.push(file);
    return file;
  };
  const port = await freePort();
  const addr = `127.0.0.1:${port}`;
  const bridge = startBridge(addr, plan);

  const context = await browser.newContext(cameraContext({ recordVideoDir: plan.videoDir }));
  const page = await context.newPage();
  const chapters = new ChapterRecorder();
  let videoPath: string | null = null;
  let chaptersPath: string | null = null;
  let frameManifestPath: string | null = null;
  let representativeFramePath: string | null = null;
  let chapterPacingFailures: string[] = [];
  let videoTrimStartMs = 0;
  let firstChapterStartAfterTrimMs = 0;
  try {
    await page.goto(`/player/?ws=ws://${addr}/pty`);
    await page.waitForFunction(() => (window as any).__ready === true);
    await expect.poll(() => page.evaluate(() => (window as any).__warpOverlayState())).toMatchObject({
      visible: false,
    });
    await expect
      .poll(() => page.evaluate(() => (window as any).__status()), { timeout: 60_000 })
      .toBe("connected");
    await waitForScreen(page, "free-form workbench", 60_000);
    await dwell(page, START_FRAME_SETTLE_MS);
    videoTrimStartMs = chapters.elapsedMs();
    await shot(page, "connected-kitsoki-dev");
    await dwell(page, INTRO_HOLD_MS);

    chapters.open("kitsoki-dev-choice-widget", "Move the kitsoki-dev TUI choice widget with arrow keys", RECORDING);
    await exerciseChoiceWidget(page, shot, "kitsoki-dev");

    chapters.open("kitsoki-dev-request", "Request the dogfood marathon from kitsoki-dev", RECORDING);
    await focusChat(page);
    await typeLine(page, "I want to do a dogfood marathon", shot, "typed-dev-request");
    await waitForScreen(page, "Dogfood marathon", 60_000);
    expect(await fullBuffer(page)).toContain("Drive a backlog of cases");
    await scrollUpUntilVisible(page, "Drive a backlog of cases");
    await shot(page, "dogfood-idle-full-message");
    await dwell(page, START_HOLD_MS);
    const actionScreen = await bottom(page);
    expect(actionScreen).toContain("Plan");
    expect(actionScreen).toContain("refine plan");
    expect(actionScreen).not.toContain("resume the marathon");

    chapters.open("dogfood-choice-widget", "Move the dogfood marathon action choice widget with arrow keys", RECORDING);
    await exerciseChoiceWidget(page, shot, "dogfood", {
      initialIncludes: "start the marathon",
      downIncludes: "refine plan",
    });

    chapters.open("start", "Start autonomous 15-bug dogfood marathon", RECORDING);
    await focusChat(page);
    await typeLine(page, "start the marathon", shot, "typed-start-marathon", async () => {
      await setWarpOverlay(page, true);
    });
    if (PACE === 0) {
      await waitForBuffer(page, cases[0].id, 60_000);
    } else {
      await waitForScreen(page, `Case 1 / ${cases.length} · driving`, 60_000);
      await waitForScreenPattern(page, /Recorded:\s+0/);
    }
    await shot(page, "backlog-loaded-driving");
    await dwell(page, START_HOLD_MS);

    for (let i = 1; i <= cases.length; i += 1) {
      const { id: caseId, title, utterance } = cases[i - 1];
      const caseTitle = title ?? utterance ?? caseId;
      chapters.open(`bug-${String(i).padStart(2, "0")}`, `Process ${caseId}: ${caseTitle}`, RECORDING);
      await waitForBuffer(page, caseId, 90_000);
      await expect.poll(() => page.evaluate(() => (window as any).__warpOverlayState())).toMatchObject({
        visible: true,
        label: "FAST FORWARD",
      });
      if (i === 5) {
        await waitForScreen(page, "core.dogfood.exception_review");
        await waitForBuffer(page, "! SERIOUS EXCEPTION");
        await waitForBuffer(page, caseId);
        await waitForBuffer(page, "Should this marathon leave");
        await waitForBuffer(page, "Answer decides whether");
        await waitForBuffer(page, "Issue:");
        await waitForBuffer(page, "Kitsoku Issue #61");
        await waitForBuffer(page, "case-05 trace");
        await scrollUpUntilVisible(page, "! SERIOUS EXCEPTION");
        await dwell(page, COMMAND_HOLD_MS);
        await shot(page, "exception-review");
        await dwell(page, EXCEPTION_HOLD_MS);
        chapters.open("operator-exception", "Park the serious exception through the choice widget", RECORDING);
        await bottom(page);
        await parkExceptionWithChoice(page, shot);
      }
      if (i === cases.length) {
        await waitForBuffer(page, "15 case(s)", 60_000);
      }
      await shot(page, i === 5 ? "exception-parked" : `processed-${String(i).padStart(2, "0")}`);
      await dwell(page, CASE_HOLD_MS);
    }

    chapters.open("report", "Aggregate and render slidey decks", RECORDING);
    await setWarpOverlay(page, false);
    await waitForBuffer(page, "15 case(s)", 60_000);
    await waitForBuffer(page, "15 per-bug deck(s)", 60_000);
    const finalScreen = await fullBuffer(page);
    expect(finalScreen).toContain("15");
    await scrollLines(page, -14);
    await dwell(page, COMMAND_HOLD_MS);
    await shot(page, "done-summary");
    await dwell(page, START_HOLD_MS);
    await bottom(page);
    await shot(page, "done-report");
    await dwell(page, REPORT_HOLD_MS);
  } finally {
    const chapterList = shiftChapters(chapters.list(), videoTrimStartMs);
    firstChapterStartAfterTrimMs = chapterList[0]?.start_ms ?? 0;
    const video = page.video();
    await context.close();
    videoPath = await saveVideoAsMp4(video, plan.artifactDir, plan.videoName, {
      trimStartMs: videoTrimStartMs,
    });
    chaptersPath = writeChapters(videoPath, chapterList);
    if (shotPaths[0]) {
      fs.mkdirSync(path.dirname(plan.renderedFramePath), { recursive: true });
      fs.copyFileSync(shotPaths[0], plan.renderedFramePath);
      representativeFramePath = plan.renderedFramePath;
    }
    frameManifestPath = writeFrameManifest(plan, shotPaths, videoPath, chaptersPath);
    if (PACE !== 0) {
      chapterPacingFailures = chapterList
        .filter((chapter) => chapter.end_ms - chapter.start_ms < READABLE_CHAPTER_MIN_MS)
        .map((chapter) => `${chapter.id}=${chapter.end_ms - chapter.start_ms}ms`);
    }
    stopBridge(bridge);
  }
  expect(firstChapterStartAfterTrimMs).toBeLessThanOrEqual(MAX_USEFUL_PREROLL_MS);
  expect(chapterPacingFailures).toEqual([]);
  if (!videoPath || !frameManifestPath || !representativeFramePath) {
    throw new Error("recording did not produce all required scenario evidence artifacts");
  }
  attachEvidence(
    plan,
    "key_interaction_video",
    videoPath,
    "Scenario-backed xterm.js MP4 showing one continuous dogfood marathon TUI session.",
  );
  attachEvidence(
    plan,
    "png-sequence",
    frameManifestPath,
    "Frame manifest for the scenario-backed dogfood marathon TUI recording.",
  );
  attachEvidence(
    plan,
    "rendered_tui_frame",
    representativeFramePath,
    "Representative TUI frame from the start of the scenario-backed dogfood marathon recording.",
  );
  const evidenceRefs = [
    relToRun(plan, videoPath),
    relToRun(plan, frameManifestPath),
    relToRun(plan, representativeFramePath),
  ];
  recordDriverEvent(plan, evidenceRefs);
  writeLatestRun(plan, videoPath, chaptersPath, frameManifestPath, representativeFramePath);
});
