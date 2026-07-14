# Scenario QA Story

`scenario-qa` is the operator-facing Persona QA surface. Use it when one
scenario should drive one or more Kitsoki transports with the same deterministic
setup contract. `check` runs automatically create or reuse a managed
clone-backed capsule workspace through `scripts/dev-workspace.sh`; preview runs
stay side-effect-free and do not create a workspace.

```sh
kitsoki run @kitsoki/scenario-qa
```

`@kitsoki/persona-qa` is a story alias that resolves here — use either name.

## Naming

One product, four names:

| Name | What it is |
|---|---|
| **Persona QA** | The product ([`docs/persona-qa.md`](../../docs/persona-qa.md)). |
| **`scenario-qa`** | This story — `kitsoki run @kitsoki/scenario-qa` (alias `@kitsoki/persona-qa`). |
| **`product-journey-qa`** | The broader persona x scenario x 10-repo matrix story ([`../product-journey-qa/README.md`](../product-journey-qa/README.md)). |
| **`product-journey`** | The deterministic runner backend this story drives ([`../../tools/product-journey/README.md`](../../tools/product-journey/README.md)). |
| **`persona_qa`** | A shared support kit, not an operator surface. |

Useful prompts:

- `preview <catalog-scenario-id> across all transports` shows the scenario x
  transport suite without creating a run bundle, launching capture, or calling
  an LLM.
- `check scenario=<catalog-scenario-id> transport=tui,web persona=<persona-id>
  target=<project-id>` creates the run bundle and drives the first transport
  check for a catalog scenario.
- `check whether settings validation persists transport=web`
  drafts an ad-hoc scenario from prose and checks it on the requested
  transport.
- `check bugfix on all transports` (or any multi-transport `check`) drains
  every transport leg automatically and lands straight on the report — no
  per-leg ceremony. Add `pause=each-leg` to opt back into the manual
  ceremony (pause at `recording` after each leg for an explicit `next
  transport`/`next_leg`, e.g. for an MCP caller driving the loop itself with
  `session.submit next_leg`).
- `next transport` (intent `next_leg`) advances to the next transport check.
  With the default `pause=auto` it fires itself; with `pause=each-leg` it is
  how you continue by hand.
- `check bugfix on all transports parallel=true` hands the resolved transport
  legs to arena's persona-qa job type instead of driving them one at a time,
  folding the results into the SAME `report.md` / `deck.slidey.json` shape —
  opt-in, always zero-LLM-spend. See
  [`docs/persona-qa.md`](../../docs/persona-qa.md)'s "Parallel legs" section
  for the full contract and scope note.
- `report` rebuilds `report.md` and `deck.slidey.json` for the current run.
- `main room` returns from the closeout report to the Scenario QA start screen
  while keeping the last run available.

The idle and report rooms also expose a `check_request` free-text input. Treat
the prose as the scenario description, and add `scenario=<id>` only when you
want a catalog scenario instead of an ad-hoc behavior check.

The story writes run artifacts under
`.capsules/workspaces/scenario-qa/.artifacts/product-journey/<run-id>/` by
default, or under the workspace named by `KITSOKI_SCENARIO_QA_WORKSPACE_ID`.
The two review entrypoints are:

- `report.md` for the per-transport verdict table.
- `deck.slidey.json` for the deterministic Slidey deck folded from the recorded
  transport-check results and any captured playback evidence.

The report room shows result counts first and keeps `report.md` in a `kv` row
so web and TUI render it as an openable markdown artifact.

The story owns the product workflow. Python modules under `tools/` remain
implementation adapters for deterministic planning, schema validation,
completion-state conversion, deck generation, and no-LLM tests. Operators should
not need to find `tools/persona_qa` to run Persona QA.
