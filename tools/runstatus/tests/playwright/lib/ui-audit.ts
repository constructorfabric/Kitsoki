/**
 * ui-audit.ts — the DETERMINISTIC (no-LLM) layout audit for the kitsoki web UI.
 *
 * This is the geometry half of the kitsoki-ui-review pipeline (the LLM vision
 * half lives in .agents/skills/kitsoki-ui-review/scripts/review.sh). It measures
 * the REAL laid-out DOM in a live browser — the same authority slidey's
 * `--audit` gives a deck, but richer, because here we have a real layout engine
 * and a real viewport rather than an SVG.
 *
 * `geometryProbe` is shipped INTO the browser via `page.evaluate(geometryProbe)`,
 * so it must be entirely self-contained: only DOM / window APIs, no imports, no
 * outer-scope references, no TS-only runtime constructs. It returns plain JSON.
 *
 * Each finding carries enough DOM CONTEXT to fix it without re-running: the
 * element's selector + ancestor path, its outerHTML, the computed styles that
 * matter for the defect, and its laid-out rect. The report pairs this with a
 * deterministic reproduction recipe (server command + viewport + tour step).
 *
 * Reliability rule (the slidey lesson — vision models over-report overflow): the
 * geometry checks are HIGH-PRECISION on purpose. Each one targets a defect class
 * a human would unambiguously reject, with explicit guards against the obvious
 * false positives (scroll containers, deliberately-clipped decorations). When in
 * doubt a check stays silent and leaves the judgement to the vision pass.
 */

/** Severity gate semantics, shared with report.sh. */
export type Severity = "error" | "warn" | "info";

/** DOM context attached to every geometry finding so it is fixable from the report. */
export interface DomContext {
  selector: string;
  path: string; // ancestor chain (outermost → element), testid/tag/id/class
  html: string; // outerHTML snippet (truncated)
  styles: Record<string, string>; // the computed styles relevant to layout defects
  rect: { x: number; y: number; w: number; h: number };
}

/** One deterministic finding, before it is tagged with surface + viewport. */
export interface RawFinding extends DomContext {
  check: string;
  severity: Severity;
  text: string;
  detail: string;
}

/**
 * Runs in the BROWSER. Walks the rendered DOM and returns high-precision layout
 * findings, each with the DOM context needed to fix it. Keep this function
 * dependency-free — it is serialized to the page.
 */
export function geometryProbe(): RawFinding[] {
  const out: RawFinding[] = [];
  const vw = window.innerWidth;
  const vh = window.innerHeight;

  const selectorOf = (el: Element): string => {
    const tid = el.getAttribute && el.getAttribute("data-testid");
    if (tid) return `[data-testid="${tid}"]`;
    const parts: string[] = [];
    let node: Element | null = el;
    let depth = 0;
    while (node && depth < 4 && node.nodeType === 1) {
      let part = node.tagName.toLowerCase();
      const ptid = node.getAttribute && node.getAttribute("data-testid");
      if (ptid) {
        parts.unshift(`[data-testid="${ptid}"]`);
        break;
      }
      if (node.id) part += `#${node.id}`;
      else if (node.classList && node.classList.length) part += `.${node.classList[0]}`;
      parts.unshift(part);
      node = node.parentElement;
      depth++;
    }
    return parts.join(" > ");
  };

  // Outermost → element ancestor chain, labelled by testid where possible — the
  // quickest way to locate the node in the Vue component tree.
  const pathOf = (el: Element): string => {
    const chain: string[] = [];
    let node: Element | null = el;
    let depth = 0;
    while (node && depth < 7 && node.nodeType === 1 && node !== document.body) {
      const tid = node.getAttribute && node.getAttribute("data-testid");
      let label = node.tagName.toLowerCase();
      if (tid) label += `[${tid}]`;
      else if (node.id) label += `#${node.id}`;
      else if (node.classList && node.classList.length) label += `.${node.classList[0]}`;
      chain.unshift(label);
      node = node.parentElement;
      depth++;
    }
    return chain.join(" › ");
  };

  const htmlOf = (el: Element): string => {
    const h = (el as HTMLElement).outerHTML || "";
    const oneline = h.replace(/\s+/g, " ").trim();
    return oneline.length > 300 ? oneline.slice(0, 297) + "…" : oneline;
  };

  const textOf = (el: Element): string => {
    const t = (el.textContent || "").replace(/\s+/g, " ").trim();
    return t.length > 80 ? t.slice(0, 77) + "…" : t;
  };

  const rectOf = (el: Element) => {
    const r = el.getBoundingClientRect();
    return { x: Math.round(r.x), y: Math.round(r.y), w: Math.round(r.width), h: Math.round(r.height) };
  };

  // The computed styles that explain a layout defect — kept compact and uniform.
  const stylesOf = (el: Element): Record<string, string> => {
    const s = getComputedStyle(el);
    const pick: Record<string, string> = {
      fontSize: s.fontSize,
      fontWeight: s.fontWeight,
      color: s.color,
      backgroundColor: s.backgroundColor,
      overflowX: s.overflowX,
      overflowY: s.overflowY,
      whiteSpace: s.whiteSpace,
      textOverflow: s.textOverflow,
      position: s.position,
      display: s.display,
      zIndex: s.zIndex,
    };
    const he = el as HTMLElement;
    pick.scrollWidth = String(he.scrollWidth);
    pick.clientWidth = String(he.clientWidth);
    return pick;
  };

  const ctx = (el: Element): DomContext => ({
    selector: selectorOf(el),
    path: pathOf(el),
    html: htmlOf(el),
    styles: stylesOf(el),
    rect: rectOf(el),
  });

  const isVisible = (el: Element): boolean => {
    const r = el.getBoundingClientRect();
    if (r.width < 1 || r.height < 1) return false;
    const s = getComputedStyle(el);
    if (s.visibility === "hidden" || s.display === "none" || parseFloat(s.opacity) < 0.05) return false;
    return true;
  };

  const all = Array.from(document.body.querySelectorAll("*"));

  // ── 1. Page-level horizontal scroll — the canonical responsive break. ───────
  const docW = document.documentElement.scrollWidth;
  if (docW > vw + 2) {
    out.push({
      check: "page-horizontal-scroll",
      severity: "error",
      text: "",
      detail: `document scrollWidth ${docW}px exceeds viewport ${vw}px (horizontal scrollbar)`,
      ...ctx(document.documentElement),
    });
  }

  for (const el of all) {
    if (!isVisible(el)) continue;
    const r = el.getBoundingClientRect();
    const s = getComputedStyle(el);

    // ── 2. Element clipped off the right/left edge of the viewport. ───────────
    const offRight = r.right > vw + 2;
    const offLeft = r.left < -2;
    if ((offRight || offLeft) && r.width < vw * 1.5 && textOf(el).length > 0) {
      const pos = s.position;
      const parked = (s.transform && s.transform !== "none") || pos === "fixed";
      if (!parked) {
        out.push({
          check: "offscreen-clip",
          severity: "error",
          text: textOf(el),
          detail: offRight
            ? `right edge ${Math.round(r.right)}px past viewport ${vw}px`
            : `left edge ${Math.round(r.left)}px before 0`,
          ...ctx(el),
        });
      }
    }

    // ── 3. Content overflowing a CLIPPING box (overflow:hidden/clip). ─────────
    const ox = s.overflowX;
    const clips = ox === "hidden" || ox === "clip";
    if (clips && el.scrollWidth > el.clientWidth + 3 && el.clientWidth > 0) {
      const isEllipsis = s.textOverflow === "ellipsis" && s.whiteSpace === "nowrap";
      out.push({
        check: isEllipsis ? "text-truncated" : "content-clipped",
        severity: isEllipsis ? "warn" : "error",
        text: textOf(el),
        detail: `scrollWidth ${el.scrollWidth}px > clientWidth ${el.clientWidth}px${
          isEllipsis ? " (ellipsis truncation)" : " (content clipped)"
        }`,
        ...ctx(el),
      });
    }
  }

  // ── 4. Leftover template tokens / debug sentinels in visible text. ──────────
  const TOKEN = /(\{\{[^}]+\}\}|\$\{[^}]+\}|\[object Object\]|\bundefined\b|\bNaN\b|^null$)/;
  const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
  let tn: Node | null;
  const flaggedTokens = new Set<string>();
  while ((tn = walker.nextNode())) {
    const raw = (tn.nodeValue || "").trim();
    if (raw.length === 0) continue;
    const m = raw.match(TOKEN);
    if (!m) continue;
    const parent = tn.parentElement;
    if (!parent || !isVisible(parent)) continue;
    const key = selectorOf(parent) + "|" + m[0];
    if (flaggedTokens.has(key)) continue;
    flaggedTokens.add(key);
    out.push({
      check: "stray-template-token",
      severity: "error",
      text: textOf(parent),
      detail: `unrendered token "${m[0]}" in visible text`,
      ...ctx(parent),
    });
  }

  // ── 5. Tiny text — below a legible floor. ───────────────────────────────────
  for (const el of all) {
    if (!isVisible(el)) continue;
    const own = Array.from(el.childNodes).some(
      (n) => n.nodeType === 3 && (n.nodeValue || "").trim().length > 1,
    );
    if (!own) continue;
    const fs = parseFloat(getComputedStyle(el).fontSize);
    if (fs > 0 && fs < 11) {
      out.push({
        check: "tiny-text",
        severity: "info", // advisory + flood-prone in dense UI — never blocks
        text: textOf(el),
        detail: `computed font-size ${fs}px (< 11px legibility floor)`,
        ...ctx(el),
      });
    }
  }

  // ── 6. Tiny tap targets — interactive controls too small to hit. ────────────
  const interactive = Array.from(
    document.body.querySelectorAll(
      'button, a[href], input:not([type="hidden"]), select, textarea, [role="button"], [role="link"], [role="tab"]',
    ),
  );
  for (const el of interactive) {
    if (!isVisible(el)) continue;
    const r = el.getBoundingClientRect();
    if (r.bottom < 0 || r.top > vh || r.right < 0 || r.left > vw) continue; // off-screen
    if ((el as HTMLButtonElement).disabled) continue;
    const min = Math.min(r.width, r.height);
    if (min > 0 && min < 24) {
      out.push({
        check: "tiny-tap-target",
        severity: "info", // advisory; the touch-ergonomics heuristic makes the blocking call
        text: textOf(el),
        detail: `${Math.round(r.width)}×${Math.round(r.height)}px (< 24px min tap target)`,
        ...ctx(el),
      });
    }
  }

  return out;
}
