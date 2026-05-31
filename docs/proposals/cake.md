end-to-end integration test. all inputs mockable for stable runs. needs a demo project with bugs / features / epics under a parent repo with frontend + backend submodules. artifacts land in `.artifacts/`.

# the test

four flow fixtures, all `test_kind: flow` (see `docs/tracing/testing.md` §1):

```
stories/dev-story/flows/
  cake_router_picks_each_type.yaml   — main → tickets → pick {bug,feature,epic} → expect bf / impl / cyp
  cake_bugfix_walk.yaml              — full bugfix → pr → main (status: merged)
  cake_feature_walk.yaml             — full impl → pr → main (status: merged)
  cake_epic_walk.yaml                — cypilot idle → … → code → (optional) pr → main
```

# the demo project

```
testdata/projects/cake/
  .gitmodules                — pins for frontend/ + backend/
  frontend/                  — submodule (trivial notes/tasks UI)
  backend/                   — submodule (trivial notes/tasks API)
  issues/
    bugs/<iso>-<slug>.md     — bug-format-proposal.md §2 frontmatter (supported today)
    features/<iso>-<slug>.md — same frontmatter, new source dir
    epics/<iso>-<slug>.md    — same frontmatter, new source dir
  .artifacts/                — gitignored; checkpoints write here
  README.md
```

deterministic ISO-timestamped IDs so test assertions match exact strings. one of each ticket type at the seed.

submodules ship for **manual repro** only. flow fixtures stub `host.git` and `host.git_worktree`, so `workdir: "testdata/projects/cake"` is just a string. real submodule ops under test would need a new `host.git submodule_update` op + git on PATH — defer.

# scenario walk

| sketch line | maps to |
|---|---|
| user opens main room | `initial_state: main`; `main.on_enter` calls `iface.ticket.list_mine` |
| enters tickets | `intent: go_ticket_search` (or input `"tickets"` via recording); `ticket_search.on_enter` calls `iface.ticket.search` |
| sees a list with bugs / features / epics | view renders `world.ticket_results`; assert `expect_view_matches:` regex |
| picks a bug → bugfix pipeline | `pick_ticket` sets `ticket_type=bug`; `drive` arc routes to `bf` |
| picks a feature → impl pipeline | `pick_ticket` sets `ticket_type=feature`; `drive` arc routes to `impl` |
| picks an epic → cypilot pipeline | `pick_ticket` sets `ticket_type=epic`; `drive` arc routes to `cyp` (gap — see below) |

## bugfix pipeline

`bf.idle → reproducing → proposing → implementing → testing → reviewing → validating → done → @exit:done`. then `pr.open_pr → ci_monitoring → merge_executing → merge_awaiting_reply → @exit:merged` → back to `main` with `status: "merged"`.

reproduction phase (`bf.reproducing_executing`) calls `host.oracle.ask_with_mcp` with `prompts/reproducing_executing.md` and binds `world.reproduction_artifact` from the envelope's `submitted:` blob (schema: `stories/bugfix/schemas/reproducing_artifact.json`).

- **environment check** (frontend/backend running, dataset ready): add as optional `environment_check:` field on the reproduction artifact schema. stubbed oracle envelope sets the fields to `true`. no new room.
- **default env vs prompt**: seed `initial_world.workdir` for the "configured" path; ship a second variant (`cake_bugfix_walk_prompts_for_workspace.yaml`) that omits `workdir` and asserts the run routes through `workspace_manager` first.
- **tests + evidence**: assert `reproduction_artifact.tests_added` non-empty and `evidence` has at least one key (build/api/ui).
- **continue**: kitsoki calls it `proceed`. add `"continue"` as a synonym in `stories/bugfix/app.yaml` `proceed.examples`.

canonical walk to clone: `stories/dev-story/flows/bugfix_to_pr.yaml`.

## feature pipeline

`impl.idle → review_task → write_code → test → review → handoff → impl.pr.* → @exit:done`. no reproduction phase by design (`stories/implementation/app.yaml:18-22`). canonical walk to clone: `stories/implementation/flows/happy_human.yaml`.

environment: seed `workdir`; skip `workspace_manager`. `iface.ci.run_tests` inside `test_executing` is the implicit local-env probe.

## cypilot pipeline

`cyp.idle → prd → adr → design → decomposition → feature×N → code → @exit:code_ready`. environment is not relevant — cypilot declares 5 ifaces (`artifact`, `vcs`, `ci`, `transport`, `inbox.add`) and explicitly does **not** declare `workspace` or `ticket` (`stories/cypilot/README.md:100-103`).

each `_executing` calls `iface.artifact.create` (→ `cpt generate`); each `_awaiting_reply` calls `iface.artifact.validate` (→ `cpt analyze`). `feature_count` comes from `decomposition_artifact.phase_count`; the feature room walks N times, then routes to `code_executing`.

stub `host.cypilot_artifacts` with one envelope satisfying every op (`phase_count: 1` keeps the loop short). set `judge_mode: human` for deterministic step-through. canonical walk to clone: `stories/cypilot/flows/handoff_to_pr.yaml`.

# mocks must be expectation-based

a stub that returns a canned envelope tells us nothing about whether the room actually called it. passing fixture + never invoked `iface.vcs.branch` = false positive. every host stub pairs with **call-verification assertions**: who got called, how many times, with what args.

runner already has the pieces:

- `HostInvoked` (pre-bind args) and `HostDispatched` (post-rerender args the handler saw) events fire per call. always assert against `HostDispatched`.
- `expect_events:` (subsequence) and `expect_events_exact:` (exact list) work per turn. partial-map matching on `effect:`.

```yaml
- intent: { name: proceed }
  expect_state: bf.reproducing_awaiting_reply
  expect_events:
    - kind: HostDispatched
      effect:
        handler: iface.transport.post
        args: { thread: "TKT-200", phase_id: "reproducing_TKT-200_0" }
    - kind: HostDispatched
      effect:
        handler: host.inbox.add
        args: { kind: checkpoint, state: reproducing_awaiting_reply }
```

worth adding to the runner for fixture readability:

- `expect_host_calls:` — turn-level shorthand expanding to `HostDispatched` events.
- `expect_no_host_calls:` — turn or fixture level. fails if the named handlers ever fire.
- `HostStub.by_op:` — different envelopes per op (search vs get vs list_mine) under one handler name. needed to validate that rooms read the right fields from the right op.

## per-fixture call pins

**router fixture**: `iface.ticket.list_mine` 1×, `iface.ticket.search` 1× per `tickets` keystroke. `expect_no_host_calls:` for `iface.vcs.*`, `iface.ci.*`, `host.oracle.ask_with_mcp`.

**bugfix walk**:
- `iface.workspace.{create,sync}` 1× each (seeded-workdir variant: 0×)
- `iface.vcs.branch` 1× with `name: "fix/<ticket-id>"`, `base: "main"`
- `host.oracle.ask_with_mcp` 1× per `_executing` (×5) + 1× per `_awaiting_reply` when `judge_mode != human`
- `iface.transport.post` + `host.inbox.add` 1× per `_awaiting_reply` (×5)
- `iface.vcs.{commit,push,open_pr}` 1× each in `pr.open_pr`
- `iface.vcs.pr_status` ≥1× in `pr.ci_monitoring`
- `iface.vcs.merge` 1× with `strategy: "squash"`
- `iface.ticket.transition` 1× with `to: "resolved"` after merge — **not wired today**, see checklist

**feature walk**: same shape, no reproducing calls; `iface.ticket.get` 1× at `impl.review_task_executing`.

**epic walk**:
- `iface.artifact.create` 1× per room, `kind:` matches room name
- `iface.artifact.validate` 1× per `_awaiting_reply`
- `iface.artifact.decompose` 1× at `decomposition_executing`
- `iface.ci.run_tests` 1× at `code_executing`

**all fixtures**: `expect_no_host_calls:` for handlers that should never fire (`host.jira_comment`, `host.github`, and the off-pipeline cypilot/bugfix handler in the wrong fixture).

# `.artifacts/` folder

today every `_awaiting_reply` posts via `iface.transport.post` with `thread: "{{ world.thread }}"` → kitsoki-dev binds that to `host.append_to_file` writing into the bug file. for cake we want artifact bodies at `testdata/projects/cake/.artifacts/<phase>_<ticket>_<cycle>.md`.

cleanest path: new `host.artifacts_dir` transport binding that interprets `thread:` as a filename under a configurable `artifacts_root`. cake fixture's app sets `host_bindings.transport: host.artifacts_dir`.

assertion needs a fixture-level `expect_files:` hook (regex on path → regex on content). simpler than capturing transport writes in memory and generalises to other test outputs.

# gaps to close first

ordered bottom-up (provider + transport + runner first, then YAML wiring, then fixtures):

1. `internal/host/localfiles_ticket.go` — scan `issues/{bugs,features,epics}/`; set `type` on each row from source dir
2. `internal/host/` — new `host.artifacts_dir` transport handler
3. `internal/testrunner/` — add `expect_files:`, `expect_host_calls:`, `expect_no_host_calls:`, `HostStub.by_op:`
4. `stories/dev-story/rooms/ticket_search.yaml` — `pick_ticket` reads `ticket_type` from the picked row, not hard-coded
5. `stories/dev-story/app.yaml` — add `imports.cyp` (mirror `bf`/`impl` shape); lift cyp intents
6. `stories/dev-story/rooms/main.yaml` — add `ticket_type == 'epic' → cyp` arm to `drive`; add `go_cypilot` escape hatch
7. `stories/bugfix/schemas/reproducing_artifact.json` — optional `environment_check` field
8. `stories/bugfix/app.yaml` — `"continue"` synonym on `proceed`
9. `stories/bugfix/rooms/done.yaml` — wire `iface.ticket.transition to: "resolved"`
10. `testdata/projects/cake/` — seed the demo project (one bug + one feature + one epic, README, gitignored `.artifacts/`)
11. four `stories/dev-story/flows/cake_*.yaml` fixtures
