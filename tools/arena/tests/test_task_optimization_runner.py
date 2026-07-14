#!/usr/bin/env python3
"""No-provider tests for the immutable task-optimization dispatcher."""

from __future__ import annotations

import json
import os
import subprocess
import sys
import tempfile
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from arena.model import canonical_json, receipt_sha256  # noqa: E402
from arena.task_optimization_runner import run  # noqa: E402


failures: list[str] = []


def check(label: str, got: object, want: object) -> None:
    if got != want:
        failures.append(f"{label}: got {got!r}, want {want!r}")


def fixture() -> tuple[dict[str, object], dict[str, object]]:
    plan: dict[str, object] = {
        "schema": "task-optimization/v1", "study_id": "fixture", "live_gate_env": "TASK_OPT_FIXTURE_LIVE",
        "retry_policy": {"max_infra_retries": 1}, "cells": [
            {"id": "one", "task_id": "one", "split": "learning", "candidate_id": "ready", "treatment": "raw", "repeat": 1},
            {"id": "two", "task_id": "two", "split": "learning", "candidate_id": "ready", "treatment": "raw", "repeat": 1},
            {"id": "blocked", "task_id": "three", "split": "learning", "candidate_id": "not-ready", "treatment": "raw", "repeat": 1},
        ],
    }
    preflight: dict[str, object] = {"schema": "task-optimization/preflight/v1", "study_id": "fixture", "candidates": [
        {"candidate_id": "ready", "status": "ready", "provider": "fixture", "profile_hash": "profile", "launch_plan_hash": "launch", "pacing": {"min_interval_s": 3}},
        {"candidate_id": "not-ready", "status": "unsupported"},
    ]}
    preflight["preflight_sha256"] = receipt_sha256(preflight)
    return plan, preflight


with tempfile.TemporaryDirectory() as td:
    root = Path(td)
    plan, preflight = fixture()
    summary = run(plan=plan, preflight=preflight, attempts_dir=root / "attempts", out_dir=root / "dry", live=False)
    check("dry dispatch selects ready cells", summary.dispatched, ["one", "two"])
    check("dry dispatch skips nonready", summary.skipped, [{"cell_id": "blocked", "reason": "candidate preflight is not ready"}])
    dry = json.loads((root / "dry" / "dispatch.json").read_text(encoding="utf-8"))
    check("dry never calls provider", dry["provider_dispatched"], False)
    check("dry creates no attempts", list((root / "attempts").rglob("*.json")) if (root / "attempts").exists() else [], [])
    plan_path, preflight_path = root / "plan.json", root / "preflight.json"
    plan_path.write_bytes(canonical_json(plan))
    preflight_path.write_bytes(canonical_json(preflight))
    cli = subprocess.run(
        [sys.executable, str(Path(__file__).resolve().parents[1] / "arena.py"), "task-optimization", "run",
         "--plan", str(plan_path), "--preflight", str(preflight_path), "--attempts", str(root / "cli-attempts"),
         "--out", str(root / "cli-dry")], text=True, capture_output=True, check=False,
    )
    check("CLI exposes dry run", cli.returncode, 0)
    check("CLI dry output names dispatch", "dry dispatch" in cli.stdout, True)

    try:
        run(plan=plan, preflight=preflight, attempts_dir=root / "attempts", out_dir=root / "blocked-live", live=True, executor=lambda _c, _r: {})
        failures.append("live run accepted absent gate")
    except ValueError as exc:
        check("live gate error", "live task-optimization dispatch" in str(exc), True)

    os.environ["TASK_OPT_FIXTURE_LIVE"] = "1"
    calls: list[dict[str, object]] = []
    sleeps: list[float] = []
    def fake(_cell: dict[str, object], request: dict[str, object]) -> dict[str, object]:
        calls.append(request)
        # First cell proves bounded infra retry.  The other returns a terminal
        # non-scored state, which is still canonical lifecycle evidence.
        if len(calls) == 1:
            return {"status": "blocked", "health": "infra:fixture", "retryable": True}
        return {"status": "unsupported", "health": "policy:fixture"}

    ticks = iter([0.0] * 32)
    summary = run(plan=plan, preflight=preflight, attempts_dir=root / "attempts", out_dir=root / "live", live=True,
                  executor=fake, sleep=sleeps.append, clock=lambda: next(ticks))
    check("live retries only infra", len(calls), 3)
    check("live records every dispatch", summary.dispatched, ["one", "one", "two"])
    check("provider pacing is applied", sleeps, [3.0])
    receipts = sorted((root / "attempts").glob("*/*/receipt.json"))
    check("attempts are immutable canonical records", len(receipts), 3)
    leases = sorted((root / "attempts" / ".leases").glob("*.json"))
    check("every attempt owns a lease", len(leases), 3)
    workspaces = sorted((root / "live" / "workspaces").glob("*/*/dispatch-request.json"))
    check("every attempt owns an isolated workspace", len(workspaces), 3)
    for request_path in workspaces:
        request = json.loads(request_path.read_text(encoding="utf-8"))
        check("request binds matching lease", Path(request["lease"]).is_file(), True)

if failures:
    raise SystemExit("\n".join(failures))
print("task optimization runner tests passed")
