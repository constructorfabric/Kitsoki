#!/usr/bin/env python3
"""No-LLM/no-Docker tests for BugSwarm source verification planning."""

from __future__ import annotations

import json
import os
import subprocess
import sys
import tempfile
from pathlib import Path

import yaml  # type: ignore

HERE = Path(__file__).resolve().parent
REPO_ROOT = HERE.parent.parent.parent
CONVERT = REPO_ROOT / "tools/arena/scripts/bugswarm_to_arena.py"
VERIFY = REPO_ROOT / "tools/arena/scripts/bugswarm_verify_source.py"
SOURCES = REPO_ROOT / "tools/arena/corpus/sources.yaml"

failures: list[str] = []


def check(label: str, got, want) -> None:
    if got != want:
        failures.append(f"{label}: got {got!r}, want {want!r}")


sources = yaml.safe_load(SOURCES.read_text(encoding="utf-8"))
bugswarm = next((s for s in sources.get("sources", []) if s.get("id") == "bugswarm"), None)
check("bugswarm verifier path recorded", bugswarm.get("verifier") if bugswarm else "", "tools/arena/scripts/bugswarm_verify_source.py")

with tempfile.TemporaryDirectory() as tmp:
    tmpdir = Path(tmp)
    artifacts = tmpdir / "artifacts.json"
    source = tmpdir / "source.yaml"
    report = tmpdir / "verification.json"
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

    verify = subprocess.run(
        [sys.executable, str(VERIFY), "--source", str(source), "--out", str(report), "--dry-run"],
        cwd=REPO_ROOT,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    check("verifier dry-run exits zero", verify.returncode, 0)
    payload = json.loads(report.read_text(encoding="utf-8"))
    check("verification kind", payload["kind"], "arena_bugswarm_verification")
    check("verification mode", payload["mode"], "dry-run")
    check("verified count dry-run", payload["verified_count"], 0)
    result = payload["results"][0]
    check("dry-run red false", result["verified_red"], False)
    check("dry-run green false", result["verified_green"], False)
    check("failed command uses run_failed", "run_failed.sh" in result["commands"]["failed"], True)
    check("passed command uses run_passed", "run_passed.sh" in result["commands"]["passed"], True)
    check("commands use cached image first", "bugswarm/cached-images:square-okio-140452393" in result["commands"]["failed"], True)
    check("fallback command recorded", "bugswarm/images:square-okio-140452393" in result["commands"]["passed_fallback"], True)

with tempfile.TemporaryDirectory() as tmp:
    tmpdir = Path(tmp)
    bin_dir = tmpdir / "bin"
    bin_dir.mkdir()
    fake_docker = bin_dir / "docker"
    fake_docker.write_text(
        "#!/bin/sh\n"
        "echo 'docker daemon metadata I/O error' >&2\n"
        "exit 125\n",
        encoding="utf-8",
    )
    fake_docker.chmod(0o755)
    source = tmpdir / "source.yaml"
    report = tmpdir / "verification.json"
    source.write_text(
        "\n".join(
            [
                "kind: arena_bugswarm_source",
                "version: 1",
                "source: bugswarm",
                "tasks:",
                "- id: bugswarm-square-okio-140452393",
                "  repo: okio",
                "  repo_label: square/okio",
                "  image_tag: square-okio-140452393",
                "  failed_job_id: '140452393'",
                "  passed_job_id: '140452394'",
            ]
        )
        + "\n",
        encoding="utf-8",
    )
    env = dict(os.environ)
    env["PATH"] = str(bin_dir) + os.pathsep + env.get("PATH", "")
    verify = subprocess.run(
        [sys.executable, str(VERIFY), "--source", str(source), "--out", str(report), "--execute"],
        cwd=REPO_ROOT,
        env=env,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    check("infrastructure verifier exits nonzero", verify.returncode, 1)
    payload = json.loads(report.read_text(encoding="utf-8"))
    result = payload["results"][0]
    check("infrastructure red false", result["verified_red"], False)
    check("infrastructure green false", result["verified_green"], False)
    check("infrastructure flag set", result["infrastructure_error"], True)
    check("failed exit carried for infra", result["failed_exit_code"], 125)
    check("passed exit carried for infra", result["passed_exit_code"], 125)
    check("infrastructure notes mention failed side", "failed-side infrastructure exit 125" in result["notes"], True)

if failures:
    print("FAIL: bugswarm verify source")
    for failure in failures:
        print(f"  - {failure}")
    sys.exit(1)
print("PASS: bugswarm source verifier planner (no LLM, no Docker)")
