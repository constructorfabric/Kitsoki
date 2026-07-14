# Decompose → implement workflow

Turn a ticket or an epic/proposal into shipped code. Two tracks, chosen at
the operator's discretion (never a size heuristic):

- **Direct implement** — one ticket (bug or feature/task) drives straight
  through a maker pipeline: [`stories/bugfix/`](../../stories/bugfix/README.md)
  for bugs (see [`fix-a-bug.md`](fix-a-bug.md)) or
  [`stories/implementation/`](../../stories/implementation/README.md) for
  feature/task tickets (`idle → review_task → write_code → test → review →
  handoff`, then `pr-refinement` opens/merges the PR).
- **Decompose** — an epic or a published proposal is too big for one ticket.
  [`stories/deliver/`](../../stories/deliver/README.md) reads the epic,
  writes a **decomposition manifest** (an ordered list of independently
  shippable briefs with a deterministic `gate_command` per brief), runs a
  no-LLM lint plus an adversarial review pass, then fans
  `stories/fleet/` (no README yet — see
  [`stories/deliver/README.md#see-also`](../../stories/deliver/README.md#see-also)
  for its role) — which runs
  [`stories/ship-it/`](../../stories/ship-it/README.md) (maker → integrate →
  verify) per brief behind a merge lock — over every accepted brief.

`deliver` is the decided canonical decomposition story (see
[`stories/deliver/README.md`](../../stories/deliver/README.md) for the full
pipeline, the manifest contract, and its 11-flow no-LLM proof suite). The
[`work-decomposition`](../../.agents/skills/work-decomposition/SKILL.md)
skill is the manual, by-hand twin of the same schema/lint — reach for it
when you want to author a decomposition without a running kitsoki session.
A third, older epic route (`stories/cypilot/`, a full SDLC waterfall) exists
for epic-type tickets picked via `ticket_search`, but `deliver` is the path
this doc and the workflow matrix track.

## Prerequisite

Your repo must be onboarded (see [`../getting-started.md`](../getting-started.md)).
Decompose needs an epic/proposal markdown file to read (e.g. one published
by the [PRD → Design walk](prd-and-design.md)).

## TUI

Reachable from dev-story's `landing` (free-form workbench) two ways —
picking an existing ticket, or right after a design publish:

```
kitsoki run
> tickets                  # landing → ticket_search
> search_tickets open
> pick_ticket TKT-100
> drive                    # routes by ticket_type: bug → bf, feature/task → impl, epic → cyp
```

Or, immediately after a design doc publishes (`design_done`), the operator
chooses **`implement`** (`go_implementation` — straight into `impl` on the
just-filed feature ticket) or **`decompose`** (`go_deliver` — straight into
`deliver.configure` on the just-published proposal, no detour through
`ticket_search`). See
[`stories/deliver/README.md#reachable-from-dev-story-b3`](../../stories/deliver/README.md#reachable-from-dev-story-b3)
for both arcs' exact wiring and world projection.

Inside `deliver` you drive the epic path, then read-through the lint/review
verdicts; a rejected manifest loops back to a budgeted refine, never a
silent stall (`refine_cycle`/`refine_budget`, default budget 3 cycles). See
[`stories/deliver/README.md#rooms`](../../stories/deliver/README.md#rooms)
for the full room table.

Standalone (deliver on its own, no dev-story hub):

```
kitsoki run stories/deliver/app.yaml
> start epic_path=<path-to-epic.md>
```

**Verify the commands above yourself** (no LLM):

```
go run ./cmd/kitsoki test flows stories/deliver/app.yaml --flows 'stories/deliver/flows/*.yaml'
go run ./cmd/kitsoki test flows stories/dev-story/app.yaml --flows 'stories/dev-story/flows/design_to_decompose_to_impl.yaml'
go run ./cmd/kitsoki test flows stories/dev-story/app.yaml --flows 'stories/dev-story/flows/deliver_router_picks_arc.yaml'
```

## Web

**Works, proof thin.** The same dev-story/deliver/fleet orchestrator runs
under `kitsoki web`, and a committed Playwright walk drives the full
decompose chain (design publish → `go_deliver` → lint → review → fleet
fan-out → back to landing) against a `kitsoki web --flow` server:
`tools/runstatus/tests/playwright/deliver-decompose-walk.spec.ts`. One
happy-path proof — the refine/red paths are proven only at the engine
layer so far.

## VS Code

**Works, proof thin.** The extension's e2e suite drives the same walk
through the webview against real VS Code using the `kitsoki.flow` /
`kitsoki.storiesDir` settings:
`tools/vscode-kitsoki/tests/vscode-deliver-decompose-walk.e2e.spec.ts`.
Same caveat as web — one happy-path proof.

## gh-agent

**Gap.** [`internal/ghagent/router.go`](../../internal/ghagent/router.go)
has no epic→`deliver` route. See
[`../architecture/github-agent.md`](../architecture/github-agent.md) for
gh-agent's honesty contract — an unrouted mention gets an honest
"acknowledged — pipeline not yet enabled for this route" stub, never a
fake "done".

## Standing proof status

See the `decompose-implement` row of
[`../testing/dev-workflow-matrix.md`](../testing/dev-workflow-matrix.md).
