# goal-seeker — the outer loop of the `/goal` goal-seeker

Drives a **decomposition** (`docs/goals/<slug>/decomposition.yaml` — a list of
"changes", each with a deterministic gate) to **done**, dogfooding kitsoki. It
**composes** two existing stories rather than reinventing them:

- **punch-list** (`../punch-list`) — dispatch drives one change as a single-item
  `punch-list/v1` manifest (load → policy → drive → verify → report → done).
- **ship-it** (`../ship-it`) — integrate lands the change's branch lost-work-safely
  via the `integrate_existing` seam (delivery-tail integrate → re-verify → cleanup →
  shipped).

The expensive orchestrator context stays **bounded**: the deterministic backbone
(ledger fold, bounded preamble, decomposition lint, single-item manifest, log append,
scope-disjoint ready-set) is `host.starlark.run` glue (`scripts/gs_*.star`), **not**
accumulated LLM history. `.agents/skills/goal/goal.py` is the golden reference oracle
those scripts are ported from and tested against.

## The loop

```
start → bootstrap(init) → lint_check → evaluate ──remaining==0──▶ retrospective → @exit:done
                                          │
                                    remaining>0
                                          ▼
   deciding → deciding_gate(decide) → dispatch → redcheck(RED-first)
        ▲                                              │
        │                              ┌───────RED─────┴───not RED───┐
        │                              ▼                             ▼
        │                          punch-list                 park(gate_not_red)
        │                              │ done                        │
        └──── evaluate ◀── record(log integration) ◀── ship-it       │
        └──── evaluate ◀─────────────────────────────────────────────┘
```

The loop is **driven one turn at a time** — WM.0's contract that `/goal` (a thin
Claude-Code driver) advances the goal-seeker *session* one turn per invocation via
`kitsoki turn` / MCP `session.drive`. Each gate room runs its `host.starlark.run` /
`host.agent.decide` step on entry and then **pauses**; the next `advance` routes on
the now-settled result. This is required, not cosmetic — a `host.starlark.run` bind
only settles across a **turn boundary**, never inside a synthetic emit cascade (see
Limitations).

## Determinism guarantees (no-LLM, flow-tested)

These are enforced by the deterministic backbone, never trusted to the evaluator LLM.
Each has an adversarial flow fixture, cross-checked against `goal.py`'s golden verdicts:

1. **Anti-false-`done`** (`flows/anti_false_done.yaml`). `verdict=done` is the
   deterministic fact `remaining==0` from `gs_ledger`, never the decide output. The
   `deciding_gate` room is reached only when `remaining>0`, so a decide that returns
   `done` is *rejected*: the loop dispatches the ledger's `ready_id` and continues.
2. **Reviewer-only-green** (`flows/reviewer_only_green.yaml`). `gs_ledger`'s fold flips
   a change's gate green **only** from a log entry whose `actor` is `reviewer` or
   `integrator`; a worker's self-reported `gate.status=green` is ignored. "green" means
   independently verified.
3. **RED-first** (`flows/red_first_refuses.yaml`, `flows/gate_not_red_parks_and_advances.yaml`).
   Before any work, `redcheck` runs the change's gate on the current tree. A gate that
   already **passes** (exit 0) is not RED — the bug isn't present / the work is
   already done — so the change is **parked** (`park_reason: gate_not_red`, never
   `needs_human`) and the loop advances to the next ready change instead of
   dispatching a no-op. Routing to `needs_human` here would dead-end the loop:
   `needs_human.retry` re-folds the *same* ledger, which deterministically re-derives
   the *same* first ready change forever. A parked change is excluded from
   `ready_id` (`gs_ledger`'s fold, mirrored in `goal.py`'s `PARK_REASONS`) — it is
   **not** a verified completion, it's a triage signal (weak gate or already-done
   work) that a human resolves later.
4. **Structural lint** (`flows/lint_rejects_cycle.yaml`, `flows/lint_rejects_scope_overlap.yaml`).
   `gs_lint` (goal.py `cmd_lint` parity) rejects a dependency cycle or two ready-at-intake
   changes with overlapping scope (plus dup ids, dangling deps, missing gate/acceptance/scope).

## Testing — the gate command wipes `.artifacts/goal/flow-*` first

```
find .artifacts/goal -maxdepth 1 -name 'flow-*' -exec rm -rf {} + &&
go run ./cmd/kitsoki test flows stories/goal-seeker/app.yaml
```

`reset_log: true` is a TEST-FIXTURE-ONLY destructive reset. It truncates
`<work_dir>/log.jsonl`, which discards all append-only history and therefore every
`integrated` fact. To keep hermetic flow fixtures from reading stale derived state,
`gs_init.star` also writes the empty `ledger.json` shape, blanks `preamble.md`, and
neutralizes existing `<work_dir>/manifests/*.json` files with `{}`. Real sessions must
leave `reset_log:false`; targeted unpark/reopen work should append a precise log entry
instead of wiping the run.
**Always wipe before invoking `test flows`** (per-fixture `work_dir`s already live under
`.artifacts/goal/flow-*`, so the `find` cleanup above covers all of them even under zsh
`nomatch`); the two-clean-runs check in this story's plan record exists specifically to
catch a residue-masked flow.

## Importer contract

| Aspect | Value |
|---|---|
| **Entry** (`root`) | `start` (a live run auto-advances into `bootstrap`). |
| **Exits** | `done` (no requires — the goal is complete) · `needs-human` (`requires: [last_error]`). |
| **world_in** | `goal_slug`, `goal_dir` (holds `GOAL.md` + `decomposition.yaml`), `work_dir` (holds `log.jsonl` + `ledger.json`), `reset_log` (truncate the log for a fresh run), `base_branch`, `main_worktree_path`. |
| **Hosts** | `host.starlark.run`, `host.agent.decide`, `host.run`. Imported children carry their own host allow-lists (`inherit` mode). |
| **Agents** | `evaluator` — the bounded-context decide gate (advisory ordering only). |

### Scripts (`scripts/gs_*.star`, each with a `.star.yaml` sidecar)

| Script | Role (goal.py analogue) |
|---|---|
| `gs_init` | ensure/reset `log.jsonl` (`init`) |
| `gs_lint` | structural gate (`cmd_lint`) |
| `gs_ledger` | fold log → ledger + `remaining` + `ready_id` (`fold_ledger`, `ready_set`) with reviewer-only-green |
| `gs_preamble` | bounded projection (`build_preamble`) |
| `gs_manifest` | single-item `punch-list/v1` manifest for a change |
| `gs_append` | append one structured log entry (`append_entry`) |
| `report_deck` | deterministic PM report/deck hierarchy from `ledger.json` + `log.jsonl` |

## Report artifacts

From `evaluate` or `retrospective`, use `report` to rebuild the deterministic PM
summary artifacts under `<work_dir>/reports/`:

- `exec-summary.md`
- `exec-summary.slidey.json`
- `changes/<change_id>.md`
- `changes/<change_id>.slidey.json`

The report room re-folds the ledger with `gs_ledger` and then projects only
`ledger.json`, `log.jsonl`, and `GOAL.md`. It does not call an LLM, render a deck, or
inspect conversational history.

## Limitations (surfaced by building this — for the plan, per the "limitations → plan" rule)

Building this composed loop exposed several kitsoki gaps. Per the stories/ rule they are
**reported, not hacked around**; where a legitimate composition tool let the story proceed
it was used and flagged. These belong on the WM.1–WM.x list.

1. **punch-list is not import-safe.** Its `@exit:done` (`requires: report_path`) and
   `@exit:needs-human` (`requires: last_error`) transitions don't re-pin those keys in
   the exit arc's own effects, so the loader's import requires-check
   (`internal/app/imports.go:840`) rejects the import. It passes *standalone* because
   that check only runs on the import path. **Worked around** from the parent with
   `imports.punchlist.overrides.states` re-pinning `report_path`/`last_error` (a
   sanctioned import feature). **Proper fix:** punch-list should adopt the import-safe
   re-pin discipline (`delivery-tail/report.yaml`, `bugfix/done`, `cherny/committing`)
   on those exit arcs; then delete the override block.

2. **`host.starlark.run` binds don't settle inside a synthetic emit cascade.** A value
   bound by `gs_*` (e.g. `remaining`, `manifest_path`, `lint_route`, `red_exit`) is
   **not** visible to a guard / `emit_intent` / later-invoke input **in the same
   `on_enter` or the same emit cascade** — only across a real turn boundary. (`host.
   agent.decide`/`host.run` binds behave the same for *routing*; only a synchronous
   `set:` value or a prior-turn value is reliably in-cascade.) This forced the whole loop
   to be **turn-paced** (each gate pauses), and forced `manifest_path` to be a synchronous
   `set:` from the deterministic path formula rather than the `gs_manifest` bind. Not a
   bug per se, but an unintuitive contract worth documenting/naming (WM territory).

3. **Driving punch-list's INTERNAL board loop cannot be flow-tested here.** Two reasons:
   (a) flow `host_handlers` replace a whole handler, so `host.starlark.run` can't be
   stubbed for punch-list's own scripts while the goal-seeker's `gs_*.star` run for real
   — and `gs_ledger` *must* run real for the deterministic `remaining` 1→0 sequencing
   (flow stubs are static and can't return not_done-then-done); (b) running punch-list's
   scripts for real trips a latent **async-bind marking bug in punch-list's `verify`
   room** (`verify_status` is cleared then bound async, so the `verify_done` emit sets
   `_record_status` from a pre-bind empty value → `punch_board` never marks the item),
   which punch-list's own flows mask by stubbing `punch_board` with canned outputs. So
   `flows/loop.yaml` proves the deterministic spine **through the punch-list entry seam**
   (`punchlist.load` with the manifest loaded), and `flows/ship_and_close.yaml` proves the
   ship-it half + deterministic done + retrospective; the punch-list internal drive is
   punch-list's own tested concern. **Proper fix:** punch-list's `verify` room needs the
   same turn-boundary / re-pin discipline used here.

4. **The flow harness's init behavior is inconsistent.** Whether a story's initial-state
   `on_enter` runs (and whether the first intent "bubbles" through the init cascade)
   varies between the single-`--flows` runner and the whole-suite runner, and with which
   `host_handlers` are present. The `start` landing room + a `look`-settling first turn
   are the robust accommodations. Worth stabilizing.
