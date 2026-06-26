import { buildSessionEnvelope, snapshotSessionEvents, type RrwebEnvelope } from "../data/session-capture.js";

export interface VisualRect {
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface VisualAction {
  handle: string;
  selector: string;
  testid?: string;
  role: string;
  label: string;
  disabled: boolean;
  bbox: VisualRect;
}

export interface VisualRegion {
  id: string;
  label: string;
  selector: string;
  bbox: VisualRect;
}

export interface VisualObservation {
  ok: true;
  route: string;
  title: string;
  viewport: { width: number; height: number };
  focused: string;
  dirty_regions: string[];
  regions: VisualRegion[];
  actions: VisualAction[];
  observed_at: string;
}

export interface KitsokiVisualHelper {
  observe(): VisualObservation;
  recording(): RrwebEnvelope;
  dirtyRegions(): string[];
  resetDirty(): void;
  resolve(handle: string): VisualAction | null;
}

interface InstallOptions {
  document?: Document;
  window?: Window;
}

const ACTION_SELECTOR = [
  "button",
  "a[href]",
  "input",
  "textarea",
  "select",
  "[role='button']",
  "[role='link']",
  "[role='menuitem']",
  "[role='tab']",
  "[data-testid]",
].join(",");

const TEXT_LIMIT = 96;
const ACTION_LIMIT = 80;

const REGION_DEFS = [
  { id: "chat", label: "Chat", selectors: ["[data-testid='chat-section']", "[data-testid='surface-chat']", "[data-testid='chat-transcript']"] },
  { id: "media", label: "Media", selectors: ["[data-testid='media-element']", "[data-testid='media-slideshow-frame']", "[data-testid='artifact-annotator']"] },
  { id: "deck", label: "Deck", selectors: ["[data-testid='media-slideshow-frame']", "[data-testid='aa-slidey']", "[data-testid='artifact-annotator']"] },
  { id: "annotation", label: "Annotation", selectors: ["[data-testid='media-annotate-panel']", "[data-testid='artifact-annotator']", "[data-testid='media-annotate-composer']"] },
  { id: "graph", label: "Graph", selectors: ["[data-testid='surface-graph']", "[data-testid='trace-diagram']"] },
  { id: "trace", label: "Trace", selectors: ["[data-testid='surface-trace']", "[data-testid='trace-timeline']"] },
  { id: "inbox", label: "Inbox", selectors: ["[data-testid='inbox-panel']", "[data-testid='inbox-badge']"] },
  { id: "composer", label: "Composer", selectors: ["[data-testid='composer-input']", "[data-testid='text-floor-input']", "[data-testid='input-bar']"] },
  { id: "modal", label: "Modal", selectors: ["[role='dialog']", "[data-testid$='modal']", "[data-testid='operator-question-modal']"] },
] as const;

export function installKitsokiVisualHelper(options: InstallOptions = {}): KitsokiVisualHelper | null {
  const doc = options.document ?? globalThis.document;
  const win = options.window ?? globalThis.window;
  if (!doc || !win) return null;

  const dirty = new Set<string>(["full"]);
  const markDirty = (target: Node | null): void => {
    dirty.add("full");
    const el = nodeElement(target);
    for (const region of regionsForElement(el)) dirty.add(region);
  };

  let lastHref = win.location.href;
  const observer = new MutationObserver((mutations: MutationRecord[]) => {
    for (const mutation of mutations) markDirty(mutation.target);
  });
  if (doc.body) {
    observer.observe(doc.body, { attributes: true, childList: true, subtree: true, characterData: true });
  }

  const routePoll = win.setInterval(() => {
    if (win.location.href === lastHref) return;
    lastHref = win.location.href;
    dirty.add("full");
  }, 250);
  win.addEventListener("beforeunload", () => {
    observer.disconnect();
    win.clearInterval(routePoll);
  });

  const helper: KitsokiVisualHelper = {
    observe(): VisualObservation {
      return {
        ok: true,
        route: win.location.hash || win.location.pathname || "/",
        title: doc.title,
        viewport: { width: win.innerWidth, height: win.innerHeight },
        focused: selectorFor(doc.activeElement),
        dirty_regions: Array.from(dirty).sort(),
        regions: collectRegions(doc),
        actions: collectActions(doc),
        observed_at: new Date().toISOString(),
      };
    },
    recording(): RrwebEnvelope {
      return buildSessionEnvelope(snapshotSessionEvents(), {
        source: "kitsoki-visual-record",
        viewport: { width: win.innerWidth, height: win.innerHeight, deviceScaleFactor: win.devicePixelRatio || 1 },
      });
    },
    dirtyRegions(): string[] {
      return Array.from(dirty).sort();
    },
    resetDirty(): void {
      dirty.clear();
    },
    resolve(handle: string): VisualAction | null {
      return collectActions(doc).find((action) => action.handle === handle) ?? null;
    },
  };

  (win as Window & { __kitsokiVisual?: KitsokiVisualHelper }).__kitsokiVisual = helper;
  return helper;
}

function collectActions(doc: Document): VisualAction[] {
  const seen = new Set<string>();
  const out: VisualAction[] = [];
  for (const el of Array.from(doc.querySelectorAll(ACTION_SELECTOR))) {
    if (!(el instanceof HTMLElement) || !isVisible(el) || isSecretInput(el)) continue;
    if (!isActionCandidate(el)) continue;
    const target = closestStableElement(el);
    if (!(target instanceof HTMLElement) || !isVisible(target) || isSecretInput(target)) continue;
    const selector = selectorFor(target);
    if (!selector || seen.has(selector)) continue;
    seen.add(selector);
    const bbox = rectOf(target);
    if (bbox.width <= 0 || bbox.height <= 0) continue;
    const testid = target.getAttribute("data-testid") ?? undefined;
    const handle = testid ? `testid:${testid}` : `selector:${selector}`;
    out.push({
      handle,
      selector,
      testid,
      role: roleOf(target),
      label: labelOf(target),
      disabled: isDisabled(target),
      bbox,
    });
    if (out.length >= ACTION_LIMIT) break;
  }
  return out;
}

function isActionCandidate(el: HTMLElement): boolean {
  const tag = el.tagName.toLowerCase();
  if (["button", "a", "input", "textarea", "select"].includes(tag)) return true;
  const role = el.getAttribute("role");
  return role === "button" || role === "link" || role === "menuitem" || role === "tab";
}

function collectRegions(doc: Document): VisualRegion[] {
  const out: VisualRegion[] = [];
  for (const def of REGION_DEFS) {
    for (const selector of def.selectors) {
      const el = doc.querySelector(selector);
      if (!(el instanceof HTMLElement) || !isVisible(el)) continue;
      const bbox = rectOf(el);
      if (bbox.width <= 0 || bbox.height <= 0) continue;
      out.push({ id: def.id, label: def.label, selector: selectorFor(el), bbox });
      break;
    }
  }
  return out;
}

function nodeElement(node: Node | null): Element | null {
  if (!node) return null;
  if (node.nodeType === Node.ELEMENT_NODE) return node as Element;
  return node.parentElement;
}

function regionsForElement(el: Element | null | undefined): string[] {
  if (!el) return [];
  const out: string[] = [];
  for (const def of REGION_DEFS) {
    if (def.selectors.some((selector) => el.closest(selector))) out.push(def.id);
  }
  return out;
}

function closestStableElement(el: Element): Element {
  const tagged = el.closest("[data-testid]");
  if (tagged) return tagged;
  return el;
}

function selectorFor(el: Element | null): string {
  if (!el) return "";
  const testid = el.getAttribute("data-testid");
  if (testid) return `[data-testid="${cssEscape(testid)}"]`;
  return structuralPath(el);
}

function roleOf(el: Element): string {
  const explicit = el.getAttribute("role");
  if (explicit) return explicit;
  const tag = el.tagName.toLowerCase();
  switch (tag) {
    case "button":
      return "button";
    case "a":
      return el.getAttribute("href") ? "link" : "generic";
    case "input": {
      const type = (el.getAttribute("type") ?? "text").toLowerCase();
      return type === "submit" || type === "button" ? "button" : "textbox";
    }
    case "textarea":
      return "textbox";
    case "select":
      return "combobox";
    default:
      return tag;
  }
}

function labelOf(el: HTMLElement): string {
  const aria = el.getAttribute("aria-label")?.trim();
  if (aria) return truncate(aria);
  const title = el.getAttribute("title")?.trim();
  if (title) return truncate(title);
  if (el instanceof HTMLInputElement || el instanceof HTMLTextAreaElement) {
    const placeholder = el.getAttribute("placeholder")?.trim();
    if (placeholder) return truncate(placeholder);
    return "";
  }
  return truncate((el.textContent ?? "").replace(/\s+/g, " ").trim());
}

function isDisabled(el: HTMLElement): boolean {
  return Boolean(
    el.getAttribute("aria-disabled") === "true" ||
      (el as HTMLButtonElement | HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement).disabled
  );
}

function isSecretInput(el: Element): boolean {
  return el instanceof HTMLInputElement && el.type.toLowerCase() === "password";
}

function isVisible(el: HTMLElement): boolean {
  if (el.hidden) return false;
  const style = el.ownerDocument.defaultView?.getComputedStyle(el);
  if (style && (style.display === "none" || style.visibility === "hidden")) return false;
  const r = el.getBoundingClientRect();
  return r.width > 0 && r.height > 0;
}

function rectOf(el: Element): VisualRect {
  const r = el.getBoundingClientRect();
  return {
    x: Math.round(r.left),
    y: Math.round(r.top),
    width: Math.round(r.width),
    height: Math.round(r.height),
  };
}

function truncate(s: string): string {
  return s.length > TEXT_LIMIT ? `${s.slice(0, TEXT_LIMIT - 1)}…` : s;
}

function structuralPath(el: Element): string {
  const parts: string[] = [];
  let cur: Element | null = el;
  while (cur && cur.tagName.toLowerCase() !== "html") {
    const tag = cur.tagName.toLowerCase();
    if (tag === "body") {
      parts.unshift("body");
      break;
    }
    const node: Element = cur;
    const parent: Element | null = node.parentElement;
    if (!parent) {
      parts.unshift(tag);
      break;
    }
    const same = Array.from(parent.children).filter(
      (child): child is Element => child instanceof Element && child.tagName === node.tagName
    );
    const idx = same.indexOf(node) + 1;
    parts.unshift(same.length > 1 ? `${tag}:nth-of-type(${idx})` : tag);
    cur = parent;
  }
  return parts.join(" > ");
}

function cssEscape(value: string): string {
  return value.replace(/\\/g, "\\\\").replace(/"/g, '\\"');
}

declare global {
  interface Window {
    __kitsokiVisual?: KitsokiVisualHelper;
  }
}
