// jsdom unit tests for the built player bundle (dist/tour-player.esm.js —
// run `npm run build` first). Exercises anchor resolution + healing, the
// state machine, advanceOn gating, and act policy, without a real browser.
import { test } from "node:test";
import assert from "node:assert/strict";
import { JSDOM } from "jsdom";
import { fileURLToPath } from "node:url";
import path from "node:path";

const here = path.dirname(fileURLToPath(import.meta.url));
const bundlePath = path.join(here, "..", "dist", "tour-player.esm.js");

async function loadInJsdom(html) {
  const dom = new JSDOM(html, { url: "https://example.test/", pretendToBeVisual: true });
  const { window } = dom;
  const define = (name, value) => Object.defineProperty(globalThis, name, { value, configurable: true, writable: true });
  define("window", window);
  define("document", window.document);
  // Node 21+ defines a read-only `navigator` getter on globalThis; a plain
  // assignment throws, so override it with a configurable data property.
  define("navigator", window.navigator);
  define("CSS", window.CSS ?? { escape: (s) => s.replace(/["\\]/g, "\\$&") });
  define("MutationObserver", window.MutationObserver);
  define("HTMLInputElement", window.HTMLInputElement);
  define("HTMLTextAreaElement", window.HTMLTextAreaElement);
  define("KeyboardEvent", window.KeyboardEvent);
  define("Event", window.Event);
  define("MessageEvent", window.MessageEvent);
  // jsdom (even with pretendToBeVisual) has no requestAnimationFrame;
  // player.ts's scheduleRefresh() needs it to coalesce MutationObserver
  // callbacks. A setTimeout(0) shim is close enough for tests.
  define("requestAnimationFrame", window.requestAnimationFrame ?? ((cb) => setTimeout(() => cb(Date.now()), 0)));
  const mod = await import(`${bundlePath}?t=${Date.now()}`);
  return { dom, mod };
}

test("resolveAnchor: primary strategy resolves uniquely with no heal", async () => {
  const { mod } = await loadInJsdom(`<body><button data-testid="save-btn">Save</button></body>`);
  const { resolveAnchor } = mod;
  const { el, strategy, heal } = resolveAnchor({ testid: "save-btn" }, "s1");
  assert.equal(el.tagName, "BUTTON");
  assert.equal(strategy, "testid");
  assert.equal(heal, null);
});

test("resolveAnchor: heals from testid to text when the primary strategy misses", async () => {
  const { mod } = await loadInJsdom(`<body><button data-testid="save-button-v2">Save</button></body>`);
  const { resolveAnchor } = mod;
  const { strategy, heal } = resolveAnchor({ testid: "save-btn", text: "Save" }, "s1");
  assert.equal(strategy, "text");
  assert.ok(heal);
  assert.equal(heal.failedAnchor, "testid");
  assert.equal(heal.matchedAnchor, "text");
  assert.equal(heal.stepId, "s1");
});

test("resolveAnchor: throws AnchorResolutionError with every attempt when nothing resolves uniquely", async () => {
  const { mod } = await loadInJsdom(`<body></body>`);
  const { resolveAnchor, AnchorResolutionError } = mod;
  assert.throws(
    () => resolveAnchor({ testid: "missing" }, "s1"),
    (err) => err instanceof AnchorResolutionError && err.attempts.length === 1 && err.attempts[0].count === 0
  );
});

test("resolveAnchor: <head><title> text never false-matches a body text anchor", async () => {
  const { mod } = await loadInJsdom(`
    <head><title>fixture (mutated: save-btn renamed)</title></head>
    <body><button data-testid="save-button-v2">Save</button></body>
  `);
  const { resolveAnchor } = mod;
  const { strategy } = resolveAnchor({ testid: "save-btn", text: "Save" }, "s1");
  assert.equal(strategy, "text");
});

test("TourPlayer: highlight step shows a popover and Next advances to finish", async () => {
  const { mod } = await loadInJsdom(`<body></body>`);
  const { TourPlayer } = mod;
  const events = [];
  const tour = {
    version: 2,
    id: "t1",
    steps: [
      { id: "s1", kind: "highlight", popover: { title: "Hi", body: "Welcome" } },
      { id: "s2", kind: "highlight", popover: { title: "Bye", body: "Done" } }
    ]
  };
  const player = new TourPlayer(tour, { onEvent: (e) => events.push(e) });
  player.start();
  assert.ok(document.getElementById("kitsoki-tour-popover"), "popover mounted");
  assert.ok(document.querySelector('[data-kt-action="next"]'), "Next button present for a highlight step");

  document.querySelector('[data-kt-action="next"]').click();
  assert.ok(events.some((e) => e.type === "advanced" && e.toStepId === "s2"));

  document.querySelector('[data-kt-action="next"]').click();
  assert.ok(events.some((e) => e.type === "finished"));
  assert.equal(document.getElementById("kitsoki-tour-popover"), null, "popover torn down on finish");
});

test("TourPlayer: gate step advances on the REAL click, not a Next button", async () => {
  const { mod } = await loadInJsdom(`<body><button data-testid="save-btn">Save</button></body>`);
  const { TourPlayer } = mod;
  const events = [];
  const tour = {
    version: 2,
    id: "t1",
    steps: [{ id: "s1", kind: "gate", target: { testid: "save-btn" }, advanceOn: { event: "click" }, popover: { title: "Click Save" } }]
  };
  const player = new TourPlayer(tour, { onEvent: (e) => events.push(e) });
  player.start();
  assert.equal(document.querySelector('[data-kt-action="next"]'), null, "gate steps have no Next button");
  document.querySelector('[data-testid="save-btn"]').click();
  assert.ok(events.some((e) => e.type === "finished"), "the real click on the target advanced the (single-step) tour to finish");
});

test("TourPlayer: act step with policy=confirm waits for the Confirm button before acting", async () => {
  const { mod } = await loadInJsdom(`<body><button data-testid="save-btn">Save</button></body>`);
  const { TourPlayer } = mod;
  let clicked = false;
  document.querySelector('[data-testid="save-btn"]').addEventListener("click", () => (clicked = true));
  const tour = {
    version: 2,
    id: "t1",
    steps: [{ id: "s1", kind: "act", target: { testid: "save-btn" }, act: { kind: "click" }, popover: { title: "Save?" } }]
  };
  const player = new TourPlayer(tour);
  player.start();
  assert.equal(clicked, false, "confirm policy must not act before the user confirms");
  const confirmBtn = document.querySelector('[data-kt-action="confirm-act"]');
  assert.ok(confirmBtn, "confirm button present for a confirm-policy act step");
  confirmBtn.click();
  assert.equal(clicked, true, "act performs after confirmation");
});

test("TourPlayer: act step with policy=auto performs immediately", async () => {
  const { mod } = await loadInJsdom(`<body><button data-testid="save-btn">Save</button></body>`);
  const { TourPlayer } = mod;
  let clicked = false;
  document.querySelector('[data-testid="save-btn"]').addEventListener("click", () => (clicked = true));
  const tour = {
    version: 2,
    id: "t1",
    steps: [{ id: "s1", kind: "act", target: { testid: "save-btn" }, act: { kind: "click" }, policy: "auto", popover: { title: "Saving" } }]
  };
  const player = new TourPlayer(tour);
  player.start();
  assert.equal(clicked, true);
  // A single-step act tour never reaches finish() on its own (there's no
  // Next/gate click to advance it) — abort() to release its setInterval
  // poll + MutationObserver so the test process can exit.
  player.abort();
});

test("TourRegistry: postMessage fallback answers start_guided_tour/list_tour_anchors", async () => {
  const { mod } = await loadInJsdom(`<body></body>`);
  const { TourRegistry } = mod;
  const registry = new TourRegistry();
  registry.registerAnchor("save", { testid: "save-btn" }, "the save button");
  registry.registerModelContext();

  const anchors = await new Promise((resolve) => {
    window.addEventListener("message", function handler(e) {
      if (e.data?.type === "webmcp:result" && e.data.requestId === "req-1") {
        window.removeEventListener("message", handler);
        resolve(e.data.result);
      }
    });
    window.postMessage({ type: "webmcp:invoke", tool: "list_tour_anchors", requestId: "req-1" }, "*");
  });
  assert.deepEqual(anchors, [{ name: "save", bundle: { testid: "save-btn" }, description: "the save button" }]);
});
