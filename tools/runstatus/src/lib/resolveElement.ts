/**
 * resolveElement — the spatial picker's "what is at this pixel?" resolver
 * (docs/tui/spatial-capture.md).
 *
 * `elementFromPoint(root, x, y)` is a PURE function over a DOM root: it asks the
 * root's `elementFromPoint` for the topmost node at (x, y) and describes it as a
 * stable {selector, role, text, bbox}. It is "one resolver, two roots" — pass
 * the LIVE `document` on the run surface, or the rrweb Replayer iframe's
 * `contentDocument` in a recorded/review context (proposal open-question 2). It
 * never reaches for a global `document`, so it is trivially testable against a
 * fixture root and works against either DOM.
 *
 * Identity prefers a `data-testid` ancestor — the app testid's everything
 * (`intent-btn-*`, `chat-row-*`, `composer-*`) so a testid is the most stable
 * handle (proposal open-question 1 / epic Q1). When no ancestor carries one it
 * falls back to a structural `:nth-of-type` path from the body, and always
 * records the resolved element's bbox so a downstream consumer (and the oracle)
 * can see WHERE, not just WHAT.
 *
 * The bbox is in the root document's own pixel space (the rect
 * `getBoundingClientRect` returns); the picker maps the operator's click from
 * the rendered frame back into that space before calling here (SpatialPicker).
 */

/** A resolved element: a stable selector, an ARIA-ish role, its visible text,
 *  and its bounding box. `bbox` is {x, y, width, height} in the root's pixels. */
export interface ResolvedElement {
  selector: string;
  role: string;
  text: string;
  bbox: { x: number; y: number; width: number; height: number };
}

/** Max characters of visible text we record — enough to identify, never a wall
 *  of prose (the oracle gets the selector + role besides). */
const TEXT_LIMIT = 80;

/**
 * elementFromPoint resolves the topmost element at (x, y) in `root` to a
 * {selector, role, text, bbox}, or null when nothing is there (off-frame, or a
 * root whose elementFromPoint returns null). Pure: no global DOM, no side
 * effects.
 */
export function elementFromPoint(
  root: Document,
  x: number,
  y: number
): ResolvedElement | null {
  const hit = root.elementFromPoint(x, y);
  if (!hit) return null;
  // Resolve identity against the nearest data-testid ancestor when present; the
  // selector then points at the testid'd node, but the bbox/text/role describe
  // THAT same node (the thing the operator meant), not the bare leaf.
  const tagged = closestTestid(hit);
  const target = tagged ?? hit;
  return {
    selector: tagged
      ? `[data-testid="${tagged.getAttribute("data-testid")}"]`
      : structuralPath(target),
    role: roleOf(target),
    text: textOf(target),
    bbox: bboxOf(target),
  };
}

/** Nearest ancestor-or-self carrying a non-empty data-testid, else null. */
function closestTestid(el: Element): Element | null {
  let cur: Element | null = el;
  while (cur) {
    const id = cur.getAttribute("data-testid");
    if (id) return cur;
    cur = cur.parentElement;
  }
  return null;
}

/**
 * roleOf reports an explicit `role` attribute, else maps a few common tags to
 * their implicit ARIA role, else falls back to the lowercased tag name. This is
 * a description for the operator + oracle, not a strict ARIA computation.
 */
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
    case "img":
      return "img";
    case "h1":
    case "h2":
    case "h3":
    case "h4":
    case "h5":
    case "h6":
      return "heading";
    default:
      return tag;
  }
}

/** Visible text, collapsed and truncated to TEXT_LIMIT (with an ellipsis). */
function textOf(el: Element): string {
  const raw = (el.textContent ?? "").replace(/\s+/g, " ").trim();
  return raw.length > TEXT_LIMIT ? raw.slice(0, TEXT_LIMIT - 1) + "…" : raw;
}

/** The element's bounding box as {x, y, width, height}, rounded to whole pixels
 *  (the visual bundle carries integer frame coordinates). */
function bboxOf(el: Element): {
  x: number;
  y: number;
  width: number;
  height: number;
} {
  const r = el.getBoundingClientRect();
  return {
    x: Math.round(r.left),
    y: Math.round(r.top),
    width: Math.round(r.width),
    height: Math.round(r.height),
  };
}

/**
 * structuralPath builds a `:nth-of-type` chain from <body> down to `el` — the
 * fallback identity when no data-testid is in scope. Stable enough to re-find
 * the node in the same reconstructed DOM, and human-readable in the chip
 * (e.g. `body > div:nth-of-type(2) > p`).
 */
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
    const parent = node.parentElement;
    if (!parent) {
      parts.unshift(tag);
      break;
    }
    // Index among same-tag siblings (1-based, matching :nth-of-type).
    const sameTag = Array.from(parent.children).filter(
      (c: Element) => c.tagName === node.tagName
    );
    const idx = sameTag.indexOf(node) + 1;
    parts.unshift(sameTag.length > 1 ? `${tag}:nth-of-type(${idx})` : tag);
    cur = parent;
  }
  return parts.join(" > ");
}
