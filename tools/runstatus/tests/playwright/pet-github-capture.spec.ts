import { test, expect, chromium, type Browser, type BrowserContext, type Page } from "@playwright/test";
import path from "path";
import fs from "fs";
import { repoRoot, dwell } from "./_helpers/server.js";
import { makeCaption, makeSpotlight, type Beat, type Spotlight } from "./_helpers/demo.js";
import { installCapture, dumpCapture, writeEvents } from "./_helpers/rrweb-replay.js";
import { cameraContext } from "./_helpers/camera.js";

const OUT_DIR = path.join(repoRoot, "docs", "decks", "clips");
const VIEWPORT = { width: 1600, height: 900 } as const;

interface IssueCase {
  phase: string;
  url: string;
  title: string;
  narration: string;
}

const CASES: IssueCase[] = [
  {
    phase: "design-issue",
    url: "https://github.com/bsacrobatix/Kitsoki/issues/52",
    title: "GitHub · Issue #52 · Design",
    narration: "Here is the real design issue filed on GitHub, with the slidey deck artifact linked and assigned to Arden."
  },
  {
    phase: "decomp-issue",
    url: "https://github.com/bsacrobatix/Kitsoki/issues/53",
    title: "GitHub · Issue #53 · Decomposition",
    narration: "The decomposition work plan is tracked live on Issue #53, carrying the validated slidey deck asset."
  },
  {
    phase: "bug-issue",
    url: "https://github.com/bsacrobatix/Kitsoki/issues/54",
    title: "GitHub · Issue #54 · Bug Report",
    narration: "And here is the live bug ticket #54, reporting the redundant Observe link with its embedded rrweb replay."
  }
];

test.beforeAll(() => {
  fs.mkdirSync(OUT_DIR, { recursive: true });
});

for (const c of CASES) {
  test(`capture real github issue · ${c.phase}`, async () => {
    test.setTimeout(120000);
    const browser: Browser = await chromium.launch({ headless: true });
    
    // Bypass CSP and set colorScheme to dark to match the deck theme
    const context: BrowserContext = await browser.newContext({
      ...cameraContext(),
      bypassCSP: true,
      colorScheme: "dark",
    });
    
    const page: Page = await context.newPage();

    // Ensure dark theme attribute is locked for rrweb snapshot
    await page.addInitScript(() => {
      document.documentElement.setAttribute("data-color-mode", "dark");
      document.documentElement.setAttribute("data-dark-theme", "dark");
    });

    try {
      console.log(`[pet-github-capture] Navigating to ${c.url}`);
      await page.goto(c.url, { waitUntil: "domcontentloaded", timeout: 60000 });
      await page.waitForTimeout(3000); // let page scripts settle

      // Start rrweb recording
      await installCapture(page);
      
      const caption = await makeCaption(page);
      const spotlight = await makeSpotlight(page);

      // 1. Highlight issue title
      await caption(c.title, "The live GitHub issue for this case.", 3000);
      const titleSelector = "h1.gh-header-title, bdi.js-issue-title, .js-issue-title";
      const titleEl = page.locator(titleSelector).first();
      if (await titleEl.isVisible()) {
        await spotlight(titleSelector);
        await dwell(page, 2000);
      }

      // 2. Highlight issue body (with linked slidey deck)
      await caption("Artifact Attachment", "The validated slidey deck is uploaded and linked directly in the issue body.", 4000);
      const bodySelector = ".comment-body, .markdown-body";
      const bodyEl = page.locator(bodySelector).first();
      if (await bodyEl.isVisible()) {
        await spotlight(bodySelector);
        await dwell(page, 3000);
      }

      await spotlight(null);
      await dwell(page, 1000);

      // Dump rrweb events
      const { events, viewport } = await dumpCapture(page);
      const outPath = path.join(OUT_DIR, `pet-github-${c.phase}.rrweb.json`);
      writeEvents(events, outPath, viewport);

      console.log(`[pet-github-capture] Wrote ${events.length} events to ${outPath}`);
      expect(events.length).toBeGreaterThanOrEqual(10);
    } catch (e) {
      console.error(`[pet-github-capture] Failed:`, e);
      throw e;
    } finally {
      await context.close();
      await browser.close();
    }
  });
}
