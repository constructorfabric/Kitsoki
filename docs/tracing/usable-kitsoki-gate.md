# Tracing: usable-kitsoki release gate

**Status:** Shipped (S6 of the `usable-kitsoki.md` epic). Tasks 1 (parity-metric
spec), 2 (plugin skeleton), 3.1 + 3.2 (wire the real scenario corpus + S1
completion signal), 3.3's no-LLM half (bounded-concurrency flow-replay
harness for the tui/mcp surfaces), 3.3's live half
(`tools/usable-kitsoki-gate/run_live_gate.py`, a real agent driving
`stories/dev-story`'s real `workbench:` room — double-gated behind
`arena run --live` plus that script's own `--live-gate` argv flag, never
executed automatically), 4.1 (golden regression fixtures), 4.2
(calibration-set run, checked in and diffed byte-for-byte in CI), 5.1 + 5.2
(`.github/workflows/usable-kitsoki-gate.yml` — a cassette-only no-LLM CI job
path-filtered on the S1/S2/S4/S5 code, plus a release-candidate live-gate
job whose TRIGGER routing is real — `rc-*` tag / explicit
`workflow_dispatch` confirmation only — but whose actual credential/image
arming remains deliberate operator follow-up), and 5.3 (this doc) are all
landed and tested, zero LLM spend in CI. The one deliberately gated
remainder is Task 3.3's real browser-driven web-surface harness
(`tests/playwright/usable-kitsoki-gate-web.spec.ts`), which does not exist
yet — separately scoped, larger, browser-specific follow-up, tracked as an
honest gap rather than blocking this slice.

**Honesty note on what "shipped" means here:** every piece of this gate
(schema, constants, plugin, both no-LLM harnesses, the live harness, CI
wiring) is real, tested, and zero-LLM-spend in CI. The no-LLM path now drives
the real workbench-target projection (S6 "no-llm-parity", epic finalization):
the checked-in calibration report sweeps the 18-scenario calibration set
against all THREE real `workbench:` rooms this project ships
(`stories/dev-story`, and its thin inheritors `stories/pets-dev` /
`stories/slidey-dev` — see `tools/session-mining/flow_fixture_compiler.py`'s
`WORKBENCH_TARGETS`), not the non-workbench harness stub round 1 measured
against. Measured: `worst_surface_parity_percent = 100.0%` (162 records —
18 scenarios x 3 surfaces x 3 targets), `silent_bounce_count = 0`,
`misroute_adjacent_count = 0` (still hard-false — S1 does not compute this).
Read this with its own caveat, not as a clean bill of health: the
calibration corpus's 18 scenarios are all non-abandoned, and its stubbed
`host.agent.task` answers are DERIVED FROM the same `expected_effects` the
engine-side join then checks against — this proves the join/rollup/schema
machinery and the real per-target app wiring (import-folding included) work
end to end, not that a real LLM-driven workbench agent's own answers would
satisfy those effects (see `usable_kitsoki_gate_constants.py`'s calibration-
contact note for the full caveat). A LIVE green run of this gate over
`stories/dev-story` with a real agent (not a canned cassette) was the
epic's own release-readiness bar for that stronger claim — that run has now
happened (epic finalization, one deliberate `run_live_gate.py --live-gate`
pass over a bounded 6-cell manifest — 2 calibration scenarios x all 3 real
`workbench:` targets, `mcp` surface). Result: **`dev-story` is 2/2 real
green** (zero silent bounce, zero misroute, real `opus` dispatches that
correctly stayed read-only on a mutating "commit it" ask and routed it to
the gitops hub instead of fabricating completion) — the live claim this
section used to only aspire to is now backed by a real run, for `dev-story`
specifically. `pets-dev`/`slidey-dev` did NOT get a real signal in this run
(a reproducible "session opens, no turn ever drives" anomaly, ruled out as
a stale-binary or app-load problem but not yet root-caused — a live-gate
harness gap, not an observed workbench defect) and their own live sign-off
is still open. Full per-cell detail, cost, and the anomaly writeup:
[`tools/arena/tests/fixtures/usable-kitsoki-gate/live-run-summary.md`](../../tools/arena/tests/fixtures/usable-kitsoki-gate/live-run-summary.md).
This live path is still never run automatically, only ever behind
`run_live_gate.py --live-gate` / `run_live_calibration.py --live-gate`.

This is the day-one contract `S1` (the free-form workbench) develops
against, and the schema `S6` (`tools/arena/arena/plugins/usable_kitsoki_gate.py`)
scores against. Nothing here is prose-only: the schema and the gate
constants are checked-in, importable artifacts, not just this description of
them.

## Parity verdict record

Schema:
[`tools/arena/arena/plugins/usable_kitsoki_gate_schema.json`](../../tools/arena/arena/plugins/usable_kitsoki_gate_schema.json)
(versioned, draft-07, `$id` ends in `/v1.0.0`).

One record per scenario x persona x surface, at the end of a
`usable-kitsoki-gate` arena cell:

```jsonc
{
  "schema_version": "1.0.0",
  "scenario_id": "scn-git-ops-0007",
  "persona": "impatient-debugger",
  "surface": "web",
  "source_completed": true,
  "candidate_completed": false,
  "silent_bounce": false,
  "misroute_adjacent": true,
  "evidence_refs": [
    ".artifacts/usable-kitsoki-gate/<run_id>/<scenario_id>/trace.jsonl",
    ".artifacts/usable-kitsoki-gate/<run_id>/<scenario_id>/rrweb.json"
  ],
  "notes": "workbench asked a clarifying question the source session never needed"
}
```

**`scenario_id` naming note:** the release-gate proposal's own worked example
writes `scenario_id` where S4's scenario IR (`docs/proposals/scenario-foundry.md`)
names the same concept `id` (e.g. `"scn-git-ops-0007"`). This is resolved as
a documented **alias, not a second identity scheme**: `scenario_id :=
IR.id`, verbatim. Do not derive a different string (e.g. from
`provenance.session_id` + a turn offset) — join a parity record back to its
scenario by exact string equality on this field.

This record is job-type-specific evidence alongside the job-agnostic
`CellResult` (`schemas/completion-state.schema.json`) every other arena
plugin scores through — the cell's aggregate rollup still produces a
`verdict`/`health`/`metrics` grade the same way `swarm.py`'s
`_completion_from_swarm_results` does; this record is what an aggregate rule
reduces over (see "Gate conditions" below).

## Gate conditions and parity threshold

Constants (not prose):
[`tools/arena/arena/plugins/usable_kitsoki_gate_constants.py`](../../tools/arena/arena/plugins/usable_kitsoki_gate_constants.py).

- `GATE_CONDITIONS` — the three named conditions, all of which must hold for
  a cell to pass: `zero_silent_bounce`, `zero_misroute_adjacent`,
  `parity_at_or_above_threshold`.
- `PARITY_THRESHOLD_PERCENT = 90.0` — the initial global floor (open
  question 1's lean: one global floor, calibrated later against S4's
  20-scenario calibration set; split per-persona only if that run shows a
  persona systematically dragging the number down for reasons unrelated to
  workbench quality). **This is a placeholder pending calibration** — see the
  constant's own docstring for the sign-off Brad needs to give before it
  gates a real release decision.
- `WORST_SURFACE_GATING = True` — open question 2's lean: parity is computed
  per surface (web / TUI / MCP) and the cell's overall parity is the
  **minimum** across surfaces, never an average. A product that's
  productive on web but silently bounces on MCP is not released-ready.
- `parity_percent()` / `gate_passes()` — the two pure functions the rollup
  calls; both take counts, not records, so `usable_kitsoki_gate.py`'s
  rollup (Task 2) owns reducing the per-scenario records into those counts.

## Producer contract — what S1 must emit

S1 (the free-form workbench, `internal/orchestrator/workbench_gate_signal.go`)
emits, **per scenario turn**, a machine-readable completion signal — ideally
keyed to the scenario IR's `expected_effects` list
(`docs/proposals/scenario-foundry.md`'s IR shape). This is the *consumer*
shape the parity scorer needs.

As landed, S1 emits a real `expected_effects`-coverage join for point 1 below
when the dispatching room's world carries the scenario's `expected_effects`
list under the `<noteKey>_expected_effects` convention key (`candidate_completed`
is then true iff dispatch didn't take its `on_error` redirect AND every
expected effect is found, case-insensitively, as a substring of the
workbench's own bound close-out note); on any turn without that world var
(the overwhelming majority — ordinary workbench use, and every pre-S6
fixture) it falls back to the narrower `candidate_completed := !dispatchFailed`
proxy, unchanged. `misroute_adjacent` (point 3) is still hard-coded `false`
rather than computed — a documented, honest gap in that file's own HONESTY
NOTE, not a silently-wrong value. `usable_kitsoki_gate
.py`'s `build_parity_record` (Task 3.2) performs the join `score()` can do
today from that proxy (`source_completed` off the scenario IR's own
`abandoned` field, `candidate_completed`/`silent_bounce` reduced across
turns) and calls out — in the record's own `notes` — exactly which of the
three points below the record does NOT yet cover, rather than
overclaiming.

Concretely, for each turn kitsoki drives against a mined scenario, S1's
trace must let a downstream reader answer, without re-running an LLM judge:

1. **Which `expected_effects` (if any) fired for this turn** — the effect
   names/predicates from the scenario IR that this turn's actions actually
   satisfied. `candidate_completed` for the scenario as a whole is derived
   from whether the full `expected_effects` set was covered by the run, the
   same way `source_completed` is read off the mined session's own
   `outcomes.py` satisfaction signal (never re-adjudicated by an LLM at gate
   time — S1 emits a fact, not an opinion).
2. **Whether an `on_error` arc fired with no rendered explanation** — the
   raw signal `silent_bounce` is computed from. S2 (the never-silent
   runtime) is what should make this always `false`; this gate is S2's
   regression test at scale.
3. **Which command/room this turn actually routed to, vs. the scenario's
   expected command** — the raw signal `misroute_adjacent` is computed from
   (true when the workbench routed to a command *adjacent* to the ask, not
   the ask itself).

The trace-event shape is pinned down: an existing `turn.end` trace event's
`payload` gains an additional `usable_kitsoki_gate` key (no new event kind —
`{"turn":N, "seq":N, "ts":..., "kind":"turn.end", "payload":{
"usable_kitsoki_gate": {"candidate_completed":bool, "silent_bounce":bool,
"misroute_adjacent":bool, "evidence_refs":[]}}}`, `internal/store/event.go`'s
`TurnEnded` JSONL shape). Point 1 is now computed for real wherever a
workbench-driving harness threads a scenario's `expected_effects` into the
room's world/context (the workbench-target no-LLM harness, and
`flow_fixture_compiler.py`'s real-workbench projection, both do this). Point
3 (`misroute_adjacent`) remains a documented, honest gap — still hard-false,
not computed.

## Determinism

No-LLM path replays every mined scenario through existing zero-spend
harnesses (flow fixtures / cassettes, swarm tier 1/2); this is what
Task 2.3's plugin tests and Task 4.1's golden regression scenarios prove
without touching a real workbench or an LLM. The live path (real workbench
turns, `tools/usable-kitsoki-gate/run_live_gate.py`) is a separate, gated,
release-candidate-cadence run: double-gated (`arena run --live` at the top
level, that script's own `--live-gate` argv flag with no env fallback,
mirroring `tools/swarm/tiers/liveExplorerCli.ts`), never invoked by any
test in this repo, and never triggered by CI except an explicit `rc-*` tag
push or a `workflow_dispatch` with `confirm_live: yes`
(`.github/workflows/usable-kitsoki-gate.yml`) — see the proposal's own
"Determinism" section for the full split; nothing about the schema or
constants above changes between the two paths, only what produces
`candidate_completed`.

## Plugin usage

`usable-kitsoki-gate` is a fourth `arena` job type, registered alongside
`bugfix`/`persona-qa`/`swarm` (`arena plugins` lists it;
`tools/arena/arena/plugins/usable_kitsoki_gate.py`). One cell drives one
scenario x surface combination; a spec's `targets_from` points at S4's
scenario IR corpus directory (default `tools/session-mining/calibration/`,
the committed calibration set) and `arena.model
.load_targets_from_corpus`'s directory branch turns each scenario IR
document into a `Target`, crossed with `axes.surface` — `arena plan --spec
tools/arena/specs/usable-kitsoki-gate-calibration.yaml` enumerates 18 x 3 =
54 cells (`tools/arena/README.md`'s "Status — usable-kitsoki-gate job type
registered" section has the full `image()`/`drive_command()`/`score()`
walkthrough). With no scenario corpus configured, `arena plan` returns zero
cells, not an error. Two of the three concrete harness entry points now
exist: `tools/usable-kitsoki-gate/run_tui_gate.py` and `run_mcp_gate.py`
drive a real `kitsoki test flows` replay of each scenario's compiled flow
fixture and join the resulting trace via this plugin's own
`extract_turn_signals`/`build_parity_record` (see
`tools/usable-kitsoki-gate/flow_gate_runner.py`'s module docstring: by
default this now replays the real workbench-target projection —
`stories/dev-story`/`pets-dev`/`slidey-dev`'s real `workbench:` rooms — so
`candidate_completed` is a real engine-side join, not a harness-stub gap;
the older non-workbench `stories/scenario-foundry-harness` stub still
exists as an explicit back-compat/schema-proving path, not the default).
`tests/playwright/usable-kitsoki-gate-web.spec.ts` (the real
browser-driven web surface) still doesn't exist — separately gated,
larger, browser-specific work. The plugin, its two landed no-LLM harnesses,
and the calibration sweep can all be exercised today through no-LLM tests
(`tools/arena/tests/test_usable_kitsoki_gate_plugin.py`,
`tools/arena/tests/test_usable_kitsoki_gate_corpus.py`,
`tools/arena/tests/test_usable_kitsoki_gate_schema.py`,
`tools/arena/tests/test_usable_kitsoki_gate_golden_fixtures.py`,
`tools/arena/tests/test_usable_kitsoki_gate_calibration.py`), all of
which run with zero docker and zero LLM spend.
