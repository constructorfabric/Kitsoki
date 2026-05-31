/**
 * bugfix.spec.ts — exercises the click-to-highlight behaviour and oracle-trace
 * fidelity using the bugfix snapshot (produced by
 * `make -C tools/runstatus/fixtures bugfix`, which drives
 * stories/bugfix/flows/happy_human.yaml through the real orchestrator with a
 * host cassette — so oracle metadata is captured and replayed, and the
 * snapshot carries oracle.<verb>.start/.complete events with full prompt and
 * response attrs).
 */

import { test, expect, type Page } from "@playwright/test";
import { fileURLToPath } from "url";
import path from "path";
import fs from "fs";
import { buildArtifact } from "./_helpers/artifact.js";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

const FIXTURES_DIR = path.resolve(__dirname, "../../fixtures");
const BUGFIX_SNAPSHOT = path.join(FIXTURES_DIR, "bugfix.snapshot.json");

async function load(page: Page): Promise<void> {
  const url = buildArtifact(BUGFIX_SNAPSHOT);
  await page.goto(url);
  await page.waitForSelector(".run-view__topbar", { timeout: 10000 });
}

test.describe("bugfix fixture", () => {
  test("renders phase cards in topological order from idle", async ({ page }) => {
    await load(page);
    const phases = page.locator(".state-diagram__phase .state-diagram__phase-name");
    await expect(phases.first()).toBeVisible();
    const names = await phases.allTextContents();
    // First five should follow the canonical bugfix flow; exits come last.
    expect(names.slice(0, 5)).toEqual([
      "idle",
      "reproducing",
      "proposing",
      "implementing",
      "testing",
    ]);
  });

  test("trace timeline turn headers are grouped under real phase names, not '—'", async ({ page }) => {
    await load(page);
    await page.waitForSelector(".trace-timeline__phase-header", { timeout: 8000 });

    const phaseNames = await page
      .locator(".trace-timeline__phase-header .trace-timeline__turn-phase")
      .allTextContents();

    // Regression: every event used to carry an empty state_path, so every
    // phase header collapsed to the "—" fallback. The faithful trace now
    // resolves each turn group to its room's phase.
    expect(phaseNames.length).toBeGreaterThan(1);
    expect(phaseNames).not.toContain("—");
    // The canonical bugfix flow visits these phases in order.
    expect(phaseNames).toContain("reproducing");
    expect(phaseNames).toContain("proposing");
    expect(phaseNames).toContain("done");
  });

  test("clicking a room highlights every event whose state_path falls under it", async ({ page }) => {
    await load(page);

    // Click the "reproducing" room.
    const reproducing = page
      .locator(".state-diagram__room")
      .filter({ hasText: "reproducing" })
      .first();
    await reproducing.click();

    // The room itself should be highlighted (orange ring).
    await expect(reproducing).toHaveClass(/state-diagram__room--highlight/);

    // The "clear highlight" pill should appear in the timeline panel header.
    await expect(page.locator(".run-view__clear-highlight")).toBeVisible();

    // Several timeline rows should pick up the .highlighted class.
    // (machine.* and turn.* rows are filtered by default, so the count
    // reflects the oracle + host + world rows with state_path='reproducing':
    // 2 host-call rows + 1 merged oracle row + 1 grouped world.update row.)
    const highlighted = page.locator(".trace-timeline__row.highlighted");
    expect(await highlighted.count()).toBeGreaterThanOrEqual(4);
  });

  test("clicking a phase header highlights all of its rooms' events", async ({ page }) => {
    await load(page);

    // Click the "testing" phase header.
    const phaseHeader = page
      .locator(".state-diagram__phase-header")
      .filter({ hasText: /\btesting\b/ })
      .first();
    await phaseHeader.click();

    // Highlighted rows in the timeline.  The TraceTimeline is virtualised,
    // so only the currently-rendered window of rows can carry the .highlighted
    // class — but at least the first matching row must scroll into view.
    await expect(page.locator(".run-view__clear-highlight")).toBeVisible();
    const highlighted = page.locator(".trace-timeline__row.highlighted");
    expect(await highlighted.count()).toBeGreaterThan(0);
  });

  test("clear-highlight pill removes the highlight", async ({ page }) => {
    await load(page);

    await page
      .locator(".state-diagram__room")
      .filter({ hasText: "reproducing" })
      .first()
      .click();
    await expect(page.locator(".run-view__clear-highlight")).toBeVisible();

    await page.locator(".run-view__clear-highlight").click();
    await expect(page.locator(".run-view__clear-highlight")).toHaveCount(0);
    await expect(page.locator(".trace-timeline__row.highlighted")).toHaveCount(0);
  });

  // ── Phase 8 oracle-trace assertions ──────────────────────────────────────────

  test("snapshot carries at least one oracle.<verb>.complete event", async ({ page }) => {
    // Verify directly in the snapshot JSON before loading the UI — if the
    // cassette replay didn't write KindOracleCall journal entries, this fails
    // fast with a clear message rather than a Playwright selector timeout.
    const snap = JSON.parse(fs.readFileSync(BUGFIX_SNAPSHOT, "utf-8"));
    const oracleCompletes = (snap.events as Array<{ msg: string; attrs: Record<string, unknown> }>)
      .filter((e) => /^oracle\.[a-z]+\.complete$/.test(e.msg));

    expect(
      oracleCompletes.length,
      `Expected ≥1 oracle.<verb>.complete event in ${BUGFIX_SNAPSHOT} but found 0. ` +
        "Did the cassette replay write KindOracleCall journal entries?"
    ).toBeGreaterThan(0);
  });

  test("canonical oracle.call events carry a prompt reference (start) and response (complete)", async ({ page }) => {
    // Pre-flight: assert the canonical trace shape in the snapshot JSON so the
    // Playwright DOM test below is meaningful. The engine emits oracle.call.start
    // (verb in attrs.verb) carrying a prompt reference — inline `prompt` for
    // small prompts, else a `prompt_file` sidecar — and oracle.call.complete
    // carrying the `response`. (If this fails, the fromhistory pipeline isn't
    // merging journal attrs correctly.)
    const snap = JSON.parse(fs.readFileSync(BUGFIX_SNAPSHOT, "utf-8"));
    const events = snap.events as Array<{ msg: string; attrs: Record<string, unknown> }>;
    const oracleStarts = events.filter((e) => e.msg === "oracle.call.start");
    const oracleCompletes = events.filter((e) => e.msg === "oracle.call.complete");

    expect(oracleStarts.length, "Expected ≥1 oracle.call.start event").toBeGreaterThan(0);
    expect(oracleCompletes.length, "Expected ≥1 oracle.call.complete event").toBeGreaterThan(0);

    // At least one start event must carry a prompt reference: inline `prompt`
    // or a `prompt_file` sidecar ref. (build-artifact inlines the sidecar into
    // `prompt` for the artifact, so the detail pane can render it under file://.)
    const withPrompt = oracleStarts.filter(
      (e) =>
        (typeof e.attrs.prompt === "string" && (e.attrs.prompt as string).length > 0) ||
        (typeof e.attrs.prompt_file === "string" && (e.attrs.prompt_file as string).length > 0)
    );
    expect(
      withPrompt.length,
      "Expected at least one oracle.call.start event to carry a prompt or prompt_file reference"
    ).toBeGreaterThan(0);

    // At least one complete event must carry a non-empty response.
    // Response may be stored as a string or object (AskDetail handles both).
    const withResponse = oracleCompletes.filter((e) => {
      const r = e.attrs.response;
      if (typeof r === "string") return r.length > 0;
      if (typeof r === "object" && r !== null) return true;
      return false;
    });
    expect(
      withResponse.length,
      "Expected at least one oracle.call.complete event to carry a non-empty response attr"
    ).toBeGreaterThan(0);
  });

  test("clicking an oracle.complete row opens OracleDetail with non-empty prompt pane", async ({
    page,
  }) => {
    await load(page);
    await page.waitForSelector(".trace-timeline__row", { timeout: 8000 });

    // Find a merged oracle.<verb> row in the timeline.
    // The timeline merges oracle.start+complete pairs into a single row whose
    // .trace-timeline__msg text reads "oracle.<verb>" (e.g. "oracle.task").
    const oracleCompleteRow = page.locator(".trace-timeline__row", {
      has: page.locator(".trace-timeline__msg").filter({ hasText: /^oracle\.[a-z]+$/ }),
    }).first();

    await expect(oracleCompleteRow).toBeVisible({ timeout: 5000 });
    await oracleCompleteRow.click();

    // Row expands inline — look inside the row's body, not a separate drawer.
    const rowBody = oracleCompleteRow.locator(".trace-timeline__row-body");
    await expect(rowBody).toBeVisible({ timeout: 3000 });

    // The OracleDetail verb badge must be visible (confirms OracleDetail rendered).
    await expect(rowBody.locator(".oracle-detail__verb-badge")).toBeVisible({ timeout: 3000 });

    // The prompt pane: CollapsibleText renders a .ct-pre when text is non-empty.
    // TaskDetail uses CollapsibleText for "Prompt"; AskDetail also uses it.
    // We assert that at least one .ct-pre is non-empty, confirming the prompt
    // attr reached the sub-renderer.
    const promptPre = rowBody.locator(".ct-pre").first();
    await expect(promptPre).toBeVisible({ timeout: 3000 });
    const promptText = await promptPre.innerText();
    expect(promptText.trim().length, "Expected the prompt pane to be non-empty").toBeGreaterThan(0);
  });

  test("clicking an oracle.complete row shows non-empty response in OracleDetail", async ({
    page,
  }) => {
    await load(page);
    await page.waitForSelector(".trace-timeline__row", { timeout: 8000 });

    // Find merged oracle.<verb> rows — the timeline merges start+complete pairs
    // into a single row whose .trace-timeline__msg reads "oracle.<verb>".
    const oracleCompleteRows = page.locator(".trace-timeline__row", {
      has: page.locator(".trace-timeline__msg").filter({ hasText: /^oracle\.[a-z]+$/ }),
    });
    const count = await oracleCompleteRows.count();
    expect(count, "Expected at least one oracle row in the timeline").toBeGreaterThan(0);

    // Iterate rows to find one whose inline expansion shows a non-empty response.
    // (Some verbs render response as .od-pre--response; tasks render it via
    // CollapsibleText or the Transcript tab. We look for any non-empty .od-pre
    // or .ct-pre in the expanded row body.)
    let foundNonEmptyResponse = false;
    for (let i = 0; i < Math.min(count, 5); i++) {
      const row = oracleCompleteRows.nth(i);
      await row.click();

      // Row expands inline — look inside the row's body.
      const rowBody = row.locator(".trace-timeline__row-body");
      await expect(rowBody).toBeVisible({ timeout: 2000 });

      // Check for any pre block (CollapsibleText or response) with content.
      const pres = rowBody.locator(".ct-pre, .od-pre");
      const preCount = await pres.count();
      for (let j = 0; j < preCount; j++) {
        const text = await pres.nth(j).innerText();
        if (text.trim().length > 0) {
          foundNonEmptyResponse = true;
          break;
        }
      }
      if (foundNonEmptyResponse) break;

      // Collapse row and try next.
      await row.click();
    }

    expect(
      foundNonEmptyResponse,
      "Expected to find a non-empty prompt/response pane in at least one oracle row body"
    ).toBe(true);
  });
});
