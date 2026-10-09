# git-ops — Interactive Git Workflow

A guided, deterministic git workflow story: staging, commit (with agent-authored
message), rebase, squash-merge, worktree lifecycle, protected-main sync/publish,
and conflict resolution.

The agent appears in exactly two cases: authoring a commit message and resolving
a rebase/merge conflict. All other git operations are deterministic `host.run` shell calls.

> **No auto-fetch:** Rebase targets the **local** integration ref. If your integration
> branch tracks a remote, run `pull` on main first — the stale-rebase guard at
> `merge_into_main` will detect if the remote has advanced since your last rebase.
> (Remote fetch/push is a v2 ergonomics item; v1 is fully local.)

## Entry

```
kitsoki run stories/git-ops/app.yaml
```

Optionally set `working_dir` to your repository root and `integration_branch`
if you use a branch other than `main`:

```
kitsoki run stories/git-ops/app.yaml \
  --world '{"working_dir":"/path/to/repo","integration_branch":"main"}'
```

## Exits

None — git-ops is a standalone hub story with a terminal `done` state.

## World contract (operator-settable at launch)

| Key | Type | Default | Description |
|---|---|---|---|
| `working_dir` | string | `.` | Repository root for all git operations. |
| `integration_branch` | string | `main` | The long-lived branch to rebase and merge onto. |
| `build_check_cmd` | string | `go build ./... && go test ./...` | Post-merge / post-conflict build gate. |
| `build_check_disabled` | bool | `false` | Skip the build gate entirely. |
| `refactor_mode` | bool | `false` | Signals the agent to use `refactor` commit type. |

## Hub routing

On entry, `idle` detects the current branch and worktrees, then routes:
- Integration branch → `main_ops`
- Feature branch → `branch_ops`

## Operations

### Command hub (`intercept`)

A branch-agnostic room that groups every command below into one place, each arc
delegating to its command room (no duplicated git logic). It exists for the
**pre-LLM intercept gate** (`kitsoki intercept --room intercept`): a stateless,
no-LLM one-shot classifies a natural phrasing ("rebase this onto main") and runs
the command directly. The flagship `stories/dev-story/` surfaces the same room by
importing git-ops. See
[prompt-intercept.md §6](../../docs/architecture/prompt-intercept.md#6-worked-example--git-ops-command-hub).

### Feature branch (`branch_ops`)

| Intent | Description |
|---|---|
| `rebase` | Rebase onto integration branch. Sets `rebase_done=true` on success. Auto-creates a backup tag for branches with >1 commit. |
| `merge_into_main` | Merge into integration branch (requires `rebase_done=true`). Runs three guards before merging. |
| `squash` | Squash all branch commits into one agent-authored commit. |
| `stage` | Classify and stage changes. Flags suspicious files. |
| `commit` | Agent-authored conventional commit message. |
| `undo` | Undo last commit (`--mixed`, `--soft`, or confirmed `--hard`). |
| `worktree_list` | List and classify existing worktrees. |
| `cleanup` | Remove worktree and branch. |

### `rebase` - feature branch sync

Use `rebase` from `branch_ops` or the `intercept` command hub to replace direct
rebasing-helper recipes. It delegates to `scripts/rebase-current-branch.sh`,
rebases the current branch onto the local `integration_branch`, creates a
pre-rebase backup tag when the branch is more than one commit ahead, and tries
`git rerere` before routing unresolved conflicts to the conflict room. It does
not fetch or pull; use `sync_main remote=origin` or `pull` on the integration
branch first when remote freshness is required.

### Integration branch (`main_ops`)

| Intent | Description |
|---|---|
| `pull` | `git pull --rebase`. On conflict, routes to `conflict`. |
| `sync_main` | Reconcile protected local `main` with a remote `main` through an integration worktree. |
| `push_main` | Fast-forward `origin/main` (or another remote main) from protected local `main`, never force-push. |
| `merge_branch` | Merge a named branch with same guards as `merge_into_main`. |
| `worktree_create` | Create a new linked worktree under `.worktrees/`. |
| `worktree_list` | List and classify existing worktrees. |
| `cleanup` | Remove the **named** worktree and branch (operand-as-slot). |
| `integrate` | Lost-work-safe: rebase a branch onto **current** main, build-check, merge. |
| `stage` | Classify and stage changes. |
| `commit` | Agent-authored conventional commit message. |
| `undo` | Undo last commit. |

`merge_branch branch=<name>` also accepts the branch as a slot and skips the
picker when given.

### `sync_main` - protected-main remote sync

Use `sync_main remote=origin` from `main_ops` or the `intercept` command hub to
replace the direct helper-script recipe for reconciling guarded local main with
a remote main. If the first pass stops for conflicts, resolve them in the
integration worktree and then use `sync_main continue_branch=<branch>` to run
the helper's continue/review/commit path through git-ops as well. It delegates
to `scripts/sync-main-from-remote.sh`, creates an integration worktree under
`.worktrees/`, applies the helper's deterministic known resolutions, and
surfaces the integration branch/worktree plus the validation and
`merge-to-main` command. When local main already contains the remote but has
local-only commits, `sync_main` surfaces that as `local_ahead` and offers the
guarded `push_main` action instead of leaving the operator at a dead-end
message. Slots:

| Slot | Default | Description |
|---|---|---|
| `remote` | `origin` | Remote to fetch from. |
| `remote_branch` | `main` | Remote branch to reconcile. |
| `name` | auto-generated | Optional integration worktree name. |
| `continue_branch` / `branch` | empty | Existing sync integration branch to continue after conflicts are resolved. |
| `auto_resolve` | `false` | Enable the helper's configured resolver command after deterministic known resolutions. |

### `push_main` - guarded protected-main publish

Use `push_main remote=origin` from `main_ops` when protected local `main` is
ahead of `origin/main` and the remote should be brought up to the same commit.
The room fetches first, refuses to push if the remote has commits local main
does not contain, and uses a normal non-force `git push <remote> main:main`.
If it reports `remote_ahead`, run `sync_main remote=<remote>` first.

### `cleanup` — operand-as-slot, no swallowed failures

`remove_all` / `remove_worktree` take their target from the `worktree` slot
(`remove_all worktree=.worktrees/foo`), not ambient `world.worktree_path`. The
branch to delete is resolved **from the worktree itself**
(`git -C <wt> rev-parse --abbrev-ref HEAD`), never from the ambient
`current_branch` (the branch-leak root cause). The remove scripts drop the
blanket `|| true`; the real exit binds to `last_op_ok` and `last_op_outcome`
only flips to `"cleaned"` for a remove the bind saw succeed — a failed/no-op
remove **never** reports a false `"cleaned"` (the smoking gun). `force=true`
upgrades to `git worktree remove --force` + `git branch -D`; an unmerged branch
under a non-force remove leaves the worktree gone and routes to a
`confirm_force_delete` interstitial.

`stories/git-ops/tests/mutation_failure_surfacing.sh` re-introduces the
false-success bug and asserts `cleanup_noop_is_not_cleaned` then FAILS —
proving the surfacing is load-bearing, not decorative.

`stories/git-ops/tests/no_shared_stash_stack.sh` fails if any git-ops room
reintroduces `git stash push` or `git stash pop`. Checkpoint restore may still
use `git stash create` / `git stash apply <commit>` because those operate on
explicit commit objects instead of the repository-global stash stack.

### `integrate` — lost-work-safe single-shot

`integrate branch=<name>` (or the current branch) rebases the feature branch
onto **current** main, re-read at room entry (never a stored SHA), runs the
build check, and merges `--no-ff`. A concurrent session that moved main between
maker-start and integrate is **absorbed** — the rebase replays the branch on
top of the new HEAD rather than dropping its commits. A rebase/merge conflict
the auto-rebase can't resolve aborts the partial rebase, leaves the tree clean,
and surfaces `last_error` (no swallowed success). A build failure on the
integrated tree surfaces `last_error` as `build_fail`.
`stories/git-ops/tests/integrate_moved_main.sh` proves the no-lost-work
replay against a real throwaway repo.

### Conflict resolution

The `conflict_resolver` agent has `tools: [Read, Edit]` only — no Bash.
It physically cannot run `git commit`, `git push`, or `git checkout`.
The story drives all git operations.

After editing, the story runs `git rebase --continue --no-edit` and then
`build_check_cmd`. A syntactically clean but build-breaking resolution is
**rejected** — the operator sees the build output and can provide guidance.

## Real-git bad-state harness

Flow fixtures stub host calls, so they prove routing contracts but not every
Git edge condition. `stories/git-ops/tests/lib/bad_git_repo.sh` is the reusable
real-git fixture factory for those cases. It creates isolated temp repos using
nonsense files only and supports named scenarios:

```
bash stories/git-ops/tests/lib/bad_git_repo.sh list
repo=$(bash stories/git-ops/tests/lib/bad_git_repo.sh create pull-rebase-conflict)
bash stories/git-ops/tests/lib/bad_git_repo.sh assert pull-rebase-conflict "$repo"
```

Current scenarios cover no upstream, pull/rebase conflicts, an in-progress
rebase with no unmerged paths, a two-conflict rebase, branch ownership by a
linked worktree, dirty linked worktrees, merge conflicts in progress, detached
HEAD, diverged clean upstreams, dirty pull blockers, untracked overwrite
blockers, stale worktree entries, interrupted cherry-picks, bisects in progress,
rename/delete conflicts, modify/delete conflicts, binary rebase conflicts, and
local/remote tag disagreement. `stories/git-ops/tests/bad_git_repo_harness.sh`
smoke-tests that each scenario materializes as claimed.

## `merge_into_main` guards

Three guards run in sequence before the merge:

1. **Descendant check + stale-rebase check:** `git merge-base --is-ancestor integration HEAD`
   AND current merge-base == stored `rebase_base_sha`. Fails → `re_rebase_needed`.
2. **Dirty-tree check on target worktree:** dirty → stop and surface the dirty
   file list. `git-ops` deliberately does not use `git stash` here because the
   stash stack is shared across linked worktrees in the same repository.
   MERGE_HEAD present → `merge_in_progress` error.
3. **Merge with `--no-ff`, `cwd: main_worktree_path`:** no `git checkout` required.
   Guard 1 guarantees the merge is fast-forwardable (no conflicts possible).

## Non-goals (v1)

- General branch push / PR creation
- Interactive conflict editor
- Branch creation / checkout without worktree
- Cherry-pick, bisect, rebase -i
- Force-push
- Submodules / worktrees outside `.worktrees/`
