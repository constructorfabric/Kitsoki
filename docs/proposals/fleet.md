# Story: fleet — fan ship-it over a brief list behind a merge-lock

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   story
**Epic:**   [../delivery-loop.md](delivery-loop.md) (slice 3)

## Why

`ship-it` (slice 2) delivers **one** brief. The proven session pattern fans the
loop out over **N** briefs — the `decomposition.yaml` that
[`work-decomposition.md`](work-decomposition.md) produces — with a single
**merge-lock** so integrations serialize (you cannot ff-merge two branches onto
main at once without a race, and a moved-main mid-integrate is the lost-work
hazard the epic targets). Today that fan-out is the operator running N
delegations by hand and serializing the merges in their head. fleet is the
deterministic graph for it.

work-decomposition.md already builds the *front* half (proposal → validated
`decomposition.yaml`) and explicitly **defers** "parallel autonomous fan-out
(spawn an implementer per brief, run them concurrently, auto-integrate)" as a
non-goal needing exactly this slice. fleet is that deferred half — minus the
"concurrently" for v1 (see open question #1).

## What changes

A new parent story **`stories/fleet/`** that:

1. Loads a `decomposition.yaml` brief list into `fleet_briefs` (the cyclic-graph-
   over-a-list shape `decompose`'s `board` uses over `decomp_briefs`).
2. For each brief, **dispatches `ship-it`** (import) with the brief's text +
   `gate_command` projected in, behind a `merge_lock_held` world flag so only one
   brief's `integrate`/`verify` runs at a time.
3. Marks each brief `shipped` or `needs-human` (parking the latter, continuing the
   rest), and on the last brief routes `all_done` to a summary read-out.

One sentence: **a board-shaped parent that walks a brief list, dispatching ship-it
per brief behind a merge-lock, parking failures and continuing.**

## Impact

- **Net-new:** `stories/fleet/` — ~4 rooms (`load`, `board`, `dispatch`,
  `summary`), 1 import (`ship-it`), ~4 flow fixtures.
- **Engine/host changes:** none — merge-lock is fleet-scoped world state (epic
  shared decision #4), brief-list is a cyclic graph (no new "iterate" primitive).
- **Docs on ship:** `docs/stories/fleet.md`, README queue entry.

## Reuse inventory

| Pipeline step | Mechanism | Reference |
|---|---|---|
| Load brief list | `host.run`/starlark parse `decomposition.yaml` → `fleet_briefs` | `decompose`'s `decompose_load.py` board-load (work-decomposition.md §3) |
| Iterate over briefs | cyclic graph over a world list, `board` re-pin on `next` | `decompose` `board` over `decomp_briefs` (work-decomposition.md OQ #4) |
| Deliver one brief | import `ship-it`, entry `configure` | [`ship-it.md`](ship-it.md) |
| Serialize integration | `merge_lock_held` world bool gating `dispatch` | epic shared decision #4 |
| Per-brief reset | clear carry keys so brief N+1 regenerates | `decompose` `impl__*` reset (work-decomposition.md task 3.3) |

## Story graph

```
load ── start ──▶ board ◀──────────────────────┐
 │ (parse                │ pick next pending     │ ship-it @exit
 │  decomposition.yaml    ▼                       │
 │  → fleet_briefs)    dispatch  (lock held)      │
 │                     import ship-it{brief,gate} │
 │                        ├─ exit:shipped ────────┤ mark brief shipped, release lock, next
 │                        └─ exit:needs-human ────┘ park brief (last_error stored), next
 │
 └─ all_done (no pending) ──▶ summary ──▶ @exit:done
```

`board` re-pins itself after each brief (the loader requires-check passes, the
`decompose` discipline). `@exit:done` requires `[fleet_summary]`.

## World schema (sketch)

```yaml
world:
  decomposition_path: { type: string, default: "" }      # the input YAML
  fleet_briefs:       { type: object, default: {} }       # [{id, brief, gate_command, status, last_error}]
  current_brief:      { type: object, default: {} }       # the brief being dispatched
  merge_lock_held:    { type: bool,   default: false }    # serializes integrate/verify (decision #4)
  max_concurrent:     { type: int,    default: 1 }         # v1: sequential (OQ #1)
  shipped_count:      { type: int,    default: 0 }
  parked_count:       { type: int,    default: 0 }
  fleet_summary:      { type: string, default: "" }       # final read-out
```

`exits:` — `done: { requires: [fleet_summary] }`.

## Per-room detail

### `load` — parse the decomposition into a brief list

- **`on_enter`:** `host.run` (or `host.starlark.run`) reads `decomposition_path`,
  emits `fleet_briefs` = `[{id, brief, gate_command, status:"pending"}]`. Reuses
  `decompose`'s load script shape.
- **Intent:** `start` → `board`.

### `board` — the dispatcher loop head

- **View:** a typed list of briefs with status (pending / shipped / parked) —
  the live-result-list pattern (`dev-story` design lists). Renders
  `shipped_count` / `parked_count`.
- **Routing:** guarded emits — a pending brief with `merge_lock_held == false` →
  `dispatch`; no pending → `all_done → summary`. `board` re-pins on return.

### `dispatch` — deliver one brief via ship-it (lock held)

- **`on_enter`:** set `merge_lock_held: true`, pick the next pending brief into
  `current_brief`, **reset** ship-it carry keys (so brief N+1 regenerates, not
  reuses — the `decompose` `impl__*` reset discipline).
- **Import:** `source: ../ship-it`, `entry: configure`, `world_in:` projects
  `brief = current_brief.brief`, `gate_command = current_brief.gate_command`,
  `workspace_id = current_brief.id`, `base_branch`.
- **Exit projection:**
  - `ship-it.@exit:shipped → board`, set the brief's `status="shipped"`,
    `shipped_count++`, **release** `merge_lock_held: false`.
  - `ship-it.@exit:needs-human → board`, set `status="parked"`,
    `last_error = ship-it last_error`, `parked_count++`, **release the lock**.
  - The lock is released on *both* arcs so the next brief proceeds regardless.

> **Why the lock matters:** ship-it's `integrate` rebases onto *current* main and
> `verify` re-runs the gate on the merged commit. If two briefs integrated
> concurrently, brief B could rebase onto a main that brief A is mid-merge into —
> the exact moved-main lost-work hazard. Holding `merge_lock_held` across one
> brief's `integrate`+`verify` makes each integration see a settled main. v1
> runs the *whole* ship-it under the lock (sequential); a later parallel variant
> would hold the lock only across `integrate`+`verify` while makers run
> concurrently (open question #1).

### `summary` — the fan-out read-out

- Renders `shipped_count` shipped, `parked_count` parked (with each parked
  brief's `last_error`), and the per-brief table. `fleet_summary` set; `@exit:done`.

### Net-new files

```
stories/fleet/
├── app.yaml
├── rooms/{load,board,dispatch,summary}.yaml
├── scripts/fleet_load.py      # parse decomposition.yaml → fleet_briefs
├── flows/{happy_path_two_briefs,one_parks_others_ship,lock_serializes,all_parked}.yaml
└── README.md
```

## Flow fixtures (no-LLM)

- `happy_path_two_briefs` — two briefs, both ship-it imports stubbed to
  `@exit:shipped`; both end `status="shipped"`, `shipped_count=2`, `@exit:done`.
- `one_parks_others_ship` — brief 1 ships, brief 2's ship-it returns
  `@exit:needs-human`; fleet parks brief 2 (`last_error` stored), brief 3 still
  ships; summary shows 2 shipped / 1 parked. The continue-on-failure invariant.
- `lock_serializes` — asserts `merge_lock_held` is `true` for exactly the span of
  one brief's `dispatch` and `false` between briefs (the serialization contract).
- `all_parked` — every brief returns `needs-human`; fleet completes with
  `shipped_count=0`, summary lists every error (no swallowed failures).

## Tasks

```
## 1. Scaffold
- [ ] 1.1 app.yaml: import ship-it, world schema, root=load, exits
- [ ] 1.2 rooms/{load,board,dispatch,summary}.yaml; scripts/fleet_load.py

## 2. Wire the loop
- [ ] 2.1 load: parse decomposition.yaml → fleet_briefs
- [ ] 2.2 dispatch: merge_lock_held set/release, per-brief reset, ship-it import + both exit projections
- [ ] 2.3 board: pending-pick guarded emits, all_done → summary

## 3. Lock the graph
- [ ] 3.1 Probe rooms with kitsoki turn
- [ ] 3.2 Flows: happy_path_two_briefs, one_parks_others_ship, lock_serializes, all_parked

## 4. Live + document
- [ ] 4.1 kitsoki run against a real 2-brief decomposition.yaml (ship-it makers mocked or a tiny real gate)
- [ ] 4.2 README (entry, exits, the decomposition input contract, the lock semantics)
- [ ] 4.3 Migrate to docs/stories/fleet.md; close work-decomposition.md's deferred fan-out non-goal; trim/delete this child
```

## Open questions

1. **Sequential vs. truly-parallel makers.** v1 is sequential (whole ship-it
   under the lock). True parallelism (N makers concurrently, lock only across
   integrate+verify) needs the background-job runner + write/git sandboxing from
   `project_execution_modes_gate_deciders` / `task-fs-sandbox.md` and hits
   cherny's `EmitIntentMaxDepth` autonomy cap (`cherny-loop/app.yaml:13`).
   *Lean:* ship v1 sequential — correct, no-LLM-gateable, matches
   work-decomposition.md's own v1 stance; parallel is a follow-on. **Flagged, not
   invented.**
2. **Lock granularity if/when parallel.** Hold the lock only across
   `integrate`+`verify`, not the maker, so makers overlap. Deferred to the
   parallel follow-on (#1).
3. **Resume after a crash.** Should fleet persist `fleet_briefs` status so a
   re-run skips already-shipped briefs? *Lean:* yes — write status back to the
   `decomposition.yaml` (or a sidecar) so fleet is restartable, mirroring
   cherny's restartable artifact trail. Defer the persistence format to impl.

## Non-goals

- Authoring or validating the decomposition — that's
  [`work-decomposition.md`](work-decomposition.md); fleet consumes its output.
- Truly-concurrent makers (open question #1 — a follow-on).
- Any `git push` / remote — every integrate is local-main (epic non-goal).
