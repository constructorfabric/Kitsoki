# Managed development workspaces

Kitsoki development work happens in managed clone-backed Capsule workspaces, not
Git linked worktrees. The primary checkout is protected as read-mostly; agents
and operators should delegate lifecycle operations to
`scripts/dev-workspace.sh` instead of running `git clone`, `git worktree`,
rebase, merge, or teardown by hand.

The script creates an isolated clone, writes Kitsoki ownership metadata,
bootstraps the clone when requested, commits finished work, lands it into the
configured local target branch, and removes it when requested. Normal
development starts from `staging/local` and lands back into `staging/local`, so
local stabilization does not move `main`. The native Capsule workspace CLI is
the general-project product surface described in
[`guide/development/capsule-ci.md`](guide/development/capsule-ci.md); Kitsoki's
own protected checkout keeps this script as its contributor-facing lifecycle.

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
scripts/dev-workspace.sh create --id <id> --branch <branch> --bootstrap
```

`id` is a single path segment and becomes the workspace directory name. The
default base and target are `staging/local`; pass the task's `agent/<id>` branch
explicitly and use `--bootstrap` whenever the workspace will run Kitsoki or its
browser tooling.

Use `--session-id <id>` from story/runtime code so repeated entry by the same
session is idempotent while a different session is refused instead of being
handed another session's workspace.

### `bootstrap`

Pass `--bootstrap` to `scripts/dev-workspace.sh create`; the script runs
`make bootstrap-workspace` in the new clone before returning. Custom Capsule CI
definitions should record the equivalent setup as a declared environment hook.

Bootstrap prepares embed-only story/SPA assets, installs the runstatus
dependencies, and warms the Go build cache. In Capsule CI this is also declared
in `.kitsoki/environments/ci.yaml` as `bootstrap.command: bootstrap-workspace`
with project-scoped `go-build` and `runstatus-node-modules` cache grants, so
story CI, remote workers, and local managed workspaces share the same setup
contract. `create --bootstrap` is the normal path when a workspace will run
`go run ./cmd/kitsoki`, Playwright specs, or web UI tooling. Pure documentation
edits usually do not need it.

If the source checkout has a gitignored `.kitsoki.local.yaml`, bootstrap copies
it into the managed clone before running the Make target. This keeps local
harness/provider profiles available in clone-backed workspaces, including the
long-lived `.capsules/staging/local` staging capsule, without committing the
machine-specific config.

### `status`

```sh
scripts/dev-workspace.sh status <workspace-id> [--json]
```

Use `status` to find the path, current branch, short HEAD, and dirty state. For
agent sessions, this is the supported replacement for manually inspecting linked
worktree registries. While a create is still cloning, `status` reports
`state: initializing` (or `"state": "initializing"` with `--json`) and exits
non-zero rather than misclassifying the incomplete directory as unmanaged.
Wait for the owner PID to finish; do not run a second create or teardown.

### `commit`

```sh
scripts/dev-workspace.sh commit <workspace-id> --message "<message>"
```

`commit` stages the full workspace delta and commits it on the workspace branch.
It refuses a clean workspace, adds the repository-required DCO sign-off, and
refreshes the workspace manifest's recorded HEAD to the resulting commit.
Commit messages should describe the completed slice, not the mechanics of the
workspace.

### `merge`

```sh
scripts/dev-workspace.sh merge <workspace-id> --gate "<focused validation>" --teardown
```

`merge` refuses dirty workspaces, fetches the current target branch when it
exists, rebases the workspace branch onto that target, runs the optional gate
inside the workspace, imports the branch into the primary checkout as a temporary
`capsule/<id>-land` branch, and fast-forwards the local target ref.

Before rebasing, the helper verifies that every object reachable from the
freshly fetched target is readable inside the capsule. It repeats the
clean-checkout/object verification after rebase, after the gate, and for the
temporary landing ref before updating the target. A gate is validation-only:
if it moves the captured workspace commit, the merge stops. The helper imports
that immutable commit rather than the moving branch, rechecks the branch after
import, and updates the target with the fetched target commit as its
compare-and-swap expectation. If the workspace branch advances after landing,
requested teardown keeps the workspace unless the target contains that newer
tip. Before rebase it constructs the expected transplanted tree independently;
a flattened merge or other rebase result that loses resolution-only content is
rejected, and the original commit is retained under a content-addressed primary
recovery ref. Untracked and ignored paths that an incoming tree would overwrite
are reported before rebase. This makes a stale or broken clone object store,
committed branch writer, or target race fail without deleting or overwriting
work. Once the target update succeeds,
the command reports a successful merge even if best-effort temporary-ref cleanup
or requested teardown has a problem; those follow-up failures are warnings, not
a false report that the landing failed.

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
Source-directory gates are validation-only and cannot move the captured
commit. The pre-gate source commit is copied to a content-addressed
`refs/kitsoki/promotion-source-recovery/<commit>` ref in the primary repository.
Changed-path protection is NUL-safe and includes both sides of renames; a merge
refusal restores only paths proven clean before the attempt, never the whole
checkout with `reset --hard`. Ignored preimages are included, and permission
guard changes never follow symlinks outside the repository. Source rebases use
the same independent expected-tree and ignored-collision checks as workspace
rebases.

Create the long-lived staging capsule with:

```sh
scripts/dev-workspace.sh create --root .capsules/staging --id local \
  --branch staging/local --base staging/local --target main --bootstrap
```

For day-to-day staging operations from the primary checkout:

```sh
make test-staging
make web-dev-staging
make site-dev-staging
make install-staging
```

When a workspace is merged to `staging/local` without an explicit `--gate`,
`dev-workspace.sh` automatically runs `make capsule-ci-quick` when the target
repository declares that target. The gate is deterministic and no-spend: it
checks diff hygiene, validates the Capsule CI story, replays its flow fixtures,
and runs focused short Go tests. Use `--gate` to replace it with a narrower or
broader intentional gate.

After a successful `staging/local` landing, the helper also refreshes the
managed `.capsules/staging/local` checkout when it exists. If that refresh
fails, the landing is reported as failed rather than silently leaving the
long-lived staging capsule stale. Staging consumers still refresh defensively
before running.

`refresh-staging-local.sh` checks the selected remote main first. If local
`main` is stale, it delegates to `sync-main-from-remote.sh`, prints the required
remote-sync steps, and stops; complete that sync and rerun the refresh. Once
local `main` is current, the helper snapshots local `staging/local`, refreshes
`.capsules/staging/local` from that exact snapshot, rebases it onto local
`main`, and imports the refreshed ref with a compare-and-swap update. If a
workspace merge advances primary `staging/local` during the refresh, the helper
refuses rather than overwriting the newer work; rerun refresh from that newer
snapshot. Before it mutates the capsule, the helper copies both the original
capsule branch tip and its old `source/staging/local` tracking tip into
content-addressed `refs/kitsoki/staging-capsule-recovery/<commit>` refs in the
primary repository. Those durable, idempotent refs survive a successful
refresh and preserve capsule-only history even if the long-lived capsule is
later replaced. Failed runs retain the original/post-rebase/candidate refs that
carry diagnostic state; ephemeral captured-input refs are removed by exit
cleanup. If failure happens after the candidate was copied into the primary
repository, that immutable import ref is retained there too.
The expected post-rebase tree is constructed independently. If sequential
replay produces a different tree, refresh replaces that replay with a signed
reconciliation merge whose first parent is the captured destination, whose
second parent is the exact pre-refresh staging snapshot, and whose tree is the
independently proven three-way result. This preserves resolution-only content
and the complete original history instead of accepting the changed replay.
The final primary update verifies captured `main` and `staging/local`
atomically. If the
staging capsule is dirty and the helper is attached to a terminal, it asks
whether to inspect,
move the work into a new committed managed recovery capsule (the default),
preserve-and-clean, discard-and-clean, or stop. Non-interactive runs stop with
the same next-step commands; pass `--dirty-action move`,
`--dirty-action preserve`, or `--dirty-action discard` only when that outcome
is intentional. `move` captures the working tree and index as Git objects plus
the complete non-ignored untracked archive, materializes a signed recovery
commit, and anchors both snapshots under content-addressed
`refs/kitsoki/dirty-*` refs in the primary repository. It rechecks the source,
atomically renames the whole dirty checkout to a managed
`closed-recovered-*` quarantine, creates and bootstraps a clean staging capsule
at the original path when the repository exposes a bootstrap target, then
seals the quarantine as a signed commit. This preserves ignored evidence and
prevents a late writer from racing a reset or clean. Capsule cleanup recognizes
the sealed quarantine only while its exact HEAD/tree and all recovery refs
remain valid. The move path does not use configurable diff output as a recovery
transport. `preserve` stashes tracked and non-ignored untracked work without a
pathspec and anchors the exact stash commit (including index and untracked
parents) under `refs/kitsoki/dirty-snapshot/<commit>` in the primary repository;
ignored capsule-management sentinels and evidence remain in place throughout.
The Make targets
call this refresh helper first, then verify that `.capsules/staging/local` is a
managed capsule at the current `staging/local` head before running the
corresponding command inside it. To refresh without running a staging command,
use `make refresh-staging`.

If a workspace merge rebase conflicts, resolve it inside that managed workspace,
rerun the focused validation, then rerun `merge`. If a staging refresh rebase
conflicts, resolve it inside `.capsules/staging/local`, rerun the needed staging
validation, then rerun `refresh-staging-local.sh`. Do not resolve either path by
editing the primary checkout.

Teardown anchors the exact committed tip in
`refs/kitsoki/workspace-teardown-recovery/<commit>`, preserves ignored
`.context`, `.artifacts`, and local configuration under
`.artifacts/workspace-close/`, then atomically renames the managed checkout to
a same-filesystem `closed-*` managed quarantine. Preservation copies only
ignored/untracked review files; tracked files under those roots stay in the Git
checkout. The branch, manifests, complete source issue tree, and original
evidence stay intact in quarantine.
The normal Capsule cleanup plan discovers that quarantine as a legacy managed
workspace and applies the same clean, merged, inactive, and age guards. Only
cleanup apply invokes the provider's `--purge-quarantine` path. The provider
atomically moves the path to a visible managed purge state, repeats exact
branch/HEAD/status checks, re-preserves review artifacts and the complete
ignored tree, archives complete Git metadata plus a verified pack of every
object not reachable from a primary repository ref, proves inactivity both
before and after preservation,
and only then removes it and its teardown recovery ref. Stashes, secondary
refs, reflog-only commits, and unreachable objects therefore remain
reconstructible even though they do not make `git status` dirty.

### Hygiene

Long-running Capsule CI and development loops accumulate local evidence and
cache state. Inspect reclaimable Capsule-managed state before or after large
work blocks:

```sh
kitsoki capsule cleanup plan --keep-runs 20
```

The default plan inventories managed development and staging workspaces as well
as Capsule CI evidence. It keeps the five newest clean terminal workspaces,
requires older workspaces to be at least 24 hours old, keeps the newest 20 CI
run records, and proposes only terminal run/receipt/trace bundles beyond that
retention. The default safety inventory avoids a slow recursive workspace byte
walk; it reports the number of unmeasured candidates plus filesystem
capacity/free bytes and whether free space is below the configured floor. Add
`--measure-workspace-bytes` when a deliberate deeper accounting pass is useful.
That plan reports measured inventory/reclaimable bytes and projected free bytes.
Its `bytes_basis` field records that workspace estimates exclude
`.git/objects`: clone-backed object databases are shared data, so counting every
hardlink would overstate what deleting one workspace can actually reclaim.

Workspace cleanup is deliberately conservative. Active, dirty, current,
pinned, unmanaged, or otherwise unknown workspaces are inventory entries with
`safe: false`; cleanup never treats them as reclaimable. Both native and legacy
workspaces require a known-zero process-activity probe, and apply repeats that
probe immediately before provider-owned close. Pin a workspace for an
investigation either with `--pin-workspace <id>`, a
`.kitsoki-capsule-pin` file inside the workspace, or a
`.capsules/workspace-pins/<id>` marker in the project. Tune the guards without
bypassing them. Long-lived workspaces under `.capsules/staging` are implicitly
pinned; managed `closed-*` quarantines there remain eligible for normal
retention cleanup.

```sh
kitsoki capsule cleanup plan --keep-workspaces 10 --workspace-min-age 72h --min-free-bytes 21474836480
```

`cleanup apply` rebuilds the plan and then rechecks each workspace's generation,
lifecycle state, owner, pin/current status, and Git cleanliness immediately
before asking its provider to close it. A workspace that changes during that
window is reported under `skipped`, not deleted. Add explicit cache flags only
when cache pressure matters:

The same inventory generically discovers direct, sentinel-owned Capsule control
projects under `.capsules/projects/`; no tool name is hardcoded. A valid
`.kitsoki-capsule-project` must declare schema `capsule-project/v1`, its kind,
`managed_by`, and the canonical parent project. Cleanup records a provenance digest,
rejects symlink/escaping/malformed roots, and rechecks that provenance plus
current-directory, pin, process-activity, initialization, workspace-record, and
age guards before deletion. Child workspaces close through their native manager
first. The now-empty project root remains protected by recent workspace-record
activity and becomes reclaimable on a later pass; invalid provenance is
reported as unsafe for investigation rather than deleted.

Workspaces created by the compatibility script before the native instance index
existed are not permanent disk debt. Cleanup recognizes them as `legacy: true`
only when `.kitsoki-capsule`, `.kitsoki-clone`,
`.kitsoki-dev-workspace.json`, and `capsule-manifest.json` all agree on the
project, workspace root, id, branch, and target. It then proves the checkout is
clean, its exact HEAD is contained in the declared target branch, no
initialization marker exists, and no process has an open file or working
directory anywhere under the workspace. The default activity proof uses
`lsof`; if that probe is unavailable or inconclusive, activity is `unknown`
and the candidate remains unsafe. Apply repeats every proof and uses
the compatibility provider's `teardown` command, including its issue-preserving
guard, instead of deleting the directory directly. The provider atomically
moves a quarantine to a visible managed `closed-purging-*` state, archives its
complete ignored tree and Git repository recovery state, and repeats the
process-activity proof both before preservation and immediately before
deletion. The activity probe must
produce no process IDs, no diagnostic output, and only a recognized no-match
status. An interrupted purge therefore remains discoverable by the next plan,
and an unavailable or inconclusive activity probe fails closed. A
`closed-recovered-*` quarantine is the one exception to target containment: its
signed seal manifest must prove exact content-addressed primary `dirty-snapshot`,
`dirty-recovery`, and `recovered-quarantine` refs plus unchanged HEAD/tree.
Missing or changed proof makes the candidate unsafe.

For routine reclamation of completed agent capsules, use the parameterless
command below. It removes only clean workspaces with a lossless branch proof:
legacy workspaces must have their exact HEAD contained in a local or fetched
remote branch, while native workspaces must be recorded as integrated. It keeps
no count-based retention, never removes CI evidence or caches, and requires a
full five-minute cooloff since the latest workspace activity before both
planning and provider-owned teardown. Current, pinned, initializing, dirty, or
process-active capsules remain untouched.

```sh
kitsoki capsule cleanup clear
```

Inspect durable recovery state without moving any branch:

```sh
git for-each-ref --format='%(refname) %(objectname)' refs/kitsoki/dirty-snapshot/ refs/kitsoki/dirty-recovery/ refs/kitsoki/staging-capsule-recovery/
git show refs/kitsoki/dirty-recovery/<commit>
git fsck --connectivity-only --no-dangling refs/kitsoki/dirty-recovery/<commit>
```

Cleanup removes ordinary teardown refs after target containment is proven.
Content-addressed recovered-quarantine tips, canonical dirty refs, and
staging-capsule recovery refs are retained deliberately so archived Git
metadata remains reconstructible even after reflog expiry and object pruning;
do not delete them ad hoc.

```sh
kitsoki capsule cleanup plan --include-capsule-cache --include-go-build-cache
kitsoki capsule cleanup apply --include-capsule-cache --include-go-build-cache
```

Go build cache cleanup intentionally uses `go clean -cache -testcache` because
the active cache can live outside the project root. This keeps disk hygiene in
the Capsule lifecycle without letting agents delete arbitrary host paths.
Concurrent Go builds can race while cache entries disappear; cleanup retries
those recognized races and reports persistent concurrent cleanup under
`tolerated` instead of turning an otherwise successful hygiene pass red.

### `close` / `teardown`

```sh
scripts/dev-workspace.sh teardown <workspace-id>
```

Teardown refuses unmanaged directories and dirty workspaces. A successful close
moves the checkout to a managed `closed-*` quarantine; it does not immediately
delete the only remaining checkout or branch. Capsule cleanup later purges that
quarantine after its independent safety and retention checks. If requested
teardown fails after a successful merge, the merge result remains successful
and carries an explicit cleanup warning so automation does not retry the
already-landed change as though landing failed.

### `recover`

```sh
scripts/dev-workspace.sh recover <workspace-or-id>
scripts/dev-workspace.sh recover <workspace-or-id> --discard-incomplete
```

Creation claims `<root>/.initializing/<id>` atomically before cloning. The
marker records the owning PID and intended target without writing anything into
the target itself, so concurrent lifecycle calls can distinguish initialization
from an unmanaged workspace. A live marker is never recoverable. A stale marker
is explicit recovery work: `recover` removes it when the target is already a
valid managed workspace or absent. If an interrupted clone remains, recovery
first refuses; pass `--discard-incomplete` to remove only that marker-matched
unmanaged Git clone and then release the stale marker. It never deletes an
unexpected non-workspace directory.

## Story/runtime integration

The forward host interface for new story-owned workspaces is
`host.capsule_workspace`. It accepts a workspace `id` and a checked-in Capsule
`definition`, then delegates source materialization, branch policy, bootstrap,
commit, and close semantics to the Capsule manager. Use it when the story can
express its workspace lifecycle through `.kitsoki/capsules/<definition>.yaml`.

The historical `workspace` host binding still uses `host.git_worktree` in older
stories. That provider is now a compatibility surface:

- `workspace.create` and `workspace.clone_create` use the `development`
  compatibility provider where the historical protected lifecycle is required.
- New workspaces default to `.capsules/workspaces/<id>`.
- Legacy linked worktree list and cleanup remain so older local checkouts can be
  inspected and removed.
- Bugfix and implementation stories derive session-scoped workspace ids such as
  `bf-<ticket>-<session>` and bind `world.workdir` from the returned `path`.

Do not migrate a story binding from `host.git_worktree` to
`host.capsule_workspace` until the story's `host_interfaces.workspace` contract
has moved from dynamic `name` / `base` / `sync` arguments to checked-in
definition ids and commit/close operations.

The old `worktree_path` world key still exists in some story contracts as a
field name. Treat it as a compatibility field for "the isolated workspace path",
not a direction to create a Git linked worktree.

## Recovery rules

- **Existing workspace, same session:** `create` is idempotent and returns the
  existing path when branch and owner metadata match.
- **Existing workspace, different session:** `create` refuses reuse. Pick a
  distinct id or close the old session/workspace deliberately.
- **Workspace is initializing:** `status` identifies the live or stale
  initialization marker, and all mutating lifecycle commands refuse it. Wait
  for the live owner. For an interrupted owner, use `recover`; use
  `--discard-incomplete` only after confirming its marker-matched clone can be
  discarded.
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
