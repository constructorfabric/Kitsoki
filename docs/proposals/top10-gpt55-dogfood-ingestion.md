# Epic: Top-10 GPT-5.5 dogfood ingestion

**Status:** Draft v1. `stories/punch-list/` shipped; the top-10 manifest now lives at `stories/punch-list/testdata/top10_gpt55.yaml`; item runs remain.
**Kind:**   epic
**Slices:** 10 (0/10 shipped)

## Why

The current top-10 backlog mixes runtime substrate, story/productization work,
and process hardening. If we implement it directly from chat, we lose the main
reason kitsoki exists: the work should be driven through kitsoki Studio MCP,
using real operator turns, traces, cassettes, and friction findings. This epic
turns the top-10 into a **`punch-list/v1` manifest** consumed by the generic
[`punch-list`](../stories/punch-list.md) story, with the right story entrypoint per item
and a strict model policy: **implementation attempts use GPT-5.5 through
`codex-native`, not Claude**.

## What changes

This proposal no longer creates a one-off `top-10` story. Instead:

1. Author a `punch-list/v1` manifest for the ten items.
2. Run that YAML through `stories/punch-list/`.
3. Let `punch-list` enforce the Studio MCP driving discipline, profile/model
   policy, verification, findings capture, and reporting.
4. Keep this epic as the top-10 manifest and sequencing plan; delete it when the
   list has either shipped or been split into narrower current proposals.

## Impact

- **Spans:** story, runtime, tracing, TUI, and process.
- **Net surface:** one top-10 `punch-list/v1` manifest, plus per-item traces,
  findings, and follow-up proposal/story updates.
- **Docs on ship:** shipped item details migrate to `docs/architecture/`,
  `docs/stories/`, `docs/tracing/`, or `docs/tui/`; this proposal is deleted
  when all items either ship or are split into narrower current proposals.

## Model and Harness Policy

The driver reference is [`.agents/agents/kitsoki-mcp-driver.md`](../../.agents/agents/kitsoki-mcp-driver.md):
it defines the MCP-only driving discipline and the rule that the session
`profile:` selects the story's maker model. The agent definition itself is
currently declared as `model: opus`; for this epic that file is a **reference
for tool discipline**, not permission to run implementation through Claude.

Implementation attempts must use:

```yaml
harness: live
profile: codex-native
model: gpt-5.5
```

`codex-native` is the local harness profile that resolves to the Codex backend
and defaults to `gpt-5.5` in `.kitsoki.local.yaml.example`. If a Studio MCP tool
cannot pin both profile and model, that is an MCP gap to file before spending
implementation tokens. If a story's `agents:` block names `claude-*`, the active
profile must supersede it; before accepting a run, inspect the trace for the
actual `profile` / `model` stamped on `agent.call.*`.

Allowed non-implementation model use:

- deterministic `story.validate` / `story.test` / `render.*`: no LLM.
- live Studio MCP driving itself: Codex can drive MCP directly from this session.
- read-only investigation may use replay, static inspection, and cassettes.

Disallowed:

- `profile: claude-native` for implementation.
- `synthetic-claude` for implementation.
- accepting a story-local `model: claude-*` default without trace proof that
  `codex-native` overrode it.
- automated tests that call a real LLM.

## Punch-List Manifest Sketch

```yaml
version: punch-list/v1
defaults:
  harness: live
  profile: codex-native
  model: gpt-5.5
  trace_root: .artifacts/top10-dogfood/traces
  require_trace_model: true
items:
  - id: load-bug
    title: Fix imported bf default expression load failure
    story: .kitsoki/stories/kitsoki-dev/app.yaml
    mode: drive
    prompt: "Start from the dogfood hub and reproduce the project-init/bf load failure."
    implementation_story: stories/cherny-loop/app.yaml
    gate_command: "go test ./internal/app ./internal/orchestrator"
    verify:
      - kind: story_validate
        story: .kitsoki/stories/kitsoki-dev
      - kind: command
        cmd: "go test ./internal/app ./internal/orchestrator"
```

The full manifest lives with the implementation under
`stories/punch-list/examples/top10-gpt55.yaml` or `.artifacts/top10-dogfood/`
while dogfooding, then migrates to durable docs only if it remains useful as an
example.

## Story Entrypoint Matrix

| # | Backlog item | Right story entrypoint | How to drive it | Why this entrypoint |
|---|---|---|---|---|
| 1 | Fix imported `bf` `|default:` expression load failure | `.kitsoki/stories/kitsoki-dev/app.yaml` first, then `stories/dev-story/app.yaml` for the isolated import | Open a Studio MCP session on `kitsoki-dev`; drive the human phrase that reaches bugfix/project-init; confirm the load failure in trace; then use a scoped `cherny-loop` or direct runtime fix run with `profile: codex-native` | The failure appears through the dogfood instance's imported `bf` path, so the first proof should be through the same operator surface that broke |
| 2 | Agent capability model: `effect:` taxonomy | `stories/dev-story/app.yaml` design pipeline (`idea`) for the design pass; `stories/cherny-loop/app.yaml` for the runtime slice | Drive a design proposal from the operator hub; once accepted, run cherny-loop with a deterministic Go-test gate over loader/agent packages | This is engine vocabulary, not a workflow story; cherny-loop is the smallest scoped implementation loop with a hard gate |
| 3 | Toolbox/enforcement wiring | `stories/cherny-loop/app.yaml` | Configure goal + gate around `internal/host` and agent policy tests; drive live with `profile: codex-native`; inspect trace for actual model | This is a bounded runtime implementation after the taxonomy lands |
| 4 | Strict cassette/toolbox conformance linting | `stories/cherny-loop/app.yaml`, with `stories/model-harness-eval/app.yaml` as the evidence consumer | Implement the lint path with a no-LLM gate; then drive model-harness-eval to prove the conformance result is visible where eval consumers need it | The work is runtime/tracing lint plus an eval-facing consumer |
| 5 | Project-init hardening | `stories/dev-story/app.yaml` at the init/onboarding rooms; `.kitsoki/stories/kitsoki-dev/app.yaml` for full dogfood | Drive project onboarding like a new operator; use `profile: codex-native`; validate with existing init flows after the load bug is fixed | The feature already lives inside dev-story and must be tested as a human onboarding flow |
| 6 | Session-mining productization | `stories/dev-story-mining/app.yaml` | Drive transcript/source selection through the mining story; use replay for deterministic mining checks; use live only for interpretation gates with `codex-native` | This story exists specifically to turn transcripts into dev-story gates and coverage |
| 7 | Resolve `work-decomposition` vs `stories/deliver` | `stories/deliver/app.yaml` | Drive `deliver` over an accepted proposal and watch where it falls short versus the richer work-decomposition proposal; decide whether to document/delete or build `stories/decompose` | The shipped path is `deliver -> fleet -> ship-it`; the dogfood run should test whether it is enough |
| 8 | Contextual routing operator controls | `.kitsoki/stories/kitsoki-dev/app.yaml` and `stories/routing-demo/app.yaml` | Drive ambiguous/free-form operator phrases through the real dogfood hub, then isolate routing behavior in routing-demo with replay fixtures | The value is operator routing behavior; it must be felt in the hub and then reduced to fixtures |
| 9 | Multi-hop contextual routing | `.kitsoki/stories/kitsoki-dev/app.yaml` | Drive realistic cross-room requests from the hub, e.g. "check X in another room and come back"; capture trace gaps, then implement `route_plan` only after base controls are solid | Multi-hop is only meaningful from a persistent hub with real rooms to leave and return to |
| 10 | `story-qa` skill workflow | `stories/dogfood-marathon/app.yaml` for process dogfood; `stories/dev-story/app.yaml` design pipeline for skill proposal refinement | Drive a small QA backlog through dogfood-marathon, using the future skill shape as the inner verification method; file studio/MCP gaps when the driver cannot capture evidence | The goal is a reusable QA process, so the marathon wrapper is the right surface to expose friction and reporting gaps |

## Sequencing

```
#1 load bug ──▶ #5 project-init
       │
       ├──▶ #2 effect taxonomy ──▶ #3 toolbox enforcement ──▶ #4 conformance lint
       │
       ├──▶ #8 contextual controls ──▶ #9 multi-hop routing
       │
       ├──▶ #7 deliver/work-decomposition decision
       │
       ├──▶ #6 session-mining productization
       │
       └──▶ #10 story-qa skill workflow
```

The load bug is first because it blocks honest dev-story/kitsoki-dev dogfood.
The capability-model slices should land in dependency order. Multi-hop waits
until base contextual controls are observable and correct.

## Shared Decisions

1. **Studio MCP is the driving surface.** Use `session.new`, `session.drive`,
   `session.submit`, `session.inspect`, `session.trace`, and render tools. If a
   needed action cannot be expressed through MCP, file an MCP gap with evidence.
2. **Use real human phrasing before fixtures.** Each item gets at least one
   operator-like live drive when the behavior is interpretive; then the behavior
   is reduced to deterministic fixtures.
3. **GPT-5.5 only for implementation attempts.** The session profile is part of
   the acceptance criteria. Trace proof of `codex-native` / `gpt-5.5` is required
   before any implementation result is accepted.
4. **No real LLM in automated tests.** Live drives are manual dogfood evidence;
   regression tests use replay/cassettes/host stubs.
5. **Independent verification beats self-report.** For code changes, run the
   deterministic gate ourselves on the produced worktree/commit before marking
   a slice shipped.
6. **Do not overfit story fixes.** Any story or prompt hardening found during
   dogfood must help the general class, not just the single item being driven.

## Per-Item Acceptance Evidence

Each item should leave:

- a Studio MCP trace path;
- the story entrypoint, harness, profile, and model used;
- a short operator transcript of the human-like phrases tried;
- deterministic validation (`story.validate`, `story.test`, `go test`, or a
  targeted script gate);
- findings: story bug, runtime bug, MCP gap, prompt issue, or no finding;
- any filed issue links for MCP/story/runtime gaps.

## Tasks

```
## 1. Prepare the punch-list runner
- [x] 1.1 Implement and dogfood `stories/punch-list/`; it now parks when live driver handoff lacks required trace/model evidence.
- [ ] 1.2 Verify Studio MCP `studio.ping` and available handles.
- [ ] 1.3 Verify `codex-native` profile resolves to `gpt-5.5`.
- [ ] 1.4 Add a run ledger under `.artifacts/top10-dogfood/` for traces,
          findings, and per-item summaries.

## 2. First pass: prove entrypoints and friction
- [x] 2.1 Author the top-10 `punch-list/v1` manifest.
- [ ] 2.2 Run #1 through punch-list; capture the load bug.
- [ ] 2.3 Run #5 project-init through punch-list once #1 is fixed.
- [ ] 2.4 Run #8 and #9 routing phrases through punch-list.
- [ ] 2.5 Run #7, #6, and #10 through punch-list.

## 3. Implementation slices
- [ ] 3.1 Implement #2 with `stories/cherny-loop/app.yaml`, profile `codex-native`.
- [ ] 3.2 Implement #3 with `stories/cherny-loop/app.yaml`, profile `codex-native`.
- [ ] 3.3 Implement #4 with `stories/cherny-loop/app.yaml` plus
          `stories/model-harness-eval/app.yaml` evidence, profile `codex-native`.

## 4. Harden and publish
- [ ] 4.1 Add no-LLM flow/cassette regressions for each accepted behavior.
- [ ] 4.2 File MCP/story/runtime gap issues with trace evidence.
- [ ] 4.3 Migrate shipped design into narrative docs and delete/trim this proposal.
```

## Open Questions

1. **How do we force `model: gpt-5.5` from Studio MCP?** `profile: codex-native`
   defaults to GPT-5.5 in the local example, but the MCP API must either accept
   an explicit model override or expose trace-confirmed model selection. *Lean:
   require trace confirmation now; file an MCP gap if explicit model selection
   is unavailable.*
2. **Should we create a codex-specific driver agent definition?** The existing
   `kitsoki-mcp-driver` is a Claude agent definition but contains the right MCP
   discipline. *Lean: for this epic, use it as reference text and drive MCP from
   Codex; later add `kitsoki-mcp-driver-codex` if dispatched agents need the same
   discipline without Claude.*
3. **Does punch-list replace dogfood-marathon?** No. `punch-list` is a generic
   YAML-driven operator worklist. `dogfood-marathon` remains the bug/backlog
   benchmark/report story with triage and cost rollups. *Lean: punch-list can
   call dogfood-marathon for an item, but should not absorb its domain logic.*

## Non-goals

- Using Claude for implementation attempts.
- Replacing deterministic tests with live model runs.
- Building every top-10 item in one session without stopping to fix story/MCP
  friction found along the way.
- Overriding the proposal lifecycle convention: shipped content still migrates
  to narrative docs and proposals are deleted when done.
