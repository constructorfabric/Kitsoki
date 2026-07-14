#!/usr/bin/env python3
"""Freeze a verified BugSwarm source into a deterministic task-optimization lock.

This command never runs Docker or an LLM.  It consumes the output of
``bugswarm_apply_verification.py`` and produces a content-addressed receipt.  A
receipt is deliberately written even when the corpus is not ready, so callers
can persist and review the exact blocker rather than treating an undersized
pool as a ready campaign.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import sys
from pathlib import Path
from typing import Any

import yaml  # type: ignore


SCHEMA = "arena_bugswarm_corpus_lock/v1"


def canonical(value: Any) -> bytes:
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=True).encode("utf-8")


def digest(value: Any) -> str:
    return hashlib.sha256(canonical(value)).hexdigest()


def file_digest(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def load_source(path: Path) -> dict[str, Any]:
    value = yaml.safe_load(path.read_text(encoding="utf-8"))
    if not isinstance(value, dict) or value.get("kind") != "arena_bugswarm_source":
        raise ValueError("source kind must be arena_bugswarm_source")
    return value


def verified_task(task: dict[str, Any]) -> bool:
    return bool(task.get("verified_red")) and bool(task.get("verified_green")) and str(task.get("meta", {}).get("bugswarm_verification", {}).get("mode") or "") == "execute"


def task_record(task: dict[str, Any]) -> tuple[dict[str, Any] | None, str | None]:
    meta = task.get("meta") if isinstance(task.get("meta"), dict) else {}
    verification = meta.get("bugswarm_verification") if isinstance(meta.get("bugswarm_verification"), dict) else {}
    oracle = task.get("oracle") if isinstance(task.get("oracle"), dict) else {}
    required = {
        "id": task.get("id"), "repository": task.get("repo_label") or task.get("repo"),
        "language": meta.get("language"), "source": task.get("source"),
        "image_tag": task.get("image_tag"), "image_digest": verification.get("image_digest"),
        "failed_job_id": task.get("failed_job_id"), "passed_job_id": task.get("passed_job_id"),
        "failed_commit_sha": meta.get("failed_commit_sha"), "passed_commit_sha": meta.get("passed_commit_sha"),
        "ticket": task.get("ticket"), "selection_rationale": meta.get("selection_note"),
        "verification_receipt": verification.get("report"), "verification_receipt_sha256": verification.get("report_sha256"),
        "verification_source_snapshot": verification.get("source_snapshot"), "verification_source_snapshot_sha256": verification.get("source_snapshot_sha256"),
        "oracle": oracle,
    }
    missing = [key for key, value in required.items() if not value]
    if missing:
        return None, f"{task.get('id') or '<unknown>'}: missing required provenance: {', '.join(missing)}"
    return {
        "id": str(required["id"]), "source": str(required["source"]), "repository": str(required["repository"]),
        "language": str(required["language"]), "image_tag": str(required["image_tag"]),
        "image_digest": str(required["image_digest"]), "failed_job_id": str(required["failed_job_id"]),
        "passed_job_id": str(required["passed_job_id"]), "commits": {"failed": str(required["failed_commit_sha"]), "passed": str(required["passed_commit_sha"])},
        "verification": {
            "receipt": str(required["verification_receipt"]), "receipt_sha256": str(required["verification_receipt_sha256"]),
            "source_snapshot": str(required["verification_source_snapshot"]),
            "source_snapshot_sha256": str(required["verification_source_snapshot_sha256"]),
            "red": True, "green": True,
        },
        "public_task": {"text": str(required["ticket"]), "sha256": digest(str(required["ticket"]))},
        "hidden_oracle": {"reference": str(oracle.get("reference") or oracle.get("kind") or ""), "sha256": digest(oracle)},
        "selection_rationale": str(required["selection_rationale"]),
    }, None


def choose(records: list[dict[str, Any]], learning_count: int, confirmation_count: int) -> tuple[list[dict[str, Any]], list[str]]:
    # Stable digest ordering prevents filesystem/YAML order from selecting the
    # live corpus.  A repository is consumed by the first split it enters.
    ordered = sorted(records, key=lambda r: (digest({"id": r["id"], "repository": r["repository"]}), r["id"]))
    selected: list[dict[str, Any]] = []
    used_repos: set[str] = set()
    blockers: list[str] = []
    for split, need in (("learning", learning_count), ("confirmation", confirmation_count)):
        available = [r for r in ordered if r["repository"] not in used_repos]
        picked = available[:need]
        if len(picked) != need:
            blockers.append(f"need {need} repository-distinct verified tasks for {split}; found {len(picked)}")
            continue
        for record in picked:
            record = dict(record)
            record["split"] = split
            selected.append(record)
            used_repos.add(record["repository"])
    return sorted(selected, key=lambda r: r["id"]), blockers


def build_lock(source_path: Path, *, learning_count: int, confirmation_count: int) -> dict[str, Any]:
    source = load_source(source_path)
    source_hash = file_digest(source_path)
    eligible: list[dict[str, Any]] = []
    blockers: list[str] = []
    for task in source.get("tasks", []):
        if not isinstance(task, dict) or not verified_task(task):
            continue
        record, error = task_record(task)
        if error:
            blockers.append(error)
        elif record:
            eligible.append(record)
    selected, split_blockers = choose(eligible, learning_count, confirmation_count)
    blockers.extend(split_blockers)
    status = "ready" if not blockers and len(selected) == learning_count + confirmation_count else "blocked"
    payload: dict[str, Any] = {
        "schema": SCHEMA, "status": status,
        "source": str(source_path), "source_sha256": source_hash,
        "selection": {"algorithm": "sha256(id,repository)-ordered/v1", "learning_count": learning_count, "confirmation_count": confirmation_count, "repository_separated": True},
        "eligible_verified_count": len(eligible), "tasks": selected if status == "ready" else [],
        "reserve_task_ids": sorted(r["id"] for r in eligible if r["id"] not in {s["id"] for s in selected}),
        "blockers": sorted(blockers),
    }
    payload["lock_sha256"] = digest(payload)
    return payload


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", required=True)
    parser.add_argument("--out", required=True)
    parser.add_argument("--learning-count", type=int, default=4)
    parser.add_argument("--confirmation-count", type=int, default=8)
    args = parser.parse_args(argv)
    if args.learning_count < 1 or args.confirmation_count < 1:
        parser.error("split counts must be positive")
    payload = build_lock(Path(args.source), learning_count=args.learning_count, confirmation_count=args.confirmation_count)
    out = Path(args.out)
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(json.dumps(payload, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    print(f"{payload['status'].upper()}: wrote {out} ({len(payload['tasks'])} selected, {len(payload['blockers'])} blocker(s))")
    return 0 if payload["status"] == "ready" else 2


if __name__ == "__main__":
    raise SystemExit(main())
