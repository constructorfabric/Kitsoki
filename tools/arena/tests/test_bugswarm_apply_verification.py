#!/usr/bin/env python3
"""No-LLM/no-Docker tests for applying BugSwarm verification reports."""

from __future__ import annotations

import json
import subprocess
import sys
import tempfile
from pathlib import Path

import yaml  # type: ignore

HERE = Path(__file__).resolve().parent
REPO_ROOT = HERE.parent.parent.parent
CONVERT = REPO_ROOT / "tools/arena/scripts/bugswarm_to_arena.py"
APPLY = REPO_ROOT / "tools/arena/scripts/bugswarm_apply_verification.py"

failures: list[str] = []


def check(label: str, got, want) -> None:
    if got != want:
        failures.append(f"{label}: got {got!r}, want {want!r}")


with tempfile.TemporaryDirectory() as tmp:
    tmpdir = Path(tmp)
    artifacts = tmpdir / "artifacts.json"
    source = tmpdir / "source.yaml"
    verification = tmpdir / "verification.json"
    verified_source = tmpdir / "verified.yaml"
    artifacts.write_text(json.dumps({
        "artifacts": [
            {
                "image_tag": "square-okio-140452393",
                "repo": "square/okio",
                "failed_job_id": "140452393",
                "passed_job_id": "140452394",
            }
        ]
    }), encoding="utf-8")
    convert = subprocess.run(
        [sys.executable, str(CONVERT), "--in", str(artifacts), "--out", str(source)],
        cwd=REPO_ROOT,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    check("converter exits zero", convert.returncode, 0)
    verification.write_text(json.dumps({
        "kind": "arena_bugswarm_verification",
        "version": 1,
        "mode": "execute",
        "task_count": 1,
        "verified_count": 1,
        "results": [
            {
                "task_id": "bugswarm-square-okio-140452393",
                "image_tag": "square-okio-140452393",
                "verified_red": True,
                "verified_green": True,
                "failed_exit_code": 1,
                "passed_exit_code": 0,
            }
        ],
    }), encoding="utf-8")
    apply = subprocess.run(
        [sys.executable, str(APPLY), "--source", str(source), "--verification", str(verification), "--out", str(verified_source)],
        cwd=REPO_ROOT,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    check("apply exits zero", apply.returncode, 0)
    payload = yaml.safe_load(verified_source.read_text(encoding="utf-8"))
    task = payload["tasks"][0]
    check("verified red set", task["verified_red"], True)
    check("verified green set", task["verified_green"], True)
    check("source verification mode", payload["verification"]["mode"], "execute")
    check("task verification report path", task["meta"]["bugswarm_verification"]["report"], str(verification))
    check("failed exit carried", task["meta"]["bugswarm_verification"]["failed_exit_code"], 1)

    dry = tmpdir / "dry.json"
    dry_out = tmpdir / "dry-verified.yaml"
    dry.write_text(json.dumps({
        "kind": "arena_bugswarm_verification",
        "version": 1,
        "mode": "dry-run",
        "task_count": 1,
        "verified_count": 0,
        "results": [{"task_id": "bugswarm-square-okio-140452393", "verified_red": False, "verified_green": False}],
    }), encoding="utf-8")
    dry_apply = subprocess.run(
        [sys.executable, str(APPLY), "--source", str(source), "--verification", str(dry), "--out", str(dry_out)],
        cwd=REPO_ROOT,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    check("dry-run apply rejected by default", dry_apply.returncode != 0, True)
    dry_apply_allowed = subprocess.run(
        [sys.executable, str(APPLY), "--source", str(source), "--verification", str(dry), "--out", str(dry_out), "--allow-dry-run"],
        cwd=REPO_ROOT,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    check("dry-run apply allowed", dry_apply_allowed.returncode, 0)
    dry_payload = yaml.safe_load(dry_out.read_text(encoding="utf-8"))
    check("dry-run does not set red", dry_payload["tasks"][0]["verified_red"], False)
    check("dry-run records mode", dry_payload["tasks"][0]["meta"]["bugswarm_verification"]["mode"], "dry-run")

if failures:
    print("FAIL: bugswarm apply verification")
    for failure in failures:
        print(f"  - {failure}")
    sys.exit(1)
print("PASS: bugswarm verification application (no LLM, no Docker)")
