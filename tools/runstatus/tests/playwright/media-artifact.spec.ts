/**
 * media-artifact.spec.ts — Playwright end-to-end test + demo video for the
 * recorded-media ViewElement feature.
 *
 * What this tests
 * ───────────────
 * The recorded-media feature adds a "media" ViewElement kind to the kitsoki
 * web UI. When a story room emits a typed view containing media elements, the
 * SPA renders:
 *   • video/* MIME → <video controls> with src=/artifact/<handle>
 *   • image/* MIME → <img loading="lazy"> with src=/artifact/<handle>
 *
 * The Go server exposes a GET /artifact/<handle> route (added in
 * internal/runstatus/server/server.go). This test verifies the full round-trip:
 *   1. A typed view with media elements is served by the mock RPC layer.
 *   2. The SPA renders <video> and <img> in the chat surface.
 *   3. Each element's src points to /artifact/<handle>.
 *   4. The /artifact/ endpoint serves real binary content.
 *
 * Why the test would FAIL without /artifact/<handle>
 * ──────────────────────────────────────────────────
 * Without the /artifact/ route the browser would receive a 404 for each media
 * src. The explicit network-response assertions (artifactImageOk / artifactVideoOk)
 * catch it at the fetch level. To reproduce the failure:
 *   1. Comment out `handleArtifact` in server.go and rebuild.
 *   2. Run this spec — "artifact /demo-image → 200 with correct MIME" will fail
 *      with "Expected: 200, Received: 404".
 *
 * No kitsoki binary required
 * ──────────────────────────
 * This spec spawns a lean Node.js HTTP server that serves:
 *   • dist/ static assets (SPA bundle)
 *   • POST /rpc  — mocked JSON-RPC dispatcher
 *   • GET  /artifact/<handle> — serves binary fixtures from memory
 * The server starts on 127.0.0.1:7748 (no collision with existing specs).
 *
 * Demo video — 4 distinct scenes
 * ────────────────────────────────
 * Scene 01-home        — home screen (story browser, no active sessions yet)
 * Scene 02-session     — session started, chat view loading
 * Scene 03-media-view  — chat view with video player + image rendered inline
 * Scene 04-detail      — zoomed view of the image artifact caption
 *
 * The PNG artifact served by /artifact/demo-image is a real screenshot of the
 * kitsoki home view taken during scene 1 — the feature demonstrating itself.
 *
 * Run:
 *   pnpm playwright test tests/playwright/media-artifact.spec.ts --reporter=list
 */

import {
  test,
  expect,
  chromium,
  type Browser,
  type BrowserContext,
  type Page,
} from "@playwright/test";
import http from "http";
import path from "path";
import fs from "fs";
import { fileURLToPath } from "url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const projectRoot = path.resolve(__dirname, "../..");

import { repoRoot, makeShot } from "./_helpers/server.js";
import { cameraContext } from "./_helpers/camera.js";

// ── Artifact dirs ────────────────────────────────────────────────────────────

const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "visual-outputs-demo");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "video");

// ── Binary fixtures ───────────────────────────────────────────────────────────

/**
 * The smallest valid ftyp-box MP4 container that browsers accept enough of to
 * recognise as video/mp4 and render a <video> element (not playable, but the
 * browser won't reject the src URL).
 */
const MP4_STUB = Buffer.from(
  "AAAAFGZ0eXBtcDQyAAAAAAAAbXA0Mg==",
  "base64"
);

// The PNG artifact is set dynamically from a real Playwright screenshot of the
// home view (captured during scene 1). This makes the demo self-referential:
// kitsoki's web UI is itself displayed as a media artifact inline in the chat.
let demoPngBuffer: Buffer = Buffer.alloc(0);

// ── Mock session data ─────────────────────────────────────────────────────────

const SESSION_ID = "media-artifact-test-session";
const APP_ID = "media-demo";

const mockSession = {
  session_id: SESSION_ID,
  app_id: APP_ID,
  current_state: "show_media",
  turn: 1,
  started_at: new Date().toISOString(),
  terminal: false,
};

const mockApp = {
  id: APP_ID,
  name: "Media Demo",
  root: "show_media",
  states: { show_media: { description: "Shows media elements", States: {} } },
};

const mockMermaid = {
  source: 'flowchart LR\n  show_media["show_media"]',
  node_map: { show_media: { kind: "state", ref: "show_media" } },
};

/**
 * Typed view carrying all supported media kinds:
 *   video/mp4   → <video controls>
 *   image/png   → <img> (real home-screen screenshot captured in scene 1)
 *   application/pdf → <iframe>
 *   text/html   → sandboxed <iframe>
 *   application/octet-stream → labeled <a> fallback
 */
const mockTypedView = {
  Elements: [
    {
      Kind: "prose",
      Source: "Step complete. Visual artifacts are ready:",
    },
    {
      Kind: "media",
      Handle: "demo-video",
      Mime: "video/mp4",
      Caption: "Architecture walkthrough (MP4)",
    },
    {
      Kind: "media",
      Handle: "demo-image",
      Mime: "image/png",
      Caption: "Home view screenshot (PNG)",
    },
    {
      Kind: "media",
      Handle: "demo-pdf",
      Mime: "application/pdf",
      Caption: "Report summary (PDF)",
    },
    {
      Kind: "media",
      Handle: "demo-html",
      Mime: "text/html",
      Caption: "Interactive deck (HTML)",
    },
    {
      Kind: "media",
      Handle: "demo-binary",
      Mime: "application/octet-stream",
      Caption: "Raw data export (download)",
    },
  ],
};

const mockViewResult = {
  mode: "transitioned",
  state: "show_media",
  view: "Step complete. Visual artifacts are ready:\n[video: demo-video]\n[image: demo-image]\n[pdf: demo-pdf]\n[html: demo-html]\n[binary: demo-binary]",
  typed_view: mockTypedView,
  allowed_intents: ["done"],
  intents: [{ name: "done", title: "Done", has_slots: false }],
  turn_number: 1,
};

// ── Mutable server state: sessions appear after scene 1 ──────────────────────

let serveActiveSessions = false;

// ── Mock RPC dispatcher ───────────────────────────────────────────────────────

function handleRpc(body: {
  method: string;
  params: Record<string, unknown>;
  id: unknown;
}): unknown {
  const { method } = body;
  switch (method) {
    case "runstatus.stories.list":
      return [
        {
          path: "/mock/media-demo/app.yaml",
          app_id: APP_ID,
          title: "Media Demo",
          active_sessions: serveActiveSessions ? [SESSION_ID] : [],
        },
      ];
    case "runstatus.sessions.list":
      return serveActiveSessions ? [mockSession] : [];
    case "runstatus.session.get":
      return mockSession;
    case "runstatus.session.app":
      return mockApp;
    case "runstatus.session.mermaid":
      return mockMermaid;
    case "runstatus.session.trace":
      return {
        events: [
          {
            time: new Date().toISOString(),
            level: "info",
            msg: "turn.start",
            session_id: SESSION_ID,
            turn: 1,
            state_path: "show_media",
            attrs: { turn: 1 },
          },
        ],
        last_turn: 1,
      };
    case "runstatus.session.view":
      return mockViewResult;
    case "runstatus.session.new":
      return { session_id: SESSION_ID };
    case "runstatus.meta.modes":
      return { modes: [] };
    default:
      throw new Error(`method not found: ${method}`);
  }
}

// ── MIME types for static asset serving ──────────────────────────────────────

const MIME: Record<string, string> = {
  ".html": "text/html; charset=utf-8",
  ".js": "application/javascript",
  ".mjs": "application/javascript",
  ".css": "text/css",
  ".svg": "image/svg+xml",
  ".png": "image/png",
  ".ico": "image/x-icon",
  ".woff2": "font/woff2",
  ".woff": "font/woff",
  ".json": "application/json",
};

// ── Mock HTTP server ──────────────────────────────────────────────────────────

const MOCK_PORT = 7748;
const MOCK_BASE = `http://127.0.0.1:${MOCK_PORT}`;
const distDir = path.join(projectRoot, "dist");

interface MockServer {
  base: string;
  stop(): void;
}

async function startMockServer(): Promise<MockServer> {
  const server = http.createServer((req, res) => {
    const url = new URL(req.url ?? "/", `http://localhost`);
    const pathname = url.pathname;

    // ── /artifact/<handle> ──────────────────────────────────────────────────
    if (pathname.startsWith("/artifact/")) {
      const handle = pathname.slice("/artifact/".length);
      if (handle === "demo-video") {
        res.writeHead(200, {
          "Content-Type": "video/mp4",
          "Content-Length": String(MP4_STUB.length),
          "Accept-Ranges": "bytes",
        });
        res.end(MP4_STUB);
        return;
      }
      if (handle === "demo-image") {
        if (demoPngBuffer.length === 0) {
          res.writeHead(503, { "Content-Type": "text/plain" });
          res.end("demo image not captured yet");
          return;
        }
        res.writeHead(200, {
          "Content-Type": "image/png",
          "Content-Length": String(demoPngBuffer.length),
        });
        res.end(demoPngBuffer);
        return;
      }
      if (handle === "demo-pdf") {
        // Minimal valid PDF so the browser iframe can load it.
        const pdf = Buffer.from(
          "%PDF-1.0\n1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj " +
          "2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj " +
          "3 0 obj<</Type/Page/MediaBox[0 0 612 792]/Parent 2 0 R>>endobj\n" +
          "xref\n0 4\n0000000000 65535 f\n0000000009 00000 n\n" +
          "0000000058 00000 n\n0000000115 00000 n\n" +
          "trailer<</Size 4/Root 1 0 R>>\nstartxref\n190\n%%EOF"
        );
        res.writeHead(200, { "Content-Type": "application/pdf", "Content-Length": String(pdf.length) });
        res.end(pdf);
        return;
      }
      if (handle === "demo-html") {
        const html = Buffer.from(
          "<!doctype html><html><body style='font-family:sans-serif;padding:2em;background:#1a1a2e;color:#e0e0ff'>" +
          "<h2>Interactive Deck</h2><p>Slide 1 of 3 — Architecture overview</p></body></html>"
        );
        res.writeHead(200, { "Content-Type": "text/html", "Content-Length": String(html.length) });
        res.end(html);
        return;
      }
      if (handle === "demo-binary") {
        const bin = Buffer.from("BINARY_EXPORT_V1\x00\x01\x02\x03");
        res.writeHead(200, { "Content-Type": "application/octet-stream", "Content-Length": String(bin.length) });
        res.end(bin);
        return;
      }
      res.writeHead(404, { "Content-Type": "text/plain" });
      res.end(`artifact not found: ${handle}`);
      return;
    }

    // ── POST /rpc ───────────────────────────────────────────────────────────
    if (pathname === "/rpc" && req.method === "POST") {
      let raw = "";
      req.on("data", (chunk: Buffer) => (raw += chunk.toString()));
      req.on("end", () => {
        try {
          const body = JSON.parse(raw) as {
            method: string;
            params: Record<string, unknown>;
            id: unknown;
          };
          const result = handleRpc(body);
          const envelope = { jsonrpc: "2.0", id: body.id, result };
          const json = JSON.stringify(envelope);
          res.writeHead(200, {
            "Content-Type": "application/json",
            "Content-Length": String(Buffer.byteLength(json)),
          });
          res.end(json);
        } catch (err) {
          const errEnvelope = {
            jsonrpc: "2.0",
            id: null,
            error: {
              code: -32601,
              message: err instanceof Error ? err.message : String(err),
            },
          };
          const json = JSON.stringify(errEnvelope);
          res.writeHead(200, {
            "Content-Type": "application/json",
            "Content-Length": String(Buffer.byteLength(json)),
          });
          res.end(json);
        }
      });
      return;
    }

    // ── GET /rpc/events — SSE keep-alive ────────────────────────────────────
    if (pathname === "/rpc/events") {
      res.writeHead(200, {
        "Content-Type": "text/event-stream",
        "Cache-Control": "no-cache",
        Connection: "keep-alive",
      });
      res.write(": keepalive\n\n");
      req.on("close", () => res.end());
      return;
    }

    // ── Static assets from dist/ ─────────────────────────────────────────────
    let filePath = path.join(distDir, pathname === "/" ? "index.html" : pathname);
    if (!filePath.startsWith(distDir)) {
      res.writeHead(400);
      res.end("bad path");
      return;
    }
    if (!fs.existsSync(filePath)) {
      filePath = path.join(distDir, "index.html");
    }
    const ext = path.extname(filePath);
    const mime = MIME[ext] ?? "application/octet-stream";
    try {
      const data = fs.readFileSync(filePath);
      res.writeHead(200, {
        "Content-Type": mime,
        "Content-Length": String(data.length),
      });
      res.end(data);
    } catch {
      res.writeHead(404, { "Content-Type": "text/plain" });
      res.end("not found");
    }
  });

  await new Promise<void>((resolve, reject) => {
    server.on("error", reject);
    server.listen(MOCK_PORT, "127.0.0.1", resolve);
  });

  return {
    base: MOCK_BASE,
    stop(): void { server.close(); },
  };
}

// ── Test suite ────────────────────────────────────────────────────────────────

let mockServer: MockServer;

test.beforeAll(async () => {
  fs.mkdirSync(VIDEO_DIR, { recursive: true });
  mockServer = await startMockServer();
});

test.afterAll(() => mockServer?.stop());

const DWELL = 2500;
const SETTLE = 800;

const shot = makeShot(ARTIFACT_DIR);

test("media-artifact — video and image elements render with /artifact/ src", async () => {
  test.setTimeout(120_000);

  const browser: Browser = await chromium.launch({ headless: true });
  const context: BrowserContext = await browser.newContext(
    cameraContext({ recordVideoDir: VIDEO_DIR }),
  );
  const page: Page = await context.newPage();
  const BASE = mockServer.base;

  const artifactResponses: Array<{ url: string; status: number; mime: string }> = [];
  page.on("response", (resp) => {
    if (resp.url().includes("/artifact/")) {
      artifactResponses.push({
        url: resp.url(),
        status: resp.status(),
        mime: resp.headers()["content-type"] ?? "",
      });
    }
  });

  try {
    // ── Scene 1: Home screen (no active sessions) ───────────────────────────
    // With serveActiveSessions=false the SPA renders the home view without
    // auto-redirecting (no active session to jump to).
    serveActiveSessions = false;
    await page.goto(`${BASE}/#/`);
    await expect(page.getByTestId("home-view")).toBeVisible({ timeout: 15_000 });
    await page.waitForTimeout(DWELL);

    // Capture home screenshot → this becomes the PNG served as /artifact/demo-image.
    // The demo is self-referential: the kitsoki UI is itself shown as a media artifact.
    demoPngBuffer = await page.screenshot();
    await shot(page, "home");

    // ── Scene 2: Activate session + navigate to chat ────────────────────────
    serveActiveSessions = true;
    await page.goto(`${BASE}/#/s/${SESSION_ID}/chat`);
    await expect(page.locator(".iv__topbar")).toBeVisible({ timeout: 15_000 });
    await page.waitForTimeout(SETTLE);
    await shot(page, "session");

    // ── Scene 3: Media elements render ─────────────────────────────────────
    // The mock session.view returns a typed_view with video + image elements.
    await expect(page.locator("[data-testid='chat-row-agent']").first()).toBeVisible({
      timeout: 15_000,
    });
    await page.waitForTimeout(SETTLE);

    // Assert <video> present and pointing at /artifact/
    const video = page.locator("video.ve-media-video").first();
    await expect(video).toBeVisible({ timeout: 8_000 });
    const videoSrc = await video.getAttribute("src");
    expect(videoSrc).toMatch(/\/artifact\//);
    await expect(video).toHaveAttribute("controls", /.*/);

    // Assert <img> present and pointing at /artifact/
    const img = page.locator("img.ve-media-image").first();
    await expect(img).toBeVisible({ timeout: 8_000 });
    const imgSrc = await img.getAttribute("src");
    expect(imgSrc).toMatch(/\/artifact\//);
    await expect(img).toHaveAttribute("loading", "lazy");

    // Wait for the image to fully load (it's a real PNG of the home view)
    await page.waitForTimeout(SETTLE);
    await shot(page, "media-view");

    // ── Assert pdf iframe ───────────────────────────────────────────────────
    const pdfFrame = page.locator("iframe.ve-media-iframe").first();
    await expect(pdfFrame).toBeVisible({ timeout: 8_000 });

    // ── Assert html sandboxed iframe ────────────────────────────────────────
    const htmlFrame = page.locator("iframe[sandbox].ve-media-iframe").first();
    await expect(htmlFrame).toBeVisible({ timeout: 8_000 });

    // ── Assert unknown-MIME fallback link ───────────────────────────────────
    const fallbackLink = page.locator("a.ve-media-link").first();
    await expect(fallbackLink).toBeVisible({ timeout: 8_000 });

    // ── Scene 4: Captions + final shot ──────────────────────────────────────
    const captions = page.locator("p.ve-media-caption");
    await expect(captions.first()).toBeVisible({ timeout: 5_000 });
    const captionTexts = await captions.allTextContents();
    expect(captionTexts).toContain("Architecture walkthrough (MP4)");
    expect(captionTexts).toContain("Home view screenshot (PNG)");
    expect(captionTexts).toContain("Report summary (PDF)");
    expect(captionTexts).toContain("Interactive deck (HTML)");
    expect(captionTexts).toContain("Raw data export (download)");

    await page.waitForTimeout(DWELL);
    await shot(page, "detail");

    // ── Assert /artifact/ responses returned 200 with correct MIME ──────────
    await page.waitForTimeout(SETTLE);

    // demo-binary is a <a> link — browser only fetches on click, not on render.
    const checks: Array<[string, RegExp]> = [
      ["demo-video", /video\/mp4/],
      ["demo-image", /image\/png/],
      ["demo-pdf",   /application\/pdf/],
      ["demo-html",  /text\/html/],
    ];
    for (const [handle, mimeRe] of checks) {
      const r = artifactResponses.find((x) => x.url.endsWith(`/${handle}`));
      expect(r, `No /artifact/${handle} response seen`).toBeDefined();
      if (r) {
        expect(r.status, `${handle} → 200`).toBe(200);
        expect(r.mime, `${handle} MIME`).toMatch(mimeRe);
      }
    }

  } finally {
    await context.close();
    await browser.close();
  }
});

test("media-artifact — /artifact/missing → 404 for unknown handles", async () => {
  const resp = await fetch(`${MOCK_BASE}/artifact/does-not-exist`);
  expect(resp.status).toBe(404);
});
