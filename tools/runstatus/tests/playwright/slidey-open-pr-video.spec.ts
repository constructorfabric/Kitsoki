/**
 * Slidey open-PR feature-spotlight video demo (binary-rendered).
 *
 * Phase 6 of the slidey hybrid deck: the PR-refinement pipeline
 * (stories/pr-refinement) opening, monitoring and merging a pull request for
 * the real slidey grid-cards narration-drift fix (slidey-128). The narrated
 * walk is the SLIDEY_OPEN_PR_TOUR_STEPS manifest (generated from
 * features/slidey-open-pr.yaml into src/tour/generated/slidey-open-pr.ts),
 * driving the deterministic no-LLM flow
 *   stories/pr-refinement/flows/slidey_open_pr.yaml
 * (judge_mode=human, CI success, no review comments).
 *
 * RECORD PATH: the native rrweb capture spec
 *   tests/playwright/slidey-open-pr-rrweb-capture.spec.ts
 * produces the embeddable rrweb clip for the slidey deck.
 *
 * This Playwright file exists only to (1) satisfy the feature schema's
 * "a tour needs a demo binding" rule, and (2) satisfy the features:check
 * spec<->feature bijection, which requires demo.spec to exist AND reference its
 * tour export symbol. It is skipped so CI never runs an empty recording.
 */
import { test } from "@playwright/test";
// Bijection anchor: the generated manifest for this feature.
import { SLIDEY_OPEN_PR_TOUR_STEPS } from "../../src/tour/generated/slidey-open-pr.js";

test.describe("slidey open-PR (PR-refinement tail for the slidey fix)", () => {
  test.skip(true, "rrweb-captured via slidey-open-pr-rrweb-capture.spec.ts; Playwright body not used");

  test("records the slidey open-PR walk", () => {
    // Intentionally empty — see file header.
    void SLIDEY_OPEN_PR_TOUR_STEPS;
  });
});
