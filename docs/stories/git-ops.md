# git-ops — Interactive Git Workflow Story

`stories/git-ops/` is a hub-and-spoke story that provides a guided,
deterministic git workflow: staging, commit (agent-authored message), rebase,
squash-merge, worktree lifecycle, and conflict resolution. The agent appears
in exactly two places — authoring a commit message and resolving a conflict.
All other operations are deterministic `host.run` shell calls.

## Architecture

On entry, `idle` detects the current branch and worktrees with a single
JSON-emitting bash script and routes to the appropriate hub:

- **`main_ops`** — integration branch: pull, merge branch, worktree lifecycle
- **`branch_ops`** — feature branch: rebase, commit, squash, merge to main

Each hub refreshes its status on every return. Operations leave the hub,
execute, and return — one round-trip per operation except `conflict` which
loops until the operator resolves or aborts.

### Design principles

**Emit-intent routing via static targets.** Rooms that auto-route after a
host call use static guarded `emit_intent:` directives (`when: "world.X == 'Y'"`)
rather than templated `emit_intent: "{{ world.route }}"`. Static targets register
in the engine's `emitTargets` set, keeping the room out of the `isDecisionGate`
test and allowing `DispatchPostBindEmits` to fire correctly in staged (flow-test)
mode. A templated emit_intent matches no static name → the room appears as a
decision gate → the engine's staged-mode gate stops the emit. See the `/state-machine`
docs §staged-gate for the full contract.

**Single-script guards.** Rooms with multi-step guard chains (merge_into_main,
rebase, squash) run one comprehensive bash script in `on_enter` that performs
all checks and returns a JSON envelope with the outcome. This avoids the
`applyEffectsTraced` limitation where guarded `when:` clauses in on_enter
evaluate against the pre-bind (machine-phase) world rather than the post-dispatch
world. The single invoke + static emit_intent pattern is the canonical shape
for auto-routing rooms.

**once: for idempotent on_enter.** The conflict room uses `once: true` on both
`gather_conflict_files` (keyed on `conflict_files`) and `host.agent.task`
(keyed on `conflict_verdict`). When the guide intent clears both fields, the
next on_enter re-runs both; when verdict is pre-seeded (flow tests), agent
is skipped.

**No `git checkout` for merges.** The `merge_into_main` room runs
`git merge --no-ff` with `cwd: "{{ world.main_worktree_path }}"` — the worktree
holding the integration branch — rather than checking out the integration branch.
`git checkout` fails from a linked worktree because that branch is already checked
out elsewhere. The guard chain (descendant check → no-conflict invariant) ensures
the merge is always fast-forwardable and can never itself conflict.

**Conflict resolver fence.** The `conflict_resolver` agent is declared with
`tools: [Read, Edit]` and **no Bash**. It physically cannot run `git commit`,
`git push`, or `git checkout`. All git commands are driven by the story's
deterministic effects.

## Rooms

| Room | Purpose |
|---|---|
| `idle` | Entry: detect branch/worktrees → route to hub |
| `main_ops` | Integration branch hub |
| `branch_ops` | Feature branch hub |
| `staging` | Classify working-tree changes, interactive git add |
| `commit` | Agent-authored conventional commit message |
| `squash` | Squash all branch commits into one |
| `rebase` | git rebase against integration branch (local ref) |
| `conflict` | Agent auto-resolution with operator escalation |
| `merge_into_main` | Merge feature branch into integration (worktree-aware) |
| `merge_branch` + `merge_exec` | Merge a named branch (from main_ops) |
| `pull` | git pull --rebase from upstream |
| `stash_sandwich` | Reference room for stash-around-operation pattern |
| `worktree_create` | Create linked worktree under `.worktrees/` |
| `worktree_list` | Audit and classify existing worktrees |
| `cleanup` | Remove worktree + branch after merge |
| `undo` | Undo last commit (--mixed/--soft/--hard) |
| `done` | Terminal |

## merge_into_main guard sequence

The `merge_exec` bash script runs all three guards in order:

1. **Descendant + stale-rebase check** — `git merge-base --is-ancestor integration HEAD`
   AND current merge-base equals stored `rebase_base_sha`. Either failure →
   `outcome: "re_rebase_needed"`.
2. **Dirty-tree + MERGE_HEAD check on target worktree** — reads the integration
   checkout's git state. Dirty → stash sandwich (inline, not a separate room).
   MERGE_HEAD or rebase-in-progress → `outcome: "merge_in_progress"`.
3. **Merge with `--no-ff`** in `cwd: main_worktree_path`. Guard 1 guarantees the
   branch is a strict descendant → merge cannot conflict → no cross-worktree
   conflict handling needed.

Post-merge build gate: runs `world.build_check_cmd` (skipped when
`build_check_disabled: true`). Failure → `outcome: "post_merge_test_fail"`.

## Conflict resolution flow

1. `gather_conflict_files` — `git diff --diff-filter=U` to list conflicted files.
   go.sum special case: `git checkout --theirs go.sum && go mod tidy`.
2. `host.agent.task` (agent: `conflict_resolver`, tools: [Read, Edit]) — removes
   all conflict markers. Default strategy: take target-branch version, re-apply
   source additive changes.
3. `git diff --check` (`acceptance.post_cmd`) rejects leftover markers.
4. Story runs `git rebase --continue --no-edit`.
5. `build_check_cmd` validates semantic correctness. Failure routes to escalation
   (operator provides guidance → retry agent round).

## Flow fixtures

All 26 fixtures are intent-only with no LLM calls. `host.run` calls are stubbed
via `by_call:` keyed on `id:`. `host.agent.task` and `host.agent.decide` use
global `data:` stubs.

Key invariants verified:
- `merge_descendant_guard`: `re_rebase_needed` blocks merge when HEAD is not a descendant
- `merge_from_worktree`: merge proceeds with `cwd=main_worktree_path` (no checkout)
- `stale_rebase_check`: stale `rebase_base_sha` blocks merge even when `rebase_done=true`
- `conflict_build_reject`: build failure post-rebase-continue does not set `rebase_done=true`
- `staging_classify_suspicious`: suspicious files require explicit confirmation before `add_all`
- Natural-language routing: bare imperatives ("commit", "doit", "sync with main") route correctly

## Non-goals (v1)

- Push to remote / PR creation
- Interactive conflict editor
- `git rebase -i` (non-interactive forms only)
- Force-push
- Submodules / worktrees outside `.worktrees/`
- Auto-fetch before rebase (operators must `pull` on main first; documented limitation)

## See also

- `stories/git-ops/README.md` — operator entry guide
- `docs/architecture/hosts.md` §host.run — `cwd:` arg and argv mode
- `docs/stories/state-machine.md` §staged-gate — why static emit targets matter
- [`story-coverage-mining.md`](story-coverage-mining.md) — git-ops is the **flagship
  worked example** for mining a story's coverage from real transcripts; the corpus,
  demo, and worked worksheet live in `tools/session-mining/examples/git-ops/`, the
  profile in `stories/git-ops/mining.profile.yaml`.
