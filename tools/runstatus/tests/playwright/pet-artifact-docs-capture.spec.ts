/**
 * pet-artifact-docs-capture.spec.ts — rrweb capture of the pet scenario's
 * MARKDOWN artifacts, full-screened in the kitsoki web artifact viewer.
 *
 * The trace-column pet scenario produces two document artifacts that are
 * markdown (NOT slidey decks): the PRD and the design doc. This spec opens each
 * one in kitsoki web's global ArtifactModal (window.__openArtifact → the
 * markdown-modal, which fetches the file via the runstatus.file.read RPC and
 * renders it), scrolls smoothly through the whole document, and records the
 * rrweb event stream. Each doc → one brief, self-contained clip the hybrid deck
 * embeds as the "the produced PRD / design document" artifact reveal.
 *
 * Why markdown (not a deck): the PRD and design are written/published as
 * markdown documents; only the mockup + demo deliverables are slidey decks
 * (HTML). The deck-review clips (pet-<phase>-review) cover the HTML decks; these
 * cover the markdown documents.
 *
 * No LLM, no flow: just a plain `kitsoki web` (real RPC) + the global artifact
 * viewer hook. The file is read from the server cwd (repoRoot), so the path is
 * repo-relative.
 *
 * Output (one per doc):
 *   docs/decks/clips/pet-<slug>-doc.rrweb.json          ← rrweb event stream
 *   docs/decks/clips/pet-<slug>-doc.rrweb.capture.json  ← viewport sidecar
 *
 * 1600x900 DSF1. Run:
 *   cd tools/runstatus && npx playwright test tests/playwright/pet-artifact-docs-capture.spec.ts --reporter=line
 */
import { test, expect, chromium, type Browser, type BrowserContext, type Page } from "@playwright/test";
import path from "path";
import fs from "fs";
import { startWebServer, repoRoot, STORIES_DIR, cinematicGoto, demoAddr, type WebServer } from "./_helpers/server.js";
import { installCapture, dumpCapture, writeEvents } from "./_helpers/rrweb-replay.js";

const ADDR = demoAddr(7795);
const OUT_DIR = path.join(repoRoot, "docs", "decks", "clips");
const VIEWPORT = { width: 1600, height: 900 } as const;

interface DocCase {
  slug: string; // clip slug → pet-<slug>-doc
  docPath: string; // repo-relative markdown artifact
  label: string;
}

const DOCS: DocCase[] = [
  { slug: "prd", docPath: "stories/pets-dev/assets/pm_idea-prd.md", label: "PRD · Trace-Column Pet" },
  { slug: "design", docPath: "stories/pets-dev/assets/architect_design-doc.md", label: "Design · Trace-Column Pet" },
];

let server: WebServer;

test.beforeAll(async () => {
  fs.mkdirSync(OUT_DIR, { recursive: true });
  // Plain web server (real RPC) — the global ArtifactModal reads the markdown via
  // runstatus.file.read; no story flow / LLM involved.
  server = await startWebServer({ addr: ADDR, storiesDir: STORIES_DIR });
});

test.afterAll(() => server?.stop());

// Open the markdown artifact full-screen and ease through it, leaving the modal
// OPEN so the clip rests on the rendered document (no closing outro). Mirrors
// showArtifact()'s open+scroll, minus the close.
async function openAndScroll(page: Page, docPath: string): Promise<void> {
  await page.evaluate((p) => {
    (window as unknown as { __openArtifact?: (s: string) => void }).__openArtifact?.(p);
  }, docPath);
  await expect(page.getByTestId("markdown-modal")).toBeVisible({ timeout: 8000 });
  await page.waitForFunction(
    () => {
      const el = document.querySelector('[data-testid="markdown-modal-body"] .mm-md') as HTMLElement | null;
      return !!el && el.scrollHeight > 0;
    },
    undefined,
    { timeout: 8000 },
  );
  await page.waitForTimeout(1600); // read the top of the document
  await page.evaluate(async () => {
    const el = document.querySelector('[data-testid="markdown-modal-body"]') as HTMLElement | null;
    if (!el) return;
    const max = el.scrollHeight - el.clientHeight;
    if (max <= 2) return;
    const t0 = performance.now();
    const ms = 5600;
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
  await page.waitForTimeout(1600); // rest on the end of the document
}

for (const doc of DOCS) {
  test(`capture pet artifact doc · ${doc.slug}`, async () => {
    test.setTimeout(120000);
    const browser: Browser = await chromium.launch({ headless: true });
    const context: BrowserContext = await browser.newContext({
      viewport: { ...VIEWPORT },
      deviceScaleFactor: 1,
    });
    const page: Page = await context.newPage();

    try {
      await cinematicGoto(page, `${server.base}/#/`, { waitForTestId: "home-view", settleMs: 1500 });
      await installCapture(page);
      await openAndScroll(page, doc.docPath);

      // Prove the rendered markdown is the real document (a heading rendered into
      // the modal body), not an empty/error panel.
      const bodyText = await page
        .locator('[data-testid="markdown-modal-body"]')
        .innerText({ timeout: 4000 })
        .catch(() => "");
      expect(bodyText.length, `markdown body rendered for ${doc.slug}`).toBeGreaterThan(200);

      const { events, viewport } = await dumpCapture(page);
      const outPath = path.join(OUT_DIR, `pet-${doc.slug}-doc.rrweb.json`);
      writeEvents(events, outPath, viewport);

      const clipBytes = JSON.stringify(events).length;
      console.log(
        `[pet-artifact-doc] ${doc.slug}: events=${events.length} clipBytes=${clipBytes} ` +
          `bodyChars=${bodyText.length} @ ${viewport.width}x${viewport.height} -> ${outPath}`,
      );
      expect(events.length, "recorded an open + scroll arc, not just a bare snapshot").toBeGreaterThanOrEqual(12);
    } catch (e) {
      console.log(`[pet-artifact-doc] ${doc.slug} FAILED: ${e instanceof Error ? e.message : String(e)}`);
      console.log(`--- server log (tail) ---\n${(server?.log?.() ?? "").slice(-2000)}`);
      throw e;
    } finally {
      await context.close();
      await browser.close();
    }
  });
}
