# cherny-loop

Facilitate a **Cherny loop** (loop engineering, after Boris Cherny): an agent
iterates toward a goal until a **gate** proves the goal is *actually met* — or a
**budget** ceiling is hit. **The loop runs itself** — the operator kicks it off
once and watches it converge; every iteration streams a breadcrumb and is
persisted as a numbered artifact, so the run is watchable, restartable, and
shareable.

## The loop

```
configuring → baseline ─ RED (auto) ─→ iterating → gating ──┐ (auto-loop)
     ▲            │                        ▲                 │
     └─ reconfig ─┤ GREEN                  └── loop_again ───┤ goal met   → @exit:achieved
                  └─ accept                                  ├ budget hit → @exit:exhausted
                     (already met → @exit:achieved)          └ abort      → @exit:abandoned
```

`configuring` is the root — the operator lands where they act, with no `idle`/
`begin` pass-through turn. **After `launch`, no further prodding is needed**: the
loop drives itself maker → gate → repeat until a gate passes or a budget stops it.

- **planning** — when the operator gives only a free-form goal, `configure` asks
  a planner to choose the proof strategy: existing script, script the maker
  should create, prompt-only review, or **hybrid** (script + focused review).
  The operator can still configure `gate_command` / `gate_mode` explicitly.
- **baseline** — before any maker spend, `launch` runs the gate **once on the
  unchanged artifact** to prove it can fail. A **RED** baseline (gate fails)
  **auto-proceeds** into the loop. A **GREEN** baseline (gate passes) means the
  gate proves nothing — the goal is already met, or the gate is too weak — so the
  loop *refuses to spend* and rests for the operator (`reconfigure` or `accept`).
  This is the red-before-green discipline: never spend budget on a gate that can't
  fail. The RED proof becomes the first maker feedback.
- **iterating** — the **maker** (`host.agent.task`) makes the smallest change
  toward the goal, fed the *previous gate's failure reason* as feedback (the
  ralph-style reset: anchors + one failure reason, not a growing transcript),
  then **auto-emits `check`** into gating.
- **gating** — the **checker** runs and routes automatically: a pass exits
  (`@exit:achieved`), a budget overrun exits (`@exit:exhausted`), otherwise
  `loop_again` re-enters `iterating` with the failure captured — the autonomous
  step.

## Autonomy & the depth cap

The loop is autonomous **in-story**: `iterating` auto-emits `check` → `gating`
auto-emits `loop_again` → `iterating`. Each iteration is two `emit_intent` hops,
and the engine caps emit chains at **8 per turn** (`EmitIntentMaxDepth`), so an
autonomous-from-`launch` run completes in one turn for **iteration budgets up to
~3**. This is a real engine constraint the story deliberately exposes, not papers
over — larger budgets want a **background-job runner** (`background: true` +
`on_complete`; see `docs/stories/background-jobs/`), which runs the loop off the
turn loop with no depth limit. That runner is the documented next step for
unbounded autonomy.

## Gate modes (`world.gate_mode`)

| Mode | Checker | Use for |
|---|---|---|
| `script` | `host.run` the command in `gate_command`; pass iff exit 0 | mechanically checkable goals — tests pass, type-checks, lint clean. Deterministic, free, incorruptible. |
| `agent` | `host.agent.decide` adversarially reviews the artifact; pass iff verdict `pass` | goals no test can encode — prose quality, design soundness. |
| `hybrid` | `host.run` **and** `host.agent.decide`; pass iff both pass | mostly deterministic work with a small judgment tail — e.g. tests pass and docs are persuasive. |

The script gate is the strongest maker/checker split (code can't be talked into
passing) and costs nothing. The planner prefers it when it can honestly prove
the goal, but will use `agent` or `hybrid` when the user's input calls for
judgment.

## Termination — goal met OR budget hit

Evaluated every turn after a failed gate, in priority order:

1. **goal met** → `@exit:achieved` (always wins)
2. **cost ceiling** → `@exit:exhausted` — `world.session_cost_usd` (the reserved,
   engine-maintained $ spend; see `docs/stories/state-machine.md` §6) ≥
   `cost_budget_usd`, when that budget is > 0
3. **iteration ceiling** → `@exit:exhausted` — `iteration` ≥ `iteration_budget`

## Configure & run

For ad hoc work, say the goal first and let the planner pick the right gate
shape:

```
make docs/stories/story-qa.md convincing to a skeptical lead engineer
launch
```

The first line routes to `configure`, which runs the planner. The planner may
choose an existing command, a script the maker should create, a prompt-only
review, or a hybrid gate. If you already know the proof, set
`gate_command=...` / `gate_mode=...` explicitly.

For mechanically checkable work, configure the script gate explicitly:

```
configure goal="Get go test ./internal/ratelimit green" artifact="internal/ratelimit/limiter.go" gate_command="go test ./internal/ratelimit/" iteration_budget=3
launch     # proves the gate is RED, then runs the loop to completion on its own
```

That's it — `launch` runs the whole loop. No per-iteration prodding.

## Worktree confinement

The maker is an autonomous agent that **edits the repo**, so it must never write
the operator's checkout directly. On `launch`, `baseline` mints a dedicated git
worktree under `.worktrees/<workspace_id>` on a throwaway branch
(`workspace_branch`, default `cherny-loop/run`) via the provider-neutral
`workspace` host_interface — the **same seam dev-story uses**, default-bound to
`host.git_worktree`. Every maker turn (`host.agent.task`), every script gate
(`host.run`), and every agent gate (`host.agent.decide`) then runs with its
`working_dir` / `cwd` pinned to that worktree. The maker can **read** the rest of
the repo (cwd doesn't wall off reads); its **writes** land in the isolated
branch. A failed mint routes to `workspace_error` and the loop refuses to run the
maker — it never spends in your checkout.

Because the worktree is reused across re-launches (the mint is idempotent on the
same branch/id), a run is restartable. For parallel loops, set a distinct
`configure workspace_branch=… workspace_id=…`.

> **Scope note — convention, not an OS jail.** `working_dir` pins the agent's
> cwd; it confines writes *by convention*, exactly as dev-story documents
> (`working_dir is NOT a write jail`). Today both backends run with their own
> sandbox disabled (`claude --permission-mode bypassPermissions`,
> `codex --dangerously-bypass-approvals-and-sandbox`), so a determined agent
> writing an absolute / `../` path is not hard-blocked. A true OS-level write
> guarantee (Seatbelt / namespace wrap of the agent subprocess, reusing
> `internal/host/validator_sandbox.go`, plus resolving codex's MCP-submit-vs-
> sandbox conflict) is an engine-layer change tracked separately — it is **not**
> achievable from story YAML.

## Tracking / restart / share

Each iteration writes `iteration-<n>` via `host.artifacts_dir` under
`.artifacts/`, recording the maker's summary and the gate failure it acted on —
the run trail that makes a loop auditable and resumable.

## Tests

Deterministic, no-LLM flow fixtures under `flows/` cover: achieved (script,
agent, and hybrid gates), free-form recording configuration, planner-selected
gates, baseline-green blocking, iteration-budget exhaustion, cost-budget
exhaustion, the feedback-into-next-iteration edge, maker-error recovery, and a
full multi-iteration run to the ceiling. The fixtures assert host-call contracts
with `expect_host_calls` / `expect_no_host_calls`, so they prove both the final
state and the side effects that got there.

```
kitsoki test flows stories/cherny-loop/app.yaml
```

The general story QA runbook uses this story as its worked example:
[`docs/stories/story-qa.md`](../../docs/stories/story-qa.md).

## Provider profiles

The story is backend-neutral: `host.agent.task` and `host.agent.decide` run
through the active Kitsoki agent backend/profile. That means the same loop can
drive native Claude Code, native Codex, or synthetic.new-backed profiles without
story changes.

Quick local setup:

```
cp .kitsoki.local.yaml.example .kitsoki.local.yaml
export SYNTHETIC_API_KEY=syn_...
kitsoki web --stories-dir stories
```

Then pick `synthetic-claude`, `synthetic-codex`, `claude-native`, or
`codex-native` in the web header. In the TUI, use `/provider synthetic-claude`
or `/provider synthetic-codex`. For `synthetic-codex`, follow the codex provider
note in [docs/getting-started.md](../../docs/getting-started.md): Codex may need
a `[model_providers.synthetic]` entry in `~/.codex/config.toml`; environment
variables alone are not enough when Codex is authenticated through a ChatGPT
account.

## Implementation note (engine discipline)

The gate result is bound by a host call in `gating.on_enter`; routing is done by
**guarded `emit_intent`s that compare `gate_ok` to a concrete bool**. `gate_ok`
defaults to `""` (tri-state) and is reset before each check, so every routing
guard is false in the pre-bind emit pass and the routing defers to the post-bind
pass once the checker has run — the bugfix decision-emit discipline. See
`rooms/gating.yaml`.

The autonomous self-loop relies on `loop_again` targeting `iterating` by its
**explicit state name** (not `.`): an `emit_intent` only re-runs a target's
`on_enter` when the target differs from the current state (the maker room and
checker room alternate, so each hop is a real state change). Per-iteration host
responses are addressable via a templated invoke `id:` (`maker-{{iteration}}` /
`gate-{{iteration}}`) threaded into `args.call` — `by_call:` / cassettes select
on it.
