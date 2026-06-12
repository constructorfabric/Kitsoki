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
  ├── imports pr  (../pr-refinement)
  │     entry: open_pr   # skip pr-refinement's standalone-only idle
  │     world_in: ticket_id, workdir, feature_branch, base_branch,
  │               pr_title, pr_body, judge_mode, …
  │     exits:
  │       merged          → main (status: "merged", last_pr_url=pr__pr_url)
  │       abandoned       → main (status: "abandoned")
  │       pushback_resolved → main (Wave 3 reserves; Wave 2 maps to main)
  │
  └── imports prd (../prd)             # the front of the PRD → Design walk
        entry: idle
        world_in: workdir, judge_mode, judge_confidence_threshold
        exits:
          done      → prd_published    # landing room; carries the PRD into design
          abandoned → main (status: "abandoned")
```

The bf → pr handoff is one import edge. When bf fires `@exit:done` the
runtime evaluates dev-story's `imports.bf.exits.done` projection in bf
scope (writing `world.pr_title` / `world.pr_body` in the parent), then
transitions into `pr` — whose compound OnEnter runs the pr `world_in:`
setters in parent scope to project those keys into `pr__<key>` (which
pr's own rooms then reference). The full chain is exercised by
`flows/bugfix_to_pr.yaml`.

## PRD → Design walk

`main → prd → (publish) → prd_published → continue → design` is the
discovery-to-design walk. From `main`, `prd` enters the imported
[`stories/prd/`](../prd/) discovery pipeline (idle → clarifying → brief →
references → drafting). When the operator accepts, prd publishes the PRD
to `docs/prd/<slug>.md` and fires `@exit:done`; dev-story lands in the
**`prd_published`** room ([`rooms/prd_published.yaml`](./rooms/prd_published.yaml)),
which confirms the published path and offers two arcs:

- **`continue`** → the **design** intake, seeding `design_seed_idea` with
  a pointer to the just-published PRD (`"Author a design from the PRD at
  <prd_file>"`) so the design author reads it as prior art.
- **`go_main`** → back to the hub.

`prd_file` is a host **bind** in prd's drafting accept arc (it comes from
`prd_publish.py` stdout), so it commits post-dispatch — too late for a
synchronous exit `set:` projection to carry it (contrast bf → pr, whose
carried `done_artifact` is a synchronous `set:`). The flat world keeps
`prd__prd_file` once the turn settles, so `prd_published` reads
`world.prd__prd_file` directly. prd stays runnable standalone
(`kitsoki run stories/prd/app.yaml`) — the redirect lives only in
dev-story's composition. The walk is exercised by
[`flows/prd_to_design.yaml`](./flows/prd_to_design.yaml).

## Doc profile — targeting an external project

The PRD → Design walk above publishes into kitsoki's own `docs/` by
default, but the *document shape* and *placement* are a **profile** an
instance app can override — no engine or room change needed. An instance
points the same hub at a foreign repo (different doc shape, fixed
filenames, per-scope tree) purely by setting world keys. The worked,
copy-me example is [`stories/gears-rust/`](../gears-rust/), which retargets
[`constructorfabric/gears-rust`](https://github.com/constructorfabric/gears-rust)
and lands gears-sdlc-shaped `PRD.md` / `DESIGN.md` under
`gears/<gear>/docs/`.

The profile is the "External-target profile" world block in
[`app.yaml`](./app.yaml) (search `External-target profile`). Every key has
a default that reproduces kitsoki's own behaviour — **overriding them is
the profile**:

| World key | Default | Effect |
|---|---|---|
| `repo_root` | `""` | external checkout root (forward-compat; ticket passthrough is the deferred gh-adapter slice) |
| `publish_durable_path` | `docs/prd` | PRD publish home (relative to `workdir`); projected into the `prd` import via `world_in`. Per-gear: `gears/<gear>/docs` |
| `prd_doc_filename` | `""` | fixed PRD filename (e.g. `PRD` → `PRD.md`); `""` ⇒ slug-named (`<slug>.md`) |
| `design_template_dir` | `docs/proposals/templates` | dir the design author reads its doc templates from |
| `design_durable_path` | `docs/proposals` | DESIGN publish home (relative to `workdir`). Per-gear: `gears/<gear>/docs` |
| `design_doc_filename` | `""` | fixed DESIGN filename (e.g. `DESIGN` → `DESIGN.md`); `""` ⇒ slug-named |
| `design_ticket_dir` | `issues/features` | where the linking feature ticket is minted; `""` ⇒ **skip** minting (an external target tracks work elsewhere, e.g. GitHub issues) |

How the keys reach the glue: the `prd` import's `world_in` projects
`publish_durable_path` + `prd_doc_filename` into the prd child;
[`rooms/design_draft.yaml`](./rooms/design_draft.yaml) passes the
`design_*` keys to `publish_design.py` and threads `design_template_dir`
into the author prompt (`prompts/design_draft.md` reads
`{{ args.template_dir }}`).

The placement seam is the two publish scripts, which take optional
positional args:

- [`stories/prd/scripts/prd_publish.py`](../prd/scripts/prd_publish.py)
  `… [workdir] [durable] [change_target] [doc_filename]` — `durable` is
  the publish home relative to `workdir`; a non-empty `doc_filename`
  overwrites a **fixed** `<durable>/<doc_filename>.md` instead of
  `<durable>/<slug>.md`.
- [`stories/dev-story/scripts/publish_design.py`](./scripts/publish_design.py)
  `… [workdir] [durable] [doc_filename] [ticket_dir]` — same `workdir` /
  `durable` / `doc_filename` contract, plus `ticket_dir`: a non-empty
  value mints the kitsoki feature ticket there (`issues/features` by
  default); an **empty** `ticket_dir` skips ticket minting entirely.

Per-gear placement is expressed simply as `publish_durable_path:
gears/<gear>/docs` (a plain relative dir) plus the `doc_filename`
override — there is no placement enum. See
[`stories/gears-rust/`](../gears-rust/) for the filled profile, its
scenario, and the two no-LLM flows that assert the resolved paths.

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
| `ticket_search` | Wave 2 | iface.ticket.search; picks a ticket, then `drive` routes by `ticket_type` (bug → bf, feature → impl, epic → cyp). `pick_ticket` reads the type off the picked row; `go_bugfix` forces bf regardless of type. |
| `workspace_manager` | Wave 2 | iface.workspace.list. Minimal Wave 2 shape. |
| `inbox` | Wave 2 | Navigation surface; the runtime's inbox subsystem manages items. |
| `oracle` | Wave 2 | One-shot ask_question via `host.oracle.ask` (agent: `oracle_qa`). |
| `standup` | Wave 2 | Aggregates iface.ticket.list_mine. |
| `design*` | — | **Design pipeline** (formerly the "proposal" pipeline): discovery+brief (one room: the first message mints the workspace + scaffolds an editable brief, then every turn converses + distils it; `ready` runs the quality judge and a passing brief auto-advances) → existing-state → completeness → references → draft → publish (to `docs/proposals/<slug>.md`). **Publish also files a feature ticket** (`issues/features/`) linking back to the design doc, and `design_done`'s `implement` action (the `go_implementation` intent) drives that ticket straight into the impl pipeline (`flows/design_to_implementation.yaml`) — no detour through `ticket_search`. The design pipeline does not create a worktree; `impl.idle.on_enter` self-provisions it on entry (mirroring `bf.idle`), so the impl run gets a real `feature/<ticket>` branch regardless of entry path. Reached ad-hoc via `idea`, or as the back half of the [PRD → Design walk](#prd--design-walk). |
| `prd_published` | — | PRD → Design landing room (see [PRD → Design walk](#prd--design-walk)). |
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
design slug step). `pick N` seeds `world.design_seed_idea` from candidate N
and jumps into the `design` intake — so a blocked author flows straight into
authoring a design doc (slug + workspace minting is reused as-is). `regenerate`
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
| `design_to_implementation.yaml` | The publish → implement bridge: design_done → `go_implementation` → impl.idle (on_enter self-provisions the worktree — the fixture seeds NO workspace) → walk the impl pipeline to @exit:done → main with status="merged". |
| `prd_to_design.yaml` | The PRD → Design walk: main → `go_prd` → walk the imported prd pipeline to @exit:done → land in `prd_published` (prd__prd_file lifted) → `continue` → the `design` intake, seeded with a pointer to the published PRD. |

These are a sample; the full suite (30 / 30) passes under `kitsoki test flows stories/dev-story/app.yaml`.

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

The walkthrough above picks a **bug** and types `go_bugfix`. For a
**feature** ticket (e.g. one filed by the design pipeline), type
`drive` instead of `go_bugfix` after picking — `drive` reads
`ticket_type` and routes into the impl pipeline (`impl.idle`), which
self-provisions a `feature/<ticket>` worktree before the first room
runs. A published design doc can also skip `ticket_search` entirely: from
`design_done`, `implement` drives the freshly-filed feature ticket
straight into impl.

## Demo: PRD → Design (judge_mode=human)

The [PRD → Design walk](#prd--design-walk) replayed by hand. With the
standalone defaults (or via the `kitsoki-dev` instance, which rebinds
providers to local files):

```
$ kitsoki run stories/dev-story/app.yaml
> prd                       # main → prd.idle (discovery chat opens)
> I want a CLI for X         # discovery conversation (prd__discuss)
> prd__start                # distil idea → prd.search (prior-art gate)
> prd__confirm              # no overlap → prd.clarifying (questions posed)
> developers; time-to-first-success   # answer (prd__answer); last answer auto-advances
> prd__confirm              # brief → prd.references
> prd__confirm              # references → prd.drafting (PRD authored)
> prd__accept               # publish docs/prd/<slug>.md → prd_published
> continue                  # → design intake, seeded "Author a design from the PRD at …"
> <describe / refine>        # the design pipeline takes over: search → brief → draft → publish
```

`prd_published` also offers `main` to return to the hub without
designing. The deterministic, no-LLM version of this exact walk is
[`flows/prd_to_design.yaml`](./flows/prd_to_design.yaml).

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
