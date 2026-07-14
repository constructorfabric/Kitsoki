# dev-story-mining

Turn real Claude Code and Codex transcripts into Kitsoki improvements: named
story gates, rooms, Starlark scripts, flow fixtures, hub routes, skill updates,
and honest enforcement-limit records. The repeatable process from
[`docs/proposals/session-pattern-mining/`](../../docs/proposals/session-pattern-mining/)
is made first-class and runnable as a Kitsoki story.

A meta / dogfood story: Kitsoki improving the very state machines it uses for
its own development. The mechanical skeleton (prepare sources -> mine -> write
the map -> review inventory -> author -> test) is automated; the few real judgements are named
checkpoint gates with a recorded decision at each - the same shape mining is
built to find.

## The pipeline

```
idle ──start──▶ prepare ──▶ mine ──▶ mapping ──▶ map ──▶ decide ──▶ author ──▶ record ──▶ @exit:done
                    │         │          │          │        │          │           │
                    └ refine ─┴ refine ─┴ refine ──┴ refine ┴ refine ─┴ refine ───┴ refine (budgeted)
                                                                   │
   any room: quit / budget-exhausted ──────────────▶ @exit:abandoned
```

| Phase | Producer (persona) | Decision the gate records |
|---|---|---|
| **prepare** | `host.starlark.run` (`scripts/plan_sources.star`) | Which transcript sources, artifact classes, enforcement limits, and L0-L4 ladder apply before any agent mines. |
| **mine** | `miner` (`host.agent.task`) | Is the brief fresh & large enough (>= `min_intents`) across the prepared Claude/Codex sources, with unavailable signals called out? |
| **mapping** | `mapper` (`host.agent.task`) | Announce mapping before MAP review, classify each opportunity against regenerated inventory, then write `OPPORTUNITY_MAP.md`. |
| **map** | existing `map_artifact` | Review the linked opportunity map and decide whether the classifications are acceptable. |
| **decide** | `ranker` (`host.agent.ask`) | Which actionable item to apply next (rank by #intents x mechanicalness x Kitsoki-adoption leverage). |
| **author** | `author` (`host.agent.task`) | Improvement + no-LLM coverage authored, with a real unified diff artifact; **accept is refused while `flows_green` is false**. |
| **record** | `recorder` (`host.agent.task`) | Writes `FINAL_REPORT.md` and checks whether an existing gate can drop a determinism rung (L2->L3->L4). Empty ladder moves are valid. |

Each producer phase emits a schema-validated artifact in `on_enter` (idempotent
via `once:` — reload-safe; the refine/restart arms clear the bind to force a
fresh run). Review phases render those artifacts; the operator (or the LLM
judge) accepts / refines / restarts / quits.

## Judge polymorphism

One `world.judge_mode` flag selects who answers every checkpoint (mirrors
`stories/bugfix`):

- `human` — operator answers (no judge LLM call).
- `llm` — run the judge; a confident verdict is captured, uncertain holds.
- `llm_then_human` — confident verdict auto-advances; uncertain falls through
  to the human view.

`judge_confidence_threshold` (default 0.8) is the auto-advance floor.

## Entry / exits (importable contract)

- **Entry state:** `idle`.
- **Exits:**
  - `done` — `requires: [record_artifact]` — an improvement was authored (or a
    ladder move recorded). An importer maps it via
    `imports.<alias>.exits.done.to`.
  - `abandoned` — operator quit or a phase budget was exhausted.
- **`world_in` contract (optional overrides):** `job`, `transcript_sources`
  (`claude,codex` by default), `project_dir` (Claude transcript dir; empty ->
  current repo slug), `codex_sessions_dir`, `stories_dir`, `target_artifacts`,
  `automation_goal`, `min_intents`, `judge_mode`,
  `judge_confidence_threshold`.
- **Intent surface:** exports `start, accept, refine, restart_from, quit, look`.
- **Hosts required:** `host.starlark.run`, `host.run`, `host.agent.task`,
  `host.agent.ask`, `host.agent.decide`. No `host_interfaces` — the story runs
  standalone with no transport registry; the phase artifacts are the durable
  record.

## Progressive determinism and enforcement limits

`prepare` is intentionally deterministic. It records the source matrix and the
L0-L4 ladder before any mining agent runs:

- L2 is the default target for new story/script skeletons: deterministic effects
  plus named recorded gates.
- L3/L4 changes require recorded gate decisions; the `record` phase proposes
  rung drops only when the data supports them.
- Claude Code can be routed through a pre-model Kitsoki hook.
- Codex cannot be hard-intercepted before the model sees a prompt today. Codex
  enforcement should be represented as launch/workflow routing, MCP dispatch,
  guidance, transcript mining feedback, or an explicit `ENFORCEMENT-LIMIT` item -
  not as a fabricated hook.

## Run it

```sh
# standalone, human-driven
kitsoki run stories/dev-story-mining/app.yaml

# deterministic, LLM-free flow tests (seeded artifacts short-circuit on_enter)
kitsoki test flows stories/dev-story-mining/app.yaml
```

Flows: `flows/happy_human.yaml` (accept through to `@exit:done`),
`flows/prepare_refine_budget.yaml` (refine past the source-planning budget bails),
and `flows/map_refine_budget.yaml` (refine past the map budget bails to
`@exit:abandoned`). `happy_human` stubs the `plan_sources` host envelope and
uses seeded artifacts for later LLM-producing phases, so it remains LLM-free;
`plan_sources.star` is validated separately with the Starlark checker.

## Not yet wired

The `miner` / `author` personas describe the real kit and authoring loop in
their prompts, but this story does not yet ship cassettes for a recorded
end-to-end run against a live agent. The deterministic demo fixture includes
reviewable markdown and diff artifacts under `demo-artifacts/`; the next step is
to record a real run that applies one improvement, convert its trace with
`kitsoki trace to-flow`, and replace the demo fixture with that trace-derived
bundle.
