#!/usr/bin/env python3
"""Regression for autonomous_marathon command failure semantics."""

import contextlib
import importlib.util
import io
import json
import sys
from pathlib import Path

_spec = importlib.util.spec_from_file_location(
    "pj_run", str(Path(__file__).with_name("run.py"))
)
run = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(run)


def fake_autonomous_marathon(*_args, **_kwargs):
    return {
        "status": "autonomous_marathon_invalid",
        "autonomous_marathon_status": "autonomous_marathon_invalid",
        "autonomous_marathon_summary": "credible_issues=1, fix=autonomous_fix_invalid, review=ready, validation=invalid",
        "autonomous_marathon_report_path": "/tmp/product-journey-run/autonomous-marathon-report.md",
        "run_dir": "/tmp/product-journey-run",
        "validation_status": "invalid",
        "validation_errors": 1,
        "validation_warnings": 0,
        "autonomous_gate_summary": "filing=pass, gh_agent=fail, independent_verify=fail, review=pass, validation=fail",
    }


def run_case(extra_args, expected_exit):
    out = io.StringIO()
    sys.argv = [
        "run.py",
        "--autonomous-marathon",
        "--json-output",
        "--run-dir",
        "/tmp/product-journey-run",
        "--ticket-repo",
        "o/r",
        "--gh-agent-db",
        "/tmp/gh-agent.sqlite",
        *extra_args,
    ]
    with contextlib.redirect_stdout(out):
        try:
            run.main()
        except SystemExit as exc:
            if exc.code != expected_exit:
                print(f"FAIL: expected exit {expected_exit}, got {exc.code}")
                raise SystemExit(1)
        else:
            if expected_exit != 0:
                print(f"FAIL: expected exit {expected_exit}, got success")
                raise SystemExit(1)
    payload = json.loads(out.getvalue())
    if payload.get("status") != "autonomous_marathon_invalid":
        print(f"FAIL: expected invalid JSON status, got {payload.get('status')}")
        raise SystemExit(1)


def main():
    original_argv = sys.argv
    original_marathon = run.autonomous_marathon
    original_run_dir_from_arg = run.run_dir_from_arg
    try:
        run.autonomous_marathon = fake_autonomous_marathon
        run.run_dir_from_arg = lambda value: Path(value)
        run_case([], 1)
        run_case(["--report-invalid-autonomous-marathon"], 0)
    finally:
        sys.argv = original_argv
        run.autonomous_marathon = original_marathon
        run.run_dir_from_arg = original_run_dir_from_arg

    print("PASS")


if __name__ == "__main__":
    main()
