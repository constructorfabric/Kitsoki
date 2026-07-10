#!/usr/bin/env python3
"""Verify imported BugSwarm source tasks with fresh failed/passed containers.

Default mode is `--dry-run`, which only writes the exact Docker commands and a
verification report shape. `--execute` is explicit because it may pull multi-GB
BugSwarm images and run long CI jobs. No mode calls an LLM.
"""

from __future__ import annotations

import argparse
import json
import shlex
import subprocess
import sys
import time
from pathlib import Path
from typing import Any

try:
    import yaml  # type: ignore
except ModuleNotFoundError:
    sys.exit("bugswarm_verify_source.py needs pyyaml")


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", required=True, help="YAML from bugswarm_to_arena.py")
    parser.add_argument("--out", required=True, help="verification report JSON")
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument("--dry-run", action="store_true", help="write command plan only (default)")
    mode.add_argument("--execute", action="store_true", help="actually run Docker verification")
    parser.add_argument("--timeout-s", type=int, default=7200)
    parser.add_argument("--image-repo", default="bugswarm/images")
    parser.add_argument("--cached-image-repo", default="bugswarm/cached-images")
    args = parser.parse_args(argv)

    source = load_source(Path(args.source))
    tasks = source.get("tasks") or []
    started = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
    execute = bool(args.execute)
    results = [
        verify_task(
            task,
            execute=execute,
            timeout_s=args.timeout_s,
            image_repo=args.image_repo,
            cached_image_repo=args.cached_image_repo,
        )
        for task in tasks
        if isinstance(task, dict)
    ]
    payload = {
        "kind": "arena_bugswarm_verification",
        "version": 1,
        "source": str(args.source),
        "mode": "execute" if execute else "dry-run",
        "generated_at": started,
        "task_count": len(results),
        "verified_count": sum(1 for r in results if r["verified_red"] and r["verified_green"]),
        "results": results,
    }
    out = Path(args.out)
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(json.dumps(payload, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    print(f"wrote BugSwarm verification report to {out}")
    if execute and payload["verified_count"] != len(results):
        return 1
    return 0


def load_source(path: Path) -> dict[str, Any]:
    data = yaml.safe_load(path.read_text(encoding="utf-8"))
    if not isinstance(data, dict):
        raise ValueError(f"source must be a mapping: {path}")
    if data.get("kind") != "arena_bugswarm_source":
        raise ValueError(f"source kind must be arena_bugswarm_source: {path}")
    return data


def verify_task(
    task: dict[str, Any],
    *,
    execute: bool,
    timeout_s: int,
    image_repo: str,
    cached_image_repo: str,
) -> dict[str, Any]:
    image_tag = str(task.get("image_tag") or "")
    if not image_tag:
        raise ValueError(f"BugSwarm task {task.get('id')!r} missing image_tag")
    failed_cmd = docker_cmd(cached_image_repo, image_tag, "run_failed.sh")
    passed_cmd = docker_cmd(cached_image_repo, image_tag, "run_passed.sh")
    fallback_failed_cmd = docker_cmd(image_repo, image_tag, "run_failed.sh")
    fallback_passed_cmd = docker_cmd(image_repo, image_tag, "run_passed.sh")

    if not execute:
        return {
            "task_id": str(task.get("id") or ""),
            "image_tag": image_tag,
            "mode": "dry-run",
            "verified_red": False,
            "verified_green": False,
            "commands": {
                "failed": shell_join(failed_cmd),
                "passed": shell_join(passed_cmd),
                "failed_fallback": shell_join(fallback_failed_cmd),
                "passed_fallback": shell_join(fallback_passed_cmd),
            },
            "notes": "dry-run only; run with --execute to verify fresh failed/passed containers",
        }

    failed = run_with_fallback(failed_cmd, fallback_failed_cmd, timeout_s=timeout_s)
    passed = run_with_fallback(passed_cmd, fallback_passed_cmd, timeout_s=timeout_s)
    failed_infra = is_infrastructure_exit(failed["exit_code"])
    passed_infra = is_infrastructure_exit(passed["exit_code"])
    verified_red = failed["exit_code"] != 0 and not failed_infra
    verified_green = passed["exit_code"] == 0 and not passed_infra
    infrastructure_error = failed_infra or passed_infra
    return {
        "task_id": str(task.get("id") or ""),
        "image_tag": image_tag,
        "mode": "execute",
        "verified_red": verified_red,
        "verified_green": verified_green,
        "infrastructure_error": infrastructure_error,
        "failed_exit_code": failed["exit_code"],
        "passed_exit_code": passed["exit_code"],
        "commands": {
            "failed": shell_join(failed["command"]),
            "passed": shell_join(passed["command"]),
        },
        "stdout_tail": {
            "failed": failed["stdout_tail"],
            "passed": passed["stdout_tail"],
        },
        "stderr_tail": {
            "failed": failed["stderr_tail"],
            "passed": passed["stderr_tail"],
        },
        "notes": verification_notes(failed, passed),
    }


def docker_cmd(repo: str, image_tag: str, script: str) -> list[str]:
    # BugSwarm docs warn not to run failed and passed scripts consecutively in
    # the same container; each command therefore uses a fresh --rm container.
    return [
        "docker",
        "run",
        "--rm",
        f"{repo}:{image_tag}",
        "bash",
        "-lc",
        script,
    ]


def run_with_fallback(primary: list[str], fallback: list[str], *, timeout_s: int) -> dict[str, Any]:
    first = run_cmd(primary, timeout_s=timeout_s)
    if first["exit_code"] == 125:
        second = run_cmd(fallback, timeout_s=timeout_s)
        second["command"] = fallback
        return second
    first["command"] = primary
    return first


def run_cmd(cmd: list[str], *, timeout_s: int) -> dict[str, Any]:
    try:
        proc = subprocess.run(cmd, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=timeout_s, check=False)
        return {
            "exit_code": proc.returncode,
            "stdout_tail": proc.stdout[-4000:],
            "stderr_tail": proc.stderr[-4000:],
        }
    except subprocess.TimeoutExpired as exc:
        return {
            "exit_code": 124,
            "stdout_tail": (exc.stdout or "")[-4000:] if isinstance(exc.stdout, str) else "",
            "stderr_tail": f"timed out after {exc.timeout}s",
        }


def is_infrastructure_exit(exit_code: int) -> bool:
    # Docker uses 125 when the daemon/client/container setup fails before the
    # artifact command can run. The verifier uses 124 for its own timeout.
    return exit_code in {124, 125}


def verification_notes(failed: dict[str, Any], passed: dict[str, Any]) -> str:
    notes = []
    if is_infrastructure_exit(failed["exit_code"]):
        notes.append(f"failed-side infrastructure exit {failed['exit_code']}")
    if is_infrastructure_exit(passed["exit_code"]):
        notes.append(f"passed-side infrastructure exit {passed['exit_code']}")
    return "; ".join(notes)


def shell_join(cmd: list[str]) -> str:
    return " ".join(shlex.quote(part) for part in cmd)


if __name__ == "__main__":
    raise SystemExit(main())
