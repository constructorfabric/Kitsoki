#!/usr/bin/env python3
"""Verify imported BugSwarm source tasks with fresh failed/passed containers.

Default mode is `--dry-run`, which only writes the exact Docker commands and a
verification report shape. `--execute` is explicit because it may pull multi-GB
BugSwarm images and run long CI jobs. No mode calls an LLM.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import shlex
import subprocess
import sys
import time
from pathlib import Path
from typing import Any, Mapping

try:
    import yaml  # type: ignore
except ModuleNotFoundError:
    sys.exit("bugswarm_verify_source.py needs pyyaml")


# The marker is emitted by the wrapper inside each fresh artifact container.
# Parsing happens before output is tailed so a verbose CI script cannot discard
# the exact checked-out revision from the durable verification receipt.
COMMIT_MARKER = "__KITSOKI_BUGSWARM_COMMIT__="
COMMIT_RE = re.compile(r"(?m)^" + re.escape(COMMIT_MARKER) + r"([0-9a-fA-F]{40,64})\s*$")
DOCKER_CONTEXT_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9_.-]*$")


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
    parser.add_argument("--task-id", action="append", default=[], help="verify one task id; repeat for a bounded batch")
    parser.add_argument(
        "--docker-context",
        default=None,
        help=(
            "Docker context for every run and image inspection. Defaults to "
            "DOCKER_CONTEXT when set, otherwise Docker's currently selected context."
        ),
    )
    args = parser.parse_args(argv)

    try:
        docker_context, docker_context_source = resolve_docker_context(args.docker_context, os.environ)
    except ValueError as exc:
        parser.error(str(exc))
    source = load_source(Path(args.source))
    tasks = select_tasks(source.get("tasks") or [], args.task_id)
    started = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
    execute = bool(args.execute)
    results = [
        verify_task(
            task,
            execute=execute,
            timeout_s=args.timeout_s,
            image_repo=args.image_repo,
            cached_image_repo=args.cached_image_repo,
            docker_context=docker_context,
        )
        for task in tasks
        if isinstance(task, dict)
    ]
    payload = {
        "kind": "arena_bugswarm_verification",
        "version": 1,
        "source": str(args.source),
        "source_sha256": hashlib.sha256(Path(args.source).read_bytes()).hexdigest(),
        "selected_task_ids": [str(task["id"]) for task in tasks],
        "mode": "execute" if execute else "dry-run",
        # Persist the exact explicit or environment-selected context.  `null`
        # deliberately means the Docker CLI's selected current context, so
        # existing local dry-runs do not require a daemon or Docker config.
        "docker_context": docker_context,
        "docker_context_source": docker_context_source,
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


def resolve_docker_context(
    requested: str | None, environ: Mapping[str, str]
) -> tuple[str | None, str]:
    """Return one safe context binding for every Docker invocation.

    An explicit flag wins over ``DOCKER_CONTEXT``.  When neither is set we
    intentionally return ``None``: Docker then uses its configured current
    context, retaining dry-run's no-Docker/no-daemon contract.  Every command
    in a verifier execution is nevertheless constructed from this one value.
    """
    if requested is not None:
        return validate_docker_context(requested, source="--docker-context"), "explicit"
    from_environment = environ.get("DOCKER_CONTEXT", "")
    if from_environment:
        return validate_docker_context(from_environment, source="DOCKER_CONTEXT"), "environment"
    return None, "current"


def validate_docker_context(value: str, *, source: str) -> str:
    """Reject ambiguous or option-like context values before any Docker call."""
    if not DOCKER_CONTEXT_RE.fullmatch(value):
        raise ValueError(
            f"{source} must be a Docker context name containing only letters, digits, '.', '_', or '-'"
        )
    return value


def select_tasks(tasks: list[Any], requested_ids: list[str]) -> list[dict[str, Any]]:
    """Return an explicit source-order batch, rejecting ambiguous selection."""
    normalized = [task for task in tasks if isinstance(task, dict)]
    by_id = {str(task.get("id") or ""): task for task in normalized}
    if len(by_id) != len(normalized) or "" in by_id:
        raise ValueError("source tasks require unique non-empty ids")
    if not requested_ids:
        return normalized
    requested = [str(task_id) for task_id in requested_ids]
    if len(requested) != len(set(requested)):
        raise ValueError("--task-id must not repeat an id")
    unknown = sorted(set(requested) - set(by_id))
    if unknown:
        raise ValueError("unknown --task-id: " + ", ".join(unknown))
    return [task for task in normalized if str(task["id"]) in set(requested)]


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
    docker_context: str | None,
) -> dict[str, Any]:
    image_tag = str(task.get("image_tag") or "")
    if not image_tag:
        raise ValueError(f"BugSwarm task {task.get('id')!r} missing image_tag")
    failed_source_dir, passed_source_dir = bugswarm_checkout_dirs(task)
    failed_cmd = docker_cmd(cached_image_repo, image_tag, "run_failed.sh", failed_source_dir, docker_context)
    passed_cmd = docker_cmd(cached_image_repo, image_tag, "run_passed.sh", passed_source_dir, docker_context)
    fallback_failed_cmd = docker_cmd(image_repo, image_tag, "run_failed.sh", failed_source_dir, docker_context)
    fallback_passed_cmd = docker_cmd(image_repo, image_tag, "run_passed.sh", passed_source_dir, docker_context)

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
            "checkout_dirs": {"failed": failed_source_dir, "passed": passed_source_dir},
            "notes": "dry-run only; run with --execute to verify fresh failed/passed containers",
        }

    failed = run_with_fallback(failed_cmd, fallback_failed_cmd, timeout_s=timeout_s)
    passed = run_with_fallback(passed_cmd, fallback_passed_cmd, timeout_s=timeout_s)
    failed_infra = is_infrastructure_exit(failed["exit_code"])
    passed_infra = is_infrastructure_exit(passed["exit_code"])
    # A non-zero script result without a source-bound commit is not RED
    # evidence: for example, `git rev-parse` at the artifact root itself can
    # fail.  Do not infer a revision from the image tag, API metadata, or a
    # neighbouring checkout.
    verified_red = failed["exit_code"] != 0 and not failed_infra and bool(failed["observed_commit_sha"])
    verified_green = passed["exit_code"] == 0 and not passed_infra and bool(passed["observed_commit_sha"])
    infrastructure_error = failed_infra or passed_infra
    image_digest = inspect_image_digest(failed["command"], docker_context=docker_context)
    return {
        "task_id": str(task.get("id") or ""),
        "image_tag": image_tag,
        "mode": "execute",
        "verified_red": verified_red,
        "verified_green": verified_green,
        "infrastructure_error": infrastructure_error,
        "failed_exit_code": failed["exit_code"],
        "passed_exit_code": passed["exit_code"],
        "failed_commit_sha": failed["observed_commit_sha"],
        "passed_commit_sha": passed["observed_commit_sha"],
        "image_digest": image_digest,
        "checkout_dirs": {"failed": failed_source_dir, "passed": passed_source_dir},
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


def bugswarm_checkout_dirs(task: dict[str, Any]) -> tuple[str, str]:
    """Return the exact failed/passed repository roots in a BugSwarm image.

    BugSwarm's entrypoint is an artifact root, not a Git repository.  The
    source repositories live below the side-specific ``failed`` and ``passed``
    roots.  A legacy ``bugswarm_source_dir`` can describe the failed side only
    when it visibly contains that path segment; otherwise an explicit passed
    directory is mandatory.  This deliberately refuses to guess sibling
    layouts such as ``/workspace/src``.
    """
    meta = task.get("meta") or {}
    failed_explicit = str(meta.get("bugswarm_failed_source_dir") or "").strip()
    passed_explicit = str(meta.get("bugswarm_passed_source_dir") or "").strip()
    legacy = str(meta.get("bugswarm_source_dir") or "").strip()
    if legacy and not failed_explicit:
        failed_explicit = legacy
    if failed_explicit and not passed_explicit and "/failed/" in failed_explicit:
        passed_explicit = failed_explicit.replace("/failed/", "/passed/", 1)
    if failed_explicit or passed_explicit:
        if not failed_explicit or not passed_explicit:
            raise ValueError(
                f"BugSwarm task {task.get('id')!r} requires both "
                "meta.bugswarm_failed_source_dir and meta.bugswarm_passed_source_dir "
                "when the source layout cannot be derived from /failed/"
            )
        return failed_explicit.rstrip("/"), passed_explicit.rstrip("/")
    repo = str(task.get("repo_label") or task.get("repo") or "").strip("/")
    if "/" not in repo:
        raise ValueError(
            f"BugSwarm task {task.get('id')!r} needs repo_label owner/name or explicit source dirs"
        )
    return f"/home/travis/build/failed/{repo}", f"/home/travis/build/passed/{repo}"


def docker_cmd(
    repo: str, image_tag: str, script: str, checkout_dir: str, docker_context: str | None
) -> list[str]:
    # BugSwarm docs warn not to run failed and passed scripts consecutively in
    # the same container; each command therefore uses a fresh --rm container.
    return [
        *docker_cli_prefix(docker_context),
        "run",
        "--rm",
        f"{repo}:{image_tag}",
        "bash",
        "-lc",
        # Capture the revision from the side-specific repository, not the
        # artifact root. Preserve the artifact script's exit code exactly.
        # The failed and passed commands are built independently and each has
        # its own `docker run --rm`, as BugSwarm requires.
        f"cd -- {shlex.quote(checkout_dir)} || exit 98; "
        f"printf '%s' '{COMMIT_MARKER}'; git rev-parse HEAD || exit 98; "
        f"{script}; status=$?; exit $status",
    ]


def docker_cli_prefix(docker_context: str | None) -> list[str]:
    """Return the only Docker command prefix used by this verifier."""
    if docker_context is None:
        return ["docker"]
    return ["docker", "--context", docker_context]


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
            "observed_commit_sha": extract_observed_commit(proc.stdout),
            "stdout_tail": proc.stdout[-4000:],
            "stderr_tail": proc.stderr[-4000:],
        }
    except subprocess.TimeoutExpired as exc:
        return {
            "exit_code": 124,
            "observed_commit_sha": extract_observed_commit(exc.stdout) if isinstance(exc.stdout, str) else "",
            "stdout_tail": (exc.stdout or "")[-4000:] if isinstance(exc.stdout, str) else "",
            "stderr_tail": f"timed out after {exc.timeout}s",
        }


def extract_observed_commit(stdout: str) -> str:
    """Return the exact container-side ``git rev-parse HEAD`` value, if any."""
    matches = COMMIT_RE.findall(stdout)
    # A repeat marker is ambiguous evidence (for example, a test script echoed
    # it).  Never promote an arbitrary value into corpus provenance.
    return matches[0].lower() if len(matches) == 1 else ""


def inspect_image_digest(run_command: list[str], *, docker_context: str | None) -> str:
    """Return Docker's immutable repo digest for the exact verified image.

    A missing digest is not fabricated: the corpus locker will block rather
    than freeze a mutable tag.  This is intentionally best-effort because the
    verification result itself already captures infrastructure failures.
    """
    try:
        image_index = run_command.index("--rm") + 1
        image = run_command[image_index]
        proc = subprocess.run(
            [
                *docker_cli_prefix(docker_context),
                "image",
                "inspect",
                "--format={{index .RepoDigests 0}}",
                image,
            ],
            text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=30, check=False,
        )
    except (ValueError, OSError, subprocess.TimeoutExpired):
        return ""
    return proc.stdout.strip() if proc.returncode == 0 else ""


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
    if not failed.get("observed_commit_sha"):
        notes.append("failed-side source commit unavailable; verification is not provenance-complete")
    if not passed.get("observed_commit_sha"):
        notes.append("passed-side source commit unavailable; verification is not provenance-complete")
    return "; ".join(notes)


def shell_join(cmd: list[str]) -> str:
    return " ".join(shlex.quote(part) for part in cmd)


if __name__ == "__main__":
    raise SystemExit(main())
