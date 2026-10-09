# Product journey driver plan

- Run: `20260705T225534Z-gears-rust-core-maintainer-dwf3-a3-onboard`
- Driver: `.agents/agents/product-journey-qa-driver.md`
- Project: `constructorfabric/gears-rust`
- Persona: `Core maintainer`

## 1. Project onboarding

- Scenario: `project-onboarding`
- Story: `stories/dev-story/app.yaml`
- Harness: `replay-or-record`
- Visual surface: `tui`
- Live budget: Spend at most 20 live minutes on this scenario. When the budget is reached, stop live exploration, record the blocker or partial evidence, and journal the attempt before moving to the next scenario.
- MCP: session.open, session.inspect, render.tui, visual.observe
- MCP tools: mcp__kitsoki__session_new, mcp__kitsoki__session_attach, mcp__kitsoki__session_inspect, mcp__kitsoki__session_world, mcp__kitsoki__session_trace, mcp__kitsoki__render_tui, mcp__kitsoki__render_tui_png, mcp__kitsoki__visual_observe
- Evidence dir: `evidence/gears-rust--core-maintainer/project-onboarding`

Onboard constructorfabric/gears-rust using Kitsoki's documented project setup path. Confirm the generated project profile names plausible rust commands, repo files, and the next story to launch.

### Action Sequence

- session.new or session.attach using the scenario primary_story
- render.tui or render.tui_png before and after meaningful turns
- visual.observe before acting and when capturing evidence
- session.status/session.world first; session.inspect only when targeted reads are insufficient

### Driver Actions

#### open_surface

- Goal: Open or attach the Kitsoki/product surface named by the scenario.
- Tools: session.open
- MCP tools: mcp__kitsoki__session_new, mcp__kitsoki__session_attach
- Evidence: (none)
- Record: Record the handle, URL, or reason this surface could not be opened.

#### read_current_frame

- Goal: Observe the exact operator-visible state before acting.
- Tools: session.status, render.tui, visual.observe
- MCP tools: mcp__kitsoki__session_status, mcp__kitsoki__render_tui, mcp__kitsoki__render_tui_png, mcp__kitsoki__visual_observe
- Evidence: rendered_tui_frame
- Record: Save frame evidence under evidence/gears-rust--core-maintainer/project-onboarding/ before evaluating usability.

#### act_as_persona

- Goal: Take the next natural persona action and preserve route/interaction evidence.
- Tools: session.submit, session.trace
- MCP tools: mcp__kitsoki__session_submit, mcp__kitsoki__session_drive, mcp__kitsoki__session_trace
- Evidence: session_trace, key_interaction_video
- Record: Prefer natural phrasing when route quality is under test; otherwise use deterministic action handles.

#### capture_required_evidence

- Goal: Attach every minimum-evidence slot or record the matching quality-gate blocker.
- Tools: visual.observe, render.tui, session.trace
- MCP tools: mcp__kitsoki__visual_observe, mcp__kitsoki__render_tui, mcp__kitsoki__render_tui_png, mcp__kitsoki__session_trace
- Evidence: session_trace, rendered_tui_frame, generated_config_diff, onboarding_smoke_result, key_interaction_video, flow-fixture
- Record: Use attach commands for captured evidence; use blocker command for honest gaps.

#### journal_attempt

- Goal: Append the driver's actual attempt, tools used, evidence references, and blockers.
- Tools: story.driver_event, tools/product-journey/run.py --record-driver-event
- MCP tools: (none)
- Evidence: driver-journal.md
- Record: Journal the attempt even when the scenario only produced a blocker.


### Persona Prompts

- Act as Core maintainer: Maintains a mature repository and is skeptical of bot-generated churn.
- Risk focus: reviewability, house-style, minimal diffs
- Start from: terminal-first; prefer TUI/session state before browser surfaces
- First skepticism check: Will this produce a minimal, reviewable diff that follows the repository's style?
- Escalate when: generated churn, hidden broad scope, missing deterministic test proof, or unclear ownership boundary
- Evidence emphasis: candidate diff, targeted tests, full-suite classification, and trace events for unexpected routing
- Use natural operator phrasing where route quality or prompt quality is under test.

### Persona Lens

- Starting surface: terminal-first; prefer TUI/session state before browser surfaces
- First question: Will this produce a minimal, reviewable diff that follows the repository's style?
- Evidence emphasis: candidate diff, targeted tests, full-suite classification, and trace events for unexpected routing
- Escalation trigger: generated churn, hidden broad scope, missing deterministic test proof, or unclear ownership boundary
- Finding bias: Prefer reviewability, bisectability, and least-surprise findings over cosmetic notes.

### Evidence

- `session_trace`: .context/dwf3-a3-recording-notes.md - Save the Kitsoki session trace or trace id.
- `rendered_tui_frame`: <path-or-retained-id> - Save the rendered TUI/web frame for the room under review.
- `generated_config_diff`: <path-or-retained-id> - Save the generated config diff or a no-change note.
- `onboarding_smoke_result`: <path-or-retained-id> - Save the deterministic onboarding smoke result.
- `key_interaction_video` playback: <path-or-retained-id> - Save an MP4/GIF clip or retained video reference for Slidey playback.
- `flow-fixture`: stories/dev-story/flows/onboard_gears_rust.yaml - Save a local flow fixture (`kitsoki test flows <app.yaml> --flows ...`) that replays this scenario no-LLM.

### Minimum Proof

- Done when: The persona can identify the generated project profile, the relevant commands/files, and the next Kitsoki story to launch.
- Minimum evidence: `session_trace`, `rendered_tui_frame`, `generated_config_diff`, `onboarding_smoke_result`, `key_interaction_video`
- Block if:
  - The onboarding story cannot be opened or rendered.
  - The path requires live LLM authorization and no cassette exists.
  - Generated config or smoke output is unavailable for deterministic review.

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

### Finding Command

```sh
python3 tools/product-journey/run.py --record-finding --run-dir .artifacts/product-journey/20260705T225534Z-gears-rust-core-maintainer-dwf3-a3-onboard --finding-kind <strength|weakness|issue|fix> --scenario project-onboarding --title <title> --summary <summary> --evidence-path <path-or-retained-id>
```

### Blocker Command

```sh
python3 tools/product-journey/run.py --record-blocker --run-dir .artifacts/product-journey/20260705T225534Z-gears-rust-core-maintainer-dwf3-a3-onboard --scenario project-onboarding --title <blocker-title> --summary <why-this-scenario-could-not-be-captured> --evidence-path <trace-or-frame-path>
```

### Journal Command

```sh
python3 tools/product-journey/run.py --record-driver-event --run-dir .artifacts/product-journey/20260705T225534Z-gears-rust-core-maintainer-dwf3-a3-onboard --scenario project-onboarding --dispatch-mode <replay|record|live> --driver-status <attempted|captured|blocked|validated> --mcp-tools <comma-separated-tools-used> --evidence-refs <comma-separated-paths-or-retained-ids> --blockers <comma-separated-blockers-if-any> --summary <what-the-driver-actually-tried>
```

### Success Criteria

- Generated project config names real commands and relevant project files.
- Persona can state the next Kitsoki command or room to use.
- No silent operator-question defaulting occurs.

## Final Gates

```sh
autonomous_fix ticket_repo=<owner/repo> gh_agent_public_base_url=<public-gh-agent-url>
```
```sh
review
```
```sh
validate
```
