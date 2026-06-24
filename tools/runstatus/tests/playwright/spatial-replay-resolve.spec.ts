/**
 * spatial-replay-resolve.spec.ts — Playwright end-to-end proving the spatial
 * picker resolves a REAL app control against an rrweb-reconstructed DOM (epic
 * shared decision 2: "rrweb's reconstructed DOM is the pixel↔element bridge").
 *
 * Same deterministic, no-LLM posture as spatial-capture.spec.ts: the built
 * dist/index.html is served WITHOUT an inlined snapshot, so createDataSource()
 * returns LiveSource and the page issues real JSON-RPC — which we intercept with
 * page.route. The oracle (runstatus.session.offpath) is STUBBED. No live kitsoki
 * server, no LLM, no ffmpeg.
 *
 * The new RPC here is runstatus.video.events: it returns a CHECKED-IN rrweb
 * fixture (tests/fixtures/spatial-replay.rrweb.json) recorded at a FIXED
 * 1280×720 viewport — a CONTENT-RICH real kitsoki room: the interactive chat of
 * the bugfix story (a populated chat transcript, the state diagram + trace
 * panels, and the ACTIONS intent buttons). When videoEvents returns events,
 * ReviewPage renders the rrweb Replayer (REAL reconstructed UI) under the picker
 * instead of the opaque <video>; the picker's root is the replay iframe's
 * contentDocument.
 *
 * Flow: open /review, flag the scene (selects the flag → mounts ReplayFrame),
 * wait for the replay to render, click over the Start intent button's location
 * (mapped from the fixture's natural pixels), and assert the resolved element
 * chip contains intent-btn-start with role=button — resolution against the
 * reconstructed DOM, not the live <video>.
 */
import { test, expect, type Page } from "@playwright/test";
import { fileURLToPath } from "url";
import path from "path";
import fs from "fs";
import http from "http";
import type { AddressInfo } from "net";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const projectRoot = path.resolve(__dirname, "../..");

// The recording's intrinsic viewport (the fixture's Meta width/height) — the
// replay iframe's own pixel space, which the picker maps clicks into.
const REC_W = 1280;
const REC_H = 720;
// The Start intent button's center in those natural pixels (measured from the
// fixture's reconstructed DOM — bbox {x:20, y:481, w:177, h:47}).
const START_CENTER = { x: 108, y: 504 };

function startStaticServer(html: string): Promise<{ origin: string; close: () => void }> {
  return new Promise((resolve) => {
    const server = http.createServer((_req, res) => {
      res.setHeader("Content-Type", "text/html");
      res.end(html);
    });
    server.listen(0, "127.0.0.1", () => {
      const { port } = server.address() as AddressInfo;
      resolve({ origin: `http://127.0.0.1:${port}`, close: () => server.close() });
    });
  });
}

const SID = "sess-spatial";
const VIDEO = "demo_video#ab12cd34";

const CHAPTERS = [
  {
    index: 0,
    id: "intro",
    label: "Intro",
    start_ms: 0,
    end_ms: 10000,
    source_ref: { kind: "slidey", spec_path: "deck.json", scene_id: "intro" },
  },
];

interface OffpathParams {
  session_id: string;
  input: string;
  visual?: {
    frame_handle?: string;
    point?: { x: number; y: number };
    element?: { selector: string; role: string; text: string; bbox: number[] };
  };
}

async function setup(page: Page): Promise<{
  offpathCalls: OffpathParams[];
  close: () => void;
}> {
  const distIndex = path.join(projectRoot, "dist", "index.html");
  if (!fs.existsSync(distIndex)) {
    throw new Error(`dist/index.html not found — run the build (globalSetup) first`);
  }
  const html = fs.readFileSync(distIndex, "utf-8");

  // The checked-in deterministic rrweb fixture (Meta + FullSnapshot @ 1280×720).
  const fixturePath = path.join(
    projectRoot,
    "tests",
    "fixtures",
    "spatial-replay.rrweb.json"
  );
  const events = JSON.parse(fs.readFileSync(fixturePath, "utf-8")) as unknown[];

  const { origin, close } = await startStaticServer(html);
  const offpathCalls: OffpathParams[] = [];

  await page.route("**/rpc", async (route) => {
    const body = route.request().postDataJSON() as {
      method: string;
      params: Record<string, unknown>;
    };
    let result: unknown = {};
    switch (body.method) {
      case "runstatus.video.chapters":
        result = { chapters: CHAPTERS };
        break;
      case "runstatus.video.events":
        result = { events, width: REC_W, height: REC_H };
        break;
      case "runstatus.video.frame":
        result = { handle: "frame#deadbeef", mime: "image/png", kind: "image" };
        break;
      case "runstatus.session.offpath":
        offpathCalls.push(body.params as unknown as OffpathParams);
        result = { answer: "That's the Start intent button." };
        break;
      default:
        result = {};
    }
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({ jsonrpc: "2.0", id: 1, result }),
    });
  });

  const url = `${origin}/#/review/${SID}?video=${encodeURIComponent(VIDEO)}`;
  await page.goto(url);

  return { offpathCalls, close };
}

test.describe("spatial resolve against the rrweb-reconstructed DOM", () => {
  test("click the Start intent button in the replay → resolves a real app control", async ({
    page,
  }) => {
    const { offpathCalls, close } = await setup(page);
    await expect(page.getByTestId("review-page")).toBeVisible({ timeout: 10000 });

    // Flag the intro scene → selects the flag, which mounts ReplayFrame.
    await page.getByTestId("ct-marker-intro").click();
    await page.getByTestId("ct-flag-btn").click();
    await expect(page.getByTestId("flag-detail")).toBeVisible();

    // The replay frame (rrweb Replayer + picker) renders REAL UI, not <video>.
    await expect(page.getByTestId("rp-replay-frame")).toBeVisible();
    // The live-DOM video path must NOT be present in replay mode.
    await expect(page.getByTestId("rp-player")).toHaveCount(0);

    // The picker mounts once the Replayer has built its iframe.
    const picker = page.getByTestId("spatial-picker");
    await expect(picker).toBeVisible({ timeout: 10000 });

    // Click over the Start button's location. position is relative to the picker's
    // box, which covers the rendered (scaled) replay exactly — so the fraction of
    // the natural pixels equals the fraction of the picker box.
    const boxRect = await picker.boundingBox();
    if (!boxRect) throw new Error("picker has no bounding box");
    await picker.click({
      position: {
        x: (START_CENTER.x / REC_W) * boxRect.width,
        y: (START_CENTER.y / REC_H) * boxRect.height,
      },
    });

    // The crosshair pins, and the resolved element chip names the REAL control.
    await expect(page.getByTestId("sp-point")).toBeVisible();
    const chip = page.getByTestId("fd-element");
    await expect(chip).toBeVisible();
    // Resolution against the reconstructed DOM: the testid'd Start intent button,
    // role=button — NOT the rp-player video (proving the replay-iframe root).
    await expect(chip).toContainText("intent-btn-start");
    await expect(chip).toContainText("button");

    // The bundle rides the existing off-path oracle unchanged.
    await page.getByTestId("fd-chat-box").fill("what is this control?");
    await page.getByTestId("fd-chat-send").click();
    await expect.poll(() => offpathCalls.length).toBe(1);
    const params = offpathCalls[0];
    expect(params.visual).toBeTruthy();
    expect(params.visual!.element!.selector).toBe(
      '[data-testid="intent-btn-start"]'
    );
    expect(params.visual!.element!.role).toBe("button");
    expect(Array.isArray(params.visual!.element!.bbox)).toBe(true);
    expect(params.visual!.element!.bbox).toHaveLength(4);

    close();
  });
});
