#!/usr/bin/env python3
"""Convert an exported BugSwarm artifact list into arena corpus tasks.

The converter is intentionally offline: it accepts JSON, JSONL, or CSV exported
from BugSwarm tooling and writes a small YAML fragment whose tasks follow the
same broad shape as tools/arena/corpus/cost-bench.manifest.yaml. It does not
query BugSwarm, pull Docker images, or run an LLM.
"""

from __future__ import annotations

import argparse
import csv
import importlib
import json
import re
import sys
from pathlib import Path
from typing import Any, Iterable


def _import_pyyaml() -> Any:
    """Import installed PyYAML rather than the local offline test shim."""
    arena_root = Path(__file__).resolve().parents[1]

    def is_arena_path(entry: str) -> bool:
        try:
            Path(entry or ".").resolve().relative_to(arena_root)
            return True
        except ValueError:
            return False

    previous_module = sys.modules.pop("yaml", None)
    previous_path = sys.path[:]
    sys.path[:] = [entry for entry in sys.path if not is_arena_path(entry)]
    try:
        module = importlib.import_module("yaml")
    except ModuleNotFoundError:  # pragma: no cover - depends on CLI environment.
        if previous_module is not None:
            sys.modules["yaml"] = previous_module
        sys.exit("bugswarm_to_arena.py needs pyyaml")
    finally:
        sys.path[:] = previous_path

    origin = Path(str(getattr(module, "__file__", ""))).resolve()
    if is_arena_path(str(origin)):
        raise RuntimeError(f"refusing local YAML shim for corpus serialization: {origin}")
    return module


yaml = _import_pyyaml()

REQUIRED = ("image_tag", "repo", "failed_job_id", "passed_job_id")


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--in", dest="input", required=True, help="BugSwarm artifact JSON/JSONL/CSV")
    parser.add_argument("--out", required=True, help="YAML output path")
    parser.add_argument("--limit", type=int, default=0, help="optional max artifacts to emit")
    parser.add_argument("--split", default="heldout", choices=("training", "heldout"))
    args = parser.parse_args(argv)

    artifacts = list(load_artifacts(Path(args.input)))
    if args.limit:
        artifacts = artifacts[: args.limit]
    tasks = [task_from_artifact(a, split=args.split) for a in artifacts]
    payload = {
        "kind": "arena_bugswarm_source",
        "version": 1,
        "source": "bugswarm",
        "generated_from": str(args.input),
        "task_count": len(tasks),
        "tasks": tasks,
    }
    out = Path(args.out)
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(yaml.safe_dump(payload, sort_keys=False), encoding="utf-8")
    print(f"wrote {len(tasks)} BugSwarm task(s) to {out}")
    return 0


def load_artifacts(path: Path) -> Iterable[dict[str, Any]]:
    text = path.read_text(encoding="utf-8")
    suffix = path.suffix.lower()
    if suffix == ".csv":
        yield from csv.DictReader(text.splitlines())
        return
    if suffix == ".jsonl":
        for line in text.splitlines():
            line = line.strip()
            if line:
                yield _normalize_record(json.loads(line))
        return
    data = json.loads(text)
    if isinstance(data, dict):
        rows = data.get("artifacts") or data.get("items") or data.get("data") or []
    else:
        rows = data
    if not isinstance(rows, list):
        raise ValueError("JSON input must be a list or contain artifacts/items/data list")
    for row in rows:
        yield _normalize_record(row)


def task_from_artifact(artifact: dict[str, Any], *, split: str) -> dict[str, Any]:
    normalized = _normalize_record(artifact)
    missing = [field for field in REQUIRED if not normalized.get(field)]
    if missing:
        label = normalized.get("image_tag") or normalized.get("repo") or "<unknown>"
        raise ValueError(f"BugSwarm artifact {label} missing required field(s): {', '.join(missing)}")

    image_tag = str(normalized["image_tag"])
    repo = str(normalized["repo"])
    failed_job = str(normalized["failed_job_id"])
    passed_job = str(normalized["passed_job_id"])
    task_id = _task_id(image_tag)
    language = str(normalized.get("language") or "")
    build_system = str(normalized.get("build_system") or "")
    classification = str(normalized.get("classification") or "")
    reproducibility = str(normalized.get("reproducibility") or "reproducible")

    task: dict[str, Any] = {
        "id": task_id,
        "repo": _repo_id(repo),
        "repo_label": repo,
        "archetype": "bugfix_test_repair",
        "source": "bugswarm",
        "image_tag": image_tag,
        "failed_job_id": failed_job,
        "passed_job_id": passed_job,
        "ticket": f"Repair BugSwarm artifact {image_tag}: CI job {failed_job} fails and job {passed_job} passes.",
        "split": split,
        "verified_red": False,
        "verified_green": False,
        "oracle": {
            "kind": "bugswarm_fail_pass_pair",
            "image_tag": image_tag,
            "failed_job_id": failed_job,
            "passed_job_id": passed_job,
            "red_rule": "failed job script exits non-zero inside the BugSwarm artifact",
            "green_rule": "passed job script exits zero inside the same BugSwarm artifact",
        },
    }
    meta = {
        "language": language,
        "build_system": build_system,
        "classification": classification,
        "reproducibility": reproducibility,
        "source_url": str(normalized.get("source_url") or ""),
        "selection_note": str(normalized.get("selection_note") or ""),
    }
    task["meta"] = {k: v for k, v in meta.items() if v}
    return task


def _normalize_record(row: Any) -> dict[str, Any]:
    if not isinstance(row, dict):
        raise ValueError(f"artifact row must be a mapping, got {row!r}")
    aliases = {
        "image-tag": "image_tag",
        "imageTag": "image_tag",
        "repo_slug": "repo",
        "repo-slug": "repo",
        "repository": "repo",
        "failed_job": "failed_job_id",
        "failed-job-id": "failed_job_id",
        "failedJobId": "failed_job_id",
        "passed_job": "passed_job_id",
        "passed-job-id": "passed_job_id",
        "passedJobId": "passed_job_id",
        "build-system": "build_system",
    }
    out: dict[str, Any] = {}
    for key, value in row.items():
        normalized_key = aliases.get(str(key), str(key))
        out[normalized_key] = value
    return out


def _task_id(image_tag: str) -> str:
    slug = re.sub(r"[^a-zA-Z0-9._-]+", "-", image_tag.strip()).strip("-").lower()
    return f"bugswarm-{slug}"


def _repo_id(repo: str) -> str:
    leaf = repo.rstrip("/").split("/")[-1] if repo else "unknown"
    return re.sub(r"[^a-zA-Z0-9._-]+", "-", leaf).strip("-").lower() or "unknown"


if __name__ == "__main__":
    raise SystemExit(main())
