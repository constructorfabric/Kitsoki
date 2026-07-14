/**
 * dev-story-mining improvement loop tour video.
 *
 * Drives the dev-story-mining story through its deterministic no-LLM flow:
 * prepare sources -> mine -> mapping -> map -> decide -> author -> record. The flow uses
 * real story rooms/views and seeded/cassetted host outputs; the spec records
 * the actual web UI at watch speed with Kitsoki's tour overlay.
 *
 * Fast gate:
 *   WEB_CHAT_PACE=0 pnpm exec playwright test dev-story-mining-video --project=chromium
 * Record:
 *   pnpm exec playwright test dev-story-mining-video --project=chromium
 */
import { test, expect, chromium, type Browser, type BrowserContext, type Page } from "@playwright/test";
import path from "path";
import fs from "fs";
import {
  startWebServer,
  repoRoot,
  makeShot,
  prepareVideoDir,
  saveVideoAsMp4,
  ChapterRecorder,
  writeChapters,
  demoAddr,
  dwell,
  cinematicGoto,
  type WebServer,
} from "./_helpers/server.js";
import { cameraContext } from "./_helpers/camera.js";
import { type TourStep } from "../../src/tour/types.js";

const CHAPTER_SOURCE = "tools/runstatus/tests/playwright/dev-story-mining-video.spec.ts";
const ADDR = demoAddr(7789);
const STORY_DIR = path.join(repoRoot, "stories", "dev-story-mining");
const FLOW = path.join(STORY_DIR, "flows", "happy_human.yaml");
const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "dev-story-mining-demo");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "video");
const ERROR_TXT = path.join(ARTIFACT_DIR, "ERROR.txt");

const TOUR_STEPS: readonly TourStep[] = [
  {
    id: "dsm-home",
    route: "home",
    title: "Mine transcripts into Kitsoki improvements",
    body: "This story turns Claude Code and Codex session evidence into rooms, Starlark scripts, flow fixtures, hub routes, or honest enforcement-limit records.",
    placement: "center",
    kind: "explain",
    advance: "next",
    waitForTarget: "home-view",
    dwellMs: 4600,
  },
  {
    id: "dsm-card",
    route: "home",
    target: "story-card",
    title: "A first-class improvement story",
    body: "The session is a real Kitsoki story run over the dev-story-mining app. The flow fixture keeps the capture deterministic and free of live LLM calls.",
    placement: "right",
    kind: "explain",
    advance: "next",
    waitForTarget: "story-card",
    dwellMs: 4400,
  },
  {
    id: "dsm-idle",
    route: "interactive",
    target: "intent-btn-start",
    title: "Start by preparing sources",
    body: "The first operator action records the source matrix: Claude transcripts, Codex rollouts, target artifact classes, and the L0-L4 determinism ladder.",
    placement: "top",
    kind: "explain",
    advance: "next",
    waitForTarget: "intent-btn-start",
    dwellMs: 5200,
  },
  {
    id: "dsm-prepare",
    route: "interactive",
    title: "Prepare makes the policy explicit",
    body: "The prepared plan states the important limit: Claude can use the pre-model hook; Codex cannot be pre-model intercepted today, so the story records guidance and routing mitigations honestly.",
    placement: "right",
    kind: "explain",
    advance: "next",
    waitForTarget: "current-state",
    dwellMs: 6600,
  },
  {
    id: "dsm-mine",
    route: "interactive",
    title: "Mine both transcript families",
    body: "The mined artifact now shows conversations per harness, total messages, known tokens, total intents, and a plain-language Green / Yellow / Red determinism legend.",
    placement: "right",
    kind: "explain",
    advance: "next",
    waitForTarget: "current-state",
    dwellMs: 5600,
  },
  {
    id: "dsm-brief-modal",
    route: "interactive",
    target: "markdown-modal",
    title: "The brief opens as markdown",
    body: "This is not prose pretending a file exists. The demo clicks the brief path and the web UI reads the real markdown through runstatus.file.read.",
    placement: "right",
    kind: "explain",
    advance: "next",
    waitForTarget: "markdown-modal",
    dwellMs: 5600,
  },
  {
    id: "dsm-map-transition",
    route: "interactive",
    title: "Mapping is announced before MAP",
    body: "The story now pauses in a real MAPPING checkpoint. The mapper writes the opportunity-map artifact here, before the operator enters MAP review.",
    placement: "right",
    kind: "explain",
    advance: "next",
    waitForTarget: "current-state",
    dwellMs: 4600,
  },
  {
    id: "dsm-map",
    route: "interactive",
    title: "Map opportunities to concrete artifacts",
    body: "The map now links its markdown artifact and summarizes actionable, limited, and already-modeled opportunities before the operator decides.",
    placement: "right",
    kind: "explain",
    advance: "next",
    waitForTarget: "current-state",
    dwellMs: 5600,
  },
  {
    id: "dsm-map-modal",
    route: "interactive",
    target: "markdown-modal",
    title: "The opportunity map is reviewable",
    body: "The opportunity map is a real markdown artifact with statuses, target paths, validators, and enforcement-limit notes.",
    placement: "right",
    kind: "explain",
    advance: "next",
    waitForTarget: "markdown-modal",
    dwellMs: 5600,
  },
  {
    id: "dsm-decide",
    route: "interactive",
    title: "Pick the highest-leverage improvement",
    body: "The decision gate ranks by intent count, mechanicalness, and Kitsoki-adoption leverage. Here it selects the Starlark source planner.",
    placement: "right",
    kind: "explain",
    advance: "next",
    waitForTarget: "current-state",
    dwellMs: 5400,
  },
  {
    id: "dsm-author",
    route: "interactive",
    title: "Apply the improvement with no-LLM coverage",
    body: "The author artifact records the files changed, the focused flow gate, and a unified diff path. Accept is refused unless validation is green.",
    placement: "right",
    kind: "explain",
    advance: "next",
    waitForTarget: "current-state",
    dwellMs: 5600,
  },
  {
    id: "dsm-diff-modal",
    route: "interactive",
    target: "diff-artifact-modal",
    title: "The diff viewer shows what changed",
    body: "A .diff KV value opens the existing UnifiedDiff renderer, so authored changes are inspectable in the story instead of hidden in a claim.",
    placement: "left",
    kind: "explain",
    advance: "next",
    waitForTarget: "diff-artifact-modal",
    dwellMs: 5600,
  },
  {
    id: "dsm-record",
    route: "interactive",
    title: "Record the determinism ladder move",
    body: "The final room links the close-out report, points back to the diff, and records that source-policy planning moved from L1 prose into an L2 deterministic story/script skeleton.",
    placement: "right",
    kind: "explain",
    advance: "next",
    waitForTarget: "current-state",
    dwellMs: 5600,
  },
  {
    id: "dsm-final-modal",
    route: "interactive",
    target: "markdown-modal",
    title: "The final report closes the loop",
    body: "The close-out report states what was found, what was applied, what was validated, and which determinism move was recorded.",
    placement: "right",
    kind: "explain",
    advance: "next",
    waitForTarget: "markdown-modal",
    dwellMs: 5600,
  },
] as const;

const STEP_BY_ID = Object.fromEntries(TOUR_STEPS.map((s) => [s.id, s]));

let server: WebServer;

function diag(msg: string): void {
  const line = `[${new Date().toISOString()}] ${msg}\n`;
  try {
    fs.appendFileSync(ERROR_TXT, line);
  } catch {
    /* best-effort */
  }
}

test.beforeAll(async () => {
  prepareVideoDir(VIDEO_DIR);
  fs.mkdirSync(ARTIFACT_DIR, { recursive: true });
  fs.writeFileSync(ERROR_TXT, "");
  server = await startWebServer({ addr: ADDR, flow: FLOW, storiesDir: STORY_DIR });
});

test.afterAll(() => server?.stop());

async function narrate(
  page: Page,
  chapters: ChapterRecorder,
  shot: (p: Page, name: string) => Promise<void>,
  stepId: string,
): Promise<void> {
  const step = STEP_BY_ID[stepId];
  if (!step) throw new Error(`unknown tour step: ${stepId}`);
  diag(`narrate ${stepId}`);
  if (step.waitForTarget) {
    await expect(page.getByTestId(step.waitForTarget).first()).toBeVisible({ timeout: 20000 });
  }
  const overlayVisible = await page.getByTestId("tour-overlay").isVisible().catch(() => false);
  if (!overlayVisible) {
    await page.evaluate((stepsJson: string) => {
      (window as unknown as { __startTourWithSteps?: (s: string) => void })
        .__startTourWithSteps?.(stepsJson);
    }, JSON.stringify(TOUR_STEPS));
  }
  await page.evaluate((id: string) => {
    (window as unknown as { __tourGoTo?: (s: string) => void }).__tourGoTo?.(id);
  }, stepId);
  await expect(page.getByTestId("tour-overlay")).toBeVisible({ timeout: 8000 });
  await expect(page.getByTestId("tour-title")).toHaveText(step.title, { timeout: 12000 });
  chapters.open(step.id, step.title, CHAPTER_SOURCE);
  await dwell(page, step.dwellMs ?? 3500);
  await shot(page, step.id);
}

async function clickIntent(page: Page, intent: string, expectedState: string): Promise<void> {
  diag(`click intent ${intent} -> ${expectedState}`);
  const btn = page.getByTestId(`intent-btn-${intent}`).first();
  await expect(btn).toBeVisible({ timeout: 20000 });
  await btn.evaluate((el) => (el as HTMLElement).click());
  await expect(page.getByTestId("current-state")).toHaveText(expectedState, { timeout: 30000 });
  await dwell(page, 900);
}

async function revealLatestAgentCard(page: Page): Promise<void> {
  await page.evaluate(() => {
    const scroller = document.querySelector('[data-testid="chat-transcript"]') as HTMLElement | null;
    const rows = Array.from(
      document.querySelectorAll<HTMLElement>('[data-testid="chat-row-agent"]'),
    );
    const row = rows[rows.length - 1];
    if (!scroller || !row) return;
    const visibleRowHeight = Math.min(row.offsetHeight, scroller.clientHeight - 24);
    const centeredTop = row.offsetTop - Math.max(12, (scroller.clientHeight - visibleRowHeight) / 2);
    scroller.scrollTop = Math.max(0, centeredTop);
  });
  await dwell(page, 350);
}

async function clickFileLink(page: Page, name: RegExp): Promise<void> {
  const btn = page.getByRole("button", { name }).first();
  await expect(btn).toBeVisible({ timeout: 20000 });
  await btn.evaluate((el) => (el as HTMLElement).click());
}

async function openMarkdownLink(
  page: Page,
  chapters: ChapterRecorder,
  shot: (p: Page, name: string) => Promise<void>,
  name: RegExp,
  expectedText: string,
  stepId: string,
): Promise<void> {
  await clickFileLink(page, name);
  await expect(page.getByTestId("markdown-modal")).toBeVisible({ timeout: 10000 });
  await expect(page.getByTestId("markdown-modal-body")).toContainText(expectedText, { timeout: 10000 });
  await narrate(page, chapters, shot, stepId);
  await page.getByTestId("markdown-modal-close").evaluate((el) => (el as HTMLElement).click());
  await expect(page.getByTestId("markdown-modal")).toHaveCount(0, { timeout: 8000 });
  await dwell(page, 500);
}

async function openDiffLink(
  page: Page,
  chapters: ChapterRecorder,
  shot: (p: Page, name: string) => Promise<void>,
  name: RegExp,
  expectedText: string,
  stepId: string,
): Promise<void> {
  await clickFileLink(page, name);
  await expect(page.getByTestId("diff-artifact-modal")).toBeVisible({ timeout: 10000 });
  await expect(page.getByTestId("diff-artifact-modal-body")).toContainText(expectedText, { timeout: 10000 });
  await narrate(page, chapters, shot, stepId);
  await page.getByTestId("diff-artifact-modal-close").evaluate((el) => (el as HTMLElement).click());
  await expect(page.getByTestId("diff-artifact-modal")).toHaveCount(0, { timeout: 8000 });
  await dwell(page, 500);
}

test("dev-story-mining improvement loop video", async () => {
  test.setTimeout(300000);
  const browser: Browser = await chromium.launch({ headless: true });
  const context: BrowserContext = await browser.newContext(
    cameraContext({ recordVideoDir: VIDEO_DIR }),
  );
  const page: Page = await context.newPage();
  const video = page.video();
  const shot = makeShot(ARTIFACT_DIR);
  const chapters = new ChapterRecorder();

  try {
    await cinematicGoto(page, `${server.base}/#/`, { waitForTestId: "home-view" });
    await page.evaluate((stepsJson: string) => {
      (window as unknown as { __startTourWithSteps?: (s: string) => void })
        .__startTourWithSteps?.(stepsJson);
    }, JSON.stringify(TOUR_STEPS));
    await expect(page.getByTestId("tour-overlay")).toBeVisible({ timeout: 8000 });

    await narrate(page, chapters, shot, "dsm-home");
    await narrate(page, chapters, shot, "dsm-card");

    await page.getByTestId("story-card").first().getByTestId("new-session-btn").click();
    await page.waitForURL(/#\/s\/[0-9a-f-]{36}\/chat$/, { timeout: 20000 });
    await expect(page.getByTestId("current-state")).toHaveText("idle", { timeout: 15000 });
    await dwell(page, 1000);

    await narrate(page, chapters, shot, "dsm-idle");
    await clickIntent(page, "start", "prepare");
    await expect(page.getByTestId("chat-section")).toContainText("Codex cannot be pre-model intercepted", { timeout: 15000 });
    await expect(page.getByTestId("chat-section")).toContainText("Prepared source plan", { timeout: 15000 });

    await revealLatestAgentCard(page);
    await narrate(page, chapters, shot, "dsm-prepare");
    await clickIntent(page, "accept", "mine");
    await expect(page.getByTestId("chat-section")).toContainText("Mining Claude Code and Codex now", { timeout: 15000 });
    await expect(page.getByTestId("chat-section")).toContainText("Total reviewed", { timeout: 15000 });
    await expect(page.getByTestId("chat-section")).toContainText("Green - deterministic / defaultable", { timeout: 15000 });

    await revealLatestAgentCard(page);
    await narrate(page, chapters, shot, "dsm-mine");
    await openMarkdownLink(
      page,
      chapters,
      shot,
      /stories\/dev-story-mining\/demo-artifacts\/brief\.md$/,
      "Corpus Reviewed",
      "dsm-brief-modal",
    );
    await clickIntent(page, "accept", "mapping");
    await expect(page.getByTestId("chat-section")).toContainText("Mapping opportunities against story rooms", { timeout: 15000 });
    await expect(page.getByTestId("chat-section")).toContainText("stories/dev-story-mining/demo-artifacts/opportunity-map.md", { timeout: 15000 });

    await revealLatestAgentCard(page);
    await narrate(page, chapters, shot, "dsm-map-transition");
    await clickIntent(page, "accept", "map");
    await expect(page.getByTestId("chat-section")).toContainText("source-policy Starlark planner", { timeout: 15000 });

    await revealLatestAgentCard(page);
    await narrate(page, chapters, shot, "dsm-map");
    await openMarkdownLink(
      page,
      chapters,
      shot,
      /stories\/dev-story-mining\/demo-artifacts\/opportunity-map\.md$/,
      "Opportunity Map",
      "dsm-map-modal",
    );
    await clickIntent(page, "accept", "decide");
    await expect(page.getByTestId("chat-section")).toContainText("Selected: Starlark source planner", { timeout: 15000 });

    await revealLatestAgentCard(page);
    await narrate(page, chapters, shot, "dsm-decide");
    await clickIntent(page, "accept", "author");
    await expect(page.getByTestId("chat-section")).toContainText("stories/dev-story-mining/demo-artifacts/author.diff", { timeout: 15000 });

    await revealLatestAgentCard(page);
    await narrate(page, chapters, shot, "dsm-author");
    await openDiffLink(
      page,
      chapters,
      shot,
      /stories\/dev-story-mining\/demo-artifacts\/author\.diff$/,
      "Total intents mined",
      "dsm-diff-modal",
    );
    await clickIntent(page, "accept", "record");
    await expect(page.getByTestId("chat-section")).toContainText("Final report written", { timeout: 15000 });

    await revealLatestAgentCard(page);
    await narrate(page, chapters, shot, "dsm-record");
    await openMarkdownLink(
      page,
      chapters,
      shot,
      /stories\/dev-story-mining\/demo-artifacts\/final-report\.md$/,
      "What Was Found",
      "dsm-final-modal",
    );
    await clickIntent(page, "accept", "__exit__done");
    await expect(page.getByTestId("state-badge")).toHaveAttribute("data-terminal", "true", { timeout: 15000 });
    await page.evaluate(() => {
      (window as unknown as { __tourSkip?: () => void }).__tourSkip?.();
    });
    await expect(page.getByTestId("tour-overlay")).toBeHidden({ timeout: 8000 });
    await shot(page, "dsm-done");
  } catch (e) {
    diag(`FAILED: ${e instanceof Error ? e.stack ?? e.message : String(e)}`);
    diag(`--- server log ---\n${server?.log?.() ?? ""}`);
    throw e;
  } finally {
    await context.close();
    const mp4 = await saveVideoAsMp4(video, ARTIFACT_DIR, "dev-story-mining-demo");
    writeChapters(mp4, chapters.list());
    await browser.close();
  }
});
