# Managed development workspaces

Kitsoki development work happens in managed clone-backed capsule workspaces, not
Git linked worktrees. The primary checkout is protected as read-mostly; agents
and operators should delegate workspace lifecycle operations to
`scripts/dev-workspace.sh` instead of running `git clone`, `git worktree`,
rebase, merge, or teardown commands by hand.

The script is the contract. It creates an isolated clone, writes Kitsoki
ownership metadata, optionally bootstraps the clone, commits the finished work,
lands it into the configured local target branch, and removes the workspace when
requested. Normal development starts from `staging/local` and lands back into
`staging/local`, so local stabilization does not move `main`.

## Quick path

From the primary checkout:

```sh
scripts/dev-workspace.sh create --id docs-example --branch agent/docs-example --bootstrap
scripts/dev-workspace.sh status docs-example
```

Do the implementation inside the reported workspace path, then:

```sh
scripts/dev-workspace.sh commit docs-example --message "Document managed workspaces"
scripts/dev-workspace.sh merge docs-example --gate "go test ./internal/host" --teardown
```

For docs-only changes, the merge gate can be a focused committed-diff check
such as `git show --check --format= HEAD` plus any story flow or package test
affected by the edit. Before committing, `git diff --check` is still useful for
checking the working tree. Always prefer the narrowest gate that proves the
changed behavior.

## Workspace layout

By default, workspaces live under:

```text
<repo>/.capsules/workspaces/<id>
```

Each workspace is a local Git clone of the source checkout with the remote named
`source`. Git objects are hardlinked from the source checkout when possible, but
refs, the index, and the working tree are isolated inside the managed capsule
clone. Bootstrap also reuses a shared runstatus `node_modules` cache under
`.capsules/cache`, keyed by `tools/runstatus/pnpm-lock.yaml`, package metadata,
Node ABI, and pnpm version, then symlinks the matching cached tree into the
workspace. The script writes these unmanaged-by-Git metadata files inside the
clone:

| File | Purpose |
|---|---|
| `.kitsoki-capsule` | Sentinel that marks the directory as a Kitsoki capsule workspace. |
| `capsule-manifest.json` | Capsule-compatible provenance: source repo, source commit, workspace id, base, branch, target, and session id. |
| `.kitsoki-clone` | Clone lifecycle metadata consumed by host cleanup/listing code. |
| `.kitsoki-dev-workspace.json` | Script-specific manifest used by `status`, `commit`, `merge`, and `teardown`. |
| `.kitsoki-owner` | Compatibility owner marker when a session id is supplied. |

The script also appends those paths to `.git/info/exclude` in the clone, so the
metadata is local provenance rather than project source.

## Commands

### `create`

```sh
scripts/dev-workspace.sh create --id <id> --branch <branch> [--bootstrap] [--json]
```

`id` is a single path segment and becomes the workspace directory name.
`branch` defaults to `agent/<id>`. `base` and `target` default to
`staging/local`. `--bootstrap` runs `make bootstrap-workspace` inside the clone
after checkout.

Use `--session-id <id>` from story/runtime code so repeated entry by the same
session is idempotent while a different session is refused instead of being
handed another session's workspace.

### `bootstrap`

```sh
scripts/dev-workspace.sh bootstrap <workspace-or-id>
```

Bootstrap prepares embed-only story/SPA assets, installs the runstatus
dependencies, and warms the Go build cache. `create --bootstrap` is the normal
path when a workspace will run `go run ./cmd/kitsoki`, Playwright specs, or web
UI tooling. Pure documentation edits usually do not need it.

If the source checkout has a gitignored `.kitsoki.local.yaml`, bootstrap copies
it into the managed clone before running the Make target. This keeps local
harness/provider profiles available in clone-backed workspaces, including the
long-lived `.capsules/staging/local` staging capsule, without committing the
machine-specific config.

### `status`

```sh
scripts/dev-workspace.sh status <workspace-or-id> [--json]
```

Use `status` to find the path, current branch, short HEAD, and dirty state. For
agent sessions, this is the supported replacement for manually inspecting linked
worktree registries.

### `commit`

```sh
scripts/dev-workspace.sh commit <workspace-or-id> --message "<message>"
```

`commit` stages the full workspace delta and commits it on the workspace branch.
It refuses a clean workspace. Commit messages should describe the completed
slice, not the mechanics of the workspace.

### `merge`

```sh
scripts/dev-workspace.sh merge <workspace-or-id> --gate "<focused validation>" --teardown
```

`merge` refuses dirty workspaces, fetches the current target branch when it
exists, rebases the workspace branch onto that target, runs the optional gate
inside the workspace, imports the branch into the primary checkout as a temporary
`capsule/<id>-land` branch, and fast-forwards the local target ref.

For non-`main` targets such as `staging/local`, the primary checkout can stay on
`main`; the helper updates the target branch ref without touching the primary
working tree.

### `merge-to-main`

```sh
scripts/merge-to-main.sh
```

Final local promotion is staging-capsule first. The default source is the
managed capsule checkout at `.capsules/staging/local` on branch `staging/local`.
The helper refuses unmanaged source directories, prompts for dirty staging-state
recovery when interactive, rebases staging onto local `main`, runs `make test`
in the staging capsule, then fast-forwards protected local `main`. Pass
`--gate "<cmd>"` to use a different gate. Pass `--force` only when an equivalent
gate has already run and you intentionally want to skip the default `make test`.

Create the long-lived staging capsule with:

```sh
scripts/dev-workspace.sh create --root .capsules/staging --id local --branch staging/local --base staging/local --target main --bootstrap
```

For day-to-day staging operations from the primary checkout:

```sh
make test-staging
make web-dev-staging
make site-dev-staging
make install-staging
```

`refresh-staging-local.sh` checks the selected remote main first. If local
`main` is stale, it delegates to `sync-main-from-remote.sh`, prints the required
remote-sync steps, and stops; complete that sync and rerun the refresh. Once
local `main` is current, the helper refreshes `.capsules/staging/local` from
local `staging/local`, rebases it onto local `main`, and imports the refreshed
`staging/local` ref back into the primary checkout. If the staging capsule is
dirty and the helper is attached to a terminal, it asks whether to inspect,
preserve-and-clean, discard-and-clean, or stop. Non-interactive runs stop with
the same next-step commands; pass `--dirty-action preserve` or
`--dirty-action discard` only when that outcome is intentional. The Make targets
call this refresh helper first, then verify that `.capsules/staging/local` is a
managed capsule at the current `staging/local` head before running the
corresponding command inside it. To refresh without running a staging command,
use `make refresh-staging`.

If a workspace merge rebase conflicts, resolve it inside that managed workspace,
rerun the focused validation, then rerun `merge`. If a staging refresh rebase
conflicts, resolve it inside `.capsules/staging/local`, rerun the needed staging
validation, then rerun `refresh-staging-local.sh`. Do not resolve either path by
editing the primary checkout.

### `close` / `teardown`

```sh
scripts/dev-workspace.sh teardown <workspace-or-id>
scripts/dev-workspace.sh teardown <workspace-or-id> --force
```

Teardown refuses unmanaged directories and dirty workspaces. Use `--force` only
when intentionally discarding uncommitted local state.

## Story/runtime integration

The `workspace` host interface still uses the historical provider name
`host.git_worktree`, but default creation is script-backed:

- `workspace.create` and `workspace.clone_create` call
  `scripts/dev-workspace.sh create`.
- New workspaces default to `.capsules/workspaces/<id>`.
- Legacy linked worktree list and cleanup remain so older local checkouts can be
  inspected and removed.
- Bugfix and implementation stories derive session-scoped workspace ids such as
  `bf-<ticket>-<session>` and bind `world.workdir` from the script's returned
  `path`.

The old `worktree_path` world key still exists in some story contracts as a
field name. Treat it as a compatibility field for "the isolated workspace path",
not a direction to create a Git linked worktree.

## Recovery rules

- **Existing workspace, same session:** `create` is idempotent and returns the
  existing path when branch and owner metadata match.
- **Existing workspace, different session:** `create` refuses reuse. Pick a
  distinct id or close the old session/workspace deliberately.
- **Primary checkout has dirt:** normal staging merges update branch refs
  without touching the primary working tree.
- **Workspace is dirty:** `merge` refuses to land. Commit or discard the
  workspace changes first.
- **Target advanced:** `merge` rebases the workspace onto the current local
  target before running the gate.
- **Gate fails:** fix the workspace, recommit or amend there, and rerun `merge`.
- **Workspace no longer needed:** run `teardown`; do not leave merged workspaces
  behind.

## Validation

The lifecycle script has a focused shell regression test:

```sh
scripts/test-dev-workspace.sh
scripts/test-refresh-staging-local.sh
```

Host integration is covered by:

```sh
go test ./internal/host
```

When story provisioning changes, use no-LLM flow tests:

```sh
go run ./cmd/kitsoki test flows stories/bugfix/app.yaml --flows <fixture.yaml>
go run ./cmd/kitsoki test flows stories/dev-story/app.yaml --flows <fixture.yaml>
go run ./cmd/kitsoki test flows stories/implementation/app.yaml --flows <fixture.yaml>
```
