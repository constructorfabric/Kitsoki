/**
 * Persona QA feature tour video.
 *
 * Drives the scenario-qa story against a real kitsoki web server in a
 * deterministic no-LLM posture. The flow fixture stubs host calls so the
 * browser still exercises the story surface end to end:
 *
 *   home -> new session -> type preview -> type check -> next transport
 *   -> report -> main room
 *
 * Validate fast:
 *   WEB_CHAT_PACE=0 pnpm exec playwright test persona-qa-video --project=chromium
 * Record:
 *   pnpm exec playwright test persona-qa-video --project=chromium
 */
import { test, expect, chromium, type Browser, type BrowserContext, type Locator, type Page } from "@playwright/test";
import path from "path";
import fs from "fs";
import {
  startWebServer,
  repoRoot,
  makeShot,
  waitForState,
  prepareVideoDir,
  saveVideoAsMp4,
  dwell,
  cinematicGoto,
  ChapterRecorder,
  writeChapters,
  demoAddr,
  SETTLE_MS,
  PACE,
  type WebServer,
} from "./_helpers/server.js";
import { cameraContext } from "./_helpers/camera.js";
import { PERSONA_QA_TOUR_STEPS, type TourStep } from "../../src/tour/generated/persona-qa.js";

const ADDR = demoAddr(7758);
const STORY_DIR = path.join(repoRoot, "stories", "scenario-qa");
const FLOW = path.join(STORY_DIR, "flows", "persona_qa_demo.yaml");
const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "persona-qa");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "video");
const CHAPTER_SOURCE = "features/persona-qa.yaml";
const DIAG_LOG = path.join(ARTIFACT_DIR, "diagnostic.log");

let server: WebServer;

function diag(msg: string): void {
  try {
    fs.appendFileSync(DIAG_LOG, `[${new Date().toISOString()}] ${msg}\n`);
  } catch {
    /* best effort */
  }
}

test.beforeAll(async () => {
  prepareVideoDir(VIDEO_DIR);
  fs.mkdirSync(ARTIFACT_DIR, { recursive: true });
  fs.writeFileSync(DIAG_LOG, "");
  server = await startWebServer({ addr: ADDR, flow: FLOW, storiesDir: STORY_DIR });
});

test.afterAll(() => server?.stop());

function currentRouteKind(page: Page): "home" | "interactive" | "any" {
  const url = page.url();
  if (url.includes("/chat")) return "interactive";
  if (url.match(/#\/s\/[0-9a-f-]{36}$/)) return "any";
  return "home";
}

function resolveTarget(page: Page, step: TourStep): Locator {
  return page.getByTestId(step.target!).first();
}

async function typeAndSend(page: Page, text: string, beforeSend?: () => Promise<void>): Promise<void> {
  const input = page.getByTestId("text-floor-input").or(page.getByTestId("composer-input")).first();
  await expect(input).toBeVisible({ timeout: 15000 });
  await input.scrollIntoViewIfNeeded().catch(() => undefined);
  await input.click();
  await input.fill("");
  await input.pressSequentially(text, { delay: Math.round(24 * PACE) });
  await dwell(page, 1200);
  if (beforeSend) {
    await beforeSend();
    await dwell(page, 500);
  }
  const send = page.getByTestId("text-floor-send").or(page.getByTestId("composer-send")).first();
  await send.evaluate((el) => (el as HTMLButtonElement).click());
}

async function injectTour(page: Page): Promise<void> {
  await page.evaluate((stepsJson: string) => {
    (window as unknown as { __startTourWithSteps?: (s: string) => void })
      .__startTourWithSteps?.(stepsJson);
  }, JSON.stringify(PERSONA_QA_TOUR_STEPS));
  await expect(page.getByTestId("tour-overlay")).toBeVisible({ timeout: 8000 });
}

async function waitForScenarioQASettle(page: Page, step: TourStep): Promise<void> {
  if (step.id === "pqa-start") {
    await page.waitForURL(/#\/s\/[0-9a-f-]{36}\/chat$/, { timeout: 15000 });
    await waitForState(page, "idle", 15000);
    return;
  }
  if (step.id === "pqa-preview") {
    await waitForState(page, "idle", 15000);
    await expect(page.getByTestId("chat-section")).toContainText("Preview ready: 2 transport checks", { timeout: 15000 });
    await expect(page.getByTestId("chat-section")).toContainText("required user input affordance as a QA engineer and developer", { timeout: 15000 });
    await expect(page.getByTestId("chat-section")).toContainText("Goal: Check that the requested behavior is usable", { timeout: 15000 });
    await expect(page.getByTestId("chat-section")).toContainText("Web", { timeout: 15000 });
    await expect(page.getByTestId("chat-section")).toContainText("TUI", { timeout: 15000 });
    await expect(page.getByTestId("chat-section")).toContainText(/Last run\s*\(none yet\)/, { timeout: 15000 });
    return;
  }
  if (step.id === "pqa-check") {
    await waitForState(page, "recording", 15000);
    await expect(page.getByTestId("chat-section")).toContainText(/Transport check\s*1 of 2/, { timeout: 15000 });
    await expect(page.getByTestId("chat-section")).toContainText(/Driver status\s*captured/, { timeout: 15000 });
    await expect(page.getByTestId("chat-section")).toContainText(/Pass\s*1/, { timeout: 15000 });
    await expect(page.getByTestId("chat-section")).toContainText("required user input affordance as a QA engineer and developer", { timeout: 15000 });
    return;
  }
  if (step.id === "pqa-next-tui") {
    await waitForState(page, "report", 15000);
    await expect(page.getByTestId("chat-section")).toContainText("2 / 2 transport checks passed", { timeout: 15000 });
    await expect(page.getByTestId("chat-section")).toContainText("Summary report", { timeout: 15000 });
    await expect(page.getByTestId("chat-section")).toContainText("required user input affordance", { timeout: 15000 });
    await expect(page.getByTestId("chat-section")).toContainText("deck.slidey.json", { timeout: 15000 });
    await expect(page.getByTestId("chat-section")).toContainText("clips/web-required-input.rrweb.json", { timeout: 15000 });
    await expect(page.getByTestId("chat-section")).toContainText("clips/tui-required-input.rrweb.json", { timeout: 15000 });
    await expect(page.getByTestId("intent-btn-main_room")).toBeVisible({ timeout: 15000 });
    return;
  }
  if (step.id === "pqa-main-room") {
    await waitForState(page, "idle", 15000);
    await expect(page.getByTestId("chat-section")).toContainText("SCENARIO QA", { timeout: 15000 });
    await expect(page.getByTestId("chat-section")).toContainText(/Last run\s*scenario-qa-affordance-demo-run/, { timeout: 15000 });
    await expect(page.getByTestId("intent-btn-report")).toBeVisible({ timeout: 15000 });
  }
}

async function runStepDrive(page: Page, step: TourStep, shot?: (page: Page, label: string) => Promise<string>): Promise<void> {
  for (const action of step.drive ?? []) {
    if (action.type === "type-and-send") {
      await typeAndSend(page, action.text, shot ? () => shot(page, `${step.id}-typed`) : undefined);
    } else if (action.type === "click-intent") {
      await page.getByTestId(`intent-btn-${action.intent}`).first().evaluate((el) => (el as HTMLElement).click());
    } else if (action.type === "wait-state") {
      await waitForState(page, action.state, 15000);
    } else if (action.type === "dwell-ms") {
      await dwell(page, action.ms);
    }
  }
  if ((step.drive ?? []).length > 0) {
    await waitForScenarioQASettle(page, step);
    await dwell(page, SETTLE_MS);
  }
}

async function performAction(page: Page, step: TourStep): Promise<void> {
  const target = resolveTarget(page, step);
  await target.scrollIntoViewIfNeeded().catch(() => undefined);
  if (step.advance === "route-match") {
    await target.click();
  } else {
    await target.evaluate((el) => (el as HTMLElement).click());
  }
  await waitForScenarioQASettle(page, step);
  await dwell(page, SETTLE_MS);
}

test("persona qa end-to-end tour video", async () => {
  test.setTimeout(300000);
  const browser: Browser = await chromium.launch({ headless: true });
  const context: BrowserContext = await browser.newContext(
    cameraContext({ recordVideoDir: VIDEO_DIR }),
  );
  const page = await context.newPage();
  const video = page.video();
  const shot = makeShot(ARTIFACT_DIR);
  const chapters = new ChapterRecorder();

  try {
    await cinematicGoto(page, `${server.base}/#/`, { waitForTestId: "home-view" });
    await expect(page.getByTestId("story-title").first()).toHaveText(/Scenario QA/i, { timeout: 15000 });
    await injectTour(page);

    for (const step of PERSONA_QA_TOUR_STEPS) {
      diag(`step ${step.id}`);
      const route = currentRouteKind(page);
      if (step.route !== "any" && step.route !== route) {
        diag(`skip ${step.id}; current route ${route}`);
        continue;
      }

      if (step.waitForTarget) {
        await expect(page.getByTestId(step.waitForTarget).first()).toBeVisible({ timeout: 15000 });
      }

      const titleEl = page.getByTestId("tour-title");
      await expect(titleEl).toHaveText(step.title, { timeout: 12000 });
      chapters.open(step.id, step.title, CHAPTER_SOURCE);

      await dwell(page, step.dwellMs ?? 3200);
      await shot(page, step.id);

      if (step.kind === "explain") {
        await runStepDrive(page, step, shot);
        await page.getByTestId("tour-next").click();
        await dwell(page, 700);
      } else {
        await performAction(page, step);
      }
    }

    await expect(page.getByTestId("tour-overlay")).toHaveCount(0, { timeout: 5000 });
    await dwell(page, 2500);
    await shot(page, "pqa-dismissed");
  } catch (e) {
    diag(`FAILED: ${e instanceof Error ? e.stack ?? e.message : String(e)}`);
    diag(`--- server log ---\n${server?.log?.() ?? ""}`);
    throw e;
  } finally {
    await context.close();
    const mp4 = await saveVideoAsMp4(video, ARTIFACT_DIR, "persona-qa-demo");
    writeChapters(mp4, chapters.list());
    await browser.close();
  }

  const pngs = fs.readdirSync(ARTIFACT_DIR).filter((f) => f.endsWith(".png"));
  console.log(`[persona-qa-video] screenshots (${pngs.length}) in ${ARTIFACT_DIR}`);
});
