/**
 * index.ts — barrel export for tools/swarm/capture.
 *
 * See recordFinding.ts's doc comment for the design (reuses the existing
 * bug-report evidence machinery: window.__kitsokiVisual.recording() for
 * rrweb, the caller's own console collection, runstatus.bug.preview for a
 * scrubbed HAR) and tools/swarm/README.md for why tools/swarm/** has no npm
 * dependencies of its own.
 */
export * from "./types.js";
export * from "./serverSha.js";
export * from "./recordFinding.js";
