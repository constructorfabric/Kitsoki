/**
 * pet-prd-dev-capture.spec.ts — rrweb capture of the PRD being DEVELOPED for the
 * trace-column pet scenario (the PM half of the dev story). The hybrid deck
 * previously jumped straight from the GitHub kickoff to the design; this clip
 * fills that gap with the actual PRD-development conversation, in order.
 *
 * Deterministic, no LLM: drives the pets-dev `pm_idea.yaml` nil-harness flow
 * (every host.agent.* call stubbed by the flow's host_handlers) IN THE BROWSER,
 * turn by turn, via the InteractiveView __kitsokiSubmitIntent hook. Each turn is
 * the flow's exact explicit intent + slots; the slot-bearing discuss/answer
 * turns pass a displayLabel so the transcript shows the operator's real message
 * (a nil-harness --flow cannot route typed prose to a slot, so we submit the
 * slots directly — same posture as the flow fixture's `turns`).
 *
 * The walk: open the PRD workspace → frame the one-line idea → discovery →
 * clarifying questions → the PM answers → brief → references → drafting → the
 * PRD is published. A caption banner narrates each phase on-camera.
 *
 * Output:
 *   docs/decks/clips/pet-prd-dev.rrweb.json          ← rrweb event stream
 *   docs/decks/clips/pet-prd-dev.rrweb.capture.json  ← viewport sidecar
 *
 * Run:
 *   cd tools/runstatus && npx playwright test tests/playwright/pet-prd-dev-capture.spec.ts --reporter=line
 */
import { test, expect, chromium, type Browser, type BrowserContext, type Page } from "@playwright/test";
import path from "path";
import fs from "fs";
import { startWebServer, repoRoot, dwell, cinematicGoto, demoAddr, type WebServer } from "./_helpers/server.js";
import { makeCaption } from "./_helpers/demo.js";
import { installCapture, dumpCapture, writeEvents } from "./_helpers/rrweb-replay.js";

const ADDR = demoAddr(7794);
const STORY_DIR = path.join(repoRoot, "stories", "pets-dev");
const FLOW = path.join(STORY_DIR, "flows", "pm_idea.yaml");
const OUT_DIR = path.join(repoRoot, "docs", "decks", "clips");
const EVENTS_JSON = path.join(OUT_DIR, "pet-prd-dev.rrweb.json");
const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "pet-prd-dev");
const VIEWPORT = { width: 1600, height: 900 } as const;

// Repo-relative PRD artifact, full-screened at the end (the published output).
const PRD_PATH = "stories/pets-dev/assets/pm_idea-prd.md";

interface Turn {
  intent: string;
  slots?: Record<string, unknown>;
  label?: string; // displayLabel → renders as the operator's message bubble
  stateLeaf: string; // tolerant suffix match on the current-state testid
  caption: [string, string];
  holdMs: number;
}

// Mirrors stories/pets-dev/flows/pm_idea.yaml `turns` exactly.
const TURNS: Turn[] = [
  { intent: "core__go_prd", stateLeaf: "idle", caption: ["The PM opens the PRD workspace", "Conversation-driven intake begins — no template, just an idea."], holdMs: 2600 },
  {
    intent: "core__prd__discuss",
    slots: { message: "I want a tiny SVG pet at the bottom of the kitsoki trace column" },
    label: "I want a tiny SVG pet at the bottom of the kitsoki trace column",
    stateLeaf: "idle",
    caption: ["A one-line idea", "“A tiny SVG pet at the bottom of the trace column.”"],
    holdMs: 3400,
  },
  { intent: "core__prd__start", stateLeaf: "search", caption: ["Discovery", "kitsoki scans for prior art and overlapping work."], holdMs: 2600 },
  { intent: "core__prd__confirm", stateLeaf: "clarifying", caption: ["Clarifying questions", "Who is the actor, and what is the one success metric?"], holdMs: 3000 },
  {
    intent: "core__prd__answer",
    slots: { text: "developers watching a long kitsoki trace; metric is opt-in-pet-enabled-sessions" },
    label: "developers watching a long kitsoki trace; metric is opt-in-pet-enabled-sessions",
    stateLeaf: "clarifying",
    caption: ["The PM answers", "Actor: developers watching a long run. Metric: opt-in-pet-enabled-sessions."],
    holdMs: 3400,
  },
  { intent: "core__prd__submit_answers", stateLeaf: "brief", caption: ["The brief comes together", "Problem, actor, goals, and the headline metric."], holdMs: 3000 },
  { intent: "core__prd__confirm", stateLeaf: "references", caption: ["References", "The PRD is anchored to the surfaces it touches."], holdMs: 2600 },
  { intent: "core__prd__confirm", stateLeaf: "drafting", caption: ["Drafting the PRD", "kitsoki writes the validated requirements document."], holdMs: 3000 },
  { intent: "core__prd__accept", stateLeaf: "published", caption: ["PRD published", "The product-manager phase's validated output — handed to the architect."], holdMs: 3200 },
];

let server: WebServer;

test.beforeAll(async () => {
  fs.mkdirSync(OUT_DIR, { recursive: true });
  fs.mkdirSync(ARTIFACT_DIR, { recursive: true });
  server = await startWebServer({ addr: ADDR, flow: FLOW, storiesDir: STORY_DIR });
});

test.afterAll(() => server?.stop());

async function currentState(page: Page): Promise<string> {
  return page.evaluate(() => {
    const el = document.querySelector('[data-testid="current-state"]');
    return el ? (el.textContent || "").trim() : "";
  });
}

async function waitForStateLeaf(page: Page, leaf: string, timeoutMs = 20000): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  let cur = "";
  while (Date.now() < deadline) {
    cur = await currentState(page);
    if (cur.toLowerCase().includes(leaf.toLowerCase())) return;
    await page.waitForTimeout(250);
  }
  throw new Error(`wait-state "${leaf}" timed out (last "${cur}")`);
}

async function submitIntent(page: Page, intent: string, slots: Record<string, unknown>, label?: string): Promise<void> {
  const ok = await page.evaluate(
    async ({ n, s, l }) => {
      const fn = (
        window as unknown as {
          __kitsokiSubmitIntent?: (n: string, s?: Record<string, unknown>, l?: string) => Promise<void>;
        }
      ).__kitsokiSubmitIntent;
      if (!fn) return false;
      await fn(n, s, l);
      return true;
    },
    { n: intent, s: slots, l: label },
  );
  if (!ok) throw new Error(`__kitsokiSubmitIntent hook not present for ${intent}`);
}

async function scrollChatToEnd(page: Page): Promise<void> {
  await page.evaluate(() => {
    const el = document.querySelector('[data-testid="chat-transcript"]') as HTMLElement | null;
    if (el) el.scrollTop = el.scrollHeight;
  });
}

test("pet PRD-development rrweb capture (pm_idea flow, turn by turn)", async () => {
  test.setTimeout(300000);
  const browser: Browser = await chromium.launch({ headless: true });
  const context: BrowserContext = await browser.newContext({
    viewport: { ...VIEWPORT },
    deviceScaleFactor: 1,
  });
  const page: Page = await context.newPage();

  try {
    // Home → start a session (mounts InteractiveView at core.landing) → capture.
    await cinematicGoto(page, `${server.base}/#/`, { waitForTestId: "home-view" });
    await expect(page.getByTestId("story-card").first()).toBeVisible({ timeout: 15000 });
    await page.getByTestId("new-session-btn").first().click();
    await page.waitForURL(/#\/s\/[0-9a-f-]{36}\/chat$/, { timeout: 20000 });
    await expect(page.getByTestId("chat-section")).toBeVisible({ timeout: 20000 });
    await expect(page.getByTestId("current-state")).toBeVisible({ timeout: 20000 });

    await installCapture(page);
    const caption = await makeCaption(page);

    await caption("Idea → PRD", "A product manager talks a one-line idea into a validated PRD.", 2600);

    for (const t of TURNS) {
      await submitIntent(page, t.intent, t.slots ?? {}, t.label);
      await waitForStateLeaf(page, t.stateLeaf);
      await scrollChatToEnd(page);
      await caption(t.caption[0], t.caption[1], t.holdMs);
    }

    // Full-screen the published PRD document (the validated output).
    await page.evaluate((p) => {
      (window as unknown as { __openArtifact?: (s: string) => void }).__openArtifact?.(p);
    }, PRD_PATH);
    await expect(page.getByTestId("markdown-modal")).toBeVisible({ timeout: 8000 }).catch(() => undefined);
    await page.waitForTimeout(1400);
    await page.evaluate(async () => {
      const el = document.querySelector('[data-testid="markdown-modal-body"]') as HTMLElement | null;
      if (!el) return;
      const max = el.scrollHeight - el.clientHeight;
      if (max <= 2) return;
      const t0 = performance.now();
      const ms = 4200;
      await new Promise<void>((res) => {
        const tick = (now: number) => {
          const p = Math.min(1, (now - t0) / ms);
          const eased = p < 0.5 ? 2 * p * p : 1 - Math.pow(-2 * p + 2, 2) / 2;
          el.scrollTop = max * eased;
          if (p < 1) requestAnimationFrame(tick);
          else res();
        };
        requestAnimationFrame(tick);
      });
    });
    await page.waitForTimeout(1400);

    const finalState = await currentState(page);
    const { events, viewport } = await dumpCapture(page);
    writeEvents(events, EVENTS_JSON, viewport);
    console.log(
      `[pet-prd-dev] events=${events.length} finalState="${finalState}" @ ${viewport.width}x${viewport.height} -> ${EVENTS_JSON}`,
    );
    expect(finalState.toLowerCase()).toContain("published");
    expect(events.length, "recorded the full PRD-development walk").toBeGreaterThanOrEqual(60);
  } catch (e) {
    console.log(`[pet-prd-dev] FAILED: ${e instanceof Error ? e.message : String(e)}`);
    console.log(`--- server log (tail) ---\n${(server?.log?.() ?? "").slice(-2500)}`);
    throw e;
  } finally {
    await context.close();
    await browser.close();
  }
});
