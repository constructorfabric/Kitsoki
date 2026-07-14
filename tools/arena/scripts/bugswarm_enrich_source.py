#!/usr/bin/env python3
"""Enrich a BugSwarm arena source with commit provenance, fail closed.

Corpus locks require the failed and passed source commits, but the compact
candidate seed deliberately contains only the public artifact identity.  This
operator command copies commit identifiers from an exported BugSwarm artifact
record into a *new* source file.  It never infers commits from a GitHub URL,
image tag, or task id and it never marks a task verified.

Normal use is intentionally offline: first export the exact records with the
official ``bugswarm-common`` ``DatabaseAPI``, then pass that JSON snapshot with
``--metadata-json``.  ``--fetch-database-api`` is the only network path.  It
requires ``--metadata-out`` so the raw response is retained and hashed before
any value is copied into the source.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import re
import sys
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Iterable

import yaml  # type: ignore


SHA = re.compile(r"^[0-9a-fA-F]{7,64}$")
SCHEMA = "arena_bugswarm_provenance_enrichment/v1"


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", required=True, help="arena_bugswarm_source YAML to enrich")
    parser.add_argument("--out", required=True, help="new enriched source YAML (must not equal --source)")
    parser.add_argument("--metadata-json", action="append", default=[], help="saved BugSwarm DatabaseAPI artifact JSON; repeatable")
    parser.add_argument("--task-id", action="append", default=[], help="only enrich this task id; repeatable")
    parser.add_argument("--fetch-database-api", action="store_true", help="explicitly query bugswarm-common DatabaseAPI (network)")
    parser.add_argument("--metadata-out", help="required with --fetch-database-api; immutable raw metadata receipt")
    args = parser.parse_args(argv)
    if Path(args.source).resolve() == Path(args.out).resolve():
        parser.error("--out must be a new path; source files are never mutated")
    if args.fetch_database_api and args.metadata_json:
        parser.error("choose --metadata-json or --fetch-database-api, not both")
    if args.fetch_database_api and not args.metadata_out:
        parser.error("--fetch-database-api requires --metadata-out to retain raw metadata")
    if not args.fetch_database_api and not args.metadata_json:
        parser.error("provide --metadata-json or explicitly opt into --fetch-database-api")

    source_path = Path(args.source)
    source = load_source(source_path)
    selected = select_tasks(source, args.task_id)
    if args.fetch_database_api:
        exported = fetch_database_api(selected)
        metadata_paths = [write_metadata(Path(args.metadata_out), exported)]
    else:
        metadata_paths = [Path(value) for value in args.metadata_json]
    artifacts = metadata_artifacts(metadata_paths)
    enriched, unresolved = enrich(source, selected, artifacts, metadata_paths)
    if unresolved:
        print("REFUSED: no source was written; provenance is incomplete or ambiguous:", file=sys.stderr)
        for issue in unresolved:
            print(f"  - {issue}", file=sys.stderr)
        return 2
    out = Path(args.out)
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(yaml.safe_dump(enriched, sort_keys=False), encoding="utf-8")
    print(f"wrote provenance-enriched BugSwarm source to {out} ({len(selected)} task(s))")
    return 0


def load_source(path: Path) -> dict[str, Any]:
    value = yaml.safe_load(path.read_text(encoding="utf-8"))
    if not isinstance(value, dict) or value.get("kind") != "arena_bugswarm_source":
        raise ValueError("source kind must be arena_bugswarm_source")
    tasks = value.get("tasks")
    if not isinstance(tasks, list):
        raise ValueError("source tasks must be a list")
    return value


def select_tasks(source: dict[str, Any], requested: list[str]) -> list[dict[str, Any]]:
    tasks = [task for task in source["tasks"] if isinstance(task, dict)]
    by_id = {str(task.get("id") or ""): task for task in tasks}
    if len(by_id) != len(tasks) or "" in by_id:
        raise ValueError("source tasks require unique non-empty ids")
    if not requested:
        return tasks
    if len(requested) != len(set(requested)):
        raise ValueError("--task-id must not repeat an id")
    unknown = sorted(set(requested) - set(by_id))
    if unknown:
        raise ValueError("unknown --task-id: " + ", ".join(unknown))
    return [task for task in tasks if str(task["id"]) in set(requested)]


def fetch_database_api(tasks: list[dict[str, Any]]) -> dict[str, Any]:
    """Use BugSwarm's documented client API, not an invented HTTP endpoint."""
    try:
        # This is the documented bugswarm-common public module path.  Do not
        # import a re-export from ``bugswarm.common``: released client versions
        # do not promise one there.
        from bugswarm.common.rest_api.database_api import DatabaseAPI  # type: ignore
    except ModuleNotFoundError as exc:
        raise RuntimeError(
            "--fetch-database-api needs the official bugswarm-common client; install it in the operator environment "
            "or export records there and use --metadata-json"
        ) from exc
    api = DatabaseAPI()
    rows: list[Any] = []
    for task in tasks:
        tag = str(task.get("image_tag") or "")
        # DatabaseAPI.filter_artifacts accepts a MongoDB query *string*, not
        # a Python mapping. Compact, sorted JSON makes the exact query durable
        # in client logs and prevents representation-dependent behavior.
        query = json.dumps({"image_tag": tag}, sort_keys=True, separators=(",", ":"))
        found = api.filter_artifacts(query)
        if not isinstance(found, list) or len(found) != 1:
            raise RuntimeError(f"DatabaseAPI returned {len(found) if isinstance(found, list) else 'non-list'} records for {tag!r}; refusing ambiguity")
        rows.append(found[0])
    return {
        "kind": "bugswarm_database_api_export/v1",
        "provider": "bugswarm-common DatabaseAPI",
        "fetched_at": datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z"),
        "artifacts": rows,
    }


def write_metadata(path: Path, payload: dict[str, Any]) -> Path:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(payload, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    return path


def metadata_artifacts(paths: Iterable[Path]) -> dict[str, tuple[dict[str, Any], Path, str]]:
    by_tag: dict[str, tuple[dict[str, Any], Path, str]] = {}
    for path in paths:
        raw = path.read_bytes()
        data = json.loads(raw)
        rows = data.get("artifacts") if isinstance(data, dict) else data
        if not isinstance(rows, list):
            raise ValueError(f"metadata {path} must be an artifact list or contain an artifacts list")
        for row in rows:
            if not isinstance(row, dict):
                raise ValueError(f"metadata {path} contains a non-mapping artifact")
            tag = artifact_tag(row)
            if not tag:
                raise ValueError(f"metadata {path} contains an artifact without image_tag")
            value = (row, path, hashlib.sha256(raw).hexdigest())
            previous = by_tag.get(tag)
            if previous and canonical_json(previous[0]) != canonical_json(row):
                raise ValueError(f"conflicting metadata records for image_tag {tag!r}")
            by_tag[tag] = value
    return by_tag


def enrich(source: dict[str, Any], selected: list[dict[str, Any]], artifacts: dict[str, tuple[dict[str, Any], Path, str]], metadata_paths: list[Path]) -> tuple[dict[str, Any], list[str]]:
    # JSON round-trip makes a deep copy without mutating caller data.
    output = json.loads(json.dumps(source))
    selected_ids = {str(task["id"]) for task in selected}
    problems: list[str] = []
    for task in output["tasks"]:
        if str(task.get("id")) not in selected_ids:
            continue
        tag = str(task.get("image_tag") or "")
        entry = artifacts.get(tag)
        if not entry:
            problems.append(f"{task.get('id')}: no metadata record for image_tag {tag!r}")
            continue
        artifact, metadata_path, metadata_hash = entry
        failed, passed = commits(artifact)
        if not valid_sha(failed) or not valid_sha(passed):
            problems.append(f"{task.get('id')}: metadata record {tag!r} lacks valid failed/passed commit SHA values")
            continue
        meta = task.setdefault("meta", {})
        if not isinstance(meta, dict):
            problems.append(f"{task.get('id')}: task meta must be a mapping")
            continue
        # Existing conflicting values are a provenance conflict, never an
        # opportunity to overwrite history with a convenient new value.
        for field, value in (("failed_commit_sha", failed), ("passed_commit_sha", passed)):
            existing = str(meta.get(field) or "")
            if existing and existing.lower() != value.lower():
                problems.append(f"{task.get('id')}: existing {field} conflicts with metadata")
        if problems:
            continue
        meta["failed_commit_sha"] = failed.lower()
        meta["passed_commit_sha"] = passed.lower()
        meta["bugswarm_provenance"] = {
            "schema": SCHEMA,
            "metadata_path": str(metadata_path),
            "metadata_sha256": metadata_hash,
            "artifact_image_tag": tag,
            "artifact_sha256": hashlib.sha256(canonical_json(artifact)).hexdigest(),
            "commit_fields": {"failed": "failed", "passed": "passed"},
        }
    if problems:
        return source, problems
    output["provenance_enrichment"] = {
        "schema": SCHEMA,
        "metadata": [{"path": str(path), "sha256": hashlib.sha256(path.read_bytes()).hexdigest()} for path in metadata_paths],
        "enriched_task_ids": sorted(selected_ids),
    }
    return output, []


def artifact_tag(artifact: dict[str, Any]) -> str:
    for key in ("image_tag", "imageTag", "artifact_id", "artifact"):
        value = artifact.get(key)
        if isinstance(value, str) and value.strip():
            return value.strip()
    return ""


def commits(artifact: dict[str, Any]) -> tuple[str, str]:
    """Accept the DatabaseAPI's common compact and nested artifact layouts."""
    failed = first_string(artifact, (("failed_commit_sha",), ("failed_commit",), ("failed", "commit"), ("failed", "commit_sha"), ("failed_job", "commit"), ("failed_job", "commit_sha"), ("failed_build", "commit"), ("failed_build", "commit_sha")))
    passed = first_string(artifact, (("passed_commit_sha",), ("passed_commit",), ("passed", "commit"), ("passed", "commit_sha"), ("passed_job", "commit"), ("passed_job", "commit_sha"), ("passed_build", "commit"), ("passed_build", "commit_sha")))
    return failed, passed


def first_string(value: dict[str, Any], paths: Iterable[tuple[str, ...]]) -> str:
    for path in paths:
        current: Any = value
        for key in path:
            if not isinstance(current, dict):
                break
            current = current.get(key)
        if isinstance(current, str) and current.strip():
            return current.strip()
    return ""


def valid_sha(value: str) -> bool:
    return bool(SHA.fullmatch(value))


def canonical_json(value: Any) -> bytes:
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=True).encode("utf-8")


if __name__ == "__main__":
    raise SystemExit(main())
