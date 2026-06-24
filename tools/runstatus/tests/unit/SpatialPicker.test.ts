/**
 * Component tests for src/components/SpatialPicker.vue — the transparent overlay
 * that turns a click into a point and a drag into a box, mapping rendered
 * coordinates into FRAME pixels and resolving the element under the point
 * (docs/tui/spatial-capture.md).
 *
 * We pin the overlay's rendered rect (happy-dom has no layout) so the
 * rendered→frame map is deterministic: a 640×360 rendered overlay over a
 * 1280×720 frame is a clean 2× scale, so a click at client (160,90) lands at
 * frame (320,180). The resolver root is a SEPARATE document (not the overlay's
 * ownerDocument), so the picker resolves at frame pixels and the emitted bundle
 * carries the resolved element. No server, no LLM.
 */
import { describe, it, expect } from "vitest";
import { mount } from "@vue/test-utils";
import SpatialPicker from "../../src/components/SpatialPicker.vue";

const NAT = { width: 1280, height: 720 };
// Rendered overlay rect: 640×360 at origin (0,0) → exactly 2× the frame.
const RENDER = { left: 0, top: 0, width: 640, height: 360 };

/** Mount the picker with a stubbed overlay rect and an optional resolver root. */
function mountPicker(root?: Document) {
  const w = mount(SpatialPicker, {
    props: {
      naturalWidth: NAT.width,
      naturalHeight: NAT.height,
      ...(root ? { root } : {}),
    },
    attachTo: document.body,
  });
  const overlay = w.get("[data-testid='spatial-picker']").element as HTMLElement;
  overlay.getBoundingClientRect = () =>
    ({
      x: RENDER.left,
      y: RENDER.top,
      left: RENDER.left,
      top: RENDER.top,
      right: RENDER.left + RENDER.width,
      bottom: RENDER.top + RENDER.height,
      width: RENDER.width,
      height: RENDER.height,
      toJSON: () => ({}),
    }) as DOMRect;
  return { w, overlay };
}

/** A resolver root whose elementFromPoint always returns a fixed testid'd node. */
function resolverRoot(): Document {
  const doc = document.implementation.createHTMLDocument("root");
  doc.body.innerHTML = `<button data-testid="intent-btn-run">Run</button>`;
  const btn = doc.querySelector("[data-testid='intent-btn-run']")!;
  (btn as unknown as { getBoundingClientRect: () => DOMRect }).getBoundingClientRect =
    () =>
      ({ x: 100, y: 50, left: 100, top: 50, right: 180, bottom: 80, width: 80, height: 30, toJSON: () => ({}) }) as DOMRect;
  (doc as unknown as { elementFromPoint: () => Element | null }).elementFromPoint =
    () => btn;
  return doc;
}

describe("SpatialPicker", () => {
  it("emits a point in FRAME pixels for a click (no drag)", async () => {
    const { w, overlay } = mountPicker();
    // Click at rendered (160,90) → frame (320,180) at the 2× scale.
    overlay.dispatchEvent(
      new PointerEvent("pointerdown", { clientX: 160, clientY: 90, pointerId: 1, bubbles: true })
    );
    overlay.dispatchEvent(
      new PointerEvent("pointerup", { clientX: 160, clientY: 90, pointerId: 1, bubbles: true })
    );
    const ev = w.emitted("pick");
    expect(ev).toBeTruthy();
    const bundle = ev![0][0] as { point: { x: number; y: number }; box?: unknown };
    expect(bundle.point).toEqual({ x: 320, y: 180 });
    expect(bundle.box).toBeUndefined();
    w.unmount();
  });

  it("emits a box in FRAME pixels for a drag", async () => {
    const { w, overlay } = mountPicker();
    // Drag from rendered (10,10)→(110,60) → frame (20,20)→(220,120).
    overlay.dispatchEvent(
      new PointerEvent("pointerdown", { clientX: 10, clientY: 10, pointerId: 1, bubbles: true })
    );
    overlay.dispatchEvent(
      new PointerEvent("pointermove", { clientX: 110, clientY: 60, pointerId: 1, bubbles: true })
    );
    overlay.dispatchEvent(
      new PointerEvent("pointerup", { clientX: 110, clientY: 60, pointerId: 1, bubbles: true })
    );
    const bundle = w.emitted("pick")![0][0] as {
      point: { x: number; y: number };
      box?: { x: number; y: number; width: number; height: number };
    };
    expect(bundle.box).toEqual({ x: 20, y: 20, width: 200, height: 100 });
    // The point pins to the box's anchor corner.
    expect(bundle.point).toEqual({ x: 20, y: 20 });
    w.unmount();
  });

  it("carries the resolved element when a root is supplied", async () => {
    const { w, overlay } = mountPicker(resolverRoot());
    overlay.dispatchEvent(
      new PointerEvent("pointerdown", { clientX: 200, clientY: 100, pointerId: 1, bubbles: true })
    );
    overlay.dispatchEvent(
      new PointerEvent("pointerup", { clientX: 200, clientY: 100, pointerId: 1, bubbles: true })
    );
    const bundle = w.emitted("pick")![0][0] as {
      element?: { selector: string; role: string; text: string };
    };
    expect(bundle.element).toMatchObject({
      selector: '[data-testid="intent-btn-run"]',
      role: "button",
      text: "Run",
    });
    w.unmount();
  });

  it("omits the element when no root is supplied", async () => {
    const { w, overlay } = mountPicker();
    overlay.dispatchEvent(
      new PointerEvent("pointerdown", { clientX: 100, clientY: 100, pointerId: 1, bubbles: true })
    );
    overlay.dispatchEvent(
      new PointerEvent("pointerup", { clientX: 100, clientY: 100, pointerId: 1, bubbles: true })
    );
    const bundle = w.emitted("pick")![0][0] as { element?: unknown };
    expect(bundle.element).toBeUndefined();
    w.unmount();
  });
});
