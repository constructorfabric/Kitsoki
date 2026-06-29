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
// page): the issue TITLE first, then EVERY comment in DOM order.
//
// The title beat ZOOMs the real on-page title (the <bdi data-testid=issue-title>)
// BIG — a dramatic readable pop-out + glowing select border. A subtle 1.3× nudge
// barely resized the single-line title and read as "not called out" next to the
// comments' full-width pop-outs; a large minScale makes the title unmistakably
// selected/expanded, exactly like the comments.
//
// The comment beats mirror github-agent-live-capture.spec.ts (the original live
// deck): (1) BREATHE the literal "@kitsoki" token on the comment that carries the
// mention — a brief bold/glow so the mention reads as the trigger — and (2) ZOOM
// the WHOLE comment box (avatar, author, timestamp, full markdown body) into a
// readable pop-out, then return it. Spotlighting only the #issuecomment-<id>
// anchor was wrong: on GitHub's React layout that node is effectively the header
// bar, so the outline framed only the chrome strip and never the message content.
//
// Comments are enumerated LIVE from the page ([id^='issuecomment-'] in DOM order)
// so each clip always reflects the real thread — the requester's @kitsoki
// invocation, kitsoki's reply with the validated artifacts, and any others.

const OUT_DIR = path.join(repoRoot, "docs", "decks", "clips");

interface ThreadCase {
  clip: string;
  url: string;
  title: string;
  route: string;
}

const CASES: ThreadCase[] = [
  {
    // The single #52 video: title + the full thread (the @kitsoki invocation AND
    // kitsoki's reply with the artifacts). There used to be a second #52 clip
    // (pet-github-feature-thread) for the design phase, but now that the kickoff
    // enumerates EVERY comment it shows the same thread — so that duplicate clip
    // and its deck scene were removed.
    clip: "pet-github-feature-kickoff",
    url: "https://github.com/bsacrobatix/Kitsoki/issues/52",
    title: "GitHub · Issue #52 · Design: trace-column pet (Kit)",
    route: "A labelled issue → a real @kitsoki mention claims the job",
  },
  {
    clip: "pet-github-decomp-thread",
    url: "https://github.com/bsacrobatix/Kitsoki/issues/53",
    title: "GitHub · Issue #53 · Decomposition",
    route: "kitsoki replies with the decomposition work plan",
  },
  {
    clip: "pet-github-bug-thread",
    url: "https://github.com/bsacrobatix/Kitsoki/issues/54",
    title: "GitHub · Issue #54 · Bug Report",
    route: "kitsoki replies with the bug report + fix report",
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

// List every comment on the issue in DOM order, with whether it carries the
// literal "@kitsoki" mention (the requester's invocation) vs not (kitsoki's
// reply). Reads the FULL comment-box text (not just the anchor header bar) so a
// mention in the body is detected.
async function listComments(page: Page): Promise<{ id: string; hasMention: boolean }[]> {
  return page.evaluate(() => {
    const anchors = Array.from(document.querySelectorAll<HTMLElement>("[id^='issuecomment-']"));
    return anchors.map((a) => {
      const box = a.closest<HTMLElement>("[data-testid^='comment-viewer-outer-box']") || a;
      const text = box.textContent || "";
      return { id: a.id, hasMention: /@kitsoki/i.test(text) };
    });
  });
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

      // Callout order (top-to-bottom), the way a reader scans the page: the
      // issue TITLE first, then EVERY comment in DOM order.
      //
      // 1. The issue title — where the work enters. ZOOM it BIG (a dramatic
      //    readable pop-out + glowing select border) so it is called out as
      //    forcefully as the comments. The title is a single line, so a subtle
      //    1.3× nudge barely changes its size and reads as "not called out" next
      //    to the comments' full-width pop-outs; a large minScale makes
      //    "Design: trace-column pet (Kit)" unmistakably selected/expanded.
      const titleSel = await markTitle(page, "issue-title");
      if (titleSel) {
        await zoom(titleSel, { title: c.title, fontSize: 48, minScale: 2.4 });
        await caption(c.title, c.route, 3200);
        await dwell(page, 700);
        await zoom(null);
        await dwell(page, 600);
      } else {
        await caption(c.title, c.route, 3000);
      }

      // 2. EVERY comment on the issue, in DOM order. Enumerate the thread live so
      //    the clip always reflects the real comments (the requester's @kitsoki
      //    invocation, then kitsoki's reply, and any others). Breathe the literal
      //    "@kitsoki" token on the comment that carries the mention, then zoom the
      //    whole comment box into a readable pop-out.
      const comments = await listComments(page);
      for (let i = 0; i < comments.length; i++) {
        const cm = comments[i];
        const sel = await markComment(page, cm.id, `comment-${i}`);
        const [beatTitle, beatSub] = cm.hasMention
          ? [
              "Requester mentions @kitsoki",
              "A real @kitsoki mention claims the job from where the work already lives — the whole comment, not just the token.",
            ]
          : [
              "kitsoki reports back on the thread",
              "The run finishes and kitsoki replies on the same thread with the validated artifacts, available straight from GitHub.",
            ];
        await commentBeat(page, caption, zoom, textBreath, sel, beatTitle, beatSub, cm.hasMention ? 4200 : 4500, {
          breathe: cm.hasMention,
        });
        await dwell(page, 1100);
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
