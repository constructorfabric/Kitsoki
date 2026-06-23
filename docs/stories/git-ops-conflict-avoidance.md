# git-ops — conflict avoidance & safe step-retry (design & research)

This is the design rationale behind the git-ops story's conflict-avoidance
machinery: the strategies we surveyed, the three we shipped, why we put each
where we did, and what we deliberately left for later. The **operational
reference** lives in [`git-ops.md` § Conflict avoidance](git-ops.md#conflict-avoidance-for-parallel-agents);
this doc is the authoritative "why".

## The problem

kitsoki runs many agents in parallel, each in its own worktree under
`.worktrees/`, each rebasing onto a fast-moving `main`. Three pains fall out of
that:

1. **Conflict storms.** N branches rebasing past the same upstream commit hit the
   *same* conflict, N times.
2. **An LLM-resolution tax.** Every conflict that reaches the `conflict` room
   spends an agent turn (latency + tokens) — even when it's the identical conflict
   another agent already resolved an hour ago.
3. **No safe retry.** A workflow that wants to "go back to a step and try again"
   had nowhere safe to park the previous state — a `git reset` just destroys it,
   and the single stash stack is shared across a worktree so parallel agents
   clobber each other.

The goal: **fewer conflicts**, **resolve the unavoidable ones deterministically
(no LLM) wherever possible**, and **make any workflow step reversible**.

## Strategies surveyed

We grouped the established git techniques into three tiers by where they act.

### Tier 1 — prevent conflicts before they happen
- **Trunk-based / small batches.** Short-lived branches that integrate often never
  diverge far enough to conflict. The `integrate` room already re-reads *current*
  main at entry; the lever is cadence, not mechanism.
- **Worktree isolation.** Already in place (`internal/host/git_worktree.go`): each
  agent gets `.worktrees/<id>`, a shared object store, a separate index.
- **Merge drivers (`.gitattributes`).** Resolve additive / order-independent
  hotspot files deterministically: `merge=union` for append-only content,
  `merge=ours` + regeneration for generated files.
- **Disjoint-file ownership.** Partition work so agents touch non-overlapping
  files; detect overlap *before* dispatch so a collision is a planning signal, not
  a merge-time surprise.
- **Stop committing derived artifacts.** Anything regenerable is better regenerated
  post-merge than merged.

### Tier 2 — resolve the unavoidable conflicts deterministically (no LLM)
- **`git rerere` (reuse recorded resolution).** Records how a conflict was resolved
  and *replays the identical resolution automatically* the next time the same hunk
  appears. In a parallel-agent repo the same structural conflict recurs constantly,
  so the first resolution (human or LLM) becomes free forever after. The single
  biggest no-LLM lever after merge drivers.
- **Strategy options (`-X ours` / `-X theirs`).** Where one side wins by policy,
  pick it deterministically instead of dropping to manual resolution. Scope
  tightly — it silently discards the losing hunks.
- **`merge.conflictStyle=zdiff3`.** Shows the common base plus both sides; far
  better input for the rare case an agent *is* needed (and for humans).

### Tier 3 — checkpoint / retry / safe restore
- **Per-step checkpoint refs.** Before a risky step, snapshot committed HEAD *and*
  any dirty tree under a namespaced ref — without disturbing the working tree.
  `git stash create` returns a commit object **without** pushing onto the stash
  stack, which sidesteps the parallel-agent clobber.
- **Reflog + backup tags.** The safety net under destructive ops; the `rebase` room
  already auto-tags before a multi-commit rebase.
- **Staging / integration branch.** For batch recombination of N parallel branches,
  merge them onto a throwaway integration branch, gate once, then fast-forward to
  main. Recombination — not isolation — is the hard part of parallel agent work.

## What we shipped

Three layers, chosen for the best ratio of (conflict reduction × LLM avoidance) to
new surface area:

### 1. `git rerere` auto-resolution — at the rebase layer
The `rebase` room's `rebase_exec` script, on a conflicting rebase, loops:
`git rerere` (replay recorded resolutions) → stage any now-marker-free file →
`git rebase --continue`, until the rebase finishes or a never-seen conflict
remains. If the cache covers every hunk the rebase completes and the session lands
at `branch_ops` — the `conflict` room (and its LLM) is **never entered**. When the
conflict room *does* run, `git rebase --continue` with `rerere.enabled` **records**
the agent's resolution, so the next branch to hit that conflict gets it replayed
for free.

**Why the rebase layer and not the conflict room.** The natural first instinct was
to run rerere inside the `conflict` room's `gather` step and skip the agent when it
cleared everything. That doesn't work: a `when:` guard on an `on_enter` **invoke**
is evaluated *pre-bind* (synchronously, before the preceding `host.run` binds its
output), so it can't see the gather result — whereas standalone `emit_intent`
guards re-evaluate *post-bind*. Gating the agent that way silently skipped it in
the normal path and broke five flows. The rebase script is the correct home: it's
real bash with real git state, it resolves the conflict at the moment it's
produced, and it leaves the `conflict` room as the pure LLM/operator escalation
path. (This asymmetry is documented in
[`state-machine.md` § Routing on an async host result](state-machine.md).)

### 2. Embedded git config + `.gitattributes`
`scripts/setup.sh → configure_git` writes `rerere.enabled`, `rerere.autoupdate`,
`merge.conflictStyle=zdiff3`, and `rebase.autostash` into the clone's `.git/config`
and seeds the rerere cache from recent merge history. This lives in `make setup`
rather than a committed file because git refuses to read repo-tracked config (a
security boundary) — `make setup` is the embedding point.

`.gitattributes` marks `go.sum merge=union`: module checksums are append-only and
order-independent, so keeping both sides' lines (a later `go mod tidy` prunes) is
always valid. This is the same resolution the `conflict` room's go.sum special case
applies, but at *merge-driver time* — so the conflict never reaches the conflict
path at all. We kept `.gitattributes` deliberately conservative: the other obvious
candidates (generated embeds, the SPA bundle) are already gitignored, so they can't
conflict, and `union` on a *structured* file (lockfiles, YAML) would interleave
both sides into garbage. Per-path drivers apply to **local** merges/rebases only —
GitHub's web merge ignores `.gitattributes` — which is fine, since git-ops merges
locally.

### 3. Checkpoints — `checkpoint` / `restore` rooms
`checkpoint` parks committed HEAD **and** the dirty tracked tree under
`refs/kitsoki/checkpoints/<slug>` (+ a `.base` ref recording HEAD) via
`git stash create` — a commit object that does **not** push onto the single stash
stack, so two parallel agents on a shared worktree don't clobber each other.
`restore` resets committed history to the checkpoint base and re-applies the dirty
tree, **auto-saving the pre-restore state to `auto-pre-restore` first** so a restore
is itself reversible. Reachable from both hubs. Limitation: `git stash create`
captures tracked modifications only — `git add` new files before checkpointing.

## What we deferred (and why)

- **Disjoint-file ownership pre-dispatch check.** The highest-leverage *prevention*
  lever, but it's orchestrator-level (it needs each agent's declared file set
  before dispatch), not a git-ops room. Out of scope for a story change.
- **Staging-branch batch recombine.** Valuable for landing N branches at once, but
  a sizeable new room with its own gate semantics; better as its own slice than
  bolted on here.
- **Cross-machine rerere resolution library.** `rr-cache` is already shared across a
  clone's worktrees (it lives in the common git dir). Committing a resolution
  library to share *across machines* is a heavier, separable piece.
- **Lockfile regen merge driver.** `merge=ours` + a post-merge `pnpm install` is the
  standard pattern, but a silent `ours` that drops the incoming lockfile without an
  automated regen hook is a footgun — deferred until the regen hook exists.

## Testing approach

Flow fixtures stub `host.run`, so the embedded bash never executes — the entire
class of "the shell script mishandles real git state" is invisible to the flow
suite. So the git-plumbing-sensitive behaviors are covered by **real-repo** Go
tests in `internal/orchestrator/`:

| Test | Proves |
|---|---|
| `TestGitOps_RebaseConflict_RoutesToConflictRoom` | a real conflict routes to `conflict` (not a swallowed false-success) |
| `TestGitOps_RebaseConflict_RerereAutoResolves` | a rerere-cached conflict auto-resolves to `branch_ops`; the agent stub *escalates*, so a green run proves no LLM was called |
| `TestGitOps_CheckpointRestore_Roundtrip` | checkpoint a dirty tree, advance the branch, restore back to exact HEAD + dirty content; `auto-pre-restore` preserved |

The `checkpoint_restore.yaml` flow locks the routing / world-binding contract; the
static `TestGitOps_NoSwallowedExitAfterTrueGuard` keeps the exit-code-swallowing
anti-pattern from creeping into any room script.

## Sources

- git rerere — <https://git-scm.com/docs/git-rerere>, <https://git-scm.com/book/en/v2/Git-Tools-Rerere>
- `.gitattributes` merge drivers / `merge=union` — <https://github.com/orgs/community/discussions/9288>, <https://charpeni.com/blog/use-custom-merge-driver-to-simplify-git-conflicts>
- worktrees for parallel AI agents — <https://www.augmentcode.com/guides/git-worktrees-parallel-ai-agent-execution>
- reflog / stash recovery — <https://www.atlassian.com/git/tutorials/rewriting-history/git-reflog/>, <https://git-scm.com/docs/git-stash>

## See also

- [`git-ops.md`](git-ops.md) — the story reference (rooms, guards, operational flow)
- [`state-machine.md`](state-machine.md) — the pre-bind/post-bind emit asymmetry that decided where rerere lives
