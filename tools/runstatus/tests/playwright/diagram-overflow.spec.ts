/**
 * Text-containment / overflow gate for the four state-diagram views.
 *
 * Drives the dev-story design pipeline to a mid-pipeline room (design_refine
 * — the worst case: a traveled leg + live pills + a road ahead, all with long
 * `design_*` room labels), deterministically and with NO LLM (the
 * design_happy_path flow stubs every host call). Then for each view it asserts
 * nothing overflows:
 *
 *   - metro / path / full — DOM/CSS: every labelled element must satisfy
 *     `scrollWidth <= clientWidth + 1` (+1 tolerates sub-pixel rounding).
 *   - ego — SVG: every node-label `<text>` must fit inside its sibling `<rect>`
 *     (2-unit inset). This is the SVG-containment gate the proposal calls for;
 *     it's why the node labels carry `textLength` (long ids are compressed, not
 *     clipped).
 *
 * Exits non-zero on any overflow.
 *
 *   pnpm exec playwright test diagram-overflow --project=chromium
 */
import { test, expect, chromium, type Browser, type BrowserContext, type Page } from "@playwright/test";
import path from "path";
import { startWebServer, repoRoot, waitForState, type WebServer } from "./_helpers/server.js";

const ADDR = "127.0.0.1:7751";
const STORY_DIR = path.join(repoRoot, "stories", "dev-story");
const FLOW = path.join(STORY_DIR, "flows", "design_happy_path.yaml");

let server: WebServer;

test.beforeAll(async () => {
  server = await startWebServer({ addr: ADDR, flow: FLOW, storiesDir: STORY_DIR });
});
test.afterAll(() => server?.stop());

/** DOM overflow offenders inside the active diagram view (empty = pass). */
async function domOverflow(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    const root = document.querySelector(".state-diagram");
    if (!root) return ["NO .state-diagram root"];
    const sel = [
      ".state-diagram__ms-name",
      ".state-diagram__ms-banner",
      ".state-diagram__ms-via",
      ".state-diagram__crumb-room",
      ".state-diagram__crumb-via",
      ".state-diagram__station-label",
      ".state-diagram__station-room",
      ".state-diagram__pill-intent",
      ".state-diagram__pill-target",
      ".state-diagram__phase-name",
      ".state-diagram__room-label",
      ".state-diagram__elsewhere",
      ".state-diagram__tab",
    ].join(",");
    const bad: string[] = [];
    for (const el of Array.from(root.querySelectorAll(sel))) {
      const e = el as HTMLElement;
      if (e.offsetParent === null) continue; // not visible
      if (e.scrollWidth > e.clientWidth + 1) {
        bad.push(`${e.className}: ${e.scrollWidth}>${e.clientWidth} ("${(e.textContent ?? "").trim().slice(0, 40)}")`);
      }
    }
    return bad;
  });
}

test("all four diagram views: nothing overflows", async () => {
  test.setTimeout(120000);
  const browser: Browser = await chromium.launch({ headless: true });
  const context: BrowserContext = await browser.newContext({ viewport: { width: 1600, height: 900 } });
  const page: Page = await context.newPage();
  let sid = "";
  const submit = (intent: string, slots: Record<string, unknown> = {}) =>
    server.rpc("runstatus.session.submit", { session_id: sid, intent, slots });

  try {
    await page.goto(`${server.base}/#/`);
    await expect(page.getByTestId("home-view")).toBeVisible({ timeout: 15000 });
    const card = page.locator("[data-testid='story-card']").filter({ hasText: /dev.story/i }).first();
    await card.getByTestId("new-session-btn").click();
    await page.waitForURL(/#\/s\/[0-9a-f-]{36}\/chat$/, { timeout: 15000 });
    sid = page.url().match(/\/s\/([0-9a-f-]{36})\/chat$/)?.[1] ?? "";
    await waitForState(page, "main", 15000);

    // Drive to mid-pipeline (no LLM; the flow stubs answer every host call).
    await submit("go_idea", { message: "work on a proposal" });
    await submit("discuss", { message: "I want a per-session working folder primitive" });
    await submit("confirm", {});
    await page.reload();
    await waitForState(page, "design_refine", 15000);
    await expect(page.getByTestId("diagram-tabs")).toBeVisible({ timeout: 8000 });

    // DOM views.
    for (const mode of ["metro", "path", "full"]) {
      await page.getByTestId(`diagram-tab-${mode}`).click();
      await page.waitForTimeout(250);
      const bad = await domOverflow(page);
      expect(bad, `[${mode}] overflowing:\n${bad.join("\n")}`).toEqual([]);
    }

    // SVG containment for the ego graph: each node-label text fits its rect.
    await page.getByTestId("diagram-tab-ego").click();
    await expect(page.getByTestId("diagram-ego")).toBeVisible();
    await page.waitForTimeout(250);
    const svgBad = await page.evaluate(() => {
      const svg = document.querySelector(".state-diagram__ego svg");
      if (!svg) return ["NO ego svg"];
      const bad: string[] = [];
      for (const g of Array.from(svg.querySelectorAll("g"))) {
        const rect = g.querySelector("rect") as SVGGraphicsElement | null;
        const text = g.querySelector("text") as SVGGraphicsElement | null;
        if (!rect || !text) continue;
        const rb = (rect as unknown as SVGGraphicsElement).getBBox();
        const tb = (text as unknown as SVGGraphicsElement).getBBox();
        const inset = 2;
        if (tb.x < rb.x - inset || tb.x + tb.width > rb.x + rb.width + inset) {
          bad.push(`"${(text.textContent ?? "").trim()}" text [${tb.x.toFixed(0)},${(tb.x + tb.width).toFixed(0)}] escapes rect [${rb.x.toFixed(0)},${(rb.x + rb.width).toFixed(0)}]`);
        }
      }
      return bad;
    });
    expect(svgBad, `ego SVG label overflow:\n${svgBad.join("\n")}`).toEqual([]);
  } finally {
    await context.close();
    await browser.close();
  }
});
