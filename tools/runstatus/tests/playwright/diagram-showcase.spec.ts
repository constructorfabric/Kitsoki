/**
 * State-diagram SHOWCASE video — the four route views against the dev-story
 * proposal pipeline (the exact scenario the design mockup was drawn for:
 * `.artifacts/diagram-options/index.html`).
 *
 * A dedicated, feature-focused demo (distinct from the generic onboarding tour
 * in tour-video.spec.ts). It drives the kitsoki-dev proposal pipeline and walks
 * all four diagram views slowly, with captions:
 *
 *   1. Metro stepper — vertical route: traveled leg (TRACE) with the intent that
 *      entered each stop, the amber current station (LIVE) + horizon pills, and
 *      the muted road ahead (PROJECTION). Phase banners (INTAKE / SEARCHING /
 *      BRIEF / DRAFTING / PUBLISHED) ride each stop.
 *   2. Ego-graph — the same neighbourhood as a node-link SVG.
 *   3. Path & Horizon — breadcrumb (room + "via <intent>") + hero card + chips.
 *   4. Full — the whole static machine.
 *
 * Then it advances the run ON-CAMERA so the metro line visibly grows.
 *
 * DETERMINISM (see docs/skills/kitsoki-ui-demo/SKILL.md → "Deterministic
 * recording" and _helpers/demo.ts):
 *   - Setup (home → session → main → proposal_search) is driven OFF-CAMERA via
 *     RPC behind a full-screen CURTAIN, so the recording never shows rushed nav.
 *   - The on-camera advance is a REAL same-page UI click (one deterministic
 *     path — not a cross-client SSE/reload race).
 *   - No LLM: the proposal_happy_path flow stubs every host call.
 *
 * Record:  pnpm exec playwright test diagram-showcase --project=chromium
 * Fast:    WEB_CHAT_PACE=0 pnpm exec playwright test diagram-showcase --project=chromium
 * Then:    docs/skills/kitsoki-ui-demo/scripts/render.sh \
 *            .artifacts/diagram-showcase/diagram-showcase-demo.webm
 */
import { test, expect, chromium, type Browser, type BrowserContext, type Page } from "@playwright/test";
import path from "path";
import {
  startWebServer,
  repoRoot,
  makeShot,
  waitForState,
  prepareVideoDir,
  saveAndRemuxVideo,
  type WebServer,
} from "./_helpers/server.js";
import {
  DEMO_VIEWPORT,
  dwell,
  installCurtain,
  liftCurtain,
  makeCaption,
  captureDiagnostics,
} from "./_helpers/demo.js";

const ADDR = "127.0.0.1:7753";
const STORY_DIR = path.join(repoRoot, "stories", "dev-story");
const FLOW = path.join(STORY_DIR, "flows", "proposal_happy_path.yaml");
const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "diagram-showcase");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "video");
const BEAT = 5000;

let server: WebServer;
test.beforeAll(async () => {
  prepareVideoDir(VIDEO_DIR); // clear stale webm so saveAndRemuxVideo picks THIS run's
  server = await startWebServer({ addr: ADDR, flow: FLOW, storiesDir: STORY_DIR });
});
test.afterAll(() => server?.stop());

/** Give the diagram the stage: shrink chat, widen the diagram panel. Injected
 *  presentation CSS only — not a render hack on the trace. */
async function stageDiagram(page: Page): Promise<void> {
  await page.addStyleTag({
    content: `
      .iv__chat { flex: 0 0 22% !important; }
      .iv__trace { flex: 1 1 78% !important; }
      .iv__panel--diagram { flex: 1 1 82% !important; }
      .iv__panel--timeline { flex: 1 1 18% !important; }
    `,
  });
}

test("state-diagram four-view showcase (dev-story, no-LLM)", async () => {
  test.setTimeout(300000);
  const browser: Browser = await chromium.launch({ headless: true });
  const context: BrowserContext = await browser.newContext({
    viewport: { ...DEMO_VIEWPORT },
    recordVideo: { dir: VIDEO_DIR, size: { ...DEMO_VIEWPORT } },
  });
  const page: Page = await context.newPage();
  const video = page.video(); // capture BEFORE context.close()
  const shot = makeShot(ARTIFACT_DIR);
  const { mark, onThrow } = captureDiagnostics(page, ARTIFACT_DIR);

  let sid = "";
  const submit = (intent: string, slots: Record<string, unknown> = {}) =>
    server.rpc("runstatus.session.submit", { session_id: sid, intent, slots });
  const tab = async (mode: string): Promise<void> => {
    mark(`tab:${mode}`);
    await page.getByTestId(`diagram-tab-${mode}`).click();
    await dwell(page, 700);
  };
  // Advance one stage ON-CAMERA via the real chat button. Clicking in the
  // driving page renders the turn result directly (no cross-client SSE timing,
  // no reload) — one deterministic visual path. `confirm` from proposal_search
  // cascades search → materialize → refine.
  const advance = async (intent: string, next: string): Promise<void> => {
    mark(`advance:${intent}->${next}`);
    await page.getByTestId(`intent-btn-${intent}`).first().click();
    await waitForState(page, next, 12000);
  };

  // Curtain BEFORE the first goto — hides all off-camera setup.
  await installCurtain(page, "kitsoki — the state diagram");

  try {
    // ── Off-camera setup (behind the curtain): reach proposal_search ──────
    await page.goto(`${server.base}/#/`);
    await expect(page.getByTestId("home-view")).toBeVisible({ timeout: 15000 });
    const card = page.locator("[data-testid='story-card']").filter({ hasText: /dev.story/i }).first();
    await card.getByTestId("new-session-btn").click();
    await page.waitForURL(/#\/s\/[0-9a-f-]{36}\/chat$/, { timeout: 15000 });
    sid = page.url().match(/\/s\/([0-9a-f-]{36})\/chat$/)?.[1] ?? "";
    await waitForState(page, "main", 15000);
    await server.rpc("runstatus.session.patch_world", { session_id: sid, patch: { judge_mode: "human" } });

    await submit("go_idea", { message: "work on a proposal" });
    await submit("discuss", { message: "I want a per-session working folder primitive" });

    // Reload into the populated mid-pipeline state, stage the diagram, install
    // the caption, then lift the curtain → first visible frame is the finished
    // metro view, never setup.
    await page.reload();
    await waitForState(page, "proposal_search", 15000);
    await stageDiagram(page);
    const beat = await makeCaption(page, BEAT);
    await expect(page.getByTestId("diagram-metro")).toBeVisible({ timeout: 8000 });
    await dwell(page, 1600);
    await liftCurtain(page);

    // ── 1. Metro stepper (default view) ──────────────────────────────────
    mark("metro");
    await beat("The state diagram is your route",
      "A “metro stepper” centred on where the run is — derived entirely from the trace.", 6000);
    await shot(page, "metro-overview");

    await beat("Where you've been",
      "The bright leg is ground truth from the trace — each stop labelled with the intent that got you there (via go_idea, via discuss …).");
    await expect(page.getByTestId("diagram-metro-station").first()).toBeVisible();
    await shot(page, "metro-traveled");

    await beat("Where you are",
      "The amber station is the current room with its declared phase banner; the pills are the live moves available right now.");
    await expect(page.getByTestId("diagram-current-station")).toBeVisible();
    await shot(page, "metro-current");

    const fwdPill = page.locator('[data-testid="diagram-horizon-pill"].state-diagram__pill--forward').first();
    if ((await fwdPill.count()) > 0) {
      await beat("Click a next-move to see where it leads",
        "Each pill highlights its target room across the views and the timeline.", 2500);
      await fwdPill.click();
      await dwell(page, 3500);
      await shot(page, "metro-pill-highlight");
    }

    await beat("Where you can go",
      "The muted, dashed road ahead is projection from the static graph — declared, not yet travelled.");
    await expect(page.getByTestId("diagram-road-ahead").first()).toBeVisible();
    await shot(page, "metro-road-ahead");

    // ── 2. Ego-graph (node-link) ─────────────────────────────────────────
    await tab("ego");
    await beat("The same neighbourhood as a node graph",
      "Came-from → you-are-here → the rooms each live move leads to, with directed elbow connectors.", 6000);
    await expect(page.getByTestId("diagram-ego")).toBeVisible();
    await shot(page, "ego-graph");

    // ── 3. Path & Horizon (breadcrumb + chips) ───────────────────────────
    await tab("path");
    await beat("Or as a breadcrumb + live chips",
      "Provenance on top (room + via-intent), the current room as a hero card, the live exits as chips.", 6000);
    await expect(page.getByTestId("diagram-path-horizon")).toBeVisible();
    await shot(page, "path-horizon");

    // ── 4. Watch the metro line move with the run ────────────────────────
    await tab("metro");
    await beat("Watch it move with the run",
      "Confirm the search and the pipeline advances: the traveled leg grows, the road ahead shrinks — live from the trace.", 3500);
    await advance("confirm", "proposal_refine");
    await dwell(page, 5000);
    await shot(page, "metro-advanced");

    // ── 5. The whole machine is one click away ───────────────────────────
    await tab("full");
    await beat("The whole machine is always one click away",
      "“Full” flips to the entire static graph — every phase and room — and back to your route.", 5500);
    await shot(page, "full-graph");

    await tab("metro");
    await beat("Path & Horizon",
      "Provenance, live moves, and projection — one feature, four views, all from the trace.", 6000);
    await shot(page, "finale");
  } catch (err) {
    onThrow(err);
    throw err;
  } finally {
    await context.close(); // finalises the recording
    await saveAndRemuxVideo(video, ARTIFACT_DIR, "diagram-showcase-demo");
    await browser.close();
  }
});
