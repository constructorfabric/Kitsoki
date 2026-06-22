# Slice 4 — bugfix convergence onto the ship-it tail

**Status:** Draft. Part of the [delivery-loop epic](delivery-loop.md).
**Kind:**   story
**Depends:** #2 ship-it (its `integrate` / `re-verify` / `cleanup` rooms must exist as the shared tail).

## Why

The bug-fix pipeline (`stories/bugfix/`) and `ship-it` share one backbone —
**brief → implement-in-an-isolated-worktree → deterministic gate** — but bugfix
stops three steps short, and those three steps are exactly what an operator had
to do by hand when dogfooding a real bug fix this session (see the git-ops
cleanup bug, `issues/bugs/2026-06-22T114500Z-gitops-cleanup-…md`):

1. **No lost-work-safe integrate.** `bugfix @exit:done` hands off to
   `pr-refinement` to *open a PR*; it never merges to local main and nothing
   rebases onto a *moved* main. The operator drove `git-ops merge_branch` by hand
   and cherry-picked onto fresh main by hand when a concurrent session moved it.
2. **No independent re-verify on the MERGED commit.** `bugfix`'s `testing` /
   `validating` rooms run in the *branch worktree* — "passed in the branch", not
   "passes on main". The operator re-ran every gate on the merged commit himself
   (the `[[workflow-gate-on-independent-verify]]` discipline).
3. **`needs-human` doesn't reliably carry the real error** — same family as the
   git-ops `|| true` swallow (`.context/gitops-ux-review.md`).

ship-it already builds all three as a reusable tail (`integrate` → `re-verify` →
`cleanup` → `shipped | needs-human`, per [delivery-loop.md](delivery-loop.md)
shared decisions 2–3). bugfix should **compose that tail, not reinvent a weaker
one** — the "dev-story = the general reusable piece" principle.

### The bugfix-specific gap ship-it does NOT cover: the regression gate isn't RED→GREEN

Driven over MCP this session, the pipeline's `reproduce`/`test` phases produced a
test that **asserts the bug is present** (`assert.Contains(BR="current_branch")`)
— a *characterization* test, not a *regression* test. After the fix it would
**fail**, yet the pipeline reached `@exit:done` because the gate (`go test ./...`)
never required that specific test to flip. That violates the goal's own contract
(reproduce with a deterministic test → fix → **validate with the deterministic
test**, goal lines 45–47): the test must **fail before the fix and pass after**,
and `validate` must assert *both*.

## What changes

1. **bugfix composes the ship-it tail.** Replace the always-PR-handoff exit with
   the shared `integrate → re-verify → cleanup → shipped | needs-human` rooms
   from ship-it (imported, not copied). The bugfix maker (reproduce → propose →
   implement) feeds the tail exactly as cherny-loop does, via the same
   `worktree_path` / `workspace_branch` handoff seam.
2. **RED→GREEN regression gate.** `reproduce`/`test` emit a regression test that
   is **RED with the bug present**; `validate` (now the shared `re-verify` on the
   merged commit) requires it **GREEN post-fix** *and* records that it was **RED
   on the pre-fix snapshot** (run once before the implement room mutates the
   tree). A fix whose regression test was never RED, or isn't GREEN on merged
   main, routes to `needs-human` — never `shipped`.
3. **Exit is a choice, not fixed:** `direct-ship` (tail → local main, the dogfood
   loop) vs `open-PR` (today's `pr-refinement` handoff). Default `direct-ship` for
   the self-hosting loop; `open-PR` for human-review flows.

## Design (rooms)

- bugfix keeps `reproducing` / `proposing` / `implementing` (the maker).
- `testing` is reframed: author/run the regression test and **record its pre-fix
  RED** (it runs on the snapshot before `implementing` mutates the worktree).
- `validating` is replaced by an `import` of ship-it's `verify` room (re-run the
  regression gate on the MERGED commit) + `integrate` + `cleanup`.
- `reviewing` stays optional (full mode); `done` becomes the `shipped` exit or the
  `open-PR` handoff per the exit slot.

## Gate (no-LLM, deterministic)

- `bugfix_ships_direct.yaml` — mocked-maker flow: ticket → maker (cassette) →
  integrate → re-verify on merged commit → `shipped`. Proves the tail is reused.
- `bugfix_regression_gate_red_then_green.yaml` — proves the regression test is
  **RED on the pre-fix snapshot and GREEN after** (a fix that leaves it RED, or a
  characterization test that was never RED, routes to `needs-human`).
- `bugfix_needs_human_on_merged_red.yaml` — gate green in branch but RED on
  merged main → `needs-human` with `last_error`, not `shipped`.
- kitsoki-dev 6/6 + existing bugfix flows stay green (the maker rooms are
  unchanged; only the tail + the gate discipline move).

## Implemented BY ship-it (dogfood)

This slice's brief — *"converge bugfix onto the ship-it tail and make its
regression gate RED→GREEN"* — is **run through ship-it itself** once #2 lands:
`configure{brief, gate_command:"kitsoki test flows stories/bugfix"}` → maker →
integrate → re-verify → `shipped`. The delivery loop shipping its own bug-fix
convergence is the acceptance demonstration for ship-it. If ship-it can't ship
this slice, it isn't ready — a self-referential gate.
