#!/usr/bin/env python3
"""Runner-level test for product-journey live budget contracts.

Run directly:  python3 tools/product-journey/live_budget_test.py

This creates local dry-run bundles only; it never calls a live LLM or GitHub.
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


def main():
    catalog = run.load_catalog(run.CATALOG)
    github_targets = run.load_github_targets(run.GITHUB_TARGETS)
    personas = run.load_personas(run.PERSONAS)
    scenarios = run.select_scenarios(run.load_scenarios(run.SCENARIOS), "bugfix")

    with tempfile.TemporaryDirectory() as tmp:
        tmp = Path(tmp)
        run.ARTIFACT_ROOT = tmp / "product-journey"
        run.MATRIX_ROOT = run.ARTIFACT_ROOT / "matrices"
        run.TARGET_PROOF_ROOT = run.ARTIFACT_ROOT / "target-proofs"
        run.DOGFOOD_ROOT = run.ARTIFACT_ROOT / "dogfood"
        run.PREFLIGHT_ROOT = run.ARTIFACT_ROOT / "preflights"

        run_dir, run_json = run.build_run_bundle(
            catalog,
            github_targets,
            personas,
            scenarios,
            "gears-rust",
            "core-maintainer",
            "live-budget-test",
            "dry-run",
            None,
            7,
        )

        driver_plan = run.read_json(run_dir / "driver-plan.json")
        agent_brief = run.read_json(run_dir / "agent-brief.json")
        driver_markdown = (run_dir / "driver-plan.md").read_text(encoding="utf-8")
        brief_markdown = (run_dir / "agent-brief.md").read_text(encoding="utf-8")
        summary = run.run_story_summary(run_dir)

        budget = driver_plan["scenarios"][0]["live_budget"]
        _check("run json stores live budget", run_json["live_budget_minutes"] == 7)
        _check("driver plan stores live budget", budget["max_live_minutes"] == 7)
        _check("driver budget records exhaustion action", budget["remaining_action"] == "record_blocker")
        _check("agent brief stores live budget", agent_brief["scenario_order"][0]["live_budget"]["max_live_minutes"] == 7)
        _check("story summary exposes live budget", summary["live_budget_minutes"] == 7)
        _check("driver markdown mentions live budget", "Live budget:" in driver_markdown and "7 live minutes" in driver_markdown)
        _check("agent brief markdown mentions live budget", "Live budget:" in brief_markdown and "7 live minutes" in brief_markdown)

        validation = run.validate_run_bundle(run_dir)
        _check(
            "validation accepts present live budget",
            not any(issue["id"] in {"driver-plan-live-budget", "driver-plan-scenario-required-keys", "agent-brief-scenario-required-keys"} for issue in validation["issues"]),
        )

        del driver_plan["scenarios"][0]["live_budget"]
        run.write_json(run_dir / "driver-plan.json", driver_plan)
        invalid = run.validate_run_bundle(run_dir)
        _check("missing live budget fails validation", invalid["status"] == "invalid")
        _check(
            "validation names live budget requirement",
            any(issue["id"] == "driver-plan-scenario-required-keys" for issue in invalid["issues"]),
        )

        zero_run_dir, _ = run.build_run_bundle(
            catalog,
            github_targets,
            personas,
            scenarios,
            "gears-rust",
            "core-maintainer",
            "live-budget-zero",
            "dry-run",
            None,
            0,
        )
        try:
            run.record_driver_event(
                zero_run_dir,
                "bugfix",
                "live",
                "captured",
                "Live dispatch should not be accepted when live budget is zero.",
                "session.open",
                "",
                "",
                None,
            )
        except SystemExit as exc:
            _check("zero-budget live capture fails closed", "Live driver dispatch is disabled" in str(exc))
        else:
            _check("zero-budget live capture fails closed", False)

        blocked_event = run.record_driver_event(
            zero_run_dir,
            "bugfix",
            "live",
            "blocked",
            "Live dispatch was blocked because this run has no live budget.",
            "session.open",
            "",
            "live budget disabled",
            None,
        )
        _check("zero-budget live blocker is journaled", blocked_event["status"] == "blocked")

        live_event = run.record_driver_event(
            run_dir,
            "bugfix",
            "live",
            "captured",
            "Live dispatch is allowed only when the run has a positive budget.",
            "session.open",
            "",
            "",
            None,
        )
        _check("positive-budget live capture is allowed", live_event["dispatch_mode"] == "live")

    print("PASS")


if __name__ == "__main__":
    main()
