/**
 * agent-prompts.spec.ts — artifact-mode coverage for the separate-prompt-file
 * feature (usePromptLoader).
 *
 * Some agent traces store large prompts/responses in sidecar files and carry
 * only `prompt_file` / `system_prompt_file` paths on the agent.<verb>.complete
 * event (no inline `prompt`). The agent sub-renderers feed those attrs through
 * usePromptLoader, which fetch()es the referenced file and shows its contents
 * in the CollapsibleText panes.
 *
 * Like every spec here this runs in artifact mode: buildArtifact() inlines the
 * snapshot into dist/index.html and the test navigates a file:// URL — no dev
 * server. Two consequences of file:// that this spec pins down:
 *
 *   1. A distributed artifact cannot fetch the sidecar files (the browser
 *      blocks file:// fetch and the files aren't co-located), so the loader's
 *      graceful "[Prompt file: … - could not load]" placeholder is what a real
 *      shared artifact shows. Test: "degrades gracefully …".
 *   2. When the fetch DOES resolve (here: a window.fetch stub serving the real
 *      sidecar files), the returned text flows all the way to the DOM. Test:
 *      "displays prompt contents …". This is the success path a static host
 *      that serves /agent-prompts/*.txt would exercise.
 *
 * page.route() is deliberately NOT used to serve the files — it does not
 * intercept file:// requests, so the stub is injected at the window.fetch level.
 */

import { test, expect, type Page } from "@playwright/test";
import { fileURLToPath } from "url";
import path from "path";
import fs from "fs";
import { buildArtifact } from "./_helpers/artifact.js";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

const FIXTURES_DIR = path.resolve(__dirname, "../../fixtures");
const SNAPSHOT = path.join(FIXTURES_DIR, "agent-with-separate-prompts.snapshot.json");
const PROMPTS_DIR = path.join(FIXTURES_DIR, "agent-prompts");

/** Read every sidecar prompt file into a { "agent-prompts/<name>": text } map. */
function readPromptFiles(): Record<string, string> {
  const map: Record<string, string> = {};
  for (const name of fs.readdirSync(PROMPTS_DIR)) {
    map[`agent-prompts/${name}`] = fs.readFileSync(path.join(PROMPTS_DIR, name), "utf-8");
  }
  return map;
}

/**
 * Replace window.fetch (before the SPA boots) with a stub that serves the
 * sidecar prompt files by URL suffix and 404s everything else. This stands in
 * for the static host that would serve /agent-prompts/*.txt in production —
 * file:// can't, so the test supplies the bytes.
 */
async function stubPromptFetch(page: Page): Promise<void> {
  await page.addInitScript((files: Record<string, string>) => {
    window.fetch = (async (input: RequestInfo | URL) => {
      const url = String(input);
      const key = Object.keys(files).find((k) => url.endsWith(k));
      return key
        ? new Response(files[key], { status: 200 })
        : new Response("", { status: 404 });
    }) as typeof fetch;
  }, readPromptFiles());
}

async function load(page: Page): Promise<void> {
  await page.goto(buildArtifact(SNAPSHOT));
  await page.waitForSelector(".trace-timeline__row", { timeout: 10000 });
}

/**
 * The merged agent.decide row. The timeline pairs agent.decide.start with
 * its .complete (shared call_id) into one row whose msg reads "agent.decide";
 * the expanded body renders AgentDetail against the .complete attrs (which
 * carry the prompt_file paths).
 */
function decideRow(page: Page) {
  return page
    .locator(".trace-timeline__row", {
      has: page.locator(".trace-timeline__msg").filter({ hasText: /^agent\.decide$/ }),
    })
    .first();
}

test.describe("agent separate-prompt files (artifact mode)", () => {
  test("fixture's agent.complete events reference sidecar prompt files, not inline prompts", () => {
    // Pre-flight on the snapshot JSON so the DOM tests below are meaningful: if
    // the generator stopped emitting prompt_file (or started inlining prompt),
    // this fails fast with a clear message instead of a selector timeout.
    const snap = JSON.parse(fs.readFileSync(SNAPSHOT, "utf-8"));
    const completes = (snap.events as Array<{ msg: string; attrs: Record<string, unknown> }>)
      .filter((e) => /^agent\.[a-z]+\.complete$/.test(e.msg));

    expect(completes.length, "expected ≥1 agent.<verb>.complete event").toBeGreaterThan(0);

    const withPromptFile = completes.filter(
      (e) => typeof e.attrs.prompt_file === "string" && (e.attrs.prompt_file as string).length > 0
    );
    expect(
      withPromptFile.length,
      "expected at least one agent.complete to carry a prompt_file path"
    ).toBeGreaterThan(0);

    // The feature only engages when the prompt is NOT inlined — guard that.
    for (const e of withPromptFile) {
      expect(
        e.attrs.prompt,
        "prompt_file events must not also carry an inline prompt (loader prefers inline)"
      ).toBeUndefined();
    }

    // Every referenced sidecar file must exist on disk.
    for (const e of withPromptFile) {
      const rel = e.attrs.prompt_file as string;
      expect(
        fs.existsSync(path.join(FIXTURES_DIR, rel)),
        `sidecar prompt file missing: ${rel}`
      ).toBe(true);
    }
  });

  test("displays prompt contents fetched from the sidecar files", async ({ page }) => {
    await stubPromptFetch(page);
    await load(page);

    const row = decideRow(page);
    await expect(row).toBeVisible({ timeout: 5000 });
    await row.click();

    const body = row.locator(".trace-timeline__row-body");
    await expect(body).toBeVisible({ timeout: 3000 });
    // AgentDetail rendered (confirms we routed to the verb sub-renderer).
    await expect(body.locator(".agent-detail__verb-badge")).toBeVisible({ timeout: 3000 });

    // Since Slice #2 the prompts live inside the collapsed evidence drawer — expand it first.
    const evidenceToggle = body.locator("[data-testid='decide-evidence-toggle']");
    if (await evidenceToggle.isVisible()) {
      await evidenceToggle.click();
    }

    // The system-prompt and prompt panes show the actual sidecar contents.
    // (toContainText auto-retries while usePromptLoader's async fetch settles.)
    await expect(body).toContainText("strategy router for the bugfix pipeline");
    await expect(body).toContainText("start BUG-4711");

    // The "could not load" placeholder must be absent on the success path.
    await expect(body).not.toContainText("could not load");
  });

  test("degrades gracefully when the sidecar files are unreachable", async ({ page }) => {
    // No fetch stub: this is the real distributed-artifact situation (file://
    // can't fetch the sidecars). The loader must show its placeholder rather
    // than crashing or leaving a blank pane.
    await load(page);

    const row = decideRow(page);
    await expect(row).toBeVisible({ timeout: 5000 });
    await row.click();

    const body = row.locator(".trace-timeline__row-body");
    await expect(body).toBeVisible({ timeout: 3000 });
    await expect(body.locator(".agent-detail__verb-badge")).toBeVisible({ timeout: 3000 });

    // Since Slice #2 the prompts live inside the collapsed evidence drawer — expand it first.
    const evidenceToggle = body.locator("[data-testid='decide-evidence-toggle']");
    if (await evidenceToggle.isVisible()) {
      await evidenceToggle.click();
    }

    // Placeholder names the unreachable file so an operator knows what's missing.
    await expect(body).toContainText("agent-prompts/decide-001.txt");
    await expect(body).toContainText("could not load");
  });
});
