/**
 * Generic HTML semantic annotation video.
 *
 * Records a deterministic, no-LLM tour of the generalized semantic overlay /
 * spatial oracle paths. The page is a focused Vite harness around the real
 * ArtifactAnnotator component: it feeds sidecars/events through DataSource DI,
 * clicks real semantic markers, types visible feedback, and shows the serialized
 * semantic_element anchor that would ride to the read-only feedback/oracle path.
 *
 * Validate fast:
 *   WEB_CHAT_PACE=0 pnpm exec playwright test generic-html-semantic-video --project=chromium
 * Record at watch-speed:
 *   pnpm exec playwright test generic-html-semantic-video --project=chromium
 */
import { test, expect, chromium, type Browser, type BrowserContext, type Page } from "@playwright/test";
import { createServer, type ViteDevServer } from "vite";
import { fileURLToPath } from "url";
import path from "path";
import fs from "fs";
import net from "net";
import {
  makeShot,
  prepareVideoDir,
  saveVideoAsMp4,
  dwell,
  ChapterRecorder,
  writeChapters,
  SETTLE_MS,
  repoRoot,
  PACE,
} from "./_helpers/server.js";
import { cameraContext } from "./_helpers/camera.js";
import { makeCaption, makeSpotlight, captureDiagnostics } from "./_helpers/demo.js";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const projectRoot = path.resolve(__dirname, "../..");
const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "generic-html-semantic");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "video");
const CHAPTER_SOURCE = "tools/runstatus/tests/playwright/generic-html-semantic-video.spec.ts";

async function freePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const srv = net.createServer();
    srv.once("error", reject);
    srv.listen(0, "127.0.0.1", () => {
      const addr = srv.address();
      srv.close(() => {
        if (typeof addr === "object" && addr) resolve(addr.port);
        else reject(new Error("no free port address"));
      });
    });
  });
}

async function startHarnessServer(): Promise<{ url: string; close: () => Promise<void> }> {
  const port = await freePort();
  const server: ViteDevServer = await createServer({
    configFile: path.join(projectRoot, "vite.config.ts"),
    logLevel: "error",
    server: {
      host: "127.0.0.1",
      port,
      strictPort: true,
      open: false,
    },
  });
  await server.listen();
  const base = server.resolvedUrls?.local[0] ?? `http://127.0.0.1:${port}/`;
  return {
    url: new URL("tests/playwright/fixtures/generic-html-semantic/harness.html", base).toString(),
    close: () => server.close(),
  };
}

async function typeFeedback(page: Page, text: string): Promise<void> {
  const input = page.getByTestId("feedback-input");
  await input.click();
  await page.keyboard.press(process.platform === "darwin" ? "Meta+A" : "Control+A");
  await page.keyboard.press("Backspace");
  await input.pressSequentially(text, { delay: Math.round(12 * PACE) });
}

async function sendSelectedFeedback(page: Page, text: string): Promise<void> {
  await expect(page.getByTestId("selected-anchor")).toContainText("semantic_element", { timeout: 10000 });
  await typeFeedback(page, text);
  await dwell(page, 800);
  await page.getByTestId("send-feedback").click();
  await expect(page.getByTestId("sent-status")).toContainText("semantic_element");
}

test("generic HTML semantic annotation tour video", async () => {
  test.setTimeout(360000);
  prepareVideoDir(VIDEO_DIR);
  fs.mkdirSync(ARTIFACT_DIR, { recursive: true });

  const harness = await startHarnessServer();
  const browser: Browser = await chromium.launch({ headless: true });
  const context: BrowserContext = await browser.newContext(cameraContext({ recordVideoDir: VIDEO_DIR }));
  const page: Page = await context.newPage();
  const video = page.video();
  const shot = makeShot(ARTIFACT_DIR);
  const chapters = new ChapterRecorder();
  const { mark, onThrow } = captureDiagnostics(page, ARTIFACT_DIR);

  try {
    mark("open harness");
    await page.goto(harness.url);
    await expect(page.getByTestId("semantic-demo")).toBeVisible({ timeout: 15000 });
    await dwell(page, SETTLE_MS);

    const caption = await makeCaption(page, 2800);
    const spotlight = await makeSpotlight(page);

    mark("intro");
    chapters.open("intro", "Generic semantic annotation", CHAPTER_SOURCE);
    await spotlight("[data-testid='semantic-demo']");
    await caption(
      "One annotation mechanism for any HTML-shaped artifact",
      "The harness uses the real ArtifactAnnotator and sidecar contracts.",
      2800,
    );
    await shot(page, "intro");

    mark("html selector");
    await page.getByTestId("case-tab-html-selector").click();
    await expect(page.getByTestId("case-html-selector")).toBeVisible();
    await expect(page.getByTestId("so-marker-form.submit")).toBeVisible({ timeout: 10000 });
    chapters.open("html-selector", "Selector-only static HTML field", CHAPTER_SOURCE);
    await spotlight("[data-testid='so-marker-form.submit']");
    await caption(
      "Static HTML: selector-only sidecar",
      "The marker was resolved from the iframe DOM at annotation time.",
      2600,
    );
    await page.getByTestId("so-marker-form.submit").click();
    await sendSelectedFeedback(
      page,
      "Why does this submit control look enabled before the required email field is valid?",
    );
    await shot(page, "html-selector");

    mark("object data");
    await page.getByTestId("case-tab-object-data").click();
    await expect(page.getByTestId("case-object-data")).toBeVisible();
    await expect(page.getByTestId("so-marker-issue.priority")).toBeVisible({ timeout: 10000 });
    chapters.open("object-data", "Rendered object field", CHAPTER_SOURCE);
    await spotlight("[data-testid='so-marker-issue.priority']");
    await caption(
      "Object views: click the represented field",
      "The anchor preserves value and data.path, not just a DOM node.",
      2600,
    );
    await page.getByTestId("so-marker-issue.priority").click();
    await sendSelectedFeedback(page, "This priority value should explain why ISS-248 is still blocked.");
    await shot(page, "object-data");

    mark("mockup bbox");
    await page.getByTestId("case-tab-mockup-bbox").click();
    await expect(page.getByTestId("case-mockup-bbox")).toBeVisible();
    await expect(page.getByTestId("so-marker-wire.submit")).toBeVisible({ timeout: 10000 });
    chapters.open("mockup-bbox", "BBox marker over a mockup", CHAPTER_SOURCE);
    await spotlight("[data-testid='so-marker-wire.submit']");
    await caption(
      "Mockups: producers can declare boxes",
      "No DOM selector is required when the sidecar carries a bbox.",
      2600,
    );
    await page.getByTestId("so-marker-wire.submit").click();
    await sendSelectedFeedback(page, "Make this create-account CTA less dominant than the plan comparison.");
    await shot(page, "mockup-bbox");

    mark("rrweb selector");
    await page.getByTestId("case-tab-rrweb-selector").click();
    await expect(page.getByTestId("case-rrweb-selector")).toBeVisible();
    await expect(page.getByTestId("so-marker-run.start")).toBeVisible({ timeout: 15000 });
    chapters.open("rrweb-selector", "rrweb selector resolved field", CHAPTER_SOURCE);
    await spotlight("[data-testid='so-marker-run.start']");
    await caption(
      "Bug playbacks: selectors resolve inside rrweb",
      "ReplayFrame enriches the sidecar against the reconstructed DOM.",
      2600,
    );
    await page.getByTestId("so-marker-run.start").click();
    await sendSelectedFeedback(page, "In this bug replay, is Start the control the operator should click next?");
    await shot(page, "rrweb-selector");

    mark("live embed");
    await page.getByTestId("case-tab-live-embed").click();
    await expect(page.getByTestId("case-live-embed")).toBeVisible();
    await expect(page.getByTestId("aa-slidey-embed")).toBeVisible({ timeout: 10000 });
    chapters.open("live-embed", "Live embed producer pick", CHAPTER_SOURCE);
    await spotlight("[data-testid='aa-slidey-embed']");
    await caption(
      "Live embeds: the producer owns picking",
      "The iframe posts an opaque embed:pick that becomes the same semantic_element anchor.",
      2600,
    );
    await page.frameLocator("[data-testid='aa-slidey-embed']").locator("[data-pick-ref='scene-7.title']").click();
    await sendSelectedFeedback(page, "Tighten the headline on this scene without changing the metrics row.");
    await shot(page, "live-embed");

    mark("wrap");
    chapters.open("wrap", "One read-only anchor shape", CHAPTER_SOURCE);
    await spotlight("[data-testid='selected-anchor']");
    await caption(
      "The result is one read-only feedback bundle",
      "semantic_element carries plugin, ref, selector/text/value/data, and bbox when available.",
      3200,
    );
    await shot(page, "wrap");
    await spotlight(null);
  } catch (err) {
    onThrow(err);
    throw err;
  } finally {
    chapters.close();
    await context.close();
    const videoPath = await saveVideoAsMp4(video, ARTIFACT_DIR, "generic-html-semantic-demo");
    writeChapters(videoPath, chapters.list());
    await browser.close();
    await harness.close();
  }
});
