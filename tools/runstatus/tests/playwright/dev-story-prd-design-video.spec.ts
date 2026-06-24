/**
 * Dev-story PRD → Design feature-spotlight video demo (STUB — slice 2 prerequisite).
 *
 * This is the GOLDEN example for conversation-driven development (CDD): the
 * dev-story hub authors kitsoki's OWN PRD and design in a single self-targeting
 * conversation — multi-round clarification, brief refinement, PRD published to
 * docs/prd/, design published to docs/proposals/, and an auto-minted feature
 * ticket at issues/features/. "kitsoki on kitsoki."
 *
 * The no-LLM flow that this demo drives the UI against is
 *   stories/dev-story/flows/prd_to_design_full.yaml
 * and the narrated walk is the DEV_STORY_PRD_DESIGN_TOUR_STEPS manifest
 * (generated from features/dev-story-prd-design.yaml into
 * src/tour/generated/dev-story-prd-design.ts).
 *
 * STATUS: stub. The intended record path is the binary-native `kitsoki tour`
 * subcommand (slice 2 of the kitsoki-as-dependency epic): once it ships, this
 * demo renders with
 *   kitsoki tour --feature dev-story-prd-design --out .artifacts/dev-story-prd-design/
 * — no Playwright, no Node, headless Chrome + ffmpeg from the binary alone.
 *
 * Until slice 2 lands, the full Playwright body is intentionally deferred (the
 * agent-actions-video.spec.ts is the template to copy when authoring it). This
 * file exists today only to:
 *   (1) satisfy the feature schema's "a tour needs a demo binding" rule, and
 *   (2) satisfy the features:check spec<->feature bijection, which requires the
 *       demo.spec to exist AND reference its tour export symbol.
 * It is skipped so CI never runs an empty recording.
 */
import { test } from "@playwright/test";
// Bijection anchor: the generated manifest for this feature. The features:check
// reverse map walks every spec's `src/tour/generated/<id>.js` import back to a
// feature, and the forward map asserts this spec references the export symbol.
import { DEV_STORY_PRD_DESIGN_TOUR_STEPS } from "../../src/tour/generated/dev-story-prd-design.js";

test.describe("dev-story PRD → Design (CDD golden example)", () => {
  test.skip(true, "slice 2 prerequisite — renders via `kitsoki tour`; Playwright body TBD");

  test("records the PRD → Design walk", () => {
    // TBD (slice 2): copy agent-actions-video.spec.ts and drive the
    // DEV_STORY_PRD_DESIGN_TOUR_STEPS tour against a `kitsoki web` server seeded
    // with stories/dev-story/flows/prd_to_design_full.yaml. Reference the import
    // so the bijection's export check resolves and lint stays clean.
    void DEV_STORY_PRD_DESIGN_TOUR_STEPS;
  });
});
