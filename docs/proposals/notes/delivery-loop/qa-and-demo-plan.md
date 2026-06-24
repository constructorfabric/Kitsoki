# delivery-loop — QA & demo plan (design artifact)

Companion to the [delivery-loop epic](../../delivery-loop.md).
Every gate here is **deterministic and no-LLM** (CLAUDE.md); the maker is mocked
via cassette. No real LLM, no cost — ever, unless explicitly gated and requested.

## 1. Deterministic flow-fixture coverage (per room)

The RED gate for each slice is `kitsoki test flows stories/<name>/app.yaml` green.
Each fixture is Mode-2 (intent-only, no-LLM, CI-fast), modelled on
`stories/cherny-loop/flows/achieved_via_loop.yaml`.

### Slice 1 — git-ops smoothing
| Fixture | Proves |
|---|---|
| `cleanup_by_slot` | named operand removed in one shot (no picker) |
| `cleanup_noop_is_not_cleaned` | a failing remove never reports `cleaned` (the smoking-gun regression) |
| `cleanup_force` | dirty worktree + unmerged branch removed after confirm |
| `integrate_clean` | rebase onto current main → build_check → merge |
| `integrate_moved_main` | main advanced post-fork → branch replayed, no lost work |
| `integrate_conflict_unresolvable` | routes out with `last_error`, no swallowed success |
| **mutation:** re-add `\|\| true` | `cleanup_noop_is_not_cleaned` must FAIL — failure-surfacing is load-bearing |

### Slice 2 — ship-it
| Fixture | Proves |
|---|---|
| `happy_path` | configure → maker → integrate → verify → cleanup → `@exit:shipped` |
| `maker_exhausted` | cherny `@exit:exhausted` → `needs-human` with budget reason |
| `integrate_conflict` | unresolvable conflict → `needs-human` (leans on slice 1) |
| `verify_red_on_main` | maker green in branch, gate RED on merged commit → `needs-human` (trust-the-gate) |
| `cleanup_refused` | cleanup failure → `needs-human` |

### Slice 3 — fleet
| Fixture | Proves |
|---|---|
| `happy_path_two_briefs` | both briefs ship → `@exit:done`, `shipped_count=2` |
| `one_parks_others_ship` | a failure parks one brief, the rest continue |
| `lock_serializes` | `merge_lock_held` true for exactly one brief's `dispatch` span |
| `all_parked` | every error surfaced in summary, `shipped_count=0` |

## 2. End-to-end no-LLM proof (the headline)

`stories/ship-it/flows/e2e_mocked_maker.yaml` — the full loop with the maker
**mocked via cassette**:

- `host.agent.task` (the cherny maker) → cassette returning a one-line summary.
- `host.run` gate → `by_call`: `gate-0` RED (baseline), `gate-1` GREEN (post-maker),
  `verify-0` GREEN (re-run on merged commit). Same command, three call sites.
- `host.git_worktree` / git-ops `host.run` integrate + cleanup → stubbed to clean.

Asserts the full host-call sequence: worktree mint → baseline gate (RED) → maker →
branch gate (GREEN) → integrate (rebase+merge) → **re-verify gate on merged sha
(GREEN)** → cleanup → `@exit:shipped`, `shipped_sha` set. This single fixture is the
epic's deliverable-existence gate: it proves brief→maker→integrate→verify→cleanup→
shipped with zero LLM.

## 3. Demo-video + QA outline (the showcase)

Once slices land, produce a deterministic tour via the **kitsoki-ui-demo** skill
and gate it with **kitsoki-ui-qa** (the inverse). No-LLM throughout — driven by
the `e2e_mocked_maker` flow + a host-cassette under `kitsoki web --flow`.

**Demo (kitsoki-ui-demo):** a feature-spotlight tour of the ship-it pipeline rail
(the [web mock](./ship-it-web-mock.html) is the target layout), then a fleet
board fan-out over delivery-loop's own two-brief decomposition. Chapters:
1. configure — brief + verbatim gate.
2. maker — cherny iterating in the isolated worktree (red→green).
3. integrate — rebase-onto-current-main badge (the lost-work-safe beat).
4. verify — the "on MERGED commit" re-run (the trust beat — the differentiator).
5. shipped — clean worktree, merged sha.
6. fleet board — three briefs, merge-lock serializing, one parks + continues.

**QA (kitsoki-ui-qa):** scenarios the vision agent must find evidence for:
- the gate command renders **verbatim** in both configure and verify (the contract);
- verify runs on the **merged** commit (badge present), not the branch;
- a failure shows a **real** `last_error`, never a swallowed "cleaned"/"shipped";
- fleet's merge-lock is visibly held for one brief at a time;
- a parked brief does **not** stop the others.
Adversarially re-check every pass; gate on `verdict.json`.

## 4. The independent re-verify discipline (why two evaluation sites)

The maker's checker proves the gate green *in the branch worktree*. ship-it's
`verify` re-runs the **identical** command *on the merged commit from the main
worktree*. A green branch + RED main = the merge broke something (stale rebase,
merge artifact) — caught here, routed to `needs-human`, never shipped. This is the
"trust the gate, not the agent's done" rule encoded as a room
(`verify_red_on_main` is its regression).
