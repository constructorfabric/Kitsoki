import { expect, test, type Page } from "@playwright/test";
import fs from "fs";
import path from "path";
import { RRWEB_BUNDLE, RRWEB_STYLE } from "./_helpers/rrweb-replay.js";
import { repoRoot } from "./_helpers/server.js";

const MEDIA_ROOT = path.join(repoRoot, ".artifacts", "github-agent-live");
const OUT_DIR = path.join(repoRoot, ".artifacts", "github-agent-live-zoom-qa");
const CASES = ["bug-issue", "feature-issue", "guidance", "pr-status"] as const;

type RrwebEvent = {
  type?: number;
  timestamp?: number;
  data?: {
    tag?: string;
    payload?: {
      title?: string;
      selector?: string;
      sourceSelector?: string;
      sourceText?: string;
      resolvedSourceKind?: string;
      sourceRect?: { width?: number; height?: number };
      finalRect?: { width?: number; height?: number };
      styleSignature?: {
        pageBackgroundColor?: string;
        backgroundColor?: string;
        color?: string;
        themeAdjusted?: boolean;
      };
    };
  };
};

type ZoomFrameState = {
  panelVisible: boolean;
  panelRect: { width: number; height: number };
  panelBackground: string;
  panelColor: string;
  panelTextSample: string;
  panelText: string;
  sourceBackground: string;
  sourceColor: string;
  expectedBackground: string;
  expectedColor: string;
  pageBackground: string;
  annotationPaint: AnnotationPaintState;
  contentScale: ContentScaleState;
};

type ContentScaleState = {
  panelWidthScale: number;
  panelHeightScale: number;
  rootWidthScale: number;
  descendantWidthScale: number;
  sourceMaxDescendantWidth: number;
  cloneMaxDescendantWidth: number;
  sourceFontSize: number;
  cloneFontSize: number;
  fontScale: number;
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

test("live GitHub readable zooms replay with correct colors", async ({ page }) => {
  fs.rmSync(OUT_DIR, { recursive: true, force: true });
  fs.mkdirSync(OUT_DIR, { recursive: true });

  let checked = 0;
  const bugThreadZooms = new Set<string>();
  for (const caseSlug of CASES) {
    const caseDir = path.join(MEDIA_ROOT, caseSlug);
    if (!fs.existsSync(caseDir)) continue;
    const logs = fs.readdirSync(caseDir).filter((file) => file.endsWith(".rrweb.json")).sort();
    for (const file of logs) {
      const logPath = path.join(caseDir, file);
      const raw = JSON.parse(fs.readFileSync(logPath, "utf8"));
      const events = (Array.isArray(raw) ? raw : raw.events ?? []) as RrwebEvent[];
      const firstTimestamp = events.find((event) => Number.isFinite(event.timestamp))?.timestamp ?? 0;
      const zoomEvents = events.filter((event) => event.type === 5 && event.data?.tag === "kitsoki.readable_zoom");
      if (zoomEvents.length === 0) continue;

      await mountReplay(page, events);
      for (let i = 0; i < zoomEvents.length; i += 1) {
        const zoom = zoomEvents[i];
        const seekMs = Math.max(0, (zoom.timestamp ?? firstTimestamp) - firstTimestamp + 1100);
        await seekReplay(page, seekMs);
        const out = path.join(OUT_DIR, `${caseSlug}-${file.replace(".rrweb.json", "")}-zoom-${i + 1}.png`);
        await page.screenshot({ path: out, fullPage: false });
        const payload = zoom.data?.payload ?? {};
        const state = await readZoomFrameState(page, payload);
        checked += 1;
        expect(state.panelVisible, `${caseSlug}/${file} zoom ${i + 1} should be visible`).toBeTruthy();
        expect(state.panelRect.width, `${caseSlug}/${file} zoom ${i + 1} width`).toBeGreaterThan(10);
        expect(state.panelRect.height, `${caseSlug}/${file} zoom ${i + 1} height`).toBeGreaterThan(10);
        expect(
          colorDistance(state.panelBackground, state.expectedBackground),
          `${caseSlug}/${file} zoom ${i + 1} background must match selected source; screenshot ${out}`,
        ).toBeLessThan(72);
        expect(
          colorDistance(state.panelColor, state.expectedColor),
          `${caseSlug}/${file} zoom ${i + 1} foreground must match selected source; screenshot ${out}`,
        ).toBeLessThan(72);
        assertNonObscuringAnnotationPaint(
          state.annotationPaint,
          `${caseSlug}/${file} zoom ${i + 1}; screenshot ${out}`,
        );
        if (caseSlug === "bug-issue" && /01-github-thread/.test(file)) {
          bugThreadZooms.add(payload.title ?? "");
          assertBugIssueCommentZoom(payload, state, `${caseSlug}/${file} zoom ${i + 1}; screenshot ${out}`);
        }
        if (/job state/i.test(payload.title ?? "")) {
          expect(
            state.panelTextSample.toLowerCase(),
            `${caseSlug}/${file} zoom ${i + 1} should show the state label and value; screenshot ${out}`,
          ).toMatch(/\bstate\b.*\b(done|running|queued|failed|awaiting[_\s-]*guidance|complete|completed)\b/);
        }
      }
    }
  }

  for (const requiredTitle of ["Read the bug report", "Read the requester comment", "Read the App response"]) {
    expect(
      bugThreadZooms.has(requiredTitle),
      `bug-issue thread rrweb must include full-comment zoom beat: ${requiredTitle}`,
    ).toBeTruthy();
  }
  expect(checked, "expected at least one readable zoom in live rrweb logs").toBeGreaterThan(0);
  console.log(`[github-agent-live-zoom-qa] checked ${checked} zoom frame(s); screenshots in ${OUT_DIR}`);
});

async function mountReplay(page: Page, events: RrwebEvent[]): Promise<void> {
  await page.setViewportSize({ width: 1600, height: 900 });
  await page.addScriptTag({ path: RRWEB_BUNDLE });
  await page.addStyleTag({ path: RRWEB_STYLE });
  await page.setContent(
    `<!doctype html><html><head><meta charset="utf-8">
      <style>
        html,body{margin:0;padding:0;background:#070d1a;width:100%;height:100%;overflow:hidden}
        #replay-host{position:fixed;inset:0;background:#070d1a}
        #replay-host .replayer-wrapper{position:absolute;top:0;left:0;transform:none!important}
        #replay-host iframe{border:none;background:#fff}
      </style></head>
      <body><div id="replay-host"></div></body></html>`,
    { waitUntil: "load" },
  );
  await page.evaluate(
    ({ evts }) => {
      const host = document.getElementById("replay-host");
      if (!host) throw new Error("missing replay host");
      const rrweb = (window as unknown as { rrweb?: { Replayer?: new (events: unknown[], opts: Record<string, unknown>) => { pause(t?: number): void } } }).rrweb;
      if (!rrweb?.Replayer) throw new Error("missing rrweb Replayer");
      const player = new rrweb.Replayer(evts as unknown[], {
        root: host,
        speed: 1,
        skipInactive: false,
        showWarning: false,
        mouseTail: false,
      });
      (window as unknown as { __player?: { pause(t?: number): void } }).__player = player;
      player.pause(0);
    },
    { evts: events },
  );
  await page.waitForSelector("#replay-host iframe", { timeout: 10000 });
  await page.waitForTimeout(500);
}

async function seekReplay(page: Page, seekMs: number): Promise<void> {
  await page.evaluate((t) => {
    const player = (window as unknown as { __player?: { pause(t?: number): void } }).__player;
    if (!player) throw new Error("missing player");
    player.pause(t);
  }, seekMs);
  await page.waitForTimeout(350);
}

async function readZoomFrameState(page: Page, payload: NonNullable<RrwebEvent["data"]>["payload"]): Promise<ZoomFrameState> {
  return await page.evaluate((p) => {
    const iframe = document.querySelector<HTMLIFrameElement>("#replay-host iframe");
    const doc = iframe?.contentDocument;
    if (!doc) throw new Error("missing replay iframe document");
    const panel = doc.getElementById("demo-readable-zoom");
    if (!panel) throw new Error("missing readable zoom panel");
    const sel = p?.sourceSelector || p?.selector || "";
    const source = sel ? doc.querySelector<HTMLElement>(sel) : null;
    if (!source) throw new Error(`missing readable zoom source ${sel}`);
    const panelStyle = doc.defaultView!.getComputedStyle(panel);
    const content = panel.querySelector<HTMLElement>(".rz-source-copy") || panel;
    const contentStyle = doc.defaultView!.getComputedStyle(content);
    const contentTag = content.tagName.toLowerCase();
    const sourceStyle = doc.defaultView!.getComputedStyle(source);
    const bodyStyle = doc.defaultView!.getComputedStyle(doc.body);
    const rect = panel.getBoundingClientRect();
    const sourceBackground = effectiveBackground(source, sourceStyle.color, p?.styleSignature?.backgroundColor);
    const sourceColor = sourceStyle.color;
    const expectedBackground = normalizeColor(p?.styleSignature?.backgroundColor) || sourceBackground;
    const expectedColor = p?.styleSignature?.themeAdjusted && contentTag === "a"
      ? "rgb(88, 166, 255)"
      : p?.styleSignature?.themeAdjusted
      ? "rgb(230, 237, 243)"
      : normalizeColor(p?.styleSignature?.color) || sourceColor;
    return {
      panelVisible: panel.classList.contains("show") && panelStyle.opacity !== "0",
      panelRect: { width: Math.round(rect.width), height: Math.round(rect.height) },
      panelBackground: panelStyle.backgroundColor,
      panelColor: contentStyle.color,
      panelTextSample: (panel.innerText || panel.textContent || "").replace(/\s+/g, " ").trim().slice(0, 160),
      panelText: (panel.innerText || panel.textContent || "").replace(/\s+/g, " ").trim().slice(0, 1200),
      sourceBackground,
      sourceColor,
      expectedBackground,
      expectedColor,
      pageBackground: bodyStyle.backgroundColor,
      annotationPaint: {
        spotBackdrop: paint("demo-spot-back"),
        spotBox: paint("demo-spot"),
        readableBackdrop: paint("demo-readable-back"),
      },
      contentScale: measureContentScale(source, panel, content, p?.sourceRect, p?.finalRect),
    };
    function measureContentScale(
      sourceEl: HTMLElement,
      panelEl: HTMLElement,
      contentEl: HTMLElement,
      sourceMarker: { width?: number; height?: number } | undefined,
      finalMarker: { width?: number; height?: number } | undefined,
    ): ContentScaleState {
      const panelRect = panelEl.getBoundingClientRect();
      const sourceRect = sourceEl.getBoundingClientRect();
      const contentRect = contentEl.getBoundingClientRect();
      const sourceWidth = Math.max(1, sourceMarker?.width ?? sourceRect.width);
      const sourceHeight = Math.max(1, sourceMarker?.height ?? sourceRect.height);
      const finalWidth = Math.max(1, finalMarker?.width ?? panelRect.width);
      const finalHeight = Math.max(1, finalMarker?.height ?? panelRect.height);
      const sourceFontSize = parseFloat(doc.defaultView!.getComputedStyle(sourceEl).fontSize || "0");
      const cloneFontSize = parseFloat(doc.defaultView!.getComputedStyle(contentEl).fontSize || "0");
      const sourceMaxDescendantWidth = largestDescendantWidth(sourceEl);
      const cloneMaxDescendantWidth = largestDescendantWidth(contentEl);
      return {
        panelWidthScale: finalWidth / sourceWidth,
        panelHeightScale: finalHeight / sourceHeight,
        rootWidthScale: contentRect.width / sourceWidth,
        descendantWidthScale: cloneMaxDescendantWidth / Math.max(1, sourceMaxDescendantWidth),
        sourceMaxDescendantWidth,
        cloneMaxDescendantWidth,
        sourceFontSize,
        cloneFontSize,
        fontScale: cloneFontSize / Math.max(1, sourceFontSize),
      };
    }
    function largestDescendantWidth(root: HTMLElement): number {
      let max = 0;
      for (const el of Array.from(root.querySelectorAll<HTMLElement>("*"))) {
        const rect = el.getBoundingClientRect();
        const style = doc.defaultView!.getComputedStyle(el);
        if (rect.width <= 24 || rect.height <= 8 || style.display === "none" || style.visibility === "hidden") continue;
        max = Math.max(max, rect.width);
      }
      return Math.round(max);
    }
    function paint(id: string): OverlayPaint {
      const el = doc.getElementById(id);
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
      const style = doc.defaultView!.getComputedStyle(el);
      return {
        present: true,
        opacity: style.opacity,
        backgroundColor: style.backgroundColor,
        boxShadow: style.boxShadow,
        backdropFilter: style.backdropFilter,
        webkitBackdropFilter: style.getPropertyValue("-webkit-backdrop-filter"),
      };
    }
    function effectiveBackground(el: HTMLElement, foreground: string, explicitFallback: string | undefined): string {
      let current: HTMLElement | null = el;
      while (current) {
        const bg = doc.defaultView!.getComputedStyle(current).backgroundColor;
        if (isOpaque(bg)) return bg;
        current = current.parentElement;
      }
      const htmlBg = doc.defaultView!.getComputedStyle(doc.documentElement).backgroundColor;
      if (isOpaque(bodyStyle.backgroundColor)) return bodyStyle.backgroundColor;
      if (isOpaque(htmlBg)) return htmlBg;
      const fallback = normalizeColor(explicitFallback);
      if (fallback && isOpaque(fallback)) return fallback;
      return colorLuminance(foreground) > 0.55 ? "rgb(13, 17, 23)" : "rgb(255, 255, 255)";
    }
    function normalizeColor(value: string | undefined): string {
      if (!value || value === "transparent") return "";
      if (value.startsWith("#")) {
        const hex = value.length === 4
          ? value.slice(1).split("").map((ch) => ch + ch).join("")
          : value.slice(1);
        return `rgb(${parseInt(hex.slice(0, 2), 16)}, ${parseInt(hex.slice(2, 4), 16)}, ${parseInt(hex.slice(4, 6), 16)})`;
      }
      return value;
    }
    function isOpaque(value: string): boolean {
      if (!value || value === "transparent") return false;
      const match = value.match(/rgba?\(([^)]+)\)/);
      if (!match) return true;
      const parts = match[1].split(",").map((part) => part.trim());
      return parts.length < 4 || Number(parts[3]) > 0.01;
    }
    function colorLuminance(value: string): number {
      const match = value.match(/rgba?\(([^)]+)\)/);
      if (!match) return 0;
      const [r, g, b] = match[1].split(",").slice(0, 3).map((part) => Number(part.trim()));
      const linear = [r, g, b].map((part) => {
        const c = part / 255;
        return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
      });
      return 0.2126 * linear[0] + 0.7152 * linear[1] + 0.0722 * linear[2];
    }
  }, payload);
}

function assertNonObscuringAnnotationPaint(state: AnnotationPaintState, label: string): void {
  for (const [name, paint] of [
    ["spot backdrop", state.spotBackdrop],
    ["readable backdrop", state.readableBackdrop],
  ] as const) {
    if (!paint.present) continue;
    expect(Number(paint.opacity), `${label}: ${name} must stay visually transparent`).toBeLessThanOrEqual(0.01);
    expect(isTransparent(paint.backgroundColor), `${label}: ${name} must not tint the page`).toBeTruthy();
    expect(isNoBackdropFilter(paint.backdropFilter), `${label}: ${name} must not blur the page`).toBeTruthy();
    expect(isNoBackdropFilter(paint.webkitBackdropFilter), `${label}: ${name} must not blur the page`).toBeTruthy();
    expect(hasDarkScreenMask(paint.boxShadow), `${label}: ${name} must not paint a full-screen mask`).toBeFalsy();
  }
  if (state.spotBox.present) {
    expect(hasDarkScreenMask(state.spotBox.boxShadow), `${label}: spotlight outline must not dim the page outside the target`).toBeFalsy();
  }
}

function assertBugIssueCommentZoom(
  payload: NonNullable<RrwebEvent["data"]>["payload"],
  state: ZoomFrameState,
  label: string,
): void {
  const title = payload?.title ?? "";
  const sourceText = (payload?.sourceText ?? "").replace(/\s+/g, " ").trim();
  const panelText = state.panelText.replace(/\s+/g, " ").trim();
  if (!/Read the (bug report|requester comment|App response)/.test(title)) return;

  expect(sourceText.length, `${label}: comment zoom should capture a full GitHub comment, not a tiny text node`).toBeGreaterThan(80);
  expect(payload?.resolvedSourceKind, `${label}: comment zoom should resolve the selected comment as one DOM element`).toBe("element");
  expect(
    Math.min(payload?.sourceRect?.width ?? 0, state.panelRect.width),
    `${label}: comment zoom should use the wide GitHub comment box geometry`,
  ).toBeGreaterThan(360);
  assertContentScalesWithPanel(state.contentScale, label);

  if (/requester comment/.test(title)) {
    expect(sourceText, `${label}: requester zoom must include the full mention comment`).toMatch(/@kitsoki/i);
    expect(sourceText, `${label}: requester zoom must include GitHub comment author/context`).toMatch(/\b(brad|commented|maintainer|owner)\b/i);
    expect(panelText, `${label}: panel should show more than only the mention token`).not.toMatch(/^@kitsoki\b\s*$/i);
  }
  if (/App response/.test(title)) {
    expect(sourceText, `${label}: app-response zoom must include the run link`).toMatch(/kitsoki-test\.slothattax\.me\/run\//i);
    expect(sourceText, `${label}: app-response zoom must include kitsoki/GitHub comment context`).toMatch(/\b(kitsoki|commented|bot)\b/i);
    expect(panelText, `${label}: app-response replay panel must visibly include the run link`).toMatch(/run_url:\s*https:\/\/kitsoki-test\.slothattax\.me\/run\//i);
  }
}

function assertContentScalesWithPanel(scale: ContentScaleState, label: string): void {
  expect(scale.panelWidthScale, `${label}: selected box should expand wide enough for a meaningful zoom`).toBeGreaterThan(1.05);
  expect(
    scale.rootWidthScale,
    `${label}: cloned root should expand with the panel, not remain at source width`,
  ).toBeGreaterThanOrEqual(scale.panelWidthScale * 0.86);
  expect(
    scale.descendantWidthScale,
    `${label}: cloned content descendants should expand with the panel; source max ${scale.sourceMaxDescendantWidth}px, clone max ${scale.cloneMaxDescendantWidth}px`,
  ).toBeGreaterThanOrEqual(scale.panelWidthScale * 0.86);
  expect(
    scale.fontScale,
    `${label}: cloned text should scale with the expanded panel`,
  ).toBeGreaterThanOrEqual(Math.min(scale.panelWidthScale, 1.12) * 0.92);
}

function colorDistance(a: string, b: string): number {
  const ca = rgb(a);
  const cb = rgb(b);
  return Math.sqrt((ca.r - cb.r) ** 2 + (ca.g - cb.g) ** 2 + (ca.b - cb.b) ** 2);
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

function rgb(value: string): { r: number; g: number; b: number } {
  const match = value.match(/rgba?\(([^)]+)\)/);
  if (!match) {
    if (value.startsWith("#")) {
      const hex = value.length === 4
        ? value.slice(1).split("").map((ch) => ch + ch).join("")
        : value.slice(1);
      return {
        r: parseInt(hex.slice(0, 2), 16),
        g: parseInt(hex.slice(2, 4), 16),
        b: parseInt(hex.slice(4, 6), 16),
      };
    }
    return { r: 255, g: 255, b: 255 };
  }
  const [r, g, b] = match[1].split(",").slice(0, 3).map((part) => Number(part.trim()));
  return { r, g, b };
}
