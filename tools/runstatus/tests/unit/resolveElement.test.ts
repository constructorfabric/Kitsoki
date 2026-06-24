/**
 * Unit tests for src/lib/resolveElement.ts — the spatial picker's pure
 * "what is at this pixel?" resolver (docs/tui/spatial-capture.md).
 *
 * elementFromPoint(root, x, y) is pure over a DOM root, so we drive it against a
 * fixture document whose `elementFromPoint` we stub to return a chosen node (the
 * happy-dom environment does not implement real hit-testing / layout). We also
 * stub each node's getBoundingClientRect so the recorded bbox is deterministic.
 *
 * Pins: a point over a data-testid node resolves to that selector + role + text
 * + bbox; a point over a NESTED node prefers the nearest data-testid ANCESTOR; a
 * point over a bare node falls back to a structural :nth-of-type path. No server,
 * no LLM.
 */
import { describe, it, expect } from "vitest";
import { elementFromPoint } from "../../src/lib/resolveElement.js";

/** Build a fixture document and return it + a setter that pins which element
 *  `elementFromPoint` returns for the next call. */
function fixture(html: string): {
  root: Document;
  hit: (el: Element | null) => void;
} {
  const doc = document.implementation.createHTMLDocument("fx");
  doc.body.innerHTML = html;
  let hitEl: Element | null = null;
  // Stub hit-testing: the resolver only asks the root for the topmost node.
  (doc as unknown as { elementFromPoint: () => Element | null }).elementFromPoint =
    () => hitEl;
  return { root: doc, hit: (el) => (hitEl = el) };
}

/** Give an element a fixed bounding rect so bboxOf is deterministic. */
function setRect(
  el: Element,
  r: { x: number; y: number; width: number; height: number }
): void {
  (el as unknown as { getBoundingClientRect: () => DOMRect }).getBoundingClientRect =
    () =>
      ({
        x: r.x,
        y: r.y,
        left: r.x,
        top: r.y,
        right: r.x + r.width,
        bottom: r.y + r.height,
        width: r.width,
        height: r.height,
        toJSON: () => ({}),
      }) as DOMRect;
}

describe("elementFromPoint", () => {
  it("resolves a data-testid node to its selector + role + text + bbox", () => {
    const { root, hit } = fixture(
      `<button data-testid="intent-btn-run">Run the story</button>`
    );
    const btn = root.querySelector("[data-testid='intent-btn-run']")!;
    setRect(btn, { x: 10, y: 20, width: 80, height: 30 });
    hit(btn);

    const res = elementFromPoint(root, 50, 35);
    expect(res).toEqual({
      selector: '[data-testid="intent-btn-run"]',
      role: "button",
      text: "Run the story",
      bbox: { x: 10, y: 20, width: 80, height: 30 },
    });
  });

  it("prefers the nearest data-testid ANCESTOR for a nested hit", () => {
    const { root, hit } = fixture(
      `<div data-testid="chat-row-3"><span class="label">why <em>disabled</em>?</span></div>`
    );
    const row = root.querySelector("[data-testid='chat-row-3']")!;
    const leaf = root.querySelector("em")!;
    // The bbox/text/role describe the testid'd ancestor (the thing meant), not
    // the bare leaf — so set the ancestor's rect.
    setRect(row, { x: 0, y: 100, width: 200, height: 24 });
    hit(leaf); // pointer landed on the inner <em>

    const res = elementFromPoint(root, 5, 110)!;
    expect(res.selector).toBe('[data-testid="chat-row-3"]');
    expect(res.role).toBe("div"); // a bare <div> falls back to its tag name
    expect(res.text).toBe("why disabled?");
    expect(res.bbox).toEqual({ x: 0, y: 100, width: 200, height: 24 });
  });

  it("falls back to a structural :nth-of-type path for a bare node", () => {
    const { root, hit } = fixture(
      `<section><p>first</p><p>second</p><p>third</p></section>`
    );
    const ps = root.querySelectorAll("p");
    const second = ps[1];
    setRect(second, { x: 4, y: 4, width: 100, height: 18 });
    hit(second);

    const res = elementFromPoint(root, 10, 10)!;
    // No data-testid anywhere → structural path from body, with :nth-of-type
    // only where a tag has same-tag siblings.
    expect(res.selector).toBe("body > section > p:nth-of-type(2)");
    expect(res.role).toBe("p");
    expect(res.text).toBe("second");
    expect(res.bbox).toEqual({ x: 4, y: 4, width: 100, height: 18 });
  });

  it("returns null when nothing is under the point", () => {
    const { root } = fixture(`<div>x</div>`);
    // hit() left at null → elementFromPoint stub returns null.
    expect(elementFromPoint(root, 0, 0)).toBeNull();
  });

  it("reports an explicit role attribute over the implicit tag role", () => {
    const { root, hit } = fixture(
      `<div data-testid="composer-send" role="button">Send</div>`
    );
    const el = root.querySelector("[data-testid='composer-send']")!;
    setRect(el, { x: 0, y: 0, width: 40, height: 20 });
    hit(el);
    expect(elementFromPoint(root, 1, 1)!.role).toBe("button");
  });
});
