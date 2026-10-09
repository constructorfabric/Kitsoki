#!/usr/bin/env python3
"""No-LLM, no-docker test for the usable-kitsoki-gate parity-spec (Task 1).

Covers:
  1. the parity verdict record schema
     (arena/plugins/usable_kitsoki_gate_schema.json) validates the
     proposal's own worked example record
     (docs/proposals/usable-kitsoki-release-gate.md, Event/format model),
     both as-given and with the `schema_version` field this schema requires.
  2. the schema REJECTS records missing a required field, and rejects an
     unknown `surface` value — proving the schema has teeth, not just a
     shape that happens to accept everything.
  3. `usable_kitsoki_gate_constants.py`'s three gate conditions + threshold
     + worst-surface-gating constants are wired correctly:
     parity_percent()/gate_passes() behave on the boundary and on each of
     the three gate conditions independently (mirrors the "prove the gate
     has teeth" discipline Task 4.1 uses at the plugin level).

Never imports docker/Playwright/an LLM client.
"""

from __future__ import annotations

import copy
import json
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
ARENA_DIR = HERE.parent
sys.path.insert(0, str(ARENA_DIR))

from arena.plugins import usable_kitsoki_gate_constants as gate_constants  # noqa: E402

SCHEMA_PATH = ARENA_DIR / "arena" / "plugins" / "usable_kitsoki_gate_schema.json"

failures: list[str] = []


def check(label: str, got, want) -> None:
    if got != want:
        failures.append(f"{label}: got {got!r}, want {want!r}")


def check_true(label: str, cond: bool, detail: str = "") -> None:
    if not cond:
        failures.append(f"{label}: expected true{f' ({detail})' if detail else ''}")


schema = json.loads(SCHEMA_PATH.read_text(encoding="utf-8"))

try:
    import jsonschema

    validator = jsonschema.Draft7Validator(schema)
except ImportError:  # pragma: no cover - advisory only, mirrors test_completion_state.py
    jsonschema = None
    validator = None


def assert_conforms(label: str, instance: dict) -> None:
    if validator is None:
        return
    errors = sorted(validator.iter_errors(instance), key=lambda e: e.path)
    if errors:
        failures.append(f"{label}: schema violations: {[e.message for e in errors]}")


def assert_rejects(label: str, instance: dict) -> None:
    if validator is None:
        return
    errors = list(validator.iter_errors(instance))
    if not errors:
        failures.append(f"{label}: expected schema violation, got none")


# ---------------------------------------------------------------------------
# 1. The proposal's own worked example
# (docs/proposals/usable-kitsoki-release-gate.md, Event/format model)
# validates. The proposal's prose example predates schema_version (this
# task's own addition); add it explicitly rather than silently accepting a
# record without one.
# ---------------------------------------------------------------------------
PROPOSAL_EXAMPLE = {
    "scenario_id": "sess-3f2a1c-turn-12",
    "persona": "impatient-debugger",
    "surface": "web",
    "source_completed": True,
    "candidate_completed": False,
    "silent_bounce": False,
    "misroute_adjacent": True,
    "evidence_refs": [
        ".artifacts/usable-kitsoki-gate/<run_id>/<scenario_id>/trace.jsonl",
        ".artifacts/usable-kitsoki-gate/<run_id>/<scenario_id>/rrweb.json",
    ],
    "notes": "workbench asked a clarifying question the source session never needed",
}

example_with_version = dict(PROPOSAL_EXAMPLE, schema_version="1.0.0")
assert_conforms("proposal's example record (+schema_version) conforms", example_with_version)

# The doc's own worked example (docs/tracing/usable-kitsoki-gate.md) uses S4's
# `id`-shaped scenario_id convention (scn-git-ops-0007) rather than the
# proposal's session-derived string — both are valid strings, the schema
# doesn't (and shouldn't) constrain the format, only that scenario_id is the
# join key.
doc_example = dict(example_with_version, scenario_id="scn-git-ops-0007")
assert_conforms("doc's example record conforms", doc_example)

if jsonschema is None:
    print(
        "NOTE: jsonschema not installed - schema-conformance checks were "
        "skipped, not failed (pip install jsonschema to enforce them)."
    )

# ---------------------------------------------------------------------------
# 2. The schema has teeth: missing required fields and bad enums are rejected.
# ---------------------------------------------------------------------------
missing_evidence = copy.deepcopy(example_with_version)
del missing_evidence["evidence_refs"]
assert_rejects("record missing evidence_refs is rejected", missing_evidence)

missing_scenario_id = copy.deepcopy(example_with_version)
del missing_scenario_id["scenario_id"]
assert_rejects("record missing scenario_id is rejected", missing_scenario_id)

bad_surface = dict(example_with_version, surface="desktop-app")
assert_rejects("record with an unknown surface is rejected", bad_surface)

empty_evidence = dict(example_with_version, evidence_refs=[])
assert_rejects("record with empty evidence_refs is rejected", empty_evidence)

extra_field = dict(example_with_version, unexpected_field="nope")
assert_rejects("record with an undeclared field is rejected (additionalProperties: false)", extra_field)


# ---------------------------------------------------------------------------
# 3. Gate constants: the three conditions + threshold + worst-surface gating.
# ---------------------------------------------------------------------------
check(
    "GATE_CONDITIONS names all three epic release-blockers",
    gate_constants.GATE_CONDITIONS,
    ("zero_silent_bounce", "zero_misroute_adjacent", "parity_at_or_above_threshold"),
)
check("PARITY_THRESHOLD_PERCENT is the proposal's open-question-1 lean (90%)", gate_constants.PARITY_THRESHOLD_PERCENT, 90.0)
check_true("WORST_SURFACE_GATING is the proposal's open-question-2 lean (gate on worst surface)", gate_constants.WORST_SURFACE_GATING is True)

# parity_percent(): binary completion ratio, empty-denominator convention.
check("parity_percent: full parity", gate_constants.parity_percent(10, 10), 100.0)
check("parity_percent: at the threshold boundary", gate_constants.parity_percent(9, 10), 90.0)
check("parity_percent: below the threshold", gate_constants.parity_percent(8, 10), 80.0)
check("parity_percent: empty denominator does not spuriously fail", gate_constants.parity_percent(0, 0), 100.0)

# gate_passes(): each of the three conditions independently flips PASS->FAIL.
check_true(
    "gate_passes: all three conditions clean -> True",
    gate_constants.gate_passes(silent_bounce_count=0, misroute_adjacent_count=0, worst_surface_parity_percent=95.0),
)
check_true(
    "gate_passes: exactly at threshold -> True (>=, not >)",
    gate_constants.gate_passes(silent_bounce_count=0, misroute_adjacent_count=0, worst_surface_parity_percent=90.0),
)
check(
    "gate_passes: one silent bounce -> False",
    gate_constants.gate_passes(silent_bounce_count=1, misroute_adjacent_count=0, worst_surface_parity_percent=100.0),
    False,
)
check(
    "gate_passes: one misroute -> False",
    gate_constants.gate_passes(silent_bounce_count=0, misroute_adjacent_count=1, worst_surface_parity_percent=100.0),
    False,
)
check(
    "gate_passes: parity just under threshold -> False",
    gate_constants.gate_passes(silent_bounce_count=0, misroute_adjacent_count=0, worst_surface_parity_percent=89.9),
    False,
)


# ---------------------------------------------------------------------------
if failures:
    print(f"FAILED ({len(failures)}):")
    for f in failures:
        print(f"  - {f}")
    sys.exit(1)
print("OK: usable-kitsoki-gate parity schema + gate constants (Task 1) all checks passed.")
