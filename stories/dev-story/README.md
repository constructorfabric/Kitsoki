# dev-story — engineer's-day hub

Wave 2 / Phase 2 of the dev-story / bugfix unify proposal (§5.1). The
GENERAL-PURPOSE app that imports `stories/bugfix/` and
`stories/pr-refinement/` and routes between them via day-level rooms
(main, inbox, ticket_search, workspace_manager, standup, oracle,
code_review, deploy, observability, incident, docs).

This app does **not** bind providers. Concrete bindings happen at the
INSTANCE level: `stories/kitsoki-dev/` (Wave 3) for local-file
providers; `cyber-repo/stories/devstory/` (Phase 7) for Jira /
Bitbucket / Jenkins.

Standalone:

```
kitsoki run stories/dev-story/app.yaml
```

The defaults in `host_interfaces:` (host.local_files.ticket, host.git,
host.local, host.git_worktree, host.append_to_file) make standalone
runs work for smoke testing without an instance wrapper.

## Composition

```
dev-story
  ├── imports bf  (../bugfix)
  │     entry: idle
  │     world_in: ticket_id, ticket_title, workdir, feature_branch,
  │               base_branch, judge_mode, …
  │     exits:
  │       done    → pr (with pr_title / pr_body lifted from
  │                     bf__done_artifact.summary_{title,markdown})
  │       abandoned → main (status: "abandoned")
  │
  └── imports pr  (../pr-refinement)
        entry: open_pr   # skip pr-refinement's standalone-only idle
        world_in: ticket_id, workdir, feature_branch, base_branch,
                  pr_title, pr_body, judge_mode, …
        exits:
          merged          → main (status: "merged", last_pr_url=pr__pr_url)
          abandoned       → main (status: "abandoned")
          pushback_resolved → main (Wave 3 reserves; Wave 2 maps to main)
```

The bf → pr handoff is one import edge. When bf fires `@exit:done` the
runtime evaluates dev-story's `imports.bf.exits.done` projection in bf
scope (writing `world.pr_title` / `world.pr_body` in the parent), then
transitions into `pr` — whose compound OnEnter runs the pr `world_in:`
setters in parent scope to project those keys into `pr__<key>` (which
pr's own rooms then reference). The full chain is exercised by
`flows/bugfix_to_pr.yaml`.

## Provider neutrality

The legacy `testdata/apps/dev-story/` stub had Jira-flavoured world
keys (`jira_query`, `jira_results`) and called `host.run` with hard-
coded `echo` commands. dev-story (this app) strips those:

| Legacy | Provider-neutral |
|---|---|
| `world.jira_query` | `world.ticket_query` |
| `world.jira_results` | `world.ticket_results` |
| `host.run` (echo) | `iface.ticket.search` / `iface.ticket.list_mine` |

The cyber-repo flavour rebinds `iface.ticket` to `host.jira`; kitsoki-
dev rebinds to `host.local_files.ticket`. Same YAML, two providers.

## Rooms

| Room | Status | Notes |
|---|---|---|
| `main` | Wave 2 | Landing / navigation. Dispatches to bf / pr / day rooms. |
| `ticket_search` | Wave 2 | iface.ticket.search; picks a ticket; dispatches into bf. |
| `workspace_manager` | Wave 2 | iface.workspace.list. Minimal Wave 2 shape. |
| `inbox` | Wave 2 | Navigation surface; the runtime's inbox subsystem manages items. |
| `oracle` | Wave 2 | One-shot ask_question via `host.oracle.ask` (agent: `oracle_qa`). |
| `standup` | Wave 2 | Aggregates iface.ticket.list_mine. |
| `proposal*` | — | Proposal-authoring pipeline: discovery+brief (one room: the first message mints the workspace + scaffolds an editable brief, then every turn converses + distils it; `ready` runs the quality judge and a passing brief auto-advances) → existing-state → completeness → references → draft → publish. |
| `ideas` | — | Ideas-backlog reviewer (see below). |
| `code_review` | Wave 3 stub | Reserves the room; imports `stories/code-review/` in Wave 3. |
| `deploy`, `observability`, `incident`, `docs` | Wave 3 stubs | Routing-back-to-main placeholders. |

### Ideas reviewer (`ideas`)

Reached from `main` via `ideas`. Reconciles the hand-maintained ideas backlog
(`world.ideas_path`, default repo-root `ideas.md`, with `## Done` /
`## Partial / in progress` / `## Ideas` sections) against work that has actually
shipped. `on_enter` runs the read-only `ideas_reviewer` agent against the repo
root — it reads the backlog, the commit history (`git log`), and the docs
(especially `docs/proposals/`) and proposes section **moves**, each backed by
concrete evidence, plus a few high-value **candidates** worth proposing next.

The decide is interpretation; the mutation is deterministic. `apply` is a
confirm gate: it hands the persisted report to `scripts/ideas_reconcile.py`,
which rewrites the backlog file (the same decide→script discipline as the
proposal slug step). `pick N` seeds `world.proposal_seed_idea` from candidate N
and jumps into the `proposal` intake — so a blocked author flows straight into
authoring a proposal (slug + workspace minting is reused as-is). `regenerate`
re-scans the rewritten backlog.

## Intent surface

Day-level intents live in this app's `intents:` block. Importing
overlapping bare names from bf and pr is impossible (the loader
rejects collisions); the operator types prefixed forms (`bf__accept`,
`pr__proceed`) when inside an imported sub-story. Imported bare-name
intents in Wave 2:

| From | Lifted to bare name |
|---|---|
| `bf` | `start` |
| `pr` | `open`, `monitor`, `retry`, `resolve`, `merge_now` |

The parent declares additional navigation / pipeline-launching
intents at the bare name: `go_main`, `go_back`, `go_inbox`, `go_oracle`,
`go_ticket_search`, `go_workspace_manager`, `go_standup`,
`go_code_review`, `go_deploy`, `go_observability`, `go_incident`,
`go_docs`, `go_bugfix`, `go_pr_refinement`, `search_tickets`,
`pick_ticket`, `ask_question`, `summarize_day`, `proceed`, `quit`,
`look`.

## Flows

| Flow | Coverage |
|---|---|
| `main_smoke.yaml` | Boot, land in main, render view. Smallest possible smoke. |
| `ticket_search_smoke.yaml` | main → ticket_search → run search → pick → return. |
| `pickup_to_bugfix.yaml` | Same as above, then dispatch into the bf import (lands in bf.idle with world_in: projections firing). |
| `bugfix_to_pr.yaml` | The full closed-loop walk: main → bf.idle → walk every bf room to @exit:done → handoff into pr → walk pr to @exit:merged → land back in main with status="merged" and last_pr_url populated. |

All 4 / 4 pass under `kitsoki test flows`.

## Manual TUI walkthrough

The same chain `bugfix_to_pr.yaml` exercises is replayable by hand.
With `judge_mode=human` and the standalone defaults:

```
$ kitsoki run stories/dev-story/app.yaml
> tickets                  # main → ticket_search
> search_tickets open      # → ticket_searching → ticket_search
> pick_ticket TKT-100      # ticket_id / thread populated
> go_bugfix                # → bf.idle
> bf__start                # → bf.reproducing_executing
> bf__proceed              # → bf.reproducing_awaiting_reply
> bf__accept               # → bf.proposing_executing
> bf__proceed              # → bf.proposing_awaiting_reply
> bf__accept               # → bf.implementing_executing
> bf__proceed              # → bf.testing_executing
> bf__proceed              # → bf.testing_awaiting_reply
> bf__accept               # → bf.reviewing_executing
> bf__proceed              # → bf.validating_executing
> bf__proceed              # → bf.validating_awaiting_reply
> bf__accept               # → bf.done_executing
> bf__proceed              # → bf.done_awaiting_reply
> bf__accept               # bf @exit:done → pr.open_pr
> pr__proceed              # → pr.ci_monitoring (CI poll happens in on_enter)
> pr__proceed              # → pr.merge_executing
> pr__proceed              # → pr.merge_awaiting_reply
> pr__accept               # pr @exit:merged → main (status="merged")
```

In Wave 3 the kitsoki-dev instance rebinds the providers and the same
20-turn walk-through writes real diffs / opens a real PR / merges
on github.com.

## Oracle-split persona table (Phase 8)

The dev-story hub's own oracle room makes prose Q&A calls. The
`oracle_qa` agent is declared in `app.yaml agents:` and carries
`bash_profile: read-only` (no mutations).

| Persona | Verb | Room |
|---|---|---|
| `oracle_qa` | `ask` | `oracle_asking` — one-shot prose Q&A answer |

`ask` is the oracle-split verb for read-only, prose-output inspection.
It is distinct from `decide` (which requires a JSON schema and emits a
structured verdict) and `task` (which may write files). The oracle
persona has `tools: [Read, Grep, Glob]` — codebase inspection without
side effects.

Note: imported sub-stories (`stories/implementation/`,
`stories/code-review/`) were migrated to the new oracle verbs in Phase 9.
Flow fixtures that exercise those imports carry `host.oracle.decide:` and
`host.oracle.ask:` stubs alongside the Phase 8 stubs.

## See also

- [`docs/case-studies/bug-fix.md`](../../docs/case-studies/bug-fix.md)
  — the design.
- [`docs/proposals/notes/dev-story-implementation-contract.md`](../../docs/proposals/notes/dev-story-implementation-contract.md)
  — Wave 1 + Wave 2 contracts.
- [`docs/stories/imports.md`](../../docs/stories/imports.md) — imports authoring
  reference.
- [`stories/bugfix/`](../bugfix/), [`stories/pr-refinement/`](../pr-refinement/)
  — the imported sub-stories.
- [`stories/oregon-trail/`](../oregon-trail/) — three-layer composition
  demo (the pattern this hub mirrors).
- [`testdata/apps/dev-story/`](../../testdata/apps/dev-story/) — the
  legacy Jira-flavoured stub. Retained for now to keep existing
  loader / metamode / flow tests passing; retired in Wave 3 once no
  test references it.
