/**
 * Oregon Trail end-to-end demo + visual QA.
 *
 * Drives the full intro wizard → store purchase → 7 trail legs → ended_won
 * against a real `kitsoki web` server in the deterministic no-LLM posture
 * (--flow winning_deterministic.yaml).
 *
 * Records a video + per-scene screenshots to .artifacts/oregon-trail-e2e/.
 *
 * Run:
 *   pnpm playwright test tests/playwright/oregon-trail-e2e.spec.ts --reporter=list
 */
import {
  test,
  expect,
  chromium,
  type Browser,
  type BrowserContext,
  type Page,
} from "@playwright/test";
import path from "path";
import fs from "fs";
import {
  startWebServer,
  repoRoot,
  makeShot,
  waitForState,
  demoAddr,
  type WebServer,
} from "./_helpers/server.js";
import { cameraContext } from "./_helpers/camera.js";

const FLOW = path.join(repoRoot, "stories", "oregon-trail", "flows", "winning_deterministic.yaml");
const ADDR = demoAddr(7743);

const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "oregon-trail-e2e");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "video");

let server: WebServer;

interface TurnResult {
  mode: string;
  state: string;
  view?: string;
}

test.beforeAll(async () => {
  fs.mkdirSync(VIDEO_DIR, { recursive: true });
  server = await startWebServer({ addr: ADDR, flow: FLOW });
});

test.afterAll(() => server?.stop());

// ── Timings ──────────────────────────────────────────────────────────────────
const DWELL = 3500;        // pause at each scene (ms)
const BEFORE_ACT = 1200;   // pause before each user action
const SNAP_DELAY = 1000;   // settle after navigation before screenshot
const TRAIL_STEP_MS = 180; // delay between rapid RPC continues (lets SSE update UI)

const shot = makeShot(ARTIFACT_DIR);

async function clickBtn(page: Page, label: string): Promise<void> {
  await page.waitForTimeout(BEFORE_ACT);
  const byTestId = page.getByTestId(`intent-btn-${label}`);
  if ((await byTestId.count()) > 0) {
    await byTestId.first().click();
  } else {
    await page.getByRole("button", { name: label }).first().click();
  }
}

/**
 * Patch the session world and fire `continue` once.
 * Used to pre-set miles_traveled at the leg's distance so the arrival guard
 * fires on the very next step — mirrors the flow-file world_override pattern.
 */
async function arriveAt(sessionId: string, milesOverride: number): Promise<void> {
  await server.rpc("runstatus.session.patch_world", {
    session_id: sessionId,
    patch: { miles_traveled: milesOverride },
  });
  const result = await server.rpc<TurnResult>("runstatus.session.submit", {
    session_id: sessionId,
    intent: "continue",
    slots: {},
  });
  if (!result.state.endsWith("_awaiting_reply") && result.state !== "ended_won") {
    throw new Error(`arriveAt(${milesOverride}): unexpected state ${result.state}`);
  }
}

/** Fire `continue` via RPC to advance from _awaiting_reply to the next leg. */
async function advanceLeg(sessionId: string, expectedPrefix: string): Promise<void> {
  const result = await server.rpc<TurnResult>("runstatus.session.submit", {
    session_id: sessionId,
    intent: "continue",
    slots: {},
  });
  if (!result.state.startsWith(expectedPrefix) && result.state !== "ended_won") {
    throw new Error(`advanceLeg: expected prefix ${expectedPrefix}, got ${result.state}`);
  }
}

// ── The demo ─────────────────────────────────────────────────────────────────

test("Oregon Trail — full intro wizard + trail + win (no-LLM)", async () => {
  test.setTimeout(300000);
  const browser: Browser = await chromium.launch({ headless: true });
  const context: BrowserContext = await browser.newContext(
    cameraContext({ recordVideoDir: VIDEO_DIR }),
  );
  const page: Page = await context.newPage();
  const BASE = server.base;

  let sessionId = "";

  try {
    // ── Scene 1: Home screen ────────────────────────────────────────────────
    await page.goto(`${BASE}/#/`);
    await expect(page.getByTestId("home-view")).toBeVisible({ timeout: 15000 });
    await page.waitForTimeout(DWELL);
    await shot(page, "home");

    // ── Scene 2: Start Oregon Trail session ─────────────────────────────────
    const oregonCard = page.locator("[data-testid='story-card']").filter({ hasText: /oregon.trail/i });
    await expect(oregonCard).toBeVisible({ timeout: 8000 });
    await page.waitForTimeout(BEFORE_ACT);
    await oregonCard.getByTestId("new-session-btn").click();

    await page.waitForURL(/#\/s\/[0-9a-f-]{36}\/chat$/, { timeout: 15000 });
    const urlMatch = page.url().match(/\/s\/([0-9a-f-]{36})\/chat$/);
    sessionId = urlMatch?.[1] ?? "";

    await waitForState(page, "intro");
    await page.waitForTimeout(SNAP_DELAY);
    await shot(page, "intro-welcome");

    // ── Scene 3: Begin setup ────────────────────────────────────────────────
    await page.waitForTimeout(DWELL);
    await clickBtn(page, "begin_setup");
    await waitForState(page, "intro_profession");
    await page.waitForTimeout(SNAP_DELAY);
    await shot(page, "intro-profession");

    await expect(page.getByTestId("intent-btn-pick_profession").first()).toBeVisible({ timeout: 5000 });
    await expect(page.getByTestId("intent-btn-pick_profession")).toHaveCount(3);

    // ── Scene 4: Pick Banker ─────────────────────────────────────────────────
    await page.waitForTimeout(DWELL);
    await page.getByRole("button", { name: /Banker/i }).first().click();
    await waitForState(page, "intro_month");
    await page.waitForTimeout(SNAP_DELAY);
    await shot(page, "intro-month");

    // ── Scene 5: Depart in May ──────────────────────────────────────────────
    await page.waitForTimeout(DWELL);
    const mayBtn = page.getByRole("button", { name: /Depart in May/i }).first();
    await expect(mayBtn).toBeVisible({ timeout: 5000 });
    await mayBtn.click();
    await waitForState(page, "intro_party_names");
    await page.waitForTimeout(SNAP_DELAY);
    await shot(page, "intro-party-names");

    // ── Scene 6: Name the party ─────────────────────────────────────────────
    await page.waitForTimeout(DWELL);
    // The param composer is a wrapping <textarea> now (not a single-line input).
    const csvInput = page.locator("textarea[placeholder*='Adam']").first();
    if ((await csvInput.count()) > 0) {
      await csvInput.fill("Alice,Bob,Carol,Dan,Eve");
      await csvInput.press("Enter");
    } else {
      await page.getByTestId("intent-btn-name_party").first().click();
    }
    await page.waitForTimeout(1000);
    await shot(page, "intro-party-names-filled");

    // ── Scene 7: Continue to summary ───────────────────────────────────────
    await page.waitForTimeout(DWELL);
    const continueBtn = page.getByRole("button", { name: /^continue/i }).first();
    await expect(continueBtn).toBeVisible({ timeout: 5000 });
    await continueBtn.click();
    await waitForState(page, "intro_summary");
    await page.waitForTimeout(SNAP_DELAY);
    await shot(page, "intro-summary");

    // ── Scene 8: Start the journey → general store ─────────────────────────
    await page.waitForTimeout(DWELL);
    const startBtn = page.getByRole("button", { name: /Start the journey/i }).first();
    await expect(startBtn).toBeVisible({ timeout: 5000 });
    await startBtn.click();
    await waitForState(page, "general_store.idle", 15000);
    await page.waitForTimeout(SNAP_DELAY);
    await shot(page, "general-store");

    // ── Scene 9: Set a budget ($800) ───────────────────────────────────────
    await page.waitForTimeout(DWELL);
    // The "Set a budget" item has a param form with placeholder "e.g. 200"
    // (a wrapping <textarea> now, not a single-line input).
    const budgetInput = page.locator("textarea[placeholder*='200']").first();
    await expect(budgetInput).toBeVisible({ timeout: 5000 });
    await page.waitForTimeout(BEFORE_ACT);
    await budgetInput.fill("800");
    await budgetInput.press("Enter");
    await waitForState(page, "general_store.reviewing", 8000);
    await page.waitForTimeout(SNAP_DELAY);
    await shot(page, "store-reviewing");

    // ── Scene 10: Accept the purchase ──────────────────────────────────────
    await page.waitForTimeout(DWELL);
    await page.getByRole("button", { name: /accept_purchase/i }).first().click();
    await waitForState(page, "general_store.done", 8000);
    await page.waitForTimeout(SNAP_DELAY);
    await shot(page, "store-done");

    // ── Scene 11: Leave the store ──────────────────────────────────────────
    await page.waitForTimeout(DWELL);
    await page.getByRole("button", { name: /leave_store/i }).first().click();
    await waitForState(page, "leg_a_executing.traveling", 10000);
    await page.waitForTimeout(SNAP_DELAY);
    await shot(page, "trail-leg-a");

    // ── Trail: 7 legs ──────────────────────────────────────────────────────
    // For each leg: patch miles_traveled to the leg's distance, fire continue
    // once → arrival. Then fire continue again → next leg (or ended_won).
    // This mirrors the flow-file world_override strategy exactly.
    //
    // Leg distances from winning_deterministic.yaml:
    //   A=102  B=202  C=250  D=86  E=292  F=250  G=318

    const legs: Array<{ id: string; dist: number; nextPrefix: string; landmark: string }> = [
      { id: "leg_a", dist: 102, nextPrefix: "leg_b", landmark: "Kansas River Crossing" },
      { id: "leg_b", dist: 202, nextPrefix: "leg_c", landmark: "Fort Kearney" },
      { id: "leg_c", dist: 250, nextPrefix: "leg_d", landmark: "Chimney Rock" },
      { id: "leg_d", dist:  86, nextPrefix: "leg_e", landmark: "Fort Laramie" },
      { id: "leg_e", dist: 292, nextPrefix: "leg_f", landmark: "South Pass" },
      { id: "leg_f", dist: 250, nextPrefix: "leg_g", landmark: "Snake River Crossing" },
      { id: "leg_g", dist: 318, nextPrefix: "ended_won", landmark: "Willamette Valley" },
    ];

    for (let i = 0; i < legs.length; i++) {
      const leg = legs[i];
      await page.waitForTimeout(DWELL);

      // Advance a few UI steps so the video shows the trail ticking over.
      for (let step = 0; step < 3; step++) {
        const cont = page.getByRole("button", { name: /^Continue$/i }).first();
        if ((await cont.count()) > 0) {
          await page.waitForTimeout(TRAIL_STEP_MS);
          await cont.click();
          await page.waitForTimeout(TRAIL_STEP_MS);
        }
      }

      // Fast-forward to arrival via world patch + one continue RPC.
      await arriveAt(sessionId, leg.dist);
      await waitForState(page, `${leg.id}_awaiting_reply`, 8000);
      await page.waitForTimeout(SNAP_DELAY);
      await shot(page, `trail-arrive-${leg.landmark.toLowerCase().replace(/[^a-z0-9]+/g, "-")}`);

      if (leg.nextPrefix === "ended_won") {
        // Final leg: continue → ended_won
        await page.waitForTimeout(DWELL);
        const finalCont = page.getByRole("button", { name: /^Continue$/i }).first();
        if ((await finalCont.count()) > 0) {
          await finalCont.click();
        } else {
          await advanceLeg(sessionId, "ended_won");
        }
        await waitForState(page, "ended_won", 10000);
      } else {
        // Intermediate leg: advance via RPC to keep the UI consistent.
        await page.waitForTimeout(DWELL);
        await advanceLeg(sessionId, leg.nextPrefix);
        await waitForState(page, `${leg.nextPrefix}_executing.traveling`, 8000);
      }
    }

    // ── Scene final: ended_won ──────────────────────────────────────────────
    await page.waitForTimeout(SNAP_DELAY);
    await shot(page, "ended-won");
    await page.waitForTimeout(DWELL);
    await shot(page, "final");
  } finally {
    await context.close();
    await browser.close();
  }
});
