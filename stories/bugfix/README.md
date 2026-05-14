# bugfix — general-purpose, provider-neutral bug-fix pipeline

A reusable kitsoki story implementing the Wave 1 / Slice α scope of
`docs/proposals/dev-story-bugfix-unify-proposal.md`. The seven visible
rooms (`idle → reproducing → proposing → implementing → testing →
reviewing → validating → done`) collapse the cyber-repo's 14-phase
autonomous pipeline into one state machine while keeping every
checkpoint shape identical across `human` / `llm` / `llm_then_human`
judge modes.

Standalone:

```
kitsoki run stories/bugfix/app.yaml
```

Imported (see Wave 2's `stories/dev-story/app.yaml` or
`stories/kitsoki-dev/app.yaml`).

## Contract

### Entry state

`idle` — the operator starts the pipeline by typing `start`. Set on
import via `entry: idle`.

### Exits

| Name | Description | `requires:` keys | Typical world_out |
|---|---|---|---|
| `done` | Pipeline succeeded; hand off to pr-refinement. | `done_artifact` | Parent stories project `done_artifact` into their own `pr_id` / `pr_url` after pr-refinement runs. |
| `abandoned` | User or LLM bailed (`quit`). | (none) | Parent stories usually route to a `main` / inbox state. |

Standalone (no parent) load synthesises `__exit__done` and
`__exit__abandoned` terminals so `kitsoki run` and `kitsoki test flows`
both terminate cleanly.

### Visible rooms

| Room | Substates | Checkpoint? | On `accept` |
|---|---|---|---|
| `idle` | one atomic | n/a | `reproducing_executing` (via intent `start`) |
| `reproducing` | `_executing`, `_awaiting_reply` | yes — `reproduction_artifact` | `proposing_executing` |
| `proposing` | `_executing`, `_awaiting_reply` | yes — `propose_fix_artifact` | `implementing_executing` |
| `implementing` | `_executing` only | no | `testing_executing` (via `proceed`) |
| `testing` | `_executing`, `_awaiting_reply` | yes — `implement_review_artifact` | `reviewing_executing` |
| `reviewing` | `_executing` only | no | `validating_executing` (via `proceed`) |
| `validating` | `_executing`, `_awaiting_reply` | yes — `validate_artifact` | `done_executing` |
| `done` | `_executing`, `_awaiting_reply` | yes — `done_artifact` | `@exit:done` |

### `world_in:` keys (parent → child)

The importer projects these from its own world. All have type+default
in `app.yaml`'s `world:` block so the child loads standalone for tests.

| Key | Type | Used by | Default |
|---|---|---|---|
| `ticket_id` | string | Every checkpoint's `phase_id:` and post title. | `""` |
| `ticket_title` | string | Views / artifact prompts. | `""` |
| `ticket_url` | string | Returned to parent on completion. | `""` |
| `thread` | string | The transport's thread identifier (file path / Jira key / chat ID). | `""` |
| `workspace_id` | string | `iface.workspace.sync` arg. | `""` |
| `workdir` | string | Most `iface.{vcs,ci}.*` calls. | `""` |
| `base_branch` | string | `iface.vcs.open_pr.base`. | `""` |
| `feature_branch` | string | `iface.vcs.branch.name`. | `""` |
| `bugfix_mode` | string | `full` (walk every room) \| `quick` (Wave 2 shortcut). | `full` |
| `judge_mode` | string | `human` \| `llm` \| `llm_then_human` — see Judge polymorphism below. | `human` |
| `judge_confidence_threshold` | float | Floor for auto-firing the LLM's verdict (Wave 2 — runtime gap). | `0.8` |
| `allowed_authors` | string (CSV) | Authorisation filter for reply intents arriving over the transport. | `""` |

### `world_out:` keys (child → parent on exit)

| Key | Type | Description |
|---|---|---|
| `done_artifact` | object | Postmortem-style close-out (see `schemas/done_artifact.json`). Parent stories carry this into pr-refinement. |
| `reproduction_artifact` | object | Evidence the bug is reproducible. |
| `propose_fix_artifact` | object | The proposed fix. |
| `implement_review_artifact` | object | Test review + status. |
| `validate_artifact` | object | Full-env validation outcome. |
| `status` | string | `fixed` after `@exit:done`; left as `"open"` on `@exit:abandoned`. |
| `cycle` | int | Total refinement cycles consumed. |
| `pr_id`, `pr_url`, `ci_state` | string | Held for the pr-refinement handoff (populated by Wave 2). |

### Intent surface

| Intent | Slots | Description |
|---|---|---|
| `start` | — | Begin the pipeline from `idle`. |
| `proceed` | — | Advance from an `_executing` room into its `_awaiting_reply` checkpoint. |
| `accept` | (opt) `author`, `feedback` | Accept the current checkpoint artifact; advance to the next room. (In `bugfix_mode=quick`, accept at `testing_awaiting_reply` jumps to `done_executing`, skipping reviewing + validating.) |
| `refine` | (opt) `feedback` | Re-execute the current room with feedback in `world.refine_feedback`; increments both `<phase>_cycle` and the global `cycle`. When `<phase>_cycle` has hit `<phase>_budget` the refine arc instead routes to `@exit:abandoned` with `abandon_reason=<phase>_cycle_budget_exhausted` (see Cycle budgets below). |
| `restart_from` | (opt) `stage` | Rewind to a named earlier room; the target phase's `<phase>_cycle` is reset to 0 so the operator gets a fresh budget. Stages: `reproducing`, `proposing`, `implementing`, `testing`, `validating`. |
| `jump_to` | (opt) `stage` | Skip forward to a later room; increments `world.unsafe_jumps_made` for audit. Stages: `testing`, `validating`, `done` (aliases: `test`, `validate`, `pr`). Unknown stage → `@exit:abandoned` with `abandon_reason=jump_to_unknown_stage`. |
| `quick_fix` | — | Shortcut from `idle` (or the reproducing checkpoint): set `bugfix_mode=quick`. The testing checkpoint's `accept` arc reads this flag and jumps to `done_executing`, skipping reviewing + validating (~5 LLM calls total instead of 7). |
| `skip_to_pr` | — | Shortcut from `idle` (or the reproducing checkpoint): jump directly to `validating_executing` with `restart_from_stage=validate`. Sets `bugfix_mode=full` (the user wants the full validate/done tail) and increments `unsafe_jumps_made`. |
| `full_pipeline` | — | Explicit default: walk all phases. Sets `bugfix_mode=full` and routes to `reproducing_executing`. Useful when a previous run left `bugfix_mode=quick` in the carried world. |
| `quit` | — | Bail; exits via `@exit:abandoned`. |
| `look` | — | Re-render the current view. |

### `host_interfaces:` contract

The story declares six capability surfaces. Operation names and I/O
shapes are fixed by contract §2 of
`docs/proposals/notes/dev-story-implementation-contract.md`. The
`default:` value names the standalone binding (provider-neutral local
files / git); parent stories rebind via `imports.<alias>.host_bindings`.

| Iface | Ops | Default binding |
|---|---|---|
| `ticket` | `search`, `get`, `comment`, `transition`, `list_mine` | `host.local_files.ticket` |
| `vcs` | `branch`, `diff`, `commit`, `push`, `open_pr`, `pr_status`, `pr_comment` | `host.git` |
| `ci` | `run_tests`, `build`, `remote_status` | `host.local` |
| `workspace` | `list`, `get`, `create`, `sync` | `host.git_worktree` |
| `transport` | `post` | `host.append_to_file` (kitsoki-dev appends to the local bug file) |
| `inbox.add` | — | always-on bare host call, NOT an iface (per contract §2.6) |

Rebinding from an importer is straightforward — see proposal §5.1–5.3
worked examples. The cyber-repo flavor will rebind to
`{ticket: host.jira, vcs: host.bitbucket, ci: host.jenkins,
workspace: host.workspace_manager, transport: host.jira_comment}`.

### Host requirements

Standalone Wave 1 needs every iface's default handler PLUS
`host.inbox.add` and `host.oracle.ask_with_mcp`. The flow fixtures
stub them all with canned envelopes; Slice β ships the real handlers
in `internal/host/`.

| Handler | Status | File |
|---|---|---|
| `host.local_files.ticket` | Slice β (in flight) | `internal/host/localfiles_ticket.go` |
| `host.git` | Slice β (in flight) | `internal/host/git_vcs.go` |
| `host.local` | Slice β (in flight) | `internal/host/local_ci.go` |
| `host.git_worktree` | Slice β (in flight) | `internal/host/git_worktree.go` |
| `host.append_to_file` | Slice β (in flight) | `internal/host/append_file_transport.go` |
| `host.inbox.add` | Slice β (in flight) | `internal/host/inbox_add.go` |
| `host.oracle.ask_with_mcp` | already shipped | `internal/host/oracle_ask_with_mcp.go` |

The host registry's prefix-fallback lets each "default" handler back
every op on the iface; per-op handlers can be added later without
touching the YAML.

## Judge polymorphism

The defining property of this story: every `_awaiting_reply` state
runs **the same `on_enter` chain** in all three judge modes. The flag
is `world.judge_mode`:

| Mode | Behaviour at every checkpoint |
|---|---|
| `human` | Post + inbox-mirror; wait for an explicit reply intent. (No LLM call.) |
| `llm` | Post + inbox-mirror + run the LLM-judge. The verdict lands in `world.llm_verdict`; when the verdict's `verdict`/`intent` are not "uncertain" AND `confidence >= judge_confidence_threshold` (defaults to 0.8), the `emit_intent:` effect at step 4 auto-fires the verdict's intent in the same turn. An uncertain or low-confidence verdict holds the state for an operator. |
| `llm_then_human` | Same as `llm` for the auto-fire path; the mode flag exists so cyber-repo-flavour parent stories can declare "always also notify a human", which Wave 2 layers above this base contract. |

The judge polymorphism is a single host call per checkpoint, gated by
`when:` — **not** a fork in the state graph. The seven
`_awaiting_reply` states have **identical** `on_enter` shapes
(contract §6) — only `<phase>` and the next-room target vary.

The `emit_intent:` effect is depth-capped at
`machine.EmitIntentMaxDepth` (= 8); a misbehaving LLM that emits a
self-cycling verdict fails loud rather than spinning. See
`internal/machine/machine.go::dispatchEmittedIntents` for the runtime
and `internal/machine/emit_intent_test.go` for the regression suite.

## Cycle budgets and shortcuts (Wave 3 / Phase 4)

The L2 cycle-budget pattern from cyber-repo's 14-phase bugfix is wired
into every checkpointed `_awaiting_reply` room. Per-phase counters
(`<phase>_cycle`) and per-phase budgets (`<phase>_budget`, default 3)
together gate `refine`: when the counter hits the budget the next
refine fires an abandon arc instead of looping. `restart_from`
rewinds to an earlier phase and resets that phase's counter to 0.
`jump_to` skips forward (audit-tracked).

### World keys

| Key | Type | Default | Description |
|---|---|---|---|
| `<phase>_cycle` | int | 0 | Refines consumed in this phase. Incremented by `refine`; reset by `restart_from` into this phase. (`<phase>` ∈ `reproducing`, `proposing`, `implementing`, `testing`, `reviewing`, `validating`, `done`.) |
| `<phase>_budget` | int | 3 | Max refines for this phase. Override per-session via `initial_world` to widen/tighten. |
| `cycle_budget` | int | 3 | Documented global default; no arc reads it directly. |
| `cycle` | int | 0 | Coarse audit counter; sum of all `<phase>_cycle` increments. |
| `unsafe_jumps_made` | int | 0 | Incremented every time a `jump_to` arc fires (including `skip_to_pr` from idle). |
| `abandon_reason` | string | "" | Structured reason set by an abandon arc: `<phase>_cycle_budget_exhausted` or `jump_to_unknown_stage`. |
| `restart_from_stage` | string | "" | Set by `restart_from` to the slot's stage name for audit. |
| `jump_to` | string | "" | Set by `jump_to` to the slot's stage name for audit. |

### Refine → exhaust → abandon sequence

ASCII sequence diagram for a single phase. The pattern is identical
across every checkpointed room — substitute `proposing` /  `testing` /
`validating` / `done` as needed.

```
operator                        machine                              world
   |                               |                                   |
   |--- refine (feedback="x") ---->|                                   |
   |                               |  reproducing_cycle (0) < 3 ✓      |
   |                               |--- target: reproducing_executing  |
   |                               |    set reproducing_cycle = 1 ---->|
   |                               |        re-execute artifact LLM    |
   |<-- view (reproducing_executing) ----|                              |
   |--- proceed ------------------>|                                   |
   |<-- view (reproducing_awaiting_reply) |                             |
   |                                                                   |
   |--- refine (feedback="y") ---->|                                   |
   |                               |  reproducing_cycle (1) < 3 ✓      |
   |                               |--- target: reproducing_executing  |
   |                               |    set reproducing_cycle = 2 ---->|
   |<-- view (reproducing_executing) ----|                              |
   |--- proceed ------------------>|                                   |
   |<-- view (reproducing_awaiting_reply) |                             |
   |                                                                   |
   |--- refine (feedback="z") ---->|                                   |
   |                               |  reproducing_cycle (2) < 3 ✓      |
   |                               |--- target: reproducing_executing  |
   |                               |    set reproducing_cycle = 3 ---->|
   |<-- view (reproducing_executing) ----|                              |
   |--- proceed ------------------>|                                   |
   |<-- view (reproducing_awaiting_reply) |                             |
   |                                                                   |
   |--- refine (one too many) ---->|                                   |
   |                               |  reproducing_cycle (3) >= 3 ✗     |
   |                               |--- target: @exit:abandoned ------>|
   |                               |    set abandon_reason             |
   |                               |        = reproducing_cycle_budget_exhausted
   |                               |    set status = "abandoned"       |
   |<-- terminal (__exit__abandoned) ----|                              |
```

### Mode shortcuts

The `bugfix_mode` world key gates collapse paths:

| Mode | Behaviour |
|---|---|
| `full` (default) | Walks every room in order. |
| `quick` | At `testing_awaiting_reply.accept`, jump to `done_executing` (skipping reviewing + validating). Set via the `quick_fix` intent at idle or the first checkpoint. |

Entry intents:

| Intent | Sets | Lands at |
|---|---|---|
| `start` / `full_pipeline` | `bugfix_mode=full` | `reproducing_executing` |
| `quick_fix` | `bugfix_mode=quick` | `reproducing_executing` |
| `skip_to_pr` | `bugfix_mode=full`, `restart_from_stage=validate`, `unsafe_jumps_made++` | `validating_executing` |

The shortcuts are also reachable from `reproducing_awaiting_reply` for
operators who decide mid-flow (after seeing the reproducer) that the
fix is trivial.

## Wave 1 limitations

What is NOT in Wave 1 (deferred to Wave 2+):

- **`@exit:done` parent (pr-refinement import).** Standalone Wave 1
  exits at `__exit__done`. Wave 2 imports `stories/pr-refinement/`
  for the tail (CI watch, comment resolution, merge).
- ~~**Cycle budgets.**~~ ✓ Landed in Wave 3 / Phase 4 (see above).
- ~~**`restart_from_stage` plumbing.**~~ ✓ Landed in Wave 3 / Phase 4.
- ~~**`quick_fix` / `skip_to_pr` shortcut intents.**~~ ✓ Landed in Wave 3 / Phase 4.

## File layout

```
stories/bugfix/
  app.yaml                                — manifest (this story's loadable surface)
  README.md                               — this file
  rooms/
    idle.yaml                             — pipeline parked
    reproducing.yaml                      — _executing + _awaiting_reply
    proposing.yaml                        — _executing + _awaiting_reply
    implementing.yaml                     — _executing only
    testing.yaml                          — _executing + _awaiting_reply
    reviewing.yaml                        — _executing only
    validating.yaml                       — _executing + _awaiting_reply
    done.yaml                             — _executing + _awaiting_reply
  prompts/
    reproducing_executing.md              — artifact-producing
    proposing_executing.md                — artifact-producing
    testing_executing.md                  — artifact-producing
    validating_executing.md               — artifact-producing
    done_executing.md                     — artifact-producing
    judge_reproducing.md                  — LLM-judge for reproducing_awaiting_reply
    judge_proposing.md                    — LLM-judge for proposing_awaiting_reply
    judge_testing.md                      — LLM-judge for testing_awaiting_reply
    judge_validating.md                   — LLM-judge for validating_awaiting_reply
    judge_done.md                         — LLM-judge for done_awaiting_reply
  schemas/
    judge_verdict.json                    — { verdict, intent, reason, confidence }
    reproducing_artifact.json
    proposing_artifact.json
    testing_artifact.json
    validating_artifact.json
    done_artifact.json
  flows/                                  — deterministic flow fixtures (host stubs only)
    happy_human.yaml                      — accept at every checkpoint (canonical)
    happy_llm.yaml                        — judge_mode=llm with confident verdict
    happy_llm_then_human.yaml             — judge_mode=llm_then_human, human-driven advance
    happy_quick_fix.yaml                  — bugfix_mode=quick collapses testing → done
    llm_uncertain_holds.yaml              — judge_mode=llm with uncertain verdict
    llm_then_human_refine_then_accept.yaml — LLM verdict=refine → refine arc fires via emit_intent
    budget_exhaust_llm_then_human.yaml    — LLM-driven refine bounded by per-phase budget gate
    refine_once_then_accept.yaml          — reproducing: refine → re-execute → accept
    refine_at_each_stage.yaml             — refine + counter increment per phase
    refine_budget_exhaust_*.yaml          — per-phase: counter at budget, next refine abandons
    restart_from_proposing.yaml           — restart_from reproducing from proposing checkpoint
    restart_from_each_stage.yaml          — restart_from each stage from done_awaiting_reply
    restart_from_resets_budget.yaml       — restart_from resets target phase's <phase>_cycle
    jump_to_each_target.yaml              — jump_to each stage alias; unknown stage abandons
    skip_to_pr_from_idle.yaml             — skip directly to validating_executing
    full_pipeline_from_idle.yaml          — explicit-mode default; overrides carried-in quick
    mode_switch_full_to_quick.yaml        — full → quick mid-flow at reproducing checkpoint
    mixed_judge_swap.yaml                 — start llm_then_human, flip to human mid-run
    quit_at_{idle,proposing,validating}.yaml — quit from various states → @exit:abandoned
```

## See also

- [`docs/proposals/dev-story-bugfix-unify-proposal.md`](../../docs/proposals/dev-story-bugfix-unify-proposal.md)
  — the full design.
- [`docs/proposals/notes/dev-story-implementation-contract.md`](../../docs/proposals/notes/dev-story-implementation-contract.md)
  — Slice α / β / γ contract.
- [`docs/imports.md`](../../docs/imports.md) — the imports authoring
  reference for parent stories that wrap `bugfix`.
- [`stories/robbery/`](../robbery/) — the canonical importable
  sub-story (smaller, used by `oregon-trail` as an imports demo).
