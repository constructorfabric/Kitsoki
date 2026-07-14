/**
 * ad-hoc-workbench-video.spec.ts — the AD-HOC WORKBENCH epic feature-spotlight
 * video, driven against a REAL `kitsoki web` server in the deterministic no-LLM
 * `--flow` (nil-harness) posture: intents are submitted explicitly, no LLM is
 * ever invoked, so the frames are byte-stable and free to reproduce.
 *
 * Like the golden agent-actions / off-ramp specs, the WHOLE video is
 * TOUR-DRIVEN: it runs AD_HOC_WORKBENCH_TOUR_STEPS from
 * src/tour/ad-hoc-workbench-manifest.ts via window.__startTourWithSteps and
 * asserts each popover `title` against the manifest so the recording can't drift
 * from what the manifest declares.
 *
 * POSTURE. The epic's free-form landing room (with QUICK-ACTION choice buttons +
 * a free-text floor) is the REAL dev-story `landing` room (root: landing,
 * stories/dev-story/rooms/landing.yaml) — the actual workbench floor, not a
 * lookalike. The story is driven by stories/dev-story/flows/landing_quick_action.yaml
 * (nil harness), so a fresh session lands directly on `landing` showing the
 * quick-action choice buttons (intent-btn-go_ticket_search, go_bugfix, go_prd, …)
 * the room ships AND the free-text floor beneath them.
 *
 * The other two surfaces — the scoped capsule write opt-in and the /mine proposals —
 * are PRODUCED by a real agent at runtime, which a no-LLM demo never invokes.
 * They are seeded through the deterministic demo seams registered onMounted:
 *   - window.__pushOperatorQuestion (OperatorQuestionModal) — the write-mode
 *     "May I edit?" opt-in card (operator-question-modal / oq-option-* / oq-submit).
 *   - window.__pushProposal (InteractiveView) — the structure proposal that lights
 *     the proposals-badge; clicking it surfaces the SAME operator-question card.
 * Both carry "demo-" ids so the card resolves LOCALLY (no parked backend entry to
 * 404 against) — exactly the short-circuit proposals.spec.ts / operator-ask-video
 * use. The REAL badge + REAL card render with the injected content.
 *
 * Record:  pnpm exec playwright test ad-hoc-workbench-video --project=chromium
 * Fast:    WEB_CHAT_PACE=0 pnpm exec playwright test ad-hoc-workbench-video --project=chromium
 */
import { test, expect, chromium, type Browser, type BrowserContext, type Page } from "@playwright/test";
import { fileURLToPath } from "url";
import path from "path";
import fs from "fs";
import os from "os";
import { spawn, type ChildProcess } from "child_process";
import {
  makeShot,
  prepareVideoDir,
  saveVideoAsMp4,
  dwell,
  cinematicGoto,
  SETTLE_MS,
  ChapterRecorder,
  writeChapters,
} from "./_helpers/server.js";
import { DEMO_VIEWPORT, captureDiagnostics } from "./_helpers/demo.js";
import { AD_HOC_WORKBENCH_TOUR_STEPS, type TourStep } from "../../src/tour/ad-hoc-workbench-manifest.js";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

// repoRoot = .../kitsoki (tools/runstatus/tests/playwright -> 4 up)
const repoRoot = path.resolve(__dirname, "../../../..");
const BIN = path.join(repoRoot, "bin", "kitsoki");
// Point at the whole stories/ tree so the home screen shows the real catalogue.
const STORIES_DIR = path.join(repoRoot, "stories");
const STORY_DIR = path.join(repoRoot, "stories", "dev-story");
// landing_quick_action.yaml is the nil-harness flow: a fresh session lands on
// the REAL dev-story `landing` room (root: landing) and intents are submitted
// explicitly — no LLM, byte-stable frames.
const FLOW = path.join(STORY_DIR, "flows", "landing_quick_action.yaml");

// Unique port — distinct from every other spec (7740 multi-story, 7746-7748
// trace/onboarding/agent-actions, 7751 off-ramp/proposals) so parallel runs
// never race on the bind.
const ADDR = "127.0.0.1:7753";
const BASE = `http://${ADDR}`;
const RPC = `${BASE}/rpc`;

const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "ad-hoc-workbench");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "video");
const CHAPTER_SOURCE = "tools/runstatus/src/tour/ad-hoc-workbench-manifest.ts";

// ── The seeded demo frames (deterministic, "demo-" ids resolve locally) ───────

// The scoped capsule write opt-in, surfaced in the operator-question card.
const WRITE_MODE_FRAME = {
  session_id: "demo-session",
  question_id: "demo-write-mode-1",
  questions: [
    {
      question:
        "I'd like to update docs/architecture/ambient-mining.md in a managed capsule, then validate and merge it to staging/local. May I make that scoped change?",
      header: "May I change this?",
      multiSelect: false,
      options: [
        { label: "accept", description: "Grant mutation capability for this capsule change (emits WriteModeGranted)." },
        { label: "refine", description: "Narrow the ask — scope it to a smaller change." },
        { label: "dismiss", description: "Make no change; keep the primary checkout protected." },
      ],
    },
  ],
};

// The mined structure proposal that lights the proposals badge.
const STRUCTURE_PROPOSAL = {
  id: "demo-prop-render",
  kind: "structure",
  title: "Capture `make render` after every doc edit as a gate?",
  detail: "You ran `make render` after each of the last 8 doc edits — promote it to a post-edit gate?",
};

// ── server lifecycle (tmp DB; spawned directly so we own the args/DB path) ────

let server: ChildProcess | null = null;
let serverLog = "";
let tmpDbDir = "";

async function rpc<T>(method: string, params: Record<string, unknown>): Promise<T> {
  const res = await fetch(RPC, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ jsonrpc: "2.0", id: 1, method, params }),
  });
  const body = (await res.json()) as { result?: T; error?: { message: string } };
  if (body.error) throw new Error(`${method} failed: ${body.error.message}`);
  return body.result as T;
}

async function waitForHealthy(timeoutMs: number): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  let lastErr = "";
  while (Date.now() < deadline) {
    try {
      const res = await fetch(`${BASE}/`, { method: "GET" });
      if (res.status === 200) return;
      lastErr = `status ${res.status}`;
    } catch (e) {
      lastErr = e instanceof Error ? e.message : String(e);
    }
    await new Promise((r) => setTimeout(r, 200));
  }
  throw new Error(`server not healthy after ${timeoutMs}ms (last: ${lastErr})\n--- server log ---\n${serverLog}`);
}

test.beforeAll(async () => {
  for (const p of [STORIES_DIR, FLOW, BIN]) {
    if (!fs.existsSync(p)) {
      throw new Error(`missing required path: ${p} (run 'make build-bin' first)`);
    }
  }
  prepareVideoDir(VIDEO_DIR); // clears stale .webm; must run before context creation

  tmpDbDir = fs.mkdtempSync(path.join(os.tmpdir(), "kitsoki-ad-hoc-workbench-"));
  const dbPath = path.join(tmpDbDir, "s.db");

  // Nil-harness --flow posture: intents submitted explicitly, no LLM.
  server = spawn(
    BIN,
    ["web", "--stories-dir", STORIES_DIR, "--flow", FLOW, "--addr", ADDR, "--db", dbPath],
    { cwd: repoRoot, stdio: ["ignore", "pipe", "pipe"] },
  );
  server.stdout?.on("data", (d) => (serverLog += d.toString()));
  server.stderr?.on("data", (d) => (serverLog += d.toString()));
  server.on("exit", (code, sig) => {
    serverLog += `\n[server exited code=${code} sig=${sig}]\n`;
  });

  await waitForHealthy(20000);
});

test.afterAll(async () => {
  // Stop ONLY this spec's server, by its own handle (never pkill -f kitsoki).
  if (server && !server.killed) {
    server.kill("SIGTERM");
    await new Promise((r) => setTimeout(r, 500));
    if (!server.killed) server.kill("SIGKILL");
  }
  if (tmpDbDir) fs.rmSync(tmpDbDir, { recursive: true, force: true });
});

/** The dev-story story card (matched by its title — the real workbench). */
function workbenchCard(page: Page) {
  return page
    .getByTestId("story-card")
    .filter({ has: page.getByTestId("story-title").filter({ hasText: "Engineer's Day" }) });
}

/** Inject (or re-inject) the tour and confirm the overlay is up. */
async function injectTour(page: Page, steps: readonly TourStep[]): Promise<void> {
  await page.evaluate((stepsJson: string) => {
    (window as unknown as { __startTourWithSteps?: (s: string) => void })
      .__startTourWithSteps?.(stepsJson);
  }, JSON.stringify(steps));
  await expect(page.getByTestId("tour-overlay")).toBeVisible({ timeout: 8000 });
}

/** Seed an operator-question frame via the deterministic demo seam. */
async function pushOperatorQuestion(page: Page, frame: unknown): Promise<void> {
  await page.evaluate((json: string) => {
    (window as unknown as { __pushOperatorQuestion?: (s: string) => void }).__pushOperatorQuestion?.(json);
  }, JSON.stringify(frame));
}

/** Seed a proposal via the deterministic demo seam. */
async function pushProposal(page: Page, proposal: unknown): Promise<void> {
  await page.evaluate((json: string) => {
    (window as unknown as { __pushProposal?: (s: string) => void }).__pushProposal?.(json);
  }, JSON.stringify(proposal));
}

test.describe("ad-hoc workbench feature-spotlight (live, no-LLM --flow)", () => {
  test("home → free-form landing → write-mode opt-in → proposals badge → proposal card → refine flow", async () => {
    test.setTimeout(180000);

    // Startup discovers the whole catalogue but creates no sessions.
    const stories = await rpc<Array<{ path: string; app_id: string; title: string }>>(
      "runstatus.stories.list",
      {},
    );
    const devStory = stories.find((s) => s.app_id === "dev-story");
    expect(devStory, "dev-story story is in the catalogue").toBeTruthy();

    const browser: Browser = await chromium.launch({ headless: true });
    const context: BrowserContext = await browser.newContext({
      viewport: { ...DEMO_VIEWPORT }, // 1600x900 — matches every other demo
      recordVideo: { dir: VIDEO_DIR, size: { ...DEMO_VIEWPORT } },
    });
    const page = await context.newPage();
    const video = page.video(); // capture BEFORE context.close()
    const shot = makeShot(ARTIFACT_DIR);
    const { mark, onThrow } = captureDiagnostics(page, ARTIFACT_DIR);

    // Per-step chapter windows for the MP4 sidecar (clock starts now).
    const chapters = new ChapterRecorder();

    try {
      // ── 1. Open the home story library and start the tour ON it ──────────────
      mark("navigating home");
      await cinematicGoto(page, `${BASE}/#/`, { waitForTestId: "home-view" });
      await expect(page.getByTestId("story-card").first()).toBeVisible({ timeout: 15000 });
      await injectTour(page, AD_HOC_WORKBENCH_TOUR_STEPS);

      // ── 2. Walk AD_HOC_WORKBENCH_TOUR_STEPS ──────────────────────────────────
      // Session id captured from the URL after awb-intro-start navigates to
      // /s/<UUID>/chat — used by __seedMetaRefine to seed the correct scope.
      let sessionId = "";

      for (const step of AD_HOC_WORKBENCH_TOUR_STEPS) {
        mark(`step ${step.id}`);

        // Mirror the overlay's route-guard.
        const currentUrl = page.url();
        const currentRouteKind = currentUrl.includes("/chat")
          ? "interactive"
          : currentUrl.match(/#\/s\/[0-9a-f-]{36}$/)
            ? "any"
            : "home";
        if (step.route !== "any" && step.route !== currentRouteKind) {
          mark(`  route-skip (${currentRouteKind})`);
          continue;
        }

        // ── Pre-step setup ────────────────────────────────────────────────────
        if (step.id === "awb-landing") {
          // Fresh run lands on the REAL dev-story `landing` room: the
          // quick-action choice buttons the room ships (intent-btn-go_ticket_search,
          // go_bugfix, go_prd, …) + a free-text floor beneath them. Anchor the
          // landing room's OWN testids before the spotlight lands.
          await expect(page.getByTestId("current-state")).toContainText("landing", { timeout: 15000 });
          await expect(page.getByTestId("intent-actions")).toBeVisible({ timeout: 15000 });
          await expect(page.getByTestId("intent-btn-go_ticket_search")).toBeVisible({ timeout: 15000 });
          await expect(page.getByTestId("intent-btn-go_bugfix")).toBeVisible({ timeout: 15000 });
          await expect(page.getByTestId("text-floor-input")).toBeVisible({ timeout: 15000 });
          await dwell(page, SETTLE_MS);
        }
        if (step.id === "awb-writemode-card") {
          // Seed the scoped capsule write opt-in as a real operator-question frame so
          // the REAL card renders (operator-question-modal / oq-option-* / oq-submit).
          await pushOperatorQuestion(page, WRITE_MODE_FRAME);
          await expect(page.getByTestId("operator-question-modal")).toBeVisible({ timeout: 8000 });
          await expect(page.getByTestId("operator-question-modal")).toContainText("managed capsule");
          await expect(page.getByTestId("operator-question-modal")).toContainText("staging/local");
          await expect(page.getByTestId("oq-option-0-0")).toContainText("accept");
          await expect(page.getByTestId("oq-option-0-2")).toContainText("dismiss");
          // Pick "accept" so the submit (the step's click-target) grants write mode.
          await page.getByTestId("oq-option-0-0").evaluate((el) => (el as HTMLElement).click());
          await dwell(page, SETTLE_MS);
        }
        if (step.id === "awb-proposals-badge") {
          // The write-mode card was submitted on the prior step; now seed a mined
          // structure proposal so the proposals badge appears with a count.
          await pushProposal(page, STRUCTURE_PROPOSAL);
          await expect(page.getByTestId("proposals-badge")).toBeVisible({ timeout: 8000 });
          await expect(page.getByTestId("proposals-badge-count")).toHaveText("1");
          await dwell(page, SETTLE_MS);
        }
        if (step.id === "awb-proposal-card") {
          // The prior step (awb-proposals-badge, click-target) already clicked the
          // badge, which surfaces the head proposal into the operator-question card
          // AND pops it off the queue (so the badge hides). The modal is therefore
          // already open here — just confirm it carries the proposal content before
          // the spotlight lands.
          await expect(page.getByTestId("operator-question-modal")).toBeVisible({ timeout: 8000 });
          await expect(page.getByTestId("operator-question-modal")).toContainText("make render");
          await dwell(page, SETTLE_MS);
        }
        if (step.id === "awb-refine-pick") {
          // The proposal card is still open from awb-proposal-card (the prior step
          // was explain/next, which only advanced the tour overlay — it did NOT
          // dismiss the operator-question card). Pre-select the "refine" radio (index 1).
          await expect(page.getByTestId("operator-question-modal")).toBeVisible({ timeout: 8000 });
          await page.getByTestId("oq-option-0-1").evaluate((el) => (el as HTMLElement).click());
          await dwell(page, SETTLE_MS);
        }
        if (step.id === "awb-refine-open") {
          // After awb-refine-pick's click-target (oq-submit) fires, the proposal
          // card resolves locally and dismisses. Now seed the meta overlay with the
          // reworked draft so the tour can spotlight it.
          await page.evaluate((json: string) => {
            (window as unknown as { __seedMetaRefine?: (p: unknown) => void }).__seedMetaRefine?.(JSON.parse(json));
          }, JSON.stringify({
            sessionId: sessionId,
            transcript: [
              { role: "assistant", text: "**Mined draft — gate `render_docs`**\n\n```yaml\ngate:\n  id: render_docs\n  when: docs_edited\n  run: make render        # re-render the whole site\n```" },
              { role: "user", text: "Only re-render the docs that changed in this edit — not the whole site. Diff against the base and render just those files." },
              { role: "assistant", text: "Reworked — render only the changed docs:\n\n```yaml\ngate:\n  id: render_docs\n  when: docs_edited\n  run: |\n    changed=\"$(git diff --name-only \"$BASE_SHA\"... -- 'docs/**/*.md')\"\n    [ -z \"$changed\" ] && { echo 'no docs changed — skip'; exit 0; }\n    for f in $changed; do\n      make render-one FILE=\"$f\"\n    done\n```\nThis goes beyond the flat `make render` — it computes the changed set and renders each. Applying + reloading." },
            ],
            reloadNote: "story.edit applied · render_docs gate updated · flow suite green (23/23)",
          }));
          await expect(page.getByTestId("meta-overlay")).toBeVisible({ timeout: 8000 });
          // Hard gate: the reworked more-complex draft must actually be rendered.
          await expect(page.getByTestId("meta-transcript")).toContainText("render-one", { timeout: 5000 });
          await expect(page.getByTestId("meta-transcript")).toContainText("git diff --name-only", { timeout: 5000 });
          await dwell(page, SETTLE_MS);
        }
        if (step.id === "awb-refine-result") {
          // Scroll to the last agent row so the spotlight targets visible content.
          const agentRows = page.getByTestId("meta-row-agent");
          const count = await agentRows.count();
          if (count > 0) {
            await agentRows.nth(count - 1).scrollIntoViewIfNeeded();
          }
          await dwell(page, SETTLE_MS);
        }
        if (step.id === "awb-refine-applied") {
          await expect(page.getByTestId("meta-reload-note")).toBeVisible({ timeout: 8000 });
          await dwell(page, SETTLE_MS);
        }

        // Honor DOM-presence preconditions.
        if (step.waitForTarget) {
          await expect(page.getByTestId(step.waitForTarget).first()).toBeVisible({ timeout: 15000 });
        }

        // Anti-drift assertion: the popover must show THIS step's title.
        const titleEl = page.getByTestId("tour-title");
        const actualTitle = await titleEl.textContent({ timeout: 8000 }).catch(() => "");
        if (actualTitle !== step.title) {
          const remaining = AD_HOC_WORKBENCH_TOUR_STEPS.slice(
            AD_HOC_WORKBENCH_TOUR_STEPS.indexOf(step) + 1,
          );
          const isOnNext = remaining.some((s) => s.title === actualTitle);
          if (isOnNext) {
            mark(`  drift-skip: overlay on "${actualTitle}"`);
            continue;
          }
        }
        await expect(titleEl).toHaveText(step.title, { timeout: 12000 });

        // Spotlight settled → open this step's chapter window.
        chapters.open(step.id, step.title, CHAPTER_SOURCE);

        await dwell(page, step.dwellMs ?? 3000);
        await shot(page, step.id);

        if (step.kind === "explain") {
          await page.getByTestId("tour-next").click();
          await dwell(page, 700);
        } else if (step.advance === "route-match") {
          // The only route-match step is awb-intro-start (→ interactive): click
          // New session on the off-ramp-demo card specifically.
          const target = workbenchCard(page).getByTestId("new-session-btn");
          await target.scrollIntoViewIfNeeded().catch(() => undefined);
          await target.evaluate((el) => (el as HTMLElement).click());
          await page.waitForTimeout(300);
          await page.waitForURL(/#\/s\/[0-9a-f-]{36}\/chat$/, { timeout: 15000 });
          // Capture session id for later __seedMetaRefine call.
          const chatUrl = page.url();
          const sidMatch = chatUrl.match(/#\/s\/([0-9a-f-]{36})\/chat$/);
          if (sidMatch) sessionId = sidMatch[1];
          await dwell(page, 1000);
        } else {
          // click-target steps: dispatch the DOM click directly (the overlay
          // backdrop is up). It fires the control's own @click AND the overlay's
          // capture-phase advance listener bound on the same element.
          //   - awb-writemode-card: oq-submit (already a picked option) → resolves
          //     the write-mode opt-in and dismisses the card.
          //   - awb-proposals-badge: proposals-badge → opens the proposal card,
          //     handled by the awb-proposal-card pre-step hook on the next iter.
          const target = page.getByTestId(step.target!).first();
          await target.scrollIntoViewIfNeeded().catch(() => undefined);
          await target.evaluate((el) => (el as HTMLElement).click());
          await dwell(page, 1000);
        }
      }

      // The final step's "Done" closes the tour.
      await expect(page.getByTestId("tour-overlay")).toHaveCount(0, { timeout: 5000 });
    } catch (err) {
      onThrow(err);
      fs.writeFileSync(path.join(ARTIFACT_DIR, "server.log"), serverLog);
      throw err;
    } finally {
      await page.close();
      await context.close(); // finalises the video
      const mp4 = await saveVideoAsMp4(video, ARTIFACT_DIR, "ad-hoc-workbench-demo");
      writeChapters(mp4, chapters.list());
      await browser.close();
    }

    const pngs = fs.readdirSync(ARTIFACT_DIR).filter((f) => f.endsWith(".png"));
    console.log(`[ad-hoc-workbench-video] screenshots (${pngs.length}) in ${ARTIFACT_DIR}`);
  });
});
