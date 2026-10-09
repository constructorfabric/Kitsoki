#!/usr/bin/env python3
"""Regression for autonomous_fix_loop command failure semantics.

The story-owned autonomous gate returns a structured status, but headless
drivers also need the process status to fail when filing, gh-agent fixing,
review, or validation fails. This keeps CI and story host.run callers from
silently accepting an invalid issue-to-fix loop.
"""

import contextlib
import importlib.util
import io
import json
import os
import sys
from pathlib import Path

_spec = importlib.util.spec_from_file_location(
    "pj_run", str(Path(__file__).with_name("run.py"))
)
run = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(run)


def fake_autonomous_fix_loop(*_args, **_kwargs):
    return {
        "status": "autonomous_fix_invalid",
        "autonomous_fix_status": "autonomous_fix_invalid",
        "filing_summary": "Filed findings to o/r: 1 filed, 0 already filed, 0 failed; 0 credible finding(s) remain unfiled.",
        "gh_agent_drain_status": "drained",
        "gh_agent_done_count": 0,
        "gh_agent_failed_count": 1,
        "gh_agent_active_count": 0,
        "review_summary": "ready: review passed",
        "validation_status": "invalid",
        "validation_errors": 1,
        "validation_warnings": 0,
    }


def run_case(extra_args, expected_exit):
    out = io.StringIO()
    sys.argv = [
        "run.py",
        "--autonomous-fix-loop",
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
    if payload.get("status") != "autonomous_fix_invalid":
        print(f"FAIL: expected invalid JSON status, got {payload.get('status')}")
        raise SystemExit(1)


def main():
    original_argv = sys.argv
    original_loop = run.autonomous_fix_loop
    original_run_dir_from_arg = run.run_dir_from_arg
    old_fake = os.environ.get("KITSOKI_GITOPS_AUTOFIX_USE_KITSOKI_BIN_FAKE")
    try:
        run.autonomous_fix_loop = fake_autonomous_fix_loop
        run.run_dir_from_arg = lambda value: Path(value)
        os.environ.pop("KITSOKI_GITOPS_AUTOFIX_USE_KITSOKI_BIN_FAKE", None)
        sys.argv = [
            "run.py",
            "--autonomous-fix-loop",
            "--json-output",
            "--run-dir",
            "/tmp/product-journey-run",
            "--ticket-repo",
            "o/r",
            "--gh-agent-db",
            "/tmp/gh-agent.sqlite",
        ]
        try:
            run.main()
        except SystemExit as exc:
            if "--autonomous-fix-loop is an internal test backend" not in str(exc):
                print(f"FAIL: direct autonomous-fix-loop should point callers at gitops, got {exc}")
                raise SystemExit(1)
        else:
            print("FAIL: direct autonomous-fix-loop unexpectedly succeeded")
            raise SystemExit(1)
        os.environ["KITSOKI_GITOPS_AUTOFIX_USE_KITSOKI_BIN_FAKE"] = "1"
        run_case([], 1)
        run_case(["--report-invalid-autonomous-fix"], 0)
    finally:
        sys.argv = original_argv
        run.autonomous_fix_loop = original_loop
        run.run_dir_from_arg = original_run_dir_from_arg
        if old_fake is None:
            os.environ.pop("KITSOKI_GITOPS_AUTOFIX_USE_KITSOKI_BIN_FAKE", None)
        else:
            os.environ["KITSOKI_GITOPS_AUTOFIX_USE_KITSOKI_BIN_FAKE"] = old_fake

    print("PASS")


if __name__ == "__main__":
    main()
