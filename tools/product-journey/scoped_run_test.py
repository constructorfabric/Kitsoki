#!/usr/bin/env python3
"""Runner-level test for scoped --emit-run bundles.

Run directly:  python3 tools/product-journey/scoped_run_test.py

The test builds local run bundles under a temp artifact root and never calls a
live LLM or GitHub.
"""

import importlib.util
import sys
import tempfile
from pathlib import Path

_spec = importlib.util.spec_from_file_location(
    "pj_run", str(Path(__file__).with_name("run.py"))
)
run = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(run)


def _check(name, cond):
    if not cond:
        print(f"FAIL: {name}")
        sys.exit(1)
    print(f"ok: {name}")


def _expect_system_exit(name, fn, expected_text):
    try:
        fn()
    except SystemExit as exc:
        _check(name, expected_text in str(exc))
        return
    print(f"FAIL: {name}")
    sys.exit(1)


def main():
    catalog = run.load_catalog(run.CATALOG)
    github_targets = run.load_github_targets(run.GITHUB_TARGETS)
    personas = run.load_personas(run.PERSONAS)
    scenarios = run.load_scenarios(run.SCENARIOS)

    selected = run.select_scenarios(scenarios, "project-onboarding,bugfix")
    _check("preserves requested order", [item["id"] for item in selected] == ["project-onboarding", "bugfix"])
    _expect_system_exit(
        "rejects duplicate scenario ids",
        lambda: run.select_scenarios(scenarios, "bugfix,bugfix"),
        "duplicate scenario",
    )
    _expect_system_exit(
        "rejects unknown scenario ids",
        lambda: run.select_scenarios(scenarios, "missing-scenario"),
        "unknown active scenario",
    )

    with tempfile.TemporaryDirectory() as tmp:
        tmp = Path(tmp)
        run.ARTIFACT_ROOT = tmp / "product-journey"
        run.MATRIX_ROOT = run.ARTIFACT_ROOT / "matrices"
        run.TARGET_PROOF_ROOT = run.ARTIFACT_ROOT / "target-proofs"
        run.DOGFOOD_ROOT = run.ARTIFACT_ROOT / "dogfood"

        run_dir, run_json = run.build_run_bundle(
            catalog,
            github_targets,
            personas,
            selected,
            "gears-rust",
            "core-maintainer",
            "scoped-test",
            "dry-run",
            None,
        )
        metrics = run.read_json(run_dir / "metrics.json")
        evidence = run.read_json(run_dir / "evidence.json")
        summary = run.run_story_summary(run_dir)

        scenario_ids = [scenario["id"] for scenario in run_json["scenarios"]]
        evidence_scenarios = {item["scenario"] for item in evidence["items"]}

        _check("run json is scoped", scenario_ids == ["project-onboarding", "bugfix"])
        _check("metrics scenario count is scoped", metrics["scenario_count"] == 2)
        _check("story summary scenario count is scoped", summary["scenario_count"] == 2)
        _check("evidence only references scoped scenarios", evidence_scenarios == set(scenario_ids))
        _check("evidence count shrinks with scope", metrics["required_evidence_count"] == len(evidence["items"]) == 12)

    print("PASS")


if __name__ == "__main__":
    main()
