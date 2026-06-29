# Landing Proposal Routing Tuning Report

Fixture: `stories/dev-story/intents/landing_proposal_routing.yaml`

Harness: `claude`, profile `codex-native`, model `gpt-5.5`

## Result

Final pass: 15/15 fixtures.

The first three-run pass caught five misses:

- proposal cleanup phrases like "resolve the open questions" stayed in broad work;
- narration-quality phrases stayed in broad work;
- proposal audit phrasing stayed in broad work;
- one generic proposal phrase was unstable;
- the docs-after-ship negative case correctly routed to `go_docs`, so the expectation was wrong.

After tuning `go_idea` descriptions/examples and sharpening the `work` boundary,
all proposal-positive cases routed to `go_idea`. The remaining mismatch was an
expectation error: "implement the dynamic workflow service from the proposal"
correctly routes to `go_implementation`, not broad `work`.

## Changes

- Added 15 route fixtures covering proposal/design positives and near-miss work,
  docs, implementation, investigation, and test requests.
- Tuned `go_idea` to own proposal authoring, review, clarification, decisions,
  and human-review readiness.
- Clarified that broad `work` should not capture proposal authoring, proposal
  review, design workflow, or proposal-decision turns.
- Emitted a replay recording so future checks can run without a live model.

## Session Mining Follow-Up

Mine future cases from real sessions where users correct a landing route, ask for
proposal cleanup from a broad work room, or enter a specialized room by accident.
Promote those phrases into intent fixtures before retuning.
