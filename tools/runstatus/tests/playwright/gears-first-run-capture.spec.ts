/**
 * gears-first-run-capture.spec.ts — REAL web/session trace capture for the
 * constructorfabric/gears-rust first-run demo scenario.
 *
 * This deliberately records against `--harness recording --record <jsonl>` and
 * the real project instance app from /Users/brad/code/gears-rust. Generated
 * screenshots / rrweb / JSONL live under the product-journey run evidence dir
 * supplied by FIRST_RUN_RUN_DIR; they are evidence, not committed fixtures.
 */
import { test, expect, chromium, type Browser, type BrowserContext, type Page } from "@playwright/test";
import fs from "fs";
import os from "os";
import path from "path";
import {
  startWebServer,
  repoRoot,
  cinematicGoto,
  dwell,
  demoAddr,
  maybeInstallAutoRrwebCapture,
  type WebServer,
} from "./_helpers/server.js";

const ADDR = demoAddr(7812);
const GEARS_ROOT = process.env.GEARS_REPO_ROOT ?? "/Users/brad/code/gears-rust";
const STORY_DIR = path.join(GEARS_ROOT, ".kitsoki", "stories", "gears-rust-dev");
const RUN_DIR =
  process.env.FIRST_RUN_RUN_DIR ??
  path.join(repoRoot, ".artifacts", "product-journey", "gears-first-run-manual-capture");
const EVIDENCE_DIR = path.join(
  RUN_DIR,
  "evidence",
  "gears-rust--core-maintainer",
  "gears-first-run-web-demo",
);
const SESSION_TRACE = path.join(EVIDENCE_DIR, "gears-first-run-web-demo-session_trace.jsonl");
const PRD_SESSION_TRACE = path.join(EVIDENCE_DIR, "gears-first-run-web-demo-prd-session_trace.jsonl");
const TICKET_SESSION_TRACE = path.join(EVIDENCE_DIR, "gears-first-run-web-demo-ticket-session_trace.jsonl");
const ARTIFACT_OPEN_EVIDENCE = path.join(EVIDENCE_DIR, "gears-first-run-artifact-open-evidence.json");

const VIEWPORT = { width: 1600, height: 900 } as const;
const PRD_IDEA =
  "Create a PRD for a notes service gear: every note must link to the project work it belongs to, and the headline metric is notes saved per session.";

let server: WebServer;

test.beforeAll(async () => {
  fs.mkdirSync(EVIDENCE_DIR, { recursive: true });
  server = await startWebServer({
    addr: ADDR,
    storiesDir: STORY_DIR,
    harness: "recording",
    record: SESSION_TRACE,
    ticketRepo: "constructorfabric/gears-rust",
    extraEnv: {
      KITSOKI_REPO: repoRoot,
      KITSOKI_TICKETS_ROOT: GEARS_ROOT,
    },
  });
});

test.afterAll(() => server?.stop());

async function currentState(page: Page): Promise<string> {
  return page.evaluate(() => {
    const el = document.querySelector('[data-testid="current-state"]');
    return el ? (el.textContent || "").trim() : "";
  });
}

async function waitForStateContains(page: Page, needle: string, timeoutMs = 45000): Promise<string> {
  const deadline = Date.now() + timeoutMs;
  let cur = "";
  while (Date.now() < deadline) {
    cur = await currentState(page);
    if (cur.toLowerCase().includes(needle.toLowerCase())) return cur;
    await page.waitForTimeout(300);
  }
  throw new Error(`state did not contain ${JSON.stringify(needle)}; last state was ${JSON.stringify(cur)}`);
}

async function submitIntent(
  page: Page,
  intent: string,
  slots: Record<string, unknown> = {},
  label?: string,
): Promise<void> {
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

async function shot(page: Page, name: string): Promise<string> {
  const out = path.join(EVIDENCE_DIR, `${name}.png`);
  await page.screenshot({ path: out, fullPage: true });
  return out;
}

async function scrollTranscriptToEnd(page: Page): Promise<void> {
  await page.evaluate(() => {
    const el = document.querySelector('[data-testid="chat-transcript"]') as HTMLElement | null;
    if (el) el.scrollTop = el.scrollHeight;
  });
}

async function copyNewestTrace(startedAtMs: number, outPath = SESSION_TRACE): Promise<string> {
  const sessionsDir = path.join(os.homedir(), ".kitsoki", "sessions", "gears-rust-dev");
  const deadline = Date.now() + 10000;
  let src = "";
  while (Date.now() < deadline) {
    const candidates = fs.existsSync(sessionsDir)
      ? fs
          .readdirSync(sessionsDir)
          .filter((name) => name.endsWith(".jsonl"))
          .map((name) => path.join(sessionsDir, name))
          .filter((p) => fs.statSync(p).mtimeMs >= startedAtMs - 5000 && fs.statSync(p).size > 0)
          .sort((a, b) => fs.statSync(b).mtimeMs - fs.statSync(a).mtimeMs)
      : [];
    if (candidates.length > 0) {
      src = candidates[0];
      break;
    }
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
  if (!src) throw new Error(`session trace not found under ${sessionsDir} after ${new Date(startedAtMs).toISOString()}`);
  fs.copyFileSync(src, outPath);
  return src;
}

async function startStorySession(page: Page): Promise<{ sessionId: string; startedAtMs: number }> {
  const startedAtMs = Date.now();
  await cinematicGoto(page, `${server.base}/#/`, { waitForTestId: "home-view", settleMs: 900 });
  await expect(page.getByTestId("story-card").first()).toBeVisible({ timeout: 20000 });
  const gearsCard = page.locator(`[data-testid="story-card"][data-story-path="${path.join(STORY_DIR, "app.yaml")}"]`);
  const card = (await gearsCard.count()) > 0 ? gearsCard.first() : page.getByTestId("story-card").first();
  await card.getByTestId("new-session-btn").click();
  await page.waitForURL(/#\/s\/[0-9a-f-]{36}\/chat$/, { timeout: 30000 });
  const sessionId = page.url().match(/#\/s\/([0-9a-f-]{36})\/chat$/)?.[1] ?? "";
  if (!sessionId) throw new Error(`could not parse session id from ${page.url()}`);
  await expect(page.getByTestId("chat-section")).toBeVisible({ timeout: 30000 });
  await expect(page.getByTestId("current-state")).toBeVisible({ timeout: 30000 });
  return { sessionId, startedAtMs };
}

test("records gears-rust first-run web journey against recording harness", async () => {
  test.setTimeout(420000);

  const browser: Browser = await chromium.launch({ headless: true });
  const context: BrowserContext = await browser.newContext({
    viewport: { ...VIEWPORT },
    deviceScaleFactor: 1,
  });
  const page: Page = await context.newPage();
  const opened: Array<{ label: string; path: string; exists: boolean }> = [];

  try {
    await cinematicGoto(page, `${server.base}/#/`, { waitForTestId: "home-view", settleMs: 1200 });
    await maybeInstallAutoRrwebCapture(page);
    await expect(page.getByTestId("story-card").first()).toBeVisible({ timeout: 20000 });
    opened.push({ label: "home", path: await shot(page, "01-home"), exists: true });

    const { sessionId, startedAtMs } = await startStorySession(page);
    opened.push({ label: "landing", path: await shot(page, "02-landing"), exists: true });

    await submitIntent(page, "core__go_profile_setup", { target: GEARS_ROOT }, "Configure provider profile");
    await waitForStateContains(page, "profile_setup");
    await dwell(page, 1200);
    opened.push({ label: "provider-discovery", path: await shot(page, "03-provider-discovery"), exists: true });

    await submitIntent(page, "core__profile_setup_discovered", {}, "Review detected provider setup");
    await waitForStateContains(page, "profile_setup");
    await dwell(page, 1200);
    opened.push({ label: "provider-review", path: await shot(page, "04-provider-review"), exists: true });

    await submitIntent(page, "core__skip_profile_setup", {}, "Continue without changing local provider config");
    await waitForStateContains(page, "landing");
    await dwell(page, 800);

    await submitIntent(page, "core__go_init", { target: GEARS_ROOT }, `Onboard ${GEARS_ROOT}`);
    await waitForStateContains(page, "init");
    await scrollTranscriptToEnd(page);
    opened.push({ label: "project-onboarding", path: await shot(page, "05-project-onboarding"), exists: true });

    await submitIntent(page, "core__init_discovered", {}, "Review discovered project profile");
    await waitForStateContains(page, "init");
    await scrollTranscriptToEnd(page);
    opened.push({ label: "project-onboarding-review", path: await shot(page, "06-project-onboarding-review"), exists: true });

    const sourceTrace = await copyNewestTrace(startedAtMs);

    const { sessionId: prdSessionId, startedAtMs: prdStartedAtMs } = await startStorySession(page);
    await submitIntent(page, "core__go_prd", {}, "Create a PRD");
    await waitForStateContains(page, "prd");
    await submitIntent(page, "core__prd__discuss", { message: PRD_IDEA }, PRD_IDEA);
    await scrollTranscriptToEnd(page);
    opened.push({ label: "prd-discussion", path: await shot(page, "07-prd-discussion"), exists: true });
    const sourcePrdTrace = await copyNewestTrace(prdStartedAtMs, PRD_SESSION_TRACE);

    const { sessionId: ticketSessionId, startedAtMs: ticketStartedAtMs } = await startStorySession(page);
    await submitIntent(page, "core__go_ticket_search", {}, "Browse open bug tickets");
    await waitForStateContains(page, "ticket");
    await scrollTranscriptToEnd(page);
    opened.push({ label: "ticket-search", path: await shot(page, "08-ticket-search"), exists: true });
    const sourceTicketTrace = await copyNewestTrace(ticketStartedAtMs, TICKET_SESSION_TRACE);
    fs.writeFileSync(
      ARTIFACT_OPEN_EVIDENCE,
      JSON.stringify(
        {
          opened,
          session_id: sessionId,
          source_trace: sourceTrace,
          session_trace: SESSION_TRACE,
          prd_session_id: prdSessionId,
          source_prd_trace: sourcePrdTrace,
          prd_session_trace: PRD_SESSION_TRACE,
          ticket_session_id: ticketSessionId,
          source_ticket_trace: sourceTicketTrace,
          ticket_session_trace: TICKET_SESSION_TRACE,
        },
        null,
        2,
      ),
    );
  } finally {
    await context.close();
    await browser.close();
  }
});
