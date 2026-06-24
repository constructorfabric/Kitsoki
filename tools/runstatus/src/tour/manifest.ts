/**
 * Compatibility shim for the onboarding tour.
 *
 * The tour content lives in the feature catalog (features/onboarding-tour.yaml
 * at the repo root — the single source of truth for every feature's tour steps,
 * demo binding, and site/QA metadata); `make features` code-generates
 * ./generated/onboarding-tour.ts from it. This module preserves the historical
 * import surface (`src/tour/manifest.js`) for the tour store, the overlay, the
 * unit tests, and the tour-video / tour-review specs.
 *
 * Step types live in ./types.ts (hand-written, Vue/DOM-free).
 */
export type { TourStep, TourRoute, AdvanceTrigger, Placement } from "./types.js";
export { TOUR_STEPS } from "./generated/onboarding-tour.js";
