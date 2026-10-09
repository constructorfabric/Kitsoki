/**
 * Dev-story PRD → Design feature-spotlight video demo (STUB — DE-LISTED).
 *
 * This was the GOLDEN example for conversation-driven development (CDD): the
 * dev-story hub authoring kitsoki's OWN PRD and design in a single
 * self-targeting conversation — multi-round clarification, brief refinement,
 * PRD published to docs/prd/, design published to docs/proposals/, and an
 * auto-minted feature ticket at issues/features/. "kitsoki on kitsoki."
 *
 * STATUS: de-listed. features/dev-story-prd-design.yaml was removed from the
 * catalog (permanently unrecordable in this repo — the binary `kitsoki tour`
 * renderer this demo needed can advance the imported PRD state, but the chat
 * surface doesn't remount into the imported PRD room reliably enough for Pages
 * CI; see docs/proposals/kitsoki-as-dependency.md for that renderer gap). This
 * file is kept as a placeholder/reference for reviving the demo once that gap
 * closes — it no longer imports a generated tour manifest (there is no catalog
 * entry to generate one from), so it's exempt from the features:check
 * spec<->feature bijection. It is skipped so CI never runs an empty recording.
 */
import { test } from "@playwright/test";

test.describe("dev-story PRD → Design (CDD golden example, de-listed)", () => {
  test.skip(true, "de-listed from features/*.yaml — renders via `kitsoki tour`; Playwright body TBD");

  test("records the PRD → Design walk", () => {
    // TBD: once the binary tour renderer's chat-remount gap closes, re-catalog
    // this feature (features/dev-story-prd-design.yaml, `make features`), copy
    // agent-actions-video.spec.ts, and drive its generated tour manifest against
    // a `kitsoki web` server seeded with stories/dev-story/flows/prd_to_design_full.yaml.
  });
});
