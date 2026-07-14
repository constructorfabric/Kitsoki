# Product journey execution plan

- Run: `20260705T225534Z-gears-rust-core-maintainer-dwf3-a3-onboard`
- Project: `constructorfabric/gears-rust`
- Persona: `Core maintainer`
- Scenarios: 1
- Evidence slots: 6

## 1. Project onboarding

- Scenario: `project-onboarding`
- Story: `stories/dev-story/app.yaml`
- Stage: `onboard_project`

Use the documented Kitsoki onboarding path to configure the assigned repository and explain how bugfix/design work should be launched.

Driver prompt:

Onboard constructorfabric/gears-rust using Kitsoki's documented project setup path. Confirm the generated project profile names plausible rust commands, repo files, and the next story to launch.

### MCP Steps

- `session.open`: Open or resume the Kitsoki story session for this scenario.
- `session.inspect`: Inspect the current Kitsoki session state and trace context.
- `render.tui`: Capture the rendered TUI or web frame for the current room.
- `visual.observe`: Capture the current browser frame or retained screenshot reference.

### Evidence

- `session_trace` (captured): .context/dwf3-a3-recording-notes.md - Save the Kitsoki session trace or trace id.
- `rendered_tui_frame` (missing): <path-or-retained-id> - Save the rendered TUI/web frame for the room under review.
- `generated_config_diff` (missing): <path-or-retained-id> - Save the generated config diff or a no-change note.
- `onboarding_smoke_result` (missing): <path-or-retained-id> - Save the deterministic onboarding smoke result.
- `key_interaction_video` (missing): <path-or-retained-id> - Save an MP4/GIF clip or retained video reference for Slidey playback.
- `flow-fixture` (captured): stories/dev-story/flows/onboard_gears_rust.yaml - Save a local flow fixture (`kitsoki test flows <app.yaml> --flows ...`) that replays this scenario no-LLM.

### Attach Commands

```sh
python3 tools/product-journey/run.py --attach-evidence --run-dir .artifacts/product-journey/20260705T225534Z-gears-rust-core-maintainer-dwf3-a3-onboard --scenario project-onboarding --evidence-kind session_trace --evidence-path <path-or-retained-id> --evidence-source <retained|external|local|cassette> --notes "Save the Kitsoki session trace or trace id."
```
```sh
python3 tools/product-journey/run.py --attach-evidence --run-dir .artifacts/product-journey/20260705T225534Z-gears-rust-core-maintainer-dwf3-a3-onboard --scenario project-onboarding --evidence-kind rendered_tui_frame --evidence-path <path-or-retained-id> --evidence-source <retained|external|local|cassette> --notes "Save the rendered TUI/web frame for the room under review."
```
```sh
python3 tools/product-journey/run.py --attach-evidence --run-dir .artifacts/product-journey/20260705T225534Z-gears-rust-core-maintainer-dwf3-a3-onboard --scenario project-onboarding --evidence-kind generated_config_diff --evidence-path <path-or-retained-id> --evidence-source <retained|external|local|cassette> --notes "Save the generated config diff or a no-change note."
```
```sh
python3 tools/product-journey/run.py --attach-evidence --run-dir .artifacts/product-journey/20260705T225534Z-gears-rust-core-maintainer-dwf3-a3-onboard --scenario project-onboarding --evidence-kind onboarding_smoke_result --evidence-path <path-or-retained-id> --evidence-source <retained|external|local|cassette> --notes "Save the deterministic onboarding smoke result."
```
```sh
python3 tools/product-journey/run.py --attach-evidence --run-dir .artifacts/product-journey/20260705T225534Z-gears-rust-core-maintainer-dwf3-a3-onboard --scenario project-onboarding --evidence-kind key_interaction_video --evidence-path <path-or-retained-id> --evidence-source <retained|external|local|cassette> --notes "Save an MP4/GIF clip or retained video reference for Slidey playback."
```
```sh
python3 tools/product-journey/run.py --attach-evidence --run-dir .artifacts/product-journey/20260705T225534Z-gears-rust-core-maintainer-dwf3-a3-onboard --scenario project-onboarding --evidence-kind flow-fixture --evidence-path <path-or-retained-id> --evidence-source <retained|external|local|cassette> --notes "Save a local flow fixture (`kitsoki test flows <app.yaml> --flows ...`) that replays this scenario no-LLM."
```

### Blocker Command

```sh
python3 tools/product-journey/run.py --record-blocker --run-dir .artifacts/product-journey/20260705T225534Z-gears-rust-core-maintainer-dwf3-a3-onboard --scenario project-onboarding --title <blocker-title> --summary <why-this-scenario-could-not-be-captured> --evidence-path <trace-or-frame-path>
```

### Success Criteria

- Generated project config names real commands and relevant project files.
- Persona can state the next Kitsoki command or room to use.
- No silent operator-question defaulting occurs.

## Finalize

```sh
python3 tools/product-journey/run.py --record-finding --run-dir .artifacts/product-journey/20260705T225534Z-gears-rust-core-maintainer-dwf3-a3-onboard --finding-kind <strength|weakness|issue|fix> --title <title> --summary <summary>
```
```sh
python3 tools/product-journey/run.py --record-blocker --run-dir .artifacts/product-journey/20260705T225534Z-gears-rust-core-maintainer-dwf3-a3-onboard --scenario <scenario> --title <title> --summary <summary>
```
```sh
autonomous_fix ticket_repo=<owner/repo> gh_agent_public_base_url=<public-gh-agent-url>
```
```sh
review
```
```sh
validate
```
