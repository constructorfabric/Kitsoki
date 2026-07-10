// Hermetic stand-in for slidey's web/graph-projection/renderer.js, used via
// create-mockup.mjs's --renderer flag so tests never depend on a real
// slidey checkout. Deliberately a no-op: it never touches `document`, so it
// stays safe to invoke from the DOM-lite harness in create-mockup.test.js.
(function (global) {
  "use strict";
  function renderGraphProjection(canvas, projection, stateId) {
    return { graphId: stateId, nodeCount: 0, edgeCount: 0, stub: true };
  }
  var api = { renderGraphProjection: renderGraphProjection };
  if (typeof module !== "undefined" && module.exports) module.exports = api;
  if (global) {
    global.SlideyGraphProjection = api;
    global.renderGraphProjection = renderGraphProjection;
  }
})(typeof window !== "undefined" ? window : typeof globalThis !== "undefined" ? globalThis : this);
