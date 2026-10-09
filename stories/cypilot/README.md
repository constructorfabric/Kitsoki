# cypilot — SDLC waterfall story (PRD → ADR/DESIGN → DECOMPOSITION → FEATURE → CODE)

A kitsoki story that wraps cypilot's three workflows (`cypilot-generate`,
`cypilot-plan`, `cypilot-analyze`) as a state machine.  The cypilot
pipeline from the [bug-fix case study](../../docs/case-studies/bug-fix.md).

> **Interim home.** This story lives in kitsoki for fast iteration; per
> proposal §5.5 / Phase 8 it migrates to the cypilot upstream repo once
> the artifact-pipeline shape stabilises. The Go provider
> (`internal/host/cypilot_artifacts.go`) is permanent in kitsoki.

Standalone:

```
kitsoki run stories/cypilot/app.yaml
```

Imported (a parent story like `stories/dev-story/` adds an `imports.cyp`
edge once cypilot is wired in — Wave 3+ work).

## Contract

### Entry state

`idle` — the operator sets `world.ticket_id` + `world.feature_slug`
(typically projected by a parent story's `world_in:`) and types `begin`.
On import use `entry: idle`.

### Exits

| Name | Description | `requires:` keys | Typical world_out |
|---|---|---|---|
| `code_ready` | Code generated + tests pass; hand off to pr-refinement. | `code_artifact` | Parent stories project `code_artifact.pr_title` / `pr_body` into pr-refinement's `world_in:`. |
| `abandoned` | User or LLM bailed. | (none) | Parent stories usually route to `main`. |
| `validation_failed` | An artifact's validate gate failed and could not be refined within the cycle budget. | (none) | Wave 3 — declared but not yet exercised by the rooms; cycle-budget plumbing lands in a later phase. |

Standalone load synthesises `__exit__code_ready`,
`__exit__abandoned`, and `__exit__validation_failed` terminals.

### Visible rooms

| Room | Substates | Checkpoint? | On `accept` |
|---|---|---|---|
| `idle` | one atomic | n/a | `prd_executing` (via intent `begin`) |
| `prd` | `_executing`, `_awaiting_reply` | yes — `prd_artifact` + validate report | `adr_executing` |
| `adr` | `_executing`, `_awaiting_reply` | yes — `adr_artifact` | `design_executing` |
| `design` | `_executing`, `_awaiting_reply` | yes — `design_artifact` | `decomposition_executing` |
| `decomposition` | `_executing`, `_awaiting_reply` | yes — `decomposition_artifact` (plan_path + phase_count) | `feature_executing` (with `feature_index: 0`) |
| `feature` | `_executing`, `_awaiting_reply` | yes — `feature_artifact` (walked N times) | next `feature_executing` if more phases, else `code_executing` |
| `code` | `_executing`, `_awaiting_reply` | yes — `code_artifact` | `@exit:code_ready` |

### `world_in:` keys (parent → child)

| Key | Type | Used by | Default |
|---|---|---|---|
| `ticket_id` | string | every checkpoint's `phase_id:` + post title. | `""` |
| `ticket_title` | string | views / artifact prompts; seeds PRD title. | `""` |
| `feature_slug` | string | canonical slug under which cpt writes artifacts. Required at idle → prd_executing. | `""` |
| `workdir` | string | every iface.artifact / iface.ci call. | `""` |
| `judge_mode` | string | `human` \| `llm` \| `llm_then_human` (default per autonomous §6.4). | `llm_then_human` |
| `judge_confidence_threshold` | float | LLM auto-fire floor. | `0.8` |
| `thread` | string | transport thread (file path / Jira key / chat id). | `""` |

### `world_out:` keys (child → parent on exit)

| Key | Type | Description |
|---|---|---|
| `code_artifact` | object | The final implementation artifact (id, path, summary, pr_title, pr_body, files_changed, tests_*). Required on `@exit:code_ready`. |
| `prd_artifact`, `adr_artifact`, `design_artifact`, `decomposition_artifact`, `feature_artifact` | object | Intermediate artifacts; kept for parent-side post-hoc inspection. |
| `pr_title`, `pr_body` | string | Projected from `code_artifact` for the pr-refinement handoff. |
| `status` | string | `shipped` after `@exit:code_ready`; `"open"` otherwise. |
| `feature_count` | int | Number of decomposed phases the run produced. |

### Intent surface

| Intent | Slots | Description |
|---|---|---|
| `begin` | — | Boot from `idle` into `prd_executing`. |
| `proceed` | — | Advance an `_executing` to its `_awaiting_reply` checkpoint. |
| `accept` | (opt) `author`, `feedback` | Accept the current artifact; advance. |
| `refine` | (opt) `feedback` | Re-run the room's `cpt generate` with feedback. |
| `next_feature` | — | (feature room only) Advance to the next decomposed phase. |
| `quit` | — | Bail; exits via `@exit:abandoned`. |
| `look` | — | Re-render the current view. |

### `host_interfaces:` contract

Five capability surfaces.  The `artifact` iface is the new one
introduced by this story (proposal §2.6).

| Iface | Ops | Default binding |
|---|---|---|
| `artifact` | `list`, `get`, `create`, `validate`, `decompose` | `host.cypilot_artifacts` (shells to `cpt`) |
| `vcs` | `branch`, `diff`, `commit`, `push`, `open_pr`, `pr_status`, `pr_comment`, `merge` | `host.git` (already routes to `gh pr` when gh is installed) |
| `ci` | `run_tests`, `build`, `remote_status` | `host.local` |
| `transport` | `post` | `host.append_to_file` |
| `inbox.add` | — | always-on bare host call (per contract §2.6) |

The cypilot story does NOT declare the `ticket` or `workspace` ifaces —
those are upstream concerns owned by the parent story (dev-story /
cyber-devstory) that picks up the cypilot-track ticket and creates the
workspace before delegating into the cypilot import.

### Host requirements

Standalone Wave 3 needs:

| Handler | Status | File |
|---|---|---|
| `host.cypilot_artifacts` | NEW (Wave 3 / Phase 5) | `internal/host/cypilot_artifacts.go` |
| `host.git`, `host.local`, `host.append_to_file`, `host.inbox.add` | Wave 1 | (existing) |
| `host.agent.decide` | agent-split Phase 8 | `internal/host/agent_decide.go` |

When `cpt` is not on PATH, `host.cypilot_artifacts` surfaces a clean
domain error from every op rather than crashing.  The room's
`on_error:` arc routes back to the previous `_awaiting_reply` so the
operator can fix the environment and refine.

### Agent-split persona table (Phase 8)

All agent calls in this story are judge verdicts — no artifact
production, no file writes. The single persona is:

| Persona | Verb | Phases |
|---|---|---|
| `judge` | `decide` | every `*_awaiting_reply` judge call (prd, adr, design, decomposition, feature, code) |

`decide` emits `{ verdict, intent, reason, confidence }` against the
provided artifact. The agent has no tools (read-only evaluation from
context only).

## Judge polymorphism

Same shape as `stories/bugfix/` / `stories/pr-refinement/`.  Every
`*_awaiting_reply` runs the canonical §6 checkpoint chain:

1. `iface.transport.post` — artifact body to the bound channel.
2. `host.inbox.add` — mirror to the local TUI inbox.
3. Conditional `host.agent.decide` (agent: `judge`) — LLM-judge over
   the artifact + validate report (when judge_mode != "human").
4. Conditional `emit_intent:` — auto-fire the verdict's intent when
   confidence >= threshold AND verdict/intent != "uncertain".

The default `world.judge_mode` for cypilot is `llm_then_human` rather
than `human` (the bugfix default).  Per proposal §6.4 the cypilot
story is autonomous-only for v1 — the LLM-judge handles routine clean
analyze reports without human intervention; cycle-budget escalation
to a human happens at the next `_awaiting_reply` when the verdict's
confidence dips.

## Wave 3 / Phase 5 limitations

What is NOT in this story (deferred to later phases):

- **Interactive prose editing.** v1 is autonomous-only per proposal
  §6.4 — drafting a PRD's prose in a TUI is a lousy UX.  v2 may add an
  interactive editing room once a clean prose surface lands.
- **Parallel ADR + DESIGN.** Proposal §3 sketches ADR and DESIGN as
  parallel rooms after PRD.  v1 serialises them for simplicity; v2 may
  parallelise.
- **Per-feature code rooms.** Proposal §3 has one code room per
  feature phase.  v1 runs one code room at the end of the feature
  chain (the LLM agent's actual code-write work happens via
  `iface.artifact.create kind=code` once, against the union of
  feature artifacts).  v2 may split.
- **Cycle budgets / `validation_failed` exit consumer.** The exit is
  declared but no in-flow path produces it; Wave 3+ cycle budgets
  will route here when refinement exhausts the budget.

## File layout

```
stories/cypilot/
  app.yaml                                — manifest
  README.md                               — this file
  rooms/
    idle.yaml                             — pipeline parked
    prd.yaml                              — _executing + _awaiting_reply
    adr.yaml                              — _executing + _awaiting_reply
    design.yaml                           — _executing + _awaiting_reply
    decomposition.yaml                    — _executing + _awaiting_reply
    feature.yaml                          — _executing + _awaiting_reply (walked N times)
    code.yaml                             — _executing + _awaiting_reply
  prompts/
    judge_prd.md                          — LLM-judge for prd_awaiting_reply
    judge_adr.md
    judge_design.md
    judge_decomposition.md
    judge_feature.md
    judge_code.md
  schemas/
    judge_verdict.json                    — canonical verdict shape
    prd_artifact.json
    adr_artifact.json
    design_artifact.json
    decomposition_artifact.json
    feature_artifact.json
    code_artifact.json
  flows/
    happy_prd_only.yaml                   — autonomous walk through PRD validate
    prd_to_feature.yaml                   — full chain (llm_then_human, confident)
    analyze_fails_bails.yaml              — analyze finds issues → human refine
    handoff_to_pr.yaml                    — human walk to @exit:code_ready
```

## `cpt` CLI mapping

The provider (`internal/host/cypilot_artifacts.go`) shells out to `cpt`
using the proposal's §6.4 idealised command shapes:

| Op | cpt invocation |
|---|---|
| `list` | `cpt artifact list [--kind <k>] --json` |
| `get` | read the file directly (`cpt artifact path --id <id> --json` resolves) |
| `create` | `cpt generate --kind <k> --title <t> --slug <s> [--parent <p>] --json` |
| `validate` | `cpt analyze --target <id> [--mode <m>] --json` |
| `decompose` | `cpt plan --task <id> --json` |

Today's real `cpt` CLI (per `focused-engineering/cypilot/.core/workflows/`)
uses `--json` as a top-level flag and slightly different subcommand
verbs.  The provider tolerates both JSON envelopes and plain-text
fallback for `list` and `decompose`; the LLM-judge prompts read the
`report` field regardless of envelope shape.  See the provider source
for the full adapter behaviour.

## See also

- [`docs/case-studies/bug-fix.md`](../../docs/case-studies/bug-fix.md)
  — the full cypilot story design.
- [`docs/proposals/notes/dev-story-implementation-contract.md`](../../docs/proposals/notes/dev-story-implementation-contract.md)
  Wave 3 / Phase 5 section — handler names, world keys, op schemas.
- [`stories/bugfix/`](../bugfix/) — the canonical judge-polymorphism /
  checkpoint shape this story mirrors.
- [`stories/pr-refinement/`](../pr-refinement/) — the tail this story
  hands off to via `@exit:code_ready`.
- `focused-engineering/cypilot/.core/workflows/{plan,generate,analyze}.md` —
  the cypilot workflows wrapped by this story.
