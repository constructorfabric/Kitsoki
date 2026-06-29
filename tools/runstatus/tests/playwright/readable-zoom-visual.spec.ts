import { expect, test, type Page } from "@playwright/test";
import fs from "fs";
import path from "path";
import { makeReadableZoom, makeSpotlight } from "./_helpers/demo.js";
import { makeShot, repoRoot } from "./_helpers/server.js";

const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "readable-zoom-visual-qa");

type Rect = { top: number; left: number; width: number; height: number };

type ZoomState = {
  sourceRect: Rect;
  panelRect: Rect;
  selectRect: Rect;
  sourceBackground: string;
  sourceColor: string;
  panelBackground: string;
  panelColor: string;
  pageBackground: string;
  panelText: string;
  panelVisible: boolean;
  selectVisible: boolean;
};

type OverlayPaint = {
  present: boolean;
  opacity: string;
  backgroundColor: string;
  boxShadow: string;
  backdropFilter: string;
  webkitBackdropFilter: string;
};

type AnnotationPaintState = {
  spotBackdrop: OverlayPaint;
  spotBox: OverlayPaint;
  readableBackdrop: OverlayPaint;
};

test("readable zoom faithfully matches source colors and proportions", async ({ page }) => {
  fs.rmSync(ARTIFACT_DIR, { recursive: true, force: true });
  const shot = makeShot(ARTIFACT_DIR);
  await page.setViewportSize({ width: 1000, height: 700 });
  await page.setContent(`
    <!doctype html>
    <html>
      <head>
        <style>
          :root { color-scheme: dark; }
          html, body {
            margin: 0;
            min-height: 100%;
            background: #0d1117;
            color: #c9d1d9;
            font: 15px/1.5 -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
          }
          main {
            width: 760px;
            margin: 58px auto;
          }
          .comment {
            width: 560px;
            min-height: 152px;
            padding: 18px 22px;
            background: #161b22;
            color: #c9d1d9;
            border: 1px solid #30363d;
            border-radius: 6px;
            box-shadow: 0 12px 32px rgba(1, 4, 9, .36);
          }
          .comment h2 {
            margin: 0 0 10px;
            color: #f0f6fc;
            font-size: 18px;
            line-height: 1.35;
          }
          .comment p {
            margin: 0 0 10px;
          }
          .comment code {
            background: rgba(110, 118, 129, .4);
            color: #f0f6fc;
            border-radius: 4px;
            padding: 2px 5px;
          }
          .thread {
            margin-top: 36px;
            width: 640px;
            padding: 16px 18px;
            background: #161b22;
            border: 1px solid #30363d;
            border-radius: 6px;
          }
          .run-link {
            color: #58a6ff;
            background: transparent;
            font-weight: 600;
          }
          .light-pre {
            margin-top: 32px;
            width: 560px;
            padding: 14px 16px;
            background: #f6f8fa;
            color: #1f2328;
            border: 1px solid #d0d7de;
            border-radius: 6px;
            font: 12px/1.45 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
            white-space: pre-wrap;
          }
          .run-proof {
            margin-top: 32px;
            width: 560px;
            padding: 18px 20px;
            background: #ffffff;
            color: #17202a;
            border: 1px solid #d0d7de;
            border-radius: 6px;
          }
          .run-proof dt {
            font-weight: 700;
            margin-top: 14px;
          }
          .run-proof dt:first-child {
            margin-top: 0;
          }
          .run-proof dd {
            margin: 4px 0 0;
          }
          .state-pill {
            display: inline-block;
            padding: 2px 8px;
            border-radius: 999px;
            background: #ecfdf5;
            color: #065f46;
          }
        </style>
      </head>
      <body>
        <main>
          <section id="dark-card" class="comment">
            <h2>kitsoki answered on the thread</h2>
            <p>The App-authenticated response includes <code>state: done</code>,
            a job id, and a run URL that the requester can open.</p>
            <p>This is intentionally dark-theme GitHub-style evidence.</p>
          </section>
          <section class="thread">
            <a id="dark-link" class="run-link" href="#">https://kitsoki-test.slothattax.me/run/job-123</a>
          </section>
          <pre id="light-pre" class="light-pre">state: done
run_url: https://kitsoki-test.slothattax.me/run/job-123
source: github.com/bsacrobatix/Kitsoki/issues/123</pre>
          <section class="run-proof">
            <dl>
              <dt id="state-label">State</dt>
              <dd><span class="state-pill">done</span></dd>
            </dl>
          </section>
        </main>
      </body>
    </html>
  `);

  await shot(page, "01-source");
  const spotlight = await makeSpotlight(page);
  await spotlight("#dark-card");
  await shot(page, "01b-spotlight-outline");
  assertNonObscuringAnnotationPaint(await readAnnotationPaintState(page));
  await spotlight(null);

  const zoom = await makeReadableZoom(page);

  const cardOpening = zoom("#dark-card", { fontSize: 22, selectHoldMs: 650 });
  await page.waitForTimeout(220);
  await shot(page, "02-card-selected");
  assertNonObscuringAnnotationPaint(await readAnnotationPaintState(page));
  await cardOpening;
  await page.waitForTimeout(120);
  await shot(page, "03-card-expanded");
  assertNonObscuringAnnotationPaint(await readAnnotationPaintState(page));
  const cardState = await readZoomState(page, "#dark-card");
  assertDarkSourcePreserved(cardState);
  assertUniformExpansion(cardState);

  await zoom(null);
  await shot(page, "04-card-returned");
  await expect(page.locator("#demo-readable-zoom.show")).toHaveCount(0);

  const linkOpening = zoom("#dark-link", { fontSize: 24, selectHoldMs: 650 });
  await page.waitForTimeout(220);
  await shot(page, "05-link-selected");
  await linkOpening;
  await page.waitForTimeout(120);
  await shot(page, "06-link-expanded");
  const linkState = await readZoomState(page, "#dark-link");
  assertDarkSourcePreserved(linkState);
  assertUniformExpansion(linkState);
  expect(linkState.panelRect.height).toBeLessThan(90);

  await zoom(null);
  await shot(page, "07-link-returned");
  await expect(page.locator("#demo-readable-zoom.show")).toHaveCount(0);

  const preOpening = zoom("#light-pre", { fontSize: 20, selectHoldMs: 650 });
  await page.waitForTimeout(220);
  await shot(page, "08-light-pre-selected");
  await preOpening;
  await page.waitForTimeout(120);
  await shot(page, "09-light-pre-expanded");
  const preState = await readZoomState(page, "#light-pre");
  // A light evidence block on a dark page must keep its own light surface — the
  // zoom is a magnified copy, not a recolored card. (Regression guard: it was
  // previously coerced to a forced-dark #0d1117 panel.)
  assertFaithfulSource(preState);
  expect(luminance(preState.panelBackground)).toBeGreaterThan(0.85);
  assertUniformExpansion(preState);

  await zoom(null);
  await shot(page, "10-light-pre-returned");
  await expect(page.locator("#demo-readable-zoom.show")).toHaveCount(0);

  const stateOpening = zoom("#state-label", { fontSize: 20, selectHoldMs: 650 });
  await page.waitForTimeout(220);
  await shot(page, "11-state-selected");
  const stateResult = await stateOpening;
  await page.waitForTimeout(120);
  await shot(page, "12-state-expanded");
  const definitionState = await readZoomState(page, "#state-label");
  expect(stateResult.resolvedSourceKind).toBe("definition-pair");
  expect((stateResult.sourceText || "").toLowerCase()).toContain("state done");
  expect(definitionState.panelText).toMatch(/\bState\b\s*\bdone\b/);
  expect(stateResult.sourceRect?.height).toBeGreaterThan(35);
  // The run-proof State row is a light surface; it must stay light and match.
  assertFaithfulSource(definitionState);
  expect(luminance(definitionState.panelBackground)).toBeGreaterThan(0.85);
  assertUniformExpansion({ ...definitionState, sourceRect: stateResult.sourceRect! });

  await zoom(null);
  await shot(page, "13-state-returned");
  await expect(page.locator("#demo-readable-zoom.show")).toHaveCount(0);

  console.log(`[readable-zoom-visual] screenshots in ${ARTIFACT_DIR}`);
});

async function readZoomState(page: Page, selector: string): Promise<ZoomState> {
  return await page.evaluate((sel) => {
    const source = document.querySelector<HTMLElement>(sel);
    const panel = document.getElementById("demo-readable-zoom");
    const select = document.getElementById("demo-readable-select");
    if (!source || !panel || !select) throw new Error("missing readable zoom elements");
    const sourceStyle = getComputedStyle(source);
    const panelStyle = getComputedStyle(panel);
    const bodyStyle = getComputedStyle(document.body);
    return {
      sourceRect: rect(source),
      panelRect: rect(panel),
      selectRect: rect(select),
      sourceBackground: effectiveBackground(source),
      sourceColor: sourceStyle.color,
      panelBackground: panelStyle.backgroundColor,
      panelColor: panelStyle.color,
      pageBackground: bodyStyle.backgroundColor,
      panelText: ((panel as HTMLElement).innerText || panel.textContent || "").replace(/\s+/g, " ").trim(),
      panelVisible: panel.classList.contains("show"),
      selectVisible: select.classList.contains("show"),
    };

    function rect(el: Element): Rect {
      const r = el.getBoundingClientRect();
      return {
        top: Math.round(r.top),
        left: Math.round(r.left),
        width: Math.round(r.width),
        height: Math.round(r.height),
      };
    }
    function effectiveBackground(el: HTMLElement): string {
      let current: HTMLElement | null = el;
      while (current) {
        const bg = getComputedStyle(current).backgroundColor;
        if (isOpaque(bg)) return bg;
        current = current.parentElement;
      }
      return getComputedStyle(document.body).backgroundColor;
    }
    function isOpaque(value: string): boolean {
      if (!value || value === "transparent") return false;
      const match = value.match(/rgba?\(([^)]+)\)/);
      if (!match) return true;
      const parts = match[1].split(",").map((part) => part.trim());
      return parts.length < 4 || Number(parts[3]) > 0.01;
    }
  }, selector);
}

async function readAnnotationPaintState(page: Page): Promise<AnnotationPaintState> {
  return await page.evaluate(() => {
    return {
      spotBackdrop: paint("demo-spot-back"),
      spotBox: paint("demo-spot"),
      readableBackdrop: paint("demo-readable-back"),
    };

    function paint(id: string): OverlayPaint {
      const el = document.getElementById(id);
      if (!el) {
        return {
          present: false,
          opacity: "0",
          backgroundColor: "rgba(0, 0, 0, 0)",
          boxShadow: "none",
          backdropFilter: "none",
          webkitBackdropFilter: "none",
        };
      }
      const style = getComputedStyle(el);
      return {
        present: true,
        opacity: style.opacity,
        backgroundColor: style.backgroundColor,
        boxShadow: style.boxShadow,
        backdropFilter: style.backdropFilter,
        webkitBackdropFilter: style.getPropertyValue("-webkit-backdrop-filter"),
      };
    }
  });
}

function assertNonObscuringAnnotationPaint(state: AnnotationPaintState): void {
  for (const [name, paint] of [
    ["spot backdrop", state.spotBackdrop],
    ["readable backdrop", state.readableBackdrop],
  ] as const) {
    if (!paint.present) continue;
    expect(Number(paint.opacity), `${name} must stay visually transparent`).toBeLessThanOrEqual(0.01);
    expect(isTransparent(paint.backgroundColor), `${name} must not tint the page`).toBeTruthy();
    expect(isNoBackdropFilter(paint.backdropFilter), `${name} must not blur the page`).toBeTruthy();
    expect(isNoBackdropFilter(paint.webkitBackdropFilter), `${name} must not blur the page`).toBeTruthy();
    expect(hasDarkScreenMask(paint.boxShadow), `${name} must not paint a full-screen mask`).toBeFalsy();
  }
  if (state.spotBox.present) {
    expect(hasDarkScreenMask(state.spotBox.boxShadow), "spotlight outline must not dim the page outside the target").toBeFalsy();
  }
}

// The zoom panel is a magnified copy of the real element, so its background and
// text colour must match the source's own rendered colours — never coerced to a
// flat black or white. Works for both dark and light sources.
function assertFaithfulSource(state: ZoomState): void {
  expect(state.panelVisible).toBeTruthy();
  expect(state.selectVisible).toBeTruthy();
  expect(colorDistance(state.panelBackground, state.sourceBackground)).toBeLessThan(64);
  expect(colorDistance(state.panelColor, state.sourceColor)).toBeLessThan(24);
}

function assertDarkSourcePreserved(state: ZoomState): void {
  assertFaithfulSource(state);
  expect(luminance(state.sourceBackground)).toBeLessThan(0.08);
  expect(luminance(state.panelBackground)).toBeLessThan(0.08);
}

function assertUniformExpansion(state: ZoomState): void {
  const scaleX = state.panelRect.width / state.sourceRect.width;
  const scaleY = state.panelRect.height / state.sourceRect.height;
  expect(scaleX).toBeGreaterThan(1.04);
  expect(scaleY).toBeGreaterThan(1.04);
  expect(Math.abs(scaleX - scaleY) / Math.max(scaleX, scaleY)).toBeLessThan(0.12);
}

function isTransparent(value: string): boolean {
  if (!value || value === "transparent") return true;
  const match = value.match(/rgba?\(([^)]+)\)/);
  if (!match) return false;
  const parts = match[1].split(",").map((part) => part.trim());
  return parts.length >= 4 && Number(parts[3]) <= 0.01;
}

function isNoBackdropFilter(value: string): boolean {
  return !value || value === "none" || value === "blur(0px)";
}

function hasDarkScreenMask(value: string): boolean {
  const normalized = value.toLowerCase();
  return /rgba?\(2,\s*6,\s*23/.test(normalized) || /\b[1-9]\d{3}px\b/.test(normalized);
}

function colorDistance(a: string, b: string): number {
  const ca = rgb(a);
  const cb = rgb(b);
  return Math.sqrt(
    (ca.r - cb.r) ** 2 +
    (ca.g - cb.g) ** 2 +
    (ca.b - cb.b) ** 2,
  );
}

function luminance(value: string): number {
  const { r, g, b } = rgb(value);
  const linear = [r, g, b].map((part) => {
    const c = part / 255;
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  });
  return 0.2126 * linear[0] + 0.7152 * linear[1] + 0.0722 * linear[2];
}

function rgb(value: string): { r: number; g: number; b: number } {
  const match = value.match(/rgba?\(([^)]+)\)/);
  if (!match) throw new Error(`unsupported color ${value}`);
  const [r, g, b] = match[1].split(",").slice(0, 3).map((part) => Number(part.trim()));
  return { r, g, b };
}
