/**
 * slidey PM-idea → PRD feature-spotlight video demo (STUB — rrweb-native).
 *
 * Phase 1 of the slidey dev-story hybrid: a product manager talks a real slidey
 * feature (a `slidey --notes` speaker-notes export) from a one-line idea into a
 * published PRD, on the slidey-dev instance. The no-LLM flow this demo drives
 * the UI against is stories/slidey-dev/flows/pm_idea.yaml; the narrated walk is
 * SLIDEY_PM_IDEA_TOUR_STEPS (generated from features/slidey-dev-prd-design.yaml).
 *
 * STATUS: stub. The deck consumes the NATIVE rrweb clip captured by
 *   slidey-pm-idea-rrweb-capture.spec.ts
 * (→ .artifacts/rrweb-eval/slidey-pm-idea/slidey-pm-idea.rrweb.json), inlined
 * into a slidey hybrid deck as a data-URI rrweb scene — no MP4 render here.
 * This file exists only to satisfy the feature schema's tour⇒demo rule and the
 * features:check spec↔feature bijection. It is skipped so CI never runs an
 * empty recording.
 */
import { test } from "@playwright/test";
import { SLIDEY_PM_IDEA_TOUR_STEPS } from "../../src/tour/generated/slidey-dev-prd-design.js";

test.describe("slidey PM-idea → PRD (rrweb-native)", () => {
  test.skip(true, "rrweb-native — captured by slidey-pm-idea-rrweb-capture.spec.ts; MP4 body N/A");

  test("records the PM idea → PRD walk", () => {
    void SLIDEY_PM_IDEA_TOUR_STEPS;
  });
});
