#!/usr/bin/env python3
"""Golden regression fixtures for the usable-kitsoki-gate rollup (Task 4.1).

Offline proof the gate has teeth: three hand-written parity-verdict-record
bundles under tests/fixtures/usable-kitsoki-gate/, each identical to the
`clean-pass.json` baseline except for exactly one scripted violation --

  - `scripted-silent-bounce.json`      -- one record with silent_bounce=true
  - `scripted-misroute-adjacent.json`  -- one record with misroute_adjacent=true
  - `scripted-parity-miss.json`        -- one surface (mcp) dropped to 70%
                                           parity while the flat aggregate
                                           across all records stays at 90.0%,
                                           which specifically proves the
                                           rollup gates on the WORST surface
                                           (min), not the average

No S1 (workbench) or S4 (scenario foundry) involved: these are hand-scripted
parity records read straight off disk through the plugin's real `score()`
entry point (same `[usable-kitsoki-gate] wrote <path>` stdout-pointer contract
production would use), never fabricated verdicts and never a live LLM call.

Each test proves the golden independently FLIPS the rollup from the
clean-pass baseline's `solved` to `failed`, and that the clean baseline itself
passes. If a future change to `_rollup_from_records` / `gate_passes` /
`GATE_CONDITIONS` regresses any of the three gate conditions, exactly one of
these fixtures fails without needing S1/S4 to exist.
"""

from __future__ import annotations

import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE.parent))

from arena.model import JobSpec  # noqa: E402
from arena.plugins import base as plugins  # noqa: E402

FIXTURES_DIR = HERE / "fixtures" / "usable-kitsoki-gate"

failures: list[str] = []


def check(label: str, got, want) -> None:
    if got != want:
        failures.append(f"{label}: got {got!r}, want {want!r}")


def check_true(label: str, cond: bool, detail: str = "") -> None:
    if not cond:
        failures.append(f"{label}: expected true{f' ({detail})' if detail else ''}")


plugin = plugins.get("usable-kitsoki-gate")

spec = JobSpec.from_dict({
    "job_type": "usable-kitsoki-gate",
    "targets": [{"id": "kitsoki", "label": "kitsoki workbench"}],
    "variants": [{"id": "gate-v1", "backend": "replay"}],
    "axes": {"surface": ["web"], "persona": ["core-maintainer"]},
    "placement": {"hosts": ["local"], "concurrency": 1, "retry": 0},
})
cell = spec.cells()[0]


def score_fixture(name: str):
    path = FIXTURES_DIR / f"{name}.json"
    check_true(f"{name}.json exists on disk", path.exists(), str(path))
    stdout = f"[usable-kitsoki-gate] wrote {path} (30 records)\n"
    return plugin.score(cell, exit_code=0, stdout=stdout, stderr="")


# ---- 1. The clean baseline passes -------------------------------------------

clean = score_fixture("clean-pass")
check("clean-pass.json: verdict is solved", clean.verdict, "solved")
check("clean-pass.json: health is model:result", clean.health, "model:result")
check("clean-pass.json: record_count is 30 (3 surfaces x 10 scenarios)", clean.metrics["record_count"], 30)
check("clean-pass.json: silent_bounce_count is 0", clean.metrics["silent_bounce_count"], 0)
check("clean-pass.json: misroute_adjacent_count is 0", clean.metrics["misroute_adjacent_count"], 0)
check("clean-pass.json: overall parity is 100.0%", clean.metrics["parity_percent"], 100.0)
check("clean-pass.json: worst-surface parity is 100.0%", clean.metrics["worst_surface_parity_percent"], 100.0)

# ---- 2. Scripted silent bounce flips clean-pass's solved -> failed ---------

bounced = score_fixture("scripted-silent-bounce")
check("scripted-silent-bounce.json: verdict flips to failed", bounced.verdict, "failed")
check("scripted-silent-bounce.json: exactly one bounce counted", bounced.metrics["silent_bounce_count"], 1)
check("scripted-silent-bounce.json: misroute_adjacent_count stays 0 (isolated)", bounced.metrics["misroute_adjacent_count"], 0)
check("scripted-silent-bounce.json: parity is still 100% (isolated)", bounced.metrics["worst_surface_parity_percent"], 100.0)
check_true(
    "scripted-silent-bounce.json: the ONLY diff from clean-pass is the bounce condition",
    bounced.metrics["silent_bounce_count"] != clean.metrics["silent_bounce_count"]
    and bounced.metrics["misroute_adjacent_count"] == clean.metrics["misroute_adjacent_count"]
    and bounced.metrics["worst_surface_parity_percent"] == clean.metrics["worst_surface_parity_percent"],
)

# ---- 3. Scripted misroute-adjacent flips clean-pass's solved -> failed -----

misrouted = score_fixture("scripted-misroute-adjacent")
check("scripted-misroute-adjacent.json: verdict flips to failed", misrouted.verdict, "failed")
check("scripted-misroute-adjacent.json: exactly one misroute counted", misrouted.metrics["misroute_adjacent_count"], 1)
check("scripted-misroute-adjacent.json: silent_bounce_count stays 0 (isolated)", misrouted.metrics["silent_bounce_count"], 0)
check("scripted-misroute-adjacent.json: parity is still 100% (isolated)", misrouted.metrics["worst_surface_parity_percent"], 100.0)
check_true(
    "scripted-misroute-adjacent.json: the ONLY diff from clean-pass is the misroute condition",
    misrouted.metrics["misroute_adjacent_count"] != clean.metrics["misroute_adjacent_count"]
    and misrouted.metrics["silent_bounce_count"] == clean.metrics["silent_bounce_count"]
    and misrouted.metrics["worst_surface_parity_percent"] == clean.metrics["worst_surface_parity_percent"],
)

# ---- 4. Scripted parity miss flips clean-pass's solved -> failed, and -----
#         specifically proves WORST-SURFACE gating (not a flat average) ----

parity_missed = score_fixture("scripted-parity-miss")
check("scripted-parity-miss.json: verdict flips to failed", parity_missed.verdict, "failed")
check("scripted-parity-miss.json: silent_bounce_count stays 0 (isolated)", parity_missed.metrics["silent_bounce_count"], 0)
check("scripted-parity-miss.json: misroute_adjacent_count stays 0 (isolated)", parity_missed.metrics["misroute_adjacent_count"], 0)
check(
    "scripted-parity-miss.json: the flat aggregate across all 30 records is 90.0% -- "
    "at the threshold, i.e. it would PASS a naive average-based check",
    parity_missed.metrics["parity_percent"],
    90.0,
)
check(
    "scripted-parity-miss.json: mcp's per-surface parity is 70.0% (7 of 10 completed)",
    parity_missed.metrics["per_surface_parity_percent"]["mcp"],
    70.0,
)
check(
    "scripted-parity-miss.json: web and tui stay at 100% (isolated to mcp)",
    (parity_missed.metrics["per_surface_parity_percent"]["web"], parity_missed.metrics["per_surface_parity_percent"]["tui"]),
    (100.0, 100.0),
)
check(
    "scripted-parity-miss.json: worst-surface parity (70.0%, mcp) is what the rollup "
    "actually gates on -- below the 90% threshold even though the flat aggregate "
    "(90.0%) would have passed",
    parity_missed.metrics["worst_surface_parity_percent"],
    70.0,
)
check_true(
    "scripted-parity-miss.json: proves worst-surface gating has teeth -- the aggregate "
    "alone (90.0%) sits AT the threshold and would wrongly pass; only the per-surface "
    "min (70.0%) correctly fails this bundle",
    parity_missed.metrics["parity_percent"] >= 90.0 and parity_missed.metrics["worst_surface_parity_percent"] < 90.0,
)

if failures:
    print("FAIL: usable-kitsoki-gate golden regression fixtures")
    for failure in failures:
        print(f"  - {failure}")
    sys.exit(1)
print(
    "PASS: usable-kitsoki-gate golden regression fixtures "
    "(clean-pass solves; scripted silent-bounce/misroute-adjacent/parity-miss each "
    "independently flip the rollup to failed; parity-miss specifically proves "
    "worst-surface, not average, gating)"
)
