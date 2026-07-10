#!/usr/bin/env python3
"""No-LLM tests for the reusable BugSwarm arena source adapter."""

from __future__ import annotations

import json
import subprocess
import sys
import tempfile
from pathlib import Path

import yaml  # type: ignore

HERE = Path(__file__).resolve().parent
REPO_ROOT = HERE.parent.parent.parent
SCRIPT = REPO_ROOT / "tools/arena/scripts/bugswarm_to_arena.py"
SOURCES = REPO_ROOT / "tools/arena/corpus/sources.yaml"
SEED_ARTIFACTS = REPO_ROOT / "tools/arena/corpus/bugswarm.seed-artifacts.json"
SEED_SOURCE = REPO_ROOT / "tools/arena/corpus/bugswarm.seed.yaml"

failures: list[str] = []


def check(label: str, got, want) -> None:
    if got != want:
        failures.append(f"{label}: got {got!r}, want {want!r}")


sources = yaml.safe_load(SOURCES.read_text(encoding="utf-8"))
bugswarm = next((s for s in sources.get("sources", []) if s.get("id") == "bugswarm"), None)
check("bugswarm source exists", bugswarm is not None, True)
check("bugswarm adapter path recorded", bugswarm.get("adapter") if bugswarm else "", "tools/arena/scripts/bugswarm_to_arena.py")
check("bugswarm verification applier path recorded", bugswarm.get("verification_applier") if bugswarm else "", "tools/arena/scripts/bugswarm_apply_verification.py")
check("bugswarm spec generator path recorded", bugswarm.get("spec_generator") if bugswarm else "", "tools/arena/scripts/bugswarm_to_arena_spec.py")
check("bugswarm oracle kind recorded", bugswarm.get("oracle_contract", {}).get("kind") if bugswarm else "", "bugswarm_fail_pass_pair")

seed_payload = yaml.safe_load(SEED_SOURCE.read_text(encoding="utf-8"))
check("seed source kind", seed_payload.get("kind"), "arena_bugswarm_source")
check("seed generated from artifacts", seed_payload.get("generated_from"), "tools/arena/corpus/bugswarm.seed-artifacts.json")
check("seed task count", seed_payload.get("task_count"), 1)
seed_task = seed_payload["tasks"][0]
check("seed task id", seed_task["id"], "bugswarm-square-okio-140452393")
check("seed source url preserved", seed_task["meta"]["source_url"], "https://www.bugswarm.org/docs/tutorials/setting-up-an-experiment/")
check("seed starts unverified red", seed_task["verified_red"], False)
check("seed starts unverified green", seed_task["verified_green"], False)

with tempfile.TemporaryDirectory() as tmp:
    tmpdir = Path(tmp)
    src = tmpdir / "artifacts.json"
    out = tmpdir / "bugswarm.yaml"
    src.write_text(json.dumps({
        "artifacts": [
            {
                "image_tag": "square-okio-140452393",
                "repo": "square/okio",
                "failed_job_id": 140452393,
                "passed_job_id": 140452394,
                "language": "Java",
                "build_system": "Gradle",
                "classification": "code",
                "source_url": "https://www.bugswarm.org/docs/tutorials/setting-up-an-experiment/",
            }
        ]
    }), encoding="utf-8")
    proc = subprocess.run(
        [sys.executable, str(SCRIPT), "--in", str(src), "--out", str(out)],
        cwd=REPO_ROOT,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    check("converter exits zero", proc.returncode, 0)
    payload = yaml.safe_load(out.read_text(encoding="utf-8"))
    check("converted kind", payload.get("kind"), "arena_bugswarm_source")
    check("converted task count", payload.get("task_count"), 1)
    task = payload["tasks"][0]
    check("task id stable", task["id"], "bugswarm-square-okio-140452393")
    check("repo label preserved", task["repo_label"], "square/okio")
    check("verified red initially false", task["verified_red"], False)
    check("verified green initially false", task["verified_green"], False)
    check("oracle kind", task["oracle"]["kind"], "bugswarm_fail_pass_pair")
    check("oracle image tag", task["oracle"]["image_tag"], "square-okio-140452393")
    check("metadata preserved", task["meta"]["build_system"], "Gradle")
    check("source url preserved", task["meta"]["source_url"], "https://www.bugswarm.org/docs/tutorials/setting-up-an-experiment/")

if failures:
    print("FAIL: bugswarm source")
    for failure in failures:
        print(f"  - {failure}")
    sys.exit(1)
print("PASS: bugswarm source adapter (no LLM, no Docker)")
