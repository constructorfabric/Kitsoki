# Story: git-ops smoothing — operand-as-slot, no swallowed failures, lost-work-safe integrate

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   story
**Epic:**   [../delivery-loop.md](delivery-loop.md) (slice 1)

## Why

Driving `stories/git-ops/` over MCP for a real worktree lifecycle, the author
kept falling back to **raw git** — the symptom of four design problems
catalogued in [`.context/gitops-ux-review.md`](../../.context/gitops-ux-review.md).
The smoking gun, verbatim from a free-text drive:

> `"clean up the worktree bf-gitops-cleanup and delete its branch"` →
> routed to the `cleanup` room (good), reported `last_op_outcome="cleaned"`,
> `last_op_ok=True` — **and removed nothing.** The named operand was dropped
> (the room reads ambient `world.worktree_path`, empty on free-text arrival), so
> it ran `git worktree remove ""`, which failed, was swallowed by `|| true`, and
> still reported `cleaned`. A confident **false success for a no-op** — the worst
> outcome for trust.

The delivery loop (epic) cannot compose on a git surface that drops operands and
lies about results. This slice makes git-ops the *only* git surface needed.

## What changes

Four targeted reworks to existing git-ops rooms — no new story, no new mechanism:

1. **Operands become slots, not ambient world.** `cleanup worktree=<path|branch>`,
   `merge branch=<name>` resolve in one shot; the picker stays only as a fallback
   when the slot is omitted.
2. **Stop swallowing failures.** Drop blanket `|| true`; bind the real exit code
   + stderr; route non-zero to an error view that renders `last_error`. Never set
   `last_op_outcome: "cleaned"` on a no-op.
3. **Force / confirm for the normal end-states of abandoned work.**
   `remove_worktree … force=true` (→ `git worktree remove --force`) and
   `git branch -D` after a merged-check with an explicit confirm when unmerged.
4. **One-shot lost-work-safe `integrate branch=<name>`.** Rebases a stale feature
   branch onto **current** main (or 3-way merges), re-runs the build check, and
   reports — so a moved-main never needs raw git. Today `merge_into_main` only
   *detects* the moved-main (`re_rebase_needed` outcome,
   `stories/git-ops/rooms/merge_into_main.yaml:32`) and punts the rebase back to
   the operator; this folds the rebase + recheck into the single `integrate` arc.

## Impact

- **Net-new:** 0 rooms, 1 new `integrate` arc on the hub (or a thin `integrate`
  room); reworks to `cleanup.yaml`, `merge_into_main.yaml`, `merge_branch.yaml`.
- **Engine/host changes:** none expected — the `host.run` bind already exposes
  `ok` / exit code; the fix is to *stop discarding* it. See epic open question #2
  (story-only vs. a render micro-slice); this slice must close it with a mutation
  flow.
- **Docs on ship:** fold into `docs/stories/git-ops.md`.

## Reuse inventory

| Step | Mechanism | Reference |
|---|---|---|
| Operand as command slot | `slots:` on a hub intent | `git-ops/app.yaml:205` (`merge_branch.branch` already declared — just stop ignoring it) |
| Bind real exit + stderr | `bind: { last_op_ok: ok, last_op_output: stderr }` | `git-ops/rooms/cleanup.yaml:54` (today binds `ok` but `|| true` masks it) |
| Route non-zero to error view | guarded `when:` + `last_error` render | `git-ops/rooms/merge_into_main.yaml:31` (the `re_rebase_needed` view pattern) |
| Force remove / `-D` confirm | interstitial confirm intent | `git-ops/rooms/staging.yaml` suspicious-file `confirm_add_all` pattern (`git-ops/app.yaml:311`) |
| Rebase-onto-current-main | the existing rebase room + descendant guard | `git-ops/rooms/rebase.yaml`, `merge_into_main.yaml:92` (guard 1) |

## Story graph (the reworked arcs)

```
main_ops ── cleanup worktree=X ──▶ cleanup ── remove_all force=? ──▶ (real exit bound)
   │                                              ├─ ok ────▶ main_ops (outcome="cleaned")
   │                                              └─ fail ──▶ cleanup (last_error rendered)  ◀── no more false "cleaned"
   │
   └── integrate branch=Y ──▶ integrate
            on_enter: rebase Y onto CURRENT main → (conflict? → conflict room)
                      → build_check → merge → report
            ├─ clean ───▶ main_ops (outcome="integrated")
            └─ conflict/fail ─▶ @surface last_error  (the loop reads this as needs-human)
```

## Per-room detail

### `cleanup` — rework (operand-as-slot + no swallowed failure)

- **Slots:** lift `worktree=<path|branch>` and `force=<bool>` onto the
  `cleanup` / `remove_all` / `remove_worktree` intents (`remove_*` already have a
  `path` slot, `git-ops/app.yaml:270` — wire it through to the command instead of
  reading ambient `world.worktree_path`).
- **Effect:** drop `|| true` (`cleanup.yaml:49-51`); `set -e` already present, so
  bind the real `ok`. Resolve the target branch from the worktree
  (`git -C <wt> rev-parse --abbrev-ref HEAD`) rather than ambient `current_branch`
  (the branch-leak root cause, ux-review #2).
- **`set:` guard:** `last_op_outcome: "cleaned"` moves behind
  `when: "world.last_op_ok == true"` — **the** fix for the false-success.
- **Force:** `force=true` → `git worktree remove --force` + `git branch -D`;
  unmerged `-d` failure → an explicit confirm interstitial (the
  `confirm_add_all` pattern).

### `integrate` — new single-shot arc (lost-work-safe)

- **`on_enter`:** rebase `branch` onto **current** main (re-read main's HEAD at
  entry, never a stored SHA), run `build_check_cmd`, then merge. A conflict the
  fence agent (`conflict_resolver`, `git-ops/app.yaml:28`) resolves continues;
  one it can't routes out with `last_error`.
- **Why it's lost-work-safe:** the rebase base is *current* main, recomputed at
  entry, so a concurrent session moving main between maker-start and integrate is
  absorbed (rebase replays the branch on top) rather than silently dropping the
  branch's commits. The descendant guard (`merge_into_main.yaml:92`) becomes a
  *pre-rebase* check, not a dead-end.
- **Intents:** `integrate{branch, force}`; on conflict → existing `conflict`
  room; on build fail → `rollback` (existing) with `last_error`.

## Flow fixtures

- `cleanup_by_slot` — `cleanup worktree=.worktrees/foo` removes the *named* tree
  in one shot (no picker round-trip); asserts the right `git worktree remove`
  arg.
- `cleanup_noop_is_not_cleaned` — a failing remove (empty/invalid path) binds
  `last_op_ok=false`, renders `last_error`, and **does not** set
  `outcome="cleaned"`. The regression for the smoking gun.
- `cleanup_force` — `force=true` removes a dirty worktree (`--force`) + `-D`s an
  unmerged branch after confirm.
- `integrate_clean` — `integrate branch=feature/x` rebases onto current main,
  build_check green, merges, `outcome="integrated"`.
- `integrate_moved_main` — main advanced one commit after the branch forked;
  `integrate` rebases onto the *new* HEAD, replays the branch, merges — proving
  no lost work.
- `integrate_conflict_unresolvable` — rebase conflict the fence can't resolve →
  routes out with `last_error` (no swallowed success).
- **Mutation flow** (closes epic OQ #2): re-add a `|| true` to the cleanup effect
  and assert `cleanup_noop_is_not_cleaned` **fails** — proving the
  failure-surfacing is load-bearing, not decorative.

## Tasks

```
## 1. Operand-as-slot
- [ ] 1.1 Wire cleanup/remove_* to read the `worktree`/`path` slot, not ambient world
- [ ] 1.2 merge_branch consumes `branch=` in one shot (skip the picker when present)
- [ ] 1.3 Flows: cleanup_by_slot

## 2. No swallowed failures
- [ ] 2.1 Drop `|| true`; bind real exit; gate `outcome="cleaned"` on last_op_ok
- [ ] 2.2 Error view renders last_error on non-zero
- [ ] 2.3 Flows: cleanup_noop_is_not_cleaned + the mutation flow

## 3. Force / confirm
- [ ] 3.1 remove_worktree force=true; branch -D after merged-check + confirm
- [ ] 3.2 Flow: cleanup_force

## 4. Lost-work-safe integrate
- [ ] 4.1 integrate{branch,force}: rebase onto CURRENT main → build_check → merge → report
- [ ] 4.2 Flows: integrate_clean, integrate_moved_main, integrate_conflict_unresolvable

## 5. Live + document
- [ ] 5.1 kitsoki test flows stories/git-ops/app.yaml green
- [ ] 5.2 Drive over MCP free-text (the ux-review repro) — confirm no false-success
- [ ] 5.3 Fold into docs/stories/git-ops.md; trim/delete this child
```

## Open questions

1. **`integrate` as a hub arc vs. a thin room.** A room gives a clean conflict
   off-ramp and a stable target for ship-it to import. *Lean: a thin `integrate`
   room reusing the existing `conflict` / `rollback` rooms.*
2. **3-way merge fallback when rebase is hostile.** Some histories rebase badly
   (many conflicts); offer `integrate strategy=merge`? *Lean: rebase-first,
   `strategy=merge` as an explicit opt-in slot — defer if it bloats the slice.*

## Non-goals

- Any `git push` / remote operation — integrate is local-main only.
- Reworking the commit-message or staging rooms (out of scope; this slice is the
  worktree/branch/integrate surface the loop needs).
