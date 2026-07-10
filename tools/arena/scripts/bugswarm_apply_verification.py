#!/usr/bin/env python3
"""Apply a BugSwarm verification report to a converted arena source YAML.

`bugswarm_verify_source.py --execute` proves RED/GREEN behavior, but the source
YAML should not be hand-edited afterward. This script writes a new source file
with `verified_red` / `verified_green` set from the verification report and
records the evidence in each task's `meta.bugswarm_verification`.
"""

from __future__ import annotations

import argparse
import sys
from pathlib import Path
from typing import Any

try:
    import yaml  # type: ignore
except ModuleNotFoundError:
    sys.exit("bugswarm_apply_verification.py needs pyyaml")


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", required=True, help="YAML from bugswarm_to_arena.py")
    parser.add_argument("--verification", required=True, help="JSON from bugswarm_verify_source.py")
    parser.add_argument("--out", required=True, help="updated source YAML")
    parser.add_argument(
        "--allow-dry-run",
        action="store_true",
        help="carry dry-run evidence into meta without setting verified flags",
    )
    args = parser.parse_args(argv)

    source_path = Path(args.source)
    verification_path = Path(args.verification)
    source = load_yaml(source_path)
    verification = load_yaml_or_json(verification_path)
    updated = apply_verification(source, verification, verification_path=str(args.verification), allow_dry_run=args.allow_dry_run)
    out = Path(args.out)
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(yaml.safe_dump(updated, sort_keys=False), encoding="utf-8")
    verified_count = sum(1 for task in updated.get("tasks", []) if task.get("verified_red") and task.get("verified_green"))
    print(f"wrote {out} ({verified_count} verified task(s))")
    return 0


def load_yaml(path: Path) -> dict[str, Any]:
    data = yaml.safe_load(path.read_text(encoding="utf-8"))
    if not isinstance(data, dict):
        raise ValueError(f"expected mapping: {path}")
    if data.get("kind") != "arena_bugswarm_source":
        raise ValueError(f"source kind must be arena_bugswarm_source: {path}")
    return data


def load_yaml_or_json(path: Path) -> dict[str, Any]:
    data = yaml.safe_load(path.read_text(encoding="utf-8"))
    if not isinstance(data, dict):
        raise ValueError(f"expected mapping: {path}")
    if data.get("kind") != "arena_bugswarm_verification":
        raise ValueError(f"verification kind must be arena_bugswarm_verification: {path}")
    return data


def apply_verification(
    source: dict[str, Any],
    verification: dict[str, Any],
    *,
    verification_path: str,
    allow_dry_run: bool,
) -> dict[str, Any]:
    mode = str(verification.get("mode") or "")
    if mode != "execute" and not allow_dry_run:
        raise ValueError("verification mode must be execute; pass --allow-dry-run to carry dry-run metadata only")
    results = {
        str(result.get("task_id") or ""): result
        for result in verification.get("results", [])
        if isinstance(result, dict)
    }
    out = dict(source)
    out["verification"] = {
        "path": verification_path,
        "mode": mode,
        "task_count": int(verification.get("task_count") or 0),
        "verified_count": int(verification.get("verified_count") or 0),
    }
    tasks = []
    for task in source.get("tasks", []):
        if not isinstance(task, dict):
            continue
        updated = dict(task)
        result = results.get(str(task.get("id") or ""))
        if result:
            red = bool(result.get("verified_red"))
            green = bool(result.get("verified_green"))
            if mode == "execute":
                updated["verified_red"] = red
                updated["verified_green"] = green
            meta = dict(updated.get("meta") or {})
            meta["bugswarm_verification"] = {
                "mode": mode,
                "verified_red": red,
                "verified_green": green,
                "failed_exit_code": result.get("failed_exit_code"),
                "passed_exit_code": result.get("passed_exit_code"),
                "report": verification_path,
            }
            updated["meta"] = meta
        tasks.append(updated)
    out["tasks"] = tasks
    out["task_count"] = len(tasks)
    return out


if __name__ == "__main__":
    raise SystemExit(main())
