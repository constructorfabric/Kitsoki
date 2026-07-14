/**
 * Player-side anchor resolution: ranked strategy order role+name -> testid
 * -> text -> css -> ancestor fallback, the first strategy that resolves to
 * EXACTLY one element wins. Same resolution order and same "never silent" a
 * heal contract as tools/browser-mcp/lib/anchors.mjs (the authoring-side
 * twin) and internal/tour/manifest_v2.go's HealEvent — kept in lockstep by
 * doc comment, not by shared code, since the player runs directly in the
 * target page (no Stagehand wrapper to work around) and browser-mcp runs
 * out-of-process over Playwright.
 */
import type { HealEvent, TargetBundle } from "./types.js";

export class AnchorResolutionError extends Error {
  anchor: TargetBundle;
  attempts: Array<{ strategy: string; count?: number; error?: string }>;
  constructor(message: string, anchor: TargetBundle, attempts: Array<{ strategy: string; count?: number; error?: string }>) {
    super(message);
    this.name = "AnchorResolutionError";
    this.anchor = anchor;
    this.attempts = attempts;
  }
}

export interface ResolvedAnchor {
  el: HTMLElement;
  strategy: string;
  heal: HealEvent | null;
}

function accessibleName(el: Element): string {
  return (
    el.getAttribute("aria-label") ||
    (el.textContent || "").trim() ||
    el.getAttribute("title") ||
    el.getAttribute("placeholder") ||
    ""
  );
}

function implicitRole(el: Element): string | null {
  const tag = el.tagName.toLowerCase();
  if (tag === "button") return "button";
  if (tag === "a" && el.hasAttribute("href")) return "link";
  if (tag === "input") {
    const type = (el.getAttribute("type") || "text").toLowerCase();
    if (["button", "submit", "reset"].includes(type)) return "button";
    return "textbox";
  }
  if (tag === "textarea") return "textbox";
  if (tag === "select") return "combobox";
  return null;
}

interface Candidate {
  strategy: string;
  matches: Element[];
}

function candidatesFor(root: ParentNode, anchor: TargetBundle): Candidate[] {
  const candidates: Candidate[] = [];
  if (anchor.role) {
    const byAttr = Array.from(root.querySelectorAll(`[role="${CSS.escape(anchor.role)}"]`));
    const byImplicit = Array.from(root.querySelectorAll("*")).filter((el) => implicitRole(el) === anchor.role);
    let matches = [...byAttr, ...byImplicit];
    if (anchor.name) {
      const needle = anchor.name.toLowerCase();
      matches = matches.filter((el) => accessibleName(el).toLowerCase().includes(needle));
    }
    candidates.push({ strategy: "role+name", matches });
  }
  if (anchor.testid) {
    candidates.push({ strategy: "testid", matches: Array.from(root.querySelectorAll(`[data-testid="${CSS.escape(anchor.testid)}"]`)) });
  }
  if (anchor.text) {
    const needle = anchor.text.toLowerCase();
    const matches = Array.from(root.querySelectorAll("*")).filter(
      (el) => el.children.length === 0 && (el.textContent || "").trim().toLowerCase().includes(needle)
    );
    candidates.push({ strategy: "text", matches });
  }
  if (anchor.css) {
    let matches: Element[] = [];
    try {
      matches = Array.from(root.querySelectorAll(anchor.css));
    } catch {
      matches = [];
    }
    candidates.push({ strategy: "css", matches });
  }
  return candidates;
}

/**
 * Resolves anchor against root (defaults to document.body — never the
 * document, so <head> text never false-matches a text anchor). Returns the
 * resolved element, which strategy resolved it, and a HealEvent when the
 * PRIMARY strategy (the first candidate the anchor specifies) missed but a
 * later one uniquely resolved. Throws AnchorResolutionError, carrying every
 * attempt, when nothing resolves uniquely — resolution failure is always
 * loud, never a silent "first match" pick.
 */
export function resolveAnchor(anchor: TargetBundle, stepId: string, root: ParentNode = document.body): ResolvedAnchor {
  const scopeRoot = anchor.ancestor ? document.querySelector(anchor.ancestor) : root;
  const attempts: Array<{ strategy: string; count?: number; error?: string }> = [];
  if (anchor.ancestor && !scopeRoot) {
    attempts.push({ strategy: "ancestor-scope", error: "ancestor selector matched nothing" });
    throw new AnchorResolutionError(`ancestor selector matched nothing: ${anchor.ancestor}`, anchor, attempts);
  }

  let candidates = candidatesFor(scopeRoot as ParentNode, anchor);
  if (candidates.length === 0 && anchor.ancestor) {
    candidates = [{ strategy: "ancestor-fallback", matches: [scopeRoot as Element] }];
  }
  if (candidates.length === 0) {
    throw new AnchorResolutionError("anchor has no resolvable fields (role, testid, text, css, ancestor)", anchor, []);
  }

  const primaryStrategy = candidates[0].strategy;
  for (const { strategy, matches } of candidates) {
    attempts.push({ strategy, count: matches.length });
    if (matches.length === 1) {
      const el = matches[0] as HTMLElement;
      const heal: HealEvent | null =
        strategy === primaryStrategy
          ? null
          : { stepId, failedAnchor: primaryStrategy, matchedAnchor: strategy, confidence: strategy === "testid" ? 0.9 : 0.6 };
      return { el, strategy, heal };
    }
  }
  throw new AnchorResolutionError(`anchor did not resolve to exactly one element: ${JSON.stringify(anchor)}`, anchor, attempts);
}
