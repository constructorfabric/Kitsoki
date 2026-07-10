# Product journey driver handoff

- Run: `20260705T225534Z-gears-rust-core-maintainer-dwf3-a3-onboard`
- Driver agent: `.agents/agents/product-journey-qa-driver.md`
- Run dir: `.artifacts/product-journey/20260705T225534Z-gears-rust-core-maintainer-dwf3-a3-onboard`
- Project: `constructorfabric/gears-rust`
- Persona: `Core maintainer`
- Review: `needs_evidence`
- Evidence: 2 / 6
- Proof evidence: 1 attached; minimum proof 1 / 5
- Findings: 0

## Operator Warning

This handoff does not automatically launch an LLM. Use it as the reviewable contract for a live or cassette-backed driver pass, then attach evidence and run review + validation.

## Suggested Driver Prompt

Drive product journey QA for run_dir=.artifacts/product-journey/20260705T225534Z-gears-rust-core-maintainer-dwf3-a3-onboard. Open or attach stories/product-journey-qa/app.yaml, submit `load run_dir=.artifacts/product-journey/20260705T225534Z-gears-rust-core-maintainer-dwf3-a3-onboard`, then inspect story world `last_result.driver_scenarios`, `last_result.next_driver_capture`, `last_result.next_driver_attach_command`, `last_result.next_driver_blocker_command`, `last_result.missing_proof_evidence`, and `last_result.driver_final_gates`. Respect each scenario's `live_budget` from driver-plan.json; when the live budget is exhausted, record the blocker, journal the attempt, and move to the next scenario. Use `last_result.next_driver_attach_command` for the first proof attach when present, or `last_result.next_driver_blocker_command` when the slot is attempted but blocked, then use Kitsoki Studio MCP and visual MCP to capture proof-source evidence or blockers, record findings, then run the autonomous issue-to-fix gate when credible issue findings exist. If the autonomous gate is not armed with ticket_repo and gh_agent_public_base_url, leave those parameters explicit for the operator instead of silently skipping the gate. Finish with review and validation.

## Inputs

- `agent_brief`: `agent-brief.md`
- `driver_plan`: `driver-plan.md`
- `driver_journal`: `driver-journal.md`
- `execution_plan`: `execution-plan.md`
- `evidence`: `evidence.json`
- `scenario_outcomes`: `scenario-outcomes.md`
- `media_manifest`: `media-manifest.json`

## Dispatch Modes

- `replay`: Use existing cassettes or deterministic fixtures. Safe for no-LLM regression runs.
- `record`: Capture a new reusable cassette or visual evidence path with explicit operator approval.
- `live`: Use live model behavior only when the operator explicitly authorizes cost-bearing exploration.

## Missing Evidence

- `project-onboarding` / `rendered_tui_frame`: Save the rendered TUI/web frame for the room under review.
- `project-onboarding` / `generated_config_diff`: Save the generated config diff or a no-change note.
- `project-onboarding` / `onboarding_smoke_result`: Save the deterministic onboarding smoke result.
- `project-onboarding` / `key_interaction_video`: Save an MP4/GIF clip or retained video reference for Slidey playback.

## Missing Proof Evidence

- `project-onboarding`: proof 1 / 5 (captured 1); missing `generated_config_diff`, `key_interaction_video`, `onboarding_smoke_result`, `rendered_tui_frame`
  - `generated_config_diff`: Save the generated config diff or a no-change note.
    ```sh
    python3 tools/product-journey/run.py --attach-evidence --run-dir .artifacts/product-journey/20260705T225534Z-gears-rust-core-maintainer-dwf3-a3-onboard --scenario project-onboarding --evidence-kind generated_config_diff --evidence-path <path-or-retained-id> --evidence-source <retained|external|local|cassette> --notes "Save the generated config diff or a no-change note."
    ```
  - `key_interaction_video`: Save an MP4/GIF clip or retained video reference for Slidey playback.
    ```sh
    python3 tools/product-journey/run.py --attach-evidence --run-dir .artifacts/product-journey/20260705T225534Z-gears-rust-core-maintainer-dwf3-a3-onboard --scenario project-onboarding --evidence-kind key_interaction_video --evidence-path <path-or-retained-id> --evidence-source <retained|external|local|cassette> --notes "Save an MP4/GIF clip or retained video reference for Slidey playback."
    ```
  - `onboarding_smoke_result`: Save the deterministic onboarding smoke result.
    ```sh
    python3 tools/product-journey/run.py --attach-evidence --run-dir .artifacts/product-journey/20260705T225534Z-gears-rust-core-maintainer-dwf3-a3-onboard --scenario project-onboarding --evidence-kind onboarding_smoke_result --evidence-path <path-or-retained-id> --evidence-source <retained|external|local|cassette> --notes "Save the deterministic onboarding smoke result."
    ```
  - `rendered_tui_frame`: Save the rendered TUI/web frame for the room under review.
    ```sh
    python3 tools/product-journey/run.py --attach-evidence --run-dir .artifacts/product-journey/20260705T225534Z-gears-rust-core-maintainer-dwf3-a3-onboard --scenario project-onboarding --evidence-kind rendered_tui_frame --evidence-path <path-or-retained-id> --evidence-source <retained|external|local|cassette> --notes "Save the rendered TUI/web frame for the room under review."
    ```

## Finalize

```sh
autonomous_fix ticket_repo=<owner/repo> gh_agent_public_base_url=<public-gh-agent-url>
```
```sh
review
```
```sh
validate
```
