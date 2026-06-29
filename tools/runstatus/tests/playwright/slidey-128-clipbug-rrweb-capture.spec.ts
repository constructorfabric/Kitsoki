/**
 * slidey-128-clipbug-rrweb-capture.spec.ts — before/after rrweb capture for the
 * Agent-Actions title-overflow bugfix deck (docs/decks/slidey-128-bugfix...).
 *
 * Why a self-contained fixture rather than driving live `kitsoki web`: the bug
 * is a pure-CSS regression in ONE component (AgentActionRow `.aar__title`), and
 * the bugfix-engine's web --flow replay is independently red in this tree. So we
 * render a faithful Agent-Actions drawer — the REAL `.aar*` CSS copied verbatim
 * from AgentActionRow.vue, populated with the REAL slidey-128 run's agent-action
 * titles (the verbose reasoning summary is what overflows) — and rrweb-capture
 * it TWICE. The only difference between the two runs is the `.aar--clip-bug`
 * class, which re-adds the pre-fix CSS (a flex child without `min-width: 0`):
 *
 *   docs/decks/assets/slidey-128-bugfix/before.rrweb.json  ← .aar--clip-bug (overflows)
 *   docs/decks/assets/slidey-128-bugfix/after.rrweb.json   ← fixed (ellipsis truncation)
 *
 * The capture asserts the geometry both ways (title overflows its header before,
 * fits after) so a green run is proof the bug actually renders.
 *
 * Run:
 *   pnpm exec playwright test slidey-128-clipbug-rrweb-capture --project=chromium
 */
import { test, expect, chromium, type Page } from "@playwright/test";
import path from "path";
import fs from "fs";
import { repoRoot } from "./_helpers/server.js";
import { installCapture, dumpCapture, writeEvents } from "./_helpers/rrweb-replay.js";

const ASSET_DIR = path.join(repoRoot, "docs", "decks", "assets", "slidey-128-bugfix");
const DIAG_DIR = path.join(repoRoot, ".artifacts", "rrweb-eval", "slidey-128-clipbug");
const VIEWPORT = { width: 1280, height: 800 } as const;

// The REAL agent-action rows from the slidey-128 run (titles verbatim from
// stories/slidey-bugfix/cassettes/tour.cassette.yaml). The reasoning summaries
// are long enough to overflow the drawer — exactly the bug. The final row is the
// WORST CASE for the fix: a long title PLUS a PASS verdict and an ERR flag, the
// most right-side content a real row can carry — proof that min-width:0 truncates
// the title without collapsing it to nothing when the cluster is widest.
const ROWS = [
  { kind: "reasoning", chip: "🧠 THINK", title: "slidey-128 CONFIRMED: grid cards estimator over-counts items beyond cards_item_5 by 10 frames each", tokens: "in:4.1k out:612", cost: "$0.04", off: "+2.1s" },
  { kind: "tool", chip: "TOOL", title: "Read src/timing.js · Read src/renderer.js", tokens: "in:1.8k out:0", cost: "$0.01", off: "+3.4s" },
  { kind: "reasoning", chip: "🧠 THINK", title: "slidey-128: align grid cards estimator fallback (30→20) with the renderer per-item hold", tokens: "in:3.2k out:488", cost: "$0.03", off: "+5.0s" },
  { kind: "tool", chip: "TOOL", title: "Edit src/timing.js · Edit test/timing.test.js", tokens: "in:2.0k out:210", cost: "$0.02", off: "+6.2s" },
  { kind: "result", chip: "RESULT", title: "slidey-128: grid cards timing drift — regression test green (19/19, 94/97 suite)", tokens: "in:1.1k out:96", cost: "$0.01", off: "+7.9s" },
  { kind: "guardrail", chip: "GUARD", title: "slidey-128: verdict on the timing-drift fix — diff scoped to estimator + renderer, regression added", tokens: "in:0.9k out:54", cost: "$0.01", off: "+8.4s", verdict: "PASS", err: true },
] as const;

/** Read the REAL `.aar*` CSS straight from AgentActionRow.vue's <style scoped>
 *  block so this capture is coupled to the actual component — if the min-width:0
 *  fix is removed from the source, the "after" assertion below fails. Vue's scoped
 *  selectors are plain `.aar*` in source (the data-v hash is added at build time),
 *  so the rules apply verbatim in a standalone fixture. */
function componentCss(): string {
  const vue = fs.readFileSync(
    path.join(repoRoot, "tools", "runstatus", "src", "components", "agent", "AgentActionRow.vue"),
    "utf8",
  );
  const m = vue.match(/<style scoped>([\s\S]*?)<\/style>/);
  if (!m) throw new Error("AgentActionRow.vue: <style scoped> block not found");
  return m[1];
}

// Fixture-only chrome (the drawer shell + theme vars the SPA supplies at runtime).
// NOT a copy of any component rule — purely the host page around the real rows.
const FIXTURE_CHROME = `
:root{
  --k-fg:#e2e8f0; --k-border:#1e293b; --k-bg-widget:#0a1728; --k-bg-hover:#0f1e38;
  --k-fg-subtle:#475569; --k-success-bg:#042f1c; --k-success:#6ee7b7; --k-fg-accent:#7dd3fc;
  --k-error:#fca5a5;
}
body{margin:0;background:#020617;font-family:ui-sans-serif,system-ui,sans-serif;}
.drawer{box-sizing:border-box;width:440px;margin:24px;padding:12px;background:#04101f;border:1px solid var(--k-border);border-radius:8px;display:flex;flex-direction:column;gap:6px;}
.drawer__head{color:#94a3b8;font:600 0.8rem ui-sans-serif;margin:0 2px 4px;letter-spacing:.02em;}
`;

const AAR_CSS = FIXTURE_CHROME + componentCss();

function buildHtml(clipBug: boolean): string {
  const rows = ROWS.map((r) => {
    const verdict = "verdict" in r && r.verdict
      ? `<span class="aar__verdict aar__verdict--pass">${r.verdict}</span>`
      : "";
    const errFlag = "err" in r && r.err ? `<span class="aar__err-flag">ERR</span>` : "";
    return `
    <div class="aar aar--${r.kind}${clipBug ? " aar--clip-bug" : ""}">
      <div class="aar__header">
        <span class="aar__kind-chip aar__kind-chip--${r.kind}">${r.chip}</span>
        <span class="aar__title">${r.title}</span>
        ${verdict}${errFlag}
        <span class="aar__spacer"></span>
        <span class="aar__tokens">${r.tokens}</span>
        <span class="aar__cost">${r.cost}</span>
        <span class="aar__offset">${r.off}</span>
        <span class="aar__toggle">+</span>
      </div>
    </div>`;
  }).join("");
  return `<!doctype html><html><head><meta charset="utf-8"><style>${AAR_CSS}</style></head>
    <body><div class="drawer" data-testid="agent-actions-drawer">
      <p class="drawer__head">Agent Actions · slidey-128 bugfix</p>${rows}
    </div></body></html>`;
}

interface Variant { name: "before" | "after"; clipBug: boolean }
const VARIANTS: Variant[] = [
  { name: "before", clipBug: true },
  { name: "after", clipBug: false },
];

/** Geometry proof per title: does its box run into the cluster to its right
 *  (overflows), and how wide does it render (width — to catch a min-width:0
 *  collapse-to-nothing, the actual risk the fix introduces)? */
async function overflowReport(page: Page): Promise<{ title: string; overflows: boolean; width: number }[]> {
  return page.evaluate(() => {
    return Array.from(document.querySelectorAll(".aar__title")).map((t) => {
      const el = t as HTMLElement;
      const header = el.closest(".aar__header") as HTMLElement;
      // The cluster to the title's right (verdict/err+spacer+tokens+cost+offset+toggle).
      const right = header.getBoundingClientRect().right;
      const box = el.getBoundingClientRect();
      const overflows = box.right > right - 40; // within 40px of edge ⇒ colliding
      return { title: (el.textContent || "").slice(0, 30), overflows, width: Math.round(box.width) };
    });
  });
}

test("slidey-128 clipBug before/after rrweb capture", async () => {
  test.setTimeout(180000);
  fs.mkdirSync(ASSET_DIR, { recursive: true });
  fs.mkdirSync(DIAG_DIR, { recursive: true });
  const browser = await chromium.launch({ headless: true });
  try {
    for (const v of VARIANTS) {
      const ctx = await browser.newContext({ viewport: { ...VIEWPORT }, deviceScaleFactor: 1 });
      const page = await ctx.newPage();
      try {
        await page.setContent(buildHtml(v.clipBug), { waitUntil: "load" });
        await page.waitForTimeout(200);
        await installCapture(page);

        // Motion so the clip reads as a ~11s held "video" beat (long enough to
        // carry the scene narration): pulse a soft highlight down the rows twice,
        // settle, then stamp a final mutation so rrweb's totalTime spans the hold.
        const highlight = async (idx: number) =>
          page.evaluate((j0) => {
            document.querySelectorAll(".aar").forEach((r, j) => {
              (r as HTMLElement).style.outline = j === j0 ? "1px solid #38bdf8" : "none";
            });
          }, idx);
        for (let pass = 0; pass < 2; pass++) {
          for (let i = 0; i < ROWS.length; i++) {
            await highlight(i);
            await page.waitForTimeout(750);
          }
        }
        await page.evaluate(() => document.querySelectorAll(".aar").forEach((r) => ((r as HTMLElement).style.outline = "none")));
        await page.waitForTimeout(1800);
        // End-stamp: a no-op attribute touch so the last rrweb event lands here
        // and totalTime covers the full ~11s (rrweb is bounded by its last event).
        await page.evaluate(() => document.querySelector(".drawer")?.setAttribute("data-settled", "1"));
        await page.waitForTimeout(150);

        const report = await overflowReport(page);
        console.log(`[clipbug ${v.name}]`, JSON.stringify(report));
        await page.screenshot({ path: path.join(DIAG_DIR, `${v.name}-frame.png`) });

        // The long reasoning titles MUST overflow before and fit after.
        const longTitleOverflows = report.filter((r) => r.title.startsWith("slidey-128")).every((r) => r.overflows);
        if (v.clipBug) {
          expect(longTitleOverflows, "before: long titles should overflow their card").toBeTruthy();
        } else {
          expect(report.every((r) => !r.overflows), "after: no title should collide with the cluster").toBeTruthy();
          // The fix must TRUNCATE, not COLLAPSE: even the worst-case row (long
          // title + PASS verdict + ERR flag + full cluster) must keep a legible
          // title width. A min-width:0 that shrinks the title to ~0 would be a
          // different, worse bug — guard against it explicitly.
          expect(report.every((r) => r.width >= 60), "after: no title should collapse to near-zero width").toBeTruthy();
        }

        const { events, viewport } = await dumpCapture(page);
        writeEvents(events, path.join(ASSET_DIR, `${v.name}.rrweb.json`), viewport);
        console.log(`[clipbug ${v.name}] ${events.length} events → ${v.name}.rrweb.json`);
        expect(events.length, `${v.name} should be a replayable rrweb stream`).toBeGreaterThanOrEqual(5);
      } finally {
        await ctx.close();
      }
    }
  } finally {
    await browser.close();
  }
});
