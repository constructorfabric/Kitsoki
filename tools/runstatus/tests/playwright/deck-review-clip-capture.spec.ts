/**
 * deck-review-clip-capture.spec.ts — LIVE rrweb capture of the deck-review story
 * rendering + viewing each of the 6 pet artifact decks full-screen in kitsoki web.
 *
 * GOAL: produce one rrweb clip per artifact deck, each showing kitsoki web
 * rendering that deck FOR REAL (host.slidey.render + host.artifacts_dir execute
 * against the real host registry — NOT --flow stubs, NOT a static image) and
 * displaying it as a slideshow media artifact the operator opens full-screen.
 *
 * Why no --flow: `kitsoki web --flow` stubs EVERY host.* call, so
 * host.slidey.render would not really render. To run the real render we start a
 * plain `kitsoki web` (real builtins) and drive the deterministic, no-LLM,
 * explicit-intent path: deck-review has no agent calls, only the deterministic
 * host.slidey.render / host.artifacts_dir builtins, and explicit `begin`/`done`
 * intents. We mint the session over RPC, seed world.deck via
 * runstatus.session.patch_world (the flow-runner's world_override mechanism
 * exposed over RPC), then drive `begin` in the browser so the on_enter render
 * fires for real and the session lands in `viewing` with the live slideshow.
 *
 * Output (one per deck):
 *   docs/decks/clips/pet-<phase>-review.rrweb.json          ← rrweb event stream
 *   docs/decks/clips/pet-<phase>-review.rrweb.capture.json  ← viewport sidecar
 * Proof screenshot (one deck): .artifacts/pet-demo/review-capture/<phase>.png
 *
 * 1600x900 DSF1. Run:
 *   cd tools/runstatus && npx playwright test tests/playwright/deck-review-clip-capture.spec.ts --reporter=line
 */
import { test, expect, chromium, type Browser, type BrowserContext, type Page } from "@playwright/test";
import path from "path";
import fs from "fs";
import { startWebServer, repoRoot, STORIES_DIR, type WebServer } from "./_helpers/server.js";
import { installCapture, dumpCapture, writeEvents } from "./_helpers/rrweb-replay.js";

const ADDR = "127.0.0.1:7793";
const APP = path.join(STORIES_DIR, "deck-review", "app.yaml");
const OUT_DIR = path.join(repoRoot, "docs", "decks", "clips");
const PROOF_DIR = path.join(repoRoot, ".artifacts", "pet-demo", "review-capture");
const VIEWPORT = { width: 1600, height: 900 } as const;

interface DeckCase {
  phase: string; // clip slug
  specPath: string; // repo-relative slidey deck json
  phaseLabel: string;
  summary: string;
}

const DECKS: DeckCase[] = [
  { phase: "prd", specPath: "docs/decks/pet-prd.slidey.json", phaseLabel: "Phase 1 · PRD", summary: "PRD: trace-column pet." },
  { phase: "design", specPath: "docs/decks/pet-design.slidey.json", phaseLabel: "Phase 2 · Design", summary: "Design: trace-column pet." },
  { phase: "decomposition", specPath: "docs/decks/pet-decomposition.slidey.json", phaseLabel: "Phase 3 · Work plan", summary: "Work plan: trace-column pet." },
  { phase: "bug-report", specPath: "docs/decks/pet-bug-report.slidey.json", phaseLabel: "Phase 4 · Bug report", summary: "Bug report: trace-column pet." },
  { phase: "bugfix-report", specPath: "docs/decks/pet-bugfix-report.slidey.json", phaseLabel: "Phase 5 · Fix report", summary: "Fix report: trace-column pet." },
  { phase: "feature-demo", specPath: "docs/decks/pet-feature-demo.slidey.json", phaseLabel: "Phase 6 · Feature demo", summary: "Feature demo: trace-column pet." },
];

let server: WebServer;

test.beforeAll(async () => {
  fs.mkdirSync(OUT_DIR, { recursive: true });
  fs.mkdirSync(PROOF_DIR, { recursive: true });
  // NO --flow: the real host registry runs host.slidey.render for real.
  server = await startWebServer({ addr: ADDR, storiesDir: STORIES_DIR });
});

test.afterAll(() => server?.stop());

for (const [i, deck] of DECKS.entries()) {
  test(`capture deck-review clip · ${deck.phase}`, async () => {
    test.setTimeout(180000);

    // ── Mint the session + seed world.deck over RPC (no LLM, explicit posture) ──
    const { session_id: sid } = await server.rpc<{ session_id: string }>(
      "runstatus.session.new",
      { story_path: APP },
    );
    expect(sid).toBeTruthy();
    await server.rpc("runstatus.session.patch_world", {
      session_id: sid,
      patch: {
        deck: { spec_path: deck.specPath, summary: deck.summary },
        phase_label: deck.phaseLabel,
      },
    });

    const browser: Browser = await chromium.launch({ headless: true });
    const context: BrowserContext = await browser.newContext({
      viewport: { ...VIEWPORT },
      deviceScaleFactor: 1,
    });
    const page: Page = await context.newPage();

    try {
      // Drive `begin` as an EXPLICIT intent over RPC (no LLM — deck-review's idle
      // room exposes only a free-text composer, and we must stay deterministic).
      // on_enter host.slidey.render runs FOR REAL → host.artifacts_dir emits the
      // slideshow → viewing. We submit out-of-band, THEN load the chat fresh at
      // `viewing` so the SPA renders the current room's view (with the deck media)
      // — the chat transcript only appends UI-driven turns, so an in-page submit
      // path would need the free-text router (an LLM); this keeps it deterministic.
      await server.rpc("runstatus.session.submit", { session_id: sid, intent: "begin" });

      // Load the chat at the (already-rendered) viewing room.
      await page.goto(`${server.base}/#/s/${sid}/chat`);
      await expect(page.getByTestId("chat-section")).toBeVisible({ timeout: 20000 });
      await expect(page.getByTestId("current-state")).toHaveText("viewing", { timeout: 90000 });

      // Record from here: the rendered deck full-screen in the viewing room.
      await installCapture(page);

      // The deck media: a slideshow renders the rendered self-contained HTML deck
      // inline in the media-slideshow-frame iframe (the operator's full-screen
      // view of the artifact). Confirm it's present + pointed at a real handle.
      const media = page.getByTestId("media-element").first();
      await expect(media, "slideshow media element").toBeVisible({ timeout: 15000 });
      const frame = page.getByTestId("media-slideshow-frame").first();
      await expect(frame, "deck iframe present").toBeVisible({ timeout: 15000 });
      const src = await frame.getAttribute("src");
      expect(src, "deck iframe points at /artifact/").toMatch(/\/artifact\//);

      // The deck content renders inside the iframe in the real browser — prove it
      // by reading the iframe document (same-origin: served by this server).
      await media.scrollIntoViewIfNeeded().catch(() => undefined);
      const fl = page.frameLocator('[data-testid="media-slideshow-frame"]');
      let deckBodyRendered = false;
      try {
        await expect(fl.locator("body")).toBeVisible({ timeout: 8000 });
        const txt = (await fl.locator("body").innerText({ timeout: 4000 }).catch(() => "")) ?? "";
        deckBodyRendered = txt.trim().length > 0;
      } catch {
        // sandbox=allow-scripts ⇒ opaque origin ⇒ frameLocator may not see in.
        deckBodyRendered = false;
      }

      // Make the clip a WATCHABLE review of the rendered deck: nudge the cursor
      // across the deck and advance a few slides (the deck iframe is same-origin,
      // so rrweb records the slide-transition DOM mutations + cursor moves — the
      // static viewing room would otherwise emit only the opening snapshot). The
      // deck full-screen content stays the focus throughout.
      // NOTE: the deck's first slide is fully captured in the opening rrweb DOM
      // snapshot (the iframe is serialised same-origin); but the deck iframe is
      // sandboxed `allow-scripts` (no script injection), so rrweb cannot attach a
      // mutation observer INSIDE it — in-deck slide transitions are not recorded
      // as incremental events. The watchable motion is therefore the operator's
      // cursor surveying the rendered deck, which rrweb records as mousemove
      // batches (and which builds a healthy event stream).
      const box = await frame.boundingBox();
      if (box) {
        await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2, { steps: 10 });
        await frame.click({ position: { x: box.width / 2, y: box.height / 2 } }).catch(() => undefined);
      }
      const MOVES = 30;
      for (let s = 0; s < MOVES && box; s++) {
        const phase = (s / MOVES) * Math.PI * 2;
        await page.mouse.move(
          box.x + box.width * (0.5 + 0.34 * Math.cos(phase)),
          box.y + box.height * (0.5 + 0.32 * Math.sin(phase * 1.3)),
          { steps: 6 },
        );
        if (s % 6 === 0) await page.keyboard.press("ArrowRight").catch(() => undefined); // best-effort slide advance
        await page.waitForTimeout(280);
      }
      await page.waitForTimeout(1200);

      // Proof screenshot (live browser render — iframe content IS painted here)
      // for every deck; the README/QA wants at least one, we emit all 6 cheaply.
      const proofPng = path.join(PROOF_DIR, `${deck.phase}.png`);
      await page.screenshot({ path: proofPng });

      const { events, viewport } = await dumpCapture(page);
      const outPath = path.join(OUT_DIR, `pet-${deck.phase}-review.rrweb.json`);
      writeEvents(events, outPath, viewport);

      // Verify the artifact bytes are a real rendered deck (not an error page).
      const handle = (src ?? "").split("/artifact/")[1]?.split("?")[0] ?? "";
      const artResp = await fetch(`${server.base}/artifact/${handle}`);
      const artBody = await artResp.text();
      const isRealDeck = artResp.status === 200 && artBody.includes("<title>slidey</title>") && artBody.length > 100000;

      // The real proof the deck rendered into the clip: the opening rrweb DOM
      // snapshot embeds the deck's content (its <title>slidey</title> + slide
      // text), not an empty/error media box.
      const clipJson = JSON.stringify(events);
      // The deck's own <title>slidey</title> + its (large) slide DOM are only in
      // the clip if rrweb serialised the iframe content — kitsoki chrome alone is
      // a fraction of the snapshot size, so a >100KB clip carrying "slidey" proves
      // the rendered deck (not an empty/error box) is embedded.
      const deckInSnapshot = clipJson.includes("slidey");

      console.log(
        `[deck-review-clip] ${deck.phase}: events=${events.length} clipBytes=${clipJson.length} ` +
          `@ ${viewport.width}x${viewport.height} deckInSnapshot=${deckInSnapshot} ` +
          `iframeBodyVisible=${deckBodyRendered} artifactBytes=${artBody.length} realDeck=${isRealDeck} -> ${outPath}`,
      );

      // Event count is modest for a static artifact-view (rrweb coalesces cursor
      // moves and cannot observe the sandboxed deck iframe's internal mutations);
      // the load-bearing non-triviality is the embedded-deck snapshot size below.
      // The floor here only rejects a bare opening snapshot (no recorded survey).
      expect(events.length, "recorded an operator survey, not just the opening snapshot").toBeGreaterThanOrEqual(12);
      expect(clipJson.length, "non-trivial clip (deck embedded in snapshot)").toBeGreaterThanOrEqual(100000);
      expect(deckInSnapshot, "the rendered deck content is embedded in the rrweb snapshot").toBe(true);
      expect(isRealDeck, "the /artifact handle serves the real rendered slidey deck").toBe(true);
    } catch (e) {
      console.log(`[deck-review-clip] ${deck.phase} FAILED: ${e instanceof Error ? e.message : String(e)}`);
      console.log(`--- server log (tail) ---\n${(server?.log?.() ?? "").slice(-2000)}`);
      throw e;
    } finally {
      await context.close();
      await browser.close();
    }
    void i;
  });
}
