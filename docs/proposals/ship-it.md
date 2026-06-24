# Story: ship-it — the single-brief delivery loop

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   story
**Epic:**   [../delivery-loop.md](delivery-loop.md) (slice 2)

## Why

A scoped brief should *deliver itself*: implemented in isolation, proven by a
deterministic gate, integrated to main safely even when main moved, **re-proven
on the merged commit**, and cleaned up — without the operator hand-driving git or
trusting the maker's "done." Today that loop is glue in the operator's head
(epic Why). `cherny-loop` already does the *interpretive* half (the maker loop);
ship-it adds the *deterministic* delivery half around it.

## What changes

A new importable story **`stories/ship-it/`**, pipeline-shaped (structurally like
`stories/bugfix/`), that **imports three sub-stories** and chains them:

```
configure ─▶ maker[cherny-loop] ─▶ integrate[git-ops] ─▶ re-verify ─▶ cleanup ─▶ @exit:shipped
   (brief,        (worktree,            (rebase-onto-       (re-run gate     (remove        │
    gate_cmd)      red→green loop)       moved-main safe)    on MERGED commit) worktree)     │
                                                                                             ▼
   any failure ───────────────────────────────────────────────────────────────▶ @exit:needs-human
                                                                                  (last_error set)
```

The single sentence: **a pipeline-shaped delivery story that wraps cherny-loop's
maker with a lost-work-safe integrate, an independent re-verify, and cleanup,
exiting `shipped` or `needs-human` with the real error.**

## Impact

- **Net-new:** `stories/ship-it/` — ~3 own rooms (`configure`, `verify`,
  `report`), 3 imports (cherny-loop / git-ops / — re-verify reuses cherny's
  script-gate effect), 1 schema, ~5 flow fixtures + 1 end-to-end.
- **Engine/host changes:** none — composes existing mechanisms. Depends on
  slice 1 (`git-ops-smoothing.md`) for the `integrate` arc + non-swallowed
  cleanup.
- **Docs on ship:** `docs/stories/ship-it.md`, README queue entry.

## Reuse inventory

| Pipeline step | Mechanism | Reference |
|---|---|---|
| Capture brief + gate_command | `configure` slots, intake | `cherny-loop/rooms/configuring.yaml`, `git-ops` slot pattern |
| Implement in isolated worktree | import `cherny-loop`, entry `configuring` | `cherny-loop/app.yaml:252`; mints `.worktrees/<id>` at `launch` |
| Maker handoff seam | `@exit:achieved` leaves `worktree_path`+`workspace_branch` in world | `cherny-loop/rooms/gating.yaml:71` |
| Lost-work-safe integrate | import smoothed `git-ops`, `integrate{branch}` | `git-ops-smoothing.md` slice 1 `integrate` arc |
| Independent re-verify | re-run `gate_command` via `host.run` on the merged worktree | `cherny-loop/rooms/baseline.yaml:56` (the same script-gate effect) |
| Cleanup worktree + branch | import smoothed `git-ops`, `cleanup worktree=` | `git-ops-smoothing.md` slice 1 `cleanup` |
| Surface real error on fail | `@exit:needs-human` + `last_error` | epic shared decision #3 |

## Story graph

```
configure ── ship ──▶ maker (cherny import: configuring → … → @exit:achieved)
 │ (brief,                  │
 │  gate_command)           │ exit:achieved  (worktree_path, workspace_branch carried up)
 │                          ▼
 └─ quit ─▶ @exit:        integrate (git-ops import: integrate{branch=workspace_branch})
    needs-human            │
                           ├─ exit:integrated ─▶ verify
                           └─ exit:conflict/fail ─▶ @exit:needs-human (last_error)
                              ▼
                          verify  ── on_enter: re-run gate_command in main worktree (post-merge)
                           ├─ gate green ─▶ cleanup
                           └─ gate red ──▶ @exit:needs-human (last_error = gate stdout tail)
                              ▼
                          cleanup (git-ops import: cleanup worktree=workspace_id force=true)
                           ├─ cleaned ─▶ report ─▶ @exit:shipped
                           └─ refused ─▶ @exit:needs-human (last_error)
```

`@exit:shipped` requires `[shipped_sha]`; `@exit:needs-human` requires
`[last_error]`.

## World schema (sketch)

```yaml
world:
  brief:            { type: string, default: "" }   # the scoped task text → cherny goal_text
  gate_command:     { type: string, default: "" }   # the deterministic gate (script mode)
  base_branch:      { type: string, default: "main" }
  workspace_id:     { type: string, default: "" }   # passed to cherny; .worktrees/<id>
  workspace_branch: { type: string, default: "" }   # bound back from cherny's exit
  worktree_path:    { type: string, default: "" }   # bound back from cherny's exit
  integrated:       { type: bool,   default: false }
  verify_ok:        { type: string, default: "" }    # tri-state: "" until re-verify binds
  shipped_sha:      { type: string, default: "" }   # the merged commit, set after re-verify green
  last_error:       { type: string, default: "" }   # the real failure on any needs-human arc
  status:           { type: string, default: "" }   # shipped | needs-human
```

`exits:` — `shipped: { requires: [shipped_sha] }`, `needs-human: { requires: [last_error] }`.

## Per-room detail

### `configure` — root; capture the brief + gate

- **Intents:** `ship{brief, gate_command, base_branch}` (slots), `quit`.
- **View:** renders the brief and the gate command **verbatim** (a `code` block —
  the gate is the contract, mirroring cherny's `configuring`); confirms isolation
  (`.worktrees/<workspace_id>`).
- **`ship` arc:** projects `brief → goal_text`, `gate_command → gate_command`,
  `gate_mode: "script"` into the cherny import via `world_in:`, then transitions
  into the maker.

### maker — import `cherny-loop` (the interpretive half)

- **Import:** `source: ../cherny-loop`, `entry: configuring` (or warp straight to
  `launch` with config pre-projected). `world_in:` projects
  `goal_text / gate_command / gate_mode=script / workspace_id / workspace_branch /
  base_branch`. cherny mints the worktree, runs red-before-green, loops.
- **Exit projection:** `cherny.@exit:achieved → integrate`, carrying
  `workspace_branch` + `worktree_path` up (they're already in cherny's world post-
  achieve; lift via the exit `set:`, the bf→pr pattern at
  `dev-story/app.yaml:1043`). `cherny.@exit:exhausted` / `@exit:abandoned` →
  `@exit:needs-human` with `last_error` = cherny's `terminal_reason` +
  `last_gate_failure`.

### integrate — import smoothed `git-ops` (slice 1)

- **Import:** `source: ../git-ops`, `entry: integrate` (the new room from slice 1).
  `world_in:` projects `branch=workspace_branch`, `base_branch`. The room rebases
  onto **current** main, build-checks, merges.
- **Exit projection:** `integrated → verify` (set `integrated: true`, lift the
  merged SHA); `conflict`/`fail` → `@exit:needs-human` with `last_error` = the
  git-ops `last_op_output`.

### `verify` — independent re-verify (the trust-the-gate room)

- **`on_enter`:** `host.run` the **identical** `gate_command`, but with
  `cwd`/`working_dir` = the **main worktree** (post-merge), not the maker's branch
  worktree. Binds tri-state `verify_ok` (the cherny gate-bind discipline so the
  post-bind guarded emits route correctly). Pinned `decider: llm` so the
  deterministic verdict fires in STAGED mode (the `verifying.yaml` precedent,
  `dev-story` ad-hoc plan).
- **Why a distinct room from the maker's checker:** the maker can converge a gate
  in its branch and still break main (a stale rebase, a merge artifact). Re-running
  the *same* gate on the *merged* commit is what makes "passes on main" true, not
  "passed in the branch." This is the independent re-verify the manual pattern does
  by hand.
- **Routing:** green → `cleanup` (set `shipped_sha` = the merged SHA); red →
  `@exit:needs-human` with `last_error` = the gate's stdout/stderr tail. A red
  here means main is broken by the merge — a rollback may be warranted (open
  question #2).

### `cleanup` — import smoothed `git-ops`

- **Import:** `cleanup worktree=<workspace_id> force=true` (slice 1's single-shot
  form). `cleaned → report`; `refused → @exit:needs-human`.

### `report` — the shipped read-out

- Renders `shipped_sha`, the brief, and a one-line summary; `@exit:shipped`. This
  is the room fleet reads to mark a brief done and release the merge-lock.

### Net-new files

```
stories/ship-it/
├── app.yaml
├── rooms/{configure,verify,report}.yaml
├── schemas/ship-result.json
├── flows/{happy_path,maker_exhausted,integrate_conflict,verify_red_on_main,cleanup_refused,e2e_mocked_maker}.yaml
└── README.md
```

## Flow fixtures (no-LLM)

- `happy_path` — configure → maker (cherny stubbed: baseline red, one maker turn,
  gate green) → integrate (clean) → verify (green on main) → cleanup → `@exit:shipped`.
- `maker_exhausted` — cherny `@exit:exhausted` → `@exit:needs-human`,
  `last_error` carries the budget reason.
- `integrate_conflict` — integrate hits an unresolvable conflict →
  `@exit:needs-human` (no swallowed success — leans on slice 1).
- `verify_red_on_main` — maker green in branch, integrate clean, but the **re-run
  gate is RED on the merged commit** → `@exit:needs-human`, `last_error` = gate
  tail. The trust-the-gate-not-the-agent case.
- `cleanup_refused` — cleanup fails (dirty tree, force off) → `@exit:needs-human`.
- `e2e_mocked_maker` — the full end-to-end with the maker mocked via cassette
  (`host.agent.task` + `host.run` gate stubbed): brief → maker → integrate →
  verify → cleanup → shipped. The epic's headline no-LLM proof.

## Tasks

```
## 1. Scaffold
- [ ] 1.1 app.yaml: imports (cherny-loop, git-ops), world schema, exits, root=configure
- [ ] 1.2 rooms/{configure,verify,report}.yaml; schemas/ship-result.json

## 2. Wire the imports
- [ ] 2.1 cherny import world_in + @exit:achieved → integrate exit projection (carry workspace_branch)
- [ ] 2.2 git-ops integrate import + exit projection; cleanup import
- [ ] 2.3 verify room: re-run gate_command in MAIN worktree, tri-state bind, decider: llm

## 3. Lock the graph
- [ ] 3.1 Probe each room with kitsoki turn --state --intent --world
- [ ] 3.2 Flows: happy_path, maker_exhausted, integrate_conflict, verify_red_on_main, cleanup_refused
- [ ] 3.3 e2e_mocked_maker (maker via cassette) green

## 4. Live + document
- [ ] 4.1 kitsoki run end-to-end against a throwaway one-line brief (real maker, gated)
- [ ] 4.2 README (entry, exits, world contract, the three import seams)
- [ ] 4.3 Migrate to docs/stories/ship-it.md; trim/delete this child
```

## Open questions

1. **Does ship-it re-derive the gate, or only accept an explicit `gate_command`?**
   cherny can have a *planner* pick the gate; ship-it's contract is determinism.
   *Lean:* ship-it **requires** an explicit `gate_command` (script mode) — the
   re-verify must re-run an identical command, so it cannot be planner-chosen
   per-run. A planner-chosen gate is a non-goal here.
2. **Rollback on `verify_red_on_main`.** A red gate on the merged commit means the
   merge broke main. Auto-rollback (revert the merge) vs. park for human?
   *Lean:* v1 parks at `needs-human` with `last_error` and leaves main as-is
   (the human decides revert vs. fix-forward); auto-revert is a follow-on once
   slice 1's `rollback` is proven against the merged commit. Flagged, not invented.
3. **Worktree confinement is by-convention, not OS-enforced** (cherny inherits
   this, `cherny-loop/app.yaml:82`). ship-it inherits the same caveat; a hard
   write-jail is the separately-tracked engine slice, not this story's job.

## Non-goals

- Fan-out over multiple briefs — that's `fleet.md` (slice 3).
- A new git or maker mechanism — ship-it is composition over cherny + slice-1
  git-ops.
- Any `git push` / PR — integrate is local-main only (epic non-goal).
