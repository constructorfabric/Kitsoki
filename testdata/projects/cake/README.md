# cake — kitsoki end-to-end demo project

A tiny "notes + tasks" web application split across a frontend submodule
and a backend submodule. Used as the working tree for the cake flow
fixtures (`stories/dev-story/flows/cake_*.yaml`), which walk a bug, a
feature and an epic ticket through their respective pipelines (bugfix,
implementation, cypilot) using only stubbed host calls — no real network
or git operations.

```
testdata/projects/cake/
  .gitmodules                pins for frontend/ and backend/ (placeholder)
  frontend/                  trivial notes/tasks UI (submodule placeholder)
  backend/                   trivial notes/tasks API (submodule placeholder)
  issues/
    bugs/B-2026-05-18-list-broken.md
    features/F-2026-05-18-export-csv.md
    epics/E-2026-05-18-notes-platform.md
  .artifacts/                gitignored; host.artifacts_dir writes here
  README.md
```

## Why this lives in `testdata/`

The full demo project is fixture-only: the submodule pins are
placeholders so the flow fixtures' stubs (which never invoke the real
git CLI) can run anywhere on disk. A future "manual repro" mode that
walks the same flows against real frontend / backend submodules + a
running stack would need a `host.git submodule_update` op + git on
PATH — deferred; today the fixtures stand on their own.

## Running the fixtures

```sh
kitsoki test flows stories/dev-story/app.yaml \
    --flows "stories/dev-story/flows/cake_*.yaml"
```

Each fixture seeds `world.workdir = "testdata/projects/cake"`, stubs
every host iface, and asserts on:

  - per-turn `expect_state` / `expect_world` / `expect_host_calls`
  - fixture-level `expect_files:` against `.artifacts/`
  - fixture-level `expect_no_host_calls:` for handlers that must never
    fire during this pipeline's walk
