import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { installKitsokiVisualHelper } from "../../src/lib/visualHelper.js";
import { __resetSessionCapture, startSessionCapture, type RrwebEvent } from "../../src/data/session-capture.js";

function setRect(el: Element, r: { x: number; y: number; width: number; height: number }): void {
  (el as unknown as { getBoundingClientRect: () => DOMRect }).getBoundingClientRect = () =>
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

function visible(selector: string, rect: { x: number; y: number; width: number; height: number }): Element {
  const el = document.querySelector(selector);
  if (!el) throw new Error(`missing ${selector}`);
  setRect(el, rect);
  return el;
}

afterEach(() => {
  document.body.innerHTML = "";
  delete window.__kitsokiVisual;
  __resetSessionCapture();
});

beforeEach(() => {
  __resetSessionCapture();
});

describe("installKitsokiVisualHelper", () => {
  it("publishes stable action handles from data-testid, role, disabled state, label, and bbox", () => {
    document.body.innerHTML = `
      <section data-testid="chat-section">
        <button data-testid="intent-btn-go">Go west</button>
        <button data-testid="intent-btn-wait" disabled>Wait</button>
        <input data-testid="secret" type="password" value="nope" />
      </section>
    `;
    visible("[data-testid='chat-section']", { x: 0, y: 0, width: 400, height: 200 });
    visible("[data-testid='intent-btn-go']", { x: 10, y: 20, width: 90, height: 32 });
    visible("[data-testid='intent-btn-wait']", { x: 110, y: 20, width: 90, height: 32 });
    visible("[data-testid='secret']", { x: 10, y: 70, width: 160, height: 32 });

    const helper = installKitsokiVisualHelper();
    expect(window.__kitsokiVisual).toBe(helper);

    const got = helper!.observe();
    expect(got.ok).toBe(true);
    expect(got.regions.map((r) => r.id)).toContain("chat");
    expect(got.actions).toEqual([
      {
        handle: "testid:intent-btn-go",
        selector: '[data-testid="intent-btn-go"]',
        testid: "intent-btn-go",
        role: "button",
        label: "Go west",
        disabled: false,
        bbox: { x: 10, y: 20, width: 90, height: 32 },
      },
      {
        handle: "testid:intent-btn-wait",
        selector: '[data-testid="intent-btn-wait"]',
        testid: "intent-btn-wait",
        role: "button",
        label: "Wait",
        disabled: true,
        bbox: { x: 110, y: 20, width: 90, height: 32 },
      },
    ]);
    expect(helper!.resolve("testid:intent-btn-go")?.label).toBe("Go west");
  });

  it("tracks dirty regions from DOM mutations and can reset them", async () => {
    document.body.innerHTML = `
      <section data-testid="trace-timeline"><button data-testid="trace-action">Inspect</button></section>
    `;
    const trace = visible("[data-testid='trace-timeline']", { x: 5, y: 40, width: 500, height: 180 });
    visible("[data-testid='trace-action']", { x: 10, y: 50, width: 80, height: 28 });

    const helper = installKitsokiVisualHelper();
    expect(helper!.dirtyRegions()).toEqual(["full"]);
    helper!.resetDirty();
    expect(helper!.dirtyRegions()).toEqual([]);

    trace.setAttribute("data-state", "changed");
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(helper!.dirtyRegions()).toEqual(["full", "trace"]);
  });

  it("reports media, deck, and annotation regions for visual MCP deck QA", () => {
    document.body.innerHTML = `
      <section data-testid="chat-section">
        <div data-testid="media-element">
          <iframe data-testid="media-slideshow-frame"></iframe>
          <div data-testid="media-annotate-panel">
            <div data-testid="artifact-annotator"></div>
          </div>
        </div>
      </section>
    `;
    visible("[data-testid='chat-section']", { x: 0, y: 0, width: 900, height: 700 });
    visible("[data-testid='media-element']", { x: 20, y: 80, width: 800, height: 560 });
    visible("[data-testid='media-slideshow-frame']", { x: 30, y: 90, width: 780, height: 430 });
    visible("[data-testid='media-annotate-panel']", { x: 30, y: 530, width: 780, height: 120 });
    visible("[data-testid='artifact-annotator']", { x: 40, y: 540, width: 760, height: 90 });

    const got = installKitsokiVisualHelper()!.observe();
    expect(got.regions.map((r) => r.id)).toEqual(
      expect.arrayContaining(["chat", "media", "deck", "annotation"])
    );
    expect(got.regions.find((r) => r.id === "deck")?.bbox).toEqual({
      x: 30,
      y: 90,
      width: 780,
      height: 430,
    });
  });

  it("falls back to structural handles when no testid exists", () => {
    document.body.innerHTML = `<main><button aria-label="Refresh sessions"></button></main>`;
    visible("button", { x: 1, y: 2, width: 30, height: 24 });

    const got = installKitsokiVisualHelper()!.observe();
    expect(got.actions[0]).toMatchObject({
      handle: "selector:body > main > button",
      selector: "body > main > button",
      role: "button",
      label: "Refresh sessions",
    });
  });

  it("exposes Report bug through the visible Meta launcher even when the menu is closed", () => {
    document.body.innerHTML = `
      <div data-testid="meta-launcher">
        <button data-testid="meta-button" title="Meta mode">Meta</button>
      </div>
    `;
    visible("[data-testid='meta-launcher']", { x: 1200, y: 820, width: 180, height: 48 });
    visible("[data-testid='meta-button']", { x: 1210, y: 830, width: 110, height: 36 });

    const got = installKitsokiVisualHelper()!.observe();
    expect(got.actions).toEqual(
      expect.arrayContaining([
        {
          handle: "testid:meta-report-bug",
          selector: '[data-testid="meta-report-bug"]',
          testid: "meta-report-bug",
          role: "button",
          label: "Report bug",
          disabled: false,
          bbox: { x: 1210, y: 830, width: 110, height: 36 },
        },
      ])
    );
  });

  it("exposes a Slidey-compatible rrweb envelope from the rolling capture buffer", () => {
    let emit!: (event: RrwebEvent) => void;
    startSessionCapture((opts) => {
      emit = opts.emit;
      return undefined;
    });
    emit({ type: 4, timestamp: 10, data: { width: 800, height: 600 } });
    emit({ type: 2, timestamp: 20, data: "snapshot" });

    const helper = installKitsokiVisualHelper()!;
    const envelope = helper.recording();
    expect(envelope.schemaVersion).toBe(1);
    expect(envelope.source).toBe("kitsoki-visual-record");
    expect(envelope.events.map((event) => event.type)).toEqual([4, 2]);
    expect(envelope.viewport).toMatchObject({ deviceScaleFactor: window.devicePixelRatio || 1 });
  });
});
