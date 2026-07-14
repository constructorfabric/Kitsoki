#!/usr/bin/env python3
"""No-LLM, no-docker test for wiring the usable-kitsoki-gate plugin's real
inputs (docs/proposals/usable-kitsoki-release-gate.md Task 3.1 + 3.2):

  - Task 3.1: cell enumeration consumes S4's scenario IR corpus (the
    committed 18-scenario calibration set as the default, an arbitrary
    corpus directory as the override) via `targets_from` + `arena.model
    .load_targets_from_corpus`'s directory branch, one cell per
    scenario x surface -- no hand-rolled enumeration logic in the plugin.
  - Task 3.2: `score()` performs the real S1 (candidate) x S4 (source) join
    when the harness hands it S1's raw per-turn `usable_kitsoki_gate`
    signal (either already-unwrapped `turn_signals` or raw `kind: "turn.end"`
    `trace_events`) instead of a pre-built parity record, reading
    `source_completed` off the scenario's own `abandoned` field (the hidden
    oracle, never re-derived) and marking -- never fabricating -- the fields
    S1 cannot yet compute (`misroute_adjacent`, full `expected_effects`
    coverage).

Never launches docker, Playwright, or an LLM.
"""

from __future__ import annotations

import json
import sys
import tempfile
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE.parent))

from arena.model import JobSpec, load_targets_from_corpus  # noqa: E402
from arena.plugins import base as plugins  # noqa: E402
from arena.plugins.usable_kitsoki_gate import (  # noqa: E402
    DEFAULT_SCENARIO_CORPUS,
    REPO_ROOT,
    build_parity_record,
    extract_turn_signals,
)

failures: list[str] = []


def check(label: str, got, want) -> None:
    if got != want:
        failures.append(f"{label}: got {got!r}, want {want!r}")


def check_true(label: str, cond: bool, detail: str = "") -> None:
    if not cond:
        failures.append(f"{label}: expected true{f' ({detail})' if detail else ''}")


plugin = plugins.get("usable-kitsoki-gate")
CALIBRATION_DIR = REPO_ROOT / "tools" / "session-mining" / "calibration"

# ---- 1. Task 3.1: cell enumeration off the real calibration corpus ---------

calibration_targets = load_targets_from_corpus(CALIBRATION_DIR)
check("calibration set has 18 scenario documents", len(calibration_targets), 18)

sample = next(t for t in calibration_targets if t.id == "scn-c4d281a2-30e4-4002-9152-59d28d824abc-0000")
check("scenario id becomes the Target id", sample.id, "scn-c4d281a2-30e4-4002-9152-59d28d824abc-0000")
check("persona folds into target.meta verbatim", sample.meta.get("persona"), "bugfix-contributor")
check("abandoned folds into target.meta verbatim", sample.meta.get("abandoned"), False)
check_true("expected_effects folds into target.meta", len(sample.meta.get("expected_effects", [])) > 0)

spec = JobSpec.from_dict({
    "job_type": "usable-kitsoki-gate",
    "targets_from": "tools/session-mining/calibration",
    "variants": [{"id": "gate-v1", "backend": "replay"}],
    "axes": {"surface": ["web", "tui", "mcp"]},
    "placement": {"hosts": ["local"], "concurrency": 1, "retry": 0},
})
cells = spec.cells()
check("plan enumerates 18 scenarios x 3 surfaces = 54 cells", len(cells), 54)
check("targets_from is recorded on the spec", spec.targets_from, "tools/session-mining/calibration")

web_cells = [c for c in cells if c.axis.get("surface") == "web"]
check("18 cells for the web surface alone", len(web_cells), 18)

one_cell = next(c for c in cells if c.target.id == sample.id and c.axis.get("surface") == "web")
coords = plugin._coords(one_cell)  # noqa: SLF001 - internal, exercised directly for the wiring proof
check("persona falls back to the scenario's own IR persona (no explicit axis)", coords["persona"], "bugfix-contributor")
check("scenario_id coord threads the target id", coords["scenario_id"], sample.id)

drive = plugin.drive_command(one_cell, live=False)[2]
check_true("drive_command threads GATE_SCENARIO_ID", f"GATE_SCENARIO_ID={sample.id}" in drive, drive)
check_true("drive_command threads GATE_PERSONA from the scenario", "GATE_PERSONA=bugfix-contributor" in drive, drive)

# A configured, non-default corpus directory works identically (no
# hard-coded path assumptions beyond the directory contract).
with tempfile.TemporaryDirectory() as tmp:
    tmp_corpus = Path(tmp)
    (tmp_corpus / "scn-custom-0000.json").write_text(
        json.dumps({
            "schema_version": "1.0",
            "kind": "conversation",
            "id": "scn-custom-0000",
            "source": "hand-authored",
            "persona": "gap-filler",
            "goal": "test goal",
            "turns": [{"role": "user", "text": "hi", "corrected": False}],
            "expected_effects": [],
            "abandoned": False,
        }),
        encoding="utf-8",
    )
    custom_targets = load_targets_from_corpus(tmp_corpus)
    check("a configured non-default corpus dir loads independently", len(custom_targets), 1)
    check("custom corpus scenario id round-trips", custom_targets[0].id, "scn-custom-0000")

check_true("DEFAULT_SCENARIO_CORPUS points at the committed calibration set", DEFAULT_SCENARIO_CORPUS == "tools/session-mining/calibration")

# ---- 2. Task 3.2: extract_turn_signals off a raw synthetic S1 trace --------

trace_events = [
    {"turn": 1, "seq": 0, "kind": "turn.start", "payload": {}},
    {
        "turn": 1,
        "seq": 1,
        "kind": "turn.end",
        "payload": {
            "usable_kitsoki_gate": {
                "candidate_completed": True,
                "silent_bounce": False,
                "misroute_adjacent": False,
                "evidence_refs": [],
            }
        },
    },
    {"turn": 2, "seq": 0, "kind": "turn.end", "payload": {}},  # non-workbench turn: no key at all
]
signals = extract_turn_signals(trace_events)
check("extract_turn_signals pulls exactly the one workbench turn's signal", len(signals), 1)
check("the extracted signal's candidate_completed carries through", signals[0]["candidate_completed"], True)

# ---- 3. Task 3.2: build_parity_record performs the real S1 x S4 join ------

not_abandoned_scenario = {"id": "scn-git-ops-0007", "persona": "core-maintainer", "abandoned": False}
abandoned_scenario = {"id": "scn-abandoned-0001", "persona": "impatient-debugger", "abandoned": True}

all_ok_record = build_parity_record(
    not_abandoned_scenario,
    "web",
    [{"candidate_completed": True, "silent_bounce": False, "misroute_adjacent": False}],
    evidence_refs=[".artifacts/usable-kitsoki-gate/run-1/scn-git-ops-0007/trace.jsonl"],
)
check("scenario_id joins verbatim from the IR's own id", all_ok_record["scenario_id"], "scn-git-ops-0007")
check("persona joins verbatim from the IR", all_ok_record["persona"], "core-maintainer")
check("source_completed := not abandoned", all_ok_record["source_completed"], True)
check("candidate_completed reduces true across a single true turn signal", all_ok_record["candidate_completed"], True)
check("silent_bounce reduces false", all_ok_record["silent_bounce"], False)
check("misroute_adjacent reduces false", all_ok_record["misroute_adjacent"], False)
check_true("notes honestly flags the misroute_adjacent gap", "misroute_adjacent" in all_ok_record["notes"])
check_true("notes honestly flags the expected_effects gap", "expected_effects" in all_ok_record["notes"])

abandoned_record = build_parity_record(
    abandoned_scenario, "mcp", [{"candidate_completed": True}], evidence_refs=["some/trace.jsonl"],
)
check("an abandoned source scenario yields source_completed False", abandoned_record["source_completed"], False)

failed_turn_record = build_parity_record(
    not_abandoned_scenario,
    "tui",
    [
        {"candidate_completed": True, "silent_bounce": False},
        {"candidate_completed": False, "silent_bounce": True},
    ],
    evidence_refs=["some/trace.jsonl"],
)
check("candidate_completed is false if ANY turn failed", failed_turn_record["candidate_completed"], False)
check("silent_bounce is true if ANY turn bounced", failed_turn_record["silent_bounce"], True)

empty_signal_record = build_parity_record(not_abandoned_scenario, "web", [], evidence_refs=["some/trace.jsonl"])
check("no captured turn signal at all -> candidate_completed False, not fabricated True", empty_signal_record["candidate_completed"], False)
check_true("notes says no signal was captured", "no usable_kitsoki_gate turn signal" in empty_signal_record["notes"])

try:
    build_parity_record(not_abandoned_scenario, "web", [{"candidate_completed": True}], evidence_refs=[])
    failures.append("build_parity_record should reject empty evidence_refs rather than fabricate a path")
except ValueError:
    pass

# ---- 4. Task 3.2: score() performs the join end-to-end via a raw S1 bundle -

gate_spec = JobSpec.from_dict({
    "job_type": "usable-kitsoki-gate",
    "targets": [{
        "id": "scn-git-ops-0007",
        "label": "scn-git-ops-0007",
        "persona": "core-maintainer",
        "goal": "rebase and land the fix",
        "expected_effects": ["mcp__kitsoki__vcs_commit: {} completed"],
        "abandoned": False,
    }],
    "variants": [{"id": "gate-v1", "backend": "replay"}],
    "axes": {"surface": ["web"]},
    "placement": {"hosts": ["local"], "concurrency": 1, "retry": 0},
})
gate_cell = gate_spec.cells()[0]

with tempfile.TemporaryDirectory() as tmp:
    results_path = Path(tmp) / "parity-records.json"
    results_path.write_text(
        json.dumps({
            "run_id": "run-1",
            "trace_events": [
                {
                    "turn": 1,
                    "seq": 1,
                    "kind": "turn.end",
                    "payload": {
                        "usable_kitsoki_gate": {
                            "candidate_completed": True,
                            "silent_bounce": False,
                            "misroute_adjacent": False,
                            "evidence_refs": [],
                        }
                    },
                }
            ],
        }),
        encoding="utf-8",
    )
    stdout = f"[usable-kitsoki-gate] wrote {results_path} (1 record)\n"
    joined = plugin.score(gate_cell, exit_code=0, stdout=stdout, stderr="")
    check("score() joins a raw trace_events bundle to a solved verdict", joined.verdict, "solved")
    check("score() records exactly one joined record", joined.metrics["record_count"], 1)
    check_true("evidence_refs falls back to the results path itself", str(results_path) in joined.evidence_refs)

    # An abandoned source scenario (source_completed False) with an empty
    # denominator convention: no source-completed scenarios to have
    # regressed on, so the parity condition itself can't fail -- but
    # candidate_completed still reflects the real signal for `notes`/audit.
    abandoned_spec = JobSpec.from_dict({
        "job_type": "usable-kitsoki-gate",
        "targets": [{
            "id": "scn-abandoned-0001",
            "label": "scn-abandoned-0001",
            "persona": "impatient-debugger",
            "abandoned": True,
        }],
        "variants": [{"id": "gate-v1", "backend": "replay"}],
        "axes": {"surface": ["mcp"]},
        "placement": {"hosts": ["local"], "concurrency": 1, "retry": 0},
    })
    abandoned_cell = abandoned_spec.cells()[0]
    results_path2 = Path(tmp) / "parity-records-2.json"
    results_path2.write_text(json.dumps({"run_id": "run-2", "turn_signals": []}), encoding="utf-8")
    stdout2 = f"[usable-kitsoki-gate] wrote {results_path2} (1 record)\n"
    abandoned_joined = plugin.score(abandoned_cell, exit_code=0, stdout=stdout2, stderr="")
    check("empty-denominator (source not completed) still solves the parity condition", abandoned_joined.verdict, "solved")
    check("source_completed_count reflects the abandoned scenario", abandoned_joined.metrics["source_completed_count"], 0)

if failures:
    print(f"FAIL ({len(failures)}):")
    for f in failures:
        print(f"  - {f}")
    sys.exit(1)
print("PASS: usable-kitsoki-gate corpus wiring (Task 3.1 cell enumeration off S4's calibration set) "
      "+ real S1 x S4 join (Task 3.2 build_parity_record / extract_turn_signals / score() raw-signal bundle)")
