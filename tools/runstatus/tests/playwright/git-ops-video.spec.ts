/**
 * git-ops-video.spec.ts — the git-ops story walkthrough video, driven against a
 * REAL `kitsoki web` server in the deterministic no-LLM posture
 * (--flow stories/git-ops/flows/demo_tour.yaml --mode one-shot: host responses
 * stubbed, harness nil, intents submitted explicitly — no LLM is ever called).
 *
 * Like multi-story.spec.ts, the WHOLE video is TOUR-DRIVEN: it runs the
 * GIT_OPS_TOUR_STEPS from src/tour/git-ops-manifest.ts via
 * window.__startTourWithSteps. The tour opens on the home story library, frames
 * the git-ops card, drives home → new session → the interactive /chat view via a
 * route-match action step, then narrates a natural feature-branch session
 * turn-by-turn: stage → commit (oracle-authored message) → rebase → merge to
 * main. The spec asserts each step's `title` against the live popover so the
 * manifest and video cannot silently drift.
 *
 * DRIVING: git-ops' rooms include the root `idle` router, which renders NO
 * clickable menu (it auto-routes via on_enter) — so it can't be advanced by a
 * button click or a free-text turn in the no-LLM posture. We therefore drive
 * every intent through the SPA's own demo hook `window.__kitsokiSubmitIntent`
 * (added by InteractiveView for exactly this: submit through the view's store
 * path so the chat + InputBar re-render reactively, unlike an out-of-band RPC).
 * `--mode one-shot` runs each turn's full emit cascade so `idle` routes to
 * branch_ops on a single `look`. These drives are PRE-STEP HOOKS (as multi-story
 * advances the PRD pipeline) so each spotlighted state exists before the
 * spotlight lands.
 *
 * Uses a tmp DB (beforeAll/afterAll). ADDR 7753 (distinct from every other spec).
 *
 * Record:  pnpm exec playwright test git-ops-video --project=chromium
 * Fast:    WEB_CHAT_PACE=0 pnpm exec playwright test git-ops-video --project=chromium
 *
 * NOTE: the harness suppresses Playwright stdout — per-step progress + failure
 * context is also written to .artifacts/git-ops/ERROR.txt.
 */
import { test, expect, chromium, type Browser, type BrowserContext, type Page, type Locator } from "@playwright/test";
import { fileURLToPath } from "url";
import path from "path";
import fs from "fs";
import os from "os";
import { spawn, type ChildProcess } from "child_process";
import {
  makeShot,
  waitForState,
  prepareVideoDir,
  saveVideoAsMp4,
  dwell,
  cinematicGoto,
  SETTLE_MS,
  ChapterRecorder,
  writeChapters,
} from "./_helpers/server.js";
import { DEMO_VIEWPORT, captureDiagnostics } from "./_helpers/demo.js";
import { GIT_OPS_TOUR_STEPS, type TourStep } from "../../src/tour/git-ops-manifest.js";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

const repoRoot = path.resolve(__dirname, "../../../..");
const BIN = path.join(repoRoot, "bin", "kitsoki");
const STORIES_DIR = path.join(repoRoot, "stories");
const FLOW = path.join(repoRoot, "stories", "git-ops", "flows", "demo_tour.yaml");

const ADDR = "127.0.0.1:7753";
const BASE = `http://${ADDR}`;
const RPC = `${BASE}/rpc`;

const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "git-ops");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "video");
const ERROR_LOG = path.join(ARTIFACT_DIR, "ERROR.txt");
const CHAPTER_SOURCE = "tools/runstatus/src/tour/git-ops-manifest.ts";

let server: ChildProcess | null = null;
let serverLog = "";
let tmpDbDir = "";

function diag(msg: string): void {
  try {
    fs.appendFileSync(ERROR_LOG, `[${new Date().toISOString()}] ${msg}\n`);
  } catch {
    /* best-effort */
  }
}

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
    if (!fs.existsSync(p)) throw new Error(`missing required path: ${p} (run 'make build' first)`);
  }
  prepareVideoDir(VIDEO_DIR);
  fs.mkdirSync(ARTIFACT_DIR, { recursive: true });
  fs.writeFileSync(ERROR_LOG, "");

  tmpDbDir = fs.mkdtempSync(path.join(os.tmpdir(), "kitsoki-git-ops-"));
  const dbPath = path.join(tmpDbDir, "s.db");

  server = spawn(
    BIN,
    // one-shot: run each turn's full emit cascade so the root `idle` router
    // advances to branch_ops on a single `look` (staged mode holds its templated
    // auto-route emit). Host calls are stubbed by --flow; no LLM.
    ["web", "--stories-dir", STORIES_DIR, "--flow", FLOW, "--mode", "one-shot", "--addr", ADDR, "--db", dbPath],
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
  if (server && !server.killed) {
    server.kill("SIGTERM");
    await new Promise((r) => setTimeout(r, 500));
    if (!server.killed) server.kill("SIGKILL");
  }
  if (tmpDbDir) fs.rmSync(tmpDbDir, { recursive: true, force: true });
});

/** The git-ops story card (matched by its title). */
function gitOpsCard(page: Page): Locator {
  return page
    .getByTestId("story-card")
    .filter({ has: page.getByTestId("story-title").filter({ hasText: "git-ops" }) });
}

/** Resolve an action step's real target. The new-session click must land on the
 *  git-ops card (the deterministic --flow posture is git-ops-specific). */
function resolveTarget(page: Page, step: TourStep): Locator {
  if (step.id === "gitops-intro-start") {
    return gitOpsCard(page).getByTestId("new-session-btn");
  }
  return page.getByTestId(step.target!).first();
}

async function injectTour(page: Page, steps: readonly TourStep[]): Promise<void> {
  await page.evaluate((stepsJson: string) => {
    (window as unknown as { __startTourWithSteps?: (s: string) => void })
      .__startTourWithSteps?.(stepsJson);
  }, JSON.stringify(steps));
  await expect(page.getByTestId("tour-overlay")).toBeVisible({ timeout: 8000 });
}

test.describe("git-ops story walkthrough (live, no-LLM)", () => {
  test("extensive walkthrough: stage(gate) → commit → undo → rebase → merge → pull → worktree → cleanup", async () => {
    test.setTimeout(300000);

    const stories = await rpc<Array<{ path: string; app_id: string; title: string }>>(
      "runstatus.stories.list",
      {},
    );
    const gitops = stories.find((s) => s.app_id === "git-ops");
    expect(gitops, "git-ops story is in the catalogue").toBeTruthy();

    const browser: Browser = await chromium.launch({ headless: true });
    const context: BrowserContext = await browser.newContext({
      viewport: { ...DEMO_VIEWPORT },
      recordVideo: { dir: VIDEO_DIR, size: { ...DEMO_VIEWPORT } },
    });
    const page = await context.newPage();
    const video = page.video(); // capture BEFORE context.close()
    const shot = makeShot(ARTIFACT_DIR);
    const { mark, onThrow } = captureDiagnostics(page, ARTIFACT_DIR);
    const chapters = new ChapterRecorder();

    let sid = "";

    /** Drive a git intent through the SPA's OWN store path (InteractiveView's
     *  __kitsokiSubmitIntent demo hook), so the chat + InputBar re-render
     *  reactively. This is the deterministic way to advance git-ops' button-less
     *  router rooms (idle) and every other room in the no-LLM posture, mirroring
     *  the window.__startTourWithSteps hook the tour itself uses. */
    async function drive(intent: string, slots: Record<string, unknown> = {}): Promise<void> {
      await page.waitForFunction(
        () => typeof (window as unknown as { __kitsokiSubmitIntent?: unknown }).__kitsokiSubmitIntent === "function",
        { timeout: 15000 },
      );
      await page.evaluate(
        ([name, s]) =>
          (window as unknown as {
            __kitsokiSubmitIntent: (n: string, sl: Record<string, unknown>) => Promise<void>;
          }).__kitsokiSubmitIntent(name as string, s as Record<string, unknown>),
        [intent, slots] as const,
      );
    }
    /** Patch session world server-side (demo tooling RPC). Used once after the
     *  merge lands on main_ops to flip on_integration=true — we merged a FEATURE
     *  branch so it's still false, which would route the integration-hub rooms'
     *  `back` to branch_ops. The next drive() reads the patched world. */
    async function patchWorld(patch: Record<string, unknown>): Promise<void> {
      await rpc("runstatus.session.patch_world", { session_id: sid, patch });
    }

    try {
      // ── 1. Open the home story library and start the tour ON it ──────────────
      mark("navigating home");
      await cinematicGoto(page, `${BASE}/#/`, { waitForTestId: "home-view" });
      await expect(page.getByTestId("story-card").first()).toBeVisible({ timeout: 15000 });
      await injectTour(page, GIT_OPS_TOUR_STEPS);

      // ── 2. Walk the GIT_OPS_TOUR_STEPS ───────────────────────────────────────
      for (const step of GIT_OPS_TOUR_STEPS) {
        mark(`step ${step.id}`);

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

        // ── Pre-step setup: advance the git-ops session so the state exists ────
        if (step.id === "gitops-hub") {
          await drive("look"); // idle → branch_ops (one-shot routes via on_enter)
          await waitForState(page, "branch_ops", 15000);
          await dwell(page, SETTLE_MS);
        }
        if (step.id === "gitops-stage") {
          await drive("stage"); // → staging (classifies; .npmrc flagged suspicious)
          await waitForState(page, "staging", 15000);
          await dwell(page, SETTLE_MS);
        }
        if (step.id === "gitops-gate") {
          await drive("add_all"); // suspicious file → confirm interstitial (stays in staging)
          await dwell(page, SETTLE_MS);
        }
        if (step.id === "gitops-commit") {
          await drive("confirm_add_all"); // confirm the suspicious stage
          await dwell(page, SETTLE_MS);
          await drive("back"); // → branch_ops
          await waitForState(page, "branch_ops", 15000);
          await drive("commit"); // → commit room (oracle authors the message)
          await waitForState(page, "commit", 15000);
          await dwell(page, SETTLE_MS);
        }
        if (step.id === "gitops-undo") {
          await drive("regenerate"); // re-draft the message (stays in commit)
          await dwell(page, SETTLE_MS);
          await drive("accept"); // commit → branch_ops
          await waitForState(page, "branch_ops", 15000);
          await drive("undo"); // → undo room (reset modes shown; we won't undo)
          await waitForState(page, "undo", 15000);
          await dwell(page, SETTLE_MS);
        }
        if (step.id === "gitops-rebase") {
          await drive("back"); // undo → branch_ops (keep the commit)
          await waitForState(page, "branch_ops", 15000);
          await drive("rebase"); // conflict auto-resolved in one turn → branch_ops (rebase_done)
          await waitForState(page, "branch_ops", 15000);
          await dwell(page, SETTLE_MS);
        }
        if (step.id === "gitops-merge") {
          await drive("merge_into_main"); // gated on rebase_done → the merge room
          await waitForState(page, "merge_into_main", 15000);
          await dwell(page, SETTLE_MS);
        }
        if (step.id === "gitops-pull") {
          await drive("back"); // merge → main_ops (integration hub)
          await waitForState(page, "main_ops", 15000);
          await patchWorld({ on_integration: true }); // we merged a feature branch; flip the hub flag
          await drive("pull"); // → pull room (no upstream tracking)
          await waitForState(page, "pull", 15000);
          await dwell(page, SETTLE_MS);
        }
        if (step.id === "gitops-worktree") {
          await drive("back"); // pull → main_ops
          await waitForState(page, "main_ops", 15000);
          await drive("worktree_create"); // → worktree_create room
          await waitForState(page, "worktree_create", 15000);
          await drive("describe", { desc: "add login" }); // names the branch add-login, creates it
          await dwell(page, SETTLE_MS);
        }
        if (step.id === "gitops-list") {
          await drive("back"); // worktree_create → main_ops
          await waitForState(page, "main_ops", 15000);
          await drive("worktree_list"); // → worktree_list room (lists worktrees)
          await waitForState(page, "worktree_list", 15000);
          await dwell(page, SETTLE_MS);
        }
        if (step.id === "gitops-cleanup") {
          await drive("back"); // worktree_list → main_ops
          await waitForState(page, "main_ops", 15000);
          await drive("cleanup"); // → cleanup room (remove options)
          await waitForState(page, "cleanup", 15000);
          await dwell(page, SETTLE_MS);
        }
        if (step.id === "gitops-done") {
          await drive("remove_all"); // cleanup → main_ops (worktree + branch removed)
          await waitForState(page, "main_ops", 15000);
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
          const remaining = GIT_OPS_TOUR_STEPS.slice(GIT_OPS_TOUR_STEPS.indexOf(step) + 1);
          if (remaining.some((s) => s.title === actualTitle)) {
            mark(`  drift-skip: overlay on "${actualTitle}"`);
            continue;
          }
        }
        await expect(titleEl).toHaveText(step.title, { timeout: 12000 });

        chapters.open(step.id, step.title, CHAPTER_SOURCE);
        await dwell(page, step.dwellMs ?? 3000);
        await shot(page, step.id);

        if (step.kind === "explain") {
          await page.getByTestId("tour-next").click();
          await dwell(page, 700);
        } else {
          const target = resolveTarget(page, step);
          await target.scrollIntoViewIfNeeded().catch(() => undefined);
          if (step.advance === "route-match") {
            await target.evaluate((el) => (el as HTMLElement).click());
            await page.waitForTimeout(300);
            if (step.advanceRoute === "interactive") {
              await page.waitForURL(/#\/s\/[0-9a-f-]{36}\/chat$/, { timeout: 15000 });
              const m = page.url().match(/\/s\/([0-9a-f-]{36})\/chat$/);
              if (m) {
                sid = m[1];
                mark(`session ${sid}`);
              }
            }
            await dwell(page, 1000);
          } else {
            await target.evaluate((el) => (el as HTMLElement).click());
            await dwell(page, 1000);
          }
        }
      }

      await expect(page.getByTestId("tour-overlay")).toHaveCount(0, { timeout: 5000 });
    } catch (err) {
      diag(`FAILED: ${err instanceof Error ? err.stack ?? err.message : String(err)}`);
      diag(`--- server log ---\n${serverLog}`);
      onThrow(err);
      throw err;
    } finally {
      await page.close();
      await context.close();
      const mp4 = await saveVideoAsMp4(video, ARTIFACT_DIR, "git-ops-demo");
      writeChapters(mp4, chapters.list());
      await browser.close();
    }

    const pngs = fs.readdirSync(ARTIFACT_DIR).filter((f) => f.endsWith(".png"));
    console.log(`[git-ops-video] screenshots (${pngs.length}) in ${ARTIFACT_DIR}`);
  });
});
