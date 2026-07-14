# task-bakeoff — deprecated orchestration stub

> **Deprecated.** This story predates the native Capsule evaluation adapter and
> refers to retired `bakeoff.yaml` / `prepare.sh` / `run_cell.sh` machinery.
> Do not use it for CI, workspace lifecycle, or new comparisons. Use Capsule CI
> for project validation and the `matrix-task-comparison` skill with Arena as
> the canonical planner/scheduler, plus `tools/bugfix-bakeoff/external` for
> project/oracle adaptation.
> It remains temporarily for replay/history compatibility only.

> Sibling: for an EXTERNAL repo (onboard a third-party project + fix real bugs), see [`stories/repo-bakeoff`](../repo-bakeoff/README.md) (wraps `tools/bugfix-bakeoff/external`).

A replay-compatible historical story for the former matrix-bakeoff workflow.
It is not an Arena adapter and must not be extended into a second campaign
engine. New studies use `task-optimization/v1` plus
`tools/arena/arena.py task-optimization` directly; their canonical review
surface is Arena-produced JSON, Markdown, and a `.slidey.json` source deck.

```
kitsoki run stories/task-bakeoff/app.yaml
```

## What it wraps

- **Arena owns** study planning, profile/treatment preflight, immutable attempt
  receipts, resumption, scoring inputs, and review-deck sources.
- **The external adapter** at `tools/bugfix-bakeoff/external` owns project and
  hidden-oracle adaptation only. It is not a scheduler.
- **This story owns no current campaign behavior.** Its baked deck/flows remain
  solely so old replay references retain a readable historical surface.

## Rooms

```
idle ──start──▶ configure ──accept──▶ running ──accept──▶ scoring ──(auto)──▶
   reporting ──accept──▶ slideshow ──(auto render)──▶ done ──accept──▶ @exit:done
```

| Room | Split | What it does |
|---|---|---|
| `idle` | deterministic | Park. `start` boots the bake-off; `quit` → `@exit:abandoned`. |
| `configure` | deterministic | Declare the matrix (the three axes, echoed from the harness manifest) and compute the authoritative `cells_total`. |
| `running` | historical stub | Tracks an old supplied roster only; it never dispatches cells. |
| `scoring` | historical stub | Reads pre-existing legacy cell data for replay only. |
| `reporting` | historical stub | Builds its baked compatibility deck only. |
| `slideshow` | deprecated compatibility | Existing replay-only HTML rendering. New studies emit a `.slidey.json` source deck from Arena and do not use this room. |
| `done` | gallery | `media(deck_handle)` + the headline rollup. `accept` → `@exit:done` (requires `deck_handle` — a real rendered report exists). |

## Honesty: what is real vs. stubbed

Per `stories/AGENTS.md` (never paper-over):

- **`running` is a thin orchestration stub by design.** The matrix-comparison
  method is explicit that `run_cell.sh` is the only cost-bearing piece and is run
  **by hand**, never automatically (the AGENTS.md no-LLM rule). This story drives
  the *free, deterministic* half end-to-end (configure → aggregate → report →
  render) and tracks the cell roster; it does **not** fabricate cell results. In
  no-LLM mode the cell results are the harness's already-committed `cells/*.json`.
- **The render path is real.** `slideshow` is a faithful copy of `slidey-edit`'s
  render room; under `kitsoki web --flow` `host.artifacts_dir` runs for real so
  the media handle resolves through the journal. `baked/deck.json` is a real
  3-scene slidey deck so the report renders without the harness running live.

## Deterministic, no-LLM testing

```
kitsoki test flows stories/task-bakeoff/app.yaml
```

| Fixture | Covers |
|---|---|
| `flows/happy_path.yaml` | idle → configure → running → scoring → reporting → slideshow → done → `@exit:done`. All host calls stubbed by invoke id; render points at `baked/`. |
| `flows/quit_at_configure.yaml` | `quit` at configure → `@exit:abandoned`. |

## Exits

| Exit | `requires:` | When |
|---|---|---|
| `done` | `deck_handle` | A real rendered slidey report deck exists. |
| `abandoned` | — | `quit` at idle / configure. |

## Migration

Do not implement the old “remaining work” here. The equivalent, supported
workflow is:

```sh
python3 tools/arena/arena.py task-optimization preflight \
  --study tools/arena/specs/bugfix-task-optimization-v1.yaml \
  --out .artifacts/task-optimization/bugfix-codeact-v1
python3 tools/arena/arena.py task-optimization plan \
  --study tools/arena/specs/bugfix-task-optimization-v1.yaml \
  --out .artifacts/task-optimization/bugfix-codeact-v1
```

Both commands are no-provider. A real campaign remains double-gated by explicit
operator authorization and `KITSOKI_TASK_OPT_LIVE`; do not retrofit that work
into this compatibility story.
