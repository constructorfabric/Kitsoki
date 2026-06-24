/**
 * Gears bug-fix feature-spotlight video demo (binary-rendered).
 *
 * The bugfix pipeline run against the real upstream constructorfabric/gears-rust
 * repo, fixing issue #4115 (gear config can't be overridden via env vars in
 * Kubernetes — dashes in gear names vs the C_IDENTIFIER rule). The narrated walk
 * is the GEARS_BUGFIX_TOUR_STEPS manifest (generated from features/gears-bugfix.yaml
 * into src/tour/generated/gears-bugfix.ts), driving the deterministic no-LLM flow
 *   stories/bugfix/flows/tour_gears_gh4115.yaml (+ cassette tour_gears_gh4115.cassette.yaml)
 * whose agent artifacts, fix diff, and 316/0 cargo log are replayed verbatim
 * from a real LLM run.
 *
 * RECORD PATH: binary-native `kitsoki tour` (no Playwright, no Node):
 *   kitsoki tour --feature gears-bugfix --out .artifacts/gears-bugfix/
 *
 * This Playwright file exists only to (1) satisfy the feature schema's
 * "a tour needs a demo binding" rule, and (2) satisfy the features:check
 * spec<->feature bijection, which requires demo.spec to exist AND reference its
 * tour export symbol. It is skipped so CI never runs an empty recording.
 */
import { test } from "@playwright/test";
// Bijection anchor: the generated manifest for this feature.
import { GEARS_BUGFIX_TOUR_STEPS } from "../../src/tour/generated/gears-bugfix.js";

test.describe("gears bug-fix (real #4115, end to end)", () => {
  test.skip(true, "binary-rendered via `kitsoki tour --feature gears-bugfix`; Playwright body not used");

  test("records the gears #4115 bug-fix walk", () => {
    // Intentionally empty — see file header. The render path is `kitsoki tour`.
    void GEARS_BUGFIX_TOUR_STEPS;
  });
});
