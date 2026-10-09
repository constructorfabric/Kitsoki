#!/usr/bin/env python3
"""No-LLM/no-Docker tests for applying BugSwarm verification reports."""

from __future__ import annotations

import json
import hashlib
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


def load_with_external_pyyaml(path: Path) -> dict:
    """Exercise an installed YAML parser from outside the arena shim paths."""
    command = (
        "import json, pathlib, sys, yaml; "
        "print(json.dumps(yaml.safe_load(pathlib.Path(sys.argv[1]).read_text()), sort_keys=True))"
    )
    result = subprocess.run(
        [sys.executable, "-c", command, str(path)],
        cwd="/tmp",
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    check("external PyYAML parses source", result.returncode, 0)
    if result.returncode:
        return {}
    return json.loads(result.stdout)


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
        "source_sha256": hashlib.sha256(source.read_bytes()).hexdigest(),
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
                "failed_commit_sha": "a" * 40,
                "passed_commit_sha": "b" * 40,
                "image_digest": "bugswarm/cached-images@sha256:abc",
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
    external_payload = load_with_external_pyyaml(verified_source)
    check("external PyYAML retains colon ticket", external_payload["tasks"][0]["ticket"], "Repair BugSwarm artifact square-okio-140452393: CI job 140452393 fails and job 140452394 passes.")
    first_serialization = verified_source.read_bytes()
    repeat_apply = subprocess.run(
        [sys.executable, str(APPLY), "--source", str(source), "--verification", str(verification), "--out", str(verified_source)],
        cwd=REPO_ROOT,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    check("repeat apply exits zero", repeat_apply.returncode, 0)
    check("apply serialization deterministic", verified_source.read_bytes(), first_serialization)
    payload = yaml.safe_load(verified_source.read_text(encoding="utf-8"))
    task = payload["tasks"][0]
    check("verified red set", task["verified_red"], True)
    check("verified green set", task["verified_green"], True)
    check("source verification mode", payload["verification"]["mode"], "execute")
    receipt_snapshot = Path(task["meta"]["bugswarm_verification"]["report"])
    source_snapshot = Path(task["meta"]["bugswarm_verification"]["source_snapshot"])
    check("receipt snapshot is retained", receipt_snapshot.read_bytes(), verification.read_bytes())
    check("source snapshot is retained", source_snapshot.read_bytes(), source.read_bytes())
    check("source snapshot hash carried", task["meta"]["bugswarm_verification"]["source_snapshot_sha256"], hashlib.sha256(source.read_bytes()).hexdigest())
    check("failed exit carried", task["meta"]["bugswarm_verification"]["failed_exit_code"], 1)
    check("verification receipt hash carried", len(task["meta"]["bugswarm_verification"]["report_sha256"]), 64)
    check("image digest carried", task["meta"]["bugswarm_verification"]["image_digest"], "bugswarm/cached-images@sha256:abc")
    check("failed container commit promotes to source meta", task["meta"]["failed_commit_sha"], "a" * 40)
    check("passed container commit promotes to source meta", task["meta"]["passed_commit_sha"], "b" * 40)

    dry = tmpdir / "dry.json"
    dry_out = tmpdir / "dry-verified.yaml"
    dry.write_text(json.dumps({
        "kind": "arena_bugswarm_verification",
        "version": 1,
        "source_sha256": hashlib.sha256(source.read_bytes()).hexdigest(),
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

    stale = tmpdir / "stale.json"
    stale.write_text(json.dumps({
        "kind": "arena_bugswarm_verification", "version": 1, "mode": "execute", "source_sha256": "0" * 64,
        "task_count": 0, "verified_count": 0, "results": [],
    }), encoding="utf-8")
    stale_apply = subprocess.run(
        [sys.executable, str(APPLY), "--source", str(source), "--verification", str(stale), "--out", str(verified_source)],
        cwd=REPO_ROOT, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False,
    )
    check("stale source receipt rejected", stale_apply.returncode != 0, True)

    # An ignored .artifacts directory in a managed workspace disappears during
    # teardown. Reject it before writing a source that would retain dead paths.
    workspace = tmpdir / "managed-workspace"
    workspace.mkdir()
    (workspace / ".kitsoki-dev-workspace.json").write_text("{}", encoding="utf-8")
    unsafe_out = workspace / ".artifacts" / "verified.yaml"
    unsafe_apply = subprocess.run(
        [sys.executable, str(APPLY), "--source", str(source), "--verification", str(verification), "--out", str(unsafe_out)],
        cwd=REPO_ROOT, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False,
    )
    check("managed workspace evidence rejected", unsafe_apply.returncode != 0, True)
    check("managed workspace error explains durable root", "primary checkout's .artifacts" in unsafe_apply.stderr, True)

if failures:
    print("FAIL: bugswarm apply verification")
    for failure in failures:
        print(f"  - {failure}")
    sys.exit(1)
print("PASS: bugswarm verification application (no LLM, no Docker)")
