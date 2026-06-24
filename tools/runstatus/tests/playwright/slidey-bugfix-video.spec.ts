/**
 * Slidey bug-fix feature-spotlight video demo (binary-rendered).
 *
 * The bugfix pipeline run against the real slidey repo (a Node/Vue narrated-deck
 * renderer), fixing slidey-128 (grid `cards` scenes with more than six items
 * desync their narration — the frame estimator over-counted each card beyond the
 * table by ten frames). The narrated walk is the SLIDEY_BUGFIX_TOUR_STEPS
 * manifest (generated from features/slidey-bugfix.yaml into
 * src/tour/generated/slidey-bugfix.ts), driving the deterministic no-LLM flow
 *   stories/slidey-bugfix/flows/tour.yaml (+ cassette cassettes/tour.cassette.yaml)
 * whose agent artifacts, fix diff, and 19/0 node --test log are replayed
 * verbatim from a real fix run (slidey main commit 19c8798).
 *
 * RECORD PATH: binary-native `kitsoki tour` (no Playwright, no Node):
 *   kitsoki tour --feature slidey-bugfix --out .artifacts/slidey-bugfix/
 *
 * This Playwright file exists only to (1) satisfy the feature schema's
 * "a tour needs a demo binding" rule, and (2) satisfy the features:check
 * spec<->feature bijection, which requires demo.spec to exist AND reference its
 * tour export symbol. It is skipped so CI never runs an empty recording.
 */
import { test } from "@playwright/test";
// Bijection anchor: the generated manifest for this feature.
import { SLIDEY_BUGFIX_TOUR_STEPS } from "../../src/tour/generated/slidey-bugfix.js";

test.describe("slidey bug-fix (real cards-timing-drift, end to end)", () => {
  test.skip(true, "binary-rendered via `kitsoki tour --feature slidey-bugfix`; Playwright body not used");

  test("records the slidey cards-timing-drift bug-fix walk", () => {
    // Intentionally empty — see file header. The render path is `kitsoki tour`.
    void SLIDEY_BUGFIX_TOUR_STEPS;
  });
});
