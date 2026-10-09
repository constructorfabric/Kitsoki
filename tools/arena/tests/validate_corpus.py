#!/usr/bin/env python3
"""Validate the frozen arena cost corpus manifest.

This is a no-LLM/no-network WB.1 gate. It checks structure, frozen selection
metadata, deterministic oracle proof flags, and a train/held-out split. It does
not mine transcripts or verify external repos; those happen before this manifest
is committed.
"""

from __future__ import annotations

import argparse
import sys
from collections import Counter, defaultdict
from pathlib import Path
from typing import Any

try:
    import yaml  # type: ignore
except ModuleNotFoundError:
    sys.exit("validate_corpus.py needs pyyaml")

REQUIRED_TASK_FIELDS = {
    "id",
    "repo",
    "archetype",
    "baseline_sha",
    "oracle",
    "ticket",
    "verified_red",
    "verified_green",
    "split",
}
ALLOWED_SPLITS = {"training", "heldout"}


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("manifest", help="tools/arena/corpus/cost-bench.manifest.yaml")
    parser.add_argument(
        "--allow-small",
        action="store_true",
        help="allow fewer than 24 tasks; intended only for validator unit fixtures",
    )
    args = parser.parse_args(argv)

    failures = validate(Path(args.manifest), allow_small=args.allow_small)
    if failures:
        print("FAIL: corpus manifest")
        for failure in failures:
            print(f"  - {failure}")
        return 1
    print("PASS: corpus manifest")
    return 0


def validate(path: Path, *, allow_small: bool = False) -> list[str]:
    failures: list[str] = []
    if not path.exists():
        return [f"manifest does not exist: {path}"]
    data = _load_yaml(path, failures)
    if not isinstance(data, dict):
        return failures + ["manifest root must be a mapping"]

    if data.get("kind") != "arena_cost_corpus":
        failures.append("kind must be arena_cost_corpus")
    if data.get("version") != 1:
        failures.append("version must be 1")
    if data.get("frozen") is not True:
        failures.append("frozen must be true")
    if not data.get("selection_rule_committed_at"):
        failures.append("selection_rule_committed_at is required")
    if not data.get("archetypes"):
        failures.append("archetypes path is required")
    source_catalog = data.get("source_catalog")
    if source_catalog and not _resolve_declared_path(path, str(source_catalog)).exists():
        failures.append(f"source_catalog does not exist: {source_catalog}")

    tasks = data.get("tasks")
    if not isinstance(tasks, list):
        return failures + ["tasks must be a list"]
    if not allow_small and not (24 <= len(tasks) <= 26):
        failures.append(f"tasks must contain 24-26 tasks, got {len(tasks)}")

    seen_ids: set[str] = set()
    split_counts: Counter[str] = Counter()
    archetype_splits: dict[str, set[str]] = defaultdict(set)
    repo_counts: Counter[str] = Counter()
    for idx, task in enumerate(tasks):
        label = f"tasks[{idx}]"
        if not isinstance(task, dict):
            failures.append(f"{label} must be a mapping")
            continue
        missing = sorted(REQUIRED_TASK_FIELDS - set(task))
        if missing:
            failures.append(f"{label} missing fields: {', '.join(missing)}")
        task_id = str(task.get("id") or "")
        if not task_id:
            failures.append(f"{label}.id is required")
        elif task_id in seen_ids:
            failures.append(f"duplicate task id: {task_id}")
        seen_ids.add(task_id)

        split = str(task.get("split") or "")
        if split not in ALLOWED_SPLITS:
            failures.append(f"{label}.split must be one of {sorted(ALLOWED_SPLITS)}")
        else:
            split_counts[split] += 1
            archetype = str(task.get("archetype") or "")
            if archetype:
                archetype_splits[archetype].add(split)

        if task.get("verified_red") is not True:
            failures.append(f"{label}.verified_red must be true")
        if task.get("verified_green") is not True:
            failures.append(f"{label}.verified_green must be true")
        if not isinstance(task.get("oracle"), dict):
            failures.append(f"{label}.oracle must be a mapping")
        repo = str(task.get("repo") or "")
        if repo:
            repo_counts[repo] += 1

    if split_counts["training"] == 0:
        failures.append("at least one training task is required")
    if split_counts["heldout"] == 0:
        failures.append("at least one heldout task is required")
    for archetype, splits in sorted(archetype_splits.items()):
        if "training" not in splits:
            failures.append(f"archetype {archetype!r} has no training task")
        if "heldout" not in splits:
            failures.append(f"archetype {archetype!r} has no heldout task")
    if not allow_small and len(repo_counts) < 10:
        failures.append(f"corpus must cover 10 repos, got {len(repo_counts)}")

    return failures


def _load_yaml(path: Path, failures: list[str]) -> Any:
    try:
        return yaml.safe_load(path.read_text(encoding="utf-8"))
    except Exception as exc:  # noqa: BLE001 - report parser failure as gate text.
        failures.append(f"cannot parse YAML: {exc}")
        return None


def _resolve_declared_path(manifest_path: Path, declared: str) -> Path:
    p = Path(declared)
    if p.is_absolute():
        return p
    cwd_relative = Path.cwd() / p
    if cwd_relative.exists():
        return cwd_relative
    return manifest_path.parent / p


if __name__ == "__main__":
    raise SystemExit(main())
