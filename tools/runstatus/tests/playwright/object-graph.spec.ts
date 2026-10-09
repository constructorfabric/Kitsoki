/**
 * object-graph.spec.ts — W5.0's deterministic no-LLM gate: the project
 * object graph viewer at /#/graph, driven against a REAL `kitsoki web`
 * server, reading the seed catalog (docs/proposals/project-object-graph/
 * seed-objects.yaml) via runstatus.objectgraph.load — no session, no LLM.
 *
 * Per repo convention (AGENTS.md: prefer `go run` over building a binary),
 * this spec starts the server with `go run ./cmd/kitsoki web` rather than a
 * prebuilt bin/kitsoki.
 */
import { test, expect, chromium, type Browser, type Page } from "@playwright/test";
import { fileURLToPath } from "url";
import path from "path";
import fs from "fs";
import os from "os";
import { spawn, type ChildProcess } from "child_process";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const repoRoot = path.resolve(__dirname, "../../../..");
const STORIES_DIR = path.join(repoRoot, "stories");
const SEED_CATALOG = "docs/proposals/project-object-graph/seed-objects.yaml";

const ADDR = "127.0.0.1:7799";
const BASE = `http://${ADDR}`;

let server: ChildProcess | null = null;
let serverLog = "";
let tmpDbDir = "";
let browser: Browser;

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
  throw new Error(`server not healthy after ${timeoutMs}ms (last: ${lastErr})\n${serverLog}`);
}

test.beforeAll(async () => {
  for (const p of [STORIES_DIR, path.join(repoRoot, SEED_CATALOG)]) {
    if (!fs.existsSync(p)) throw new Error(`missing required path: ${p}`);
  }
  tmpDbDir = fs.mkdtempSync(path.join(os.tmpdir(), "kitsoki-objectgraph-"));
  server = spawn(
    "go",
    [
      "run", "./cmd/kitsoki", "web",
      "--stories-dir", STORIES_DIR,
      "--addr", ADDR,
      "--db", path.join(tmpDbDir, "s.db"),
    ],
    { cwd: repoRoot, stdio: ["ignore", "pipe", "pipe"] }
  );
  server.stdout?.on("data", (d) => (serverLog += d.toString()));
  server.stderr?.on("data", (d) => (serverLog += d.toString()));
  await waitForHealthy(30000);
  browser = await chromium.launch();
});

test.afterAll(async () => {
  await browser?.close();
  if (server && !server.killed) {
    server.kill("SIGTERM");
    await new Promise((r) => setTimeout(r, 300));
    if (!server.killed) server.kill("SIGKILL");
  }
  if (tmpDbDir) fs.rmSync(tmpDbDir, { recursive: true, force: true });
});

test("project object graph viewer renders the seed catalog as the primary, integrated view", async () => {
  const page: Page = await browser.newPage();
  await page.goto(`${BASE}/#/graph?catalog=${encodeURIComponent(SEED_CATALOG)}`);

  await page.waitForSelector('[data-testid="objectgraph-page"]', { timeout: 10000 });
  await page.waitForSelector('[data-testid="objectgraph-catalog"]', { timeout: 10000 });

  // The count badge proves the RPC round-tripped real data, not an empty
  // shell — the seed catalog has 68+ nodes as of W6.0.
  const countText = await page.locator('[data-testid="objectgraph-count"]').innerText();
  expect(countText).toMatch(/^\d+ nodes \/ \d+ edges$/);
  const nodeCount = Number(countText.match(/^(\d+) nodes/)?.[1] ?? "0");
  expect(nodeCount).toBeGreaterThan(60);

  // The catalog is the whole page — no mode switch to a separate graph view.
  const firstNodeButton = page.locator(".object-list button").first();
  await expect(firstNodeButton).toBeVisible();

  await page.close();
});

test("selecting an object renders its relationships as an inline Cytoscape graph", async () => {
  const page: Page = await browser.newPage();
  await page.goto(`${BASE}/#/graph?catalog=${encodeURIComponent(SEED_CATALOG)}`);
  await page.waitForSelector('[data-testid="objectgraph-catalog"]', { timeout: 10000 });

  const firstNodeButton = page.locator(".object-list button").first();
  const label = (await firstNodeButton.locator("strong").innerText()).trim();
  await firstNodeButton.click();
  await expect(page.locator(".focus-card h2")).toHaveText(label);

  // The relationship graph is embedded inline (not behind a mode toggle) and
  // renders the selected node's neighborhood via Cytoscape.
  const relationshipGraph = page.locator('[data-testid="relationship-graph"]');
  await expect(relationshipGraph).toBeVisible();
  await expect(relationshipGraph.locator('[data-testid="graph-view-host"] canvas').first()).toBeVisible();

  // Layout is pluggable: switching it re-runs without erroring the page.
  await relationshipGraph.locator('[data-testid="graph-view-layout"]').selectOption("cola");
  await expect(relationshipGraph.locator('[data-testid="graph-view-host"] canvas').first()).toBeVisible();

  // Node color is lifecycle-coded — a legend must spell out what each color
  // means, or the coding is unreadable.
  const legend = relationshipGraph.locator('[data-testid="graph-view-legend"] li');
  await expect(legend).toContainText(["Available", "Active", "Proof", "Roadmap", "Candidate"]);

  await page.close();
});

test("the inline relationship graph can pop out to a mostly full-screen modal", async () => {
  const page: Page = await browser.newPage();
  await page.goto(`${BASE}/#/graph?catalog=${encodeURIComponent(SEED_CATALOG)}`);
  await page.waitForSelector('[data-testid="objectgraph-catalog"]', { timeout: 10000 });

  const firstNodeButton = page.locator(".object-list button").first();
  const label = (await firstNodeButton.locator("strong").innerText()).trim();
  await firstNodeButton.click();
  await expect(page.locator(".focus-card h2")).toHaveText(label);

  await page.click('[data-testid="relationship-graph-expand"]');
  const modal = page.locator('[data-testid="relationship-graph-modal"]');
  await expect(modal).toBeVisible();
  await expect(modal.locator('[data-testid="graph-view-host"] canvas').first()).toBeVisible();

  // "Mostly" full-screen: a backdrop with margin, not edge-to-edge.
  const box = await modal.locator(".relationship-graph-modal").boundingBox();
  const viewport = page.viewportSize();
  expect(box).not.toBeNull();
  expect(viewport).not.toBeNull();
  if (box && viewport) {
    expect(box.width).toBeLessThan(viewport.width);
    expect(box.height).toBeLessThan(viewport.height);
  }

  await page.keyboard.press("Escape");
  await expect(modal).toHaveCount(0);

  await page.close();
});

test("the full graph is available as a de-emphasized overlay, not a primary toggle", async () => {
  const page: Page = await browser.newPage();
  await page.goto(`${BASE}/#/graph?catalog=${encodeURIComponent(SEED_CATALOG)}`);
  await page.waitForSelector('[data-testid="objectgraph-catalog"]', { timeout: 10000 });

  await expect(page.locator('[data-testid="objectgraph-view-catalog"]')).toHaveCount(0);
  await expect(page.locator('[data-testid="objectgraph-view-graph"]')).toHaveCount(0);

  await page.click('[data-testid="objectgraph-open-full-graph"]');
  const modal = page.locator('[data-testid="objectgraph-graph-modal"]');
  await expect(modal).toBeVisible();
  await expect(modal.locator('[data-testid="graph-view-host"] canvas').first()).toBeVisible();

  await page.click('[data-testid="objectgraph-close-full-graph"]');
  await expect(modal).toHaveCount(0);

  await page.close();
});

test("project object graph viewer surfaces a load error for a bad catalog path", async () => {
  const page: Page = await browser.newPage();
  await page.goto(`${BASE}/#/graph?catalog=${encodeURIComponent("does/not/exist.yaml")}`);

  await page.waitForSelector('[data-testid="objectgraph-error"]', { timeout: 10000 });
  await expect(page.locator('[data-testid="objectgraph-error"]')).not.toBeEmpty();

  await page.close();
});
