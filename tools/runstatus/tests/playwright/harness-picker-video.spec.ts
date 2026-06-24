/**
 * Harness-profile picker feature-spotlight video — driven from the CHAT pane.
 *
 * Drives the runstatus chat-drive surface (InteractiveView) against a real
 * `kitsoki web` server in the deterministic NO-LLM posture: the agent-probe
 * story (testdata/apps/agent_probe) asks the active harness profile's model to
 * identify itself; a host cassette returns a different scripted identity per
 * turn. The demo switches the header provider (claude-native → synthetic-claude
 * → codex-native), types "who are you" each time, and shows two things:
 *   1. the chat answer changes with the provider, and
 *   2. each agent row in the trace (right pane) is stamped with the selected
 *      profile + model — the live selection, not the cassette.
 *
 * No real key is used: the harness fixture's ${SYNTHETIC_API_KEY} is satisfied
 * by a dummy the spec sets; the flow posture never forks a backend.
 *
 * Validate fast (assertions only, no dwells):
 *   WEB_CHAT_PACE=0 pnpm exec playwright test harness-picker-video --project=chromium
 * Record at watch-speed:
 *   pnpm exec playwright test harness-picker-video --project=chromium
 *
 * Per-step context + failures: .artifacts/harness-picker/ERROR.txt + NN-*.png.
 */
import { test, expect, chromium, type Browser, type BrowserContext, type Page } from "@playwright/test";
import path from "path";
import {
  startWebServer,
  repoRoot,
  makeShot,
  prepareVideoDir,
  saveVideoAsMp4,
  ChapterRecorder,
  writeChapters,
  demoAddr,
  cinematicGoto,
  dwell,
  SETTLE_MS,
  type WebServer,
} from "./_helpers/server.js";
import { makeCaption, captureDiagnostics } from "./_helpers/demo.js";
import { cameraContext } from "./_helpers/camera.js";

const ADDR = demoAddr(7752);
const STORY_DIR = path.join(repoRoot, "testdata", "apps", "agent_probe");
const FLOW = path.join(STORY_DIR, "flows", "who_are_you.flow.yaml");
const CASSETTE = path.join(STORY_DIR, "flows", "who_are_you.cassette.yaml");
const CONFIG = path.join(repoRoot, "tools", "runstatus", "tests", "playwright", "fixtures", "harness.kitsoki.yaml");
const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "harness-picker");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "video");
const CHAPTER_SOURCE = "features/harness-picker.yaml";

// The three provider switches, in order, matched to the cassette's three
// scripted identities. `reply` is the distinguishing word in each answer.
const STEPS: {
  profile: string;
  reply: string;
  caption: string;
  sub: string;
  model?: string;
  effort?: string;
}[] = [
  { profile: "claude-native", reply: "Claude", model: "opus", effort: "high", caption: "Native Anthropic Claude Code", sub: "Pick the model (opus) and the reasoning effort (high)." },
  { profile: "synthetic-claude", reply: "GLM-5.1", model: "hf:zai-org/GLM-5.2", caption: "claude-code on synthetic.new", sub: "Pick a specific always-on model — hf:zai-org/GLM-5.2." },
  { profile: "codex-native", reply: "Codex", model: "gpt-5", caption: "codex on your subscription", sub: "A different backend CLI — with its own models." },
];

let server: WebServer;

test.beforeAll(async () => {
  prepareVideoDir(VIDEO_DIR);
  server = await startWebServer({
    addr: ADDR,
    flow: FLOW,
    hostCassette: CASSETTE,
    storiesDir: STORY_DIR,
    config: CONFIG,
    extraEnv: { SYNTHETIC_API_KEY: "demo-key-not-real" },
  });
});

test.afterAll(() => server?.stop());

async function typeInto(page: Page, testid: string, value: string): Promise<void> {
  const input = page.getByTestId(testid).first();
  await expect(input).toBeVisible({ timeout: 15000 });
  await input.evaluate((el, v) => {
    const node = el as HTMLInputElement | HTMLTextAreaElement;
    const setter = Object.getOwnPropertyDescriptor(Object.getPrototypeOf(node), "value")?.set;
    setter?.call(node, v);
    node.dispatchEvent(new Event("input", { bubbles: true }));
  }, value);
}

test("harness picker from the chat pane — switch provider, ask who-are-you", async () => {
  test.setTimeout(300000);
  const browser: Browser = await chromium.launch({ headless: true });
  const context: BrowserContext = await browser.newContext(
    cameraContext({ recordVideoDir: VIDEO_DIR }),
  );
  const page: Page = await context.newPage();
  const video = page.video();
  const shot = makeShot(ARTIFACT_DIR);
  const diag = captureDiagnostics(page, ARTIFACT_DIR);
  const chapters = new ChapterRecorder();

  const provider = () => page.getByTestId("provider-select");
  const harnessLabels = () => page.getByTestId("trace-harness-label");

  try {
    const { session_id: sid } = await server.rpc<{ session_id: string }>(
      "runstatus.session.new",
      { story_path: path.join(STORY_DIR, "app.yaml") },
    );

    diag.mark("open-chat");
    await cinematicGoto(page, `${server.base}/#/s/${sid}/chat`, { waitForTestId: "composer-input" });
    chapters.open("hp-intro", "Pick the harness, live", CHAPTER_SOURCE);
    const beat = await makeCaption(page, 3800);
    await page.addStyleTag({
      content:
        `#demo-caption{top:auto !important;bottom:30px !important;}` +
        `[data-testid="harness-picker"]{outline:2px solid #fbbf24;outline-offset:3px;border-radius:6px;}`,
    });

    await beat(
      "Pick the harness, live — from the chat",
      "The header has a provider dropdown; switching it changes which LLM answers the next turn.",
    );
    await shot(page, "00-chat-open");

    for (let i = 0; i < STEPS.length; i++) {
      const step = STEPS[i];
      chapters.open(`hp-${step.profile}`, step.caption, CHAPTER_SOURCE);
      diag.mark(`switch-${step.profile}`);
      await beat(step.caption, step.sub);
      await dwell(page, SETTLE_MS);
      await provider().selectOption(step.profile);
      await expect(provider()).toHaveValue(step.profile);
      await dwell(page, SETTLE_MS);

      // Pick a model from this profile's catalog…
      if (step.model) {
        await page.getByTestId("model-select").selectOption(step.model);
        await expect(page.getByTestId("model-select")).toHaveValue(step.model);
        await dwell(page, SETTLE_MS);
      }
      // …and an effort, where the model supports it.
      if (step.effort) {
        await page.getByTestId("effort-select").selectOption(step.effort);
        await expect(page.getByTestId("effort-select")).toHaveValue(step.effort);
        await dwell(page, SETTLE_MS);
      }

      // Ask "who are you" from the composer.
      await typeInto(page, "composer-input", "who are you");
      await dwell(page, SETTLE_MS);
      await page.getByTestId("composer-send").first().evaluate((el) => (el as HTMLElement).click());

      // The chat shows this provider's distinct answer…
      await expect(page.getByTestId("chat-transcript")).toContainText(step.reply, { timeout: 15000 });
      // …and the trace stamps the selected profile on this turn's agent call.
      await expect(harnessLabels().filter({ hasText: step.profile }).first()).toBeVisible({ timeout: 15000 });
      await beat(`${step.profile} answered`, `Trace row stamped profile=${step.profile} — matching the picker.`);
      await shot(page, `0${i + 1}-${step.profile}`);
      await dwell(page, SETTLE_MS);
    }

    // Final proof: three agent calls, three distinct profile stamps in the
    // trace, and the claude call stamped with its picked model + effort.
    for (const step of STEPS) {
      await expect(harnessLabels().filter({ hasText: step.profile }).first()).toBeVisible();
    }
    await expect(harnessLabels().filter({ hasText: "opus" }).first()).toBeVisible();
    await expect(harnessLabels().filter({ hasText: "high" }).first()).toBeVisible();
    // synthetic ran a specific hf: model.
    await expect(harnessLabels().filter({ hasText: "hf:zai-org/GLM-5.2" }).first()).toBeVisible();
    chapters.open("hp-recap", "Three turns, three providers", CHAPTER_SOURCE);
    await beat("Three turns, three providers", "Each agent call in the trace carries the profile, model + effort it ran on.");
    await shot(page, "04-trace-all");
    await dwell(page, SETTLE_MS);
  } catch (err) {
    diag.onThrow(err);
    throw err;
  } finally {
    chapters.close();
    await context.close();
    const mp4 = await saveVideoAsMp4(video, ARTIFACT_DIR, "harness-picker-demo");
    writeChapters(mp4, chapters.list());
    await browser.close();
  }
});
