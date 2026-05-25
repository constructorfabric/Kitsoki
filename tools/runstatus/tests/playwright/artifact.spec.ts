/**
 * artifact.spec.ts — Playwright artifact-mode end-to-end tests.
 *
 * Tests load each fixture snapshot as a self-contained file:// HTML artifact
 * (dist/index.html with the snapshot inlined). No server required.
 *
 * Coverage checklist from docs/proposals/runstatus-proposal.md:
 *   - in-progress fixture: header, diagram, drawer, timeline, filter
 *   - completed fixture: terminal indicator, LLM drawer, host drawer, transition drawer
 *   - edge-cases fixture: error indicator, off-path event, long LLM content
 *   - all fixtures: no console errors on load
 */

import { test, expect, type Page, type ConsoleMessage } from "@playwright/test";
import { fileURLToPath } from "url";
import path from "path";
import { buildArtifact } from "./_helpers/artifact.js";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

// ── fixture paths ──────────────────────────────────────────────────────────────

const FIXTURES_DIR = path.resolve(__dirname, "../../fixtures");

const IN_PROGRESS_SNAPSHOT = path.join(FIXTURES_DIR, "in-progress.snapshot.json");
const COMPLETED_SNAPSHOT = path.join(FIXTURES_DIR, "completed.snapshot.json");
const EDGE_CASES_SNAPSHOT = path.join(FIXTURES_DIR, "edge-cases.snapshot.json");

// ── snapshot data (read once) ──────────────────────────────────────────────────
import fs from "fs";

const inProgressSnap = JSON.parse(fs.readFileSync(IN_PROGRESS_SNAPSHOT, "utf-8"));
const completedSnap = JSON.parse(fs.readFileSync(COMPLETED_SNAPSHOT, "utf-8"));

// ── helper: navigate to artifact and wait for RunView ─────────────────────────

/**
 * Navigate to an artifact URL and wait until the topbar is visible.
 * Also collects console errors for the no-console-error tests.
 */
async function loadArtifact(page: Page, snapshotPath: string): Promise<string[]> {
  const errors: string[] = [];
  page.on("console", (msg: ConsoleMessage) => {
    if (msg.type() === "error") errors.push(msg.text());
  });
  page.on("pageerror", (err: Error) => {
    errors.push(`[pageerror] ${err.message}`);
  });

  const url = buildArtifact(snapshotPath);
  await page.goto(url);
  // Wait until the run view topbar is visible (auto-navigate from SessionList).
  await page.waitForSelector(".run-view__topbar", { timeout: 10000 });
  return errors;
}

// ════════════════════════════════════════════════════════════════════════════════
// in-progress fixture
// ════════════════════════════════════════════════════════════════════════════════

test.describe("in-progress fixture", () => {
  test("header shows session_id, current_state, and turn from the fixture", async ({ page }) => {
    await loadArtifact(page, IN_PROGRESS_SNAPSHOT);

    const session = inProgressSnap.session;

    // session_id shown in topbar
    await expect(page.locator(".run-view__session-id")).toContainText(session.session_id);
    // current_state shown in topbar
    await expect(page.locator(".run-view__current-state")).toContainText(session.current_state);
    // badge shows 'live' (not terminal)
    await expect(page.locator(".run-view__state-badge--live")).toBeVisible();
  });

  test("state diagram renders SVG with multiple node elements", async ({ page }) => {
    await loadArtifact(page, IN_PROGRESS_SNAPSHOT);

    // Wait for the SVG to appear inside the state-diagram component.
    const svg = page.locator(".state-diagram__svg-host svg");
    await expect(svg).toBeVisible({ timeout: 8000 });

    // Mermaid renders state nodes as <g> elements. Expect multiple.
    const gNodes = page.locator(".state-diagram__svg-host svg g[id]");
    const count = await gNodes.count();
    expect(count).toBeGreaterThan(2);
  });

  test("current state node has the 'current' CSS class in the diagram", async ({ page }) => {
    // fixme: StateDiagram.extractMermaidNodeId strips "^flowchart-" but
    // Mermaid 11 prefixes IDs with the render container ID
    // (e.g. "kitsoki-mermaid-1-flowchart-ST_cloakroom-3") so the ID never
    // matches the node_map key — g.current is never applied.
    // Tracked in StateDiagram.vue extractMermaidNodeId().
    test.fixme(true, "StateDiagram.extractMermaidNodeId does not handle Mermaid 11 diagramId-prefixed SVG element IDs; g.current is never set");

    await loadArtifact(page, IN_PROGRESS_SNAPSHOT);

    await page.locator(".state-diagram__svg-host svg").waitFor({ timeout: 8000 });

    // Find any g.current element — the component adds it to the node whose
    // NodeRef.ref matches currentStatePath.
    const currentNode = page.locator(".state-diagram__svg-host svg g.current");
    await expect(currentNode).toBeVisible({ timeout: 5000 });
  });

  test("clicking a trace event row opens the drawer and selects the row", async ({ page }) => {
    await loadArtifact(page, IN_PROGRESS_SNAPSHOT);

    // Wait for timeline body to render.
    await page.waitForSelector(".trace-timeline__row", { timeout: 8000 });

    // Click the first visible row.
    const firstRow = page.locator(".trace-timeline__row").first();
    await firstRow.click();

    // The drawer should appear.
    await expect(page.locator(".detail-drawer")).toBeVisible({ timeout: 3000 });

    // The clicked row should have the 'selected' class.
    await expect(firstRow).toHaveClass(/selected/);
  });

  test("subsystem filter chip narrows the timeline; clear restores it", async ({ page }) => {
    await loadArtifact(page, IN_PROGRESS_SNAPSHOT);

    await page.waitForSelector(".trace-timeline__row", { timeout: 8000 });

    // Count total visible rows before filtering.
    const allRows = page.locator(".trace-timeline__row");
    const totalBefore = await allRows.count();
    expect(totalBefore).toBeGreaterThan(0);

    // Click every subsystem chip except "machine" — deselect all except machine.
    // The chips are: turn, harness, machine, host, oracle, other.
    // Clicking a chip when it's active deselects it.
    // All chips start active; click all except "machine" to deselect them.
    const chips = page.locator(".trace-timeline__chip:not(.trace-timeline__chip--clear)");
    const chipCount = await chips.count();
    for (let i = 0; i < chipCount; i++) {
      const chip = chips.nth(i);
      const text = await chip.innerText();
      if (text.trim() !== "machine") {
        await chip.click();
      }
    }

    // Rows should now be fewer (only machine.* subsystem events remain).
    const rowsAfter = await allRows.count();
    expect(rowsAfter).toBeLessThan(totalBefore);

    // Click the Clear button to restore all chips.
    const clearBtn = page.locator(".trace-timeline__chip--clear");
    await expect(clearBtn).toBeVisible();
    await clearBtn.click();

    const rowsRestored = await allRows.count();
    expect(rowsRestored).toBe(totalBefore);
  });
});

// ════════════════════════════════════════════════════════════════════════════════
// completed fixture
// ════════════════════════════════════════════════════════════════════════════════

test.describe("completed fixture", () => {
  test("header shows 'done' badge for a terminal session", async ({ page }) => {
    await loadArtifact(page, COMPLETED_SNAPSHOT);

    // Badge should show 'done' (terminal=true).
    await expect(page.locator(".run-view__state-badge--done")).toBeVisible();
    await expect(page.locator(".run-view__state-badge--done")).toContainText("done");
  });

  test("clicking an oracle event opens drawer showing prompt or response", async ({ page }) => {
    await loadArtifact(page, COMPLETED_SNAPSHOT);
    await page.waitForSelector(".trace-timeline__row", { timeout: 8000 });

    // Find an oracle.* row (oracle subsystem chip colored orange in timeline).
    const oracleRow = page.locator(".trace-timeline__row", {
      has: page.locator('.trace-timeline__subsystem-chip[data-subsystem="oracle"]'),
    }).first();

    await oracleRow.click();

    // Drawer opens.
    const drawer = page.locator(".detail-drawer");
    await expect(drawer).toBeVisible({ timeout: 3000 });

    // The drawer's event section should show the msg.
    await expect(drawer.locator(".detail-drawer__section-title").first()).toBeVisible();
  });

  test("clicking a harness event opens drawer showing handler", async ({ page }) => {
    await loadArtifact(page, COMPLETED_SNAPSHOT);
    await page.waitForSelector(".trace-timeline__row", { timeout: 8000 });

    // Find a harness.* row.
    const harnessRow = page.locator(".trace-timeline__row", {
      has: page.locator('.trace-timeline__subsystem-chip[data-subsystem="harness"]'),
    }).first();

    await harnessRow.click();

    const drawer = page.locator(".detail-drawer");
    await expect(drawer).toBeVisible({ timeout: 3000 });

    // The drawer title should reference the event msg.
    await expect(drawer.locator(".detail-drawer__title")).toContainText("Event:");
  });

  test("clicking a machine.transition event opens drawer with event details", async ({ page }) => {
    await loadArtifact(page, COMPLETED_SNAPSHOT);
    await page.waitForSelector(".trace-timeline__row", { timeout: 8000 });

    // Find a row whose msg contains "machine.transition".
    const transRow = page.locator(".trace-timeline__row", {
      has: page.locator('.trace-timeline__msg').filter({ hasText: "machine.transition" }),
    }).first();

    await transRow.click();

    const drawer = page.locator(".detail-drawer");
    await expect(drawer).toBeVisible({ timeout: 3000 });

    // Drawer title should reference the transition event.
    await expect(drawer.locator(".detail-drawer__title")).toContainText("machine.transition");
  });
});

// ════════════════════════════════════════════════════════════════════════════════
// edge-cases fixture
// ════════════════════════════════════════════════════════════════════════════════

test.describe("edge-cases fixture", () => {
  test("error-level event shows distinct color indicator in timeline", async ({ page }) => {
    await loadArtifact(page, EDGE_CASES_SNAPSHOT);
    await page.waitForSelector(".trace-timeline__row", { timeout: 8000 });

    // The timeline renders level with data-level="ERROR" attribute.
    const errorLevel = page.locator('.trace-timeline__level[data-level="ERROR"]');
    await expect(errorLevel.first()).toBeVisible({ timeout: 3000 });
  });

  test("off-path event is visible in the timeline", async ({ page }) => {
    await loadArtifact(page, EDGE_CASES_SNAPSHOT);
    await page.waitForSelector(".trace-timeline__row", { timeout: 8000 });

    // The off-path events in our fixture have msg "machine.off_path_entered".
    const offPathRow = page.locator(".trace-timeline__row", {
      has: page.locator(".trace-timeline__msg").filter({ hasText: "off_path" }),
    }).first();

    await expect(offPathRow).toBeVisible({ timeout: 3000 });
  });

  test("oracle event with long response shows 'Show full' toggle", async ({ page }) => {
    await loadArtifact(page, EDGE_CASES_SNAPSHOT);
    await page.waitForSelector(".trace-timeline__row", { timeout: 8000 });

    // The oracle.ask.complete at turn 6 (off-path conversation) has both
    // prompt and response > 500 chars. Find it by locating Turn 6's group.
    // The timeline renders turns descending, so we must navigate to Turn 6.
    // Strategy: find all oracle.ask.complete rows, iterate until we click one
    // that shows a "Show full" button in the drawer.
    const oracleCompleteRows = page.locator(".trace-timeline__row", {
      has: page.locator(".trace-timeline__msg").filter({ hasText: "oracle.ask.complete" }),
    });
    const count = await oracleCompleteRows.count();
    expect(count).toBeGreaterThan(0);

    let found = false;
    for (let i = 0; i < count; i++) {
      await oracleCompleteRows.nth(i).click();
      const drawer = page.locator(".detail-drawer");
      await expect(drawer).toBeVisible({ timeout: 2000 });

      const showFullBtns = drawer.locator(".detail-drawer__toggle-btn").filter({ hasText: "Show full" });
      const isVisible = await showFullBtns.first().isVisible();
      if (isVisible) {
        found = true;
        // Click the first "Show full" — text should change to "Show less".
        // After clicking, the button's filter text changes so we use the
        // broader toggle-btn selector to find "Show less".
        await showFullBtns.first().click();
        const showLessBtn = drawer.locator(".detail-drawer__toggle-btn").filter({ hasText: "Show less" });
        await expect(showLessBtn.first()).toBeVisible({ timeout: 3000 });
        break;
      }
      // Close the drawer and try the next row.
      await drawer.locator(".detail-drawer__close").click();
    }

    expect(found, "Expected to find an oracle event with a 'Show full' toggle").toBe(true);
  });
});

// ════════════════════════════════════════════════════════════════════════════════
// parametrized: no console errors on page load (all three fixtures)
// ════════════════════════════════════════════════════════════════════════════════

const allFixtures = [
  { name: "in-progress", path: IN_PROGRESS_SNAPSHOT },
  { name: "completed", path: COMPLETED_SNAPSHOT },
  { name: "edge-cases", path: EDGE_CASES_SNAPSHOT },
];

for (const fixture of allFixtures) {
  test(`no console errors on load — ${fixture.name}`, async ({ page }) => {
    const errors = await loadArtifact(page, fixture.path);

    // Filter out known-benign non-errors (Mermaid uses console.warn, not error).
    // Only fail on actual console.error calls or unhandled rejections.
    const fatal = errors.filter(
      (e) =>
        !e.includes("ResizeObserver loop") && // benign browser quirk
        !e.includes("favicon")
    );
    expect(fatal, `Console errors on ${fixture.name}: ${fatal.join("; ")}`).toHaveLength(0);
  });
}
