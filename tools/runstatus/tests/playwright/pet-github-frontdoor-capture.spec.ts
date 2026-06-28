import { test, expect, chromium, type Browser, type BrowserContext, type Page } from "@playwright/test";
import path from "path";
import fs from "fs";
import { repoRoot, dwell } from "./_helpers/server.js";
import {
  makeCaption,
  makeReadableZoom,
  makeTextBreath,
  type Beat,
  type ReadableZoom,
  type TextBreath,
} from "./_helpers/demo.js";
import { installCapture, dumpCapture, writeEvents } from "./_helpers/rrweb-replay.js";
import { cameraContext } from "./_helpers/camera.js";

// Captures the real GitHub @kitsoki threads for the trace-column pet scenario.
//
// Callout order within every clip (top-to-bottom, the way a reader scans the
// page): the issue TITLE first, then each comment in DOM order.
//
// Narration technique mirrors github-agent-live-capture.spec.ts (the original
// live deck): the comment beats (1) BREATHE the literal "@kitsoki" token — a
// brief bold/glow so the mention reads as the trigger — and (2) ZOOM the WHOLE
// comment box (avatar, author, timestamp, full markdown body) into a readable
// pop-out, then return it. Spotlighting only the #issuecomment-<id> anchor was
// wrong: on GitHub's React layout that node is effectively the header bar, so
// the outline framed only the chrome strip and never the message content.
//
// Two clip shapes:
//   - "kickoff": title + the human @kitsoki invocation only (NO artifact reveal).
//     Used by the lean front door.
//   - "thread":  title + invocation + the bsacrobatix-kitsoki-test[bot] reply
//     that links the validated artifacts. Used by the per-phase ticket scenes
//     (Design #52, Decomposition #53, Bug #54).
//
// Comment DOM ids come from the live issues (GET /issues/<n>/comments).

const OUT_DIR = path.join(repoRoot, "docs", "decks", "clips");

interface ThreadCase {
  clip: string;
  url: string;
  title: string;
  route: string;
  invocationId: string;
  replyId: string;
  reveal: "kickoff" | "thread";
}

const CASES: ThreadCase[] = [
  {
    clip: "pet-github-feature-kickoff",
    url: "https://github.com/bsacrobatix/Kitsoki/issues/52",
    title: "GitHub · Issue #52",
    route: "A labelled issue → a real @kitsoki mention claims the job",
    invocationId: "issuecomment-4826046811",
    replyId: "issuecomment-4826047171",
    reveal: "kickoff",
  },
  {
    clip: "pet-github-feature-thread",
    url: "https://github.com/bsacrobatix/Kitsoki/issues/52",
    title: "GitHub · Issue #52 · Design",
    route: "kitsoki replies with the PRD + validated design deck",
    invocationId: "issuecomment-4826046811",
    replyId: "issuecomment-4826047171",
    reveal: "thread",
  },
  {
    clip: "pet-github-decomp-thread",
    url: "https://github.com/bsacrobatix/Kitsoki/issues/53",
    title: "GitHub · Issue #53 · Decomposition",
    route: "kitsoki replies with the decomposition work plan",
    invocationId: "issuecomment-4826046915",
    replyId: "issuecomment-4826047236",
    reveal: "thread",
  },
  {
    clip: "pet-github-bug-thread",
    url: "https://github.com/bsacrobatix/Kitsoki/issues/54",
    title: "GitHub · Issue #54 · Bug Report",
    route: "kitsoki replies with the bug report + fix report",
    invocationId: "issuecomment-4826047020",
    replyId: "issuecomment-4826047290",
    reveal: "thread",
  },
];

// Climb from the stable #issuecomment-<id> anchor to the FULL comment container
// (avatar + header + body), mark it with a data-attribute, and scroll it into
// view. Returns the marker selector the zoom/breath helpers can resolve.
async function markComment(page: Page, anchorId: string, name: string): Promise<string | null> {
  const ok = await page
    .evaluate(
      ({ id, attr, targetName }) => {
        const anchor = document.getElementById(id);
        if (!anchor) return false;
        const containerSel = [
          "[data-testid^='comment-viewer-outer-box']",
          "[data-testid^='timeline-row-border']",
          ".js-timeline-item",
          ".TimelineItem",
          ".timeline-comment-group",
          ".timeline-comment",
          ".js-comment-container",
        ].join(",");
        const container = anchor.closest<HTMLElement>(containerSel) || anchor;
        container.setAttribute(attr, targetName);
        container.scrollIntoView({ block: "center", inline: "nearest" });
        return true;
      },
      { id: anchorId, attr: "data-kitsoki-demo-target", targetName: name },
    )
    .catch(() => false);
  return ok ? `[data-kitsoki-demo-target="${name}"]` : null;
}

// Mark the issue title element, scroll it into view, and return its selector.
async function markTitle(page: Page, name: string): Promise<string | null> {
  const ok = await page
    .evaluate(
      ({ attr, targetName }) => {
        const sels = [
          "[data-testid='issue-title']",
          "bdi.js-issue-title",
          ".js-issue-title",
          ".gh-header-title",
          "h1",
        ];
        for (const sel of sels) {
          const el = document.querySelector<HTMLElement>(sel);
          if (el) {
            el.setAttribute(attr, targetName);
            el.scrollIntoView({ block: "center", inline: "nearest" });
            return true;
          }
        }
        return false;
      },
      { attr: "data-kitsoki-demo-target", targetName: name },
    )
    .catch(() => false);
  return ok ? `[data-kitsoki-demo-target="${name}"]` : null;
}

// One narration beat over a comment: breathe the @kitsoki mention (if present),
// then zoom the whole comment box into a readable pop-out and return it.
async function commentBeat(
  page: Page,
  caption: Beat,
  zoom: ReadableZoom,
  textBreath: TextBreath,
  selector: string | null,
  title: string,
  sub: string,
  holdMs: number,
  opts: { breathe?: boolean } = {},
): Promise<void> {
  if (!selector) return;
  if (opts.breathe) {
    await textBreath(selector, { pattern: "@kitsoki", context: title });
    await dwell(page, 600);
  }
  await zoom(selector, { title, fontSize: 17, minScale: 1.05 });
  await caption(title, sub, holdMs);
  await zoom(null);
}

test.beforeAll(() => {
  fs.mkdirSync(OUT_DIR, { recursive: true });
});

for (const c of CASES) {
  test(`capture github thread · ${c.clip}`, async () => {
    test.setTimeout(120000);
    const browser: Browser = await chromium.launch({ headless: true });

    const context: BrowserContext = await browser.newContext({
      ...cameraContext(),
      bypassCSP: true,
      colorScheme: "dark",
    });

    const page: Page = await context.newPage();

    await page.addInitScript(() => {
      const pin = (): void => {
        const html = document.documentElement;
        if (!html) return;
        html.setAttribute("data-color-mode", "dark");
        html.setAttribute("data-dark-theme", "dark");
        html.setAttribute("data-light-theme", "dark");
      };
      pin();
      document.addEventListener("DOMContentLoaded", pin);
    });

    try {
      console.log(`[pet-github] ${c.clip} ← ${c.url}`);
      await page.goto(c.url, { waitUntil: "domcontentloaded", timeout: 60000 });
      await page.waitForTimeout(3000);
      // Re-assert dark theme after GitHub's boot JS settles.
      await page.evaluate(() => {
        const html = document.documentElement;
        html.setAttribute("data-color-mode", "dark");
        html.setAttribute("data-dark-theme", "dark");
        html.setAttribute("data-light-theme", "dark");
      });

      await installCapture(page);

      const caption = await makeCaption(page);
      const zoom = await makeReadableZoom(page);
      const textBreath = await makeTextBreath(page);

      // Callout order (top-to-bottom): the issue TITLE first, then each comment
      // in DOM order.
      //
      // 1. The issue title — where the work enters. ZOOM it (same readable
      //    pop-out the comments get) so the title is unmistakably called out; a
      //    thin spotlight outline reads as "not called out" next to the comment
      //    zooms.
      const titleSel = await markTitle(page, "issue-title");
      if (titleSel) {
        await zoom(titleSel, { title: c.title, fontSize: 26, minScale: 1.3 });
        await caption(c.title, c.route, 3000);
        await dwell(page, 600);
        await zoom(null);
      } else {
        await caption(c.title, c.route, 3000);
      }

      // 2. The @kitsoki invocation comment — breathe the mention, zoom the box.
      const invSel = await markComment(page, c.invocationId, "invocation");
      await commentBeat(
        page,
        caption,
        zoom,
        textBreath,
        invSel,
        "Requester mentions @kitsoki",
        "A real @kitsoki mention claims the job from where the work already lives — the whole comment, not just the token.",
        4000,
        { breathe: true },
      );
      await dwell(page, 1200);

      // 3. (thread only) kitsoki's reply — the artifacts, straight from GitHub.
      if (c.reveal === "thread") {
        const replySel = await markComment(page, c.replyId, "reply");
        await commentBeat(
          page,
          caption,
          zoom,
          textBreath,
          replySel,
          "Artifacts ready for review",
          "kitsoki processes the issue and replies on the thread with the validated decks as GitHub release downloads.",
          4500,
        );
        await dwell(page, 1200);
      }

      await zoom(null);
      await dwell(page, 1000);

      const { events, viewport } = await dumpCapture(page);
      const outPath = path.join(OUT_DIR, `${c.clip}.rrweb.json`);
      writeEvents(events, outPath, viewport);

      console.log(`[pet-github] Wrote ${events.length} events to ${outPath}`);
      expect(events.length).toBeGreaterThanOrEqual(10);
    } catch (e) {
      console.error(`[pet-github] Failed:`, e);
      throw e;
    } finally {
      await context.close();
      await browser.close();
    }
  });
}
