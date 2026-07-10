#!/usr/bin/env python3
"""Generate a paired-task arena spec from a verified BugSwarm source YAML.

This script is offline and deterministic. It does not run Docker or a model; it
only turns already-verified BugSwarm tasks into a schedulable arena matrix.
"""

from __future__ import annotations

import argparse
import sys
from pathlib import Path
from typing import Any

try:
    import yaml  # type: ignore
except ModuleNotFoundError:
    sys.exit("bugswarm_to_arena_spec.py needs pyyaml")


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", required=True, help="verified YAML from bugswarm_apply_verification.py")
    parser.add_argument("--out", required=True, help="arena JobSpec YAML to write")
    parser.add_argument("--target-id", default="bugswarm-verified")
    parser.add_argument("--target-label", default="BugSwarm verified subset")
    parser.add_argument("--image", default="kitsoki-arena/paired-task:latest")
    parser.add_argument("--candidate", default="glm-5.2")
    parser.add_argument(
        "--include-unverified",
        action="store_true",
        help="include tasks even when verified_red/verified_green are not both true",
    )
    parser.add_argument(
        "--backend",
        default="synthetic",
        help="variant backend to record in the spec; synthetic keeps generated specs no-spend by default",
    )
    parser.add_argument("--kitsoki-backend", default="", help="override the Kitsoki variant backend")
    parser.add_argument("--raw-backend", default="", help="override the raw-prompt variant backend")
    args = parser.parse_args(argv)

    source_path = Path(args.source)
    source = load_source(source_path)
    tasks = verified_tasks(source, include_unverified=args.include_unverified)
    spec = build_spec(
        source_path=str(args.source),
        tasks=tasks,
        target_id=args.target_id,
        target_label=args.target_label,
        image=args.image,
        candidate=args.candidate,
        kitsoki_backend=args.kitsoki_backend or args.backend,
        raw_backend=args.raw_backend or args.backend,
    )
    out = Path(args.out)
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(yaml.safe_dump(spec, sort_keys=False), encoding="utf-8")
    print(f"wrote paired-task spec for {len(tasks)} BugSwarm task(s) to {out}")
    return 0


def load_source(path: Path) -> dict[str, Any]:
    data = yaml.safe_load(path.read_text(encoding="utf-8"))
    if not isinstance(data, dict):
        raise ValueError(f"expected mapping: {path}")
    if data.get("kind") != "arena_bugswarm_source":
        raise ValueError(f"source kind must be arena_bugswarm_source: {path}")
    return data


def verified_tasks(source: dict[str, Any], *, include_unverified: bool) -> list[dict[str, Any]]:
    tasks = [task for task in source.get("tasks", []) if isinstance(task, dict)]
    if include_unverified:
        return tasks
    return [task for task in tasks if task.get("verified_red") is True and task.get("verified_green") is True]


def build_spec(
    *,
    source_path: str,
    tasks: list[dict[str, Any]],
    target_id: str,
    target_label: str,
    image: str,
    candidate: str,
    kitsoki_backend: str,
    raw_backend: str,
) -> dict[str, Any]:
    return {
        "job_type": "paired-task",
        "targets": [
            {
                "id": target_id,
                "label": target_label,
                "image": image,
                "corpus": source_path,
            }
        ],
        "variants": [
            {
                "id": f"kitsoki-{candidate}",
                "treatment": "kitsoki",
                "candidate": candidate,
                "backend": kitsoki_backend,
                "model": candidate,
                "effort": "medium",
            },
            {
                "id": f"raw-prompt-{candidate}",
                "treatment": "single-briefed",
                "candidate": candidate,
                "backend": raw_backend,
                "model": candidate,
                "effort": "medium",
            },
        ],
        "axes": {
            "task": [str(task["id"]) for task in tasks],
        },
        "placement": {
            "hosts": ["local"],
            "concurrency": 1,
            "retry": 0,
        },
    }


if __name__ == "__main__":
    raise SystemExit(main())
