"""usable-kitsoki-gate — the three gate conditions + the parity threshold, as
CONSTANTS (not prose), per docs/proposals/usable-kitsoki-release-gate.md
Task 1.2.

This is the single source of truth S1 (workbench), S4 (scenario foundry), and
S6 (this arena plugin, `usable_kitsoki_gate.py`, Task 2) all read the same
number from. S1/S4 are not yet built as of this commit — until they exist in
their own language/runtime, treat this module as the canonical value and
mirror it verbatim rather than re-deriving it; docs/tracing/usable-kitsoki-gate.md
points back here for the producer-contract side.

Do not inline `90` (or any of these) anywhere else in the gate's rollup logic
— import from here so a threshold change is a one-line diff with a single
blast radius.
"""

from __future__ import annotations

from typing import Final

# ---------------------------------------------------------------------------
# Open question 1 (parity threshold X): resolved as one global floor, not a
# per-persona floor, per the proposal's own lean — "start with one global
# floor calibrated against the 20-scenario calibration set; split per-persona
# only if the calibration run shows a persona systematically dragging the
# number down for reasons unrelated to workbench quality."
#
# 90% is a PLACEHOLDER pinned to that lean, not a measured number — no
# calibration set exists yet (S4 Task 4.2, gated on S4 landing). Whoever runs
# the first calibration pass (epic §5's "20 mined Kitsoki scenarios, hand-
# checked") must revisit this constant, cite the run that justified it in the
# comment right here, and get Brad's sign-off before it gates a real release
# decision instead of a placeholder for wiring the plugin.
#
# CALIBRATION CONTACT, round 2 (S6 "no-llm-parity", supersedes the round-1
# note below): tools/arena/tests/fixtures/usable-kitsoki-gate/
# calibration-report.json now sweeps the 18-scenario calibration set against
# the THREE real `workbench:` rooms this project ships (dev-story, the
# hand-authored primary, and its thin inheritors pets-dev/slidey-dev — see
# tools/session-mining/flow_fixture_compiler.py's WORKBENCH_TARGETS), not the
# non-workbench harness stub round 1 measured against. Measured result: 162
# records (18 scenarios x 3 surfaces x 3 targets), silent_bounce_count = 0,
# misroute_adjacent_count = 0 (still hard-false — S1 does not compute this;
# see GATE_CONDITIONS doc below), worst_surface_parity_percent = 100.0% —
# ABOVE this 90.0% placeholder, so the gate PASSES on the calibration set as
# measured (unlike round 1's 0.0%, this number does not disagree with the
# threshold; nothing needed lowering).
#
# DO NOT read 100.0% as proof real workbench answer quality is perfect,
# though — flag this honestly rather than banking it: the calibration set's
# 18 scenarios are ALL non-abandoned (`abandoned: false` on every one; grep
# confirmed, none exercise the failure branch), and
# `flow_fixture_compiler.py`'s `canned_answer()` derives each stubbed
# `host.agent.task` response's content DETERMINISTICALLY FROM the scenario's
# own `expected_effects`/`abandoned` fields — the same oracle
# `workbenchGateSignal`'s real expected_effects join then checks the note
# against. For a non-abandoned scenario the stub is engineered to state its
# expected_effects verbatim, so the join is close to tautological by
# construction on this corpus; it proves the JOIN/ROLLUP/SCHEMA machinery
# and the real per-target app wiring (import-folding included) work
# end to end, and — separately, via
# internal/testrunner/flows_workbench_smoke_expected_effects_test.go's
# explicit "unsatisfied" case — that the join CAN report false when a note
# omits an expected effect. It is not yet a measurement of whether a REAL
# (LLM-driven) workbench agent's own answers would satisfy those effects; that
# would need either abandoned/near-miss scenarios added to the calibration
# corpus (to exercise the false branch through this harness, not just the
# direct testrunner test) or a live-gate run (`run_live_gate.py --live-gate`)
# driving a real agent instead of a canned cassette answer.
#
# Round 1 (Task 4.2, first no-LLM calibration run, pre-workbench-target):
# the 18-scenario calibration set measured worst_surface_parity_percent =
# 0.0% against this 90.0% placeholder — the threshold did NOT survive
# calibration contact as measured. Brad's sign-off at the time: DO NOT lower
# this constant on the strength of that number — the 0.0% was an honest
# artifact of what the no-LLM harness could drive THEN, not a workbench
# quality regression: `stories/scenario-foundry-harness`'s `desk` room (the
# only app the S4->flow-fixture compiler projected mined turns onto at that
# point) is not a `workbench:` room, so
# `internal/orchestrator/workbench_gate_signal.go` never fired for it and
# `candidate_completed` was honestly False for every one of the 54 swept
# (scenario, surface) cells. That gap is what round 2 above closes.
# ---------------------------------------------------------------------------
PARITY_THRESHOLD_PERCENT: Final[float] = 90.0

# Open question 2 (surface parity vs. worst-surface gating): resolved as
# worst-surface gating, per the proposal's lean — "a product that's
# productive on web but silently bounces on MCP is not released-ready, and
# averaging would hide exactly that." The rollup computes the parity
# percentage PER SURFACE and the cell's overall parity is the MINIMUM across
# surfaces, never an average.
WORST_SURFACE_GATING: Final[bool] = True

# The three gate conditions (Event/format model, "The three gate conditions").
# All three must hold for the rollup to pass; each maps directly to an epic
# release-blocker. Encoded as machine-checkable predicate names (not prose)
# so a rollup implementation and a test can both enumerate
# `GATE_CONDITIONS` and know they covered all three.
GATE_CONDITIONS: Final[tuple[str, ...]] = (
    "zero_silent_bounce",     # kills R2 regressions: no record has silent_bounce == true
    "zero_misroute_adjacent",  # kills R3 regressions: no record has misroute_adjacent == true
    "parity_at_or_above_threshold",  # see PARITY_THRESHOLD_PERCENT / WORST_SURFACE_GATING
)


def parity_percent(candidate_and_source_completed: int, source_completed: int) -> float:
    """count(candidate_completed AND source_completed) / count(source_completed) * 100.

    Binary completion, not a scored rubric (epic shared decision 6). Returns
    100.0 for an empty denominator (no scenarios where the source completed
    means there is nothing to have regressed on for this surface) so an
    empty-corpus cell does not spuriously fail the parity condition; callers
    that care about "no scenarios ran at all" should gate on that separately
    (health, not this metric).
    """
    if source_completed <= 0:
        return 100.0
    return 100.0 * candidate_and_source_completed / source_completed


def gate_passes(
    *,
    silent_bounce_count: int,
    misroute_adjacent_count: int,
    worst_surface_parity_percent: float,
) -> bool:
    """The rollup predicate: all three GATE_CONDITIONS must hold.

    `worst_surface_parity_percent` is expected to already be the MINIMUM
    parity percentage across surfaces when WORST_SURFACE_GATING is True (the
    caller computes per-surface `parity_percent()` values and reduces with
    `min()`; this function does not re-derive per-surface data because it has
    no access to the per-record list — see `usable_kitsoki_gate.py`'s
    rollup for that reduction).
    """
    return (
        silent_bounce_count == 0
        and misroute_adjacent_count == 0
        and worst_surface_parity_percent >= PARITY_THRESHOLD_PERCENT
    )
