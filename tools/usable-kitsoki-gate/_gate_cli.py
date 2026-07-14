#!/usr/bin/env python3
"""_gate_cli.py — shared single-cell entry point for the no-LLM gate
harnesses (`run_tui_gate.py` / `run_mcp_gate.py`,
docs/proposals/usable-kitsoki-release-gate.md Task 3.3). Reads the exact env
var contract `usable_kitsoki_gate.py`'s `drive_command()` threads to a cell's
container: `GATE_SURFACE`, `GATE_SCENARIO_CORPUS`, `GATE_SCENARIO_ID`,
`GATE_RUN_ID`, `GATE_RESULTS_PATH` (`GATE_PERSONA` is accepted but not
consulted -- persona is a fixed property of the scenario IR document, not a
runner input, per `usable_kitsoki_gate.py`'s own module docstring). `GATE_TARGET`
is optional (S6 "no-llm-parity") -- one of `flow_gate_runner.WORKBENCH_TARGETS`
(`dev-story`/`pets-dev`/`slidey-dev`) to replay that target's real
`workbench:` room instead of the harness-stub default; unset reproduces the
prior harness-stub behavior byte-for-byte (`usable_kitsoki_gate.py`'s
`drive_command()` does not set this today, so a live `arena run` sweep is
unaffected by this addition until that wiring is extended separately).

Drives exactly ONE (scenario, surface) cell via `flow_gate_runner.py`,
writes `{"records": [<record>]}` to `GATE_RESULTS_PATH`, and prints the
`[usable-kitsoki-gate] wrote <path> (N records)` pointer line the plugin's
`score()` scans stdout for -- mirrors `swarm.py`'s `[swarm] wrote <path>`
convention exactly.
"""

from __future__ import annotations

import json
import os
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import flow_gate_runner as runner  # noqa: E402


def main(surface: str) -> int:
    scenario_corpus = os.environ.get("GATE_SCENARIO_CORPUS") or str(runner.DEFAULT_CORPUS)
    corpus_dir = Path(scenario_corpus)
    if not corpus_dir.is_absolute():
        corpus_dir = runner.REPO_ROOT / corpus_dir

    scenario_id = os.environ.get("GATE_SCENARIO_ID")
    if not scenario_id:
        print("[usable-kitsoki-gate] GATE_SCENARIO_ID is required (one cell = one scenario)", file=sys.stderr)
        return 2

    run_id = os.environ.get("GATE_RUN_ID") or "local"
    results_path_env = os.environ.get("GATE_RESULTS_PATH")
    if results_path_env:
        results_path = Path(results_path_env)
        if not results_path.is_absolute():
            results_path = runner.REPO_ROOT / results_path
    else:
        results_path = (
            runner.REPO_ROOT / ".artifacts" / "usable-kitsoki-gate" / run_id / "parity-records.json"
        )

    evidence_dir = results_path.parent / "evidence"
    target = os.environ.get("GATE_TARGET") or None

    try:
        record = runner.build_record_for_cell(
            scenario_id, corpus_dir, surface, evidence_dir=evidence_dir, target=target,
        )
    except runner.ScenarioNotFound as exc:
        print(f"[usable-kitsoki-gate] {exc}", file=sys.stderr)
        return 2

    results_path.parent.mkdir(parents=True, exist_ok=True)
    results_path.write_text(json.dumps({"run_id": run_id, "records": [record]}, indent=2), encoding="utf-8")
    print(f"[usable-kitsoki-gate] wrote {results_path} (1 record)")
    return 0
