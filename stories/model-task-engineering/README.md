# Model task engineering

This story wraps the repeatable model-performance loop used to harden provider
tasks such as the GLM-5.2 deliver decomposer.

The story is intentionally offline. It scores an existing trace with
`kitsoki agent-bench score`, writes JSON, Markdown, and Slidey report artifacts,
and records the paths in world state. Live provider calls happen outside this
story through the bench manifest's `run` command and remain gated by
`agent-bench run --live`.

## Workflow

1. Configure a bench manifest, optional case id, optional trace override, and
   output directory.
2. Run `score`.
3. Review the generated evidence:
   - `*-report.json` for automation.
   - `*-report.md` for human review and issue filing.
   - `*-deck.slidey.json` for shareable status decks.
4. Accept with `done` once the artifacts explain the outcome.

## Testing

The flow fixture stubs `host.run`, so it does not call a provider and does not
execute the shell command:

```sh
go run ./cmd/kitsoki test flows stories/model-task-engineering/app.yaml
```
