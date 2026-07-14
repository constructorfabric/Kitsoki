#!/usr/bin/env python3
"""No-Docker tests for the BugSwarm disk budget planner."""

from __future__ import annotations

import json
import os
import subprocess
import sys
import tempfile
from pathlib import Path

HERE = Path(__file__).resolve().parent
REPO_ROOT = HERE.parent.parent.parent
SCRIPT = REPO_ROOT / "tools/arena/scripts/bugswarm_disk_budget.py"
failures: list[str] = []


def check(label: str, got, want) -> None:
    if got != want:
        failures.append(f"{label}: got {got!r}, want {want!r}")


with tempfile.TemporaryDirectory() as tmp:
    tmpdir = Path(tmp)
    bin_dir = tmpdir / "bin"
    bin_dir.mkdir()
    log = tmpdir / "docker.log"
    fake = bin_dir / "docker"
    fake.write_text(
        "#!/bin/sh\n"
        "printf '%s\\n' \"$*\" >> \"$BUDGET_DOCKER_LOG\"\n"
        "case \"$*\" in\n"
        "  *'image inspect --format={{json .}} bugswarm/cached-images:cached') printf '%s\\n' '{\"Id\":\"sha256:cached\",\"Size\":1234,\"RepoDigests\":[\"bugswarm/cached-images@sha256:abc\"],\"RootFS\":{\"Layers\":[\"a\",\"b\"]}}' ;;\n"
        "  *'image inspect --format={{json .}} unrelated:old') printf '%s\\n' '{\"Id\":\"sha256:old\",\"Size\":777,\"RootFS\":{\"Layers\":[\"old\"]}}' ;;\n"
        "  *'image inspect'*) echo absent >&2; exit 1 ;;\n"
        "  *'run --rm --pull=never --entrypoint df bugswarm/cached-images:cached -Pk /'*) printf 'Filesystem 1024-blocks Used Available Capacity Mounted on\\n/dev/vda 100 1 5242880 1%% /\\n' ;;\n"
        "  *) echo unexpected >&2; exit 99 ;;\n"
        "esac\n",
        encoding="utf-8",
    )
    fake.chmod(0o755)
    evidence = tmpdir / "evidence"
    evidence.mkdir()
    (evidence / "bugswarm-source-a.yaml").write_text("kind: arena_bugswarm_source\n", encoding="utf-8")
    (evidence / "bugswarm-verification-b.json").write_text("{}\n", encoding="utf-8")
    out = tmpdir / "budget.json"
    env = dict(os.environ, PATH=str(bin_dir) + os.pathsep + os.environ.get("PATH", ""), BUDGET_DOCKER_LOG=str(log))
    run = subprocess.run(
        [sys.executable, str(SCRIPT), "--image", "bugswarm/cached-images:cached", "--out", str(out), "--docker-context", "vm-1", "--min-free-gib", "2", "--durable-evidence-dir", str(evidence), "--reclaim-image", "unrelated:old"],
        cwd=REPO_ROOT, env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False,
    )
    check("cached image budget exits zero", run.returncode, 0)
    payload = json.loads(out.read_text(encoding="utf-8"))
    check("cached image ready", payload["status"], "ready")
    check("cached image uses no new-image budget", payload["budget"]["additional_image_bytes"], 0)
    check("layer count recorded", payload["image_inspection"]["layer_count"], 2)
    check("free bytes parsed", payload["free_space"]["available_bytes"], 5242880 * 1024)
    check("durable evidence accepted", payload["durable_evidence"]["present"], True)
    check("reclaim remains manual", payload["reclamation_plan"][0]["status"], "manual-review-required")
    check("reclaim command is context-bound", payload["reclamation_plan"][0]["command"], "docker --context vm-1 image rm unrelated:old")
    check("probe forbids pull", "--pull=never" in log.read_text(encoding="utf-8"), True)

    uncached = tmpdir / "uncached.json"
    run = subprocess.run(
        [sys.executable, str(SCRIPT), "--image", "bugswarm/images:uncached", "--out", str(uncached), "--available-bytes", str(3 * 1024**3), "--min-free-gib", "2", "--uncached-image-budget-gib", "12"],
        cwd=REPO_ROOT, env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False,
    )
    check("uncached underbudget exits blocked", run.returncode, 2)
    blocked = json.loads(uncached.read_text(encoding="utf-8"))
    check("uncached budget blocked", blocked["status"], "blocked")
    check("uncached budget reserved", blocked["budget"]["additional_image_bytes"], 12 * 1024**3)
    check("blocker names observed capacity", "observed" in blocked["blockers"][0], True)

    missing_evidence = tmpdir / "missing-evidence.json"
    run = subprocess.run(
        [sys.executable, str(SCRIPT), "--image", "bugswarm/cached-images:cached", "--out", str(missing_evidence), "--available-bytes", str(5 * 1024**3), "--reclaim-image", "unrelated:old"],
        cwd=REPO_ROOT, env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False,
    )
    check("missing evidence does not block verification budget", run.returncode, 0)
    plan = json.loads(missing_evidence.read_text(encoding="utf-8"))["reclamation_plan"][0]
    check("eviction without evidence blocked", plan["status"], "blocked")

if failures:
    print("FAIL: bugswarm disk budget")
    for failure in failures:
        print("  - " + failure)
    raise SystemExit(1)
print("PASS: bugswarm disk budget (no Docker, no LLM)")
