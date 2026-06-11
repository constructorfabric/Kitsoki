/**
 * review.spec.ts — Playwright end-to-end for the /review video-feedback surface
 * (proposal: docs/proposals/video-feedback-mode.md, slice 2).
 *
 * The SPA is loaded from the built dist/index.html WITHOUT an inlined snapshot,
 * so createDataSource() returns LiveSource and the page issues real JSON-RPC
 * calls. We intercept those calls with page.route — deterministic, no live
 * server, no LLM, no ffmpeg:
 *
 *   - runstatus.video.chapters → a fixture chapter list
 *   - runstatus.video.frame    → a fixture still handle (the panel's eager grab)
 *   - runstatus.feedback.add    → asserted: fired with the resolved source_ref
 *   - GET /artifact/*           → a 1×1 PNG (the player + still media elements)
 *
 * Flow: navigate to #/review/<sid>?video=<handle>, flag a chapter, type an
 * instruction, click "Send to refine", assert the dispatched note carries the
 * resolved source_ref + frame handle.
 *
 * Route-less guard: the route is registered in src/router.ts. Before the change
 * this spec fails because #/review/* falls through to no component and the
 * [data-testid='review-page'] shell never mounts.
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

/**
 * Serve dist/index.html over http (not file://) so the SPA boots in LiveSource
 * mode and its relative fetch('/rpc') resolves to a real origin page.route can
 * intercept. The server only serves index.html for any path; the RPC + artifact
 * routes are stubbed via page.route, never reaching this server.
 */
function startStaticServer(html: string): Promise<{ origin: string; close: () => void }> {
  return new Promise((resolve) => {
    const server = http.createServer((_req, res) => {
      res.setHeader("Content-Type", "text/html");
      res.end(html);
    });
    server.listen(0, "127.0.0.1", () => {
      const { port } = server.address() as AddressInfo;
      resolve({
        origin: `http://127.0.0.1:${port}`,
        close: () => server.close(),
      });
    });
  });
}

const SID = "sess-review";
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
    end_ms: 4000,
    source_ref: { kind: "slidey", spec_path: "deck.json", scene_id: "intro" },
  },
  {
    index: 1,
    id: "run_view",
    label: "Run view",
    start_ms: 4000,
    end_ms: 10000,
    source_ref: {
      kind: "slidey",
      spec_path: "deck.json",
      scene_id: "run_view",
      line: 42,
    },
  },
];

/**
 * Serve the built SPA with NO snapshot so it boots in LiveSource mode, and
 * stub every RPC / artifact request. Returns the captured feedback.add params.
 */
async function setupReviewPage(page: Page): Promise<{
  feedbackCalls: Record<string, unknown>[];
  frameCalls: Record<string, unknown>[];
  close: () => void;
}> {
  const distIndex = path.join(projectRoot, "dist", "index.html");
  if (!fs.existsSync(distIndex)) {
    throw new Error(`dist/index.html not found — run the build (globalSetup) first`);
  }
  const html = fs.readFileSync(distIndex, "utf-8");
  const { origin, close } = await startStaticServer(html);

  const feedbackCalls: Record<string, unknown>[] = [];
  const frameCalls: Record<string, unknown>[] = [];

  // Stub the JSON-RPC surface.
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
        frameCalls.push(body.params);
        result = { handle: "frame#deadbeef", mime: "image/png", kind: "image" };
        break;
      case "runstatus.feedback.add":
        feedbackCalls.push(body.params);
        result = { ok: true };
        break;
      default:
        result = {};
    }
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({ jsonrpc: "2.0", id: body && 1, result }),
    });
  });

  // Stub artifact serving (the video player + captured still media elements).
  await page.route("**/artifact/**", async (route) => {
    await route.fulfill({ contentType: "image/png", body: ONE_PX_PNG });
  });

  // Load the SPA over http with hash routing into /review.
  const url = `${origin}/#/review/${SID}?video=${encodeURIComponent(VIDEO)}`;
  await page.goto(url);

  return { feedbackCalls, frameCalls, close };
}

test.describe("/review feedback surface", () => {
  test("renders the two-column shell with player, timeline and flags", async ({ page }) => {
    const { close } = await setupReviewPage(page);

    await expect(page.getByTestId("review-page")).toBeVisible({ timeout: 10000 });
    await expect(page.getByTestId("rp-player")).toBeVisible();
    await expect(page.getByTestId("chapter-timeline")).toBeVisible();
    await expect(page.getByTestId("flag-list")).toBeVisible();

    // Chapters render one marker each.
    await expect(page.getByTestId("ct-marker-intro")).toBeVisible();
    await expect(page.getByTestId("ct-marker-run_view")).toBeVisible();
    close();
  });

  test("flag a scene, instruct, and dispatch a feedback note with the resolved source_ref", async ({ page }) => {
    const { feedbackCalls, frameCalls, close } = await setupReviewPage(page);
    await expect(page.getByTestId("review-page")).toBeVisible({ timeout: 10000 });

    // Seek to the "Run view" chapter (4000ms) → sets the selection.
    await page.getByTestId("ct-marker-run_view").click();
    // Flag the current selection (point flag at 4000ms).
    await page.getByTestId("ct-flag-btn").click();

    // The eager still grab fired against the right video + time.
    await expect.poll(() => frameCalls.length).toBeGreaterThan(0);
    expect(frameCalls[0].video).toBe(VIDEO);
    expect(frameCalls[0].t_ms).toBe(4000);

    // The flag is selected in the detail panel; the still resolves.
    await expect(page.getByTestId("flag-detail")).toBeVisible();
    await expect(page.getByTestId("fd-still").locator("img")).toBeVisible();

    // The resolved source_ref + deep-link show the dominant chapter.
    await expect(page.getByTestId("fd-source")).toContainText("run_view");
    await expect(page.getByTestId("fd-open")).toHaveAttribute(
      "href",
      "vscode://file/deck.json:42",
    );

    // Type an instruction and dispatch.
    await page.getByTestId("fd-instruction").fill("heading clips on mobile");
    await page.getByTestId("fd-send-refine").click();

    await expect.poll(() => feedbackCalls.length).toBe(1);
    const note = feedbackCalls[0];
    expect(note.video).toBe(VIDEO);
    expect(note.instruction).toBe("heading clips on mobile");
    expect(note.frame_handle).toBe("frame#deadbeef");
    expect(note.source_ref).toMatchObject({ kind: "slidey", scene_id: "run_view" });

    // The flag shows as sent.
    await expect(page.getByTestId("fd-sent-badge")).toBeVisible();
    close();
  });
});
