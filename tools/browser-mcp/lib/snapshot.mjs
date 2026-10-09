// Capped, interactive-only, ref-based page snapshots (browser_snapshot,
// browser_find). A stale ref fails loudly — refs are single-use per
// snapshot generation; resolving a ref from a prior snapshot after the DOM
// changed throws rather than silently rebinding to whatever now occupies
// that index.
//
// Implementation note: Stagehand's wrapped Page/Locator lacks
// Locator.all()/.evaluate() (see anchors.mjs's header comment), so capture
// runs as a single page.evaluate() over the real DOM that tags each
// captured element with a one-shot data-kitsoki-ref attribute; resolveRef
// then hands back a plain page.locator([data-kitsoki-ref="..."]).

export const DEFAULT_SNAPSHOT_CAP = 50;

const INTERACTIVE_SELECTOR = [
  "a[href]",
  "button",
  "input:not([type=hidden])",
  "select",
  "textarea",
  "[role]",
  "[data-testid]",
  "[tabindex]",
  "[contenteditable=true]"
].join(",");

function captureInPage({ selector, cap, withinSelector }) {
  document.querySelectorAll("[data-kitsoki-ref]").forEach((el) => el.removeAttribute("data-kitsoki-ref"));

  const scopeRoot = withinSelector ? document.querySelector(withinSelector) : document;
  if (withinSelector && !scopeRoot) {
    return { total: 0, refs: [], truncated: false };
  }
  const all = Array.from(scopeRoot.querySelectorAll(selector));
  const total = all.length;
  const slice = all.slice(0, cap);
  const refs = slice.map((node, i) => {
    const ref = `e${i + 1}`;
    node.setAttribute("data-kitsoki-ref", ref);
    return {
      ref,
      tag: node.tagName.toLowerCase(),
      role: node.getAttribute("role") || null,
      testid: node.getAttribute("data-testid") || null,
      name: node.getAttribute("aria-label") || node.getAttribute("name") || null,
      text: (node.textContent || "").trim().slice(0, 120)
    };
  });
  return { total, refs, truncated: total > cap };
}

// Captures up to `cap` interactive elements from the live page (or the
// subtree under `withinSelector`, a CSS selector) and assigns each a
// stable-for-this-snapshot ref ("e1", "e2", ...). Returns
// {generation, refs, truncated, total, cap}.
export async function captureSnapshot(page, { cap = DEFAULT_SNAPSHOT_CAP, withinSelector } = {}) {
  const result = await page.evaluate(captureInPage, { selector: INTERACTIVE_SELECTOR, cap, withinSelector });
  return { generation: Date.now(), cap, ...result };
}

// Filters a captured snapshot's refs by a case-insensitive substring match
// against name/text/testid/role/tag — never returns the full page, only the
// matching subset.
export function filterSnapshot(snapshot, query) {
  const q = String(query || "").toLowerCase();
  if (!q) return { ...snapshot, refs: [] };
  const refs = snapshot.refs.filter((r) =>
    [r.name, r.text, r.testid, r.role, r.tag].some((field) => field && String(field).toLowerCase().includes(q))
  );
  return { ...snapshot, refs };
}

// Resolves a ref from the most recent captureSnapshot() call against the
// live page. Throws when the ref is no longer tagged in the DOM (it was
// removed, or the DOM changed enough that a fresh capture cleared it)
// rather than silently returning a different element — a caller that needs
// stability across DOM mutation should re-snapshot and re-find instead of
// holding a stale ref.
export async function resolveRef(page, ref) {
  if (!/^e[1-9][0-9]*$/.test(String(ref))) {
    throw new Error(`invalid ref ${JSON.stringify(ref)}: expected "e<N>"`);
  }
  const locator = page.locator(`[data-kitsoki-ref="${ref}"]`);
  const count = await locator.count();
  if (count !== 1) {
    throw new Error(`stale ref ${JSON.stringify(ref)}: ${count} elements now carry it; re-snapshot`);
  }
  return locator;
}
