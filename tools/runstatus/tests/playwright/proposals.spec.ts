/**
 * proposals.spec.ts — functional regression for the proposals inbox surface
 * (ad-hoc-workbench slice 4, web side):
 *
 *   - ProposalsBadge renders the queued-proposal count in the session topbar.
 *   - Clicking the badge surfaces the head proposal in the SAME operator-question
 *     card (accept / refine / dismiss).
 *   - Accepting submits the verdict over the shared answer_question gesture and
 *     dismisses the card, decrementing the badge.
 *
 * Runs a REAL `kitsoki web` server in the deterministic no-LLM posture (--flow)
 * on its OWN port + db. The proposals are seeded through the window.__pushProposal
 * demo seam (registered by InteractiveView onMounted) — the runtime miner needs a
 * real LLM to PRODUCE proposals, which we never invoke in tests; the seam injects
 * realistic proposals straight into the store so the REAL badge + card render.
 * The injected proposals carry "demo-" ids, so the card's submit resolves locally
 * (no parked backend entry to 404 against) — the same short-circuit the
 * operator-question demo seam uses. Rendering + selection UX is unchanged.
 */
import { test, expect, chromium, type Browser, type BrowserContext, type Page } from "@playwright/test";
import { fileURLToPath } from "url";
import path from "path";
import fs from "fs";
import os from "os";
import { spawn, type ChildProcess } from "child_process";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

// repoRoot = .../kitsoki (tools/runstatus/tests/playwright -> 4 up)
const repoRoot = path.resolve(__dirname, "../../../..");
const BIN = path.join(repoRoot, "bin", "kitsoki");
const STORIES_DIR = path.join(repoRoot, "stories");
const FLOW = path.join(repoRoot, "stories", "prd", "flows", "happy_path.yaml");

// Distinct port from the other web specs so servers can run in parallel.
const ADDR = "127.0.0.1:7751";
const BASE = `http://${ADDR}`;
const RPC = `${BASE}/rpc`;

// Seeded proposals (one structure, one write-mode) — deterministic, demo- ids.
const STRUCTURE_PROPOSAL = {
  id: "demo-prop-1",
  kind: "structure",
  title: "Capture `make render` after every doc edit?",
  detail: "Recurring across the last 8 sessions.",
};
const WRITE_MODE_PROPOSAL = {
  id: "demo-prop-2",
  kind: "write_mode",
  title: "May I edit docs/architecture/ambient-mining.md?",
};

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

/** Inject a proposal via the deterministic demo seam. */
async function pushProposal(page: Page, proposal: unknown): Promise<void> {
  await page.evaluate((json: string) => {
    (window as unknown as { __pushProposal?: (s: string) => void }).__pushProposal?.(json);
  }, JSON.stringify(proposal));
}

test.beforeAll(async () => {
  for (const p of [STORIES_DIR, FLOW, BIN]) {
    if (!fs.existsSync(p)) throw new Error(`missing required path: ${p} (run 'make build' first)`);
  }
  tmpDbDir = fs.mkdtempSync(path.join(os.tmpdir(), "kitsoki-proposals-"));
  const dbPath = path.join(tmpDbDir, "s.db");

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
  if (server && !server.killed) {
    server.kill("SIGTERM");
    await new Promise((r) => setTimeout(r, 500));
    if (!server.killed) server.kill("SIGKILL");
  }
  if (tmpDbDir) fs.rmSync(tmpDbDir, { recursive: true, force: true });
});

test.describe("proposals inbox", () => {
  test("badge shows the count; click opens the card; accept submits over answer_question", async () => {
    test.setTimeout(60000);

    const stories = await rpc<Array<{ path: string; app_id: string }>>("runstatus.stories.list", {});
    const prd = stories.find((s) => s.app_id === "prd");
    expect(prd, "PRD story is in the catalogue").toBeTruthy();
    const { session_id: sid } = await rpc<{ session_id: string }>(
      "runstatus.session.new",
      { story_path: prd!.path },
    );
    expect(sid).toBeTruthy();

    const browser: Browser = await chromium.launch();
    const context: BrowserContext = await browser.newContext({ viewport: { width: 1280, height: 800 } });
    const page = await context.newPage();
    try {
      await page.goto(`${BASE}/#/s/${sid}/chat`);
      await expect(page.getByTestId("chat-section")).toBeVisible({ timeout: 15000 });

      // No proposals → no badge (the hide-when-zero contract).
      await expect(page.getByTestId("proposals-badge")).toHaveCount(0);

      // Seed two proposals (structure + write-mode). The badge appears with the
      // count, and the write-mode opt-in flips it to the attention variant.
      await pushProposal(page, STRUCTURE_PROPOSAL);
      await pushProposal(page, WRITE_MODE_PROPOSAL);

      const badge = page.getByTestId("proposals-badge");
      await expect(badge).toBeVisible({ timeout: 5000 });
      await expect(page.getByTestId("proposals-badge-count")).toHaveText("2");
      await expect(badge).toHaveAttribute("data-attention", "true");

      // Click the badge → the head proposal opens in the operator-question card,
      // reusing the operator-question testids (the SAME surface).
      await badge.click();
      const modal = page.getByTestId("operator-question-modal");
      await expect(modal).toBeVisible({ timeout: 5000 });
      // The head is the structure proposal; the card shows accept/refine/dismiss.
      await expect(modal).toContainText("make render");
      await expect(page.getByTestId("oq-option-0-0")).toContainText("accept");
      await expect(page.getByTestId("oq-option-0-2")).toContainText("dismiss");

      // Badge dropped to 1 the moment the head moved into the card.
      await expect(page.getByTestId("proposals-badge-count")).toHaveText("1");

      // Accept and submit — resolves locally (demo- id) and dismisses the card.
      await page.getByTestId("oq-option-0-0").click();
      await page.getByTestId("oq-submit").click();
      await expect(modal).toHaveCount(0, { timeout: 5000 });

      // One proposal (the write-mode opt-in) still queued; badge still shows 1.
      await expect(page.getByTestId("proposals-badge-count")).toHaveText("1");
    } finally {
      await page.close();
      await context.close();
      await browser.close();
    }
  });
});
