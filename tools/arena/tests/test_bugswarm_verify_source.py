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
check("bugswarm disk budget planner recorded", bugswarm.get("disk_budget_planner") if bugswarm else "", "tools/arena/scripts/bugswarm_disk_budget.py")

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
        [
            sys.executable,
            str(VERIFY),
            "--source",
            str(source),
            "--out",
            str(report),
            "--dry-run",
            "--docker-context",
            "arena-test-vm",
        ],
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
    check("explicit Docker context is recorded", payload["docker_context"], "arena-test-vm")
    check("explicit Docker context source is recorded", payload["docker_context_source"], "explicit")
    check("verification pins source bytes", len(payload["source_sha256"]), 64)
    check("verified count dry-run", payload["verified_count"], 0)
    result = payload["results"][0]
    check("dry-run red false", result["verified_red"], False)
    check("dry-run green false", result["verified_green"], False)
    check("failed command uses run_failed", "run_failed.sh" in result["commands"]["failed"], True)
    check("passed command uses run_passed", "run_passed.sh" in result["commands"]["passed"], True)
    check("commands use cached image first", "bugswarm/cached-images:square-okio-140452393" in result["commands"]["failed"], True)
    check("fallback command recorded", "bugswarm/images:square-okio-140452393" in result["commands"]["passed_fallback"], True)
    check("failed command binds explicit context", "docker --context arena-test-vm run" in result["commands"]["failed"], True)
    check("passed command binds explicit context", "docker --context arena-test-vm run" in result["commands"]["passed"], True)

    selected = subprocess.run(
        [sys.executable, str(VERIFY), "--source", str(source), "--out", str(report), "--dry-run", "--task-id", "bugswarm-square-okio-140452393"],
        cwd=REPO_ROOT, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False,
    )
    check("single task batch exits zero", selected.returncode, 0)
    check("single task batch contains one result", json.loads(report.read_text(encoding="utf-8"))["task_count"], 1)
    missing = subprocess.run(
        [sys.executable, str(VERIFY), "--source", str(source), "--out", str(report), "--dry-run", "--task-id", "missing"],
        cwd=REPO_ROOT, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False,
    )
    check("unknown task batch fails", missing.returncode != 0, True)

    environment = dict(os.environ)
    environment["DOCKER_CONTEXT"] = "arena-env-vm"
    env_selected = subprocess.run(
        [sys.executable, str(VERIFY), "--source", str(source), "--out", str(report), "--dry-run"],
        cwd=REPO_ROOT, env=environment, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False,
    )
    check("environment context dry-run exits zero", env_selected.returncode, 0)
    payload = json.loads(report.read_text(encoding="utf-8"))
    check("environment Docker context is recorded", payload["docker_context"], "arena-env-vm")
    check("environment Docker context source is recorded", payload["docker_context_source"], "environment")
    check("environment context binds command", "docker --context arena-env-vm run" in payload["results"][0]["commands"]["failed"], True)

    invalid_context = subprocess.run(
        [sys.executable, str(VERIFY), "--source", str(source), "--out", str(report), "--dry-run", "--docker-context", "bad/context"],
        cwd=REPO_ROOT, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False,
    )
    check("option-like Docker context is rejected", invalid_context.returncode != 0, True)

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
        [
            sys.executable,
            str(VERIFY),
            "--source",
            str(source),
            "--out",
            str(report),
            "--execute",
            "--docker-context",
            "receipt-context",
        ],
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

with tempfile.TemporaryDirectory() as tmp:
    tmpdir = Path(tmp)
    bin_dir = tmpdir / "bin"
    bin_dir.mkdir()
    invocation_log = tmpdir / "docker-invocations.log"
    fake_docker = bin_dir / "docker"
    # This is a deliberately tiny Docker seam: it proves the verifier parses
    # container output and retains the artifact script's status without a
    # daemon, image pull, or actual container.
    fake_docker.write_text(
        "#!/bin/sh\n"
        "printf '%s\\n' \"$*\" >> \"$KITSOKI_FAKE_DOCKER_LOG\"\n"
        "case \"$*\" in\n"
        # This intentionally treats the image root as non-Git. It only emits
        # provenance when the verifier asks for the side-specific checkout;
        # the old artifact-root probe therefore fails this no-Docker seam.
        "  *'cd -- /home/travis/build/failed/example/provenance'*run_failed.sh*) printf '__KITSOKI_BUGSWARM_COMMIT__=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\\n'; exit 17 ;;\n"
        "  *'cd -- /home/travis/build/passed/example/provenance'*run_passed.sh*) printf '__KITSOKI_BUGSWARM_COMMIT__=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\\n'; exit 0 ;;\n"
        "  *run_failed.sh*|*run_passed.sh*) echo 'fatal: not a git repository (or any parent up to mount point /)' >&2; exit 98 ;;\n"
        "esac\n"
        "exit 99\n",
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
                "- id: bugswarm-container-provenance",
                "  repo: provenance",
                "  repo_label: example/provenance",
                "  image_tag: provenance-1",
                "  failed_job_id: '1'",
                "  passed_job_id: '2'",
            ]
        )
        + "\n",
        encoding="utf-8",
    )
    env = dict(os.environ)
    env["PATH"] = str(bin_dir) + os.pathsep + env.get("PATH", "")
    env["KITSOKI_FAKE_DOCKER_LOG"] = str(invocation_log)
    verify = subprocess.run(
        [
            sys.executable,
            str(VERIFY),
            "--source",
            str(source),
            "--out",
            str(report),
            "--execute",
            "--docker-context",
            "receipt-context",
        ],
        cwd=REPO_ROOT, env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False,
    )
    check("container provenance execute exits zero", verify.returncode, 0)
    payload = json.loads(report.read_text(encoding="utf-8"))
    check("execute receipt records Docker context", payload["docker_context"], "receipt-context")
    result = payload["results"][0]
    check("container failed script exit preserved", result["failed_exit_code"], 17)
    check("container passed script exit preserved", result["passed_exit_code"], 0)
    check("container failed commit recorded", result["failed_commit_sha"], "a" * 40)
    check("container passed commit recorded", result["passed_commit_sha"], "b" * 40)
    check("container provenance remains valid RED", result["verified_red"], True)
    check("container provenance remains valid GREEN", result["verified_green"], True)
    check("failed checkout path is retained", result["checkout_dirs"]["failed"], "/home/travis/build/failed/example/provenance")
    check("passed checkout path is retained", result["checkout_dirs"]["passed"], "/home/travis/build/passed/example/provenance")
    invocations = invocation_log.read_text(encoding="utf-8").splitlines()
    check("separate fresh container invocations", len(invocations), 3)  # failed, passed, image inspect
    check("failed invocation uses receipt context", invocations[0].startswith("--context receipt-context run "), True)
    check("passed invocation uses receipt context", invocations[1].startswith("--context receipt-context run "), True)
    check("image inspection uses receipt context", invocations[2].startswith("--context receipt-context image inspect "), True)
    check("failed invocation remains fresh", "run --rm" in invocations[0], True)
    check("passed invocation remains fresh", "run --rm" in invocations[1], True)
    check("failed invocation captures checkout HEAD", "cd -- /home/travis/build/failed/example/provenance" in invocations[0], True)
    check("passed invocation captures checkout HEAD", "cd -- /home/travis/build/passed/example/provenance" in invocations[1], True)
    check("passed invocation preserves script status", "status=$?; exit $status" in invocations[1], True)

with tempfile.TemporaryDirectory() as tmp:
    tmpdir = Path(tmp)
    source = tmpdir / "source.yaml"
    report = tmpdir / "verification.json"
    source.write_text(
        "\n".join(
            [
                "kind: arena_bugswarm_source",
                "version: 1",
                "source: bugswarm",
                "tasks:",
                "- id: bugswarm-explicit-layout",
                "  repo: project",
                "  repo_label: example/project",
                "  image_tag: explicit-layout-1",
                "  failed_job_id: '1'",
                "  passed_job_id: '2'",
                "  meta:",
                "    bugswarm_failed_source_dir: /opt/bugswarm/failure/project",
                "    bugswarm_passed_source_dir: /opt/bugswarm/success/project",
            ]
        )
        + "\n",
        encoding="utf-8",
    )
    verify = subprocess.run(
        [sys.executable, str(VERIFY), "--source", str(source), "--out", str(report), "--dry-run"],
        cwd=REPO_ROOT, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False,
    )
    check("explicit checkout dirs dry-run exits zero", verify.returncode, 0)
    result = json.loads(report.read_text(encoding="utf-8"))["results"][0]
    check("explicit failed dir retained", result["checkout_dirs"]["failed"], "/opt/bugswarm/failure/project")
    check("explicit passed dir retained", result["checkout_dirs"]["passed"], "/opt/bugswarm/success/project")
    check("explicit failed dir used in command", "cd -- /opt/bugswarm/failure/project" in result["commands"]["failed"], True)
    check("explicit passed dir used in command", "cd -- /opt/bugswarm/success/project" in result["commands"]["passed"], True)

if failures:
    print("FAIL: bugswarm verify source")
    for failure in failures:
        print(f"  - {failure}")
    sys.exit(1)
print("PASS: bugswarm source verifier planner (no LLM, no Docker)")
