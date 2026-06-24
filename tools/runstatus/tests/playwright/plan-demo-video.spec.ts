/**
 * plan-demo-video.spec.ts — deterministic, no-LLM tour of the ad-hoc structured
 * plan feature in the dev-story landing workbench:
 *
 *   workbench → propose a structured plan card → Accept & apply → applying runs
 *   the step → verifying runs the Starlark verify gate → PLAN DONE.
 *
 * Driven against a real `kitsoki web` server in the no-LLM posture
 * (--flow stories/dev-story/flows/plan_apply_verify_green.yaml: host.agent.task
 * stubbed to return the plan; the verify probe replays from the inspect
 * cassette the fixture names — no LLM, no network, no `gh`). The composer free
 * text routes to `work` (default_intent); the plan card's Accept & apply fires
 * intent-btn-accept_plan.
 *
 * Record: pnpm exec playwright test plan-demo-video --project=chromium
 * Fast:   WEB_CHAT_PACE=0 pnpm exec playwright test plan-demo-video --project=chromium
 */
import { test, expect, chromium, type Browser, type BrowserContext, type Page } from "@playwright/test";
import { fileURLToPath } from "url";
import path from "path";
import {
  startWebServer,
  type WebServer,
  prepareVideoDir,
  saveVideoAsMp4,
  dwell,
  cinematicGoto,
  pacedClick,
  waitForState,
  makeShot,
  SETTLE_MS,
  demoAddr,
} from "./_helpers/server.js";
import { installCurtain, liftCurtain, makeCaption, captureDiagnostics, DEMO_VIEWPORT } from "./_helpers/demo.js";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const repoRoot = path.resolve(__dirname, "../../../..");
const STORY_DIR = path.join(repoRoot, "stories", "dev-story");
const FLOW = path.join(STORY_DIR, "flows", "plan_demo.yaml");
const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "plan-demo");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "video");
const ADDR = demoAddr(7748);

let server: WebServer;
test.beforeAll(async () => {
  prepareVideoDir(VIDEO_DIR);
  // one-shot so accept → applying → verifying → plan_done auto-advances through
  // the deterministic gate (the operator's decision point is Accept).
  server = await startWebServer({ addr: ADDR, flow: FLOW, storiesDir: STORY_DIR, mode: "one-shot" });
});
test.afterAll(() => server?.stop());

test("ad-hoc structured plan tour", async () => {
  test.setTimeout(180_000);
  const browser: Browser = await chromium.launch();
  const context: BrowserContext = await browser.newContext({
    viewport: DEMO_VIEWPORT,
    deviceScaleFactor: 2,
    recordVideo: { dir: VIDEO_DIR, size: DEMO_VIEWPORT },
  });
  const page: Page = await context.newPage();
  const video = page.video();
  const shot = makeShot(ARTIFACT_DIR);
  const diag = captureDiagnostics(page, ARTIFACT_DIR);

  try {
    await installCurtain(page, "Ad-hoc structured plan — propose · accept · apply · verify");
    // Stage off-camera: home → new session → the chat view.
    await cinematicGoto(page, `${server.base}/#/`, { waitForTestId: "home-view" });
    await page.getByTestId("new-session-btn").first().click();
    await page.waitForURL(/#\/s\//);
    const m = page.url().match(/#\/s\/([^/]+)/);
    const sid = m ? m[1] : "";
    await cinematicGoto(page, `${server.base}/#/s/${sid}/chat`, { waitForTestId: "chat-section" });
    await dwell(page, SETTLE_MS);
    await liftCurtain(page);

    const beat = await makeCaption(page);

    // 1) The workbench.
    await beat("The free-form workbench", "Describe a piece of work in your own words.");
    await shot(page, "01-workbench");
    await dwell(page, SETTLE_MS);

    // 2) Describe the work → the planner proposes a STRUCTURED plan card.
    await beat("Describe the work", "“import the issues folder in the repo to github”");
    // The workbench floor renders a choice widget (Quick actions), so the free
    // text input is the floor composer (text-floor-input), which submits on Enter.
    const composer = page.getByTestId("text-floor-input");
    await composer.scrollIntoViewIfNeeded();
    await composer.fill("import the issues folder in the repo to github");
    await dwell(page, SETTLE_MS);
    await composer.press("Enter");
    await waitForState(page, "landing");
    await dwell(page, SETTLE_MS);
    await beat("A validated, executable plan", "Goal · one run-then-verify step · a Starlark verify gate — not prose.");
    await shot(page, "02-plan-card");
    await dwell(page, SETTLE_MS);

    // 3) Accept & apply → applying runs the step, verifying runs the gate.
    await beat("Accept & apply", "Apply runs the step, then proves it landed with a real verify gate.");
    await pacedClick(page, page.getByTestId("intent-btn-accept_plan"));
    await waitForState(page, "plan_done");
    await dwell(page, SETTLE_MS);

    // 4) The verify gate passed → PLAN DONE.
    await beat("Verified — plan done", "The Starlark gate confirmed the work landed (red-after-green).");
    await shot(page, "03-plan-done");
    await dwell(page, SETTLE_MS * 2);
  } catch (err) {
    diag.onThrow(err);
    throw err;
  } finally {
    await context.close();
    await saveVideoAsMp4(video, ARTIFACT_DIR, "plan-demo");
    await browser.close();
  }
});
