// Deterministic anchor resolution for the primitive tools (browser_click,
// browser_fill, browser_find). Anchors are ranked multi-anchor bundles
// shaped exactly like tour format v2's TargetBundle
// (internal/tour/manifest_v2.go, tools/runstatus/src/tour/types-v2.ts) —
// {role,name,testid,text,css,ancestor} — so a resolved anchor from
// browser_snapshot/browser_find can be dropped straight into a tour_step
// call in P3 with no reshaping.
//
// Resolution order, per anchor field present: role+name -> testid -> text ->
// css -> ancestor-scoped fallback. The first candidate that resolves to
// EXACTLY one element wins. Anything else (zero matches, or more than one)
// is a loud failure — never a silent pick of "the first match".
//
// Implementation note: Stagehand wraps Playwright's Page/Locator with a
// reduced API (no getByRole/getByText, no Locator.evaluate/.all — see
// server.mjs's header comment). Resolution therefore runs as a single
// page.evaluate() querying the real DOM, which tags the winning element
// with a one-shot unique marker attribute; the caller gets back a plain
// page.locator('[data-kitsoki-anchor-id="..."]'), which DOES support the
// click/fill/count/nth methods the wrapper keeps.

export class AnchorResolutionError extends Error {
  constructor(message, anchor, attempts) {
    super(message);
    this.name = "AnchorResolutionError";
    this.anchor = anchor;
    this.attempts = attempts;
  }
}

const ANCHOR_FIELDS = ["role", "name", "testid", "text", "css", "ancestor"];

// Runs entirely inside the page. Returns {resolved: {strategy,id}|null, attempts}.
function computeInPage(anchor) {
  function accessibleName(el) {
    return el.getAttribute("aria-label") || (el.textContent || "").trim() || el.getAttribute("title") || el.getAttribute("placeholder") || "";
  }
  function implicitRole(el) {
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
  function markUnique(matches, strategy) {
    const unique = Array.from(new Set(matches));
    if (unique.length === 1) {
      const id = `kitsoki-anchor-${Math.random().toString(36).slice(2)}-${unique[0].getAttribute("data-kitsoki-probe") || Date.now()}`;
      unique[0].setAttribute("data-kitsoki-anchor-id", id);
      return { attempt: { strategy, count: 1 }, resolved: { strategy, id } };
    }
    return { attempt: { strategy, count: unique.length }, resolved: null };
  }

  // document.body, not document: <head> leaf elements (title, meta,
  // style/script text) are never legitimate click/fill targets but can
  // still substring-match a text anchor (e.g. a <title> that happens to
  // mention the same word), turning a real unique match into a false
  // ambiguous one.
  const scopeRoot = anchor.ancestor ? document.querySelector(anchor.ancestor) : document.body;
  const attempts = [];
  if (anchor.ancestor && !scopeRoot) {
    attempts.push({ strategy: "ancestor-scope", error: "ancestor selector matched nothing" });
    return { resolved: null, attempts };
  }

  if (anchor.role) {
    const byAttr = Array.from(scopeRoot.querySelectorAll(`[role="${CSS.escape(anchor.role)}"]`));
    const byImplicit = Array.from(scopeRoot.querySelectorAll("*")).filter((el) => implicitRole(el) === anchor.role);
    let matches = [...byAttr, ...byImplicit];
    if (anchor.name) {
      const needle = anchor.name.toLowerCase();
      matches = matches.filter((el) => accessibleName(el).toLowerCase().includes(needle));
    }
    const { attempt, resolved } = markUnique(matches, "role+name");
    attempts.push(attempt);
    if (resolved) return { resolved, attempts };
  }
  if (anchor.testid) {
    const matches = Array.from(scopeRoot.querySelectorAll(`[data-testid="${CSS.escape(anchor.testid)}"]`));
    const { attempt, resolved } = markUnique(matches, "testid");
    attempts.push(attempt);
    if (resolved) return { resolved, attempts };
  }
  if (anchor.text) {
    const needle = anchor.text.toLowerCase();
    const matches = Array.from(scopeRoot.querySelectorAll("*")).filter(
      (el) => el.children.length === 0 && (el.textContent || "").trim().toLowerCase().includes(needle)
    );
    const { attempt, resolved } = markUnique(matches, "text");
    attempts.push(attempt);
    if (resolved) return { resolved, attempts };
  }
  if (anchor.css) {
    let matches = [];
    try {
      matches = Array.from(scopeRoot.querySelectorAll(anchor.css));
    } catch (err) {
      attempts.push({ strategy: "css", error: String(err) });
      matches = [];
    }
    if (matches.length) {
      const { attempt, resolved } = markUnique(matches, "css");
      attempts.push(attempt);
      if (resolved) return { resolved, attempts };
    }
  }
  if (anchor.ancestor && attempts.length === 0) {
    const { attempt, resolved } = markUnique([scopeRoot], "ancestor-fallback");
    attempts.push(attempt);
    if (resolved) return { resolved, attempts };
  }
  return { resolved: null, attempts };
}

// Resolves anchor against page, returning {locator, strategy}. Throws
// AnchorResolutionError with the full attempt list when no strategy
// uniquely resolves — resolution failure is always loud, never a silent
// "first match" pick.
export async function resolveAnchor(page, anchor) {
  if (!anchor || typeof anchor !== "object") {
    throw new AnchorResolutionError("anchor must be an object", anchor, []);
  }
  if (!ANCHOR_FIELDS.some((field) => anchor[field])) {
    throw new AnchorResolutionError("anchor has no resolvable fields (role, testid, text, css, ancestor)", anchor, []);
  }
  const { resolved, attempts } = await page.evaluate(computeInPage, anchor);
  if (!resolved) {
    throw new AnchorResolutionError(`anchor did not resolve to exactly one element: ${JSON.stringify(anchor)}`, anchor, attempts);
  }
  return { locator: page.locator(`[data-kitsoki-anchor-id="${resolved.id}"]`), strategy: resolved.strategy };
}

// Resolves anchor, healing to a secondary strategy when the primary
// (the first field kitsoki checks, per resolution order) fails but a later
// strategy uniquely resolves. Returns {locator, strategy, heal} where heal
// is a HealEvent-shaped object (internal/tour manifest_v2.go HealEvent) or
// null when the primary strategy resolved outright. Never silent: the
// caller is expected to journal `heal` whenever it is non-null.
export async function resolveAnchorWithHeal(page, anchor, stepId) {
  if (!ANCHOR_FIELDS.some((field) => anchor[field])) {
    throw new AnchorResolutionError("anchor has no resolvable fields (role, testid, text, css, ancestor)", anchor, []);
  }
  const { resolved, attempts } = await page.evaluate(computeInPage, anchor);
  if (!resolved) {
    throw new AnchorResolutionError(`no anchor strategy resolved uniquely: ${JSON.stringify(anchor)}`, anchor, attempts);
  }
  const locator = page.locator(`[data-kitsoki-anchor-id="${resolved.id}"]`);
  const primaryStrategy = attempts[0]?.strategy;
  if (resolved.strategy === primaryStrategy) {
    return { locator, strategy: resolved.strategy, heal: null };
  }
  return {
    locator,
    strategy: resolved.strategy,
    heal: {
      stepId: stepId || "",
      failedAnchor: primaryStrategy || "unknown",
      matchedAnchor: resolved.strategy,
      confidence: resolved.strategy === "testid" ? 0.9 : 0.6
    }
  };
}
