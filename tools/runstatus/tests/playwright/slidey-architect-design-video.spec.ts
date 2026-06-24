/**
 * slidey architect → design feature-spotlight video demo (STUB — rrweb-native).
 *
 * Phase 2 of the slidey dev-story hybrid: an architect picks up the published
 * slidey speaker-notes PRD and authors the design (epic, slices, ADRs, a mermaid
 * data-flow), on the slidey-dev instance. The no-LLM flow this demo drives the
 * UI against is stories/slidey-dev/flows/architect_design.yaml; the narrated
 * walk is SLIDEY_ARCHITECT_DESIGN_TOUR_STEPS (generated from
 * features/slidey-architect-design.yaml).
 *
 * STATUS: stub. The deck consumes the NATIVE rrweb clip captured by
 *   slidey-architect-design-rrweb-capture.spec.ts
 * (→ .artifacts/rrweb-eval/slidey-architect-design/slidey-architect-design.rrweb.json),
 * inlined into a slidey hybrid deck as a data-URI rrweb scene — no MP4 render
 * here. This file exists only to satisfy the feature schema's tour⇒demo rule
 * and the features:check spec↔feature bijection. It is skipped so CI never runs
 * an empty recording.
 */
import { test } from "@playwright/test";
import { SLIDEY_ARCHITECT_DESIGN_TOUR_STEPS } from "../../src/tour/generated/slidey-architect-design.js";

test.describe("slidey architect → design (rrweb-native)", () => {
  test.skip(true, "rrweb-native — captured by slidey-architect-design-rrweb-capture.spec.ts; MP4 body N/A");

  test("records the architect → design walk", () => {
    void SLIDEY_ARCHITECT_DESIGN_TOUR_STEPS;
  });
});
