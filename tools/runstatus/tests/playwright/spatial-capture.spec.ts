/**
 * spatial-capture.spec.ts — Playwright end-to-end for the spatial picker on the
 * /review surface (docs/tui/spatial-capture.md).
 *
 * Same deterministic posture as review.spec.ts: the built dist/index.html is
 * served WITHOUT an inlined snapshot, so createDataSource() returns LiveSource
 * and the page issues real JSON-RPC calls — which we intercept with page.route.
 * No live kitsoki server, no LLM, no ffmpeg. The oracle is STUBBED:
 * runstatus.session.offpath returns a canned answer and we capture its params.
 *
 * Flow: open /review, flag a scene (selects the flag → mounts SpatialPicker over
 * the player), click a point on the frame, type a question, click Ask. Assert
 * runstatus.session.offpath fired with params.visual = {frame_handle, point,
 * element}: the bundle slice 1 lifts into host.WithVisualAmbient server-side.
 *
 * The picker resolves the element BEHIND the transparent overlay against the
 * live document (it drops pointer-events for the hit-test); behind the overlay
 * is the player, so the resolved selector is the player's data-testid.
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

// A valid 1×1 red PNG (same bytes as the Go test fixture).
const ONE_PX_PNG = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGNgYGAAAAAEAAHzwAAAAABJRU5ErkJggg==",
  "base64"
);

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
      case "runstatus.video.frame":
        result = { handle: "frame#deadbeef", mime: "image/png", kind: "image" };
        break;
      case "runstatus.session.offpath":
        offpathCalls.push(body.params as unknown as OffpathParams);
        result = { answer: "That control is the player." };
        break;
      default:
        result = {};
    }
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({ jsonrpc: "2.0", id: 1, result }),
    });
  });

  await page.route("**/artifact/**", async (route) => {
    await route.fulfill({ contentType: "image/png", body: ONE_PX_PNG });
  });

  const url = `${origin}/#/review/${SID}?video=${encodeURIComponent(VIDEO)}`;
  await page.goto(url);

  return { offpathCalls, close };
}

test.describe("spatial capture on /review", () => {
  test("click the frame, ask, and offpath carries the visual bundle", async ({ page }) => {
    const { offpathCalls, close } = await setup(page);
    await expect(page.getByTestId("review-page")).toBeVisible({ timeout: 10000 });

    // Flag the intro scene → selects the flag, which mounts the picker.
    await page.getByTestId("ct-marker-intro").click();
    await page.getByTestId("ct-flag-btn").click();
    await expect(page.getByTestId("flag-detail")).toBeVisible();

    // The picker is mounted over the player frame.
    const picker = page.getByTestId("spatial-picker");
    await expect(picker).toBeVisible();

    // Click a point on the frame → pins a point + resolves the element behind
    // the overlay (the player). The crosshair confirms the pin.
    await picker.click({ position: { x: 80, y: 60 } });
    await expect(page.getByTestId("sp-point")).toBeVisible();

    // The resolved element renders as a "pointing at:" chip on the detail panel.
    const chip = page.getByTestId("fd-element");
    await expect(chip).toBeVisible();
    await expect(chip).toContainText("rp-player");

    // Ask a question through the per-flag chat.
    await page.getByTestId("fd-chat-box").fill("what is this control?");
    await page.getByTestId("fd-chat-send").click();

    // offpath fired with the visual bundle.
    await expect.poll(() => offpathCalls.length).toBe(1);
    const params = offpathCalls[0];
    expect(params.input).toBe("what is this control?");
    expect(params.visual).toBeTruthy();
    expect(params.visual!.frame_handle).toBe("frame#deadbeef");
    expect(params.visual!.point).toEqual({
      x: expect.any(Number),
      y: expect.any(Number),
    });
    expect(params.visual!.element!.selector).toBe('[data-testid="rp-player"]');
    // bbox rides as the positional [x, y, w, h] array host.VisualAmbient expects.
    expect(Array.isArray(params.visual!.element!.bbox)).toBe(true);
    expect(params.visual!.element!.bbox).toHaveLength(4);

    close();
  });
});
