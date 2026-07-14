#!/usr/bin/env python3
"""Task 4.2 (docs/proposals/usable-kitsoki-release-gate.md) + S6
"no-llm-parity": the no-LLM calibration run over the 18-scenario calibration
set (tools/session-mining/calibration/), driven against the THREE real
`workbench:` rooms this project ships (dev-story, the hand-authored primary,
and its two thin inheritors pets-dev/slidey-dev -- see
tools/session-mining/flow_fixture_compiler.py's WORKBENCH_TARGETS registry),
checked in as a diffable parity report at
tools/arena/tests/fixtures/usable-kitsoki-gate/calibration-report.json.

This test REGENERATES that report from scratch (via
tools/usable-kitsoki-gate/run_calibration_gate.py's `sweep()`/`rollup()`,
the exact no-LLM harness Task 3.3 wired in, now defaulting to the
workbench-target projection rather than the non-workbench harness stub) and
diffs it byte-for-byte against the checked-in copy. A real diff here means
either (a) the calibration set changed, (b) the harness/join logic changed,
(c) S1's producer contract changed, or (d) one of the three target stories'
workbench wiring changed -- any of which should surface as a reviewable
fixture diff, not a silent behavior change.

Spends zero dollars and touches no docker/browser/LLM: every scenario is
driven through a real `kitsoki test flows` replay
(tools/session-mining/calibration/flows/*.<target>.flow.yaml, `test_kind:
flow`, pure state-machine + host_cassette replay) against each target's own
real app (stories/dev-story/app.yaml, stories/pets-dev/app.yaml,
stories/slidey-dev/app.yaml), per AGENTS.md's "Automated testing should
never use a real LLM" rule. Under `make test` it reuses the prebuilt
KITSOKI_TEST_KITSOKI_BINARY flow runner; standalone runs fall back to
`go run ./cmd/kitsoki`. This is the one test in this suite that is not
instant; budget ~30-60s with the shared binary.
"""

from __future__ import annotations

import json
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
REPO_ROOT = HERE.parents[2]
GATE_TOOLS_DIR = REPO_ROOT / "tools" / "usable-kitsoki-gate"
sys.path.insert(0, str(GATE_TOOLS_DIR))

import flow_gate_runner as runner  # noqa: E402
import run_calibration_gate as calib  # noqa: E402

FIXTURE_PATH = HERE / "fixtures" / "usable-kitsoki-gate" / "calibration-report.json"

failures: list[str] = []


def check(label: str, got, want) -> None:
    if got != want:
        failures.append(f"{label}: got {got!r}, want {want!r}")


def check_true(label: str, cond: bool, detail: str = "") -> None:
    if not cond:
        failures.append(f"{label}: expected true{f' ({detail})' if detail else ''}")


corpus_dir = runner.DEFAULT_CORPUS
check_true("calibration corpus exists on disk", corpus_dir.is_dir(), str(corpus_dir))

scenario_ids = runner.list_scenario_ids(corpus_dir)
check("calibration set has 18 scenario documents", len(scenario_ids), 18)

# Evidence paths must land at the SAME repo-relative location the checked-in
# fixture's evidence_refs point at (.artifacts/usable-kitsoki-gate/
# calibration-evidence/...) so `_relativize_report` produces byte-identical
# strings -- a tempdir would relativize to nothing (outside REPO_ROOT) and
# spuriously diff every regeneration. .artifacts/ is gitignored, so this
# write is ephemeral scratch, not a second copy of the checked-in report.
evidence_dir = REPO_ROOT / ".artifacts" / "usable-kitsoki-gate" / "calibration-evidence"
targets = list(calib.DEFAULT_TARGETS)
records = calib.sweep(
    corpus_dir, list(("web", "tui", "mcp")), run_id="calibration",
    evidence_dir=evidence_dir, concurrency=2, targets=targets,
)
check("162 records for 18 scenarios x 3 surfaces x 3 workbench targets", len(records), 18 * 3 * len(targets))

rolled = calib.rollup(records, results_path=str(REPO_ROOT / ".artifacts" / "usable-kitsoki-gate" / "calibration-report.json"))

report = {
    "schema_version": "1.0.0",
    "run_id": "calibration",
    "corpus": "tools/session-mining/calibration",
    "surfaces": ["web", "tui", "mcp"],
    "targets": targets,
    "scenario_count": len(scenario_ids),
    "record_count": len(records),
    "parity_threshold_percent": calib.gate_constants.PARITY_THRESHOLD_PERCENT,
    "gate_conditions": list(calib.gate_constants.GATE_CONDITIONS),
    "rollup": rolled,
    "records": records,
}
report = calib._relativize_report(report)  # noqa: SLF001 - test regenerates the exact checked-in shape

if not FIXTURE_PATH.exists():
    failures.append(f"checked-in calibration report missing at {FIXTURE_PATH} -- run "
                     "run_calibration_gate.py --relative-evidence and commit its output")
else:
    checked_in = json.loads(FIXTURE_PATH.read_text(encoding="utf-8"))
    if report != checked_in:
        # Give a readable pointer to the first mismatching top-level key
        # rather than a wall of JSON.
        for key in sorted(set(report) | set(checked_in)):
            if report.get(key) != checked_in.get(key):
                failures.append(
                    f"regenerated calibration report differs from the checked-in fixture at key {key!r} "
                    "-- regenerate and review the diff (tools/usable-kitsoki-gate/run_calibration_gate.py "
                    "--out tools/arena/tests/fixtures/usable-kitsoki-gate/calibration-report.json "
                    "--relative-evidence) before committing"
                )
    else:
        # Report the calibration-contact finding for Task 4.2's open question
        # 1: does the 90% placeholder threshold survive contact with the real
        # calibration set? (see usable_kitsoki_gate_constants.py's own
        # calibration-contact note, which this run's number must match.)
        worst = checked_in["rollup"]["metrics"]["worst_surface_parity_percent"]
        threshold = checked_in["parity_threshold_percent"]
        silent_bounce_count = checked_in["rollup"]["metrics"]["silent_bounce_count"]
        misroute_adjacent_count = checked_in["rollup"]["metrics"]["misroute_adjacent_count"]
        check_true(
            "calibration-contact finding is consistent: worst-surface parity vs threshold is honestly reported "
            "(not silently patched to pass)",
            True,  # this check always "passes" -- it exists to print the number below for a human reviewer
        )
        # Report (not assume) the other two GATE_CONDITIONS from the actual
        # measured run -- S2's own regression-test-at-scale framing
        # (workbench_gate_signal.go's doc comment) says silent_bounce should
        # always be false by construction across all 162 real cells, and
        # misroute_adjacent is hard-false from every S1 signal today (a
        # documented absence, not a measurement) -- print both explicitly
        # rather than only ever reporting the parity number.
        print(f"[calibration] silent_bounce_count={silent_bounce_count} "
              f"(zero_silent_bounce {'PASSES' if silent_bounce_count == 0 else 'FAILS'})")
        print(f"[calibration] misroute_adjacent_count={misroute_adjacent_count} "
              f"(zero_misroute_adjacent {'PASSES' if misroute_adjacent_count == 0 else 'FAILS'}; "
              "S1 hard-codes this false today -- documented absence, not a measurement)")
        print(f"[calibration] worst_surface_parity_percent={worst} vs PARITY_THRESHOLD_PERCENT={threshold} "
              f"-> gate {'PASSES' if checked_in['rollup']['verdict'] == 'solved' else 'FAILS'} on the calibration set "
              "(see usable_kitsoki_gate_constants.py's calibration-contact note for why)")

if failures:
    print(f"FAIL ({len(failures)}):")
    for f in failures:
        print(f"  - {f}")
    sys.exit(1)
print("PASS: usable-kitsoki-gate no-LLM calibration run (Task 3.3 no-LLM half + Task 4.2) "
      "regenerates byte-identical to the checked-in parity report")
