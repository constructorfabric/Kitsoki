# `deliver` — the canonical decomposition story

`deliver` is the epic front door of the [delivery loop](delivery-loop.md): hand
it a path to an accepted epic or proposal and it decomposes it into
independently-shippable briefs, lints the manifest deterministically, runs it
past an adversarial reviewer, and fans [`stories/fleet/`](https://github.com/bsacrobatix/Kitsoki/tree/main/stories/fleet)
(which runs [`stories/ship-it/`](https://github.com/bsacrobatix/Kitsoki/tree/main/stories/ship-it) per brief
behind a merge lock) over the result. It absorbed the
`.agents/skills/work-decomposition/` skill's richer manifest schema, refine
loop, and adversarial review discipline, and is reachable directly from
[`stories/dev-story/`](https://github.com/bsacrobatix/Kitsoki/tree/main/stories/dev-story) as the
decompose-vs-direct sibling of the plain `impl` pipeline. *Audience: story
authors extending the decomposition chain, and operators deciding whether to
decompose or implement directly.*

## Story graph

```
configure {epic_path}
  └─ start ─▶ decompose (router: prior decomposition on disk?)
               ├─ fresh ──▶ decomposing (decomposer agent writes the manifest)
               │              ├─ ok ──▶ lint
               │              └─ host error ─▶ decompose_error ─▶ @exit:needs-human
               └─ redecompose (a prior manifest exists) ──▶ redecompose
                      (decomposer authors an ADDITIVE DELTA, never an overwrite)
                        └─▶ redecompose_apply (decompose-update transaction)
                              ├─ ok ──▶ lint
                              └─ fail ─▶ redecompose_error ─▶ @exit:needs-human

lint ├─ pass ─▶ review (adversarial: buildability + coverage_note)
     │           ├─ accept ─▶ fleet (import — fans ship-it per brief)
     │           │             └─ @exit:done {delivery_summary}
     │           └─ revise ─▶ decompose (budgeted refine)
     └─ fail ─▶ decompose (budgeted refine)

any refine-budget exhaustion ─▶ @exit:needs-human {last_error}
```

`lint` and `review` share ONE `refine_cycle`/`refine_budget` counter (default
3 cycles) — one budget for the whole decompose↔lint↔review ring, not one per
gate. The full room-by-room contract, world schema, and manifest field
reference live in [`stories/deliver/README.md`](../../stories/deliver/README.md)
(what ships **today** — keep that as the source of truth; this page is the
narrative overview).

## The manifest contract, briefly

A decomposition is `briefs:` — an ordered list of `{id, brief, gate_command,
deps}` (required) plus optional richer fields absorbed from the
work-decomposition skill: `title`, `kind`, `scope[]`, `acceptance[]`, `risk`,
and a top-level `coverage_note`. The deterministic Starlark lint checks
structure (unique ids, non-empty fields, acyclic deps, bounded scope globs);
the `review` room's adversarial LLM pass checks what the lint cannot —
buildability and whether `coverage_note` actually covers the epic. See
[`stories/deliver/README.md#the-manifest-contract`](../../stories/deliver/README.md#the-manifest-contract)
for the full field list.

## Reachable from `dev-story`

[`stories/dev-story/`](../../stories/dev-story/README.md) imports `deliver`
(alias `deliver`, entry `configure`) and offers it as the
**decompose-vs-direct** sibling to the `impl` import on `design_done` (right
after a proposal publish) and `landing` (picking up a proposal published
earlier) — an operator choice between `implement` (`go_implementation` →
straight into `impl`) and `decompose` (`go_deliver` → `deliver.configure`),
never a size heuristic. Both arcs land back on `@exit:done` /
`@exit:needs-human` → `landing`. See
[`stories/dev-story/flows/design_to_decompose_to_impl.yaml`](../../stories/dev-story/flows/design_to_decompose_to_impl.yaml)
for the full publish → decompose → review → fleet fan-out walk, and
[`stories/dev-story/flows/deliver_router_picks_arc.yaml`](../../stories/dev-story/flows/deliver_router_picks_arc.yaml)
for both arcs routing correctly from the same published proposal.

## Proof per surface

The decompose-vs-direct chain is proven, no LLM, on every surface that drives
a live conversation:

| Surface | Proof |
|---|---|
| Engine / TUI | `go run ./cmd/kitsoki test flows stories/dev-story/app.yaml --flows 'flows/design_to_decompose_to_impl.yaml'` (and `stories/deliver`'s own 11-flow suite — see its README) |
| Web | [`tools/runstatus/tests/playwright/deliver-decompose-walk.spec.ts`](../../tools/runstatus/tests/playwright/deliver-decompose-walk.spec.ts) — a real `kitsoki web --flow` server, the same fixture, driven through `intent-btn-*` clicks |
| VS Code | [`tools/vscode-kitsoki/tests/vscode-deliver-decompose-walk.e2e.spec.ts`](../../tools/vscode-kitsoki/tests/vscode-deliver-decompose-walk.e2e.spec.ts) — the same fixture through the extension's chat webview |
| gh-agent | deferred (epic-labeled issue → deliver) — not yet built |

Both the web and VS Code proofs need **`--mode one-shot`**
(`kitsoki.mode: "one-shot"` in VS Code settings): `deliver`'s
decompose → lint → review chain auto-advances through synthetic emit/decision
gates with no operator-facing button at any one hop, so the default `staged`
posture (gated human-in-the-loop review at every internal gate) stalls at the
first one. See `internal/testrunner/flows.go`'s execution-modes doc for the
staged/one-shot distinction, which is the same for `kitsoki test flows`
(default one-shot) and `kitsoki web`/the VS Code extension (default staged).

Driving these flow fixtures live (not just through the flow-test harness)
also required `kitsoki web`/the extension to honor a fixture's
`starlark_inspect_cassette` — the fs/probe replay seam that lets a `host.
starlark.run` script (like `lint`'s) run for REAL against a faked filesystem,
instead of a real disk read for a decomposition manifest the stubbed decomposer
agent never actually wrote. `cmd/kitsoki/runtime.go` now wires it the same way
`internal/testrunner/flows.go` always has for `kitsoki test flows`.

## Validation

```sh
go run ./cmd/kitsoki test flows stories/deliver/app.yaml
go run ./cmd/kitsoki test flows stories/dev-story/app.yaml --flows 'flows/design_to_decompose_to_impl.yaml'
cd tools/runstatus && pnpm exec playwright test deliver-decompose-walk --project=chromium
cd tools/vscode-kitsoki && KITSOKI_VSCODE_PACE=0 pnpm exec playwright test vscode-deliver-decompose-walk
```

## See also

- [`stories/deliver/README.md`](https://github.com/bsacrobatix/Kitsoki/tree/main/stories/deliver) — the full
  room-by-room reference: every room's contract, the complete world schema,
  the manifest field list, and the flow-fixture table.
- [`delivery-loop.md`](delivery-loop.md) — how `deliver` fits with `fleet` and
  `ship-it` in the shipped delivery stack.
- [`stories/decompose-update/README.md`](https://github.com/bsacrobatix/Kitsoki/tree/main/stories/decompose-update) —
  the standalone review-then-apply demo of the transaction `deliver`'s
  `redecompose_apply` room wraps directly.
- [`.agents/skills/work-decomposition/SKILL.md`](https://github.com/bsacrobatix/Kitsoki/tree/main/.agents/skills/work-decomposition) —
  the manual twin of this pipeline for by-hand runs; its schema is kept
  identical to `deliver`'s rather than left to drift.
