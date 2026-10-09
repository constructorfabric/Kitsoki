# Scenario Product Platform story

This story is the first story-backed implementation slice for
`.context/scenario-product-platform-proposal.md`.

It does **not** replace `stories/scenario-qa/` or `stories/product-journey-qa/`.
Instead, it proves the normalized platform layer above those runners in a
small no-LLM vertical:

1. import `features/complete-product-tour.yaml`;
2. import one `tools/product-journey/scenarios.json` scenario and persona;
3. emit a `scenario-pack/v1` bundle with targets, semantic refs, evidence
   contracts, and report settings;
4. synthesize a deterministic web/TUI/VS Code run bundle where VS Code is
   honestly marked bridge-level/degraded;
5. write `report.md`, `deck.slidey.json`, `completion-state.json`, Release
   Readiness, Product Learning, a semantic-resolution manifest, an Arena fake
   rollup, and a neutral generated site page.

All work is deterministic Starlark glue via `host.starlark.run`; automated flows
use no live LLM and no network.

## Entry

`ready` is the root state. Use the `build` intent to generate the bundle.

Useful slots:

- `feature`: defaults to `features/complete-product-tour.yaml`
- `scenario`: defaults to `product-discovery`
- `persona`: defaults to `core-maintainer`
- `targets`: comma-separated targets or `all` (default)
- `run_id`: stable artifact directory name
- `output_root`: artifact root, default `.artifacts/scenario-product-platform`

## Validation

```sh
go run ./cmd/kitsoki validate stories/scenario-product-platform/app.yaml
go run ./cmd/kitsoki test flows stories/scenario-product-platform/app.yaml --flows 'stories/scenario-product-platform/flows/build_complete_product_tour.yaml' --v
```

To exercise the real Starlark artifact writer (the flow stubs the writer to stay
side-effect-light), run:

```sh
rm -rf .artifacts/scenario-product-platform-smoke
go run ./cmd/kitsoki turn stories/scenario-product-platform/app.yaml \
  --state ready \
  --intent build \
  --slots '{"run_id":"validation-smoke","output_root":".artifacts/scenario-product-platform-smoke"}'
```
