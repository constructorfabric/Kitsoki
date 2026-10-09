# ship-it — the single-brief delivery loop

Part of the shipped [delivery-loop story stack](../../docs/stories/delivery-loop.md). A
scoped brief delivers itself: implemented in isolation by the cherny-loop maker,
integrated to main lost-work-safely, **re-proven on the merged commit** by an
independent re-verify, and cleaned up — exiting `@exit:shipped` or
`@exit:needs-human` with the **real** error.

```
configure{brief,gate_command} ─▶ maker[cherny-loop] ─▶ integrate ─▶ verify ─▶ cleanup ─▶ @exit:shipped
   (the gate is              (worktree,            (rebase onto    (re-run gate    (remove        │
    the contract)            red→green loop)        moved-main safe) on MERGED sha) worktree)     │
                                                                                                  ▼
   any failure ──────────────────────────────────────────────────────────────────▶ @exit:needs-human
                                                                                     (last_error set)
```

## Entry / exits

- **Root:** `configure`.
- **Exits:** `shipped` (`requires: [shipped_sha]`), `needs-human`
  (`requires: [last_error]`). Every failure arc carries the real error — never a
  swallowed false success.

## World contract

| Key | Meaning |
|---|---|
| `brief` | the scoped task → cherny `goal_text` |
| `gate_command` | the deterministic gate (script mode); re-run **identically** on the merged commit |
| `base_branch` | integration branch (default `main`) |
| `workspace_id` / `workspace_branch` / `worktree_path` | the maker worktree handoff |
| `integrated_sha` / `shipped_sha` | the merged commit (shipped_sha set only after a green re-verify) |
| `verify_ok` | tri-state ("" until re-verify binds) |
| `last_error` | the real failure on any needs-human arc |
| `status` | `shipped` \| `needs-human` |

## The three import / reuse seams

1. **maker — imports `cherny-loop`** (`entry: configuring`). `world_in` projects
   `brief → goal_text`, `gate_command`, `gate_mode: script`, `workspace_*`. Its
   `@exit:achieved` leaves `worktree_path` + `workspace_branch` in world (the
   handoff seam) and projects to `integrate`; `@exit:exhausted`/`abandoned`
   project to `@exit:needs-human` with `last_error`. ship-it sets
   `baseline_green_policy: needs-human` so a maker gate that already passes
   before any change stops for human review instead of being auto-accepted.
2. **integrate — reuses the slice-1 git-ops lost-work-safe mechanism**: rebase
   `workspace_branch` onto **current** main (re-read at entry — a concurrent
   moved-main is absorbed), build-check, merge `--no-ff`. No swallowed failures;
   conflict/build-fail → `@exit:needs-human`.
3. **cleanup — reuses the slice-1 no-swallowed cleanup**: force-remove the
   worktree + branch; a refused remove → `@exit:needs-human` (never a false
   "cleaned"/"shipped").

> **Why integrate/cleanup are ship-it rooms, not a git-ops import.** git-ops is a
> hub-and-spoke story that must stay standalone-runnable; its integrate/cleanup
> rooms return to the git-ops hubs, not to `@exit:` arcs. Re-targeting them at
> exits would terminate a standalone git-ops session. ship-it therefore wraps the
> identical slice-1 *mechanism* (the same rebase-onto-current-main + no-swallow
> scripts) in its own exit-bearing rooms. See
> `.context/ship-it-design-decision.md`.

## verify — the trust room

`verify` re-runs the **identical** `gate_command` on the **merged commit** from
the **main** worktree (post-merge), not the maker's branch worktree. The maker
can converge a gate in its branch and still break main; re-running the same gate
on the merged commit is what makes "passes on main" true. Pinned `decider: llm`
so the deterministic verdict auto-fires in STAGED mode. Green → `cleanup`
(`shipped_sha` set); red → `@exit:needs-human` (the merge broke main).

## Flows (no-LLM)

| Flow | Proves |
|---|---|
| `happy_path` | integrate (clean) → verify (green on merged commit) → cleanup → `@exit:shipped` |
| `e2e_mocked_maker` | the headline: the full host-call sequence integrate → re-verify-on-merged → cleanup → shipped, maker mocked |
| `maker_entry` | the configure → ship → maker import seam (brief + gate projected into cherny's world) |
| `integrate_conflict` | unresolvable conflict → `@exit:needs-human` (no swallowed success) |
| `verify_red_on_main` | branch green but gate RED on the merged commit → `@exit:needs-human` (trust-the-gate) |
| `cleanup_refused` | cleanup failure → `@exit:needs-human` |

```
kitsoki test flows stories/ship-it/app.yaml
```

### Maker loop under import (resolved engine fix)

Driving cherny-loop's **autonomous** maker→checker loop to `@exit:achieved`
*through* the import once stalled at the `gating` room — it settled standalone
(`stories/cherny-loop/flows/achieved_via_loop.yaml`) but not when cherny was an
imported sub-story. The story exposed a real engine bug (stories/AGENTS.md:
expose, don't paper over), now **fixed in the engine**:

- the import rewriter did not rewrite an effect's `id:` template, so cherny's
  gating gate `id: "gate-{{ world.iteration }}"` rendered against the bare
  `world.iteration` (absent under the import alias). The unrendered call id
  missed the cassette, `gate_ok` never bound, and the gating emit-chain stalled.
  Fixed at `internal/app/imports_rewriter.go` (`rewriteEffect` now rewrites
  `eff.Id`).
- in **staged** mode the same chain was additionally misclassified as an
  operator decision gate: `isDecisionGate` built its emit-target set from the
  bare child emit names while the rewritten `on:` arcs were aliased. Fixed at
  `internal/machine/machine.go` (`isDecisionGate` now alias-resolves the emit
  targets).

The `stories/importer-gate` fixture gates both (one-shot + staged) by driving an
importer of cherny-loop all the way through its loop. ship-it's delivery-half
flows still **mock the maker handoff** via the `integrate_existing` intent (a
genuine operator affordance, and the same `worktree_path` + `workspace_branch`
the maker's `@exit:achieved` leaves) — that mock remains the right shape for the
deterministic delivery-half flows per the
delivery-loop validation plan, but the
maker path now settles end-to-end through the import when driven.
