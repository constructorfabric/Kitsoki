/**
 * swarm-finding-capture.spec.ts — swarm-capture: proves a swarm gate failure
 * produces a complete, independently replayable evidence bundle instead of
 * just a Playwright error message.
 *
 * Reuses swarm-tier1's own negative-control mechanism
 * (swarm-replay-users.spec.ts's "seeded cross-talk fault" test): two pages
 * are deliberately pointed at the SAME minted session id, which
 * `assertIsolated` (tools/swarm/journey.ts, backed by isolation.ts's
 * trace-based ground truth) must flag as a leak — a real per-user isolation
 * gate going red for a real reason, not a hardcoded throw. When that
 * happens, `tools/swarm/capture`'s `recordFinding` is called for the
 * "victim" page and the resulting bundle is validated end-to-end:
 *
 *   1. the bundle directory + manifest.json exist and self-describe the
 *      finding (persona, journey step, assertion, server sha);
 *   2. rrweb.json is schema-valid AND replays — loaded through the exact
 *      rrweb bundle path (`RRWEB_BUNDLE`) the existing rrweb-replay.ts
 *      loader uses, constructing a real `rrweb.Replayer` in a headless page
 *      (the same construction renderReplayWithHolds uses to compute
 *      totalTime — see that file's doc comment) rather than a new parser;
 *   3. har.json is scrubbed — the server's own $HOME does not appear
 *      anywhere in the captured HAR (harscrub's home-path redaction, the
 *      same guarantee `runstatus.bug.preview` already provides interactive
 *      bug reports).
 *
 * Run:  cd tools/runstatus && npx playwright test tests/playwright/swarm-finding-capture.spec.ts
 */
import { test, expect, chromium, type Browser, type BrowserContext, type Page } from "@playwright/test";
import fs from "fs";
import path from "path";
import os from "os";
import { startWebServer, repoRoot, STORIES_DIR, waitForState, type WebServer } from "./_helpers/server.js";
import { RRWEB_BUNDLE } from "./_helpers/rrweb-replay.js";
import { loadPersonas, personaForIndex } from "../../../swarm/personas.js";
import { markerFor } from "../../../swarm/isolation.js";
import { openUserSession, driveUserJourney, assertIsolated, type AuditFn } from "../../../swarm/journey.js";
import { recordFinding, defaultFindingsDir, type FindingManifest } from "../../../swarm/capture/index.js";

const FLOW = path.join(repoRoot, "stories", "prd", "flows", "happy_path.yaml");
const ADDR = "127.0.0.1:7803"; // distinct from every other spec's port (see swarm-replay-users.spec.ts's note)
const RUN_ID = `finding-capture-${Date.now()}`;

// No-op audit: this spec is about the CAPTURE path, not the audit probes
// themselves (those are exercised by swarm-replay-users.spec.ts).
const noAudit: AuditFn = async () => [];

let server: WebServer;

test.beforeAll(async () => {
  for (const p of [STORIES_DIR, FLOW]) {
    if (!fs.existsSync(p)) throw new Error(`missing required path: ${p}`);
  }
  server = await startWebServer({ addr: ADDR, flow: FLOW, storiesDir: STORIES_DIR });
});

test.afterAll(async () => {
  server?.stop();
});

test.describe("swarm-capture — finding capture bundle", () => {
  test("a forced isolation-gate failure produces a replayable, scrubbed evidence bundle", async () => {
    test.setTimeout(120000);

    const personas = loadPersonas();
    const persona = personaForIndex(personas, 0);

    const stories = await server.rpc<Array<{ path: string; app_id: string }>>("runstatus.stories.list", {});
    const prd = stories.find((s) => s.app_id === "prd");
    expect(prd, "PRD story is in the catalogue").toBeTruthy();
    const storyPath = prd!.path;

    // Mint ONE real session and drive it normally (the "owner").
    const marker = markerFor(RUN_ID, 0, persona.id);

    const browser: Browser = await chromium.launch({ headless: true });
    const ownerContext: BrowserContext = await browser.newContext();
    const victimContext: BrowserContext = await browser.newContext();
    const ownerPage: Page = await ownerContext.newPage();
    const victimPage: Page = await victimContext.newPage();

    const victimConsoleErrors: string[] = [];
    victimPage.on("console", (msg) => {
      if (msg.type() === "error") victimConsoleErrors.push(msg.text());
    });
    victimPage.on("pageerror", (err) => victimConsoleErrors.push(err.message));

    let bundleDir = "";
    let manifest: FindingManifest | null = null;

    try {
      const { session_id } = await openUserSession({
        page: ownerPage,
        base: server.base,
        rpc: server.rpc.bind(server),
        storyPath,
      });
      await driveUserJourney({
        page: ownerPage,
        session_id,
        rpc: server.rpc.bind(server),
        persona,
        marker,
        audit: noAudit,
      });

      // FORCED FAILURE (swarm-tier1's own negative-control mechanism, reused
      // rather than reinvented): point the "victim" page at the SAME session
      // id the owner is using — exactly the registry bug isolation.ts exists
      // to catch.
      // The owner's journey (driveUserJourney) already advanced this shared
      // session to "clarifying" — the victim page loads the SAME session id
      // mid-journey, so it must wait for THAT state, not "idle".
      await victimPage.goto(`${server.base}/#/s/${session_id}/chat`);
      await victimPage.getByTestId("chat-section").waitFor({ state: "visible", timeout: 15000 });
      await waitForState(victimPage, "clarifying", 15000);

      const isolation = await assertIsolated(server.rpc.bind(server), session_id, [marker]);
      expect(isolation.ok, "the seeded cross-talk fault must be detected (gate must go red)").toBe(false);
      expect(isolation.leaked).toContain(marker);

      // The per-user gate failed — capture the victim's evidence bundle
      // rather than just letting the assertion throw.
      const bundle = await recordFinding({
        page: victimPage,
        rpc: server.rpc.bind(server),
        consoleMessages: victimConsoleErrors,
        context: {
          persona_id: persona.id,
          user_index: 1,
          marker,
          journey_step: "chat/clarifying",
          assertion: "isolation: session trace must not contain another user's marker",
          detail: { session_id, leaked: isolation.leaked },
        },
        repoRoot,
      });
      bundleDir = bundle.dir;
      manifest = bundle.manifest;
    } finally {
      await ownerPage.close().catch(() => undefined);
      await victimPage.close().catch(() => undefined);
      await ownerContext.close().catch(() => undefined);
      await victimContext.close().catch(() => undefined);
      await browser.close();
    }

    // ── 1. Bundle + manifest exist and self-describe the finding. ──────────
    expect(bundleDir, "recordFinding wrote a bundle dir").not.toBe("");
    expect(fs.existsSync(bundleDir)).toBe(true);
    expect(bundleDir.startsWith(defaultFindingsDir(repoRoot)), "bundle lives under .artifacts/swarm/findings/").toBe(
      true,
    );

    const manifestPath = path.join(bundleDir, "manifest.json");
    expect(fs.existsSync(manifestPath), "manifest.json exists").toBe(true);
    const onDiskManifest = JSON.parse(fs.readFileSync(manifestPath, "utf-8")) as FindingManifest;
    expect(manifest).not.toBeNull();
    expect(onDiskManifest.persona_id).toBe(persona.id);
    expect(onDiskManifest.marker).toBe(marker);
    expect(onDiskManifest.journey_step).toBe("chat/clarifying");
    expect(onDiskManifest.assertion).toContain("isolation");
    expect(onDiskManifest.server_sha, "server_sha is stamped (non-empty)").toBeTruthy();
    expect(onDiskManifest.detail.leaked).toContain(marker);

    // ── 2. rrweb.json is schema-valid AND replays. ─────────────────────────
    expect(onDiskManifest.files.rrweb, "rrweb evidence was captured").toBe("rrweb.json");
    const rrwebPath = path.join(bundleDir, onDiskManifest.files.rrweb!);
    expect(fs.existsSync(rrwebPath)).toBe(true);
    const rrwebEnvelope = JSON.parse(fs.readFileSync(rrwebPath, "utf-8")) as {
      schemaVersion: number;
      events: Array<{ type: number }>;
    };
    expect(rrwebEnvelope.schemaVersion).toBe(1);
    expect(Array.isArray(rrwebEnvelope.events)).toBe(true);
    expect(rrwebEnvelope.events.length, "captured at least one rrweb event").toBeGreaterThan(0);

    const replayBrowser: Browser = await chromium.launch({ headless: true });
    try {
      const replayContext = await replayBrowser.newContext();
      const replayPage = await replayContext.newPage();
      await replayPage.setContent("<!doctype html><html><body><div id='replay-host'></div></body></html>");
      // Reuse the EXACT rrweb bundle the existing replay specs load — no
      // reinvented parser, no CDN fetch (see rrweb-replay.ts's design
      // constraints).
      await replayPage.addScriptTag({ path: RRWEB_BUNDLE });
      const totalTime = await replayPage.evaluate((events) => {
        const host = document.getElementById("replay-host")!;
        const rrweb = (window as unknown as Record<string, { Replayer: new (e: unknown[], c: Record<string, unknown>) => { getMetaData(): { totalTime: number }; pause(t?: number): void } }>)[
          "rrweb"
        ];
        if (!rrweb || typeof rrweb.Replayer !== "function") throw new Error("rrweb global missing Replayer");
        const player = new rrweb.Replayer(events, { root: host, showWarning: false, mouseTail: false });
        player.pause(0);
        return player.getMetaData().totalTime;
      }, rrwebEnvelope.events);
      expect(totalTime, "the captured rrweb stream constructs a Replayer without throwing").toBeGreaterThanOrEqual(0);
    } finally {
      await replayBrowser.close();
    }

    // ── 3. har.json is scrubbed. ────────────────────────────────────────────
    expect(onDiskManifest.files.har, "HAR evidence was captured").toBe("har.json");
    const harPath = path.join(bundleDir, onDiskManifest.files.har!);
    expect(fs.existsSync(harPath)).toBe(true);
    const harRaw = fs.readFileSync(harPath, "utf-8");
    const home = os.homedir();
    if (home) {
      expect(harRaw.includes(home), "scrubbed HAR must not contain the raw $HOME path").toBe(false);
    }
    const har = JSON.parse(harRaw) as { log?: { entries?: unknown[] } };
    expect(har.log, "har.json parses as a HAR document").toBeTruthy();
  });
});
