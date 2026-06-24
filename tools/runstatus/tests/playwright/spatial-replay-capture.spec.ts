/**
 * spatial-replay-capture.spec.ts — ONE-SHOT capture of the content-rich rrweb
 * fixture that backs the spatial-oracle demo + the picker-resolution unit test.
 *
 * This is a GENERATOR, not a kept assertion. It drives the real `kitsoki web`
 * server (bugfix story under the happy_llm flow + demo cassette, the same
 * deterministic no-LLM posture as agent-actions-rrweb-capture.spec.ts), opens a
 * new session in the INTERACTIVE CHAT, streams one autofix turn so the chat
 * transcript + trace panels populate, then records the rrweb DOM via the local
 * bundled rrweb (installCapture → dumpCapture). It trims the stream to Meta +
 * the single rich FullSnapshot and writes it to
 * tests/fixtures/spatial-replay.rrweb.json (REPLACING the old minimal fragment).
 *
 * The interactive chat is content-rich: a header with current-state + state
 * badge, the harness picker, a populated <ChatTranscript> (user/assistant rows),
 * the trace diagram + timeline panels, and the InputBar intent buttons — a
 * POPULATED body, not an empty middle. It also exposes stable real controls the
 * spatial picker can resolve (intent-btn-*, chat-row-*, current-state).
 *
 * Determinism: the fixture is a CHECKED-IN static JSON; once captured it never
 * re-captures, so the demo/test replay it deterministically. This spec is run by
 * hand when the fixture needs refreshing, never in CI.
 *
 * Capture viewport is 1280×720 (REC_W/REC_H in the consuming specs) so the
 * replay's natural pixel space is unchanged.
 *
 * Run by hand to regenerate the fixture:
 *   pnpm exec playwright test spatial-replay-capture --project=chromium
 */
import { test, expect, chromium, type Browser, type BrowserContext, type Page } from "@playwright/test";
import path from "path";
import fs from "fs";
import {
  startWebServer,
  repoRoot,
  cinematicGoto,
  dwell,
  SETTLE_MS,
  type WebServer,
} from "./_helpers/server.js";
import { installCapture, dumpCapture } from "./_helpers/rrweb-replay.js";

// Distinct port from the golden (7748) + the agent-actions capture (7749).
const ADDR = "127.0.0.1:7751";
const STORY_DIR = path.join(repoRoot, "stories", "bugfix");
const FLOW = path.join(STORY_DIR, "flows", "happy_llm.yaml");
const HOST_CASSETTE = path.join(STORY_DIR, "flows", "demo.cassette.yaml");

const FIXTURE = path.join(
  repoRoot,
  "tools",
  "runstatus",
  "tests",
  "fixtures",
  "spatial-replay.rrweb.json",
);
const DIAG = path.join(repoRoot, ".artifacts", "spatial-replay-capture", "diagnostic.log");

// MUST stay 1280×720 — the consuming specs' REC_W/REC_H and the fixture's
// natural pixel space depend on it.
const VIEWPORT = { width: 1280, height: 720 } as const;

// This regenerator OVERWRITES the committed fixture, so it is SKIPPED in normal
// runs (a full `playwright test` must never clobber spatial-replay.rrweb.json).
// Set SPATIAL_FIXTURE_REGEN=1 to regenerate.
const REGEN = process.env.SPATIAL_FIXTURE_REGEN === "1";

let server: WebServer | undefined;

function diag(msg: string): void {
  const line = `[${new Date().toISOString()}] ${msg}\n`;
  try {
    fs.mkdirSync(path.dirname(DIAG), { recursive: true });
    fs.appendFileSync(DIAG, line);
  } catch {
    /* best-effort */
  }
}

test.beforeAll(async () => {
  if (!REGEN) return; // do not spawn a server (or clobber the fixture) in normal runs
  fs.mkdirSync(path.dirname(DIAG), { recursive: true });
  fs.writeFileSync(DIAG, "");
  server = await startWebServer({ addr: ADDR, flow: FLOW, hostCassette: HOST_CASSETTE, storiesDir: STORY_DIR });
});

test.afterAll(() => server?.stop());

test("capture the content-rich spatial-replay rrweb fixture", async () => {
  test.skip(!REGEN, "one-shot fixture regenerator — set SPATIAL_FIXTURE_REGEN=1 (it overwrites the committed fixture)");
  test.setTimeout(300000);
  const browser: Browser = await chromium.launch({ headless: true });
  const context: BrowserContext = await browser.newContext({
    viewport: { ...VIEWPORT },
    deviceScaleFactor: 1,
  });
  const page: Page = await context.newPage();

  try {
    // ── 1. Home → new session → interactive chat ────────────────────────────
    await cinematicGoto(page, `${server.base}/#/`, { waitForTestId: "home-view" });

    // Open a new session for the bugfix story. The home view's first story card
    // is the bugfix pipeline (only story under STORY_DIR); its "New session"
    // opens the interactive chat.
    await page.getByTestId("new-session-btn").first().click();
    await page.waitForURL(/#\/s\/[0-9a-f-]{36}\/chat$/, { timeout: 15000 });
    const sessionId = page.url().match(/\/s\/([0-9a-f-]{36})\/chat$/)?.[1] ?? "";
    diag(`session ${sessionId}`);
    await expect(page.getByTestId("chat-section")).toBeVisible({ timeout: 15000 });

    // ── 2. Stream one turn so the transcript + panels populate ──────────────
    // Submit `start` so the autofix turn streams its thinking/tool transcript
    // into the chat and the trace panels fill — a POPULATED body.
    await server.rpc("runstatus.session.submit", {
      session_id: sessionId,
      intent: "start",
      slots: {},
    });
    // Wait for an AGENT row to land in the transcript (the streamed turn) and
    // the ACTIONS intent buttons to render (the picker target lives here).
    await expect(
      page.getByTestId("chat-transcript"),
    ).toBeVisible({ timeout: 15000 });
    await expect
      .poll(
        async () => page.getByTestId("chat-row-agent").count(),
        { timeout: 30000 },
      )
      .toBeGreaterThan(0);
    await expect(page.getByTestId("intent-btn-start").first()).toBeVisible({ timeout: 15000 });
    // Let the stream finish + the trace panels settle BEFORE we record, so the
    // single rrweb FullSnapshot captures the fully-populated chat DOM.
    await dwell(page, SETTLE_MS * 3);

    // Start rrweb capture NOW, on the fully-rendered rich chat. rrweb emits ONE
    // FullSnapshot at record-start, so installing here (not at home) makes that
    // snapshot the rich chat DOM — Meta + that snapshot is the whole fixture, no
    // incremental mutations needed.
    await installCapture(page);
    diag("rrweb capture installed on the rich chat");
    // A tick for the FullSnapshot to be emitted into the buffer.
    await dwell(page, SETTLE_MS);

    // Report the intent buttons present so we can pick a stable picker target.
    const intentBtns = await page.evaluate(() =>
      Array.from(document.querySelectorAll('[data-testid^="intent-btn-"]')).map((el) => ({
        testid: el.getAttribute("data-testid"),
        text: (el.textContent ?? "").trim().slice(0, 40),
        rect: (() => {
          const r = el.getBoundingClientRect();
          return { x: Math.round(r.x), y: Math.round(r.y), w: Math.round(r.width), h: Math.round(r.height) };
        })(),
      })),
    );
    diag(`intent buttons: ${JSON.stringify(intentBtns, null, 2)}`);
    const chatRows = await page.evaluate(() =>
      document.querySelectorAll('[data-testid^="chat-row-"]').length,
    );
    diag(`chat rows: ${chatRows}`);

    // ── 3. Dump + trim to Meta + the rich FullSnapshot ──────────────────────
    const { events, viewport } = await dumpCapture(page);
    diag(`captured ${events.length} events @ ${viewport.width}x${viewport.height} dsf=${viewport.deviceScaleFactor}`);

    // Find the LAST FullSnapshot (type 2) — the richest reconstructed DOM (after
    // the turn streamed) — and the Meta (type 4) that precedes it.
    let lastFull = -1;
    for (let i = events.length - 1; i >= 0; i--) {
      if ((events[i] as { type?: number }).type === 2) {
        lastFull = i;
        break;
      }
    }
    expect(lastFull, "no FullSnapshot in the stream").toBeGreaterThanOrEqual(0);
    // The Meta event immediately preceding (rrweb emits Meta then FullSnapshot).
    let meta = -1;
    for (let i = lastFull - 1; i >= 0; i--) {
      if ((events[i] as { type?: number }).type === 4) {
        meta = i;
        break;
      }
    }
    // Fall back to the first Meta if none precedes (shouldn't happen).
    if (meta < 0) {
      meta = events.findIndex((e) => (e as { type?: number }).type === 4);
    }
    expect(meta, "no Meta event in the stream").toBeGreaterThanOrEqual(0);

    const metaEvt = events[meta] as { data?: { width?: number; height?: number } };
    diag(`Meta @ ${metaEvt.data?.width}x${metaEvt.data?.height}; using events[${meta}] (Meta) + events[${lastFull}] (FullSnapshot)`);
    expect(metaEvt.data?.width).toBe(VIEWPORT.width);
    expect(metaEvt.data?.height).toBe(VIEWPORT.height);

    const trimmed = [events[meta], events[lastFull]];
    fs.writeFileSync(FIXTURE, JSON.stringify(trimmed, null, 0) + "\n");
    const sizeKb = fs.statSync(FIXTURE).size / 1024;
    diag(`wrote ${FIXTURE} (${sizeKb.toFixed(1)} KB)`);
    expect(sizeKb, "fixture should be a sensible size").toBeLessThan(300);
  } catch (e) {
    diag(`FAILED: ${e instanceof Error ? e.stack ?? e.message : String(e)}`);
    diag(`--- server log ---\n${server?.log?.() ?? ""}`);
    throw e;
  } finally {
    await context.close();
    await browser.close();
  }
});
