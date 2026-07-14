# Product journey QA agent brief

- Run: `20260705T225534Z-gears-rust-core-maintainer-dwf3-a3-onboard`
- Project: `constructorfabric/gears-rust`
- Persona: `Core maintainer`
- Surface preference: `terminal-first`
- Risk focus: reviewability, house-style, minimal diffs
- Recommended driver: `.agents/agents/product-journey-qa-driver.md`
- Driver plan: `driver-plan.md`

## Persona Lens

- Starting surface: terminal-first; prefer TUI/session state before browser surfaces
- First question: Will this produce a minimal, reviewable diff that follows the repository's style?
- Evidence emphasis: candidate diff, targeted tests, full-suite classification, and trace events for unexpected routing
- Escalation trigger: generated churn, hidden broad scope, missing deterministic test proof, or unclear ownership boundary
- Finding bias: Prefer reviewability, bisectability, and least-surprise findings over cosmetic notes.

## Mission

Drive the product journey as this persona using Kitsoki MCP and visual MCP. Capture evidence, record concrete findings, and avoid treating planned steps as validated.

## Operating Rules

- Read the current visual or Kitsoki frame before choosing the next action.
- Use natural persona phrasing; do not optimize only for the scripted happy path.
- Prefer MCP evidence over prose claims: screenshots, session traces, TUI frames, diffs, oracle output, and videos.
- Record strengths as well as weaknesses, issues, and fixes.
- If a live LLM or paid service would be required, stop and record the blocker instead of calling it from an automated test.
- Attach every useful artifact, then submit autonomous_fix when credible issue findings exist, review, and validate through the story session. Use the CLI fallback commands only when the story session is unavailable.

## Scenario Order

### 1. Project onboarding

- Scenario: `project-onboarding`
- Story: `stories/dev-story/app.yaml`
- MCP tools: session.open, session.inspect, render.tui, visual.observe
- Evidence: session_trace, rendered_tui_frame, generated_config_diff, onboarding_smoke_result, key_interaction_video, flow-fixture
- Live budget: Spend at most 20 live minutes on this scenario. When the budget is reached, stop live exploration, record the blocker or partial evidence, and journal the attempt before moving to the next scenario.

Onboard constructorfabric/gears-rust using Kitsoki's documented project setup path. Confirm the generated project profile names plausible rust commands, repo files, and the next story to launch.

Success criteria:
- Generated project config names real commands and relevant project files.
- Persona can state the next Kitsoki command or room to use.
- No silent operator-question defaulting occurs.

Minimum proof:
- Done when: The persona can identify the generated project profile, the relevant commands/files, and the next Kitsoki story to launch.
- Minimum evidence: `session_trace`, `rendered_tui_frame`, `generated_config_diff`, `onboarding_smoke_result`, `key_interaction_video`
- Block if:
  - The onboarding story cannot be opened or rendered.
  - The path requires live LLM authorization and no cassette exists.
  - Generated config or smoke output is unavailable for deterministic review.

## Missing Evidence

- `project-onboarding` / `rendered_tui_frame`: Save the rendered TUI/web frame for the room under review.
- `project-onboarding` / `generated_config_diff`: Save the generated config diff or a no-change note.
- `project-onboarding` / `onboarding_smoke_result`: Save the deterministic onboarding smoke result.
- `project-onboarding` / `key_interaction_video`: Save an MP4/GIF clip or retained video reference for Slidey playback.

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
