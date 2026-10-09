# Model task engineering

This story wraps the repeatable model-performance loop used to harden provider
tasks such as the GLM-5.2 deliver decomposer.

The story is intentionally offline. In **single-boundary** mode it scores an existing trace with
`kitsoki agent-bench score`, writes JSON, Markdown, and Slidey report artifacts,
and records the paths in world state. Live provider calls happen outside this
story through the bench manifest's `run` command and remain gated by
`agent-bench run --live`.

For **campaign optimization**, Arena owns planning, arming, immutable attempt
receipts, resumption, and offline comparison. Use a `task-optimization/v1`
study with a resolved lock and separate learning/confirmation splits, then
review JSON, Markdown, and `.slidey.json` artifacts under
`.artifacts/task-optimization/`. This story never launches provider cells: a
campaign needs explicit operator authorization and its separate live gate.

## Workflow

1. For a single boundary, configure a bench manifest, optional case id, optional
   trace override, and output directory, then run `score`.
2. For a campaign, run `campaign review`. It asks Arena to materialize
   `plan.json` and `plan.md`, then renders `plan-deck.slidey.json` from that
   canonical plan. This is deterministic and no-spend.
3. `campaign arm` is a distinct operator intent. It passes `--live` to Arena,
   which also requires the study-specific live-gate environment variable; the
   resulting receipt records authorization only and `provider_dispatched: false`.
4. Review the generated evidence:
   - `*-report.json` for automation.
   - `*-report.md` for human review and issue filing.
   - `*-deck.slidey.json` for shareable status decks.
5. Accept with `done` once the artifacts explain the outcome.

## Testing

The flow fixture runs the Starlark artifact-path derivation and stubs
`host.run`, so it does not call a provider and does not execute the score
command:

```sh
go run ./cmd/kitsoki test flows stories/model-task-engineering/app.yaml
```
