#!/usr/bin/env python3
"""Plan a disk-safe BugSwarm verification without pulling or deleting images.

BugSwarm artifacts are often several GiB once expanded.  This command inspects
the selected Docker context, reports an image's known layer footprint, and
blocks a run when the supplied or observed free-space budget is insufficient.
It deliberately never runs ``docker prune`` or ``docker image rm``.  Optional
reclamation suggestions are emitted only after durable corpus evidence is
present outside a managed development workspace.

Use this before ``bugswarm_verify_source.py --execute``.  The JSON output is a
review artifact: retain it beside the immutable source/receipt chain.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import subprocess
import sys
from pathlib import Path
from typing import Any, Mapping


GIB = 1024**3
DOCKER_CONTEXT_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9_.-]*$")


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--image", required=True, help="fully-qualified BugSwarm image to assess")
    parser.add_argument("--out", required=True, help="JSON budget report to write")
    parser.add_argument("--docker-context", default=None, help="explicit Docker context, or DOCKER_CONTEXT")
    parser.add_argument(
        "--available-bytes",
        type=positive_int,
        help="known free bytes on the Docker host; avoids starting a probe container",
    )
    parser.add_argument(
        "--space-probe-image",
        help="already-cached image used only with --pull=never to run `df -Pk /` when --image is absent",
    )
    parser.add_argument("--min-free-gib", type=positive_float, default=2.0, help="reserve after verification (default: 2)")
    parser.add_argument(
        "--uncached-image-budget-gib",
        type=positive_float,
        default=12.0,
        help="conservative additional reservation for an absent image (default: 12)",
    )
    parser.add_argument(
        "--durable-evidence-dir",
        help="primary-checkout evidence directory containing immutable source and receipt snapshots",
    )
    parser.add_argument(
        "--reclaim-image",
        action="append",
        default=[],
        help="image that may be suggested for manual eviction; never deleted by this command",
    )
    args = parser.parse_args(argv)

    try:
        context, context_source = resolve_docker_context(args.docker_context, os.environ)
    except ValueError as exc:
        parser.error(str(exc))

    report = build_report(
        image=args.image,
        docker_context=context,
        docker_context_source=context_source,
        available_bytes=args.available_bytes,
        space_probe_image=args.space_probe_image,
        min_free_bytes=int(args.min_free_gib * GIB),
        uncached_image_budget_bytes=int(args.uncached_image_budget_gib * GIB),
        durable_evidence_dir=Path(args.durable_evidence_dir) if args.durable_evidence_dir else None,
        reclaim_images=args.reclaim_image,
    )
    out = Path(args.out)
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    print(f"wrote BugSwarm disk budget report to {out}: {report['status']}")
    return 0 if report["status"] == "ready" else 2


def positive_int(value: str) -> int:
    parsed = int(value)
    if parsed <= 0:
        raise argparse.ArgumentTypeError("must be positive")
    return parsed


def positive_float(value: str) -> float:
    parsed = float(value)
    if parsed <= 0:
        raise argparse.ArgumentTypeError("must be positive")
    return parsed


def resolve_docker_context(requested: str | None, environ: Mapping[str, str]) -> tuple[str | None, str]:
    if requested is not None:
        return validate_docker_context(requested, "--docker-context"), "explicit"
    if environ.get("DOCKER_CONTEXT"):
        return validate_docker_context(environ["DOCKER_CONTEXT"], "DOCKER_CONTEXT"), "environment"
    return None, "current"


def validate_docker_context(value: str, source: str) -> str:
    if not DOCKER_CONTEXT_RE.fullmatch(value):
        raise ValueError(f"{source} must be a Docker context name containing only letters, digits, '.', '_', or '-'")
    return value


def docker_prefix(context: str | None) -> list[str]:
    return ["docker", "--context", context] if context else ["docker"]


def run(cmd: list[str]) -> subprocess.CompletedProcess[str]:
    return subprocess.run(cmd, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=30, check=False)


def inspect_image(image: str, context: str | None) -> dict[str, Any]:
    command = [*docker_prefix(context), "image", "inspect", "--format={{json .}}", image]
    try:
        proc = run(command)
    except (OSError, subprocess.TimeoutExpired) as exc:
        return {"present": False, "command": command, "error": str(exc)}
    if proc.returncode != 0:
        return {"present": False, "command": command, "stderr": proc.stderr[-1000:]}
    try:
        payload = json.loads(proc.stdout)
    except json.JSONDecodeError:
        return {"present": False, "command": command, "error": "docker image inspect returned invalid JSON"}
    layers = payload.get("RootFS", {}).get("Layers", []) if isinstance(payload, dict) else []
    return {
        "present": True,
        "command": command,
        "id": payload.get("Id"),
        "repo_digests": payload.get("RepoDigests") or [],
        "size_bytes": int(payload.get("Size") or 0),
        "layer_count": len(layers) if isinstance(layers, list) else 0,
        "layers": layers if isinstance(layers, list) else [],
    }


def probe_free_bytes(image: str, context: str | None) -> dict[str, Any]:
    command = [*docker_prefix(context), "run", "--rm", "--pull=never", "--entrypoint", "df", image, "-Pk", "/"]
    try:
        proc = run(command)
    except (OSError, subprocess.TimeoutExpired) as exc:
        return {"available_bytes": None, "command": command, "error": str(exc)}
    if proc.returncode != 0:
        return {"available_bytes": None, "command": command, "stderr": proc.stderr[-1000:]}
    lines = [line.split() for line in proc.stdout.splitlines() if line.strip()]
    if len(lines) < 2 or len(lines[-1]) < 4:
        return {"available_bytes": None, "command": command, "error": "unable to parse df -Pk output"}
    try:
        return {"available_bytes": int(lines[-1][3]) * 1024, "command": command}
    except ValueError:
        return {"available_bytes": None, "command": command, "error": "df available-block value was not numeric"}


def durable_evidence_status(path: Path | None) -> dict[str, Any]:
    if path is None:
        return {"present": False, "reason": "--durable-evidence-dir was not supplied"}
    if managed_workspace_root(path) is not None:
        return {"present": False, "reason": f"evidence directory is inside managed workspace: {path}"}
    sources = sorted(path.glob("bugswarm-source-*.yaml")) if path.is_dir() else []
    receipts = sorted(path.glob("bugswarm-verification-*.json")) if path.is_dir() else []
    if not sources or not receipts:
        return {"present": False, "reason": "durable directory needs immutable bugswarm-source-* and bugswarm-verification-* snapshots"}
    return {"present": True, "path": str(path.resolve()), "source_snapshots": len(sources), "receipt_snapshots": len(receipts)}


def managed_workspace_root(path: Path) -> Path | None:
    resolved = path.resolve()
    for candidate in (resolved, *resolved.parents):
        if (candidate / ".kitsoki-dev-workspace.json").is_file():
            return candidate
    return None


def build_report(
    *, image: str, docker_context: str | None, docker_context_source: str, available_bytes: int | None,
    space_probe_image: str | None, min_free_bytes: int, uncached_image_budget_bytes: int,
    durable_evidence_dir: Path | None, reclaim_images: list[str],
) -> dict[str, Any]:
    inspection = inspect_image(image, docker_context)
    probe: dict[str, Any] | None = None
    if available_bytes is None:
        probe_image = image if inspection["present"] else space_probe_image
        if probe_image:
            probe = probe_free_bytes(probe_image, docker_context)
            available_bytes = probe.get("available_bytes")
    additional_bytes = 0 if inspection["present"] else uncached_image_budget_bytes
    required_bytes = min_free_bytes + additional_bytes
    blockers: list[str] = []
    if available_bytes is None:
        blockers.append("Docker-host free space is unknown; supply --available-bytes or an already-cached --space-probe-image")
    elif available_bytes < required_bytes:
        blockers.append(
            f"need {required_bytes} bytes free ({min_free_bytes} reserve + {additional_bytes} image budget), observed {available_bytes}"
        )
    evidence = durable_evidence_status(durable_evidence_dir)
    reclaim_plan: list[dict[str, Any]] = []
    for reclaim_image in reclaim_images:
        candidate = inspect_image(reclaim_image, docker_context)
        row: dict[str, Any] = {"image": reclaim_image, "inspection": candidate}
        if not evidence["present"]:
            row["status"] = "blocked"
            row["reason"] = "durable corpus evidence is required before image eviction"
        elif not candidate["present"]:
            row["status"] = "blocked"
            row["reason"] = "candidate image is absent or could not be inspected"
        else:
            row["status"] = "manual-review-required"
            row["estimated_reclaimable_bytes"] = candidate["size_bytes"]
            row["command"] = shell_join([*docker_prefix(docker_context), "image", "rm", reclaim_image])
            row["reason"] = "review image ownership and shared layers; this tool never executes the command"
        reclaim_plan.append(row)
    return {
        "kind": "arena_bugswarm_disk_budget",
        "version": 1,
        "status": "blocked" if blockers else "ready",
        "docker_context": docker_context,
        "docker_context_source": docker_context_source,
        "image": image,
        "image_inspection": inspection,
        "free_space": {"available_bytes": available_bytes, "probe": probe},
        "budget": {
            "min_free_bytes": min_free_bytes,
            "additional_image_bytes": additional_bytes,
            "required_bytes": required_bytes,
            "image_cached": inspection["present"],
        },
        "blockers": blockers,
        "durable_evidence": evidence,
        "reclamation_plan": reclaim_plan,
        "safety": {
            "pulls_images": False,
            "deletes_images": False,
            "blind_prune": False,
            "next_step": "run bugswarm_verify_source.py --execute only when status is ready",
        },
    }


def shell_join(command: list[str]) -> str:
    import shlex
    return " ".join(shlex.quote(part) for part in command)


if __name__ == "__main__":
    raise SystemExit(main())
