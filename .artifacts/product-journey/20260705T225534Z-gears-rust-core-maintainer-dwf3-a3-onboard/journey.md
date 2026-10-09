# Product journey dry run

- Run: `20260705T225534Z-gears-rust-core-maintainer-dwf3-a3-onboard`
- Mode: `dry-run`
- Project: `constructorfabric/gears-rust`
- Persona: `Core maintainer`

## Stage Plan

- `discover_product`: planned
  - evidence: requires visual MCP/browser evidence in live or cassette run
- `follow_tutorial`: planned
  - evidence: requires visual MCP/browser evidence in live or cassette run
- `onboard_project`: captured
  - scenarios: project-onboarding
  - evidence: tools/bugfix-bakeoff/external/projects/gears-rust/manifest.yaml
- `plan_project_work`: cached_validated
  - evidence: tools/bugfix-bakeoff/external/projects/gears-rust/manifest.yaml
- `fix_bug`: cached_validated
  - evidence: tools/bugfix-bakeoff/external/projects/gears-rust/manifest.yaml
- `file_product_issue`: planned
  - evidence: requires visual MCP/browser evidence in live or cassette run
- `score_and_report`: cached_validated
  - evidence: tools/bugfix-bakeoff/external/projects/gears-rust/manifest.yaml

## Scenarios

### Project onboarding

- Stage: `onboard_project`
- Story: `stories/dev-story/app.yaml`
- MCP: session.open, session.inspect, render.tui, visual.observe
- Evidence: session_trace, rendered_tui_frame, generated_config_diff, onboarding_smoke_result, key_interaction_video, flow-fixture

Use the documented Kitsoki onboarding path to configure the assigned repository and explain how bugfix/design work should be launched.


## Next Evidence Needed

- Visual MCP frames or browser screenshots for product discovery and docs/tutorial stages.
- Kitsoki session traces for onboarding, PRD/design, feature implementation, and bugfix paths.
- Oracle result JSON for every attempted project bug.
- Video clips or retained screenshot IDs for Slidey playback scenes.
