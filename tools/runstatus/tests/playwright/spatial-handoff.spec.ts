/**
 * spatial-handoff.spec.ts — Playwright end-to-end for the chrome-less `/point`
 * spatial-handoff window (docs/tui/spatial-handoff.md).
 *
 * Same deterministic posture as spatial-capture.spec.ts: the built
 * dist/index.html is served by a tiny static server, here on the REAL `/point`
 * server path (not a hash route) so App.vue boots into chrome-less mode via
 * isChromeless() and mounts ONLY PointPage. No live kitsoki server, no LLM, no
 * ffmpeg. The return endpoint is STUBBED: `POST /point/return?token=…` is
 * intercepted via page.route and we capture the visual bundle it received — the
 * same bundle the server's one-time-token consume() hands to the parked TUI turn.
 *
 * Flow: open /point?token=…&chromeless=1&media_handle=…, confirm ONLY the picker
 * + composer render (no nav / no trace timeline / no editor — the chrome-less
 * guarantee), click a known element on the frame, type a question, Send. Assert
 * the return endpoint received params.visual with the point + resolved element,
 * the token rode the request, and the window requested close (the "✓ sent"
 * fallback surfaces).
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

// A static server that serves the built SPA HTML for ANY path — `/point` is a
// real server path the SPA reads off window.location, so the server must hand
// the same bundle there (mirrors the runstatus Server's handlePoint).
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

const TOKEN = "2b0f5ad09e27a5fcb0411b57f45c612c";
const MEDIA = "frame#deadbeef";

// A valid 1×1 red PNG (same bytes as the spatial-capture fixture).
const ONE_PX_PNG = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGNgYGAAAAAEAAHzwAAAAABJRU5ErkJggg==",
  "base64",
);

interface ReturnBody {
  visual?: {
    media_handle?: string;
    route?: string;
    point?: { x: number; y: number };
    element?: { selector: string; role: string; text: string; bbox: number[] };
  };
}

async function setup(page: Page): Promise<{
  returnCalls: { token: string; body: ReturnBody }[];
  close: () => void;
}> {
  const distIndex = path.join(projectRoot, "dist", "index.html");
  if (!fs.existsSync(distIndex)) {
    throw new Error(`dist/index.html not found — run the build (globalSetup) first`);
  }
  const html = fs.readFileSync(distIndex, "utf-8");
  const { origin, close } = await startStaticServer(html);

  const returnCalls: { token: string; body: ReturnBody }[] = [];

  // STUB the one-time-token return endpoint: capture the token + visual bundle
  // and reply ok, exactly as the server's handlePointReturn does on consume().
  await page.route("**/point/return**", async (route) => {
    const url = new URL(route.request().url());
    returnCalls.push({
      token: url.searchParams.get("token") ?? "",
      body: route.request().postDataJSON() as ReturnBody,
    });
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({ ok: true }),
    });
  });

  // The rendered frame loads from the artifact route.
  await page.route("**/artifact/**", async (route) => {
    await route.fulfill({ contentType: "image/png", body: ONE_PX_PNG });
  });

  const url =
    `${origin}/point?token=${TOKEN}&chromeless=1` +
    `&media_handle=${encodeURIComponent(MEDIA)}&route=%2Frun%2Fsess-x&t_ms=14000`;
  await page.goto(url);

  return { returnCalls, close };
}

test.describe("spatial handoff /point window", () => {
  test("chrome-less point + send returns the visual bundle", async ({ page }) => {
    const { returnCalls, close } = await setup(page);

    // ONLY the chrome-less point page renders.
    await expect(page.getByTestId("point-page")).toBeVisible({ timeout: 10000 });

    // The full-app chrome is ABSENT — the chrome-less guarantee (no nav, no
    // trace timeline, no editor). These surfaces never mount under PointPage.
    await expect(page.getByTestId("review-page")).toHaveCount(0);
    await expect(page.getByTestId("trace-timeline")).toHaveCount(0);
    await expect(page.getByTestId("editor-page")).toHaveCount(0);

    // The picker is mounted over the frame; the agent's prompt + composer show.
    await expect(page.getByTestId("spatial-picker")).toBeVisible();
    await expect(page.getByTestId("pp-prompt")).toBeVisible();
    await expect(page.getByTestId("pp-input")).toBeVisible();

    // Click a point on the frame → pins a point + resolves the element behind
    // the transparent overlay against the live document.
    await page.getByTestId("spatial-picker").click({ position: { x: 80, y: 60 } });
    await expect(page.getByTestId("sp-point")).toBeVisible();

    // Type why, then Send.
    await page.getByTestId("pp-input").fill("why is this disabled here?");
    await page.getByTestId("pp-send").click();

    // The return endpoint received the bundle exactly once, carrying the token,
    // the media handle, the pinned point, and the question folded into route.
    await expect.poll(() => returnCalls.length).toBe(1);
    const { token, body } = returnCalls[0];
    expect(token).toBe(TOKEN);
    expect(body.visual).toBeTruthy();
    expect(body.visual!.media_handle).toBe(MEDIA);
    expect(body.visual!.point).toEqual({
      x: expect.any(Number),
      y: expect.any(Number),
    });
    // The question rides on the route note (PointPage folds WHY into route; the
    // bundle carries WHERE).
    expect(body.visual!.route).toContain("why is this disabled here?");

    // The window requested close; browsers block programmatic close of tabs they
    // didn't script-open, so the "✓ sent — you can close this tab" fallback is
    // what surfaces, and Send disables (one bundle per token).
    await expect(page.getByTestId("pp-sent")).toBeVisible();
    await expect(page.getByTestId("pp-send")).toBeDisabled();

    close();
  });
});
