/**
 * Component tests for src/components/RegionDrawLayer.vue — the reusable <canvas>
 * region tool. We pin the canvas rendered rect (happy-dom has no layout) so the
 * rendered→natural map is deterministic: a 640×360 rendered canvas over a
 * 1280×720 still is a clean 2× scale. We stub a no-op 2d context (happy-dom does
 * not implement canvas drawing) so the draw calls are inert; the assertions are
 * purely on the emitted region geometry in NATURAL pixels. No server, no LLM.
 */
import { describe, it, expect, beforeEach } from "vitest";
import { mount } from "@vue/test-utils";
import RegionDrawLayer from "../../src/components/RegionDrawLayer.vue";
import type { RegionShape } from "../../src/lib/annotationAnchor.js";

const NAT = { width: 1280, height: 720 };
const RENDER = { left: 0, top: 0, width: 640, height: 360 }; // exactly 2×

/** A no-op CanvasRenderingContext2D — happy-dom ships none, so we install one. */
function stubCtx(): CanvasRenderingContext2D {
  const noop = () => undefined;
  return {
    clearRect: noop,
    strokeRect: noop,
    fillRect: noop,
    beginPath: noop,
    moveTo: noop,
    lineTo: noop,
    stroke: noop,
    lineWidth: 0,
    strokeStyle: "",
    fillStyle: "",
    lineJoin: "",
    lineCap: "",
  } as unknown as CanvasRenderingContext2D;
}

function mountLayer(shape: RegionShape) {
  const w = mount(RegionDrawLayer, {
    props: { naturalWidth: NAT.width, naturalHeight: NAT.height, shape },
    attachTo: document.body,
  });
  const canvas = w.get("[data-testid='region-draw-layer']")
    .element as HTMLCanvasElement;
  canvas.getBoundingClientRect = () =>
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
  canvas.getContext = (() => stubCtx()) as HTMLCanvasElement["getContext"];
  return { w, canvas };
}

function drag(
  el: HTMLElement,
  from: { x: number; y: number },
  via: { x: number; y: number }[],
  to: { x: number; y: number }
) {
  el.dispatchEvent(
    new PointerEvent("pointerdown", { clientX: from.x, clientY: from.y, pointerId: 1, bubbles: true })
  );
  for (const p of via) {
    el.dispatchEvent(
      new PointerEvent("pointermove", { clientX: p.x, clientY: p.y, pointerId: 1, bubbles: true })
    );
  }
  el.dispatchEvent(
    new PointerEvent("pointerup", { clientX: to.x, clientY: to.y, pointerId: 1, bubbles: true })
  );
}

beforeEach(() => {
  document.body.innerHTML = "";
});

describe("RegionDrawLayer", () => {
  it("emits a box region in NATURAL pixels for a drag", () => {
    const { w, canvas } = mountLayer("box");
    // Drag rendered (10,10)→(110,60) → natural (20,20)→(220,120) at 2×.
    drag(canvas, { x: 10, y: 10 }, [{ x: 60, y: 35 }], { x: 110, y: 60 });
    const region = w.emitted("region")![0][0] as {
      shape: string;
      bbox: { x: number; y: number; width: number; height: number };
      path?: unknown;
    };
    expect(region.shape).toBe("box");
    expect(region.bbox).toEqual({ x: 20, y: 20, width: 200, height: 100 });
    expect(region.path).toBeUndefined();
    w.unmount();
  });

  it("emits a highlight region (drag, no path)", () => {
    const { w, canvas } = mountLayer("highlight");
    drag(canvas, { x: 0, y: 0 }, [], { x: 50, y: 25 });
    const region = w.emitted("region")![0][0] as { shape: string; path?: unknown };
    expect(region.shape).toBe("highlight");
    expect(region.path).toBeUndefined();
    w.unmount();
  });

  it("emits a freeform region with its path + derived bbox", () => {
    const { w, canvas } = mountLayer("freeform");
    // Path rendered (5,5)→(15,20)→(10,30) → natural (10,10)→(30,40)→(20,60).
    drag(canvas, { x: 5, y: 5 }, [{ x: 15, y: 20 }], { x: 10, y: 30 });
    const region = w.emitted("region")![0][0] as {
      shape: string;
      bbox: { x: number; y: number; width: number; height: number };
      path: { x: number; y: number }[];
    };
    expect(region.shape).toBe("freeform");
    expect(region.path).toEqual([
      { x: 10, y: 10 },
      { x: 30, y: 40 },
      { x: 20, y: 60 },
    ]);
    // bbox is the path's extent.
    expect(region.bbox).toEqual({ x: 10, y: 10, width: 20, height: 50 });
    w.unmount();
  });

  it("collapses a zero-area box drag (a click) to a 1×1 region", () => {
    const { w, canvas } = mountLayer("box");
    drag(canvas, { x: 100, y: 100 }, [], { x: 100, y: 100 });
    const region = w.emitted("region")![0][0] as {
      bbox: { width: number; height: number };
    };
    expect(region.bbox.width).toBe(1);
    expect(region.bbox.height).toBe(1);
    w.unmount();
  });
});
