---
name: goal
description: Run one bounded-context evaluation cycle of the goal-seeker — "is the goal done, and if not what's the next step?" — emitting a small {verdict, summary, next_instruction} JSON. Keeps the expensive orchestrator context bounded by driving a deterministic log/ledger/preamble (goal.py) and dispatching fresh ephemeral evaluator and worker agents that never accumulate history. Use when the user types /goal, wants to advance a declared goal under docs/goals, or dogfood generalized-usage stabilization. Workers use managed clone-backed Capsule workspaces with no-lost-work integration.
---

# /goal — the goal-seeker

`/goal` runs **one** evaluation cycle against a declared goal and prints a small
JSON verdict. Re-run it (or wrap it in `/loop`) to advance. The whole point is
**bounded context**: the expensive reasoning happens in *fresh, ephemeral*
sub-agents that read a deterministically-produced preamble and are then discarded;
this conversation only ever holds tiny JSON verdicts, so it never grows into
cache-death-and-re-read cost.

Default goal: `docs/goals/generalized-usage/`. Design: `.context/goal-command-design.md`.

> **Target vs interim.** The dogfooded endpoint is a kitsoki **story**
> (`stories/goal-seeker/`, change WM.0): `/goal` drives its *session* one turn; the
> deterministic steps below are `host.starlark.run` glue; the evaluator is a
> `host.agent.decide` gate; integration reuses `ship-it`; the running status is a
> **slidey report deck** refreshed each cycle (WM.3, summary-level + artifact links).
> Until WM.0 lands, this skill runs the **interim** path over `goal.py` (the golden
> reference oracle the Starlark is tested against). Same contract either way. Any
> kitsoki limitation hit while building the story is appended to the plan as WM.x —
> never worked around outside the engine.

## The invariant (do not violate)

> The evaluator's input is a **bounded projection of current state** (the
> preamble), never an accumulation of history. Work detail lives in the log;
> fetch it with a tool **only if necessary**, following the `detail →` pointers.

Never paste worker output, diffs, traces, or file contents into *this*
conversation. They go to the log; the preamble carries one summary line + a
pointer each.

## One cycle

1. **Rebuild the bounded preamble (deterministic, no LLM).** From repo root:
   ```
   python3 .agents/skills/goal/goal.py ledger   --goal-dir docs/goals/<slug>
   python3 .agents/skills/goal/goal.py preamble --goal-dir docs/goals/<slug>
   ```
   (First run only: `goal.py init --goal-dir docs/goals/<slug>` — it lints the
   decomposition first; a non-zero exit is the gate, fix the manifest before
   proceeding.)

2. **Dispatch a FRESH evaluator sub-agent** (Agent tool, `model: opus`, high
   effort — or a gpt-5.5 codex agent). Its **only** input is the preamble text.
   Its **only** output is the JSON:
   ```json
   {"verdict":"done|not_done|blocked","summary":"…","next_instruction": {…} | [ {…}, … ] | null}
   ```
   It may open a `detail →` pointer with a read-only tool *only if* the summary +
   gate leave it genuinely unsure. It must not restate the preamble. Validate its
   output — the exit code is the gate:
   ```
   python3 .agents/skills/goal/goal.py validate-verdict --goal-dir docs/goals/<slug> --json '<json>'
   ```
   Append the verdict to the log:
   ```
   python3 .agents/skills/goal/goal.py append --work .artifacts/goal/<slug> \
     --entry '{"kind":"verdict","actor":"evaluator:opus","summary":"…","verdict":"…"}'
   ```

3. **If `verdict == done`:** print the JSON and stop. The goal is met (every
   change `integrated` and every wall-gate green).

4. **If not done — dispatch worker(s) for `next_instruction`.** For each item
   (they are guaranteed dep-satisfied and scope-disjoint — that's what
   `goal.py ready` selected):
   - Create the per-change managed workspace off the integration base:
     ```sh
     scripts/dev-workspace.sh create --id gu-<id> \
       --branch stabilize/gu-<id> \
       --base stabilize/generalized-usage \
       --target stabilize/generalized-usage \
       --bootstrap
     scripts/dev-workspace.sh status gu-<id>
     ```
   - Dispatch a **worker** (`kitsoki-mcp-driver` agent, or a tool-limited codex
     agent) with the change's `agent_brief` + `gate`. The worker drives the named
     `pipeline` (bugfix / implementation / docs) **through kitsoki** — dogfood: it
     writes the RED gate first, drives to GREEN, commits on its branch. It returns
     a **one-paragraph structured summary + detail pointers**, nothing more.
   - Append the worker summary, then run an **independent reviewer** (never the
     worker) that re-runs the change's `gate.cmd` and appends a `review_summary`
     with the deterministic `gate.status`.

5. **Integrate (serialized, no-lost-work).** For each `verified` change, first
   commit through `scripts/dev-workspace.sh commit gu-<id> --message "…"`, then
   call `scripts/dev-workspace.sh merge gu-<id> --target
   stabilize/generalized-usage --gate "<gate>" --teardown`. The helper owns the
   target refresh, conflict stop, validation, integration, ancestry proof, and
   teardown. Append `{"kind":"integration","state":"integrated","sha":"…"}`
   only after it succeeds. On conflict, log a `block`, preserve the managed
   workspace, resolve there, rerun the gate, and retry the helper; never discard
   committed or dirty work.

6. **Print the cycle's JSON verdict** and stop. The next `/goal` starts clean from
   the updated log.

## Roles (all communicate ONLY through the log)

- **Evaluator** — opus / gpt-5.5 high, fresh each cycle, reads the preamble, emits JSON.
- **Worker** — kitsoki-mcp-driver / limited codex, one per managed Capsule
  workspace, drives the pipeline, emits a summary.
- **Reviewer** — independent, re-runs the gate, emits a `review_summary`.

## The no-lost-work guarantee (why work can't vanish)

1. Output is a **committed ref** the instant a worker finishes (backstop: recover
   from the trace `final_diff` if an agent dies mid-response).
2. Refs are **preserved until integration is proven** by an ancestry check.
3. Integration is **serialized + gate-verified** (union of affected gates green).
4. Conflicts **re-derive or park-with-ref** — never discard. Worst case is
   parked-with-ref, surfaced as a `next_instruction`.

## Files

- `goal.py` — the deterministic backbone (lint/init/ledger/preamble/ready/append/validate-verdict).
- `docs/goals/<slug>/GOAL.md` — the target (criteria the verdict judges).
- `docs/goals/<slug>/decomposition.yaml` — the changes + gates (the ledger seed).
- `.artifacts/goal/<slug>/` — generated runtime (`log.jsonl`, `ledger.json`, `preamble.md`).
