/**
 * Meta improve completion-loop feature-tour video.
 *
 * Drives a brief no-LLM Cloak session to terminal, records the completion
 * reminder affordance, then clicks Run improve now to exercise the real
 * story.improve meta mode through the web UI. The meta turn is backed by the
 * deterministic StubAgentCaller because the server runs with --flow.
 *
 * Fast gate:
 *   WEB_CHAT_PACE=0 KITSOKI_META_STREAM_DELAY_MS=60 pnpm exec playwright test meta-improve-video --project=chromium
 * Record:
 *   pnpm exec playwright test meta-improve-video --project=chromium
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
  dwell,
  cinematicGoto,
  ChapterRecorder,
  writeChapters,
  SETTLE_MS,
  demoAddr,
  maybeInstallAutoRrwebCapture,
  type WebServer,
} from "./_helpers/server.js";
import { cameraContext } from "./_helpers/camera.js";
import {
  META_IMPROVEMENT_TOUR_STEPS,
  type TourStep,
} from "../../src/tour/generated/meta-improvement.js";

const CHAPTER_SOURCE = "features/meta-improvement.yaml";
const ADDR = demoAddr(7771);
const STORY_DIR = path.join(repoRoot, "testdata", "apps", "cloak");
const FLOW = path.join(STORY_DIR, "flows", "winning.yaml");
const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "meta-improvement");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "video");
const DIAG_LOG = path.join(ARTIFACT_DIR, "diagnostic.log");
const STEP_BY_ID: Record<string, TourStep> = Object.fromEntries(
  META_IMPROVEMENT_TOUR_STEPS.map((s) => [s.id, s])
);

let server: WebServer;

function diag(msg: string): void {
  const line = `[${new Date().toISOString()}] ${msg}\n`;
  try {
    fs.appendFileSync(DIAG_LOG, line);
  } catch {
    /* best-effort */
  }
}

test.beforeAll(async () => {
  prepareVideoDir(VIDEO_DIR);
  fs.mkdirSync(ARTIFACT_DIR, { recursive: true });
  fs.writeFileSync(DIAG_LOG, "");
  if (process.env.KITSOKI_META_STREAM_DELAY_MS === undefined) {
    process.env.KITSOKI_META_STREAM_DELAY_MS =
      process.env.WEB_CHAT_PACE === "0" ? "60" : "90";
  }
  server = await startWebServer({ addr: ADDR, flow: FLOW, storiesDir: STORY_DIR });
});

test.afterAll(() => server?.stop());

async function narrate(
  page: Page,
  chapters: ChapterRecorder,
  shot: (p: Page, name: string) => Promise<void>,
  stepId: string
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
    }, JSON.stringify(META_IMPROVEMENT_TOUR_STEPS));
  }
  await page.evaluate((id: string) => {
    (window as unknown as { __tourGoTo?: (s: string) => void }).__tourGoTo?.(id);
  }, stepId);
  await expect(page.getByTestId("tour-overlay")).toBeVisible({ timeout: 8000 });
  await expect(page.getByTestId("tour-title")).toHaveText(step.title, { timeout: 12000 });
  chapters.open(step.id, step.title, CHAPTER_SOURCE);
  await dwell(page, step.dwellMs ?? 3000);
  await shot(page, step.id);
}

async function submitIntent(
  page: Page,
  sessionId: string,
  intent: string,
  expectedState: string,
  slots: Record<string, unknown> = {},
): Promise<void> {
  diag(`submit intent ${intent} ${JSON.stringify(slots)} -> ${expectedState}`);
  await server.rpc("runstatus.session.submit", { session_id: sessionId, intent, slots });
  await page.reload();
  await maybeInstallAutoRrwebCapture(page);
  await expect(page.getByTestId("current-state")).toHaveText(expectedState, { timeout: 30000 });
  await dwell(page, SETTLE_MS);
}

test("completion reminder to story.improve report video", async () => {
  test.setTimeout(300000);
  const browser: Browser = await chromium.launch({ headless: true });
  const context: BrowserContext = await browser.newContext(
    cameraContext({ recordVideoDir: VIDEO_DIR }),
  );
  const page: Page = await context.newPage();
  const video = page.video();
  const shot = makeShot(ARTIFACT_DIR);
  const chapters = new ChapterRecorder();

  let sawPrompt = false;
  let sawAutoToggle = false;
  let sawStreaming = false;
  let sawReport = false;
  let sawArtifacts = false;

  try {
    await cinematicGoto(page, `${server.base}/#/`, { waitForTestId: "home-view" });
    await page.evaluate((stepsJson: string) => {
      (window as unknown as { __startTourWithSteps?: (s: string) => void })
        .__startTourWithSteps?.(stepsJson);
    }, JSON.stringify(META_IMPROVEMENT_TOUR_STEPS));
    await expect(page.getByTestId("tour-overlay")).toBeVisible({ timeout: 8000 });

    await narrate(page, chapters, shot, "mi-intro-home");
    await narrate(page, chapters, shot, "mi-story-card");
    await narrate(page, chapters, shot, "mi-new-session");

    const stories = await server.rpc<Array<{ path: string }>>("runstatus.stories.list", {});
    const storyPath = stories[0]?.path;
    if (!storyPath) throw new Error("no story to drive");
    const { session_id: sessionId } = await server.rpc<{ session_id: string }>(
      "runstatus.session.new",
      { story_path: storyPath },
    );
    await cinematicGoto(page, `${server.base}/#/s/${sessionId}/chat`, {
      waitForTestId: "current-state",
    });
    await expect(page.getByTestId("current-state")).toHaveText("foyer", { timeout: 15000 });
    await dwell(page, SETTLE_MS);

    await narrate(page, chapters, shot, "mi-start-run");
    await submitIntent(page, sessionId, "go", "cloakroom", { direction: "west" });

    await narrate(page, chapters, shot, "mi-mid-run");
    await submitIntent(page, sessionId, "hang_cloak", "cloakroom");
    await submitIntent(page, sessionId, "go", "foyer", { direction: "east" });
    await submitIntent(page, sessionId, "go", "bar.lit", { direction: "south" });

    await narrate(page, chapters, shot, "mi-complete-run");
    await page.getByTestId("intent-btn-read_message").first().evaluate((el) => (el as HTMLElement).click());
    await expect(page.getByTestId("current-state")).toHaveText("ended", { timeout: 30000 });
    await expect(page.getByTestId("state-badge")).toHaveAttribute("data-terminal", "true", {
      timeout: 15000,
    });
    await expect(page.getByTestId("improve-prompt")).toBeVisible({ timeout: 15000 });
    sawPrompt = true;

    await narrate(page, chapters, shot, "mi-reminder");
    sawAutoToggle = await page.getByTestId("improve-auto-toggle").isVisible().catch(() => false);
    await narrate(page, chapters, shot, "mi-auto-run");
    await narrate(page, chapters, shot, "mi-evidence-report");
    await expect(page.getByTestId("improve-report-toggle")).toBeChecked({ timeout: 15000 });

    await narrate(page, chapters, shot, "mi-run-improve");
    const runButton = page.getByTestId("improve-run").first();
    await expect(runButton).toBeEnabled({ timeout: 15000 });
    await runButton.evaluate((el) => (el as HTMLElement).click());
    await expect(page.getByTestId("meta-overlay")).toBeVisible({ timeout: 15000 });
    await expect(page.getByTestId("meta-row-streaming")).toBeVisible({ timeout: 15000 });
    sawStreaming = true;

    await narrate(page, chapters, shot, "mi-improve-stream");
    await expect(page.getByTestId("meta-row-streaming")).toHaveCount(0, { timeout: 60000 });
    const report = page.getByTestId("meta-row-agent").last();
    await expect(report).toContainText("Introspection report", { timeout: 15000 });
    await expect(report).toContainText("Tool and permission notes", { timeout: 15000 });
    await expect(report).toContainText("Evidence bundle", { timeout: 15000 });
    await expect(report).toContainText("Posting", { timeout: 15000 });
    await expect(page.getByTestId("improve-report-artifacts")).toBeVisible({ timeout: 30000 });
    sawReport = true;
    await narrate(page, chapters, shot, "mi-report-ready");

    await page.getByTestId("meta-close").click();
    await expect(page.getByTestId("improve-report-status")).toBeVisible({ timeout: 30000 });
    await expect(page.getByTestId("improve-report-artifacts")).toContainText("har.json", {
      timeout: 15000,
    });
    await expect(page.getByTestId("improve-report-artifacts")).toContainText("rrweb.json", {
      timeout: 15000,
    });
    await expect(page.getByTestId("improve-report-artifacts")).toContainText("trace.redacted.jsonl", {
      timeout: 15000,
    });
    const filedPath = (await page.getByTestId("improve-report-path").innerText()).trim();
    const reportPath = path.join(repoRoot, filedPath);
    if (!fs.existsSync(reportPath)) {
      throw new Error(`meta improve report path does not exist: ${reportPath}`);
    }
    const reportMarkdown = fs.readFileSync(reportPath, "utf8");
    for (const text of ["## Introspection Report", "## Evidence Bundle", "## Artifacts"]) {
      if (!reportMarkdown.includes(text)) {
        throw new Error(`meta improve report missing ${text}: ${reportPath}`);
      }
    }
    const artifactsDir = reportPath.replace(/\.md$/, ".artifacts");
    for (const name of ["har.json", "rrweb.json", "trace.redacted.jsonl"]) {
      const artifactPath = path.join(artifactsDir, name);
      if (!fs.existsSync(artifactPath)) {
        throw new Error(`missing meta improve artifact ${name}: ${artifactPath}`);
      }
    }
    sawArtifacts = true;
    await narrate(page, chapters, shot, "mi-artifacts-filed");

    await page.getByTestId("tour-next").click().catch(() => undefined);
  } catch (e) {
    diag(`FAILED: ${e instanceof Error ? e.stack ?? e.message : String(e)}`);
    diag(`--- server log ---\n${server?.log?.() ?? ""}`);
    throw e;
  } finally {
    await context.close();
    const mp4 = await saveVideoAsMp4(video, ARTIFACT_DIR, "meta-improvement-demo");
    writeChapters(mp4, chapters.list());
    await browser.close();
  }

  diag(
    `VERDICTS sawPrompt=${sawPrompt} sawAutoToggle=${sawAutoToggle} ` +
      `sawStreaming=${sawStreaming} sawReport=${sawReport} sawArtifacts=${sawArtifacts}`
  );
  expect(sawPrompt, "completion improve prompt visible").toBe(true);
  expect(sawAutoToggle, "auto-run toggle visible").toBe(true);
  expect(sawStreaming, "story.improve streaming turn visible").toBe(true);
  expect(sawReport, "introspection report visible").toBe(true);
  expect(sawArtifacts, "evidence report artifacts filed").toBe(true);
});
