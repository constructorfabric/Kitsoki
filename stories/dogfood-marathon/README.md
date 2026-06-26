# dogfood-marathon — process a backlog through an inner pipeline, report the run

A kitsoki story that drives a **backlog of cases** (tickets / bugs) through an
**inner pipeline** (`bugfix` / `delivery-tail` / `ship-it`), independently
verifies every deliverable, captures friction as findings, and bakes the run into
a **slidey report** — outcomes · effectiveness · time · cost · what was fixed ·
what worked · what didn't.

It is the drivable wrapper around the **`dogfood-marathon` agent skill**
(`.agents/skills/dogfood-marathon/SKILL.md`) and the method recorded in
`.context/bakeoff-learnings.md`. The skill is the by-hand runbook; this story
journals the per-case data into a typed world and renders the deck.

```
kitsoki run stories/dogfood-marathon/app.yaml
```

## The workflow

```
idle ──start──▶ intake ──▶ processing ⇄ (triaging → driving → verifying → recording)
                (load      (per-case        per case, then next_case re-enters
                 backlog)   checkpoint)      the checkpoint
                                │
                                │ backlog drained / budget hit
                                ▼
                          aggregating ──▶ reporting ──▶ slideshow ──▶ done
                          (roll up)       (build deck   (host.slidey.render  (gallery +
                                           spec)         → static-HTML deck)  headline)
```

| Room | Split | What it does |
|---|---|---|
| `idle` | deterministic | Confirm the inner pipeline, backlog source, baseline policy, maker profile; `start`. |
| `intake` | deterministic | `host.starlark.run` (`load_backlog`) loads the backlog into `{items:[{id,title,baseline,repro_command}]}`. Idempotent: a pre-seeded backlog passes through. |
| `processing` | deterministic | The **per-case dispatcher + checkpoint**. `next_case` picks `backlog.items[case_index]` (via `pick_case.star` — the template language can't index by a variable) and enters triage, or aggregates when drained. **One case per turn** (operator-paced, and it keeps each turn's internal emit chain under the engine's emit-depth cap). |
| `triaging` | interpretive (delegated) | ONE `host.agent.task` (`triager`) — read-only verdict `ALREADY-FIXED \| STILL-LIVE \| PARTIAL \| UNCLEAR`. `ALREADY-FIXED` cases are dropped (degenerate baseline). |
| `driving` | interpretive (delegated) | The inner pipeline driven LIVE over the case — modeled as ONE `host.agent.task` (`driver`) that a real marathon dispatches through **kitsoki-mcp-driver** (fresh per-case worktree, baseline SHA, explicit trace, scoped test_cmd). Returns the exit + worktree + trace; **does not self-grade**. |
| `verifying` | deterministic | INDEPENDENT verify — `host.starlark.run` (`verify_case`) grades on the produced deliverable (oracle pass + deliverable existence), **not** the maker's return. |
| `recording` | deterministic | `host.starlark.run` (`record_case`) appends the per-case record (triage · exit · verify · cost/tokens · time · findings) to `world.results`, then advances the loop. |
| `aggregating` | deterministic | `host.starlark.run` (`aggregate_run`) rolls up counts, cost/token/time totals, what-worked / what-didn't, the honest headline. |
| `reporting` | deterministic | `host.starlark.run` (`build_deck`) returns the report deck `{spec_path, summary}` built from the journaled run data. |
| `slideshow` | deterministic | `host.slidey.render` (`format: html`) bundles the deck → a self-contained **static-HTML slidey report**, then `host.artifacts_dir` publishes it as a media handle (kind `slideshow`). Same producer/shape as `stories/slidey-edit/rooms/rendering.yaml` (HTML, **not** mp4). |
| `done` | gallery | The rendered report + the headline rollup. |

## How it produces the slidey report

`reporting` builds the deck spec from `world.results` + `world.rollup`; `slideshow`
runs that spec through `host.slidey.render` exactly like `stories/slidey-edit`,
producing a static HTML deck emitted to `host.artifacts_dir` as `report_handle`.
A baked report deck (`baked/report.deck.json`) lets the render/flow run
deterministically; a live drive overwrites it from the journaled run data (the
slidey-edit baked-deck discipline — `ctx.fs` is read-only, so a script can't write
the deck JSON itself).

## Honesty posture — keep the rooms honest

The triage / drive / verify rooms are **thin orchestration stubs** that delegate to
the live **kitsoki-mcp-driver** agent + the operator's own oracle when driven for
real. They **never fabricate an outcome**:

- **Independent verify, oracle-gated.** `verifying` grades on the produced
  deliverable (`verify_case.star` returns `failed` for a missing deliverable),
  never on the maker's self-report — the skill's non-negotiable control. An honest
  `deliverable_present:false` is required of the driver when the maker produced
  nothing (no lookalike substitution).
- **`needs-human` parks are expected, not failures.** An un-instrumented ticket
  (no `repro_command`) parks at `needs-human` under the inner pipeline's RED→GREEN
  discipline; the maker still produces the fix, a human verifies + merges.
- Where a room delegates rather than computes, the YAML says so in a comment.

See `stories/AGENTS.md` (never paper over runtime issues) and the skill's
"Honesty controls".

## Improve WITHOUT overfitting

The point of a marathon is to **harden the inner pipeline for the general class** —
never to paper over one case. Every finding feeds a generic prompt/room/gate change
(the worked example: a blind-implementer failure on one bug drove a generic
hardening across the bugfix prompts — commit `d210ea67` — naming no case). Run the
skill's overfitting checklist on every change; a fix that names a case, helps only
these inputs, or wouldn't help an unseen bug of the same class is overfit — drop it.
Every regression added is **cassette-stubbed** (no real LLM in automated tests).

## Deterministic, no-LLM testing

```
kitsoki test flows stories/dogfood-marathon/app.yaml
```

| Fixture | Covers |
|---|---|
| `flows/happy_two_cases.yaml` | idle → intake → per-case loop ×2 (triage STILL-LIVE → drive shipped → verify solved) → aggregate → report → render → done. Every host call stubbed by invoke id; backlog seeded via `initial_world`; render points at the baked report deck. |

## Exits

| Exit | `requires:` | When |
|---|---|---|
| `done` | `report_handle` | the marathon finished and a slidey report was rendered. |
| `abandoned` | — | `quit` at idle / processing. |

## Remaining work

- **Real backlog loader.** `load_backlog.star` enumerates the source via the
  read-only `ctx.fs.glob` and normalizes filenames; it does **not** yet parse
  ticket frontmatter (title / `repro_command`) or pin each case's baseline per
  `baseline_policy` (`<fix>^` discovery). Today the operator pins baselines; wire
  real frontmatter parsing + `<fix>^` resolution.
- **Live deck authoring.** A live drive must overwrite `baked/report.deck.json`
  from the journaled `results`/`rollup` (a script can't write — `ctx.fs` is
  read-only). Wire the kitsoki-mcp-driver / a write-capable host call to
  materialize the per-run deck (richer per-case scenes, cost/time charts) instead
  of rendering the static baked scaffold.
- **Findings → tickets.** `record_case.star` carries per-case findings into
  `world.findings`, but the story does not yet file them via `issue_create`. Add a
  findings room (or a `done` action) that files the consequential ones in the
  `issues/bugs/` frontmatter shape.
- **Triage drop accounting.** A dropped `ALREADY-FIXED` case is recorded
  `skipped`, but the dispatcher still spends a triage turn on it; add a batch
  triage-only pre-pass (the skill's step 2) to filter the backlog before the
  maker loop.
- **More flow fixtures.** Add: an `ALREADY-FIXED` skip path; a `needs-human` park
  (verify `partial`); a `failed`-oracle case (deliverable absent); a budget-hit
  drain; and a `quit_at_processing` abandon. Each must stay cassette-stubbed.
- **`external_side_effect` warning.** The `driver` agent declares
  `external_side_effect: true` (correct — it drives a worktree-mutating pipeline),
  which the loader's WebFetch-only inference flags with a cosmetic WARN. Harmless;
  revisit if the inference heuristic is broadened to Edit/Write/Bash.
- **Cost/token rollup fidelity.** Totals are summed from per-case `drive_result`
  fields; wire the authoritative per-call `payload.meta.cost_usd` extraction from
  the drive's trace (the skill's gotcha #5/#6) rather than trusting the driver's
  reported numbers.
